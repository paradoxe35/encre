use std::collections::VecDeque;

use super::audio::SAMPLE_RATE;
use super::speech::Speech;

/// earshot wants exactly 256 samples (16 ms) at 16 kHz.
const VAD_FRAME: usize = 256;
const VAD_THRESHOLD: f32 = 0.5;

/// Keeps a trailing word from being clipped.
const HANGOVER_FRAMES: usize = 28; // ~450 ms
/// Recovers the attack the detector needed to fire.
const PREFILL_FRAMES: usize = 28;
/// Rejects clicks.
const ONSET_FRAMES: usize = 4;

const RESAMPLER_CHUNK: usize = 1024;

/// Where the leveler brings speech peaks for the detector: about -6 dBFS.
const TARGET_PEAK: f32 = 0.5;
/// About +40 dB at most.
const MAX_GAIN: f32 = 100.0;
/// The background, once lifted, stays under about -66 dBFS RMS: higher, distant voices and TV start to count as speech.
const FLOOR_CEILING: f32 = 0.0005;
/// Per 16 ms frame: the speech peak eases down about 4 dB a second, the noise floor creeps up about 2 dB a second.
const PEAK_RELEASE: f32 = 0.993;
const FLOOR_RISE: f32 = 1.004;
/// About -100 dBFS RMS: under any microphone's own noise, so digital silence rather than a background.
const SILENCE: f32 = 1e-5;

/// Lifts a quiet microphone for the detector, which drops speech below about -40 dBFS.
/// Only what the detector hears is lifted; the audio kept for recognition stays as captured.
#[derive(Debug)]
struct Leveler {
    peak: f32,
    floor: f32,
    heard: Vec<f32>,
}

impl Default for Leveler {
    fn default() -> Self {
        Self {
            peak: 0.0,
            floor: 1.0,
            heard: Vec::with_capacity(VAD_FRAME),
        }
    }
}

impl Leveler {
    fn gain(&mut self, frame: &[f32]) -> f32 {
        let peak = frame.iter().fold(0f32, |most, s| most.max(s.abs()));
        let rms = (frame.iter().map(|s| s * s).sum::<f32>() / frame.len() as f32).sqrt();
        self.peak = peak.max(self.peak * PEAK_RELEASE);
        // After silence the background is unknown: one too loud to lift is taken as it is, a quieter one creeps up.
        self.floor = if rms < SILENCE {
            0.0
        } else if rms < self.floor || (self.floor < SILENCE && rms >= FLOOR_CEILING) {
            rms
        } else {
            self.floor.max(SILENCE) * FLOOR_RISE
        };

        let to_target = TARGET_PEAK / self.peak.max(f32::MIN_POSITIVE);
        let under_ceiling = FLOOR_CEILING / self.floor.max(f32::MIN_POSITIVE);
        to_target.min(under_ceiling).clamp(1.0, MAX_GAIN)
    }

    fn lift(&mut self, frame: &[f32]) -> &[f32] {
        let gain = self.gain(frame);
        self.heard.clear();
        self.heard
            .extend(frame.iter().map(|s| (s * gain).clamp(-1.0, 1.0)));
        &self.heard
    }
}

pub struct Pipeline {
    resampler: Option<rubato::FftFixedIn<f32>>,
    pending: Vec<f32>,
    frame: Vec<f32>,
    detector: earshot::Detector,
    leveler: Option<Leveler>,
    speech: Speech,
    prefill: VecDeque<Vec<f32>>,
    onset: usize,
    hangover: usize,
}

impl Pipeline {
    pub fn new(input_rate: u32) -> Self {
        let resampler = if input_rate == SAMPLE_RATE {
            None
        } else {
            rubato::FftFixedIn::<f32>::new(
                input_rate as usize,
                SAMPLE_RATE as usize,
                RESAMPLER_CHUNK,
                1,
                1,
            )
            .ok()
        };
        Self {
            resampler,
            pending: Vec::new(),
            frame: Vec::with_capacity(VAD_FRAME),
            detector: earshot::Detector::default(),
            leveler: Some(Leveler::default()),
            speech: Speech::default(),
            prefill: VecDeque::with_capacity(PREFILL_FRAMES),
            onset: 0,
            hangover: 0,
        }
    }

    pub fn feed(&mut self, samples: &[f32]) {
        if samples.is_empty() {
            return;
        }

        let resampled = self.resample(samples);
        for sample in resampled {
            self.frame.push(sample);
            if self.frame.len() == VAD_FRAME {
                let frame = std::mem::replace(&mut self.frame, Vec::with_capacity(VAD_FRAME));
                self.classify(frame);
            }
        }
    }

    fn resample(&mut self, samples: &[f32]) -> Vec<f32> {
        let Some(resampler) = self.resampler.as_mut() else {
            return samples.to_vec();
        };

        use rubato::Resampler;
        self.pending.extend_from_slice(samples);

        let mut out = Vec::new();
        while self.pending.len() >= RESAMPLER_CHUNK {
            let chunk: Vec<f32> = self.pending.drain(..RESAMPLER_CHUNK).collect();
            if let Ok(mut done) = resampler.process(&[chunk], None) {
                out.append(&mut done[0]);
            }
        }
        out
    }

    fn classify(&mut self, frame: Vec<f32>) {
        let heard = match self.leveler.as_mut() {
            Some(leveler) => leveler.lift(&frame),
            None => &frame,
        };
        let speaking = self.detector.predict_f32(heard) >= VAD_THRESHOLD;

        if speaking {
            self.onset += 1;
        } else {
            self.onset = 0;
        }

        if self.onset >= ONSET_FRAMES {
            self.speech.samples.extend(self.prefill.drain(..).flatten());
            self.hangover = HANGOVER_FRAMES;
        }

        if self.hangover > 0 {
            self.hangover -= 1;
            self.speech.samples.extend_from_slice(&frame);
            if self.hangover == 0 {
                self.speech.pauses.push(self.speech.samples.len());
            }
            return;
        }

        if self.prefill.len() == PREFILL_FRAMES {
            self.prefill.pop_front();
        }
        self.prefill.push_back(frame);
    }

    #[cfg(test)]
    pub fn unleveled(mut self) -> Self {
        self.leveler = None;
        self
    }

    /// Pause offsets are relative to this burst.
    pub fn take(&mut self) -> Speech {
        self.speech.take()
    }

    pub fn finish(&mut self) -> Speech {
        // Zero-padded to a whole chunk so the resampler flushes what it holds.
        if !self.pending.is_empty() {
            let mut tail = std::mem::take(&mut self.pending);
            tail.resize(RESAMPLER_CHUNK, 0.0);
            self.feed(&tail);
        }

        if self.hangover > 0 && !self.frame.is_empty() {
            self.speech.samples.extend_from_slice(&self.frame);
        }
        self.frame.clear();

        self.speech.take()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const HANGOVER: usize = HANGOVER_FRAMES * VAD_FRAME;
    const PREFILL: usize = PREFILL_FRAMES * VAD_FRAME;
    /// The detector trails the audio by a frame or two either way.
    const SLACK: usize = 4 * VAD_FRAME;

    fn within(value: usize, low: usize, high: usize, what: &str) {
        assert!(
            (low..=high).contains(&value),
            "{what}: {value} is outside {low}..={high}"
        );
    }

    fn tone(seconds: f32, rate: u32) -> Vec<f32> {
        let n = (seconds * rate as f32) as usize;
        (0..n)
            .map(|i| (i as f32 * 200.0 * std::f32::consts::TAU / rate as f32).sin() * 0.5)
            .collect()
    }

    fn silence(seconds: f32) -> Vec<f32> {
        vec![0.0; (seconds * SAMPLE_RATE as f32) as usize]
    }

    fn samples(seconds: f32) -> usize {
        (seconds * SAMPLE_RATE as f32) as usize
    }

    #[test]
    fn silence_yields_nothing() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&silence(2.0));
        assert!(pipeline.take().is_empty());
        assert!(pipeline.finish().is_empty());
    }

    #[test]
    fn feeding_nothing_changes_nothing() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&[]);
        assert_eq!(pipeline.finish(), Speech::default());
    }

    #[test]
    fn a_tone_is_kept_in_full() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&tone(1.0, SAMPLE_RATE));
        let kept = pipeline.finish();
        assert_eq!(kept.samples.len(), samples(1.0));
        assert!(kept.pauses.is_empty(), "no pause inside continuous speech");
    }

    #[test]
    fn a_pause_marks_where_the_next_phrase_starts() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&tone(1.0, SAMPLE_RATE));
        pipeline.feed(&silence(1.5));
        pipeline.feed(&tone(1.0, SAMPLE_RATE));
        let kept = pipeline.finish();

        let [pause] = kept.pauses[..] else {
            panic!("pauses {:?}, want exactly one", kept.pauses);
        };
        let first_phrase = samples(1.0) + HANGOVER;
        within(pause, first_phrase, first_phrase + SLACK, "pause");

        let second_phrase = kept.samples.len() - pause;
        within(
            second_phrase,
            samples(1.0),
            samples(1.0) + PREFILL + SLACK,
            "second phrase with its prefill",
        );
    }

    #[test]
    fn pause_offsets_are_relative_to_the_burst() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&tone(1.0, SAMPLE_RATE));
        let first = pipeline.take();
        assert!(first.pauses.is_empty());

        pipeline.feed(&silence(1.5));
        pipeline.feed(&tone(1.0, SAMPLE_RATE));
        let second = pipeline.take();
        let [pause] = second.pauses[..] else {
            panic!("pauses {:?}, want exactly one", second.pauses);
        };
        within(
            pause,
            HANGOVER,
            HANGOVER + SLACK,
            "hangover ending inside this burst",
        );
    }

    #[test]
    fn silence_after_the_last_phrase_leaves_a_pause_at_the_end() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&tone(1.0, SAMPLE_RATE));
        pipeline.feed(&silence(1.0));
        let kept = pipeline.finish();
        assert_eq!(kept.pauses, vec![kept.samples.len()]);
    }

    #[test]
    fn finish_keeps_a_partial_frame_still_inside_speech() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        let odd = samples(1.0) + VAD_FRAME / 2;
        pipeline.feed(&tone(1.0, SAMPLE_RATE)[..0]);
        pipeline.feed(&tone(2.0, SAMPLE_RATE)[..odd]);
        assert_eq!(pipeline.finish().samples.len(), odd);
    }

    #[test]
    fn a_partial_frame_outside_speech_is_dropped_at_finish() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&silence(1.0)[..VAD_FRAME / 2]);
        assert!(pipeline.finish().is_empty());
    }

    #[test]
    fn other_rates_are_resampled_to_sixteen_kilohertz() {
        let mut pipeline = Pipeline::new(48_000);
        pipeline.feed(&tone(2.0, 48_000));
        let kept = pipeline.finish();
        let expected = samples(2.0);
        assert!(
            kept.samples.len().abs_diff(expected) <= RESAMPLER_CHUNK,
            "kept {} of an expected {expected}",
            kept.samples.len()
        );
    }

    fn quiet(signal: Vec<f32>, level: f32) -> Vec<f32> {
        signal.into_iter().map(|s| s * level).collect()
    }

    /// Deterministic, so a failure reproduces.
    fn hiss(seconds: f32, rms: f32) -> Vec<f32> {
        let mut x: u32 = 0x9e37_79b9;
        (0..samples(seconds))
            .map(|_| {
                x ^= x << 13;
                x ^= x >> 17;
                x ^= x << 5;
                (x as f32 / u32::MAX as f32 * 2.0 - 1.0) * rms * 3f32.sqrt()
            })
            .collect()
    }

    #[test]
    fn a_quiet_microphone_is_heard_and_kept_as_captured() {
        // A tone reaches the detector more easily than a voice, so it takes a very quiet one to be dropped.
        let level = 0.0003;
        let speech = quiet(tone(1.0, SAMPLE_RATE), level);
        let mut audio = silence(0.5);
        audio.extend(&speech);

        let mut unleveled = Pipeline::new(SAMPLE_RATE).unleveled();
        unleveled.feed(&audio);
        assert!(
            unleveled.finish().is_empty(),
            "the detector heard a -76 dBFS tone without the leveler"
        );

        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&audio);
        let kept = pipeline.finish();
        assert!(
            kept.samples.len() >= samples(1.0) - SLACK,
            "kept {} of a quiet second",
            kept.samples.len()
        );
        let loudest = kept.samples.iter().fold(0f32, |most, s| most.max(s.abs()));
        assert!(
            loudest <= 0.5 * level * 1.01,
            "the kept audio was lifted to {loudest}"
        );
    }

    #[test]
    fn a_steady_background_is_never_lifted() {
        let mut leveler = Leveler::default();
        let background = hiss(2.0, 0.003);
        let gains: Vec<f32> = background
            .chunks(VAD_FRAME)
            .map(|frame| leveler.gain(frame))
            .collect();
        assert!(
            gains.iter().all(|&g| g == 1.0),
            "a -50 dBFS background was lifted: {gains:?}"
        );
    }

    #[test]
    fn a_silent_frame_does_not_let_a_steady_background_be_lifted() {
        let mut leveler = Leveler::default();
        leveler.gain(&silence(0.1)[..VAD_FRAME]);
        let background = hiss(2.0, 0.003);
        let gains: Vec<f32> = background
            .chunks(VAD_FRAME)
            .map(|frame| leveler.gain(frame))
            .collect();
        assert!(
            gains.iter().all(|&g| g == 1.0),
            "a -50 dBFS background after a silent frame was lifted: {gains:?}"
        );
    }

    #[test]
    fn loud_input_passes_through_and_quiet_input_is_lifted_at_most_forty_decibels() {
        let mut leveler = Leveler::default();
        assert_eq!(leveler.gain(&tone(0.1, SAMPLE_RATE)[..VAD_FRAME]), 1.0);

        let mut leveler = Leveler::default();
        leveler.gain(&silence(0.1)[..VAD_FRAME]);
        let faint = quiet(tone(0.1, SAMPLE_RATE), 1e-5);
        assert_eq!(leveler.gain(&faint[..VAD_FRAME]), MAX_GAIN);
    }

    #[test]
    fn a_short_click_is_not_speech() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&tone(1.0, SAMPLE_RATE)[..VAD_FRAME * (ONSET_FRAMES - 1)]);
        pipeline.feed(&silence(1.0));
        assert!(pipeline.finish().is_empty());
    }
}

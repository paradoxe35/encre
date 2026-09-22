use std::collections::VecDeque;

use super::audio::SAMPLE_RATE;
use super::speech::Speech;

/// earshot wants exactly 256 samples (16 ms) at 16 kHz.
const VAD_FRAME: usize = 256;
const VAD_THRESHOLD: f32 = 0.5;

/// Speech stays open this long after the detector drops it, so a trailing word is not clipped.
const HANGOVER_FRAMES: usize = 28; // ~450 ms
/// Frames kept before onset, recovering the attack the detector needed to fire.
const PREFILL_FRAMES: usize = 28;
/// Consecutive speech frames before onset is believed, rejecting clicks.
const ONSET_FRAMES: usize = 4;

const RESAMPLER_CHUNK: usize = 1024;

/// Resamples to 16 kHz, then keeps only the frames the detector calls speech, noting
/// where a pause split it into phrases.
pub struct Pipeline {
    resampler: Option<rubato::FftFixedIn<f32>>,
    pending: Vec<f32>,
    frame: Vec<f32>,
    detector: earshot::Detector,
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
        let speaking = self.detector.predict_f32(&frame) >= VAD_THRESHOLD;

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

    /// Speech since the last call. Pause offsets are relative to this burst.
    pub fn take(&mut self) -> Speech {
        self.speech.take()
    }

    pub fn finish(&mut self) -> Speech {
        // Zero-padded to a whole chunk so the resampler gives back what it still holds.
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

    /// A 200 Hz tone, which the voice detector keeps in full.
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
        within(pause, HANGOVER, HANGOVER + SLACK, "hangover ending inside this burst");
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

    #[test]
    fn a_short_click_is_not_speech() {
        let mut pipeline = Pipeline::new(SAMPLE_RATE);
        pipeline.feed(&tone(1.0, SAMPLE_RATE)[..VAD_FRAME * (ONSET_FRAMES - 1)]);
        pipeline.feed(&silence(1.0));
        assert!(pipeline.finish().is_empty());
    }
}

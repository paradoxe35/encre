use std::collections::VecDeque;

use super::audio::SAMPLE_RATE;

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

/// Resamples to 16 kHz, then keeps only the frames the detector calls speech.
pub struct Pipeline {
    resampler: Option<rubato::FftFixedIn<f32>>,
    pending: Vec<f32>,
    frame: Vec<f32>,
    detector: earshot::Detector,
    speech: Vec<f32>,
    prefill: VecDeque<Vec<f32>>,
    onset: usize,
    hangover: usize,
}

impl Pipeline {
    pub fn new(input_rate: u32) -> Self {
        Self {
            resampler: (input_rate != SAMPLE_RATE)
                .then(|| {
                    rubato::FftFixedIn::<f32>::new(
                        input_rate as usize,
                        SAMPLE_RATE as usize,
                        RESAMPLER_CHUNK,
                        1,
                        1,
                    )
                    .ok()
                })
                .flatten(),
            pending: Vec::new(),
            frame: Vec::with_capacity(VAD_FRAME),
            detector: earshot::Detector::default(),
            speech: Vec::new(),
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
            self.speech.extend(self.prefill.drain(..).flatten());
            self.hangover = HANGOVER_FRAMES;
        }

        if self.hangover > 0 {
            self.hangover -= 1;
            self.speech.extend_from_slice(&frame);
            return;
        }

        if self.prefill.len() == PREFILL_FRAMES {
            self.prefill.pop_front();
        }
        self.prefill.push_back(frame);
    }

    pub fn take(&mut self) -> Vec<f32> {
        std::mem::take(&mut self.speech)
    }

    pub fn finish(&mut self) -> Vec<f32> {
        if !self.pending.is_empty() {
            let tail: Vec<f32> = std::mem::take(&mut self.pending);
            let mut padded = tail;
            padded.resize(RESAMPLER_CHUNK, 0.0);
            let flushed = self.resample(&padded);
            for sample in flushed {
                self.frame.push(sample);
                if self.frame.len() == VAD_FRAME {
                    let frame = std::mem::replace(&mut self.frame, Vec::with_capacity(VAD_FRAME));
                    self.classify(frame);
                }
            }
        }

        if self.hangover > 0 && !self.frame.is_empty() {
            self.speech.extend_from_slice(&self.frame);
        }
        self.frame.clear();

        std::mem::take(&mut self.speech)
    }
}

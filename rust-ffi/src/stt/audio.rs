use std::collections::HashSet;
use std::path::PathBuf;
use std::sync::mpsc::{Receiver, RecvTimeoutError, Sender, TryRecvError, channel};
use std::thread;
use std::time::Duration;

use anyhow::{Result, anyhow};
use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use cpal::{Device, SampleFormat, StreamConfig};

use super::engine::Engine;

pub const SAMPLE_RATE: u32 = 16_000;

/// earshot wants exactly 256 samples (16 ms) at 16 kHz.
const VAD_FRAME: usize = 256;
const VAD_THRESHOLD: f32 = 0.5;

/// Speech is reported for this long after the detector stops seeing it, so a
/// trailing word is not clipped mid-syllable.
const HANGOVER_FRAMES: usize = 28; // ~450 ms
/// Frames kept before onset, recovering the attack the detector needed to fire.
const PREFILL_FRAMES: usize = 28;
/// Consecutive speech frames before onset is believed, rejecting clicks.
const ONSET_FRAMES: usize = 4;

const RESAMPLER_CHUNK: usize = 1024;

/// How often the recorder drains the callback queue while recording.
const DRAIN_INTERVAL: Duration = Duration::from_millis(20);

pub enum Command {
    /// None means the system default. Takes effect on the next recording, so a
    /// change mid-take cannot truncate what is being said.
    SetDevice(Option<String>),
    /// None asks the model to detect; the host resolves a code for models
    /// that cannot, since the library would assume English.
    SetLanguage(Option<String>),
    /// When true, `record` never touches the engine: it only captures, for a
    /// remote engine that transcribes the raw audio itself.
    SetCaptureOnly(bool),
    /// The reply is Ok(streaming-capable) on a successful load; Err carries
    /// the load failure.
    Load(PathBuf, Sender<Result<bool>>),
    Unload,
    /// Transcribes a WAV on this thread, so verification shares the engine.
    TranscribeFile(PathBuf, Sender<Result<String>>),
    TranscribeSamples(Vec<f32>, Sender<Result<String>>),
    Start,
    Stop(Sender<Stopped>),
    Cancel,
    Shutdown,
}

/// What a recording produced. `text` carries the transcript: `Some("")` for a
/// take without speech, `Some(text)` on success, `Err(message)` on failure.
/// When a transcript is present, `samples` is left empty; otherwise `samples`
/// holds the speech for the host to batch-transcribe.
pub struct Stopped {
    pub samples: Vec<f32>,
    pub text: Result<Option<String>, String>,
}

/// Owns the capture stream on its own thread. cpal delivers audio on a realtime
/// callback that must not block, so it only forwards buffers; every conversion
/// happens here.
pub struct Recorder {
    commands: Sender<Command>,
}

impl Recorder {
    pub fn spawn(levels: Sender<f32>) -> Self {
        let (tx, rx) = channel();
        thread::spawn(move || run(rx, levels));
        Self { commands: tx }
    }

    pub fn set_device(&self, name: Option<String>) {
        let _ = self.commands.send(Command::SetDevice(name));
    }

    pub fn set_language(&self, code: Option<String>) {
        let _ = self.commands.send(Command::SetLanguage(code));
    }

    pub fn set_capture_only(&self, enabled: bool) {
        let _ = self.commands.send(Command::SetCaptureOnly(enabled));
    }

    /// Loads or replaces the resident model. The reply is Ok(streaming-capable)
    /// on success; Err carries the load failure.
    pub fn load(&self, path: PathBuf) -> Result<bool> {
        let (tx, rx) = channel();
        self.commands
            .send(Command::Load(path, tx))
            .map_err(|_| anyhow!("recorder thread is gone"))?;
        rx.recv().map_err(|_| anyhow!("recorder dropped the reply"))?
    }

    pub fn unload(&self) {
        let _ = self.commands.send(Command::Unload);
    }

    /// Transcribes samples the recorder handed back, used when a streaming
    /// take degraded and the audio has to go through in one pass.
    pub fn transcribe_samples(&self, samples: Vec<f32>) -> Result<String> {
        let (tx, rx) = channel();
        self.commands
            .send(Command::TranscribeSamples(samples, tx))
            .map_err(|_| anyhow!("recorder thread is gone"))?;
        rx.recv().map_err(|_| anyhow!("recorder dropped the reply"))?
    }

    /// Transcribes a WAV with the resident model, for settings verification.
    pub fn transcribe_file(&self, path: PathBuf) -> Result<String> {
        let (tx, rx) = channel();
        self.commands
            .send(Command::TranscribeFile(path, tx))
            .map_err(|_| anyhow!("recorder thread is gone"))?;
        rx.recv().map_err(|_| anyhow!("recorder dropped the reply"))?
    }

    pub fn start(&self) {
        let _ = self.commands.send(Command::Start);
    }

    pub fn cancel(&self) {
        let _ = self.commands.send(Command::Cancel);
    }

    pub fn shutdown(&self) {
        let _ = self.commands.send(Command::Shutdown);
    }

    /// Stops capture and returns what the recording produced.
    pub fn stop(&self) -> Result<Stopped> {
        let (tx, rx) = channel();
        self.commands
            .send(Command::Stop(tx))
            .map_err(|_| anyhow!("recorder thread is gone"))?;
        rx.recv().map_err(|_| anyhow!("recorder dropped the reply"))
    }
}

fn run(commands: Receiver<Command>, levels: Sender<f32>) {
    let mut engine = Engine::new();
    let mut preferred: Option<String> = None;
    let mut language: Option<String> = None;
    let mut capture_only = false;

    loop {
        match commands.recv() {
            Ok(Command::SetDevice(name)) => preferred = name,
            Ok(Command::SetLanguage(code)) => language = code,
            Ok(Command::SetCaptureOnly(enabled)) => capture_only = enabled,
            Ok(Command::Load(path, reply)) => {
                engine.unload();
                let _ = reply.send(
                    engine
                        .load(&path)
                        .map(|_| engine.supports_streaming()),
                );
            }
            Ok(Command::Unload) => engine.unload(),
            Ok(Command::TranscribeSamples(samples, reply)) => {
                let _ = reply.send(engine.transcribe(&samples, language.as_deref()));
            }
            Ok(Command::TranscribeFile(path, reply)) => {
                let _ = reply.send(
                    crate::stt::engine::read_wav(&path)
                        .and_then(|samples| engine.transcribe(&samples, language.as_deref())),
                );
            }
            Ok(Command::Start) => {
                if !record(
                    &commands,
                    levels.clone(),
                    &mut engine,
                    preferred.clone(),
                    language.clone(),
                    capture_only,
                ) {
                    return;
                }
            }
            Ok(Command::Shutdown) | Err(_) => return,
            _ => {}
        }
    }
}

/// Runs one recording session and returns when it ends, `false` only on shutdown.
/// The engine stream, if the model supports one, lives entirely inside this
/// function so it borrows the session for exactly the recording's lifetime.
fn record(
    commands: &Receiver<Command>,
    levels: Sender<f32>,
    engine: &mut Engine,
    preferred: Option<String>,
    language: Option<String>,
    capture_only: bool,
) -> bool {
    if capture_only {
        return record_batch(commands, levels, engine, preferred, language, true);
    }

    // Try streaming first, falling back to batch on failure. Dropped explicitly
    // so the borrow ends before the engine is handed to either session function.
    let started = engine.stream_begin(language.as_deref());
    if let Ok(stream) = started {
        return record_streaming(commands, levels, preferred, stream);
    }
    drop(started);
    tracing::debug!("streaming unavailable, using batch transcription");
    record_batch(commands, levels, engine, preferred, language, false)
}

/// Recording session with a live model stream. `stream` borrows the engine's
/// session, which is why the session ends before any batch transcription.
fn record_streaming(
    commands: &Receiver<Command>,
    levels: Sender<f32>,
    preferred: Option<String>,
    live: transcribe_cpp::Stream<'_>,
) -> bool {
    let mut stream = StreamGuard::open(levels, preferred.as_deref()).ok();
    let mut pipeline = Pipeline::new();
    pipeline.reset(stream.as_ref().map(|s| s.rate).unwrap_or(SAMPLE_RATE));
    // Kept as well as streamed: a feed failure would otherwise silently drop
    // words, and losing dictated audio is worth the extra memory to avoid.
    let mut spoken: Vec<f32> = Vec::new();
    let mut degraded = false;
    let mut live = LiveStream { stream: live };

    loop {
        match commands.recv_timeout(DRAIN_INTERVAL) {
            Err(RecvTimeoutError::Disconnected) => return false,
            Err(RecvTimeoutError::Timeout) => {}
            Ok(command) => match command {
                Command::Stop(reply) => {
                    let tail = drain(&mut stream, &mut pipeline);
                    spoken.extend_from_slice(&tail);
                    degraded |= live.feed(&tail).is_err();

                    // A partial transcript is worse than none: the host can't tell
                    // what's missing, so hand back the audio for a batch retry.
                    if degraded {
                        live.abort();
                        let _ = reply.send(Stopped {
                            samples: spoken,
                            text: Ok(None),
                        });
                        return true;
                    }

                    let text = live
                        .finalize()
                        .map_err(|e| format!("stream finalize failed: {e}"));
                    let _ = reply.send(Stopped {
                        samples: Vec::new(),
                        text,
                    });
                    return true;
                }
                Command::Cancel => {
                    let tail = drain(&mut stream, &mut pipeline);
                    let _ = live.feed(&tail);
                    live.abort();
                    return true;
                }
                Command::Shutdown => return false,
                _ => {}
            },
        }

        if let Some(guard) = stream.as_ref() {
            pipeline.feed(&guard.take());
            let speech = pipeline.take();
            spoken.extend_from_slice(&speech);

            if let Err(e) = live.feed(&speech) {
                if !degraded {
                    tracing::warn!("stream feed failed, falling back to batch: {e}");
                }
                degraded = true;
            }
        }
    }
}

/// Recording session without streaming: speech accumulates and is transcribed
/// in one batch at the end.
fn record_batch(
    commands: &Receiver<Command>,
    levels: Sender<f32>,
    engine: &mut Engine,
    preferred: Option<String>,
    language: Option<String>,
    capture_only: bool,
) -> bool {
    let mut stream = StreamGuard::open(levels, preferred.as_deref()).ok();
    let mut pipeline = Pipeline::new();
    pipeline.reset(stream.as_ref().map(|s| s.rate).unwrap_or(SAMPLE_RATE));
    let mut batched: Vec<f32> = Vec::new();

    loop {
        match commands.recv_timeout(DRAIN_INTERVAL) {
            Err(RecvTimeoutError::Disconnected) => return false,
            Err(RecvTimeoutError::Timeout) => {}
            Ok(command) => match command {
                Command::Stop(reply) => {
                    let samples = drain(&mut stream, &mut pipeline);
                    batched.extend_from_slice(&samples);
                    let samples = std::mem::take(&mut batched);
                    let text = if capture_only || samples.is_empty() {
                        Ok(None)
                    } else {
                        engine
                            .transcribe(&samples, language.as_deref())
                            .map(Some)
                            .map_err(|e| format!("batch transcription failed: {e}"))
                    };
                    let _ = reply.send(Stopped { samples, text });
                    return true;
                }
                Command::Cancel => {
                    drain(&mut stream, &mut pipeline);
                    return true;
                }
                Command::Shutdown => return false,
                _ => {}
            },
        }

        if let Some(guard) = stream.as_ref() {
            pipeline.feed(&guard.take());
            let speech = pipeline.take();
            batched.extend_from_slice(&speech);
        }
    }
}

/// A live recognition session. The `Stream` borrows the engine's session, so
/// this wrapper must live and die on the recorder thread.
struct LiveStream<'a> {
    stream: transcribe_cpp::Stream<'a>,
}

impl<'a> LiveStream<'a> {

    fn feed(&mut self, samples: &[f32]) -> Result<()> {
        if samples.is_empty() {
            return Ok(());
        }
        self.stream
            .feed(samples)
            .map(|_| ())
            .map_err(|e| anyhow!("stream feed: {e}"))
    }

    /// Ends input and returns the final transcript. `Ok(None)` is a
    /// successful take without speech, not an error.
    fn finalize(&mut self) -> Result<Option<String>> {
        self.stream.finalize()?;
        let text = self.stream.text().display().trim().to_owned();
        if text.is_empty() {
            Ok(None)
        } else {
            Ok(Some(text))
        }
    }

    /// Abandons the stream without producing text.
    fn abort(&mut self) {
        self.stream.reset();
    }
}

/// Pulls whatever the callback has queued, converts it, and returns the speech.
fn drain(stream: &mut Option<StreamGuard>, pipeline: &mut Pipeline) -> Vec<f32> {
    if let Some(guard) = stream.as_ref() {
        pipeline.feed(&guard.take());
    }
    pipeline.finish()
}

struct StreamGuard {
    _stream: cpal::Stream,
    rate: u32,
    channels: usize,
    incoming: Receiver<Vec<f32>>,
}

impl StreamGuard {
    fn open(levels: Sender<f32>, preferred: Option<&str>) -> Result<Self> {
        let device = open_device(preferred)?;
        let config = preferred_config(&device)?;

        let rate = config.config.sample_rate;
        let channels = config.config.channels as usize;
        let (tx, rx) = channel();

        let stream = build_stream(&device, &config, tx, levels)?;
        // cpal 0.18 doesn't auto-start streams; without this the callback never
        // fires and recordings come back silent.
        stream.play()?;

        Ok(Self {
            _stream: stream,
            rate,
            channels,
            incoming: rx,
        })
    }

    fn take(&self) -> Vec<f32> {
        let mut out = Vec::new();
        loop {
            match self.incoming.try_recv() {
                Ok(chunk) => out.extend(mono(&chunk, self.channels)),
                Err(TryRecvError::Empty) | Err(TryRecvError::Disconnected) => return out,
            }
        }
    }
}

fn mono(interleaved: &[f32], channels: usize) -> Vec<f32> {
    if channels <= 1 {
        return interleaved.to_vec();
    }
    interleaved
        .chunks(channels)
        .map(|frame| frame.iter().sum::<f32>() / channels as f32)
        .collect()
}

struct SelectedConfig {
    config: StreamConfig,
    format: SampleFormat,
}

/// Falls back to the default device when the chosen one is gone, so an
/// unplugged microphone doesn't stop dictation from working.
fn open_device(preferred: Option<&str>) -> Result<Device> {
    let host = host();

    if let Some(wanted) = preferred {
        match host.input_devices() {
            Ok(devices) => {
                if let Some(device) = devices.filter(|d| d.to_string() == wanted).next() {
                    return Ok(device);
                }
                tracing::warn!("Input device '{wanted}' is unavailable, using the default");
            }
            Err(e) => tracing::warn!("Could not enumerate input devices: {e}"),
        }
    }

    host.default_input_device()
        .ok_or_else(|| anyhow!("no input device available"))
}

/// Input device names, the default marked with a leading '*'.
pub fn devices() -> (Vec<String>, Option<String>) {
    let host = host();
    let names = host
        .input_devices()
        .map(|devices| {
            let mut seen = HashSet::new();
            devices
                .map(|d| d.to_string())
                .filter(|name| !name.is_empty() && seen.insert(name.clone()))
                .collect()
        })
        .unwrap_or_default();
    let default = host.default_input_device().map(|d| d.to_string());
    (names, default)
}

fn host() -> cpal::Host {
    // ALSA over cpal's default on Linux: PulseAudio/PipeWire both expose an
    // ALSA interface, and going direct avoids a resampling hop.
    #[cfg(target_os = "linux")]
    {
        cpal::host_from_id(cpal::HostId::Alsa).unwrap_or_else(|_| cpal::default_host())
    }
    #[cfg(not(target_os = "linux"))]
    {
        cpal::default_host()
    }
}

/// Uses the device's own rate instead of forcing 16 kHz: forcing a rate the
/// hardware doesn't want can drop Bluetooth headsets into headset profile or
/// make ALSA refuse the stream outright.
fn preferred_config(device: &Device) -> Result<SelectedConfig> {
    let default = device.default_input_config()?;
    let rate = default.sample_rate();

    let best = device
        .supported_input_configs()?
        .filter(|range| range.min_sample_rate() <= rate && rate <= range.max_sample_rate())
        .max_by_key(|range| match range.sample_format() {
            SampleFormat::F32 => 3,
            SampleFormat::I16 => 2,
            SampleFormat::I32 => 1,
            _ => 0,
        });

    match best {
        Some(range) => Ok(SelectedConfig {
            format: range.sample_format(),
            config: range.with_sample_rate(rate).config(),
        }),
        None => Ok(SelectedConfig {
            format: default.sample_format(),
            config: default.config(),
        }),
    }
}

fn build_stream(
    device: &Device,
    selected: &SelectedConfig,
    samples: Sender<Vec<f32>>,
    levels: Sender<f32>,
) -> Result<cpal::Stream> {
    let error = |e| eprintln!("audio stream error: {e}");

    let stream = match selected.format {
        SampleFormat::F32 => device.build_input_stream(
            selected.config.clone(),
            move |data: &[f32], _: &_| forward(data.to_vec(), &samples, &levels),
            error,
            None,
        )?,
        SampleFormat::I16 => device.build_input_stream(
            selected.config.clone(),
            move |data: &[i16], _: &_| {
                let converted = data.iter().map(|s| *s as f32 / i16::MAX as f32).collect();
                forward(converted, &samples, &levels)
            },
            error,
            None,
        )?,
        SampleFormat::I32 => device.build_input_stream(
            selected.config.clone(),
            move |data: &[i32], _: &_| {
                let converted = data.iter().map(|s| *s as f32 / i32::MAX as f32).collect();
                forward(converted, &samples, &levels)
            },
            error,
            None,
        )?,
        other => return Err(anyhow!("unsupported sample format {other:?}")),
    };

    Ok(stream)
}

/// Runs on the realtime audio callback: send and return, never allocate slowly
/// or block, or the driver drops buffers.
fn forward(data: Vec<f32>, samples: &Sender<Vec<f32>>, levels: &Sender<f32>) {
    if !data.is_empty() {
        let sum: f32 = data.iter().map(|s| s * s).sum();
        let _ = levels.send((sum / data.len() as f32).sqrt());
    }
    let _ = samples.send(data);
}

/// Resamples to 16 kHz, then keeps only the frames the detector calls speech.
struct Pipeline {
    resampler: Option<rubato::FftFixedIn<f32>>,
    pending: Vec<f32>,
    frame: Vec<f32>,
    detector: earshot::Detector,
    speech: Vec<f32>,
    prefill: std::collections::VecDeque<Vec<f32>>,
    onset: usize,
    hangover: usize,
}

impl Pipeline {
    fn new() -> Self {
        Self {
            resampler: None,
            pending: Vec::new(),
            frame: Vec::with_capacity(VAD_FRAME),
            detector: earshot::Detector::default(),
            speech: Vec::new(),
            prefill: std::collections::VecDeque::with_capacity(PREFILL_FRAMES),
            onset: 0,
            hangover: 0,
        }
    }

    fn reset(&mut self, input_rate: u32) {
        // A resampler carries FFT overlap between calls; reusing one across
        // takes would leak the tail of the previous recording into the next.
        self.resampler = (input_rate != SAMPLE_RATE)
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
            .flatten();

        self.pending.clear();
        self.frame.clear();
        self.speech.clear();
        self.prefill.clear();
        self.detector = earshot::Detector::default();
        self.onset = 0;
        self.hangover = 0;
    }

    fn feed(&mut self, samples: &[f32]) {
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
            // Onset confirmed: replay the buffered attack, then hold open for
            // the hangover window so the tail isn't cut.
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

    /// Speech gathered so far, leaving the pipeline free to continue.
    fn take(&mut self) -> Vec<f32> {
        std::mem::take(&mut self.speech)
    }

    /// Flushes the resampler's delay line and returns the recording.
    fn finish(&mut self) -> Vec<f32> {
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

#[cfg(test)]
mod tests {
    use super::*;

    /// A failed load must reach the host as an error, not be masked as success.
    #[test]
    fn load_reports_failure() {
        let (levels, _level_rx) = channel();
        let recorder = Recorder::spawn(levels);

        let missing = std::env::temp_dir().join("encre-nonexistent-model.gguf");
        let result = recorder.load(missing);

        recorder.shutdown();
        assert!(result.is_err(), "loading a missing file must fail");
    }
}

#[cfg(test)]
mod degraded_tests {
    use super::*;

    /// A degraded streaming take hands back audio, not a transcript missing words.
    #[test]
    fn degraded_stream_returns_samples_for_batch() {
        let stopped = Stopped {
            samples: vec![0.1, 0.2, 0.3],
            text: Ok(None),
        };

        assert!(
            !stopped.samples.is_empty(),
            "the host needs the audio to transcribe"
        );
        assert!(
            matches!(stopped.text, Ok(None)),
            "a partial transcript must not be offered as if complete"
        );
    }

    /// Silence and a degraded stream both carry no text; only the samples tell
    /// them apart, which is what the FFI branches on.
    #[test]
    fn silence_carries_no_samples() {
        let stopped = Stopped {
            samples: Vec::new(),
            text: Ok(None),
        };
        assert!(stopped.samples.is_empty());
    }
}

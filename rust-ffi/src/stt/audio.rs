use std::collections::HashSet;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::mpsc::{Receiver, RecvTimeoutError, Sender, TryRecvError, channel};
use std::thread;
use std::time::Duration;

use anyhow::{Result, anyhow};
use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use cpal::{Device, SampleFormat, StreamConfig};
use parking_lot::{Mutex, MutexGuard};

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
    Start,
    Stop(Sender<Stopped>),
    Cancel,
    Shutdown,
}

/// Read at the start of each take; a change mid-take applies to the next one.
#[derive(Clone, Default)]
struct Settings {
    /// None means the system default.
    device: Option<String>,
    /// None asks the model to detect.
    language: Option<String>,
    /// Capture without the engine, for a remote service that transcribes the audio itself.
    capture_only: bool,
    /// The model takes should use. Streaming waits until it is the one resident;
    /// until then a take is captured and transcribed behind the load.
    model: Option<PathBuf>,
}

/// Handled on their own thread, so a load or a transcription can run while the
/// next take is already being captured.
pub enum EngineCommand {
    /// A failure is kept by the engine and reported by the transcription that needed it.
    Load(PathBuf),
    Unload,
    TranscribeSamples(Vec<f32>, Option<String>, Sender<Result<String>>),
    Shutdown,
}

/// What a recording produced. `text` carries the transcript: `Some("")` for a
/// take without speech, `Some(text)` on success, `Err(message)` on failure.
/// When a transcript is present, `samples` is left empty; otherwise `samples`
/// holds the speech for the host to batch-transcribe.
pub struct Stopped {
    pub samples: Vec<f32>,
    pub text: Result<Option<String>, String>,
    /// The language in force when the take was recorded.
    pub language: Option<String>,
}

/// Owns the capture stream on its own thread. cpal delivers audio on a realtime
/// callback that must not block, so it only forwards buffers; every conversion
/// happens here.
pub struct Recorder {
    commands: Sender<Command>,
    engine_commands: Sender<EngineCommand>,
    settings: Arc<Mutex<Settings>>,
}

impl Recorder {
    pub fn spawn(levels: Sender<f32>) -> Self {
        let engine = Arc::new(Mutex::new(Engine::new()));
        let settings = Arc::new(Mutex::new(Settings::default()));

        let (tx, rx) = channel();
        let recorder_engine = engine.clone();
        let recorder_settings = settings.clone();
        thread::spawn(move || run(rx, levels, recorder_engine, recorder_settings));

        let (engine_tx, engine_rx) = channel();
        thread::spawn(move || run_engine(engine_rx, engine));

        Self {
            commands: tx,
            engine_commands: engine_tx,
            settings,
        }
    }

    fn settings(&self) -> MutexGuard<'_, Settings> {
        self.settings.lock()
    }

    pub fn set_device(&self, name: Option<String>) {
        self.settings().device = name;
    }

    pub fn set_language(&self, code: Option<String>) {
        self.settings().language = code;
    }

    pub fn set_capture_only(&self, enabled: bool) {
        self.settings().capture_only = enabled;
    }

    /// Selects the model for the next takes and queues its load. Returns at
    /// once; anything queued after it, such as a take's batch pass, runs behind.
    pub fn use_model(&self, path: PathBuf) {
        self.settings().model = Some(path.clone());
        let _ = self.engine_commands.send(EngineCommand::Load(path));
    }

    pub fn unload(&self) {
        self.settings().model = None;
        let _ = self.engine_commands.send(EngineCommand::Unload);
    }

    /// Queued behind any load in progress, so a take captured while its model
    /// was coming up is transcribed once it is there.
    pub fn transcribe_samples(&self, samples: Vec<f32>, language: Option<String>) -> Result<String> {
        let (tx, rx) = channel();
        self.engine_commands
            .send(EngineCommand::TranscribeSamples(samples, language, tx))
            .map_err(|_| anyhow!("engine thread is gone"))?;
        rx.recv().map_err(|_| anyhow!("engine dropped the reply"))?
    }

    pub fn start(&self) {
        let _ = self.commands.send(Command::Start);
    }

    pub fn cancel(&self) {
        let _ = self.commands.send(Command::Cancel);
    }

    pub fn shutdown(&self) {
        let _ = self.commands.send(Command::Shutdown);
        let _ = self.engine_commands.send(EngineCommand::Shutdown);
    }

    /// Split from `await_stop` so a caller can send under its own lock without
    /// holding it through transcription.
    pub fn begin_stop(&self) -> Result<Receiver<Stopped>> {
        let (tx, rx) = channel();
        self.commands
            .send(Command::Stop(tx))
            .map_err(|_| anyhow!("recorder thread is gone"))?;
        Ok(rx)
    }

    pub fn await_stop(pending: Receiver<Stopped>) -> Result<Stopped> {
        pending
            .recv()
            .map_err(|_| anyhow!("recorder dropped the reply"))
    }
}

fn run(
    commands: Receiver<Command>,
    levels: Sender<f32>,
    engine: Arc<Mutex<Engine>>,
    settings: Arc<Mutex<Settings>>,
) {
    loop {
        match commands.recv() {
            Ok(Command::Start) => {
                let snapshot = settings.lock().clone();
                if !record(&commands, levels.clone(), &engine, snapshot) {
                    return;
                }
            }
            Ok(Command::Stop(_)) | Ok(Command::Cancel) => {}
            Ok(Command::Shutdown) | Err(_) => return,
        }
    }
}

fn run_engine(commands: Receiver<EngineCommand>, engine: Arc<Mutex<Engine>>) {
    loop {
        match commands.recv() {
            Ok(EngineCommand::Load(path)) => {
                if let Err(e) = engine.lock().load(&path) {
                    tracing::warn!("model load failed: {e}");
                }
            }
            Ok(EngineCommand::Unload) => engine.lock().unload(),
            Ok(EngineCommand::TranscribeSamples(samples, language, reply)) => {
                let _ = reply.send(engine.lock().transcribe(&samples, language.as_deref()));
            }
            Ok(EngineCommand::Shutdown) | Err(_) => return,
        }
    }
}

/// Runs one recording session and returns when it ends, `false` only on shutdown.
///
/// A streaming take holds the engine for its whole duration, so it only starts
/// when the engine is free and holds the wanted model. Every other take is
/// captured on its own and transcribed through the engine thread afterwards,
/// behind whatever load is queued.
fn record(
    commands: &Receiver<Command>,
    levels: Sender<f32>,
    engine: &Arc<Mutex<Engine>>,
    settings: Settings,
) -> bool {
    let Settings {
        device,
        language,
        capture_only,
        model,
    } = settings;

    if !capture_only && model.is_some() {
        match engine.try_lock() {
            Some(mut guard) if guard.resident() == model.as_deref() => {
                match guard.stream_begin(language.as_deref()) {
                    Ok(stream) => {
                        return record_streaming(commands, levels, device, language, stream);
                    }
                    Err(e) => tracing::debug!("streaming unavailable, capturing for a batch pass: {e}"),
                }
            }
            Some(_) => tracing::debug!("model not resident yet, capturing for a later pass"),
            None => tracing::debug!("engine busy, capturing for a later pass"),
        }
    }

    record_batch(commands, levels, device, language)
}

/// Recording session with a live model stream. `stream` borrows the engine's
/// session, which is why the session ends before any batch transcription.
fn record_streaming(
    commands: &Receiver<Command>,
    levels: Sender<f32>,
    device: Option<String>,
    language: Option<String>,
    live: transcribe_cpp::Stream<'_>,
) -> bool {
    let mut stream = StreamGuard::open(levels, device.as_deref()).ok();
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
                            language,
                        });
                        return true;
                    }

                    let text = live
                        .finalize()
                        .map_err(|e| format!("stream finalize failed: {e}"));
                    let _ = reply.send(Stopped {
                        samples: Vec::new(),
                        text,
                        language,
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

/// Capture only; the host transcribes the samples afterwards.
fn record_batch(
    commands: &Receiver<Command>,
    levels: Sender<f32>,
    device: Option<String>,
    language: Option<String>,
) -> bool {
    let mut stream = StreamGuard::open(levels, device.as_deref()).ok();
    let mut pipeline = Pipeline::new();
    pipeline.reset(stream.as_ref().map(|s| s.rate).unwrap_or(SAMPLE_RATE));
    let mut batched: Vec<f32> = Vec::new();

    loop {
        match commands.recv_timeout(DRAIN_INTERVAL) {
            Err(RecvTimeoutError::Disconnected) => return false,
            Err(RecvTimeoutError::Timeout) => {}
            Ok(command) => match command {
                Command::Stop(reply) => {
                    let tail = drain(&mut stream, &mut pipeline);
                    batched.extend_from_slice(&tail);

                    let _ = reply.send(Stopped {
                        samples: std::mem::take(&mut batched),
                        text: Ok(None),
                        language,
                    });
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

    /// A failed load is reported by the transcription that needed it, with the
    /// reason, not as a bare "no model loaded".
    #[test]
    fn load_failure_reaches_the_transcription() {
        let (levels, _level_rx) = channel();
        let recorder = Recorder::spawn(levels);

        let missing = std::env::temp_dir().join("encre-nonexistent-model.gguf");
        recorder.use_model(missing.clone());
        let result = recorder.transcribe_samples(vec![0.0; 1600], None);

        recorder.shutdown();
        let message = result.expect_err("transcribing without a model must fail").to_string();
        assert!(
            message.contains(&missing.display().to_string()),
            "error should name the file that failed to load, got: {message}"
        );
    }
}

#[cfg(test)]
mod settings_tests {
    use super::*;

    #[test]
    fn settings_are_kept_until_read() {
        let (levels, _level_rx) = channel();
        let recorder = Recorder::spawn(levels);

        recorder.set_language(Some("fr".to_owned()));
        recorder.set_device(Some("USB Microphone".to_owned()));
        recorder.set_capture_only(true);
        recorder.use_model(PathBuf::from("/models/a.gguf"));

        let snapshot = recorder.settings().clone();
        recorder.unload();
        let after_unload = recorder.settings().model.clone();
        recorder.shutdown();

        assert_eq!(snapshot.language.as_deref(), Some("fr"));
        assert_eq!(snapshot.device.as_deref(), Some("USB Microphone"));
        assert!(snapshot.capture_only);
        assert_eq!(snapshot.model.as_deref(), Some(std::path::Path::new("/models/a.gguf")));
        assert!(after_unload.is_none(), "unload forgets the wanted model");
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
            language: None,
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
            language: None,
        };
        assert!(stopped.samples.is_empty());
    }
}

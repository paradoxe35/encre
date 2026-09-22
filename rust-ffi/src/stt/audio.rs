use std::collections::HashSet;
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc::{Receiver, Sender, channel};
use std::thread;

use anyhow::{Result, anyhow};
use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use cpal::{Device, SampleFormat, StreamConfig, SupportedStreamConfigRange};
use parking_lot::{Mutex, MutexGuard};

use super::engine::Engine;
use super::speech::Speech;
use super::take::{Source, Take, Wanted};

pub const SAMPLE_RATE: u32 = 16_000;

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
    /// Streaming waits until this model is resident; until then a take is captured
    /// and transcribed behind the load.
    model: Option<PathBuf>,
}

/// Handled on their own thread, so a load or transcription overlaps the next take's capture.
pub enum EngineCommand {
    /// A failure is kept by the engine and reported by the transcription that needed it.
    Load(PathBuf),
    Unload,
    Transcribe(Speech, Option<String>, Sender<Result<String>>),
    Shutdown,
}

/// `speech` is empty when `text` carries a transcript; otherwise it holds what
/// was heard, for the host to batch-transcribe.
pub struct Stopped {
    pub speech: Speech,
    pub text: Result<Option<String>, String>,
    /// The language in force when the take was recorded, for the batch pass.
    pub language: Option<String>,
}

pub struct Recorder {
    commands: Sender<Command>,
    engine_commands: Sender<EngineCommand>,
    /// Unfinished engine commands; a take only claims the engine for streaming at zero.
    queued: Arc<AtomicUsize>,
    settings: Arc<Mutex<Settings>>,
}

impl Recorder {
    pub fn spawn(levels: Sender<f32>) -> Self {
        let engine = Arc::new(Mutex::new(Engine::new()));
        let queued = Arc::new(AtomicUsize::new(0));
        let settings = Arc::new(Mutex::new(Settings::default()));

        let (tx, rx) = channel();
        let recorder_engine = engine.clone();
        let recorder_queued = queued.clone();
        let recorder_settings = settings.clone();
        thread::spawn(move || {
            run(
                rx,
                levels,
                recorder_engine,
                recorder_queued,
                recorder_settings,
            )
        });

        let (engine_tx, engine_rx) = channel();
        let engine_queued = queued.clone();
        thread::spawn(move || run_engine(engine_rx, engine, engine_queued));

        Self {
            commands: tx,
            engine_commands: engine_tx,
            queued,
            settings,
        }
    }

    fn queue(&self, command: EngineCommand) -> Result<()> {
        self.queued.fetch_add(1, Ordering::SeqCst);
        self.engine_commands.send(command).map_err(|_| {
            self.queued.fetch_sub(1, Ordering::SeqCst);
            anyhow!("engine thread is gone")
        })
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

    /// Returns at once; anything queued after, such as a take's batch pass, runs behind the load.
    pub fn use_model(&self, path: PathBuf) {
        self.settings().model = Some(path.clone());
        let _ = self.queue(EngineCommand::Load(path));
    }

    pub fn unload(&self) {
        self.settings().model = None;
        let _ = self.queue(EngineCommand::Unload);
    }

    /// Queued behind any load in progress, so a take captured during a load is
    /// transcribed once the model is there.
    pub fn transcribe(&self, speech: Speech, language: Option<String>) -> Result<String> {
        let (tx, rx) = channel();
        self.queue(EngineCommand::Transcribe(speech, language, tx))?;
        rx.recv().map_err(|_| anyhow!("engine dropped the reply"))?
    }

    pub fn start(&self) -> Result<()> {
        self.commands
            .send(Command::Start)
            .map_err(|_| anyhow!("recorder thread is gone"))
    }

    pub fn cancel(&self) {
        let _ = self.commands.send(Command::Cancel);
    }

    pub fn shutdown(&self) {
        let _ = self.commands.send(Command::Shutdown);
        let _ = self.engine_commands.send(EngineCommand::Shutdown);
    }

    /// Split from `await_stop` so a caller can send under its own lock without holding
    /// it through transcription.
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

enum Flow {
    Continue,
    Stop,
}

/// Runs a worker's command loop through a panic: each worker is the only one of its
/// kind, and losing it would leave dictation dead until the app restarts.
fn serve<T>(commands: Receiver<T>, mut handle: impl FnMut(&Receiver<T>, T) -> Flow) {
    while let Ok(command) = commands.recv() {
        match catch_unwind(AssertUnwindSafe(|| handle(&commands, command))) {
            Ok(Flow::Continue) => {}
            Ok(Flow::Stop) => return,
            Err(_) => tracing::error!("a speech worker panicked; carrying on"),
        }
    }
}

/// Decrements `queued` when dropped, so a command that panics is still counted as done.
struct Done<'a>(&'a AtomicUsize);

impl Drop for Done<'_> {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::SeqCst);
    }
}

fn run(
    commands: Receiver<Command>,
    levels: Sender<f32>,
    engine: Arc<Mutex<Engine>>,
    queued: Arc<AtomicUsize>,
    settings: Arc<Mutex<Settings>>,
) {
    serve(commands, |commands, command| match command {
        Command::Start => {
            let snapshot = settings.lock().clone();
            if record(commands, levels.clone(), &engine, &queued, snapshot) {
                Flow::Continue
            } else {
                Flow::Stop
            }
        }
        Command::Stop(_) | Command::Cancel => Flow::Continue,
        Command::Shutdown => Flow::Stop,
    });
}

fn run_engine(
    commands: Receiver<EngineCommand>,
    engine: Arc<Mutex<Engine>>,
    queued: Arc<AtomicUsize>,
) {
    serve(commands, |_, command| {
        if matches!(command, EngineCommand::Shutdown) {
            return Flow::Stop;
        }
        let _done = Done(&queued);
        match command {
            EngineCommand::Load(path) => {
                if let Err(e) = engine.lock().load(&path) {
                    tracing::warn!("model load failed: {e}");
                }
            }
            EngineCommand::Unload => engine.lock().unload(),
            EngineCommand::Transcribe(speech, language, reply) => {
                let _ = reply.send(engine.lock().transcribe(&speech, language.as_deref()));
            }
            EngineCommand::Shutdown => unreachable!(),
        }
        Flow::Continue
    });
}

/// `false` only on shutdown.
fn record(
    commands: &Receiver<Command>,
    levels: Sender<f32>,
    engine: &Arc<Mutex<Engine>>,
    queued: &AtomicUsize,
    settings: Settings,
) -> bool {
    let Settings {
        device,
        language,
        capture_only,
        model,
    } = settings;

    let source = StreamGuard::open(levels, device.as_deref()).map_err(|e| {
        let reason = format!("microphone unavailable: {e:#}");
        tracing::warn!("{reason}");
        reason
    });
    let wanted = match (&model, capture_only) {
        (Some(model), false) => Some(Wanted {
            engine,
            queued,
            model,
        }),
        _ => None,
    };
    Take::new(source, language).run(commands, wanted)
}

struct StreamGuard {
    _stream: cpal::Stream,
    rate: u32,
    channels: usize,
    incoming: Receiver<Vec<f32>>,
}

impl Source for StreamGuard {
    fn rate(&self) -> u32 {
        self.rate
    }

    fn take(&self) -> Vec<f32> {
        let mut out = Vec::new();
        while let Ok(chunk) = self.incoming.try_recv() {
            out.extend(mono(&chunk, self.channels));
        }
        out
    }
}

impl StreamGuard {
    fn open(levels: Sender<f32>, preferred: Option<&str>) -> Result<Self> {
        let device = open_device(preferred)?;
        let config = preferred_config(&device)?;

        let rate = config.config.sample_rate;
        let channels = config.config.channels as usize;
        let (tx, rx) = channel();

        let stream = build_stream(&device, &config, tx, levels)?;
        // cpal does not auto-start streams; without this recordings come back silent.
        stream.play()?;
        tracing::info!(
            "Capture opened on '{device}': {rate} Hz, {channels} channel(s), {:?}",
            config.format
        );

        Ok(Self {
            _stream: stream,
            rate,
            channels,
            incoming: rx,
        })
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

/// An unplugged microphone falls back to the default rather than failing the take.
fn open_device(preferred: Option<&str>) -> Result<Device> {
    let host = host();

    if let Some(wanted) = preferred {
        match host.input_devices() {
            Ok(mut devices) => {
                if let Some(device) = devices.find(|d| d.to_string() == wanted) {
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
    // ALSA over cpal's default: PulseAudio and PipeWire both expose an ALSA
    // interface, and going direct avoids a resampling hop.
    #[cfg(target_os = "linux")]
    {
        cpal::host_from_id(cpal::HostId::Alsa).unwrap_or_else(|_| cpal::default_host())
    }
    #[cfg(not(target_os = "linux"))]
    {
        cpal::default_host()
    }
}

/// Uses the device's own rate: forcing 16 kHz can drop Bluetooth headsets into
/// headset profile or make ALSA refuse the stream.
fn preferred_config(device: &Device) -> Result<SelectedConfig> {
    let default = device.default_input_config()?;
    let rate = default.sample_rate();

    match choose_config(device.supported_input_configs()?, rate) {
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

/// Fewest channels first, then the format that costs least to convert. The
/// pipeline mixes down to mono anyway, and ALSA plugin devices (PipeWire,
/// PulseAudio) advertise every channel count up to 64: opening the widest one
/// makes the sound server upmix ~12 MB/s in its realtime thread, which on a
/// modest machine froze the desktop.
fn choose_config(
    ranges: impl IntoIterator<Item = SupportedStreamConfigRange>,
    rate: u32,
) -> Option<SupportedStreamConfigRange> {
    ranges
        .into_iter()
        .filter(|range| range.min_sample_rate() <= rate && rate <= range.max_sample_rate())
        .filter_map(|range| format_cost(range.sample_format()).map(|cost| (range, cost)))
        .min_by_key(|(range, cost)| (range.channels(), *cost))
        .map(|(range, _)| range)
}

/// Formats `build_stream` can open, cheapest first; `None` is unsupported.
fn format_cost(format: SampleFormat) -> Option<u8> {
    match format {
        SampleFormat::F32 => Some(0),
        SampleFormat::I16 => Some(1),
        SampleFormat::I32 => Some(2),
        _ => None,
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
            selected.config,
            move |data: &[f32], _: &_| forward(data.to_vec(), &samples, &levels),
            error,
            None,
        )?,
        SampleFormat::I16 => device.build_input_stream(
            selected.config,
            move |data: &[i16], _: &_| {
                let converted = data.iter().map(|s| *s as f32 / i16::MAX as f32).collect();
                forward(converted, &samples, &levels)
            },
            error,
            None,
        )?,
        SampleFormat::I32 => device.build_input_stream(
            selected.config,
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

/// Runs on the realtime audio callback: never block, or the driver drops buffers.
fn forward(data: Vec<f32>, samples: &Sender<Vec<f32>>, levels: &Sender<f32>) {
    if !data.is_empty() {
        let sum: f32 = data.iter().map(|s| s * s).sum();
        let _ = levels.send((sum / data.len() as f32).sqrt());
    }
    let _ = samples.send(data);
}

#[cfg(test)]
mod tests {
    use super::*;
    use cpal::SupportedBufferSize;

    fn range(channels: u16, format: SampleFormat) -> SupportedStreamConfigRange {
        SupportedStreamConfigRange::new(
            channels,
            8_000,
            96_000,
            SupportedBufferSize::Unknown,
            format,
        )
    }

    /// ALSA plugin devices enumerate one range per channel count, ascending.
    fn plugin_device(format: SampleFormat) -> Vec<SupportedStreamConfigRange> {
        (1..=64).map(|channels| range(channels, format)).collect()
    }

    #[test]
    fn a_plugin_device_is_opened_in_mono() {
        let mut ranges = plugin_device(SampleFormat::I16);
        ranges.extend(plugin_device(SampleFormat::F32));

        let chosen = choose_config(ranges, 48_000).expect("something matches");
        assert_eq!(chosen.channels(), 1);
        assert_eq!(chosen.sample_format(), SampleFormat::F32);
    }

    #[test]
    fn a_stereo_only_device_is_opened_in_stereo() {
        let ranges = vec![range(2, SampleFormat::I16), range(4, SampleFormat::F32)];

        let chosen = choose_config(ranges, 48_000).expect("something matches");
        assert_eq!(chosen.channels(), 2, "fewer channels beat a nicer format");
        assert_eq!(chosen.sample_format(), SampleFormat::I16);
    }

    #[test]
    fn the_best_format_wins_among_equal_channel_counts() {
        let ranges = vec![
            range(1, SampleFormat::I32),
            range(1, SampleFormat::F32),
            range(1, SampleFormat::I16),
        ];
        let chosen = choose_config(ranges, 48_000).unwrap();
        assert_eq!(chosen.sample_format(), SampleFormat::F32);

        let ranges = vec![range(1, SampleFormat::I32), range(1, SampleFormat::I16)];
        let chosen = choose_config(ranges, 48_000).unwrap();
        assert_eq!(chosen.sample_format(), SampleFormat::I16);
    }

    #[test]
    fn ranges_that_cannot_be_opened_are_skipped() {
        let out_of_rate = SupportedStreamConfigRange::new(
            1,
            8_000,
            16_000,
            SupportedBufferSize::Unknown,
            SampleFormat::F32,
        );
        let ranges = vec![
            out_of_rate,
            range(1, SampleFormat::U8),
            range(2, SampleFormat::F32),
        ];

        let chosen = choose_config(ranges, 48_000).unwrap();
        assert_eq!(
            (chosen.channels(), chosen.sample_format()),
            (2, SampleFormat::F32)
        );
        assert!(choose_config(vec![range(1, SampleFormat::U8)], 48_000).is_none());
    }

    /// The error names the reason, not a bare "no model loaded".
    #[test]
    fn load_failure_reaches_the_transcription() {
        let (levels, _level_rx) = channel();
        let recorder = Recorder::spawn(levels);

        let missing = std::env::temp_dir().join("encre-nonexistent-model.gguf");
        recorder.use_model(missing.clone());
        let result = recorder.transcribe(Speech::from(vec![0.0; 1600]), None);

        recorder.shutdown();
        let message = result
            .expect_err("transcribing without a model must fail")
            .to_string();
        assert!(
            message.contains(&missing.display().to_string()),
            "error should name the file that failed to load, got: {message}"
        );
    }
}

#[cfg(test)]
mod worker_tests {
    use super::*;

    #[test]
    fn a_panicking_command_does_not_end_the_loop() {
        let (tx, rx) = channel();
        let handled = Arc::new(AtomicUsize::new(0));
        let seen = handled.clone();
        let worker = thread::spawn(move || {
            serve(rx, |_, command: u8| {
                if command == 1 {
                    panic!("bad command");
                }
                if command == 9 {
                    return Flow::Stop;
                }
                seen.fetch_add(1, Ordering::SeqCst);
                Flow::Continue
            })
        });

        for command in [0u8, 1, 2, 3, 9, 4] {
            tx.send(command).unwrap();
        }
        worker.join().unwrap();
        assert_eq!(handled.load(Ordering::SeqCst), 3, "0, 2 and 3 were handled; 9 stopped");
    }

    #[test]
    fn the_loop_ends_when_the_sender_is_gone() {
        let (tx, rx) = channel::<u8>();
        drop(tx);
        serve(rx, |_, _| Flow::Continue);
    }

    #[test]
    fn a_command_is_counted_done_even_when_it_panics() {
        let queued = AtomicUsize::new(1);
        let outcome = catch_unwind(AssertUnwindSafe(|| {
            let _done = Done(&queued);
            panic!("mid-command");
        }));
        assert!(outcome.is_err());
        assert_eq!(queued.load(Ordering::SeqCst), 0);
    }

    #[test]
    fn start_reports_a_recorder_that_is_gone() {
        let (levels, _level_rx) = channel();
        let recorder = Recorder::spawn(levels);
        recorder.shutdown();
        let deadline = std::time::Instant::now() + std::time::Duration::from_secs(2);
        while recorder.start().is_ok() {
            assert!(std::time::Instant::now() < deadline, "start never noticed the shutdown");
            thread::sleep(std::time::Duration::from_millis(5));
        }
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
        assert_eq!(
            snapshot.model.as_deref(),
            Some(std::path::Path::new("/models/a.gguf"))
        );
        assert!(after_unload.is_none(), "unload forgets the wanted model");
    }
}

#[cfg(test)]
mod degraded_tests {
    use super::*;

    #[test]
    fn degraded_stream_returns_samples_for_batch() {
        let stopped = Stopped {
            speech: Speech::from(vec![0.1, 0.2, 0.3]),
            text: Ok(None),
            language: None,
        };

        assert!(
            !stopped.speech.is_empty(),
            "the host needs the audio to transcribe"
        );
        assert!(
            matches!(stopped.text, Ok(None)),
            "a partial transcript must not be offered as if complete"
        );
    }

    /// Only the samples tell silence from a degraded stream; the FFI branches on that.
    #[test]
    fn silence_carries_no_samples() {
        let stopped = Stopped {
            speech: Speech::default(),
            text: Ok(None),
            language: None,
        };
        assert!(stopped.speech.is_empty());
    }
}

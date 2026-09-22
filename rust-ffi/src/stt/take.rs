use std::path::Path;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc::{Receiver, RecvTimeoutError};
use std::time::Duration;

use anyhow::Result;
use parking_lot::{Mutex, MutexGuard};

use super::audio::{Command, SAMPLE_RATE, Stopped};
use super::pipeline::Pipeline;
use super::speech::Speech;

const DRAIN_INTERVAL: Duration = Duration::from_millis(20);

/// Backlog is fed a slice at a time, draining the microphone in between so its
/// queue stays bounded.
const BACKLOG_SLICE: usize = SAMPLE_RATE as usize;

/// Audio arriving from the microphone, so a take can run without a device in tests.
pub trait Source {
    fn rate(&self) -> u32;
    fn take(&self) -> Vec<f32>;
}

pub trait Live {
    fn feed(&mut self, samples: &[f32]) -> Result<()>;
    /// `Ok(None)` is a successful take without speech, not an error.
    fn finalize(&mut self) -> Result<Option<String>>;
    fn abort(&mut self);
}

/// What a take needs from the engine, so it can be tested without a model.
pub trait Recognizer {
    type Live<'a>: Live
    where
        Self: 'a;

    fn resident(&self) -> Option<&Path>;
    fn stream_begin(&mut self, language: Option<&str>) -> Result<Self::Live<'_>>;
}

/// The model a take should stream with as soon as the engine is free and holds it.
pub struct Wanted<'a, E> {
    pub engine: &'a Arc<Mutex<E>>,
    /// A take never claims the engine ahead of queued commands, or an earlier
    /// take's transcription would wait for this one to end.
    pub queued: &'a AtomicUsize,
    pub model: &'a Path,
}

/// Captures until the engine is idle with the wanted model, then streams. All audio
/// is kept, so a broken stream or a model that never arrives still gets a batch pass.
pub struct Take<S: Source> {
    /// `Err` when the microphone could not be opened; reported at stop instead of
    /// an empty transcript.
    source: Result<S, String>,
    pipeline: Pipeline,
    spoken: Speech,
    language: Option<String>,
}

enum Captured<'a, E> {
    Ended(bool),
    Ready(MutexGuard<'a, E>),
}

enum Streamed {
    Ended(bool),
    /// The stream broke; the take goes on capturing for a batch pass.
    Degraded,
}

impl<S: Source> Take<S> {
    pub fn new(source: Result<S, String>, language: Option<String>) -> Self {
        let rate = source.as_ref().map(Source::rate).unwrap_or(SAMPLE_RATE);
        Self {
            source,
            pipeline: Pipeline::new(rate),
            spoken: Speech::default(),
            language,
        }
    }

    /// `false` only on shutdown.
    pub fn run<E: Recognizer>(
        mut self,
        commands: &Receiver<Command>,
        mut wanted: Option<Wanted<'_, E>>,
    ) -> bool {
        loop {
            let mut guard = match self.capture(commands, wanted.take()) {
                Captured::Ended(running) => return running,
                Captured::Ready(guard) => guard,
            };

            let live = match guard.stream_begin(self.language.as_deref()) {
                Ok(live) => live,
                Err(e) => {
                    tracing::info!("streaming unavailable, capturing for a batch pass: {e}");
                    continue;
                }
            };

            tracing::info!(
                backlog_ms = self.spoken.samples.len() * 1000 / SAMPLE_RATE as usize,
                "streaming"
            );
            match self.stream(commands, live) {
                Streamed::Ended(running) => return running,
                Streamed::Degraded => {}
            }
        }
    }

    fn capture<'a, E: Recognizer>(
        &mut self,
        commands: &Receiver<Command>,
        wanted: Option<Wanted<'a, E>>,
    ) -> Captured<'a, E> {
        loop {
            match commands.recv_timeout(DRAIN_INTERVAL) {
                Err(RecvTimeoutError::Disconnected) => return Captured::Ended(false),
                Err(RecvTimeoutError::Timeout) => {}
                Ok(Command::Stop(reply)) => {
                    self.drain();
                    let _ = reply.send(self.stopped_with_speech());
                    return Captured::Ended(true);
                }
                Ok(Command::Cancel) => {
                    self.drain();
                    return Captured::Ended(true);
                }
                Ok(Command::Shutdown) => return Captured::Ended(false),
                Ok(Command::Start) => {}
            }

            self.pull();

            if let Some(wanted) = &wanted
                && wanted.queued.load(Ordering::SeqCst) == 0
                && let Some(guard) = wanted.engine.try_lock()
                && guard.resident() == Some(wanted.model)
            {
                return Captured::Ready(guard);
            }
        }
    }

    fn stream<L: Live>(&mut self, commands: &Receiver<Command>, live: L) -> Streamed {
        let mut live = Streaming::new(live);

        let mut fed = 0;
        while fed < self.spoken.samples.len() && !live.degraded {
            let end = (fed + BACKLOG_SLICE).min(self.spoken.samples.len());
            live.feed(&self.spoken.samples[fed..end]);
            fed = end;
            self.pull();
        }
        if live.degraded {
            live.abort();
            return Streamed::Degraded;
        }

        loop {
            match commands.recv_timeout(DRAIN_INTERVAL) {
                Err(RecvTimeoutError::Disconnected) => return Streamed::Ended(false),
                Err(RecvTimeoutError::Timeout) => {}
                Ok(Command::Stop(reply)) => {
                    let tail = self.drain();
                    live.feed(tail);
                    let _ = reply.send(self.finish(live));
                    return Streamed::Ended(true);
                }
                Ok(Command::Cancel) => {
                    live.abort();
                    return Streamed::Ended(true);
                }
                Ok(Command::Shutdown) => return Streamed::Ended(false),
                Ok(Command::Start) => {}
            }

            let speech = self.pull();
            live.feed(speech);
            if live.degraded {
                live.abort();
                return Streamed::Degraded;
            }
        }
    }

    /// Anything short of a transcript hands back the audio for a batch pass: a partial
    /// or empty result is worse than none, since the host cannot tell what is missing.
    fn finish<L: Live>(&mut self, mut live: Streaming<L>) -> Stopped {
        if live.degraded || self.source.is_err() {
            live.abort();
            return self.stopped_with_speech();
        }
        match live.inner.finalize() {
            Ok(Some(text)) => Stopped {
                speech: Speech::default(),
                text: Ok(Some(text)),
                language: self.language.clone(),
            },
            Ok(None) => {
                tracing::info!("stream produced no text, falling back to batch");
                live.abort();
                self.stopped_with_speech()
            }
            Err(e) => {
                tracing::warn!("stream finalize failed, falling back to batch: {e}");
                live.abort();
                self.stopped_with_speech()
            }
        }
    }

    fn pull(&mut self) -> &[f32] {
        if let Ok(source) = &self.source {
            self.pipeline.feed(&source.take());
        }
        let burst = self.pipeline.take();
        self.keep(burst)
    }

    fn drain(&mut self) -> &[f32] {
        if let Ok(source) = &self.source {
            self.pipeline.feed(&source.take());
        }
        let tail = self.pipeline.finish();
        self.keep(tail)
    }

    fn keep(&mut self, burst: Speech) -> &[f32] {
        let from = self.spoken.samples.len();
        self.spoken.append(burst);
        &self.spoken.samples[from..]
    }

    fn stopped_with_speech(&mut self) -> Stopped {
        let text = match &self.source {
            Ok(_) => Ok(None),
            Err(reason) => Err(reason.clone()),
        };
        Stopped {
            speech: self.spoken.take(),
            text,
            language: self.language.clone(),
        }
    }
}

/// After the first failure nothing more is fed; the take falls back to the audio it kept.
struct Streaming<L: Live> {
    inner: L,
    degraded: bool,
}

impl<L: Live> Streaming<L> {
    fn new(inner: L) -> Self {
        Self {
            inner,
            degraded: false,
        }
    }

    fn feed(&mut self, samples: &[f32]) {
        if self.degraded || samples.is_empty() {
            return;
        }
        if let Err(e) = self.inner.feed(samples) {
            tracing::warn!("stream feed failed, falling back to batch: {e}");
            self.degraded = true;
        }
    }

    fn abort(&mut self) {
        self.inner.abort();
    }
}

#[cfg(test)]
mod tests {
    use std::marker::PhantomData;
    use std::path::PathBuf;
    use std::sync::mpsc::{Sender, channel};
    use std::thread;
    use std::time::Instant;

    use anyhow::anyhow;

    use super::*;

    #[derive(Default)]
    struct FakeState {
        resident: Option<PathBuf>,
        refuse_stream: bool,
        fail_feed: bool,
        fail_finalize: bool,
        text: Option<String>,
        begins: usize,
        fed: Vec<f32>,
        aborted: bool,
    }

    /// A streaming take holds the engine lock, so observations go through a lock of their own.
    struct FakeEngine {
        resident: Option<PathBuf>,
        state: Arc<Mutex<FakeState>>,
    }

    struct FakeLive<'a> {
        state: Arc<Mutex<FakeState>>,
        engine: PhantomData<&'a FakeEngine>,
    }

    impl Live for FakeLive<'_> {
        fn feed(&mut self, samples: &[f32]) -> Result<()> {
            let mut state = self.state.lock();
            if state.fail_feed {
                return Err(anyhow!("feed refused"));
            }
            state.fed.extend_from_slice(samples);
            Ok(())
        }

        fn finalize(&mut self) -> Result<Option<String>> {
            let state = self.state.lock();
            if state.fail_finalize {
                return Err(anyhow!("finalize refused"));
            }
            Ok(state.text.clone())
        }

        fn abort(&mut self) {
            self.state.lock().aborted = true;
        }
    }

    impl Recognizer for FakeEngine {
        type Live<'a> = FakeLive<'a>;

        fn resident(&self) -> Option<&Path> {
            self.resident.as_deref()
        }

        fn stream_begin(&mut self, _language: Option<&str>) -> Result<FakeLive<'_>> {
            let mut state = self.state.lock();
            state.begins += 1;
            if state.refuse_stream {
                return Err(anyhow!("this model cannot stream"));
            }
            Ok(FakeLive {
                state: self.state.clone(),
                engine: PhantomData,
            })
        }
    }

    struct Microphone(Receiver<Vec<f32>>);

    impl Source for Microphone {
        fn rate(&self) -> u32 {
            SAMPLE_RATE
        }
        fn take(&self) -> Vec<f32> {
            let mut out = Vec::new();
            while let Ok(chunk) = self.0.try_recv() {
                out.extend(chunk);
            }
            out
        }
    }

    /// A 200 Hz tone, which the voice detector keeps in full.
    fn tone(seconds: f32) -> Vec<f32> {
        let n = (seconds * SAMPLE_RATE as f32) as usize;
        (0..n)
            .map(|i| (i as f32 * 200.0 * std::f32::consts::TAU / SAMPLE_RATE as f32).sin() * 0.5)
            .collect()
    }

    struct Harness {
        commands: Sender<Command>,
        audio: Sender<Vec<f32>>,
        state: Arc<Mutex<FakeState>>,
        engine: Arc<Mutex<FakeEngine>>,
        queued: Arc<AtomicUsize>,
        handle: thread::JoinHandle<bool>,
    }

    fn start(state: FakeState, resident: Option<&str>, model: Option<&str>) -> Harness {
        let state = Arc::new(Mutex::new(state));
        let engine = Arc::new(Mutex::new(FakeEngine {
            resident: resident.map(PathBuf::from),
            state: state.clone(),
        }));
        let queued = Arc::new(AtomicUsize::new(0));
        let (commands, rx) = channel();
        let (audio, incoming) = channel();

        let model = model.map(PathBuf::from);
        let thread_engine = engine.clone();
        let thread_queued = queued.clone();
        let handle = thread::spawn(move || {
            let wanted = model.as_deref().map(|model| Wanted {
                engine: &thread_engine,
                queued: &thread_queued,
                model,
            });
            Take::new(Ok(Microphone(incoming)), Some("fr".to_owned())).run(&rx, wanted)
        });

        Harness {
            commands,
            audio,
            state,
            engine,
            queued,
            handle,
        }
    }

    impl Harness {
        fn stop(self) -> (Stopped, bool) {
            let (reply, stopped) = channel();
            self.commands.send(Command::Stop(reply)).unwrap();
            let stopped = stopped
                .recv_timeout(Duration::from_secs(2))
                .expect("the take must answer a stop");
            (stopped, self.handle.join().unwrap())
        }

        fn begins(&self) -> usize {
            self.state.lock().begins
        }

        fn wait_until(&self, what: &str, condition: impl Fn(&FakeState) -> bool) {
            let deadline = Instant::now() + Duration::from_secs(2);
            while !condition(&self.state.lock()) {
                assert!(Instant::now() < deadline, "timed out waiting for {what}");
                thread::sleep(Duration::from_millis(5));
            }
        }

        /// Feeds audio and gives the take a few ticks to pull it.
        fn speak(&self, seconds: f32) {
            self.audio.send(tone(seconds)).unwrap();
            thread::sleep(DRAIN_INTERVAL * 3);
        }
    }

    fn streams(text: &str) -> FakeState {
        FakeState {
            text: Some(text.to_owned()),
            ..FakeState::default()
        }
    }

    const MODEL: &str = "/models/a.gguf";

    #[test]
    fn a_take_without_a_model_hands_back_everything_it_heard() {
        let take = start(FakeState::default(), None, None);
        take.speak(2.0);

        let (stopped, running) = take.stop();
        assert!(running, "a stop keeps the recorder running");
        assert!(matches!(stopped.text, Ok(None)));
        assert_eq!(stopped.speech.samples.len(), tone(2.0).len());
        assert_eq!(stopped.language.as_deref(), Some("fr"));
    }

    #[test]
    fn a_take_without_a_microphone_reports_why() {
        let (commands, rx) = channel();
        let handle = thread::spawn(move || {
            Take::<Microphone>::new(Err("no input device available".to_owned()), None)
                .run::<FakeEngine>(&rx, None)
        });

        let (reply, stopped) = channel();
        commands.send(Command::Stop(reply)).unwrap();
        let stopped = stopped
            .recv_timeout(Duration::from_secs(2))
            .expect("the take must answer a stop");

        assert!(handle.join().unwrap(), "a stop keeps the recorder running");
        assert_eq!(stopped.text, Err("no input device available".to_owned()));
        assert!(stopped.speech.samples.is_empty());
    }

    #[test]
    fn no_stream_while_the_model_is_missing() {
        let take = start(FakeState::default(), None, Some(MODEL));
        take.speak(0.5);

        let begins = take.begins();
        let (stopped, _) = take.stop();
        assert!(matches!(stopped.text, Ok(None)));
        assert_eq!(begins, 0);
    }

    #[test]
    fn no_stream_while_the_engine_has_work_queued() {
        let take = start(streams("bonjour"), Some(MODEL), Some(MODEL));
        take.queued.store(1, Ordering::SeqCst);
        take.speak(0.5);

        let begins = take.begins();
        take.stop();
        assert_eq!(begins, 0, "queued work goes first");
    }

    #[test]
    fn no_stream_while_the_engine_is_held() {
        let take = start(streams("bonjour"), Some(MODEL), Some(MODEL));
        let held = take.engine.lock();
        take.speak(0.5);

        let (reply, stopped) = channel();
        take.commands.send(Command::Stop(reply)).unwrap();
        stopped
            .recv_timeout(Duration::from_secs(2))
            .expect("a stop never waits for the engine");
        drop(held);
        take.handle.join().unwrap();
        assert_eq!(take.state.lock().begins, 0);
    }

    #[test]
    fn no_stream_with_a_different_model() {
        let take = start(streams("bonjour"), Some("/models/other.gguf"), Some(MODEL));
        take.speak(0.5);

        let begins = take.begins();
        take.stop();
        assert_eq!(begins, 0);
    }

    #[test]
    fn the_take_switches_to_streaming_when_the_model_arrives_and_feeds_the_backlog() {
        let take = start(streams("bonjour"), None, Some(MODEL));
        take.speak(1.5);
        assert_eq!(take.begins(), 0);

        take.engine.lock().resident = Some(PathBuf::from(MODEL));
        take.wait_until("the switch to streaming", |s| s.begins == 1);
        take.speak(0.5);

        let state = take.state.clone();
        let (stopped, running) = take.stop();
        assert!(running);
        assert_eq!(stopped.text, Ok(Some("bonjour".to_owned())));
        assert!(
            stopped.speech.samples.is_empty(),
            "a streamed take hands back text, not audio"
        );
        assert_eq!(
            state.lock().fed.len(),
            tone(2.0).len(),
            "everything heard reached the stream"
        );
    }

    #[test]
    fn a_refused_stream_falls_back_to_a_batch_pass() {
        let state = FakeState {
            refuse_stream: true,
            ..streams("bonjour")
        };
        let take = start(state, Some(MODEL), Some(MODEL));
        take.wait_until("the stream attempt", |s| s.begins == 1);
        take.speak(1.0);

        let begins = take.begins();
        let (stopped, _) = take.stop();
        assert!(matches!(stopped.text, Ok(None)));
        assert_eq!(stopped.speech.samples.len(), tone(1.0).len());
        assert_eq!(begins, 1, "streaming is not retried within a take");
    }

    #[test]
    fn a_broken_stream_releases_the_engine_and_keeps_the_audio() {
        let state = FakeState {
            fail_feed: true,
            ..streams("bonjour")
        };
        let take = start(state, Some(MODEL), Some(MODEL));
        take.speak(1.0);
        take.wait_until("the stream to break", |s| s.aborted);

        assert!(
            take.engine.try_lock().is_some(),
            "the engine is free again mid-take"
        );
        take.speak(1.0);

        let (stopped, _) = take.stop();
        assert!(matches!(stopped.text, Ok(None)));
        assert_eq!(
            stopped.speech.samples.len(),
            tone(2.0).len(),
            "audio before and after the break is kept"
        );
    }

    #[test]
    fn a_failed_finalize_hands_back_the_audio() {
        let state = FakeState {
            fail_finalize: true,
            ..streams("bonjour")
        };
        let take = start(state, Some(MODEL), Some(MODEL));
        take.wait_until("streaming", |s| s.begins == 1);
        take.speak(1.0);

        let state = take.state.clone();
        let (stopped, _) = take.stop();
        assert!(matches!(stopped.text, Ok(None)));
        assert_eq!(stopped.speech.samples.len(), tone(1.0).len());
        assert!(state.lock().aborted);
    }

    #[test]
    fn an_empty_streamed_result_hands_back_the_audio() {
        let state = FakeState {
            text: None,
            ..FakeState::default()
        };
        let take = start(state, Some(MODEL), Some(MODEL));
        take.wait_until("streaming", |s| s.begins == 1);
        take.speak(1.0);

        let state = take.state.clone();
        let (stopped, _) = take.stop();
        assert!(matches!(stopped.text, Ok(None)));
        assert_eq!(stopped.speech.samples.len(), tone(1.0).len());
        assert!(state.lock().aborted, "the finished stream is released");
    }

    #[test]
    fn pauses_survive_the_take() {
        let take = start(FakeState::default(), None, None);
        take.speak(1.0);
        take.audio.send(vec![0.0; SAMPLE_RATE as usize * 2]).unwrap();
        thread::sleep(DRAIN_INTERVAL * 3);
        take.speak(1.0);

        let (stopped, _) = take.stop();
        assert_eq!(stopped.speech.pauses.len(), 1);
        let pause = stopped.speech.pauses[0];
        assert!(pause > tone(1.0).len() && pause < stopped.speech.samples.len(), "pause at {pause}");
    }

    #[test]
    fn cancel_while_streaming_aborts_the_stream() {
        let take = start(streams("bonjour"), Some(MODEL), Some(MODEL));
        take.wait_until("streaming", |s| s.begins == 1);

        take.commands.send(Command::Cancel).unwrap();
        assert!(
            take.handle.join().unwrap(),
            "cancel keeps the recorder running"
        );
        assert!(take.state.lock().aborted);
    }

    #[test]
    fn shutdown_ends_the_take() {
        let take = start(FakeState::default(), None, None);
        take.commands.send(Command::Shutdown).unwrap();
        assert!(!take.handle.join().unwrap());
    }
}

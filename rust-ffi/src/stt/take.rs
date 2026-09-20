use std::path::Path;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::mpsc::{Receiver, RecvTimeoutError};
use std::time::Duration;

use anyhow::Result;
use parking_lot::{Mutex, MutexGuard};

use super::audio::{Command, SAMPLE_RATE, Stopped};
use super::pipeline::Pipeline;

/// How often the take drains the microphone and, while waiting for a model,
/// looks at the engine.
const DRAIN_INTERVAL: Duration = Duration::from_millis(20);

/// Audio captured before a switch to streaming is fed this much at a time,
/// draining the microphone in between so its queue stays bounded.
const BACKLOG_SLICE: usize = SAMPLE_RATE as usize;

/// Audio arriving from the microphone, so a take can run without a device in tests.
pub trait Source {
    fn rate(&self) -> u32;
    fn take(&self) -> Vec<f32>;
}

/// A live recognition session on the engine.
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
    /// Commands still queued for the engine thread. A take never claims the
    /// engine ahead of them, or an earlier take's transcription would wait
    /// for this one to end.
    pub queued: &'a AtomicUsize,
    pub model: &'a Path,
}

/// One recording session, from Start to Stop or Cancel.
///
/// A take begins by capturing on its own. If a model is wanted, every tick it
/// looks at the engine, and the moment the engine is idle and holds that model
/// it opens a stream, feeds what was captured so far, and streams the rest.
/// Everything captured is also kept, so a stream that breaks, or a model that
/// never arrives, still hands the host the audio for a batch pass.
pub struct Take<S: Source> {
    /// `Err` when the microphone could not be opened; the take then reports that
    /// at stop instead of handing back an empty transcript.
    source: Result<S, String>,
    pipeline: Pipeline,
    spoken: Vec<f32>,
    language: Option<String>,
}

enum Captured<'a, E> {
    /// The take ended; `false` only on shutdown.
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
            spoken: Vec::new(),
            language,
        }
    }

    /// Runs the session and returns when it ends, `false` only on shutdown.
    pub fn run<E: Recognizer>(
        mut self,
        commands: &Receiver<Command>,
        wanted: Option<Wanted<'_, E>>,
    ) -> bool {
        let mut wanted = wanted;
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
                backlog_ms = self.spoken.len() * 1000 / SAMPLE_RATE as usize,
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
                    let _ = reply.send(self.stopped_with_samples());
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
        while fed < self.spoken.len() && !live.degraded {
            let end = (fed + BACKLOG_SLICE).min(self.spoken.len());
            live.feed(&self.spoken[fed..end]);
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
                    live.feed(&tail);
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
            live.feed(&speech);
            if live.degraded {
                live.abort();
                return Streamed::Degraded;
            }
        }
    }

    /// Ends the stream. A partial transcript is worse than none, since the host
    /// cannot tell what is missing, so any failure hands back the audio instead.
    fn finish<L: Live>(&mut self, mut live: Streaming<L>) -> Stopped {
        if live.degraded || self.source.is_err() {
            live.abort();
            return self.stopped_with_samples();
        }
        match live.inner.finalize() {
            Ok(text) => Stopped {
                samples: Vec::new(),
                text: Ok(text),
                language: self.language.clone(),
            },
            Err(e) => {
                tracing::warn!("stream finalize failed, falling back to batch: {e}");
                live.abort();
                self.stopped_with_samples()
            }
        }
    }

    /// Pulls what the microphone has queued and returns the speech in it.
    fn pull(&mut self) -> Vec<f32> {
        if let Ok(source) = &self.source {
            self.pipeline.feed(&source.take());
        }
        let speech = self.pipeline.take();
        self.spoken.extend_from_slice(&speech);
        speech
    }

    /// Pulls the rest, flushing the resampler, and returns the final speech.
    fn drain(&mut self) -> Vec<f32> {
        if let Ok(source) = &self.source {
            self.pipeline.feed(&source.take());
        }
        let tail = self.pipeline.finish();
        self.spoken.extend_from_slice(&tail);
        tail
    }

    fn stopped_with_samples(&mut self) -> Stopped {
        let text = match &self.source {
            Ok(_) => Ok(None),
            Err(reason) => Err(reason.clone()),
        };
        Stopped {
            samples: std::mem::take(&mut self.spoken),
            text,
            language: self.language.clone(),
        }
    }
}

/// A live stream that remembers the first failure: after it, nothing more is
/// fed and the take falls back to the audio it kept.
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

    /// A streaming take holds the engine lock, so observations go through a
    /// lock of their own. The resident path is owned here too, so `resident`
    /// can hand out a plain borrow.
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
        assert_eq!(stopped.samples.len(), tone(2.0).len());
        assert_eq!(stopped.language.as_deref(), Some("fr"));
    }

    /// A microphone that never opened must not look like a take with nothing said in it.
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
        assert!(stopped.samples.is_empty());
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
            stopped.samples.is_empty(),
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
        assert_eq!(stopped.samples.len(), tone(1.0).len());
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
            stopped.samples.len(),
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
        assert_eq!(stopped.samples.len(), tone(1.0).len());
        assert!(state.lock().aborted);
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

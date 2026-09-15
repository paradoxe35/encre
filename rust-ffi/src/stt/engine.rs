use std::path::{Path, PathBuf};

use anyhow::{Result, anyhow};
use transcribe_cpp::{RunOptions, StreamOptions};

/// Holds the loaded model between calls. Loading costs seconds; recording costs
/// milliseconds. Keeping the session resident is the largest win available to
/// dictation latency.
///
/// The engine supports one streaming dictation at a time: [`Engine::stream_begin`]
/// claims the model's compute lease, [`Engine::stream_feed`] delivers live audio,
/// and [`Engine::stream_finalize`] ends input. Models without streaming support
/// reject `stream_begin` and the host falls back to [`Engine::transcribe`].
pub struct Engine {
    loaded: Option<Loaded>,
    /// Why the last load failed, reported by the transcription that needed it.
    load_error: Option<String>,
}

struct Loaded {
    path: PathBuf,
    session: transcribe_cpp::Session,
}

impl Engine {
    pub fn new() -> Self {
        Self {
            loaded: None,
            load_error: None,
        }
    }

    pub fn unload(&mut self) {
        self.loaded = None;
    }

    pub fn resident(&self) -> Option<&Path> {
        self.loaded.as_ref().map(|l| l.path.as_path())
    }

    /// Idempotent for the resident model. The old model is freed before the new
    /// one is read, so two are never in memory at once.
    pub fn load(&mut self, path: &Path) -> Result<()> {
        if self.loaded.as_ref().is_some_and(|l| l.path == path) {
            return Ok(());
        }

        self.loaded = None;
        self.load_error = None;

        let result = Self::open(path);
        match &result {
            Ok(_) => {}
            Err(e) => self.load_error = Some(e.to_string()),
        }
        self.loaded = Some(result?);
        Ok(())
    }

    fn open(path: &Path) -> Result<Loaded> {
        let model =
            transcribe_cpp::Model::load_with(path, &transcribe_cpp::ModelOptions::default())
                .map_err(|e| anyhow!("failed to load {}: {e}", path.display()))?;
        let session = model
            .session_with(&transcribe_cpp::SessionOptions::default())
            .map_err(|e| anyhow!("failed to open a session: {e}"))?;
        Ok(Loaded {
            path: path.to_path_buf(),
            session,
        })
    }

    fn unavailable(&self) -> anyhow::Error {
        match &self.load_error {
            Some(reason) => anyhow!("{reason}"),
            None => anyhow!("no model loaded"),
        }
    }

    pub fn transcribe(&mut self, samples: &[f32], language: Option<&str>) -> Result<String> {
        let unavailable = self.unavailable();
        let loaded = self.loaded.as_mut().ok_or(unavailable)?;

        let options = RunOptions {
            language: language.map(str::to_owned),
            ..Default::default()
        };

        loaded
            .session
            .run(samples, &options)
            .map(|out| out.text.trim().to_owned())
            .map_err(|e| anyhow!("transcription failed: {e}"))
    }

    /// Reports whether the resident model can stream. Models advertise this in
    /// their GGUF metadata; it is not a build-time property.
    pub fn supports_streaming(&self) -> bool {
        self.loaded
            .as_ref()
            .is_some_and(|l| l.session.model().capabilities().supports_streaming)
    }

    /// Begins a live stream. The returned `Stream` borrows the session, so it
    /// must be fed and finalized by the same owner with no other `run` call
    /// in between; the recorder thread guarantees that.
    pub fn stream_begin(&mut self, language: Option<&str>) -> Result<transcribe_cpp::Stream<'_>> {
        let unavailable = self.unavailable();
        let loaded = self.loaded.as_mut().ok_or(unavailable)?;

        let options = RunOptions {
            language: language.map(str::to_owned),
            ..Default::default()
        };
        loaded
            .session
            .stream(&options, &StreamOptions::default())
            .map_err(|e| anyhow!("failed to begin stream: {e}"))
    }
}

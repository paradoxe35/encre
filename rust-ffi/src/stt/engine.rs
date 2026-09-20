use std::path::{Path, PathBuf};

use anyhow::{Result, anyhow};
use transcribe_cpp::{RunOptions, StreamOptions};

use super::take::{Live, Recognizer};

/// Keeps the session resident between takes: loading costs seconds, a take costs
/// milliseconds.
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

    /// The resident model is freed before the new one is read, so two are never in memory at once.
    pub fn load(&mut self, path: &Path) -> Result<()> {
        if self.loaded.as_ref().is_some_and(|l| l.path == path) {
            return Ok(());
        }

        self.loaded = None;
        self.load_error = None;

        match Self::open(path) {
            Ok(loaded) => {
                self.loaded = Some(loaded);
                Ok(())
            }
            Err(e) => {
                self.load_error = Some(e.to_string());
                Err(e)
            }
        }
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

    fn session(&mut self) -> Result<&mut transcribe_cpp::Session> {
        let unavailable = match &self.load_error {
            Some(reason) => anyhow!("{reason}"),
            None => anyhow!("no model loaded"),
        };
        self.loaded
            .as_mut()
            .map(|l| &mut l.session)
            .ok_or(unavailable)
    }

    pub fn transcribe(&mut self, samples: &[f32], language: Option<&str>) -> Result<String> {
        self.session()?
            .run(samples, &run_options(language))
            .map(|out| out.text.trim().to_owned())
            .map_err(|e| anyhow!("transcription failed: {e}"))
    }
}

fn run_options(language: Option<&str>) -> RunOptions {
    RunOptions {
        language: language.map(str::to_owned),
        ..Default::default()
    }
}

impl Recognizer for Engine {
    type Live<'a> = transcribe_cpp::Stream<'a>;

    fn resident(&self) -> Option<&Path> {
        self.loaded.as_ref().map(|l| l.path.as_path())
    }

    fn stream_begin(&mut self, language: Option<&str>) -> Result<Self::Live<'_>> {
        self.session()?
            .stream(&run_options(language), &StreamOptions::default())
            .map_err(|e| anyhow!("failed to begin stream: {e}"))
    }
}

impl Live for transcribe_cpp::Stream<'_> {
    fn feed(&mut self, samples: &[f32]) -> Result<()> {
        transcribe_cpp::Stream::feed(self, samples)
            .map(|_| ())
            .map_err(|e| anyhow!("stream feed: {e}"))
    }

    fn finalize(&mut self) -> Result<Option<String>> {
        transcribe_cpp::Stream::finalize(self)?;
        let text = self.text().display().trim().to_owned();
        Ok((!text.is_empty()).then_some(text))
    }

    fn abort(&mut self) {
        self.reset();
    }
}

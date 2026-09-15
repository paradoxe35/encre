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
}

struct Loaded {
    path: PathBuf,
    session: transcribe_cpp::Session,
}

impl Engine {
    pub fn new() -> Self {
        Self { loaded: None }
    }

    pub fn unload(&mut self) {
        self.loaded = None;
    }

    /// Idempotent for the resident model. The old model is freed before the new
    /// one is read, so two are never in memory at once.
    pub fn load(&mut self, path: &Path) -> Result<()> {
        if self.loaded.as_ref().is_some_and(|l| l.path == path) {
            return Ok(());
        }

        self.loaded = None;

        let model =
            transcribe_cpp::Model::load_with(path, &transcribe_cpp::ModelOptions::default())
                .map_err(|e| anyhow!("failed to load {}: {e}", path.display()))?;
        let session = model
            .session_with(&transcribe_cpp::SessionOptions::default())
            .map_err(|e| anyhow!("failed to open a session: {e}"))?;

        self.loaded = Some(Loaded {
            path: path.to_path_buf(),
            session,
        });
        Ok(())
    }

    pub fn transcribe(&mut self, samples: &[f32], language: Option<&str>) -> Result<String> {
        let loaded = self
            .loaded
            .as_mut()
            .ok_or_else(|| anyhow!("no model loaded"))?;

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
        let loaded = self
            .loaded
            .as_mut()
            .ok_or_else(|| anyhow!("no model loaded"))?;

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

/// Reads a 16 kHz mono 16-bit WAV, the format the engine expects.
pub fn read_wav(path: &Path) -> Result<Vec<f32>> {
    let mut reader = hound::WavReader::open(path)?;
    let spec = reader.spec();

    if spec.channels != 1 || spec.sample_rate != 16_000 {
        return Err(anyhow!(
            "expected 16 kHz mono, got {} Hz and {} channels",
            spec.sample_rate,
            spec.channels
        ));
    }

    Ok(reader
        .samples::<i16>()
        .filter_map(Result::ok)
        .map(|s| s as f32 / i16::MAX as f32)
        .collect())
}

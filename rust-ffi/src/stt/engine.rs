use std::path::{Path, PathBuf};

use anyhow::{Result, anyhow};
use transcribe_cpp::{Feature, RunOptions, StreamOptions};

use super::audio::SAMPLE_RATE;
use super::speech::Speech;
use super::take::{Live, Recognizer};

/// Models without long-form support decode one window and quietly drop the rest
/// (Canary is built for clips under 40 s), so their takes are cut at pauses.
const PIECE_SECS: usize = 30;

/// A piece that starts or ends mid-word can decode to nothing; a little silence on
/// both sides keeps the decoder honest. One side alone is not enough.
const SILENCE_PAD_SECS: f32 = 0.5;

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
    /// Whether the model handles audio longer than its window on its own.
    long_form: bool,
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
            long_form: model.supports(Feature::LongForm),
        })
    }

    fn loaded(&mut self) -> Result<&mut Loaded> {
        let unavailable = match &self.load_error {
            Some(reason) => anyhow!("{reason}"),
            None => anyhow!("no model loaded"),
        };
        self.loaded.as_mut().ok_or(unavailable)
    }

    pub fn transcribe(&mut self, speech: &Speech, language: Option<&str>) -> Result<String> {
        let loaded = self.loaded()?;
        let pieces = speech.pieces(piece_limit(loaded.long_form));
        if pieces.len() > 1 {
            tracing::info!(pieces = pieces.len(), "transcribing the take in pieces");
        }

        let options = run_options(language);
        join(pieces.into_iter().map(|piece| {
            loaded
                .session
                .run(&padded(piece), &options)
                .map(|out| out.text.trim().to_owned())
                .map_err(|e| anyhow!("transcription failed: {e}"))
        }))
    }
}

fn piece_limit(long_form: bool) -> usize {
    if long_form {
        usize::MAX
    } else {
        PIECE_SECS * SAMPLE_RATE as usize
    }
}

fn padded(piece: &[f32]) -> Vec<f32> {
    let pad = (SILENCE_PAD_SECS * SAMPLE_RATE as f32) as usize;
    let mut padded = vec![0.0; pad];
    padded.extend_from_slice(piece);
    padded.resize(pad + piece.len() + pad, 0.0);
    padded
}

/// One failure fails the take: a transcript with a hole is worse than none.
fn join(parts: impl Iterator<Item = Result<String>>) -> Result<String> {
    let mut text = String::new();
    for part in parts {
        let part = part?;
        if part.is_empty() {
            continue;
        }
        if !text.is_empty() {
            text.push(' ');
        }
        text.push_str(&part);
    }
    Ok(text)
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
        self.loaded()?
            .session
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn long_form_models_get_the_whole_take() {
        assert_eq!(piece_limit(true), usize::MAX);
    }

    #[test]
    fn other_models_get_thirty_second_pieces() {
        assert_eq!(piece_limit(false), 30 * 16_000);
    }

    #[test]
    fn a_piece_gets_half_a_second_of_silence_on_each_side() {
        let out = padded(&[0.5, -0.5]);
        assert_eq!(out.len(), 8_000 + 2 + 8_000);
        assert!(out[..8_000].iter().all(|&s| s == 0.0));
        assert_eq!(&out[8_000..8_002], &[0.5, -0.5]);
        assert!(out[8_002..].iter().all(|&s| s == 0.0));
    }

    #[test]
    fn an_empty_piece_is_only_silence() {
        assert_eq!(padded(&[]).len(), 16_000);
    }

    #[test]
    fn join_puts_one_space_between_pieces_and_skips_empty_ones() {
        let parts = ["Hello", "", "world.", "  "].map(|s| Ok(s.trim().to_owned()));
        assert_eq!(join(parts.into_iter()).unwrap(), "Hello world.");
    }

    #[test]
    fn join_of_nothing_is_empty() {
        assert_eq!(join(std::iter::empty()).unwrap(), "");
    }

    #[test]
    fn join_fails_on_the_first_failed_piece() {
        let parts = vec![Ok("Hello".to_owned()), Err(anyhow!("boom")), Ok("late".to_owned())];
        let err = join(parts.into_iter()).unwrap_err();
        assert_eq!(err.to_string(), "boom");
    }

    #[test]
    fn transcribing_without_a_model_says_so() {
        let mut engine = Engine::new();
        let err = engine.transcribe(&Speech::from(vec![0.0; 16]), None).unwrap_err();
        assert_eq!(err.to_string(), "no model loaded");
    }

    #[test]
    fn a_failed_load_is_reported_by_name_until_the_next_load() {
        let mut engine = Engine::new();
        let missing = std::env::temp_dir().join("encre-missing.gguf");
        assert!(engine.load(&missing).is_err());

        let err = engine.transcribe(&Speech::from(vec![0.0; 16]), None).unwrap_err();
        assert!(err.to_string().contains("encre-missing.gguf"), "{err}");
        assert!(engine.resident().is_none());
    }
}

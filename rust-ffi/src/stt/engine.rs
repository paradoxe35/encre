use std::path::{Path, PathBuf};

use anyhow::{Result, anyhow};
use transcribe_cpp::{Feature, RunOptions, StreamOptions};

use super::audio::SAMPLE_RATE;
use super::speech::{Speech, halves};
use super::take::{Live, Recognizer};

/// Models without long-form support decode one window and quietly drop the rest
/// (Canary is built for clips under 40 s), so their takes are cut at pauses.
const PIECE_SECS: usize = 30;

/// A piece that starts or ends mid-word can decode to nothing; a little silence on
/// both sides keeps the decoder honest. One side alone is not enough.
const SILENCE_PAD_SECS: f32 = 0.5;

/// A truncated piece is halved and retried down to this length; shorter, its partial text is kept.
const MIN_RETRY_SECS: usize = 4;

/// Keeps the session resident between takes: loading costs seconds, a take costs
/// milliseconds.
#[derive(Default)]
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
        Self::default()
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
        let session = &mut loaded.session;
        let mut decode = |piece: &[f32]| run_piece(session, piece, &options);
        join(
            pieces
                .into_iter()
                .map(|piece| transcribe_piece(&mut decode, piece)),
        )
    }
}

enum Decoded {
    Text(String),
    /// The decode ran past the model's output budget, usually by repeating itself.
    Truncated(String),
}

fn run_piece(
    session: &mut transcribe_cpp::Session,
    piece: &[f32],
    options: &RunOptions,
) -> Result<Decoded> {
    match session.run(&padded(piece), options) {
        Ok(out) => Ok(Decoded::Text(out.text.trim().to_owned())),
        Err(error @ transcribe_cpp::Error::OutputTruncated { .. }) => {
            tracing::warn!(
                seconds = piece.len() / SAMPLE_RATE as usize,
                "a piece ran past the output budget"
            );
            let partial = error.partial().map(|t| t.text.trim().to_owned());
            Ok(Decoded::Truncated(partial.unwrap_or_default()))
        }
        Err(error) => Err(anyhow!("transcription failed: {error}")),
    }
}

/// A truncated piece is split at its quietest point and retried, as shorter audio fits the budget
/// and rarely loops; too short to split, it keeps the text it reached.
fn transcribe_piece(
    decode: &mut impl FnMut(&[f32]) -> Result<Decoded>,
    piece: &[f32],
) -> Result<String> {
    match decode(piece)? {
        Decoded::Text(text) => Ok(text),
        Decoded::Truncated(partial) if piece.len() < 2 * MIN_RETRY_SECS * SAMPLE_RATE as usize => {
            Ok(without_repetition(&partial))
        }
        Decoded::Truncated(_) => join(
            halves(piece)
                .into_iter()
                .map(|half| transcribe_piece(decode, half)),
        ),
    }
}

/// Drops a phrase repeated at the end of the text, keeping it once: a decoder stuck in a loop
/// says the same words until its budget runs out.
fn without_repetition(text: &str) -> String {
    let words: Vec<&str> = text.split_whitespace().collect();
    for phrase in 1..=words.len() / 3 {
        let end = &words[words.len() - phrase..];
        let repeats = words
            .rchunks_exact(phrase)
            .take_while(|chunk| *chunk == end)
            .count();
        if repeats >= 3 {
            return words[..words.len() - (repeats - 1) * phrase].join(" ");
        }
    }
    words.join(" ")
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

    fn seconds(secs: f32) -> Vec<f32> {
        vec![0.1; (secs * SAMPLE_RATE as f32) as usize]
    }

    #[test]
    fn a_truncated_piece_is_retried_in_halves_until_it_fits() {
        let mut decoded = Vec::new();
        let mut decode = |piece: &[f32]| {
            if piece.len() > 10 * SAMPLE_RATE as usize {
                return Ok(Decoded::Truncated("so so so so".into()));
            }
            decoded.push(piece.len());
            Ok(Decoded::Text(format!("part{}", decoded.len())))
        };

        let take = seconds(30.0);
        let text = transcribe_piece(&mut decode, &take).unwrap();
        let expected: Vec<String> = (1..=decoded.len()).map(|n| format!("part{n}")).collect();
        assert_eq!(text, expected.join(" "));
        assert!(decoded.len() > 1);
        assert!(decoded.iter().all(|&len| len <= 10 * SAMPLE_RATE as usize));
        assert_eq!(
            decoded.iter().sum::<usize>(),
            take.len(),
            "every sample is decoded once"
        );
    }

    #[test]
    fn a_short_truncated_piece_keeps_what_it_said_without_the_loop() {
        let mut decode = |_: &[f32]| {
            Ok(Decoded::Truncated(
                "what is the weather in paris paris paris paris".into(),
            ))
        };
        let text = transcribe_piece(&mut decode, &seconds(6.0)).unwrap();
        assert_eq!(text, "what is the weather in paris");
    }

    #[test]
    fn a_failed_piece_still_fails_the_take() {
        let mut decode = |_: &[f32]| Err(anyhow!("decoder crashed"));
        assert!(transcribe_piece(&mut decode, &seconds(30.0)).is_err());
    }

    #[test]
    fn only_a_phrase_repeated_three_times_or_more_is_a_loop() {
        assert_eq!(
            without_repetition("thank you thank you thank you thank you"),
            "thank you"
        );
        assert_eq!(
            without_repetition("it was very very good"),
            "it was very very good"
        );
        assert_eq!(without_repetition("one two three"), "one two three");
        assert_eq!(without_repetition(""), "");
    }

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
        let parts = vec![
            Ok("Hello".to_owned()),
            Err(anyhow!("boom")),
            Ok("late".to_owned()),
        ];
        let err = join(parts.into_iter()).unwrap_err();
        assert_eq!(err.to_string(), "boom");
    }

    #[test]
    fn transcribing_without_a_model_says_so() {
        let mut engine = Engine::new();
        let err = engine
            .transcribe(&Speech::from(vec![0.0; 16]), None)
            .unwrap_err();
        assert_eq!(err.to_string(), "no model loaded");
    }

    #[test]
    fn a_failed_load_is_reported_by_name_until_the_next_load() {
        let mut engine = Engine::new();
        let missing = std::env::temp_dir().join("encre-missing.gguf");
        assert!(engine.load(&missing).is_err());

        let err = engine
            .transcribe(&Speech::from(vec![0.0; 16]), None)
            .unwrap_err();
        assert!(err.to_string().contains("encre-missing.gguf"), "{err}");
        assert!(engine.resident().is_none());
    }
}

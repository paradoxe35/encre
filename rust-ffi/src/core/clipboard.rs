use anyhow::{Context, Result};
use arboard::{Clipboard, ImageData};
use parking_lot::Mutex;

#[cfg(target_os = "macos")]
use arboard::SetExtApple as _;
#[cfg(all(
    unix,
    not(any(target_os = "macos", target_os = "android", target_os = "emscripten"))
))]
use arboard::SetExtLinux as _;
#[cfg(windows)]
use arboard::SetExtWindows as _;

#[derive(Clone, Debug)]
enum Content {
    Text(String),
    Image(ImageData<'static>),
    /// Also covers unreadable content such as files; restoring it clears Encre's text.
    Nothing,
}

/// Text first: a spreadsheet or document copy also carries a picture of it.
fn content_of(text: Option<String>, image: impl FnOnce() -> Option<ImageData<'static>>) -> Content {
    match text {
        Some(text) if !text.is_empty() => Content::Text(text),
        _ => image().map_or(Content::Nothing, Content::Image),
    }
}

/// Anything on the clipboard that is not ours was copied by the user meanwhile and is left alone.
struct Borrow {
    saved: Content,
    /// What the capture copied and what Encre wrote.
    ours: Vec<String>,
}

impl Borrow {
    fn new(saved: Content) -> Self {
        Self {
            saved,
            ours: Vec::new(),
        }
    }

    /// Nothing is what Encre's clear leaves when no copy landed.
    fn holds_ours(&self, current: &Content) -> bool {
        match current {
            Content::Nothing => true,
            Content::Text(text) => self.ours.iter().any(|ours| ours == text),
            Content::Image(_) => false,
        }
    }

    /// Encre must overwrite to paste, so a copy made meanwhile becomes what is put back.
    fn before_write(&mut self, current: Content) {
        if !self.holds_ours(&current) {
            self.saved = current;
        }
    }

    fn take_restore(&mut self, current: &Content) -> Option<Content> {
        let restore = self.holds_ours(current).then(|| self.saved.clone());
        self.ours.clear();
        restore
    }
}

pub struct ClipboardManager {
    clipboard: Mutex<Clipboard>,
    borrow: Mutex<Borrow>,
}

impl ClipboardManager {
    pub fn new() -> Result<Self> {
        Ok(Self {
            clipboard: Mutex::new(Clipboard::new()?),
            borrow: Mutex::new(Borrow::new(Content::Nothing)),
        })
    }

    /// `None` covers both empty and image; neither is a failure.
    pub fn get_text(&self) -> Result<Option<String>> {
        match self.clipboard.lock().get_text() {
            Ok(text) => Ok(Some(text)),
            Err(arboard::Error::ContentNotAvailable) => Ok(None),
            Err(e) => Err(e).context("Failed to read the clipboard"),
        }
    }

    pub fn set_text(&self, text: String) -> Result<()> {
        let current = self.snapshot();
        self.borrow.lock().before_write(current);
        self.put(Content::Text(text.clone()))
            .context("Failed to set clipboard text")?;
        self.borrow.lock().ours.push(text);
        Ok(())
    }

    /// What the host's simulated copy produced, so restoring can tell it from a copy by the user.
    pub fn captured(&self, text: String) {
        self.borrow.lock().ours.push(text);
    }

    pub fn clear(&self) -> Result<()> {
        self.put(Content::Nothing)
            .context("Failed to clear clipboard")
    }

    pub fn save_clipboard(&self) {
        let saved = self.snapshot();
        *self.borrow.lock() = Borrow::new(saved);
    }

    pub fn restore_clipboard(&self) -> Result<()> {
        let current = self.snapshot();
        // Lock released before writing, which blocks on X11 while handing over the selection.
        let Some(saved) = self.borrow.lock().take_restore(&current) else {
            tracing::info!("something new was copied; leaving the clipboard as it is");
            return Ok(());
        };
        self.put(saved).context("Failed to restore the clipboard")
    }

    fn snapshot(&self) -> Content {
        let mut clipboard = self.clipboard.lock();
        let text = clipboard.get_text().ok();
        content_of(text, || clipboard.get_image().ok())
    }

    /// Kept out of clipboard history: Encre's text is transient and the restored content is already there.
    fn put(&self, content: Content) -> Result<(), arboard::Error> {
        let mut clipboard = self.clipboard.lock();
        match content {
            Content::Text(text) => clipboard.set().exclude_from_history().text(text),
            Content::Image(image) => clipboard.set().exclude_from_history().image(image),
            Content::Nothing => clipboard.clear(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn picture() -> ImageData<'static> {
        ImageData {
            width: 1,
            height: 1,
            bytes: vec![255, 0, 0, 255].into(),
        }
    }

    fn text(text: &str) -> Content {
        Content::Text(text.to_string())
    }

    fn is_text(content: Option<Content>, expected: &str) -> bool {
        matches!(content, Some(Content::Text(text)) if text == expected)
    }

    /// The state after save, clear, and a copy of the selection that landed.
    fn captured(saved: &str, selection: &str) -> Borrow {
        let mut borrow = Borrow::new(text(saved));
        borrow.ours.push(selection.to_string());
        borrow
    }

    fn pasted(borrow: &mut Borrow, current: Content, written: &str) {
        borrow.before_write(current);
        borrow.ours.push(written.to_string());
    }

    #[test]
    fn text_is_kept_as_text() {
        let content = content_of(Some("user's own text".to_string()), || {
            panic!("text was enough")
        });
        assert!(matches!(content, Content::Text(text) if text == "user's own text"));
    }

    #[test]
    fn whitespace_is_content() {
        let content = content_of(Some("  \n".to_string()), || None);
        assert!(matches!(content, Content::Text(text) if text == "  \n"));
    }

    #[test]
    fn an_image_is_kept_when_there_is_no_text() {
        let content = content_of(None, || Some(picture()));
        assert!(
            matches!(content, Content::Image(image) if image.width == 1 && image.bytes.len() == 4)
        );
    }

    #[test]
    fn what_cannot_be_read_back_is_nothing_to_put_back() {
        assert!(matches!(content_of(None, || None), Content::Nothing));
        assert!(matches!(
            content_of(Some(String::new()), || None),
            Content::Nothing
        ));
    }

    #[test]
    fn abandoning_puts_back_the_original_while_the_selection_is_on_the_clipboard() {
        let mut borrow = captured("original", "selection");
        assert!(is_text(borrow.take_restore(&text("selection")), "original"));
    }

    #[test]
    fn abandoning_puts_back_the_original_when_the_copy_never_landed() {
        let mut borrow = Borrow::new(text("original"));
        assert!(is_text(borrow.take_restore(&Content::Nothing), "original"));
    }

    #[test]
    fn a_copy_made_while_waiting_survives_abandoning() {
        let mut borrow = captured("original", "selection");
        assert!(borrow.take_restore(&text("https://example.com")).is_none());
    }

    #[test]
    fn an_image_copied_while_waiting_survives_abandoning() {
        let mut borrow = captured("original", "selection");
        assert!(borrow.take_restore(&Content::Image(picture())).is_none());
    }

    #[test]
    fn the_original_is_put_back_after_pasting() {
        let mut borrow = captured("original", "selection");
        pasted(&mut borrow, text("selection"), "revised");
        assert!(is_text(borrow.take_restore(&text("revised")), "original"));
    }

    #[test]
    fn the_original_is_put_back_when_encres_text_never_took() {
        let mut borrow = captured("original", "selection");
        pasted(&mut borrow, text("selection"), "revised");
        assert!(is_text(borrow.take_restore(&text("selection")), "original"));
    }

    #[test]
    fn a_copy_made_while_waiting_is_what_comes_back_after_pasting() {
        let mut borrow = captured("original", "selection");
        pasted(&mut borrow, text("https://example.com"), "revised");
        assert!(is_text(
            borrow.take_restore(&text("revised")),
            "https://example.com"
        ));
    }

    #[test]
    fn a_copy_made_after_pasting_is_never_overwritten() {
        let mut borrow = captured("original", "selection");
        pasted(&mut borrow, text("selection"), "revised");
        assert!(borrow.take_restore(&text("copied later")).is_none());
    }

    #[test]
    fn inserting_without_a_capture_puts_back_the_original() {
        let mut borrow = Borrow::new(text("original"));
        pasted(&mut borrow, text("original"), "dictated");
        assert!(is_text(borrow.take_restore(&text("dictated")), "original"));
    }

    #[test]
    fn a_second_restore_leaves_the_restored_clipboard_alone() {
        let mut borrow = captured("original", "selection");
        assert!(borrow.take_restore(&text("selection")).is_some());
        assert!(borrow.take_restore(&text("original")).is_none());
    }
}

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

struct Write {
    text: String,
    replaced: Option<String>,
}

/// Something new was copied since, so restoring would overwrite it.
fn still_borrowed(write: Option<&Write>, current: Option<&str>) -> bool {
    write.is_none_or(|write| {
        current == Some(write.text.as_str()) || current == write.replaced.as_deref()
    })
}

struct Borrow {
    saved: Content,
    write: Option<Write>,
}

pub struct ClipboardManager {
    clipboard: Mutex<Clipboard>,
    borrow: Mutex<Borrow>,
}

impl ClipboardManager {
    pub fn new() -> Result<Self> {
        Ok(Self {
            clipboard: Mutex::new(Clipboard::new()?),
            borrow: Mutex::new(Borrow {
                saved: Content::Nothing,
                write: None,
            }),
        })
    }

    /// `None` covers both empty and image; neither is a failure.
    pub fn get_text(&self) -> Option<String> {
        self.clipboard.lock().get_text().ok()
    }

    pub fn set_text(&self, text: String) -> Result<()> {
        let replaced = self.get_text();
        self.put(Content::Text(text.clone()))
            .context("Failed to set clipboard text")?;
        self.borrow.lock().write = Some(Write { text, replaced });
        Ok(())
    }

    pub fn clear(&self) -> Result<()> {
        self.put(Content::Nothing)
            .context("Failed to clear clipboard")?;
        self.borrow.lock().write = None;
        Ok(())
    }

    pub fn save_clipboard(&self) {
        let saved = {
            let mut clipboard = self.clipboard.lock();
            let text = clipboard.get_text().ok();
            content_of(text, || clipboard.get_image().ok())
        };
        *self.borrow.lock() = Borrow { saved, write: None };
    }

    pub fn restore_clipboard(&self) -> Result<()> {
        let current = self.get_text();
        // Lock released before writing, which blocks on X11 while handing over the selection.
        let saved = {
            let mut borrow = self.borrow.lock();
            let write = borrow.write.take();
            if !still_borrowed(write.as_ref(), current.as_deref()) {
                tracing::info!("something new was copied; leaving the clipboard as it is");
                return Ok(());
            }
            borrow.saved.clone()
        };
        self.put(saved).context("Failed to restore the clipboard")
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

    fn write(text: &str, replaced: Option<&str>) -> Write {
        Write {
            text: text.to_string(),
            replaced: replaced.map(str::to_string),
        }
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
    fn the_clipboard_is_put_back_while_it_holds_encres_text() {
        let pasted = write("revised", Some("selection"));
        assert!(still_borrowed(Some(&pasted), Some("revised")));
    }

    #[test]
    fn the_clipboard_is_put_back_when_encres_text_never_took() {
        let pasted = write("revised", Some("selection"));
        assert!(still_borrowed(Some(&pasted), Some("selection")));
    }

    #[test]
    fn a_new_copy_is_never_overwritten() {
        let pasted = write("revised", Some("selection"));
        assert!(!still_borrowed(Some(&pasted), Some("copied meanwhile")));
    }

    #[test]
    fn without_a_write_the_clipboard_is_always_put_back() {
        assert!(still_borrowed(None, Some("anything")));
        assert!(still_borrowed(None, None));
    }
}

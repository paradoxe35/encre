use anyhow::Result;
use arboard::Clipboard;
use parking_lot::Mutex;

#[derive(Debug, PartialEq, Eq)]
enum Saved {
    Text(String),
    /// Occupied by something this layer cannot round-trip, such as an image.
    Foreign,
    Empty,
}

/// Split out from [`ClipboardManager`] so the rule is testable without a display server.
fn classify(current: Option<String>) -> Saved {
    match current {
        Some(text) if text.is_empty() => Saved::Empty,
        Some(text) => Saved::Text(text),
        // Telling "empty" from "holds a PNG" needs a per-platform format query. Assuming occupied
        // is the safe way to be wrong: restore then clears instead of leaving pasted text behind.
        None => Saved::Foreign,
    }
}

fn restore_target(saved: &Saved) -> Option<String> {
    match saved {
        Saved::Text(text) => Some(text.clone()),
        Saved::Foreign | Saved::Empty => None,
    }
}

pub struct ClipboardManager {
    clipboard: Mutex<Clipboard>,
    saved: Mutex<Saved>,
}

impl ClipboardManager {
    pub fn new() -> Result<Self> {
        Ok(Self {
            clipboard: Mutex::new(Clipboard::new()?),
            saved: Mutex::new(Saved::Empty),
        })
    }

    /// `None` covers both "empty" and "holds an image"; neither is a failure.
    pub fn get_text(&self) -> Option<String> {
        self.clipboard.lock().get_text().ok()
    }

    pub fn set_text(&self, text: String) -> Result<()> {
        self.clipboard
            .lock()
            .set_text(text)
            .map_err(|e| anyhow::anyhow!("Failed to set clipboard text: {}", e))
    }

    pub fn clear(&self) -> Result<()> {
        self.clipboard
            .lock()
            .clear()
            .map_err(|e| anyhow::anyhow!("Failed to clear clipboard: {}", e))
    }

    pub fn save_clipboard(&self) {
        *self.saved.lock() = classify(self.get_text());
    }

    /// Clears when the original cannot be restored, so a borrow never becomes an overwrite.
    pub fn restore_clipboard(&self) -> Result<()> {
        // Lock released before set_text, which blocks on X11 while handing over the selection.
        let restore_to = restore_target(&self.saved.lock());
        match restore_to {
            Some(text) => self.set_text(text),
            None => self.clear(),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn text_is_saved_and_put_back() {
        let saved = classify(Some("user's own text".to_string()));
        assert_eq!(saved, Saved::Text("user's own text".to_string()));
        assert_eq!(restore_target(&saved), Some("user's own text".to_string()));
    }

    #[test]
    fn an_unreadable_clipboard_is_cleared_rather_than_left_holding_our_text() {
        let saved = classify(None);
        assert_eq!(saved, Saved::Foreign);
        assert_eq!(restore_target(&saved), None);
    }

    #[test]
    fn an_empty_clipboard_is_left_empty() {
        let saved = classify(Some(String::new()));
        assert_eq!(saved, Saved::Empty);
        assert_eq!(restore_target(&saved), None);
    }

    #[test]
    fn whitespace_is_content_and_is_preserved() {
        let saved = classify(Some("  \n".to_string()));
        assert_eq!(restore_target(&saved), Some("  \n".to_string()));
    }
}

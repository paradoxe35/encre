use std::path::PathBuf;

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
    Files(Vec<PathBuf>),
    Html {
        html: String,
        alt: Option<String>,
    },
    Text(String),
    Image(ImageData<'static>),
    /// Also covers unreadable content; restoring it clears Encre's text.
    Nothing,
}

/// A format the platform lacks or the clipboard does not hold reads as `None`.
trait Formats {
    fn files(&mut self) -> Option<Vec<PathBuf>>;
    fn html(&mut self) -> Option<String>;
    fn text(&mut self) -> Option<String>;
    fn image(&mut self) -> Option<ImageData<'static>>;
}

impl Formats for Clipboard {
    fn files(&mut self) -> Option<Vec<PathBuf>> {
        self.get().file_list().ok()
    }

    fn html(&mut self) -> Option<String> {
        self.get().html().ok()
    }

    fn text(&mut self) -> Option<String> {
        self.get_text().ok()
    }

    fn image(&mut self) -> Option<ImageData<'static>> {
        self.get_image().ok()
    }
}

/// GTK ends text/uri-list lines with CRLF, and arboard splits them on LF alone.
fn without_cr(path: PathBuf) -> PathBuf {
    match path.to_str() {
        Some(name) if name.ends_with('\r') => PathBuf::from(name.trim_end_matches('\r')),
        _ => path,
    }
}

/// Firefox on Windows adds a temporary file to a copied image; Explorer's file copies carry no picture.
/// Elsewhere a file manager's copy may carry the file's icon, so the files win there.
const IMAGE_BEATS_FILES: bool = cfg!(target_os = "windows");

/// Files first, since their names come along as text that pastes no file. HTML keeps its text as
/// the plain alternative. Text beats an image, since a document copy also carries a picture of it,
/// but an image beats HTML without text, which a browser adds to a copied image as a bare tag.
fn content_of(formats: &mut impl Formats) -> Content {
    content_by(formats, IMAGE_BEATS_FILES)
}

fn content_by(formats: &mut impl Formats, image_beats_files: bool) -> Content {
    if let Some(files) = formats.files().filter(|files| !files.is_empty()) {
        if image_beats_files && let Some(image) = formats.image() {
            return Content::Image(image);
        }
        return Content::Files(files.into_iter().map(without_cr).collect());
    }
    let text = formats.text().filter(|text| !text.is_empty());
    let html = formats.html().filter(|html| !html.is_empty());
    match (text, html) {
        (Some(text), Some(html)) => Content::Html {
            html,
            alt: Some(text),
        },
        (Some(text), None) => Content::Text(text),
        (None, html) => match formats.image() {
            Some(image) => Content::Image(image),
            None => html.map_or(Content::Nothing, |html| Content::Html { html, alt: None }),
        },
    }
}

/// Anything on the clipboard that is not ours was copied by the user meanwhile and is left alone.
struct Borrow {
    saved: Content,
    /// What the capture copied and what Encre wrote.
    ours: Vec<String>,
    /// From the clear until the capture is noted, whatever lands came from the capture's own copy.
    copying: bool,
    /// Files the capture copied along with their names, as a Finder copy does.
    copied_files: Option<Vec<PathBuf>>,
    /// A picture the capture's copy put up with its text, which on Windows is read before the text.
    copied_image: Option<ImageData<'static>>,
}

impl Borrow {
    fn new(saved: Content) -> Self {
        Self {
            saved,
            ours: Vec::new(),
            copying: false,
            copied_files: None,
            copied_image: None,
        }
    }

    fn captured(&mut self, text: String, current: Content) {
        self.ours.push(text);
        self.copying = false;
        match current {
            Content::Files(files) => self.copied_files = Some(files),
            Content::Image(image) => self.copied_image = Some(image),
            _ => {}
        }
    }

    /// Nothing is what Encre's clear leaves when no copy landed; a rich copy is ours by its text.
    fn holds_ours(&self, current: &Content) -> bool {
        self.copying
            || match current {
                Content::Nothing => true,
                Content::Text(text)
                | Content::Html {
                    alt: Some(text), ..
                } => self.ours.contains(text),
                Content::Files(files) => self.copied_files.as_ref() == Some(files),
                Content::Image(image) => self.copied_image.as_ref().is_some_and(|copied| {
                    copied.width == image.width
                        && copied.height == image.height
                        && copied.bytes == image.bytes
                }),
                Content::Html { alt: None, .. } => false,
            }
    }

    /// Encre must overwrite to paste, so a copy made meanwhile becomes what is put back.
    fn before_write(&mut self, current: Content) {
        if !self.holds_ours(&current) {
            self.saved = current;
        }
        self.copying = false;
    }

    fn take_restore(&mut self, current: &Content) -> Option<Content> {
        let restore = self.holds_ours(current).then(|| self.saved.clone());
        self.ours.clear();
        self.copying = false;
        self.copied_files = None;
        self.copied_image = None;
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
        let current = self.snapshot();
        self.borrow.lock().captured(text, current);
    }

    pub fn clear(&self) -> Result<()> {
        self.put(Content::Nothing)
            .context("Failed to clear clipboard")?;
        self.borrow.lock().copying = true;
        Ok(())
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
        content_of(&mut *self.clipboard.lock())
    }

    /// Kept out of clipboard history: Encre's text is transient and the restored content is already there.
    fn put(&self, content: Content) -> Result<(), arboard::Error> {
        let mut clipboard = self.clipboard.lock();
        match content {
            // A file deleted meanwhile cannot be listed; its path still beats Encre's text left behind.
            Content::Files(files) => clipboard
                .set()
                .exclude_from_history()
                .file_list(&files)
                .or_else(|_| {
                    let paths: Vec<_> = files.iter().map(|file| file.to_string_lossy()).collect();
                    clipboard
                        .set()
                        .exclude_from_history()
                        .text(paths.join("\n"))
                }),
            Content::Html { html, alt } => clipboard.set().exclude_from_history().html(html, alt),
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

    #[derive(Default)]
    struct Fake {
        files: Option<Vec<PathBuf>>,
        html: Option<String>,
        text: Option<String>,
        image: Option<ImageData<'static>>,
        image_read: bool,
    }

    impl Formats for Fake {
        fn files(&mut self) -> Option<Vec<PathBuf>> {
            self.files.clone()
        }

        fn html(&mut self) -> Option<String> {
            self.html.clone()
        }

        fn text(&mut self) -> Option<String> {
            self.text.clone()
        }

        fn image(&mut self) -> Option<ImageData<'static>> {
            self.image_read = true;
            self.image.clone()
        }
    }

    fn some(text: &str) -> Option<String> {
        Some(text.to_string())
    }

    fn files(paths: &[&str]) -> Vec<PathBuf> {
        paths.iter().map(PathBuf::from).collect()
    }

    fn rich(html: &str, alt: &str) -> Content {
        Content::Html {
            html: html.to_string(),
            alt: some(alt),
        }
    }

    #[test]
    fn text_is_kept_as_text() {
        let mut fake = Fake {
            text: some("user's own text"),
            image: Some(picture()),
            ..Fake::default()
        };
        let content = content_of(&mut fake);
        assert!(matches!(content, Content::Text(text) if text == "user's own text"));
        assert!(!fake.image_read, "text was enough");
    }

    #[test]
    fn a_browser_image_with_a_temporary_file_stays_an_image_where_that_happens() {
        let copied = || Fake {
            files: Some(files(&["/tmp/image.png"])),
            image: Some(picture()),
            ..Fake::default()
        };
        assert!(matches!(content_by(&mut copied(), true), Content::Image(_)));
        assert!(matches!(
            content_by(&mut copied(), false),
            Content::Files(_)
        ));

        let mut explorer = Fake {
            files: Some(files(&["/home/me/report.pdf"])),
            ..Fake::default()
        };
        assert!(matches!(content_by(&mut explorer, true), Content::Files(_)));
    }

    #[test]
    fn whitespace_is_content() {
        let mut fake = Fake {
            text: some("  \n"),
            ..Fake::default()
        };
        assert!(matches!(content_of(&mut fake), Content::Text(text) if text == "  \n"));
    }

    #[test]
    fn an_image_is_kept_when_there_is_no_text() {
        let mut fake = Fake {
            image: Some(picture()),
            ..Fake::default()
        };
        assert!(
            matches!(content_of(&mut fake), Content::Image(image) if image.width == 1 && image.bytes.len() == 4)
        );
    }

    #[test]
    fn what_cannot_be_read_back_is_nothing_to_put_back() {
        assert!(matches!(content_of(&mut Fake::default()), Content::Nothing));
        let mut empty = Fake {
            files: Some(Vec::new()),
            html: some(""),
            text: some(""),
            ..Fake::default()
        };
        assert!(matches!(content_of(&mut empty), Content::Nothing));
    }

    #[test]
    fn rich_text_keeps_its_html_and_its_plain_text() {
        let mut fake = Fake {
            html: some("<b>bold</b>"),
            text: some("bold"),
            image: Some(picture()),
            ..Fake::default()
        };
        let content = content_of(&mut fake);
        assert!(
            matches!(&content, Content::Html { html, alt: Some(alt) } if html == "<b>bold</b>" && alt == "bold"),
            "{content:?}"
        );
    }

    #[test]
    fn a_copied_image_beats_the_tag_a_browser_adds_to_it() {
        let mut fake = Fake {
            html: some("<img src=\"cat.png\">"),
            image: Some(picture()),
            ..Fake::default()
        };
        assert!(matches!(content_of(&mut fake), Content::Image(_)));

        let mut html_only = Fake {
            html: some("<hr>"),
            ..Fake::default()
        };
        assert!(matches!(
            content_of(&mut html_only),
            Content::Html { alt: None, .. }
        ));
    }

    #[test]
    fn copied_files_are_kept_as_files_rather_than_their_names() {
        let mut fake = Fake {
            files: Some(files(&["/home/me/report.pdf\r", "/home/me/notes.txt"])),
            text: some("report.pdf"),
            ..Fake::default()
        };
        let content = content_of(&mut fake);
        assert!(
            matches!(&content, Content::Files(paths) if *paths == files(&["/home/me/report.pdf", "/home/me/notes.txt"])),
            "{content:?}"
        );
    }

    #[test]
    fn a_picture_the_capture_copied_is_ours_and_another_is_not() {
        let mut borrow = Borrow::new(Content::Text("original".to_string()));
        borrow.copying = true;
        borrow.captured("selection".to_string(), Content::Image(picture()));
        assert!(borrow.holds_ours(&Content::Image(picture())));

        let other = ImageData {
            width: 2,
            height: 1,
            bytes: vec![0; 8].into(),
        };
        assert!(!borrow.holds_ours(&Content::Image(other)));
    }

    #[test]
    fn files_copied_while_waiting_survive_abandoning() {
        let mut borrow = captured("original", "selection");
        assert!(
            borrow
                .take_restore(&Content::Files(files(&["/a.txt"])))
                .is_none()
        );
    }

    #[test]
    fn files_copied_while_waiting_are_what_comes_back_after_pasting() {
        let mut borrow = captured("original", "selection");
        pasted(&mut borrow, Content::Files(files(&["/a.txt"])), "revised");
        let restored = borrow.take_restore(&text("revised"));
        assert!(
            matches!(&restored, Some(Content::Files(paths)) if *paths == files(&["/a.txt"])),
            "{restored:?}"
        );
    }

    #[test]
    fn rich_text_copied_while_waiting_survives_abandoning() {
        let mut borrow = captured("original", "selection");
        assert!(
            borrow
                .take_restore(&rich("<a href=\"x\">link</a>", "link"))
                .is_none()
        );
        let mut borrow = captured("original", "selection");
        let tag = Content::Html {
            html: "<hr>".to_string(),
            alt: None,
        };
        assert!(borrow.take_restore(&tag).is_none());
    }

    #[test]
    fn a_rich_selection_is_still_the_capture_and_the_original_comes_back() {
        let mut borrow = captured("original", "selection");
        pasted(
            &mut borrow,
            rich("<i>selection</i>", "selection"),
            "revised",
        );
        assert!(is_text(borrow.take_restore(&text("revised")), "original"));

        let mut borrow = captured("original", "selection");
        assert!(is_text(
            borrow.take_restore(&rich("<i>selection</i>", "selection")),
            "original"
        ));
    }

    #[test]
    fn rich_text_and_files_saved_are_what_comes_back() {
        let mut borrow = Borrow::new(rich("<b>kept</b>", "kept"));
        borrow.captured("selection".to_string(), text("selection"));
        pasted(&mut borrow, text("selection"), "revised");
        assert!(matches!(
            borrow.take_restore(&text("revised")),
            Some(Content::Html { html, alt: Some(alt) }) if html == "<b>kept</b>" && alt == "kept"
        ));

        let mut borrow = Borrow::new(Content::Files(files(&["/a.txt"])));
        pasted(&mut borrow, Content::Files(files(&["/a.txt"])), "dictated");
        assert!(matches!(
            borrow.take_restore(&text("dictated")),
            Some(Content::Files(paths)) if paths == files(&["/a.txt"])
        ));
    }

    /// Ctrl+C in Explorer with a file selected copies the file and no text, so the capture waits out.
    #[test]
    fn what_the_capture_copied_without_text_is_not_mistaken_for_the_users() {
        for landed in [
            Content::Files(files(&["/selected.txt"])),
            Content::Image(picture()),
            Content::Html {
                html: "<hr>".to_string(),
                alt: None,
            },
        ] {
            let mut borrow = Borrow::new(text("original"));
            borrow.copying = true;
            assert!(
                is_text(borrow.take_restore(&landed), "original"),
                "{landed:?}"
            );
        }
    }

    /// A Finder copy carries the files and their names; the names are what the capture sees.
    #[test]
    fn files_the_capture_copied_with_their_names_are_ours() {
        let mut borrow = Borrow::new(text("original"));
        borrow.copying = true;
        let landed = Content::Files(files(&["/Users/me/report.pdf"]));
        borrow.captured("report.pdf".to_string(), landed.clone());
        pasted(&mut borrow, landed, "revised.pdf");
        assert!(is_text(
            borrow.take_restore(&text("revised.pdf")),
            "original"
        ));

        let mut borrow = Borrow::new(text("original"));
        borrow.captured(
            "report.pdf".to_string(),
            Content::Files(files(&["/Users/me/report.pdf"])),
        );
        assert!(
            borrow
                .take_restore(&Content::Files(files(&["/Users/me/other.pdf"])))
                .is_none()
        );
    }

    #[test]
    fn a_copy_after_the_capture_was_noted_is_the_users() {
        let mut borrow = Borrow::new(text("original"));
        borrow.copying = true;
        borrow.captured("selection".to_string(), text("selection"));
        assert!(borrow.take_restore(&Content::Image(picture())).is_none());
    }

    #[test]
    fn writing_ends_the_capture_even_if_it_was_never_noted() {
        let mut borrow = Borrow::new(text("original"));
        borrow.copying = true;
        pasted(&mut borrow, text("selection"), "revised");
        assert!(borrow.take_restore(&Content::Image(picture())).is_none());
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

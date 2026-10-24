use anyhow::Result;
use tracing::debug;

#[cfg(not(target_os = "macos"))]
use enigo::{Direction, Enigo, Key, Keyboard, Settings};

pub struct KeySimulator {
    #[cfg(not(target_os = "macos"))]
    enigo: Enigo,
}

/// Lets chord ordering be tested without a keyboard.
#[cfg(not(target_os = "macos"))]
trait KeyEvents {
    fn event(&mut self, key: Key, direction: Direction) -> Result<()>;
}

#[cfg(not(target_os = "macos"))]
impl KeyEvents for Enigo {
    fn event(&mut self, key: Key, direction: Direction) -> Result<()> {
        self.key(key, direction)?;
        Ok(())
    }
}

/// Every key that went down comes back up even after a failure, or later keystrokes become shortcuts.
#[cfg(not(target_os = "macos"))]
fn press_chord(keyboard: &mut impl KeyEvents, keys: &[Key]) -> Result<()> {
    let mut outcome = Ok(());
    let mut held = 0;
    for &key in keys {
        match keyboard.event(key, Direction::Press) {
            Ok(()) => held += 1,
            Err(e) => {
                outcome = Err(e);
                break;
            }
        }
    }
    for &key in keys[..held].iter().rev() {
        if let Err(e) = keyboard.event(key, Direction::Release) {
            outcome = outcome.and(Err(e));
        }
    }
    outcome
}

/// Ctrl shortcuts match virtual keys, and a letter the layout lacks would go out as typed text.
#[cfg(target_os = "windows")]
const LETTERS: [Key; 3] = [Key::A, Key::C, Key::V];

#[cfg(all(not(target_os = "windows"), not(target_os = "macos")))]
const LETTERS: [Key; 3] = [Key::Unicode('a'), Key::Unicode('c'), Key::Unicode('v')];

#[cfg(not(target_os = "macos"))]
const SELECT_ALL: [Key; 2] = [Key::Control, LETTERS[0]];
#[cfg(not(target_os = "macos"))]
const COPY: [Key; 2] = [Key::Control, LETTERS[1]];
#[cfg(not(target_os = "macos"))]
const PASTE: [Key; 2] = [Key::Control, LETTERS[2]];
#[cfg(not(target_os = "macos"))]
const TERMINAL_PASTE: [Key; 3] = [Key::Control, Key::Shift, LETTERS[2]];

/// What each virtual keycode types with Command held, then bare.
#[cfg(any(target_os = "macos", test))]
type LayoutTable = Vec<[Option<char>; 2]>;

/// AppKit matches shortcuts against the letter typed, so Cmd+A goes to whichever key types "a".
#[cfg(any(target_os = "macos", test))]
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
struct Shortcuts {
    select_all: u16,
    copy: u16,
    paste: u16,
}

#[cfg(any(target_os = "macos", test))]
impl Shortcuts {
    const ANSI: Self = Self {
        select_all: 0x00,
        copy: 0x08,
        paste: 0x09,
    };

    /// Layouts in order of preference; none with Latin letters leaves the US keys.
    fn in_layouts(layouts: &[LayoutTable]) -> Self {
        let find = |letter, fallback| key_code_typing(letter, layouts).unwrap_or(fallback);
        Self {
            select_all: find('a', Self::ANSI.select_all),
            copy: find('c', Self::ANSI.copy),
            paste: find('v', Self::ANSI.paste),
        }
    }
}

#[cfg(any(target_os = "macos", test))]
fn key_code_typing(letter: char, layouts: &[LayoutTable]) -> Option<u16> {
    layouts
        .iter()
        .find_map(|table| {
            (0..2).find_map(|held| table.iter().position(|typed| typed[held] == Some(letter)))
        })
        .and_then(|code| u16::try_from(code).ok())
}

/// Lets the hotkey listener tell posted events from the user's keystrokes.
#[cfg(target_os = "macos")]
pub const SYNTHETIC_TAG: i64 = 0x454E_4352;

#[cfg(target_os = "macos")]
mod macos_native {
    use super::*;
    use parking_lot::Mutex;
    use std::ffi::{c_int, c_void};
    use std::ptr;
    use std::sync::mpsc::{SyncSender, sync_channel};
    use std::thread::sleep;
    use std::time::{Duration, Instant};

    /// Both sides: the flags say a modifier is down, never which side.
    const MODIFIER_KEYS: [u16; 8] = [
        0x37, // Command
        0x36, // Right Command
        0x38, // Shift
        0x3C, // Right Shift
        0x3A, // Option
        0x3D, // Right Option
        0x3B, // Control
        0x3E, // Right Control
    ];

    const FLAG_COMMAND: u64 = 1 << 20;
    const HID_EVENT_TAP: u32 = 0;
    const COMBINED_SESSION_STATE: i32 = 0;
    const HID_SYSTEM_STATE: i32 = 1;
    const EVENT_SOURCE_USER_DATA: u32 = 42;

    /// Long enough for a shortcut the user is still holding, short enough not to feel stuck.
    const RELEASE_TIMEOUT: Duration = Duration::from_millis(600);
    const POLL_INTERVAL: Duration = Duration::from_millis(15);
    const KEY_HOLD: Duration = Duration::from_millis(10);
    const LAYOUT_TIMEOUT: Duration = Duration::from_millis(500);

    const KEY_CODES: u16 = 128;
    const KEY_ACTION_DISPLAY: u16 = 3;
    /// Carbon's cmdKey shifted right by 8, as UCKeyTranslate wants modifiers.
    const COMMAND_HELD: u32 = 0x01;
    const NO_DEAD_KEYS: u32 = 1;

    static LAST_KNOWN: Mutex<Option<Shortcuts>> = Mutex::new(None);

    unsafe extern "C" {
        fn CGEventCreateKeyboardEvent(
            source: *mut c_void,
            virtual_key: u16,
            key_down: bool,
        ) -> *mut c_void;

        fn CGEventSetFlags(event: *mut c_void, flags: u64);
        fn CGEventSetIntegerValueField(event: *mut c_void, field: u32, value: i64);
        fn CGEventPost(tap: u32, event: *mut c_void);
        fn CGEventSourceKeyState(state_id: i32, key: u16) -> bool;
        fn CFRelease(cf: *mut c_void);

        fn TISCopyCurrentKeyboardLayoutInputSource() -> *mut c_void;
        fn TISCopyCurrentASCIICapableKeyboardLayoutInputSource() -> *mut c_void;
        fn TISGetInputSourceProperty(source: *mut c_void, key: *const c_void) -> *const c_void;
        static kTISPropertyUnicodeKeyLayoutData: *const c_void;
        fn CFDataGetBytePtr(data: *const c_void) -> *const u8;
        fn LMGetKbdType() -> u8;
        fn UCKeyTranslate(
            layout: *const u8,
            virtual_key: u16,
            action: u16,
            modifiers: u32,
            keyboard_type: u32,
            options: u32,
            dead_key_state: *mut u32,
            max_length: usize,
            length: *mut usize,
            chars: *mut u16,
        ) -> i32;

        fn pthread_main_np() -> c_int;
        static _dispatch_main_q: c_void;
        fn dispatch_async_f(
            queue: *const c_void,
            context: *mut c_void,
            work: extern "C" fn(*mut c_void),
        );
    }

    fn typed(layout: *const u8, key_code: u16, modifiers: u32) -> Option<char> {
        let mut dead_key_state = 0;
        let mut length = 0;
        let mut chars = [0u16; 4];
        let status = unsafe {
            UCKeyTranslate(
                layout,
                key_code,
                KEY_ACTION_DISPLAY,
                modifiers,
                LMGetKbdType().into(),
                NO_DEAD_KEYS,
                &mut dead_key_state,
                chars.len(),
                &mut length,
                chars.as_mut_ptr(),
            )
        };
        if status != 0 || length != 1 {
            return None;
        }
        char::from_u32(chars[0].into())
    }

    /// Takes ownership of a copied input source.
    fn layout_table(source: *mut c_void) -> Option<LayoutTable> {
        if source.is_null() {
            return None;
        }
        unsafe {
            let data = TISGetInputSourceProperty(source, kTISPropertyUnicodeKeyLayoutData);
            let layout = if data.is_null() {
                ptr::null()
            } else {
                CFDataGetBytePtr(data)
            };
            let table = (!layout.is_null()).then(|| {
                (0..KEY_CODES)
                    .map(|code| [typed(layout, code, COMMAND_HELD), typed(layout, code, 0)])
                    .collect()
            });
            CFRelease(source);
            table
        }
    }

    /// Non-Latin layouts take their shortcuts from the ASCII-capable one.
    fn read_layout() -> Shortcuts {
        let sources = unsafe {
            [
                TISCopyCurrentKeyboardLayoutInputSource(),
                TISCopyCurrentASCIICapableKeyboardLayoutInputSource(),
            ]
        };
        let layouts: Vec<LayoutTable> = sources.into_iter().filter_map(layout_table).collect();
        let shortcuts = Shortcuts::in_layouts(&layouts);
        *LAST_KNOWN.lock() = Some(shortcuts);
        shortcuts
    }

    extern "C" fn read_layout_for(context: *mut c_void) {
        let reply = unsafe { Box::from_raw(context as *mut SyncSender<Shortcuts>) };
        if let Ok(shortcuts) = std::panic::catch_unwind(read_layout) {
            let _ = reply.send(shortcuts);
        }
    }

    /// Text Input Sources must be asked on the main thread; while it is busy, the last layout seen stands.
    fn shortcuts() -> Result<Shortcuts> {
        if unsafe { pthread_main_np() } != 0 {
            return Ok(read_layout());
        }
        let (reply, answer) = sync_channel(1);
        unsafe {
            dispatch_async_f(
                &raw const _dispatch_main_q,
                Box::into_raw(Box::new(reply)) as *mut c_void,
                read_layout_for,
            );
        }
        answer
            .recv_timeout(LAYOUT_TIMEOUT)
            .ok()
            .or(*LAST_KNOWN.lock())
            .ok_or_else(|| anyhow::anyhow!("The keyboard layout could not be read"))
    }

    fn post(key_code: u16, key_down: bool, flags: u64) -> Result<()> {
        unsafe {
            let event = CGEventCreateKeyboardEvent(ptr::null_mut(), key_code, key_down);
            if event.is_null() {
                return Err(anyhow::anyhow!("Failed to create key event"));
            }
            CGEventSetFlags(event, flags);
            CGEventSetIntegerValueField(event, EVENT_SOURCE_USER_DATA, SYNTHETIC_TAG);
            CGEventPost(HID_EVENT_TAP, event);
            CFRelease(event);
        }
        Ok(())
    }

    /// Both tables: recent macOS can leave a held key out of the HID table.
    fn key_down(key: u16) -> bool {
        unsafe {
            CGEventSourceKeyState(HID_SYSTEM_STATE, key)
                || CGEventSourceKeyState(COMBINED_SESSION_STATE, key)
        }
    }

    fn any_modifier_held() -> bool {
        MODIFIER_KEYS.iter().any(|key| key_down(*key))
    }

    /// Hardware modifier state merges into posted events regardless of flags, so wait for real release.
    pub fn release_modifiers() -> Result<()> {
        debug!("Releasing held modifiers");
        for key in MODIFIER_KEYS {
            post(key, false, 0)?;
        }

        let deadline = Instant::now() + RELEASE_TIMEOUT;
        while any_modifier_held() {
            if Instant::now() >= deadline {
                debug!("A modifier is still held; simulating anyway");
                break;
            }
            sleep(POLL_INTERVAL);
        }
        Ok(())
    }

    fn command_combo(key: fn(Shortcuts) -> u16) -> Result<()> {
        let key_code = key(shortcuts()?);
        release_modifiers()?;
        post(key_code, true, FLAG_COMMAND)?;
        sleep(KEY_HOLD);
        post(key_code, false, FLAG_COMMAND)
    }

    pub fn select_all() -> Result<()> {
        command_combo(|keys| keys.select_all)
    }

    pub fn copy() -> Result<()> {
        command_combo(|keys| keys.copy)
    }

    pub fn paste() -> Result<()> {
        command_combo(|keys| keys.paste)
    }
}

impl KeySimulator {
    pub fn new() -> Result<Self> {
        Ok(Self {
            #[cfg(not(target_os = "macos"))]
            enigo: Enigo::new(&Settings::default())
                .map_err(|e| anyhow::anyhow!("Failed to initialize key simulator: {}", e))?,
        })
    }

    /// So Ctrl+A does not arrive as Ctrl+Alt+A.
    pub fn release_modifiers(&mut self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            macos_native::release_modifiers()
        }

        #[cfg(not(target_os = "macos"))]
        {
            debug!("Releasing held modifiers");
            // A modifier that was never down releases harmlessly.
            for key in [Key::Control, Key::Alt, Key::Shift, Key::Meta] {
                let _ = self.enigo.key(key, Direction::Release);
            }
            Ok(())
        }
    }

    #[cfg(not(target_os = "macos"))]
    fn chord(&mut self, keys: &[Key]) -> Result<()> {
        self.release_modifiers()?;
        press_chord(&mut self.enigo, keys)
    }

    pub fn select_all(&mut self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            macos_native::select_all()
        }

        #[cfg(not(target_os = "macos"))]
        {
            self.chord(&SELECT_ALL)
        }
    }

    pub fn copy(&mut self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            macos_native::copy()
        }

        #[cfg(not(target_os = "macos"))]
        {
            self.chord(&COPY)
        }
    }

    pub fn paste(&mut self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            macos_native::paste()
        }

        #[cfg(not(target_os = "macos"))]
        {
            self.chord(&PASTE)
        }
    }

    /// macOS terminals take the same Cmd+V as everything else.
    pub fn paste_terminal(&mut self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            macos_native::paste()
        }

        #[cfg(not(target_os = "macos"))]
        {
            self.chord(&TERMINAL_PASTE)
        }
    }
}

#[cfg(all(test, not(target_os = "macos")))]
mod tests {
    use super::*;

    #[derive(Default)]
    struct Recorder {
        events: Vec<(Key, Direction)>,
        refuses: Option<Key>,
    }

    impl KeyEvents for Recorder {
        fn event(&mut self, key: Key, direction: Direction) -> Result<()> {
            if direction == Direction::Press && self.refuses == Some(key) {
                anyhow::bail!("refused");
            }
            self.events.push((key, direction));
            Ok(())
        }
    }

    #[test]
    fn terminal_paste_releases_in_reverse_order() {
        let mut keyboard = Recorder::default();
        press_chord(&mut keyboard, &TERMINAL_PASTE).unwrap();

        assert_eq!(
            keyboard.events,
            vec![
                (Key::Control, Direction::Press),
                (Key::Shift, Direction::Press),
                (LETTERS[2], Direction::Press),
                (LETTERS[2], Direction::Release),
                (Key::Shift, Direction::Release),
                (Key::Control, Direction::Release),
            ]
        );
    }

    #[test]
    fn held_modifiers_come_up_when_the_letter_fails() {
        let mut keyboard = Recorder {
            refuses: Some(LETTERS[2]),
            ..Recorder::default()
        };
        let outcome = press_chord(&mut keyboard, &TERMINAL_PASTE);

        assert!(outcome.is_err());
        assert_eq!(
            keyboard.events,
            vec![
                (Key::Control, Direction::Press),
                (Key::Shift, Direction::Press),
                (Key::Shift, Direction::Release),
                (Key::Control, Direction::Release),
            ]
        );
    }

    #[cfg(target_os = "windows")]
    #[test]
    fn windows_never_types_a_shortcut_letter_as_text() {
        let chords: [&[Key]; 4] = [&SELECT_ALL, &COPY, &PASTE, &TERMINAL_PASTE];
        assert!(
            chords
                .iter()
                .flat_map(|chord| chord.iter())
                .all(|key| !matches!(key, Key::Unicode(_)))
        );
        assert_eq!(LETTERS, [Key::A, Key::C, Key::V]);
    }
}

#[cfg(test)]
mod layout_tests {
    use super::*;

    const QWERTY: &str = "asdfhgzxcv\0bqweryt";
    const AZERTY: &str = "qsdfhgwxcv\0baze";
    const RUSSIAN: &str = "фывапрячсм\0иййцук";

    fn layout(command: &str, bare: &str) -> LayoutTable {
        let typed = |keys: &str, code| keys.chars().nth(code).filter(|&c| c != '\0');
        (0..128)
            .map(|code| [typed(command, code), typed(bare, code)])
            .collect()
    }

    #[test]
    fn qwerty_keeps_the_us_keys() {
        assert_eq!(
            Shortcuts::in_layouts(&[layout(QWERTY, QWERTY)]),
            Shortcuts::ANSI
        );
    }

    #[test]
    fn azerty_selects_all_with_the_key_that_types_a() {
        assert_eq!(
            Shortcuts::in_layouts(&[layout(AZERTY, AZERTY)]),
            Shortcuts {
                select_all: 0x0C,
                ..Shortcuts::ANSI
            }
        );
    }

    #[test]
    fn dvorak_moves_copy_and_paste() {
        let mut dvorak = layout("", "");
        for (code, letter) in [
            (0x00, 'a'),
            (0x08, 'j'),
            (0x09, 'k'),
            (0x22, 'c'),
            (0x2F, 'v'),
        ] {
            dvorak[code] = [Some(letter); 2];
        }
        assert_eq!(
            Shortcuts::in_layouts(&[dvorak]),
            Shortcuts {
                select_all: 0x00,
                copy: 0x22,
                paste: 0x2F,
            }
        );
    }

    #[test]
    fn a_layout_that_turns_qwerty_under_command_follows_command() {
        let mut dvorak_qwerty = layout(QWERTY, "");
        dvorak_qwerty[0x22][1] = Some('c');
        dvorak_qwerty[0x2F][1] = Some('v');
        assert_eq!(Shortcuts::in_layouts(&[dvorak_qwerty]), Shortcuts::ANSI);
    }

    #[test]
    fn a_non_latin_layout_defers_to_the_ascii_capable_one() {
        let layouts = [layout(RUSSIAN, RUSSIAN), layout(AZERTY, AZERTY)];
        assert_eq!(Shortcuts::in_layouts(&layouts).select_all, 0x0C);
    }

    #[test]
    fn no_latin_letters_anywhere_leaves_the_us_keys() {
        assert_eq!(
            Shortcuts::in_layouts(&[layout(RUSSIAN, RUSSIAN)]),
            Shortcuts::ANSI
        );
        assert_eq!(Shortcuts::in_layouts(&[]), Shortcuts::ANSI);
    }
}

use anyhow::Result;
use tracing::debug;

#[cfg(not(target_os = "macos"))]
use enigo::{Direction, Enigo, Key, Keyboard, Settings};

pub struct KeySimulator {
    #[cfg(not(target_os = "macos"))]
    enigo: Enigo,
}

/// The one call a chord needs from the keyboard, so the ordering can be checked without one.
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

/// Presses the keys in order and releases them in reverse. Every key that went down comes
/// back up even after a failure, or every keystroke that follows is a shortcut.
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

/// Stamped on every event the simulator posts, so the hotkey listener can tell them from the
/// user's own keystrokes.
#[cfg(target_os = "macos")]
pub const SYNTHETIC_TAG: i64 = 0x454E_4352;

#[cfg(target_os = "macos")]
mod macos_native {
    use super::*;
    use std::ffi::c_void;
    use std::ptr;
    use std::thread::sleep;
    use std::time::{Duration, Instant};

    const KEY_A: u16 = 0x00;
    const KEY_C: u16 = 0x08;
    const KEY_V: u16 = 0x09;

    /// Left and right of each modifier: the flags say a modifier is down, never which side.
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

    /// Hardware modifier state merges into posted events regardless of their flags, so a stray
    /// Cmd+A is prevented by releasing and polling real key state, not by masking flags.
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

    fn command_combo(key_code: u16) -> Result<()> {
        release_modifiers()?;
        post(key_code, true, FLAG_COMMAND)?;
        sleep(KEY_HOLD);
        post(key_code, false, FLAG_COMMAND)
    }

    pub fn select_all() -> Result<()> {
        command_combo(KEY_A)
    }

    pub fn copy() -> Result<()> {
        command_combo(KEY_C)
    }

    pub fn paste() -> Result<()> {
        command_combo(KEY_V)
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

    /// Drops modifiers the triggering hotkey left down, so Ctrl+A does not arrive as Ctrl+Alt+A.
    pub fn release_modifiers(&mut self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            macos_native::release_modifiers()
        }

        #[cfg(not(target_os = "macos"))]
        {
            debug!("Releasing held modifiers");
            // Best effort; a modifier that was never down releases harmlessly.
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
            self.chord(&[Key::Control, Key::Unicode('a')])
        }
    }

    pub fn copy(&mut self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            macos_native::copy()
        }

        #[cfg(not(target_os = "macos"))]
        {
            self.chord(&[Key::Control, Key::Unicode('c')])
        }
    }

    pub fn paste(&mut self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            macos_native::paste()
        }

        #[cfg(not(target_os = "macos"))]
        {
            self.chord(&[Key::Control, Key::Unicode('v')])
        }
    }

    /// Terminals bind Ctrl+Shift+V; macOS terminals take the same Cmd+V as everything else.
    pub fn paste_terminal(&mut self) -> Result<()> {
        #[cfg(target_os = "macos")]
        {
            macos_native::paste()
        }

        #[cfg(not(target_os = "macos"))]
        {
            self.chord(&[Key::Control, Key::Shift, Key::Unicode('v')])
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
        press_chord(
            &mut keyboard,
            &[Key::Control, Key::Shift, Key::Unicode('v')],
        )
        .unwrap();

        assert_eq!(
            keyboard.events,
            vec![
                (Key::Control, Direction::Press),
                (Key::Shift, Direction::Press),
                (Key::Unicode('v'), Direction::Press),
                (Key::Unicode('v'), Direction::Release),
                (Key::Shift, Direction::Release),
                (Key::Control, Direction::Release),
            ]
        );
    }

    #[test]
    fn held_modifiers_come_up_when_the_letter_fails() {
        let mut keyboard = Recorder {
            refuses: Some(Key::Unicode('v')),
            ..Recorder::default()
        };
        let outcome = press_chord(
            &mut keyboard,
            &[Key::Control, Key::Shift, Key::Unicode('v')],
        );

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
}

use std::ffi::CString;
use std::os::raw::{c_char, c_int};
use std::sync::Arc;
use std::thread;

use parking_lot::Mutex;
#[cfg(not(target_os = "macos"))]
use rdev::Event;
use rdev::{EventType, Key};

// Linux listens through X11, which on Wayland means XWayland; macOS uses tap_macos.
#[cfg(target_os = "windows")]
use rdev::listen;
#[cfg(target_os = "linux")]
use rdev::{listen, start_grab_listen};

#[cfg(target_os = "macos")]
use super::tap_macos::{self, TapKind};

use super::ffi_types::*;

#[cfg(target_os = "linux")]
fn is_wayland() -> bool {
    if let Ok(session_type) = std::env::var("XDG_SESSION_TYPE") {
        match session_type.to_lowercase().as_str() {
            "wayland" => return true,
            "x11" => return false,
            _ => {}
        }
    }

    std::env::var("WAYLAND_DISPLAY").is_ok()
}

#[cfg(target_os = "linux")]
fn has_x_display() -> bool {
    std::env::var_os("DISPLAY").is_some_and(|display| !display.is_empty())
}

/// rdev listens through X11 only, and its grab reports success even with no X server to grab from.
#[cfg(target_os = "linux")]
fn x11_problem(wayland: bool, display: bool) -> Option<&'static str> {
    if !display {
        Some(
            "Global hotkeys need an X11 session, or XWayland with DISPLAY set, and no X display \
             was found. Log in to an X11 session, or start Encre with DISPLAY set.",
        )
    } else if wayland {
        Some(
            "Global hotkeys need an X11 session, or XWayland with DISPLAY set. On Wayland they \
             only fire while an XWayland window has focus; log in to an X11 session for hotkeys \
             that work everywhere.",
        )
    } else {
        None
    }
}

pub type HotkeyCallback = extern "C" fn(*const c_char);

/// Receives the action string and 1 on key down, 0 on key up.
pub type PttCallback = extern "C" fn(*const c_char, c_int);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum Modifier {
    Ctrl,
    Alt,
    Shift,
    Meta,
}

impl Modifier {
    fn from_name(name: &str) -> Option<Self> {
        match name {
            "ctrl" | "control" => Some(Self::Ctrl),
            "alt" | "option" => Some(Self::Alt),
            "shift" => Some(Self::Shift),
            "meta" | "cmd" | "super" | "win" => Some(Self::Meta),
            _ => None,
        }
    }

    fn from_key(key: &Key) -> Option<Self> {
        match key {
            Key::ControlLeft | Key::ControlRight => Some(Self::Ctrl),
            Key::Alt | Key::AltGr => Some(Self::Alt),
            Key::ShiftLeft | Key::ShiftRight => Some(Self::Shift),
            Key::MetaLeft | Key::MetaRight => Some(Self::Meta),
            _ => None,
        }
    }
}

/// Resolved at registration since matching runs inside the system's event callback.
#[derive(Debug, Default, Clone, Copy, PartialEq, Eq)]
struct Modifiers {
    ctrl: bool,
    alt: bool,
    shift: bool,
    meta: bool,
}

impl Modifiers {
    fn is_empty(self) -> bool {
        !self.ctrl && !self.alt && !self.shift && !self.meta
    }

    fn get(self, modifier: Modifier) -> bool {
        match modifier {
            Modifier::Ctrl => self.ctrl,
            Modifier::Alt => self.alt,
            Modifier::Shift => self.shift,
            Modifier::Meta => self.meta,
        }
    }

    fn set(&mut self, modifier: Modifier, down: bool) {
        match modifier {
            Modifier::Ctrl => self.ctrl = down,
            Modifier::Alt => self.alt = down,
            Modifier::Shift => self.shift = down,
            Modifier::Meta => self.meta = down,
        }
    }

    fn merged(self, other: Self) -> Self {
        Self {
            ctrl: self.ctrl || other.ctrl,
            alt: self.alt || other.alt,
            shift: self.shift || other.shift,
            meta: self.meta || other.meta,
        }
    }

    #[cfg(any(test, target_os = "macos"))]
    fn from_flags(flags: u64) -> Self {
        Self {
            shift: flags & (1 << 17) != 0,
            ctrl: flags & (1 << 18) != 0,
            alt: flags & (1 << 19) != 0,
            meta: flags & (1 << 20) != 0,
        }
    }
}

const MODIFIERS: [Modifier; 4] = [
    Modifier::Ctrl,
    Modifier::Alt,
    Modifier::Shift,
    Modifier::Meta,
];

/// The hook never sees a release made on the secure desktop or in an elevated window. A failed
/// query reads as up, which can stop a hotkey but never fire one.
#[cfg(target_os = "windows")]
fn modifier_is_down(modifier: Modifier) -> bool {
    #[link(name = "user32")]
    unsafe extern "system" {
        safe fn GetAsyncKeyState(key: i32) -> i16;
    }
    // VK_LCONTROL, VK_LMENU, VK_LSHIFT and VK_LWIN, each followed by its right-hand twin.
    let keys = match modifier {
        Modifier::Ctrl => [0xA2, 0xA3],
        Modifier::Alt => [0xA4, 0xA5],
        Modifier::Shift => [0xA0, 0xA1],
        Modifier::Meta => [0x5B, 0x5C],
    };
    keys.into_iter().any(|key| GetAsyncKeyState(key) < 0)
}

/// One table both ways, so a name the listener cannot match fails at registration.
const KEYS: &[(Key, &str)] = &[
    (Key::KeyA, "a"),
    (Key::KeyB, "b"),
    (Key::KeyC, "c"),
    (Key::KeyD, "d"),
    (Key::KeyE, "e"),
    (Key::KeyF, "f"),
    (Key::KeyG, "g"),
    (Key::KeyH, "h"),
    (Key::KeyI, "i"),
    (Key::KeyJ, "j"),
    (Key::KeyK, "k"),
    (Key::KeyL, "l"),
    (Key::KeyM, "m"),
    (Key::KeyN, "n"),
    (Key::KeyO, "o"),
    (Key::KeyP, "p"),
    (Key::KeyQ, "q"),
    (Key::KeyR, "r"),
    (Key::KeyS, "s"),
    (Key::KeyT, "t"),
    (Key::KeyU, "u"),
    (Key::KeyV, "v"),
    (Key::KeyW, "w"),
    (Key::KeyX, "x"),
    (Key::KeyY, "y"),
    (Key::KeyZ, "z"),
    (Key::Num0, "0"),
    (Key::Num1, "1"),
    (Key::Num2, "2"),
    (Key::Num3, "3"),
    (Key::Num4, "4"),
    (Key::Num5, "5"),
    (Key::Num6, "6"),
    (Key::Num7, "7"),
    (Key::Num8, "8"),
    (Key::Num9, "9"),
    // The recorder names a numpad digit like the top row's, so either key fires the binding.
    (Key::Kp0, "0"),
    (Key::Kp1, "1"),
    (Key::Kp2, "2"),
    (Key::Kp3, "3"),
    (Key::Kp4, "4"),
    (Key::Kp5, "5"),
    (Key::Kp6, "6"),
    (Key::Kp7, "7"),
    (Key::Kp8, "8"),
    (Key::Kp9, "9"),
    (Key::Space, "space"),
    (Key::Return, "return"),
    (Key::KpReturn, "return"),
    (Key::Escape, "escape"),
    (Key::Tab, "tab"),
    (Key::Backspace, "backspace"),
    (Key::Delete, "delete"),
    (Key::Insert, "insert"),
    (Key::Home, "home"),
    (Key::End, "end"),
    (Key::PageUp, "pageup"),
    (Key::PageDown, "pagedown"),
    (Key::UpArrow, "up"),
    (Key::DownArrow, "down"),
    (Key::LeftArrow, "left"),
    (Key::RightArrow, "right"),
    (Key::F1, "f1"),
    (Key::F2, "f2"),
    (Key::F3, "f3"),
    (Key::F4, "f4"),
    (Key::F5, "f5"),
    (Key::F6, "f6"),
    (Key::F7, "f7"),
    (Key::F8, "f8"),
    (Key::F9, "f9"),
    (Key::F10, "f10"),
    (Key::F11, "f11"),
    (Key::F12, "f12"),
];

fn key_name(key: &Key) -> Option<&'static str> {
    KEYS.iter()
        .find(|(known, _)| known == key)
        .map(|(_, name)| *name)
}

fn canonical_key_name(name: &str) -> Option<&'static str> {
    KEYS.iter()
        .find(|(_, known)| *known == name)
        .map(|(_, name)| *name)
}

/// Bound only while Encre shows something Esc can close.
const STANDALONE_KEY: &str = "escape";

/// A modifier is required, or the binding would fire on ordinary typing.
fn parse_binding(binding: &str) -> Result<(Modifiers, Option<&'static str>), String> {
    if binding.trim().is_empty() {
        return Err("binding is empty".to_string());
    }
    // An empty part is a key the recorder could not name, not one to drop: "ctrl+alt+" is not ctrl+alt.
    let parts: Vec<&str> = binding.split('+').map(str::trim).collect();
    if parts.iter().any(|part| part.is_empty()) {
        return Err(format!("'{binding}' has a key with no name"));
    }
    let Some((last, leading)) = parts.split_last() else {
        return Err("binding is empty".to_string());
    };

    let mut modifiers = Modifiers::default();
    for part in leading {
        let name = part.to_lowercase();
        let modifier =
            Modifier::from_name(&name).ok_or_else(|| format!("'{}' is not a modifier", part))?;
        modifiers.set(modifier, true);
    }

    let name = last.to_lowercase();
    let key = match Modifier::from_name(&name) {
        Some(modifier) => {
            modifiers.set(modifier, true);
            None
        }
        None => Some(canonical_key_name(&name).ok_or_else(|| format!("unknown key '{}'", last))?),
    };

    if modifiers.is_empty() && key != Some(STANDALONE_KEY) {
        return Err("binding needs a modifier".to_string());
    }
    Ok((modifiers, key))
}

enum Trigger {
    Tap(HotkeyCallback),
    Hold(PttCallback),
}

struct HotkeyBinding {
    binding: String,
    action: String,
    trigger: Trigger,
    modifiers: Modifiers,
    key: Option<&'static str>,
}

impl HotkeyBinding {
    fn is_hold(&self) -> bool {
        matches!(self.trigger, Trigger::Hold(_))
    }
}

fn fire(binding: &HotkeyBinding, down: bool) {
    tracing::debug!(
        "Hotkey {} : {} (action: {})",
        if down { "down" } else { "up" },
        binding.binding,
        binding.action
    );
    // Lent via `as_ptr`, not transferred; the host must copy the string during the call.
    let Ok(action) = CString::new(binding.action.as_str()) else {
        return;
    };
    match binding.trigger {
        Trigger::Tap(callback) => {
            if down {
                callback(action.as_ptr())
            }
        }
        Trigger::Hold(callback) => callback(action.as_ptr(), c_int::from(down)),
    }
}

/// Modifier-only bindings fire on release so `ctrl+cmd` does not fire on the way to `ctrl+cmd+space`.
#[derive(Default)]
struct ListenerState {
    held: Modifiers,
    /// The largest modifier set held since the last time every modifier was up.
    chord: Modifiers,
    interrupted: bool,
    /// Tracked by key, not name, so auto-repeat is not read as a second press and numpad 1 and the
    /// top-row 1, which share a name, do not end each other's hold.
    held_key: Option<Key>,
    holding: Option<(Key, &'static str)>,
    /// Windows types AltGr as a made-up left Ctrl then right Alt, which must not read as Ctrl+Alt.
    /// The made-up Ctrl is kept out of `held`; a right Alt while it is down is typing, not Alt.
    fake_ctrl: bool,
    /// Set while an AltGr press is being read as typing, so its release is ignored too.
    typing_altgr: bool,
    delivery_announced: bool,
    unmatched_announced: Vec<&'static str>,
    /// Asked before each key event, for a listener that can miss a modifier's release.
    modifier_is_down: Option<fn(Modifier) -> bool>,
}

impl ListenerState {
    fn process(&mut self, event: EventType, bindings: &Mutex<Vec<HotkeyBinding>>) {
        match event {
            EventType::KeyPress(key) => {
                self.drop_released();
                self.on_press(key, bindings)
            }
            EventType::KeyRelease(key) => {
                self.drop_released();
                self.on_release(key, bindings)
            }
            EventType::ButtonPress(_) => self.interrupted = true,
            _ => {}
        }
    }

    /// Dropped without firing, as the release happened long ago. The hook runs before the system
    /// records its own key, so a modifier being pressed reads up and one being released reads down.
    fn drop_released(&mut self) {
        let Some(is_down) = self.modifier_is_down else {
            return;
        };
        // The made-up Ctrl's release may be lost like any other, and would make every right Alt typing.
        if self.fake_ctrl && !is_down(Modifier::Ctrl) {
            self.fake_ctrl = false;
        }
        for modifier in MODIFIERS {
            if self.held.get(modifier) && !is_down(modifier) {
                self.held.set(modifier, false);
            }
        }
    }

    /// Follows each event's flags, not edges, which our own posted key-ups would skew.
    #[cfg(any(test, target_os = "macos"))]
    fn observe(
        &mut self,
        now: Modifiers,
        event: Option<EventType>,
        bindings: &Mutex<Vec<HotkeyBinding>>,
    ) {
        for modifier in MODIFIERS {
            if self.held.get(modifier) && !now.get(modifier) {
                self.modifier_up(modifier, bindings);
            }
        }
        for modifier in MODIFIERS {
            if !self.held.get(modifier) && now.get(modifier) {
                self.modifier_down(modifier);
            }
        }
        if let Some(event) = event {
            self.process(event, bindings);
        }
    }

    fn modifier_down(&mut self, modifier: Modifier) {
        if self.held.get(modifier) {
            return;
        }
        if self.held.is_empty() {
            self.chord = Modifiers::default();
            self.interrupted = false;
        }
        self.held.set(modifier, true);
        self.chord = self.chord.merged(self.held);
    }

    fn modifier_up(&mut self, modifier: Modifier, bindings: &Mutex<Vec<HotkeyBinding>>) {
        if !self.held.get(modifier) {
            return;
        }
        let before = self.held;
        self.held.set(modifier, false);

        // Releasing the remaining modifiers must not fire again.
        if !self.interrupted && before == self.chord {
            for binding in bindings.lock().iter() {
                if binding.key.is_none() && binding.modifiers == before {
                    fire(binding, true);
                }
            }
        }
    }

    fn on_press(&mut self, key: Key, bindings: &Mutex<Vec<HotkeyBinding>>) {
        if key == Key::AltGr && self.fake_ctrl {
            self.typing_altgr = true;
            self.interrupted = true;
            return;
        }
        if let Some(modifier) = Modifier::from_key(&key) {
            self.modifier_down(modifier);
            return;
        }

        let Some(name) = key_name(&key) else {
            self.interrupted = true;
            return;
        };
        // Auto-repeat, or a second key of the same name (numpad and top-row 1) while the first is down.
        if self.held_key.and_then(|held| key_name(&held)) == Some(name) {
            return;
        }
        self.held_key = Some(key);
        self.interrupted = true;

        // Logged once, without naming the key, to prove key events arrive at all.
        if !self.delivery_announced {
            self.delivery_announced = true;
            tracing::info!("The system is delivering key events to Encre");
        }

        let bindings = bindings.lock();
        let mut fired = false;
        for binding in bindings.iter() {
            if binding.key == Some(name) && binding.modifiers == self.held {
                fire(binding, true);
                fired = true;
                if binding.is_hold() {
                    self.holding = Some((key, name));
                }
            }
        }
        if !fired {
            self.note_unmatched(name, &bindings);
        }
    }

    fn on_release(&mut self, key: Key, bindings: &Mutex<Vec<HotkeyBinding>>) {
        if key == Key::AltGr && self.typing_altgr {
            self.typing_altgr = false;
            return;
        }
        let Some(modifier) = Modifier::from_key(&key) else {
            if self.held_key == Some(key) {
                self.held_key = None;
            }
            // A modifier released first is a slipped finger, not the end of the hold.
            if let Some((held, name)) = self.holding
                && held == key
            {
                self.holding = None;
                for binding in bindings.lock().iter() {
                    if binding.is_hold() && binding.key == Some(name) {
                        fire(binding, false);
                    }
                }
            }
            return;
        };
        self.modifier_up(modifier, bindings);
    }

    /// Tells a wrong binding apart from a listener the system never delivers to.
    fn note_unmatched(&mut self, name: &'static str, bindings: &[HotkeyBinding]) {
        if self.held.is_empty() || self.unmatched_announced.contains(&name) {
            return;
        }
        if !bindings.iter().any(|binding| binding.key == Some(name)) {
            return;
        }
        self.unmatched_announced.push(name);
        tracing::info!(
            "Saw {} with ctrl={} alt={} shift={} meta={}, which matched no binding",
            name,
            self.held.ctrl,
            self.held.alt,
            self.held.shift,
            self.held.meta
        );
    }
}

#[derive(Default)]
pub struct SimpleHotkeyManager {
    bindings: Arc<Mutex<Vec<HotkeyBinding>>>,
    listener_handle: Option<thread::JoinHandle<()>>,
    active: Arc<Mutex<bool>>,
    /// The listener fails on its own thread and a macOS .app discards stdout.
    listen_error: Arc<Mutex<Option<String>>>,
}

impl SimpleHotkeyManager {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn clear_bindings(&mut self) {
        self.bindings.lock().clear();
        tracing::info!("Cleared all hotkey bindings");
    }

    pub fn unregister(&mut self, action: &str) {
        self.bindings
            .lock()
            .retain(|binding| binding.action != action);
    }

    pub fn register(
        &mut self,
        binding: String,
        action: String,
        callback: HotkeyCallback,
    ) -> Result<(), String> {
        self.push(binding, action, Trigger::Tap(callback))
    }

    pub fn register_hold(
        &mut self,
        binding: String,
        action: String,
        callback: PttCallback,
    ) -> Result<(), String> {
        self.push(binding, action, Trigger::Hold(callback))
    }

    fn push(&mut self, binding: String, action: String, trigger: Trigger) -> Result<(), String> {
        let (modifiers, key) = parse_binding(&binding)?;
        if key.is_none() && matches!(trigger, Trigger::Hold(_)) {
            return Err(format!(
                "{binding} is modifiers only, which cannot be held for push-to-talk"
            ));
        }
        tracing::info!("Registered hotkey: {} (action: {})", binding, action);
        self.bindings.lock().push(HotkeyBinding {
            binding,
            action,
            trigger,
            modifiers,
            key,
        });
        Ok(())
    }

    pub fn start(&mut self) -> Result<(), String> {
        let mut active = self.active.lock();
        if *active {
            return Ok(());
        }
        *active = true;
        drop(active);

        // rdev cannot stop a listener; a second thread would fire every action twice.
        if self.listener_handle.is_some() {
            return Ok(());
        }

        let bindings = self.bindings.clone();
        let active_flag = self.active.clone();
        let listen_error = self.listen_error.clone();

        let handle = thread::spawn(move || {
            let mut state = ListenerState::default();
            #[cfg(target_os = "windows")]
            {
                state.modifier_is_down = Some(modifier_is_down);
            }

            #[cfg(target_os = "macos")]
            {
                let result = tap_macos::listen(move |tap| {
                    // A panic must not unwind into the system's callback.
                    let _ = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                        if tap.synthetic || !*active_flag.lock() {
                            return;
                        }
                        let key = rdev::key_from_code(tap.keycode);
                        let event = match tap.kind {
                            TapKind::FlagsChanged => None,
                            TapKind::KeyDown => Some(EventType::KeyPress(key)),
                            TapKind::KeyUp => Some(EventType::KeyRelease(key)),
                            TapKind::MouseDown => Some(EventType::ButtonPress(rdev::Button::Left)),
                        };
                        state.observe(Modifiers::from_flags(tap.flags), event, &bindings);
                    }));
                });
                if let Err(e) = result {
                    *listen_error.lock() = Some(format!(
                        "The system refused to let Encre listen for hotkeys ({e}). Grant Encre \
                         Input Monitoring, then restart it."
                    ));
                }
            }

            #[cfg(not(target_os = "macos"))]
            {
                let mut dispatch = move |event: &Event| {
                    let _ = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                        if !*active_flag.lock() {
                            return;
                        }
                        // AltGr's made-up left Ctrl carries scan code 0x21D, a real one 0x1D.
                        #[cfg(target_os = "windows")]
                        if let EventType::KeyPress(Key::ControlLeft)
                        | EventType::KeyRelease(Key::ControlLeft) = event.event_type
                            && event.position_code & 0x200 != 0
                        {
                            state.fake_ctrl = matches!(event.event_type, EventType::KeyPress(_));
                            return;
                        }
                        state.process(event.event_type, &bindings);
                    }));
                };

                #[cfg(target_os = "linux")]
                {
                    let wayland = is_wayland();
                    let display = has_x_display();
                    if let Some(problem) = x11_problem(wayland, display) {
                        tracing::warn!("{problem}");
                        // XWayland still delivers some keys, so only a missing display is a failure.
                        if !display {
                            *listen_error.lock() = Some(problem.to_string());
                        }
                    }

                    if !display {
                        return;
                    }

                    if wayland {
                        tracing::info!(
                            "Wayland session detected, grabbing hotkeys through XWayland"
                        );
                        let callback = move |event: Event| {
                            dispatch(&event);
                            Some(event)
                        };

                        if let Err(e) = start_grab_listen(callback) {
                            *listen_error.lock() = Some(format!(
                                "Could not grab hotkeys through XWayland ({:?}). Global hotkeys \
                                 need an X11 session, or XWayland with DISPLAY set.",
                                e
                            ));
                        }
                    } else {
                        tracing::info!("X11 session detected, using X11 listener for hotkeys");
                        if let Err(e) = listen(move |event| dispatch(&event)) {
                            *listen_error.lock() =
                                Some(format!("Could not listen for hotkeys on X11 ({:?})", e));
                        }
                    }
                }

                #[cfg(target_os = "windows")]
                {
                    if let Err(e) = listen(move |event| dispatch(&event)) {
                        *listen_error.lock() = Some(format!(
                            "The system refused to let Encre listen for hotkeys ({:?})",
                            e
                        ));
                    }
                }
            }
        });

        self.listener_handle = Some(handle);
        Ok(())
    }

    pub fn stop(&mut self) -> Result<(), String> {
        let mut active = self.active.lock();
        *active = false;
        drop(active);

        // rdev cannot stop a listener; this only closes the gate.
        Ok(())
    }
}

fn manager<'a>(handle: HotkeyManagerHandle) -> Option<&'a mut SimpleHotkeyManager> {
    if handle.is_null() {
        set_last_error("Null hotkey manager handle provided".to_string());
        return None;
    }
    Some(unsafe { &mut *(handle as *mut SimpleHotkeyManager) })
}

/// # Safety
/// Non-null pointers must be valid null-terminated C strings.
unsafe fn registration(
    binding: *const c_char,
    action: *const c_char,
) -> Result<(String, String), c_int> {
    if binding.is_null() || action.is_null() {
        set_last_error("Null binding or action provided".to_string());
        return Err(FFIErrorCode::NullPointer as c_int);
    }
    let read = |ptr, what| {
        unsafe { c_str_to_string(ptr) }.map_err(|e| {
            set_last_error(format!("Invalid {what} string: {}", e));
            FFIErrorCode::InvalidUtf8 as c_int
        })
    };
    Ok((read(binding, "binding")?, read(action, "action")?))
}

fn registered(result: Result<(), String>) -> c_int {
    match result {
        Ok(()) => FFIErrorCode::Success as c_int,
        Err(e) => {
            set_last_error(format!("Hotkey registration failed: {}", e));
            FFIErrorCode::OperationFailed as c_int
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_manager_new() -> HotkeyManagerHandle {
    init_logging();

    let manager = Box::new(SimpleHotkeyManager::new());
    Box::into_raw(manager) as HotkeyManagerHandle
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_clear(handle: HotkeyManagerHandle) -> c_int {
    guard(FFIErrorCode::OperationFailed as c_int, || {
        let Some(manager) = manager(handle) else {
            return FFIErrorCode::NullPointer as c_int;
        };
        manager.clear_bindings();
        FFIErrorCode::Success as c_int
    })
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_unregister(
    handle: HotkeyManagerHandle,
    action: *const c_char,
) -> c_int {
    guard(FFIErrorCode::OperationFailed as c_int, || {
        let Some(manager) = manager(handle) else {
            return FFIErrorCode::NullPointer as c_int;
        };
        if action.is_null() {
            set_last_error("Null action provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }
        match unsafe { c_str_to_string(action) } {
            Ok(action) => {
                manager.unregister(&action);
                FFIErrorCode::Success as c_int
            }
            Err(e) => {
                set_last_error(format!("Invalid action string: {}", e));
                FFIErrorCode::InvalidUtf8 as c_int
            }
        }
    })
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_register(
    handle: HotkeyManagerHandle,
    binding: *const c_char,
    action: *const c_char,
    callback: HotkeyCallback,
) -> c_int {
    guard(FFIErrorCode::OperationFailed as c_int, || {
        let Some(manager) = manager(handle) else {
            return FFIErrorCode::NullPointer as c_int;
        };
        match unsafe { registration(binding, action) } {
            Ok((binding, action)) => registered(manager.register(binding, action, callback)),
            Err(code) => code,
        }
    })
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_register_hold(
    handle: HotkeyManagerHandle,
    binding: *const c_char,
    action: *const c_char,
    callback: PttCallback,
) -> c_int {
    guard(FFIErrorCode::OperationFailed as c_int, || {
        let Some(manager) = manager(handle) else {
            return FFIErrorCode::NullPointer as c_int;
        };
        match unsafe { registration(binding, action) } {
            Ok((binding, action)) => registered(manager.register_hold(binding, action, callback)),
            Err(code) => code,
        }
    })
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_start(handle: HotkeyManagerHandle) -> c_int {
    guard(FFIErrorCode::OperationFailed as c_int, || {
        let Some(manager) = manager(handle) else {
            return FFIErrorCode::NullPointer as c_int;
        };
        match manager.start() {
            Ok(()) => FFIErrorCode::Success as c_int,
            Err(e) => {
                set_last_error(format!("Failed to start hotkey listener: {}", e));
                FFIErrorCode::OperationFailed as c_int
            }
        }
    })
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_stop(handle: HotkeyManagerHandle) -> c_int {
    guard(FFIErrorCode::OperationFailed as c_int, || {
        let Some(manager) = manager(handle) else {
            return FFIErrorCode::NullPointer as c_int;
        };
        match manager.stop() {
            Ok(()) => FFIErrorCode::Success as c_int,
            Err(e) => {
                set_last_error(format!("Failed to stop hotkey listener: {}", e));
                FFIErrorCode::OperationFailed as c_int
            }
        }
    })
}

/// Null when the listener is running. Free with `encre_free_string`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_listen_error(handle: HotkeyManagerHandle) -> *mut c_char {
    if handle.is_null() {
        return std::ptr::null_mut();
    }

    let manager = unsafe { &*(handle as *mut SimpleHotkeyManager) };
    match manager.listen_error.lock().clone() {
        Some(message) => string_to_c_str(message),
        None => std::ptr::null_mut(),
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_manager_free(handle: HotkeyManagerHandle) {
    unsafe {
        if !handle.is_null() {
            let _ = Box::from_raw(handle as *mut SimpleHotkeyManager);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use rdev::Button;
    use std::ffi::CStr;

    static FIRED: Mutex<Vec<String>> = Mutex::new(Vec::new());
    static SERIAL: Mutex<()> = Mutex::new(());

    extern "C" fn record(action: *const c_char) {
        let text = unsafe { CStr::from_ptr(action) }
            .to_string_lossy()
            .into_owned();
        FIRED.lock().push(text);
    }

    enum Tap {
        Down(Key),
        Up(Key),
        Click,
        /// Released where the listener cannot see, as on the Ctrl+Alt+Del screen.
        Unheard(Key),
        /// The left Ctrl Windows makes up for AltGr, told apart by its scan code.
        FakeCtrl(bool),
    }

    use Tap::{Click, Down, FakeCtrl, Unheard, Up};

    /// What the system reports, updated only after the listener has seen the event.
    static PHYSICAL: Mutex<Modifiers> = Mutex::new(Modifiers {
        ctrl: false,
        alt: false,
        shift: false,
        meta: false,
    });

    fn physically_down(modifier: Modifier) -> bool {
        PHYSICAL.lock().get(modifier)
    }

    fn fired(bindings: &[(&str, &str)], taps: &[Tap]) -> Vec<String> {
        run(bindings, taps, None)
    }

    fn fired_on_windows(bindings: &[(&str, &str)], taps: &[Tap]) -> Vec<String> {
        run(bindings, taps, Some(physically_down))
    }

    fn run(
        bindings: &[(&str, &str)],
        taps: &[Tap],
        modifier_is_down: Option<fn(Modifier) -> bool>,
    ) -> Vec<String> {
        let _serial = SERIAL.lock();
        FIRED.lock().clear();
        *PHYSICAL.lock() = Modifiers::default();

        let mut manager = SimpleHotkeyManager::new();
        for (binding, action) in bindings {
            manager
                .register(binding.to_string(), action.to_string(), record)
                .unwrap_or_else(|e| panic!("could not register {}: {}", binding, e));
        }

        let mut state = ListenerState {
            modifier_is_down,
            ..ListenerState::default()
        };
        for tap in taps {
            let event = match tap {
                Down(key) => Some(EventType::KeyPress(*key)),
                Up(key) => Some(EventType::KeyRelease(*key)),
                Click => Some(EventType::ButtonPress(Button::Left)),
                Unheard(_) => None,
                FakeCtrl(down) => {
                    state.fake_ctrl = *down;
                    PHYSICAL.lock().set(Modifier::Ctrl, *down);
                    None
                }
            };
            if let Some(event) = event {
                state.process(event, &manager.bindings);
            }
            if let Down(key) | Up(key) | Unheard(key) = tap
                && let Some(modifier) = Modifier::from_key(key)
            {
                PHYSICAL.lock().set(modifier, matches!(tap, Down(_)));
            }
        }
        FIRED.lock().clone()
    }

    const MAC: &[(&str, &str)] = &[
        ("ctrl+cmd", "revise_selection"),
        ("ctrl+option+space", "revise_everything"),
        ("ctrl+option+g", "translate_selection"),
    ];

    #[test]
    fn escape_alone_is_the_one_key_needing_no_modifier() {
        assert_eq!(
            fired(
                &[("escape", "close")],
                &[Down(Key::Escape), Up(Key::Escape)]
            ),
            ["close"]
        );
        assert!(
            fired(
                &[("escape", "close")],
                &[Down(Key::ShiftLeft), Down(Key::Escape)]
            )
            .is_empty(),
            "shift+escape fired the bare escape binding"
        );

        let mut manager = SimpleHotkeyManager::new();
        assert!(
            manager
                .register("a".to_string(), "x".to_string(), record)
                .is_err()
        );
    }

    #[test]
    fn unregistering_drops_only_that_action() {
        let mut manager = SimpleHotkeyManager::new();
        manager
            .register("escape".to_string(), "close".to_string(), record)
            .unwrap();
        manager
            .register("ctrl+alt+k".to_string(), "ask".to_string(), record)
            .unwrap();
        manager.unregister("close");

        let actions: Vec<String> = manager
            .bindings
            .lock()
            .iter()
            .map(|b| b.action.clone())
            .collect();
        assert_eq!(actions, ["ask"]);
    }

    #[test]
    fn ctrl_option_space_revises_everything() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::Alt),
                Down(Key::Space),
                Up(Key::Space),
                Up(Key::Alt),
                Up(Key::ControlLeft),
            ],
        );

        assert_eq!(actions, vec!["revise_everything"]);
    }

    #[test]
    fn ctrl_option_g_translates() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::Alt),
                Down(Key::KeyG),
                Up(Key::KeyG),
                Up(Key::Alt),
                Up(Key::ControlLeft),
            ],
        );

        assert_eq!(actions, vec!["translate_selection"]);
    }

    #[test]
    fn ctrl_cmd_fires_when_the_combination_is_released() {
        let held = fired(MAC, &[Down(Key::ControlLeft), Down(Key::MetaLeft)]);
        assert!(held.is_empty(), "fired while the keys were still down");

        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::MetaLeft),
                Up(Key::MetaLeft),
                Up(Key::ControlLeft),
            ],
        );
        assert_eq!(actions, vec!["revise_selection"]);
    }

    /// ctrl+cmd+space opens the macOS emoji picker.
    #[test]
    fn ctrl_cmd_space_leaves_the_selection_alone() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::MetaLeft),
                Down(Key::Space),
                Up(Key::Space),
                Up(Key::MetaLeft),
                Up(Key::ControlLeft),
            ],
        );

        assert!(actions.is_empty(), "fired on the way to the emoji picker");
    }

    #[test]
    fn a_modifier_only_binding_fires_once_whatever_the_release_order() {
        for (first, second) in [
            (Key::MetaLeft, Key::ControlLeft),
            (Key::ControlLeft, Key::MetaLeft),
        ] {
            let actions = fired(
                MAC,
                &[
                    Down(Key::ControlLeft),
                    Down(Key::MetaLeft),
                    Up(first),
                    Up(second),
                ],
            );
            assert_eq!(
                actions,
                vec!["revise_selection"],
                "releasing {:?} first",
                first
            );
        }
    }

    #[test]
    fn typing_before_the_chord_does_not_cancel_it() {
        let actions = fired(
            MAC,
            &[
                Down(Key::KeyH),
                Up(Key::KeyH),
                Down(Key::ControlLeft),
                Down(Key::MetaLeft),
                Up(Key::MetaLeft),
                Up(Key::ControlLeft),
            ],
        );

        assert_eq!(actions, vec!["revise_selection"]);
    }

    #[test]
    fn a_cancelled_chord_does_not_cancel_the_next() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::MetaLeft),
                Down(Key::Space),
                Up(Key::Space),
                Up(Key::MetaLeft),
                Up(Key::ControlLeft),
                Down(Key::ControlLeft),
                Down(Key::MetaLeft),
                Up(Key::MetaLeft),
                Up(Key::ControlLeft),
            ],
        );

        assert_eq!(actions, vec!["revise_selection"]);
    }

    #[test]
    fn an_extra_modifier_cancels_the_modifier_only_binding() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::MetaLeft),
                Down(Key::ShiftLeft),
                Up(Key::ShiftLeft),
                Up(Key::MetaLeft),
                Up(Key::ControlLeft),
            ],
        );

        assert!(actions.is_empty(), "an extra modifier still triggered it");
    }

    #[test]
    fn a_click_cancels_the_modifier_only_binding() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::MetaLeft),
                Click,
                Up(Key::MetaLeft),
                Up(Key::ControlLeft),
            ],
        );

        assert!(actions.is_empty(), "ctrl+cmd+click triggered it");
    }

    #[test]
    fn the_chord_can_be_repeated_without_releasing_every_modifier() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::MetaLeft),
                Up(Key::MetaLeft),
                Down(Key::MetaLeft),
                Up(Key::MetaLeft),
                Up(Key::ControlLeft),
            ],
        );

        assert_eq!(actions, vec!["revise_selection", "revise_selection"]);
    }

    #[test]
    fn holding_a_shortcut_runs_the_action_once() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::Alt),
                Down(Key::Space),
                Down(Key::Space),
                Down(Key::Space),
                Up(Key::Space),
            ],
        );

        assert_eq!(actions, vec!["revise_everything"]);
    }

    #[test]
    fn holding_a_modifier_repeats_neither_state_nor_action() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::MetaLeft),
                Down(Key::MetaLeft),
                Up(Key::MetaLeft),
            ],
        );

        assert_eq!(actions, vec!["revise_selection"]);
    }

    #[test]
    fn a_stray_modifier_refuses_a_key_binding() {
        let actions = fired(
            MAC,
            &[
                Down(Key::ControlLeft),
                Down(Key::Alt),
                Down(Key::ShiftLeft),
                Down(Key::Space),
            ],
        );

        assert!(
            actions.is_empty(),
            "ctrl+option+shift+space matched ctrl+option+space"
        );
    }

    #[test]
    fn the_right_hand_modifiers_are_the_same_modifiers() {
        let actions = fired(
            MAC,
            &[Down(Key::ControlRight), Down(Key::AltGr), Down(Key::Space)],
        );

        assert_eq!(actions, vec!["revise_everything"]);
    }

    #[test]
    fn the_linux_and_windows_defaults_still_fire() {
        for binding in ["ctrl+super", "ctrl+win"] {
            let actions = fired(
                &[(binding, "revise_selection")],
                &[
                    Down(Key::ControlLeft),
                    Down(Key::MetaLeft),
                    Up(Key::MetaLeft),
                    Up(Key::ControlLeft),
                ],
            );
            assert_eq!(actions, vec!["revise_selection"], "binding {}", binding);
        }

        let actions = fired(
            &[("ctrl+alt+space", "revise_everything")],
            &[Down(Key::ControlLeft), Down(Key::Alt), Down(Key::Space)],
        );
        assert_eq!(actions, vec!["revise_everything"]);
    }

    const WINDOWS: &[(&str, &str)] = &[
        ("ctrl+win", "revise_selection"),
        ("ctrl+alt+space", "revise_everything"),
        ("ctrl+alt+g", "translate_selection"),
    ];

    #[test]
    fn releases_lost_to_the_secure_desktop_do_not_turn_typing_into_hotkeys() {
        let taps = [
            Down(Key::ControlLeft),
            Down(Key::Alt),
            Down(Key::Delete),
            Unheard(Key::Alt),
            Unheard(Key::ControlLeft),
            Down(Key::Space),
            Up(Key::Space),
            Down(Key::KeyG),
            Up(Key::KeyG),
        ];

        assert_eq!(
            fired(WINDOWS, &taps),
            ["revise_everything", "translate_selection"]
        );
        assert!(fired_on_windows(WINDOWS, &taps).is_empty());
    }

    #[test]
    fn a_chord_after_lost_releases_starts_afresh() {
        let taps = [
            Down(Key::ControlLeft),
            Down(Key::Alt),
            Unheard(Key::Alt),
            Unheard(Key::ControlLeft),
            Down(Key::ControlLeft),
            Down(Key::MetaLeft),
            Up(Key::MetaLeft),
            Up(Key::ControlLeft),
        ];

        assert!(fired(WINDOWS, &taps).is_empty());
        assert_eq!(fired_on_windows(WINDOWS, &taps), ["revise_selection"]);
    }

    #[test]
    fn only_the_modifier_released_unseen_is_dropped() {
        let taps = [
            Down(Key::ShiftLeft),
            Unheard(Key::ShiftLeft),
            Down(Key::ControlLeft),
            Down(Key::Alt),
            Down(Key::Space),
        ];

        assert!(fired(WINDOWS, &taps).is_empty());
        assert_eq!(fired_on_windows(WINDOWS, &taps), ["revise_everything"]);
    }

    /// The system records a key only after the listener has seen it, so its own modifier reads up.
    #[test]
    fn asking_the_system_keeps_every_chord_firing() {
        let scripts: &[(&[Tap], &[&str])] = &[
            (
                &[
                    Down(Key::ControlLeft),
                    Down(Key::Alt),
                    Down(Key::Space),
                    Up(Key::Space),
                    Up(Key::Alt),
                    Up(Key::ControlLeft),
                ],
                &["revise_everything"],
            ),
            (
                &[Down(Key::ControlRight), Down(Key::AltGr), Down(Key::KeyG)],
                &["translate_selection"],
            ),
            (
                &[
                    Down(Key::ControlLeft),
                    Down(Key::MetaLeft),
                    Up(Key::MetaLeft),
                    Down(Key::MetaLeft),
                    Up(Key::ControlLeft),
                    Up(Key::MetaLeft),
                ],
                &["revise_selection", "revise_selection"],
            ),
            (
                &[
                    Down(Key::ControlLeft),
                    Down(Key::ControlLeft),
                    Down(Key::MetaLeft),
                    Down(Key::MetaLeft),
                    Up(Key::MetaLeft),
                ],
                &["revise_selection"],
            ),
            (
                &[
                    Down(Key::ControlLeft),
                    Down(Key::Alt),
                    Down(Key::ShiftLeft),
                    Down(Key::Space),
                ],
                &[],
            ),
        ];
        for (taps, expected) in scripts {
            assert_eq!(fired_on_windows(WINDOWS, taps), *expected);
            assert_eq!(fired(WINDOWS, taps), *expected);
        }
    }

    #[test]
    fn a_binding_naming_a_key_the_listener_cannot_see_is_refused() {
        let mut manager = SimpleHotkeyManager::new();
        let error = manager
            .register(
                "ctrl+alt+f13".to_string(),
                "revise_everything".to_string(),
                record,
            )
            .expect_err("f13 is not a key the listener knows");

        assert!(error.contains("f13"), "unhelpful message: {}", error);
    }

    #[test]
    fn a_binding_without_a_modifier_is_refused() {
        let mut manager = SimpleHotkeyManager::new();
        assert!(
            manager
                .register("space".to_string(), "revise_everything".to_string(), record)
                .is_err()
        );
    }

    extern "C" fn record_edge(action: *const c_char, down: c_int) {
        let text = unsafe { CStr::from_ptr(action) }.to_string_lossy();
        FIRED.lock().push(format!("{text}:{down}"));
    }

    #[test]
    fn push_to_talk_needs_a_key_to_hold() {
        let mut manager = SimpleHotkeyManager::new();
        let error = manager
            .register_hold("ctrl+cmd".to_string(), "dictate".to_string(), record_edge)
            .expect_err("a modifier-only binding cannot be held");
        assert!(error.contains("ctrl+cmd"), "unhelpful message: {}", error);

        manager
            .register_hold("ctrl+alt+d".to_string(), "dictate".to_string(), record_edge)
            .expect("a keyed binding can be held");
        assert_eq!(manager.bindings.lock().len(), 1);
    }

    #[test]
    fn a_binding_that_names_two_keys_is_refused() {
        let mut manager = SimpleHotkeyManager::new();
        assert!(
            manager
                .register(
                    "ctrl+a+b".to_string(),
                    "revise_everything".to_string(),
                    record
                )
                .is_err()
        );
    }

    #[test]
    fn typing_an_altgr_character_on_windows_is_not_ctrl_alt() {
        let bindings = [("ctrl+alt+a", "ask")];
        let altgr_a = [
            FakeCtrl(true),
            Down(Key::AltGr),
            Down(Key::KeyA),
            Up(Key::KeyA),
            Up(Key::AltGr),
            FakeCtrl(false),
        ];
        assert!(
            fired_on_windows(&bindings, &altgr_a).is_empty(),
            "AltGr+A ran the action"
        );

        for real in [Key::Alt, Key::AltGr] {
            let taps = [Down(Key::ControlLeft), Down(real), Down(Key::KeyA)];
            assert_eq!(
                fired_on_windows(&bindings, &taps),
                vec!["ask"],
                "left Ctrl with {real:?}"
            );
        }

        let lost = [
            FakeCtrl(true),
            Unheard(Key::ControlLeft),
            Down(Key::KeyB),
            Up(Key::KeyB),
            Down(Key::AltGr),
            Down(Key::Space),
        ];
        assert_eq!(
            fired_on_windows(&[("alt+space", "revise")], &lost),
            vec!["revise"],
            "a lost made-up Ctrl kept right Alt from being Alt"
        );
    }

    #[test]
    fn letting_go_of_one_key_does_not_end_the_hold_of_another_with_its_name() {
        let _serial = SERIAL.lock();
        FIRED.lock().clear();
        let mut manager = SimpleHotkeyManager::new();
        manager
            .register_hold("ctrl+1".to_string(), "dictate".to_string(), record_edge)
            .unwrap();
        let mut state = ListenerState::default();
        for event in [
            EventType::KeyPress(Key::ControlLeft),
            EventType::KeyPress(Key::Kp1),
            EventType::KeyPress(Key::Num1),
            EventType::KeyRelease(Key::Num1),
            EventType::KeyPress(Key::Kp1),
        ] {
            state.process(event, &manager.bindings);
        }
        assert_eq!(
            *FIRED.lock(),
            vec!["dictate:1"],
            "the hold restarted or ended early"
        );
    }

    #[test]
    fn a_numpad_digit_fires_a_digit_binding() {
        assert_eq!(key_name(&Key::Kp1), Some("1"));
        assert_eq!(key_name(&Key::Num1), Some("1"));
    }

    #[test]
    fn a_key_with_no_name_is_refused_not_dropped() {
        for binding in ["ctrl+alt+", "ctrl+alt++", "+a", "ctrl++a"] {
            assert!(parse_binding(binding).is_err(), "{binding} was accepted");
        }
        assert!(parse_binding("  ").is_err());
    }

    #[test]
    fn each_platform_spells_the_same_modifier_its_own_way() {
        let ctrl_meta = Modifiers {
            ctrl: true,
            meta: true,
            ..Modifiers::default()
        };
        for binding in ["ctrl+cmd", "ctrl+super", "ctrl+win", "control+meta"] {
            assert_eq!(parse_binding(binding), Ok((ctrl_meta, None)), "{}", binding);
        }

        let ctrl_alt = Modifiers {
            ctrl: true,
            alt: true,
            ..Modifiers::default()
        };
        for binding in ["ctrl+alt+space", "ctrl+option+space"] {
            assert_eq!(
                parse_binding(binding),
                Ok((ctrl_alt, Some("space"))),
                "{}",
                binding
            );
        }
    }

    /// Mirrors what `keyNameToString` in ui/hotkey_capture.go can produce.
    #[test]
    fn every_name_the_recorder_can_produce_is_a_key_the_listener_knows() {
        let recorded = [
            "space",
            "return",
            "escape",
            "tab",
            "backspace",
            "delete",
            "insert",
            "home",
            "end",
            "pageup",
            "pagedown",
            "left",
            "right",
            "up",
            "down",
            "a",
            "z",
            "0",
            "9",
            "f1",
            "f12",
        ];
        for name in recorded {
            assert!(
                canonical_key_name(name).is_some(),
                "the recorder can produce '{}' and the listener cannot match it",
                name
            );
        }
    }

    /// rdev labels a FlagsChanged by comparing its flags with the previous event's, whoever posted it.
    mod mac {
        use super::*;

        const NON_COALESCED: u64 = 0x100;
        const CTRL: u64 = 0x40000 | 0x1;
        const SHIFT: u64 = 0x20000 | 0x2;
        const ALT: u64 = 0x80000 | 0x20;
        pub const META: u64 = 0x100000 | 0x8;

        pub enum Step {
            Press(Key),
            Release(Key),
            Unseen(Key),
            /// Dropped as synthetic, but rdev still takes its flags as the next baseline.
            Dropped(u64),
        }

        pub use Step::{Dropped, Press, Release, Unseen};

        struct Mac {
            physical: Modifiers,
            last_flags: u64,
        }

        impl Mac {
            fn flags(&self) -> u64 {
                let held = self.physical;
                NON_COALESCED
                    | if held.ctrl { CTRL } else { 0 }
                    | if held.alt { ALT } else { 0 }
                    | if held.shift { SHIFT } else { 0 }
                    | if held.meta { META } else { 0 }
            }

            fn flags_changed(&mut self, key: Key) -> EventType {
                let flags = self.flags();
                let release = flags < self.last_flags;
                self.last_flags = flags;
                if release {
                    EventType::KeyRelease(key)
                } else {
                    EventType::KeyPress(key)
                }
            }

            fn run(script: &[Step]) -> Vec<(EventType, u64)> {
                let mut mac = Mac {
                    physical: Modifiers::default(),
                    last_flags: NON_COALESCED,
                };
                let mut delivered = Vec::new();
                for step in script {
                    match step {
                        Press(key) | Release(key) => {
                            let event = match Modifier::from_key(key) {
                                Some(modifier) => {
                                    mac.physical.set(modifier, matches!(step, Press(_)));
                                    mac.flags_changed(*key)
                                }
                                None if matches!(step, Press(_)) => EventType::KeyPress(*key),
                                None => EventType::KeyRelease(*key),
                            };
                            delivered.push((event, mac.flags()));
                        }
                        Unseen(key) => {
                            let modifier = Modifier::from_key(key).expect("a modifier");
                            mac.physical.set(modifier, true);
                        }
                        Dropped(flags) => mac.last_flags = *flags,
                    }
                }
                delivered
            }
        }

        /// `from_flags` mimics the macOS listener; without it the state trusts rdev's labels.
        pub fn fired(bindings: &[(&str, &str)], script: &[Step], from_flags: bool) -> Vec<String> {
            let _serial = SERIAL.lock();
            FIRED.lock().clear();

            let mut manager = SimpleHotkeyManager::new();
            for (binding, action) in bindings {
                manager
                    .register(binding.to_string(), action.to_string(), record)
                    .unwrap_or_else(|e| panic!("could not register {}: {}", binding, e));
            }

            let mut state = ListenerState::default();
            for (event, flags) in Mac::run(script) {
                if !from_flags {
                    state.process(event, &manager.bindings);
                    continue;
                }
                let key = match event {
                    EventType::KeyPress(key) | EventType::KeyRelease(key)
                        if Modifier::from_key(&key).is_some() =>
                    {
                        None
                    }
                    other => Some(other),
                };
                state.observe(Modifiers::from_flags(flags), key, &manager.bindings);
            }
            FIRED.lock().clone()
        }

        /// The first press's action leaves a synthetic FlagsChanged in rdev's baseline.
        pub fn three_presses_around_an_action() -> Vec<Step> {
            let mut script = Vec::new();
            for press in 0..3 {
                script.extend([
                    Press(Key::ControlLeft),
                    Press(Key::Alt),
                    Press(Key::Space),
                    Release(Key::Space),
                    Release(Key::Alt),
                    Release(Key::ControlLeft),
                ]);
                if press == 0 {
                    script.push(Dropped(META | NON_COALESCED));
                }
            }
            script
        }
    }

    /// rdev reports the second press's ctrl down as a release; the third must still fire.
    #[test]
    fn mislabelled_edges_drop_every_other_press() {
        let actions = mac::fired(MAC, &mac::three_presses_around_an_action(), false);

        assert_eq!(actions, vec!["revise_everything", "revise_everything"]);
    }

    #[test]
    fn the_event_flags_survive_mislabelled_edges() {
        let actions = mac::fired(MAC, &mac::three_presses_around_an_action(), true);

        assert_eq!(
            actions,
            vec![
                "revise_everything",
                "revise_everything",
                "revise_everything"
            ]
        );
    }

    #[test]
    fn the_event_flags_keep_the_modifier_only_chord_firing_on_release() {
        use mac::{Dropped, META, Press, Release};

        let script = [
            Press(Key::ControlLeft),
            Press(Key::Alt),
            Press(Key::Space),
            Release(Key::Space),
            Release(Key::Alt),
            Release(Key::ControlLeft),
            Dropped(META),
            Press(Key::ControlLeft),
            Press(Key::MetaLeft),
            Release(Key::MetaLeft),
            Release(Key::ControlLeft),
        ];

        assert_eq!(mac::fired(MAC, &script, false), vec!["revise_everything"]);
        assert_eq!(
            mac::fired(MAC, &script, true),
            vec!["revise_everything", "revise_selection"]
        );
    }

    #[test]
    fn the_event_flags_know_a_modifier_the_listener_never_saw_pressed() {
        use mac::{Press, Release, Unseen};

        let script = [
            Unseen(Key::ControlLeft),
            Press(Key::Alt),
            Press(Key::Space),
            Release(Key::Space),
            Release(Key::Alt),
            Release(Key::ControlLeft),
        ];

        assert!(mac::fired(MAC, &script, false).is_empty());
        assert_eq!(mac::fired(MAC, &script, true), vec!["revise_everything"]);
    }

    #[test]
    fn resuming_reuses_the_listener_thread() {
        let mut manager = SimpleHotkeyManager::new();

        manager.start().expect("start");
        let first = manager
            .listener_handle
            .as_ref()
            .expect("a listener")
            .thread()
            .id();

        manager.stop().expect("stop");
        manager.start().expect("resume");
        let second = manager
            .listener_handle
            .as_ref()
            .expect("a listener")
            .thread()
            .id();

        assert_eq!(first, second, "resuming spawned a second listener");
    }

    #[test]
    fn unregistering_an_invalid_action_says_why() {
        let _slot = ERROR_SLOT.lock();
        take_last_error();

        // Not encre_hotkey_manager_new: its global log subscriber would leak into the ffi_log tests.
        let handle = Box::into_raw(Box::new(SimpleHotkeyManager::new())) as HotkeyManagerHandle;
        let invalid = [0xffu8 as c_char, 0];
        let code = unsafe { encre_hotkey_unregister(handle, invalid.as_ptr()) };
        unsafe { encre_hotkey_manager_free(handle) };

        assert_eq!(code, FFIErrorCode::InvalidUtf8 as c_int);
        let error = take_last_error().expect("an error was set");
        assert!(error.contains("action"), "{error}");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn an_x11_session_listens_as_before() {
        assert_eq!(x11_problem(false, true), None);
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn without_an_x_display_the_reason_is_the_missing_display() {
        for wayland in [false, true] {
            let problem = x11_problem(wayland, false).expect("a problem");
            assert!(problem.contains("X11 session"), "{problem}");
            assert!(problem.contains("DISPLAY"), "{problem}");
            assert!(!problem.contains("input"), "{problem}");
        }
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn wayland_is_reported_rather_than_blamed_on_the_input_group() {
        let problem = x11_problem(true, true).expect("a problem");
        assert!(problem.contains("XWayland"), "{problem}");
        assert!(!problem.contains("input"), "{problem}");
    }
}

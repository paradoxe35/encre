use std::ffi::CString;
use std::os::raw::{c_char, c_int};
use std::sync::Arc;
use std::thread;

use parking_lot::Mutex;
use rdev::{Event, EventType, Key};

// Wayland needs rdev's evdev grab (user in the `input` group); X11, macOS and Windows work
// through listen() with no special permissions.
#[cfg(not(target_os = "linux"))]
use rdev::listen;
#[cfg(target_os = "linux")]
use rdev::{listen, start_grab_listen};

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

/// Receives the action string the binding was registered with.
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

/// Resolved once at registration rather than re-derived from strings on every key press, since
/// matching runs inside the system's own event callback.
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
}

const MODIFIERS: [Modifier; 4] = [
    Modifier::Ctrl,
    Modifier::Alt,
    Modifier::Shift,
    Modifier::Meta,
];

/// Which modifiers are physically down right now.
type ModifierSource = Box<dyn FnMut() -> Modifiers + Send>;

/// rdev labels a macOS FlagsChanged press or release by comparing its flags with the previous
/// one's, a baseline every FlagsChanged rewrites, the simulator's tagged key-ups included. Once
/// it sits above the physical state a real press reads as a release. The system's key state
/// tables carry no history, so on macOS the listener reads those instead.
#[cfg(target_os = "macos")]
fn system_modifiers() -> Modifiers {
    const MODIFIER_KEYS: [(u16, Modifier); 8] = [
        (0x37, Modifier::Meta),
        (0x36, Modifier::Meta),
        (0x38, Modifier::Shift),
        (0x3C, Modifier::Shift),
        (0x3A, Modifier::Alt),
        (0x3D, Modifier::Alt),
        (0x3B, Modifier::Ctrl),
        (0x3E, Modifier::Ctrl),
    ];

    let mut held = Modifiers::default();
    for (key, modifier) in MODIFIER_KEYS {
        if crate::core::simulator::key_down(key) {
            held.set(modifier, true);
        }
    }
    held
}

#[cfg(target_os = "macos")]
fn system_modifier_source() -> Option<ModifierSource> {
    let source: ModifierSource = Box::new(system_modifiers);
    Some(source)
}

#[cfg(not(target_os = "macos"))]
fn system_modifier_source() -> Option<ModifierSource> {
    None
}

/// One table for both directions, so a name the recorder emits but the listener cannot match
/// fails at registration instead of saving as a binding that silently never fires.
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

/// A binding is modifiers, then optionally one key: `ctrl+alt+space`, or `ctrl+cmd` on its own.
/// A modifier is required, or the binding would fire on ordinary typing.
fn parse_binding(binding: &str) -> Result<(Modifiers, Option<&'static str>), String> {
    let parts: Vec<&str> = binding
        .split('+')
        .map(str::trim)
        .filter(|part| !part.is_empty())
        .collect();
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

    if modifiers.is_empty() {
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
    /// `None` for a modifier-only binding such as `ctrl+cmd`.
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

/// A modifier-only binding fires on release, and only if nothing else happened while it was held;
/// firing on press would trigger `ctrl+cmd` on the way to `ctrl+cmd+space`.
#[derive(Default)]
struct ListenerState {
    /// Replaces the events' own press/release labels when set.
    source: Option<ModifierSource>,
    held: Modifiers,
    /// The largest modifier set held since the last time every modifier was up.
    chord: Modifiers,
    /// Something beyond the chord's own modifiers happened while it was held.
    interrupted: bool,
    /// The non-modifier key currently down, so auto-repeat is not read as a second press.
    held_key: Option<&'static str>,
    /// The key that started a push-to-talk hold, so the up edge is only sent for a hold that began.
    holding: Option<&'static str>,
    delivery_announced: bool,
    unmatched_announced: Vec<&'static str>,
}

impl ListenerState {
    fn new(source: Option<ModifierSource>) -> Self {
        Self {
            source,
            ..Self::default()
        }
    }

    fn process(&mut self, event: EventType, bindings: &Mutex<Vec<HotkeyBinding>>) {
        self.sync(bindings);
        match event {
            EventType::KeyPress(key) => self.on_press(key, bindings),
            EventType::KeyRelease(key) => self.on_release(key, bindings),
            EventType::ButtonPress(_) => self.interrupted = true,
            _ => {}
        }
    }

    /// Turns whatever changed in the system's tables since the last event into edges.
    fn sync(&mut self, bindings: &Mutex<Vec<HotkeyBinding>>) {
        let now = match self.source.as_mut() {
            Some(source) => source(),
            None => return,
        };
        let before = self.held;
        for modifier in MODIFIERS {
            if before.get(modifier) && !now.get(modifier) {
                self.modifier_up(modifier, bindings);
            }
        }
        for modifier in MODIFIERS {
            if !before.get(modifier) && now.get(modifier) {
                self.modifier_down(modifier);
            }
        }
    }

    fn modifier_down(&mut self, modifier: Modifier) {
        if self.held.get(modifier) {
            return;
        }
        // The first modifier down begins a chord, whatever was typed before it.
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

        // The first modifier to come up ends the chord; releasing the rest must not fire again.
        if !self.interrupted && before == self.chord {
            for binding in bindings.lock().iter() {
                if binding.key.is_none() && binding.modifiers == before {
                    fire(binding, true);
                }
            }
        }
    }

    fn on_press(&mut self, key: Key, bindings: &Mutex<Vec<HotkeyBinding>>) {
        if let Some(modifier) = Modifier::from_key(&key) {
            if self.source.is_none() {
                self.modifier_down(modifier);
            }
            return;
        }

        let Some(name) = key_name(&key) else {
            self.interrupted = true;
            return;
        };
        if self.held_key == Some(name) {
            return;
        }
        self.held_key = Some(name);
        self.interrupted = true;

        // Logged once, without naming the key: proof that key events arrive at all.
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
                    self.holding = Some(name);
                }
            }
        }
        if !fired {
            self.note_unmatched(name, &bindings);
        }
    }

    fn on_release(&mut self, key: Key, bindings: &Mutex<Vec<HotkeyBinding>>) {
        let Some(modifier) = Modifier::from_key(&key) else {
            let name = key_name(&key);
            if self.held_key == name {
                self.held_key = None;
            }
            // Only the named key ends a hold; a modifier released first is a slipped finger.
            if let (Some(name), Some(holding)) = (name, self.holding) {
                if name == holding {
                    self.holding = None;
                    for binding in bindings.lock().iter() {
                        if binding.is_hold() && binding.key == Some(name) {
                            fire(binding, false);
                        }
                    }
                }
            }
            return;
        };
        if self.source.is_none() {
            self.modifier_up(modifier, bindings);
        }
    }

    /// Logged once per key; it tells a wrong binding apart from a listener the system never
    /// delivers to.
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

/// The simulator's own events must not reach the state machine: its Cmd+A would count as a key
/// the user pressed, and rdev labels its modifier key-ups as presses.
#[cfg(target_os = "macos")]
fn is_synthetic(event: &Event) -> bool {
    event.extra_data == crate::core::simulator::SYNTHETIC_TAG
}

#[cfg(not(target_os = "macos"))]
fn is_synthetic(_: &Event) -> bool {
    false
}

pub struct SimpleHotkeyManager {
    bindings: Arc<Mutex<Vec<HotkeyBinding>>>,
    listener_handle: Option<thread::JoinHandle<()>>,
    active: Arc<Mutex<bool>>,
    /// The listener fails on its own thread and a macOS .app bundle discards stdout, so the
    /// error is surfaced here instead of the shortcut going dead without a trace.
    listen_error: Arc<Mutex<Option<String>>>,
}

impl SimpleHotkeyManager {
    pub fn new() -> Self {
        Self {
            bindings: Arc::new(Mutex::new(Vec::new())),
            listener_handle: None,
            active: Arc::new(Mutex::new(false)),
            listen_error: Arc::new(Mutex::new(None)),
        }
    }

    pub fn clear_bindings(&mut self) {
        self.bindings.lock().clear();
        tracing::info!("Cleared all hotkey bindings");
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

        // rdev cannot stop a listener, so resume reuses this thread; a second one would fire
        // every action twice.
        if self.listener_handle.is_some() {
            return Ok(());
        }

        let bindings = self.bindings.clone();
        let active_flag = self.active.clone();
        let listen_error = self.listen_error.clone();

        let handle = thread::spawn(move || {
            let mut state = ListenerState::new(system_modifier_source());
            // A panic here would unwind into the system's callback; caught so the listener lives on.
            let mut dispatch = move |event: &Event| {
                let _ = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
                    if !*active_flag.lock() || is_synthetic(event) {
                        return;
                    }
                    state.process(event.event_type, &bindings);
                }));
            };

            #[cfg(target_os = "linux")]
            {
                if is_wayland() {
                    tracing::info!("Wayland session detected, using evdev grab for hotkeys");
                    let callback = move |event: Event| {
                        dispatch(&event);
                        Some(event)
                    };

                    if let Err(e) = start_grab_listen(callback) {
                        *listen_error.lock() = Some(format!(
                            "Wayland grab failed ({:?}). Add your user to the 'input' group: \
                             sudo usermod -aG input $USER",
                            e
                        ));
                    }
                } else {
                    tracing::info!("X11 session detected, using X11 listener for hotkeys");
                    if let Err(e) = listen(move |event| dispatch(&event)) {
                        *listen_error.lock() = Some(format!("X11 listener failed ({:?})", e));
                    }
                }
            }

            #[cfg(not(target_os = "linux"))]
            {
                if let Err(e) = listen(move |event| dispatch(&event)) {
                    *listen_error.lock() = Some(format!(
                        "The system refused the key listener ({:?}). On macOS this is Input \
                         Monitoring; grant it to Encre and restart.",
                        e
                    ));
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

/// The binding and action strings a registration call receives.
///
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
    let Some(manager) = manager(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    manager.clear_bindings();
    FFIErrorCode::Success as c_int
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_register(
    handle: HotkeyManagerHandle,
    binding: *const c_char,
    action: *const c_char,
    callback: HotkeyCallback,
) -> c_int {
    let Some(manager) = manager(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    match unsafe { registration(binding, action) } {
        Ok((binding, action)) => registered(manager.register(binding, action, callback)),
        Err(code) => code,
    }
}

/// Push-to-talk: the callback receives 1 on key down and 0 on key up.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_register_hold(
    handle: HotkeyManagerHandle,
    binding: *const c_char,
    action: *const c_char,
    callback: PttCallback,
) -> c_int {
    let Some(manager) = manager(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    match unsafe { registration(binding, action) } {
        Ok((binding, action)) => registered(manager.register_hold(binding, action, callback)),
        Err(code) => code,
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_start(handle: HotkeyManagerHandle) -> c_int {
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
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_hotkey_stop(handle: HotkeyManagerHandle) -> c_int {
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
}

/// Null when the listener is running. The caller frees the string with `encre_free_string`.
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
    }

    use Tap::{Click, Down, Up};

    fn fired(bindings: &[(&str, &str)], taps: &[Tap]) -> Vec<String> {
        let _serial = SERIAL.lock();
        FIRED.lock().clear();

        let mut manager = SimpleHotkeyManager::new();
        for (binding, action) in bindings {
            manager
                .register(binding.to_string(), action.to_string(), record)
                .unwrap_or_else(|e| panic!("could not register {}: {}", binding, e));
        }

        let mut state = ListenerState::default();
        for tap in taps {
            state.process(
                match tap {
                    Down(key) => EventType::KeyPress(*key),
                    Up(key) => EventType::KeyRelease(*key),
                    Click => EventType::ButtonPress(Button::Left),
                },
                &manager.bindings,
            );
        }
        FIRED.lock().clone()
    }

    const MAC: &[(&str, &str)] = &[
        ("ctrl+cmd", "revise_selection"),
        ("ctrl+option+space", "revise_all"),
        ("ctrl+option+g", "translate_selection"),
    ];

    /// Option arrives as `Key::Alt`, the space bar as "space".
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

        assert_eq!(actions, vec!["revise_all"]);
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

    /// ctrl+cmd+space opens the macOS emoji picker; the modifier-only binding must not fire on
    /// the way there.
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

    /// Auto-repeat redelivers the key press while the shortcut is held.
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

        assert_eq!(actions, vec!["revise_all"]);
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

        assert_eq!(actions, vec!["revise_all"]);
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
            &[("ctrl+alt+space", "revise_all")],
            &[Down(Key::ControlLeft), Down(Key::Alt), Down(Key::Space)],
        );
        assert_eq!(actions, vec!["revise_all"]);
    }

    #[test]
    fn a_binding_naming_a_key_the_listener_cannot_see_is_refused() {
        let mut manager = SimpleHotkeyManager::new();
        let error = manager
            .register("ctrl+alt+f13".to_string(), "revise_all".to_string(), record)
            .expect_err("f13 is not a key the listener knows");

        assert!(error.contains("f13"), "unhelpful message: {}", error);
    }

    #[test]
    fn a_binding_without_a_modifier_is_refused() {
        let mut manager = SimpleHotkeyManager::new();
        assert!(
            manager
                .register("space".to_string(), "revise_all".to_string(), record)
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
                .register("ctrl+a+b".to_string(), "revise_all".to_string(), record)
                .is_err()
        );
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

    /// The list mirrors what `HotkeyRecorder.keyName` on the host side can produce.
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

    /// A Mac as rdev shows it to the listener: FlagsChanged events labelled press or release by
    /// comparing their flags with the previous one's, whoever posted that one.
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
            /// A press the listener is never shown.
            Unseen(Key),
            /// A FlagsChanged the listener drops as synthetic; rdev has still taken its flags
            /// as the next baseline.
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

            /// Each delivered event with the modifiers physically down as it arrives.
            fn run(script: &[Step]) -> Vec<(EventType, Modifiers)> {
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
                            delivered.push((event, mac.physical));
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

        /// `from_system` is the macOS listener; without it the state trusts rdev's labels.
        pub fn fired(bindings: &[(&str, &str)], script: &[Step], from_system: bool) -> Vec<String> {
            let _serial = SERIAL.lock();
            FIRED.lock().clear();

            let mut manager = SimpleHotkeyManager::new();
            for (binding, action) in bindings {
                manager
                    .register(binding.to_string(), action.to_string(), record)
                    .unwrap_or_else(|e| panic!("could not register {}: {}", binding, e));
            }

            let physical = Arc::new(Mutex::new(Modifiers::default()));
            let source: ModifierSource = {
                let physical = physical.clone();
                Box::new(move || *physical.lock())
            };
            let mut state = ListenerState::new(from_system.then_some(source));
            for (event, held) in Mac::run(script) {
                *physical.lock() = held;
                state.process(event, &manager.bindings);
            }
            FIRED.lock().clone()
        }

        /// Three ctrl+option+space presses; the first one's action leaves a synthetic
        /// FlagsChanged in rdev's baseline.
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

    /// rdev hands the second press's ctrl down over as a release; that press's own releases
    /// bring the baseline back down, so the third fires.
    #[test]
    fn mislabelled_edges_drop_every_other_press() {
        let actions = mac::fired(MAC, &mac::three_presses_around_an_action(), false);

        assert_eq!(actions, vec!["revise_all", "revise_all"]);
    }

    #[test]
    fn the_system_tables_survive_mislabelled_edges() {
        let actions = mac::fired(MAC, &mac::three_presses_around_an_action(), true);

        assert_eq!(actions, vec!["revise_all", "revise_all", "revise_all"]);
    }

    #[test]
    fn the_system_tables_keep_the_modifier_only_chord_firing_on_release() {
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

        assert_eq!(mac::fired(MAC, &script, false), vec!["revise_all"]);
        assert_eq!(
            mac::fired(MAC, &script, true),
            vec!["revise_all", "revise_selection"]
        );
    }

    #[test]
    fn the_system_tables_know_a_modifier_the_listener_never_saw_pressed() {
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
        assert_eq!(mac::fired(MAC, &script, true), vec!["revise_all"]);
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
}

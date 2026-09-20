//! A keyboard tap served by the listener's own run loop. rdev parks its tap on the main loop,
//! so with it every key event waits for whatever the UI thread is doing.

use std::ffi::c_void;
use std::ptr;
use std::sync::atomic::{AtomicPtr, Ordering};

pub enum TapKind {
    KeyDown,
    KeyUp,
    FlagsChanged,
    MouseDown,
}

pub struct TapEvent {
    pub kind: TapKind,
    pub keycode: u16,
    /// Names the modifiers down as the event was made.
    pub flags: u64,
    /// Posted by the simulator rather than typed.
    pub synthetic: bool,
}

const HID_TAP: u32 = 0;
const HEAD_INSERT: u32 = 0;
const LISTEN_ONLY: u32 = 1;

const LEFT_MOUSE_DOWN: u32 = 1;
const RIGHT_MOUSE_DOWN: u32 = 3;
const KEY_DOWN: u32 = 10;
const KEY_UP: u32 = 11;
const FLAGS_CHANGED: u32 = 12;
const OTHER_MOUSE_DOWN: u32 = 25;
const TAP_DISABLED_BY_TIMEOUT: u32 = 0xFFFF_FFFE;
const TAP_DISABLED_BY_USER_INPUT: u32 = 0xFFFF_FFFF;

const KEYBOARD_EVENT_KEYCODE: u32 = 9;
const EVENT_SOURCE_USER_DATA: u32 = 42;

static TAP: AtomicPtr<c_void> = AtomicPtr::new(ptr::null_mut());

unsafe extern "C" {
    fn CGEventTapCreate(
        tap: u32,
        place: u32,
        options: u32,
        mask: u64,
        callback: extern "C" fn(*mut c_void, u32, *mut c_void, *mut c_void) -> *mut c_void,
        user_info: *mut c_void,
    ) -> *mut c_void;
    fn CGEventTapEnable(tap: *mut c_void, enable: bool);
    fn CGEventGetIntegerValueField(event: *mut c_void, field: u32) -> i64;
    fn CGEventGetFlags(event: *mut c_void) -> u64;
    fn CFMachPortCreateRunLoopSource(
        allocator: *const c_void,
        port: *mut c_void,
        order: isize,
    ) -> *mut c_void;
    fn CFRunLoopGetCurrent() -> *mut c_void;
    fn CFRunLoopAddSource(run_loop: *mut c_void, source: *mut c_void, mode: *const c_void);
    fn CFRunLoopRun();
    static kCFRunLoopCommonModes: *const c_void;
}

extern "C" fn deliver<F: FnMut(TapEvent)>(
    _proxy: *mut c_void,
    kind: u32,
    event: *mut c_void,
    user_info: *mut c_void,
) -> *mut c_void {
    // macOS switches a tap off when its callback runs late or on user input; switch it back on.
    if kind == TAP_DISABLED_BY_TIMEOUT || kind == TAP_DISABLED_BY_USER_INPUT {
        unsafe { CGEventTapEnable(TAP.load(Ordering::Acquire), true) };
        return event;
    }

    let kind = match kind {
        KEY_DOWN => TapKind::KeyDown,
        KEY_UP => TapKind::KeyUp,
        FLAGS_CHANGED => TapKind::FlagsChanged,
        LEFT_MOUSE_DOWN | RIGHT_MOUSE_DOWN | OTHER_MOUSE_DOWN => TapKind::MouseDown,
        _ => return event,
    };

    let field = |field| unsafe { CGEventGetIntegerValueField(event, field) };
    let callback = unsafe { &mut *(user_info as *mut F) };
    callback(TapEvent {
        kind,
        keycode: field(KEYBOARD_EVENT_KEYCODE) as u16,
        flags: unsafe { CGEventGetFlags(event) },
        synthetic: field(EVENT_SOURCE_USER_DATA) == crate::core::simulator::SYNTHETIC_TAG,
    });
    event
}

/// Runs the tap on the calling thread for the rest of the process.
pub fn listen<F: FnMut(TapEvent) + 'static>(callback: F) -> Result<(), String> {
    let mask = (1u64 << KEY_DOWN)
        | (1 << KEY_UP)
        | (1 << FLAGS_CHANGED)
        | (1 << LEFT_MOUSE_DOWN)
        | (1 << RIGHT_MOUSE_DOWN)
        | (1 << OTHER_MOUSE_DOWN);

    unsafe {
        let tap = CGEventTapCreate(
            HID_TAP,
            HEAD_INSERT,
            LISTEN_ONLY,
            mask,
            deliver::<F>,
            Box::into_raw(Box::new(callback)) as *mut c_void,
        );
        if tap.is_null() {
            return Err("the event tap was refused".to_owned());
        }
        TAP.store(tap, Ordering::Release);

        let source = CFMachPortCreateRunLoopSource(ptr::null(), tap, 0);
        if source.is_null() {
            return Err("the run loop source was refused".to_owned());
        }
        CFRunLoopAddSource(CFRunLoopGetCurrent(), source, kCFRunLoopCommonModes);
        CGEventTapEnable(tap, true);
        CFRunLoopRun();
    }
    Ok(())
}

use std::os::raw::{c_char, c_int};

use super::ffi_types::*;
use crate::core::ClipboardManager;

fn clipboard<'a>(handle: ClipboardHandle) -> Option<&'a ClipboardManager> {
    if handle.is_null() {
        set_last_error("Null clipboard handle provided".to_string());
        return None;
    }
    Some(unsafe { &*(handle as *mut ClipboardManager) })
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_new() -> ClipboardHandle {
    init_logging();

    match ClipboardManager::new() {
        Ok(clipboard) => Box::into_raw(Box::new(clipboard)) as ClipboardHandle,
        Err(e) => {
            set_last_error(format!("Failed to create clipboard manager: {:#}", e));
            std::ptr::null_mut()
        }
    }
}

/// Null when the clipboard holds no text, including when it holds an image.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_get_text(handle: ClipboardHandle) -> *mut c_char {
    let Some(clipboard) = clipboard(handle) else {
        return std::ptr::null_mut();
    };

    match clipboard.get_text() {
        Some(text) => string_to_c_str(text),
        None => {
            set_last_error("Clipboard holds no text".to_string());
            std::ptr::null_mut()
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_set_text(
    handle: ClipboardHandle,
    text: *const c_char,
) -> c_int {
    let Some(clipboard) = clipboard(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    if text.is_null() {
        set_last_error("Null text provided".to_string());
        return FFIErrorCode::NullPointer as c_int;
    }

    match unsafe { c_str_to_string(text) } {
        Ok(text) => result_to_error_code(clipboard.set_text(text)),
        Err(e) => {
            set_last_error(format!("Invalid text string: {}", e));
            FFIErrorCode::InvalidUtf8 as c_int
        }
    }
}

/// Empties the clipboard, so a following simulated copy landing becomes observable.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_clear(handle: ClipboardHandle) -> c_int {
    let Some(clipboard) = clipboard(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    result_to_error_code(clipboard.clear())
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_save(handle: ClipboardHandle) -> c_int {
    let Some(clipboard) = clipboard(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    clipboard.save_clipboard();
    FFIErrorCode::Success as c_int
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_restore(handle: ClipboardHandle) -> c_int {
    let Some(clipboard) = clipboard(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    result_to_error_code(clipboard.restore_clipboard())
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_free(handle: ClipboardHandle) {
    unsafe {
        if !handle.is_null() {
            let _ = Box::from_raw(handle as *mut ClipboardManager);
        }
    }
}

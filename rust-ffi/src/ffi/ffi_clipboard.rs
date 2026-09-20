use std::os::raw::{c_char, c_int};

use super::ffi_types::*;
use crate::core::ClipboardManager;

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
    unsafe {
        if handle.is_null() {
            set_last_error("Null clipboard handle provided".to_string());
            return std::ptr::null_mut();
        }

        let clipboard = &*(handle as *mut ClipboardManager);

        match clipboard.get_text() {
            Some(text) => string_to_c_str(text),
            None => {
                set_last_error("Clipboard holds no text".to_string());
                std::ptr::null_mut()
            }
        }
    }
}

/// 1 when the clipboard holds text, 0 when not.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_has_text(handle: ClipboardHandle) -> c_int {
    unsafe {
        if handle.is_null() {
            set_last_error("Null clipboard handle provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        let clipboard = &*(handle as *mut ClipboardManager);
        if clipboard.get_text().is_some() { 1 } else { 0 }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_set_text(
    handle: ClipboardHandle,
    text: *const c_char,
) -> c_int {
    unsafe {
        if handle.is_null() {
            set_last_error("Null clipboard handle provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        if text.is_null() {
            set_last_error("Null text provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        let clipboard = &*(handle as *mut ClipboardManager);

        let text_str = match c_str_to_string(text) {
            Ok(s) => s,
            Err(e) => {
                set_last_error(format!("Invalid text string: {}", e));
                return FFIErrorCode::InvalidUtf8 as c_int;
            }
        };

        result_to_error_code(clipboard.set_text(text_str))
    }
}

/// Empties the clipboard, so a following simulated copy landing becomes observable.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_clear(handle: ClipboardHandle) -> c_int {
    unsafe {
        if handle.is_null() {
            set_last_error("Null clipboard handle provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        let clipboard = &*(handle as *mut ClipboardManager);
        result_to_error_code(clipboard.clear())
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_save(handle: ClipboardHandle) -> c_int {
    unsafe {
        if handle.is_null() {
            set_last_error("Null clipboard handle provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        let clipboard = &*(handle as *mut ClipboardManager);
        result_to_error_code(clipboard.save_clipboard())
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_restore(handle: ClipboardHandle) -> c_int {
    unsafe {
        if handle.is_null() {
            set_last_error("Null clipboard handle provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        let clipboard = &*(handle as *mut ClipboardManager);
        result_to_error_code(clipboard.restore_clipboard())
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_clipboard_free(handle: ClipboardHandle) {
    unsafe {
        if !handle.is_null() {
            let _ = Box::from_raw(handle as *mut ClipboardManager);
        }
    }
}

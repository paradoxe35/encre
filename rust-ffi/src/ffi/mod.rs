pub mod ffi_clipboard;
pub mod ffi_hotkey;
pub mod ffi_simulator;
pub mod ffi_stt;
pub mod ffi_types;

pub use ffi_types::*;
use std::os::raw::c_char;

/// Null when there is no error. Free with `encre_free_string`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_get_last_error() -> *const c_char {
    match take_last_error() {
        Some(err) => string_to_c_str(err),
        None => std::ptr::null(),
    }
}

/// Frees a string returned by any function in this crate.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_free_string(s: *mut c_char) {
    unsafe {
        if !s.is_null() {
            let _ = std::ffi::CString::from_raw(s);
        }
    }
}

use std::ffi::{CStr, CString};
use std::os::raw::{c_char, c_int, c_void};
use std::panic::{AssertUnwindSafe, catch_unwind};
use std::sync::Once;

use parking_lot::Mutex;

pub type HotkeyManagerHandle = *mut c_void;
pub type ClipboardHandle = *mut c_void;
pub type SimulatorHandle = *mut c_void;
pub type SttHandle = *mut c_void;

#[repr(C)]
#[derive(Debug, Copy, Clone, PartialEq, Eq)]
pub enum FFIErrorCode {
    Success = 0,
    NullPointer = -1,
    InitFailed = -2,
    InvalidArgument = -3,
    OperationFailed = -4,
    InvalidUtf8 = -5,
    NotInitialized = -6,
}

static LAST_ERROR: Mutex<Option<String>> = Mutex::new(None);

/// Tests that assert on the shared error slot hold this so they do not take each other's errors.
#[cfg(test)]
pub static ERROR_SLOT: Mutex<()> = Mutex::new(());

pub fn set_last_error(err: String) {
    *LAST_ERROR.lock() = Some(err);
}

pub fn take_last_error() -> Option<String> {
    LAST_ERROR.lock().take()
}

/// # Safety
/// `c_str` must be a valid null-terminated C string.
pub unsafe fn c_str_to_string(c_str: *const c_char) -> Result<String, &'static str> {
    unsafe {
        if c_str.is_null() {
            return Err("Null pointer provided");
        }
        CStr::from_ptr(c_str)
            .to_str()
            .map(|s| s.to_string())
            .map_err(|_| "Invalid UTF-8 in C string")
    }
}

/// Caller must free the result with `encre_free_string`.
pub fn string_to_c_str(s: String) -> *mut c_char {
    match CString::new(s) {
        Ok(c_string) => c_string.into_raw(),
        Err(_) => {
            set_last_error("String contains null byte".to_string());
            std::ptr::null_mut()
        }
    }
}

pub fn result_to_error_code<T>(result: Result<T, anyhow::Error>) -> c_int {
    match result {
        Ok(_) => FFIErrorCode::Success as c_int,
        Err(e) => {
            set_last_error(format!("{:#}", e));
            FFIErrorCode::OperationFailed as c_int
        }
    }
}

/// A panic escaping `extern "C"` aborts the host, so it becomes the entry point's failure value.
pub fn guard<T>(fallback: T, f: impl FnOnce() -> T) -> T {
    catch_unwind(AssertUnwindSafe(f)).unwrap_or_else(|panic| {
        let reason = panic
            .downcast_ref::<&str>()
            .map(|s| s.to_string())
            .or_else(|| panic.downcast_ref::<String>().cloned())
            .unwrap_or_else(|| "unknown cause".to_string());
        set_last_error(format!("Internal error: {reason}"));
        fallback
    })
}

static INIT_LOGGING: Once = Once::new();

pub fn init_logging() {
    INIT_LOGGING.call_once(crate::ffi::ffi_log::install);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_error_storage() {
        let _slot = ERROR_SLOT.lock();
        set_last_error("test error".to_string());
        let err = take_last_error();
        assert_eq!(err, Some("test error".to_string()));

        let err2 = take_last_error();
        assert_eq!(err2, None);
    }

    #[test]
    fn a_panic_becomes_the_fallback_and_the_last_error() {
        let _slot = ERROR_SLOT.lock();
        take_last_error();

        let code = guard(FFIErrorCode::OperationFailed as c_int, || {
            panic!("clipboard went away")
        });
        assert_eq!(code, FFIErrorCode::OperationFailed as c_int);
        assert_eq!(
            take_last_error().as_deref(),
            Some("Internal error: clipboard went away")
        );

        assert_eq!(guard(-1, || 7), 7);
        assert_eq!(take_last_error(), None, "success leaves the slot alone");
    }

    #[test]
    fn test_string_conversion() {
        let rust_str = "Hello, FFI!".to_string();
        let c_str = string_to_c_str(rust_str.clone());
        assert!(!c_str.is_null());

        unsafe {
            let back_to_rust = c_str_to_string(c_str).unwrap();
            assert_eq!(back_to_rust, rust_str);

            let _ = CString::from_raw(c_str);
        }
    }
}

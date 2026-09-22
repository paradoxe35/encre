use std::ffi::c_char;
use std::os::raw::{c_float, c_int};
use std::path::PathBuf;
use std::sync::mpsc::channel;
use std::thread;

use parking_lot::Mutex;

use crate::ffi::ffi_types::{
    FFIErrorCode, SttHandle, c_str_to_string, init_logging, set_last_error, string_to_c_str,
};
use crate::stt::audio::{self, Recorder};

/// Microphone RMS level while recording, for a meter.
pub type LevelCallback = extern "C" fn(c_float);

pub struct SpeechRecogniser {
    recorder: Recorder,
    recording: Mutex<bool>,
}

impl SpeechRecogniser {
    fn new(level: LevelCallback) -> Self {
        let (tx, rx) = channel();

        // The host is called on this thread, never on the audio callback.
        thread::spawn(move || {
            for rms in rx {
                level(rms);
            }
        });

        Self {
            recorder: Recorder::spawn(tx),
            recording: Mutex::new(false),
        }
    }
}

fn recogniser<'a>(handle: SttHandle) -> Option<&'a SpeechRecogniser> {
    if handle.is_null() {
        set_last_error("Null speech handle provided".to_string());
        return None;
    }
    Some(unsafe { &*(handle as *mut SpeechRecogniser) })
}

#[unsafe(no_mangle)]
pub extern "C" fn encre_stt_new(level: LevelCallback) -> SttHandle {
    init_logging();
    Box::into_raw(Box::new(SpeechRecogniser::new(level))) as SttHandle
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_free(handle: SttHandle) {
    if handle.is_null() {
        return;
    }
    let recogniser = unsafe { Box::from_raw(handle as *mut SpeechRecogniser) };
    recogniser.recorder.shutdown();
}

/// Returns at once; a load failure is reported by the first transcription that needs it.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_use_model(handle: SttHandle, path: *const c_char) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    if path.is_null() {
        set_last_error("Null model path provided".to_string());
        return FFIErrorCode::NullPointer as c_int;
    }

    match unsafe { c_str_to_string(path) } {
        Ok(path) => {
            recogniser.recorder.use_model(PathBuf::from(path));
            FFIErrorCode::Success as c_int
        }
        Err(e) => {
            set_last_error(format!("Invalid model path: {e}"));
            FFIErrorCode::InvalidUtf8 as c_int
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_unload(handle: SttHandle) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    recogniser.recorder.unload();
    FFIErrorCode::Success as c_int
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_start(handle: SttHandle) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };

    let mut recording = recogniser.recording.lock();
    if *recording {
        set_last_error("Already recording".to_string());
        return FFIErrorCode::OperationFailed as c_int;
    }

    if let Err(e) = recogniser.recorder.start() {
        set_last_error(e.to_string());
        return FFIErrorCode::OperationFailed as c_int;
    }
    *recording = true;
    FFIErrorCode::Success as c_int
}

/// Blocks for as long as inference takes; call it off the UI thread.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_stop(handle: SttHandle) -> *mut c_char {
    let Some(recogniser) = recogniser(handle) else {
        return std::ptr::null_mut();
    };

    let stopped = match end_take(recogniser) {
        Ok(stopped) => stopped,
        Err(e) => {
            set_last_error(e.to_string());
            return std::ptr::null_mut();
        }
    };

    match stopped.text {
        Ok(Some(text)) => string_to_c_str(text),

        // Either nothing was heard, or streaming handed the audio back for a batch pass.
        Ok(None) => {
            if stopped.speech.is_empty() {
                return string_to_c_str(String::new());
            }
            match recogniser
                .recorder
                .transcribe(stopped.speech, stopped.language)
            {
                Ok(text) => string_to_c_str(text),
                Err(e) => {
                    set_last_error(e.to_string());
                    std::ptr::null_mut()
                }
            }
        }

        Err(message) => {
            set_last_error(message);
            std::ptr::null_mut()
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_cancel(handle: SttHandle) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };

    let mut recording = recogniser.recording.lock();
    recogniser.recorder.cancel();
    *recording = false;
    FFIErrorCode::Success as c_int
}

/// Stop is sent under the recording lock so no Start lands ahead of it; the wait
/// happens outside it, so the next take is not blocked by transcription.
fn end_take(recogniser: &SpeechRecogniser) -> anyhow::Result<audio::Stopped> {
    let pending = {
        let mut recording = recogniser.recording.lock();
        if !*recording {
            return Err(anyhow::anyhow!("Not recording"));
        }
        *recording = false;
        recogniser.recorder.begin_stop()?
    };
    Recorder::await_stop(pending)
}

/// Null and empty both read as `None`.
///
/// # Safety
/// A non-null `ptr` must be a valid null-terminated C string.
unsafe fn optional_string(ptr: *const c_char, what: &str) -> Result<Option<String>, c_int> {
    if ptr.is_null() {
        return Ok(None);
    }
    match unsafe { c_str_to_string(ptr) } {
        Ok(text) => Ok((!text.is_empty()).then_some(text)),
        Err(e) => {
            set_last_error(format!("Invalid {what}: {e}"));
            Err(FFIErrorCode::InvalidUtf8 as c_int)
        }
    }
}

/// Null or empty means the system default. Takes effect on the next recording.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_set_device(handle: SttHandle, name: *const c_char) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    match unsafe { optional_string(name, "device name") } {
        Ok(wanted) => {
            recogniser.recorder.set_device(wanted);
            FFIErrorCode::Success as c_int
        }
        Err(code) => code,
    }
}

/// ISO code; NULL or empty asks the model to detect the language, which only some can.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_set_language(handle: SttHandle, code: *const c_char) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    match unsafe { optional_string(code, "language code") } {
        Ok(wanted) => {
            recogniser.recorder.set_language(wanted);
            FFIErrorCode::Success as c_int
        }
        Err(code) => code,
    }
}

/// While on, a take never touches the engine: `encre_stt_stop` fails and audio is
/// read back with `encre_stt_stop_pcm`. Takes effect on the next recording.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_set_capture_only(handle: SttHandle, enabled: bool) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    recogniser.recorder.set_capture_only(enabled);
    FFIErrorCode::Success as c_int
}

/// Headerless 16-bit signed little-endian mono PCM at `encre_SAMPLE_RATE`; free with
/// `encre_stt_free_bytes`. Null on failure; a silent take is a valid zero-length buffer.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_stop_pcm(handle: SttHandle, out_len: *mut usize) -> *mut u8 {
    let Some(recogniser) = recogniser(handle) else {
        return std::ptr::null_mut();
    };
    if out_len.is_null() {
        set_last_error("Null length pointer provided".to_string());
        return std::ptr::null_mut();
    }

    let stopped = match end_take(recogniser) {
        Ok(stopped) => stopped,
        Err(e) => {
            set_last_error(e.to_string());
            return std::ptr::null_mut();
        }
    };

    let mut bytes = pcm16_bytes(&stopped.speech.samples);
    bytes.shrink_to_fit();
    let len = bytes.len();
    let ptr = bytes.as_mut_ptr();
    std::mem::forget(bytes);
    unsafe {
        *out_len = len;
    }
    ptr
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_free_bytes(ptr: *mut u8, len: usize) {
    if ptr.is_null() {
        return;
    }
    unsafe {
        drop(Vec::from_raw_parts(ptr, len, len));
    }
}

fn pcm16_bytes(samples: &[f32]) -> Vec<u8> {
    let mut bytes = Vec::with_capacity(samples.len() * 2);
    for &s in samples {
        let sample = (s.clamp(-1.0, 1.0) * i16::MAX as f32) as i16;
        bytes.extend_from_slice(&sample.to_le_bytes());
    }
    bytes
}

/// Input device names, newline separated, the default marked with a leading '*'.
#[unsafe(no_mangle)]
pub extern "C" fn encre_stt_devices() -> *mut c_char {
    let (devices, default) = audio::devices();

    let listed = devices
        .into_iter()
        .map(|name| {
            if Some(&name) == default.as_ref() {
                format!("*{name}")
            } else {
                name
            }
        })
        .collect::<Vec<_>>()
        .join("\n");

    string_to_c_str(listed)
}

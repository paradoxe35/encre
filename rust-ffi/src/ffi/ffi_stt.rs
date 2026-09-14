use std::ffi::c_char;
use std::os::raw::{c_float, c_int};
use std::path::PathBuf;
use std::sync::mpsc::channel;
use std::thread;

use parking_lot::Mutex;

use crate::ffi::ffi_types::{
    FFIErrorCode, SttHandle, c_str_to_string, set_last_error, string_to_c_str,
};
use crate::stt::audio::{self, Recorder};

/// Reports microphone level while recording, so the host can draw a meter.
pub type LevelCallback = extern "C" fn(c_float);

pub struct SpeechRecogniser {
    recorder: Recorder,
    recording: Mutex<bool>,
}

impl SpeechRecogniser {
    fn new(level: LevelCallback) -> Self {
        let (tx, rx) = channel();

        // Levels arrive far faster than a UI can use them; the host is called
        // on this thread, never on the audio callback.
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

/// Loads a model and keeps it resident. Idempotent for the same path, so the
/// host may call it on every dictation.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_load(handle: SttHandle, path: *const c_char) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    if path.is_null() {
        set_last_error("Null model path provided".to_string());
        return FFIErrorCode::NullPointer as c_int;
    }

    let path = match unsafe { c_str_to_string(path) } {
        Ok(path) => path,
        Err(e) => {
            set_last_error(format!("Invalid model path: {e}"));
            return FFIErrorCode::InvalidUtf8 as c_int;
        }
    };

    match recogniser.recorder.load(PathBuf::from(&path)) {
        Ok(_) => FFIErrorCode::Success as c_int,
        Err(e) => {
            set_last_error(e.to_string());
            FFIErrorCode::OperationFailed as c_int
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

    recogniser.recorder.start();
    *recording = true;
    FFIErrorCode::Success as c_int
}

/// Stops recording and transcribes. Blocks for as long as inference takes, so
/// the host must call it off its UI thread.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_stop(handle: SttHandle) -> *mut c_char {
    let Some(recogniser) = recogniser(handle) else {
        return std::ptr::null_mut();
    };

    {
        let mut recording = recogniser.recording.lock();
        if !*recording {
            set_last_error("Not recording".to_string());
            return std::ptr::null_mut();
        }
        *recording = false;
    }

    let stopped = match recogniser.recorder.stop() {
        Ok(stopped) => stopped,
        Err(e) => {
            set_last_error(e.to_string());
            return std::ptr::null_mut();
        }
    };

    match stopped.text {
        Ok(Some(text)) => string_to_c_str(text),

        // No transcript. Either the take held no speech, or streaming gave up
        // and handed back the audio for one batch pass.
        Ok(None) => {
            if stopped.samples.is_empty() {
                return string_to_c_str(String::new());
            }
            match recogniser.recorder.transcribe_samples(stopped.samples) {
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

    *recogniser.recording.lock() = false;
    recogniser.recorder.cancel();
    FFIErrorCode::Success as c_int
}

/// Transcribes a 16 kHz mono WAV without touching the microphone, so a model
/// can be verified from settings.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_transcribe_file(
    handle: SttHandle,
    path: *const c_char,
) -> *mut c_char {
    let Some(recogniser) = recogniser(handle) else {
        return std::ptr::null_mut();
    };
    if path.is_null() {
        set_last_error("Null audio path provided".to_string());
        return std::ptr::null_mut();
    }

    let path = match unsafe { c_str_to_string(path) } {
        Ok(path) => path,
        Err(e) => {
            set_last_error(format!("Invalid audio path: {e}"));
            return std::ptr::null_mut();
        }
    };

    match recogniser.recorder.transcribe_file(PathBuf::from(&path)) {
        Ok(text) => string_to_c_str(text),
        Err(e) => {
            set_last_error(e.to_string());
            std::ptr::null_mut()
        }
    }
}

/// Selects the capture device by name. Null or empty means the system default.
/// Takes effect on the next recording.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_set_device(handle: SttHandle, name: *const c_char) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };

    let wanted = if name.is_null() {
        None
    } else {
        match unsafe { c_str_to_string(name) } {
            Ok(name) if !name.is_empty() => Some(name),
            Ok(_) => None,
            Err(e) => {
                set_last_error(format!("Invalid device name: {e}"));
                return FFIErrorCode::InvalidUtf8 as c_int;
            }
        }
    };

    recogniser.recorder.set_device(wanted);
    FFIErrorCode::Success as c_int
}

/// Sets the spoken language as an ISO code; NULL or empty asks the model to
/// detect, which only some can.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_set_language(handle: SttHandle, code: *const c_char) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };

    let wanted = if code.is_null() {
        None
    } else {
        match unsafe { c_str_to_string(code) } {
            Ok(code) if !code.is_empty() => Some(code),
            Ok(_) => None,
            Err(e) => {
                set_last_error(format!("Invalid language code: {e}"));
                return FFIErrorCode::InvalidUtf8 as c_int;
            }
        }
    };

    recogniser.recorder.set_language(wanted);
    FFIErrorCode::Success as c_int
}

/// Enables or disables capture-only mode: while on, recording never touches the
/// engine, so `encre_stt_stop` fails and audio must be read back with
/// `encre_stt_stop_pcm`. Takes effect on the next recording.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_set_capture_only(handle: SttHandle, enabled: bool) -> c_int {
    let Some(recogniser) = recogniser(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    recogniser.recorder.set_capture_only(enabled);
    FFIErrorCode::Success as c_int
}

/// Stops a capture-only recording and returns the audio as headerless 16-bit
/// signed little-endian PCM, mono, at `encre_SAMPLE_RATE`. Free with
/// `encre_stt_free_bytes`. Null on failure; a silent take returns a valid
/// zero-length buffer.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_stop_pcm(handle: SttHandle, out_len: *mut usize) -> *mut u8 {
    let Some(recogniser) = recogniser(handle) else {
        return std::ptr::null_mut();
    };
    if out_len.is_null() {
        set_last_error("Null length pointer provided".to_string());
        return std::ptr::null_mut();
    }

    {
        let mut recording = recogniser.recording.lock();
        if !*recording {
            set_last_error("Not recording".to_string());
            return std::ptr::null_mut();
        }
        *recording = false;
    }

    let stopped = match recogniser.recorder.stop() {
        Ok(stopped) => stopped,
        Err(e) => {
            set_last_error(e.to_string());
            return std::ptr::null_mut();
        }
    };

    let mut bytes = pcm16_bytes(&stopped.samples);
    bytes.shrink_to_fit();
    let len = bytes.len();
    let ptr = bytes.as_mut_ptr();
    std::mem::forget(bytes);
    unsafe {
        *out_len = len;
    }
    ptr
}

/// Frees a buffer returned by `encre_stt_stop_pcm`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_stt_free_bytes(ptr: *mut u8, len: usize) {
    if ptr.is_null() {
        return;
    }
    unsafe {
        drop(Vec::from_raw_parts(ptr, len, len));
    }
}

/// Converts samples in [-1.0, 1.0] to headerless 16-bit signed little-endian PCM.
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

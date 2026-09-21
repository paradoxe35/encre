use std::os::raw::c_int;

use anyhow::Result;

use super::ffi_types::*;
use crate::core::KeySimulator;

fn simulator<'a>(handle: SimulatorHandle) -> Option<&'a mut KeySimulator> {
    if handle.is_null() {
        set_last_error("Null simulator handle provided".to_string());
        return None;
    }
    Some(unsafe { &mut *(handle as *mut KeySimulator) })
}

fn outcome(result: Result<()>, failure: &str) -> c_int {
    match result {
        Ok(()) => FFIErrorCode::Success as c_int,
        Err(e) => {
            set_last_error(format!("{failure}: {:#}", e));
            FFIErrorCode::OperationFailed as c_int
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulator_new() -> SimulatorHandle {
    init_logging();

    match KeySimulator::new() {
        Ok(simulator) => Box::into_raw(Box::new(simulator)) as SimulatorHandle,
        Err(e) => {
            set_last_error(format!("Failed to create key simulator: {:#}", e));
            std::ptr::null_mut()
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulate_select_all(handle: SimulatorHandle) -> c_int {
    let Some(simulator) = simulator(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    outcome(simulator.select_all(), "Select all simulation failed")
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulate_copy(handle: SimulatorHandle) -> c_int {
    let Some(simulator) = simulator(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    outcome(simulator.copy(), "Copy simulation failed")
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulate_paste(handle: SimulatorHandle) -> c_int {
    let Some(simulator) = simulator(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    outcome(simulator.paste(), "Paste simulation failed")
}

/// Ctrl+Shift+V, the paste chord terminals bind. Cmd+V on macOS, like `encre_simulate_paste`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulate_paste_terminal(handle: SimulatorHandle) -> c_int {
    let Some(simulator) = simulator(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    outcome(
        simulator.paste_terminal(),
        "Terminal paste simulation failed",
    )
}

/// Drops modifiers the triggering hotkey left down. Call once before any combo.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulate_release_modifiers(handle: SimulatorHandle) -> c_int {
    let Some(simulator) = simulator(handle) else {
        return FFIErrorCode::NullPointer as c_int;
    };
    outcome(simulator.release_modifiers(), "Releasing modifiers failed")
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulator_free(handle: SimulatorHandle) {
    unsafe {
        if !handle.is_null() {
            let _ = Box::from_raw(handle as *mut KeySimulator);
        }
    }
}

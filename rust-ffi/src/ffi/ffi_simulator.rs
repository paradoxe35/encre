use std::os::raw::c_int;

use super::ffi_types::*;
use crate::core::KeySimulator;

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
    unsafe {
        if handle.is_null() {
            set_last_error("Null simulator handle provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        let simulator = &mut *(handle as *mut KeySimulator);

        match simulator.select_all() {
            Ok(_) => FFIErrorCode::Success as c_int,
            Err(e) => {
                set_last_error(format!("Select all simulation failed: {:#}", e));
                FFIErrorCode::OperationFailed as c_int
            }
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulate_copy(handle: SimulatorHandle) -> c_int {
    unsafe {
        if handle.is_null() {
            set_last_error("Null simulator handle provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        let simulator = &mut *(handle as *mut KeySimulator);

        match simulator.copy() {
            Ok(_) => FFIErrorCode::Success as c_int,
            Err(e) => {
                set_last_error(format!("Copy simulation failed: {:#}", e));
                FFIErrorCode::OperationFailed as c_int
            }
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulate_paste(handle: SimulatorHandle) -> c_int {
    unsafe {
        if handle.is_null() {
            set_last_error("Null simulator handle provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        let simulator = &mut *(handle as *mut KeySimulator);

        match simulator.paste() {
            Ok(_) => FFIErrorCode::Success as c_int,
            Err(e) => {
                set_last_error(format!("Paste simulation failed: {:#}", e));
                FFIErrorCode::OperationFailed as c_int
            }
        }
    }
}

/// Drops modifiers the triggering hotkey left down. Call once before any combo.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulate_release_modifiers(handle: SimulatorHandle) -> c_int {
    unsafe {
        if handle.is_null() {
            set_last_error("Null simulator handle provided".to_string());
            return FFIErrorCode::NullPointer as c_int;
        }

        let simulator = &mut *(handle as *mut KeySimulator);

        match simulator.release_modifiers() {
            Ok(_) => FFIErrorCode::Success as c_int,
            Err(e) => {
                set_last_error(format!("Releasing modifiers failed: {:#}", e));
                FFIErrorCode::OperationFailed as c_int
            }
        }
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn encre_simulator_free(handle: SimulatorHandle) {
    unsafe {
        if !handle.is_null() {
            let _ = Box::from_raw(handle as *mut KeySimulator);
        }
    }
}

//go:build linux || darwin || windows

package input

/*
#cgo CFLAGS: -I${SRCDIR}/../../rust-ffi

#cgo linux LDFLAGS: ${SRCDIR}/../../lib/libencre_ffi.a -lpthread -ldl -lm -lxdo -lX11 -lXtst -lXi -lxkbcommon

#cgo darwin LDFLAGS: ${SRCDIR}/../../lib/libencre_ffi.a
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security -framework AppKit -framework ApplicationServices -framework Carbon

#cgo windows LDFLAGS: ${SRCDIR}/../../lib/libencre_ffi.a
#cgo windows LDFLAGS: -lws2_32 -luserenv -lbcrypt -lntdll -static

#include <stdlib.h>
#include "bindings.h"

extern void hotkeyCallbackGateway(char* action);
*/
import "C"
import (
	"fmt"
	"sync"
	"time"

	"unsafe"

	"github.com/paradoxe35/encre/internal/logger"
)

type FFIHotkeyManager struct {
	// ffiMu guards handle for its whole read-then-call-C sequence in every method, so a freed
	// handle can never reach Rust.
	ffiMu  sync.Mutex
	handle C.encre_HotkeyManagerHandle

	// mu guards Go-side state that never touches handle.
	mu          sync.Mutex
	handlers    map[string]func()
	active      bool
	disabled    bool
	lastTrigger map[string]time.Time
}

// Rust callbacks carry no context, so routing goes through a global.
var globalFFIHotkeyManager *FFIHotkeyManager
var globalFFIMu sync.Mutex

func NewFFIHotkeyManager() *FFIHotkeyManager {
	handle := C.encre_hotkey_manager_new()
	if handle == nil {
		logger.Error("Failed to create FFI hotkey manager")
		return nil
	}

	manager := &FFIHotkeyManager{
		handle:      handle,
		handlers:    make(map[string]func()),
		lastTrigger: make(map[string]time.Time),
	}

	globalFFIMu.Lock()
	globalFFIHotkeyManager = manager
	globalFFIMu.Unlock()

	return manager
}

func (h *FFIHotkeyManager) ClearBindings() error {
	h.ffiMu.Lock()
	defer h.ffiMu.Unlock()

	if h.handle == nil {
		return fmt.Errorf("hotkey manager not initialized")
	}

	h.mu.Lock()
	h.handlers = make(map[string]func())
	h.mu.Unlock()

	result := C.encre_hotkey_clear(h.handle)
	if result != 0 {
		return fmt.Errorf("failed to clear hotkey bindings: %s", getLastError())
	}

	logger.Info("FFI: Hotkey bindings cleared")
	return nil
}

func (h *FFIHotkeyManager) RegisterHotkey(binding, action string, handler func()) error {
	h.ffiMu.Lock()
	defer h.ffiMu.Unlock()

	if h.handle == nil {
		return fmt.Errorf("hotkey manager not initialized")
	}

	h.mu.Lock()
	h.handlers[action] = handler
	h.mu.Unlock()

	cBinding := C.CString(binding)
	cAction := C.CString(action)
	defer C.free(unsafe.Pointer(cBinding))
	defer C.free(unsafe.Pointer(cAction))

	result := C.encre_hotkey_register(
		h.handle,
		cBinding,
		cAction,
		C.encre_HotkeyCallback(C.hotkeyCallbackGateway),
	)

	if result != 0 {
		return fmt.Errorf("failed to register hotkey '%s': %s", binding, getLastError())
	}

	logger.Info("FFI: Hotkey registered", "binding", binding, "action", action)
	return nil
}

// ListenError reports why the listener is not running, or "" when it is. Start only spawns the
// thread; the system refuses the key tap afterwards, so a successful start proves nothing.
func (h *FFIHotkeyManager) ListenError() string {
	h.ffiMu.Lock()
	defer h.ffiMu.Unlock()

	if h.handle == nil {
		return ""
	}

	cStr := C.encre_hotkey_listen_error(h.handle)
	if cStr == nil {
		return ""
	}
	defer C.encre_free_string(cStr)

	return C.GoString(cStr)
}

func (h *FFIHotkeyManager) Start() error {
	h.mu.Lock()
	if h.active {
		h.mu.Unlock()
		return fmt.Errorf("hotkey manager already active")
	}
	h.mu.Unlock()

	h.ffiMu.Lock()
	if h.handle == nil {
		h.ffiMu.Unlock()
		return fmt.Errorf("hotkey manager not initialized")
	}

	logger.Info("FFI: Starting hotkey manager")
	result := C.encre_hotkey_start(h.handle)
	h.ffiMu.Unlock()

	if result != 0 {
		return fmt.Errorf("failed to start hotkey manager: %s", getLastError())
	}

	h.mu.Lock()
	h.active = true
	h.mu.Unlock()

	logger.Info("FFI: Hotkey manager started")
	return nil
}

func (h *FFIHotkeyManager) Stop() {
	h.mu.Lock()
	if !h.active {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()

	h.ffiMu.Lock()
	if h.handle == nil {
		h.ffiMu.Unlock()
		return
	}

	logger.Info("FFI: Stopping hotkey manager")
	result := C.encre_hotkey_stop(h.handle)
	h.ffiMu.Unlock()

	if result != 0 {
		logger.Error("FFI: Failed to stop hotkey manager", "error", getLastError())
	}

	h.mu.Lock()
	h.active = false
	h.mu.Unlock()

	logger.Info("FFI: Hotkey manager stopped")
}

func (h *FFIHotkeyManager) Close() {
	h.Stop()

	h.ffiMu.Lock()
	if h.handle != nil {
		logger.Info("FFI: Freeing hotkey manager resources")
		C.encre_hotkey_manager_free(h.handle)
		h.handle = nil
	}
	h.ffiMu.Unlock()

	h.mu.Lock()
	h.handlers = make(map[string]func())
	h.mu.Unlock()

	globalFFIMu.Lock()
	if globalFFIHotkeyManager == h {
		globalFFIHotkeyManager = nil
	}
	globalFFIMu.Unlock()

	logger.Info("FFI: Hotkey manager closed")
}

func (h *FFIHotkeyManager) Disable() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.disabled = true
	logger.Info("FFI: Hotkeys disabled (Go-level gate)")
}

func (h *FFIHotkeyManager) Enable() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.disabled = false
	logger.Info("FFI: Hotkeys enabled (Go-level gate)")
}

//export hotkeyCallbackGateway
func hotkeyCallbackGateway(action *C.char) {
	if action == nil {
		return
	}
	// Lent for the duration of this call, not handed over: Rust frees it when the callback
	// returns, so this copies and must not free it.
	actionStr := C.GoString(action)

	globalFFIMu.Lock()
	manager := globalFFIHotkeyManager
	globalFFIMu.Unlock()

	if manager == nil {
		return
	}

	manager.mu.Lock()
	if manager.disabled {
		manager.mu.Unlock()
		return
	}
	if t, ok := manager.lastTrigger[actionStr]; ok {
		if time.Since(t) < 500*time.Millisecond {
			manager.mu.Unlock()
			return
		}
	}
	manager.lastTrigger[actionStr] = time.Now()
	handler, exists := manager.handlers[actionStr]
	manager.mu.Unlock()

	if !exists || handler == nil {
		return
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Error("FFI: Panic in hotkey handler", "action", actionStr, "panic", r)
			}
		}()
		handler()
	}()
}

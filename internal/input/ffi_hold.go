//go:build linux || darwin || windows

package input

/*
#include <stdlib.h>
#include "bindings.h"

extern void holdCallbackGateway(char* action, int down);
*/
import "C"
import (
	"fmt"
	"sync"
	"time"
	"unsafe"

	"github.com/paradoxe35/encre/internal/logger"
)

// Under X11 a held key repeats as release/press pairs. Releases are deferred this long and
// cancelled by a matching press, so auto-repeat doesn't read as the user letting go.
const releaseGrace = 50 * time.Millisecond

type HoldHandler func(down bool)

type holdBinding struct {
	handler HoldHandler
	down    bool
	release *time.Timer
}

var (
	holdMu       sync.Mutex
	holdBindings = map[string]*holdBinding{}
)

// The binding must name a real key: a modifier-only chord cannot be held.
func (m *FFIHotkeyManager) RegisterHoldHotkey(binding, action string, handler HoldHandler) error {
	if m == nil {
		return fmt.Errorf("hotkey manager not initialized")
	}

	rememberHold(action, handler)

	cBinding := C.CString(binding)
	defer C.free(unsafe.Pointer(cBinding))
	cAction := C.CString(action)
	defer C.free(unsafe.Pointer(cAction))

	m.ffiMu.Lock()
	if m.handle == nil {
		m.ffiMu.Unlock()
		forgetHold(action)
		return fmt.Errorf("hotkey manager not initialized")
	}
	result := C.encre_hotkey_register_hold(
		m.handle, cBinding, cAction,
		C.encre_PttCallback(C.holdCallbackGateway),
	)
	m.ffiMu.Unlock()

	if result != 0 {
		forgetHold(action)
		return fmt.Errorf("failed to register %s: %s", binding, getLastError())
	}

	logger.Info("Registered push-to-talk hotkey", "binding", binding, "action", action)
	return nil
}

// rememberHold keeps a hold already in progress, so a config reload mid-press still gets its release.
func rememberHold(action string, handler HoldHandler) {
	holdMu.Lock()
	defer holdMu.Unlock()

	if binding, ok := holdBindings[action]; ok {
		binding.handler = handler
		return
	}
	holdBindings[action] = &holdBinding{handler: handler}
}

// forgetHold drops the binding. A hold still in progress is released first: no up edge will come for it.
func forgetHold(action string) {
	holdMu.Lock()
	binding, ok := holdBindings[action]
	delete(holdBindings, action)
	holdMu.Unlock()

	if !ok {
		return
	}
	if binding.release != nil {
		binding.release.Stop()
	}
	if binding.down {
		go safely(action, func() { binding.handler(false) })
	}
}

// ClearHoldBindings drops the Go-side handlers; the Rust bindings are cleared by ClearBindings.
func ClearHoldBindings() {
	holdMu.Lock()
	defer holdMu.Unlock()

	for _, binding := range holdBindings {
		if binding.release != nil {
			binding.release.Stop()
		}
	}
	holdBindings = map[string]*holdBinding{}
}

//export holdCallbackGateway
func holdCallbackGateway(action *C.char, down C.int) {
	name := C.GoString(action)
	if down != 0 {
		dispatchHoldDown(name)
		return
	}
	dispatchHoldUp(name)
}

func dispatchHoldDown(action string) {
	holdMu.Lock()
	binding, ok := holdBindings[action]
	if !ok {
		holdMu.Unlock()
		return
	}

	if binding.release != nil {
		binding.release.Stop()
		binding.release = nil
	}
	if binding.down {
		holdMu.Unlock()
		return
	}

	binding.down = true
	handler := binding.handler
	holdMu.Unlock()

	go safely(action, func() { handler(true) })
}

func dispatchHoldUp(action string) {
	holdMu.Lock()
	binding, ok := holdBindings[action]
	if !ok || !binding.down {
		holdMu.Unlock()
		return
	}

	if binding.release != nil {
		binding.release.Stop()
	}
	binding.release = time.AfterFunc(releaseGrace, func() { settleHoldUp(action) })
	holdMu.Unlock()
}

func settleHoldUp(action string) {
	holdMu.Lock()
	binding, ok := holdBindings[action]
	if !ok || !binding.down {
		holdMu.Unlock()
		return
	}

	binding.down = false
	binding.release = nil
	handler := binding.handler
	holdMu.Unlock()

	go safely(action, func() { handler(false) })
}

func safely(action string, run func()) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("Push-to-talk handler panicked", "action", action, "panic", r)
		}
	}()
	run()
}

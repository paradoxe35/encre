//go:build linux || darwin || windows

package input

/*
// ggml (C++) and cpal (platform audio) link flags live here; cgo unions LDFLAGS across the package.
#cgo linux LDFLAGS: -lstdc++ -lasound
// Accelerate: ggml-cpu calls vDSP directly under GGML_USE_ACCELERATE, but its own link
// manifest only requests the framework when BLAS is on, which this build disables.
#cgo darwin LDFLAGS: -lc++ -framework Accelerate -framework AudioToolbox -framework CoreAudio -framework AudioUnit
#cgo windows LDFLAGS: -lstdc++ -lole32 -lavrt

#include <stdlib.h>
#include "bindings.h"

extern void speechLevelGateway(float rms);
*/
import "C"
import (
	"fmt"
	"sync"
	"unsafe"
)

// mu is shared: Rust orders Start, Stop and Cancel itself, and an exclusive lock would make
// the next take wait on the previous transcription. Close takes it exclusively so the handle
// stays alive under a call.
type FFISpeech struct {
	mu     sync.RWMutex
	handle C.encre_SttHandle
}

var (
	levelMu      sync.RWMutex
	levelHandler func(float32)
)

func NewFFISpeech() (*FFISpeech, error) {
	handle := C.encre_stt_new(C.encre_LevelCallback(C.speechLevelGateway))
	if handle == nil {
		return nil, fmt.Errorf("failed to create speech recogniser: %s", getLastError())
	}
	return &FFISpeech{handle: handle}, nil
}

// The handler runs on a background thread while recording.
func OnLevel(handler func(float32)) {
	levelMu.Lock()
	defer levelMu.Unlock()
	levelHandler = handler
}

//export speechLevelGateway
func speechLevelGateway(rms C.float) {
	levelMu.RLock()
	handler := levelHandler
	levelMu.RUnlock()

	if handler != nil {
		handler(float32(rms))
	}
}

// Starts loading; a load failure is reported by the first Stop that needs the model.
func (s *FFISpeech) UseModel(path string) error {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	s.mu.RLock()
	result := C.encre_stt_use_model(s.handle, cPath)
	s.mu.RUnlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

func (s *FFISpeech) Unload() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	C.encre_stt_unload(s.handle)
}

func (s *FFISpeech) Start() error {
	s.mu.RLock()
	result := C.encre_stt_start(s.handle)
	s.mu.RUnlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

// Blocks for the length of transcription; not for the UI goroutine.
func (s *FFISpeech) Stop() (string, error) {
	s.mu.RLock()
	text := C.encre_stt_stop(s.handle)
	s.mu.RUnlock()

	return takeString(text)
}

func (s *FFISpeech) Cancel() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	C.encre_stt_cancel(s.handle)
}

func (s *FFISpeech) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.handle != nil {
		C.encre_stt_free(s.handle)
		s.handle = nil
	}
}

func takeString(text *C.char) (string, error) {
	if text == nil {
		return "", fmt.Errorf("%s", getLastError())
	}
	defer C.encre_free_string(text)
	return C.GoString(text), nil
}

// Empty means the system default. Applies to the next recording, not one in progress.
func (s *FFISpeech) SetDevice(name string) error {
	var cName *C.char
	if name != "" {
		cName = C.CString(name)
		defer C.free(unsafe.Pointer(cName))
	}

	s.mu.RLock()
	result := C.encre_stt_set_device(s.handle, cName)
	s.mu.RUnlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

// Empty means detect. Applies to the next recording, not one in progress.
func (s *FFISpeech) SetLanguage(code string) error {
	var cCode *C.char
	if code != "" {
		cCode = C.CString(code)
		defer C.free(unsafe.Pointer(cCode))
	}

	s.mu.RLock()
	result := C.encre_stt_set_language(s.handle, cCode)
	s.mu.RUnlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

// Capture-only keeps audio away from the engine, for a remote transcriber that needs the raw
// take. Applies to the next recording.
func (s *FFISpeech) SetCaptureOnly(enabled bool) error {
	s.mu.RLock()
	result := C.encre_stt_set_capture_only(s.handle, C.bool(enabled))
	s.mu.RUnlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

// Returns headerless 16-bit signed little-endian PCM, mono, at 16 kHz.
func (s *FFISpeech) StopPCM() ([]byte, error) {
	var length C.uintptr_t

	s.mu.RLock()
	ptr := C.encre_stt_stop_pcm(s.handle, &length)
	s.mu.RUnlock()

	if ptr == nil {
		return nil, fmt.Errorf("%s", getLastError())
	}
	defer C.encre_stt_free_bytes(ptr, length)

	return C.GoBytes(unsafe.Pointer(ptr), C.int(length)), nil
}

// An empty result means none were found, not that enumeration failed.
func InputDevices() []Device {
	listed := C.encre_stt_devices()
	if listed == nil {
		return nil
	}
	defer C.encre_free_string(listed)

	return parseDevices(C.GoString(listed))
}

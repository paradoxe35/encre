//go:build linux || darwin || windows

package input

/*
// Speech pulls in ggml (C++) and cpal (platform audio API); cgo unions LDFLAGS across the
// package, so they live here instead of being repeated per file.
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

// FFISpeech records from the microphone and transcribes locally. Every call
// beyond Level runs on the caller's goroutine; Stop blocks for as long as
// inference takes.
type FFISpeech struct {
	mu     sync.Mutex
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

// OnLevel receives microphone level from a background thread while recording; replaces any previous handler.
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

// Load keeps a model resident. Idempotent for the same path.
func (s *FFISpeech) Load(path string) error {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	s.mu.Lock()
	result := C.encre_stt_load(s.handle, cPath)
	s.mu.Unlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

func (s *FFISpeech) Unload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	C.encre_stt_unload(s.handle)
}

func (s *FFISpeech) Start() error {
	s.mu.Lock()
	result := C.encre_stt_start(s.handle)
	s.mu.Unlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

// Stop ends recording and returns the transcript; blocks for the length of transcription, so
// callers should not run it on the UI goroutine.
func (s *FFISpeech) Stop() (string, error) {
	s.mu.Lock()
	text := C.encre_stt_stop(s.handle)
	s.mu.Unlock()

	return takeString(text)
}

func (s *FFISpeech) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	C.encre_stt_cancel(s.handle)
}

// TranscribeFile reads a 16 kHz mono WAV, for verifying a model without a microphone.
func (s *FFISpeech) TranscribeFile(path string) (string, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))

	s.mu.Lock()
	text := C.encre_stt_transcribe_file(s.handle, cPath)
	s.mu.Unlock()

	return takeString(text)
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

// SetDevice chooses the capture device by name (empty means system default); applies to the next recording, not one in progress.
func (s *FFISpeech) SetDevice(name string) error {
	var cName *C.char
	if name != "" {
		cName = C.CString(name)
		defer C.free(unsafe.Pointer(cName))
	}

	s.mu.Lock()
	result := C.encre_stt_set_device(s.handle, cName)
	s.mu.Unlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

// SetLanguage sets the spoken language as an ISO code, empty to detect; it
// applies to the next recording, not one in progress.
func (s *FFISpeech) SetLanguage(code string) error {
	var cCode *C.char
	if code != "" {
		cCode = C.CString(code)
		defer C.free(unsafe.Pointer(cCode))
	}

	s.mu.Lock()
	result := C.encre_stt_set_language(s.handle, cCode)
	s.mu.Unlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

// SetCaptureOnly toggles capture-only recording: audio is captured but never handed to the
// engine, for a remote transcriber that needs the raw take. Applies to the next recording.
func (s *FFISpeech) SetCaptureOnly(enabled bool) error {
	s.mu.Lock()
	result := C.encre_stt_set_capture_only(s.handle, C.bool(enabled))
	s.mu.Unlock()

	if result != 0 {
		return fmt.Errorf("%s", getLastError())
	}
	return nil
}

// StopPCM ends a capture-only recording and returns the audio as headerless
// 16-bit signed little-endian PCM, mono, at 16 kHz.
func (s *FFISpeech) StopPCM() ([]byte, error) {
	var length C.uintptr_t

	s.mu.Lock()
	ptr := C.encre_stt_stop_pcm(s.handle, &length)
	s.mu.Unlock()

	if ptr == nil {
		return nil, fmt.Errorf("%s", getLastError())
	}
	defer C.encre_stt_free_bytes(ptr, length)

	return C.GoBytes(unsafe.Pointer(ptr), C.int(length)), nil
}

// InputDevices lists microphones; an empty result means none were found, not that enumeration failed.
func InputDevices() []Device {
	listed := C.encre_stt_devices()
	if listed == nil {
		return nil
	}
	defer C.encre_free_string(listed)

	return parseDevices(C.GoString(listed))
}

//go:build linux || darwin || windows

package input

/*
#cgo CFLAGS: -I${SRCDIR}/../../rust-ffi

#cgo linux LDFLAGS: ${SRCDIR}/../../lib/libencre_ffi.a -lpthread -ldl -lm -lxdo -lX11 -lXtst -lxkbcommon

#cgo darwin LDFLAGS: ${SRCDIR}/../../lib/libencre_ffi.a
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security -framework AppKit -framework Carbon

#cgo windows LDFLAGS: ${SRCDIR}/../../lib/libencre_ffi.a
#cgo windows LDFLAGS: -lws2_32 -luserenv -lbcrypt -lntdll -static

#include <stdlib.h>
#include "bindings.h"
*/
import "C"
import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"github.com/paradoxe35/encre/internal/logger"
)

// Distinguishes how a capture ended, so the user gets an accurate message.
type CaptureOutcome int

const (
	CaptureOK CaptureOutcome = iota
	// CaptureNothingSelected: the copy landed, but there was nothing selected.
	CaptureNothingSelected
	// CaptureCopyFailed: the copy never took effect - no permission, a slow app, a refusing compositor.
	CaptureCopyFailed
)

const (
	clipboardPollInterval = 15 * time.Millisecond
	clipboardCopyTimeout  = 900 * time.Millisecond
	clipboardPasteSettle  = 220 * time.Millisecond
)

type FFIClipboardManager struct {
	handle C.encre_ClipboardHandle
}

func NewFFIClipboardManager() (*FFIClipboardManager, error) {
	handle := C.encre_clipboard_new()
	if handle == nil {
		return nil, fmt.Errorf("failed to create clipboard manager: %s", getLastError())
	}

	return &FFIClipboardManager{handle: handle}, nil
}

// text reads the clipboard; ok is false when it holds no text (empty or an image, not a failure).
func (c *FFIClipboardManager) text() (string, bool) {
	if c.handle == nil {
		return "", false
	}

	cStr := C.encre_clipboard_get_text(c.handle)
	if cStr == nil {
		return "", false
	}
	defer C.encre_free_string(cStr)

	text := C.GoString(cStr)
	return text, text != ""
}

func (c *FFIClipboardManager) GetText() (string, error) {
	if c.handle == nil {
		return "", fmt.Errorf("clipboard manager not initialized")
	}
	if text, ok := c.text(); ok {
		return text, nil
	}
	return "", fmt.Errorf("clipboard holds no text")
}

// Clear empties the clipboard, which is what makes a following copy landing observable.
func (c *FFIClipboardManager) Clear() error {
	if c.handle == nil {
		return fmt.Errorf("clipboard manager not initialized")
	}

	if result := C.encre_clipboard_clear(c.handle); result != 0 {
		return fmt.Errorf("failed to clear clipboard: %s", getLastError())
	}

	return nil
}

// await polls until read answers or the deadline passes, rather than a fixed sleep that's
// wrong for either a fast or a slow application.
func await(read func() (string, bool)) (string, bool) {
	deadline := time.Now().Add(clipboardCopyTimeout)
	for {
		if text, ok := read(); ok {
			return text, true
		}
		if time.Now().After(deadline) {
			return "", false
		}
		time.Sleep(clipboardPollInterval)
	}
}

func (c *FFIClipboardManager) SetText(text string) error {
	if c.handle == nil {
		return fmt.Errorf("clipboard manager not initialized")
	}

	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	result := C.encre_clipboard_set_text(c.handle, cText)
	if result != 0 {
		return fmt.Errorf("failed to set clipboard text: %s", getLastError())
	}

	return nil
}

func (c *FFIClipboardManager) SaveCurrent() error {
	if c.handle == nil {
		return fmt.Errorf("clipboard manager not initialized")
	}

	result := C.encre_clipboard_save(c.handle)
	if result != 0 {
		return fmt.Errorf("failed to save clipboard: %s", getLastError())
	}

	return nil
}

func (c *FFIClipboardManager) Restore() error {
	if c.handle == nil {
		return fmt.Errorf("clipboard manager not initialized")
	}

	result := C.encre_clipboard_restore(c.handle)
	if result != 0 {
		return fmt.Errorf("failed to restore clipboard: %s", getLastError())
	}

	return nil
}

func (c *FFIClipboardManager) Close() {
	if c.handle != nil {
		C.encre_clipboard_free(c.handle)
		c.handle = nil
	}
}

func (c *FFIClipboardManager) CaptureSelection() (string, CaptureOutcome, error) {
	return c.capture(false)
}

func (c *FFIClipboardManager) CaptureAll() (string, CaptureOutcome, error) {
	return c.capture(true)
}

// Clears the clipboard before copying so a failed copy is observable rather than reusing stale contents.
func (c *FFIClipboardManager) capture(selectAllFirst bool) (string, CaptureOutcome, error) {
	if err := c.SaveCurrent(); err != nil {
		return "", CaptureCopyFailed, fmt.Errorf("could not read the clipboard: %w", err)
	}

	sim, err := NewFFIKeySimulator()
	if err != nil {
		c.Abandon()
		return "", CaptureCopyFailed, fmt.Errorf("failed to create simulator: %w", err)
	}
	defer sim.Close()

	// The hotkey that triggered this is still held, and Ctrl+A with Alt down is another shortcut.
	if err := sim.ReleaseModifiers(); err != nil {
		logger.Warn("Could not release held modifiers", "error", err)
	}

	// The sentinel is absence: whatever is on the clipboard afterwards came from this copy.
	if err := c.Clear(); err != nil {
		c.Abandon()
		return "", CaptureCopyFailed, fmt.Errorf("could not use the clipboard: %w", err)
	}

	if selectAllFirst {
		if err := sim.SelectAll(); err != nil {
			c.Abandon()
			return "", CaptureCopyFailed, fmt.Errorf("could not select the text: %w", err)
		}
	}

	if err := sim.Copy(); err != nil {
		c.Abandon()
		return "", CaptureCopyFailed, fmt.Errorf("could not copy the selection: %w", err)
	}

	copied, ok := await(c.text)
	if !ok {
		c.Abandon()
		// An empty selection and a refused copy are indistinguishable from here.
		if selectAllFirst {
			return "", CaptureCopyFailed, nil
		}
		return "", CaptureNothingSelected, nil
	}
	if strings.TrimSpace(copied) == "" {
		c.Abandon()
		return "", CaptureNothingSelected, nil
	}

	return copied, CaptureOK, nil
}

// The selection is still active from the capture, so pasting replaces it; the clipboard is
// put back afterwards.
func (c *FFIClipboardManager) ReplaceSelectedText(newText string) error {
	if err := c.SetText(newText); err != nil {
		c.Abandon()
		return fmt.Errorf("failed to set clipboard text: %w", err)
	}

	// Confirm the clipboard holds the new text before pasting, or a slow write pastes stale contents.
	if _, ok := await(func() (string, bool) {
		text, ok := c.text()
		return text, ok && text == newText
	}); !ok {
		c.Abandon()
		return fmt.Errorf("the clipboard did not take the revised text")
	}

	sim, err := NewFFIKeySimulator()
	if err != nil {
		c.Abandon()
		return fmt.Errorf("failed to create simulator: %w", err)
	}
	defer sim.Close()

	if err := sim.ReleaseModifiers(); err != nil {
		logger.Warn("Could not release held modifiers", "error", err)
	}

	if err := sim.Paste(); err != nil {
		c.Abandon()
		return fmt.Errorf("failed to simulate paste: %w", err)
	}

	// The paste is asynchronous; restoring immediately can hand the target application the old contents.
	time.Sleep(clipboardPasteSettle)
	c.Abandon()

	return nil
}

// Abandon puts the clipboard back and logs, rather than returning, on failure.
func (c *FFIClipboardManager) Abandon() {
	if err := c.Restore(); err != nil {
		logger.Warn("Failed to restore clipboard", "error", err)
	}
}

func getLastError() string {
	cErr := C.encre_get_last_error()
	if cErr == nil {
		return "unknown error"
	}
	defer C.encre_free_string((*C.char)(unsafe.Pointer(cErr)))

	return C.GoString(cErr)
}

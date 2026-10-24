//go:build linux || darwin || windows

package input

/*
#cgo CFLAGS: -I${SRCDIR}/../../rust-ffi

#cgo linux LDFLAGS: ${SRCDIR}/../../lib/libencre_ffi.a -lpthread -ldl -lm -lxdo -lX11 -lXtst -lxkbcommon

#cgo darwin LDFLAGS: ${SRCDIR}/../../lib/libencre_ffi.a
#cgo darwin LDFLAGS: -framework CoreFoundation -framework Security -framework AppKit -framework Carbon

#cgo windows LDFLAGS: ${SRCDIR}/../../lib/libencre_ffi.a
#cgo windows LDFLAGS: -lws2_32 -luserenv -lbcrypt -lntdll -lgdi32 -static

#include <stdlib.h>
#include "bindings.h"
*/
import "C"
import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/logger"
)

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
	clipboardPasteSettle  = 400 * time.Millisecond
)

var errClipboardClosed = errors.New("clipboard manager is closed")

// Shared by every C call; Close takes it exclusively so an action still running never uses a freed handle.
type FFIClipboardManager struct {
	mu     sync.RWMutex
	handle C.encre_ClipboardHandle
}

func NewFFIClipboardManager() (*FFIClipboardManager, error) {
	handle := C.encre_clipboard_new()
	if handle == nil {
		return nil, fmt.Errorf("failed to create clipboard manager: %s", getLastError())
	}

	return &FFIClipboardManager{handle: handle}, nil
}

func (c *FFIClipboardManager) call(op func(C.encre_ClipboardHandle) C.int) (C.int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.handle == nil {
		return 0, errClipboardClosed
	}
	return op(c.handle), nil
}

// Empty when the clipboard holds no text (empty or an image); an error only when reading failed.
func (c *FFIClipboardManager) read() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.handle == nil {
		return "", errClipboardClosed
	}

	cStr := C.encre_clipboard_get_text(c.handle)
	if cStr == nil {
		return "", fmt.Errorf("failed to read clipboard: %s", getLastError())
	}
	defer C.encre_free_string(cStr)

	return C.GoString(cStr), nil
}

// ok is false when the clipboard holds no text yet or could not be read.
func (c *FFIClipboardManager) text() (string, bool) {
	text, err := c.read()
	return text, err == nil && text != ""
}

func (c *FFIClipboardManager) GetText() (string, error) {
	text, err := c.read()
	if err != nil {
		return "", err
	}
	if text == "" {
		return "", fmt.Errorf("clipboard holds no text")
	}
	return text, nil
}

// Clearing is what makes a following copy observable.
func (c *FFIClipboardManager) Clear() error {
	result, err := c.call(func(h C.encre_ClipboardHandle) C.int { return C.encre_clipboard_clear(h) })
	if err != nil {
		return err
	}
	if result != 0 {
		return fmt.Errorf("failed to clear clipboard: %s", getLastError())
	}

	return nil
}

// Polls rather than a fixed sleep, which is wrong for either a fast or a slow app.
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
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	result, err := c.call(func(h C.encre_ClipboardHandle) C.int { return C.encre_clipboard_set_text(h, cText) })
	if err != nil {
		return err
	}
	if result != 0 {
		return fmt.Errorf("failed to set clipboard text: %s", getLastError())
	}

	return nil
}

// Tells the copied selection apart from a copy the user makes meanwhile, which restoring leaves alone.
func (c *FFIClipboardManager) markCaptured(text string) {
	cText := C.CString(text)
	defer C.free(unsafe.Pointer(cText))

	result, err := c.call(func(h C.encre_ClipboardHandle) C.int { return C.encre_clipboard_captured(h, cText) })
	if err == nil && result != 0 {
		err = errors.New(getLastError())
	}
	if err != nil {
		logger.Warn("Could not note the copied selection", "error", err)
	}
}

func (c *FFIClipboardManager) SaveCurrent() error {
	result, err := c.call(func(h C.encre_ClipboardHandle) C.int { return C.encre_clipboard_save(h) })
	if err != nil {
		return err
	}
	if result != 0 {
		return fmt.Errorf("failed to save clipboard: %s", getLastError())
	}

	return nil
}

func (c *FFIClipboardManager) Restore() error {
	result, err := c.call(func(h C.encre_ClipboardHandle) C.int { return C.encre_clipboard_restore(h) })
	if err != nil {
		return err
	}
	if result != 0 {
		return fmt.Errorf("failed to restore clipboard: %s", getLastError())
	}

	return nil
}

func (c *FFIClipboardManager) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.handle != nil {
		C.encre_clipboard_free(c.handle)
		c.handle = nil
	}
}

// Reopen replaces a closed handle, for a process that carries on after all.
func (c *FFIClipboardManager) Reopen() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.handle != nil {
		return nil
	}
	handle := C.encre_clipboard_new()
	if handle == nil {
		return fmt.Errorf("failed to create clipboard manager: %s", getLastError())
	}
	c.handle = handle
	return nil
}

func (c *FFIClipboardManager) CaptureSelection() (string, CaptureOutcome, error) {
	return c.capture(false)
}

func (c *FFIClipboardManager) CaptureAll() (string, CaptureOutcome, error) {
	return c.capture(true)
}

// Clearing first makes a failed copy observable instead of reusing stale contents.
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
	c.markCaptured(copied)
	if strings.TrimSpace(copied) == "" {
		c.Abandon()
		return "", CaptureNothingSelected, nil
	}

	return copied, CaptureOK, nil
}

// The capture's selection is still active, so pasting replaces it; the clipboard is restored.
func (c *FFIClipboardManager) ReplaceSelectedText(newText string, shortcut config.PasteShortcut) error {
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

	if err := pasteWith(sim, shortcut); err != nil {
		c.Abandon()
		return fmt.Errorf("failed to simulate paste: %w", err)
	}

	// The paste is asynchronous; restoring at once can hand the target the old contents.
	time.Sleep(clipboardPasteSettle)
	c.Abandon()

	return nil
}

// Restores unless something new was copied meanwhile; logs rather than returns failures.
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

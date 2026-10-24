//go:build linux || darwin || windows
// +build linux darwin windows

package input

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"
)

// The clipboard and key simulator open a display connection, which a headless box cannot offer.
func requireDisplay(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		t.Skip("no display server")
	}
}

func TestFFIClipboard(t *testing.T) {
	requireDisplay(t)
	clipboard, err := NewFFIClipboardManager()
	if err != nil {
		t.Fatalf("Failed to create clipboard: %v", err)
	}
	defer clipboard.Close()

	testText := "Hello from Rust FFI!"
	err = clipboard.SetText(testText)
	if err != nil {
		t.Fatalf("Failed to set text: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	retrievedText, err := clipboard.GetText()
	if err != nil {
		t.Fatalf("Failed to get text: %v", err)
	}

	if retrievedText != testText {
		t.Errorf("Text mismatch: expected '%s', got '%s'", testText, retrievedText)
	}

	t.Logf("Clipboard test passed")
}

func TestFFISimulator(t *testing.T) {
	requireDisplay(t)
	simulator, err := NewFFIKeySimulator()
	if err != nil {
		t.Fatalf("Failed to create simulator: %v", err)
	}
	defer simulator.Close()

	t.Logf("Simulator created successfully")
	t.Logf("  (Actual key simulation requires GUI context)")
}

func TestFFIHotkeys(t *testing.T) {
	hotkeyMgr := NewFFIHotkeyManager()
	if hotkeyMgr == nil {
		t.Fatal("Failed to create hotkey manager")
	}
	defer hotkeyMgr.Close()

	err := hotkeyMgr.RegisterHotkey("ctrl+alt+t", "test", func() {
		t.Log("Hotkey callback called!")
	})

	if err != nil {
		t.Fatalf("Failed to register hotkey: %v", err)
	}

	err = hotkeyMgr.Start()
	if err != nil {
		t.Fatalf("Failed to start hotkey manager: %v", err)
	}

	hotkeyMgr.Stop()

	t.Logf("Hotkey manager test passed")
	t.Logf("  (Hotkey registered and manager started/stopped successfully)")
}

// Reproduces saving settings while quitting: RegisterHotkey races Close. Run with -race.
func TestFFIHotkeyManagerConcurrentRegisterAndClose(t *testing.T) {
	mgr := NewFFIHotkeyManager()
	if mgr == nil {
		t.Fatal("Failed to create hotkey manager")
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			binding := fmt.Sprintf("ctrl+alt+%d", i%9+1)
			action := fmt.Sprintf("action-%d", i)
			_ = mgr.RegisterHotkey(binding, action, func() {})
		}
	}()

	go func() {
		defer wg.Done()
		_ = mgr.ClearBindings()
		mgr.Close()
	}()

	wg.Wait()
}

// Reproduces quitting while an action is still reading the clipboard. Run with -race.
func TestFFIClipboardConcurrentUseAndClose(t *testing.T) {
	requireDisplay(t)
	clipboard, err := NewFFIClipboardManager()
	if err != nil {
		t.Fatalf("Failed to create clipboard: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_, _ = clipboard.GetText()
			_ = clipboard.SaveCurrent()
			_ = clipboard.Restore()
		}
	}()

	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond)
		clipboard.Close()
	}()

	wg.Wait()
}

func TestFFIClipboardRefusesUseAfterClose(t *testing.T) {
	requireDisplay(t)
	clipboard, err := NewFFIClipboardManager()
	if err != nil {
		t.Fatalf("Failed to create clipboard: %v", err)
	}
	clipboard.Close()
	clipboard.Close()

	if _, err := clipboard.GetText(); err == nil {
		t.Error("GetText after Close returned no error")
	}
	if err := clipboard.SetText("late"); err == nil {
		t.Error("SetText after Close returned no error")
	}
	if err := clipboard.Clear(); err == nil {
		t.Error("Clear after Close returned no error")
	}
	if err := clipboard.SaveCurrent(); err == nil {
		t.Error("SaveCurrent after Close returned no error")
	}
	if err := clipboard.Restore(); err == nil {
		t.Error("Restore after Close returned no error")
	}
	if _, outcome, err := clipboard.CaptureSelection(); err == nil || outcome != CaptureCopyFailed {
		t.Errorf("CaptureSelection after Close = %v, %v", outcome, err)
	}
}

// An empty clipboard is the normal state while awaiting a copy, not a failure.
func TestFFIClipboardEmptyIsNotAFailure(t *testing.T) {
	requireDisplay(t)
	clipboard, err := NewFFIClipboardManager()
	if err != nil {
		t.Fatalf("Failed to create clipboard: %v", err)
	}
	defer clipboard.Close()

	if err := clipboard.Clear(); err != nil {
		t.Fatalf("Failed to clear: %v", err)
	}
	if _, err := clipboard.read(); err != nil {
		t.Errorf("reading an empty clipboard failed: %v", err)
	}
	if reason := getLastError(); reason != "unknown error" {
		t.Errorf("reading an empty clipboard set the error %q", reason)
	}
}

func TestEscapeCanBeBoundAloneAndReleased(t *testing.T) {
	manager := NewFFIHotkeyManager()
	if manager == nil {
		t.Skip("no hotkey manager on this system")
	}
	defer manager.Close()

	if err := manager.RegisterHotkey("escape", "close_answer", func() {}); err != nil {
		t.Fatalf("escape alone was refused: %v", err)
	}
	if err := manager.RegisterHotkey("k", "typing", func() {}); err == nil {
		t.Fatal("a bare letter was accepted, which would fire on ordinary typing")
	}
	if err := manager.UnregisterHotkey("close_answer"); err != nil {
		t.Fatal(err)
	}
	manager.mu.Lock()
	_, kept := manager.handlers["close_answer"]
	manager.mu.Unlock()
	if kept {
		t.Fatal("the handler outlived its binding")
	}
}

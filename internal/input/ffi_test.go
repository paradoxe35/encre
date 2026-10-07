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

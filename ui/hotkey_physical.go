package ui

import (
	"runtime"

	"fyne.io/fyne/v2"
	"github.com/paradoxe35/encre/internal/overlay"
)

// The hotkey listener matches letters and digits by where they sit on a US keyboard on Linux and
// macOS, while Fyne names them by what the layout prints; capture saves the position, as the listener reads it.
// Windows matches by virtual key, which follows the layout as Fyne does.

// X11 keycodes, evdev + 8, as GLFW reports them on Linux.
var x11Keys = map[int]string{
	24: "q", 25: "w", 26: "e", 27: "r", 28: "t", 29: "y", 30: "u", 31: "i", 32: "o", 33: "p",
	38: "a", 39: "s", 40: "d", 41: "f", 42: "g", 43: "h", 44: "j", 45: "k", 46: "l",
	52: "z", 53: "x", 54: "c", 55: "v", 56: "b", 57: "n", 58: "m",
	10: "1", 11: "2", 12: "3", 13: "4", 14: "5", 15: "6", 16: "7", 17: "8", 18: "9", 19: "0",
	79: "7", 80: "8", 81: "9", 83: "4", 84: "5", 85: "6", 87: "1", 88: "2", 89: "3", 90: "0",
}

// macOS virtual key codes (kVK_ANSI_*), as GLFW reports them.
var macKeys = map[int]string{
	0x00: "a", 0x01: "s", 0x02: "d", 0x03: "f", 0x04: "h", 0x05: "g", 0x06: "z", 0x07: "x",
	0x08: "c", 0x09: "v", 0x0B: "b", 0x0C: "q", 0x0D: "w", 0x0E: "e", 0x0F: "r", 0x10: "y",
	0x11: "t", 0x1F: "o", 0x20: "u", 0x22: "i", 0x23: "p", 0x25: "l", 0x26: "j", 0x28: "k",
	0x2D: "n", 0x2E: "m",
	0x12: "1", 0x13: "2", 0x14: "3", 0x15: "4", 0x17: "5", 0x16: "6", 0x1A: "7", 0x1C: "8", 0x19: "9", 0x1D: "0",
	0x53: "1", 0x54: "2", 0x55: "3", 0x56: "4", 0x57: "5", 0x58: "6", 0x59: "7", 0x5B: "8", 0x5C: "9", 0x52: "0",
}

func positionalTable(goos string) map[int]string {
	switch goos {
	case "linux", "freebsd", "openbsd", "netbsd":
		return x11Keys
	case "darwin":
		return macKeys
	}
	return nil
}

// bindingKey names a captured key as the listener will read it: by position for letters and digits
// where the listener does, by Fyne's name otherwise.
func bindingKey(key fyne.KeyName, scancode int) string {
	if scancode > 0 && runtime.GOOS != "darwin" && positionalTable(runtime.GOOS) != nil && overlay.NativeWayland() {
		scancode += 8
	}
	return bindingKeyOn(runtime.GOOS, key, scancode)
}

func bindingKeyOn(goos string, key fyne.KeyName, scancode int) string {
	name := keyNameToString(key)
	table := positionalTable(goos)
	if table == nil || scancode <= 0 {
		return name
	}
	if physical, ok := table[scancode]; ok {
		return physical
	}
	// A letter on a key the listener knows only by another name, such as AZERTY's M, could never fire.
	if len(name) == 1 && isAlphanumeric(name[0]) {
		return ""
	}
	return name
}

func isAlphanumeric(c byte) bool { return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' }

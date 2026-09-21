package ui

import (
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

func TestPasteShortcutOptionsRoundTrip(t *testing.T) {
	for _, shortcut := range pasteShortcuts {
		if got := pasteShortcutValueFor(shortcut.label); got != shortcut.value {
			t.Errorf("value for %q = %q, want %q", shortcut.label, got, shortcut.value)
		}
		if got := pasteShortcutLabelFor(shortcut.value); got != shortcut.label {
			t.Errorf("label for %q = %q, want %q", shortcut.value, got, shortcut.label)
		}
	}
}

// An empty value, from a config written before the setting existed, shows as the standard chord.
func TestPasteShortcutUnknownFallsBackToStandard(t *testing.T) {
	if got := pasteShortcutLabelFor(""); got != "Ctrl+V (standard)" {
		t.Errorf("label for empty = %q, want the standard chord", got)
	}
	if got := pasteShortcutValueFor("no such option"); got != config.PasteStandard {
		t.Errorf("value for an unknown label = %q, want %q", got, config.PasteStandard)
	}
}

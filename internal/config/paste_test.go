package config

import (
	"encoding/json"
	"testing"
)

func TestPasteShortcutDefaultsToStandard(t *testing.T) {
	if got := Default().PasteShortcut(); got != PasteStandard {
		t.Fatalf("paste shortcut = %q, want %q", got, PasteStandard)
	}
}

// A config written before the setting existed, or hand-edited to "", keeps pasting with Ctrl+V.
func TestPasteShortcutMissingOrEmptyReadsAsStandard(t *testing.T) {
	cases := map[string]string{
		"missing": `{}`,
		"empty":   `{"paste_shortcut": ""}`,
	}

	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := &Config{}
			if err := json.Unmarshal([]byte(raw), cfg); err != nil {
				t.Fatal(err)
			}
			cfg.applyDefaults()

			if got := cfg.PasteShortcut(); got != PasteStandard {
				t.Errorf("paste shortcut = %q, want %q", got, PasteStandard)
			}
		})
	}
}

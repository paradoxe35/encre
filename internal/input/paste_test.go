package input

import (
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

type chordRecorder struct{ chord string }

func (r *chordRecorder) Paste() error {
	r.chord = "ctrl+v"
	return nil
}

func (r *chordRecorder) PasteTerminal() error {
	r.chord = "ctrl+shift+v"
	return nil
}

func TestPasteWithPicksTheChord(t *testing.T) {
	cases := []struct {
		name     string
		shortcut config.PasteShortcut
		want     string
	}{
		{"standard", config.PasteStandard, "ctrl+v"},
		{"terminal", config.PasteTerminal, "ctrl+shift+v"},
		{"empty falls back to standard", "", "ctrl+v"},
		{"unknown falls back to standard", "something-else", "ctrl+v"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sim := &chordRecorder{}
			if err := pasteWith(sim, tc.shortcut); err != nil {
				t.Fatal(err)
			}
			if sim.chord != tc.want {
				t.Errorf("chord = %q, want %q", sim.chord, tc.want)
			}
		})
	}
}

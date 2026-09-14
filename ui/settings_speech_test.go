package ui

import (
	"testing"

	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/stt"
)

func TestAdoptPresetModel(t *testing.T) {
	gemini, _ := stt.FindPreset("gemini")
	openai, _ := stt.FindPreset("openai")
	custom, _ := stt.FindPreset("custom")

	cases := []struct {
		name    string
		current string
		preset  stt.RemotePreset
		want    string
	}{
		{"empty takes the default", "", gemini, gemini.Models[0]},
		{"another service's model is replaced", "gpt-4o-transcribe", gemini, gemini.Models[0]},
		{"a model of this service is kept", "whisper-1", openai, "whisper-1"},
		{"a hand-typed model is kept", "my-own-finetune", gemini, "my-own-finetune"},
		{"custom suggests nothing, so nothing changes", "gpt-4o-transcribe", custom, "gpt-4o-transcribe"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := widget.NewSelectEntry(nil)
			entry.SetText(tc.current)

			w := &MainWindow{speechRemoteModel: entry}
			w.adoptPresetModel(tc.preset)

			if entry.Text != tc.want {
				t.Errorf("model = %q, want %q", entry.Text, tc.want)
			}
		})
	}
}

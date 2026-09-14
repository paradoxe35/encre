package stt

import "testing"

func TestIsPresetModel(t *testing.T) {
	cases := map[string]bool{
		"whisper-1":              true,
		"whisper-large-v3-turbo": true,
		"gemini-3.8-flash":       true,
		"my-own-finetune":        false,
		"":                       false,
	}

	for model, want := range cases {
		if got := IsPresetModel(model); got != want {
			t.Errorf("IsPresetModel(%q) = %v, want %v", model, got, want)
		}
	}
}

// Every preset needs a protocol: an empty one would silently fall back to the
// OpenAI shape and post audio at an endpoint that does not exist.
func TestEveryPresetDeclaresAProtocol(t *testing.T) {
	for _, preset := range RemotePresets {
		if preset.Protocol == "" {
			t.Errorf("preset %q has no protocol", preset.ID)
		}
	}
}

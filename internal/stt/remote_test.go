package stt

import (
	"strings"
	"testing"
)

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

// The model each preset offers first is what a new user gets, so it has to be
// the one the provider currently recommends for transcribing a finished take.
func TestPresetDefaultsAreCurrentModels(t *testing.T) {
	want := map[string]string{
		"openai": "gpt-transcribe",
		"groq":   "whisper-large-v3-turbo",
		"gemini": "gemini-3.5-transcribe",
	}

	for _, preset := range RemotePresets {
		expected, checked := want[preset.ID]
		if !checked {
			continue
		}
		if len(preset.Models) == 0 || preset.Models[0] != expected {
			t.Errorf("%s defaults to %v, want %q first", preset.ID, preset.Models, expected)
		}
	}
}

// Every suggested model must reach a transcriber, not a 404.
func TestSuggestedModelsRouteSomewhere(t *testing.T) {
	for _, preset := range RemotePresets {
		for _, model := range preset.Models {
			if preset.Protocol == ProtocolGemini && isGeminiLiveModel(model) {
				t.Errorf("%s suggests %q, which needs the Live API", preset.ID, model)
			}
			if preset.Protocol == ProtocolOpenAI && strings.Contains(model, "diarize") {
				t.Errorf("%s suggests %q; diarization output is not a dictation", preset.ID, model)
			}
		}
	}
}

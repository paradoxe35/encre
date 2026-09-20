package stt

import "testing"

func TestLanguagesForKnownServices(t *testing.T) {
	cases := []struct {
		preset, model string
		wantCode      string
	}{
		{"openai", "whisper-1", "fr"},
		{"openai", "gpt-transcribe", "fr"},
		{"groq", "whisper-large-v3-turbo", "fr"},
		{"gemini", "gemini-3.5-transcribe", "fr-FR"},
	}

	for _, tc := range cases {
		languages := LanguagesFor(tc.preset, tc.model)
		if languages == nil {
			t.Errorf("%s/%s: no list", tc.preset, tc.model)
			continue
		}
		if got := MatchLanguage(languages, "fr"); got != tc.wantCode {
			t.Errorf("%s/%s: French = %q, want %q", tc.preset, tc.model, got, tc.wantCode)
		}
	}
}

// whisper-1 predates the regional zh codes; offering them would be a lie.
func TestWhisperListExcludesGPTOnlyCodes(t *testing.T) {
	whisper := LanguagesFor("openai", "whisper-1")
	if MatchLanguage(whisper, "zh-tw") == "zh-tw" {
		t.Error("whisper-1 offered zh-tw, which only the gpt- models take")
	}

	gpt := LanguagesFor("openai", "gpt-transcribe")
	if MatchLanguage(gpt, "zh-tw") != "zh-tw" {
		t.Error("gpt-transcribe should offer zh-tw")
	}
}

// An unknown model must not get a list nothing can back.
func TestLanguagesForUnknownModels(t *testing.T) {
	cases := [][2]string{
		{"custom", "some-local-whisper"},
		{"custom", ""},
		{"gemini", "gemini-3.8-flash"},
		{"whatever", "anything"},
	}

	for _, tc := range cases {
		if got := LanguagesFor(tc[0], tc[1]); got != nil {
			t.Errorf("LanguagesFor(%q, %q) returned %d languages, want nil", tc[0], tc[1], len(got))
		}
	}
}

func TestIsGeminiTranscribeModel(t *testing.T) {
	cases := map[string]bool{
		"gemini-3.5-transcribe":      true,
		"gemini-3.5-transcribe-live": true,
		"gemini-3.8-flash":           false,
		"gemini-2.5-flash":           false,
	}

	for model, want := range cases {
		if got := IsGeminiTranscribeModel(model); got != want {
			t.Errorf("IsGeminiTranscribeModel(%q) = %v, want %v", model, got, want)
		}
	}
}

// Switching service must carry the choice rather than drop to auto-detect.
func TestMatchLanguageAcrossServices(t *testing.T) {
	gemini := LanguagesFor("gemini", "gemini-3.5-transcribe")
	whisper := LanguagesFor("groq", "whisper-large-v3")

	if got := MatchLanguage(whisper, "pt-BR"); got != "pt" {
		t.Errorf("Gemini pt-BR -> Groq = %q, want %q", got, "pt")
	}
	if got := MatchLanguage(gemini, "pt"); got != "pt-BR" && got != "pt-PT" {
		t.Errorf("Groq pt -> Gemini = %q, want a Portuguese locale", got)
	}
	if got := MatchLanguage(gemini, "ja"); got != "ja-JP" {
		t.Errorf("Groq ja -> Gemini = %q, want ja-JP", got)
	}
	// Whisper has no Kabuverdianu, so there is nothing honest to carry over.
	if got := MatchLanguage(whisper, "kea-CV"); got != "" {
		t.Errorf("Gemini kea-CV -> Groq = %q, want no match", got)
	}
}

func TestBaseLanguageCode(t *testing.T) {
	cases := map[string]string{
		"fr-FR":       "fr",
		"es-419":      "es",
		"yue-Hant-HK": "yue",
		"fr":          "fr",
		"":            "",
	}

	for code, want := range cases {
		if got := BaseLanguageCode(code); got != want {
			t.Errorf("BaseLanguageCode(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestLanguageSetsHaveNoDuplicateNames(t *testing.T) {
	sets := map[string][]NamedLanguage{
		"whisper":           whisperLanguages,
		"gpt-transcribe":    gptTranscribeLanguages,
		"gemini-transcribe": geminiTranscribeLanguages,
	}

	for name, languages := range sets {
		seen := make(map[string]string, len(languages))
		for _, language := range languages {
			if first, ok := seen[language.Name]; ok {
				t.Errorf("%s: %q is both %q and %q; the picker keys on the name",
					name, language.Name, first, language.Code)
			}
			seen[language.Name] = language.Code
		}
	}
}

// LanguagesFor reads through LanguageSetName, so the two cannot disagree about
// which models have a list.
func TestLanguageSetNameAgreesWithLanguagesFor(t *testing.T) {
	cases := [][2]string{
		{"openai", "whisper-1"}, {"openai", "gpt-transcribe"}, {"groq", "whisper-large-v3"},
		{"gemini", "gemini-3.5-transcribe"}, {"gemini", "gemini-3.8-flash"},
		{"custom", "anything"}, {"custom", ""},
	}

	for _, tc := range cases {
		name := LanguageSetName(tc[0], tc[1])
		languages := LanguagesFor(tc[0], tc[1])
		if (name == "") != (languages == nil) {
			t.Errorf("%s/%s: set %q but %d languages", tc[0], tc[1], name, len(languages))
		}
	}
}

// Every name a service dispatches to must resolve to a set that exists.
func TestEveryServiceResolvesToARealSet(t *testing.T) {
	for _, preset := range RemotePresets {
		models := preset.Models
		if len(models) == 0 {
			models = []string{""}
		}
		for _, model := range models {
			name := LanguageSetName(preset.ID, model)
			if name == "" {
				continue
			}
			if _, ok := languageSets[name]; !ok {
				t.Errorf("%s/%s dispatches to %q, which is not a set", preset.ID, model, name)
			}
		}
	}
}

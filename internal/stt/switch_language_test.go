package stt

import "testing"

var (
	detecting    = []string{"en", "fr", "de", "es"}
	noDetectMany = []string{"en", "fr", "de"}
	noDetectNoEN = []string{"fr", "de", "ja"}
	frenchOnly   = []string{"fr"}
)

// The whole rule in one table: what to use after moving from one engine to
// another. "" means auto-detect.
func TestSwitchLanguage(t *testing.T) {
	cases := []struct {
		name     string
		codes    []string
		detects  bool
		previous string
		system   string
		want     string
	}{
		// The new engine can work it out, so it is left to.
		{"auto stays auto when B detects", detecting, true, "", "en", ""},
		{"a pick gives way to detection", detecting, true, "fr", "en", ""},
		{"even a pick B cannot serve", detecting, true, "sw", "en", ""},

		// B cannot detect and A was on auto: English, else the first language.
		{"auto to no-detect takes English", noDetectMany, false, "", "en", "en"},
		{"auto to no-detect without English takes the first", noDetectNoEN, false, "", "ja", "ja"},
		{"system language wins over English", noDetectMany, false, "", "de", "de"},

		// B cannot detect and A had a language: keep it when B can serve it.
		{"French survives to a French-speaking B", noDetectMany, false, "fr", "en", "fr"},
		{"French survives to a French-only B", frenchOnly, false, "fr", "en", "fr"},

		// B cannot detect and cannot serve the language: fall back.
		{"unservable falls to the system language", noDetectMany, false, "sw", "de", "de"},
		{"then to English", noDetectMany, false, "sw", "zu", "en"},
		{"then to the first listed", noDetectNoEN, false, "sw", "zu", "fr"},

		// Nothing to offer at all.
		{"an empty engine stays blank", nil, false, "fr", "en", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SwitchLanguage(tc.codes, tc.detects, tc.previous, tc.system)
			if got != tc.want {
				t.Errorf("SwitchLanguage(%v, detects=%v, previous=%q, system=%q) = %q, want %q",
					tc.codes, tc.detects, tc.previous, tc.system, got, tc.want)
			}
		})
	}
}

// Engines spell the same language differently, and a switch must see through
// that rather than treating fr-FR as a language Groq cannot speak.
func TestSwitchLanguageAcrossCodeFormats(t *testing.T) {
	cases := []struct {
		name     string
		codes    []string
		previous string
		want     string
	}{
		{"Gemini locale to a plain code", []string{"en", "fr", "de"}, "fr-FR", "fr"},
		{"plain code to a Gemini locale", []string{"en-US", "fr-FR", "de-DE"}, "fr", "fr-FR"},
		{"regional to regional", []string{"pt-PT", "en-US"}, "pt-BR", "pt-PT"},
		{"a script subtag still matches", []string{"yue", "en"}, "yue-Hant-HK", "yue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SwitchLanguage(tc.codes, false, tc.previous, "en")
			if got != tc.want {
				t.Errorf("previous %q -> %q, want %q", tc.previous, got, tc.want)
			}
		})
	}
}

// An engine that cannot detect must never be handed a blank language: the
// library behind it assumes English and silently mistranscribes everything else.
func TestNonDetectingEngineIsNeverLeftBlank(t *testing.T) {
	sets := [][]string{detecting, noDetectMany, noDetectNoEN, frenchOnly}

	for _, codes := range sets {
		for _, previous := range []string{"", "fr", "sw", "zh-tw"} {
			for _, system := range []string{"", "en", "sw"} {
				got := SwitchLanguage(codes, false, previous, system)
				if got == "" {
					t.Errorf("codes=%v previous=%q system=%q gave a blank language",
						codes, previous, system)
				}
				if MatchCode(codes, got) != got {
					t.Errorf("codes=%v gave %q, which is not one of them", codes, got)
				}
			}
		}
	}
}

// The local path delegates to the shared rule, so the two cannot drift apart.
func TestModelLanguageAfterSwitchUsesTheSharedRule(t *testing.T) {
	detects := Model{Languages: []string{"en", "fr"}, LanguageDetect: true}
	if got := detects.LanguageAfterSwitch("fr", "en"); got != "" {
		t.Errorf("a detecting model returned %q, want auto-detect", got)
	}

	english := Model{Languages: []string{"en"}, LanguageDetect: false}
	if got := english.LanguageAfterSwitch("", "fr"); got != "en" {
		t.Errorf("English-only model returned %q, want en", got)
	}
	if got := english.LanguageAfterSwitch("fr", "de"); got != "en" {
		t.Errorf("English-only model returned %q for French, want en", got)
	}

	multi := Model{Languages: []string{"en", "fr", "de"}, LanguageDetect: false}
	if got := multi.LanguageAfterSwitch("fr", "en"); got != "fr" {
		t.Errorf("returned %q, want French kept", got)
	}
}

// Every hosted set, and Wit.ai's, has to survive the rule without producing a
// code the engine does not list.
func TestSwitchLanguageOverRealSets(t *testing.T) {
	sets := map[string][]string{
		"whisper":           Codes(whisperLanguages),
		"gpt-transcribe":    Codes(gptTranscribeLanguages),
		"gemini-transcribe": Codes(geminiTranscribeLanguages),
	}

	for name, codes := range sets {
		for _, previous := range []string{"", "fr", "fr-FR", "yue-Hant-HK", "kea-CV", "xx"} {
			got := SwitchLanguage(codes, false, previous, "en")
			if got != "" && MatchCode(codes, got) != got {
				t.Errorf("%s: previous %q gave %q, which it does not list", name, previous, got)
			}
		}
	}
}

package stt

import "testing"

func TestTranscribeLanguage(t *testing.T) {
	canary := Model{Languages: []string{"de", "en", "es", "fr"}}
	whisper := Model{Languages: []string{"en", "fr"}, LanguageDetect: true}

	cases := []struct {
		name              string
		model             Model
		preferred, locale string
		want              string
	}{
		{"the choice wins", canary, "fr", "de", "fr"},
		{"the choice wins over detection", whisper, "fr", "en", "fr"},
		{"a detecting model is left to detect", whisper, "", "de", ""},
		{"a language it does not speak is ignored", canary, "sw", "de", "de"},
		{"no choice falls back to the locale", canary, "", "fr", "fr"},
		{"an unspoken locale falls back to the first", canary, "", "sw", "de"},
		{"never blank for a model that cannot detect", canary, "", "", "de"},
	}

	for _, c := range cases {
		if got := c.model.TranscribeLanguage(c.preferred, c.locale); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLanguageAfterSwitch(t *testing.T) {
	canary := Model{Languages: []string{"de", "en", "es", "fr"}}
	whisper := Model{Languages: []string{"en", "fr", "de"}, LanguageDetect: true}
	english := Model{Languages: []string{"en"}}

	cases := []struct {
		name     string
		model    Model
		previous string
		want     string
	}{
		{"a detecting model takes over from a chosen language", whisper, "en", ""},
		{"a detecting model takes over from nothing", whisper, "", ""},
		{"a chosen language carries over when it is spoken", canary, "en", "en"},
		{"and when it is not English", canary, "fr", "fr"},
		{"an unspoken choice falls back", english, "fr", "en"},
		{"no choice falls back to the locale", canary, "", "de"},
	}

	for _, c := range cases {
		if got := c.model.LanguageAfterSwitch(c.previous, "de"); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

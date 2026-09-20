package revision

import (
	"strings"
	"testing"

	"github.com/paradoxe35/encre/internal/config"
)

func mentionConfig(enabled bool) *config.Config {
	cfg := &config.Config{
		EnableProviderMentions: enabled,
		AIProvider: config.AIProviderConfig{
			Providers: map[string]config.ProviderSettings{
				"OpenAI": {},
				"claude": {},
			},
		},
	}
	return cfg
}

func TestParseProviderMention(t *testing.T) {
	cases := []struct {
		name          string
		mentionsOn    bool
		text          string
		wantProvider  string
		wantRemainder string
		wantOK        bool
	}{
		{
			name:          "mention stripped and case-insensitively matched",
			mentionsOn:    true,
			text:          "@openai fix this sentence",
			wantProvider:  "OpenAI",
			wantRemainder: "fix this sentence",
			wantOK:        true,
		},
		{
			name:          "mention alone with no remainder",
			mentionsOn:    true,
			text:          "@claude",
			wantProvider:  "claude",
			wantRemainder: "",
			wantOK:        true,
		},
		{
			name:          "unknown provider falls back untouched",
			mentionsOn:    true,
			text:          "@unknown do the thing",
			wantProvider:  "",
			wantRemainder: "@unknown do the thing",
			wantOK:        false,
		},
		{
			name:          "no leading @ is not a mention",
			mentionsOn:    true,
			text:          "openai please help",
			wantProvider:  "",
			wantRemainder: "openai please help",
			wantOK:        false,
		},
		{
			name:          "feature disabled ignores an otherwise valid mention",
			mentionsOn:    false,
			text:          "@openai fix this",
			wantProvider:  "",
			wantRemainder: "@openai fix this",
			wantOK:        false,
		},
		{
			name:          "leading whitespace before the mention is still recognised",
			mentionsOn:    true,
			text:          "  @claude   translate this",
			wantProvider:  "claude",
			wantRemainder: "translate this",
			wantOK:        true,
		},
	}

	p := &Processor{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mentionConfig(tc.mentionsOn)
			provider, remainder, ok := p.parseProviderMention(cfg, tc.text)
			if provider != tc.wantProvider || remainder != tc.wantRemainder || ok != tc.wantOK {
				t.Errorf("parseProviderMention(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.text, provider, remainder, ok, tc.wantProvider, tc.wantRemainder, tc.wantOK)
			}
		})
	}
}

func TestCheckCharacterLimit(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		limit   int
		wantErr bool
	}{
		{"under the limit", "hello", 10, false},
		{"exactly at the limit", "hello", 5, false},
		{"over the limit", "hello world", 5, true},
		{
			name: "accented runes count as one character each, not two bytes",
			// "éàç" is 6 bytes in UTF-8 but 3 runes.
			text:    "éàç",
			limit:   3,
			wantErr: false,
		},
		{
			name:    "accented text over the rune limit still fails",
			text:    "éàçé",
			limit:   3,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkCharacterLimit(tc.text, tc.limit)
			if (err != nil) != tc.wantErr {
				t.Errorf("checkCharacterLimit(%q, %d) error = %v, wantErr %v", tc.text, tc.limit, err, tc.wantErr)
			}
		})
	}
}

func TestWhitespacePreservingReconstruction(t *testing.T) {
	cases := []struct {
		name     string
		original string
		cleaned  string
		want     string
	}{
		{
			name:     "single leading and trailing space preserved",
			original: " hello world ",
			cleaned:  "Hello world!",
			want:     " Hello world! ",
		},
		{
			name:     "no surrounding whitespace",
			original: "hello world",
			cleaned:  "Hello world!",
			want:     "Hello world!",
		},
		{
			name:     "multiple leading and trailing whitespace, including newlines",
			original: "\t\n  hello world  \n\n",
			cleaned:  "Hello world!",
			want:     "\t\n  Hello world!  \n\n",
		},
		{
			name:     "only trailing whitespace",
			original: "hello world\n",
			cleaned:  "Hello world!",
			want:     "Hello world!\n",
		},
		{
			name:     "only leading whitespace",
			original: "  hello world",
			cleaned:  "Hello world!",
			want:     "  Hello world!",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := leadingWhitespace(tc.original) + tc.cleaned + trailingWhitespace(tc.original)
			if got != tc.want {
				t.Errorf("reconstruction = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLeadingAndTrailingWhitespace(t *testing.T) {
	cases := []struct {
		name         string
		text         string
		wantLeading  string
		wantTrailing string
	}{
		{"no whitespace", "hello", "", ""},
		// Both halves claim the whole string: each is computed from its own side, and callers
		// only reach the pair after transform's empty check.
		{"all whitespace", "   ", "   ", "   "},
		{"empty string", "", "", ""},
		{"leading and trailing", "  hello  ", "  ", "  "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := leadingWhitespace(tc.text); got != tc.wantLeading {
				t.Errorf("leadingWhitespace(%q) = %q, want %q", tc.text, got, tc.wantLeading)
			}
			if got := trailingWhitespace(tc.text); got != tc.wantTrailing {
				t.Errorf("trailingWhitespace(%q) = %q, want %q", tc.text, got, tc.wantTrailing)
			}
			if strings.TrimSpace(tc.text) == "" {
				return
			}
			middle := strings.TrimSpace(tc.text)
			if leadingWhitespace(tc.text)+middle+trailingWhitespace(tc.text) != tc.text {
				t.Errorf("leading+middle+trailing does not reconstruct %q", tc.text)
			}
		})
	}
}

package revision

import (
	"github.com/paradoxe35/encre/internal/overlay"
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
	}{
		{
			name:          "mention stripped and case-insensitively matched",
			mentionsOn:    true,
			text:          "@openai fix this sentence",
			wantProvider:  "OpenAI",
			wantRemainder: "fix this sentence",
		},
		{
			name:          "mention alone with no remainder",
			mentionsOn:    true,
			text:          "@claude",
			wantProvider:  "claude",
			wantRemainder: "",
		},
		{
			name:          "unknown provider falls back untouched",
			mentionsOn:    true,
			text:          "@unknown do the thing",
			wantProvider:  "",
			wantRemainder: "@unknown do the thing",
		},
		{
			name:          "no leading @ is not a mention",
			mentionsOn:    true,
			text:          "openai please help",
			wantProvider:  "",
			wantRemainder: "openai please help",
		},
		{
			name:          "feature disabled ignores an otherwise valid mention",
			mentionsOn:    false,
			text:          "@openai fix this",
			wantProvider:  "",
			wantRemainder: "@openai fix this",
		},
		{
			name:          "leading whitespace before the mention is still recognised",
			mentionsOn:    true,
			text:          "  @claude   translate this",
			wantProvider:  "claude",
			wantRemainder: "translate this",
		},
	}

	p := &Processor{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mentionConfig(tc.mentionsOn)
			provider, remainder := p.parseProviderMention(cfg, tc.text)
			if provider != tc.wantProvider || remainder != tc.wantRemainder {
				t.Errorf("parseProviderMention(%q) = (%q, %q), want (%q, %q)",
					tc.text, provider, remainder, tc.wantProvider, tc.wantRemainder)
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

func TestSetOverlayHidesThePreviousIndicator(t *testing.T) {
	p := &Processor{}
	if _, ok := p.overlay().(overlay.Disabled); !ok {
		t.Fatalf("a bare processor has %T, want the disabled indicator", p.overlay())
	}

	first := &fakeOverlay{}
	p.SetOverlay(first)
	second := &fakeOverlay{}
	p.SetOverlay(second)

	if got := first.seen(); len(got) != 1 || got[0] != "hide" {
		t.Fatalf("first indicator saw %v, want to be hidden when replaced", got)
	}
	if p.overlay() != second {
		t.Fatal("the replacement is not the current indicator")
	}
}

func TestOpenRouterBuildsAnOpenAIStyleProviderUnderItsOwnName(t *testing.T) {
	cfg := config.Default()
	cfg.SetProviderSettings(config.BuiltInOpenRouter, config.ProviderSettings{
		BaseURL: "https://openrouter.ai/api/v1", Model: "google/gemini-2.5-flash",
	})
	if err := cfg.SetAPIKey(config.BuiltInOpenRouter, "k"); err != nil {
		t.Fatal(err)
	}

	provider, err := (&Processor{}).buildProvider(cfg, config.BuiltInOpenRouter)
	if err != nil {
		t.Fatal(err)
	}
	if provider.GetName() != config.BuiltInOpenRouter {
		t.Fatalf("name %q", provider.GetName())
	}
	if provider.GetModel() != "google/gemini-2.5-flash" {
		t.Fatalf("model %q", provider.GetModel())
	}
}

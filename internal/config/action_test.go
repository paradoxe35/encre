package config

import (
	"runtime"
	"testing"

	"github.com/paradoxe35/encre/internal/prompt"
)

func TestDefaultActionsCoverEveryKind(t *testing.T) {
	actions := DefaultActions()

	for _, kind := range ActionOrder {
		action, ok := actions[kind]
		if !ok {
			t.Fatalf("%s missing from DefaultActions", kind)
		}
		if action.Hotkey == "" {
			t.Errorf("%s has no default hotkey", kind)
		}
	}
}

func TestDefaultHotkeysAreUnique(t *testing.T) {
	seen := make(map[string]ActionKind)
	for kind, action := range DefaultActions() {
		if other, clash := seen[action.Hotkey]; clash {
			t.Errorf("%s and %s share the default hotkey %q", kind, other, action.Hotkey)
		}
		seen[action.Hotkey] = kind
	}
}

func TestOnlyReviseIsEnabledByDefault(t *testing.T) {
	want := map[ActionKind]bool{
		ActionReviseSelection:    true,
		ActionReviseEverything:   true,
		ActionTranslateSelection: false,
		ActionDictate:            false,
		ActionAskByVoice:         false,
		ActionAskByTyping:        false,
	}

	for kind, action := range DefaultActions() {
		if action.Enabled != want[kind] {
			t.Errorf("%s enabled = %v, want %v", kind, action.Enabled, want[kind])
		}
	}
}

func TestDictateDefaultsToPushToTalk(t *testing.T) {
	action := DefaultActions()[ActionDictate]
	if !action.PushToTalk {
		t.Error("dictate should default to hold-to-record")
	}
	if action.Hotkey == "ctrl+alt+d" {
		t.Error("dictate should not use the commonly reserved ctrl+alt+d shortcut")
	}
	if action.Hotkey != "ctrl+shift+space" {
		t.Errorf("dictate default hotkey = %q, want ctrl+shift+space", action.Hotkey)
	}
}

func TestPromptOrDefaultFallsBackPerOperation(t *testing.T) {
	blank := OperationConfig{}

	if got := blank.PromptOrDefault(OpTranslate); got != prompt.Translate {
		t.Error("translate did not fall back to the translate prompt")
	}
	if got := blank.PromptOrDefault(OpRevise); got != prompt.Revise {
		t.Error("revise did not fall back to the revise prompt")
	}

	custom := OperationConfig{SystemPrompt: "mine"}
	if got := custom.PromptOrDefault(OpTranslate); got != "mine" {
		t.Errorf("PromptOrDefault overrode a custom prompt with %q", got)
	}
}

// Both revise shortcuts must share settings, or editing one prompt would leave the other stale.
func TestBothReviseBindingsShareOneOperation(t *testing.T) {
	if ActionReviseSelection.Operation() != OpRevise {
		t.Error("revise_selection should map to the revise operation")
	}
	if ActionReviseEverything.Operation() != OpRevise {
		t.Error("revise_everything should map to the revise operation")
	}
	if ActionTranslateSelection.Operation() == ActionReviseEverything.Operation() {
		t.Error("translate must not share the revise operation")
	}
}

func TestEditingReviseAffectsBothBindings(t *testing.T) {
	cfg := &Config{}
	cfg.applyDefaults()
	cfg.SetOperation(OpRevise, OperationConfig{SystemPrompt: "shared", CharacterLimit: 500})

	for _, kind := range []ActionKind{ActionReviseSelection, ActionReviseEverything} {
		got := cfg.Operation(kind.Operation())
		if got.SystemPrompt != "shared" || got.CharacterLimit != 500 {
			t.Errorf("%s saw %+v, expected the shared revise settings", kind, got)
		}
	}
}

func TestOperationFillsZeroedLimits(t *testing.T) {
	cfg := &Config{Operations: map[Operation]OperationConfig{OpTranslate: {}}}

	operation := cfg.Operation(OpTranslate)
	if operation.CharacterLimit != DefaultCharacterLimit {
		t.Errorf("CharacterLimit = %d, want %d", operation.CharacterLimit, DefaultCharacterLimit)
	}
	if operation.TimeoutSeconds != DefaultTimeoutSeconds {
		t.Errorf("TimeoutSeconds = %d, want %d", operation.TimeoutSeconds, DefaultTimeoutSeconds)
	}
}

func TestActionFallsBackForUnknownKind(t *testing.T) {
	cfg := &Config{Actions: map[ActionKind]ActionConfig{}}

	if got := cfg.Action(ActionReviseEverything).Hotkey; got == "" {
		t.Error("a missing action should fall back to its default")
	}
}

func TestProviderForPrefersOverrideThenDefault(t *testing.T) {
	cfg := &Config{
		AIProvider: AIProviderConfig{
			Provider: BuiltInOpenAI,
			Providers: map[string]ProviderSettings{
				BuiltInOpenAI: {},
				BuiltInClaude: {},
			},
		},
		Operations: map[Operation]OperationConfig{
			OpTranslate: {ProviderID: BuiltInClaude},
			OpRevise:    {},
			OpDictate:   {ProviderID: "deleted-provider"},
		},
	}

	if got := cfg.ProviderFor(OpTranslate); got != BuiltInClaude {
		t.Errorf("override ignored: got %q", got)
	}
	if got := cfg.ProviderFor(OpRevise); got != BuiltInOpenAI {
		t.Errorf("empty override should use the default: got %q", got)
	}
	if got := cfg.ProviderFor(OpDictate); got != BuiltInOpenAI {
		t.Errorf("override naming a deleted provider should fall back: got %q", got)
	}
}

func TestConfiguredProvidersRequireUsableSettings(t *testing.T) {
	cfg := &Config{
		AIProvider: AIProviderConfig{
			Provider: BuiltInOpenAI,
			Providers: map[string]ProviderSettings{
				BuiltInOpenAI:   {BaseURL: "https://api.openai.com/v1", Model: "gpt-4o"},
				BuiltInClaude:   {BaseURL: "https://api.anthropic.com", Model: "claude"},
				"local":         {BaseURL: "http://localhost:1234/v1", Model: "llama", NoAPIKey: true},
				"missing-model": {BaseURL: "http://localhost:1234/v1", NoAPIKey: true},
			},
		},
	}

	encrypted, err := EncryptAPIKey("openai-key")
	if err != nil {
		t.Fatalf("EncryptAPIKey failed: %v", err)
	}
	settings := cfg.AIProvider.Providers[BuiltInOpenAI]
	settings.APIKey = encrypted
	cfg.AIProvider.Providers[BuiltInOpenAI] = settings

	configured := cfg.GetConfiguredProviderNames()
	if len(configured) != 2 || configured[0] != BuiltInOpenAI || configured[1] != "local" {
		t.Fatalf("configured providers = %v, want [openai local]", configured)
	}
}

func TestApplyDefaultsRepairsPartialConfig(t *testing.T) {
	cfg := &Config{}
	cfg.applyDefaults()

	if len(cfg.Actions) != len(ActionOrder) {
		t.Errorf("Actions has %d entries, want %d", len(cfg.Actions), len(ActionOrder))
	}
	if len(cfg.Operations) != len(OperationOrder) {
		t.Errorf("Operations has %d entries, want %d", len(cfg.Operations), len(OperationOrder))
	}
	if cfg.Translate.PrimaryLanguage == cfg.Translate.SecondaryLanguage {
		t.Error("language pair must differ")
	}
	if cfg.Appearance.Theme == "" {
		t.Error("theme was left empty")
	}
}

func TestActionKindClassification(t *testing.T) {
	if !ActionReviseEverything.SelectsAll() {
		t.Error("revise_everything should select the whole field")
	}
	if ActionReviseSelection.SelectsAll() || ActionTranslateSelection.SelectsAll() {
		t.Error("selection-scoped actions must not select all")
	}
	if OpDictate.UsesAI() {
		t.Error("dictate does not call a text provider")
	}
	if !OpTranslate.UsesAI() {
		t.Error("translate calls a text provider")
	}
}

// Pinned: nobody touches these defaults, so a quiet change would go unnoticed.
func TestShippedDefaultsAreUnchanged(t *testing.T) {
	if DefaultCharacterLimit != 1000 {
		t.Errorf("DefaultCharacterLimit = %d, want 1000", DefaultCharacterLimit)
	}
	if DefaultTimeoutSeconds != 60 {
		t.Errorf("DefaultTimeoutSeconds = %d, want 60", DefaultTimeoutSeconds)
	}
	if !Default().EnableProviderMentions {
		t.Error("provider mentions were on by default and should stay on")
	}
}

func TestDefaultHotkeysMatchWhatShipped(t *testing.T) {
	actions := DefaultActions()

	want := map[ActionKind]string{
		ActionReviseEverything:   "ctrl+alt+space",
		ActionTranslateSelection: "ctrl+alt+g",
	}
	if runtime.GOOS == "linux" {
		want[ActionReviseSelection] = "ctrl+super"
	}

	for kind, binding := range want {
		if got := actions[kind].Hotkey; got != binding {
			t.Errorf("%s default = %q, want %q", kind, got, binding)
		}
	}
}

func TestAskListensAndUsesItsOwnPrompt(t *testing.T) {
	if !ActionAskByVoice.Listens() || !ActionDictate.Listens() || ActionTranslateSelection.Listens() {
		t.Error("only the voice actions listen")
	}
	if !DefaultActions()[ActionAskByVoice].PushToTalk {
		t.Error("ask should default to hold-to-record")
	}
	if ActionAskByVoice.Operation() != OpAsk || !OpAsk.UsesAI() {
		t.Error("ask should run its own AI operation")
	}
	if DefaultPrompt(OpAsk) != prompt.Ask {
		t.Error("ask should default to the ask prompt")
	}
}

func TestMemoryCountsTheQuestionAndStaysInRange(t *testing.T) {
	for memory, want := range map[int]int{0: 0, 1: 0, 2: 1, 100: 99, 500: 99, -3: 0} {
		if got := (OperationConfig{Memory: memory}).Remembered(); got != want {
			t.Errorf("memory %d remembers %d earlier questions, want %d", memory, got, want)
		}
	}
}

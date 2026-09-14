package ui

import (
	"testing"

	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/utils"
)

// Regression: saving while the AI tab shows a built-in provider used to blank its BaseURL,
// dropping it from GetConfiguredProviderNames and silently resetting any operation override.
func TestSaveSettingsPreservesBuiltInBaseURLAndOperationOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	utils.EnsureAppHomeDir()

	cfg := config.Default()
	if err := cfg.SaveAPIKey(config.BuiltInOpenAI, "sk-test"); err != nil {
		t.Fatalf("SaveAPIKey: %v", err)
	}

	wantBaseURL := cfg.GetProviderSettings(config.BuiltInOpenAI).BaseURL
	if wantBaseURL == "" {
		t.Fatal("test setup: openai must ship with a BaseURL")
	}

	// The user deliberately pinned translate to OpenAI.
	cfg.SetOperation(config.OpTranslate, config.OperationConfig{
		SystemPrompt:   "custom prompt",
		CharacterLimit: 500,
		TimeoutSeconds: 30,
		ProviderID:     config.BuiltInOpenAI,
	})

	w := &MainWindow{config: cfg}
	w.providerBinding = binding.NewString()
	w.providerBinding.Set(config.BuiltInOpenAI)
	w.apiKeyBinding = binding.NewString()
	w.apiKeyBinding.Set("sk-test")
	w.modelBinding = binding.NewString()
	w.modelBinding.Set(cfg.GetProviderSettings(config.BuiltInOpenAI).Model)
	w.baseURLBinding = binding.NewString() // built-ins leave this blank; see loadProviderSettings

	editor := &operationEditor{
		prompt:   widget.NewMultiLineEntry(),
		limit:    widget.NewEntry(),
		timeout:  widget.NewSlider(5, 300),
		provider: widget.NewSelect(w.providerOptions(), nil),
	}
	editor.limit.SetText("500")
	editor.timeout.SetValue(30)
	editor.provider.SetSelected(providerLabel(config.BuiltInOpenAI))
	w.operationEditors = map[config.Operation]*operationEditor{config.OpTranslate: editor}

	// Same order saveSettings runs them in: an unrelated hotkey/action save right after the AI tab.
	if err := w.applyProviderSettings(); err != nil {
		t.Fatalf("applyProviderSettings: %v", err)
	}
	if err := w.applyActionSettings(); err != nil {
		t.Fatalf("applyActionSettings: %v", err)
	}

	if got := cfg.GetProviderSettings(config.BuiltInOpenAI).BaseURL; got != wantBaseURL {
		t.Errorf("openai BaseURL = %q, want %q (save wiped it)", got, wantBaseURL)
	}
	if got := cfg.Operation(config.OpTranslate).ProviderID; got != config.BuiltInOpenAI {
		t.Errorf("translate override = %q, want %q (save silently reset it to Default)", got, config.BuiltInOpenAI)
	}
}

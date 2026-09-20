package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/utils"
)

// Saving with a built-in provider on the AI tab must keep its BaseURL, or
// GetConfiguredProviderNames drops it and any operation override silently resets.
func TestSaveSettingsPreservesBuiltInBaseURLAndOperationOverride(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	utils.EnsureAppHomeDir()

	cfg := config.Default()
	if err := cfg.SetAPIKey(config.BuiltInOpenAI, "sk-test"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}

	wantBaseURL := cfg.GetProviderSettings(config.BuiltInOpenAI).BaseURL
	if wantBaseURL == "" {
		t.Fatal("test setup: openai must ship with a BaseURL")
	}

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

	// Same order as saveSettings.
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

// Publishing before applyActionSettings writes the hotkey flags would make
// listeners re-register shortcuts from a config that still says this one is off.
func TestSaveDoesNotPublishBeforeActionsAreApplied(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	utils.EnsureAppHomeDir()

	cfg := config.Default()
	if cfg.Action(config.ActionTranslate).Enabled {
		t.Fatal("test setup: translate must start disabled")
	}

	w := &MainWindow{config: cfg}
	w.providerBinding = binding.NewString()
	w.providerBinding.Set(config.BuiltInOpenAI)
	w.apiKeyBinding = binding.NewString()
	w.apiKeyBinding.Set("sk-test")
	w.modelBinding = binding.NewString()
	w.modelBinding.Set(cfg.GetProviderSettings(config.BuiltInOpenAI).Model)
	w.baseURLBinding = binding.NewString()

	w.hotkeyBindings = make(map[config.ActionKind]binding.String, len(config.ActionOrder))
	w.enables = make(map[config.ActionKind]*widget.Check, len(config.ActionOrder))
	for _, kind := range config.ActionOrder {
		action := cfg.Action(kind)

		value := binding.NewString()
		value.Set(action.Hotkey)
		w.hotkeyBindings[kind] = value

		check := widget.NewCheck("", nil)
		check.SetChecked(action.Enabled)
		w.enables[kind] = check
	}

	// Listeners run on their own goroutines, so collect rather than count.
	published := make(chan bool, 8)
	config.RegisterListener(func(cfg *config.Config) {
		select {
		case published <- cfg.Action(config.ActionTranslate).Enabled:
		default:
		}
	})

	w.enables[config.ActionTranslate].SetChecked(true)

	if err := w.applyProviderSettings(); err != nil {
		t.Fatalf("applyProviderSettings: %v", err)
	}
	if err := w.applyActionSettings(); err != nil {
		t.Fatalf("applyActionSettings: %v", err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	select {
	case enabled := <-published:
		if !enabled {
			t.Error("published while translate still read as disabled")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the save never published")
	}

	select {
	case <-published:
		t.Error("one save published twice; the first carries a half-applied config")
	case <-time.After(200 * time.Millisecond):
	}
}

package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/utils"
)

// Without its BaseURL, GetConfiguredProviderNames drops the provider and resets any override.
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
	w.lowReasoningBinding = binding.NewBool()

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

// Publishing early would re-register shortcuts from a config that still says this one is off.
func TestSaveDoesNotPublishBeforeActionsAreApplied(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	utils.EnsureAppHomeDir()

	cfg := config.Default()
	if cfg.Action(config.ActionTranslateSelection).Enabled {
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
	w.lowReasoningBinding = binding.NewBool()

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
		case published <- cfg.Action(config.ActionTranslateSelection).Enabled:
		default:
		}
	})

	w.enables[config.ActionTranslateSelection].SetChecked(true)

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

func TestSavingAskKeepsItsSettingsAndRefusesBadOnes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	utils.EnsureAppHomeDir()

	cfg := config.Default()
	w := &MainWindow{config: cfg}
	editor := &operationEditor{
		prompt:   widget.NewMultiLineEntry(),
		limit:    widget.NewEntry(),
		timeout:  widget.NewSlider(5, 300),
		provider: widget.NewSelect(w.providerOptions(), nil),
		ask:      w.newAskEditor(config.OperationConfig{}),
	}
	editor.limit.SetText("1000")
	editor.timeout.SetValue(30)
	editor.provider.SetSelected(providerDefaultOption)
	w.operationEditors = map[config.Operation]*operationEditor{config.OpAsk: editor}

	editor.ask.memory.SetText("5")
	editor.ask.tools.SetChecked(true)
	if err := w.applyActionSettings(); err != nil {
		t.Fatalf("saving: %v", err)
	}
	ask := cfg.Operation(config.OpAsk)
	if ask.Remembered() != 4 || !ask.Tools {
		t.Fatalf("saved %+v", ask)
	}

	editor.ask.memory.SetText("101")
	if err := w.applyActionSettings(); err == nil {
		t.Error("101 remembered messages were saved, past the maximum of 100")
	}
}

func TestThinkingLessIsOnByDefaultAndSavedPerProvider(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	utils.EnsureAppHomeDir()

	cfg := config.Default()
	w := &MainWindow{config: cfg}
	w.providerBinding = binding.NewString()
	w.apiKeyBinding = binding.NewString()
	w.modelBinding = binding.NewString()
	w.baseURLBinding = binding.NewString()
	w.lowReasoningBinding = binding.NewBool()
	thinksLess := func(provider string) bool {
		w.providerBinding.Set(provider)
		w.loadProviderSettings(provider)
		low, _ := w.lowReasoningBinding.Get()
		return low
	}

	if !thinksLess(config.BuiltInOpenAI) {
		t.Fatal("a provider should ask reasoning models to think less by default")
	}
	w.lowReasoningBinding.Set(false)
	if err := w.applyProviderSettings(); err != nil {
		t.Fatal(err)
	}

	if thinksLess(config.BuiltInOpenAI) {
		t.Error("unticking was not kept for the provider")
	}
	if !thinksLess(config.BuiltInGemini) {
		t.Error("unticking one provider changed another")
	}
}

// newSettingsWindow builds every tab saveSettings reads except System, whose overlay check needs a real display.
func newSettingsWindow(t *testing.T, cfg *config.Config) *MainWindow {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	utils.EnsureAppHomeDir()

	a := withDefaultTheme(t)
	w := &MainWindow{Window: a.NewWindow("Encre"), app: a, config: cfg}
	t.Cleanup(w.Close)
	w.initializing = true
	w.initBindings()
	w.createProviderSection()
	w.createHotkeysSection()
	w.createActionsSection()
	w.createSpeechSection()
	w.initializing = false
	return w
}

func status(w *MainWindow) string {
	text, _ := w.statusBinding.Get()
	return text
}

func TestAFailedSaveLeavesTheLiveConfigAlone(t *testing.T) {
	cases := map[string]func(w *MainWindow){
		"character limit": func(w *MainWindow) { w.operationEditors[config.OpTranslate].limit.SetText("0") },
		"ask memory":      func(w *MainWindow) { w.operationEditors[config.OpAsk].ask.memory.SetText("101") },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := config.Default()
			w := newSettingsWindow(t, cfg)
			provider := cfg.GetCurrentProvider()
			before := cfg.GetProviderSettings(provider)
			hotkey := cfg.Action(config.ActionOrder[0]).Hotkey
			prompt := cfg.Operation(config.OpRevise).SystemPrompt

			w.modelBinding.Set("another-model")
			w.apiKeyBinding.Set("sk-new")
			w.hotkeyBindings[config.ActionOrder[0]].Set("ctrl+alt+shift+f9")
			w.operationEditors[config.OpRevise].prompt.SetText("changed prompt")
			breakIt(w)

			w.saveSettings()

			if !strings.HasPrefix(status(w), "Error") {
				t.Fatalf("status = %q", status(w))
			}
			if got := cfg.GetProviderSettings(provider); got != before {
				t.Errorf("provider settings changed to %+v", got)
			}
			if got := cfg.Action(config.ActionOrder[0]).Hotkey; got != hotkey {
				t.Errorf("hotkey changed to %q", got)
			}
			if got := cfg.Operation(config.OpRevise).SystemPrompt; got != prompt {
				t.Errorf("revise prompt changed to %q", got)
			}
		})
	}
}

func TestACustomProviderWithoutABaseURLChangesNothing(t *testing.T) {
	cfg := config.Default()
	if err := cfg.AddCustomProvider("local", config.ProviderSettings{BaseURL: "http://localhost:11434/v1", Model: "llama", NoAPIKey: true}); err != nil {
		t.Fatal(err)
	}
	w := newSettingsWindow(t, cfg)
	current := cfg.GetCurrentProvider()
	w.providerBinding.Set("local")
	w.loadProviderSettings("local")
	w.baseURLBinding.Set("")
	w.modelBinding.Set("other")

	w.saveSettings()

	if !strings.HasPrefix(status(w), "Error") || cfg.GetCurrentProvider() != current || cfg.GetProviderSettings("local").Model != "llama" {
		t.Fatalf("status %q, current %q, model %q", status(w), cfg.GetCurrentProvider(), cfg.GetProviderSettings("local").Model)
	}
}

// refuseStartOnLogin puts a file where the autostart directory belongs, which makes enabling fail.
func refuseStartOnLogin(t *testing.T) {
	t.Helper()
	home, _ := os.UserHomeDir()
	if err := os.MkdirAll(filepath.Join(home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "autostart"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// queuedDriver runs fyne.Do later, as the running app does on its own thread; the test driver runs it at once.
type queuedDriver struct {
	fyne.Driver
	queue []func()
}

func (d *queuedDriver) DoFromGoroutine(run func(), _ bool) { d.queue = append(d.queue, run) }

func (d *queuedDriver) drain() {
	for len(d.queue) > 0 {
		run := d.queue[0]
		d.queue = d.queue[1:]
		run()
	}
}

type queuedApp struct {
	fyne.App
	driver *queuedDriver
}

func (a queuedApp) Driver() fyne.Driver { return a.driver }

func queueFyneDo(t *testing.T, a fyne.App) *queuedDriver {
	t.Helper()
	driver := &queuedDriver{Driver: a.Driver()}
	fyne.SetCurrentApp(queuedApp{App: a, driver: driver})
	t.Cleanup(func() { fyne.SetCurrentApp(a) })
	return driver
}

func TestASaveLeavesNothingUnsavedOnceTheQueueRuns(t *testing.T) {
	for name, refuse := range map[string]bool{"start on login refused": true, "ordinary": false} {
		t.Run(name, func(t *testing.T) {
			w := newSettingsWindow(t, config.Default())
			if refuse {
				refuseStartOnLogin(t)
				w.startOnLoginBinding.Set(true)
			}
			w.markDirty()
			queue := queueFyneDo(t, w.app)

			w.saveSettings()
			queue.drain()

			if refuse && !strings.Contains(status(w), "Start on login") {
				t.Fatalf("status = %q", status(w))
			}
			if w.dirty {
				t.Fatal("the saved settings still show as unsaved")
			}
		})
	}
}

func TestStartOnLoginTheSystemRefusesIsShownAndNotSaved(t *testing.T) {
	cfg := config.Default()
	w := newSettingsWindow(t, cfg)
	refuseStartOnLogin(t)
	w.startOnLoginBinding.Set(true)

	w.saveSettings()

	if got := status(w); got == "Settings saved" || !strings.Contains(got, "Start on login") {
		t.Fatalf("status = %q", got)
	}
	if cfg.AppearanceSettings().StartOnLogin {
		t.Error("StartOnLogin was saved as on although the system refused it")
	}
	if on, _ := w.startOnLoginBinding.Get(); on {
		t.Error("the checkbox still claims Start on login is on")
	}
}

func TestASpeechKeyThatCannotBeEncryptedIsNotStoredInPlainText(t *testing.T) {
	cfg := config.Default()
	w := newSettingsWindow(t, cfg)
	w.speechRemoteKey.SetText("sk-speech")

	encrypt := encryptAPIKey
	encryptAPIKey = func(plain string) (string, error) {
		if plain == "sk-speech" {
			return "", errors.New("no entropy")
		}
		return encrypt(plain)
	}
	t.Cleanup(func() { encryptAPIKey = encrypt })

	w.saveSettings()

	if got := cfg.SpeechSettings().RemoteAPIKey; got == "sk-speech" {
		t.Fatal("the speech key was stored in plain text")
	}
	if !strings.HasPrefix(status(w), "Error") {
		t.Fatalf("status = %q", status(w))
	}
}

func TestShortenedErrorsKeepWholeCharacters(t *testing.T) {
	w := &MainWindow{statusBinding: binding.NewString()}
	w.reportSaveError("Error", errors.New(strings.Repeat("é", statusErrorLimit)))
	if got := status(w); !utf8.ValidString(got) {
		t.Errorf("status bar message %q is not valid UTF-8", got)
	}
	if got := shortMessage(strings.Repeat("ü", 100)); !utf8.ValidString(got) || utf8.RuneCountInString(got) != 80 {
		t.Errorf("shortMessage = %q", got)
	}
}

func TestDeletingAProviderTakesItOutOfTheActions(t *testing.T) {
	cfg := config.Default()
	if err := cfg.AddCustomProvider("local", config.ProviderSettings{BaseURL: "http://localhost:11434/v1", Model: "llama", NoAPIKey: true}); err != nil {
		t.Fatal(err)
	}
	cfg.SetOperation(config.OpTranslate, config.OperationConfig{CharacterLimit: 500, TimeoutSeconds: 30, ProviderID: "local"})
	w := newSettingsWindow(t, cfg)
	editor := w.operationEditors[config.OpTranslate]
	if editor.provider.Selected != "local" {
		t.Fatalf("test setup: translate uses %q", editor.provider.Selected)
	}

	w.deleteProvider("local")

	if slices.Contains(editor.provider.Options, "local") || editor.provider.Selected != providerDefaultOption {
		t.Errorf("the Actions tab still offers %v with %q chosen", editor.provider.Options, editor.provider.Selected)
	}
	if got := cfg.Operation(config.OpTranslate).ProviderID; got != "" {
		t.Errorf("translate still points at %q", got)
	}
}

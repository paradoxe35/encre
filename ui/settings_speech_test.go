package ui

import (
	"testing"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/stt"
)

func TestAdoptPresetModel(t *testing.T) {
	gemini, _ := stt.FindPreset("gemini")
	openai, _ := stt.FindPreset("openai")
	custom, _ := stt.FindPreset("custom")

	cases := []struct {
		name    string
		current string
		preset  stt.RemotePreset
		want    string
	}{
		{"empty takes the default", "", gemini, gemini.Models[0]},
		{"another service's model is replaced", "gpt-4o-transcribe", gemini, gemini.Models[0]},
		{"a model of this service is kept", "whisper-1", openai, "whisper-1"},
		{"a hand-typed model is kept", "my-own-finetune", gemini, "my-own-finetune"},
		{"custom suggests nothing, so nothing changes", "gpt-4o-transcribe", custom, "gpt-4o-transcribe"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := widget.NewSelectEntry(nil)
			entry.SetText(tc.current)

			w := &MainWindow{speechRemoteModel: entry}
			w.adoptPresetModel(tc.preset)

			if entry.Text != tc.want {
				t.Errorf("model = %q, want %q", entry.Text, tc.want)
			}
		})
	}
}

// newLanguageWindow builds just enough MainWindow for the language picker.
func newLanguageWindow(service, model, saved string) *MainWindow {
	return newEngineWindow(service, model, saved, "")
}

// newEngineWindow adds a local model, so a switch between engines can be driven.
func newEngineWindow(service, model, saved, localModelID string) *MainWindow {
	w := &MainWindow{config: config.Default()}

	speech := w.config.SpeechSettings()
	speech.Language = saved
	speech.ModelID = localModelID
	w.config.SetSpeechSettings(speech)
	w.speechModelDraft = localModelID

	w.speechLanguage = widget.NewSelect(nil, nil)
	w.speechLanguageEntry = widget.NewSelectEntry(nil)
	w.speechLanguageBox = container.NewStack(w.speechLanguage, w.speechLanguageEntry)

	w.speechRemote = widget.NewSelect(stt.PresetNames(), nil)
	w.speechRemote.Selected = service
	w.speechRemoteModel = widget.NewSelectEntry(nil)
	w.speechRemoteModel.Text = model

	return w
}

// The list follows the service and the model, not the local catalogue.
func TestRefreshRemoteLanguagesPerService(t *testing.T) {
	cases := []struct {
		service, model string
		wantFreeText   bool
		wantSelected   string
	}{
		{"OpenAI", "whisper-1", false, "French"},
		{"OpenAI", "gpt-transcribe", false, "French"},
		{"Groq", "whisper-large-v3", false, "French"},
		{"Google Gemini", "gemini-3.5-transcribe", false, "French"},
		{"Custom", "some-local-server", true, "French"},
		{"Google Gemini", "gemini-3.8-flash", true, "French"},
	}

	for _, tc := range cases {
		t.Run(tc.service+"/"+tc.model, func(t *testing.T) {
			w := newLanguageWindow(tc.service, tc.model, "fr")
			w.refreshLanguages(config.SpeechRemote)

			if w.speechLanguageEntry.Visible() != tc.wantFreeText {
				t.Fatalf("free text = %v, want %v", w.speechLanguageEntry.Visible(), tc.wantFreeText)
			}
			if w.speechLanguage.Visible() == tc.wantFreeText {
				t.Fatal("both pickers visible, or neither")
			}

			if tc.wantFreeText {
				if w.speechLanguageEntry.Text != tc.wantSelected {
					t.Errorf("value = %q, want %q", w.speechLanguageEntry.Text, tc.wantSelected)
				}
				return
			}
			if w.speechLanguage.Selected != tc.wantSelected {
				t.Errorf("selected = %q, want %q", w.speechLanguage.Selected, tc.wantSelected)
			}
		})
	}
}

// A saved language is restored on load, in the form the service takes: the same
// fr in the config reads as fr-FR under Gemini and fr under Groq.
func TestSavedLanguageRestoredInServiceForm(t *testing.T) {
	gemini := newLanguageWindow("Google Gemini", "gemini-3.5-transcribe", "fr")
	gemini.refreshLanguages(config.SpeechRemote)
	if got := gemini.selectedSpeechLanguage(); got != "fr-FR" {
		t.Errorf("Gemini French = %q, want fr-FR", got)
	}

	groq := newLanguageWindow("Groq", "whisper-large-v3", "fr")
	groq.refreshLanguages(config.SpeechRemote)
	if got := groq.selectedSpeechLanguage(); got != "fr" {
		t.Errorf("Groq French = %q, want fr", got)
	}
}

// Changing service is the same event as picking another local model: the new
// engine decides, and every hosted engine detects. Matches Model.LanguageAfterSwitch.
func TestServiceSwitchFallsBackToDetect(t *testing.T) {
	w := newLanguageWindow("Google Gemini", "gemini-3.5-transcribe", "fr")
	w.refreshLanguages(config.SpeechRemote)
	if w.speechLanguage.Selected != "French" {
		t.Fatalf("initial = %q, want the saved language restored", w.speechLanguage.Selected)
	}

	w.speechRemote.Selected = "Groq"
	w.speechRemoteModel.Text = "whisper-large-v3"
	w.refreshLanguages(config.SpeechRemote)

	if w.speechLanguage.Selected != detectLanguageLabel {
		t.Errorf("after switch = %q, want %q", w.speechLanguage.Selected, detectLanguageLabel)
	}
	if got := w.selectedSpeechLanguage(); got != "" {
		t.Errorf("picker holds %q; the switch should hand the choice to the engine", got)
	}
	if got := w.config.SpeechSettings().Language; got != "fr" {
		t.Errorf("config changed to %q before Save", got)
	}
}

// whisper-1 and gpt-transcribe are different sets, so moving between them is a
// switch; retyping inside one set is not, or typing a name would clear the
// language on every keystroke.
func TestModelSwitchOnlyResetsOnSetChange(t *testing.T) {
	w := newLanguageWindow("OpenAI", "whisper-1", "fr")
	w.refreshLanguages(config.SpeechRemote)

	for _, partial := range []string{"whisper-", "whisper-l", "whisper-large-v3"} {
		w.speechRemoteModel.Text = partial
		w.refreshLanguages(config.SpeechRemote)
		if w.speechLanguage.Selected != "French" {
			t.Fatalf("typing %q cleared the language", partial)
		}
	}

	w.speechRemoteModel.Text = "gpt-transcribe"
	w.refreshLanguages(config.SpeechRemote)
	if w.speechLanguage.Selected != detectLanguageLabel {
		t.Errorf("crossing to another set = %q, want %q",
			w.speechLanguage.Selected, detectLanguageLabel)
	}
}

// A language the new service cannot serve falls back rather than being sent.
func TestUnservableLanguageFallsBackToDetect(t *testing.T) {
	w := newLanguageWindow("Groq", "whisper-large-v3", "kea-CV")
	w.refreshLanguages(config.SpeechRemote)

	if w.speechLanguage.Selected != detectLanguageLabel {
		t.Errorf("selected = %q, want %q", w.speechLanguage.Selected, detectLanguageLabel)
	}
	if got := w.selectedSpeechLanguage(); got != "" {
		t.Errorf("code = %q, want empty for auto-detect", got)
	}
}

// A code typed for an unknown model is saved as typed, not dropped.
func TestFreeTextLanguageIsSaved(t *testing.T) {
	w := newLanguageWindow("Custom", "some-local-server", "")
	w.refreshLanguages(config.SpeechRemote)

	if w.speechLanguageEntry.Text != detectLanguageLabel {
		t.Fatalf("value = %q, want %q", w.speechLanguageEntry.Text, detectLanguageLabel)
	}
	if got := w.selectedSpeechLanguage(); got != "" {
		t.Errorf("code = %q, want empty", got)
	}

	w.speechLanguageEntry.Text = "sw-TZ"
	if got := w.selectedSpeechLanguage(); got != "sw-TZ" {
		t.Errorf("typed code = %q, want sw-TZ", got)
	}

	w.speechLanguageEntry.Text = "French"
	if got := w.selectedSpeechLanguage(); got != "fr" {
		t.Errorf("picked suggestion = %q, want fr", got)
	}
}

// englishOnlyModel is a real catalogue entry that cannot detect: the case the
// rule exists for.
func englishOnlyModel(t *testing.T) stt.Model {
	t.Helper()
	for _, model := range stt.Catalogue() {
		if !model.LanguageDetect && len(model.Languages) == 1 && model.Languages[0] == "en" {
			return model
		}
	}
	t.Skip("no English-only model in the catalogue")
	return stt.Model{}
}

func multilingualDetectingModel(t *testing.T) stt.Model {
	t.Helper()
	for _, model := range stt.Catalogue() {
		if model.LanguageDetect && len(model.Languages) > 20 {
			return model
		}
	}
	t.Skip("no detecting model in the catalogue")
	return stt.Model{}
}

// Case 1: hosted detects, the local model cannot, so the switch lands on a
// concrete language rather than leaving the engine to guess.
func TestSwitchFromHostedAutoToNonDetectingLocal(t *testing.T) {
	local := englishOnlyModel(t)
	w := newEngineWindow("OpenAI", "gpt-transcribe", "", local.ID)

	w.refreshLanguages(config.SpeechRemote)
	if w.speechLanguage.Selected != detectLanguageLabel {
		t.Fatalf("hosted started at %q, want Auto-detect", w.speechLanguage.Selected)
	}

	w.refreshLanguages(config.SpeechLocal)

	if got := w.selectedSpeechLanguage(); got != "en" {
		t.Errorf("after the switch the picker holds %q, want en", got)
	}
	if w.speechLanguage.Selected != "English" {
		t.Errorf("picker shows %q, want English", w.speechLanguage.Selected)
	}
}

// Case 2: a language the next engine can serve is kept, not thrown away.
func TestSpecificLanguageSurvivesToAnEngineThatSpeaksIt(t *testing.T) {
	local := multilingualDetectingModel(t)
	if !local.Speaks("fr") {
		t.Skip("the detecting model does not speak French")
	}

	// Same set both times, so nothing is re-decided: French must simply stay.
	w := newEngineWindow("Groq", "whisper-large-v3", "fr", local.ID)
	w.refreshLanguages(config.SpeechRemote)

	if w.speechLanguage.Selected != "French" {
		t.Fatalf("picker shows %q, want French", w.speechLanguage.Selected)
	}
	if got := w.selectedSpeechLanguage(); got != "fr" {
		t.Errorf("code = %q, want fr", got)
	}
}

// Switching to an engine that detects hands the decision back to it.
func TestSwitchToDetectingEngineReturnsToAuto(t *testing.T) {
	local := englishOnlyModel(t)
	w := newEngineWindow("Groq", "whisper-large-v3", "en", local.ID)

	w.refreshLanguages(config.SpeechLocal)
	if got := w.selectedSpeechLanguage(); got != "en" {
		t.Fatalf("local holds %q, want en", got)
	}

	w.refreshLanguages(config.SpeechRemote)

	if got := w.selectedSpeechLanguage(); got != "" {
		t.Errorf("hosted holds %q, want auto-detect", got)
	}
	if w.speechLanguage.Selected != detectLanguageLabel {
		t.Errorf("picker shows %q, want %q", w.speechLanguage.Selected, detectLanguageLabel)
	}
}

// The first draw shows what was saved rather than re-deciding it, or reopening
// the window would quietly discard the user's choice.
func TestFirstDrawRestoresRatherThanDecides(t *testing.T) {
	local := multilingualDetectingModel(t)
	w := newEngineWindow("Groq", "whisper-large-v3", "de", local.ID)

	w.refreshLanguages(config.SpeechRemote)

	if got := w.config.SpeechSettings().Language; got != "de" {
		t.Errorf("config changed to %q on first draw, want de untouched", got)
	}
	if w.speechLanguage.Selected != "German" {
		t.Errorf("picker shows %q, want German", w.speechLanguage.Selected)
	}
}

// Redrawing the same engine is not a switch and must decide nothing.
func TestRedrawingTheSameEngineKeepsTheLanguage(t *testing.T) {
	local := multilingualDetectingModel(t)
	w := newEngineWindow("Groq", "whisper-large-v3", "de", local.ID)

	for range 3 {
		w.refreshLanguages(config.SpeechRemote)
	}

	if got := w.config.SpeechSettings().Language; got != "de" {
		t.Errorf("config drifted to %q over repeated draws", got)
	}
}

// Typing a model name one letter at a time must not clear the language on every
// keystroke; only crossing into another set counts as a switch.
func TestTypingAModelNameDoesNotClearTheLanguage(t *testing.T) {
	w := newEngineWindow("OpenAI", "whisper-1", "fr", "")
	w.refreshLanguages(config.SpeechRemote)

	for _, partial := range []string{"whisper-", "whisper-l", "whisper-large-v3"} {
		w.speechRemoteModel.Text = partial
		w.refreshLanguages(config.SpeechRemote)
		if got := w.config.SpeechSettings().Language; got != "fr" {
			t.Fatalf("typing %q changed the language to %q", partial, got)
		}
	}
}

func nonDetectingModelSpeaking(t *testing.T, codes ...string) stt.Model {
	t.Helper()
	for _, model := range stt.Catalogue() {
		if model.LanguageDetect {
			continue
		}
		speaksAll := true
		for _, code := range codes {
			speaksAll = speaksAll && model.Speaks(code)
		}
		if speaksAll {
			return model
		}
	}
	t.Skipf("no non-detecting model in the catalogue speaks %v", codes)
	return stt.Model{}
}

func TestSwitchStartsFromTheUnsavedPick(t *testing.T) {
	local := nonDetectingModelSpeaking(t, "de", "fr")
	w := newEngineWindow("Groq", "whisper-large-v3", "fr", local.ID)
	w.refreshLanguages(config.SpeechRemote)

	w.speechLanguage.SetSelected("German")
	w.refreshLanguages(config.SpeechLocal)

	if got := w.selectedSpeechLanguage(); got != "de" {
		t.Errorf("after the switch the picker holds %q, want the German just picked", got)
	}
	if got := w.config.SpeechSettings().Language; got != "fr" {
		t.Errorf("config changed to %q before Save", got)
	}
}

func TestBuildingTheSpeechSectionKeepsTheSavedLanguage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	local := multilingualDetectingModel(t)
	if !local.Speaks("fr") {
		t.Skip("the detecting model does not speak French")
	}

	w := &MainWindow{config: config.Default(), initializing: true}
	w.Window = test.NewWindow(nil)
	defer w.Window.Close()
	w.statusBinding = binding.NewString()

	speech := w.config.SpeechSettings()
	speech.Engine = config.SpeechLocal
	speech.ModelID = local.ID
	speech.Language = "fr"
	w.config.SetSpeechSettings(speech)

	w.createSpeechSection()
	w.initializing = false

	if got := w.config.SpeechSettings().Language; got != "fr" {
		t.Errorf("building the tab changed the saved language to %q", got)
	}
	if got := w.selectedSpeechLanguage(); got != "fr" {
		t.Errorf("picker shows %q, want the saved French", got)
	}
	if w.languageSetKey != "local:"+local.ID {
		t.Errorf("set key = %q; the tab should end on the saved engine", w.languageSetKey)
	}

	other := englishOnlyModel(t)
	w.speechModels.onSelect(other)
	if w.speechModelDraft != other.ID {
		t.Errorf("picked model draft = %q, want %q", w.speechModelDraft, other.ID)
	}
	if got := w.config.SpeechSettings().ModelID; got != local.ID {
		t.Errorf("picking a model wrote %q to the config before Save", got)
	}
	if got := w.selectedSpeechLanguage(); got != "en" {
		t.Errorf("picker holds %q after moving to an English-only model, want en", got)
	}

	w.speechModels.onDeleted(other)
	if w.speechModelDraft != "" {
		t.Errorf("deleting the picked model left the draft at %q", w.speechModelDraft)
	}
}

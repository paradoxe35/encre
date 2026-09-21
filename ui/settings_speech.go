package ui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/permissions"
	"github.com/paradoxe35/encre/internal/stt"
	"github.com/paradoxe35/encre/internal/stt/witai"
)

const (
	localEngineLabel    = "On this computer"
	remoteEngineLabel   = "Hosted service"
	witaiEngineLabel    = "Wit.ai (free)"
	detectLanguageLabel = "Auto-detect"
)

func (w *MainWindow) speechLanguageCode(label string) string {
	if label == "" || label == detectLanguageLabel {
		return ""
	}
	if code, ok := w.speechLanguageCodes[label]; ok {
		return code
	}
	// Typed rather than picked: a code for a model with no list.
	return strings.TrimSpace(label)
}

func (w *MainWindow) selectedSpeechLanguage() string {
	if w.speechLanguageEntry != nil && w.speechLanguageEntry.Visible() {
		return w.speechLanguageCode(w.speechLanguageEntry.Text)
	}
	if w.speechLanguage == nil {
		return w.speechLanguageDraft
	}
	return w.speechLanguageCode(w.speechLanguage.Selected)
}

func (w *MainWindow) createSpeechSection() fyne.CanvasObject {
	local := w.localSpeechPane()
	remote := w.remoteSpeechPane()
	witaiPane := w.witaiSpeechPane()

	engineOptions := []string{localEngineLabel, remoteEngineLabel}
	if witai.Available() {
		engineOptions = append(engineOptions, witaiEngineLabel)
	}

	w.speechEngine = w.dirtySelect(engineOptions, func(label string) {
		local.Hide()
		remote.Hide()
		witaiPane.Hide()

		switch label {
		case remoteEngineLabel:
			remote.Show()
		case witaiEngineLabel:
			witaiPane.Show()
		default:
			local.Show()
		}

		w.refreshLanguages(engineFromLabel(label))
	})
	w.speechEngine.SetSelected(engineLabel(w.config.SpeechSettings().Engine))

	// Options live in a dialog so the model list keeps full height.
	options := widget.NewButtonWithIcon("", theme.SettingsIcon(), w.showSpeechOptions)
	options.Importance = widget.LowImportance

	w.buildSpeechOptions()

	w.microphoneNotice = microphoneRefusedNotice()

	hint := widget.NewLabel("Hold the dictate shortcut, speak, release.")
	hint.Wrapping = fyne.TextWrapWord

	return container.NewBorder(
		container.NewPadded(container.NewVBox(
			hint,
			w.microphoneNotice,
			container.NewBorder(nil, nil, widget.NewLabel("Transcribe"), options,
				container.NewGridWithColumns(2, w.speechEngine, w.speechLanguageBox)),
		)),
		nil, nil, nil,
		container.NewStack(local, remote, witaiPane),
	)
}

func engineFromLabel(label string) config.SpeechEngine {
	switch label {
	case remoteEngineLabel:
		return config.SpeechRemote
	case witaiEngineLabel:
		return config.SpeechWitAI
	default:
		return config.SpeechLocal
	}
}

func engineLabel(engine config.SpeechEngine) string {
	switch {
	case engine == config.SpeechRemote:
		return remoteEngineLabel
	case engine == config.SpeechWitAI && witai.Available():
		return witaiEngineLabel
	default:
		return localEngineLabel
	}
}

func microphoneRefusedNotice() *fyne.Container {
	text := widget.NewLabel("Dictation was refused the microphone. Allow Encre, then hold the shortcut again.")
	text.Wrapping = fyne.TextWrapWord
	text.Importance = widget.WarningImportance

	grant := widget.NewButtonWithIcon("Grant access", theme.SettingsIcon(), func() {
		permissions.OpenPreference(permissions.Microphone)
	})

	notice := container.NewBorder(nil, nil, nil, grant, text)
	notice.Hide()
	return notice
}

func (w *MainWindow) localSpeechPane() *fyne.Container {
	catalog := stt.Models()
	store := w.speechStore()
	w.speechModelDraft = w.config.SpeechSettings().ModelID
	active := widget.NewLabel(activeModelText(w.speechModelDraft, catalog.Models, store.Downloaded))
	active.TextStyle.Bold = true

	w.speechModels = NewModelList(store, w.Window, w.speechModelDraft,
		func(model stt.Model) {
			w.speechModelDraft = model.ID
			w.refreshLanguages(config.SpeechLocal)
			w.markDirty()
			w.statusBinding.Set("Speech model set to " + model.Name)
		})
	w.speechModels.SetActiveChanged(active.SetText)
	w.speechModels.SetDeleted(func(model stt.Model) {
		if model.ID != w.speechModelDraft {
			return
		}
		w.speechModelDraft = ""
		w.refreshLanguages(config.SpeechLocal)
		w.markDirty()
	})

	summary := widget.NewLabel(fmt.Sprintf("%d models", len(catalog.Models)))
	summary.TextStyle.Italic = true

	// The daily refresh and the button both land here; the widgets are only
	// touched on Fyne's thread.
	stt.OnCatalogChanged(func() {
		fyne.Do(func() {
			w.speechModels.Reload()
			summary.SetText(fmt.Sprintf("%d models", len(stt.Models().Models)))
		})
	})

	refresh := widget.NewButton("Check for new", w.refreshCatalog)

	return container.NewBorder(
		container.NewPadded(container.NewBorder(nil, nil, summary,
			container.NewHBox(active, refresh))),
		nil, nil, nil,
		w.speechModels,
	)
}

// Forces a rebuild from Hugging Face whatever the cache's age; the list
// itself updates through the catalogue subscription.
func (w *MainWindow) refreshCatalog() {
	w.statusBinding.Set("Checking for new models…")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), stt.RefreshTimeout)
		defer cancel()

		err := stt.Refresh(ctx)
		fyne.Do(func() {
			if err != nil {
				w.statusBinding.Set("Could not update the model list")
				return
			}
			w.statusBinding.Set("Model list updated")
		})
	}()
}

func (w *MainWindow) remoteSpeechPane() *fyne.Container {
	speech := w.config.SpeechSettings()

	w.speechRemoteModel = w.dirtySelectEntry()
	w.speechRemoteModel.SetText(speech.RemoteModel)
	w.speechRemoteModel.OnChanged = func(string) {
		w.markDirty()
		w.refreshLanguages(config.SpeechRemote)
	}

	w.speechRemoteURL = w.dirtyEntry()
	w.speechRemoteURL.SetPlaceHolder("https://api.example.com/v1")
	w.speechRemoteURL.SetText(speech.RemoteBaseURL)

	w.speechRemoteKey = w.dirtyPasswordEntry()
	w.speechRemoteKey.SetPlaceHolder("API key")
	w.speechRemoteKey.SetText(decryptedRemoteAPIKey(speech.RemoteAPIKey))

	w.speechRemote = w.dirtySelect(stt.PresetNames(), w.applyPreset)
	if preset, ok := stt.FindPreset(speech.RemoteProvider); ok {
		w.speechRemote.SetSelected(preset.Name)
	} else {
		w.speechRemote.SetSelected(stt.RemotePresets[0].Name)
	}

	note := widget.NewLabel("Audio is sent to this service. Nothing is downloaded.")
	note.Wrapping = fyne.TextWrapWord
	note.TextStyle.Italic = true

	return container.NewPadded(container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("Service", w.speechRemote),
			widget.NewFormItem("Model", w.speechRemoteModel),
			widget.NewFormItem("Endpoint", w.speechRemoteURL),
			widget.NewFormItem("API key", w.speechRemoteKey),
		),
		note,
	))
}

// The endpoint is locked for known services so a typo cannot break a working provider.
func (w *MainWindow) applyPreset(name string) {
	preset, ok := stt.PresetByName(name)
	if !ok {
		return
	}

	w.speechRemoteModel.SetOptions(preset.Models)
	if preset.ID == "custom" {
		w.speechRemoteURL.Enable()
	} else {
		w.speechRemoteURL.SetText(preset.BaseURL)
		w.speechRemoteURL.Disable()
	}
	w.adoptPresetModel(preset)
	w.refreshLanguages(config.SpeechRemote)
}

// Another service's model would be rejected, so it is swapped for the preset default;
// a value belonging to no preset was typed by hand and is left alone.
func (w *MainWindow) adoptPresetModel(preset stt.RemotePreset) {
	if len(preset.Models) == 0 {
		return
	}

	current := w.speechRemoteModel.Text
	if slices.Contains(preset.Models, current) {
		return
	}
	if current != "" && !stt.IsPresetModel(current) {
		return
	}

	w.speechRemoteModel.SetText(preset.Models[0])
}

// Built once so Save reads the same widgets whether or not the dialog was opened.
func (w *MainWindow) buildSpeechOptions() {
	speech := w.config.SpeechSettings()

	w.microphone = NewMicrophonePicker(speech.InputDevice)
	w.microphone.onChanged = w.markDirty

	w.speechLanguage = w.dirtySelect(nil, nil)
	w.speechLanguageEntry = w.dirtySelectEntry()
	w.speechLanguageEntry.Hide()
	w.speechLanguageBox = container.NewStack(w.speechLanguage, w.speechLanguageEntry)
	w.refreshLanguages(speech.Engine)

	w.speechKeepLoaded = w.dirtyCheck("Keep the model in memory", speech.KeepModelLoaded)
	w.speechCleanUp = w.dirtyCheck("Tidy the transcript with AI", speech.CleanUp)
}

// key tells a change of set from a redraw; resolve only runs on a change.
type languageSet struct {
	key     string
	resolve func() (codes []string, detects bool)
}

// The model is part of a hosted set: whisper-1 and gpt-transcribe accept different codes.
func (w *MainWindow) currentLanguageSet(engine config.SpeechEngine) languageSet {
	switch engine {
	case config.SpeechWitAI:
		if witai.Available() {
			// A Wit app is built for one language, so it never detects.
			return languageSet{key: "witai", resolve: func() ([]string, bool) { return witai.Languages(), false }}
		}

	case config.SpeechRemote:
		preset, _ := stt.PresetByName(w.speechRemote.Selected)
		model := w.speechRemoteModel.Text
		return languageSet{
			key: "remote:" + stt.LanguageSetName(preset.ID, model),
			resolve: func() ([]string, bool) {
				return stt.Codes(stt.LanguagesFor(preset.ID, model)), true
			},
		}
	}

	modelID := w.speechModelDraft
	return languageSet{
		key: "local:" + modelID,
		resolve: func() ([]string, bool) {
			model, _ := stt.FindModel(modelID)
			return model.Languages, model.LanguageDetect
		},
	}
}

// The settled language is draft state until Save; looking at the dropdown must not change the config.
func (w *MainWindow) refreshLanguages(engine config.SpeechEngine) {
	set := w.currentLanguageSet(engine)

	switch {
	case w.initializing || w.languageSetKey == "":
		w.languageSetKey = set.key
		w.speechLanguageDraft = w.config.SpeechSettings().Language

	case set.key == w.languageSetKey:
		return

	default:
		w.languageSetKey = set.key
		codes, detects := set.resolve()
		w.speechLanguageDraft = stt.SwitchLanguage(codes, detects,
			w.selectedSpeechLanguage(), stt.SystemLanguage())
	}

	switch {
	case engine == config.SpeechWitAI && witai.Available():
		w.refreshWitAILanguages()
	case engine == config.SpeechRemote:
		w.refreshRemoteLanguages()
	default:
		w.refreshSpeechLanguages()
	}
}

// Each hosted model accepts its own codes (whisper-1 ISO 639-1, Gemini BCP-47 locales);
// an unknown model gets a typable box rather than a false list.
func (w *MainWindow) refreshRemoteLanguages() {
	if w.speechLanguage == nil {
		return
	}

	preset, _ := stt.PresetByName(w.speechRemote.Selected)
	languages := stt.LanguagesFor(preset.ID, w.speechRemoteModel.Text)
	if languages == nil {
		w.showFreeformLanguages()
		return
	}

	w.speechLanguage.Options, w.speechLanguageCodes = namedLanguageOptions(languages)
	w.showFixedLanguages()

	// Carry the choice across a change of service: fr-FR and fr are one request.
	selected := detectLanguageLabel
	if code := stt.MatchLanguage(languages, w.speechLanguageDraft); code != "" {
		for _, language := range languages {
			if language.Code == code {
				selected = language.Name
				break
			}
		}
	}
	w.speechLanguage.SetSelected(selected)
}

// Suggests Whisper's codes: a custom endpoint is usually a Whisper server, and anything else can be typed.
func (w *MainWindow) showFreeformLanguages() {
	labels, codeByName := namedLanguageOptions(stt.SuggestedLanguages())
	w.speechLanguageCodes = codeByName
	w.speechLanguageEntry.SetOptions(labels)

	if w.speechLanguageDraft == "" {
		w.speechLanguageEntry.SetText(detectLanguageLabel)
	} else {
		w.speechLanguageEntry.SetText(stt.LanguageName(w.speechLanguageDraft))
	}

	w.speechLanguage.Hide()
	w.speechLanguageEntry.Show()
	w.speechLanguageBox.Refresh()
}

func (w *MainWindow) showFixedLanguages() {
	w.speechLanguageEntry.Hide()
	w.speechLanguage.Show()
	w.speechLanguageBox.Refresh()
}

// Hosted services name their languages; Auto-detect leads the list.
func namedLanguageOptions(languages []stt.NamedLanguage) ([]string, map[string]string) {
	labels := make([]string, 0, len(languages)+1)
	codeByName := make(map[string]string, len(languages))

	labels = append(labels, detectLanguageLabel)
	for _, language := range languages {
		labels = append(labels, language.Name)
		codeByName[language.Name] = language.Code
	}
	return labels, codeByName
}

// Local and Wit models only list codes, so the names are looked up and sorted.
func codeLanguageOptions(codes []string) ([]string, map[string]string) {
	names := make([]string, 0, len(codes))
	codeByName := make(map[string]string, len(codes))
	for _, code := range codes {
		name := stt.LanguageName(code)
		names = append(names, name)
		codeByName[name] = code
	}
	sort.Strings(names)
	return names, codeByName
}

func (w *MainWindow) refreshSpeechLanguages() {
	if w.speechLanguage == nil {
		return
	}
	model, known := stt.FindModel(w.speechModelDraft)

	labels, codeByName := codeLanguageOptions(model.Languages)
	if !known || model.LanguageDetect || len(model.Languages) == 0 {
		labels = append([]string{detectLanguageLabel}, labels...)
	}

	w.speechLanguage.Options = labels
	w.speechLanguageCodes = codeByName
	w.showFixedLanguages()
	w.speechLanguage.SetSelected(speechLanguageLabel(model, w.speechLanguageDraft))
}

func (w *MainWindow) refreshWitAILanguages() {
	if w.speechLanguage == nil {
		return
	}

	labels, codeByName := codeLanguageOptions(witai.Languages())
	w.speechLanguage.Options = labels
	w.speechLanguageCodes = codeByName
	w.showFixedLanguages()

	selected := stt.LanguageName(w.speechLanguageDraft)
	if _, ok := codeByName[selected]; !ok {
		selected = witaiFallbackLanguage(labels, codeByName)
	}
	w.speechLanguage.SetSelected(selected)
}

// Alphabetical order would land on Arabic, so the system language and English come first.
func witaiFallbackLanguage(labels []string, codeByName map[string]string) string {
	for _, code := range []string{stt.SystemLanguage(), "en"} {
		name := stt.LanguageName(code)
		if _, ok := codeByName[name]; ok {
			return name
		}
	}
	if len(labels) > 0 {
		return labels[0]
	}
	return ""
}

func (w *MainWindow) witaiSpeechPane() *fyne.Container {
	note := widget.NewLabel("Audio is sent to Wit.ai. Nothing is downloaded.")
	note.Wrapping = fyne.TextWrapWord
	note.TextStyle.Italic = true

	return container.NewPadded(note)
}

func speechLanguageLabel(model stt.Model, chosen string) string {
	code := model.TranscribeLanguage(chosen, stt.SystemLanguage())
	if code == "" || (model.LanguageDetect && chosen == "") {
		return detectLanguageLabel
	}
	return stt.LanguageName(code)
}

func (w *MainWindow) showSpeechOptions() {
	w.microphone.Refresh()

	content := container.NewVBox(
		widget.NewLabel("Microphone"),
		w.microphone,
		widget.NewSeparator(),
		w.speechKeepLoaded,
		w.speechCleanUp,
	)

	options := dialog.NewCustom("Speech options", "Done", content, w.Window)
	options.Resize(fyne.NewSize(430, 330))
	options.Show()
}

func (w *MainWindow) speechStore() *stt.Store {
	if w.speechStoreRef == nil {
		w.speechStoreRef = stt.NewStore()
	}
	return w.speechStoreRef
}

func (w *MainWindow) applySpeechSettings() {
	speech := w.config.SpeechSettings()

	speech.Engine = engineFromLabel(w.speechEngine.Selected)
	speech.ModelID = w.speechModelDraft

	speech.InputDevice = w.microphone.Device()
	speech.Language = w.selectedSpeechLanguage()
	speech.KeepModelLoaded = w.speechKeepLoaded.Checked
	speech.CleanUp = w.speechCleanUp.Checked

	if preset, ok := stt.PresetByName(w.speechRemote.Selected); ok {
		speech.RemoteProvider = preset.ID
	}
	speech.RemoteModel = w.speechRemoteModel.Text
	speech.RemoteBaseURL = w.speechRemoteURL.Text
	speech.RemoteAPIKey = encryptedRemoteAPIKey(w.speechRemoteKey.Text)

	w.config.SetSpeechSettings(speech)
}

// An unencrypted stored key fails to decrypt and is shown as-is rather than as garbage.
func decryptedRemoteAPIKey(stored string) string {
	if stored == "" {
		return ""
	}
	plain, err := config.DecryptAPIKey(stored)
	if err != nil {
		return stored
	}
	return plain
}

// A failure to encrypt still saves the key in plain form rather than losing it.
func encryptedRemoteAPIKey(plain string) string {
	if plain == "" {
		return ""
	}
	encrypted, err := config.EncryptAPIKey(plain)
	if err != nil {
		return plain
	}
	return encrypted
}

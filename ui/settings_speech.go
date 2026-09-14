package ui

import (
	"context"
	"fmt"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/stt"
	"github.com/paradoxe35/encre/internal/stt/witai"
)

const (
	remoteEngineLabel   = "Hosted service"
	witaiEngineLabel    = "Wit.ai (free)"
	detectLanguageLabel = "Auto-detect"
)

// speechLanguageCode maps the currently displayed label back to its ISO code, from
// whichever of refreshSpeechLanguages/refreshWitAILanguages last populated the select.
func (w *MainWindow) speechLanguageCode(label string) string {
	if label == "" || label == detectLanguageLabel {
		return ""
	}
	return w.speechLanguageCodes[label]
}

func (w *MainWindow) createSpeechSection() fyne.CanvasObject {
	local := w.localSpeechPane()
	remote := w.remoteSpeechPane()
	witaiPane := w.witaiSpeechPane()

	engineOptions := []string{"On this computer", remoteEngineLabel}
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
			w.refreshWitAILanguages()
		default:
			local.Show()
			w.refreshSpeechLanguages()
		}
	})
	w.speechEngine.SetSelected(engineLabel(w.config.SpeechSettings().Engine))

	// Options live in a dialog so the model list, the point of this screen, keeps full height.
	options := widget.NewButtonWithIcon("", theme.SettingsIcon(), w.showSpeechOptions)
	options.Importance = widget.LowImportance

	w.buildSpeechOptions()

	return container.NewBorder(
		container.NewPadded(container.NewVBox(
			w.speechHeader(),
			container.NewBorder(nil, nil, widget.NewLabel("Transcribe"), options,
				container.NewGridWithColumns(2, w.speechEngine, w.speechLanguage)),
		)),
		nil, nil, nil,
		container.NewStack(local, remote, witaiPane),
	)
}

func engineLabel(engine config.SpeechEngine) string {
	switch engine {
	case config.SpeechRemote:
		return remoteEngineLabel
	case config.SpeechWitAI:
		if witai.Available() {
			return witaiEngineLabel
		}
		return "On this computer"
	default:
		return "On this computer"
	}
}

func (w *MainWindow) speechHeader() fyne.CanvasObject {
	hint := widget.NewLabel("Hold the dictate shortcut, speak, release.")
	hint.Wrapping = fyne.TextWrapWord
	return hint
}

func (w *MainWindow) localSpeechPane() *fyne.Container {
	catalog := stt.Models()
	store := w.speechStore()
	active := widget.NewLabel(activeModelText(w.config.SpeechSettings().ModelID, catalog.Models, store.Downloaded))
	active.TextStyle.Bold = true

	w.speechModels = NewModelList(w.speechStore(), w.Window, w.config.SpeechSettings().ModelID,
		func(model stt.Model) {
			speech := w.config.SpeechSettings()
			speech.ModelID = model.ID
			speech.Language = model.LanguageAfterSwitch(speech.Language, stt.SystemLanguage())
			w.config.SetSpeechSettings(speech)
			active.SetText(activeModelText(model.ID, stt.Catalogue(), store.Downloaded))
			w.refreshSpeechLanguages()
			w.markDirty()
			w.statusBinding.Set("Speech model set to " + model.Name)
		})
	w.speechModels.SetActiveChanged(active.SetText)

	summary := widget.NewLabel(fmt.Sprintf("%d models", len(catalog.Models)))
	summary.TextStyle.Italic = true

	refresh := widget.NewButton("Check for new", w.refreshCatalog)

	return container.NewBorder(
		container.NewPadded(container.NewBorder(nil, nil, summary,
			container.NewHBox(active, refresh))),
		nil, nil, nil,
		w.speechModels,
	)
}

func (w *MainWindow) refreshCatalog() {
	w.statusBinding.Set("Checking for new models…")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := stt.Refresh(ctx)
		fyne.Do(func() {
			if err != nil {
				w.statusBinding.Set("Could not reach the model list")
				return
			}
			w.statusBinding.Set("Model list updated")
			w.speechModels.apply()
		})
	}()
}

func (w *MainWindow) remoteSpeechPane() *fyne.Container {
	speech := w.config.SpeechSettings()

	w.speechRemoteModel = w.dirtySelectEntry()
	w.speechRemoteModel.SetText(speech.RemoteModel)

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

// applyPreset locks the endpoint for known services, editable only for custom, so a typo can't break a working provider.
func (w *MainWindow) applyPreset(name string) {
	preset, ok := stt.PresetByName(name)
	if !ok {
		return
	}

	w.speechRemoteModel.SetOptions(preset.Models)

	if preset.ID == "custom" {
		w.speechRemoteURL.Enable()
		return
	}

	w.speechRemoteURL.SetText(preset.BaseURL)
	w.speechRemoteURL.Disable()
	if len(preset.Models) > 0 && w.speechRemoteModel.Text == "" {
		w.speechRemoteModel.SetText(preset.Models[0])
	}
}

// buildSpeechOptions runs once so Save reads the same widgets whether or not the dialog was ever opened.
func (w *MainWindow) buildSpeechOptions() {
	speech := w.config.SpeechSettings()

	w.microphone = NewMicrophonePicker(speech.InputDevice)
	w.microphone.onChanged = w.markDirty

	w.speechLanguage = w.dirtySelect(nil, nil)
	if speech.Engine == config.SpeechWitAI && witai.Available() {
		w.refreshWitAILanguages()
	} else {
		w.refreshSpeechLanguages()
	}

	w.speechKeepLoaded = w.dirtyCheck("Keep the model in memory", speech.KeepModelLoaded)
	w.speechCleanUp = w.dirtyCheck("Tidy the transcript with AI", speech.CleanUp)
}

// refreshSpeechLanguages lists what the selected model speaks, offering
// auto-detect only where the model can actually detect.
func (w *MainWindow) refreshSpeechLanguages() {
	if w.speechLanguage == nil {
		return
	}
	model, known := stt.FindModel(w.config.SpeechSettings().ModelID)

	names := make([]string, 0, len(model.Languages))
	codeByName := make(map[string]string, len(model.Languages))
	for _, code := range model.Languages {
		name := stt.LanguageName(code)
		names = append(names, name)
		codeByName[name] = code
	}
	sort.Strings(names)

	labels := names
	if !known || model.LanguageDetect || len(model.Languages) == 0 {
		labels = append([]string{detectLanguageLabel}, names...)
	}

	w.speechLanguage.Options = labels
	w.speechLanguageCodes = codeByName
	w.speechLanguage.SetSelected(speechLanguageLabel(model, w.config.SpeechSettings().Language))
}

// refreshWitAILanguages lists the languages with an embedded key; there is no
// auto-detect option, since a Wit app is created for exactly one language.
func (w *MainWindow) refreshWitAILanguages() {
	if w.speechLanguage == nil {
		return
	}

	codes := witai.Languages()
	labels := make([]string, 0, len(codes))
	codeByName := make(map[string]string, len(codes))
	for _, code := range codes {
		name := stt.LanguageName(code)
		labels = append(labels, name)
		codeByName[name] = code
	}
	sort.Strings(labels)

	w.speechLanguage.Options = labels
	w.speechLanguageCodes = codeByName

	selected := stt.LanguageName(w.config.SpeechSettings().Language)
	if _, ok := codeByName[selected]; !ok {
		selected = witaiFallbackLanguage(labels, codeByName)
	}
	w.speechLanguage.SetSelected(selected)
}

// Alphabetical order would land on Arabic, so try the system language and then
// English before settling for the first entry.
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

// witaiSpeechPane needs nothing beyond the shared header language select: Wit.ai
// is free and takes no key, model or URL.
func (w *MainWindow) witaiSpeechPane() *fyne.Container {
	note := widget.NewLabel("Audio is sent to Wit.ai. Nothing is downloaded.")
	note.Wrapping = fyne.TextWrapWord
	note.TextStyle.Italic = true

	return container.NewPadded(note)
}

// speechLanguageLabel shows what will be used, never a blank box.
func speechLanguageLabel(model stt.Model, chosen string) string {
	code := model.TranscribeLanguage(chosen, stt.SystemLanguage())
	if model.LanguageDetect && chosen == "" {
		return detectLanguageLabel
	}
	if code == "" {
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
	current := w.config.SpeechSettings()
	speech := &current

	switch w.speechEngine.Selected {
	case remoteEngineLabel:
		speech.Engine = config.SpeechRemote
	case witaiEngineLabel:
		speech.Engine = config.SpeechWitAI
	default:
		speech.Engine = config.SpeechLocal
	}

	speech.InputDevice = w.microphone.Device()
	speech.Language = w.speechLanguageCode(w.speechLanguage.Selected)
	speech.KeepModelLoaded = w.speechKeepLoaded.Checked
	speech.CleanUp = w.speechCleanUp.Checked

	if preset, ok := stt.PresetByName(w.speechRemote.Selected); ok {
		speech.RemoteProvider = preset.ID
	}
	speech.RemoteModel = w.speechRemoteModel.Text
	speech.RemoteBaseURL = w.speechRemoteURL.Text
	speech.RemoteAPIKey = encryptedRemoteAPIKey(w.speechRemoteKey.Text)

	w.config.SetSpeechSettings(current)
}

// decryptedRemoteAPIKey reveals the key for editing; a key saved before encryption
// existed fails to decrypt and is shown as-is rather than as garbage.
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

// encryptedRemoteAPIKey is what gets written to config.json; a failure to encrypt
// (practically never) still saves the key in plain form rather than losing it.
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

package ui

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/logger"
)

const statusErrorLimit = 60

func (w *MainWindow) saveSettings() {
	if err := w.applyProviderSettings(); err != nil {
		w.reportSaveError("Error", err)
		return
	}

	if err := w.applyActionSettings(); err != nil {
		w.reportSaveError("Error", err)
		return
	}

	w.config.SetTranslation(config.TranslateConfig{
		PrimaryLanguage:   w.primaryLanguage.Code(),
		SecondaryLanguage: w.secondaryLanguage.Code(),
	})
	w.config.SetProviderMentionsEnabled(w.mentionsCheck.Checked)
	w.applySpeechSettings()

	startMinimized, _ := w.startMinimizedBinding.Get()
	startOnLogin, _ := w.startOnLoginBinding.Get()
	dictationIndicator, _ := w.dictationIndicator.Get()
	actionIndicator, _ := w.actionIndicator.Get()
	themeSetting, _ := w.themeBinding.Get()

	w.config.SetAppearanceSettings(config.AppearanceConfig{
		Theme:          themeSetting,
		StartMinimized: startMinimized,
		StartOnLogin:   startOnLogin,
		Indicators:     &config.IndicatorsConfig{Dictation: dictationIndicator, Actions: actionIndicator},
	})

	pasteShortcut, _ := w.pasteShortcutBinding.Get()
	w.config.SetPasteShortcut(config.PasteShortcut(pasteShortcut))

	w.applyTheme(themeSetting)
	w.applyAutoStartSetting(startOnLogin)

	if err := w.config.Save(); err != nil {
		w.reportSaveError("Error saving settings", err)
		return
	}

	w.statusBinding.Set("Settings saved")
	w.markClean()
	logger.Info("Settings saved")
}

func (w *MainWindow) applyProviderSettings() error {
	provider, _ := w.providerBinding.Get()
	apiKey, _ := w.apiKeyBinding.Get()
	model, _ := w.modelBinding.Get()
	baseURL, _ := w.baseURLBinding.Get()

	existing := w.config.GetProviderSettings(provider)
	isCustom := w.config.IsCustomProvider(provider)

	if isCustom && baseURL == "" {
		return errors.New("base URL is required for custom providers")
	}

	// The form only owns the model, and the base URL for custom providers; the rest is kept as saved.
	settings := existing
	settings.Model = model
	if isCustom {
		settings.BaseURL = baseURL
	}
	w.config.SetProviderSettings(provider, settings)

	if err := w.config.SetAPIKey(provider, apiKey); err != nil {
		return err
	}

	w.config.SetCurrentProvider(provider)
	w.refreshOperationProviderOptions()
	return nil
}

func (w *MainWindow) applyActionSettings() error {
	for _, kind := range config.ActionOrder {
		action := w.config.Action(kind)

		if binding, ok := w.hotkeyBindings[kind]; ok {
			hotkey, _ := binding.Get()
			action.Hotkey = hotkey
		}
		if enable, ok := w.enables[kind]; ok {
			action.Enabled = enable.Checked
		}

		w.config.SetAction(kind, action)
	}

	for op, editor := range w.operationEditors {
		limit, err := strconv.Atoi(editor.limit.Text)
		if err != nil || validateCharacterLimit(editor.limit.Text) != nil {
			return fmt.Errorf("%s: character limit must be between 1 and 100000", op.Label())
		}

		w.config.SetOperation(op, config.OperationConfig{
			SystemPrompt:   editor.prompt.Text,
			CharacterLimit: limit,
			TimeoutSeconds: int(editor.timeout.Value),
			ProviderID:     providerID(editor.provider.Selected),
		})
	}
	return nil
}

func (w *MainWindow) reportSaveError(prefix string, err error) {
	message := err.Error()
	if len(message) > statusErrorLimit {
		message = message[:statusErrorLimit-3] + "..."
	}
	w.statusBinding.Set(prefix + ": " + message)
	logger.Error(prefix, "error", err)
}

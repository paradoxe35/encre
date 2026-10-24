package ui

import (
	"errors"
	"fmt"
	"strconv"

	"fyne.io/fyne/v2"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/platform"
)

const statusErrorLimit = 60

// encryptAPIKey is swapped in tests to make encryption fail.
var encryptAPIKey = config.EncryptAPIKey

type providerDraft struct {
	name     string
	settings config.ProviderSettings
}

// Everything that can fail is checked before anything is applied: a half-applied config would be written by the next unrelated Save.
func (w *MainWindow) saveSettings() {
	provider, err := w.providerDraft()
	if err != nil {
		w.reportSaveError("Error", err)
		return
	}
	operations, err := w.operationDrafts()
	if err != nil {
		w.reportSaveError("Error", err)
		return
	}
	speechKey, err := encryptedRemoteAPIKey(w.speechRemoteKey.Text)
	if err != nil {
		w.reportSaveError("Error", fmt.Errorf("speech API key: %w", err))
		return
	}

	w.applyProvider(provider)
	w.applyActions(operations)

	w.config.SetTranslation(config.TranslateConfig{
		PrimaryLanguage:   w.primaryLanguage.Code(),
		SecondaryLanguage: w.secondaryLanguage.Code(),
	})
	w.config.SetProviderMentionsEnabled(w.mentionsCheck.Checked)
	w.config.SetAnswerCardSettings(w.answerCardSettings())
	w.applySpeechSettings(speechKey)

	startMinimized, _ := w.startMinimizedBinding.Get()
	startOnLogin, _ := w.startOnLoginBinding.Get()
	voiceIndicator, _ := w.voiceIndicator.Get()
	textIndicator, _ := w.textIndicator.Get()
	themeSetting, _ := w.themeBinding.Get()

	w.applyTheme(themeSetting)
	w.applyAutoStartSetting(startOnLogin)
	// The saved setting follows what the system actually did, not what was asked.
	actualStartOnLogin := platform.GetAutoStart().IsEnabled()

	w.config.SetAppearanceSettings(config.AppearanceConfig{
		Theme:          themeSetting,
		StartMinimized: startMinimized,
		StartOnLogin:   actualStartOnLogin,
		Indicators:     &config.IndicatorsConfig{Voice: voiceIndicator, Text: textIndicator},
	})

	pasteShortcut, _ := w.pasteShortcutBinding.Get()
	w.config.SetPasteShortcut(config.PasteShortcut(pasteShortcut))

	if err := w.config.Save(); err != nil {
		w.reportSaveError("Error saving settings", err)
		return
	}

	if actualStartOnLogin != startOnLogin {
		w.startOnLoginBinding.Set(actualStartOnLogin)
		w.statusBinding.Set(startOnLoginFailure(startOnLogin))
		// The binding tells markDirty through Fyne's queue, after this returns.
		fyne.Do(w.markClean)
	} else {
		w.statusBinding.Set("Settings saved")
	}
	w.markClean()
	logger.Info("Settings saved")
}

func startOnLoginFailure(wanted bool) string {
	if wanted {
		return "Settings saved, but Start on login could not be turned on"
	}
	return "Settings saved, but Start on login could not be turned off"
}

func (w *MainWindow) applyProviderSettings() error {
	draft, err := w.providerDraft()
	if err != nil {
		return err
	}
	w.applyProvider(draft)
	return nil
}

func (w *MainWindow) providerDraft() (providerDraft, error) {
	provider, _ := w.providerBinding.Get()
	apiKey, _ := w.apiKeyBinding.Get()
	model, _ := w.modelBinding.Get()
	baseURL, _ := w.baseURLBinding.Get()
	lowReasoning, _ := w.lowReasoningBinding.Get()

	isCustom := w.config.IsCustomProvider(provider)
	if isCustom && baseURL == "" {
		return providerDraft{}, errors.New("base URL is required for custom providers")
	}

	encryptedKey, err := encryptAPIKey(apiKey)
	if err != nil {
		return providerDraft{}, err
	}

	// The form owns the model, reasoning, API key and, for custom providers, the base URL; the rest is kept as saved.
	settings := w.config.GetProviderSettings(provider)
	settings.Model = model
	settings.LowReasoning = lowReasoning
	settings.APIKey = encryptedKey
	if isCustom {
		settings.BaseURL = baseURL
	}
	return providerDraft{name: provider, settings: settings}, nil
}

func (w *MainWindow) applyProvider(draft providerDraft) {
	w.config.SetProviderSettings(draft.name, draft.settings)
	w.config.SetCurrentProvider(draft.name)
	w.refreshOperationProviderOptions()
}

func (w *MainWindow) applyActionSettings() error {
	operations, err := w.operationDrafts()
	if err != nil {
		return err
	}
	w.applyActions(operations)
	return nil
}

func (w *MainWindow) operationDrafts() (map[config.Operation]config.OperationConfig, error) {
	operations := make(map[config.Operation]config.OperationConfig, len(w.operationEditors))
	for op, editor := range w.operationEditors {
		limit, err := strconv.Atoi(editor.limit.Text)
		if err != nil || validateCharacterLimit(editor.limit.Text) != nil {
			return nil, fmt.Errorf("%s: character limit must be between 1 and 100000", op.Label())
		}

		operation := config.OperationConfig{
			SystemPrompt:   editor.prompt.Text,
			CharacterLimit: limit,
			TimeoutSeconds: int(editor.timeout.Value),
			ProviderID:     providerID(editor.provider.Selected),
		}
		if editor.ask != nil {
			if err := editor.ask.apply(&operation); err != nil {
				return nil, fmt.Errorf("%s: %w", op.Label(), err)
			}
		}
		operations[op] = operation
	}
	return operations, nil
}

func (w *MainWindow) applyActions(operations map[config.Operation]config.OperationConfig) {
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

	for op, operation := range operations {
		// applyProvider may have reset a choice whose provider is no longer configured.
		if editor, ok := w.operationEditors[op]; ok {
			operation.ProviderID = providerID(editor.provider.Selected)
		}
		w.config.SetOperation(op, operation)
	}
}

func (w *MainWindow) reportSaveError(prefix string, err error) {
	w.statusBinding.Set(prefix + ": " + truncateRunes(err.Error(), statusErrorLimit))
	logger.Error(prefix, "error", err)
}

// truncateRunes keeps at most limit characters, ending in "..." when it cuts.
func truncateRunes(message string, limit int) string {
	runes := []rune(message)
	if len(runes) <= limit {
		return message
	}
	return string(runes[:limit-3]) + "..."
}

package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/logger"
)

func (w *MainWindow) loadProviderSettings(provider string) {
	settings := w.config.GetProviderSettings(provider)

	apiKey, _ := w.config.GetAPIKey(provider)
	w.apiKeyBinding.Set(apiKey)
	w.modelBinding.Set(settings.Model)

	if w.config.IsCustomProvider(provider) {
		w.baseURLBinding.Set(settings.BaseURL)
	} else {
		w.baseURLBinding.Set("")
	}

	w.updateProviderUI(provider)
}

func (w *MainWindow) createProviderSection() fyne.CanvasObject {
	selected, _ := w.providerBinding.Get()

	providerSection := w.createProviderSelectionSection(selected)
	configSection := w.createProviderConfigSection()
	testSection := w.createConnectionTestSection()

	w.updateProviderUI(selected)

	return container.NewVBox(
		container.NewPadded(providerSection),
		widget.NewSeparator(),
		container.NewPadded(configSection),
		widget.NewSeparator(),
		container.NewPadded(testSection),
	)
}

func (w *MainWindow) createProviderSelectionSection(selected string) fyne.CanvasObject {
	providerLabel := widget.NewLabel("AI provider")
	providerLabel.TextStyle.Bold = true

	w.providerSelect = widget.NewSelect(w.config.GetAllProviderNames(), func(value string) {
		w.providerBinding.Set(value)
		w.loadProviderSettings(value)
		w.markDirty()
	})
	w.providerSelect.SetSelected(selected)

	addProvider := widget.NewButtonWithIcon("", theme.ContentAddIcon(), w.showAddCustomProviderDialog)
	addProvider.Importance = widget.LowImportance

	w.deleteProviderButton = widget.NewButtonWithIcon("", theme.DeleteIcon(), w.showDeleteProviderConfirmation)
	w.deleteProviderButton.Importance = widget.DangerImportance

	providerRow := container.NewBorder(nil, nil, nil,
		container.NewHBox(addProvider, w.deleteProviderButton), w.providerSelect)

	return container.NewVBox(providerLabel, providerRow)
}

func (w *MainWindow) createProviderConfigSection() fyne.CanvasObject {
	apiKeyLabel := widget.NewLabel("API key")
	apiKeyLabel.TextStyle.Bold = true
	apiKeyEntry := w.dirtyPasswordEntry()
	apiKeyEntry.Bind(w.apiKeyBinding)
	apiKeyEntry.PlaceHolder = "Enter your API key"
	apiKeyEntry.Validator = nil // Bind installs a validator that shows an icon

	modelLabel := widget.NewLabel("Model")
	modelLabel.TextStyle.Bold = true
	modelEntry := w.dirtyEntry()
	modelEntry.Bind(w.modelBinding)
	modelEntry.PlaceHolder = "e.g., gpt-4o"
	modelEntry.Validator = nil // no validation icon

	browseModels := widget.NewButtonWithIcon("", theme.ListIcon(), nil)
	browseModels.Importance = widget.LowImportance
	browseModels.OnTapped = func() { w.browseProviderModels(w.statusProgress(browseModels)) }
	modelRow := container.NewBorder(nil, nil, nil, browseModels, modelEntry)

	// Only shown for custom providers, so the placeholder can say so outright.
	baseURLLabel := widget.NewLabel("Base URL")
	baseURLLabel.TextStyle.Bold = true
	baseURLEntry := w.dirtyEntry()
	baseURLEntry.Bind(w.baseURLBinding)
	baseURLEntry.PlaceHolder = "Required for custom providers"
	baseURLEntry.Validator = nil // no validation icon

	w.baseURLContainer = container.NewVBox(
		widget.NewSeparator(),
		baseURLLabel,
		baseURLEntry,
	)

	return container.NewVBox(
		apiKeyLabel,
		apiKeyEntry,
		widget.NewSeparator(),
		modelLabel,
		modelRow,
		w.baseURLContainer,
	)
}

// Uses the key and URL on screen rather than the saved ones, so an unsaved edit can be tried out.
func (w *MainWindow) browseProviderModels(report progress) {
	provider, _ := w.providerBinding.Get()
	apiKey, _ := w.apiKeyBinding.Get()
	baseURL, _ := w.baseURLBinding.Get()
	current, _ := w.modelBinding.Get()

	settings := w.config.GetProviderSettings(provider)
	if baseURL == "" {
		baseURL = settings.BaseURL
	}
	if apiKey == "" && settings.RequiresAPIKey() {
		report.Fail("Enter the API key first: the provider needs it to list models")
		return
	}

	w.loadModels(provider, apiKey, baseURL, current, report, func(model string) {
		w.modelBinding.Set(model)
		w.markDirty()
		report.Done("Model set to " + model)
	})
}

func (w *MainWindow) createConnectionTestSection() fyne.CanvasObject {
	testBtn := widget.NewButtonWithIcon("Test Connection", theme.ConfirmIcon(), nil)
	testBtn.OnTapped = func() { w.testAPIConnection(w.statusProgress(testBtn)) }
	testBtn.Importance = widget.MediumImportance

	return container.NewHBox(testBtn)
}

func (w *MainWindow) testAPIConnection(report progress) {
	provider, _ := w.providerBinding.Get()
	settings := w.config.GetProviderSettings(provider)
	apiKey, _ := w.apiKeyBinding.Get()
	model, _ := w.modelBinding.Get()
	baseURL, _ := w.baseURLBinding.Get()
	isCustom := w.config.IsCustomProvider(provider)

	if apiKey == "" && settings.RequiresAPIKey() {
		report.Fail("Enter the API key first")
		return
	}
	if isCustom && baseURL == "" {
		report.Fail("Enter the base URL first")
		return
	}

	testProvider, err := w.providerUnderTest(provider, settings, apiKey, baseURL, model)
	if err != nil {
		report.Fail(err.Error())
		return
	}

	report.Busy("Testing the connection…")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, err := testProvider.ReviseText(ctx, "Hello", "Reply with 'Connection successful' if you receive this message.")

		fyne.Do(func() {
			if err != nil {
				logger.Error("API connection test failed", "error", err)
				report.Fail("Connection failed: " + shortMessage(err.Error()))
				return
			}
			logger.Info("API connection test successful",
				"provider", provider, "model", model, "temperature", settings.Temperature, "custom", isCustom)
			report.Done("Connection successful")
		})
	}()
}

// Builds the provider from what is on screen, so settings can be tried before they are saved.
func (w *MainWindow) providerUnderTest(provider string, settings config.ProviderSettings, apiKey, baseURL, model string) (ai.Provider, error) {
	if w.config.IsCustomProvider(provider) {
		return ai.NewCustomProvider(provider, settings.ProviderType, apiKey, baseURL, model, settings.Temperature)
	}

	switch provider {
	case config.BuiltInOpenAI:
		return ai.NewOpenAIProvider(apiKey, baseURL, model, settings.Temperature), nil
	case config.BuiltInClaude:
		return ai.NewAnthropicProvider(apiKey, baseURL, model, settings.Temperature), nil
	case config.BuiltInGemini:
		return ai.NewGeminiProvider(apiKey, baseURL, model, settings.Temperature), nil
	case config.BuiltInOpenRouter:
		return ai.NewOpenRouterProvider(apiKey, baseURL, model, settings.Temperature), nil
	default:
		return nil, fmt.Errorf("unknown provider: %s", provider)
	}
}

// The widgets are still nil when initBindings loads the first provider, before the tab exists.
func (w *MainWindow) updateProviderUI(provider string) {
	isCustom := w.config.IsCustomProvider(provider)

	if w.deleteProviderButton != nil {
		if isCustom {
			w.deleteProviderButton.Show()
		} else {
			w.deleteProviderButton.Hide()
		}
	}

	if w.baseURLContainer != nil {
		if isCustom {
			w.baseURLContainer.Show()
		} else {
			w.baseURLContainer.Hide()
		}
	}
}

func (w *MainWindow) refreshProviderList() {
	w.providerSelect.Options = w.config.GetAllProviderNames()
	w.providerSelect.Refresh()
}

func (w *MainWindow) showAddCustomProviderDialog() {
	nameEntry := widget.NewEntry()
	nameEntry.PlaceHolder = "my-provider"

	baseURLEntry := widget.NewEntry()
	baseURLEntry.PlaceHolder = "https://api.example.com/v1"

	apiKeyEntry := widget.NewPasswordEntry()
	apiKeyEntry.PlaceHolder = "Enter API key"

	requiresKey := widget.NewCheck("Requires an API key", func(checked bool) {
		if checked {
			apiKeyEntry.Enable()
		} else {
			apiKeyEntry.SetText("")
			apiKeyEntry.Disable()
		}
	})
	requiresKey.SetChecked(true)

	lowReasoning := widget.NewCheck("Ask reasoning models to think less", nil)

	modelEntry := widget.NewEntry()
	modelEntry.PlaceHolder = "Optional, e.g. gpt-4o or llama3"

	fetchModels := widget.NewButtonWithIcon("Fetch models", theme.ListIcon(), nil)
	fetchModels.Importance = widget.LowImportance

	fetchReport := newFeedback(fetchModels)
	addReport := newFeedback()

	fetchModels.OnTapped = func() {
		w.fetchModelsForCustomProvider(apiKeyEntry.Text, baseURLEntry.Text, requiresKey.Checked, modelEntry, fetchReport)
	}

	nameEntry.OnChanged = func(string) { addReport.Clear() }
	baseURLEntry.OnChanged = func(string) { addReport.Clear() }

	form := widget.NewForm(
		widget.NewFormItem("Name", nameEntry),
		widget.NewFormItem("Base URL", baseURLEntry),
		widget.NewFormItem("API key", apiKeyEntry),
		widget.NewFormItem("", container.NewVBox(requiresKey, lowReasoning)),
		widget.NewFormItem("Model", container.NewBorder(nil, nil, nil, fetchModels, modelEntry)),
		widget.NewFormItem("", fetchReport),
	)

	var d dialog.Dialog

	addBtn := widget.NewButtonWithIcon("Add", theme.ConfirmIcon(), func() {
		addReport.Clear()

		name := strings.TrimSpace(nameEntry.Text)
		if name == "" {
			addReport.Fail("Give the provider a name")
			return
		}

		baseURL := strings.TrimSpace(baseURLEntry.Text)
		if baseURL == "" {
			addReport.Fail("Enter the base URL")
			return
		}

		settings := config.ProviderSettings{
			BaseURL:      baseURL,
			Model:        strings.TrimSpace(modelEntry.Text),
			Temperature:  1.0,
			ProviderType: config.ProviderTypeOpenAICompatible,
			NoAPIKey:     !requiresKey.Checked,
			LowReasoning: lowReasoning.Checked,
		}

		if err := w.config.AddCustomProvider(name, settings); err != nil {
			addReport.Fail(err.Error())
			return
		}

		if apiKey := strings.TrimSpace(apiKeyEntry.Text); apiKey != "" {
			if err := w.config.SetAPIKey(name, apiKey); err != nil {
				addReport.Fail("Could not save the API key: " + err.Error())
				return
			}
		}

		if err := w.config.Save(); err != nil {
			addReport.Fail("Could not save: " + err.Error())
			return
		}

		d.Hide()
		w.refreshProviderList()
		w.refreshOperationProviderOptions()
		w.providerSelect.SetSelected(name)
		w.providerBinding.Set(name)
		w.loadProviderSettings(name)
		w.statusBinding.Set(fmt.Sprintf("Custom provider '%s' added", name))
	})
	addBtn.Importance = widget.HighImportance

	cancelBtn := widget.NewButton("Cancel", func() { d.Hide() })

	buttons := container.NewVBox(
		addReport,
		container.NewHBox(layout.NewSpacer(), cancelBtn, addBtn),
	)
	content := container.NewBorder(nil, buttons, nil, nil, form)

	d = dialog.NewCustomWithoutButtons("Add Custom Provider", content, w.Window)
	d.Resize(fyne.NewSize(480, 0))
	d.Show()
	w.Canvas().Focus(nameEntry)
}

func (w *MainWindow) showDeleteProviderConfirmation() {
	currentProvider, _ := w.providerBinding.Get()

	if !w.config.IsCustomProvider(currentProvider) {
		return
	}

	dialog.ShowConfirm(
		"Delete Provider",
		fmt.Sprintf("Are you sure you want to delete '%s'?\n\nThis action cannot be undone.", currentProvider),
		func(confirmed bool) {
			if !confirmed {
				return
			}

			if err := w.config.DeleteCustomProvider(currentProvider); err != nil {
				w.statusBinding.Set("Could not delete the provider: " + shortMessage(err.Error()))
				return
			}

			if err := w.config.Save(); err != nil {
				w.statusBinding.Set("Could not save: " + shortMessage(err.Error()))
				return
			}

			w.refreshProviderList()
			newProvider := w.config.GetCurrentProvider()
			w.providerSelect.SetSelected(newProvider)
			w.providerBinding.Set(newProvider)
			w.loadProviderSettings(newProvider)
			w.statusBinding.Set(fmt.Sprintf("Provider '%s' deleted", currentProvider))
		},
		w.Window,
	)
}

func (w *MainWindow) fetchModelsForCustomProvider(apiKey, baseURL string, requiresAPIKey bool, modelEntry *widget.Entry, report progress) {
	if strings.TrimSpace(baseURL) == "" {
		report.Fail("Enter the base URL first")
		return
	}
	if apiKey == "" && requiresAPIKey {
		report.Fail("Enter the API key first, or untick \"Requires an API key\"")
		return
	}

	// No name yet, so the provider lists as OpenAI-compatible, the only type a custom provider can be.
	w.loadModels("", apiKey, strings.TrimSpace(baseURL), modelEntry.Text, report, func(model string) {
		modelEntry.SetText(model)
		report.Done("Model set to " + model)
	})
}

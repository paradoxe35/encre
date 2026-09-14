package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
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

	isCustom := w.config.IsCustomProvider(provider)
	if isCustom {
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

	content := container.NewVBox(
		container.NewPadded(providerSection),
		widget.NewSeparator(),
		container.NewPadded(configSection),
		widget.NewSeparator(),
		container.NewPadded(testSection),
	)

	return content
}

func (w *MainWindow) createProviderSelectionSection(selected string) fyne.CanvasObject {
	providerLabel := widget.NewLabel("AI Provider:")
	providerLabel.TextStyle.Bold = true

	providerNames := w.config.GetAllProviderNames()
	providerOptions := append(providerNames, "-- Add Custom Provider --")

	w.providerSelect = widget.NewSelect(
		providerOptions,
		func(value string) {
			if value == "-- Add Custom Provider --" {
				w.showAddCustomProviderDialog()
				w.providerSelect.SetSelected(w.config.GetCurrentProvider())
				return
			}

			w.providerBinding.Set(value)
			w.loadProviderSettings(value)
			w.markDirty()
		},
	)
	w.providerSelect.SetSelected(selected)

	w.deleteProviderButton = widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		w.showDeleteProviderConfirmation()
	})
	w.deleteProviderButton.Importance = widget.DangerImportance

	providerRow := container.NewBorder(nil, nil, nil, w.deleteProviderButton, w.providerSelect)

	content := container.NewVBox(
		providerLabel,
		providerRow,
	)

	return content
}

func (w *MainWindow) createProviderConfigSection() fyne.CanvasObject {
	apiKeyLabel := widget.NewLabel("API Key:")
	apiKeyLabel.TextStyle.Bold = true
	apiKeyEntry := w.dirtyPasswordEntry()
	apiKeyEntry.Bind(w.apiKeyBinding)
	apiKeyEntry.PlaceHolder = "Enter your API key"
	apiKeyEntry.Validator = nil // no validation icon

	modelLabel := widget.NewLabel("Model:")
	modelLabel.TextStyle.Bold = true
	modelEntry := w.dirtyEntry()
	modelEntry.Bind(w.modelBinding)
	modelEntry.PlaceHolder = "e.g., gpt-4o"
	modelEntry.Validator = nil // no validation icon

	browseModels := widget.NewButtonWithIcon("", theme.ListIcon(), w.browseProviderModels)
	browseModels.Importance = widget.LowImportance
	modelRow := container.NewBorder(nil, nil, nil, browseModels, modelEntry)

	// Only shown for custom/OpenAI-compatible providers.
	baseURLLabel := widget.NewLabel("Base URL:")
	baseURLLabel.TextStyle.Bold = true
	w.baseURLEntry = w.dirtyEntry()
	w.baseURLEntry.Bind(w.baseURLBinding)
	w.baseURLEntry.PlaceHolder = "https://api.openai.com/v1 (optional)"
	w.baseURLEntry.Validator = nil // no validation icon

	w.baseURLContainer = container.NewVBox(
		widget.NewSeparator(),
		baseURLLabel,
		w.baseURLEntry,
	)

	configForm := container.NewVBox(
		apiKeyLabel,
		apiKeyEntry,
		widget.NewSeparator(),
		modelLabel,
		modelRow,
		w.baseURLContainer,
	)

	return configForm
}

// browseProviderModels lists what the provider currently selected on the AI tab
// offers, using the key and URL on screen rather than the saved ones so an
// unsaved edit can be tried out.
func (w *MainWindow) browseProviderModels() {
	provider, _ := w.providerBinding.Get()
	apiKey, _ := w.apiKeyBinding.Get()
	baseURL, _ := w.baseURLBinding.Get()
	current, _ := w.modelBinding.Get()

	settings := w.config.GetProviderSettings(provider)
	if baseURL == "" {
		baseURL = settings.BaseURL
	}
	if apiKey == "" && settings.RequiresAPIKey() {
		w.statusBinding.Set("Error: API key is required to list models")
		return
	}

	status := func(message string) { w.statusBinding.Set(message) }

	w.loadModels(provider, apiKey, baseURL, current, status, func(model string) {
		w.modelBinding.Set(model)
		w.markDirty()
		w.statusBinding.Set("Model set to " + model)
	})
}

func (w *MainWindow) createConnectionTestSection() fyne.CanvasObject {
	testBtn := widget.NewButtonWithIcon("Test Connection", theme.ConfirmIcon(), func() {
		w.testAPIConnection()
	})
	testBtn.Importance = widget.MediumImportance

	return container.NewVBox(
		widget.NewLabel("Test your settings:"),
		testBtn,
	)
}
func (w *MainWindow) testAPIConnection() {
	w.statusBinding.Set("Testing API connection...")

	go func() {
		provider, _ := w.providerBinding.Get()
		settings := w.config.GetProviderSettings(provider)
		apiKey, _ := w.apiKeyBinding.Get()
		model, _ := w.modelBinding.Get()
		baseURL, _ := w.baseURLBinding.Get()

		if apiKey == "" && settings.RequiresAPIKey() {
			fyne.Do(func() {
				w.statusBinding.Set("Error: API key is required")
			})
			return
		}

		var testProvider ai.Provider
		isCustom := w.config.IsCustomProvider(provider)

		if isCustom {
			if baseURL == "" {
				fyne.Do(func() {
					w.statusBinding.Set("Error: Base URL is required for custom providers")
				})
				return
			}
			customProvider, err := ai.NewCustomProvider(provider, settings.ProviderType, apiKey, baseURL, model, settings.Temperature)
			if err != nil {
				fyne.Do(func() {
					w.statusBinding.Set(fmt.Sprintf("Error: %s", err.Error()))
				})
				return
			}
			testProvider = customProvider
		} else {
			switch provider {
			case config.BuiltInOpenAI:
				testProvider = ai.NewOpenAIProvider(apiKey, baseURL, model, settings.Temperature)
			case config.BuiltInClaude:
				testProvider = ai.NewAnthropicProvider(apiKey, baseURL, model, settings.Temperature)
			case config.BuiltInGemini:
				testProvider = ai.NewGeminiProvider(apiKey, baseURL, model, settings.Temperature)
			default:
				fyne.Do(func() {
					w.statusBinding.Set("Error: Unknown provider")
				})
				return
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		testText := "Hello"
		testPrompt := "Reply with 'Connection successful' if you receive this message."

		_, err := testProvider.ReviseText(ctx, testText, testPrompt)
		if err != nil {
			errMsg := err.Error()
			if len(errMsg) > 80 {
				errMsg = errMsg[:77] + "..."
			}
			fyne.Do(func() {
				w.statusBinding.Set(fmt.Sprintf("Connection failed: %s", errMsg))
			})
			logger.Error("API connection test failed", "error", err)
		} else {
			fyne.Do(func() {
				w.statusBinding.Set("Connection successful!")
			})
			logger.Info("API connection test successful",
				"provider", provider,
				"model", model,
				"temperature", settings.Temperature,
				"custom", isCustom,
			)
		}
	}()
}
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
			if w.baseURLEntry != nil {
				w.baseURLEntry.PlaceHolder = "Required for custom providers"
			}
		} else {
			w.baseURLContainer.Hide()
		}
	}

}

func (w *MainWindow) refreshProviderList() {
	if w.providerSelect == nil {
		return
	}
	providerNames := w.config.GetAllProviderNames()
	providerOptions := append(providerNames, "-- Add Custom Provider --")
	w.providerSelect.Options = providerOptions
	w.providerSelect.Refresh()
}

func (w *MainWindow) showAddCustomProviderDialog() {
	nameEntry := widget.NewEntry()
	nameEntry.PlaceHolder = "my-custom-provider"

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
	modelEntry.PlaceHolder = "e.g., gpt-4 or llama3"

	errorLabel := widget.NewLabel("")
	errorLabel.Wrapping = fyne.TextWrapWord
	errorLabel.Importance = widget.DangerImportance

	fetchModelsBtn := widget.NewButton("Fetch Models", func() {
		w.fetchModelsForCustomProvider(apiKeyEntry.Text, baseURLEntry.Text, requiresKey.Checked, modelEntry, errorLabel)
	})

	form := container.NewVBox(
		widget.NewLabel("Provider Name:"),
		nameEntry,
		widget.NewSeparator(),
		widget.NewLabel("Base URL (required):"),
		baseURLEntry,
		widget.NewSeparator(),
		widget.NewLabel("API Key:"),
		apiKeyEntry,
		requiresKey,
		lowReasoning,
		widget.NewSeparator(),
		widget.NewLabel("Model:"),
		container.NewBorder(nil, nil, nil, fetchModelsBtn, modelEntry),
		errorLabel,
	)

	scrollContainer := container.NewVScroll(form)
	scrollContainer.SetMinSize(fyne.NewSize(400, 350))

	var d dialog.Dialog

	addBtn := widget.NewButton("Add", func() {
		errorLabel.SetText("")

		name := strings.TrimSpace(nameEntry.Text)
		if name == "" {
			errorLabel.SetText("Provider name is required")
			return
		}

		baseURL := strings.TrimSpace(baseURLEntry.Text)
		if baseURL == "" {
			errorLabel.SetText("Base URL is required")
			return
		}

		settings := config.ProviderSettings{
			BaseURL:      baseURL,
			Model:        strings.TrimSpace(modelEntry.Text),
			Temperature:  1.0,
			IsCustom:     true,
			ProviderType: config.ProviderTypeOpenAICompatible,
			NoAPIKey:     !requiresKey.Checked,
			LowReasoning: lowReasoning.Checked,
		}

		if err := w.config.AddCustomProvider(name, settings); err != nil {
			errorLabel.SetText(err.Error())
			return
		}

		if apiKey := strings.TrimSpace(apiKeyEntry.Text); apiKey != "" {
			if err := w.config.SetAPIKey(name, apiKey); err != nil {
				errorLabel.SetText(fmt.Sprintf("Error saving API key: %s", err.Error()))
				return
			}
		}

		if err := w.config.Save(); err != nil {
			errorLabel.SetText(fmt.Sprintf("Error saving: %s", err.Error()))
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

	cancelBtn := widget.NewButton("Cancel", func() {
		d.Hide()
	})

	buttons := container.NewHBox(cancelBtn, addBtn)
	content := container.NewBorder(nil, buttons, nil, nil, scrollContainer)

	d = dialog.NewCustomWithoutButtons("Add Custom Provider", content, w.Window)
	d.Resize(fyne.NewSize(450, 480))
	d.Show()
}

func (w *MainWindow) showDeleteProviderConfirmation() {
	currentProvider, _ := w.providerBinding.Get()

	if !w.config.IsCustomProvider(currentProvider) {
		dialog.ShowError(fmt.Errorf("cannot delete built-in provider"), w.Window)
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
				w.statusBinding.Set(fmt.Sprintf("Error: %s", err.Error()))
				return
			}

			if err := w.config.Save(); err != nil {
				w.statusBinding.Set(fmt.Sprintf("Error saving: %s", err.Error()))
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

func (w *MainWindow) fetchModelsForCustomProvider(apiKey, baseURL string, requiresAPIKey bool, modelEntry *widget.Entry, statusLabel *widget.Label) {
	if baseURL == "" {
		statusLabel.SetText("Base URL is required")
		return
	}
	if apiKey == "" && requiresAPIKey {
		statusLabel.SetText("API key and Base URL are required")
		return
	}

	// A provider being added has no name yet, so it lists as OpenAI-compatible,
	// which is the only type custom providers can be.
	w.loadModels("", apiKey, baseURL, modelEntry.Text, statusLabel.SetText, modelEntry.SetText)
}

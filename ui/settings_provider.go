package ui

import (
	"context"
	"errors"
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

// formChangedSince reports, when a test or a model list lands, that it is about another provider,
// key, model or address than the one now on screen.
func (w *MainWindow) formChangedSince(provider string) func() bool {
	then := w.shownProviderForm()
	return func() bool {
		now := w.shownProviderForm()
		shown, _ := w.providerBinding.Get()
		return shown != provider || now.apiKey != then.apiKey || now.model != then.model || now.baseURL != then.baseURL
	}
}

// providerForm is what the AI tab shows for one provider, kept while another is looked at until Save.
type providerForm struct {
	apiKey, model, baseURL   string
	lowReasoning, unreadable bool
}

func (w *MainWindow) shownProviderForm() providerForm {
	apiKey, _ := w.apiKeyBinding.Get()
	model, _ := w.modelBinding.Get()
	baseURL, _ := w.baseURLBinding.Get()
	lowReasoning, _ := w.lowReasoningBinding.Get()
	return providerForm{apiKey: apiKey, model: model, baseURL: baseURL, lowReasoning: lowReasoning, unreadable: w.apiKeyUnreadable}
}

// Picking the provider already shown, or loading it twice as Add and Delete do, keeps what is typed.
func (w *MainWindow) loadProviderSettings(provider string) {
	if provider == w.providerShown {
		w.updateProviderUI(provider)
		return
	}
	if w.providerShown != "" {
		if w.providerForms == nil {
			w.providerForms = make(map[string]providerForm)
		}
		w.providerForms[w.providerShown] = w.shownProviderForm()
	}
	w.providerShown = provider

	form, edited := w.providerForms[provider]
	delete(w.providerForms, provider)
	if !edited {
		settings := w.config.GetProviderSettings(provider)
		apiKey, err := w.config.GetAPIKey(provider)
		form = providerForm{apiKey: apiKey, model: settings.Model, lowReasoning: settings.LowReasoning, unreadable: err != nil}
		if w.config.IsCustomProvider(provider) {
			form.baseURL = settings.BaseURL
		}
	}

	w.apiKeyBinding.Set(form.apiKey)
	w.setAPIKeyUnreadable(form.unreadable)
	w.modelBinding.Set(form.model)
	w.lowReasoningBinding.Set(form.lowReasoning)
	w.baseURLBinding.Set(form.baseURL)

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
	apiKeyEntry.PlaceHolder = apiKeyPlaceholder(w.apiKeyUnreadable, "Enter your API key")
	apiKeyEntry.Validator = nil // Bind installs a validator that shows an icon
	w.apiKeyEntry = apiKeyEntry

	modelLabel := widget.NewLabel("Model")
	modelLabel.TextStyle.Bold = true
	modelEntry := w.dirtyEntry()
	modelEntry.Bind(w.modelBinding)
	modelEntry.PlaceHolder = "e.g. gpt-6-luna"
	modelEntry.Validator = nil

	browseModels := widget.NewButtonWithIcon("", theme.ListIcon(), nil)
	browseModels.Importance = widget.LowImportance
	browseModels.OnTapped = func() { w.browseProviderModels(w.statusProgress(browseModels)) }
	modelRow := container.NewBorder(nil, nil, nil, browseModels, modelEntry)

	lowReasoning := widget.NewCheck("Ask reasoning models to think less", nil)
	lowReasoning.Bind(w.lowReasoningBinding)

	// Only shown for custom providers, so the placeholder can say so outright.
	baseURLLabel := widget.NewLabel("Base URL")
	baseURLLabel.TextStyle.Bold = true
	baseURLEntry := w.dirtyEntry()
	baseURLEntry.Bind(w.baseURLBinding)
	baseURLEntry.PlaceHolder = "Required for custom providers"
	baseURLEntry.Validator = nil

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
		lowReasoning,
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

	stale := w.formChangedSince(provider)
	w.loadModels(provider, apiKey, baseURL, current, report, stale, func(model string) {
		if stale() {
			return
		}
		w.modelBinding.Set(model)
		w.markDirty()
		report.Done("Model set to " + model)
	})
}

func (w *MainWindow) createConnectionTestSection() fyne.CanvasObject {
	testBtn := widget.NewButtonWithIcon("Test connection", theme.ConfirmIcon(), nil)
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
	stale := w.formChangedSince(provider)

	go func() {
		err := answers(testProvider, 10*time.Second)

		fyne.Do(func() {
			if stale() {
				report.Done("")
				return
			}
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

var errAnswering = errors.New("answering")

// answers waits only for the reply to begin: a reasoning model can think for a minute first.
func answers(provider ai.Provider, wait time.Duration) error {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	silence := time.AfterFunc(wait, func() { cancel(context.DeadlineExceeded) })
	defer silence.Stop()

	ctx = ai.WithActivity(ctx, func() { cancel(errAnswering) })
	_, err := provider.Stream(ctx, ai.Prompt{System: "Reply with OK.", Text: "Hello"}, nil)
	switch cause := context.Cause(ctx); {
	case errors.Is(cause, errAnswering):
		return nil
	case errors.Is(cause, context.DeadlineExceeded):
		return fmt.Errorf("no answer within %s", wait)
	case err == nil:
		return errors.New("the server sent nothing back - check the base URL")
	}
	return err
}

// Builds the provider from what is on screen, so settings can be tried before they are saved.
func (w *MainWindow) providerUnderTest(provider string, settings config.ProviderSettings, apiKey, baseURL, model string) (ai.Provider, error) {
	settings.BaseURL = baseURL
	settings.Model = model
	return ai.FromSettings(provider, settings, apiKey, w.config.IsCustomProvider(provider))
}

const unreadableKeyHint = "The saved key can't be read on this computer. Enter it again."

func apiKeyPlaceholder(unreadable bool, placeholder string) string {
	if unreadable {
		return unreadableKeyHint
	}
	return placeholder
}

func (w *MainWindow) setAPIKeyUnreadable(unreadable bool) {
	w.apiKeyUnreadable = unreadable
	if w.apiKeyEntry != nil {
		w.apiKeyEntry.SetPlaceHolder(apiKeyPlaceholder(unreadable, "Enter your API key"))
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
	apiKeyEntry.PlaceHolder = "Enter your API key"

	requiresKey := widget.NewCheck("Requires an API key", func(checked bool) {
		if checked {
			apiKeyEntry.Enable()
		} else {
			apiKeyEntry.SetText("")
			apiKeyEntry.Disable()
		}
	})
	requiresKey.SetChecked(true)

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
		widget.NewFormItem("", requiresKey),
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
			LowReasoning: true,
		}

		// The key is encrypted first and the add undone if it cannot be saved, so a refused add leaves nothing behind.
		if apiKey := strings.TrimSpace(apiKeyEntry.Text); apiKey != "" {
			encrypted, err := encryptAPIKey(apiKey)
			if err != nil {
				addReport.Fail("Could not save the API key: " + err.Error())
				return
			}
			settings.APIKey = encrypted
		}

		if err := w.config.AddCustomProvider(name, settings); err != nil {
			addReport.Fail(err.Error())
			return
		}

		if err := w.config.Save(); err != nil {
			if undoErr := w.config.DeleteCustomProvider(name); undoErr != nil {
				logger.Error("Could not undo the unsaved provider", "provider", name, "error", undoErr)
			}
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

	d = dialog.NewCustomWithoutButtons("Add custom provider", content, w.Window)
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
		"Delete provider",
		fmt.Sprintf("Are you sure you want to delete '%s'?\n\nThis action cannot be undone.", currentProvider),
		func(confirmed bool) {
			if confirmed {
				w.deleteProvider(currentProvider)
			}
		},
		w.Window,
	)
}

func (w *MainWindow) deleteProvider(name string) {
	if err := w.config.DeleteCustomProvider(name); err != nil {
		w.statusBinding.Set("Could not delete the provider: " + shortMessage(err.Error()))
		return
	}
	for _, op := range config.OperationOrder {
		if operation := w.config.Operation(op); operation.ProviderID == name {
			operation.ProviderID = ""
			w.config.SetOperation(op, operation)
		}
	}

	// The provider is gone from the live config either way, so the tab must stop showing it.
	saveErr := w.config.Save()

	w.refreshProviderList()
	w.refreshOperationProviderOptions()
	delete(w.providerForms, name)
	if w.providerShown == name {
		w.providerShown = ""
	}
	newProvider := w.config.GetCurrentProvider()
	w.providerSelect.SetSelected(newProvider)
	w.providerBinding.Set(newProvider)
	w.loadProviderSettings(newProvider)
	if saveErr != nil {
		w.statusBinding.Set(fmt.Sprintf("Provider '%s' deleted, but could not save: %s", name, shortMessage(saveErr.Error())))
		return
	}
	w.statusBinding.Set(fmt.Sprintf("Provider '%s' deleted", name))
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
	w.loadModels("", apiKey, strings.TrimSpace(baseURL), modelEntry.Text, report, nil, func(model string) {
		modelEntry.SetText(model)
		report.Done("Model set to " + model)
	})
}

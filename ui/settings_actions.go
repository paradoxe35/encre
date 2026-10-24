package ui

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
)

const providerDefaultOption = "Default"

type operationEditor struct {
	prompt   *widget.Entry
	limit    *widget.Entry
	timeout  *widget.Slider
	provider *widget.Select
	ask      *askEditor
}

func (w *MainWindow) createActionsSection() fyne.CanvasObject {
	w.operationEditors = make(map[config.Operation]*operationEditor, len(config.OperationOrder))

	items := make([]*widget.AccordionItem, 0, len(config.OperationOrder))
	for _, op := range config.OperationOrder {
		if !op.UsesAI() {
			continue
		}
		editor := w.newOperationEditor(op)
		w.operationEditors[op] = editor
		items = append(items, widget.NewAccordionItem(op.Label(), editor.content(w, op)))
	}

	accordion := widget.NewAccordion(items...)

	w.mentionsCheck = w.dirtyCheck("Enable @provider mentions", w.config.ProviderMentionsEnabled())

	mentionsHelp := widget.NewLabel(
		"Start a selection with @provider to run that one action on it, " +
			"for example \"@claude Fix this\".")
	mentionsHelp.Wrapping = fyne.TextWrapWord
	mentionsHelp.TextStyle = fyne.TextStyle{Italic: true}

	return container.NewVScroll(container.NewVBox(
		accordion,
		widget.NewSeparator(),
		container.NewPadded(container.NewVBox(w.mentionsCheck, mentionsHelp)),
	))
}

func (w *MainWindow) newOperationEditor(op config.Operation) *operationEditor {
	operation := w.config.Operation(op)

	editor := &operationEditor{
		prompt:   w.dirtyMultiLineEntry(),
		limit:    w.dirtyEntry(),
		timeout:  widget.NewSlider(5, 300),
		provider: w.dirtySelect(w.providerOptions(), nil),
	}
	// The default prompt is what runs when the field is empty, so it shows as the placeholder.
	editor.prompt.SetPlaceHolder(config.DefaultPrompt(op))
	editor.prompt.SetText(operation.SystemPrompt)
	editor.prompt.Wrapping = fyne.TextWrapWord
	editor.prompt.SetMinRowsVisible(5)

	editor.limit.SetText(strconv.Itoa(operation.CharacterLimit))
	editor.limit.Validator = validateCharacterLimit

	editor.timeout.Step = 5
	editor.timeout.SetValue(float64(operation.TimeoutSeconds))

	editor.provider.SetSelected(providerLabel(operation.ProviderID))

	if op == config.OpAsk {
		editor.ask = w.newAskEditor(operation)
	}
	return editor
}

func (e *operationEditor) content(w *MainWindow, op config.Operation) fyne.CanvasObject {
	timeoutValue := widget.NewLabel("")
	syncTimeoutLabel(timeoutValue, e.timeout.Value)
	e.timeout.OnChanged = func(value float64) {
		syncTimeoutLabel(timeoutValue, value)
		w.markDirty()
	}

	form := widget.NewForm(
		widget.NewFormItem("Provider", e.provider),
		widget.NewFormItem("Character limit", e.limit),
		widget.NewFormItem("Timeout", container.NewBorder(nil, nil, nil, timeoutValue, e.timeout)),
	)
	if e.ask != nil {
		for _, item := range e.ask.items(w) {
			form.AppendItem(item)
		}
	}
	rows := []fyne.CanvasObject{boundBy(op), form}

	if op == config.OpTranslate {
		rows = append(rows, widget.NewSeparator(), w.translateLanguages())
	}

	rows = append(rows,
		widget.NewSeparator(),
		widget.NewLabel("System prompt"),
		e.prompt,
		container.NewHBox(
			widget.NewButton("Use built-in", func() { e.prompt.SetText("") }),
			widget.NewButton("Edit a copy", func() { e.prompt.SetText(config.DefaultPrompt(op)) }),
		),
	)

	return container.NewPadded(container.NewVBox(rows...))
}

func syncTimeoutLabel(label *widget.Label, value float64) {
	label.SetText(strconv.Itoa(int(value)) + "s")
}

func (w *MainWindow) providerOptions() []string {
	return append([]string{providerDefaultOption}, w.config.GetConfiguredProviderNames()...)
}

func (w *MainWindow) refreshOperationProviderOptions() {
	options := w.providerOptions()
	for _, editor := range w.operationEditors {
		selected := editor.provider.Selected
		editor.provider.Options = options
		if providerID(selected) != "" && !slices.Contains(options, selected) {
			selected = providerDefaultOption
		}
		editor.provider.Selected = selected
		editor.provider.Refresh()
	}
}

func providerLabel(id string) string {
	if id == "" {
		return providerDefaultOption
	}
	return id
}

func providerID(label string) string {
	if label == providerDefaultOption {
		return ""
	}
	return label
}

// The upper bound catches typos; too high a floor would make an existing smaller limit unsavable.
func validateCharacterLimit(value string) error { return validateNumber(value, 1, 100000) }

func validateNumber(value string, low, high int) error {
	if value == "" {
		return nil
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return errors.New("must be a number")
	}
	if number < low || number > high {
		return fmt.Errorf("must be between %d and %d", low, high)
	}
	return nil
}

func boundBy(op config.Operation) fyne.CanvasObject {
	var names []string
	for _, kind := range config.ActionOrder {
		if kind.Operation() == op {
			names = append(names, kind.Label())
		}
	}

	label := widget.NewLabel("Used by " + strings.Join(names, " and "))
	label.Wrapping = fyne.TextWrapWord
	label.TextStyle = fyne.TextStyle{Italic: true}
	return label
}

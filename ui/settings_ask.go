package ui

import (
	"fmt"
	"strconv"

	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
)

// askEditor holds the settings only Ask has, beside those every operation shares.
type askEditor struct {
	memory *widget.Entry
	tools  *widget.Check
}

func (w *MainWindow) newAskEditor(ask config.OperationConfig) *askEditor {
	editor := &askEditor{
		memory: w.dirtyEntry(),
		tools:  w.dirtyCheck("Let Ask look things up", ask.Tools),
	}
	editor.memory.SetText(strconv.Itoa(ask.Remembered() + 1))
	editor.memory.Validator = validateMemory
	return editor
}

func (a *askEditor) items(w *MainWindow) []*widget.FormItem {
	return []*widget.FormItem{
		{Text: "Remembered messages", Widget: a.memory, HintText: "1 sends only the question; 2 adds the previous exchange"},
		{Text: "Tools", Widget: a.tools, HintText: "Web search, web pages, Wikipedia and weather; free, with no API key"},
		widget.NewFormItem("Card style", w.answerCardStyleSelect()),
		widget.NewFormItem("Answer text size", w.answerTextSizeSelect()),
	}
}

// apply writes the Ask settings into the operation, or says which of them is wrong.
func (a *askEditor) apply(ask *config.OperationConfig) error {
	if err := validateMemory(a.memory.Text); err != nil {
		return fmt.Errorf("remembered messages %w", err)
	}
	ask.Memory, _ = strconv.Atoi(a.memory.Text)
	ask.Tools = a.tools.Checked
	return nil
}

func validateMemory(value string) error { return validateNumber(value, 1, config.MaxMemory) }

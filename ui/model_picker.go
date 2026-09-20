package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/ai"
	"github.com/paradoxe35/encre/internal/logger"
)

const modelListTimeout = 15 * time.Second

func (w *MainWindow) loadModels(provider, apiKey, baseURL, current string, report progress, apply func(string)) {
	report.Busy("Loading models…")

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), modelListTimeout)
		defer cancel()

		models, err := ai.ListModels(ctx, provider, apiKey, baseURL)

		fyne.Do(func() {
			switch {
			case err != nil:
				logger.Error("Could not list models", "provider", provider, "error", err)
				report.Fail("Could not load models: " + shortMessage(err.Error()))
			case len(models) == 0:
				report.Done("The provider lists no models")
			default:
				report.Done(fmt.Sprintf("%d models available", len(models)))
				w.showModelPicker(models, current, apply)
			}
		})
	}()
}

func (w *MainWindow) showModelPicker(models []ai.ModelInfo, current string, apply func(string)) {
	visible := models

	list := widget.NewList(
		func() int { return len(visible) },
		func() fyne.CanvasObject {
			id := widget.NewLabel("")
			name := widget.NewLabel("")
			name.TextStyle.Italic = true
			return container.NewHBox(id, name)
		},
		func(item widget.ListItemID, obj fyne.CanvasObject) {
			if item >= len(visible) {
				return
			}
			model := visible[item]
			cells := obj.(*fyne.Container).Objects

			id := cells[0].(*widget.Label)
			id.TextStyle.Bold = model.ID == current
			id.SetText(model.ID)

			cells[1].(*widget.Label).SetText(describeModel(model))
		},
	)

	search := widget.NewEntry()
	search.PlaceHolder = "Search models"
	search.OnChanged = func(query string) {
		visible = matchingModels(models, query)
		list.UnselectAll()
		list.Refresh()
		list.ScrollToTop()
	}

	picker := dialog.NewCustom("Select a model", "Cancel",
		container.NewBorder(search, nil, nil, nil, list), w.Window)
	picker.Resize(fyne.NewSize(460, 440))

	list.OnSelected = func(item widget.ListItemID) {
		if item >= len(visible) {
			return
		}
		apply(visible[item].ID)
		list.UnselectAll()
		picker.Hide()
	}

	picker.Show()
}

// Blank when the name only repeats the id, as with every built-in provider;
// a proxy like OpenRouter carries a name the id does not.
func describeModel(model ai.ModelInfo) string {
	if model.Name == "" || normalizeModelName(model.Name) == normalizeModelName(model.ID) {
		return ""
	}
	return model.Name
}

func normalizeModelName(value string) string {
	var builder strings.Builder
	for _, r := range strings.ToLower(value) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func matchingModels(models []ai.ModelInfo, query string) []ai.ModelInfo {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return models
	}

	matched := make([]ai.ModelInfo, 0, len(models))
	for _, model := range models {
		if strings.Contains(strings.ToLower(model.ID), query) ||
			strings.Contains(strings.ToLower(model.Name), query) {
			matched = append(matched, model)
		}
	}
	return matched
}

// Keeps a provider error inside the one line the status bar has.
func shortMessage(message string) string {
	message = strings.ReplaceAll(message, "\n", " ")
	if len(message) <= 80 {
		return message
	}
	return message[:77] + "..."
}

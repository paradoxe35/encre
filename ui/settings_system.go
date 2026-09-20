package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/platform"
	"github.com/paradoxe35/encre/internal/version"
)

func (w *MainWindow) createSystemSection() fyne.CanvasObject {
	themeLabel := widget.NewLabel("Theme")
	themeLabel.TextStyle.Bold = true

	// Applied in saveSettings, like every other control here.
	themeSelect := w.dirtySelect(themeLabels(), func(label string) {
		w.themeBinding.Set(themeValueFor(label))
	})

	currentTheme, _ := w.themeBinding.Get()
	themeSelect.SetSelected(themeLabelFor(currentTheme))

	themeDesc := widget.NewLabel("Auto follows the system theme.")
	themeDesc.Wrapping = fyne.TextWrapWord

	// Bind overwrites OnChanged, so dirty tracking hooks the binding instead.
	startMinimized := widget.NewCheck("Start minimized to system tray", nil)
	startMinimized.Bind(w.startMinimizedBinding)

	startOnLogin := widget.NewCheck("Start on login", nil)
	startOnLogin.Bind(w.startOnLoginBinding)

	form := container.NewVBox(
		container.NewPadded(container.NewVBox(themeLabel, themeSelect, themeDesc)),
		widget.NewSeparator(),
		container.NewPadded(container.NewVBox(startMinimized, startOnLogin)),
	)

	if version.IsProduction(w.app) {
		versionLabel := widget.NewLabel(fmt.Sprintf("Version: %s", version.GetVersion(w.app)))
		versionLabel.TextStyle.Italic = true
		versionLabel.Importance = widget.LowImportance

		form.Add(widget.NewSeparator())
		form.Add(container.NewPadded(versionLabel))
	}

	return container.NewVScroll(form)
}

func (w *MainWindow) applyTheme(themeName string) {
	switch themeName {
	case "light":
		w.app.Settings().SetTheme(&fixedVariant{theme.VariantLight})
	case "dark":
		w.app.Settings().SetTheme(&fixedVariant{theme.VariantDark})
	case "auto":
		w.app.Settings().SetTheme(theme.DefaultTheme())
	}
}

func (w *MainWindow) applyAutoStartSetting(enabled bool) {
	autoStart := platform.GetAutoStart()

	if enabled {
		if err := autoStart.Enable(); err != nil {
			logger.Error("Failed to enable auto-start", "error", err)
			w.statusBinding.Set("Failed to enable auto-start")
		} else {
			logger.Info("Auto-start enabled")
		}
	} else {
		if err := autoStart.Disable(); err != nil {
			logger.Error("Failed to disable auto-start", "error", err)
			w.statusBinding.Set("Failed to disable auto-start")
		} else {
			logger.Info("Auto-start disabled")
		}
	}
}

func (w *MainWindow) restartApplication() {
	logger.Info("User requested application restart")

	go func() {
		time.Sleep(200 * time.Millisecond)

		err := platform.RestartApplication()
		if err != nil {
			logger.Error("Failed to restart application", "error", err)
			return
		}

		logger.Info("New instance started, quitting current instance")

		fyne.Do(func() {
			w.app.Quit()
		})
	}()
}

var themes = []struct{ value, label string }{
	{"auto", "Auto"},
	{"light", "Light"},
	{"dark", "Dark"},
}

func themeLabels() []string {
	labels := make([]string, len(themes))
	for i, theme := range themes {
		labels[i] = theme.label
	}
	return labels
}

func themeLabelFor(value string) string {
	for _, theme := range themes {
		if theme.value == value {
			return theme.label
		}
	}
	return themes[0].label
}

func themeValueFor(label string) string {
	for _, theme := range themes {
		if theme.label == label {
			return theme.value
		}
	}
	return themes[0].value
}

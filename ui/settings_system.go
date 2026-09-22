package ui

import (
	"fmt"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/overlay"
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

	system := container.NewVBox(startMinimized, startOnLogin, w.createIndicatorControls())
	// Only Linux terminals refuse Ctrl+V: macOS always pastes with Cmd+V and Windows Terminal takes both.
	if runtime.GOOS == "linux" {
		system.Add(w.createPasteShortcutControls())
	}

	form := container.NewVBox(
		container.NewPadded(container.NewVBox(themeLabel, themeSelect, themeDesc)),
		widget.NewSeparator(),
		container.NewPadded(system),
		widget.NewSeparator(),
		container.NewPadded(w.createUpdateControls()),
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

// The indicator needs a window that floats without taking focus, which Wayland has no
// way to offer; the switch stays visible but off, with the reason.
func (w *MainWindow) createIndicatorControls() fyne.CanvasObject {
	indicator := widget.NewCheck("Show a floating indicator while an action runs", nil)
	indicator.Bind(w.indicatorBinding)
	if overlay.Supported() {
		return indicator
	}

	indicator.Disable()
	hint := widget.NewLabel("Not available on Wayland.")
	hint.Importance = widget.LowImportance
	return container.NewVBox(indicator, hint)
}

func (w *MainWindow) createUpdateControls() fyne.CanvasObject {
	label := widget.NewLabel("Updates")
	label.TextStyle.Bold = true

	if w.updates == nil {
		hint := widget.NewLabel("Updates are checked in release builds.")
		hint.Importance = widget.LowImportance
		return container.NewVBox(label, hint)
	}
	return container.NewVBox(label, w.updates.content())
}

func (w *MainWindow) createPasteShortcutControls() fyne.CanvasObject {
	label := widget.NewLabel("Paste shortcut")
	label.TextStyle.Bold = true

	pasteSelect := w.dirtySelect(pasteShortcutLabels(), func(label string) {
		w.pasteShortcutBinding.Set(string(pasteShortcutValueFor(label)))
	})

	current, _ := w.pasteShortcutBinding.Get()
	pasteSelect.SetSelected(pasteShortcutLabelFor(config.PasteShortcut(current)))

	hint := widget.NewLabel("Terminals paste with Ctrl+Shift+V. Pick it when you mostly dictate into a terminal.")
	hint.Wrapping = fyne.TextWrapWord

	return container.NewVBox(label, pasteSelect, hint)
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

var pasteShortcuts = []struct {
	value config.PasteShortcut
	label string
}{
	{config.PasteStandard, "Ctrl+V (standard)"},
	{config.PasteTerminal, "Ctrl+Shift+V (terminals)"},
}

func pasteShortcutLabels() []string {
	labels := make([]string, len(pasteShortcuts))
	for i, shortcut := range pasteShortcuts {
		labels[i] = shortcut.label
	}
	return labels
}

func pasteShortcutLabelFor(value config.PasteShortcut) string {
	for _, shortcut := range pasteShortcuts {
		if shortcut.value == value {
			return shortcut.label
		}
	}
	return pasteShortcuts[0].label
}

func pasteShortcutValueFor(label string) config.PasteShortcut {
	for _, shortcut := range pasteShortcuts {
		if shortcut.label == label {
			return shortcut.value
		}
	}
	return pasteShortcuts[0].value
}

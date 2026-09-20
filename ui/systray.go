package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/paradoxe35/encre/internal/logger"
)

func SetupSystemTray(desk desktop.App, mainWindow *MainWindow, onQuit func() error) {
	// Tray callbacks run off Fyne's thread, so UI work goes through fyne.Do.
	menu := fyne.NewMenu("Encre",
		fyne.NewMenuItem("Settings", func() {
			fyne.Do(mainWindow.ShowWindow)
		}),
		fyne.NewMenuItem("View Logs", func() {
			if err := logger.OpenLogFile(); err != nil {
				logger.Error("Failed to open log file", "error", err)
			}
		}),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Quit", func() {
			if err := onQuit(); err != nil {
				fyne.Do(mainWindow.app.Quit)
			}
		}),
	)

	desk.SetSystemTrayMenu(menu)
}

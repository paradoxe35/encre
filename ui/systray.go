package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/driver/desktop"
	"github.com/paradoxe35/encre/internal/logger"
)

func SetupSystemTray(desk desktop.App, mainWindow *MainWindow, onQuit func() error) {
	// Fyne runs tray callbacks on the UI thread and waits for them, so none may block on that thread.
	menu := fyne.NewMenu("Encre",
		fyne.NewMenuItem("Settings", func() {
			fyne.Do(mainWindow.ShowWindow)
		}),
		fyne.NewMenuItem("View logs", func() {
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

	mainWindow.tray = menu
	desk.SetSystemTrayMenu(menu)
}

package ui

import (
	"fyne.io/fyne/v2"
	"github.com/paradoxe35/encre/internal/logger"
)

type NotificationManager struct {
	app fyne.App
}

func NewNotificationManager(app fyne.App) *NotificationManager {
	return &NotificationManager{
		app: app,
	}
}

func (n *NotificationManager) ShowError(title, content string) {
	notification := fyne.NewNotification(title, content)
	n.app.SendNotification(notification)
	logger.Error("Error notification shown", "title", title, "content", content)
}

func (n *NotificationManager) ShowInfo(title, content string) {
	notification := fyne.NewNotification(title, content)
	n.app.SendNotification(notification)
	logger.Info("Info notification shown", "title", title, "content", content)
}

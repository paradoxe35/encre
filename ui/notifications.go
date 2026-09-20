package ui

import (
	"fyne.io/fyne/v2"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/platform"
)

type NotificationManager struct {
	app fyne.App
}

func NewNotificationManager(app fyne.App) *NotificationManager {
	return &NotificationManager{app: app}
}

func (n *NotificationManager) ShowError(title, content string) {
	n.show(title, content)
	logger.Error("Error notification shown", "title", title, "content", content)
}

func (n *NotificationManager) ShowInfo(title, content string) {
	n.show(title, content)
	logger.Info("Info notification shown", "title", title, "content", content)
}

func (n *NotificationManager) show(title, content string) {
	if platform.Toast(config.APP_ID, title, content) {
		return
	}
	n.app.SendNotification(fyne.NewNotification(title, content))
}

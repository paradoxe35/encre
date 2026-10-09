package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

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

// sentence capitalises a message built from an error, which starts in lower case. A first word
// that is not a plain lower-case word, such as a hotkey or a name, is left as it is.
func sentence(message string) string {
	word, _, _ := strings.Cut(message, " ")
	if word == "" || strings.IndexFunc(word, func(r rune) bool { return !unicode.IsLower(r) }) >= 0 {
		return message
	}
	first, size := utf8.DecodeRuneInString(message)
	return string(unicode.ToUpper(first)) + message[size:]
}

func (n *NotificationManager) show(title, content string) {
	content = sentence(content)
	if platform.Toast(config.APP_ID, title, content) {
		return
	}
	n.app.SendNotification(fyne.NewNotification(title, content))
}

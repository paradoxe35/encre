//go:build windows

package platform

import (
	"bytes"
	"os"

	"golang.org/x/sys/windows/registry"

	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/utils"
)

// RegisterNotifier tells Windows what to show as the sender of our toasts.
// Toasts are posted under the app ID, and without this registration the
// header is that raw ID with no icon. The ID belongs to the caller: Fyne
// posts under app.UniqueID().
func RegisterNotifier(id, name string, icon []byte) {
	iconPath := utils.AppHomeDir("notifier.ico")
	if current, err := os.ReadFile(iconPath); err != nil || !bytes.Equal(current, icon) {
		if err := os.WriteFile(iconPath, icon, 0o644); err != nil {
			logger.Warn("Could not write the notification icon", "error", err)
			return
		}
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+id, registry.SET_VALUE)
	if err != nil {
		logger.Warn("Could not register the notification sender", "error", err)
		return
	}
	defer key.Close()

	for value, data := range map[string]string{"DisplayName": name, "IconUri": iconPath} {
		if err := key.SetStringValue(value, data); err != nil {
			logger.Warn("Could not register the notification sender", "value", value, "error", err)
			return
		}
	}
}

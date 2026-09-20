//go:build windows

package platform

import (
	"bytes"
	"os"

	"golang.org/x/sys/windows/registry"

	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/utils"
)

// Toasts are posted under the app ID; without this registration the header is the raw ID
// with no icon. Fyne posts under app.UniqueID().
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

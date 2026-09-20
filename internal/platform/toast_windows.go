//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/paradoxe35/encre/internal/logger"
)

// Text only: the header Windows draws from the registered shortcut already carries the icon.
const toastScript = `$title = '%s'
$content = '%s'
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$toastXml = [xml] $template.GetXml()
$toastXml.GetElementsByTagName("text")[0].AppendChild($toastXml.CreateTextNode($title)) > $null
$toastXml.GetElementsByTagName("text")[1].AppendChild($toastXml.CreateTextNode($content)) > $null
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($toastXml.OuterXml)
$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('%s').Show($toast)
`

// Toast shows a toast under the app ID and reports whether it took the job.
func Toast(id, title, content string) bool {
	script := fmt.Sprintf(toastScript, psQuote(title), psQuote(content), psQuote(id))
	go func() {
		file, err := os.CreateTemp("", "encre-toast-*.ps1")
		if err != nil {
			logger.Warn("Could not show the notification", "error", err)
			return
		}
		defer os.Remove(file.Name())
		if _, err := file.WriteString(script); err != nil {
			file.Close()
			logger.Warn("Could not show the notification", "error", err)
			return
		}
		file.Close()

		cmd := exec.Command("PowerShell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Clean(file.Name()))
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err != nil {
			logger.Warn("Could not show the notification", "error", err)
		}
	}()
	return true
}

func psQuote(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

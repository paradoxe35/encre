//go:build windows

package platform

import (
	"os/exec"
	"syscall"

	"github.com/paradoxe35/encre/internal/logger"
)

func Toast(id, title, content string) bool {
	command := encodePowerShell(toastScriptFor(id, title, content))
	go func() {
		cmd := exec.Command("PowerShell", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-EncodedCommand", command)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if err := cmd.Run(); err != nil {
			logger.Warn("Could not show the notification", "error", err)
		}
	}()
	return true
}

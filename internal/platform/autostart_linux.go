//go:build linux

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/paradoxe35/encre/internal/logger"
)

type autoStart struct{}

// Quoting and escaping per the XDG desktop entry spec for the Exec field.
func escapeExecPath(path string) string {
	reservedChars := " \t\n\"'\\><~|&;$*?#()`"
	needsQuoting := false
	for _, char := range reservedChars {
		if strings.ContainsRune(path, char) {
			needsQuoting = true
			break
		}
	}

	if !needsQuoting {
		return path
	}

	escaped := path
	escaped = strings.ReplaceAll(escaped, `\`, `\\`) // backslash first
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "`", "\\`")
	escaped = strings.ReplaceAll(escaped, `$`, `\$`)

	return fmt.Sprintf(`"%s"`, escaped)
}

func (a *autoStart) Enable() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Resolving matters for AppImages and other symlinked installs.
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		logger.Warn("Failed to resolve symlinks", "error", err)
	}

	autostartDir := filepath.Join(homeDir, ".config", "autostart")
	if err := os.MkdirAll(autostartDir, 0755); err != nil {
		return fmt.Errorf("failed to create autostart directory: %w", err)
	}

	desktopFilePath := filepath.Join(autostartDir, "encre.desktop")

	escapedExec := escapeExecPath(executable)

	desktopContent := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Encre
Comment=AI-powered text revision tool
Exec=%s
Icon=encre
Terminal=false
StartupNotify=false
X-GNOME-Autostart-enabled=true
Categories=Utility;Office;
`, escapedExec)

	if err := os.WriteFile(desktopFilePath, []byte(desktopContent), 0644); err != nil {
		return fmt.Errorf("failed to write desktop file: %w", err)
	}

	logger.Info("Auto-start enabled for Linux", "desktop_file", desktopFilePath)
	return nil
}

func (a *autoStart) Disable() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	desktopFilePath := filepath.Join(homeDir, ".config", "autostart", "encre.desktop")

	if err := os.Remove(desktopFilePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove desktop file: %w", err)
	}

	logger.Info("Auto-start disabled for Linux")
	return nil
}

func (a *autoStart) IsEnabled() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false
	}

	desktopFilePath := filepath.Join(homeDir, ".config", "autostart", "encre.desktop")
	_, err = os.Stat(desktopFilePath)
	return err == nil
}

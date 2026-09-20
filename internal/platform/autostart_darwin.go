//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/paradoxe35/encre/internal/logger"
)

type autoStart struct{}

func getAppPath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		logger.Warn("Failed to resolve symlinks", "error", err)
	}

	// Path is Encre.app/Contents/MacOS/Encre when running from a bundle.
	if strings.Contains(executable, ".app/Contents/MacOS/") {
		parts := strings.Split(executable, ".app/Contents/MacOS/")
		if len(parts) >= 1 {
			return parts[0] + ".app", nil
		}
	}

	return executable, nil
}

func (a *autoStart) Enable() error {
	appPath, err := getAppPath()
	if err != nil {
		return err
	}

	script := fmt.Sprintf(`tell application "System Events" to make new login item with properties {path:"%s", hidden:false} at end`, appPath)

	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "already exists") || strings.Contains(string(output), "duplicate") {
			logger.Info("Login item already exists", "path", appPath)
			return nil
		}
		return fmt.Errorf("failed to add login item: %w, output: %s", err, string(output))
	}

	logger.Info("Auto-start enabled for macOS (Open at Login)", "path", appPath)
	return nil
}

func (a *autoStart) Disable() error {
	appPath, err := getAppPath()
	if err != nil {
		return err
	}

	appName := filepath.Base(appPath)

	script := fmt.Sprintf(`tell application "System Events" to delete login item "%s"`, strings.TrimSuffix(appName, ".app"))

	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "doesn't understand") || strings.Contains(string(output), "not found") {
			logger.Info("Login item not found (already removed)", "name", appName)
			return nil
		}
		return fmt.Errorf("failed to remove login item: %w, output: %s", err, string(output))
	}

	logger.Info("Auto-start disabled for macOS", "name", appName)
	return nil
}

func (a *autoStart) IsEnabled() bool {
	appPath, err := getAppPath()
	if err != nil {
		return false
	}

	script := `tell application "System Events" to get the name of every login item`

	cmd := exec.Command("osascript", "-e", script)
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	appName := filepath.Base(appPath)
	appName = strings.TrimSuffix(appName, ".app")

	loginItems := string(output)
	return strings.Contains(loginItems, appName)
}

//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/paradoxe35/encre/internal/logger"
	"golang.org/x/sys/windows/registry"
)

type autoStart struct{}

const (
	registryKey  = `Software\Microsoft\Windows\CurrentVersion\Run`
	registryName = "Encre"
)

func (a *autoStart) Enable() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		logger.Warn("Failed to resolve symlinks", "error", err)
	}

	// Quoted to avoid the "Unquoted Service Path" vulnerability on paths containing spaces.
	quotedPath := fmt.Sprintf(`"%s"`, executable)

	key, err := registry.OpenKey(registry.CURRENT_USER, registryKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open registry key: %w", err)
	}
	defer key.Close()

	if err := key.SetStringValue(registryName, quotedPath); err != nil {
		return fmt.Errorf("failed to set registry value: %w", err)
	}

	logger.Info("Auto-start enabled for Windows", "executable", executable)
	return nil
}

func (a *autoStart) Disable() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, registryKey, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("failed to open registry key: %w", err)
	}
	defer key.Close()

	if err := key.DeleteValue(registryName); err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("failed to delete registry value: %w", err)
	}

	logger.Info("Auto-start disabled for Windows")
	return nil
}

func (a *autoStart) IsEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, registryKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()

	_, _, err = key.GetStringValue(registryName)
	return err == nil
}

package platform

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/paradoxe35/encre/internal/logger"
)

type AutoStart interface {
	Enable() error
	Disable() error
	IsEnabled() bool
}

func GetAutoStart() AutoStart {
	return &autoStart{}
}

// launchPath is what starts Encre again: an AppImage runs from a mount that is gone once it exits.
func launchPath() (string, error) {
	if appImage := os.Getenv("APPIMAGE"); appImage != "" && runtime.GOOS == "linux" {
		return appImage, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return resolveSymlinks(executable), nil
}

func resolveSymlinks(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		logger.Warn("Failed to resolve symlinks", "path", path, "error", err)
		return path
	}
	return resolved
}

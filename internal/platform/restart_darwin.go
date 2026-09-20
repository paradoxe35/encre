package platform

import (
	"os"
	"os/exec"
	"strings"

	"github.com/paradoxe35/encre/internal/logger"
)

func RestartApplication() error {
	executable, err := os.Executable()
	if err != nil {
		logger.Error("Failed to get executable path", "error", err)
		return err
	}

	logger.Info("Restarting application", "executable", executable)

	var cmd *exec.Cmd

	if strings.Contains(executable, ".app/Contents/MacOS/") {
		appPath := executable[:strings.Index(executable, ".app/Contents/MacOS/")+4]
		logger.Info("Detected .app bundle, using 'open' command", "app", appPath)
		// -n forces a new instance instead of activating the existing one.
		cmd = exec.Command("open", "-n", appPath)
	} else {
		logger.Info("Using direct executable launch")
		cmd = exec.Command(executable)
	}

	err = cmd.Start()
	if err != nil {
		logger.Error("Failed to start new instance", "error", err)
		return err
	}

	logger.Info("New instance started successfully")
	return nil
}

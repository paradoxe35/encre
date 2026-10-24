package platform

import (
	"os"
	"os/exec"

	"github.com/paradoxe35/encre/internal/logger"
)

func RestartApplication() error {
	executable, err := os.Executable()
	if err != nil {
		logger.Error("Failed to get executable path", "error", err)
		return err
	}

	logger.Info("Restarting application", "executable", executable)

	var start func() error

	if appPath, ok := appBundlePath(executable); ok {
		logger.Info("Detected .app bundle, using 'open' command", "app", appPath)
		// -n forces a new instance instead of activating the existing one. Run, so a failed launch is reported.
		start = exec.Command("open", "-n", appPath).Run
	} else {
		logger.Info("Using direct executable launch")
		start = exec.Command(executable).Start
	}

	if err := claimed.Load().startInOurPlace(start); err != nil {
		logger.Error("Failed to start new instance", "error", err)
		return err
	}

	logger.Info("New instance started successfully")
	return nil
}

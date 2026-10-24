//go:build !darwin

package platform

import (
	"os"
	"os/exec"

	"github.com/paradoxe35/encre/internal/logger"
)

func RestartApplication() error {
	executable, err := launchPath()
	if err != nil {
		logger.Error("Failed to get executable path", "error", err)
		return err
	}

	logger.Info("Restarting application", "executable", executable)

	cmd := exec.Command(executable)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := claimed.Load().startInOurPlace(cmd.Start); err != nil {
		logger.Error("Failed to start new instance", "error", err)
		return err
	}

	logger.Info("New instance started successfully")
	return nil
}

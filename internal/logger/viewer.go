package logger

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func GetLatestLogFile() (string, error) {
	return latestLog(GetLogDirectory())
}

func latestLog(dir string) (string, error) {
	logs := listLogs(dir)
	if len(logs) == 0 {
		return "", fmt.Errorf("no log files found")
	}
	return filepath.Join(dir, logs[len(logs)-1].name), nil
}

func OpenLogFile() error {
	logFile, err := GetLatestLogFile()
	if err != nil {
		Info("Failed to find latest log file, opening directory instead", "error", err)
		return OpenLogDirectory()
	}

	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		Info("Log file does not exist, opening directory instead", "file", logFile)
		return OpenLogDirectory()
	}

	Info("Opening log file", "file", logFile)

	var openErr error
	switch runtime.GOOS {
	case "windows":
		// notepad.exe is the only editor guaranteed present; fall back to explorer if it's missing.
		openErr = launch(exec.Command("notepad.exe", logFile))
		if openErr != nil {
			openErr = launch(exec.Command("explorer.exe", logFile))
		}
	case "darwin":
		openErr = launch(exec.Command("open", logFile))
	case "linux":
		openErr = launch(exec.Command("xdg-open", logFile))
	default:
		return OpenLogDirectory()
	}

	if openErr != nil {
		Warn("Failed to open log file, opening directory instead", "error", openErr)
		return OpenLogDirectory()
	}

	return nil
}

func OpenLogDirectory() error {
	logDir := GetLogDirectory()

	Info("Opening log directory", "directory", logDir)

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("explorer.exe", logDir)
	case "darwin":
		cmd = exec.Command("open", logDir)
	case "linux":
		cmd = exec.Command("xdg-open", logDir)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	if err := launch(cmd); err != nil {
		Error("Failed to open log directory", "error", err, "directory", logDir)
		return fmt.Errorf("failed to open directory: %w", err)
	}

	return nil
}

// launch starts a viewer and reaps it in the background, so no zombie outlives the click.
func launch(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

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

	var cmd *exec.Cmd
	var openErr error

	switch runtime.GOOS {
	case "windows":
		// notepad.exe is the only editor guaranteed present; fall back to explorer if it's missing.
		cmd = exec.Command("notepad.exe", logFile)
		openErr = cmd.Start()
		if openErr != nil {
			cmd = exec.Command("explorer.exe", logFile)
			openErr = cmd.Start()
		}
	case "darwin":
		cmd = exec.Command("open", logFile)
		openErr = cmd.Start()
	case "linux":
		cmd = exec.Command("xdg-open", logFile)
		openErr = cmd.Start()
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

	err := cmd.Start()
	if err != nil {
		Error("Failed to open log directory", "error", err, "directory", logDir)
		return fmt.Errorf("failed to open directory: %w", err)
	}

	return nil
}

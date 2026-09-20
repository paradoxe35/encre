package logger

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/paradoxe35/encre/internal/utils"
)

var (
	defaultLogger  *slog.Logger
	currentLogFile string
)

func Init() error {
	logDir := utils.AppHomeDir("logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	today := time.Now().Format("2006-01-02")
	logFile := filepath.Join(logDir, fmt.Sprintf("encre-%s.log", today))
	currentLogFile = logFile

	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	logLevel := slog.LevelInfo
	if os.Getenv("DEBUG") != "" {
		logLevel = slog.LevelDebug
	}

	opts := &slog.HandlerOptions{
		Level: logLevel,
	}

	handler := slog.NewTextHandler(file, opts)
	defaultLogger = slog.New(handler)
	slog.SetDefault(defaultLogger)

	defaultLogger.Info("Logger initialized", "log_file", logFile)

	go cleanupOldLogs(logDir, 30)

	return nil
}

func Info(msg string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.Info(msg, args...)
	}
}

func Error(msg string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.Error(msg, args...)
	}
}

func Debug(msg string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.Debug(msg, args...)
	}
}

func Warn(msg string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.Warn(msg, args...)
	}
}

func GetCurrentLogFile() string {
	if currentLogFile != "" {
		return currentLogFile
	}

	today := time.Now().Format("2006-01-02")
	return utils.AppHomeDir("logs", fmt.Sprintf("encre-%s.log", today))
}

func GetLogDirectory() string {
	return utils.AppHomeDir("logs")
}

func cleanupOldLogs(logDir string, maxAgeDays int) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return
	}

	cutoffDate := time.Now().AddDate(0, 0, -maxAgeDays)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if filepath.Ext(entry.Name()) != ".log" {
			continue
		}

		logPath := filepath.Join(logDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		if info.ModTime().Before(cutoffDate) {
			if err := os.Remove(logPath); err == nil {
				Info("Removed old log file", "file", entry.Name())
			}
		}
	}
}

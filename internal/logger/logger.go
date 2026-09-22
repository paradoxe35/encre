package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/paradoxe35/encre/internal/utils"
)

var defaultLogger *slog.Logger

func Init() error {
	logDir := utils.AppHomeDir("logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	logLevel := slog.LevelInfo
	if os.Getenv("DEBUG") != "" {
		logLevel = slog.LevelDebug
	}

	handler := slog.NewTextHandler(newRotatingFile(logDir, time.Now), &slog.HandlerOptions{
		Level: logLevel,
	})
	defaultLogger = slog.New(handler)
	slog.SetDefault(defaultLogger)

	defaultLogger.Info("Logger initialized", "log_dir", logDir)
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

func Log(level slog.Level, msg string, args ...any) {
	if defaultLogger != nil {
		defaultLogger.Log(context.Background(), level, msg, args...)
	}
}

func GetLogDirectory() string {
	return utils.AppHomeDir("logs")
}

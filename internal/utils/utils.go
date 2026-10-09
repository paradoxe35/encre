package utils

import (
	"log"
	"os"
	"path/filepath"
)

const appDirName = ".encre"

// Cannot log: the logger calls this to build its path, so the import would cycle.
func AppHomeDir(elem ...string) string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Printf("failed to get user home directory, falling back to the temp directory: %v", err)
		homeDir = os.TempDir()
	}

	parts := append([]string{homeDir, appDirName}, elem...)
	return filepath.Join(parts...)
}

func EnsureAppHomeDir() {
	if err := os.MkdirAll(AppHomeDir(), 0755); err != nil {
		log.Fatalf("failed to create app home directory: %v", err)
	}
}

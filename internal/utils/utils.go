package utils

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
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
	if err := os.MkdirAll(AppHomeDir(), 0o700); err != nil {
		log.Fatalf("failed to create app home directory: %v", err)
	}
}

// MakePrivate narrows a file to its owner. Windows has no such modes, so it is skipped there.
func MakePrivate(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// WriteFileAtomic replaces path in one rename, so a crash mid-write leaves the old file whole.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()

	if runtime.GOOS != "windows" {
		if err = tmp.Chmod(perm); err != nil {
			return err
		}
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

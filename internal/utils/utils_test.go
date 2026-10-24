package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestWriteFileAtomicReplacesAndTightens(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Errorf("content = %q, want new", data)
	}
	if got := mode(t, path); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestWriteFileAtomicCleansUpOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "taken")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "child"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(target, []byte("x"), 0o600); err == nil {
		t.Fatal("renaming over a non-empty directory should fail")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp file left behind: %v", entries)
	}
}

func TestEnsureAppHomeDirIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
	t.Setenv("HOME", t.TempDir())
	EnsureAppHomeDir()
	if got := mode(t, AppHomeDir()); got != 0o700 {
		t.Errorf("mode = %o, want 700", got)
	}
}

func TestMakePrivateIgnoresAMissingFile(t *testing.T) {
	if err := MakePrivate(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Errorf("got %v, want nil", err)
	}
}

func TestAnAtomicWriteGoesThroughASymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles-config.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileAtomic(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a plain file")
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Fatalf("the linked file reads %q", data)
	}
}

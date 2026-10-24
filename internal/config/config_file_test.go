package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes on Windows")
	}
}

func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestLoadCreatesAPrivateDirectoryAndFile(t *testing.T) {
	skipOnWindows(t)
	t.Setenv("HOME", t.TempDir())

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, filepath.Dir(ConfigPath())); got != 0o700 {
		t.Errorf("directory mode = %o, want 700", got)
	}
	if got := perm(t, ConfigPath()); got != 0o600 {
		t.Errorf("file mode = %o, want 600", got)
	}
}

// Older installs wrote 0644, and the file holds API keys.
func TestLoadTightensAnExistingWorldReadableConfig(t *testing.T) {
	skipOnWindows(t)
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, ConfigPath()); got != 0o600 {
		t.Errorf("file mode = %o, want 600", got)
	}
}

func TestSaveTightensAnExistingWorldReadableConfig(t *testing.T) {
	skipOnWindows(t)
	t.Setenv("HOME", t.TempDir())
	resetListeners(t)
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Default().Save(); err != nil {
		t.Fatal(err)
	}
	if got := perm(t, ConfigPath()); got != 0o600 {
		t.Errorf("file mode = %o, want 600", got)
	}
}

func TestLoadSetsACorruptConfigAside(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetListeners(t)
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	damaged := []byte(`{"ai_provider": {"providers": {"openai": {"api_key": "sk-keep"`)
	if err := os.WriteFile(ConfigPath(), damaged, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load should report the parse failure")
	}

	if _, err := os.Stat(ConfigPath()); !os.IsNotExist(err) {
		t.Error("the corrupt file should have been moved away")
	}
	matches, _ := filepath.Glob(ConfigPath() + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("got %v, want one corrupt-* backup", matches)
	}
	kept, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != string(damaged) {
		t.Error("the backup must keep the damaged content intact")
	}

	if err := Default().Save(); err != nil {
		t.Fatal(err)
	}
	if kept, _ := os.ReadFile(matches[0]); string(kept) != string(damaged) {
		t.Error("a later save must not touch the backup")
	}
}

func TestConcurrentSavesLeaveAValidFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetListeners(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := cfg.Save(); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if _, err := Load(); err != nil {
		t.Fatalf("config unreadable after concurrent saves: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(ConfigPath()))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

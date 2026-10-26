// Package testhome gives a test its own home directory, where Encre keeps its files.
package testhome

import (
	"path/filepath"
	"testing"
)

// Use points the home directory at a new temporary one for the test: HOME on Unix, and
// USERPROFILE, which os.UserHomeDir reads instead on Windows. XDG_CONFIG_HOME goes with it, as
// what is kept under ~/.config is kept there instead when it is set.
func Use(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

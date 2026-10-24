// Package testhome gives a test its own home directory, where Encre keeps its files.
package testhome

import "testing"

// Use points the home directory at a new temporary one for the test: HOME on Unix, and
// USERPROFILE, which os.UserHomeDir reads instead on Windows.
func Use(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

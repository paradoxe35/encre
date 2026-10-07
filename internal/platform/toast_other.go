//go:build !windows && !darwin

package platform

// Toast leaves notifications to Fyne on Linux.
func Toast(id, title, content string) bool { return false }

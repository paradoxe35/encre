//go:build !windows

package platform

// Toast leaves notifications to Fyne outside Windows.
func Toast(id, title, content string) bool { return false }

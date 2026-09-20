//go:build !windows

package platform

// Only Windows posts toasts under an app ID that it otherwise shows verbatim.
func RegisterNotifier(id, name string) {}

//go:build !windows

package platform

// RegisterNotifier is only needed on Windows, where toasts are posted under an
// app ID that the system otherwise shows verbatim.
func RegisterNotifier(id, name string, icon []byte) {}

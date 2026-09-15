//go:build !windows

package platform

// SetWindowIcons is Windows-only. X11 window managers scale the image GLFW
// sets, and Wayland and macOS take the icon from the desktop file or bundle.
func SetWindowIcons(ico []byte) {}

package overlay

// waylandSession reads the session the way Fyne picks its platform, for the moment
// before the window system is up. FYNE_PLATFORM wins; otherwise a Wayland session
// without an X server to fall back to is Wayland.
func waylandSession(env func(string) string) bool {
	switch env("FYNE_PLATFORM") {
	case "x11":
		return false
	case "wayland":
		return true
	}
	return env("WAYLAND_DISPLAY") != "" && env("DISPLAY") == ""
}

//go:build linux

package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

// systray corrupts translucent pixels and panels downscale with linear filtering, so the
// tray gets a hard-edged 32 px image that halves cleanly. Rendered by scripts/generate_icons.py.
//
//go:embed assets/tray.png
var trayIconPNG []byte

func trayIcon() fyne.Resource {
	return fyne.NewStaticResource("tray.png", trayIconPNG)
}

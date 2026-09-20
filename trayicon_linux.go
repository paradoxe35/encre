//go:build linux

package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

// The systray library sends every translucent pixel with a corrupted colour,
// and panels shrink whatever they get with plain linear filtering. So the tray
// gets a hard-edged 32 px image: halving it to the panel size is what
// smooths the outline. Rendered by scripts/generate_icons.py.
//
//go:embed assets/tray.png
var trayIconPNG []byte

func trayIcon() fyne.Resource {
	return fyne.NewStaticResource("tray.png", trayIconPNG)
}

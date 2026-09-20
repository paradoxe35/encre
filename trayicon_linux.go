//go:build linux

package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

// Panels show the pixmap at 16 to 24 px with plain linear filtering, which
// turns the 256 px application icon into a jagged blob. 32 px scales cleanly.
//
//go:embed assets/tray.png
var trayIconPNG []byte

func trayIcon() fyne.Resource {
	return fyne.NewStaticResource("tray.png", trayIconPNG)
}

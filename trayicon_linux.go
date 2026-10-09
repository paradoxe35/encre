//go:build linux

package main

import (
	_ "embed"

	"fyne.io/fyne/v2"
)

// systray corrupts translucency and panels downscale linearly, so: hard-edged 32 px that halves.
//
//go:embed assets/tray.png
var trayIconPNG []byte

func trayIcon() fyne.Resource {
	return fyne.NewStaticResource("tray.png", trayIconPNG)
}

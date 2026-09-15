package main

import (
	_ "embed"

	"github.com/paradoxe35/encre/internal/platform"
)

// The .ico carries a hand-drawn frame for each size Windows shows an icon at.
// Fyne only passes the 256 px PNG on, so the Windows build hands these over
// itself once the window exists; see platform.SetWindowIcons.
//
//go:embed assets/icon.ico
var windowIconICO []byte

func applyNativeWindowIcons() {
	platform.SetWindowIcons(windowIconICO)
}

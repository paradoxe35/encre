package main

import (
	_ "embed"

	"github.com/paradoxe35/encre/internal/platform"
)

//go:embed assets/icon.ico
var windowIconICO []byte

func applyNativeWindowIcons() {
	platform.SetWindowIcons(windowIconICO)
}

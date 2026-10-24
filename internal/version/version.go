package version

import (
	"strings"

	"fyne.io/fyne/v2"
)

func GetVersion(app fyne.App) string {
	if v := app.Metadata().Version; v != "" {
		return v
	}
	return "dev"
}

func GetBuildNumber(app fyne.App) int {
	return app.Metadata().Build
}

// IsProduction needs a `fyne package --release` build: go run and go build read the version from FyneApp.toml.
func IsProduction(app fyne.App) bool {
	if !app.Metadata().Release {
		return false
	}
	version := GetVersion(app)
	if version == "dev" {
		return false
	}

	for _, marker := range []string{"-dev", "-alpha", "-beta", "-rc", "-snapshot"} {
		if strings.Contains(version, marker) {
			return false
		}
	}
	return true
}

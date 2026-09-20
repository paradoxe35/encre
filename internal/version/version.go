package version

import (
	"fyne.io/fyne/v2"
)

func GetVersion(app fyne.App) string {
	if app == nil {
		return "unknown"
	}

	meta := app.Metadata()
	if meta.Version != "" {
		return meta.Version
	}

	return "dev"
}

func GetBuildNumber(app fyne.App) int {
	if app == nil {
		return 0
	}

	meta := app.Metadata()
	return meta.Build
}

func IsProduction(app fyne.App) bool {
	if app == nil {
		return false
	}

	version := GetVersion(app)

	if version == "dev" || version == "unknown" || version == "" {
		return false
	}

	nonProdMarkers := []string{"-dev", "-alpha", "-beta", "-rc", "-snapshot"}
	for _, marker := range nonProdMarkers {
		if contains(version, marker) {
			return false
		}
	}

	return true
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

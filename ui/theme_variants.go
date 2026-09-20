package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// fixedVariant pins the colours to one variant and leaves the rest to the default theme.
type fixedVariant struct{ variant fyne.ThemeVariant }

func (t *fixedVariant) Color(name fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(name, t.variant)
}

func (t *fixedVariant) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (t *fixedVariant) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (t *fixedVariant) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}

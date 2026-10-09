package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Fyne clears every window with the app theme's background, so that stays transparent for the glass
// card, and windows paint colorNameWindow instead.
const colorNameWindow fyne.ThemeColorName = "encreWindow"

// A nil variant follows the system.
type appTheme struct {
	variant *fyne.ThemeVariant
}

func newAppTheme(variant *fyne.ThemeVariant) *appTheme {
	return &appTheme{variant: variant}
}

func (t *appTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if t.variant != nil {
		variant = *t.variant
	}
	switch name {
	case theme.ColorNameBackground:
		return color.Transparent
	case colorNameWindow:
		name = theme.ColorNameBackground
	}
	return theme.DefaultTheme().Color(name, variant)
}

func (t *appTheme) Size(name fyne.ThemeSizeName) float32 { return theme.DefaultTheme().Size(name) }

func (t *appTheme) Font(style fyne.TextStyle) fyne.Resource { return theme.DefaultTheme().Font(style) }

func (t *appTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func hex(rgb uint32) color.NRGBA {
	return color.NRGBA{R: uint8(rgb >> 16), G: uint8(rgb >> 8), B: uint8(rgb), A: 0xff}
}

func withAlpha(c color.Color, alpha float64) color.NRGBA {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	n.A = uint8(alpha * 0xff)
	return n
}

// themedFill takes its colours from the theme it is drawn in, so it follows a change of theme as
// widgets do. An empty name leaves that part undrawn.
type themedFill struct {
	widget.BaseWidget
	fill, edge   fyne.ThemeColorName
	cornerRadius float32
}

func newThemedFill(fill, edge fyne.ThemeColorName, cornerRadius float32) *themedFill {
	f := &themedFill{fill: fill, edge: edge, cornerRadius: cornerRadius}
	f.ExtendBaseWidget(f)
	return f
}

func (f *themedFill) CreateRenderer() fyne.WidgetRenderer {
	r := &themedFillRenderer{fill: f, rect: canvas.NewRectangle(color.Transparent)}
	r.Refresh()
	return r
}

type themedFillRenderer struct {
	fill *themedFill
	rect *canvas.Rectangle
}

func (r *themedFillRenderer) Layout(size fyne.Size)        { r.rect.Resize(size) }
func (r *themedFillRenderer) MinSize() fyne.Size           { return fyne.Size{} }
func (r *themedFillRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.rect} }
func (r *themedFillRenderer) Destroy()                     {}

func (r *themedFillRenderer) Refresh() {
	th := theme.CurrentForWidget(r.fill)
	variant := fyne.CurrentApp().Settings().ThemeVariant()
	r.rect.FillColor = color.Transparent
	if r.fill.fill != "" {
		r.rect.FillColor = th.Color(r.fill.fill, variant)
	}
	r.rect.CornerRadius = r.fill.cornerRadius
	r.rect.StrokeWidth = 0
	if r.fill.edge != "" {
		r.rect.StrokeColor = th.Color(r.fill.edge, variant)
		r.rect.StrokeWidth = 1
	}
	r.rect.Refresh()
}

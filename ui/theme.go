package ui

import (
	"image/color"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Fyne clears windows with the app background, kept transparent for glass; windows paint this instead.
const colorNameWindow fyne.ThemeColorName = "encreWindow"

// sizeNameCardLink is the answer card's text size, which its links take from the app theme.
const sizeNameCardLink fyne.ThemeSizeName = "encreCardLink"

// cardLinks is the theme the answer card reads in. Only the card draws links, and Fyne draws a link
// in the app theme unless a theme override is refreshed over it, keeping what it drew there for good;
// so the app theme lends links the card's colour and size instead.
var cardLinks atomic.Pointer[cardLinkLook]

type cardLinkLook struct {
	light, dark color.Color
	size        float32
}

// lendCardLinks resolves the card's look with none lent, as a card design without its own link
// colour falls back to the app theme, which would ask the card again.
func lendCardLinks(reading fyne.Theme) {
	cardLinks.Store(nil)
	cardLinks.Store(&cardLinkLook{
		light: reading.Color(theme.ColorNameHyperlink, theme.VariantLight),
		dark:  reading.Color(theme.ColorNameHyperlink, theme.VariantDark),
		size:  reading.Size(theme.SizeNameText),
	})
}

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
	case theme.ColorNameHyperlink:
		if look := cardLinks.Load(); look != nil {
			if variant == theme.VariantLight {
				return look.light
			}
			return look.dark
		}
	case theme.ColorNameInputBorder:
		// Fyne's is about 1.2:1 on the background, so an unticked box all but vanishes; these reach 3:1.
		if variant == theme.VariantLight {
			return hex(0x8f8f8f)
		}
		return hex(0x6b6b6b)
	}
	return theme.DefaultTheme().Color(name, variant)
}

func (t *appTheme) Size(name fyne.ThemeSizeName) float32 {
	if name == sizeNameCardLink {
		if look := cardLinks.Load(); look != nil {
			return look.size
		}
		name = theme.SizeNameText
	}
	return theme.DefaultTheme().Size(name)
}

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

// themedFill follows a change of theme as widgets do; an empty name leaves that part undrawn.
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

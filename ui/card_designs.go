package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"github.com/paradoxe35/encre/internal/config"
	"github.com/paradoxe35/encre/internal/overlay"
)

// Colours a card design gives its surface, beside the widget colours it sets.
const (
	colorNameCard     fyne.ThemeColorName = "encreCard"
	colorNameCardEdge fyne.ThemeColorName = "encreCardEdge"
	colorNameGlass    fyne.ThemeColorName = "encreGlass"
	colorNameFrost    fyne.ThemeColorName = "encreFrost"
)

type palette = map[fyne.ThemeColorName]color.Color

type cardDesign struct {
	// variant pins the card light or dark; nil follows the app.
	variant *fyne.ThemeVariant
	colors  func(variant fyne.ThemeVariant) palette
	glass   bool
	// monospace sets every text in the card in the monospace face, which Fyne measures as it draws.
	monospace bool
	// rounded softens the input and selections; only the original design stays square.
	rounded bool
	// glow is drawn under the card's fill, for a design of more than one colour.
	glow func() []fyne.CanvasObject
}

var (
	darkVariant  = theme.VariantDark
	lightVariant = theme.VariantLight
)

var cardDesigns = map[config.CardStyle]cardDesign{
	config.CardStyleSolid: {
		variant: &darkVariant,
		colors:  always(palette{colorNameCard: overlay.Surface, colorNameCardEdge: color.Transparent}),
	},
	config.CardStyleGlass: {colors: glassColors, glass: true, rounded: true},
	config.CardStyleGraphite: {
		variant: &darkVariant,
		rounded: true,
		colors: always(swatch{
			text: hex(0xececed), muted: hex(0x8a8f98), accent: hex(0xff6363),
			input: hex(0x1d1e21), line: withAlpha(hex(0xffffff), 0.09), edge: withAlpha(hex(0xffffff), 0.12),
		}.palette()),
		glow: func() []fyne.CanvasObject {
			return []fyne.CanvasObject{
				canvas.NewLinearGradient(hex(0x232428), hex(0x0b0c0e), 0),
				bloom(withAlpha(hex(0xff6363), 0.1), 0.45, -0.55),
			}
		},
	},
	config.CardStyleMidnight: {
		variant: &darkVariant,
		rounded: true,
		colors: always(swatch{
			text: hex(0xe8e7ff), muted: hex(0x8f8ec0), accent: hex(0x8b8cff),
			input: withAlpha(hex(0xffffff), 0.07), line: withAlpha(hex(0x8b8cff), 0.22), edge: withAlpha(hex(0x8b8cff), 0.38),
		}.palette()),
		glow: func() []fyne.CanvasObject {
			return []fyne.CanvasObject{
				canvas.NewLinearGradient(hex(0x1d1b45), hex(0x0d0c24), 0),
				bloom(withAlpha(hex(0x7c6cf2), 0.32), -0.4, -0.5),
			}
		},
	},
	config.CardStyleAurora: {
		variant: &darkVariant,
		rounded: true,
		colors: always(swatch{
			text: hex(0xf1f5f9), muted: hex(0x94a3b8), accent: hex(0x2dd4bf),
			input: withAlpha(hex(0xffffff), 0.07), line: withAlpha(hex(0xffffff), 0.11), edge: withAlpha(hex(0xffffff), 0.18),
		}.palette()),
		glow: func() []fyne.CanvasObject {
			return []fyne.CanvasObject{
				canvas.NewRectangle(hex(0x0b0f14)),
				bloom(withAlpha(hex(0x2dd4bf), 0.30), -0.45, -0.5),
				bloom(withAlpha(hex(0xe879f9), 0.26), 0.45, 0.5),
			}
		},
	},
	config.CardStylePaper: {
		variant: &lightVariant,
		rounded: true,
		colors: always(swatch{
			light: true,
			text:  hex(0x2b2622), muted: hex(0x9a8f80), accent: hex(0xd97757),
			input: hex(0xffffff), line: hex(0xe7dfd2), edge: hex(0xdcd1bf),
		}.palette()),
		glow: func() []fyne.CanvasObject {
			return []fyne.CanvasObject{canvas.NewLinearGradient(hex(0xfdfbf7), hex(0xf3ede3), 0)}
		},
	},
	config.CardStyleTerminal: {
		variant:   &darkVariant,
		monospace: true,
		colors: always(swatch{
			text: hex(0xa7f3c0), muted: hex(0x4f8a64), accent: hex(0x4ade80),
			input: hex(0x0f1812), line: withAlpha(hex(0x4ade80), 0.2), edge: withAlpha(hex(0x4ade80), 0.35),
			card: hex(0x090e0a),
		}.palette()),
	},
}

func designFor(style config.CardStyle) cardDesign {
	if design, ok := cardDesigns[style]; ok {
		return design
	}
	return cardDesigns[config.CardStyleGlass]
}

func always(colors palette) func(fyne.ThemeVariant) palette {
	return func(fyne.ThemeVariant) palette { return colors }
}

// bloom is a soft glow of colour, its centre offset by a share of the card's width and height.
func bloom(c color.Color, x, y float64) *canvas.RadialGradient {
	glow := canvas.NewRadialGradient(c, withAlpha(c, 0))
	glow.CenterOffsetX, glow.CenterOffsetY = x, y
	return glow
}

// swatch is the handful of colours a design picks; the rest of its palette follows from them.
type swatch struct {
	light                                  bool
	text, muted, accent, input, line, edge color.Color
	// card is the fill, left clear when the glow draws the surface.
	card color.Color
}

func (s swatch) palette() palette {
	tint := hex(0xffffff)
	if s.light {
		tint = hex(0x000000)
	}
	card := s.card
	if card == nil {
		card = color.Transparent
	}
	return palette{
		colorNameCard:                    card,
		colorNameCardEdge:                s.edge,
		theme.ColorNameForeground:        s.text,
		theme.ColorNamePlaceHolder:       s.muted,
		theme.ColorNameDisabled:          s.muted,
		theme.ColorNamePrimary:           s.accent,
		theme.ColorNameHyperlink:         s.accent,
		theme.ColorNameFocus:             withAlpha(s.accent, 0.6),
		theme.ColorNameSelection:         withAlpha(s.accent, 0.28),
		theme.ColorNameInputBackground:   s.input,
		theme.ColorNameInputBorder:       s.line,
		theme.ColorNameSeparator:         s.line,
		theme.ColorNameButton:            s.input,
		theme.ColorNameHover:             withAlpha(tint, 0.07),
		theme.ColorNamePressed:           withAlpha(tint, 0.13),
		theme.ColorNameScrollBar:         withAlpha(s.muted, 0.5),
		theme.ColorNameOverlayBackground: s.input,
	}
}

// glassColors is Apple's system palette, over glass of the card's own colour.
func glassColors(variant fyne.ThemeVariant) palette {
	colors := palette{
		theme.ColorNameForeground:        hex(0xf5f5f7),
		theme.ColorNamePrimary:           hex(0x0a84ff),
		theme.ColorNameFocus:             withAlpha(hex(0x0a84ff), 0.6),
		theme.ColorNameSelection:         withAlpha(hex(0x0a84ff), 0.32),
		theme.ColorNameInputBackground:   hex(0x2c2c2e),
		theme.ColorNameInputBorder:       hex(0x3a3a3c),
		theme.ColorNameButton:            hex(0x3a3a3c),
		theme.ColorNameHover:             withAlpha(hex(0xffffff), 0.08),
		theme.ColorNamePressed:           withAlpha(hex(0xffffff), 0.14),
		theme.ColorNamePlaceHolder:       hex(0x8e8e93),
		theme.ColorNameDisabled:          hex(0x636366),
		theme.ColorNameSeparator:         hex(0x38383a),
		theme.ColorNameOverlayBackground: hex(0x2c2c2e),
		theme.ColorNameScrollBar:         withAlpha(hex(0x8e8e93), 0.5),
		colorNameCard:                    hex(0x1c1c1e),
		colorNameCardEdge:                withAlpha(hex(0xffffff), 0.14),
	}
	if variant == theme.VariantLight {
		colors = palette{
			theme.ColorNameForeground:        hex(0x1c1c1e),
			theme.ColorNamePrimary:           hex(0x007aff),
			theme.ColorNameFocus:             withAlpha(hex(0x007aff), 0.5),
			theme.ColorNameSelection:         withAlpha(hex(0x007aff), 0.22),
			theme.ColorNameInputBackground:   hex(0xffffff),
			theme.ColorNameInputBorder:       hex(0xd1d1d6),
			theme.ColorNameButton:            hex(0xe5e5ea),
			theme.ColorNameHover:             withAlpha(hex(0x000000), 0.05),
			theme.ColorNamePressed:           withAlpha(hex(0x000000), 0.1),
			theme.ColorNamePlaceHolder:       hex(0x8e8e93),
			theme.ColorNameDisabled:          hex(0xaeaeb2),
			theme.ColorNameSeparator:         hex(0xd1d1d6),
			theme.ColorNameOverlayBackground: hex(0xffffff),
			theme.ColorNameScrollBar:         withAlpha(hex(0x8e8e93), 0.5),
			colorNameCard:                    hex(0xf2f2f7),
			colorNameCardEdge:                withAlpha(hex(0x000000), 0.1),
		}
	}
	colors[colorNameGlass] = withAlpha(colors[colorNameCard], 0.72)
	colors[colorNameFrost] = withAlpha(colors[colorNameCard], 0.9)
	return colors
}

// cardTheme is the app's theme under a card design's colours.
type cardTheme struct {
	base    fyne.Theme
	variant *fyne.ThemeVariant
	design  cardDesign
	colors  map[fyne.ThemeVariant]palette
}

func newCardTheme(app *appTheme, design cardDesign) *cardTheme {
	variant := design.variant
	if variant == nil {
		variant = app.variant
	}
	return &cardTheme{
		base:    newAppTheme(variant),
		variant: variant,
		design:  design,
		colors: map[fyne.ThemeVariant]palette{
			theme.VariantLight: design.colors(theme.VariantLight),
			theme.VariantDark:  design.colors(theme.VariantDark),
		},
	}
}

func (t *cardTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if t.variant != nil {
		variant = *t.variant
	}
	if c, ok := t.colors[variant][name]; ok {
		return c
	}
	return t.base.Color(name, variant)
}

func (t *cardTheme) Size(name fyne.ThemeSizeName) float32 {
	if t.design.rounded {
		switch name {
		case theme.SizeNameInputRadius:
			return 8
		case theme.SizeNameSelectionRadius:
			return 6
		case theme.SizeNameScrollBarRadius:
			return 4
		}
	}
	return t.base.Size(name)
}

func (t *cardTheme) Font(style fyne.TextStyle) fyne.Resource { return t.base.Font(style) }

func (t *cardTheme) Icon(name fyne.ThemeIconName) fyne.Resource { return t.base.Icon(name) }

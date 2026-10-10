package ui

import (
	"image/color"
	"math"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"github.com/paradoxe35/encre/internal/config"
)

// readable is WCAG AA for text; the card has no text large enough for less.
// Blurred glass trades some of it for clarity: the blur calms what shows through, and 3:1 still reads.
const (
	readable            = 4.5
	readableThroughBlur = 3
)

// ground is a colour text can land on and the contrast it needs there.
type ground struct {
	color color.NRGBA
	least float64
}

func TestEveryCardStyleKeepsItsTextReadableOnWhateverIsBehindIt(t *testing.T) {
	test.NewTempApp(t)
	texts := []fyne.ThemeColorName{theme.ColorNameForeground, theme.ColorNamePlaceHolder, theme.ColorNameHyperlink, theme.ColorNameError}

	for _, style := range config.CardStyles {
		design := designFor(style)
		for _, variant := range []fyne.ThemeVariant{theme.VariantDark, theme.VariantLight} {
			card := newCardTheme(newAppTheme(&variant), design)
			for _, ground := range surfaces(card, design, variant) {
				for _, name := range texts {
					text := over(card.Color(name, variant), ground.color)
					if ratio := contrast(text, ground.color); ratio < ground.least {
						t.Errorf("%s card (light %v): %s reads at %.2f:1 on %v, below %v:1",
							cardStyleLabels[style], variant == theme.VariantLight, name, ratio, ground.color, ground.least)
					}
				}
			}
		}
	}
}

// surfaces are every colour the card's text can land on: its fill, its glow, and for glass the lightest and darkest screens behind.
func surfaces(card *cardTheme, design cardDesign, variant fyne.ThemeVariant) []ground {
	var grounds []ground
	if fill := color.NRGBAModel.Convert(card.Color(colorNameCard, variant)).(color.NRGBA); fill.A > 0 {
		grounds = append(grounds, ground{over(fill, color.Black), readable})
	}
	if design.glass {
		for name, least := range map[fyne.ThemeColorName]float64{colorNameGlass: readableThroughBlur, colorNameFrost: readable} {
			fill := card.Color(name, variant)
			for _, screen := range []color.Color{color.White, color.Black} {
				grounds = append(grounds, ground{over(fill, screen), least}, ground{throughWindow(fill, screen), least})
			}
		}
	}
	if design.glow == nil {
		return grounds
	}

	var bases []color.NRGBA
	var blooms []color.Color
	for _, layer := range design.glow() {
		switch layer := layer.(type) {
		case *canvas.LinearGradient:
			bases = append(bases, over(layer.StartColor, color.Black), over(layer.EndColor, color.Black))
		case *canvas.Rectangle:
			bases = append(bases, over(layer.FillColor, color.Black))
		case *canvas.RadialGradient:
			blooms = append(blooms, layer.StartColor)
		}
	}
	for _, base := range bases {
		grounds = append(grounds, ground{base, readable})
		for _, glow := range blooms {
			grounds = append(grounds, ground{over(glow, base), readable})
		}
	}
	return grounds
}

func over(top, bottom color.Color) color.NRGBA {
	t := color.NRGBAModel.Convert(top).(color.NRGBA)
	b := color.NRGBAModel.Convert(bottom).(color.NRGBA)
	alpha := float64(t.A) / 255
	mix := func(front, back uint8) uint8 {
		return uint8(math.Round(float64(front)*alpha + float64(back)*(1-alpha)))
	}
	return color.NRGBA{R: mix(t.R, b.R), G: mix(t.G, b.G), B: mix(t.B, b.B), A: 255}
}

// throughWindow is a fill seen through the card's transparent window: blended with its own alpha first, then composited as premultiplied, so less of it covers the screen than over().
func throughWindow(fill, screen color.Color) color.NRGBA {
	f := color.NRGBAModel.Convert(fill).(color.NRGBA)
	s := color.NRGBAModel.Convert(screen).(color.NRGBA)
	alpha := float64(f.A) / 255
	mix := func(front, back uint8) uint8 {
		return uint8(math.Min(255, math.Round(float64(front)*alpha+float64(back)*(1-alpha*alpha))))
	}
	return color.NRGBA{R: mix(f.R, s.R), G: mix(f.G, s.G), B: mix(f.B, s.B), A: 255}
}

func contrast(a, b color.NRGBA) float64 {
	lighter, darker := luminance(a), luminance(b)
	if lighter < darker {
		lighter, darker = darker, lighter
	}
	return (lighter + 0.05) / (darker + 0.05)
}

func luminance(c color.NRGBA) float64 {
	channel := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.R) + 0.7152*channel(c.G) + 0.0722*channel(c.B)
}

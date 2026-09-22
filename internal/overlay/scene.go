package overlay

import (
	"image"
	"image/color"
	"math"

	"golang.org/x/image/vector"
)

// Logical size of the pill; frames are painted at this times the display scale.
const (
	Width  = 220
	Height = 56
)

const (
	bars = 7
	// Bars fan out from the centre: the newest level in the middle, older ones towards the edges.
	traceLen  = (bars + 1) / 2
	barWidth  = 4.0
	barGap    = 6.0
	barMin    = 6.0
	barMax    = Height * 0.55
	pillInset = 1.0
	// Cubic control distance that makes a quarter circle out of a bezier.
	kappa = 0.5523
)

// Frame is everything a picture of the indicator depends on.
type Frame struct {
	Phase Phase
	// Seconds since the indicator appeared.
	T float64
	// Recent smoothed microphone levels, 0 to 1, newest last.
	Trace [traceLen]float32
	// Fade, 0 to 1.
	Alpha float64
}

// Paint draws the frame into dst, which must be Width*scale by Height*scale.
func Paint(dst *image.RGBA, f Frame, scale float64) {
	clear(dst.Pix)
	if f.Alpha <= 0 {
		return
	}

	bounds := dst.Bounds()
	z := vector.NewRasterizer(bounds.Dx(), bounds.Dy())

	w := float64(bounds.Dx())
	h := float64(bounds.Dy())
	inset := pillInset * scale
	roundedRect(z, inset, inset, w-2*inset, h-2*inset, (h-2*inset)/2)
	z.Draw(dst, bounds, image.NewUniform(shade(22, 22, 26, 0.88*f.Alpha)), image.Point{})

	span := (bars*barWidth + (bars-1)*barGap) * scale
	left := (w - span) / 2
	ink := image.NewUniform(shade(245, 245, 250, 0.92*f.Alpha))
	for i := range bars {
		height := barHeight(f, i) * scale
		x := left + float64(i)*(barWidth+barGap)*scale
		y := (h - height) / 2
		z.Reset(bounds.Dx(), bounds.Dy())
		roundedRect(z, x, y, barWidth*scale, height, barWidth*scale/2)
		z.Draw(dst, bounds, ink, image.Point{})
	}
}

// Listening bars replay the last few levels outward from the centre, so the
// shape waves with the voice; working bars ripple so a wait reads as progress.
func barHeight(f Frame, i int) float64 {
	switch f.Phase {
	case Working:
		return barMin + (barMax-barMin)*0.5*(1+math.Sin(f.T*4-float64(i)*0.9))
	default:
		age := i - bars/2
		if age < 0 {
			age = -age
		}
		level := float64(f.Trace[traceLen-1-age])
		wobble := 0.85 + 0.15*math.Sin(f.T*6+float64(age)*0.9)
		return barMin + (barMax-barMin)*level*wobble
	}
}

func roundedRect(z *vector.Rasterizer, x, y, w, h, r float64) {
	r = math.Min(r, math.Min(w, h)/2)
	c := r * kappa
	fx, fy, fw, fh, fr, fc := float32(x), float32(y), float32(w), float32(h), float32(r), float32(c)

	z.MoveTo(fx+fr, fy)
	z.LineTo(fx+fw-fr, fy)
	z.CubeTo(fx+fw-fr+fc, fy, fx+fw, fy+fr-fc, fx+fw, fy+fr)
	z.LineTo(fx+fw, fy+fh-fr)
	z.CubeTo(fx+fw, fy+fh-fr+fc, fx+fw-fr+fc, fy+fh, fx+fw-fr, fy+fh)
	z.LineTo(fx+fr, fy+fh)
	z.CubeTo(fx+fr-fc, fy+fh, fx, fy+fh-fr+fc, fx, fy+fh-fr)
	z.LineTo(fx, fy+fr)
	z.CubeTo(fx, fy+fr-fc, fx+fr-fc, fy, fx+fr, fy)
	z.ClosePath()
}

func shade(r, g, b uint8, alpha float64) color.NRGBA {
	return color.NRGBA{R: r, G: g, B: b, A: uint8(math.Round(255 * clamp01(alpha)))}
}

func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

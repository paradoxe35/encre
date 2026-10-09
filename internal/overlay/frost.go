package overlay

import (
	"image"
	"image/color"
)

const (
	// frostShrink averages the capture down before blurring: the blur is cheap at that size, and the
	// window scales the result smoothly back up.
	frostShrink = 8
	frostRadius = 2
	frostPasses = 3
)

// Frosted is the screen inside area, blurred, and the part of area it covers, in screen pixels.
func Frosted(area image.Rectangle) (image.Image, image.Rectangle, bool) {
	shot := capture(area)
	if shot == nil || shot.Bounds().Empty() {
		return nil, image.Rectangle{}, false
	}
	return frost(shot), shot.Bounds(), true
}

// Box-blurring a few times approaches a Gaussian blur.
func frost(src *image.RGBA) *image.RGBA {
	bounds := src.Bounds()
	small := image.NewRGBA(image.Rect(0, 0, max((bounds.Dx()+frostShrink-1)/frostShrink, 1), max((bounds.Dy()+frostShrink-1)/frostShrink, 1)))
	for y := range small.Rect.Dy() {
		for x := range small.Rect.Dx() {
			block := image.Rect(x*frostShrink, y*frostShrink, (x+1)*frostShrink, (y+1)*frostShrink).Add(bounds.Min).Intersect(bounds)
			small.SetRGBA(x, y, average(src, block))
		}
	}
	for range frostPasses {
		small = boxBlur(small, true)
		small = boxBlur(small, false)
	}
	return small
}

func average(src *image.RGBA, block image.Rectangle) color.RGBA {
	var r, g, b, n int
	for y := block.Min.Y; y < block.Max.Y; y++ {
		for x := block.Min.X; x < block.Max.X; x++ {
			c := src.RGBAAt(x, y)
			r, g, b, n = r+int(c.R), g+int(c.G), b+int(c.B), n+1
		}
	}
	return color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: 0xff}
}

// boxBlur repeats the edge pixel past the edge.
func boxBlur(src *image.RGBA, horizontal bool) *image.RGBA {
	w, h := src.Rect.Dx(), src.Rect.Dy()
	dst := image.NewRGBA(src.Rect)
	for y := range h {
		for x := range w {
			var r, g, b int
			for d := -frostRadius; d <= frostRadius; d++ {
				sx, sy := x, y
				if horizontal {
					sx = min(max(x+d, 0), w-1)
				} else {
					sy = min(max(y+d, 0), h-1)
				}
				c := src.RGBAAt(sx, sy)
				r, g, b = r+int(c.R), g+int(c.G), b+int(c.B)
			}
			n := 2*frostRadius + 1
			dst.SetRGBA(x, y, color.RGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: 0xff})
		}
	}
	return dst
}

package overlay

import (
	"image"
	"image/color"
	"testing"
)

func TestFrostKeepsAnEvenColourAndShrinks(t *testing.T) {
	src := image.NewRGBA(image.Rect(100, 50, 180, 90))
	for y := 50; y < 90; y++ {
		for x := 100; x < 180; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 200, G: 40, B: 90, A: 255})
		}
	}
	got := frost(src)
	if got.Rect.Dx() != 10 || got.Rect.Dy() != 5 {
		t.Fatalf("frosted to %v, want an eighth of 80x40", got.Rect)
	}
	if c := got.RGBAAt(4, 2); c != (color.RGBA{R: 200, G: 40, B: 90, A: 255}) {
		t.Fatalf("an even colour became %v", c)
	}
}

func TestFrostSoftensAnEdge(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 160, 16))
	for y := range 16 {
		for x := 80; x < 160; x++ {
			src.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	got := frost(src)
	left, right := got.RGBAAt(9, 1).R, got.RGBAAt(10, 1).R
	if left == 0 || right == 255 || left >= right {
		t.Fatalf("the edge stayed sharp: %d then %d", left, right)
	}
}

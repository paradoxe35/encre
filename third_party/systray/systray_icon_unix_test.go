//go:build linux || freebsd || openbsd || netbsd

package systray

import (
	"image"
	"image/color"
	"testing"
)

func TestArgbForImageKeepsStraightAlphaColours(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 0x3b, G: 0x82, B: 0xf6, A: 0xff})
	img.SetNRGBA(1, 0, color.NRGBA{R: 0x3b, G: 0x82, B: 0xf6, A: 0x44})

	got := argbForImage(img)
	want := []byte{0xff, 0x3b, 0x82, 0xf6, 0x44, 0x3b, 0x82, 0xf6}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argb = % x, want % x: a translucent edge pixel must keep its colour", got, want)
		}
	}
}

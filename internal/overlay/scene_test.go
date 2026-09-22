package overlay

import (
	"bytes"
	"image"
	"testing"
)

func frame(scale float64) *image.RGBA {
	return image.NewRGBA(image.Rect(0, 0, int(Width*scale), int(Height*scale)))
}

func flat(level float32) [traceLen]float32 {
	var trace [traceLen]float32
	for i := range trace {
		trace[i] = level
	}
	return trace
}

// Bright pixels in the column of bar i, which is how tall it was drawn.
func barPixels(img *image.RGBA, i int) int {
	span := bars*barWidth + (bars-1)*barGap
	x := int((Width-span)/2 + float64(i)*(barWidth+barGap) + barWidth/2)
	n := 0
	for y := 0; y < Height; y++ {
		off := img.PixOffset(x, y)
		if img.Pix[off] > 200 && img.Pix[off+3] > 200 {
			n++
		}
	}
	return n
}

func alphaAt(img *image.RGBA, x, y int) uint8 {
	return img.Pix[img.PixOffset(x, y)+3]
}

// Bright pixels are the bars; the pill behind them is dark.
func brightPixels(img *image.RGBA) int {
	n := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] > 200 && img.Pix[i+3] > 200 {
			n++
		}
	}
	return n
}

func TestTheCornersStayTransparentAndTheMiddleIsPainted(t *testing.T) {
	img := frame(1)
	Paint(img, Frame{Phase: Listening, Trace: flat(0.5), Alpha: 1}, 1)

	for _, p := range [][2]int{{0, 0}, {Width - 1, 0}, {0, Height - 1}, {Width - 1, Height - 1}} {
		if a := alphaAt(img, p[0], p[1]); a != 0 {
			t.Errorf("corner %v has alpha %d, want transparent", p, a)
		}
	}
	if a := alphaAt(img, 20, Height/2); a < 200 {
		t.Errorf("pill edge has alpha %d, want opaque", a)
	}
}

func TestAlphaZeroPaintsNothing(t *testing.T) {
	img := frame(1)
	Paint(img, Frame{Phase: Listening, Trace: flat(1), Alpha: 1}, 1)
	Paint(img, Frame{Phase: Listening, Trace: flat(1), Alpha: 0}, 1)
	if !bytes.Equal(img.Pix, make([]byte, len(img.Pix))) {
		t.Fatal("a faded-out frame left pixels behind")
	}
}

func TestFadeScalesTheWholePicture(t *testing.T) {
	full, half := frame(1), frame(1)
	Paint(full, Frame{Phase: Listening, Trace: flat(0.5), Alpha: 1}, 1)
	Paint(half, Frame{Phase: Listening, Trace: flat(0.5), Alpha: 0.5}, 1)

	x, y := 20, Height/2
	if a, b := alphaAt(full, x, y), alphaAt(half, x, y); b >= a || b < a/3 {
		t.Fatalf("half fade alpha %d against full %d", b, a)
	}
}

func TestLouderSpeechMeansTallerBars(t *testing.T) {
	quiet, loud := frame(1), frame(1)
	Paint(quiet, Frame{Phase: Listening, Trace: flat(0.1), Alpha: 1}, 1)
	Paint(loud, Frame{Phase: Listening, Trace: flat(0.9), Alpha: 1}, 1)

	if q, l := brightPixels(quiet), brightPixels(loud); l <= q {
		t.Fatalf("loud bars cover %d pixels, quiet %d", l, q)
	}
}

func TestTranscribingKeepsMovingWithoutAnyLevel(t *testing.T) {
	a, b := frame(1), frame(1)
	Paint(a, Frame{Phase: Transcribing, T: 0, Alpha: 1}, 1)
	Paint(b, Frame{Phase: Transcribing, T: 0.3, Alpha: 1}, 1)
	if bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("the transcribing animation did not change between frames")
	}
	if brightPixels(a) == 0 {
		t.Fatal("no bars drawn while transcribing")
	}
}

func TestTheSameFrameIsPaintedTheSameWay(t *testing.T) {
	a, b := frame(1), frame(1)
	f := Frame{Phase: Listening, T: 1.25, Trace: flat(0.4), Alpha: 0.8}
	Paint(a, f, 1)
	Paint(b, f, 1)
	if !bytes.Equal(a.Pix, b.Pix) {
		t.Fatal("painting is not deterministic")
	}
}

func TestAScaledFrameFillsItsSize(t *testing.T) {
	img := frame(2)
	Paint(img, Frame{Phase: Listening, Trace: flat(0.5), Alpha: 1}, 2)

	if a := alphaAt(img, 40, Height); a < 200 {
		t.Errorf("scaled pill edge has alpha %d", a)
	}
	if a := alphaAt(img, 2*Width-1, 2*Height-1); a != 0 {
		t.Errorf("scaled corner has alpha %d", a)
	}
}

func TestTheNewestLevelSitsInTheMiddleAndOlderOnesAtTheEdges(t *testing.T) {
	var newest, oldest [traceLen]float32
	newest[traceLen-1] = 1
	oldest[0] = 1

	img := frame(1)
	Paint(img, Frame{Phase: Listening, Trace: newest, Alpha: 1}, 1)
	if c, e := barPixels(img, bars/2), barPixels(img, 0); c <= e {
		t.Fatalf("centre bar %d px, edge bar %d px: the newest level should drive the centre", c, e)
	}

	Paint(img, Frame{Phase: Listening, Trace: oldest, Alpha: 1}, 1)
	if c, e := barPixels(img, bars/2), barPixels(img, 0); e <= c {
		t.Fatalf("centre bar %d px, edge bar %d px: the oldest level should drive the edges", c, e)
	}
	if l, r := barPixels(img, 0), barPixels(img, bars-1); l != r {
		t.Fatalf("left %d px and right %d px: the shape should be symmetric", l, r)
	}
}

func TestThinkingShowsItsOwnMovingPicture(t *testing.T) {
	thinking, transcribing := frame(1), frame(1)
	Paint(thinking, Frame{Phase: Thinking, T: 0.2, Alpha: 1}, 1)
	Paint(transcribing, Frame{Phase: Transcribing, T: 0.2, Alpha: 1}, 1)
	if bytes.Equal(thinking.Pix, transcribing.Pix) {
		t.Fatal("thinking looks the same as transcribing")
	}
	if brightPixels(thinking) == 0 {
		t.Fatal("no dots drawn while thinking")
	}

	later := frame(1)
	Paint(later, Frame{Phase: Thinking, T: 0.5, Alpha: 1}, 1)
	if bytes.Equal(thinking.Pix, later.Pix) {
		t.Fatal("the thinking animation did not change between frames")
	}
	if a := alphaAt(later, 0, 0); a != 0 {
		t.Fatalf("thinking corner has alpha %d, want transparent", a)
	}
}

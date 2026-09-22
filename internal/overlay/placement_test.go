package overlay

import (
	"image"
	"testing"
)

func twoScreens() []screen {
	primary := image.Rect(0, 0, 1920, 1080)
	left := image.Rect(-1440, -200, 0, 700)
	return []screen{
		{bounds: primary, workarea: image.Rect(0, 0, 1920, 1040)},
		{bounds: left, workarea: image.Rect(-1440, -200, 0, 700)},
	}
}

func TestTheScreenUnderTheFocusIsChosen(t *testing.T) {
	screens := twoScreens()
	if got := workareaFor(screens, image.Pt(-700, 300), true); got != screens[1].workarea {
		t.Fatalf("focus on the left screen chose %v", got)
	}
	if got := workareaFor(screens, image.Pt(100, 100), true); got != screens[0].workarea {
		t.Fatalf("focus on the primary chose %v", got)
	}
}

func TestAnUnknownOrStrayFocusFallsBackToThePrimary(t *testing.T) {
	screens := twoScreens()
	if got := workareaFor(screens, image.Pt(-700, 300), false); got != screens[0].workarea {
		t.Fatalf("unknown focus chose %v", got)
	}
	if got := workareaFor(screens, image.Pt(5000, 5000), true); got != screens[0].workarea {
		t.Fatalf("focus outside every screen chose %v", got)
	}
}

func TestNoScreensYieldsAnEmptyArea(t *testing.T) {
	if got := workareaFor(nil, image.Pt(0, 0), true); got != (image.Rectangle{}) {
		t.Fatalf("no screens chose %v", got)
	}
}

func TestThePillSitsCentredAboveTheBottomEdge(t *testing.T) {
	area := image.Rect(-1440, -200, 0, 700)
	got := pillOrigin(area, Width, Height)
	want := image.Pt(-1440+(1440-Width)/2, 700-Height-bottomMargin)
	if got != want {
		t.Fatalf("origin %v, want %v", got, want)
	}
}

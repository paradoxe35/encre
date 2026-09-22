package overlay

import "image"

// screen is one monitor in the virtual desktop: its full bounds and the work area
// left once panels are excluded. The first screen is the primary.
type screen struct {
	bounds   image.Rectangle
	workarea image.Rectangle
}

// workareaFor picks the screen holding the point the user is working at, or the
// primary when the point is unknown or falls between screens.
func workareaFor(screens []screen, focus image.Point, known bool) image.Rectangle {
	if len(screens) == 0 {
		return image.Rectangle{}
	}
	if known {
		for _, s := range screens {
			if focus.In(s.bounds) {
				return s.workarea
			}
		}
	}
	return screens[0].workarea
}

// pillOrigin centres a pill of the given size at the bottom of the work area.
func pillOrigin(workarea image.Rectangle, width, height int) image.Point {
	return image.Point{
		X: workarea.Min.X + (workarea.Dx()-width)/2,
		Y: workarea.Max.Y - height - bottomMargin,
	}
}

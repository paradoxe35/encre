package overlay

import "image"

// The work area excludes panels; the first screen is the primary.
type screen struct {
	bounds   image.Rectangle
	workarea image.Rectangle
}

// Falls back to the primary when the point is unknown or between screens.
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

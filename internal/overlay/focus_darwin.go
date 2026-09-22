package overlay

/*
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices
void encre_overlay_no_focus(void* window);
int encre_overlay_focus_point(int* x, int* y);
*/
import "C"

import (
	"image"

	"github.com/go-gl/glfw/v3.4/glfw"
)

// Centre of the focused window through accessibility, which hotkeys already
// require, else the pointer. Both are in the top-left coordinates GLFW uses.
func focusPoint() (image.Point, bool) {
	var x, y C.int
	if C.encre_overlay_focus_point(&x, &y) == 0 {
		return image.Point{}, false
	}
	return image.Pt(int(x), int(y)), true
}

// A window that refuses to become key, at status level and on every space, so the
// app the user was typing in keeps the keyboard.
func noFocus(window *glfw.Window) {
	C.encre_overlay_no_focus(window.GetCocoaWindow())
}

package overlay

/*
#cgo LDFLAGS: -framework Cocoa -framework ApplicationServices
#include <stdint.h>
void encre_overlay_no_focus(void* window);
int encre_overlay_focus_point(int* x, int* y);
void encre_overlay_panel(uintptr_t window, int x, int y, int radius, int glass);
void encre_overlay_opacity(uintptr_t window, double opacity);
int encre_overlay_backdrop(void);
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

// Panel makes a focusable window float like the indicator, rounded and placed in the frame.
func Panel(window uintptr, frame image.Rectangle, look Look) {
	if window == 0 {
		return
	}
	glass := 0
	if look.Glass {
		glass = 1
	}
	C.encre_overlay_panel(C.uintptr_t(window), C.int(frame.Min.X), C.int(frame.Min.Y), C.int(look.Radius), C.int(glass))
}

// Corner is the radius asked for: macOS rounds the window to it.
func Corner(radius float32) float32 { return radius }

// GlassBackdrop is blurred, unless the user asked macOS to reduce transparency.
func GlassBackdrop() Backdrop {
	return Backdrop(C.encre_overlay_backdrop())
}

// The blur macOS draws makes reading the screen, and the permission it needs, unnecessary.
func capture(image.Rectangle) *image.RGBA { return nil }

// SetOpacity fades the whole window; 1 is opaque.
func SetOpacity(window uintptr, opacity float64) {
	if window == 0 {
		return
	}
	C.encre_overlay_opacity(C.uintptr_t(window), C.double(clamp01(opacity)))
}

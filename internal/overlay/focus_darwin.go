package overlay

/*
#cgo LDFLAGS: -framework Cocoa
void encre_overlay_no_focus(void* window);
*/
import "C"

import "github.com/go-gl/glfw/v3.4/glfw"

// A floating panel that ignores the mouse can never become key, so the app the
// user was typing in keeps the keyboard.
func noFocus(window *glfw.Window) {
	C.encre_overlay_no_focus(window.GetCocoaWindow())
}

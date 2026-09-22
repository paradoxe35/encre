//go:build linux && !wayland

package overlay

/*
#cgo LDFLAGS: -lX11
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/Xatom.h>

// The window manager, not GLFW, decides who gets focus when a window appears. A
// window that declares the "no input" model, offers no WM_TAKE_FOCUS, and calls
// itself a notification is one every manager leaves alone.
static void encre_overlay_no_focus(Display* display, Window window) {
    XWMHints hints;
    hints.flags = InputHint;
    hints.input = False;
    XSetWMHints(display, window, &hints);

    Atom del = XInternAtom(display, "WM_DELETE_WINDOW", False);
    XSetWMProtocols(display, window, &del, 1);

    Atom type = XInternAtom(display, "_NET_WM_WINDOW_TYPE", False);
    Atom notification = XInternAtom(display, "_NET_WM_WINDOW_TYPE_NOTIFICATION", False);
    XChangeProperty(display, window, type, XA_ATOM, 32, PropModeReplace,
                    (unsigned char*)&notification, 1);

    Atom state = XInternAtom(display, "_NET_WM_STATE", False);
    Atom states[3] = {
        XInternAtom(display, "_NET_WM_STATE_ABOVE", False),
        XInternAtom(display, "_NET_WM_STATE_SKIP_TASKBAR", False),
        XInternAtom(display, "_NET_WM_STATE_SKIP_PAGER", False),
    };
    XChangeProperty(display, window, state, XA_ATOM, 32, PropModeReplace,
                    (unsigned char*)states, 3);
    XFlush(display);
}
*/
import "C"

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
)

func noFocus(window *glfw.Window) {
	if glfw.GetPlatform() != glfw.PlatformX11 {
		return
	}
	display := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
	C.encre_overlay_no_focus(display, C.Window(window.GetX11Window()))
}

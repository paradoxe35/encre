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

// The active window can vanish between two calls; Xlib's default handler would
// then end the whole process, so errors are swallowed while we look.
static int encre_overlay_locate(Display* display, int* x, int* y);

static int encre_overlay_ignore_error(Display* display, XErrorEvent* error) {
    return 0;
}

// Centre of the active window in root coordinates, else the pointer; 0 if neither is known.
static int encre_overlay_focus_point(Display* display, int* x, int* y) {
    XSync(display, False);
    XErrorHandler previous = XSetErrorHandler(encre_overlay_ignore_error);
    int found = encre_overlay_locate(display, x, y);
    XSync(display, False);
    XSetErrorHandler(previous);
    return found;
}

static int encre_overlay_locate(Display* display, int* x, int* y) {
    Window root = DefaultRootWindow(display);
    Atom active = XInternAtom(display, "_NET_ACTIVE_WINDOW", True);
    if (active != None) {
        Atom type;
        int format;
        unsigned long count, rest;
        unsigned char* data = NULL;
        if (XGetWindowProperty(display, root, active, 0, 1, False, XA_WINDOW, &type, &format,
                               &count, &rest, &data) == Success && data != NULL) {
            Window focused = *(Window*)data;
            XFree(data);
            XWindowAttributes attributes;
            if (focused != None && XGetWindowAttributes(display, focused, &attributes)) {
                int rx, ry;
                Window child;
                XTranslateCoordinates(display, focused, root, 0, 0, &rx, &ry, &child);
                *x = rx + attributes.width / 2;
                *y = ry + attributes.height / 2;
                return 1;
            }
        }
    }
    Window child;
    int wx, wy;
    unsigned int mask;
    if (XQueryPointer(display, root, &root, &child, x, y, &wx, &wy, &mask)) {
        return 1;
    }
    return 0;
}
*/
import "C"

import (
	"image"
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
)

func focusPoint() (image.Point, bool) {
	if glfw.GetPlatform() != glfw.PlatformX11 {
		return image.Point{}, false
	}
	display := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
	var x, y C.int
	if C.encre_overlay_focus_point(display, &x, &y) == 0 {
		return image.Point{}, false
	}
	return image.Pt(int(x), int(y)), true
}

func noFocus(window *glfw.Window) {
	if glfw.GetPlatform() != glfw.PlatformX11 {
		return
	}
	display := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
	C.encre_overlay_no_focus(display, C.Window(window.GetX11Window()))
}

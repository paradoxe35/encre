//go:build linux && !wayland

package overlay

/*
#cgo LDFLAGS: -lX11 -lXext
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/Xatom.h>
#include <X11/extensions/shape.h>

// The window manager, not GLFW, decides who gets focus when a window appears. A
// window that declares the "no input" model and calls itself a notification is
// one every manager leaves alone.
static void encre_overlay_no_focus(Display* display, Window window) {
    XWMHints hints;
    hints.flags = InputHint;
    hints.input = False;
    XSetWMHints(display, window, &hints);

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

// Set for the next map and requested for the current one; written whole, so it keeps GLFW's floating.
static void encre_overlay_panel_state(Display* display, Window window) {
    Atom state = XInternAtom(display, "_NET_WM_STATE", False);
    Atom states[3] = {
        XInternAtom(display, "_NET_WM_STATE_SKIP_TASKBAR", False),
        XInternAtom(display, "_NET_WM_STATE_SKIP_PAGER", False),
        XInternAtom(display, "_NET_WM_STATE_ABOVE", False),
    };
    XChangeProperty(display, window, state, XA_ATOM, 32, PropModeReplace, (unsigned char*)states, 3);

    for (int i = 0; i < 3; i += 2) {
        XEvent event = {0};
        event.xclient.type = ClientMessage;
        event.xclient.window = window;
        event.xclient.message_type = state;
        event.xclient.format = 32;
        event.xclient.data.l[0] = 1;
        event.xclient.data.l[1] = states[i];
        event.xclient.data.l[2] = i + 1 < 3 ? states[i + 1] : 0;
        event.xclient.data.l[3] = 1;
        XSendEvent(display, DefaultRootWindow(display), False,
                   SubstructureRedirectMask | SubstructureNotifyMask, &event);
    }
    XFlush(display);
}

static void encre_overlay_round(Display* display, Window window, int width, int height, int radius) {
    Pixmap mask = XCreatePixmap(display, window, width, height, 1);
    GC gc = XCreateGC(display, mask, 0, NULL);
    int d = 2 * radius;

    XSetForeground(display, gc, 0);
    XFillRectangle(display, mask, gc, 0, 0, width, height);
    XSetForeground(display, gc, 1);
    XFillRectangle(display, mask, gc, radius, 0, width - d, height);
    XFillRectangle(display, mask, gc, 0, radius, width, height - d);
    XFillArc(display, mask, gc, 0, 0, d, d, 0, 360 * 64);
    XFillArc(display, mask, gc, width - d, 0, d, d, 0, 360 * 64);
    XFillArc(display, mask, gc, 0, height - d, d, d, 0, 360 * 64);
    XFillArc(display, mask, gc, width - d, height - d, d, d, 0, 360 * 64);

    XShapeCombineMask(display, window, ShapeBounding, 0, 0, mask, ShapeSet);
    XFreeGC(display, gc);
    XFreePixmap(display, mask);
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
            Window focused = (type == XA_WINDOW && format == 32 && count >= 1) ? *(Window*)data : None;
            XFree(data);
            XWindowAttributes attributes;
            int rx, ry;
            Window child;
            if (focused != None && XGetWindowAttributes(display, focused, &attributes)
                && XTranslateCoordinates(display, focused, root, 0, 0, &rx, &ry, &child)) {
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

// Panel makes a focusable window float like the indicator, rounded and placed in the frame.
func Panel(window uintptr, frame image.Rectangle, radius int) {
	if window == 0 || glfw.GetPlatform() != glfw.PlatformX11 {
		return
	}
	display := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
	C.encre_overlay_panel_state(display, C.Window(window))
	C.XMoveWindow(display, C.Window(window), C.int(frame.Min.X), C.int(frame.Min.Y))
	C.encre_overlay_round(display, C.Window(window), C.int(frame.Dx()), C.int(frame.Dy()), C.int(radius))
	C.XFlush(display)
}

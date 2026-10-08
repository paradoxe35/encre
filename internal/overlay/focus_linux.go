//go:build linux && !wayland

package overlay

/*
#cgo LDFLAGS: -lX11 -lXext
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/Xatom.h>
#include <X11/extensions/shape.h>
#include <stdio.h>
#include <stdlib.h>

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

// A compositor is running when one owns this screen's selection; without one, transparency shows black.
static int encre_overlay_compositing(Display* display) {
    char name[32];
    snprintf(name, sizeof(name), "_NET_WM_CM_S%d", DefaultScreen(display));
    return XGetSelectionOwner(display, XInternAtom(display, name, False)) != None;
}

// KWin blurs behind a window that asks; an empty region means the whole window.
static void encre_overlay_blur(Display* display, Window window, int on) {
    Atom blur = XInternAtom(display, "_KDE_NET_WM_BLUR_BEHIND_REGION", False);
    if (on) {
        XChangeProperty(display, window, blur, XA_CARDINAL, 32, PropModeReplace, NULL, 0);
    } else {
        XDeleteProperty(display, window, blur);
    }
}

// KWin's blur effect, while on, announces itself as a property of the root window.
static int encre_overlay_blurs(Display* display) {
    Atom blur = XInternAtom(display, "_KDE_NET_WM_BLUR_BEHIND_REGION", True);
    if (blur == None) {
        return 0;
    }
    int count = 0, found = 0;
    Atom* properties = XListProperties(display, DefaultRootWindow(display), &count);
    for (int i = 0; i < count; i++) {
        found |= properties[i] == blur;
    }
    if (properties != NULL) {
        XFree(properties);
    }
    return found;
}

static void encre_overlay_unshape(Display* display, Window window) {
    XShapeCombineMask(display, window, ShapeBounding, 0, 0, None, ShapeSet);
}

static void encre_overlay_opacity(Display* display, Window window, unsigned long opacity) {
    Atom property = XInternAtom(display, "_NET_WM_WINDOW_OPACITY", False);
    if (opacity == 0xffffffffUL) {
        XDeleteProperty(display, window, property);
    } else {
        XChangeProperty(display, window, property, XA_CARDINAL, 32, PropModeReplace, (unsigned char*)&opacity, 1);
    }
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
// Copies the screen inside the rectangle as RGBA, after clipping it to the root window, which
// XGetImage requires. The clipped rectangle is written back; 0 if nothing could be read.
static int encre_overlay_capture(Display* display, int* x, int* y, int* width, int* height, unsigned char** out) {
    Window root = DefaultRootWindow(display);
    int left = *x < 0 ? 0 : *x, top = *y < 0 ? 0 : *y;
    int right = *x + *width, bottom = *y + *height;
    int screenWidth = DisplayWidth(display, DefaultScreen(display));
    int screenHeight = DisplayHeight(display, DefaultScreen(display));
    if (right > screenWidth) right = screenWidth;
    if (bottom > screenHeight) bottom = screenHeight;
    if (right <= left || bottom <= top) {
        return 0;
    }

    XSync(display, False);
    XErrorHandler previous = XSetErrorHandler(encre_overlay_ignore_error);
    XImage* image = XGetImage(display, root, left, top, right - left, bottom - top, AllPlanes, ZPixmap);
    XSync(display, False);
    XSetErrorHandler(previous);
    if (image == NULL) {
        return 0;
    }
    if (image->bits_per_pixel != 32) {
        XDestroyImage(image);
        return 0;
    }

    int shifts[3];
    unsigned long masks[3] = {image->red_mask, image->green_mask, image->blue_mask};
    for (int c = 0; c < 3; c++) {
        shifts[c] = 0;
        while (masks[c] != 0 && !((masks[c] >> shifts[c]) & 1)) {
            shifts[c]++;
        }
    }
    int w = right - left, h = bottom - top;
    unsigned char* pixels = malloc((size_t)w * h * 4);
    if (pixels == NULL) {
        XDestroyImage(image);
        return 0;
    }
    for (int row = 0; row < h; row++) {
        unsigned int* line = (unsigned int*)(image->data + row * image->bytes_per_line);
        for (int col = 0; col < w; col++) {
            unsigned long p = line[col];
            unsigned char* o = pixels + ((size_t)row * w + col) * 4;
            for (int c = 0; c < 3; c++) {
                o[c] = (p & masks[c]) >> shifts[c];
            }
            o[3] = 0xff;
        }
    }
    XDestroyImage(image);
    *x = left, *y = top, *width = w, *height = h;
    *out = pixels;
    return 1;
}
*/
import "C"

import (
	"image"
	"math"
	"os"
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
// A glass panel draws its own smooth corners once a compositor shows its transparency; otherwise
// the corners are cut from the window.
func Panel(window uintptr, frame image.Rectangle, look Look) {
	if window == 0 || glfw.GetPlatform() != glfw.PlatformX11 {
		return
	}
	display := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
	handle := C.Window(window)
	C.encre_overlay_panel_state(display, handle)
	C.XMoveWindow(display, handle, C.int(frame.Min.X), C.int(frame.Min.Y))
	// A blur behind the window would show past smooth corners, so only plain glass goes unshaped.
	if look.Glass && C.encre_overlay_compositing(display) != 0 && C.encre_overlay_blurs(display) == 0 {
		C.encre_overlay_unshape(display, handle)
	} else {
		C.encre_overlay_round(display, handle, C.int(frame.Dx()), C.int(frame.Dy()), C.int(look.Radius))
	}
	C.encre_overlay_blur(display, handle, boolInt(look.Glass))
	C.XFlush(display)
}

// Corner is the radius a panel's corners are cut to, which is the one asked for.
func Corner(radius float32) float32 { return radius }

// GlassBackdrop is KWin's blur where it offers one, and otherwise the screen frosted by us. Under
// XWayland the screen of other apps cannot be read, so glass there is only see-through.
func GlassBackdrop() Backdrop {
	if glfw.GetPlatform() != glfw.PlatformX11 {
		return BackdropNone
	}
	display := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
	compositing := C.encre_overlay_compositing(display) != 0
	switch {
	case compositing && C.encre_overlay_blurs(display) != 0:
		return BackdropBlurred
	case os.Getenv("WAYLAND_DISPLAY") == "":
		return BackdropFrosted
	case compositing:
		return BackdropSharp
	}
	return BackdropNone
}

func capture(area image.Rectangle) *image.RGBA {
	if glfw.GetPlatform() != glfw.PlatformX11 {
		return nil
	}
	display := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
	x, y, width, height := C.int(area.Min.X), C.int(area.Min.Y), C.int(area.Dx()), C.int(area.Dy())
	var pixels *C.uchar
	if C.encre_overlay_capture(display, &x, &y, &width, &height, &pixels) == 0 {
		return nil
	}
	defer C.free(unsafe.Pointer(pixels))
	shot := image.NewRGBA(image.Rect(int(x), int(y), int(x+width), int(y+height)))
	copy(shot.Pix, unsafe.Slice((*byte)(unsafe.Pointer(pixels)), len(shot.Pix)))
	return shot
}

// SetOpacity fades the whole window, where a compositor runs; 1 is opaque.
func SetOpacity(window uintptr, opacity float64) {
	if window == 0 || glfw.GetPlatform() != glfw.PlatformX11 {
		return
	}
	display := (*C.Display)(unsafe.Pointer(glfw.GetX11Display()))
	C.encre_overlay_opacity(display, C.Window(window), C.ulong(math.Round(clamp01(opacity)*0xffffffff)))
}

func boolInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}

package overlay

import (
	"image"
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
	"golang.org/x/sys/windows"
)

const (
	// GWL_EXSTYLE is -20; written as the register value SetWindowLongPtrW reads.
	gwlExStyle     = ^uintptr(19)
	wsExToolWindow = 0x00000080
	wsExAppWindow  = 0x00040000
	wsExNoActivate = 0x08000000

	swpNoSize       = 0x0001
	swpNoMove       = 0x0002
	swpNoZOrder     = 0x0004
	swpNoActivate   = 0x0010
	swpFrameChanged = 0x0020

	swHide = 0
	swShow = 5

	dwmwaWindowCornerPreference = 33
	dwmwcpRound                 = 2
)

var (
	user32              = windows.NewLazySystemDLL("user32.dll")
	getWindowLongPtrW   = user32.NewProc("GetWindowLongPtrW")
	setWindowLongPtrW   = user32.NewProc("SetWindowLongPtrW")
	setWindowPos        = user32.NewProc("SetWindowPos")
	getForegroundWindow = user32.NewProc("GetForegroundWindow")
	getWindowRect       = user32.NewProc("GetWindowRect")
	getCursorPos        = user32.NewProc("GetCursorPos")
	showWindow          = user32.NewProc("ShowWindow")
	isWindowVisible     = user32.NewProc("IsWindowVisible")

	dwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
)

type winRect struct{ left, top, right, bottom int32 }

type winPoint struct{ x, y int32 }

// A no-activate tool window is never brought to the foreground and never appears
// in the taskbar; GLFW's app-window style would put it there, so it goes. The
// frame-changed call is what makes a style change stick.
func noFocus(window *glfw.Window) {
	hwnd := uintptr(unsafe.Pointer(window.GetWin32Window()))
	style, _, _ := getWindowLongPtrW.Call(hwnd, gwlExStyle)
	style = (style | wsExNoActivate | wsExToolWindow) &^ wsExAppWindow
	setWindowLongPtrW.Call(hwnd, gwlExStyle, style)
	setWindowPos.Call(hwnd, 0, 0, 0, 0, 0, swpNoSize|swpNoMove|swpNoZOrder|swpNoActivate|swpFrameChanged)
}

// Centre of the foreground window, else the cursor.
func focusPoint() (image.Point, bool) {
	if hwnd, _, _ := getForegroundWindow.Call(); hwnd != 0 {
		var r winRect
		if ok, _, _ := getWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r))); ok != 0 {
			return image.Pt(int(r.left+r.right)/2, int(r.top+r.bottom)/2), true
		}
	}
	var p winPoint
	if ok, _, _ := getCursorPos.Call(uintptr(unsafe.Pointer(&p))); ok != 0 {
		return image.Pt(int(p.x), int(p.y)), true
	}
	return image.Point{}, false
}

// Panel makes a focusable window float like the indicator, placed in the frame. Only Windows 11 rounds it.
func Panel(window uintptr, frame image.Rectangle, _ int) {
	if window == 0 {
		return
	}
	keepOffTaskbar(window)

	corner := uint32(dwmwcpRound)
	dwmSetWindowAttribute.Call(window, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
	setWindowPos.Call(window, 0, uintptr(frame.Min.X), uintptr(frame.Min.Y), 0, 0, swpNoSize|swpNoZOrder|swpNoActivate)
}

// The taskbar only rereads the style when a window is shown, so a visible one is shown again.
func keepOffTaskbar(window uintptr) {
	style, _, _ := getWindowLongPtrW.Call(window, gwlExStyle)
	if style&wsExToolWindow != 0 {
		return
	}

	visible, _, _ := isWindowVisible.Call(window)
	if visible != 0 {
		showWindow.Call(window, swHide)
	}
	setWindowLongPtrW.Call(window, gwlExStyle, (style|wsExToolWindow)&^wsExAppWindow)
	if visible != 0 {
		showWindow.Call(window, swShow)
	}
}

package overlay

import (
	"image"
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
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

	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCxVirtualScreen = 78
	smCyVirtualScreen = 79

	srcCopy    = 0x00CC0020
	captureBlt = 0x40000000
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
	getSystemMetrics    = user32.NewProc("GetSystemMetrics")
	getDC               = user32.NewProc("GetDC")
	releaseDC           = user32.NewProc("ReleaseDC")

	gdi32                 = windows.NewLazySystemDLL("gdi32.dll")
	createCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	createDIBSection      = gdi32.NewProc("CreateDIBSection")
	selectObject          = gdi32.NewProc("SelectObject")
	bitBlt                = gdi32.NewProc("BitBlt")
	deleteObject          = gdi32.NewProc("DeleteObject")
	deleteDC              = gdi32.NewProc("DeleteDC")
	dwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")
)

// Windows 11 rounds window corners from build 22000.
const roundingBuild = 22000

func windowsBuild() uint32 { return windows.RtlGetVersion().BuildNumber }

type winRect struct{ left, top, right, bottom int32 }

type winPoint struct{ x, y int32 }

// GLFW's app-window style would put it in the taskbar; the frame-changed call makes it stick.
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

// Panel floats a focusable window like the indicator; only Windows 11 rounds it.
func Panel(window uintptr, frame image.Rectangle, look Look) {
	if window == 0 {
		return
	}
	keepOffTaskbar(window)

	corner := uint32(dwmwcpRound)
	dwmSetWindowAttribute.Call(window, dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&corner)), unsafe.Sizeof(corner))
	setWindowPos.Call(window, 0, uintptr(frame.Min.X), uintptr(frame.Min.Y), uintptr(frame.Dx()), uintptr(frame.Dy()), swpNoZOrder|swpNoActivate)
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

// Frosted by Encre: acrylic goes flat on focus loss and OpenGL windows are not reliably see-through.
func GlassBackdrop() Backdrop {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err == nil {
		defer key.Close()
		if on, _, err := key.GetIntegerValue("EnableTransparency"); err == nil && on == 0 {
			return BackdropNone
		}
	}
	return BackdropFrosted
}

type bitmapInfoHeader struct {
	size                         uint32
	width, height                int32
	planes, bitCount             uint16
	compression, sizeImage       uint32
	xPelsPerMeter, yPelsPerMeter int32
	colorsUsed, colorsImportant  uint32
}

func capture(area image.Rectangle) *image.RGBA {
	metric := func(index uintptr) int {
		v, _, _ := getSystemMetrics.Call(index)
		return int(int32(v))
	}
	x, y := metric(smXVirtualScreen), metric(smYVirtualScreen)
	desktop := image.Rect(x, y, x+metric(smCxVirtualScreen), y+metric(smCyVirtualScreen))
	area = area.Intersect(desktop)
	if area.Empty() {
		return nil
	}

	screen, _, _ := getDC.Call(0)
	if screen == 0 {
		return nil
	}
	defer releaseDC.Call(0, screen)
	memory, _, _ := createCompatibleDC.Call(screen)
	if memory == 0 {
		return nil
	}
	defer deleteDC.Call(memory)

	header := bitmapInfoHeader{width: int32(area.Dx()), height: -int32(area.Dy()), planes: 1, bitCount: 32}
	header.size = uint32(unsafe.Sizeof(header))
	var bits unsafe.Pointer
	bitmap, _, _ := createDIBSection.Call(memory, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return nil
	}
	defer deleteObject.Call(bitmap)
	previous, _, _ := selectObject.Call(memory, bitmap)
	defer selectObject.Call(memory, previous)

	ok, _, _ := bitBlt.Call(memory, 0, 0, uintptr(area.Dx()), uintptr(area.Dy()), screen,
		uintptr(area.Min.X), uintptr(area.Min.Y), srcCopy|captureBlt)
	if ok == 0 {
		return nil
	}
	shot := image.NewRGBA(area)
	bgra := unsafe.Slice((*byte)(bits), len(shot.Pix))
	for i := 0; i < len(shot.Pix); i += 4 {
		shot.Pix[i], shot.Pix[i+1], shot.Pix[i+2], shot.Pix[i+3] = bgra[i+2], bgra[i+1], bgra[i], 0xff
	}
	return shot
}

// Corner is the radius Windows gives a panel's corners: its own small one on Windows 11, none before.
func Corner(float32) float32 {
	if windowsBuild() >= roundingBuild {
		return 8
	}
	return 0
}

// No-op: fading needs a layered window, which its OpenGL surface does not survive.
func SetOpacity(uintptr, float64) {}

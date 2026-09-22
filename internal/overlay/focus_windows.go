package overlay

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.4/glfw"
	"golang.org/x/sys/windows"
)

const (
	// GWL_EXSTYLE is -20; written as the register value SetWindowLongPtrW reads.
	gwlExStyle     = ^uintptr(19)
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	wsExNoActivate = 0x08000000
)

var (
	user32            = windows.NewLazySystemDLL("user32.dll")
	getWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	setWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
)

// A no-activate tool window is never brought to the foreground and never
// appears in the taskbar; the user's app keeps the keyboard.
func noFocus(window *glfw.Window) {
	hwnd := uintptr(unsafe.Pointer(window.GetWin32Window()))
	style, _, _ := getWindowLongPtrW.Call(hwnd, gwlExStyle)
	setWindowLongPtrW.Call(hwnd, gwlExStyle, style|wsExNoActivate|wsExToolWindow|wsExTopmost)
}

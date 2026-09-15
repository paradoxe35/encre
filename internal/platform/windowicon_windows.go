//go:build windows

package platform

import (
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/paradoxe35/encre/internal/logger"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")
	procDestroyIcon              = user32.NewProc("DestroyIcon")
)

const (
	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1

	smCxIcon   = 11
	smCxSmIcon = 49

	// The only icon format version there is.
	iconResourceVersion = 0x00030000

	// GLFW registers every window it creates under this class; Fyne creates no others.
	glfwWindowClass = "GLFW30"
)

// windowIcons is what the last SetWindowIcons installed. Windows keeps drawing
// from the handle, so they live until the next call replaces them.
var windowIcons struct {
	big, small windows.Handle
	applied    int
}

// enumWindowsCallback is created once: every NewCallback takes a slot from a
// small process-wide pool that is never given back.
var enumWindowsCallback = windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
	var owner uint32
	windows.GetWindowThreadProcessId(hwnd, &owner)
	if owner != windows.GetCurrentProcessId() || className(hwnd) != glfwWindowClass {
		return 1 // keep enumerating
	}

	// The handles WM_SETICON returns are GLFW's; it destroys them itself.
	if windowIcons.big != 0 {
		procSendMessageW.Call(uintptr(hwnd), wmSetIcon, iconBig, uintptr(windowIcons.big))
	}
	if windowIcons.small != 0 {
		procSendMessageW.Call(uintptr(hwnd), wmSetIcon, iconSmall, uintptr(windowIcons.small))
	}
	windowIcons.applied++
	return 1
})

// SetWindowIcons gives every GLFW window in this process the frame from ico
// that matches each size Windows asks for. Fyne offers GLFW one 256 px image,
// which GLFW then installs as the 16 px icon too, and Task Manager draws that
// at its full size. Call after the window is shown, from Fyne's main goroutine.
func SetWindowIcons(ico []byte) {
	frames, err := icoFrames(ico)
	if err != nil {
		logger.Warn("Window icon left to GLFW", "error", err)
		return
	}

	big := createIcon(frames, systemMetric(smCxIcon))
	small := createIcon(frames, systemMetric(smCxSmIcon))
	if big == 0 && small == 0 {
		logger.Warn("Window icon left to GLFW: no frame could be created")
		return
	}

	previous := windowIcons
	windowIcons.big, windowIcons.small, windowIcons.applied = big, small, 0

	if err := windows.EnumWindows(enumWindowsCallback, nil); err != nil {
		logger.Warn("Could not list windows to set icons", "error", err)
	}

	for _, handle := range []windows.Handle{previous.big, previous.small} {
		if handle != 0 {
			procDestroyIcon.Call(uintptr(handle))
		}
	}

	logger.Info("Window icons set", "windows", windowIcons.applied,
		"big_px", systemMetric(smCxIcon), "small_px", systemMetric(smCxSmIcon))
}

// createIcon builds an HICON from the frame nearest to size pixels, at the
// frame's own size: Windows scales at draw time when the two differ.
func createIcon(frames []icoFrame, size int) windows.Handle {
	frame, ok := pickFrame(frames, size)
	if !ok {
		return 0
	}

	handle, _, err := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&frame.data[0])),
		uintptr(len(frame.data)),
		1, // an icon, not a cursor
		iconResourceVersion,
		uintptr(frame.width),
		uintptr(frame.height),
		0,
	)
	if handle == 0 {
		logger.Warn("Could not create window icon", "px", frame.width, "error", err)
	}
	return windows.Handle(handle)
}

func systemMetric(index int) int {
	value, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(value)
}

func className(hwnd windows.HWND) string {
	var buf [64]uint16
	n, err := windows.GetClassName(hwnd, &buf[0], int32(len(buf)))
	if err != nil || n <= 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

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

	iconResourceVersion = 0x00030000
	glfwWindowClass     = "GLFW30"
)

// Windows keeps drawing from these handles, so they live until replaced.
var windowIcons struct {
	big, small windows.Handle
	applied    int
}

// Created once: NewCallback slots are never given back.
var enumWindowsCallback = windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
	var owner uint32
	windows.GetWindowThreadProcessId(hwnd, &owner)
	if owner != windows.GetCurrentProcessId() || className(hwnd) != glfwWindowClass {
		return 1 // keep enumerating
	}

	if windowIcons.big != 0 {
		procSendMessageW.Call(uintptr(hwnd), wmSetIcon, iconBig, uintptr(windowIcons.big))
	}
	if windowIcons.small != 0 {
		procSendMessageW.Call(uintptr(hwnd), wmSetIcon, iconSmall, uintptr(windowIcons.small))
	}
	windowIcons.applied++
	return 1
})

// SetWindowIcons replaces the single 256 px image GLFW installs for every
// size, which Task Manager draws at full size, with the frame drawn for each.
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

func createIcon(frames []icoFrame, size int) windows.Handle {
	frame, ok := pickFrame(frames, size)
	if !ok {
		return 0
	}

	handle, _, err := procCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&frame.data[0])),
		uintptr(len(frame.data)),
		1,
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

//go:build windows

package stt

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows does not wrap GlobalMemoryStatusEx, so MEMORYSTATUSEX is declared here.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	globalMemoryStatusEx = kernel32.NewProc("GlobalMemoryStatusEx")
)

func sysMemoryMB() int {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))

	// Zero means the call failed; ranking then treats memory as unknown.
	if ret, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status))); ret == 0 {
		return 0
	}
	return int(status.TotalPhys / (1 << 20))
}

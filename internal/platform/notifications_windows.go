//go:build windows

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/paradoxe35/encre/internal/logger"
)

// Toasts are posted under the app ID. Windows takes the header name and icon from a Start
// menu shortcut that carries the ID as System.AppUserModel.ID; without one it prints the ID.
func RegisterNotifier(id, name string) {
	exe, err := os.Executable()
	if err != nil {
		logger.Warn("Could not register the notification sender", "error", err)
		return
	}
	programs, err := windows.KnownFolderPath(windows.FOLDERID_Programs, 0)
	if err != nil {
		logger.Warn("Could not register the notification sender", "error", err)
		return
	}

	link := filepath.Join(programs, name, name+".lnk")
	if err := writeShortcut(link, exe, id); err != nil {
		logger.Warn("Could not register the notification sender", "shortcut", link, "error", err)
		return
	}
	// Earlier builds registered through this key; Windows must not read the two side by side.
	registry.DeleteKey(registry.CURRENT_USER, `Software\Classes\AppUserModelId\`+id)
	logger.Info("Notification sender registered", "shortcut", link)
}

var (
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procCoCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidShellLink     = mustGUID("{00021401-0000-0000-C000-000000000046}")
	iidShellLinkW      = mustGUID("{000214F9-0000-0000-C000-000000000046}")
	iidPersistFile     = mustGUID("{0000010B-0000-0000-C000-000000000046}")
	iidPropertyStore   = mustGUID("{886D8EEB-8CF2-4446-8D02-CDBA1DBDCF99}")
	pkeyAppUserModelID = propertyKey{fmtid: mustGUID("{9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3}"), pid: 5}
)

const (
	clsctxInprocServer = 1
	stgmReadWrite      = 2
	vtLPWSTR           = 31

	// vtable slots, after the three IUnknown methods
	shellLinkSetIconLocation = 17
	shellLinkSetPath         = 20
	persistFileLoad          = 5
	persistFileSave          = 6
	propertyStoreSetValue    = 6
	propertyStoreCommit      = 7
)

type propertyKey struct {
	fmtid windows.GUID
	pid   uint32
}

type propVariant struct {
	vt  uint16
	_   [3]uint16
	val uintptr
	_   uintptr
}

func writeShortcut(link, target, appID string) error {
	// COM apartments are per thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		defer windows.CoUninitialize()
	}

	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}

	var shellLink unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&shellLink)))
	if err := hresult(hr); err != nil {
		return err
	}
	defer release(shellLink)

	persistFile, err := queryInterface(shellLink, &iidPersistFile)
	if err != nil {
		return err
	}
	defer release(persistFile)

	linkW := utf16(link)
	if _, err := os.Stat(link); err == nil {
		if err := hresult(call(persistFile, persistFileLoad, uintptr(unsafe.Pointer(linkW)), stgmReadWrite)); err != nil {
			return fmt.Errorf("load shortcut: %w", err)
		}
	}

	if err := hresult(call(shellLink, shellLinkSetPath, uintptr(unsafe.Pointer(utf16(target))))); err != nil {
		return fmt.Errorf("set target: %w", err)
	}
	if err := hresult(call(shellLink, shellLinkSetIconLocation, uintptr(unsafe.Pointer(utf16(target))), 0)); err != nil {
		return fmt.Errorf("set icon: %w", err)
	}

	store, err := queryInterface(shellLink, &iidPropertyStore)
	if err != nil {
		return err
	}
	defer release(store)

	value := propVariant{vt: vtLPWSTR, val: uintptr(unsafe.Pointer(utf16(appID)))}
	if err := hresult(call(store, propertyStoreSetValue, uintptr(unsafe.Pointer(&pkeyAppUserModelID)), uintptr(unsafe.Pointer(&value)))); err != nil {
		return fmt.Errorf("set app id: %w", err)
	}
	if err := hresult(call(store, propertyStoreCommit)); err != nil {
		return fmt.Errorf("commit app id: %w", err)
	}

	if err := hresult(call(persistFile, persistFileSave, uintptr(unsafe.Pointer(linkW)), 1)); err != nil {
		return fmt.Errorf("save shortcut: %w", err)
	}
	return nil
}

// A COM object is a pointer to its vtable; a method is a slot in it, called with the object first.
func call(object unsafe.Pointer, slot int, args ...uintptr) uintptr {
	vtable := *(**[32]uintptr)(object)
	hr, _, _ := syscall.SyscallN(vtable[slot], append([]uintptr{uintptr(object)}, args...)...)
	return hr
}

func queryInterface(object unsafe.Pointer, iid *windows.GUID) (unsafe.Pointer, error) {
	var out unsafe.Pointer
	if err := hresult(call(object, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out)))); err != nil {
		return nil, fmt.Errorf("query interface: %w", err)
	}
	return out, nil
}

func release(object unsafe.Pointer) {
	call(object, 2)
}

func hresult(hr uintptr) error {
	if int32(hr) < 0 {
		return fmt.Errorf("HRESULT 0x%08X", uint32(hr))
	}
	return nil
}

func utf16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

func mustGUID(s string) windows.GUID {
	guid, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return guid
}

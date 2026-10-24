//go:build darwin

package main

/*
#cgo LDFLAGS: -framework Cocoa
#include <stdbool.h>
#include "reopen_darwin.h"
*/
import "C"

var onReopen func()

//export encreHandleReopen
func encreHandleReopen() {
	if onReopen != nil {
		onReopen()
	}
}

var onTerminate func(asked bool) bool

//export encreHandleTerminate
func encreHandleTerminate(asked C.bool) C.bool {
	if onTerminate == nil {
		return C.bool(asked)
	}
	return C.bool(onTerminate(bool(asked)))
}

// stop runs on the UI thread as macOS quits the app, so it must not wait on that thread. asked is
// whether macOS itself asked; stop reports whether the quit goes ahead.
func installTerminateHandler(stop func(asked bool) bool) {
	onTerminate = stop
	C.EncreInstallTerminateHandler()
}

// Fyne does not forward Dock-icon reopen, so the handler is added to the delegate Fyne owns.
func installReopenHandler(show func()) {
	onReopen = show
	C.EncreInstallReopenHandler()
}

//go:build darwin

package main

/*
#cgo LDFLAGS: -framework Cocoa
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

var onTerminate func()

//export encreHandleTerminate
func encreHandleTerminate() {
	if onTerminate != nil {
		onTerminate()
	}
}

// stop runs on the UI thread as macOS quits the app, so it must not wait on that thread.
func installTerminateHandler(stop func()) {
	onTerminate = stop
	C.EncreInstallTerminateHandler()
}

// Fyne does not forward Dock-icon reopen, so the handler is added to the delegate Fyne owns.
func installReopenHandler(show func()) {
	onReopen = show
	C.EncreInstallReopenHandler()
}

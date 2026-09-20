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

// Fyne does not forward Dock-icon reopen, so the handler is added to the delegate Fyne owns.
func installReopenHandler(show func()) {
	onReopen = show
	C.EncreInstallReopenHandler()
}

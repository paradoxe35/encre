//go:build darwin

package main

/*
#cgo LDFLAGS: -framework Cocoa -framework UserNotifications
#include "notifications_darwin.h"
*/
import "C"

import "github.com/paradoxe35/encre/internal/logger"

//export encreNotificationAuthorization
func encreNotificationAuthorization(granted C.bool, reason *C.char) {
	if granted {
		logger.Info("Notification permission granted")
		return
	}
	if reason == nil {
		logger.Warn("Notification permission refused: notifications fall back to AppleScript")
		return
	}
	logger.Warn("Notification permission unavailable: notifications fall back to AppleScript", "reason", C.GoString(reason))
}

// Asked at launch: macOS drops notifications while its prompt is up, and a refusal sticks.
func prepareNotifications() {
	C.EncreRequestNotificationAuthorization()
}

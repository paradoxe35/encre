//go:build darwin

package main

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

// AppKit is main-thread only; callers arrive from tray callbacks and the instance handover.
void SetActivationPolicyRegular(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp setActivationPolicy:NSApplicationActivationPolicyRegular];

        // Activate on the next run-loop turn: switching policy and activating in one turn
        // leaves the window behind whatever was in front.
        dispatch_async(dispatch_get_main_queue(), ^{
            [NSApp activateIgnoringOtherApps:YES];
        });
    });
}

void SetActivationPolicyAccessory(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        [NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
    });
}
*/
import "C"

import "github.com/paradoxe35/encre/internal/logger"

func showInDock() {
	logger.Info("Setting macOS activation policy to Regular (show in Dock)")
	C.SetActivationPolicyRegular()
}

func hideFromDock() {
	logger.Info("Setting macOS activation policy to Accessory (hide from Dock)")
	C.SetActivationPolicyAccessory()
}

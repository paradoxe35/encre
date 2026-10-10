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

        // Activate next run-loop turn: doing both in one turn leaves the window behind the front app.
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

// An active app with no window left keeps the keyboard; hidden, macOS hands it to the next app itself.
void YieldFocus(void) {
    if (![NSApp isActive]) {
        return;
    }
    for (NSWindow* window in [NSApp windows]) {
        if ([window isVisible] && [window canBecomeKeyWindow]) {
            return;
        }
    }
    [NSApp hide:nil];
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

func yieldFocus() { C.YieldFocus() }

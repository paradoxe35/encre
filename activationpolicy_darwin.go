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

static NSRunningApplication* previousApp = nil;

// Without this, hiding our last window leaves the keyboard with Encre instead of the user's app.
void RememberFrontmostApp(void) {
    NSRunningApplication* front = [[NSWorkspace sharedWorkspace] frontmostApplication];
    if (front == nil || [front isEqual:[NSRunningApplication currentApplication]]) {
        return;
    }
    [previousApp release];
    previousApp = [front retain];
}

void RestoreFrontmostApp(void) {
    [previousApp activateWithOptions:0];
    [previousApp release];
    previousApp = nil;
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

func rememberFrontmostApp() { C.RememberFrontmostApp() }

func restoreFrontmostApp() { C.RestoreFrontmostApp() }

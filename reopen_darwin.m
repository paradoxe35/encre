#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include <stdio.h>

#include "reopen_darwin.h"
#include "_cgo_export.h"

// Clicking the icon of an app that is already running does not start a second process: macOS
// activates this one and sends this message. Unanswered, the app comes forward with no window.
static BOOL shouldHandleReopen(id self, SEL cmd, NSApplication *app, BOOL hasVisibleWindows) {
    if (!hasVisibleWindows) {
        encreHandleReopen();
    }
    return YES;
}

// GLFW answers a quit from logout, restart or the Dock by asking each window to close and then
// cancelling, and Encre's windows only hide, so logout stopped at "Encre canceled log out". The
// teardown runs here instead, on this thread, and the quit goes ahead.
static NSApplicationTerminateReply shouldTerminate(id self, SEL cmd, NSApplication *app) {
    encreHandleTerminate();
    return NSTerminateNow;
}

void EncreInstallTerminateHandler(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        id delegate = [NSApp delegate];
        if (delegate == nil) {
            return;
        }

        char types[16];
        snprintf(types, sizeof(types), "%s%s%s%s",
                 @encode(NSApplicationTerminateReply), @encode(id), @encode(SEL), @encode(id));

        class_replaceMethod(object_getClass(delegate),
                            @selector(applicationShouldTerminate:),
                            (IMP)shouldTerminate,
                            types);
    });
}

void EncreInstallReopenHandler(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        id delegate = [NSApp delegate];
        if (delegate == nil) {
            return;
        }

        char types[16];
        snprintf(types, sizeof(types), "%s%s%s%s%s",
                 @encode(BOOL), @encode(id), @encode(SEL), @encode(id), @encode(BOOL));

        class_replaceMethod(object_getClass(delegate),
                            @selector(applicationShouldHandleReopen:hasVisibleWindows:),
                            (IMP)shouldHandleReopen,
                            types);
    });
}

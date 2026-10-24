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

static IMP glfwShouldTerminate;

// GLFW answers a quit by asking each window to close and then cancelling, and Encre's windows only
// hide, so logout stopped at "Encre canceled log out". A quit macOS asks for (logout, restart,
// shutdown, the Dock's Quit) comes as a quit Apple Event and is torn down here, on this thread, and
// let go ahead; Cmd+Q in Encre's own window sends none and still only hides it, as GLFW does.
static NSApplicationTerminateReply shouldTerminate(id self, SEL cmd, NSApplication *app) {
    NSAppleEventDescriptor *event = [[NSAppleEventManager sharedAppleEventManager] currentAppleEvent];
    bool asked = event != nil && [event eventClass] == kCoreEventClass && [event eventID] == kAEQuitApplication;
    if (encreHandleTerminate(asked)) {
        return NSTerminateNow;
    }
    if (glfwShouldTerminate != NULL) {
        return ((NSApplicationTerminateReply (*)(id, SEL, NSApplication *))glfwShouldTerminate)(self, cmd, app);
    }
    return NSTerminateCancel;
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

        glfwShouldTerminate = class_replaceMethod(object_getClass(delegate),
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

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>

static BOOL encre_overlay_never_key(id self, SEL _cmd) {
    return NO;
}

// GLFW's window class answers YES to becoming key so borderless windows can take
// input; this one must never, or an activation of the app could hand it the
// keyboard. A runtime subclass answering NO is swapped in before the window shows.
static void encre_overlay_refuse_key(NSWindow* window) {
    static Class refusing = Nil;
    if (refusing == Nil) {
        refusing = objc_allocateClassPair(object_getClass(window), "EncreIndicatorWindow", 0);
        class_addMethod(refusing, @selector(canBecomeKeyWindow), (IMP)encre_overlay_never_key, "c@:");
        class_addMethod(refusing, @selector(canBecomeMainWindow), (IMP)encre_overlay_never_key, "c@:");
        objc_registerClassPair(refusing);
    }
    object_setClass(window, refusing);
}

void encre_overlay_no_focus(void* window) {
    NSWindow* w = (__bridge NSWindow*)window;
    encre_overlay_refuse_key(w);
    [w setLevel:NSStatusWindowLevel];
    [w setCollectionBehavior:NSWindowCollectionBehaviorCanJoinAllSpaces
        | NSWindowCollectionBehaviorStationary
        | NSWindowCollectionBehaviorIgnoresCycle
        | NSWindowCollectionBehaviorFullScreenAuxiliary];
}

static int focused_window_centre(int* x, int* y) {
    AXUIElementRef system = AXUIElementCreateSystemWide();
    AXUIElementRef app = NULL;
    AXUIElementRef window = NULL;
    AXValueRef position = NULL;
    AXValueRef size = NULL;
    int found = 0;

    if (AXUIElementCopyAttributeValue(system, kAXFocusedApplicationAttribute, (CFTypeRef*)&app) == kAXErrorSuccess
        && AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, (CFTypeRef*)&window) == kAXErrorSuccess
        && AXUIElementCopyAttributeValue(window, kAXPositionAttribute, (CFTypeRef*)&position) == kAXErrorSuccess
        && AXUIElementCopyAttributeValue(window, kAXSizeAttribute, (CFTypeRef*)&size) == kAXErrorSuccess) {
        CGPoint origin;
        CGSize extent;
        if (AXValueGetValue(position, kAXValueTypeCGPoint, &origin) && AXValueGetValue(size, kAXValueTypeCGSize, &extent)) {
            *x = (int)(origin.x + extent.width / 2);
            *y = (int)(origin.y + extent.height / 2);
            found = 1;
        }
    }

    if (size) CFRelease(size);
    if (position) CFRelease(position);
    if (window) CFRelease(window);
    if (app) CFRelease(app);
    CFRelease(system);
    return found;
}

int encre_overlay_focus_point(int* x, int* y) {
    if (focused_window_centre(x, y)) {
        return 1;
    }
    CGEventRef event = CGEventCreate(NULL);
    if (event == NULL) {
        return 0;
    }
    CGPoint location = CGEventGetLocation(event);
    CFRelease(event);
    *x = (int)location.x;
    *y = (int)location.y;
    return 1;
}

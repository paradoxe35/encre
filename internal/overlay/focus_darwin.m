#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include <dlfcn.h>

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

// The blur behind a window is private API that Terminal, iTerm2 and others use. It is looked up at run
// time, so a macOS without it only loses the blur.
typedef void* (*connection_fn)(void);
typedef int32_t (*blur_fn)(void*, NSInteger, int32_t);
static connection_fn connection = NULL;
static blur_fn blur = NULL;

static int encre_overlay_can_blur(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        connection = (connection_fn)dlsym(RTLD_DEFAULT, "CGSDefaultConnectionForThread");
        blur = (blur_fn)dlsym(RTLD_DEFAULT, "CGSSetWindowBackgroundBlurRadius");
    });
    return connection != NULL && blur != NULL;
}

static void encre_overlay_blur(NSWindow* window, int radius) {
    if (encre_overlay_can_blur()) {
        blur(connection(), [window windowNumber], radius);
    }
}

// Matches the Backdrop constants in overlay.go.
int encre_overlay_backdrop(void) {
    if ([[NSWorkspace sharedWorkspace] accessibilityDisplayShouldReduceTransparency]) {
        return 0;
    }
    return encre_overlay_can_blur() ? 2 : 1;
}

void encre_overlay_opacity(uintptr_t window, double opacity) {
    [(__bridge NSWindow*)(void*)window setAlphaValue:opacity];
}

// GLFW places windows from the top left of the primary screen, Cocoa from its bottom left.
void encre_overlay_panel(uintptr_t window, int x, int y, int width, int height, int radius, int glass) {
    NSWindow* w = (__bridge NSWindow*)(void*)window;
    [w setCollectionBehavior:[w collectionBehavior]
        | NSWindowCollectionBehaviorCanJoinAllSpaces
        | NSWindowCollectionBehaviorIgnoresCycle
        | NSWindowCollectionBehaviorFullScreenAuxiliary];

    CGFloat top = NSMaxY([[[NSScreen screens] firstObject] frame]);
    [w setFrame:NSMakeRect(x, top - y - height, width, height) display:YES];

    [w setOpaque:NO];
    [w setBackgroundColor:[NSColor clearColor]];
    NSView* view = [w contentView];
    [view setWantsLayer:YES];
    [[view layer] setCornerRadius:radius];
    [[view layer] setMasksToBounds:YES];
    if (glass) {
        encre_overlay_blur(w, 24);
    }
    [w invalidateShadow];
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

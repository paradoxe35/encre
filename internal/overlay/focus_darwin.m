#import <Cocoa/Cocoa.h>

void encre_overlay_no_focus(void* window) {
    NSWindow* w = (__bridge NSWindow*)window;
    [w setLevel:NSStatusWindowLevel];
    [w setIgnoresMouseEvents:YES];
    [w setHidesOnDeactivate:NO];
    [w setHasShadow:NO];
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

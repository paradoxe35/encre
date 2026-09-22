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

#import <Cocoa/Cocoa.h>
#import <UserNotifications/UserNotifications.h>

#include "notifications_darwin.h"
#include "_cgo_export.h"

void EncreRequestNotificationAuthorization(void) {
    if ([[NSBundle mainBundle] bundleIdentifier] == nil) {
        encreNotificationAuthorization(false, (char *)"not running from an app bundle");
        return;
    }

    UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
    [center requestAuthorizationWithOptions:UNAuthorizationOptionAlert
                          completionHandler:^(BOOL granted, NSError *_Nullable error) {
        encreNotificationAuthorization(granted, error == nil ? NULL : (char *)[[error localizedDescription] UTF8String]);
    }];
}

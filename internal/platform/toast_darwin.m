#import <Foundation/Foundation.h>
#import <UserNotifications/UserNotifications.h>

#include "_cgo_export.h"

static void encre_toast_as_app(UNUserNotificationCenter* center, NSString* title, NSString* body) {
    UNMutableNotificationContent* content = [[[UNMutableNotificationContent alloc] init] autorelease];
    content.title = title;
    content.body = body;
    UNNotificationRequest* request = [UNNotificationRequest requestWithIdentifier:[[NSUUID UUID] UUIDString]
                                                                          content:content
                                                                          trigger:nil];
    [center addNotificationRequest:request withCompletionHandler:^(NSError* error) {
        if (error != nil) {
            encreToastByScript((char*)[title UTF8String], (char*)[body UTF8String]);
        }
    }];
}

// The permission is read each time: the user can grant or withdraw it in System Settings at any moment.
void encre_toast(const char* cTitle, const char* cBody) {
    if ([[NSBundle mainBundle] bundleIdentifier] == nil) {
        encreToastByScript((char*)cTitle, (char*)cBody);
        return;
    }

    NSString* title = [[NSString alloc] initWithUTF8String:cTitle];
    NSString* body = [[NSString alloc] initWithUTF8String:cBody];
    UNUserNotificationCenter* center = [UNUserNotificationCenter currentNotificationCenter];
    [center getNotificationSettingsWithCompletionHandler:^(UNNotificationSettings* settings) {
        UNAuthorizationStatus status = settings.authorizationStatus;
        if (status == UNAuthorizationStatusAuthorized || status == UNAuthorizationStatusProvisional) {
            encre_toast_as_app(center, title, body);
        } else {
            encreToastByScript((char*)[title UTF8String], (char*)[body UTF8String]);
        }
        [title release];
        [body release];
    }];
}

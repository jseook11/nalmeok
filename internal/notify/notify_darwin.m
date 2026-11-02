//go:build darwin && cgo

// Completion notifications through UNUserNotificationCenter (see notify.go).
//
// The delegate brings the app forward when a notification is clicked; Go's
// notifyClicked then shows the Wails window the same way a second launch
// does. Notifications are not shown while the app is frontmost (no
// willPresentNotification:): the window already shows the finished item.
//
// Manual retain/release, like Wails' own Objective-C (no -fobjc-arc).

#import <Cocoa/Cocoa.h>
#import <UserNotifications/UserNotifications.h>
#include "notify_darwin.h"
#include "_cgo_export.h"

@interface NalmeokNotifyDelegate : NSObject <UNUserNotificationCenterDelegate>
@end

@implementation NalmeokNotifyDelegate
- (void)userNotificationCenter:(UNUserNotificationCenter *)center
    didReceiveNotificationResponse:(UNNotificationResponse *)response
             withCompletionHandler:(void (^)(void))completionHandler {
    dispatch_async(dispatch_get_main_queue(), ^{
        if (NSApp != nil) {
            [NSApp unhide:nil];
            if (@available(macOS 14.0, *)) {
                [NSApp activate];
            } else {
                [NSApp activateIgnoringOtherApps:YES];
            }
            for (NSWindow *window in [NSApp windows]) {
                if (![window canBecomeMainWindow]) {
                    continue;
                }
                if ([window isMiniaturized]) {
                    [window deminiaturize:nil];
                }
                if ([window isVisible]) {
                    [window makeKeyAndOrderFront:nil];
                }
            }
        }
        notifyClicked();
    });
    completionHandler();
}
@end

static NalmeokNotifyDelegate *delegate; // kept for the life of the process

int notifyAvailable(void) {
    @autoreleasepool {
        NSBundle *bundle = [NSBundle mainBundle];
        NSString *identifier = [bundle bundleIdentifier];
        return identifier.length > 0 && [[bundle bundlePath] hasSuffix:@".app"];
    }
}

void notifyInstall(void) {
    static dispatch_once_t once;
    dispatch_once(&once, ^{
        delegate = [[NalmeokNotifyDelegate alloc] init];
        [UNUserNotificationCenter currentNotificationCenter].delegate = delegate;
    });
}

void notifySend(const char *title, const char *body) {
    @autoreleasepool {
        notifyInstall();
        NSString *t = [NSString stringWithUTF8String:title ? title : ""];
        NSString *b = [NSString stringWithUTF8String:body ? body : ""];
        if (t == nil) t = @"";
        if (b == nil) b = @"";
        UNUserNotificationCenter *center = [UNUserNotificationCenter currentNotificationCenter];
        // Asks only the first time; later calls answer from the saved
        // setting at once, so a change in System Settings is respected.
        [center requestAuthorizationWithOptions:(UNAuthorizationOptionAlert | UNAuthorizationOptionSound)
                              completionHandler:^(BOOL granted, NSError *error) {
            if (error != nil) {
                // The system refused the app outright (for example an
                // unsigned copy). The osascript notification still works.
                notifyFallback((char *)[t UTF8String], (char *)[b UTF8String],
                               (char *)[[error description] UTF8String]);
                return;
            }
            if (!granted) {
                return; // the user turned notifications off
            }
            UNMutableNotificationContent *content = [[UNMutableNotificationContent alloc] init];
            content.title = t;
            content.body = b;
            UNNotificationRequest *request =
                [UNNotificationRequest requestWithIdentifier:[[NSUUID UUID] UUIDString]
                                                     content:content
                                                     trigger:nil];
            [content release];
            [center addNotificationRequest:request
                     withCompletionHandler:^(NSError *addError) {
                if (addError != nil) {
                    notifyFallback((char *)[t UTF8String], (char *)[b UTF8String],
                                   (char *)[[addError description] UTF8String]);
                }
            }];
        }];
    }
}

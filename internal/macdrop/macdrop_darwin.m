//go:build darwin && cgo

// File-promise drops for Wails' web view (see package doc in collect.go).
//
// Hook design: Wails' WailsWebView (a WKWebView subclass) reads only NSURL
// objects in performDragOperation: and sends them to Go as "wails:file-drop".
// Rather than covering the web view with another view (which would take
// every drop, Finder's included, away from WebKit and Wails), the three
// dragging methods of WailsWebView are swapped for small wrappers:
//
//   - performDragOperation: if the pasteboard has file URLs, Wails' original
//     runs untouched. Only when it has no file URLs but has file promises are
//     the promises received here; WebKit's own drop still runs so the page
//     sees the drop and clears its highlight, but Wails' message (which would
//     carry an empty path) is skipped.
//   - draggingEntered:/draggingUpdated: keep the original result, except that
//     a promise drag the original would refuse becomes a copy.
//
// The web view is also registered for the file-promise pasteboard types, on
// top of the types it already has.
//
// Manual retain/release, like Wails' own Objective-C (no -fobjc-arc).

#import <Cocoa/Cocoa.h>
#import <objc/runtime.h>
#include <stdlib.h>
#include <string.h>
#include "macdrop_darwin.h"
#include "_cgo_export.h"

static Class hookedClass;
static IMP origPerform;
static IMP origEntered;
static IMP origUpdated;
static NSOperationQueue *receiveQueue;
static BOOL debugDrops;

// isPromiseDrag: the drop carries file promises and no file URL. Finder drags
// carry file URLs and never take this path.
static BOOL isPromiseDrag(NSPasteboard *pb) {
    NSDictionary *fileOnly = @{NSPasteboardURLReadingFileURLsOnlyKey : @YES};
    if ([pb canReadObjectForClasses:@[ [NSURL class] ] options:fileOnly]) {
        return NO;
    }
    return [pb canReadObjectForClasses:@[ [NSFilePromiseReceiver class] ] options:nil];
}

// receivePromises gives each promise its own hidden stage folder inside the
// destination; Go moves every file that arrives into place under a free name
// and drops the stage folder once the promise's files are all in.
static void receivePromises(NSArray *receivers) {
    for (NSFilePromiseReceiver *receiver in receivers) {
        if (debugDrops) {
            NSLog(@"nalmeok drop: promise types %@", receiver.fileTypes);
        }
        char *cdir = macdropStageDir();
        NSString *dir = cdir ? [NSString stringWithUTF8String:cdir] : @"";
        free(cdir);
        if (dir.length == 0) {
            macdropFailed((char *)"could not make a folder for the dropped file");
            continue;
        }
        NSURL *stage = [NSURL fileURLWithPath:dir isDirectory:YES];
        // The reader runs once per promised file, on the serial receiveQueue.
        __block NSUInteger remaining = MAX((NSUInteger)1, receiver.fileTypes.count);
        [receiver receivePromisedFilesAtDestination:stage
                                            options:@{}
                                     operationQueue:receiveQueue
                                             reader:^(NSURL *fileURL, NSError *error) {
            if (error != nil) {
                macdropFailed((char *)[[error localizedDescription] UTF8String]);
            } else {
                if (debugDrops) {
                    NSLog(@"nalmeok drop: received %@", fileURL.path);
                }
                macdropReceived((char *)[fileURL fileSystemRepresentation]);
            }
            if (remaining > 0 && --remaining == 0) {
                macdropStageDone((char *)[stage fileSystemRepresentation]);
            }
        }];
    }
}

static BOOL nal_performDragOperation(id self, SEL _cmd, id<NSDraggingInfo> sender) {
    NSPasteboard *pb = [sender draggingPasteboard];
    if (!isPromiseDrag(pb)) {
        return ((BOOL (*)(id, SEL, id))origPerform)(self, _cmd, sender);
    }
    NSArray *receivers = [pb readObjectsForClasses:@[ [NSFilePromiseReceiver class] ] options:nil];
    if (receivers.count == 0) {
        return ((BOOL (*)(id, SEL, id))origPerform)(self, _cmd, sender);
    }
    receivePromises(receivers);

    // WebKit's drop (WailsWebView's superclass), without Wails' message.
    IMP webkit = class_getMethodImplementation(class_getSuperclass(hookedClass), _cmd);
    if (webkit != NULL) {
        ((BOOL (*)(id, SEL, id))webkit)(self, _cmd, sender);
    }
    return YES;
}

static NSDragOperation promiseOperation(NSDragOperation op, id<NSDraggingInfo> sender) {
    if (op != NSDragOperationNone) {
        return op;
    }
    if (([sender draggingSourceOperationMask] & NSDragOperationCopy) == 0) {
        return op;
    }
    return isPromiseDrag([sender draggingPasteboard]) ? NSDragOperationCopy : op;
}

static NSDragOperation nal_draggingEntered(id self, SEL _cmd, id<NSDraggingInfo> sender) {
    if (debugDrops) {
        NSLog(@"nalmeok drop: pasteboard types %@", [[sender draggingPasteboard] types]);
    }
    NSDragOperation op = ((NSDragOperation (*)(id, SEL, id))origEntered)(self, _cmd, sender);
    return promiseOperation(op, sender);
}

static NSDragOperation nal_draggingUpdated(id self, SEL _cmd, id<NSDraggingInfo> sender) {
    NSDragOperation op = ((NSDragOperation (*)(id, SEL, id))origUpdated)(self, _cmd, sender);
    return promiseOperation(op, sender);
}

// swap replaces cls's method for sel with imp and returns the previous one.
// When cls only inherits the method, the wrapper is added to cls alone (the
// superclass stays untouched) and the inherited implementation is returned.
static IMP swap(Class cls, SEL sel, IMP imp) {
    Method m = class_getInstanceMethod(cls, sel);
    if (m == NULL) {
        return NULL;
    }
    IMP inherited = method_getImplementation(m);
    if (class_addMethod(cls, sel, imp, method_getTypeEncoding(m))) {
        return inherited;
    }
    return method_setImplementation(m, imp);
}

static BOOL hook(void) {
    Class cls = NSClassFromString(@"WailsWebView");
    if (cls == Nil) {
        return NO;
    }
    // All three or none: a half-hooked view would call a NULL original.
    if (class_getInstanceMethod(cls, @selector(performDragOperation:)) == NULL ||
        class_getInstanceMethod(cls, @selector(draggingEntered:)) == NULL ||
        class_getInstanceMethod(cls, @selector(draggingUpdated:)) == NULL) {
        return NO;
    }
    hookedClass = cls;
    IMP perform = swap(cls, @selector(performDragOperation:), (IMP)nal_performDragOperation);
    IMP entered = swap(cls, @selector(draggingEntered:), (IMP)nal_draggingEntered);
    IMP updated = swap(cls, @selector(draggingUpdated:), (IMP)nal_draggingUpdated);
    origPerform = perform;
    origEntered = entered;
    origUpdated = updated;
    return YES;
}

static void collectViews(NSView *view, NSMutableArray *out) {
    if ([view isKindOfClass:hookedClass]) {
        [out addObject:view];
    }
    for (NSView *sub in [view subviews]) {
        collectViews(sub, out);
    }
}

// registerViews adds the file-promise types to every WailsWebView. The window
// normally exists by the time Install runs; if not, it tries again shortly.
static void registerViews(int attempt) {
    NSMutableArray *views = [NSMutableArray array];
    for (NSWindow *window in [NSApp windows]) {
        if ([window contentView] != nil) {
            collectViews([window contentView], views);
        }
    }
    if (views.count == 0) {
        if (attempt < 20) {
            dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)(250 * NSEC_PER_MSEC)),
                           dispatch_get_main_queue(), ^{
                registerViews(attempt + 1);
            });
        } else {
            macdropFailed((char *)"no web view to register for file promises");
        }
        return;
    }
    for (NSView *view in views) {
        NSMutableOrderedSet *types = [NSMutableOrderedSet orderedSetWithArray:[view registeredDraggedTypes]];
        [types addObjectsFromArray:[NSFilePromiseReceiver readableDraggedTypes]];
        [view registerForDraggedTypes:[types array]];
    }
    if (debugDrops) {
        NSLog(@"nalmeok drop: file promises on for %lu web view(s)", (unsigned long)views.count);
    }
}

void macdropInstall(void) {
    dispatch_async(dispatch_get_main_queue(), ^{
        const char *debug = getenv("NALMEOK_DROP_DEBUG");
        debugDrops = debug != NULL && strcmp(debug, "1") == 0;
        receiveQueue = [[NSOperationQueue alloc] init];
        receiveQueue.maxConcurrentOperationCount = 1;
        receiveQueue.qualityOfService = NSQualityOfServiceUserInitiated;
        if (!hook()) {
            macdropFailed((char *)"WailsWebView drag methods not found; file promises are off");
            return;
        }
        registerViews(0);
    });
}

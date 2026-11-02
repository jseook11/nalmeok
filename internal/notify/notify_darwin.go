//go:build darwin && cgo

package notify

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation -framework UserNotifications
#include <stdlib.h>
#include "notify_darwin.h"
*/
import "C"

import (
	"log"
	"sync"
	"unsafe"
)

var bundled = sync.OnceValue(func() bool { return C.notifyAvailable() != 0 })

func send(title, message string) {
	if !bundled() {
		osascriptSend(title, message)
		return
	}
	ct, cm := C.CString(title), C.CString(message)
	defer C.free(unsafe.Pointer(ct))
	defer C.free(unsafe.Pointer(cm))
	C.notifySend(ct, cm)
}

func install() {
	if bundled() {
		C.notifyInstall()
	}
}

// notifyClicked runs on the main thread after the app was activated.
//
//export notifyClicked
func notifyClicked() { clicked() }

// notifyFallback is called when the notification centre refused the app.
//
//export notifyFallback
func notifyFallback(title, message, reason *C.char) {
	t, m, r := C.GoString(title), C.GoString(message), C.GoString(reason)
	log.Printf("notify: notification centre refused, using osascript: %s", r)
	go osascriptSend(t, m)
}

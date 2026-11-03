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

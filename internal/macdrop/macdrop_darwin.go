//go:build darwin && cgo

package macdrop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>
#include "macdrop_darwin.h"
*/
import "C"

import (
	"log"
	"sync"
)

var (
	mu      sync.Mutex
	dest    string
	batches *collector
	once    sync.Once
)

// Install hooks the window's web view once. Promised files are received into
// dir (see Dir) and reach handler on a Go goroutine, batched per drop;
// handler should hand them to the same code a normal drop uses. Calling
// Install again only replaces the folder and the handler.
func Install(dir string, handler func(paths []string)) {
	if dir == "" || handler == nil {
		return
	}
	mu.Lock()
	dest = dir
	batches = newCollector(batchDelay, handler)
	mu.Unlock()
	once.Do(func() { C.macdropInstall() })
}

// macdropStageDir returns a new hidden folder for one promise, or "" when it
// cannot be made. The caller frees the string.
//
//export macdropStageDir
func macdropStageDir() *C.char {
	mu.Lock()
	dir := dest
	mu.Unlock()
	path, err := newStageDir(dir)
	if err != nil {
		log.Printf("macdrop: %v", err)
		return C.CString("")
	}
	return C.CString(path)
}

// macdropReceived is called once per promised file the source wrote. The
// file is moved into the destination and queued for the handler.
//
//export macdropReceived
func macdropReceived(path *C.char) {
	staged := C.GoString(path)
	mu.Lock()
	dir, c := dest, batches
	mu.Unlock()
	final, err := place(staged, dir)
	if err != nil {
		log.Printf("macdrop: %v", err)
		return
	}
	if c != nil {
		c.add(final)
	}
}

// macdropStageDone is called when every file of one promise has arrived.
//
//export macdropStageDone
func macdropStageDone(dir *C.char) {
	removeStageDir(C.GoString(dir))
}

// macdropFailed reports a promised file the source did not write.
//
//export macdropFailed
func macdropFailed(message *C.char) {
	log.Printf("macdrop: %s", C.GoString(message))
}

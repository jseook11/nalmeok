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
	"time"
)

var (
	mu      sync.Mutex
	root    string
	batches *collector
	once    sync.Once
)

// Install hooks the window's web view once. Received paths reach handler on
// a Go goroutine, batched per drop; handler should hand them to the same code
// a normal drop uses. Calling Install again only replaces the folder and the
// handler.
func Install(workDir string, handler func(paths []string)) {
	if workDir == "" || handler == nil {
		return
	}
	mu.Lock()
	root = workDir
	batches = newCollector(batchDelay, handler)
	mu.Unlock()
	once.Do(func() { C.macdropInstall() })
}

// macdropNewDir returns a new folder for one drop, or "" when it cannot be
// made. The caller frees the string.
//
//export macdropNewDir
func macdropNewDir() *C.char {
	mu.Lock()
	dir := root
	mu.Unlock()
	path, err := newDropDir(dir, time.Now())
	if err != nil {
		log.Printf("macdrop: %v", err)
		return C.CString("")
	}
	return C.CString(path)
}

// macdropReceived is called once per promised file the source wrote.
//
//export macdropReceived
func macdropReceived(path *C.char) {
	p := C.GoString(path)
	mu.Lock()
	c := batches
	mu.Unlock()
	if c != nil {
		c.add(p)
	}
}

// macdropFailed reports a promised file the source did not write.
//
//export macdropFailed
func macdropFailed(message *C.char) {
	log.Printf("macdrop: %s", C.GoString(message))
}

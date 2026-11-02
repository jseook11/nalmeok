// Package macdrop accepts drags that promise a file instead of carrying one.
//
// Voice Memos on macOS is a Mac Catalyst app. Its recordings leave the app as
// UIKit drag items whose NSItemProvider registers a file representation
// (registerFileRepresentationForTypeIdentifier:… with a suggested name such
// as "New Recording 12"). AppKit sees those as file promises: the pasteboard
// carries no file URL until the receiver asks the source to write the file
// somewhere. Wails only reads file URLs from a drop, so such a drop lit up the
// window's drop highlight (WebKit shows it as "Files") and then added nothing.
//
// Install (macOS only) hooks Wails' WailsWebView so a drop with no file URLs
// but with file promises is received into <work>/dropped/<time>-*/ under the
// promised name, and the received paths go to the handler in one batch per
// drop. Every other drop runs Wails' code unchanged. On other platforms
// Install does nothing.
package macdrop

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

// batchDelay is how long the collector waits after the last received file
// before handing the batch on. Promised files arrive one at a time.
const batchDelay = 300 * time.Millisecond

// newDropDir makes a fresh folder for one drop under <root>/dropped. The
// received files stay there: they are the items' sources until the items
// finish, and the transcript is written next to them.
func newDropDir(root string, now time.Time) (string, error) {
	parent := filepath.Join(root, "dropped")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, now.Format("20060102-150405")+"-")
}

// collector groups paths that arrive close together into one call.
type collector struct {
	delay time.Duration
	flush func(paths []string)

	mu      sync.Mutex
	pending []string
	timer   *time.Timer
}

func newCollector(delay time.Duration, flush func(paths []string)) *collector {
	return &collector{delay: delay, flush: flush}
}

func (c *collector) add(path string) {
	if path == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending = append(c.pending, path)
	if c.timer != nil {
		c.timer.Stop()
	}
	c.timer = time.AfterFunc(c.delay, c.fire)
}

func (c *collector) fire() {
	c.mu.Lock()
	paths := c.pending
	c.pending = nil
	c.timer = nil
	c.mu.Unlock()
	if len(paths) > 0 {
		c.flush(paths)
	}
}

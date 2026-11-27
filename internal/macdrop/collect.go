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
// but with file promises is received into a folder the user can see
// (~/Documents/Nalmeok/Voice Memos, see Dir). The transcript is written next
// to its source, so it lands there too. Every other drop runs Wails' code
// unchanged. On other platforms Install does nothing.
package macdrop

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jseook11/nalmeok/internal/output"
)

// batchDelay is how long the collector waits after the last received file
// before handing the batch on. Promised files arrive one at a time.
const batchDelay = 300 * time.Millisecond

// stagePrefix names the hidden folder a promise is first written into. It
// sits inside the destination so the move into place is a rename on the same
// volume.
const stagePrefix = ".receiving-"

// newStageDir makes the destination when needed and a fresh hidden folder in
// it for one promise. The source writes the file there under its own name.
func newStageDir(dest string) (string, error) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(dest, stagePrefix)
}

// place moves a received file from its stage folder into dest under the
// promised name. When that name is taken, " (2)", " (3)", … go before the
// extension, the same style as transcripts: output.Reserve claims the name,
// so two files received at once never take the same one, and the rename then
// replaces the empty claim.
func place(staged, dest string) (string, error) {
	candidate, err := output.Reserve(filepath.Join(dest, filepath.Base(staged)))
	if err != nil {
		return "", err
	}
	if err := os.Rename(staged, candidate); err != nil {
		os.Remove(candidate)
		return "", err
	}
	return candidate, nil
}

// removeStageDir drops a stage folder once its files are moved out. A folder
// that still holds something (a file that could not be moved) is left alone.
func removeStageDir(dir string) {
	if strings.HasPrefix(filepath.Base(dir), stagePrefix) {
		_ = os.Remove(dir)
	}
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

package app

import (
	"context"
	"errors"
	"io"
	"os"
	"unicode/utf8"

	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Window widths. The list alone fits ListWidth; with the transcript pane the
// window grows to PaneWidth (or the screen, if smaller).
const (
	ListWidth     = 480
	ListMinWidth  = 420
	PaneWidth     = 1100
	PaneMinWidth  = 760
	WindowMinTall = 560
)

// MaxOutputRead bounds how much of a transcript the pane reads. A lecture
// transcript is far smaller; a bigger file is shown cut off.
const MaxOutputRead = 2 << 20

// OutputText is a finished item's transcript as the pane shows it.
// Truncated is set when the file is longer than MaxOutputRead.
type OutputText struct {
	Text      string `json:"Text"`
	Truncated bool   `json:"Truncated"`
}

// ReadOutput returns the text of a finished item's .md, at most
// MaxOutputRead bytes. A missing or moved file is reported with the catalog's
// message.
func (a *App) ReadOutput(id string) (OutputText, error) {
	if a.store == nil {
		return OutputText{}, store.ErrNotFound
	}
	item, ok := a.store.Get(id)
	if !ok {
		return OutputText{}, store.ErrNotFound
	}
	if item.Output == "" {
		return OutputText{}, errNoOutput
	}
	f, err := os.Open(item.Output)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return OutputText{}, i18n.Error("error.outputMoved")
		}
		return OutputText{}, i18n.Error("error.readOutputFailed")
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || info.IsDir() {
		return OutputText{}, i18n.Error("error.outputMoved")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxOutputRead+1))
	if err != nil {
		return OutputText{}, i18n.Error("error.readOutputFailed")
	}
	out := OutputText{}
	if len(data) > MaxOutputRead {
		data = data[:MaxOutputRead]
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
		out.Truncated = true
	}
	out.Text = string(data)
	return out, nil
}

// PaneOpen reports whether the transcript pane was left open.
func (a *App) PaneOpen() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg != nil && a.cfg.Pane()
}

// SetPane shows or hides the transcript pane: it remembers the choice and
// widens the window to fit the pane, or narrows it back to the width it had
// before the pane opened.
func (a *App) SetPane(open bool) error {
	a.mu.Lock()
	var err error
	if a.cfg != nil {
		err = a.cfg.SetPane(open)
	}
	a.mu.Unlock()
	if ctx := a.context(); ctx != nil {
		a.fitWindow(open)
	}
	return err
}

// fitWindow resizes the window for the pane. A maximised or full-screen
// window keeps its size; only the minimum changes.
func (a *App) fitWindow(open bool) {
	ctx := a.context()
	if open {
		wailsruntime.WindowSetMinSize(ctx, PaneMinWidth, WindowMinTall)
	} else {
		wailsruntime.WindowSetMinSize(ctx, ListMinWidth, WindowMinTall)
	}
	if wailsruntime.WindowIsFullscreen(ctx) || wailsruntime.WindowIsMaximised(ctx) {
		return
	}
	width, height := wailsruntime.WindowGetSize(ctx)
	screen := screenWidth(ctx)
	if open {
		if width >= PaneWidth {
			return
		}
		a.mu.Lock()
		a.listWidth = width
		a.mu.Unlock()
		target := PaneWidth
		if screen > 0 && target > screen {
			target = screen
		}
		if target <= width {
			return
		}
		wailsruntime.WindowSetSize(ctx, target, height)
		// Keep the widened window on screen.
		if x, y := wailsruntime.WindowGetPosition(ctx); screen > 0 && x+target > screen {
			wailsruntime.WindowSetPosition(ctx, max(0, screen-target), y)
		}
		return
	}
	a.mu.Lock()
	target := a.listWidth
	a.mu.Unlock()
	if target <= 0 {
		target = ListWidth
	}
	if width > target {
		wailsruntime.WindowSetSize(ctx, target, height)
	}
}

// screenWidth is the logical width of the screen showing the window, or 0
// when unknown.
func screenWidth(ctx context.Context) int {
	screens, err := wailsruntime.ScreenGetAll(ctx)
	if err != nil {
		return 0
	}
	for _, s := range screens {
		if s.IsCurrent {
			return s.Size.Width
		}
	}
	for _, s := range screens {
		if s.IsPrimary {
			return s.Size.Width
		}
	}
	return 0
}

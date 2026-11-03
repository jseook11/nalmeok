package app

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/output"
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
// Truncated is set when the file is longer than MaxOutputRead. Moved is set
// when the .md is gone and the text comes from the app's copy.
type OutputText struct {
	Text      string `json:"Text"`
	Truncated bool   `json:"Truncated"`
	Moved     bool   `json:"Moved"`
}

// ReadOutput returns the text of a finished item's transcript, at most
// MaxOutputRead bytes: the .md where Output says, or the app's copy when that
// file is gone. With neither, the catalog's moved-file message is returned.
func (a *App) ReadOutput(id string) (OutputText, error) {
	item, err := a.finishedItem(id)
	if err != nil {
		return OutputText{}, err
	}
	f, moved, err := a.openOutput(item)
	if err != nil {
		return OutputText{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxOutputRead+1))
	if err != nil {
		return OutputText{}, i18n.Error("error.readOutputFailed")
	}
	out := OutputText{Moved: moved}
	if len(data) > MaxOutputRead {
		data = data[:MaxOutputRead]
		// Drop only a rune the limit split (at most UTFMax-1 bytes); bytes
		// that were invalid anyway stay, as in an untruncated read.
		for i := 1; i < utf8.UTFMax && i <= len(data); i++ {
			if utf8.RuneStart(data[len(data)-i]) {
				if !utf8.FullRune(data[len(data)-i:]) {
					data = data[:len(data)-i]
				}
				break
			}
		}
		out.Truncated = true
	}
	out.Text = string(data)
	return out, nil
}

// finishedItem looks up an item that has a transcript.
func (a *App) finishedItem(id string) (store.Item, error) {
	if a.store == nil {
		return store.Item{}, store.ErrNotFound
	}
	item, ok := a.store.Get(id)
	if !ok {
		return store.Item{}, store.ErrNotFound
	}
	if item.Output == "" {
		return store.Item{}, errNoOutput
	}
	return item, nil
}

// archivePath is the app's copy of an item's transcript, or "" when the app
// keeps none.
func (a *App) archivePath(id string) string {
	return output.ArchivePath(a.TranscriptsDir, id)
}

// openOutput opens an item's transcript. The .md where Output says comes
// first, since the user may have edited it; it is also copied into the
// archive when no copy exists yet (items finished before the archive). When
// that file is gone the app's copy is opened instead, with moved set.
func (a *App) openOutput(item store.Item) (f *os.File, moved bool, err error) {
	f, err = openRegular(item.Output)
	if err == nil {
		a.archiveOutput(item)
		return f, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, i18n.Error("error.readOutputFailed")
	}
	if archive := a.archivePath(item.ID); archive != "" {
		if f, err := openRegular(archive); err == nil {
			return f, true, nil
		}
	}
	return nil, false, i18n.Error("error.outputMoved")
}

// outputText reads an item's whole transcript (see openOutput).
func (a *App) outputText(item store.Item) (string, error) {
	f, _, err := a.openOutput(item)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", i18n.Error("error.readOutputFailed")
	}
	return string(data), nil
}

// openRegular opens a file for reading; a folder counts as missing.
func openRegular(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && info.IsDir() {
		err = fs.ErrNotExist
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// archiveOutput copies an item's .md into the archive unless a copy is
// already there. It is best effort: a failed copy is tried again on the next
// read or launch.
func (a *App) archiveOutput(item store.Item) {
	archive := a.archivePath(item.ID)
	if archive == "" || item.Output == "" {
		return
	}
	if _, err := os.Stat(archive); !errors.Is(err, fs.ErrNotExist) {
		return
	}
	data, err := os.ReadFile(item.Output)
	if err != nil {
		return
	}
	_ = output.Write(archive, string(data))
}

// archiveFinished copies every finished item's .md that has no archived copy
// yet. Startup runs it once, so items finished before the archive existed
// are kept even if their .md moves before the pane ever shows them.
func (a *App) archiveFinished() {
	if a.store == nil || a.TranscriptsDir == "" {
		return
	}
	for _, item := range a.store.List("") {
		if item.State == "done" && item.Output != "" {
			a.archiveOutput(item)
		}
	}
}

// mdFilter limits file dialogs to Markdown files.
func mdFilter() []wailsruntime.FileFilter {
	return []wailsruntime.FileFilter{{DisplayName: i18n.T("dialog.mdFiles"), Pattern: "*.md"}}
}

// LocateOutput lets the user point a finished item at its moved .md. The
// picker starts in the old folder when it still exists. Canceling changes
// nothing.
func (a *App) LocateOutput(id string) error {
	item, err := a.finishedItem(id)
	if err != nil {
		return err
	}
	ctx := a.context()
	if ctx == nil {
		return errNotReady
	}
	options := wailsruntime.OpenDialogOptions{Title: i18n.T("dialog.locateTitle"), Filters: mdFilter()}
	if dir := filepath.Dir(item.Output); isDir(dir) {
		options.DefaultDirectory = dir
	}
	path, err := wailsruntime.OpenFileDialog(ctx, options)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	return a.store.Update(id, func(it *store.Item) { it.Output = path })
}

// RestoreOutput writes the app's copy of a transcript back out as a .md and
// points the item at it. It goes where a new result would (next to the
// source, numbered rather than replacing a file); when that folder is gone
// too, the user picks a place. Canceling changes nothing.
func (a *App) RestoreOutput(id string) error {
	item, err := a.finishedItem(id)
	if err != nil {
		return err
	}
	if isFile(item.Output) {
		return nil // nothing to restore
	}
	archive := a.archivePath(id)
	if archive == "" {
		return i18n.Error("error.noArchive")
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		return i18n.Error("error.noArchive")
	}
	target := output.TargetPath(item.Source, item.Kind)
	var path string
	if isDir(filepath.Dir(target)) {
		path, err = output.WriteNew(target, string(data))
		if err != nil {
			return i18n.Error("error.saveFailed", "error", err)
		}
	} else {
		ctx := a.context()
		if ctx == nil {
			return errNotReady
		}
		path, err = wailsruntime.SaveFileDialog(ctx, wailsruntime.SaveDialogOptions{
			Title:           i18n.T("dialog.restoreTitle"),
			DefaultFilename: restoreName(item) + ".md",
			Filters:         mdFilter(),
		})
		if err != nil {
			return err
		}
		if path == "" {
			return nil
		}
		// The save dialog has already asked before replacing a file.
		if err := output.Write(path, string(data)); err != nil {
			return i18n.Error("error.saveFailed", "error", err)
		}
	}
	return a.store.Update(id, func(it *store.Item) { it.Output = path })
}

// restoreName is the item's name without the audio extension it was added
// with ("강의09.m4a" → "강의09"); a renamed item keeps its name as is.
func restoreName(item store.Item) string {
	if item.Kind == "file" {
		if ext := filepath.Ext(item.Source); ext != "" && strings.EqualFold(filepath.Ext(item.Name), ext) {
			return strings.TrimSuffix(item.Name, filepath.Ext(item.Name))
		}
	}
	return item.Name
}

// isDir reports whether path names an existing folder.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
	// Only a real change resizes: a repeated call would read the already
	// widened window and remember that width as the list width. The window's
	// own state decides, not the saved choice, so a failed save still fits
	// the window to the pane the user sees.
	changed := a.paneShown != open
	a.paneShown = open
	var err error
	if a.cfg != nil {
		err = a.cfg.SetPane(open)
	}
	a.mu.Unlock()
	if changed && a.context() != nil {
		a.fitWindow(open)
	}
	return err
}

// fitWindow resizes the window for the pane. A maximised or full-screen
// window keeps its size; only the minimum changes.
func (a *App) fitWindow(open bool) {
	ctx := a.context()
	setMinSize := func() {
		if open {
			wailsruntime.WindowSetMinSize(ctx, PaneMinWidth, WindowMinTall)
		} else {
			wailsruntime.WindowSetMinSize(ctx, ListMinWidth, WindowMinTall)
		}
	}
	if wailsruntime.WindowIsFullscreen(ctx) || wailsruntime.WindowIsMaximised(ctx) {
		setMinSize()
		return
	}
	// Windows widens a window the moment the minimum rises, so the width must
	// be read before WindowSetMinSize; afterwards it would already be the
	// widened one.
	width, height := wailsruntime.WindowGetSize(ctx)
	setMinSize()
	screen := screenWidth(ctx)
	a.mu.Lock()
	remember, target := paneWidths(open, width, screen, a.listWidth)
	a.listWidth = remember
	a.mu.Unlock()
	if target <= 0 {
		return
	}
	wailsruntime.WindowSetSize(ctx, target, height)
	// Keep the widened window on screen.
	if open {
		if x, y := wailsruntime.WindowGetPosition(ctx); screen > 0 && x+target > screen {
			wailsruntime.WindowSetPosition(ctx, max(0, screen-target), y)
		}
	}
}

// paneWidths decides the widths for showing or hiding the pane, without
// touching the window. width is the window width read before any minimum-size
// change. screen is the screen width, or 0 when unknown. listWidth is the
// remembered list-only width. remember is the width to keep as the list-only
// width; target is the width to resize to, or 0 to keep the current size.
func paneWidths(open bool, width, screen, listWidth int) (remember, target int) {
	if open {
		// Remember even a window that is already wide enough, so closing
		// restores the width the user chose.
		remember = width
		target = PaneWidth
		if screen > 0 && target > screen {
			target = screen
		}
		if target <= width {
			target = 0
		}
		return remember, target
	}
	if listWidth <= 0 {
		listWidth = ListWidth
	}
	if width <= listWidth {
		return listWidth, 0
	}
	return listWidth, listWidth
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

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

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/media"
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

// tidyFinished runs once at startup over every finished item:
//
//   - A transcript outside the transcript folder (0.11 and earlier saved it
//     next to the recording) is copied in and the item points at the copy.
//     The copy comes from the app's own copy, so the old folder (often in
//     Desktop or Documents, which make macOS ask permission) is not read;
//     the old file stays where it is and the app forgets it.
//   - A transcript with no app copy yet gets one.
//   - The recording time is read once from audio still on disk and kept
//     (store.Item.RecordedFrom), so the week and sorting never read it again.
func (a *App) tidyFinished() {
	if a.store == nil {
		return
	}
	moved := map[string]string{} // old .md -> its copy, for duplicates
	for _, item := range a.store.List("") {
		if item.State != "done" || item.Output == "" {
			continue
		}
		if a.OutputDir != "" && !output.Inside(a.OutputDir, item.Output) {
			if path, ok := moved[item.Output]; ok {
				_ = a.store.Update(item.ID, func(it *store.Item) { it.Output = path })
				item.Output = path
			} else if path, err := a.copyIn(item); err == nil {
				moved[item.Output] = path
				_ = a.store.Update(item.ID, func(it *store.Item) { it.Output = path })
				item.Output = path
			}
		}
		a.archiveOutput(item)
		if item.RecordedFrom.IsZero() {
			if from, to, ok := media.RecordedSpan(item.Source); ok {
				_ = a.store.Update(item.ID, func(it *store.Item) { it.RecordedFrom, it.RecordedTo = from, to })
			}
		}
	}
}

// copyIn writes item's transcript into the transcript folder under the
// .md's own name and returns the new path. The text is the app's copy, or
// the .md itself when there is no copy (an item finished before copies
// were kept).
func (a *App) copyIn(item store.Item) (string, error) {
	data, err := os.ReadFile(a.archivePath(item.ID))
	if err != nil {
		if data, err = os.ReadFile(item.Output); err != nil {
			return "", err
		}
	}
	return output.WriteNew(filepath.Join(a.transcriptDir(item), filepath.Base(item.Output)), string(data))
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
// points the item at it. It goes where the transcript belongs in the
// transcript folder (see transcriptDir), numbered rather than replacing a
// file.
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
	if a.OutputDir == "" {
		return errNotReady
	}
	target := filepath.Join(a.transcriptDir(item), restoreName(item)+".md")
	path, err := output.WriteNew(target, string(data))
	if err != nil {
		return i18n.Error("error.saveFailed", "error", err)
	}
	return a.updateTranscript(item, func(it *store.Item) { it.Output = path })
}

// transcriptDir is the folder an item's transcript belongs in: its
// lecture's folder in the transcript folder once sorted, otherwise the
// transcript folder itself.
func (a *App) transcriptDir(item store.Item) string {
	if item.Sorted == sortedMatched && item.Course != "" {
		return filepath.Join(a.OutputDir, item.Course)
	}
	return a.OutputDir
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

// ListGroup is how the history list is grouped: "date", "course" or "week".
func (a *App) ListGroup() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return config.GroupDate
	}
	return a.cfg.ListGroup()
}

// SetListGroup remembers the list grouping.
func (a *App) SetListGroup(group string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return errNoConfig
	}
	return a.cfg.SetListGroup(group)
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

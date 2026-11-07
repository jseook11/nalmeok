// Package app exposes the methods the Wails frontend calls.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jseook11/nalmeok/internal/codexasr"
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/media"
	"github.com/jseook11/nalmeok/internal/output"
	"github.com/jseook11/nalmeok/internal/store"
	"github.com/jseook11/nalmeok/internal/telemetry"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Runner owns transcription work. Phase 1 ships a stub; phase 2 replaces it.
type Runner interface {
	Start(ctx context.Context)
	// Add queues paths. keyID pins the items to one key; empty means
	// automatic by priority.
	Add(paths []string, mode, keyID string) error
	// AddWithPDFs adds one folder with the reference PDFs the user picked
	// (empty = none) and keeps that choice on the item.
	AddWithPDFs(path string, pdfs []string, mode, keyID string) error
	Cancel(id string) error
	Retry(id string) error
	SendToBatch(id string) error
	Remove(id string) error
}

// Item is the frontend-facing history item: the stored item (its fields are
// flattened into the same JSON object) plus the pinned key's current label.
// KeyLabel is empty when KeyID is empty or names a deleted key.
type Item struct {
	store.Item
	KeyLabel string `json:"KeyLabel"`
	// OutputMissing is set on a finished item whose .md is no longer where
	// Output says (moved, renamed or deleted outside the app).
	OutputMissing bool `json:"OutputMissing"`
}

// KeyView is a key as shown in settings. It never contains the full API key.
type KeyView struct {
	ID           string `json:"ID"`
	Label        string `json:"Label"`
	Tier         string `json:"Tier"`
	Masked       string `json:"Masked"`
	LimitMinutes int    `json:"LimitMinutes"`
	Source       string `json:"Source"`
}

// ModeState is the processing mode in effect and which tiers have keys. Mode
// can differ from the stored preference when only one tier is registered; the
// stored preference itself is left untouched.
type ModeState struct {
	Mode string `json:"Mode"`
	Free bool   `json:"Free"`
	Paid bool   `json:"Paid"`
	// Backend is the transcription backend new items use.
	Backend string `json:"Backend"`
	// Codex is the local Codex login state: "ok", "expired" or "missing".
	Codex string `json:"Codex,omitempty"`
	// CodexPath is the auth file the state came from, when known.
	CodexPath string `json:"CodexPath,omitempty"`
	// CodexForced is true when NALMEOK_BACKEND fixes the backend.
	CodexForced bool `json:"CodexForced,omitempty"`
	// Experiment reports whether the Codex setting should be visible.
	Experiment bool `json:"Experiment,omitempty"`
}

// CodexStatus is the local Codex login as the settings sheet shows it.
type CodexStatus struct {
	State string `json:"State"` // "ok", "expired" or "missing"
	Path  string `json:"Path"`
}

var (
	errNotReady  = i18n.Error("error.notReady")
	errNoOutput  = i18n.Error("error.noOutput")
	errNoConfig  = i18n.Error("error.noConfig")
	errNoKey     = i18n.Error("error.noKey")
	errPinnedKey = i18n.Error("error.pinnedKeyMissing")
	errEnvKey    = i18n.Error("error.envKeyLocked")
)

// App is the object bound to the frontend.
type App struct {
	cfg    *config.Config
	store  *store.Store
	runner Runner

	ctx atomicContext

	// quitConfirmed is set when the user chooses Quit on the in-app card.
	// runtime.Quit calls BeforeClose again, and that must not ask a second
	// time.
	quitConfirmed atomic.Bool

	mu sync.Mutex // serializes config reads and writes

	// newItemKey is the toolbar key picker's choice for newly added items
	// ("" = automatic). It lives in memory only, so each launch starts on
	// automatic. Guarded by mu.
	newItemKey string

	// listWidth is the window width before the transcript pane widened it,
	// restored when the pane closes. Guarded by mu.
	listWidth int

	// paneShown is whether the window is currently sized for the pane. It
	// starts from the saved choice, since main opens the window at that
	// width. Guarded by mu.
	paneShown bool

	// pdf holds folders waiting for the user to pick their reference PDFs
	// (pdfchoice.go).
	pdf pdfChoices

	// ValidateKey is an optional, non-billing check run before saving a key.
	// Phase 2 sets this to a models.get call; it is nil in phase 1.
	ValidateKey func(ctx context.Context, key string) error

	// TranscriptsDir holds the app's copy of each finished transcript
	// (output.ArchivePath), read when the .md next to the source is gone.
	// main sets it before Startup; empty keeps no copies.
	TranscriptsDir string

	// Telemetry sends the anonymous free-key health signals and reads the
	// shared status. main sets it; nil (preview mode, tests) sends nothing.
	Telemetry *telemetry.Client

	// BackendFunc returns the backend new items should use. main points it at
	// the live settings (and NALMEOK_BACKEND); nil uses cfg.Backend().
	BackendFunc func() string

	// BackendForced is true when NALMEOK_BACKEND fixes the backend, so the
	// settings toggle must not change it.
	BackendForced bool

	// Experiment reveals the Codex setting even when config.json does not
	// carry the experiments flag (NALMEOK_EXPERIMENTS).
	Experiment bool

	// CodexAuth reads the local Codex login. nil reads the default file.
	CodexAuth func() (codexasr.Auth, error)
}

// New wires the app. The store's change callback emits "items:changed".
func New(cfg *config.Config, st *store.Store, runner Runner) *App {
	a := &App{cfg: cfg, store: st, runner: runner, paneShown: cfg != nil && cfg.Pane()}
	if st != nil {
		st.SetOnChange(a.emitItemsChanged)
	}
	return a
}

// Startup is called by Wails once the window exists.
func (a *App) Startup(ctx context.Context) {
	a.ctx.set(ctx)
	if a.store != nil {
		a.store.SetOnChange(a.emitItemsChanged)
	}
	wailsruntime.OnFileDrop(ctx, func(_ int, _ int, paths []string) {
		if len(paths) == 0 {
			return
		}
		if err := a.AddPaths(paths); err != nil {
			// A drop has no caller to return to; the window says why.
			wailsruntime.EventsEmit(ctx, "items:addFailed", err.Error(), !a.hasKeys())
		}
	})
	if a.runner != nil {
		a.runner.Start(ctx)
	}
	go a.archiveFinished()
}

// Shutdown is called by Wails when the window closes. A runner that holds
// resources (the real one keeps the log file open) is closed here.
func (a *App) Shutdown(context.Context) {
	if c, ok := a.runner.(io.Closer); ok {
		_ = c.Close()
	}
}

// BeforeClose asks before quitting while a free item is being transcribed:
// quitting stops it mid-way (it resumes on the next launch). A Batch job runs
// on Google's side and is fetched later, so it never asks. Every way out (the
// close button, ⌘Q, ⌘W) goes through here. It returns true to stay open.
//
// The window asks with its own card ("quit:confirm") rather than an OS dialog,
// so the app's icon and button names are kept on every platform.
func (a *App) BeforeClose(ctx context.Context) bool {
	// A window that never started (the CLI holds the folder lock) runs
	// nothing of its own, so it closes without asking.
	if a.context() == nil || a.store == nil || !liveRunning(a.store.List("")) {
		return false
	}
	// The user already chose Quit; runtime.Quit calls this again.
	if a.quitConfirmed.Load() {
		return false
	}
	// Windows calls this on the UI thread, so window calls go beside it
	// instead of blocking the close. The card asks from the front window.
	go func() {
		wailsruntime.WindowUnminimise(ctx)
		wailsruntime.Show(ctx)
		wailsruntime.WindowShow(ctx)
	}()
	wailsruntime.EventsEmit(ctx, "quit:confirm")
	return true
}

// ConfirmQuit closes the app after the user chose Quit on the in-app card.
// The choice is marked first because runtime.Quit calls BeforeClose again.
func (a *App) ConfirmQuit() {
	ctx := a.context()
	if ctx == nil {
		return
	}
	a.quitConfirmed.Store(true)
	wailsruntime.Quit(ctx)
}

// liveRunning reports whether a free item is in progress.
func liveRunning(items []store.Item) bool {
	for _, item := range items {
		if item.State == "running" && item.Mode == "free" {
			return true
		}
	}
	return false
}

func (a *App) context() context.Context {
	return a.ctx.get()
}

// UseLanguage switches the catalog language to the one the window picked and
// returns the language now in use. The window knows the OS language even when
// the process has no locale environment variable (macOS apps launched from
// Finder, for example), so it decides and Go follows.
//
// The menu bar was built before the window loaded, in the language main
// guessed from the environment, so it is rebuilt when the language changes.
func (a *App) UseLanguage(lang string) string {
	before := i18n.Language()
	if err := i18n.SetLanguage(lang); err != nil {
		return i18n.Language()
	}
	if i18n.Language() != before {
		a.refreshMenu()
	}
	return i18n.Language()
}

type atomicContext struct {
	mu  sync.RWMutex
	ctx context.Context
}

func (c *atomicContext) set(ctx context.Context) {
	c.mu.Lock()
	c.ctx = ctx
	c.mu.Unlock()
}

func (c *atomicContext) get() context.Context {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.ctx
}

func (a *App) emitItemsChanged() {
	ctx := a.context()
	if ctx == nil {
		return
	}
	wailsruntime.EventsEmit(ctx, "items:changed")
}

// ListItems returns the history, newest first, filtered by query.
func (a *App) ListItems(query string) []Item {
	if a.store == nil {
		return []Item{}
	}
	labels := a.keyLabels()
	stored := a.store.List(query)
	items := make([]Item, len(stored))
	for i, it := range stored {
		items[i] = Item{Item: it, OutputMissing: it.State == "done" && it.Output != "" && !isFile(it.Output)}
		if it.KeyID != "" {
			items[i].KeyLabel = labels[it.KeyID]
		}
	}
	return items
}

func (a *App) keyLabels() map[string]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	labels := map[string]string{}
	if a.cfg == nil {
		return labels
	}
	for _, key := range a.cfg.AllKeys() {
		labels[key.ID] = key.Label
	}
	return labels
}

// findKeyLocked looks up a key (saved or environment) by ID. a.mu must be held.
func (a *App) findKeyLocked(id string) (config.Key, bool) {
	if a.cfg == nil {
		return config.Key{}, false
	}
	for _, key := range a.cfg.AllKeys() {
		if key.ID == id {
			return key, true
		}
	}
	return config.Key{}, false
}

// AddPaths hands dropped or picked paths to the runner using the saved mode
// and the toolbar's key choice (see SetNewItemKey).
func (a *App) AddPaths(paths []string) error {
	return a.AddPathsWithKey(paths, a.NewItemKey())
}

// SetNewItemKey sets the toolbar key picker: files dropped or picked from now
// on are pinned to keyID. An empty keyID means automatic (priority order).
func (a *App) SetNewItemKey(keyID string) error {
	keyID = strings.TrimSpace(keyID)
	a.mu.Lock()
	defer a.mu.Unlock()
	if keyID != "" {
		if _, ok := a.findKeyLocked(keyID); !ok {
			return errPinnedKey
		}
	}
	a.newItemKey = keyID
	return nil
}

// NewItemKey returns the toolbar key picker's choice. A key deleted since it
// was chosen falls back to automatic ("").
func (a *App) NewItemKey() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.newItemKey != "" {
		if _, ok := a.findKeyLocked(a.newItemKey); !ok {
			a.newItemKey = ""
		}
	}
	return a.newItemKey
}

// AddPathsWithKey is AddPaths pinned to one key. An empty keyID is automatic.
// A pinned key's tier decides the mode: free key → free, paid key → Batch.
func (a *App) AddPathsWithKey(paths []string, keyID string) error {
	if a.runner == nil {
		return errNotReady
	}
	cleaned := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path != "" {
			cleaned = append(cleaned, path)
		}
	}
	if len(cleaned) == 0 {
		return i18n.Error("error.nothingToAdd")
	}
	keyID = strings.TrimSpace(keyID)
	if keyID != "" {
		a.mu.Lock()
		_, ok := a.findKeyLocked(keyID)
		a.mu.Unlock()
		if !ok {
			return errPinnedKey
		}
	}
	mode := a.GetMode()
	// Folders with several PDFs wait for the PDF sheet; the rest start now.
	cleaned = a.holdPDFChoices(cleaned, mode, keyID)
	if len(cleaned) == 0 {
		return nil
	}
	return a.runner.Add(cleaned, mode, keyID)
}

// SetItemKey pins a finished-with-error, canceled or free_exhausted item to
// one key (empty = automatic). It takes effect on the next Retry. Pinning
// also sets the item's mode from the key's tier so the row shows the right
// badge before the retry.
func (a *App) SetItemKey(id, keyID string) error {
	if a.store == nil {
		return store.ErrNotFound
	}
	item, ok := a.store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	// Codex items have no keys. A Gemini item (including a Batch item) keeps
	// its key choice even while Codex is the free backend.
	if item.Backend == config.BackendCodex {
		return i18n.Error("error.codexNoKeys")
	}
	keyID = strings.TrimSpace(keyID)
	mode := ""
	if keyID != "" {
		a.mu.Lock()
		key, ok := a.findKeyLocked(keyID)
		a.mu.Unlock()
		if !ok {
			return errPinnedKey
		}
		mode = "batch"
		if key.Tier == "free" {
			mode = "free"
		}
	}
	switch item.State {
	case "failed", "canceled", "free_exhausted":
	default:
		return i18n.Error("error.keyChangeLocked")
	}
	return a.store.Update(id, func(it *store.Item) {
		it.KeyID = keyID
		if mode != "" {
			it.Mode = mode
		}
	})
}

// PickFiles opens the OS file picker and adds the selection with the
// toolbar's key choice.
func (a *App) PickFiles() error {
	return a.PickFilesWithKey(a.NewItemKey())
}

// PickFilesWithKey opens the OS file picker and adds the selection pinned to
// keyID ("" = automatic).
func (a *App) PickFilesWithKey(keyID string) error {
	ctx := a.context()
	if ctx == nil {
		return errNotReady
	}
	paths, err := wailsruntime.OpenMultipleFilesDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: i18n.T("dialog.pickTitle"),
		Filters: []wailsruntime.FileFilter{
			{DisplayName: i18n.T("dialog.audioFiles"), Pattern: "*." + strings.Join(media.Extensions(), ";*.")},
		},
	})
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	return a.AddPathsWithKey(paths, keyID)
}

// Cancel stops an in-flight item.
func (a *App) Cancel(id string) error {
	if a.runner == nil {
		return errNotReady
	}
	return a.runner.Cancel(id)
}

// Retry reruns a failed item.
func (a *App) Retry(id string) error {
	if a.runner == nil {
		return errNotReady
	}
	return a.runner.Retry(id)
}

// SendToBatch moves the remaining free-tier work to a paid Batch job.
func (a *App) SendToBatch(id string) error {
	if a.runner == nil {
		return errNotReady
	}
	return a.runner.SendToBatch(id)
}

// Remove stops any running work, deletes the item's temporary work folder,
// drops it from the history and then deletes the app's copy of its
// transcript. Source and output stay on disk.
func (a *App) Remove(id string) error {
	if a.store == nil {
		return store.ErrNotFound
	}
	if a.runner != nil {
		if err := a.runner.Remove(id); err != nil {
			return err
		}
	}
	if err := a.store.Remove(id); err != nil {
		return err
	}
	if archive := a.archivePath(id); archive != "" {
		_ = os.Remove(archive)
	}
	return nil
}

// Rename changes an item's name. For a finished item the .md on disk is
// renamed to "<name>.md" in the same folder (numbered if taken); other items
// only change the name shown in the list.
func (a *App) Rename(id, name string) error {
	if a.store == nil {
		return store.ErrNotFound
	}
	name = strings.TrimSpace(name)
	if strings.EqualFold(filepath.Ext(name), ".md") {
		name = strings.TrimSpace(name[:len(name)-len(".md")])
	}
	switch {
	case name == "" || name == "." || name == "..":
		return i18n.Error("error.nameRequired")
	case strings.ContainsAny(name, `/\:`):
		return i18n.Error("error.nameBadChars")
	}
	item, ok := a.store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	if item.State != "done" || item.Output == "" {
		return a.store.Update(id, func(it *store.Item) { it.Name = name })
	}

	if !isFile(item.Output) {
		return errNoOutput
	}
	target := filepath.Join(filepath.Dir(item.Output), name+".md")
	// A case-only change names the same file on case-insensitive disks.
	if !strings.EqualFold(target, item.Output) {
		target = output.UniquePath(target)
	}
	if target != item.Output {
		if err := os.Rename(item.Output, target); err != nil {
			return i18n.Wrap("error.renameFailed", err)
		}
	}
	return a.store.Update(id, func(it *store.Item) {
		it.Name = strings.TrimSuffix(filepath.Base(target), ".md")
		it.Output = target
	})
}

// OpenOutput opens the finished .md with the OS default application.
func (a *App) OpenOutput(id string) error {
	path, err := a.readyOutput(id)
	if err != nil {
		return err
	}
	if err := openPath(path); err != nil {
		return i18n.Wrap("error.openFailed", err)
	}
	return nil
}

// RevealOutput shows the finished .md in Finder or Explorer.
func (a *App) RevealOutput(id string) error {
	path, err := a.readyOutput(id)
	if err != nil {
		return err
	}
	if err := revealPath(path); err != nil {
		return i18n.Wrap("error.revealFailed", err)
	}
	return nil
}

// CopyOutput puts the whole transcript on the clipboard: the .md, or the
// app's copy when the .md is gone.
func (a *App) CopyOutput(id string) error {
	item, err := a.finishedItem(id)
	if err != nil {
		return err
	}
	ctx := a.context()
	if ctx == nil {
		return errNotReady
	}
	text, err := a.outputText(item)
	if err != nil {
		return err
	}
	if err := wailsruntime.ClipboardSetText(ctx, text); err != nil {
		return i18n.Wrap("error.copyFailed", err)
	}
	return nil
}

func (a *App) readyOutput(id string) (string, error) {
	if a.store == nil {
		return "", store.ErrNotFound
	}
	item, ok := a.store.Get(id)
	if !ok {
		return "", store.ErrNotFound
	}
	if item.Output == "" || !isFile(item.Output) {
		return "", errNoOutput
	}
	return item.Output, nil
}

// isFile reports whether path names an existing regular file (not a folder).
func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// backend returns the backend new items use: the live setting, or
// cfg.Backend() when main did not install a function.
func (a *App) backend() string {
	if a.BackendFunc != nil {
		return a.BackendFunc()
	}
	if a.cfg == nil {
		return config.BackendGemini
	}
	return a.cfg.Backend()
}

// codexAuth reads the local Codex login.
func (a *App) codexAuth() (codexasr.Auth, error) {
	if a.CodexAuth != nil {
		return a.CodexAuth()
	}
	return codexasr.Find()
}

// codexState returns "ok", "expired" or "missing" and the auth file path.
func (a *App) codexState() (string, string) {
	auth, err := a.codexAuth()
	if err != nil {
		return "missing", ""
	}
	if auth.Expired(time.Now()) {
		return "expired", auth.Path
	}
	return "ok", auth.Path
}

// CodexStatus reports the local Codex login for the settings sheet.
func (a *App) CodexStatus() CodexStatus {
	state, path := a.codexState()
	return CodexStatus{State: state, Path: path}
}

// CheckCodex re-reads the login, so a sign-in done outside the app shows up
// without reopening the settings sheet.
func (a *App) CheckCodex() CodexStatus {
	return a.CodexStatus()
}

// Backend reports the transcription backend new items use.
func (a *App) Backend() string {
	return a.backend()
}

// SetBackend stores the backend for new items. NALMEOK_BACKEND wins.
func (a *App) SetBackend(name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return errNoConfig
	}
	if a.BackendForced {
		return i18n.Error("error.backendForced")
	}
	return a.cfg.SetBackend(name)
}

// effectiveMode picks the mode new items use: the only available tier wins,
// otherwise the stored preference.
func effectiveMode(stored string, free, paid bool) string {
	switch {
	case free && !paid:
		return "free"
	case paid && !free:
		return "batch"
	}
	if stored == "batch" {
		return "batch"
	}
	return "free"
}

// freeLaneSet reports whether the free lane counts when picking the mode.
// Gemini needs a free API key. Codex always counts: a missing or expired
// login is fixed by logging in again, so the stored mode stays and the add
// stops with the login note instead of moving new files to paid Batch.
func (a *App) freeLaneSet(freeKeys bool) bool {
	return freeKeys || a.backend() == config.BackendCodex
}

// GetMode returns the mode the next drop will use.
func (a *App) GetMode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return "free"
	}
	free, paid := a.cfg.Tiers()
	return effectiveMode(a.cfg.Mode(), a.freeLaneSet(free), paid)
}

// ModeState reports the effective mode, which tiers are usable, and which
// backend the free lane uses. The codex backend's free lane depends on its
// login, not on Gemini keys.
func (a *App) ModeState() ModeState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return ModeState{Mode: "free", Backend: config.BackendGemini}
	}
	backend := a.backend()
	freeKeys, paid := a.cfg.Tiers()
	experiment := a.Experiment || a.cfg.Experiment("codex")
	state := ModeState{
		Free:        freeKeys,
		Paid:        paid,
		Backend:     backend,
		Experiment:  experiment,
		CodexForced: a.BackendForced,
	}
	// The settings sheet shows the login state even while Gemini is selected,
	// so the user can see whether Codex is ready before switching.
	if experiment || backend == config.BackendCodex || a.BackendForced {
		state.Codex, state.CodexPath = a.codexState()
	}
	state.Mode = effectiveMode(a.cfg.Mode(), a.freeLaneSet(freeKeys), paid)
	if backend == config.BackendCodex {
		state.Free = state.Codex == "ok"
	}
	return state
}

// hasKeys reports whether any tier has a key.
func (a *App) hasKeys() bool {
	state := a.ModeState()
	return state.Free || state.Paid
}

// SetMode stores the processing mode for the next drop.
func (a *App) SetMode(mode string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return errNoConfig
	}
	if mode != "free" && mode != "batch" {
		return i18n.Error("error.badMode")
	}
	return a.cfg.SetMode(mode)
}

// SignalsEnabled reports whether anonymous free-key health signals are sent.
func (a *App) SignalsEnabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return true
	}
	return a.cfg.SignalsEnabled()
}

// SetSignalsEnabled stores the signals setting. The telemetry client reads
// config.json on every event, so the change applies right away.
func (a *App) SetSignalsEnabled(on bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return errNoConfig
	}
	return a.cfg.SetSignalsEnabled(on)
}

// FreeStatus returns how well the free and Batch tiers work right now for
// everyone, as judged by the download site from all apps' signals.
func (a *App) FreeStatus() (telemetry.Status, error) {
	ctx := a.context()
	if ctx == nil {
		ctx = context.Background()
	}
	st, err := a.Telemetry.Status(ctx)
	if err != nil {
		return st, err
	}
	// No client (dev build, signals off by environment) gives an empty
	// status; the window shows it like a status without enough data.
	if st.Free.Level == "" {
		st.Free.Level = "unknown"
	}
	if st.Batch.Level == "" {
		st.Batch.Level = "unknown"
	}
	return st, nil
}

// Keys returns the read-only environment key (if any) first, then saved keys
// in priority order. The list order is the order keys are tried in.
func (a *App) Keys() []KeyView {
	a.mu.Lock()
	defer a.mu.Unlock()

	views := []KeyView{}
	if a.cfg == nil {
		return views
	}
	for _, key := range a.cfg.AllKeys() {
		source := "config"
		if key.ID == "env" && config.EnvKey() != nil {
			source = "env"
		}
		views = append(views, KeyView{
			ID:           key.ID,
			Label:        key.Label,
			Tier:         key.Tier,
			Masked:       maskKey(key.APIKey),
			LimitMinutes: key.LimitMinutes,
			Source:       source,
		})
	}
	return views
}

// AddKey validates and saves a key. Phase 1 only checks shape unless
// ValidateKey is set.
func (a *App) AddKey(label, tier, key string) error {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier != "free" && tier != "paid" {
		return i18n.Error("error.badTier")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return i18n.Error("error.pasteKey")
	}
	if a.cfg == nil {
		return errNoConfig
	}

	if a.ValidateKey != nil {
		ctx := a.context()
		if ctx == nil {
			ctx = context.Background()
		}
		if err := a.ValidateKey(ctx, key); err != nil {
			return i18n.Error("error.badKey")
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	label = strings.TrimSpace(label)
	if label == "" {
		label = a.autoLabelLocked(tier, key)
	}
	if _, err := a.cfg.AddKey(label, tier, key); err != nil {
		return err
	}
	a.emitKeysChanged()
	return nil
}

// autoLabelLocked names a new key "Free 1", "Paid 2" and so on, taking the
// smallest number not already used. Re-adding a saved key keeps its label.
func (a *App) autoLabelLocked(tier, key string) string {
	prefix := i18n.T("tier.free")
	if tier == "paid" {
		prefix = i18n.T("tier.paid")
	}
	used := make(map[string]bool)
	for _, saved := range a.cfg.SavedKeys() {
		if saved.APIKey == key && saved.Tier == tier && strings.TrimSpace(saved.Label) != "" {
			return saved.Label
		}
		used[saved.Label] = true
	}
	for n := 1; ; n++ {
		label := fmt.Sprintf("%s %d", prefix, n)
		if !used[label] {
			return label
		}
	}
}

// DeleteKey removes a saved key. The environment key cannot be deleted here.
func (a *App) DeleteKey(id string) error {
	if strings.TrimSpace(id) == "" {
		return errNoKey
	}
	if id == "env" && config.EnvKey() != nil {
		return i18n.Error("error.envKeyNoDelete")
	}
	if a.cfg == nil {
		return errNoConfig
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	deleted, err := a.cfg.DeleteKey(id)
	if err != nil {
		return err
	}
	if !deleted {
		return errNoKey
	}
	a.emitKeysChanged()
	return nil
}

func (a *App) isEnvKey(id string) bool {
	if id != "env" || config.EnvKey() == nil {
		return false
	}
	for _, key := range a.cfg.SavedKeys() {
		if key.ID == id {
			return false
		}
	}
	return true
}

// emitKeysChanged tells the window that labels, tiers or order changed. The
// mode toggle should re-read ModeState on this event.
func (a *App) emitKeysChanged() {
	ctx := a.context()
	if ctx == nil {
		return
	}
	wailsruntime.EventsEmit(ctx, "keys:changed")
}

// RenameKey changes a saved key's label. A label already used by another key
// gets a number ("Free 12").
func (a *App) RenameKey(id, label string) error {
	if a.cfg == nil {
		return errNoConfig
	}
	a.mu.Lock()
	if a.isEnvKey(id) {
		a.mu.Unlock()
		return errEnvKey
	}
	_, err := a.cfg.RenameKey(id, label)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	a.emitKeysChanged()
	a.emitItemsChanged() // rows show the pinned key's label
	return nil
}

// SetKeyTier switches a saved key between "free" and "paid". The effective
// mode follows the new tiers (see ModeState); the stored preference is kept.
func (a *App) SetKeyTier(id, tier string) error {
	if a.cfg == nil {
		return errNoConfig
	}
	a.mu.Lock()
	if a.isEnvKey(id) {
		a.mu.Unlock()
		return errEnvKey
	}
	_, err := a.cfg.SetKeyTier(id, tier)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	a.emitKeysChanged()
	return nil
}

// SetKeyOrder sets the key priority. ids lists the saved keys (Source
// "config") from highest to lowest priority, each exactly once. The
// environment key cannot be moved; it always stays first.
func (a *App) SetKeyOrder(ids []string) error {
	if a.cfg == nil {
		return errNoConfig
	}
	a.mu.Lock()
	for _, id := range ids {
		if a.isEnvKey(id) {
			a.mu.Unlock()
			return errEnvKey
		}
	}
	// A saved key that duplicates the environment key is hidden from Keys(),
	// so the window cannot list it. Keep it at the end.
	listed := make(map[string]bool, len(ids))
	for _, id := range ids {
		listed[id] = true
	}
	visible := map[string]bool{}
	for _, key := range a.cfg.AllKeys() {
		visible[key.ID] = true
	}
	full := append([]string(nil), ids...)
	for _, key := range a.cfg.SavedKeys() {
		if !listed[key.ID] && !visible[key.ID] {
			full = append(full, key.ID)
		}
	}
	err := a.cfg.SetKeyOrder(full)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	a.emitKeysChanged()
	return nil
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "••••"
	}
	return "••••" + key[len(key)-4:]
}

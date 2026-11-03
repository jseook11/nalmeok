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

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/output"
	"github.com/jseook11/nalmeok/internal/store"
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

	mu sync.Mutex // serializes config reads and writes

	// newItemKey is the toolbar key picker's choice for newly added items
	// ("" = automatic). It lives in memory only, so each launch starts on
	// automatic. Guarded by mu.
	newItemKey string

	// listWidth is the window width before the transcript pane widened it,
	// restored when the pane closes. Guarded by mu.
	listWidth int

	// pdf holds folders waiting for the user to pick their reference PDFs
	// (pdfchoice.go).
	pdf pdfChoices

	// ValidateKey is an optional, non-billing check run before saving a key.
	// Phase 2 sets this to a models.get call; it is nil in phase 1.
	ValidateKey func(ctx context.Context, key string) error
}

// New wires the app. The store's change callback emits "items:changed".
func New(cfg *config.Config, st *store.Store, runner Runner) *App {
	a := &App{cfg: cfg, store: st, runner: runner}
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
}

// Shutdown is called by Wails when the window closes. A runner that holds
// resources (the real one keeps the log file open) is closed here.
func (a *App) Shutdown(context.Context) {
	if c, ok := a.runner.(io.Closer); ok {
		_ = c.Close()
	}
}

func (a *App) context() context.Context {
	return a.ctx.get()
}

// UseLanguage switches the catalog language to the one the window picked and
// returns the language now in use. The window knows the OS language even when
// the process has no locale environment variable (macOS apps launched from
// Finder, for example), so it decides and Go follows.
func (a *App) UseLanguage(lang string) string {
	if err := i18n.SetLanguage(lang); err != nil {
		return i18n.Language()
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
		items[i] = Item{Item: it}
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
	item, ok := a.store.Get(id)
	if !ok {
		return store.ErrNotFound
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
			{DisplayName: i18n.T("dialog.audioFiles"), Pattern: "*.m4a;*.mp4;*.mp3;*.wav"},
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

// Remove stops any running work, deletes the item's temporary work folder and
// then drops it from the history. Source and output stay on disk.
func (a *App) Remove(id string) error {
	if a.store == nil {
		return store.ErrNotFound
	}
	if a.runner != nil {
		if err := a.runner.Remove(id); err != nil {
			return err
		}
	}
	return a.store.Remove(id)
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

	if info, err := os.Stat(item.Output); err != nil || info.IsDir() {
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

// CopyOutput puts the whole .md on the clipboard.
func (a *App) CopyOutput(id string) error {
	path, err := a.readyOutput(id)
	if err != nil {
		return err
	}
	ctx := a.context()
	if ctx == nil {
		return errNotReady
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return i18n.Error("error.readOutputFailed")
	}
	if err := wailsruntime.ClipboardSetText(ctx, string(data)); err != nil {
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
	if item.Output == "" {
		return "", errNoOutput
	}
	if info, err := os.Stat(item.Output); err != nil || info.IsDir() {
		return "", errNoOutput
	}
	return item.Output, nil
}

// GetMode returns the mode the next drop will use: the stored preference when
// both tiers have keys, or the only available tier.
func (a *App) GetMode() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return "free"
	}
	return a.cfg.EffectiveMode()
}

// ModeState reports the effective mode and which tiers have keys, so the
// toggle can select the only available tier without changing the stored
// preference.
func (a *App) ModeState() ModeState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return ModeState{Mode: "free"}
	}
	free, paid := a.cfg.Tiers()
	return ModeState{Mode: a.cfg.EffectiveMode(), Free: free, Paid: paid}
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

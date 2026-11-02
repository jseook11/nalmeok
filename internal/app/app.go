// Package app exposes the methods the Wails frontend calls.
package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/output"
	"github.com/jseook11/nalmeok/internal/store"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Runner owns transcription work. Phase 1 ships a stub; phase 2 replaces it.
type Runner interface {
	Start(ctx context.Context)
	Add(paths []string, mode string) error
	Cancel(id string) error
	Retry(id string) error
	SendToBatch(id string) error
	Remove(id string) error
}

// Item is the frontend-facing history item.
type Item = store.Item

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
	errNotReady = errors.New("아직 준비 중이에요")
	errNoOutput = errors.New("결과 파일이 없어요")
	errNoConfig = errors.New("설정을 불러오지 못했어요")
	errNoKey    = errors.New("키가 없어요")
)

// App is the object bound to the frontend.
type App struct {
	cfg    *config.Config
	store  *store.Store
	runner Runner

	ctx atomicContext

	mu sync.Mutex // serializes config reads and writes

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
		if len(paths) > 0 {
			_ = a.AddPaths(paths)
		}
	})
	if a.runner != nil {
		a.runner.Start(ctx)
	}
}

// Shutdown is called by Wails when the window closes.
func (a *App) Shutdown(context.Context) {}

func (a *App) context() context.Context {
	return a.ctx.get()
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
	return a.store.List(query)
}

// AddPaths hands dropped or picked paths to the runner using the saved mode.
func (a *App) AddPaths(paths []string) error {
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
		return errors.New("추가할 파일이 없어요")
	}
	return a.runner.Add(cleaned, a.GetMode())
}

// PickFiles opens the OS file picker and adds the selection.
func (a *App) PickFiles() error {
	ctx := a.context()
	if ctx == nil {
		return errNotReady
	}
	paths, err := wailsruntime.OpenMultipleFilesDialog(ctx, wailsruntime.OpenDialogOptions{
		Title: "음성 파일 선택",
		Filters: []wailsruntime.FileFilter{
			{DisplayName: "오디오 파일", Pattern: "*.m4a;*.mp4;*.mp3;*.wav"},
		},
	})
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	return a.AddPaths(paths)
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
		return errors.New("이름을 입력하세요")
	case strings.ContainsAny(name, `/\:`):
		return errors.New(`이름에 / \ : 는 쓸 수 없어요`)
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
			return fmt.Errorf("이름을 바꾸지 못했어요: %w", err)
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
		return fmt.Errorf("결과 파일을 열지 못했어요: %w", err)
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
		return fmt.Errorf("폴더를 열지 못했어요: %w", err)
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
		return errors.New("결과 파일을 읽지 못했어요")
	}
	if err := wailsruntime.ClipboardSetText(ctx, string(data)); err != nil {
		return fmt.Errorf("복사하지 못했어요: %w", err)
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

// SetMode stores the processing mode for the next drop.
func (a *App) SetMode(mode string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return errNoConfig
	}
	if mode != "free" && mode != "batch" {
		return errors.New("처리 방식이 올바르지 않아요")
	}
	return a.cfg.SetMode(mode)
}

// Keys returns saved keys plus the read-only environment key.
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
		return errors.New("등급이 올바르지 않아요")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("API 키를 붙여넣으세요")
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
			return errors.New("키가 올바르지 않아요")
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
	return nil
}

// autoLabelLocked names a new key "무료 1", "유료 2" and so on, taking the
// smallest number not already used. Re-adding a saved key keeps its label.
func (a *App) autoLabelLocked(tier, key string) string {
	prefix := "무료"
	if tier == "paid" {
		prefix = "유료"
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
		return errors.New("환경변수 키는 지울 수 없어요")
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
	return nil
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "••••"
	}
	return "••••" + key[len(key)-4:]
}

func openPath(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Run()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Run()
	default:
		return exec.Command("xdg-open", path).Run()
	}
}

func revealPath(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", path).Run()
	case "windows":
		return exec.Command("explorer", "/select,"+path).Run()
	default:
		return exec.Command("xdg-open", filepath.Dir(path)).Run()
	}
}

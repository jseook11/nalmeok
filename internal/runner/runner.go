// Package runner turns dropped paths into .md files.
//
// Items run in two lanes, each a FIFO queue with one worker: free live work
// (scan → prepare → live calls → output) and paid Batch submission (scan →
// prepare → upload → submit). The lanes run side by side, so a Batch
// submission never waits behind a long free transcription. Another goroutine
// polls submitted Batch jobs. All progress lives in the store, so a restart
// resumes where the previous run stopped.
package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/output"
	"github.com/jseook11/nalmeok/internal/store"
)

// Item states.
const (
	StateRunning       = "running"
	StateBatchWaiting  = "batch_waiting"
	StateFreeExhausted = "free_exhausted"
	StateDone          = "done"
	StateFailed        = "failed"
	StateCanceled      = "canceled"
)

// Defaults that mirror the Python app.
const (
	DefaultUnitMaxSeconds = 130 * 60
	DefaultKeyGap         = 12 * time.Second
	DefaultPollInterval   = 30 * time.Second
	uploadMaxAge          = 47 * time.Hour
	transientRetries      = 5
	transientBaseDelay    = 10 * time.Second
	transientMaxDelay     = 2 * time.Minute
	retryDetail           = "Gemini 서버가 붐벼요 · 다시 시도 중"
	freeTPM               = 250_000
	freeRPM               = 5
	rateTokensPerSecond   = 32
	pdfName               = "reference.pdf"
	mimePDF               = "application/pdf"
)

// Input, Track, Piece and UnitPlan mirror the media and plan packages so the
// runner compiles and tests without them; media.go adapts the real ones.
type Input struct {
	Source string
	Kind   string
	Name   string
	Tracks []Track
	PDF    string
}

// Track is one source audio file.
type Track struct {
	Path    string
	Format  string
	Seconds float64
}

// Piece is one upload-ready audio file in the work directory.
type Piece struct {
	Path    string
	MIME    string
	Seconds float64
}

// UnitPlan is one planned request.
type UnitPlan struct {
	Index   int
	Pieces  []Piece
	Seconds float64
}

// Media scans inputs, prepares pieces and groups them into units.
type Media interface {
	Scan(path string) (Input, error)
	Prepare(t Track, dir string, maxSeconds float64) ([]Piece, error)
	// Units takes the pieces of each source file in order and returns one
	// request per file (long files may take several).
	Units(tracks [][]Piece, maxSeconds float64) []UnitPlan
}

// Options configures a Runner. Zero values get production defaults where a
// default makes sense.
type Options struct {
	Store     *store.Store
	Media     Media
	NewClient func(apiKey string) (gemini.Client, error)
	// Keys returns the current keys. The default re-reads config.json so keys
	// added in settings are picked up without sharing the app's Config.
	Keys func() []config.Key
	// WorkDir holds per-item work directories (<config dir>/work).
	WorkDir string
	// LogPath is the log file; empty disables logging.
	LogPath string
	// Notify shows an OS notification. nil uses the platform default.
	Notify func(title, message string)

	UnitMaxSeconds float64
	// KeyGap is the minimum time between request starts per free key. A
	// negative value disables waiting (tests).
	KeyGap       time.Duration
	PollInterval time.Duration
	// Backoff returns the wait before transient retry n (1-based).
	Backoff func(attempt int) time.Duration
	// Sleep waits for d or context cancellation. Tests replace it to keep the
	// retry schedule out of wall-clock time.
	Sleep func(ctx context.Context, d time.Duration) error
	Now   func() time.Time
}

// Runner implements app.Runner.
type Runner struct {
	opts    Options
	log     *log.Logger
	logFile *logFile

	mu        sync.Mutex
	lanes     map[string]*lane // by item mode: "free", "batch"
	pollNow   chan struct{}
	started   bool
	clients   map[string]gemini.Client
	uploads   map[string]store.Upload // keyID + "\x00" + path
	lastStart map[string]time.Time

	pollMu sync.Mutex // serializes Batch polling with Batch cancellation
}

// New returns a runner. Start must be called to begin work.
func New(opts Options) *Runner {
	if opts.UnitMaxSeconds <= 0 {
		opts.UnitMaxSeconds = DefaultUnitMaxSeconds
	}
	if opts.KeyGap == 0 {
		opts.KeyGap = DefaultKeyGap
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = DefaultPollInterval
	}
	if opts.Backoff == nil {
		opts.Backoff = defaultBackoff
	}
	if opts.Sleep == nil {
		opts.Sleep = sleep
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Keys == nil {
		opts.Keys = func() []config.Key {
			cfg, err := config.Load()
			if err != nil {
				return nil
			}
			return cfg.AllKeys()
		}
	}
	if opts.NewClient == nil {
		opts.NewClient = gemini.New
	}
	if opts.Notify == nil {
		opts.Notify = notify
	}
	r := &Runner{
		opts:      opts,
		lanes:     map[string]*lane{"free": newLane("free"), "batch": newLane("batch")},
		pollNow:   make(chan struct{}, 1),
		clients:   make(map[string]gemini.Client),
		uploads:   make(map[string]store.Upload),
		lastStart: make(map[string]time.Time),
	}
	r.log = log.New(io.Discard, "", log.LstdFlags)
	if opts.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.LogPath), 0o700); err == nil {
			if f, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				r.logFile = &logFile{f: f}
				r.log = log.New(r.logFile, "", log.LstdFlags)
			}
		}
	}
	return r
}

// Close releases the log file. Background goroutines may still be finishing;
// anything they log afterwards is dropped. Close is safe to call more than
// once. Windows cannot delete an open file, so callers close the runner on
// shutdown.
func (r *Runner) Close() error {
	if r.logFile == nil {
		return nil
	}
	return r.logFile.Close()
}

// logFile is the log destination. Writes after Close are dropped instead of
// failing, so a worker still unwinding after shutdown cannot hit a closed file.
type logFile struct {
	mu sync.Mutex
	f  *os.File
}

func (l *logFile) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return len(p), nil
	}
	return l.f.Write(p)
}

func (l *logFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Start resumes interrupted work and launches the lane workers and poller.
func (r *Runner) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	r.mu.Unlock()

	items := r.opts.Store.List("")
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.Before(items[j].CreatedAt) })
	for _, item := range items {
		if item.State == StateRunning {
			r.enqueue(item.ID, item.Mode)
		}
	}
	for _, l := range r.lanes {
		go r.worker(ctx, l)
	}
	go r.poller(ctx)
}

// Add creates one item per path and queues them. A non-empty keyID pins the
// items to that key, and the key's tier decides the mode (free key → free,
// paid key → batch). An empty keyID picks keys by priority.
func (r *Runner) Add(paths []string, mode, keyID string) error {
	if r.opts.Store == nil {
		return errors.New("아직 준비 중이에요")
	}
	keys := r.opts.Keys()
	free, paid := splitKeys(keys)
	if len(free) == 0 && len(paid) == 0 {
		return errors.New("API 키가 필요해요")
	}
	if mode != "batch" {
		mode = "free"
	}
	if mode == "free" && len(free) == 0 {
		mode = "batch"
	} else if mode == "batch" && len(paid) == 0 {
		mode = "free"
	}
	if keyID != "" {
		key, ok := findKey(keys, keyID)
		if !ok {
			return errPinnedMissing
		}
		mode = modeForTier(key.Tier)
	}

	for _, path := range paths {
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		kind := "file"
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			kind = "folder"
		}
		item := store.Item{
			ID:     store.NewID(),
			Source: path,
			Kind:   kind,
			Name:   filepath.Base(path),
			Mode:   mode,
			State:  StateRunning,
			Detail: "대기 중",
			KeyID:  keyID,
		}
		if err := r.opts.Store.Add(item); err != nil {
			return err
		}
		if keyID != "" {
			r.logf("item %s added: %s (%s, key %s)", item.ID, path, mode, keyID)
		} else {
			r.logf("item %s added: %s (%s)", item.ID, path, mode)
		}
		r.enqueue(item.ID, mode)
	}
	return nil
}

// Cancel stops a running item or cancels a submitted Batch.
func (r *Runner) Cancel(id string) error {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	switch item.State {
	case StateRunning, StateFreeExhausted:
		r.mu.Lock()
		for _, l := range r.lanes {
			l.remove(id)
		}
		r.stopActiveLocked(id)
		r.mu.Unlock()
		return r.update(id, func(it *store.Item) {
			it.State = StateCanceled
			it.Detail = "취소됨"
		})
	case StateBatchWaiting:
		r.pollMu.Lock()
		defer r.pollMu.Unlock()
		current, ok := r.opts.Store.Get(id)
		if !ok {
			return store.ErrNotFound
		}
		if current.State != StateBatchWaiting {
			return nil
		}
		// The job name is kept unless the server accepted the cancel, so a
		// later retry waits for that job instead of paying for a new one.
		forget := false
		if current.Batch != nil && current.Batch.Name != "" {
			if client, err := r.clientForKeyID(current.Batch.KeyID); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if err := client.CancelBatch(ctx, current.Batch.Name); err != nil {
					r.logf("item %s cancel batch: %v", id, r.redact(err))
					// Too late to cancel: the job is done and paid for. Keep
					// its results instead of throwing them away.
					if state, serr := client.BatchStatus(ctx, current.Batch.Name); serr == nil && state == gemini.BatchSucceeded {
						r.collectLocked(ctx, id, client, current.Batch.Name)
						return nil
					}
				} else {
					forget = true
				}
			}
		}
		return r.update(id, func(it *store.Item) {
			it.State = StateCanceled
			it.Detail = "취소됨"
			if forget && it.Batch != nil {
				it.Batch.Name = ""
			}
		})
	default:
		return errors.New("진행 중인 항목만 취소할 수 있어요")
	}
}

// Remove cancels any in-flight work for id and deletes its work directory.
// The caller is responsible for dropping the item from the store afterwards.
func (r *Runner) Remove(id string) error {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	switch item.State {
	case StateRunning, StateFreeExhausted, StateBatchWaiting:
		if err := r.Cancel(id); err != nil {
			r.logf("item %s remove: cancel: %v", id, r.redact(err))
		}
	}
	r.waitInactive(id, 2*time.Second)
	if r.opts.WorkDir == "" {
		return nil
	}
	if err := os.RemoveAll(r.workDir(id)); err != nil {
		return err
	}
	r.logf("item %s work dir removed", id)
	return nil
}

// waitInactive gives the worker a moment to stop touching an item's work
// directory after its context was canceled.
func (r *Runner) waitInactive(id string, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for {
		if !r.isActive(id) || !time.Now().Before(deadline) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Retry re-queues a failed, canceled or exhausted item, keeping finished units.
func (r *Runner) Retry(id string) error {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	switch item.State {
	case StateFailed, StateCanceled, StateFreeExhausted:
	default:
		return errors.New("실패하거나 취소한 항목만 다시 시도할 수 있어요")
	}
	mode := item.Mode
	keys := r.opts.Keys()
	free, paid := splitKeys(keys)
	if item.KeyID != "" {
		// A pinned key decides the mode. A deleted key is reported now rather
		// than silently switching to another (possibly paid) key.
		key, ok := findKey(keys, item.KeyID)
		if !ok {
			return errPinnedMissing
		}
		mode = modeForTier(key.Tier)
	} else if mode == "free" && len(free) == 0 && len(paid) > 0 {
		mode = "batch"
	} else if mode == "batch" && len(paid) == 0 && len(free) > 0 {
		mode = "free"
	}
	if err := r.update(id, func(it *store.Item) {
		it.Mode = mode
		it.State = StateRunning
		it.Detail = "대기 중"
		it.Progress = doneRatio(it.Units)
	}); err != nil {
		return err
	}
	r.enqueue(id, mode)
	return nil
}

// SendToBatch moves the unfinished units of a free item to Batch: one that
// ran out of free use, failed, was canceled, or is still running.
func (r *Runner) SendToBatch(id string) error {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	switch {
	case item.State == StateFreeExhausted, item.State == StateFailed, item.State == StateCanceled:
	case item.State == StateRunning && item.Mode != "batch":
	default:
		return errors.New("무료 항목만 유료로 이어 갈 수 있어요")
	}
	keys := r.opts.Keys()
	if _, paid := splitKeys(keys); len(paid) == 0 {
		return errors.New("유료 키가 필요해요")
	}
	if item.State == StateRunning {
		// Stop the free run (live call or retry wait); finished units stay.
		// enqueue below moves a queued item, or queues an active one once its
		// run has stopped.
		r.mu.Lock()
		r.stopActiveLocked(id)
		r.mu.Unlock()
	}
	// Continuing on paid is an explicit switch, so a pin to a free (or deleted)
	// key is dropped and the highest-priority paid key takes over.
	keepPin := false
	if item.KeyID != "" {
		if key, ok := findKey(keys, item.KeyID); ok && key.Tier != "free" {
			keepPin = true
		}
	}
	if err := r.update(id, func(it *store.Item) {
		if !keepPin {
			it.KeyID = ""
		}
		it.Mode = "batch"
		it.State = StateRunning
		it.Detail = "준비 중"
	}); err != nil {
		return err
	}
	r.enqueue(id, "batch")
	return nil
}

// ---- lanes -----------------------------------------------------------------

// lane runs one kind of work, one item at a time in FIFO order. Free live work
// is serial because of per-key rate limits; Batch submission is serial to
// share upload bandwidth, and the poller takes over once a job is submitted.
// Every field is guarded by Runner.mu.
type lane struct {
	mode     string // the item mode this lane runs
	queue    []string
	queued   map[string]bool
	active   string
	cancel   context.CancelFunc // cancels the active item; nil while it starts
	canceled bool               // the active item was stopped on purpose
	// again is the mode to queue the active item in once it has stopped: a
	// retry or a move to paid that arrived while the old run was unwinding.
	again string
	wake  chan struct{}
}

func newLane(mode string) *lane {
	return &lane{mode: mode, queued: make(map[string]bool), wake: make(chan struct{}, 1)}
}

// laneFor returns the lane that runs items of mode.
func (r *Runner) laneFor(mode string) *lane {
	if l, ok := r.lanes[mode]; ok {
		return l
	}
	return r.lanes["free"]
}

// stopActiveLocked cancels id's current run, if any. Callers hold r.mu.
func (r *Runner) stopActiveLocked(id string) {
	for _, l := range r.lanes {
		if l.active == id {
			l.canceled = true
			if l.cancel != nil {
				l.cancel()
			}
		}
	}
}

func (l *lane) remove(id string) {
	if !l.queued[id] {
		return
	}
	delete(l.queued, id)
	for i, q := range l.queue {
		if q == id {
			l.queue = append(l.queue[:i], l.queue[i+1:]...)
			break
		}
	}
}

// enqueue puts id at the back of the lane for mode. An item that is still
// active in a lane is queued once that run has stopped.
func (r *Runner) enqueue(id, mode string) {
	target := r.laneFor(mode)
	r.mu.Lock()
	for _, l := range r.lanes {
		if l.active == id {
			l.again = target.mode
			r.mu.Unlock()
			return
		}
		if l != target {
			l.remove(id)
		}
	}
	if !target.queued[id] {
		target.queue = append(target.queue, id)
		target.queued[id] = true
	}
	r.mu.Unlock()
	select {
	case target.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) isActive(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.lanes {
		if l.active == id {
			return true
		}
	}
	return false
}

func (r *Runner) next(l *lane) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(l.queue) == 0 {
		return "", false
	}
	id := l.queue[0]
	l.queue = l.queue[1:]
	delete(l.queued, id)
	// Taking the active slot under the same lock means an enqueue racing
	// with the start never queues the item a second time.
	l.active = id
	return id, true
}

func (r *Runner) worker(ctx context.Context, l *lane) {
	for {
		id, ok := r.next(l)
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-l.wake:
				continue
			}
		}
		r.runItem(ctx, l, id)
	}
}

func (r *Runner) runItem(parent context.Context, l *lane, id string) {
	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	l.cancel = cancel
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		again := l.again
		l.active = ""
		l.cancel = nil
		l.canceled = false
		l.again = ""
		r.mu.Unlock()
		if again != "" {
			r.enqueue(id, again)
		}
	}()

	// A cancel, remove or move to the other lane may land before the run
	// starts. Only a running item of this lane's mode starts work.
	current, ok := r.opts.Store.Get(id)
	if !ok || current.State != StateRunning || r.laneFor(current.Mode) != l {
		return
	}

	err := r.process(ctx, id)
	if err == nil {
		return
	}
	r.mu.Lock()
	userCanceled := l.canceled
	r.mu.Unlock()
	if userCanceled {
		r.logf("item %s canceled", id)
		return
	}
	if parent.Err() != nil {
		// App shutdown: leave the item running so the next start resumes it.
		r.logf("item %s interrupted by shutdown", id)
		return
	}
	r.logf("item %s failed: %v", id, err)
	r.fail(id, userMessage(err))
}

func (r *Runner) process(ctx context.Context, id string) error {
	item, err := r.prepare(ctx, id)
	if err != nil {
		return err
	}
	if item.Mode == "batch" {
		return r.submitBatch(ctx, item)
	}
	return r.runFree(ctx, item)
}

// ---- preparation -------------------------------------------------------------

func (r *Runner) workDir(id string) string {
	return filepath.Join(r.opts.WorkDir, id)
}

// prepare makes sure every unfinished unit has its piece files on disk,
// re-scanning the source when the work directory is gone.
func (r *Runner) prepare(ctx context.Context, id string) (store.Item, error) {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return item, store.ErrNotFound
	}
	sig := sourceSig(item.Source)
	if item.SourceSig != "" && sig != "" && sig != item.SourceSig {
		// The audio changed since its pieces were made: finished texts and
		// uploads belong to the old audio.
		r.logf("item %s source changed, starting over", id)
		if err := r.update(id, func(it *store.Item) {
			it.Units = nil
			it.Batch = nil
			it.Progress = 0
		}); err != nil {
			return item, err
		}
		_ = os.RemoveAll(r.workDir(id))
		item.Units = nil
		item.Batch = nil
	}
	if len(item.Units) > 0 && unitsReady(item.Units) {
		if item.SourceSig == "" && sig != "" {
			_ = r.update(id, func(it *store.Item) { it.SourceSig = sig })
		}
		return item, nil
	}
	if err := ctx.Err(); err != nil {
		return item, err
	}
	r.setDetail(id, "오디오 준비 중")
	// Fresh pieces are written below, so uploads of the old ones are useless.
	if err := r.update(id, func(it *store.Item) {
		if it.Batch != nil && it.Batch.Name == "" {
			it.Batch.Uploads = nil
		}
	}); err != nil {
		return item, err
	}

	input, err := r.opts.Media.Scan(item.Source)
	if err != nil {
		return item, userError{err}
	}
	dir := r.workDir(id)
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return item, err
	}
	tracks := make([][]Piece, 0, len(input.Tracks))
	for _, track := range input.Tracks {
		if err := ctx.Err(); err != nil {
			return item, err
		}
		ps, err := r.opts.Media.Prepare(track, dir, r.opts.UnitMaxSeconds)
		if err != nil {
			return item, userError{err}
		}
		tracks = append(tracks, ps)
	}
	pieces := 0
	for _, ps := range tracks {
		pieces += len(ps)
	}
	if pieces == 0 {
		return item, userError{errors.New("전사할 오디오가 없어요")}
	}
	if input.PDF != "" {
		if err := copyFile(input.PDF, filepath.Join(dir, pdfName)); err != nil {
			return item, err
		}
	}
	plans := r.opts.Media.Units(tracks, r.opts.UnitMaxSeconds)
	units := make([]store.Unit, 0, len(plans))
	var total float64
	for i, p := range plans {
		files := make([]string, 0, len(p.Pieces))
		mimes := make([]string, 0, len(p.Pieces))
		for _, piece := range p.Pieces {
			files = append(files, piece.Path)
			mimes = append(mimes, piece.MIME)
		}
		units = append(units, store.Unit{Index: i, Files: files, MIMEs: mimes, Seconds: p.Seconds})
		total += p.Seconds
	}
	// The plan is deterministic, so finished texts survive a re-prepare when
	// the unit layout is unchanged (the source itself is checked above).
	if len(item.Units) == len(units) {
		for i := range units {
			if math.Abs(item.Units[i].Seconds-units[i].Seconds) < 1 {
				units[i].Text = item.Units[i].Text
			}
		}
	}
	err = r.update(id, func(it *store.Item) {
		if input.Name != "" {
			it.Name = input.Name
		}
		if input.Kind != "" {
			it.Kind = input.Kind
		}
		it.Units = units
		it.AudioSec = total
		it.Progress = doneRatio(units)
		it.SourceSig = sig
	})
	if err != nil {
		return item, err
	}
	item, _ = r.opts.Store.Get(id)
	return item, nil
}

// audioExts are the files whose change makes earlier results stale.
var audioExts = map[string]bool{".m4a": true, ".mp4": true, ".mp3": true, ".wav": true, ".pdf": true}

// sourceSig fingerprints a dropped file, or the audio and PDF files directly
// inside a dropped folder, by name, size and modification time. It is "" when
// the source cannot be read.
func sourceSig(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	if !info.IsDir() {
		return fileSig(info)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || !audioExts[strings.ToLower(filepath.Ext(name))] {
			continue
		}
		info, err := os.Stat(filepath.Join(path, name))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		b.WriteString(name + ":" + fileSig(info) + ";")
	}
	return b.String()
}

func fileSig(info os.FileInfo) string {
	return fmt.Sprintf("%d@%d", info.Size(), info.ModTime().UnixNano())
}

// fileStamp is fileSig for a path, or "" when it cannot be read.
func fileStamp(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fileSig(info)
}

func unitsReady(units []store.Unit) bool {
	for _, u := range units {
		if u.Text != "" {
			continue
		}
		if len(u.Files) == 0 {
			return false
		}
		for _, f := range u.Files {
			if _, err := os.Stat(f); err != nil {
				return false
			}
		}
	}
	return true
}

func (r *Runner) pdfPath(id string) string {
	path := filepath.Join(r.workDir(id), pdfName)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// ---- free live -------------------------------------------------------------

type keyOutcome int

const (
	keyOK keyOutcome = iota
	keyLimited
	keyRejected
)

func (r *Runner) runFree(ctx context.Context, item store.Item) error {
	id := item.ID
	pdf := r.pdfPath(id)
	for i := range item.Units {
		if item.Units[i].Text != "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		unit := item.Units[i]
		r.setDetail(id, "전사 중")

		candidates, err := r.freeCandidates(item.KeyID, unit.Seconds)
		if err != nil {
			return err
		}
		limited := 0
		var text string
		done := false
		for k := 0; k < len(candidates) && !done; k++ {
			key := candidates[k]
			t, outcome, err := r.freeAttempt(ctx, id, key, unit, pdf)
			if err != nil {
				return err
			}
			switch outcome {
			case keyOK:
				text = t
				done = true
			case keyLimited:
				limited++
				r.logf("item %s unit %d: key %s limited (429)", id, unit.Index, key.ID)
			case keyRejected:
				r.logf("item %s unit %d: key %s rejected", id, unit.Index, key.ID)
			}
		}
		if !done {
			if limited == 0 {
				return userError{errors.New("무료 키가 올바르지 않아요")}
			}
			remaining := 0.0
			for _, u := range item.Units {
				if u.Text == "" {
					remaining += u.Seconds
				}
			}
			minutes := int(math.Ceil(remaining / 60))
			r.logf("item %s free keys exhausted, %d min left", id, minutes)
			return r.update(id, func(it *store.Item) {
				if it.State != StateRunning || it.Mode == "batch" {
					return
				}
				it.State = StateFreeExhausted
				it.Detail = fmt.Sprintf("남은 %d분", minutes)
				it.Progress = doneRatio(it.Units)
			})
		}

		item.Units[i].Text = text
		if err := r.update(id, func(it *store.Item) {
			if i < len(it.Units) {
				it.Units[i].Text = text
			}
			it.Progress = doneRatio(it.Units)
		}); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.finish(id)
}

// freeCandidates returns the free keys to try for one unit, in order.
//
// A pinned item gets only its key, so a 429 ends in free_exhausted instead of
// moving to another key. Otherwise keys follow the priority order (config
// order, environment key first). Free items run one at a time, so strict priority
// would make every unit wait out the 12 s+ gap on the first key while others
// sit idle; keys whose gap has already passed therefore go first, each group
// still in priority order. Every key is still tried before giving up.
func (r *Runner) freeCandidates(pinned string, seconds float64) ([]config.Key, error) {
	keys := r.opts.Keys()
	if pinned != "" {
		key, ok := findKey(keys, pinned)
		if !ok {
			return nil, errPinnedMissing
		}
		if key.Tier != "free" {
			return nil, userError{errors.New("선택한 키는 무료 키가 아니에요")}
		}
		return []config.Key{key}, nil
	}
	free, _ := splitKeys(keys)
	if len(free) == 0 {
		return nil, userError{errors.New("무료 키가 없어요")}
	}
	ready := make([]config.Key, 0, len(free))
	waiting := make([]config.Key, 0, len(free))
	for _, key := range free {
		if r.gapLeft(key.ID, seconds) > 0 {
			waiting = append(waiting, key)
		} else {
			ready = append(ready, key)
		}
	}
	return append(ready, waiting...), nil
}

// freeAttempt runs one unit on one key. A non-nil error ends the item; the
// outcome tells the caller whether to rotate to the next key.
func (r *Runner) freeAttempt(ctx context.Context, id string, key config.Key, unit store.Unit, pdf string) (string, keyOutcome, error) {
	client, err := r.client(key)
	if err != nil {
		return "", keyRejected, nil
	}
	parts, err := r.uploadParts(ctx, client, key.ID, unit, pdf)
	if err != nil {
		if outcome, ok := keyFailure(err); ok {
			return "", outcome, nil
		}
		return "", keyOK, err
	}
	if err := r.waitGap(ctx, key.ID, unit.Seconds); err != nil {
		return "", keyOK, err
	}

	r.setDetail(id, "전사 중")
	var text, finish string
	err = r.retryTransient(ctx, func(int) { r.setDetail(id, retryDetail) }, func() error {
		r.mu.Lock()
		r.lastStart[key.ID] = r.opts.Now()
		r.mu.Unlock()
		var genErr error
		text, finish, genErr = client.Generate(ctx, parts)
		if genErr == nil && strings.TrimSpace(text) == "" && (finish == "" || finish == gemini.FinishStop) {
			// An empty answer with no reason comes from an overloaded
			// server; the same request usually works when sent again.
			genErr = &gemini.EmptyResponseError{Finish: finish, Usage: "no usage"}
		}
		return genErr
	})
	if err != nil {
		if outcome, ok := keyFailure(err); ok {
			return "", outcome, nil
		}
		return "", keyOK, err
	}
	r.setDetail(id, "전사 중")
	if err := checkText(text, finish); err != nil {
		return "", keyOK, err
	}
	return strings.TrimSpace(text), keyOK, nil
}

func keyFailure(err error) (keyOutcome, bool) {
	switch {
	case gemini.Is429(err):
		return keyLimited, true
	case gemini.IsAuth(err):
		return keyRejected, true
	}
	return keyOK, false
}

// blockedMessage is what the user sees when Gemini refuses the audio. The
// server's reason goes to the log instead.
const blockedMessage = "Gemini가 이 오디오를 처리하지 않았어요"

// blockedError carries the server's block reason out of checkText.
type blockedError struct{ reason string }

func (e blockedError) Error() string {
	if e.reason == "" {
		return blockedMessage
	}
	return blockedMessage + " (" + e.reason + ")"
}

func checkText(text, finish string) error {
	if strings.HasPrefix(finish, gemini.FinishBlocked) {
		return blockedError{reason: strings.TrimPrefix(finish, gemini.FinishBlocked)}
	}
	if finish == gemini.FinishMaxTokens {
		return userError{errors.New("결과가 너무 길어서 잘렸어요")}
	}
	if strings.TrimSpace(text) == "" {
		if finish != "" && finish != gemini.FinishStop {
			return userError{fmt.Errorf("결과가 비어 있어요 (%s)", finish)}
		}
		return userError{errors.New("결과가 비어 있어요")}
	}
	if finish != "" && finish != gemini.FinishStop {
		return userError{fmt.Errorf("전사가 중간에 멈췄어요 (%s)", finish)}
	}
	return nil
}

// gap is the minimum spacing between request starts on one free key: 12 s
// (RPM 5) or longer when the audio would exceed the 250k TPM window.
func (r *Runner) gap(seconds float64) time.Duration {
	if r.opts.KeyGap < 0 {
		return 0
	}
	tokens := int(math.Max(0, seconds))*rateTokensPerSecond + 4000
	perMinute := freeTPM / tokens
	if perMinute < 1 {
		perMinute = 1
	}
	g := r.opts.KeyGap
	if tpm := time.Duration(float64(time.Minute) / float64(perMinute)); tpm > g {
		g = tpm
	}
	if rpm := time.Minute / freeRPM; rpm > g {
		g = rpm
	}
	return g
}

func (r *Runner) waitGap(ctx context.Context, keyID string, seconds float64) error {
	left := r.gapLeft(keyID, seconds)
	if left <= 0 {
		return nil
	}
	return r.opts.Sleep(ctx, left)
}

// gapLeft is how long keyID must still wait before its next request start.
func (r *Runner) gapLeft(keyID string, seconds float64) time.Duration {
	g := r.gap(seconds)
	if g <= 0 {
		return 0
	}
	r.mu.Lock()
	last, ok := r.lastStart[keyID]
	r.mu.Unlock()
	if !ok {
		return 0
	}
	return g - r.opts.Now().Sub(last)
}

func (r *Runner) retryTransient(ctx context.Context, onRetry func(int), fn func() error) error {
	var err error
	for attempt := 0; attempt <= transientRetries; attempt++ {
		if attempt > 0 {
			if onRetry != nil {
				onRetry(attempt)
			}
			if serr := r.opts.Sleep(ctx, r.opts.Backoff(attempt)); serr != nil {
				return serr
			}
		}
		err = fn()
		if err == nil || !gemini.IsTransient(err) || ctx.Err() != nil {
			return err
		}
		r.logf("transient error (attempt %d): %v", attempt+1, r.redact(err))
	}
	return err
}

// defaultBackoff doubles from 10 s and stops at 2 min: 10, 20, 40, 80, 120 s.
func defaultBackoff(n int) time.Duration {
	if n < 1 {
		return 0
	}
	d := transientBaseDelay
	for i := 1; i < n; i++ {
		d *= 2
		if d >= transientMaxDelay {
			return transientMaxDelay
		}
	}
	return d
}

// uploadParts builds [prompt, pdf?, audio...] for one key, uploading files
// that are not yet uploaded for that key or are close to the 48 h expiry.
func (r *Runner) uploadParts(ctx context.Context, client gemini.Client, keyID string, unit store.Unit, pdf string) ([]gemini.Part, error) {
	prompt := gemini.Prompt
	if len(unit.Files) > 1 {
		prompt = gemini.LivePrompt
	}
	parts := []gemini.Part{{Text: prompt}}
	if pdf != "" {
		up, err := r.cachedUpload(ctx, client, keyID, pdf, mimePDF)
		if err != nil {
			return nil, err
		}
		parts = append(parts, gemini.Part{FileURI: up.URI, MIME: up.MIME})
	}
	for i, path := range unit.Files {
		up, err := r.cachedUpload(ctx, client, keyID, path, unitMIME(unit, i))
		if err != nil {
			return nil, err
		}
		parts = append(parts, gemini.Part{FileURI: up.URI, MIME: up.MIME})
	}
	return parts, nil
}

func (r *Runner) cachedUpload(ctx context.Context, client gemini.Client, keyID, path, mime string) (store.Upload, error) {
	// Piece files are rewritten in place when an item is prepared again, so
	// the file's size and time are part of the key.
	cacheKey := keyID + "\x00" + path + "\x00" + fileStamp(path)
	r.mu.Lock()
	up, ok := r.uploads[cacheKey]
	r.mu.Unlock()
	if ok && r.opts.Now().Sub(up.UploadedAt) < uploadMaxAge {
		return up, nil
	}
	up, err := r.upload(ctx, client, path, mime)
	if err != nil {
		return up, err
	}
	r.mu.Lock()
	r.uploads[cacheKey] = up
	r.mu.Unlock()
	return up, nil
}

func (r *Runner) upload(ctx context.Context, client gemini.Client, path, mime string) (store.Upload, error) {
	var up store.Upload
	err := r.retryTransient(ctx, nil, func() error {
		var uerr error
		up, uerr = client.Upload(ctx, path, mime)
		return uerr
	})
	if err != nil {
		return up, err
	}
	if up.MIME == "" {
		up.MIME = mime
	}
	if up.UploadedAt.IsZero() {
		up.UploadedAt = r.opts.Now()
	}
	return up, nil
}

// unitMIME is the MIME recorded when the piece was prepared. Unit entries
// written by older versions lack MIMEs, so fall back to the extension.
func unitMIME(unit store.Unit, i int) string {
	if i < len(unit.MIMEs) && unit.MIMEs[i] != "" {
		return unit.MIMEs[i]
	}
	if i < len(unit.Files) {
		return mimeForPath(unit.Files[i])
	}
	return "application/octet-stream"
}

func mimeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return "audio/mp3"
	case ".wav":
		return "audio/wav"
	case ".pdf":
		return mimePDF
	case ".m4a", ".mp4":
		return "audio/m4a"
	}
	return "application/octet-stream"
}

// ---- paid Batch ------------------------------------------------------------

func (r *Runner) submitBatch(ctx context.Context, item store.Item) error {
	id := item.ID
	if item.Batch != nil && item.Batch.Name != "" {
		// A job was submitted and is not known to be over (a cancel the
		// server did not confirm, or a status check that failed). It is paid
		// for already, so wait for it again instead of submitting anew.
		r.logf("item %s waits again for %s", id, item.Batch.Name)
		return r.update(id, func(it *store.Item) {
			if it.State == StateRunning {
				it.State = StateBatchWaiting
				it.Detail = ""
			}
		})
	}
	key, err := r.batchKey(item.KeyID)
	if err != nil {
		return err
	}
	client, err := r.client(key)
	if err != nil {
		return err
	}

	pending := make([]store.Unit, 0, len(item.Units))
	for _, u := range item.Units {
		if u.Text == "" {
			pending = append(pending, u)
		}
	}
	if len(pending) == 0 {
		return r.finish(id)
	}

	if item.Batch != nil && item.Batch.Marker != "" {
		// The last creation got no answer. The job may exist anyway.
		bkey := key
		if item.Batch.KeyID != "" && item.Batch.KeyID != key.ID {
			if k, ok := findKey(r.opts.Keys(), item.Batch.KeyID); ok {
				bkey = k
			}
		}
		bclient, err := r.client(bkey)
		if err != nil {
			return err
		}
		r.setDetail(id, "요청 확인 중")
		name, err := bclient.FindBatch(ctx, item.Batch.Marker)
		if err != nil {
			return err
		}
		if name != "" {
			r.logf("item %s found earlier batch %s", id, name)
			return r.update(id, func(it *store.Item) {
				if it.Batch == nil {
					it.Batch = &store.BatchRef{}
				}
				it.Batch.KeyID = bkey.ID
				it.Batch.Name = name
				it.Batch.Marker = ""
				it.Batch.SubmittedAt = r.opts.Now()
				if it.State == StateRunning {
					it.State = StateBatchWaiting
					it.Detail = ""
				}
			})
		}
	}

	uploads := map[string]store.Upload{}
	if item.Batch != nil && item.Batch.KeyID == key.ID {
		for path, up := range item.Batch.Uploads {
			if r.opts.Now().Sub(up.UploadedAt) < uploadMaxAge {
				uploads[path] = up
			}
		}
	}
	pdf := r.pdfPath(id)
	type needFile struct{ path, mime string }
	need := []needFile{}
	if pdf != "" {
		need = append(need, needFile{pdf, mimePDF})
	}
	for _, u := range pending {
		for i, path := range u.Files {
			need = append(need, needFile{path, unitMIME(u, i)})
		}
	}
	r.setDetail(id, "업로드 중")
	for _, f := range need {
		if _, ok := uploads[f.path]; ok {
			continue
		}
		up, err := r.upload(ctx, client, f.path, f.mime)
		if err != nil {
			return err
		}
		uploads[f.path] = up
		saved := cloneUploads(uploads)
		if err := r.update(id, func(it *store.Item) {
			it.Batch = &store.BatchRef{KeyID: key.ID, Uploads: saved}
		}); err != nil {
			return err
		}
	}

	reqs := make([]gemini.BatchRequest, 0, len(pending))
	for _, u := range pending {
		prompt := gemini.Prompt
		if len(u.Files) > 1 {
			prompt = gemini.LivePrompt
		}
		parts := []gemini.Part{{Text: prompt}}
		if pdf != "" {
			parts = append(parts, gemini.Part{FileURI: uploads[pdf].URI, MIME: uploads[pdf].MIME})
		}
		for _, f := range u.Files {
			parts = append(parts, gemini.Part{FileURI: uploads[f].URI, MIME: uploads[f].MIME})
		}
		reqs = append(reqs, gemini.BatchRequest{Key: unitKey(u.Index), Parts: parts})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.setDetail(id, "요청 보내는 중")
	// Creating a job is never repeated automatically: when the answer is lost
	// the job may exist, and a second one would be billed again. The marker
	// is saved first so a retry can look the job up.
	marker := batchDisplayName(item.Name, store.NewID())
	if err := r.update(id, func(it *store.Item) {
		it.Batch = &store.BatchRef{KeyID: key.ID, Uploads: cloneUploads(uploads), Marker: marker}
	}); err != nil {
		return err
	}
	name, err := client.SubmitBatch(ctx, reqs, marker)
	if err != nil {
		r.logf("item %s batch create (%s): %v", id, marker, r.redact(err))
		return err
	}
	r.logf("item %s batch submitted: %s (%d requests)", id, name, len(reqs))
	saved := cloneUploads(uploads)
	return r.update(id, func(it *store.Item) {
		// A cancel during creation keeps its state; the name is kept so a
		// retry waits for this job.
		if it.State == StateRunning {
			it.State = StateBatchWaiting
			it.Detail = ""
		}
		it.Progress = doneRatio(it.Units)
		it.Batch = &store.BatchRef{KeyID: key.ID, Name: name, SubmittedAt: r.opts.Now(), Uploads: saved}
	})
}

// batchDisplayName names a job after the item plus a unique marker, so a job
// whose creation answer was lost can be found again. The item name is cut so
// the marker always fits the server's limit.
func batchDisplayName(name, token string) string {
	const maxName = 80
	if len(name) > maxName {
		name = name[:maxName]
		for len(name) > 0 && !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return name + " #" + token
}

// batchKey is the pinned paid key, or the highest-priority paid key.
func (r *Runner) batchKey(pinned string) (config.Key, error) {
	keys := r.opts.Keys()
	if pinned != "" {
		key, ok := findKey(keys, pinned)
		if !ok {
			return config.Key{}, errPinnedMissing
		}
		if key.Tier == "free" {
			return config.Key{}, userError{errors.New("선택한 키는 유료 키가 아니에요")}
		}
		return key, nil
	}
	_, paid := splitKeys(keys)
	if len(paid) == 0 {
		return config.Key{}, userError{errors.New("유료 키가 없어요")}
	}
	return paid[0], nil
}

func unitKey(index int) string { return fmt.Sprintf("unit-%d", index) }

func cloneUploads(in map[string]store.Upload) map[string]store.Upload {
	out := make(map[string]store.Upload, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (r *Runner) poller(ctx context.Context) {
	ticker := time.NewTicker(r.opts.PollInterval)
	defer ticker.Stop()
	r.PollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.pollNow:
		}
		r.PollOnce(ctx)
	}
}

// PollOnce checks every batch_waiting item once.
func (r *Runner) PollOnce(ctx context.Context) {
	for _, item := range r.opts.Store.List("") {
		if ctx.Err() != nil {
			return
		}
		if item.State == StateBatchWaiting {
			r.pollItem(ctx, item.ID)
		}
	}
}

func (r *Runner) pollItem(ctx context.Context, id string) {
	r.pollMu.Lock()
	defer r.pollMu.Unlock()
	item, ok := r.opts.Store.Get(id)
	if !ok || item.State != StateBatchWaiting {
		return
	}
	if item.Batch == nil || item.Batch.Name == "" {
		r.fail(id, "요청 정보가 없어요")
		return
	}
	client, err := r.clientForKeyID(item.Batch.KeyID)
	if err != nil {
		r.fail(id, "요청을 보낸 유료 키가 없어요")
		return
	}
	state, err := client.BatchStatus(ctx, item.Batch.Name)
	if err != nil {
		r.pollError(ctx, id, "status", err)
		return
	}
	switch state {
	case gemini.BatchPending, gemini.BatchRunning:
		return
	case gemini.BatchFailed:
		r.batchFail(id, "서버에서 처리하지 못했어요")
		return
	case gemini.BatchCancelled:
		r.batchFail(id, "서버에서 취소됐어요")
		return
	case gemini.BatchExpired:
		r.batchFail(id, "기한 안에 끝나지 않았어요")
		return
	}
	r.collectLocked(ctx, id, client, item.Batch.Name)
}

// pollError decides what a failed status or results call means. Network
// trouble, timeouts and rate limits keep waiting on the same job. A job the
// server does not know ends the wait. Other refusals mark the item failed
// but keep the job name, so a retry checks the same job again rather than
// paying for a new one.
func (r *Runner) pollError(ctx context.Context, id, what string, err error) {
	r.logf("item %s batch %s: %v", id, what, r.redact(err))
	switch {
	case ctx.Err() != nil, gemini.IsTransient(err), gemini.Is429(err):
	case gemini.IsAuth(err):
		r.fail(id, "유료 키가 올바르지 않아요")
	case gemini.IsNotFound(err):
		r.batchFail(id, "서버에 요청이 없어요")
	default:
		r.fail(id, "요청 상태를 확인하지 못했어요")
	}
}

// collectLocked stores the results of a finished job and writes the output.
// The caller holds pollMu.
func (r *Runner) collectLocked(ctx context.Context, id string, client gemini.Client, name string) {
	results, err := client.BatchResults(ctx, name)
	if err != nil {
		r.pollError(ctx, id, "results", err)
		return
	}
	failed := 0
	blocked := false
	firstReason := ""
	err = r.update(id, func(it *store.Item) {
		for i := range it.Units {
			if it.Units[i].Text != "" {
				continue
			}
			res, ok := results[unitKey(it.Units[i].Index)]
			reason := ""
			switch {
			case !ok:
				reason = "결과가 없어요"
			case res.Error != "":
				reason = res.Error
			default:
				if cerr := checkText(res.Text, res.Finish); cerr != nil {
					reason = cerr.Error()
					var be blockedError
					if errors.As(cerr, &be) {
						blocked = true
					}
				}
			}
			if reason != "" {
				failed++
				if firstReason == "" {
					firstReason = reason
				}
				continue
			}
			it.Units[i].Text = strings.TrimSpace(res.Text)
		}
		it.Progress = doneRatio(it.Units)
	})
	if err != nil {
		r.logf("item %s save batch results: %v", id, err)
		return
	}
	if failed > 0 {
		if blocked {
			r.logf("item %s batch blocked: %s", id, firstReason)
			r.batchFail(id, blockedMessage)
			return
		}
		r.logf("item %s batch: %d units failed (%s)", id, failed, firstReason)
		r.batchFail(id, fmt.Sprintf("%d개 조각을 받지 못했어요", failed))
		return
	}
	if err := r.finish(id); err != nil {
		r.logf("item %s finish: %v", id, err)
		r.fail(id, userMessage(err))
	}
}

func (r *Runner) batchFail(id, detail string) {
	r.logf("item %s: %s", id, detail)
	_ = r.update(id, func(it *store.Item) {
		it.State = StateFailed
		it.Detail = detail
		if it.Batch != nil {
			it.Batch.Name = ""
		}
	})
}

// ---- finishing -------------------------------------------------------------

func (r *Runner) finish(id string) error {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	if item.State != StateRunning && item.State != StateBatchWaiting {
		return nil // canceled meanwhile
	}
	texts := make([]string, 0, len(item.Units))
	for _, u := range item.Units {
		if u.Text == "" {
			return userError{errors.New("끝나지 않은 조각이 있어요")}
		}
		texts = append(texts, u.Text)
	}
	merged := output.Merge(texts)
	if merged == "" {
		return userError{errors.New("결과가 비어 있어요")}
	}
	target, err := output.WriteNew(output.TargetPath(item.Source, item.Kind), merged)
	if err != nil {
		return userError{fmt.Errorf("결과 파일을 저장하지 못했어요: %v", err)}
	}
	if err := r.update(id, func(it *store.Item) {
		it.State = StateDone
		it.Output = target
		it.Progress = 1
		it.Detail = ""
		it.Batch = nil
		// The .md is the record; dropping texts keeps items.json small.
		it.Units = nil
	}); err != nil {
		return err
	}
	_ = os.RemoveAll(r.workDir(id))
	r.logf("item %s done: %s", id, target)
	r.opts.Notify("Nalmeok", item.Name+" 전사 완료")
	return nil
}

func (r *Runner) fail(id, detail string) {
	_ = r.update(id, func(it *store.Item) {
		if it.State == StateCanceled {
			return
		}
		it.State = StateFailed
		it.Detail = detail
	})
}

// ---- helpers ---------------------------------------------------------------

func (r *Runner) update(id string, fn func(*store.Item)) error {
	return r.opts.Store.Update(id, fn)
}

func (r *Runner) setDetail(id, detail string) {
	_ = r.update(id, func(it *store.Item) {
		if it.State == StateRunning {
			it.Detail = detail
		}
	})
}

func (r *Runner) client(key config.Key) (gemini.Client, error) {
	r.mu.Lock()
	c, ok := r.clients[key.APIKey]
	r.mu.Unlock()
	if ok {
		return c, nil
	}
	c, err := r.opts.NewClient(key.APIKey)
	if err != nil {
		r.logf("client for key %s: %v", key.ID, r.redact(err))
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Both lanes may create a client for the same key at once; keep the first.
	if existing, ok := r.clients[key.APIKey]; ok {
		return existing, nil
	}
	r.clients[key.APIKey] = c
	return c, nil
}

func (r *Runner) clientForKeyID(keyID string) (gemini.Client, error) {
	for _, key := range r.opts.Keys() {
		if key.ID == keyID {
			return r.client(key)
		}
	}
	return nil, errors.New("key not found")
}

// errPinnedMissing is shown when an item's chosen key was deleted.
var errPinnedMissing = userError{errors.New("선택한 키가 없어요")}

func findKey(keys []config.Key, id string) (config.Key, bool) {
	for _, k := range keys {
		if k.ID == id {
			return k, true
		}
	}
	return config.Key{}, false
}

func modeForTier(tier string) string {
	if tier == "free" {
		return "free"
	}
	return "batch"
}

func splitKeys(keys []config.Key) (free, paid []config.Key) {
	for _, k := range keys {
		if k.Tier == "free" {
			free = append(free, k)
		} else {
			paid = append(paid, k)
		}
	}
	return free, paid
}

func doneRatio(units []store.Unit) float64 {
	var total, done float64
	for _, u := range units {
		total += u.Seconds
		if u.Text != "" {
			done += u.Seconds
		}
	}
	if total <= 0 {
		return 0
	}
	return done / total
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// userError marks an error whose message is already a short Korean sentence.
type userError struct{ err error }

func (e userError) Error() string { return e.err.Error() }
func (e userError) Unwrap() error { return e.err }

// userMessage turns an error into one short Korean sentence for Item.Detail.
func userMessage(err error) string {
	var ue userError
	var be blockedError
	switch {
	case errors.As(err, &be):
		return blockedMessage
	case gemini.IsEmptyResponse(err):
		return "결과가 비어 있어요"
	case errors.As(err, &ue):
		return clip(ue.Error())
	case gemini.IsAuth(err):
		return "API 키가 올바르지 않아요"
	case gemini.Is429(err):
		return "요청이 몰려 있어요. 잠시 뒤 다시 시도하세요"
	case gemini.IsTransient(err):
		return "네트워크 오류"
	case errors.Is(err, os.ErrNotExist):
		return "파일이 없어요"
	}
	if hasHangul(err.Error()) {
		return clip(err.Error())
	}
	return "처리하지 못했어요"
}

func hasHangul(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

func clip(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	runes := []rune(s)
	if len(runes) > 120 {
		return string(runes[:119]) + "…"
	}
	return s
}

func (r *Runner) logf(format string, args ...any) {
	r.log.Print(r.redactString(fmt.Sprintf(format, args...)))
}

func (r *Runner) redact(err error) string {
	if err == nil {
		return ""
	}
	return r.redactString(err.Error())
}

// redactString removes any configured API key from s before it is logged.
func (r *Runner) redactString(s string) string {
	r.mu.Lock()
	keys := make([]string, 0, len(r.clients))
	for k := range r.clients {
		keys = append(keys, k)
	}
	r.mu.Unlock()
	for _, k := range keys {
		if k != "" {
			s = strings.ReplaceAll(s, k, "[key]")
		}
	}
	return s
}

func notify(title, message string) {
	if runtime.GOOS != "darwin" {
		return
	}
	script := fmt.Sprintf("display notification %s with title %s", appleQuote(message), appleQuote(title))
	_ = exec.Command("osascript", "-e", script).Run()
}

func appleQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

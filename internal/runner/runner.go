// Package runner turns dropped paths into .md files.
//
// One worker goroutine runs items in FIFO order (scan → prepare → free live
// calls or a paid Batch submission → output). A second goroutine polls
// submitted Batch jobs. All progress lives in the store, so a restart resumes
// where the previous run stopped.
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
	opts Options
	log  *log.Logger

	mu        sync.Mutex
	queue     []string
	queued    map[string]bool
	wake      chan struct{}
	pollNow   chan struct{}
	active    string
	cancel    context.CancelFunc
	canceled  map[string]bool
	started   bool
	clients   map[string]gemini.Client
	uploads   map[string]store.Upload // keyID + "\x00" + path
	lastStart map[string]time.Time
	nextFree  int

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
		queued:    make(map[string]bool),
		wake:      make(chan struct{}, 1),
		pollNow:   make(chan struct{}, 1),
		canceled:  make(map[string]bool),
		clients:   make(map[string]gemini.Client),
		uploads:   make(map[string]store.Upload),
		lastStart: make(map[string]time.Time),
	}
	r.log = log.New(io.Discard, "", log.LstdFlags)
	if opts.LogPath != "" {
		if err := os.MkdirAll(filepath.Dir(opts.LogPath), 0o700); err == nil {
			if f, err := os.OpenFile(opts.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
				r.log = log.New(f, "", log.LstdFlags)
			}
		}
	}
	return r
}

// Start resumes interrupted work and launches the worker and poller.
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
			r.enqueue(item.ID)
		}
	}
	go r.worker(ctx)
	go r.poller(ctx)
}

// Add creates one item per path and queues them.
func (r *Runner) Add(paths []string, mode string) error {
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
		}
		if err := r.opts.Store.Add(item); err != nil {
			return err
		}
		r.logf("item %s added: %s (%s)", item.ID, path, mode)
		r.enqueue(item.ID)
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
		r.removeQueuedLocked(id)
		if r.active == id && r.cancel != nil {
			r.canceled[id] = true
			r.cancel()
		}
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
		if current.Batch != nil && current.Batch.Name != "" {
			if client, err := r.clientForKeyID(current.Batch.KeyID); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				if err := client.CancelBatch(ctx, current.Batch.Name); err != nil {
					r.logf("item %s cancel batch: %v", id, err)
				}
				cancel()
			}
		}
		return r.update(id, func(it *store.Item) {
			it.State = StateCanceled
			it.Detail = "취소됨"
			if it.Batch != nil {
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
		r.mu.Lock()
		active := r.active == id
		r.mu.Unlock()
		if !active || !time.Now().Before(deadline) {
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
	free, paid := splitKeys(r.opts.Keys())
	if mode == "free" && len(free) == 0 && len(paid) > 0 {
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
	r.enqueue(id)
	return nil
}

// SendToBatch moves the unfinished units of a free_exhausted item to Batch.
func (r *Runner) SendToBatch(id string) error {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	if item.State != StateFreeExhausted && item.State != StateFailed && item.State != StateCanceled {
		return errors.New("무료 사용량을 다 쓴 항목만 유료로 이어 갈 수 있어요")
	}
	if _, paid := splitKeys(r.opts.Keys()); len(paid) == 0 {
		return errors.New("유료 키가 필요해요")
	}
	if err := r.update(id, func(it *store.Item) {
		it.Mode = "batch"
		it.State = StateRunning
		it.Detail = "준비 중"
	}); err != nil {
		return err
	}
	r.enqueue(id)
	return nil
}

// ---- queue -----------------------------------------------------------------

func (r *Runner) enqueue(id string) {
	r.mu.Lock()
	delete(r.canceled, id)
	if !r.queued[id] && r.active != id {
		r.queue = append(r.queue, id)
		r.queued[id] = true
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) removeQueuedLocked(id string) {
	if !r.queued[id] {
		return
	}
	delete(r.queued, id)
	for i, q := range r.queue {
		if q == id {
			r.queue = append(r.queue[:i], r.queue[i+1:]...)
			break
		}
	}
}

func (r *Runner) next() (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.queue) == 0 {
		return "", false
	}
	id := r.queue[0]
	r.queue = r.queue[1:]
	delete(r.queued, id)
	return id, true
}

func (r *Runner) worker(ctx context.Context) {
	for {
		id, ok := r.next()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-r.wake:
				continue
			}
		}
		r.runItem(ctx, id)
	}
}

func (r *Runner) runItem(parent context.Context, id string) {
	item, ok := r.opts.Store.Get(id)
	if !ok || item.State != StateRunning {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	r.active = id
	r.cancel = cancel
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		r.active = ""
		r.cancel = nil
		delete(r.canceled, id)
		r.mu.Unlock()
	}()

	// A cancel or remove may land between the first read and taking the active
	// slot. Re-read so canceled items never start fresh work.
	if current, ok := r.opts.Store.Get(id); !ok || current.State != StateRunning {
		return
	}

	err := r.process(ctx, id)
	if err == nil {
		return
	}
	r.mu.Lock()
	userCanceled := r.canceled[id]
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
	if len(item.Units) > 0 && unitsReady(item.Units) {
		return item, nil
	}
	if err := ctx.Err(); err != nil {
		return item, err
	}
	r.setDetail(id, "오디오 준비 중")

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
	// the unit layout is unchanged.
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
	})
	if err != nil {
		return item, err
	}
	item, _ = r.opts.Store.Get(id)
	return item, nil
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

		free, _ := splitKeys(r.opts.Keys())
		if len(free) == 0 {
			return userError{errors.New("무료 키가 없어요")}
		}
		start := r.takeFreeStart(len(free))
		limited := 0
		var text string
		done := false
		for k := 0; k < len(free) && !done; k++ {
			key := free[(start+k)%len(free)]
			t, outcome, err := r.freeAttempt(ctx, id, key, unit, pdf)
			if err != nil {
				return err
			}
			switch outcome {
			case keyOK:
				text = t
				done = true
				r.mu.Lock()
				r.nextFree = (start + k + 1) % len(free)
				r.mu.Unlock()
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
				if it.State != StateRunning {
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

func (r *Runner) takeFreeStart(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 {
		return 0
	}
	return r.nextFree % n
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
	g := r.gap(seconds)
	if g <= 0 {
		return nil
	}
	r.mu.Lock()
	last, ok := r.lastStart[keyID]
	r.mu.Unlock()
	if !ok {
		return nil
	}
	return r.opts.Sleep(ctx, g-r.opts.Now().Sub(last))
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
	cacheKey := keyID + "\x00" + path
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
	_, paid := splitKeys(r.opts.Keys())
	if len(paid) == 0 {
		return userError{errors.New("유료 키가 없어요")}
	}
	key := paid[0]
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
	var name string
	err = r.retryTransient(ctx, func(int) { r.setDetail(id, retryDetail) }, func() error {
		var serr error
		name, serr = client.SubmitBatch(ctx, reqs, item.Name)
		return serr
	})
	if err != nil {
		return err
	}
	r.logf("item %s batch submitted: %s (%d requests)", id, name, len(reqs))
	saved := cloneUploads(uploads)
	if err := r.update(id, func(it *store.Item) {
		it.State = StateBatchWaiting
		it.Detail = ""
		it.Progress = doneRatio(it.Units)
		it.Batch = &store.BatchRef{KeyID: key.ID, Name: name, SubmittedAt: r.opts.Now(), Uploads: saved}
	}); err != nil {
		return err
	}
	return nil
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
		if gemini.IsAuth(err) {
			r.fail(id, "유료 키가 올바르지 않아요")
			return
		}
		r.logf("item %s batch status: %v", id, r.redact(err))
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

	results, err := client.BatchResults(ctx, item.Batch.Name)
	if err != nil {
		r.logf("item %s batch results: %v", id, r.redact(err))
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
	target := output.UniquePath(output.TargetPath(item.Source, item.Kind))
	if err := output.Write(target, merged); err != nil {
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
	r.opts.Notify("날먹", item.Name+" 전사 완료")
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
	r.clients[key.APIKey] = c
	r.mu.Unlock()
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

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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/plan"
	"github.com/jseook11/nalmeok/internal/store"
	"github.com/jseook11/nalmeok/internal/telemetry"
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

// Default request limits.
const (
	DefaultUnitMaxSeconds = plan.UnitMaxSeconds
	// DefaultFreeUnitMaxSeconds keeps free requests at 40 minutes: a shorter
	// request loses less when the server drops it, and still stays far under
	// the free tier's 250k tokens per minute.
	DefaultFreeUnitMaxSeconds = plan.FreeUnitMaxSeconds
	DefaultKeyGap             = 12 * time.Second
	DefaultPollInterval       = 30 * time.Second
	uploadMaxAge              = 47 * time.Hour
	transientRetries          = 5
	transientBaseDelay        = 10 * time.Second
	transientMaxDelay         = 2 * time.Minute
	freeTPM                   = 250_000
	freeRPM                   = 5
	// rateTokensPerSecond spaces free requests. It is deliberately above the
	// measured plan.AudioTokensPerSecond (25) so the gap also covers the
	// prompt, PDFs and the measurement's spread.
	rateTokensPerSecond = 32
	// overloadTries is how many 503s in a row one model gets before the unit
	// moves on: the free tier answers 503 at admission when the model is
	// busy, and every further try burns daily quota for nothing.
	overloadTries = 3
	// freeDailyRequestsFloor is a conservative floor of the free tier's
	// requests per day. A 429 before a key has sent this many requests today
	// is not the daily quota, since gap() keeps each key under RPM and TPM.
	freeDailyRequestsFloor = 20
	// Reference PDFs are copied into the work directory as
	// reference-001.pdf, reference-002.pdf, … in the order chosen. Work
	// directories made before several PDFs were allowed hold reference.pdf;
	// pdfGlob finds both.
	pdfPattern = "reference-%03d.pdf"
	pdfGlob    = "reference*.pdf"
	mimePDF    = "application/pdf"
)

// Input, Track, Piece and UnitPlan mirror the media and plan packages so the
// runner compiles and tests without them; media.go adapts the real ones.
type Input struct {
	Source string
	Kind   string
	Name   string
	Tracks []Track
	// PDFs are the reference PDFs in a folder input.
	PDFs []string
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
	// TranscriptsDir keeps the app's copy of each finished transcript
	// (<config dir>/transcripts, see output.ArchivePath); empty keeps none.
	TranscriptsDir string
	// LogPath is the log file; empty disables logging.
	LogPath string
	// Notify shows an OS notification when an item finishes. nil shows
	// none; the app and CLI pass notify.Send.
	Notify func(title, message string)

	// UnitMaxSeconds is the longest audio one Batch request carries, and
	// the upper bound for free requests too. FreeUnitMaxSeconds is the
	// longest a free (live) request carries; the smaller of the two wins.
	UnitMaxSeconds     float64
	FreeUnitMaxSeconds float64
	// OverloadWaits are the pauses before a free unit is tried again after
	// every model answered 503; the item fails once they run out.
	OverloadWaits []time.Duration
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
	// EarlyEstimate scans each added source in the background to record its
	// audio length and paid estimate before its turn comes. Off in tests,
	// which count scans.
	EarlyEstimate bool
	// Signal receives the anonymous outcome of every Gemini request
	// (telemetry.Client.Record). nil records nothing.
	Signal func(telemetry.Event)
}

// Runner implements app.Runner.
type Runner struct {
	opts    Options
	log     *log.Logger
	logFile *logFile

	mu        sync.Mutex
	lanes     map[string]*lane // by item mode (config.ModeFree, config.ModeBatch)
	pollNow   chan struct{}
	started   bool
	clients   map[string]gemini.Client
	lastStart map[string]time.Time
	// freeSent counts Generate requests per free key in the current quota
	// day, for the under_quota signal.
	freeSent map[string]dailyCount

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
	if opts.FreeUnitMaxSeconds <= 0 {
		opts.FreeUnitMaxSeconds = DefaultFreeUnitMaxSeconds
	}
	if opts.OverloadWaits == nil {
		opts.OverloadWaits = defaultOverloadWaits
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
		opts.Notify = func(title, message string) {}
	}
	r := &Runner{
		opts:      opts,
		lanes:     map[string]*lane{config.ModeFree: newLane(config.ModeFree), config.ModeBatch: newLane(config.ModeBatch)},
		pollNow:   make(chan struct{}, 1),
		clients:   make(map[string]gemini.Client),
		lastStart: make(map[string]time.Time),
		freeSent:  make(map[string]dailyCount),
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
// paid key → batch). An empty keyID picks keys by priority. A folder added
// here uses every PDF in it; AddWithPDFs records a choice instead.
func (r *Runner) Add(paths []string, mode, keyID string) error {
	mode, err := r.resolveMode(mode, keyID)
	if err != nil {
		return err
	}
	items := make([]store.Item, 0, len(paths))
	for _, path := range paths {
		items = append(items, r.newItem(path, mode, keyID, nil, false))
	}
	if len(items) > 0 {
		if err := r.opts.Store.AddMany(items); err != nil {
			return err
		}
	}
	for _, item := range items {
		r.afterAdd(item)
	}
	return nil
}

// AddWithPDFs adds one folder with the reference PDFs the user picked. An
// empty pdfs transcribes without a PDF. The choice is stored on the item, so
// a retry or a move to Batch never asks again.
func (r *Runner) AddWithPDFs(path string, pdfs []string, mode, keyID string) error {
	mode, err := r.resolveMode(mode, keyID)
	if err != nil {
		return err
	}
	item := r.newItem(path, mode, keyID, append([]string{}, pdfs...), true)
	if err := r.opts.Store.Add(item); err != nil {
		return err
	}
	r.afterAdd(item)
	return nil
}

// newItem builds the record for one dropped path. It does not save it; the
// caller saves the whole batch at once.
func (r *Runner) newItem(path, mode, keyID string, pdfs []string, pdfChosen bool) store.Item {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	kind := "file"
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		kind = "folder"
	}
	return store.Item{
		ID:        store.NewID(),
		Source:    path,
		Kind:      kind,
		Name:      filepath.Base(path),
		Mode:      mode,
		State:     StateRunning,
		Detail:    i18n.T("status.queued"),
		KeyID:     keyID,
		PDFChosen: pdfChosen,
		PDFs:      pdfs,
	}
}

// afterAdd reports and queues an item that is already saved, and starts its
// early estimate.
func (r *Runner) afterAdd(item store.Item) {
	switch {
	case item.PDFChosen:
		r.logf("item %s added: %s (%s, key %q, %d PDFs chosen)", item.ID, item.Source, item.Mode, item.KeyID, len(item.PDFs))
	case item.KeyID != "":
		r.logf("item %s added: %s (%s, key %s)", item.ID, item.Source, item.Mode, item.KeyID)
	default:
		r.logf("item %s added: %s (%s)", item.ID, item.Source, item.Mode)
	}
	r.enqueue(item.ID, item.Mode)
	if r.opts.EarlyEstimate {
		go r.estimate(item.ID, item.Source)
	}
}

// estimate records the audio length and paid estimate of a just-added item.
// prepare records the same values when the item starts, so a scan error is
// left for prepare to report.
func (r *Runner) estimate(id, path string) {
	input, err := r.opts.Media.Scan(path)
	if err != nil {
		return
	}
	var total float64
	for _, t := range input.Tracks {
		total += t.Seconds
	}
	if total <= 0 {
		return
	}
	_ = r.update(id, func(it *store.Item) {
		if it.AudioSec == 0 {
			it.AudioSec = total
			it.EstimateKRW = plan.EstimateMidKRW(total)
		}
	})
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
		moved := ""
		if err := r.update(id, func(it *store.Item) {
			// The run may have finished, or submitted its Batch, since the
			// read above; a finished result is kept.
			if it.State != StateRunning && it.State != StateFreeExhausted {
				moved = it.State
				return
			}
			it.State = StateCanceled
			it.Detail = i18n.T("status.canceled")
		}); err != nil {
			return err
		}
		if moved == StateBatchWaiting {
			return r.Cancel(id) // cancel the job it just submitted
		}
		return nil
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
						// The user asked to cancel: keep the finished job's
						// results, but never start another paid job for it.
						r.collectLocked(ctx, id, client, current.Batch.Name, false)
						return nil
					}
				} else {
					forget = true
				}
			}
		}
		return r.update(id, func(it *store.Item) {
			it.State = StateCanceled
			it.Detail = i18n.T("status.canceled")
			if forget && it.Batch != nil {
				it.Batch.Name = ""
			}
		})
	default:
		return i18n.Error("error.cancelOnlyRunning")
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
		return i18n.Error("error.retryOnlyFailed")
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
		mode = config.ModeForTier(key.Tier)
	} else {
		mode = availableMode(mode, len(free), len(paid))
	}
	if err := r.update(id, func(it *store.Item) {
		it.Mode = mode
		it.State = StateRunning
		it.Detail = i18n.T("status.queued")
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
	if !canSendToBatch(item) {
		return i18n.Error("error.batchOnlyFree")
	}
	keys := r.opts.Keys()
	if _, paid := splitKeys(keys); len(paid) == 0 {
		return i18n.Error("error.paidKeyRequired")
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
		if key, ok := findKey(keys, item.KeyID); ok && key.Tier != config.TierFree {
			keepPin = true
		}
	}
	movable := true
	if err := r.update(id, func(it *store.Item) {
		// The free run may have finished since the read above; a done item
		// has no units left and must not be sent (and paid for) again.
		if !canSendToBatch(*it) {
			movable = false
			return
		}
		if !keepPin {
			it.KeyID = ""
		}
		it.Mode = config.ModeBatch
		it.State = StateRunning
		it.Detail = i18n.T("status.preparing")
	}); err != nil {
		return err
	}
	if !movable {
		return nil
	}
	r.enqueue(id, config.ModeBatch)
	return nil
}

// canSendToBatch reports whether "continue on paid" applies to item: a free
// item that ran out of free use, failed, was canceled, or is still running.
func canSendToBatch(item store.Item) bool {
	switch {
	case item.State == StateFreeExhausted, item.State == StateFailed, item.State == StateCanceled:
		return true
	case item.State == StateRunning && item.Mode != config.ModeBatch:
		return true
	}
	return false
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

func (r *Runner) clientForKeyID(keyID string) (gemini.Client, error) {
	for _, key := range r.opts.Keys() {
		if key.ID == keyID {
			return r.client(key)
		}
	}
	return nil, errors.New("key not found")
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
		return blockedMessage()
	case gemini.IsEmptyResponse(err):
		return i18n.T("error.emptyResult")
	case errors.As(err, &ue):
		return clip(ue.Error())
	case gemini.IsAuth(err):
		return i18n.T("error.apiKeyInvalid")
	case gemini.Is429(err):
		return i18n.T("error.rateLimited")
	case gemini.IsTransient(err):
		return i18n.T("error.network")
	case errors.Is(err, os.ErrNotExist):
		return i18n.T("error.fileMissing")
	}
	if i18n.IsText(err) || hasHangul(err.Error()) {
		return clip(err.Error())
	}
	return i18n.T("error.failed")
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

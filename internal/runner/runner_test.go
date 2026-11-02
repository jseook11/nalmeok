package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/store"
	"google.golang.org/genai"
)

// ---- fakes -------------------------------------------------------------------

// fakeMedia treats every source file as one track and splits it into
// `pieces` pieces of 60 s each; every piece becomes its own unit.
type fakeMedia struct {
	pieces int
	scans  int
	mu     sync.Mutex
}

func (m *fakeMedia) Scan(path string) (Input, error) {
	m.mu.Lock()
	m.scans++
	m.mu.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		return Input{}, fmt.Errorf("파일을 찾을 수 없습니다: %s", filepath.Base(path))
	}
	if info.IsDir() {
		return Input{Source: path, Kind: "folder", Name: filepath.Base(path),
			Tracks: []Track{{Path: filepath.Join(path, "a.m4a"), Format: "aac-mp4", Seconds: 120}}}, nil
	}
	return Input{Source: path, Kind: "file", Name: filepath.Base(path),
		Tracks: []Track{{Path: path, Format: "aac-mp4", Seconds: 120}}}, nil
}

func (m *fakeMedia) Prepare(t Track, dir string, maxSeconds float64) ([]Piece, error) {
	n := m.pieces
	if n == 0 {
		n = 2
	}
	var out []Piece
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("%s.%03d.m4a", filepath.Base(t.Path), i+1))
		if err := os.WriteFile(p, []byte(fmt.Sprintf("piece-%d", i)), 0o600); err != nil {
			return nil, err
		}
		out = append(out, Piece{Path: p, MIME: "audio/m4a", Seconds: 60})
	}
	return out, nil
}

func (m *fakeMedia) Units(tracks [][]Piece, maxSeconds float64) []UnitPlan {
	var out []UnitPlan
	var pieces []Piece
	for _, ps := range tracks {
		pieces = append(pieces, ps...)
	}
	for i, p := range pieces {
		out = append(out, UnitPlan{Index: i, Pieces: []Piece{p}, Seconds: p.Seconds})
	}
	return out
}

type genFunc func(call int, parts []gemini.Part) (string, string, error)

// fakeClient is one key's client. Uploaded files get URIs "<key>:<file>",
// and the default Generate answers "T(<piece file>)".
type fakeClient struct {
	key string
	w   *fakeWorld

	mu        sync.Mutex
	uploads   []string
	mimes     []string
	generates int
	gen       genFunc
}

func (c *fakeClient) Upload(ctx context.Context, path, mime string) (store.Upload, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.uploads = append(c.uploads, filepath.Base(path))
	c.mimes = append(c.mimes, mime)
	return store.Upload{URI: c.key + ":" + filepath.Base(path), MIME: mime, UploadedAt: time.Now()}, nil
}

func (c *fakeClient) Generate(ctx context.Context, parts []gemini.Part) (string, string, error) {
	c.mu.Lock()
	c.generates++
	n := c.generates
	gen := c.gen
	c.mu.Unlock()
	if gen != nil {
		return gen(n, parts)
	}
	return answer(parts), gemini.FinishStop, nil
}

func answer(parts []gemini.Part) string {
	var names []string
	for _, p := range parts {
		if strings.HasSuffix(p.FileURI, ".m4a") {
			names = append(names, p.FileURI[strings.Index(p.FileURI, ":")+1:])
		}
	}
	return "T(" + strings.Join(names, ",") + ")"
}

func (c *fakeClient) SubmitBatch(ctx context.Context, reqs []gemini.BatchRequest, displayName string) (string, error) {
	c.w.mu.Lock()
	if c.w.submitFailures > 0 {
		c.w.submitFailures--
		c.w.mu.Unlock()
		return "", genai.APIError{Code: 503}
	}
	c.w.mu.Unlock()
	return c.w.submit(reqs)
}

func (c *fakeClient) BatchStatus(ctx context.Context, name string) (string, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	return c.w.batchState, nil
}

func (c *fakeClient) BatchResults(ctx context.Context, name string) (map[string]gemini.Result, error) {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	reqs := c.w.batches[name]
	out := make(map[string]gemini.Result)
	for _, r := range reqs {
		if res, ok := c.w.batchResults[r.Key]; ok {
			out[r.Key] = res
			continue
		}
		if c.w.batchDrop[r.Key] {
			c.w.batchDrop[r.Key] = false // fail only once
			out[r.Key] = gemini.Result{Error: "internal"}
			continue
		}
		out[r.Key] = gemini.Result{Text: "B" + answer(r.Parts), Finish: gemini.FinishStop}
	}
	return out, nil
}

func (c *fakeClient) CancelBatch(ctx context.Context, name string) error {
	c.w.mu.Lock()
	defer c.w.mu.Unlock()
	c.w.cancelled = append(c.w.cancelled, name)
	return nil
}

func (c *fakeClient) Validate(ctx context.Context) error { return nil }

type fakeWorld struct {
	mu             sync.Mutex
	clients        map[string]*fakeClient
	batches        map[string][]gemini.BatchRequest
	batchOrder     []string
	batchState     string
	batchDrop      map[string]bool
	batchResults   map[string]gemini.Result
	submitFailures int
	cancelled      []string
}

func newWorld() *fakeWorld {
	return &fakeWorld{
		clients:      map[string]*fakeClient{},
		batches:      map[string][]gemini.BatchRequest{},
		batchState:   gemini.BatchRunning,
		batchDrop:    map[string]bool{},
		batchResults: map[string]gemini.Result{},
	}
}

func (w *fakeWorld) client(key string) *fakeClient {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, ok := w.clients[key]
	if !ok {
		c = &fakeClient{key: key, w: w}
		w.clients[key] = c
	}
	return c
}

func (w *fakeWorld) newClient(apiKey string) (gemini.Client, error) {
	return w.client(apiKey), nil
}

func (w *fakeWorld) submit(reqs []gemini.BatchRequest) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	name := fmt.Sprintf("batches/%d", len(w.batches)+1)
	w.batches[name] = append([]gemini.BatchRequest(nil), reqs...)
	w.batchOrder = append(w.batchOrder, name)
	return name, nil
}

func (w *fakeWorld) setBatchState(s string) {
	w.mu.Lock()
	w.batchState = s
	w.mu.Unlock()
}

func (w *fakeWorld) setBatchResult(key string, res gemini.Result) {
	w.mu.Lock()
	w.batchResults[key] = res
	w.mu.Unlock()
}

func (w *fakeWorld) failNextSubmit(n int) {
	w.mu.Lock()
	w.submitFailures = n
	w.mu.Unlock()
}

func err429() error { return genai.APIError{Code: 429, Message: "quota"} }

// ---- harness -----------------------------------------------------------------

type harness struct {
	t      *testing.T
	dir    string
	st     *store.Store
	world  *fakeWorld
	media  *fakeMedia
	keys   []config.Key
	r      *Runner
	ctx    context.Context
	cancel context.CancelFunc

	notesMu sync.Mutex
	notes   []string

	sleepMu sync.Mutex
	sleeps  []time.Duration
}

func newHarness(t *testing.T, keys ...config.Key) *harness {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "cfg"))
	t.Setenv("GEMINI_API_KEY", "")
	st, err := store.Load(filepath.Join(dir, "cfg", "nalmeok", "items.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, dir: dir, st: st, world: newWorld(), media: &fakeMedia{}, keys: keys}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	t.Cleanup(h.cancel)
	h.r = h.newRunner()
	return h
}

func (h *harness) newRunner() *Runner {
	return New(Options{
		Store:     h.st,
		Media:     h.media,
		NewClient: h.world.newClient,
		Keys:      func() []config.Key { return h.keys },
		WorkDir:   filepath.Join(h.dir, "cfg", "nalmeok", "work"),
		LogPath:   filepath.Join(h.dir, "cfg", "nalmeok", "nalmeok.log"),
		Notify: func(title, msg string) {
			h.notesMu.Lock()
			h.notes = append(h.notes, msg)
			h.notesMu.Unlock()
		},
		KeyGap:       -1,
		PollInterval: time.Hour, // tests poll explicitly
		Sleep: func(ctx context.Context, d time.Duration) error {
			h.sleepMu.Lock()
			h.sleeps = append(h.sleeps, d)
			h.sleepMu.Unlock()
			return nil
		},
	})
}

func freeKey(id string) config.Key {
	return config.Key{ID: id, Label: id, Tier: "free", APIKey: "AIza-" + id}
}

func paidKey(id string) config.Key {
	return config.Key{ID: id, Label: id, Tier: "paid", APIKey: "AIza-" + id}
}

func (h *harness) source(name string) string {
	h.t.Helper()
	path := filepath.Join(h.dir, "src", name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
		h.t.Fatal(err)
	}
	return path
}

func (h *harness) addOne(path, mode string) store.Item {
	h.t.Helper()
	if err := h.r.Add([]string{path}, mode); err != nil {
		h.t.Fatalf("Add: %v", err)
	}
	items := h.st.List("")
	return items[0]
}

func (h *harness) wait(id string, states ...string) store.Item {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		item, ok := h.st.Get(id)
		if ok {
			for _, s := range states {
				if item.State == s {
					return item
				}
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	item, _ := h.st.Get(id)
	h.t.Fatalf("item state = %q (%s), want %v", item.State, item.Detail, states)
	return item
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// ---- tests -------------------------------------------------------------------

func TestFreeSuccessAcrossTwoUnits(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	src := h.source("강의09.m4a")
	h.r.Start(h.ctx)
	item := h.addOne(src, "free")

	done := h.wait(item.ID, StateDone)
	h.waitNotes(1)
	want := filepath.Join(h.dir, "src", "강의09.md")
	if done.Output != want {
		t.Fatalf("Output = %q, want %q", done.Output, want)
	}
	got := readFile(t, want)
	if got != "T(강의09.m4a.001.m4a)\n\nT(강의09.m4a.002.m4a)\n" {
		t.Fatalf("output = %q", got)
	}
	if done.Progress != 1 || done.Units != nil || done.Batch != nil {
		t.Errorf("done item = %+v", done)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "cfg", "nalmeok", "work", item.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("work dir not removed: %v", err)
	}
	c := h.world.client("AIza-f1")
	if c.generatesNow() != 2 || c.uploadCount() != 2 {
		t.Errorf("generates=%d uploads=%d", c.generatesNow(), c.uploadCount())
	}
	for _, mime := range c.mimesNow() {
		if mime != "audio/m4a" {
			t.Errorf("upload MIME = %q, want audio/m4a", mime)
		}
	}
	logData := readFile(t, filepath.Join(h.dir, "cfg", "nalmeok", "nalmeok.log"))
	if strings.Contains(logData, "AIza-") {
		t.Error("log contains an API key")
	}
}

func TestSingleAudioUsesPromptMultiUsesLivePrompt(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	var prompts []string
	var mu sync.Mutex
	h.world.client("AIza-f1").gen = func(_ int, parts []gemini.Part) (string, string, error) {
		mu.Lock()
		prompts = append(prompts, parts[0].Text)
		mu.Unlock()
		return answer(parts), gemini.FinishStop, nil
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("a.m4a"), "free")
	h.wait(item.ID, StateDone)
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 {
		t.Fatalf("prompts = %d", len(prompts))
	}
	for _, p := range prompts {
		if p != gemini.Prompt {
			t.Errorf("single-audio request used a different prompt")
		}
	}
}

func TestFree429RotatesToSecondKey(t *testing.T) {
	h := newHarness(t, freeKey("f1"), freeKey("f2"))
	h.world.client("AIza-f1").gen = func(int, []gemini.Part) (string, string, error) {
		return "", "", err429()
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")

	done := h.wait(item.ID, StateDone)
	if got := readFile(t, done.Output); got != "T(lec.m4a.001.m4a)\n\nT(lec.m4a.002.m4a)\n" {
		t.Fatalf("output = %q (uploads must come from the key that answered)", got)
	}
	if h.world.client("AIza-f2").generatesNow() != 2 {
		t.Errorf("second key generates = %d", h.world.client("AIza-f2").generatesNow())
	}
}

func TestAll429FreeExhaustedThenSendToBatch(t *testing.T) {
	h := newHarness(t, freeKey("f1"), paidKey("p1"))
	h.world.client("AIza-f1").gen = func(call int, parts []gemini.Part) (string, string, error) {
		if call == 1 {
			return answer(parts), gemini.FinishStop, nil
		}
		return "", "", err429()
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")

	ex := h.wait(item.ID, StateFreeExhausted)
	if ex.Detail != "남은 1분" {
		t.Errorf("Detail = %q", ex.Detail)
	}
	if ex.Progress != 0.5 {
		t.Errorf("Progress = %v", ex.Progress)
	}

	if err := h.r.SendToBatch(item.ID); err != nil {
		t.Fatal(err)
	}
	waiting := h.wait(item.ID, StateBatchWaiting)
	if waiting.Mode != "batch" || waiting.Batch == nil || waiting.Batch.Name == "" || waiting.Batch.KeyID != "p1" {
		t.Fatalf("batch ref = %+v", waiting.Batch)
	}
	reqs := h.world.requests(waiting.Batch.Name)
	if len(reqs) != 1 || reqs[0].Key != "unit-1" {
		t.Fatalf("batch requests = %+v", reqs)
	}
	if !strings.HasPrefix(reqs[0].Parts[1].FileURI, "AIza-p1:") {
		t.Errorf("batch must use files uploaded with the paid key: %+v", reqs[0].Parts)
	}

	h.r.PollOnce(h.ctx) // still running
	if it, _ := h.st.Get(item.ID); it.State != StateBatchWaiting {
		t.Fatalf("state after running poll = %s", it.State)
	}
	h.world.setBatchState(gemini.BatchSucceeded)
	h.r.PollOnce(h.ctx)
	done := h.wait(item.ID, StateDone)
	if got := readFile(t, done.Output); got != "T(lec.m4a.001.m4a)\n\nBT(lec.m4a.002.m4a)\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestBatchPartialFailureThenRetry(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.media.pieces = 3
	h.world.batchDrop["unit-1"] = true
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")

	h.wait(item.ID, StateBatchWaiting)
	h.world.setBatchState(gemini.BatchSucceeded)
	h.r.PollOnce(h.ctx)
	failed := h.wait(item.ID, StateFailed)
	if !strings.Contains(failed.Detail, "1개 조각") {
		t.Errorf("Detail = %q", failed.Detail)
	}
	if failed.Units[0].Text == "" || failed.Units[1].Text != "" || failed.Units[2].Text == "" {
		t.Fatalf("units = %+v", failed.Units)
	}
	uploadsBefore := h.world.client("AIza-p1").uploadCount()

	if err := h.r.Retry(item.ID); err != nil {
		t.Fatal(err)
	}
	waiting := h.wait(item.ID, StateBatchWaiting)
	reqs := h.world.requests(waiting.Batch.Name)
	if len(reqs) != 1 || reqs[0].Key != "unit-1" {
		t.Fatalf("retry requests = %+v", reqs)
	}
	if got := h.world.client("AIza-p1").uploadCount(); got != uploadsBefore {
		t.Errorf("retry re-uploaded fresh files: %d -> %d", uploadsBefore, got)
	}
	h.r.PollOnce(h.ctx)
	done := h.wait(item.ID, StateDone)
	want := "BT(lec.m4a.001.m4a)\n\nBT(lec.m4a.002.m4a)\n\nBT(lec.m4a.003.m4a)\n"
	if got := readFile(t, done.Output); got != want {
		t.Fatalf("output = %q", got)
	}
}

func TestResumeRunningItemAfterRestart(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	src := h.source("lec.m4a")
	work := filepath.Join(h.dir, "cfg", "nalmeok", "work", "item-1")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	p0 := filepath.Join(work, "lec.m4a.001.m4a")
	p1 := filepath.Join(work, "lec.m4a.002.m4a")
	for _, p := range []string{p0, p1} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	err := h.st.Add(store.Item{
		ID: "item-1", Source: src, Kind: "file", Name: "lec.m4a", Mode: "free",
		State: StateRunning, Progress: 0.5, AudioSec: 120,
		Units: []store.Unit{
			{Index: 0, Files: []string{p0}, Seconds: 60, Text: "이미 끝남"},
			{Index: 1, Files: []string{p1}, Seconds: 60},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// A fresh runner (as after an app restart) picks the item up in Start.
	h.r = h.newRunner()
	h.r.Start(h.ctx)
	done := h.wait("item-1", StateDone)
	if got := readFile(t, done.Output); got != "이미 끝남\n\nT(lec.m4a.002.m4a)\n" {
		t.Fatalf("output = %q", got)
	}
	c := h.world.client("AIza-f1")
	if c.generatesNow() != 1 || c.uploadCount() != 1 {
		t.Errorf("generates=%d uploads=%d", c.generatesNow(), c.uploadCount())
	}
	if h.media.scanCount() != 0 {
		t.Errorf("resume re-scanned the source")
	}
}

func TestResumeBatchWaitingPollsOnStart(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	src := h.source("lec.m4a")
	h.world.batches["batches/old"] = []gemini.BatchRequest{{Key: "unit-0", Parts: []gemini.Part{{FileURI: "AIza-p1:x.m4a"}}}}
	h.world.setBatchState(gemini.BatchSucceeded)
	err := h.st.Add(store.Item{
		ID: "item-b", Source: src, Kind: "file", Name: "lec.m4a", Mode: "batch",
		State: StateBatchWaiting, Units: []store.Unit{{Index: 0, Files: []string{"/gone.m4a"}, Seconds: 60}},
		Batch: &store.BatchRef{KeyID: "p1", Name: "batches/old", SubmittedAt: time.Now()},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.r.Start(h.ctx)
	done := h.wait("item-b", StateDone)
	if got := readFile(t, done.Output); got != "BT(x.m4a)\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestMaxTokensFailsUnit(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	h.world.client("AIza-f1").gen = func(int, []gemini.Part) (string, string, error) {
		return "잘린 텍스트", gemini.FinishMaxTokens, nil
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	failed := h.wait(item.ID, StateFailed)
	if !strings.Contains(failed.Detail, "잘렸어요") {
		t.Errorf("Detail = %q", failed.Detail)
	}
	if _, err := os.Stat(filepath.Join(h.dir, "src", "lec.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("no output may be written for a failed item")
	}
}

func TestEmptyTextFailsUnit(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	h.world.client("AIza-f1").gen = func(int, []gemini.Part) (string, string, error) {
		return "  ", gemini.FinishStop, nil
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	failed := h.wait(item.ID, StateFailed)
	if !strings.Contains(failed.Detail, "비어 있어요") {
		t.Errorf("Detail = %q", failed.Detail)
	}
}

func TestBlockedLiveResponseFailsUnit(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	h.world.client("AIza-f1").gen = func(int, []gemini.Part) (string, string, error) {
		return "", gemini.FinishBlocked + "OTHER", nil
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	failed := h.wait(item.ID, StateFailed)
	if failed.Detail != blockedMessage {
		t.Errorf("Detail = %q, want %q", failed.Detail, blockedMessage)
	}
	logData := readFile(t, filepath.Join(h.dir, "cfg", "nalmeok", "nalmeok.log"))
	if !strings.Contains(logData, "OTHER") {
		t.Error("log does not contain the block reason")
	}
}

func TestBlockedBatchResponseFailsItem(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.world.setBatchResult("unit-0", gemini.Result{Finish: gemini.FinishBlocked + "OTHER"})
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")
	h.wait(item.ID, StateBatchWaiting)
	h.world.setBatchState(gemini.BatchSucceeded)
	h.r.PollOnce(h.ctx)
	failed := h.wait(item.ID, StateFailed)
	if failed.Detail != blockedMessage {
		t.Errorf("Detail = %q, want %q", failed.Detail, blockedMessage)
	}
	logData := readFile(t, filepath.Join(h.dir, "cfg", "nalmeok", "nalmeok.log"))
	if !strings.Contains(logData, "blocked") || !strings.Contains(logData, "OTHER") {
		t.Error("log does not contain the block reason")
	}
}

func TestTransientErrorsAreRetried(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	details := make(chan string, 1)
	h.world.client("AIza-f1").gen = func(call int, parts []gemini.Part) (string, string, error) {
		if call > 1 {
			if items := h.st.List(""); len(items) > 0 {
				select {
				case details <- items[0].Detail:
				default:
				}
			}
		}
		if call <= 2 {
			return "", "", genai.APIError{Code: 503}
		}
		return answer(parts), gemini.FinishStop, nil
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	h.wait(item.ID, StateDone)
	if got := h.world.client("AIza-f1").generatesNow(); got != 4 {
		t.Errorf("generates = %d, want 4 (3 for the retried unit, 1 for the next)", got)
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second}
	if got := h.sleepsNow(); !equalDurations(got, want) {
		t.Errorf("sleeps = %v, want %v", got, want)
	}
	select {
	case detail := <-details:
		if detail != retryDetail {
			t.Errorf("detail during retry = %q, want %q", detail, retryDetail)
		}
	default:
		t.Error("no retry detail was visible while retrying")
	}
}

func TestTransientRetriesGiveUpAfterFive(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	h.world.client("AIza-f1").gen = func(int, []gemini.Part) (string, string, error) {
		return "", "", genai.APIError{Code: 503}
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	failed := h.wait(item.ID, StateFailed)
	if failed.Detail != "네트워크 오류" {
		t.Errorf("Detail = %q", failed.Detail)
	}
	if got := h.world.client("AIza-f1").generatesNow(); got != 6 {
		t.Errorf("generates = %d, want 6 (initial + 5 retries)", got)
	}
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 2 * time.Minute}
	if got := h.sleepsNow(); !equalDurations(got, want) {
		t.Errorf("sleeps = %v, want %v", got, want)
	}
}

func TestTransientBatchSubmitIsRetried(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.world.failNextSubmit(2)
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")
	h.wait(item.ID, StateBatchWaiting)
	want := []time.Duration{10 * time.Second, 20 * time.Second}
	if got := h.sleepsNow(); !equalDurations(got, want) {
		t.Errorf("sleeps = %v, want %v", got, want)
	}
}

func TestDefaultBackoffSchedule(t *testing.T) {
	r := New(Options{Store: nil})
	want := []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second, 80 * time.Second, 2 * time.Minute, 2 * time.Minute}
	for i := 1; i <= len(want); i++ {
		if got := r.opts.Backoff(i); got != want[i-1] {
			t.Errorf("Backoff(%d) = %v, want %v", i, got, want[i-1])
		}
	}
}

func equalDurations(a, b []time.Duration) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestOutputCollisionGetsNumber(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	src := h.source("강의09.m4a")
	existing := filepath.Join(h.dir, "src", "강의09.md")
	if err := os.WriteFile(existing, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.r.Start(h.ctx)
	item := h.addOne(src, "free")
	done := h.wait(item.ID, StateDone)
	if done.Output != filepath.Join(h.dir, "src", "강의09 (2).md") {
		t.Fatalf("Output = %q", done.Output)
	}
	if readFile(t, existing) != "keep" {
		t.Error("existing .md was overwritten")
	}
}

func TestFolderOutputInsideFolder(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	folder := filepath.Join(h.dir, "src", "ch07")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	h.r.Start(h.ctx)
	item := h.addOne(folder, "free")
	done := h.wait(item.ID, StateDone)
	if done.Output != filepath.Join(folder, "ch07.md") {
		t.Fatalf("Output = %q", done.Output)
	}
}

func TestCancelRunningItem(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	release := make(chan struct{})
	h.world.client("AIza-f1").gen = func(_ int, parts []gemini.Part) (string, string, error) {
		<-release
		return answer(parts), gemini.FinishStop, nil
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	deadline := time.Now().Add(5 * time.Second)
	for h.world.client("AIza-f1").generatesNow() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := h.r.Cancel(item.ID); err != nil {
		t.Fatal(err)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	it := h.wait(item.ID, StateCanceled)
	if it.Output != "" {
		t.Error("canceled item wrote output")
	}

	// Retry keeps finished units and completes.
	h.world.client("AIza-f1").setGen(nil)
	if err := h.r.Retry(item.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(item.ID, StateDone)
}

func (c *fakeClient) uploadCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.uploads)
}

func (c *fakeClient) mimesNow() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.mimes...)
}

func (h *harness) sleepsNow() []time.Duration {
	h.sleepMu.Lock()
	defer h.sleepMu.Unlock()
	return append([]time.Duration(nil), h.sleeps...)
}

func (c *fakeClient) setGen(g genFunc) {
	c.mu.Lock()
	c.gen = g
	c.mu.Unlock()
}

func (m *fakeMedia) scanCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.scans
}

func (w *fakeWorld) requests(name string) []gemini.BatchRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]gemini.BatchRequest(nil), w.batches[name]...)
}

func (w *fakeWorld) cancelledNames() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.cancelled...)
}

func (h *harness) waitNotes(n int) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.notesMu.Lock()
		got := len(h.notes)
		h.notesMu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("expected %d notifications", n)
}

func (c *fakeClient) generatesNow() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.generates
}

func TestCancelBatchWaiting(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")
	waiting := h.wait(item.ID, StateBatchWaiting)
	if err := h.r.Cancel(item.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(item.ID, StateCanceled)
	if got := h.world.cancelledNames(); len(got) != 1 || got[0] != waiting.Batch.Name {
		t.Errorf("cancelled = %v", got)
	}
}

func TestModeFallsBackToAvailableKeyTier(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	if item.Mode != "batch" {
		t.Errorf("Mode = %q, want batch when only a paid key exists", item.Mode)
	}
	h.wait(item.ID, StateBatchWaiting)
}

func TestAddWithoutKeys(t *testing.T) {
	h := newHarness(t)
	if err := h.r.Add([]string{h.source("a.m4a")}, "free"); err == nil {
		t.Error("Add without keys must fail")
	}
}

func TestScanErrorFailsItem(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	h.r.Start(h.ctx)
	item := h.addOne(filepath.Join(h.dir, "missing.m4a"), "free")
	failed := h.wait(item.ID, StateFailed)
	if !strings.Contains(failed.Detail, "찾을 수 없습니다") {
		t.Errorf("Detail = %q", failed.Detail)
	}
}

func TestRemoveCancelsAndDeletesWorkDir(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	src := h.source("lec.m4a")
	work := filepath.Join(h.dir, "cfg", "nalmeok", "work", "item-remove")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "piece.m4a"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := h.st.Add(store.Item{
		ID: "item-remove", Source: src, Kind: "file", Name: "lec.m4a",
		Mode: "free", State: StateRunning,
	}); err != nil {
		t.Fatal(err)
	}

	if err := h.r.Remove("item-remove"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(work); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("work dir still exists: %v", err)
	}
	item, ok := h.st.Get("item-remove")
	if !ok {
		t.Fatal("Remove deleted the store item; the app owns that step")
	}
	if item.State != StateCanceled {
		t.Errorf("state = %q, want canceled", item.State)
	}
	if err := h.r.Remove("missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Remove(missing) = %v, want ErrNotFound", err)
	}
}

func TestGapMatchesPythonRateLimits(t *testing.T) {
	r := New(Options{Store: nil})
	if g := r.gap(10); g != 12*time.Second {
		t.Errorf("short gap = %v", g)
	}
	if g := r.gap(130 * 60); g != time.Minute {
		t.Errorf("130 min gap = %v", g)
	}
}

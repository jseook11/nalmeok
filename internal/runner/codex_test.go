package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/codexasr"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/store"
)

// fakeCodex stands in for the HTTP client and records what it was asked to
// transcribe.
type fakeCodex struct {
	mu        sync.Mutex
	got       []string
	mimes     []string
	uploads   int
	inFlight  int
	peak      int
	delay     time.Duration
	rateLimit int  // remaining 429 answers
	authFail  bool // answer 401
}

func (c *fakeCodex) Upload(_ context.Context, path, mime string) (store.Upload, error) {
	c.mu.Lock()
	c.uploads++
	c.mu.Unlock()
	return store.Upload{URI: path, MIME: mime, UploadedAt: time.Now()}, nil
}

func (c *fakeCodex) Generate(_ context.Context, req gemini.Request) (gemini.Result, error) {
	c.mu.Lock()
	c.inFlight++
	if c.inFlight > c.peak {
		c.peak = c.inFlight
	}
	delay := c.delay
	limited := c.rateLimit > 0
	if limited {
		c.rateLimit--
	}
	authFail := c.authFail
	c.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	defer func() {
		c.mu.Lock()
		c.inFlight--
		c.mu.Unlock()
	}()
	switch {
	case authFail:
		return gemini.Result{}, &codexasr.HTTPError{Status: 401, Body: "unauthorized"}
	case limited:
		return gemini.Result{}, &codexasr.HTTPError{Status: 429, Body: "rate limited"}
	}

	var texts []string
	for _, p := range req.Parts {
		if !strings.HasPrefix(p.MIME, "audio/") {
			continue
		}
		c.mu.Lock()
		c.got = append(c.got, filepath.Base(p.FileURI))
		c.mimes = append(c.mimes, p.MIME)
		c.mu.Unlock()
		texts = append(texts, "T("+filepath.Base(p.FileURI)+")")
	}
	return gemini.Result{Text: strings.Join(texts, "\n\n"), Finish: gemini.FinishStop}, nil
}

func (c *fakeCodex) SubmitBatch(context.Context, []gemini.BatchRequest, string) (string, error) {
	return "", errCodexNoBatch
}
func (c *fakeCodex) FindBatch(context.Context, string) (string, error) { return "", errCodexNoBatch }
func (c *fakeCodex) BatchStatus(context.Context, string) (string, error) {
	return "", errCodexNoBatch
}
func (c *fakeCodex) BatchResults(context.Context, string) (map[string]gemini.Result, error) {
	return nil, errCodexNoBatch
}
func (c *fakeCodex) CancelBatch(context.Context, string) error { return errCodexNoBatch }
func (c *fakeCodex) Validate(context.Context) error            { return nil }

type codexHarness struct {
	t      *testing.T
	dir    string
	st     *store.Store
	fake   *fakeCodex
	r      *Runner
	ctx    context.Context
	stop   context.CancelFunc
	media  *fakeMedia
	mu     sync.Mutex
	sleeps []time.Duration
}

func newCodexHarness(t *testing.T, auth func() (codexasr.Auth, error)) *codexHarness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Load(filepath.Join(dir, "items.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := &codexHarness{t: t, dir: dir, st: st, fake: &fakeCodex{}, media: &fakeMedia{}}
	h.ctx, h.stop = context.WithCancel(context.Background())
	t.Cleanup(h.stop)
	h.r = New(Options{
		Store:   st,
		Media:   h.media,
		Backend: BackendCodex,
		Codex: CodexOptions{
			Auth:      auth,
			NewClient: func(codexasr.Auth) gemini.Client { return h.fake },
			Jitter:    func() time.Duration { return 0 },
		},
		WorkDir:      filepath.Join(dir, "work"),
		PollInterval: time.Hour,
		Sleep: func(_ context.Context, d time.Duration) error {
			h.mu.Lock()
			h.sleeps = append(h.sleeps, d)
			h.mu.Unlock()
			return nil
		},
	})
	t.Cleanup(func() { _ = h.r.Close() })
	return h
}

func (h *codexHarness) source(name string) string {
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

func (h *codexHarness) wait(id string, states ...string) store.Item {
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
	h.t.Fatalf("state = %q (%s), want %v", item.State, item.Detail, states)
	return item
}

func validCodexAuth() func() (codexasr.Auth, error) {
	return func() (codexasr.Auth, error) { return codexasr.Auth{AccessToken: "token", Path: "auth.json"}, nil }
}

func TestCodexBackendRunsWithoutKeys(t *testing.T) {
	h := newCodexHarness(t, validCodexAuth())
	h.r.Start(h.ctx)

	src := h.source("lecture.m4a")
	// Ask for paid: the backend has no Batch, so it must fall back to free.
	if err := h.r.Add([]string{src}, "batch", ""); err != nil {
		t.Fatal(err)
	}
	item := h.st.List("")[0]
	if item.Mode != "free" {
		t.Fatalf("mode = %q, want free", item.Mode)
	}
	done := h.wait(item.ID, StateDone)
	if done.Output == "" {
		t.Fatal("no output recorded")
	}
	out := filepath.Join(h.dir, "src", "lecture.md")
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if !strings.Contains(string(data), "T(") {
		t.Fatalf("transcript = %q", data)
	}
	if len(h.fake.got) != 2 {
		t.Fatalf("transcribed %d pieces, want 2", len(h.fake.got))
	}
	for _, m := range h.fake.mimes {
		if m != "audio/m4a" {
			t.Fatalf("mime = %q, want audio/m4a", m)
		}
	}
	// There is no server-side copy: the client posts the piece itself, so the
	// upload cache must stay untouched.
	h.fake.mu.Lock()
	uploads := h.fake.uploads
	h.fake.mu.Unlock()
	if uploads != 0 {
		t.Fatalf("uploads = %d, want 0", uploads)
	}
}

func TestCodexDropsKeyPin(t *testing.T) {
	h := newCodexHarness(t, validCodexAuth())
	h.r.Start(h.ctx)

	// A pinned Gemini key must not survive on a codex item: a later Retry
	// would otherwise follow that key's tier.
	if err := h.r.Add([]string{h.source("lecture.m4a")}, "free", "some-gemini-key"); err != nil {
		t.Fatal(err)
	}
	item := h.st.List("")[0]
	if item.KeyID != "" {
		t.Fatalf("key pin = %q, want empty", item.KeyID)
	}
	h.wait(item.ID, StateDone)
}

func TestCodexRunsUnitsConcurrentlyInOrder(t *testing.T) {
	h := newCodexHarness(t, validCodexAuth())
	h.media.pieces = 4
	h.fake.delay = 40 * time.Millisecond
	h.r.Start(h.ctx)

	if err := h.r.Add([]string{h.source("lecture.m4a")}, "free", ""); err != nil {
		t.Fatal(err)
	}
	item := h.st.List("")[0]
	h.wait(item.ID, StateDone)

	h.fake.mu.Lock()
	peak := h.fake.peak
	h.fake.mu.Unlock()
	if peak < 2 {
		t.Fatalf("peak in-flight = %d, want at least 2", peak)
	}

	data, err := os.ReadFile(filepath.Join(h.dir, "src", "lecture.md"))
	if err != nil {
		t.Fatal(err)
	}
	last := -1
	for i := 1; i <= 4; i++ {
		marker := "T(lecture.m4a." + fmt.Sprintf("%03d", i) + ".m4a)"
		at := strings.Index(string(data), marker)
		if at < 0 {
			t.Fatalf("%s missing from transcript", marker)
		}
		if at < last {
			t.Fatalf("%s out of order", marker)
		}
		last = at
	}
}

func TestCodexBackendNeedsLogin(t *testing.T) {
	h := newCodexHarness(t, func() (codexasr.Auth, error) {
		return codexasr.Auth{}, codexasr.ErrNotFound
	})
	h.r.Start(h.ctx)
	err := h.r.Add([]string{h.source("lecture.m4a")}, "free", "")
	if err == nil {
		t.Fatal("Add succeeded without a login")
	}
	if !strings.Contains(err.Error(), "Codex") {
		t.Fatalf("err = %v, want a Codex message", err)
	}
}

func TestCodexBackendExpiredLogin(t *testing.T) {
	h := newCodexHarness(t, func() (codexasr.Auth, error) {
		// exp in the past.
		token := "h.eyJleHAiOjF9.s"
		return codexasr.Auth{AccessToken: token}, nil
	})
	h.r.Start(h.ctx)
	err := h.r.Add([]string{h.source("lecture.m4a")}, "free", "")
	if err == nil {
		t.Fatal("Add succeeded with an expired login")
	}
}

func TestCodexRetriesRateLimit(t *testing.T) {
	h := newCodexHarness(t, validCodexAuth())
	h.media.pieces = 1
	h.fake.rateLimit = 2
	h.r.Start(h.ctx)

	if err := h.r.Add([]string{h.source("lecture.m4a")}, "free", ""); err != nil {
		t.Fatal(err)
	}
	item := h.st.List("")[0]
	h.wait(item.ID, StateDone)

	h.mu.Lock()
	sleeps := append([]time.Duration{}, h.sleeps...)
	h.mu.Unlock()
	if len(sleeps) < 2 {
		t.Fatalf("sleeps = %v, want two backoffs", sleeps)
	}
	if sleeps[0] != 3*time.Second || sleeps[1] != 6*time.Second {
		t.Fatalf("sleeps = %v, want 3s then 6s", sleeps)
	}
}

func TestCodexAuthRejectionFailsItem(t *testing.T) {
	h := newCodexHarness(t, validCodexAuth())
	h.media.pieces = 1
	h.fake.authFail = true
	h.r.Start(h.ctx)

	if err := h.r.Add([]string{h.source("lecture.m4a")}, "free", ""); err != nil {
		t.Fatal(err)
	}
	item := h.st.List("")[0]
	failed := h.wait(item.ID, StateFailed)
	if !strings.Contains(failed.Detail, "Codex") {
		t.Fatalf("detail = %q, want a Codex message", failed.Detail)
	}
}

func TestSendToBatchOnCodex(t *testing.T) {
	h := newCodexHarness(t, validCodexAuth())
	if err := h.r.SendToBatch("missing"); err != errCodexNoBatch {
		t.Fatalf("err = %v, want errCodexNoBatch", err)
	}
}

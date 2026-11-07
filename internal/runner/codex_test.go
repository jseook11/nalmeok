package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/codexasr"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/store"
)

// fakeCodex stands in for the HTTP client and records what it was asked to
// transcribe.
type fakeCodex struct {
	got   []string
	mimes []string
}

func (c *fakeCodex) Upload(_ context.Context, path, mime string) (store.Upload, error) {
	return store.Upload{URI: path, MIME: mime, UploadedAt: time.Now()}, nil
}

func (c *fakeCodex) Generate(_ context.Context, req gemini.Request) (gemini.Result, error) {
	var texts []string
	for _, p := range req.Parts {
		if !strings.HasPrefix(p.MIME, "audio/") {
			continue
		}
		c.got = append(c.got, filepath.Base(p.FileURI))
		c.mimes = append(c.mimes, p.MIME)
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
	t     *testing.T
	dir   string
	st    *store.Store
	fake  *fakeCodex
	r     *Runner
	ctx   context.Context
	stop  context.CancelFunc
	media *fakeMedia
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
		Store:          st,
		Media:          h.media,
		Backend:        BackendCodex,
		CodexAuth:      auth,
		NewCodexClient: func(codexasr.Auth) gemini.Client { return h.fake },
		WorkDir:        filepath.Join(dir, "work"),
		PollInterval:   time.Hour,
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

func TestSendToBatchOnCodex(t *testing.T) {
	h := newCodexHarness(t, validCodexAuth())
	if err := h.r.SendToBatch("missing"); err != errCodexNoBatch {
		t.Fatalf("err = %v, want errCodexNoBatch", err)
	}
}

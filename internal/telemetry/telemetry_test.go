package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sync"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/gemini"
)

// clearSignalEnv keeps tests independent of the developer's shell.
func clearSignalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NALMEOK_SIGNALS", "")
	t.Setenv("NALMEOK_SIGNAL_URL", "")
}

type captured struct {
	V       int    `json:"v"`
	App     string `json:"app"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Install string `json:"install"`
	Events  []struct {
		T          int64  `json:"t"`
		Mode       string `json:"mode"`
		Outcome    string `json:"outcome"`
		Code       *int   `json:"code"`
		UnderQuota *bool  `json:"under_quota"`
		Attempt    int    `json:"attempt"`
	} `json:"events"`
}

func TestBatchingAndBodyShape(t *testing.T) {
	clearSignalEnv(t)
	var mu sync.Mutex
	var got []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/signal" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var p captured
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("decode body: %v", err)
		}
		mu.Lock()
		got = append(got, p)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	fixed := time.Unix(1793750400, 0).UTC()
	c := New(Options{
		Endpoint: srv.URL,
		App:      "0.9.3",
		Dir:      t.TempDir(),
		HTTP:     srv.Client(),
		Now:      func() time.Time { return fixed },
	})
	if c == nil {
		t.Fatal("New returned nil")
	}
	for i := 0; i < 50; i++ {
		c.Record(Event{Time: fixed, Mode: "free", Outcome: OK})
	}
	under := true
	for i := 0; i < 10; i++ {
		c.Record(Event{
			Time: fixed.Add(time.Duration(i) * time.Second), Mode: "batch",
			Outcome: RateLimited, Code: 429, UnderQuota: &under, Attempt: 2,
		})
	}
	c.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("got %d posts, want 2", len(got))
	}
	if len(got[0].Events) != 50 || len(got[1].Events) != 10 {
		t.Fatalf("batch sizes = %d, %d; want 50, 10", len(got[0].Events), len(got[1].Events))
	}

	p := got[0]
	if p.V != 1 {
		t.Errorf("v = %d, want 1", p.V)
	}
	if p.App != "0.9.3" {
		t.Errorf("app = %q", p.App)
	}
	switch p.OS {
	case "windows", "darwin", "linux":
	default:
		t.Errorf("os = %q", p.OS)
	}
	switch p.Arch {
	case "x64", "arm64", "other":
	default:
		t.Errorf("arch = %q", p.Arch)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{16,32}$`).MatchString(p.Install) {
		t.Errorf("install = %q", p.Install)
	}

	ev := p.Events[0]
	if ev.T != fixed.Unix() || ev.Mode != "free" || ev.Outcome != OK || ev.Attempt != 1 {
		t.Errorf("first event = %+v", ev)
	}
	if ev.Code != nil {
		t.Errorf("code = %v, want omitted for 0", *ev.Code)
	}
	if ev.UnderQuota != nil {
		t.Errorf("under_quota = %v, want null", *ev.UnderQuota)
	}

	ev = got[1].Events[0]
	if ev.Mode != "batch" || ev.Outcome != RateLimited || ev.Attempt != 2 {
		t.Errorf("rate-limited event = %+v", ev)
	}
	if ev.Code == nil || *ev.Code != 429 {
		t.Errorf("code = %v, want 429", ev.Code)
	}
	if ev.UnderQuota == nil || !*ev.UnderQuota {
		t.Errorf("under_quota = %v, want true", ev.UnderQuota)
	}
}

func TestQueuedFiftyFlushesBeforeInterval(t *testing.T) {
	clearSignalEnv(t)
	sizes := make(chan int, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p captured
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("decode body: %v", err)
		}
		sizes <- len(p.Events)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	fixed := time.Unix(1793750400, 0)
	c := New(Options{
		Endpoint:   srv.URL,
		App:        "0.9.3",
		Dir:        t.TempDir(),
		HTTP:       srv.Client(),
		Now:        func() time.Time { return fixed },
		FlushEvery: time.Hour, // only the 50-event trigger can explain a flush
	})
	c.Start()
	defer c.Close()

	for i := 0; i < 60; i++ {
		c.Record(Event{Time: fixed, Mode: "free", Outcome: OK})
	}
	select {
	case n := <-sizes:
		if n != maxBatch {
			t.Fatalf("first post had %d events, want %d", n, maxBatch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("50 queued events did not trigger a flush")
	}
	c.Close()
	select {
	case n := <-sizes:
		if n != 10 {
			t.Fatalf("second post had %d events, want 10", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not flush the remainder")
	}
}

func TestDisabledRecordsNothing(t *testing.T) {
	clearSignalEnv(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	fixed := time.Unix(1793750400, 0)
	newClient := func(enabled func() bool) *Client {
		return New(Options{
			Endpoint: srv.URL, App: "0.9.3", Dir: t.TempDir(),
			HTTP: srv.Client(), Now: func() time.Time { return fixed },
			Enabled: enabled,
		})
	}

	// Recording while disabled drops every event.
	var on atomic.Bool
	c := newClient(func() bool { return on.Load() })
	for i := 0; i < 60; i++ {
		c.Record(Event{Time: fixed, Mode: "free", Outcome: OK})
	}
	c.Start()
	on.Store(true)
	c.Close()

	// Turning off between Record and flush must also silence the flush.
	on.Store(true)
	c2 := newClient(func() bool { return on.Load() })
	c2.Record(Event{Time: fixed, Mode: "free", Outcome: OK})
	on.Store(false)
	c2.Close()

	if n := calls.Load(); n != 0 {
		t.Fatalf("server got %d requests, want 0", n)
	}
}

func TestDevBuildIsDisabledAndNilIsSafe(t *testing.T) {
	clearSignalEnv(t)
	for _, app := range []string{"", "0.9.2-dev"} {
		if c := New(Options{App: app, Dir: t.TempDir()}); c != nil {
			t.Errorf("App %q: New returned a client, want nil", app)
		}
	}
	// A custom endpoint alone does not enable a dev build; only the env does.
	if c := New(Options{App: "0.9.2-dev", Endpoint: "http://127.0.0.1:1"}); c != nil {
		t.Error("dev build enabled by Endpoint option")
	}

	var c *Client
	c.Record(Event{Time: time.Now(), Mode: "free", Outcome: OK})
	c.Start()
	c.Close()
	st, err := c.Status(context.Background())
	if err != nil {
		t.Errorf("nil Status error = %v", err)
	}
	if st != (Status{}) {
		t.Errorf("nil Status = %+v", st)
	}

	t.Setenv("NALMEOK_SIGNAL_URL", "http://127.0.0.1:1")
	if c := New(Options{App: "0.9.2-dev"}); c == nil {
		t.Error("dev build disabled even though NALMEOK_SIGNAL_URL is set")
	}

	t.Setenv("NALMEOK_SIGNALS", "0")
	if c := New(Options{App: "0.9.3"}); c != nil {
		t.Error("NALMEOK_SIGNALS=0 did not disable signals")
	}
}

func TestInstallIDPersistsForDayAndRotates(t *testing.T) {
	clearSignalEnv(t)
	var mu sync.Mutex
	var ids []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p captured
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Errorf("decode body: %v", err)
		}
		mu.Lock()
		ids = append(ids, p.Install)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	dir := t.TempDir()
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	flush := func() string {
		c := New(Options{
			Endpoint: srv.URL, App: "0.9.3", Dir: dir,
			HTTP: srv.Client(), Now: func() time.Time { return now },
		})
		c.Record(Event{Time: now, Mode: "free", Outcome: OK})
		c.Close()
		mu.Lock()
		defer mu.Unlock()
		return ids[len(ids)-1]
	}

	id1 := flush()
	if len(id1) != 22 || !validID(id1) {
		t.Fatalf("install id = %q", id1)
	}
	var f idFile
	data, err := os.ReadFile(dir + "/signal-id.json")
	if err != nil {
		t.Fatalf("read id file: %v", err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse id file: %v", err)
	}
	if f.ID != id1 || f.Day != "2026-11-04" {
		t.Fatalf("id file = %+v, want id %q day 2026-11-04", f, id1)
	}

	// Same UTC day: the stored id is reused.
	if id2 := flush(); id2 != id1 {
		t.Fatalf("same-day install id changed: %q -> %q", id1, id2)
	}

	// New UTC day: a fresh id, and it is written to disk.
	now = time.Date(2026, 11, 5, 0, 0, 1, 0, time.UTC)
	id3 := flush()
	if id3 == id1 {
		t.Fatal("install id did not rotate on the UTC day change")
	}
	if !validID(id3) {
		t.Fatalf("rotated install id = %q", id3)
	}
	data, err = os.ReadFile(dir + "/signal-id.json")
	if err != nil {
		t.Fatalf("read id file: %v", err)
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse id file: %v", err)
	}
	if f.ID != id3 || f.Day != "2026-11-05" {
		t.Fatalf("id file = %+v, want id %q day 2026-11-05", f, id3)
	}
}

func TestStatusCachesForAMinute(t *testing.T) {
	clearSignalEnv(t)
	const body = `{
		"updated": "2026-11-04T09:30:00Z",
		"free":  {"level":"shaky","success_rate":0.78,"fake_429_rate":0.12,"events":143,"installs":9,"window_minutes":30},
		"batch": {"level":"good","success_rate":0.99,"fake_429_rate":0,"events":40,"installs":4,"window_minutes":180}
	}`
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/status" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 4, 9, 30, 0, 0, time.UTC)
	c := New(Options{
		Endpoint: srv.URL, App: "0.9.3", Dir: t.TempDir(),
		HTTP: srv.Client(), Now: func() time.Time { return now },
	})

	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Updated != "2026-11-04T09:30:00Z" || st.Free.Level != "shaky" || st.Batch.Level != "good" {
		t.Errorf("Status = %+v", st)
	}
	if st.Free.SuccessRate == nil || *st.Free.SuccessRate != 0.78 {
		t.Errorf("free success_rate = %v", st.Free.SuccessRate)
	}
	if st.Free.Fake429Rate == nil || *st.Free.Fake429Rate != 0.12 {
		t.Errorf("free fake_429_rate = %v", st.Free.Fake429Rate)
	}
	if st.Free.Events != 143 || st.Free.Installs != 9 || st.Free.WindowMinutes != 30 {
		t.Errorf("free health = %+v", st.Free)
	}

	if _, err := c.Status(context.Background()); err != nil {
		t.Fatalf("Status (cached): %v", err)
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("calls within 60 s = %d, want 1", n)
	}

	now = now.Add(61 * time.Second)
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatalf("Status (expired): %v", err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("calls after 61 s = %d, want 2", n)
	}
}

func TestStatusSendsDayIDOnlyWhenEnabled(t *testing.T) {
	clearSignalEnv(t)
	var gotQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		fmt.Fprint(w, `{"free":{"level":"poor","events":9,"installs":3,"window_minutes":60,"others_failing":2},"batch":{"level":"unknown"}}`)
	}))
	defer srv.Close()

	c := New(Options{
		Endpoint: srv.URL, App: "0.9.3", Dir: t.TempDir(), HTTP: srv.Client(),
		Enabled: func() bool { return true },
	})
	st, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	q, _ := gotQuery.Load().(string)
	if !strings.HasPrefix(q, "install=") || !validID(strings.TrimPrefix(q, "install=")) {
		t.Errorf("query with signals on = %q, want install=<day id>", q)
	}
	if st.Free.OthersFailing == nil || *st.Free.OthersFailing != 2 {
		t.Errorf("others_failing = %v, want 2", st.Free.OthersFailing)
	}

	off := New(Options{
		Endpoint: srv.URL, App: "0.9.3", Dir: t.TempDir(), HTTP: srv.Client(),
		Enabled: func() bool { return false },
	})
	if _, err := off.Status(context.Background()); err != nil {
		t.Fatalf("Status (off): %v", err)
	}
	if q, _ := gotQuery.Load().(string); q != "" {
		t.Errorf("query with signals off = %q, want none", q)
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		outcome  string
		wantCode int
	}{
		{"nil", nil, OK, 0},
		{"canceled", context.Canceled, "", 0},
		{"wrapped canceled", fmt.Errorf("upload: %w", context.Canceled), "", 0},
		{"empty response", &gemini.EmptyResponseError{Finish: "STOP"}, Empty, 0},
		{"auth 401", errors.New("Error 401, Message: unauthorized"), Auth, 401},
		{"auth 403", errors.New("Error 403, Message: forbidden"), Auth, 403},
		{"rate limited", errors.New("Error 429, Message: quota exceeded"), RateLimited, 429},
		{"server 500", errors.New("Error 500, Message: boom"), Server, 500},
		{"server 503", errors.New("Error 503, Message: unavailable"), Server, 503},
		{"deadline", fmt.Errorf("upload: %w", context.DeadlineExceeded), Timeout, 0},
		{"timeout text", errors.New("dial tcp: i/o timeout"), Timeout, 0},
		{"408", errors.New("Error 408, Message: request timeout"), Timeout, 408},
		{"network", errors.New("connection reset by peer"), Network, 0},
		{"blocked 400", errors.New("Error 400, Message: request blocked for safety"), Other, 400},
		{"other", errors.New("something odd"), Other, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcome, code := Classify(tt.err)
			if outcome != tt.outcome || code != tt.wantCode {
				t.Errorf("Classify(%v) = (%q, %d), want (%q, %d)",
					tt.err, outcome, code, tt.outcome, tt.wantCode)
			}
		})
	}
}

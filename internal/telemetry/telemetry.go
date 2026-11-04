// Package telemetry reports anonymous outcomes of Gemini requests to the
// Nalmeok site so the app can ask "is the free tier working right now?".
// Nothing identifies a person: the install id is random and rotates every UTC
// day, and no API key, file name or prompt ever leaves the app.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jseook11/nalmeok/internal/gemini"
)

// Outcomes match the server contract.
const (
	OK          = "ok"
	RateLimited = "rate_limited"
	Server      = "server"
	Empty       = "empty"
	Timeout     = "timeout"
	Network     = "network"
	Auth        = "auth"
	Blocked     = "blocked"
	Other       = "other"
)

const (
	defaultEndpoint = "https://nalmeok.seook.dev"
	defaultFlush    = 30 * time.Second
	httpTimeout     = 10 * time.Second
	statusTimeout   = 5 * time.Second
	statusTTL       = 60 * time.Second
	maxBatch        = 50
	bufferCap       = 500
	closeTimeout    = 2 * time.Second
	dayLayout       = "2006-01-02"
)

// Event is one request outcome.
type Event struct {
	Time       time.Time
	Mode       string // "free" | "batch"
	Outcome    string
	Code       int   // HTTP status, 0 = none
	UnderQuota *bool // only for RateLimited; nil otherwise
	Attempt    int   // 1-based retry number of the same request
}

// Options configures a Client.
type Options struct {
	Endpoint   string           // base URL, default "https://nalmeok.seook.dev"; env NALMEOK_SIGNAL_URL overrides
	App        string           // app version, e.g. "0.9.3"
	Dir        string           // config dir; the daily install id lives in <Dir>/signal-id.json
	Enabled    func() bool      // read on every Record and flush; nil = always on
	HTTP       *http.Client     // nil = 10 s timeout client
	Now        func() time.Time // nil = time.Now
	FlushEvery time.Duration    // default 30 s
}

// Client buffers events and posts them in batches. A nil *Client is valid and
// every method on it is a no-op.
type Client struct {
	endpoint   string
	app        string
	dir        string
	enabled    func() bool
	httpc      *http.Client
	now        func() time.Time
	flushEvery time.Duration
	osName     string
	archName   string

	mu       sync.Mutex
	buf      []Event
	closed   bool
	started  bool
	cooldown time.Time // no POSTs before this time after a 429
	id       string
	idDay    string

	startOnce sync.Once
	closeOnce sync.Once
	stop      chan struct{}
	done      chan struct{}
	wake      chan struct{}

	statusMu  sync.Mutex
	status    Status
	statusAt  time.Time
	hasStatus bool
}

// Health is the judged health of one mode.
type Health struct {
	Level         string   `json:"level"` // good|shaky|poor|bad|unknown
	SuccessRate   *float64 `json:"success_rate"`
	Fake429Rate   *float64 `json:"fake_429_rate"`
	Events        int      `json:"events"`
	Installs      int      `json:"installs"`
	WindowMinutes int      `json:"window_minutes"`
	// OthersFailing is how many other installs failed on free in the last
	// hour. Only the free tier has it, and only when the request carried this
	// app's day id (signals on); nil otherwise.
	OthersFailing *int `json:"others_failing,omitempty"`
}

// Status is the server's public health snapshot.
type Status struct {
	Updated string `json:"updated"`
	Free    Health `json:"free"`
	Batch   Health `json:"batch"`
}

// New never fails. It returns nil (disabled) for a dev build ("-dev" suffix
// or empty App) unless NALMEOK_SIGNAL_URL is set, and for NALMEOK_SIGNALS=0.
func New(opts Options) *Client {
	if strings.TrimSpace(os.Getenv("NALMEOK_SIGNALS")) == "0" {
		return nil
	}
	envURL := strings.TrimSpace(os.Getenv("NALMEOK_SIGNAL_URL"))
	app := strings.TrimSpace(opts.App)
	if envURL == "" && (app == "" || strings.HasSuffix(app, "-dev")) {
		// Development runs must not pollute the public health data.
		return nil
	}
	endpoint := envURL
	if endpoint == "" {
		endpoint = strings.TrimSpace(opts.Endpoint)
	}
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	endpoint = strings.TrimRight(endpoint, "/")

	httpc := opts.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: httpTimeout}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	flushEvery := opts.FlushEvery
	if flushEvery <= 0 {
		flushEvery = defaultFlush
	}

	return &Client{
		endpoint:   endpoint,
		app:        app,
		dir:        opts.Dir,
		enabled:    opts.Enabled,
		httpc:      httpc,
		now:        now,
		flushEvery: flushEvery,
		osName:     osName(runtime.GOOS),
		archName:   archName(runtime.GOARCH),
		buf:        make([]Event, 0, maxBatch),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		wake:       make(chan struct{}, 1),
	}
}

func (c *Client) on() bool {
	return c.enabled == nil || c.enabled()
}

// Record queues one event. It never blocks on the network; when the buffer is
// full the event is dropped.
func (c *Client) Record(ev Event) {
	if c == nil || !c.on() {
		return
	}
	c.mu.Lock()
	if c.closed || len(c.buf) >= bufferCap {
		c.mu.Unlock()
		return
	}
	c.buf = append(c.buf, ev)
	full := len(c.buf) >= maxBatch
	c.mu.Unlock()
	if full {
		select {
		case c.wake <- struct{}{}:
		default: // a flush is already pending
		}
	}
}

// Start starts the background flusher. It is safe to call more than once.
func (c *Client) Start() {
	if c == nil {
		return
	}
	c.startOnce.Do(func() {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return
		}
		c.started = true
		c.mu.Unlock()
		go c.loop()
	})
}

// Close makes a best-effort final flush and waits at most 2 s.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		started := c.started
		c.mu.Unlock()

		if started {
			close(c.stop)
			select {
			case <-c.done:
			case <-time.After(closeTimeout):
			}
			return
		}
		// The flusher never ran, so send what is buffered now.
		ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
		defer cancel()
		c.flush(ctx)
	})
}

func (c *Client) loop() {
	defer close(c.done)
	ticker := time.NewTicker(c.flushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-c.stop:
			ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
			c.flush(ctx)
			cancel()
			return
		case <-ticker.C:
			c.flush(context.Background())
		case <-c.wake:
			c.flush(context.Background())
		}
	}
}

// flush drains the buffer in batches. A failed batch is dropped; later events
// wait for the next interval rather than being retried immediately.
func (c *Client) flush(ctx context.Context) {
	if !c.on() {
		return
	}
	c.mu.Lock()
	if c.now().Before(c.cooldown) {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	for {
		evs := c.take(maxBatch)
		if len(evs) == 0 {
			return
		}
		if err := c.post(ctx, evs); err != nil {
			if errors.Is(err, errRateLimited) {
				// The site asked for room: skip the next interval too.
				c.mu.Lock()
				c.cooldown = c.now().Add(c.flushEvery)
				c.mu.Unlock()
			}
			return
		}
	}
}

func (c *Client) take(n int) []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.buf) == 0 {
		return nil
	}
	if n > len(c.buf) {
		n = len(c.buf)
	}
	out := make([]Event, n)
	copy(out, c.buf[:n])
	c.buf = c.buf[n:]
	return out
}

var errRateLimited = errors.New("signal: rate limited")

type wireEvent struct {
	T          int64  `json:"t"`
	Mode       string `json:"mode"`
	Outcome    string `json:"outcome"`
	Code       int    `json:"code,omitempty"` // omitted when 0
	UnderQuota *bool  `json:"under_quota"`    // null when nil
	Attempt    int    `json:"attempt"`
}

type payload struct {
	V       int         `json:"v"`
	App     string      `json:"app"`
	OS      string      `json:"os"`
	Arch    string      `json:"arch"`
	Install string      `json:"install"`
	Events  []wireEvent `json:"events"`
}

func (c *Client) post(ctx context.Context, evs []Event) error {
	now := c.now()
	events := make([]wireEvent, len(evs))
	for i, ev := range evs {
		t := ev.Time
		if t.IsZero() {
			t = now // a zero time would be rejected by the server
		}
		attempt := ev.Attempt
		if attempt < 1 {
			attempt = 1
		}
		if attempt > 20 {
			attempt = 20
		}
		events[i] = wireEvent{
			T:          t.Unix(),
			Mode:       ev.Mode,
			Outcome:    ev.Outcome,
			Code:       ev.Code,
			UnderQuota: ev.UnderQuota,
			Attempt:    attempt,
		}
	}
	body, err := json.Marshal(payload{
		V:       1,
		App:     c.app,
		OS:      c.osName,
		Arch:    c.archName,
		Install: c.install(),
		Events:  events,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+"/api/signal", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain a little so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode == http.StatusTooManyRequests {
		return errRateLimited
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("signal: status %d", resp.StatusCode)
	}
	return nil
}

type idFile struct {
	ID  string `json:"id"`
	Day string `json:"day"`
}

// install returns today's install id, creating and persisting a new one when
// the UTC day changes so ids cannot be linked across days.
func (c *Client) install() string {
	day := c.now().UTC().Format(dayLayout)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.id != "" && c.idDay == day {
		return c.id
	}

	path := ""
	if c.dir != "" {
		path = filepath.Join(c.dir, "signal-id.json")
		if data, err := os.ReadFile(path); err == nil {
			var f idFile
			if json.Unmarshal(data, &f) == nil && f.Day == day && validID(f.ID) {
				c.id, c.idDay = f.ID, day
				return c.id
			}
		}
	}

	c.id, c.idDay = newID(), day
	if path != "" {
		if data, err := json.Marshal(idFile{ID: c.id, Day: day}); err == nil {
			// A failed write only costs persistence; the in-memory id still works.
			_ = os.WriteFile(path, data, 0o600)
		}
	}
	return c.id
}

// newID makes a 22-character base64url id from 16 random bytes.
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // crypto/rand.Read does not fail after startup
	return base64.RawURLEncoding.EncodeToString(b)
}

const idAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-"

func validID(s string) bool {
	if len(s) < 16 || len(s) > 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(idAlphabet, rune(s[i])) {
			return false
		}
	}
	return true
}

func osName(goos string) string {
	switch goos {
	case "windows", "darwin", "linux":
		return goos
	default:
		return "linux"
	}
}

func archName(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return "other"
	}
}

// Status fetches the public health snapshot, cached in memory for 60 s.
func (c *Client) Status(ctx context.Context) (Status, error) {
	if c == nil {
		return Status{}, nil
	}
	c.statusMu.Lock()
	if c.hasStatus && c.now().Sub(c.statusAt) < statusTTL {
		st := c.status
		c.statusMu.Unlock()
		return st, nil
	}
	c.statusMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, statusTimeout)
	defer cancel()
	// With signals on, the day id lets the server leave this app's own
	// failures out of others_failing ("is it only me?"). With signals off the
	// app sends nothing identifying, not even here.
	target := c.endpoint + "/api/status"
	if c.on() {
		target += "?install=" + url.QueryEscape(c.install())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Status{}, err
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Status{}, fmt.Errorf("status: HTTP %d", resp.StatusCode)
	}
	var st Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return Status{}, err
	}

	c.statusMu.Lock()
	c.status, c.statusAt, c.hasStatus = st, c.now(), true
	c.statusMu.Unlock()
	return st, nil
}

// Classify maps a request error to an Outcome and HTTP code. A nil error is
// OK. A cancelled request returns ("", 0): the caller must not record it.
func Classify(err error) (string, int) {
	if err == nil {
		return OK, 0
	}
	if errors.Is(err, context.Canceled) {
		return "", 0
	}
	code := gemini.StatusCode(err)
	switch {
	case gemini.IsEmptyResponse(err):
		return Empty, code
	case gemini.IsAuth(err):
		return Auth, code
	case gemini.Is429(err):
		return RateLimited, code
	case code >= 500:
		return Server, code
	case errors.Is(err, context.DeadlineExceeded),
		strings.Contains(strings.ToLower(err.Error()), "timeout"):
		return Timeout, code
	case gemini.IsTransient(err):
		return Network, code
	default:
		// Includes 400 blocked/safety responses: the runner may pass Blocked
		// itself when it knows the cause.
		return Other, code
	}
}

package runner

import (
	"context"
	"errors"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/jseook11/nalmeok/internal/codexasr"
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
)

// BackendCodex is the experimental transcription backend that reuses a local
// Codex login instead of a Gemini API key.
const BackendCodex = "codex"

// DefaultCodexConcurrency is how many codex requests run side by side. The
// endpoint took 96 concurrent short requests in codex-asr's test, but long
// lecture pieces are heavier, so this stays modest.
const DefaultCodexConcurrency = 8

// DefaultCodexJitter is the longest random delay before a codex request.
const DefaultCodexJitter = time.Second

var errCodexNoBatch = userError{i18n.Error("error.codexNoBatch")}

// CodexOptions configures the codex backend. Zero fields get defaults when
// Backend is "codex".
type CodexOptions struct {
	// Auth returns the local Codex login. nil reads $CODEX_HOME or
	// ~/.codex/auth.json.
	Auth func() (codexasr.Auth, error)
	// NewClient builds the backend's client from a login. nil calls the real
	// endpoint; tests inject a fake.
	NewClient func(codexasr.Auth) gemini.Client
	// Concurrency is how many units run at once. 0 uses
	// DefaultCodexConcurrency.
	Concurrency int
	// Jitter returns the delay before one request, spreading simultaneous
	// starts. nil uses a random delay up to DefaultCodexJitter.
	Jitter func() time.Duration
}

// codexBackend transcribes through the endpoint Codex Desktop uses, with the
// login already on this machine. There is one credential and no Batch, so the
// free lane runs every unfinished unit at once under a semaphore instead of
// rotating keys one request at a time.
type codexBackend struct{}

func (codexBackend) defaults(o *Options) {
	// Codex has no documented rate limit, and the free lane's Gemini pacing
	// only slows it down.
	o.KeyGap = -1
	o.EarlyEstimate = false
	// Pieces stay short for this endpoint (see codexasr.UnitMaxSeconds). The
	// caller may override the length; 0 means the backend's own default.
	if o.UnitMaxSeconds <= 0 {
		o.UnitMaxSeconds = float64(codexasr.UnitMaxSeconds)
	}
	if o.Codex.Auth == nil {
		o.Codex.Auth = codexasr.Find
	}
	if o.Codex.Concurrency <= 0 {
		o.Codex.Concurrency = DefaultCodexConcurrency
	}
	if o.Codex.Jitter == nil {
		o.Codex.Jitter = func() time.Duration {
			return rand.N(DefaultCodexJitter)
		}
	}
}

func (codexBackend) resolveMode(r *Runner, _, _ string) (string, error) {
	// Batch is a Gemini feature; this backend always runs live.
	if _, err := r.codexAuth(); err != nil {
		return "", err
	}
	return "free", nil
}

func (codexBackend) usesKeys() bool { return false }

func (codexBackend) candidates(*Runner, string, float64) ([]config.Key, error) {
	return []config.Key{codexKey()}, nil
}

// parts keeps the files on disk. The client posts each piece itself, so there
// is no server-side upload to cache; reference PDFs are left out because the
// endpoint takes audio only.
func (codexBackend) parts(_ *Runner, _ context.Context, _ gemini.Client, _, _ string, unit store.Unit, _ []string) (gemini.Request, error) {
	parts := make([]gemini.Part, 0, len(unit.Files))
	for i, path := range unit.Files {
		parts = append(parts, gemini.Part{FileURI: path, MIME: unitMIME(unit, i)})
	}
	return gemini.Request{System: gemini.PromptFor(len(unit.Files)), Parts: parts}, nil
}

func (codexBackend) free(r *Runner, ctx context.Context, item store.Item) error {
	return r.runFreeCodex(ctx, item)
}

func (codexBackend) client(r *Runner, _ config.Key) (gemini.Client, error) {
	return r.codexClientFor()
}

func (codexBackend) batches() bool { return false }
func (codexBackend) signals() bool { return false }

// message maps the login and format errors a user can act on.
func (codexBackend) message(err error) (string, bool) {
	switch {
	case errors.Is(err, codexasr.ErrExpired):
		return i18n.T("error.codexAuthExpired"), true
	case errors.Is(err, codexasr.ErrNotFound), errors.Is(err, codexasr.ErrNoChatGPT):
		return i18n.T("error.codexAuthMissing"), true
	case errors.Is(err, codexasr.ErrUnsupported):
		return i18n.T("error.codexFormat"), true
	case errors.Is(err, codexasr.ErrNoText):
		return i18n.T("error.emptyResult"), true
	}
	return "", false
}

// codexAuth returns the local Codex login, or an error naming what is missing.
func (r *Runner) codexAuth() (codexasr.Auth, error) {
	auth, err := r.opts.Codex.Auth()
	if err != nil {
		return codexasr.Auth{}, codexAuthError(err)
	}
	if auth.Expired(r.opts.Now()) {
		return codexasr.Auth{}, codexAuthError(codexasr.ErrExpired)
	}
	return auth, nil
}

// codexClientFor builds the adapter once per runner.
func (r *Runner) codexClientFor() (gemini.Client, error) {
	r.mu.Lock()
	c, ok := r.clients[codexasr.KeyID]
	r.mu.Unlock()
	if ok {
		return c, nil
	}
	auth, err := r.codexAuth()
	if err != nil {
		return nil, err
	}
	var client gemini.Client
	if r.opts.Codex.NewClient != nil {
		client = r.opts.Codex.NewClient(auth)
	} else {
		client = codexClient{c: codexasr.New(auth)}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.clients[codexasr.KeyID]; ok {
		return existing, nil
	}
	r.clients[codexasr.KeyID] = client
	return client, nil
}

// codexKey is the synthetic key the free lane iterates over.
func codexKey() config.Key {
	return config.Key{ID: codexasr.KeyID, Label: "Codex", Tier: "free"}
}

// runFreeCodex transcribes every pending unit concurrently and stops at the
// first failure. Unlike the Gemini lane there is one key and no rate pacing,
// so units do not depend on each other. Each unit's text is saved as it
// arrives, so progress moves and a restart resumes with what is left.
func (r *Runner) runFreeCodex(ctx context.Context, item store.Item) error {
	id := item.ID
	pdfs := r.pdfPaths(id)
	var pending []int
	for i := range item.Units {
		if item.Units[i].Text == "" {
			pending = append(pending, i)
		}
	}
	if len(pending) == 0 {
		return r.finish(id)
	}

	workers := r.opts.Codex.Concurrency
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		once     sync.Once
		firstErr error
	)
	fail := func(err error) {
		once.Do(func() {
			firstErr = err
			cancel()
		})
	}
	sem := make(chan struct{}, workers)

	for _, i := range pending {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-runCtx.Done():
				return
			}
			defer func() { <-sem }()
			if runCtx.Err() != nil {
				return
			}
			if jitter := r.opts.Codex.Jitter; jitter != nil {
				if d := jitter(); d > 0 {
					if err := r.opts.Sleep(runCtx, d); err != nil {
						return
					}
				}
			}
			res, err := r.codexUnit(runCtx, id, item.Units[i], pdfs)
			if err != nil {
				if runCtx.Err() == nil {
					fail(err)
				}
				return
			}
			if err := r.saveUnitText(id, i, res); err != nil {
				fail(err)
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.finish(id)
}

// codexUnit runs one unit and retries a rate-limited (429) answer with
// exponential backoff. Network and 5xx failures are already retried inside
// freeAttempt; an auth rejection ends the item, since there is one login.
func (r *Runner) codexUnit(ctx context.Context, id string, unit store.Unit, pdfs []string) (gemini.Result, error) {
	const attempts = 5
	const base = 3 * time.Second
	var last error
	for attempt := 1; attempt <= attempts; attempt++ {
		res, outcome, err := r.freeAttempt(ctx, id, codexKey(), unit, pdfs)
		if err != nil {
			return gemini.Result{}, err
		}
		switch outcome {
		case keyOK:
			return res, nil
		case keyLimited:
			last = userError{i18n.Error("error.rateLimited")}
			if attempt == attempts {
				return gemini.Result{}, last
			}
			wait := base << (attempt - 1)
			if jitter := r.opts.Codex.Jitter; jitter != nil {
				wait += jitter()
			}
			if err := r.opts.Sleep(ctx, wait); err != nil {
				return gemini.Result{}, err
			}
		default:
			return gemini.Result{}, userError{i18n.Error("error.codexRejected")}
		}
	}
	return gemini.Result{}, last
}

// codexAuthError maps a login failure to something a user can act on.
func codexAuthError(err error) error {
	switch {
	case errors.Is(err, codexasr.ErrExpired):
		return userError{i18n.Error("error.codexAuthExpired")}
	case errors.Is(err, codexasr.ErrNotFound), errors.Is(err, codexasr.ErrNoChatGPT):
		return userError{i18n.Error("error.codexAuthMissing")}
	}
	return err
}

// codexClient adapts codexasr.Client to gemini.Client so the free lane runs
// unchanged. Files stay on disk: Upload only records the local path, and
// Generate posts each audio part. Reference PDFs are ignored — the transcribe
// endpoint takes audio only.
type codexClient struct {
	c *codexasr.Client
}

func (c codexClient) Upload(_ context.Context, path, mime string) (store.Upload, error) {
	return store.Upload{URI: path, MIME: mime, UploadedAt: time.Now()}, nil
}

func (c codexClient) Generate(ctx context.Context, req gemini.Request) (gemini.Result, error) {
	var texts []string
	for _, part := range req.Parts {
		if part.FileURI == "" || !strings.HasPrefix(part.MIME, "audio/") {
			continue
		}
		text, err := c.c.Transcribe(ctx, part.FileURI, part.MIME)
		if err != nil {
			return gemini.Result{}, err
		}
		if text = strings.TrimSpace(text); text != "" {
			texts = append(texts, text)
		}
	}
	if len(texts) == 0 {
		return gemini.Result{}, codexasr.ErrNoText
	}
	return gemini.Result{Text: strings.Join(texts, "\n\n"), Finish: gemini.FinishStop}, nil
}

func (c codexClient) SubmitBatch(context.Context, []gemini.BatchRequest, string) (string, error) {
	return "", errCodexNoBatch
}

func (c codexClient) FindBatch(context.Context, string) (string, error) {
	return "", errCodexNoBatch
}

func (c codexClient) BatchStatus(context.Context, string) (string, error) {
	return "", errCodexNoBatch
}

func (c codexClient) BatchResults(context.Context, string) (map[string]gemini.Result, error) {
	return nil, errCodexNoBatch
}

func (c codexClient) CancelBatch(context.Context, string) error {
	return errCodexNoBatch
}

func (c codexClient) Validate(context.Context) error {
	if c.c.Auth.Expired(time.Now()) {
		return codexasr.ErrExpired
	}
	return nil
}

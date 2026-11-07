package runner

import (
	"context"
	"errors"
	"strings"
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

var errCodexNoBatch = userError{i18n.Error("error.codexNoBatch")}

// codexEnabled reports the experimental backend.
func (r *Runner) codexEnabled() bool { return r.opts.Backend == BackendCodex }

// codexAuth returns the local Codex login, or an error naming what is missing.
func (r *Runner) codexAuth() (codexasr.Auth, error) {
	auth, err := r.opts.CodexAuth()
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
	if r.opts.NewCodexClient != nil {
		client = r.opts.NewCodexClient(auth)
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

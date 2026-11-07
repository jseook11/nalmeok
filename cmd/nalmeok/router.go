package main

import (
	"context"

	"github.com/jseook11/nalmeok/internal/app"
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/runner"
	"github.com/jseook11/nalmeok/internal/store"
)

// backendRouter sends each item to the runner for its backend. New items go
// to the backend currently selected in settings; everything else follows the
// backend the item was added with. The GUI builds both runners, so changing
// the setting takes effect for the next item without a restart.
type backendRouter struct {
	gemini  *runner.Runner
	codex   *runner.Runner
	store   *store.Store
	current func() string
}

var _ app.Runner = (*backendRouter)(nil)

func (r *backendRouter) Start(ctx context.Context) {
	r.gemini.Start(ctx)
	r.codex.Start(ctx)
}

func (r *backendRouter) Add(paths []string, mode, keyID string) error {
	// Batch is Gemini-only. The free lane follows the selected backend.
	if mode == "batch" || r.current() != config.BackendCodex {
		return r.gemini.Add(paths, mode, keyID)
	}
	return r.codex.Add(paths, mode, keyID)
}

// AddWithPDFs goes to Gemini: the Codex endpoint takes audio only, and a
// pending PDF choice was made while Gemini was active.
func (r *backendRouter) AddWithPDFs(path string, pdfs []string, mode, keyID string) error {
	return r.gemini.AddWithPDFs(path, pdfs, mode, keyID)
}

// runnerFor returns the runner that owns id. An empty Backend is a Gemini
// item written by an older version.
func (r *backendRouter) runnerFor(id string) (*runner.Runner, error) {
	item, ok := r.store.Get(id)
	if !ok {
		return nil, store.ErrNotFound
	}
	if item.Backend == config.BackendCodex {
		return r.codex, nil
	}
	return r.gemini, nil
}

func (r *backendRouter) Cancel(id string) error {
	run, err := r.runnerFor(id)
	if err != nil {
		return err
	}
	return run.Cancel(id)
}

func (r *backendRouter) Retry(id string) error {
	run, err := r.runnerFor(id)
	if err != nil {
		return err
	}
	return run.Retry(id)
}

func (r *backendRouter) SendToBatch(id string) error {
	run, err := r.runnerFor(id)
	if err != nil {
		return err
	}
	return run.SendToBatch(id)
}

func (r *backendRouter) Remove(id string) error {
	run, err := r.runnerFor(id)
	if err != nil {
		return err
	}
	return run.Remove(id)
}

// Close releases both runners' log files. Close is idempotent.
func (r *backendRouter) Close() error {
	gerr := r.gemini.Close()
	cerr := r.codex.Close()
	if gerr != nil {
		return gerr
	}
	return cerr
}

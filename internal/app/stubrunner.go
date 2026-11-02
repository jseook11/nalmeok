package app

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
)

// StubRunner makes dropped paths visible in the history without transcribing
// them. It lets the phase 1 UI exercise every state; phase 2 replaces it with
// the real runner.
type StubRunner struct {
	store *store.Store
}

// NewStubRunner returns a runner that only records items.
func NewStubRunner(st *store.Store) *StubRunner {
	return &StubRunner{store: st}
}

// Start satisfies Runner. The stub has no background work.
func (r *StubRunner) Start(context.Context) {}

// Add records each path as a failed item with a placeholder reason.
func (r *StubRunner) Add(paths []string, mode, keyID string) error {
	if r.store == nil {
		return errNotReady
	}
	if mode != "free" && mode != "batch" {
		mode = "free"
	}
	for _, path := range paths {
		kind := "file"
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			kind = "folder"
		}
		err := r.store.Add(store.Item{
			Source: path,
			Kind:   kind,
			Name:   filepath.Base(path),
			Mode:   mode,
			State:  "failed",
			Detail: i18n.T("status.engineNotReady"),
			KeyID:  keyID,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// AddWithPDFs records one folder like Add, keeping the PDF choice.
func (r *StubRunner) AddWithPDFs(path string, pdfs []string, mode, keyID string) error {
	if err := r.Add([]string{path}, mode, keyID); err != nil {
		return err
	}
	items := r.store.List("")
	if len(items) == 0 {
		return nil
	}
	return r.store.Update(items[0].ID, func(item *store.Item) {
		item.PDFChosen = true
		item.PDFs = append([]string{}, pdfs...)
	})
}

// Cancel marks an item canceled.
func (r *StubRunner) Cancel(id string) error {
	return r.update(id, func(item *store.Item) {
		item.State = "canceled"
		item.Progress = 0
		item.Detail = ""
		item.Batch = nil
	})
}

// Retry leaves the item failed with the placeholder reason.
func (r *StubRunner) Retry(id string) error {
	return r.update(id, func(item *store.Item) {
		item.State = "failed"
		item.Progress = 0
		item.Detail = i18n.T("status.engineNotReady")
	})
}

// SendToBatch shows the Batch waiting state with a submission time.
func (r *StubRunner) SendToBatch(id string) error {
	return r.update(id, func(item *store.Item) {
		item.Mode = "batch"
		item.State = "batch_waiting"
		item.Progress = 0
		item.Detail = ""
		item.Batch = &store.BatchRef{SubmittedAt: time.Now()}
	})
}

// Remove checks that the item exists; the stub has no work folder to delete.
func (r *StubRunner) Remove(id string) error {
	if r.store == nil {
		return errNotReady
	}
	if _, ok := r.store.Get(id); !ok {
		return store.ErrNotFound
	}
	return nil
}

func (r *StubRunner) update(id string, fn func(*store.Item)) error {
	if r.store == nil {
		return errNotReady
	}
	return r.store.Update(id, fn)
}

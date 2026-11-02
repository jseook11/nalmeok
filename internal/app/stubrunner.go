package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

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
func (r *StubRunner) Add(paths []string, mode string) error {
	if r.store == nil {
		return errors.New("저장소가 준비되지 않았습니다")
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
			Detail: "전사 엔진 준비 중",
		})
		if err != nil {
			return err
		}
	}
	return nil
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
		item.Detail = "전사 엔진 준비 중"
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

func (r *StubRunner) update(id string, fn func(*store.Item)) error {
	if r.store == nil {
		return errors.New("저장소가 준비되지 않았습니다")
	}
	return r.store.Update(id, fn)
}

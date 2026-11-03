package runner

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
)

// exhaustAfterUpload runs a one-unit free item whose only key answers 429,
// so the piece is uploaded but never transcribed.
func exhaustAfterUpload(t *testing.T) (*harness, string, string) {
	t.Helper()
	h := newHarness(t, freeKey("f1"))
	h.media.pieces = 1
	c := h.world.client("AIza-f1")
	c.setGen(func(call int, parts []gemini.Part) (string, string, error) {
		return "", "", err429()
	})
	h.r.Start(h.ctx)
	src := h.source("lec.m4a")
	item := h.addOne(src, "free")
	ex := h.wait(item.ID, StateFreeExhausted)
	if c.uploadCount() != 1 || len(ex.Uploads) != 1 {
		t.Fatalf("uploads = %d, saved = %+v", c.uploadCount(), ex.Uploads)
	}
	c.setGen(nil)
	return h, item.ID, src
}

// restart stops the running runner and builds a new one on the same store,
// as an app restart does, then retries id on it.
func (h *harness) restartAndRetry(id string, now func() time.Time) {
	h.t.Helper()
	h.cancel()
	h.ctx, h.cancel = context.WithCancel(context.Background())
	h.t.Cleanup(h.cancel)
	h.r = h.newRunner()
	if now != nil {
		h.r.opts.Now = now
	}
	h.r.Start(h.ctx)
	if err := h.r.Retry(id); err != nil {
		h.t.Fatal(err)
	}
}

func TestFreeUploadReusedAfterRestart(t *testing.T) {
	h, id, _ := exhaustAfterUpload(t)
	h.restartAndRetry(id, nil)
	done := h.wait(id, StateDone)
	if got := h.world.client("AIza-f1").uploadCount(); got != 1 {
		t.Errorf("uploads = %d, want 1 (the saved upload is reused)", got)
	}
	if done.Uploads != nil {
		t.Errorf("done item keeps uploads: %+v", done.Uploads)
	}
}

func TestFreeUploadRedoneWhenSourceChanges(t *testing.T) {
	h, id, src := exhaustAfterUpload(t)
	if err := os.WriteFile(src, []byte("AUDIO"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}
	h.restartAndRetry(id, nil)
	h.wait(id, StateDone)
	if got := h.world.client("AIza-f1").uploadCount(); got != 2 {
		t.Errorf("uploads = %d, want 2 (new pieces for the changed source)", got)
	}
}

func TestFreeUploadRedoneWhenExpired(t *testing.T) {
	h, id, _ := exhaustAfterUpload(t)
	h.restartAndRetry(id, func() time.Time { return time.Now().Add(uploadMaxAge) })
	h.wait(id, StateDone)
	if got := h.world.client("AIza-f1").uploadCount(); got != 2 {
		t.Errorf("uploads = %d, want 2 (the saved upload is too old)", got)
	}
}

func TestFreeUploadRedoneForAnotherKey(t *testing.T) {
	h, id, _ := exhaustAfterUpload(t)
	h.keys = []config.Key{freeKey("f2")}
	h.restartAndRetry(id, nil)
	h.wait(id, StateDone)
	if got := h.world.client("AIza-f2").uploadCount(); got != 1 {
		t.Errorf("f2 uploads = %d, want 1 (files are per key)", got)
	}
	if got := h.world.client("AIza-f1").uploadCount(); got != 1 {
		t.Errorf("f1 uploads = %d, want 1", got)
	}
}

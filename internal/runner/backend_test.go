package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/jseook11/nalmeok/internal/codexasr"
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
)

func TestOwns(t *testing.T) {
	codex := New(Options{Backend: BackendCodex})
	if !codex.owns(store.Item{Backend: config.BackendCodex}) {
		t.Fatal("codex runner rejected a codex item")
	}
	if codex.owns(store.Item{Backend: ""}) {
		t.Fatal("codex runner claimed a legacy gemini item")
	}

	gemini := New(Options{})
	if !gemini.owns(store.Item{Backend: ""}) || !gemini.owns(store.Item{Backend: config.BackendGemini}) {
		t.Fatal("gemini runner rejected a gemini item")
	}
	if gemini.owns(store.Item{Backend: config.BackendCodex}) {
		t.Fatal("gemini runner claimed a codex item")
	}
}

func TestStartSkipsOtherBackend(t *testing.T) {
	h := newHarness(t, freeKey("k1"))
	gemini := h.addOne(h.source("a.m4a"), "free")
	codexID := "codex-1"
	if err := h.st.Add(store.Item{
		ID:      codexID,
		Source:  h.source("b.m4a"),
		Kind:    "file",
		Name:    "b.m4a",
		Mode:    "free",
		Backend: config.BackendCodex,
		State:   StateRunning,
		Detail:  i18n.T("status.queued"),
	}); err != nil {
		t.Fatal(err)
	}

	h.r.Start(h.ctx)
	h.wait(gemini.ID, StateDone)

	item, ok := h.st.Get(codexID)
	if !ok {
		t.Fatal("codex item disappeared")
	}
	if item.State != StateRunning || !item.StartedAt.IsZero() {
		t.Fatalf("gemini runner touched a codex item: state=%q started=%v", item.State, item.StartedAt)
	}
}

func TestRetryRejectsOtherBackend(t *testing.T) {
	h := newHarness(t, freeKey("k1"))
	id := "codex-1"
	if err := h.st.Add(store.Item{
		ID:      id,
		Name:    "b.m4a",
		Mode:    "free",
		Backend: config.BackendCodex,
		State:   StateFailed,
	}); err != nil {
		t.Fatal(err)
	}
	err := h.r.Retry(id)
	if err == nil || !strings.Contains(err.Error(), i18n.T("error.wrongBackend")) {
		t.Fatalf("Retry err = %v", err)
	}
}

func TestPollOnceDoesNothingOnCodex(t *testing.T) {
	h := newCodexHarness(t, func() (codexasr.Auth, error) {
		return codexasr.Auth{AccessToken: "token"}, nil
	})
	id := "batch-1"
	if err := h.st.Add(store.Item{
		ID:      id,
		Name:    "lecture.m4a",
		Mode:    "batch",
		Backend: config.BackendGemini,
		State:   StateBatchWaiting,
		Batch:   &store.BatchRef{Name: "jobs/1", KeyID: "paid-1"},
	}); err != nil {
		t.Fatal(err)
	}

	h.r.PollOnce(context.Background())
	item, ok := h.st.Get(id)
	if !ok {
		t.Fatal("batch item disappeared")
	}
	if item.State != StateBatchWaiting {
		t.Fatalf("codex runner changed a gemini batch item: state=%q detail=%q", item.State, item.Detail)
	}
}

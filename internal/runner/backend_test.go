package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jseook11/nalmeok/internal/store"
)

// legacyBackend is the Backend value an older build stored for an item added
// with a transcription backend that has since been removed.
const legacyBackend = "codex"

// legacyItem saves an item with legacyBackend and two pending units whose
// pieces are already on disk.
func legacyItem(t *testing.T, h *harness, id, state string) {
	t.Helper()
	work := filepath.Join(h.dir, "cfg", "nalmeok", "work", id)
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	p0 := filepath.Join(work, "lec.m4a.001.m4a")
	p1 := filepath.Join(work, "lec.m4a.002.m4a")
	for _, p := range []string{p0, p1} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.st.Add(store.Item{
		ID: id, Source: h.source("lec.m4a"), Kind: "file", Name: "lec.m4a",
		Mode: "free", Backend: legacyBackend, State: state, AudioSec: 120,
		Units: []store.Unit{
			{Index: 0, Files: []string{p0}, MIMEs: []string{"audio/m4a"}, Seconds: 60},
			{Index: 1, Files: []string{p1}, MIMEs: []string{"audio/m4a"}, Seconds: 60},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// A running item stored with the retired backend name resumes on Gemini.
func TestLegacyBackendItemResumesOnGemini(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	legacyItem(t, h, "old-1", StateRunning)

	h.r = h.newRunner()
	h.r.Start(h.ctx)
	done := h.wait("old-1", StateDone)
	if got := readFile(t, done.Output); got != "T(lec.m4a.001.m4a)\n\nT(lec.m4a.002.m4a)\n" {
		t.Fatalf("output = %q", got)
	}
	if n := h.world.client("AIza-f1").generatesNow(); n != 2 {
		t.Fatalf("generates = %d, want 2", n)
	}
}

// A failed item stored with the retired backend name retries on Gemini.
func TestLegacyBackendItemRetriesOnGemini(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	legacyItem(t, h, "old-2", StateFailed)
	h.r.Start(h.ctx)

	if err := h.r.Retry("old-2"); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	done := h.wait("old-2", StateDone)
	if got := readFile(t, done.Output); got != "T(lec.m4a.001.m4a)\n\nT(lec.m4a.002.m4a)\n" {
		t.Fatalf("output = %q", got)
	}
}

func TestAddKeepsAIFF(t *testing.T) {
	h := newHarness(t, freeKey("k1"))
	if err := h.r.Add([]string{h.source("b.aiff")}, "free", ""); err != nil {
		t.Fatalf("Add err = %v", err)
	}
	if n := len(h.st.List("")); n != 1 {
		t.Fatalf("items = %d, want 1", n)
	}
}

package runner

import (
	"strings"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/store"
)

func (h *harness) addPinned(path, mode, keyID string) store.Item {
	h.t.Helper()
	if err := h.r.Add([]string{path}, mode, keyID); err != nil {
		h.t.Fatalf("Add: %v", err)
	}
	return h.st.List("")[0]
}

func limitAlways(int, []gemini.Part) (string, string, error) { return "", "", err429() }

func TestFreeUsesKeysInPriorityOrder(t *testing.T) {
	tests := []struct {
		name    string
		keys    []config.Key
		limited []string // keys that always answer 429
		want    map[string]int
	}{
		{
			name: "first key does everything",
			keys: []config.Key{freeKey("f1"), freeKey("f2")},
			want: map[string]int{"AIza-f1": 2, "AIza-f2": 0},
		},
		{
			name: "order follows the list",
			keys: []config.Key{freeKey("f2"), freeKey("f1")},
			want: map[string]int{"AIza-f1": 0, "AIza-f2": 2},
		},
		{
			name:    "first key 429 moves to second",
			keys:    []config.Key{freeKey("f1"), freeKey("f2"), freeKey("f3")},
			limited: []string{"AIza-f1"},
			want:    map[string]int{"AIza-f1": 2, "AIza-f2": 2, "AIza-f3": 0},
		},
		{
			name: "paid keys are skipped in free mode",
			keys: []config.Key{paidKey("p1"), freeKey("f1")},
			want: map[string]int{"AIza-f1": 2, "AIza-p1": 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, tt.keys...)
			for _, k := range tt.limited {
				h.world.client(k).gen = limitAlways
			}
			h.r.Start(h.ctx)
			item := h.addOne(h.source("lec.m4a"), "free")
			done := h.wait(item.ID, StateDone)
			for key, want := range tt.want {
				if got := h.world.client(key).generatesNow(); got != want {
					t.Errorf("%s generates = %d, want %d", key, got, want)
				}
			}
			if done.KeyID != "" {
				t.Errorf("automatic item got KeyID %q", done.KeyID)
			}
		})
	}
}

func TestFreeCandidatesPreferKeysOffTheirGap(t *testing.T) {
	h := newHarness(t, freeKey("f1"), freeKey("f2"), freeKey("f3"))
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	h.r.opts.KeyGap = DefaultKeyGap
	h.r.opts.Now = func() time.Time { return now }
	h.r.lastStart["f1"] = now.Add(-5 * time.Second)  // still waiting
	h.r.lastStart["f2"] = now.Add(-30 * time.Second) // ready
	got, err := h.r.freeCandidates("", 60)
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, k := range got {
		order = append(order, k.ID)
	}
	if strings.Join(order, ",") != "f2,f3,f1" {
		t.Fatalf("order = %v, want ready keys first in priority order", order)
	}
}

func TestPinnedFreeKey(t *testing.T) {
	h := newHarness(t, freeKey("f1"), freeKey("f2"))
	h.r.Start(h.ctx)
	item := h.addPinned(h.source("lec.m4a"), "batch", "f2")
	if item.Mode != "free" || item.KeyID != "f2" {
		t.Fatalf("pinned item = mode %q key %q", item.Mode, item.KeyID)
	}
	h.wait(item.ID, StateDone)
	if h.world.client("AIza-f1").generatesNow() != 0 || h.world.client("AIza-f2").generatesNow() != 2 {
		t.Errorf("pinned item used another key")
	}
}

func TestPinnedFreeKey429IsExhaustedWithoutRotation(t *testing.T) {
	h := newHarness(t, freeKey("f1"), freeKey("f2"), paidKey("p1"))
	h.world.client("AIza-f1").gen = limitAlways
	h.r.Start(h.ctx)
	item := h.addPinned(h.source("lec.m4a"), "free", "f1")

	ex := h.wait(item.ID, StateFreeExhausted)
	if ex.Detail != "남은 2분" {
		t.Errorf("Detail = %q", ex.Detail)
	}
	if got := h.world.client("AIza-f2").generatesNow(); got != 0 {
		t.Fatalf("pinned item rotated to f2 (%d calls)", got)
	}

	// "유료로 이어서" still works and drops the free pin.
	if err := h.r.SendToBatch(item.ID); err != nil {
		t.Fatal(err)
	}
	waiting := h.wait(item.ID, StateBatchWaiting)
	if waiting.KeyID != "" || waiting.Batch.KeyID != "p1" {
		t.Fatalf("after SendToBatch: KeyID %q batch key %q", waiting.KeyID, waiting.Batch.KeyID)
	}
}

func TestPinnedPaidKeyForBatch(t *testing.T) {
	h := newHarness(t, paidKey("p1"), paidKey("p2"))
	h.r.Start(h.ctx)
	item := h.addPinned(h.source("lec.m4a"), "free", "p2")
	if item.Mode != "batch" {
		t.Fatalf("Mode = %q, want batch for a paid key", item.Mode)
	}
	waiting := h.wait(item.ID, StateBatchWaiting)
	if waiting.Batch.KeyID != "p2" {
		t.Fatalf("batch key = %q, want p2", waiting.Batch.KeyID)
	}
	h.world.setBatchState(gemini.BatchSucceeded)
	h.r.PollOnce(h.ctx)
	h.wait(item.ID, StateDone)
	if got := h.world.client("AIza-p1").uploadCount(); got != 0 {
		t.Errorf("unpinned paid key uploaded %d files", got)
	}
}

func TestBatchUsesHighestPriorityPaidKey(t *testing.T) {
	h := newHarness(t, freeKey("f1"), paidKey("p2"), paidKey("p1"))
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")
	if waiting := h.wait(item.ID, StateBatchWaiting); waiting.Batch.KeyID != "p2" {
		t.Fatalf("batch key = %q, want p2", waiting.Batch.KeyID)
	}
}

func TestPinnedKeyMissing(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	if err := h.r.Add([]string{h.source("a.m4a")}, "free", "gone"); err == nil || err.Error() != "선택한 키가 없어요" {
		t.Fatalf("Add with unknown key = %v", err)
	}

	// An item whose key was deleted after it was queued fails clearly instead
	// of switching to another key.
	src := h.source("lec.m4a")
	if err := h.st.Add(store.Item{ID: "run", Source: src, Kind: "file", Name: "lec.m4a",
		Mode: "free", State: StateRunning, KeyID: "gone"}); err != nil {
		t.Fatal(err)
	}
	h.r.Start(h.ctx)
	failed := h.wait("run", StateFailed)
	if failed.Detail != "선택한 키가 없어요" {
		t.Errorf("Detail = %q", failed.Detail)
	}
	if h.world.client("AIza-f1").generatesNow() != 0 {
		t.Error("fell back to another key")
	}
	if err := h.r.Retry("run"); err == nil {
		t.Error("Retry with a deleted pinned key must fail")
	}
}

func TestPinnedKeyTierMismatchFails(t *testing.T) {
	h := newHarness(t, freeKey("f1"), paidKey("p1"))
	src := h.source("lec.m4a")
	// Pinned to p1 but queued in free mode (the key was re-tiered meanwhile).
	if err := h.st.Add(store.Item{ID: "run", Source: src, Kind: "file", Name: "lec.m4a",
		Mode: "free", State: StateRunning, KeyID: "p1"}); err != nil {
		t.Fatal(err)
	}
	h.r.Start(h.ctx)
	failed := h.wait("run", StateFailed)
	if failed.Detail != "선택한 키는 무료 키가 아니에요" {
		t.Errorf("Detail = %q", failed.Detail)
	}
	// Retry follows the key's current tier.
	if err := h.r.Retry("run"); err != nil {
		t.Fatal(err)
	}
	if waiting := h.wait("run", StateBatchWaiting); waiting.Mode != "batch" || waiting.Batch.KeyID != "p1" {
		t.Fatalf("retry = mode %q batch %+v", waiting.Mode, waiting.Batch)
	}
}

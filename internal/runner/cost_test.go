package runner

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/plan"
	"github.com/jseook11/nalmeok/internal/store"
)

func TestFreeDoneRecordsUsageAtNoCost(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")

	done := h.wait(item.ID, StateDone)
	if done.AudioSec != 120 || done.EstimateKRW != plan.EstimateMidKRW(120) {
		t.Errorf("AudioSec = %v, EstimateKRW = %d", done.AudioSec, done.EstimateKRW)
	}
	if done.Usage == nil || done.Usage.Audio != 2*fakeUsage.Audio || done.Usage.Thoughts != 2*fakeUsage.Thoughts || done.Usage.Batch {
		t.Fatalf("Usage = %+v", done.Usage)
	}
	if done.CostKRW == nil || *done.CostKRW != 0 {
		t.Fatalf("CostKRW = %v, want 0 for free requests", done.CostKRW)
	}
	if done.FinishedAt.IsZero() || done.StartedAt.IsZero() {
		t.Errorf("FinishedAt = %v, StartedAt = %v", done.FinishedAt, done.StartedAt)
	}
}

func TestMixedDonePricesOnlyBatchUnits(t *testing.T) {
	h := newHarness(t, freeKey("f1"), paidKey("p1"))
	h.world.client("AIza-f1").gen = func(call int, parts []gemini.Part) (string, string, error) {
		if call == 1 {
			return answer(parts), gemini.FinishStop, nil
		}
		return "", "", err429()
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	h.wait(item.ID, StateFreeExhausted)
	if err := h.r.SendToBatch(item.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(item.ID, StateBatchWaiting)
	h.world.setBatchState(gemini.BatchSucceeded)
	h.r.PollOnce(h.ctx)
	done := h.wait(item.ID, StateDone)

	want := plan.USDToKRW(plan.CostUSD(fakeUsage.Audio, fakeUsage.Text, fakeUsage.Output+fakeUsage.Thoughts, true))
	if done.CostKRW == nil || *done.CostKRW != want {
		t.Fatalf("CostKRW = %v, want %d (one Batch unit)", done.CostKRW, want)
	}
	if done.Usage == nil || done.Usage.Output != 2*fakeUsage.Output || !done.Usage.Batch {
		t.Fatalf("Usage = %+v", done.Usage)
	}
}

func TestItemCostUnknownWithoutUsage(t *testing.T) {
	usage, cost := itemCost([]store.Unit{{Text: "a"}, {Text: "b"}})
	if usage != nil || cost != nil {
		t.Fatalf("itemCost = %v, %v; want nil, nil", usage, cost)
	}
}

func TestRenameWhileQueuedSurvivesPrepare(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	item := h.addOne(h.source("lec.m4a"), "free")
	if err := h.st.Update(item.ID, func(it *store.Item) { it.Name = "선형대수 2주차" }); err != nil {
		t.Fatal(err)
	}
	h.r.Start(h.ctx)
	done := h.wait(item.ID, StateDone)
	if done.Name != "선형대수 2주차" {
		t.Fatalf("Name = %q, want the renamed name", done.Name)
	}
	if filepath.Base(done.Source) != "lec.m4a" {
		t.Fatalf("Source = %q", done.Source)
	}
}

func TestEarlyEstimateRecordsLength(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.r.opts.EarlyEstimate = true
	item := h.addOne(h.source("lec.m4a"), "batch") // not started: only the estimate runs
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if it, _ := h.st.Get(item.ID); it.AudioSec == 120 {
			if it.EstimateKRW != plan.EstimateMidKRW(120) {
				t.Fatalf("EstimateKRW = %d", it.EstimateKRW)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("estimate not recorded")
}

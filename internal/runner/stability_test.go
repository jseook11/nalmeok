package runner

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/store"
	"google.golang.org/genai"
)

func (w *fakeWorld) jobs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.batchOrder...)
}

// ---- 14: Batch creation is never blindly repeated ---------------------------

// The server created the job but the response was lost. The runner must not
// create a second job (double billing): it stops, and a retry finds the job
// it already made.
func TestLostBatchCreateIsNotResubmitted(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.world.mu.Lock()
	h.world.submitLost = true
	h.world.mu.Unlock()
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")

	h.wait(item.ID, StateFailed)
	if n := h.world.submitCallsNow(); n != 1 {
		t.Fatalf("SubmitBatch calls = %d, want 1 (no retry of create)", n)
	}

	if err := h.r.Retry(item.ID); err != nil {
		t.Fatal(err)
	}
	waiting := h.wait(item.ID, StateBatchWaiting)
	jobs := h.world.jobs()
	if len(jobs) != 1 || waiting.Batch.Name != jobs[0] {
		t.Fatalf("jobs = %v, item waits on %q; want the one job made before", jobs, waiting.Batch.Name)
	}
	if n := h.world.submitCallsNow(); n != 1 {
		t.Fatalf("SubmitBatch calls = %d after retry, want 1", n)
	}

	h.world.setBatchState(gemini.BatchSucceeded)
	h.r.PollOnce(h.ctx)
	h.wait(item.ID, StateDone)
}

// A clear server refusal leaves nothing behind, so a retry submits again.
func TestFailedBatchCreateRetriesAfterLookup(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.world.failNextSubmit(1)
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")
	h.wait(item.ID, StateFailed)
	if n := h.world.submitCallsNow(); n != 1 {
		t.Fatalf("SubmitBatch calls = %d, want 1", n)
	}
	if err := h.r.Retry(item.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(item.ID, StateBatchWaiting)
	if n := h.world.submitCallsNow(); n != 2 {
		t.Fatalf("SubmitBatch calls = %d, want 2", n)
	}
}

// ---- 15a: canceling a job that already finished keeps its results -----------

func TestCancelFinishedBatchCollectsResults(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")
	h.wait(item.ID, StateBatchWaiting)

	h.world.setBatchState(gemini.BatchSucceeded) // finished before the next poll
	if err := h.r.Cancel(item.ID); err != nil {
		t.Fatal(err)
	}
	done := h.wait(item.ID, StateDone)
	if got := readFile(t, done.Output); !strings.Contains(got, "BT(") {
		t.Fatalf("output = %q", got)
	}
	if n := h.world.submitCallsNow(); n != 1 {
		t.Fatalf("SubmitBatch calls = %d, want 1", n)
	}
}

// ---- 15b: a job the server no longer knows ends the wait ---------------------

func TestPollNotFoundFailsItem(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")
	h.wait(item.ID, StateBatchWaiting)

	h.world.mu.Lock()
	h.world.statusErr = genai.APIError{Code: 404, Message: "not found"}
	h.world.mu.Unlock()
	h.r.PollOnce(h.ctx)
	failed := h.wait(item.ID, StateFailed)
	if failed.Batch != nil && failed.Batch.Name != "" {
		t.Errorf("a missing job must not be waited on again: %+v", failed.Batch)
	}
}

// ---- 15c: a timeout while waiting keeps polling the same job ------------------

func TestPollTimeoutKeepsWaitingOnSameJob(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")
	waiting := h.wait(item.ID, StateBatchWaiting)

	h.world.mu.Lock()
	h.world.statusErr = context.DeadlineExceeded
	h.world.mu.Unlock()
	h.r.PollOnce(h.ctx)
	if it, _ := h.st.Get(item.ID); it.State != StateBatchWaiting || it.Batch.Name != waiting.Batch.Name {
		t.Fatalf("after timeout: state %q batch %+v", it.State, it.Batch)
	}

	h.world.mu.Lock()
	h.world.statusErr = nil
	h.world.mu.Unlock()
	h.world.setBatchState(gemini.BatchSucceeded)
	h.r.PollOnce(h.ctx)
	h.wait(item.ID, StateDone)
	if n := h.world.submitCallsNow(); n != 1 {
		t.Fatalf("SubmitBatch calls = %d, want 1", n)
	}
}

// ---- 15d: retry and continue-as-paid right after cancel ----------------------

// heldFreeItem starts one free item whose Generate blocks, and keeps the
// worker from finishing its unwind after a cancel until the returned
// function is called.
func heldFreeItem(t *testing.T, h *harness) (store.Item, func()) {
	t.Helper()
	hold, started := h.holdLane("free")
	c := h.world.client("AIza-f1")
	unwind := make(chan struct{})
	c.mu.Lock()
	c.unwind = unwind
	c.mu.Unlock()
	h.r.Start(h.ctx)
	item := h.add("lec.m4a", "free")
	h.waitFor("free item to start", started)
	return item, func() {
		c.mu.Lock()
		c.hold = nil
		c.mu.Unlock()
		close(hold)
		close(unwind)
	}
}

func TestRetryRightAfterCancel(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	item, release := heldFreeItem(t, h)

	if err := h.r.Cancel(item.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(item.ID, StateCanceled)
	if err := h.r.Retry(item.ID); err != nil {
		t.Fatal(err)
	}
	release()
	h.wait(item.ID, StateDone)
}

func TestSendToBatchRightAfterCancel(t *testing.T) {
	h := newHarness(t, freeKey("f1"), paidKey("p1"))
	item, release := heldFreeItem(t, h)

	if err := h.r.Cancel(item.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(item.ID, StateCanceled)
	if err := h.r.SendToBatch(item.ID); err != nil {
		t.Fatal(err)
	}
	release()
	h.wait(item.ID, StateBatchWaiting)
}

// ---- 25: continue as paid from a running free item ----------------------------

func TestSendToBatchFromRunningFreeItem(t *testing.T) {
	h := newHarness(t, freeKey("f1"), paidKey("p1"))
	hold, started := h.holdLane("free")
	h.r.Start(h.ctx)
	item := h.add("lec.m4a", "free")
	h.waitFor("free item to start", started)
	h.setDetailForTest(item.ID, retryDetail()) // as if retrying after 503

	if err := h.r.SendToBatch(item.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(item.ID, StateBatchWaiting)
	if n := h.world.submitCallsNow(); n != 1 {
		t.Fatalf("SubmitBatch calls = %d, want 1", n)
	}

	// The free lane is free again for the next item.
	c := h.world.client("AIza-f1")
	c.mu.Lock()
	c.hold = nil
	c.mu.Unlock()
	close(hold)
	next := h.add("next.m4a", "free")
	h.wait(next.ID, StateDone)
	if it, _ := h.st.Get(item.ID); it.State != StateBatchWaiting || it.Mode != "batch" {
		t.Fatalf("moved item = %q/%q", it.State, it.Mode)
	}
}

func TestSendToBatchFromQueuedFreeItem(t *testing.T) {
	h := newHarness(t, freeKey("f1"), paidKey("p1"))
	hold, started := h.holdLane("free")
	h.r.Start(h.ctx)
	first := h.add("first.m4a", "free")
	h.waitFor("free item to start", started)
	queued := h.add("queued.m4a", "free")

	if err := h.r.SendToBatch(queued.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(queued.ID, StateBatchWaiting)
	close(hold)
	h.wait(first.ID, StateDone)
}

func (h *harness) setDetailForTest(id, detail string) {
	h.t.Helper()
	if err := h.st.Update(id, func(it *store.Item) { it.Detail = detail }); err != nil {
		h.t.Fatal(err)
	}
}

// ---- 18: a changed source is transcribed again ---------------------------------

func TestChangedSourceIsNotReused(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	c := h.world.client("AIza-f1")
	c.setGen(func(call int, parts []gemini.Part) (string, string, error) {
		if call == 2 {
			return "", "", err429()
		}
		return answer(parts), gemini.FinishStop, nil
	})
	h.r.Start(h.ctx)
	src := h.source("lec.m4a")
	item := h.addOne(src, "free")
	h.wait(item.ID, StateFreeExhausted)
	scans := h.media.scanCount()

	// Same length, different content and modification time.
	if err := os.WriteFile(src, []byte("AUDIO"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(src, later, later); err != nil {
		t.Fatal(err)
	}
	c.setGen(nil)
	if err := h.r.Retry(item.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(item.ID, StateDone)
	if h.media.scanCount() == scans {
		t.Error("changed source was not scanned again")
	}
	if n := c.generatesNow(); n != 4 {
		t.Errorf("generates = %d, want 4 (both units again after the change)", n)
	}
}

// An unchanged source keeps finished units on retry.
func TestUnchangedSourceKeepsFinishedUnits(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	c := h.world.client("AIza-f1")
	c.setGen(func(call int, parts []gemini.Part) (string, string, error) {
		if call == 2 {
			return "", "", err429()
		}
		return answer(parts), gemini.FinishStop, nil
	})
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	h.wait(item.ID, StateFreeExhausted)
	c.setGen(nil)
	if err := h.r.Retry(item.ID); err != nil {
		t.Fatal(err)
	}
	h.wait(item.ID, StateDone)
	if n := c.generatesNow(); n != 3 {
		t.Errorf("generates = %d, want 3", n)
	}
}

// ---- a stop that lands while a run is starting -------------------------------

// Cancel, Remove and SendToBatch stop the active run before they change the
// item. When the worker has taken the item but not yet set its cancel
// function, that stop only marks the lane; the run must still start
// canceled. Otherwise it reads the still-running item and transcribes (or
// submits a paid job) after the user canceled it.
func TestStopBeforeRunStartsCancelsRun(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	// Not started: the test drives the lane by hand.
	item := h.addOne(h.source("lec.m4a"), "batch")
	l := h.r.lanes["batch"]
	id, ok := h.r.next(l)
	if !ok || id != item.ID {
		t.Fatalf("next = %q, %v", id, ok)
	}
	// The first half of Cancel: the run is stopped before the state changes.
	h.r.mu.Lock()
	h.r.stopActiveLocked(id)
	h.r.mu.Unlock()

	h.r.runItem(h.ctx, l, id)

	if n := h.world.submitCallsNow(); n != 0 {
		t.Fatalf("SubmitBatch calls = %d after a stop, want 0", n)
	}
	if got, _ := h.st.Get(id); got.State != StateRunning {
		t.Fatalf("state = %q (%s), want running (the canceler sets canceled)", got.State, got.Detail)
	}
}

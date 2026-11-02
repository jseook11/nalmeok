package runner

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/store"
)

// add queues one new source file and returns its item.
func (h *harness) add(name, mode string) store.Item {
	h.t.Helper()
	src := h.source(name)
	if err := h.r.Add([]string{src}, mode, ""); err != nil {
		h.t.Fatalf("Add: %v", err)
	}
	for _, it := range h.st.List("") {
		if it.Source == src {
			return it
		}
	}
	h.t.Fatalf("item for %s not found", name)
	return store.Item{}
}

func (h *harness) waitFor(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (w *fakeWorld) submitCallsNow() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.submitCalls
}

// holdLane makes the given lane's next call block until the returned channel
// is closed or the item's context is canceled, and returns a function that
// reports whether that call has started.
func (h *harness) holdLane(mode string) (chan struct{}, func() bool) {
	hold := make(chan struct{})
	if mode == "batch" {
		h.world.mu.Lock()
		h.world.submitHold = hold
		h.world.mu.Unlock()
		return hold, func() bool { return h.world.submitCallsNow() > 0 }
	}
	c := h.world.client("AIza-f1")
	c.mu.Lock()
	c.hold = hold
	c.mu.Unlock()
	return hold, func() bool { return c.generatesNow() > 0 }
}

func TestBatchDoesNotWaitForFreeItem(t *testing.T) {
	h := newHarness(t, freeKey("f1"), paidKey("p1"))
	hold, started := h.holdLane("free")
	h.r.Start(h.ctx)

	long := h.add("long.m4a", "free")
	h.waitFor("free item to start", started)

	paid := h.add("paid.m4a", "batch")
	h.wait(paid.ID, StateBatchWaiting)

	// A free item continued on paid goes to the batch lane as well.
	if err := h.st.Add(store.Item{
		ID: "item-exhausted", Source: h.source("rest.m4a"), Kind: "file", Name: "rest.m4a",
		Mode: "free", State: StateFreeExhausted,
	}); err != nil {
		t.Fatal(err)
	}
	if err := h.r.SendToBatch("item-exhausted"); err != nil {
		t.Fatal(err)
	}
	h.wait("item-exhausted", StateBatchWaiting)

	if it, _ := h.st.Get(long.ID); it.State != StateRunning {
		t.Fatalf("free item state = %q, want still running", it.State)
	}
	if n := h.world.client("AIza-f1").generatesNow(); n != 1 {
		t.Fatalf("free generates = %d, want 1 (still on its first unit)", n)
	}
	close(hold)
	h.wait(long.ID, StateDone)
}

// Cancel and Remove reach queued and active items in both lanes, and the lane
// keeps working afterwards.
func TestCancelAndRemoveInEachLane(t *testing.T) {
	for _, mode := range []string{"free", "batch"} {
		for _, op := range []string{"cancel", "remove"} {
			t.Run(mode+"/"+op, func(t *testing.T) {
				h := newHarness(t, freeKey("f1"), paidKey("p1"))
				hold, started := h.holdLane(mode)
				h.r.Start(h.ctx)
				stop := func(id string) {
					t.Helper()
					var err error
					if op == "cancel" {
						err = h.r.Cancel(id)
					} else {
						err = h.r.Remove(id)
					}
					if err != nil {
						t.Fatalf("%s(%s): %v", op, id, err)
					}
				}

				active := h.add("active.m4a", mode)
				h.waitFor(mode+" item to start", started)
				queued := h.add("queued.m4a", mode)

				stop(queued.ID)
				h.wait(queued.ID, StateCanceled)

				stop(active.ID)
				h.wait(active.ID, StateCanceled)
				h.waitFor("lane to go idle", func() bool { return !h.r.isActive(active.ID) })
				if op == "remove" {
					if _, err := os.Stat(h.r.workDir(active.ID)); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("active work dir still exists: %v", err)
					}
				}
				if n := h.media.scanCount(); n != 1 {
					t.Errorf("scans = %d, want 1 (the queued item must never start)", n)
				}

				close(hold)
				next := h.add("next.m4a", mode)
				if mode == "batch" {
					h.wait(next.ID, StateBatchWaiting)
				} else {
					h.wait(next.ID, StateDone)
				}
				for _, id := range []string{active.ID, queued.ID} {
					if it, _ := h.st.Get(id); it.State != StateCanceled {
						t.Errorf("%s state = %q after lane resumed", id, it.State)
					}
				}
			})
		}
	}
}

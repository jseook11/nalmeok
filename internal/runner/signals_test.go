package runner

import (
	"sync"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/telemetry"
	"google.golang.org/genai"
)

// signals collects what the runner hands to Options.Signal.
type signals struct {
	mu     sync.Mutex
	events []telemetry.Event
}

func (s *signals) record(ev telemetry.Event) {
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
}

func (s *signals) now() []telemetry.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]telemetry.Event(nil), s.events...)
}

func (h *harness) recordSignals() *signals {
	s := &signals{}
	h.r.opts.Signal = s.record
	return s
}

func TestSignalFree429OnFreshKeyIsUnderQuota(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	h.media.pieces = 1
	sig := h.recordSignals()
	h.world.client("AIza-f1").gen = func(int, []gemini.Part) (string, string, error) {
		return "", "", err429()
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	h.wait(item.ID, StateFreeExhausted)

	got := sig.now()
	if len(got) != 1 {
		t.Fatalf("events = %+v, want one", got)
	}
	ev := got[0]
	if ev.Mode != "free" || ev.Outcome != telemetry.RateLimited || ev.Code != 429 || ev.Attempt != 1 {
		t.Errorf("event = %+v", ev)
	}
	if ev.UnderQuota == nil || !*ev.UnderQuota {
		t.Errorf("UnderQuota = %v, want true", ev.UnderQuota)
	}
	if ev.Time.IsZero() {
		t.Error("event has no time")
	}
}

func TestSignalTransientThenOK(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	h.media.pieces = 1
	sig := h.recordSignals()
	h.world.client("AIza-f1").gen = func(call int, parts []gemini.Part) (string, string, error) {
		if call == 1 {
			return "", "", genai.APIError{Code: 503}
		}
		return answer(parts), gemini.FinishStop, nil
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	h.wait(item.ID, StateDone)

	got := sig.now()
	if len(got) != 2 {
		t.Fatalf("events = %+v, want two", got)
	}
	if ev := got[0]; ev.Mode != "free" || ev.Outcome != telemetry.Server || ev.Code != 503 || ev.Attempt != 1 || ev.UnderQuota != nil {
		t.Errorf("first event = %+v", ev)
	}
	if ev := got[1]; ev.Mode != "free" || ev.Outcome != telemetry.OK || ev.Attempt != 2 || ev.UnderQuota != nil {
		t.Errorf("second event = %+v", ev)
	}
}

func TestSignalBlockedAnswer(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	h.media.pieces = 1
	sig := h.recordSignals()
	h.world.client("AIza-f1").gen = func(int, []gemini.Part) (string, string, error) {
		return "", gemini.FinishBlocked + "OTHER", nil
	}
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "free")
	h.wait(item.ID, StateFailed)

	got := sig.now()
	// The primary request is refused and the fallback request is refused too:
	// one event per request.
	if len(got) != 2 || got[0].Outcome != telemetry.Blocked || got[1].Outcome != telemetry.Blocked {
		t.Fatalf("events = %+v, want two blocked", got)
	}
}

func TestSignalBatchSubmitAndJob(t *testing.T) {
	h := newHarness(t, paidKey("p1"))
	sig := h.recordSignals()
	h.r.Start(h.ctx)
	item := h.addOne(h.source("lec.m4a"), "batch")
	h.wait(item.ID, StateBatchWaiting)
	h.world.setBatchState(gemini.BatchSucceeded)
	h.r.PollOnce(h.ctx)
	h.wait(item.ID, StateDone)

	got := sig.now()
	if len(got) != 2 {
		t.Fatalf("events = %+v, want submit and job", got)
	}
	for _, ev := range got {
		if ev.Mode != "batch" || ev.Outcome != telemetry.OK || ev.Attempt != 1 || ev.UnderQuota != nil {
			t.Errorf("event = %+v", ev)
		}
	}
}

func TestFreeUnderQuotaCountsPerQuotaDay(t *testing.T) {
	h := newHarness(t, freeKey("f1"))
	// 07:59 UTC is still the previous day in UTC-8.
	now := time.Date(2026, 11, 4, 7, 59, 0, 0, time.UTC)
	h.r.opts.Now = func() time.Time { return now }
	for i := 0; i < freeDailyRequestsFloor; i++ {
		if under := h.r.freeUnderQuota("f1", true); !*under {
			t.Fatalf("request %d: under = false", i+1)
		}
	}
	if under := h.r.freeUnderQuota("f1", false); *under {
		t.Fatal("under = true after the floor was reached")
	}
	if under := h.r.freeUnderQuota("f2", false); !*under {
		t.Fatal("another key shares the count")
	}
	now = now.Add(2 * time.Minute) // 08:01 UTC: midnight UTC-8
	if under := h.r.freeUnderQuota("f1", true); !*under {
		t.Fatal("count did not reset at the quota day boundary")
	}
}

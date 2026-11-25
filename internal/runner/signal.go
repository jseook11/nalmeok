package runner

// Health signals: one anonymous event per Gemini request (docs/development.md
// "상태 신호").

import (
	"context"
	"errors"
	"time"

	"github.com/jseook11/nalmeok/internal/telemetry"
)

// signal records the outcome of one Gemini request. A canceled request says
// nothing about the service and is left out. Only the outcome, HTTP status
// and attempt leave the app; never keys, paths, text or error messages.
func (r *Runner) signal(ctx context.Context, mode string, attempt int, err error, underQuota *bool) {
	if r.opts.Signal == nil || ctx.Err() != nil {
		return
	}
	var outcome string
	var code int
	var be blockedError
	var ue userError
	switch {
	case errors.As(err, &be):
		outcome = telemetry.Blocked
	case errors.As(err, &ue):
		// checkText: an answer came back but is unusable (cut off, or
		// stopped for another reason).
		outcome = telemetry.Other
	default:
		outcome, code = telemetry.Classify(err)
	}
	r.signalOutcome(mode, attempt, outcome, code, underQuota)
}

func (r *Runner) signalOutcome(mode string, attempt int, outcome string, code int, underQuota *bool) {
	if r.opts.Signal == nil || outcome == "" {
		return
	}
	if outcome != telemetry.RateLimited {
		underQuota = nil
	}
	r.opts.Signal(telemetry.Event{
		Time:       r.opts.Now(),
		Mode:       mode,
		Outcome:    outcome,
		Code:       code,
		UnderQuota: underQuota,
		Attempt:    attempt,
	})
}

// dailyCount is one free key's request count in one quota day.
type dailyCount struct {
	day string
	n   int
}

// quotaDay names the free-tier quota day of t. Gemini's daily quotas reset at
// midnight US Pacific; a fixed UTC-8 avoids loading tzdata, and the hour it
// is off during daylight saving only matters around midnight.
func quotaDay(t time.Time) string {
	return t.UTC().Add(-8 * time.Hour).Format("2006-01-02")
}

// freeUnderQuota reports whether keyID had sent fewer than
// freeDailyRequestsFloor requests this quota day before this one; count adds
// this request. It is this app's own count: another app using the same key
// is not seen, and a restart starts from zero.
func (r *Runner) freeUnderQuota(keyID string, count bool) *bool {
	day := quotaDay(r.opts.Now())
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.freeSent[keyID]
	if c.day != day {
		c = dailyCount{day: day}
	}
	under := c.n < freeDailyRequestsFloor
	if count {
		c.n++
	}
	r.freeSent[keyID] = c
	return &under
}

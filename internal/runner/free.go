package runner

// Free live requests: one unit at a time per key, with gaps, retries and
// the fallback model.

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
)

type keyOutcome int

const (
	keyOK keyOutcome = iota
	keyLimited
	keyRejected
)

// runFreeGemini transcribes an item's unfinished units one at a time, rotating
// through the free keys. When every model stays overloaded (503), the unit
// waits through OverloadWaits and is tried again; only then does the item
// fail.
func (r *Runner) runFreeGemini(ctx context.Context, item store.Item) error {
	id := item.ID
	pdfs := r.pdfPaths(id)
	for i := range item.Units {
		if item.Units[i].Text != "" {
			continue
		}
		unit := item.Units[i]
		var res gemini.Result
		for round := 0; ; round++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			r.setDetail(id, i18n.T("status.transcribing"))
			var limited int
			var done bool
			var err error
			res, limited, done, err = r.freeUnit(ctx, id, item.KeyID, unit, pdfs)
			if err != nil {
				if !isOverload(err) {
					return err
				}
				if round >= len(r.opts.OverloadWaits) {
					r.logf("item %s unit %d: Gemini stayed busy, giving up: %v", id, unit.Index, r.redact(err))
					return userError{i18n.Error("error.geminiBusy")}
				}
				wait := r.opts.OverloadWaits[round]
				r.logf("item %s unit %d: every model busy (503), waiting %s", id, unit.Index, wait)
				r.setDetail(id, i18n.T("status.busyWaiting", "minutes", int(math.Ceil(wait.Minutes()))))
				if err := r.opts.Sleep(ctx, wait); err != nil {
					return err
				}
				continue
			}
			if !done {
				if limited == 0 {
					return userError{i18n.Error("error.freeKeyInvalid")}
				}
				return r.freeExhausted(id, item.Units)
			}
			break
		}
		item.Units[i].Text = res.Text
		if err := r.saveUnitText(id, i, res); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.finish(id)
}

// freeUnit tries one unit on each candidate key in turn. done is false when
// every key was limited or rejected; limited counts the 429s.
func (r *Runner) freeUnit(ctx context.Context, id, pinned string, unit store.Unit, pdfs []string) (gemini.Result, int, bool, error) {
	candidates, err := r.freeCandidates(pinned, unit.Seconds)
	if err != nil {
		return gemini.Result{}, 0, false, err
	}
	limited := 0
	for _, key := range candidates {
		res, outcome, err := r.freeAttempt(ctx, id, key, unit, pdfs)
		if err != nil {
			return gemini.Result{}, limited, false, err
		}
		switch outcome {
		case keyOK:
			return res, limited, true, nil
		case keyLimited:
			limited++
			r.logf("item %s unit %d: key %s limited (429)", id, unit.Index, key.ID)
		case keyRejected:
			r.logf("item %s unit %d: key %s rejected", id, unit.Index, key.ID)
		}
	}
	return gemini.Result{}, limited, false, nil
}

// freeExhausted parks a free item whose keys all hit their daily quota.
func (r *Runner) freeExhausted(id string, units []store.Unit) error {
	remaining := 0.0
	for _, u := range units {
		if u.Text == "" {
			remaining += u.Seconds
		}
	}
	minutes := int(math.Ceil(remaining / 60))
	r.logf("item %s free keys exhausted, %d min left", id, minutes)
	return r.update(id, func(it *store.Item) {
		if it.State != StateRunning || it.Mode == config.ModeBatch {
			return
		}
		it.State = StateFreeExhausted
		it.Detail = i18n.T("status.minutesLeft", "minutes", minutes)
		it.Progress = doneRatio(it.Units)
	})
}

// saveUnitText stores one finished unit and moves the item's progress. i is the
// unit's slice index, which is where the unit layout put it; results may
// arrive out of order, so text is saved by index, never by arrival order.
func (r *Runner) saveUnitText(id string, i int, res gemini.Result) error {
	return r.update(id, func(it *store.Item) {
		if i < len(it.Units) {
			it.Units[i].Text = res.Text
			it.Units[i].Model = res.Model
			if !res.Usage.Empty() {
				u := res.Usage
				it.Units[i].Usage = &u
			}
		}
		it.Progress = doneRatio(it.Units)
	})
}

// freeAttempt runs one unit on one key. A non-nil error ends the item; the
// outcome tells the caller whether to rotate to the next key.
// The result's Text is what the unit keeps (see keepText).
func (r *Runner) freeAttempt(ctx context.Context, id string, key config.Key, unit store.Unit, pdfs []string) (gemini.Result, keyOutcome, error) {
	client, err := r.client(key)
	if err != nil {
		return gemini.Result{}, keyRejected, nil
	}
	req, err := r.uploadParts(ctx, client, id, key.ID, unit, pdfs)
	if err != nil {
		// The upload retries on its own; its failure is one event.
		r.signal(ctx, config.ModeFree, 1, err, r.freeUnderQuota(key.ID, false))
		if outcome, ok := keyFailure(err); ok {
			return gemini.Result{}, outcome, nil
		}
		return gemini.Result{}, keyOK, err
	}

	r.setDetail(id, i18n.T("status.transcribing"))
	model := gemini.Model
	blocks := 0
	retriedMalformed := false
	var res gemini.Result
	for {
		res, err = r.generate(ctx, id, key, client, req, model, unit.Seconds)
		if err != nil {
			if outcome, ok := keyFailure(err); ok {
				return gemini.Result{}, outcome, nil
			}
			if isOverload(err) && model != gemini.OverloadModel {
				// 503 is the model's capacity, not the key's: another key
				// would get the same answer, a lighter model usually not.
				r.logf("item %s unit %d: %s busy, retrying on %s", id, unit.Index, model, gemini.OverloadModel)
				r.setDetail(id, i18n.T("status.busyFallback"))
				model = gemini.OverloadModel
				continue
			}
			return gemini.Result{}, keyOK, err
		}
		if cerr := checkText(res.Text, res.Finish); cerr != nil {
			var be blockedError
			if errors.As(cerr, &be) && blocks < 2 {
				// A refusal is often a one-off: the same model gets one
				// more try, then the fallback model one.
				blocks++
				next := model
				if blocks == 2 {
					next = gemini.FallbackModel
				}
				r.logf("item %s unit %d: %s blocked (%s), retrying on %s", id, unit.Index, model, be.reason, next)
				r.setDetail(id, i18n.T("status.fallback"))
				model = next
				continue
			}
		}
		if res.Finish == gemini.FinishMalformed && strings.TrimSpace(res.Text) == "" && !retriedMalformed {
			r.logf("item %s unit %d: empty %s on %s, retrying once", id, unit.Index, res.Finish, model)
			r.setDetail(id, retryDetail())
			retriedMalformed = true
			continue
		}
		break
	}
	r.setDetail(id, i18n.T("status.transcribing"))
	if err := checkText(res.Text, res.Finish); err != nil {
		return gemini.Result{}, keyOK, err
	}
	if res.Finish == gemini.FinishMaxTokens {
		r.logf("item %s unit %d: cut off at the output cap (%s)", id, unit.Index, usageLog(res.Usage))
	}
	res.Text = keepText(res.Text, res.Finish)
	res.Model = model
	r.logf("item %s unit %d: done on %s", id, unit.Index, model)
	return res, keyOK, nil
}

// generate runs one live request on model with the key's spacing, transient
// retries and telemetry. A blocked answer is not an error here: it comes back
// as a Result whose Finish is BLOCKED_*, and freeAttempt decides whether to
// try the fallback model.
func (r *Runner) generate(ctx context.Context, id string, key config.Key, client gemini.Client, req gemini.Request, model string, seconds float64) (gemini.Result, error) {
	if err := r.waitGap(ctx, key.ID, seconds); err != nil {
		return gemini.Result{}, err
	}
	req.Model = model
	var res gemini.Result
	attempt := 0
	busy := 0
	err := r.retryTransient(ctx, func(int) { r.setDetail(id, retryDetail()) }, func() error {
		r.mu.Lock()
		r.lastStart[key.ID] = r.opts.Now()
		r.mu.Unlock()
		attempt++
		under := r.freeUnderQuota(key.ID, true)
		var genErr error
		res, genErr = client.Generate(ctx, req)
		if genErr == nil && strings.TrimSpace(res.Text) == "" && (res.Finish == "" || res.Finish == gemini.FinishStop) {
			// An empty answer with no reason comes from an overloaded
			// server; the same request usually works when sent again.
			genErr = &gemini.EmptyResponseError{Finish: res.Finish, Usage: "no usage"}
		}
		if genErr != nil {
			r.signal(ctx, config.ModeFree, attempt, genErr, under)
		} else {
			r.signal(ctx, config.ModeFree, attempt, checkText(res.Text, res.Finish), nil)
		}
		if gemini.IsUnavailable(genErr) {
			busy++
			if busy >= overloadTries {
				// Ends the transient retries: overloadedError is not
				// transient.
				return overloadedError{model: model}
			}
		} else {
			busy = 0
		}
		return genErr
	})
	return res, err
}

// overloadedError means a model kept answering 503. It deliberately hides the
// status code, also from its message (StatusCode parses messages), so it is
// neither transient nor a key failure.
type overloadedError struct {
	model string
}

func (e overloadedError) Error() string { return e.model + " overloaded (503 x3)" }

// isOverload reports a live request that failed because the model is busy:
// three 503s in a row, or transient retries that ended on a 503.
func isOverload(err error) bool {
	var oe overloadedError
	return errors.As(err, &oe) || gemini.IsUnavailable(err)
}

func keyFailure(err error) (keyOutcome, bool) {
	switch {
	case gemini.Is429(err):
		return keyLimited, true
	case gemini.IsAuth(err):
		return keyRejected, true
	}
	return keyOK, false
}

// gap is the minimum spacing between request starts on one free key: 12 s
// (RPM 5) or longer when the audio would exceed the 250k TPM window.
func (r *Runner) gap(seconds float64) time.Duration {
	if r.opts.KeyGap < 0 {
		return 0
	}
	tokens := int(math.Max(0, seconds))*rateTokensPerSecond + 4000
	perMinute := freeTPM / tokens
	if perMinute < 1 {
		perMinute = 1
	}
	g := r.opts.KeyGap
	if tpm := time.Duration(float64(time.Minute) / float64(perMinute)); tpm > g {
		g = tpm
	}
	if rpm := time.Minute / freeRPM; rpm > g {
		g = rpm
	}
	return g
}

func (r *Runner) waitGap(ctx context.Context, keyID string, seconds float64) error {
	left := r.gapLeft(keyID, seconds)
	if left <= 0 {
		return nil
	}
	return r.opts.Sleep(ctx, left)
}

// gapLeft is how long keyID must still wait before its next request start.
func (r *Runner) gapLeft(keyID string, seconds float64) time.Duration {
	g := r.gap(seconds)
	if g <= 0 {
		return 0
	}
	r.mu.Lock()
	last, ok := r.lastStart[keyID]
	r.mu.Unlock()
	if !ok {
		return 0
	}
	return g - r.opts.Now().Sub(last)
}

func (r *Runner) retryTransient(ctx context.Context, onRetry func(int), fn func() error) error {
	var err error
	for attempt := 0; attempt <= transientRetries; attempt++ {
		if attempt > 0 {
			if onRetry != nil {
				onRetry(attempt)
			}
			if serr := r.opts.Sleep(ctx, r.opts.Backoff(attempt)); serr != nil {
				return serr
			}
		}
		err = fn()
		if err == nil || !gemini.IsTransient(err) || ctx.Err() != nil {
			return err
		}
		r.logf("transient error (attempt %d): %v", attempt+1, r.redact(err))
	}
	return err
}

// defaultOverloadWaits add up to an hour: free 503 storms usually clear in
// minutes but sometimes last half an hour or more.
var defaultOverloadWaits = []time.Duration{5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 15 * time.Minute, 15 * time.Minute}

// unitMax is the longest audio one request of mode carries. Units are cut
// when an item is prepared, so an item moved between modes later keeps them.
func (r *Runner) unitMax(mode string) float64 {
	if mode == config.ModeBatch {
		return r.opts.UnitMaxSeconds
	}
	return math.Min(r.opts.UnitMaxSeconds, r.opts.FreeUnitMaxSeconds)
}

// defaultBackoff doubles from 10 s and stops at 2 min: 10, 20, 40, 80, 120 s.
func defaultBackoff(n int) time.Duration {
	if n < 1 {
		return 0
	}
	d := transientBaseDelay
	for i := 1; i < n; i++ {
		d *= 2
		if d >= transientMaxDelay {
			return transientMaxDelay
		}
	}
	return d
}

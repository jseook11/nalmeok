package runner

// Paid Batch jobs: submitting, polling and retrying.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
	"github.com/jseook11/nalmeok/internal/telemetry"
)

func (r *Runner) submitBatch(ctx context.Context, item store.Item) error {
	id := item.ID
	if item.Batch != nil && item.Batch.Name != "" {
		// server did not confirm, or a status check that failed). It is paid
		// for already, so wait for it again instead of submitting anew.
		r.logf("item %s waits again for %s", id, item.Batch.Name)
		return r.update(id, func(it *store.Item) {
			if it.State == StateRunning {
				it.State = StateBatchWaiting
				it.Detail = ""
			}
		})
	}
	key, err := r.batchKey(item.KeyID)
	if err != nil {
		return err
	}
	client, err := r.client(key)
	if err != nil {
		return err
	}

	pending := make([]store.Unit, 0, len(item.Units))
	for _, u := range item.Units {
		if u.Text == "" {
			pending = append(pending, u)
		}
	}
	if len(pending) == 0 {
		return r.finish(id)
	}

	if item.Batch != nil && item.Batch.Marker != "" {
		// The last creation got no answer. The job may exist anyway.
		bkey := key
		if item.Batch.KeyID != "" && item.Batch.KeyID != key.ID {
			if k, ok := findKey(r.opts.Keys(), item.Batch.KeyID); ok {
				bkey = k
			}
		}
		bclient, err := r.client(bkey)
		if err != nil {
			return err
		}
		r.setDetail(id, i18n.T("status.checkingRequest"))
		name, err := bclient.FindBatch(ctx, item.Batch.Marker)
		if err != nil {
			return err
		}
		if name != "" {
			r.logf("item %s found earlier batch %s", id, name)
			return r.update(id, func(it *store.Item) {
				if it.Batch == nil {
					it.Batch = &store.BatchRef{}
				}
				it.Batch.KeyID = bkey.ID
				it.Batch.Name = name
				it.Batch.Marker = ""
				it.Batch.SubmittedAt = r.opts.Now()
				if it.State == StateRunning {
					it.State = StateBatchWaiting
					it.Detail = ""
				}
			})
		}
	}

	uploads := map[string]store.Upload{}
	if item.Batch != nil && item.Batch.KeyID == key.ID {
		for path, up := range item.Batch.Uploads {
			if r.opts.Now().Sub(up.UploadedAt) < uploadMaxAge {
				uploads[path] = up
			}
		}
	}
	pdfs := r.pdfPaths(id)
	type needFile struct{ path, mime string }
	need := []needFile{}
	for _, pdf := range pdfs {
		need = append(need, needFile{pdf, mimePDF})
	}
	for _, u := range pending {
		for i, path := range u.Files {
			need = append(need, needFile{path, unitMIME(u, i)})
		}
	}
	r.setDetail(id, i18n.T("status.uploading"))
	for _, f := range need {
		if _, ok := uploads[f.path]; ok {
			continue
		}
		up, err := r.upload(ctx, client, f.path, f.mime)
		if err != nil {
			r.signal(ctx, config.ModeBatch, 1, err, nil)
			return err
		}
		uploads[f.path] = up
		saved := cloneUploads(uploads)
		ref := nextBatchRef(item, key.ID)
		ref.Uploads = saved
		if err := r.update(id, func(it *store.Item) {
			it.Batch = &ref
		}); err != nil {
			return err
		}
	}

	model := jobModel(item.Batch)
	reqs := make([]gemini.BatchRequest, 0, len(pending))
	for _, u := range pending {
		var parts []gemini.Part
		for _, pdf := range pdfs {
			parts = append(parts, gemini.Part{FileURI: uploads[pdf].URI, MIME: uploads[pdf].MIME})
		}
		for _, f := range u.Files {
			parts = append(parts, gemini.Part{FileURI: uploads[f].URI, MIME: uploads[f].MIME})
		}
		reqs = append(reqs, gemini.BatchRequest{Key: unitKey(u.Index), Request: gemini.Request{Model: model, System: gemini.PromptFor(len(u.Files)), Parts: parts}})
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.setDetail(id, i18n.T("status.sending"))
	// Creating a job is never repeated automatically: when the answer is lost
	// the job may exist, and a second one would be billed again. The marker
	// is saved first so a retry can look the job up.
	marker := batchDisplayName(item.Name, store.NewID())
	markerRef := nextBatchRef(item, key.ID)
	markerRef.Uploads = cloneUploads(uploads)
	markerRef.Marker = marker
	if err := r.update(id, func(it *store.Item) {
		it.Batch = &markerRef
	}); err != nil {
		return err
	}
	name, err := client.SubmitBatch(ctx, reqs, marker)
	r.signal(ctx, config.ModeBatch, 1, err, nil)
	if err != nil {
		r.logf("item %s batch create (%s): %v", id, marker, r.redact(err))
		return err
	}
	r.logf("item %s batch submitted: %s (%d requests)", id, name, len(reqs))
	saved := cloneUploads(uploads)
	finalRef := nextBatchRef(item, key.ID)
	finalRef.Uploads = saved
	finalRef.Name = name
	finalRef.SubmittedAt = r.opts.Now()
	return r.update(id, func(it *store.Item) {
		// A cancel during creation keeps its state; the name is kept so a
		// retry waits for this job.
		if it.State == StateRunning {
			it.State = StateBatchWaiting
			it.Detail = ""
		}
		it.Progress = doneRatio(it.Units)
		it.Batch = &finalRef
	})
}

// batchDisplayName names a job after the item plus a unique marker, so a job
// whose creation answer was lost can be found again. The item name is cut so
// the marker always fits the server's limit.
func batchDisplayName(name, token string) string {
	const maxName = 80
	if len(name) > maxName {
		name = name[:maxName]
		for len(name) > 0 && !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return name + " #" + token
}

// batchKey is the pinned paid key, or the highest-priority paid key.
func (r *Runner) batchKey(pinned string) (config.Key, error) {
	keys := r.opts.Keys()
	if pinned != "" {
		key, ok := findKey(keys, pinned)
		if !ok {
			return config.Key{}, errPinnedMissing
		}
		if key.Tier == config.TierFree {
			return config.Key{}, userError{i18n.Error("error.notPaidKey")}
		}
		return key, nil
	}
	_, paid := splitKeys(keys)
	if len(paid) == 0 {
		return config.Key{}, userError{i18n.Error("error.noPaidKey")}
	}
	return paid[0], nil
}

func unitKey(index int) string { return fmt.Sprintf("unit-%d", index) }

func cloneUploads(in map[string]store.Upload) map[string]store.Upload {
	out := make(map[string]store.Upload, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// nextBatchRef starts a BatchRef for a new write and keeps the item's retry
// state (Fallback, Attempt), so rewriting the ref for an upload, a marker or
// a submitted job never loses it. The caller sets Uploads, Marker or Name.
func nextBatchRef(item store.Item, keyID string) store.BatchRef {
	var ref store.BatchRef
	if item.Batch != nil {
		ref = *item.Batch
	}
	ref.KeyID = keyID
	ref.Name = ""
	ref.Marker = ""
	ref.SubmittedAt = time.Time{}
	ref.Uploads = nil
	return ref
}

func (r *Runner) poller(ctx context.Context) {
	ticker := time.NewTicker(r.opts.PollInterval)
	defer ticker.Stop()
	r.PollOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.pollNow:
		}
		r.PollOnce(ctx)
	}
}

// PollOnce checks every batch_waiting item once.
func (r *Runner) PollOnce(ctx context.Context) {
	for _, item := range r.opts.Store.List("") {
		if ctx.Err() != nil {
			return
		}
		if item.State == StateBatchWaiting {
			r.pollItem(ctx, item.ID)
		}
	}
}

func (r *Runner) pollItem(ctx context.Context, id string) {
	r.pollMu.Lock()
	defer r.pollMu.Unlock()
	item, ok := r.opts.Store.Get(id)
	if !ok || item.State != StateBatchWaiting {
		return
	}
	if item.Batch == nil || item.Batch.Name == "" {
		r.fail(id, i18n.T("error.noRequestInfo"))
		return
	}
	client, err := r.clientForKeyID(item.Batch.KeyID)
	if err != nil {
		r.fail(id, i18n.T("error.requestKeyMissing"))
		return
	}
	state, err := client.BatchStatus(ctx, item.Batch.Name)
	if err != nil {
		r.pollError(ctx, id, "status", err)
		return
	}
	switch state {
	case gemini.BatchPending, gemini.BatchRunning:
		return
	case gemini.BatchFailed:
		r.signalOutcome(config.ModeBatch, 1, telemetry.Other, 0, nil)
		r.batchFail(id, i18n.T("error.batchFailed"))
		return
	case gemini.BatchCancelled:
		// Canceled by the user: says nothing about how Batch works.
		r.batchFail(id, i18n.T("error.batchCanceled"))
		return
	case gemini.BatchExpired:
		// The job did not finish within the server's 48 h window.
		r.signalOutcome(config.ModeBatch, 1, telemetry.Timeout, 0, nil)
		r.batchFail(id, i18n.T("error.batchExpired"))
		return
	}
	r.collectLocked(ctx, id, client, item.Batch.Name, true)
}

// pollError decides what a failed status or results call means. Network
// trouble, timeouts and rate limits keep waiting on the same job. A job the
// server does not know ends the wait. Other refusals mark the item failed
// but keep the job name, so a retry checks the same job again rather than
// paying for a new one.
func (r *Runner) pollError(ctx context.Context, id, what string, err error) {
	r.logf("item %s batch %s: %v", id, what, r.redact(err))
	switch {
	case ctx.Err() != nil, gemini.IsTransient(err), gemini.Is429(err):
	case gemini.IsAuth(err):
		r.fail(id, i18n.T("error.paidKeyInvalid"))
	case gemini.IsNotFound(err):
		r.batchFail(id, i18n.T("error.batchMissing"))
	default:
		r.fail(id, i18n.T("error.batchStatusFailed"))
	}
}

// collectLocked stores the results of a finished job and writes the output.
// The caller holds pollMu. allowAuto is false when the user canceled the item:
// the results are kept, but no new paid job is started.
func (r *Runner) collectLocked(ctx context.Context, id string, client gemini.Client, name string, allowAuto bool) {
	results, err := client.BatchResults(ctx, name)
	if err != nil {
		r.pollError(ctx, id, "results", err)
		return
	}
	failed := 0
	blocked := false
	malformed := false
	firstReason := ""
	err = r.update(id, func(it *store.Item) {
		for i := range it.Units {
			if it.Units[i].Text != "" {
				continue
			}
			res, ok := results[unitKey(it.Units[i].Index)]
			reason := ""
			switch {
			case !ok:
				reason = i18n.T("error.noResult")
			case res.Error != "":
				reason = res.Error
			default:
				if cerr := checkText(res.Text, res.Finish); cerr != nil {
					reason = cerr.Error()
					var be blockedError
					if errors.As(cerr, &be) {
						blocked = true
					} else if res.Finish == gemini.FinishMalformed && strings.TrimSpace(res.Text) == "" {
						malformed = true
					}
				}
			}
			if reason != "" {
				failed++
				if firstReason == "" {
					firstReason = reason
				}
				continue
			}
			if res.Finish == gemini.FinishMaxTokens {
				r.logf("item %s unit %d: cut off at the output cap (%s)", id, it.Units[i].Index, usageLog(res.Usage))
			}
			it.Units[i].Text = keepText(res.Text, res.Finish)
			it.Units[i].Model = jobModel(it.Batch)
			if !res.Usage.Empty() {
				u := res.Usage
				u.Batch = true
				it.Units[i].Usage = &u
			}
		}
		it.Progress = doneRatio(it.Units)
	})
	if err != nil {
		r.logf("item %s save batch results: %v", id, err)
		return
	}
	// One event per finished job, like the job's own state.
	switch {
	case blocked:
		r.signalOutcome(config.ModeBatch, 1, telemetry.Blocked, 0, nil)
	case failed > 0:
		r.signalOutcome(config.ModeBatch, 1, telemetry.Other, 0, nil)
	default:
		r.signalOutcome(config.ModeBatch, 1, telemetry.OK, 0, nil)
	}
	if failed > 0 {
		item, _ := r.opts.Store.Get(id)
		if retry, fallback := autoRetry(item.Batch, blocked, malformed); allowAuto && retry {
			r.logf("item %s batch: %d units failed (%s), retrying automatically", id, failed, firstReason)
			r.retryBatch(id, item.Batch.Attempt, fallback)
			return
		}
		if blocked {
			r.logf("item %s batch blocked: %s", id, firstReason)
			r.batchFail(id, blockedMessage())
			return
		}
		r.logf("item %s batch: %d units failed (%s)", id, failed, firstReason)
		r.batchFail(id, i18n.T("error.chunksFailed", "count", failed))
		return
	}
	if err := r.finish(id); err != nil {
		r.logf("item %s finish: %v", id, err)
		r.fail(id, userMessage(err))
	}
}

func (r *Runner) batchFail(id, detail string) {
	r.logf("item %s: %s", id, detail)
	_ = r.update(id, func(it *store.Item) {
		it.State = StateFailed
		it.Detail = detail
		if it.Batch != nil {
			it.Batch.Name = ""
		}
	})
}

// jobModel is the model a Batch job of ref runs on.
func jobModel(ref *store.BatchRef) string {
	if ref != nil && ref.Fallback {
		return gemini.FallbackModel
	}
	return gemini.Model
}

// autoRetry decides the automatic resubmission after a job with failed
// units. A refusal is retried once on the same model, then once on the
// fallback model; an empty answer once on the same model. A job already on
// the fallback model (a manual retry keeps it) is not retried again.
func autoRetry(ref *store.BatchRef, blocked, malformed bool) (retry, fallback bool) {
	if ref == nil || ref.Fallback {
		return false, false
	}
	switch {
	case blocked && ref.Attempt == 0:
		return true, false
	case blocked && ref.Attempt == 1:
		return true, true
	case malformed && ref.Attempt == 0:
		return true, false
	}
	return false, false
}

// retryBatch resubmits the unfinished units after a refused or empty job.
// fallback picks the fallback model. seen is the Attempt the caller decided
// on; a restart or another poll that already moved past it does nothing.
func (r *Runner) retryBatch(id string, seen int, fallback bool) {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return
	}
	detail := retryDetail()
	if fallback {
		detail = i18n.T("status.fallback")
	}
	moved := false
	err := r.update(id, func(it *store.Item) {
		if it.State != StateBatchWaiting || it.Batch == nil || it.Batch.Attempt != seen {
			return
		}
		ref := it.Batch
		ref.Attempt++
		ref.Fallback = ref.Fallback || fallback
		ref.Name = ""
		ref.Marker = ""
		ref.SubmittedAt = time.Time{}
		ref.Uploads = cloneUploads(ref.Uploads)
		it.Batch = ref
		it.State = StateRunning
		it.Detail = detail
		it.Progress = doneRatio(it.Units)
		moved = true
	})
	if err != nil {
		r.logf("item %s retry batch: %v", id, err)
		return
	}
	if !moved {
		return
	}
	r.logf("item %s: automatic retry on %s", id, jobModel(&store.BatchRef{Fallback: fallback}))
	r.enqueue(id, item.Mode)
}

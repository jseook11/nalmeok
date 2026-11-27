package runner

// Finishing: writing the merged .md, the item's cost, deleting a received
// recording, and failing an item.

import (
	"os"

	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/media"
	"github.com/jseook11/nalmeok/internal/output"
	"github.com/jseook11/nalmeok/internal/plan"
	"github.com/jseook11/nalmeok/internal/store"
)

func (r *Runner) finish(id string) error {
	item, ok := r.opts.Store.Get(id)
	if !ok {
		return store.ErrNotFound
	}
	if item.State != StateRunning && item.State != StateBatchWaiting {
		return nil // canceled meanwhile
	}
	texts := make([]string, 0, len(item.Units))
	for _, u := range item.Units {
		if u.Text == "" {
			return userError{i18n.Error("error.chunksUnfinished")}
		}
		texts = append(texts, u.Text)
	}
	merged := output.Merge(texts)
	if merged == "" {
		return userError{i18n.Error("error.emptyResult")}
	}
	if r.opts.OutputDir == "" {
		// A relative target would land in the working directory.
		return userError{i18n.Error("error.noOutputDir")}
	}
	target, err := output.WriteNew(output.TargetPath(r.opts.OutputDir, item.Source, item.Kind), merged)
	if err != nil {
		return userError{i18n.Error("error.saveFailed", "error", err)}
	}
	// The app's own copy outlives a moved or renamed .md. The .md is saved
	// already, so a failed copy does not fail the item.
	if archive := output.ArchivePath(r.opts.TranscriptsDir, id); archive != "" {
		if err := output.Write(archive, merged); err != nil {
			r.logf("item %s archive: %v", id, err)
		}
	}
	// The recording time is kept now: the source may be deleted next, and
	// sorting reads it later.
	from, to := item.RecordedFrom, item.RecordedTo
	if from.IsZero() {
		from, to, _ = media.RecordedSpan(item.Source)
	}
	if err := r.update(id, func(it *store.Item) {
		it.State = StateDone
		it.RecordedFrom, it.RecordedTo = from, to
		it.Output = target
		it.Progress = 1
		it.Detail = ""
		it.FinishedAt = r.opts.Now()
		it.Usage, it.CostKRW = itemCost(it.Units)
		it.Models = unitModels(it.Units)
		it.Batch = nil
		// The .md (and its archived copy) is the record; dropping texts and
		// uploads keeps items.json small.
		it.Units = nil
		it.Uploads = nil
	}); err != nil {
		return err
	}
	_ = os.RemoveAll(r.workDir(id))
	r.removeReceived(id, item.Source)
	r.logf("item %s done: %s", id, target)
	r.opts.Notify("Nalmeok", i18n.T("notify.done", "name", item.Name))
	return nil
}

// removeReceived deletes source when the app saved it itself (a recording
// dragged from Voice Memos, see Options.ReceivedDirs) and no other item still
// needs it: one not finished yet that uses the same file or a folder holding
// it. A user's own recording is never touched.
func (r *Runner) removeReceived(id, source string) {
	if !r.received(source) {
		return
	}
	for _, other := range r.opts.Store.List("") {
		uses := other.Source == source || output.Inside(other.Source, source)
		if other.ID != id && uses && other.State != StateDone {
			return
		}
	}
	if err := os.Remove(source); err != nil && !os.IsNotExist(err) {
		r.logf("item %s received recording: %v", id, err)
		return
	}
	r.logf("item %s received recording removed", id)
}

// received reports whether source is in one of Options.ReceivedDirs.
func (r *Runner) received(source string) bool {
	for _, dir := range r.opts.ReceivedDirs {
		if output.Inside(dir, source) {
			return true
		}
	}
	return false
}

// itemCost sums the units' reported usage and prices the paid part. Free
// requests cost nothing. Both are nil when no unit reported usage (results
// from before usage was recorded); a partial record counts what is known.
func itemCost(units []store.Unit) (*store.Usage, *int) {
	var total store.Usage
	usd := 0.0
	known := false
	for _, u := range units {
		if u.Usage == nil {
			continue
		}
		known = true
		total.Audio += u.Usage.Audio
		total.Text += u.Usage.Text
		total.Output += u.Usage.Output
		total.Thoughts += u.Usage.Thoughts
		if u.Usage.Batch {
			total.Batch = true
			usd += plan.CostUSD(u.Model, u.Usage.Audio, u.Usage.Text, u.Usage.Output+u.Usage.Thoughts, true)
		}
	}
	if !known {
		return nil, nil
	}
	won := plan.USDToKRW(usd)
	return &total, &won
}

// unitModels counts units by the model that produced them; nil when no unit
// recorded one.
func unitModels(units []store.Unit) map[string]int {
	var out map[string]int
	for _, u := range units {
		if u.Model == "" {
			continue
		}
		if out == nil {
			out = map[string]int{}
		}
		out[u.Model]++
	}
	return out
}

func (r *Runner) fail(id, detail string) {
	_ = r.update(id, func(it *store.Item) {
		if it.State == StateCanceled {
			return
		}
		it.State = StateFailed
		it.Detail = detail
	})
}

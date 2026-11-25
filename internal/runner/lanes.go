package runner

// Lanes: one queue and worker per mode, so a paid item never waits behind
// a free one, and the worker's run of one item (runItem, process).

import (
	"context"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/store"
)

// lane runs one kind of work, one item at a time in FIFO order. Free live work
// is serial because of per-key rate limits; Batch submission is serial to
// share upload bandwidth, and the poller takes over once a job is submitted.
// Every field is guarded by Runner.mu.
type lane struct {
	mode     string // the item mode this lane runs
	queue    []string
	queued   map[string]bool
	active   string
	cancel   context.CancelFunc // cancels the active item; nil while it starts
	canceled bool               // the active item was stopped on purpose
	// again is the mode to queue the active item in once it has stopped: a
	// retry or a move to paid that arrived while the old run was unwinding.
	again string
	wake  chan struct{}
}

func newLane(mode string) *lane {
	return &lane{mode: mode, queued: make(map[string]bool), wake: make(chan struct{}, 1)}
}

// laneFor returns the lane that runs items of mode.
func (r *Runner) laneFor(mode string) *lane {
	if l, ok := r.lanes[mode]; ok {
		return l
	}
	return r.lanes[config.ModeFree]
}

// stopActiveLocked cancels id's current run, if any. Callers hold r.mu.
func (r *Runner) stopActiveLocked(id string) {
	for _, l := range r.lanes {
		if l.active == id {
			l.canceled = true
			if l.cancel != nil {
				l.cancel()
			}
		}
	}
}

func (l *lane) remove(id string) {
	if !l.queued[id] {
		return
	}
	delete(l.queued, id)
	for i, q := range l.queue {
		if q == id {
			l.queue = append(l.queue[:i], l.queue[i+1:]...)
			break
		}
	}
}

// enqueue puts id at the back of the lane for mode. An item that is still
// active in a lane is queued once that run has stopped.
func (r *Runner) enqueue(id, mode string) {
	target := r.laneFor(mode)
	r.mu.Lock()
	for _, l := range r.lanes {
		if l.active == id {
			l.again = target.mode
			r.mu.Unlock()
			return
		}
		if l != target {
			l.remove(id)
		}
	}
	if !target.queued[id] {
		target.queue = append(target.queue, id)
		target.queued[id] = true
	}
	r.mu.Unlock()
	select {
	case target.wake <- struct{}{}:
	default:
	}
}

func (r *Runner) isActive(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.lanes {
		if l.active == id {
			return true
		}
	}
	return false
}

func (r *Runner) next(l *lane) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(l.queue) == 0 {
		return "", false
	}
	id := l.queue[0]
	l.queue = l.queue[1:]
	delete(l.queued, id)
	// Taking the active slot under the same lock means an enqueue racing
	// with the start never queues the item a second time.
	l.active = id
	return id, true
}

func (r *Runner) worker(ctx context.Context, l *lane) {
	for {
		id, ok := r.next(l)
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-l.wake:
				continue
			}
		}
		r.runItem(ctx, l, id)
	}
}

func (r *Runner) runItem(parent context.Context, l *lane, id string) {
	ctx, cancel := context.WithCancel(parent)
	r.mu.Lock()
	l.cancel = cancel
	if l.canceled {
		// Stopped between next and here, before there was anything to
		// cancel: start canceled so the stop is not lost.
		cancel()
	}
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		again := l.again
		l.active = ""
		l.cancel = nil
		l.canceled = false
		l.again = ""
		r.mu.Unlock()
		if again != "" {
			r.enqueue(id, again)
		}
	}()

	// A cancel, remove or move to the other lane may land before the run
	// starts. Only a running item of this lane's mode starts work.
	current, ok := r.opts.Store.Get(id)
	if !ok || current.State != StateRunning || r.laneFor(current.Mode) != l {
		return
	}

	err := r.process(ctx, id)
	if err == nil {
		return
	}
	r.mu.Lock()
	userCanceled := l.canceled
	r.mu.Unlock()
	if userCanceled {
		r.logf("item %s canceled", id)
		return
	}
	if parent.Err() != nil {
		// App shutdown: leave the item running so the next start resumes it.
		r.logf("item %s interrupted by shutdown", id)
		return
	}
	r.logf("item %s failed: %v", id, err)
	r.fail(id, userMessage(err))
}

func (r *Runner) process(ctx context.Context, id string) error {
	_ = r.update(id, func(it *store.Item) { it.StartedAt = r.opts.Now() })
	item, err := r.prepare(ctx, id)
	if err != nil {
		return err
	}
	if item.Mode == config.ModeBatch {
		return r.submitBatch(ctx, item)
	}
	return r.back.free(r, ctx, item)
}

package runner

import (
	"context"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
)

// backend is one transcription service the runner can drive. Gemini (API
// keys) is the default; codex (a local Codex login) is the experiment in
// codex.go. Everything backend-specific goes through this interface, so the
// runner itself never asks which service it is talking to: a new backend is a
// new implementation, not another branch in a lane.
type backend interface {
	// defaults fills in backend-specific option defaults. New applies the
	// generic defaults afterwards, so a backend only sets what differs.
	defaults(o *Options)
	// resolveMode checks the backend is usable and returns the mode new items
	// run in.
	resolveMode(r *Runner, mode, keyID string) (string, error)
	// usesKeys reports whether items can be pinned to an API key.
	usesKeys() bool
	// candidates returns the keys to try for one unit, in order.
	candidates(r *Runner, pinned string, seconds float64) ([]config.Key, error)
	// parts builds one unit's request, uploading files when the service needs
	// its own copy.
	parts(r *Runner, ctx context.Context, client gemini.Client, id, keyID string, unit store.Unit, pdfs []string) (gemini.Request, error)
	// free transcribes an item's unfinished units live.
	free(r *Runner, ctx context.Context, item store.Item) error
	// client returns the client that talks to the service with key.
	client(r *Runner, key config.Key) (gemini.Client, error)
	// batches reports whether Batch submission is available.
	batches() bool
	// signals reports whether request outcomes go to telemetry.
	signals() bool
	// message maps one of this backend's errors to a user sentence. It returns
	// false when the error is not its own, leaving the generic mapping.
	message(err error) (string, bool)
	// skips reports whether a dropped path holds audio this backend cannot
	// send. Add leaves such paths out instead of letting them fail later.
	skips(path string) bool
}

// newBackend returns the backend named by Backend. An empty or unknown name
// is Gemini, so the zero Options value keeps the original behavior.
func newBackend(name string) backend {
	switch name {
	case BackendCodex:
		return codexBackend{}
	}
	return geminiBackend{}
}

// geminiBackend transcribes with Gemini API keys: live requests on free keys,
// Batch jobs on paid ones.
type geminiBackend struct{}

func (geminiBackend) defaults(*Options) {}

func (geminiBackend) resolveMode(r *Runner, mode, keyID string) (string, error) {
	keys := r.opts.Keys()
	free, paid := splitKeys(keys)
	if len(free) == 0 && len(paid) == 0 {
		return "", i18n.Error("error.keyRequired")
	}
	if mode != "batch" {
		mode = "free"
	}
	if mode == "free" && len(free) == 0 {
		mode = "batch"
	} else if mode == "batch" && len(paid) == 0 {
		mode = "free"
	}
	if keyID != "" {
		key, ok := findKey(keys, keyID)
		if !ok {
			return "", errPinnedMissing
		}
		mode = modeForTier(key.Tier)
	}
	return mode, nil
}

func (geminiBackend) usesKeys() bool { return true }

func (geminiBackend) skips(string) bool { return false }

// candidates returns the free keys to try for one unit, in order.
//
// A pinned item gets only its key, so a 429 ends in free_exhausted instead of
// moving to another key. Otherwise keys follow the priority order (config
// order, environment key first). Free items run one at a time, so strict
// priority would make every unit wait out the 12 s+ gap on the first key while
// others sit idle; keys whose gap has already passed therefore go first, each
// group still in priority order. Every key is still tried before giving up.
func (geminiBackend) candidates(r *Runner, pinned string, seconds float64) ([]config.Key, error) {
	keys := r.opts.Keys()
	if pinned != "" {
		key, ok := findKey(keys, pinned)
		if !ok {
			return nil, errPinnedMissing
		}
		if key.Tier != "free" {
			return nil, userError{i18n.Error("error.notFreeKey")}
		}
		return []config.Key{key}, nil
	}
	free, _ := splitKeys(keys)
	if len(free) == 0 {
		return nil, userError{i18n.Error("error.noFreeKey")}
	}
	ready := make([]config.Key, 0, len(free))
	waiting := make([]config.Key, 0, len(free))
	for _, key := range free {
		if r.gapLeft(key.ID, seconds) > 0 {
			waiting = append(waiting, key)
		} else {
			ready = append(ready, key)
		}
	}
	return append(ready, waiting...), nil
}

func (geminiBackend) parts(r *Runner, ctx context.Context, client gemini.Client, id, keyID string, unit store.Unit, pdfs []string) (gemini.Request, error) {
	return r.uploadParts(ctx, client, id, keyID, unit, pdfs)
}

func (geminiBackend) free(r *Runner, ctx context.Context, item store.Item) error {
	return r.runFreeGemini(ctx, item)
}

func (geminiBackend) client(r *Runner, key config.Key) (gemini.Client, error) {
	r.mu.Lock()
	c, ok := r.clients[key.APIKey]
	r.mu.Unlock()
	if ok {
		return c, nil
	}
	c, err := r.opts.NewClient(key.APIKey)
	if err != nil {
		r.logf("client for key %s: %v", key.ID, r.redact(err))
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Both lanes may create a client for the same key at once; keep the first.
	if existing, ok := r.clients[key.APIKey]; ok {
		return existing, nil
	}
	r.clients[key.APIKey] = c
	return c, nil
}

func (geminiBackend) batches() bool                { return true }
func (geminiBackend) signals() bool                { return true }
func (geminiBackend) message(error) (string, bool) { return "", false }

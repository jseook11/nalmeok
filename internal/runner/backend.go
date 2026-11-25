package runner

import (
	"context"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
)

// backend is one transcription service the runner can drive. Gemini (API
// keys) is the only implementation. Everything service-specific goes through
// this interface, so the runner never asks which service it is talking to;
// the interface is the seam where a future backend would plug in, as a new
// implementation rather than another branch in a lane.
type backend interface {
	// resolveMode checks the backend is usable and returns the mode new items
	// run in.
	resolveMode(r *Runner, mode, keyID string) (string, error)
	// candidates returns the keys to try for one unit, in order.
	candidates(r *Runner, pinned string, seconds float64) ([]config.Key, error)
	// parts builds one unit's request, uploading files when the service needs
	// its own copy.
	parts(r *Runner, ctx context.Context, client gemini.Client, id, keyID string, unit store.Unit, pdfs []string) (gemini.Request, error)
	// free transcribes an item's unfinished units live.
	free(r *Runner, ctx context.Context, item store.Item) error
	// client returns the client that talks to the service with key.
	client(r *Runner, key config.Key) (gemini.Client, error)
}

// geminiBackend transcribes with Gemini API keys: live requests on free keys,
// Batch jobs on paid ones.
type geminiBackend struct{}

func (geminiBackend) resolveMode(r *Runner, mode, keyID string) (string, error) {
	keys := r.opts.Keys()
	free, paid := splitKeys(keys)
	if len(free) == 0 && len(paid) == 0 {
		return "", i18n.Error("error.keyRequired")
	}
	if keyID != "" {
		key, ok := findKey(keys, keyID)
		if !ok {
			return "", errPinnedMissing
		}
		return config.ModeForTier(key.Tier), nil
	}
	if mode != config.ModeBatch {
		mode = config.ModeFree
	}
	return availableMode(mode, len(free), len(paid)), nil
}

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
		if key.Tier != config.TierFree {
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

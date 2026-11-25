package runner

import (
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/gemini"
	"github.com/jseook11/nalmeok/internal/i18n"
)

// errPinnedMissing is shown when an item's chosen key was deleted.
var errPinnedMissing = userError{i18n.Error("error.pinnedKeyMissing")}

// resolveMode checks that a key exists and returns the mode new items run
// in: the asked one when its tier has keys, the other tier otherwise, and the
// pinned key's tier when keyID is set.
func (r *Runner) resolveMode(mode, keyID string) (string, error) {
	if r.opts.Store == nil {
		return "", i18n.Error("error.notReady")
	}
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

// availableMode keeps mode while its tier has keys and otherwise moves to
// the other tier when that one has keys.
func availableMode(mode string, free, paid int) string {
	switch {
	case mode == config.ModeFree && free == 0 && paid > 0:
		return config.ModeBatch
	case mode == config.ModeBatch && paid == 0 && free > 0:
		return config.ModeFree
	}
	return mode
}

// freeCandidates returns the free keys to try for one unit, in order.
//
// A pinned item gets only its key, so a 429 ends in free_exhausted instead of
// moving to another key. Otherwise keys follow the priority order (config
// order, environment key first). Free items run one at a time, so strict
// priority would make every unit wait out the 12 s+ gap on the first key while
// others sit idle; keys whose gap has already passed therefore go first, each
// group still in priority order. Every key is still tried before giving up.
func (r *Runner) freeCandidates(pinned string, seconds float64) ([]config.Key, error) {
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

// client returns the client for key, made once per API key.
func (r *Runner) client(key config.Key) (gemini.Client, error) {
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

func findKey(keys []config.Key, id string) (config.Key, bool) {
	for _, k := range keys {
		if k.ID == id {
			return k, true
		}
	}
	return config.Key{}, false
}

func splitKeys(keys []config.Key) (free, paid []config.Key) {
	for _, k := range keys {
		if k.Tier == config.TierFree {
			free = append(free, k)
		} else {
			paid = append(paid, k)
		}
	}
	return free, paid
}

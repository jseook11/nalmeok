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
	return r.back.resolveMode(r, mode, keyID)
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

// client returns the client for key from the backend.
func (r *Runner) client(key config.Key) (gemini.Client, error) {
	return r.back.client(r, key)
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

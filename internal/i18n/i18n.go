// Package i18n looks up user-facing text in the string catalog.
//
// The catalog lives in frontend/dist/i18n/<lang>.json so the Go side and the
// window share one file. Each file is a flat JSON object of key → text; text
// may contain placeholders like "{name}" that T fills in. English (en) is the
// base language: a key missing from another language falls back to en, and a
// key missing from en comes back as the key itself (a test keeps that from
// shipping).
package i18n

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/jseook11/nalmeok/frontend"
)

// Base is the language every other catalog falls back to.
const Base = "en"

var (
	mu      sync.RWMutex
	lang    = Base
	loaded  = map[string]map[string]string{}
	loadErr = map[string]error{}
)

// Path returns the catalog's path inside frontend.Assets.
func Path(lang string) string { return "dist/i18n/" + lang + ".json" }

// Load reads one language's catalog from the embedded assets.
func Load(lang string) (map[string]string, error) {
	data, err := fs.ReadFile(frontend.Assets, Path(lang))
	if err != nil {
		return nil, err
	}
	var texts map[string]string
	if err := json.Unmarshal(data, &texts); err != nil {
		return nil, fmt.Errorf("%s: %w", Path(lang), err)
	}
	return texts, nil
}

// Detect maps a locale such as "en_US.UTF-8" or "ko-KR" to a catalog
// language. Korean uses Korean; every other locale, and no setting at all,
// uses the base language, English.
func Detect(locale string) string {
	locale = strings.ToLower(strings.TrimSpace(locale))
	if locale == "ko" ||
		strings.HasPrefix(locale, "ko_") ||
		strings.HasPrefix(locale, "ko-") ||
		strings.HasPrefix(locale, "ko.") {
		return "ko"
	}
	return Base
}

// SetLanguage picks the language T uses. It reports an error, and keeps the
// current language, when that catalog cannot be read.
func SetLanguage(l string) error {
	if _, err := catalog(l); err != nil {
		return err
	}
	mu.Lock()
	lang = l
	mu.Unlock()
	return nil
}

// Language returns the language T uses.
func Language() string {
	mu.RLock()
	defer mu.RUnlock()
	return lang
}

func catalog(l string) (map[string]string, error) {
	mu.RLock()
	c, ok := loaded[l]
	err := loadErr[l]
	mu.RUnlock()
	if ok || err != nil {
		return c, err
	}
	c, err = Load(l)
	mu.Lock()
	if err != nil {
		loadErr[l] = err
	} else {
		loaded[l] = c
	}
	mu.Unlock()
	return c, err
}

// Lookup returns key's text in the current language, falling back to the
// base language. ok is false when neither has it.
func Lookup(key string) (string, bool) {
	if c, _ := catalog(Language()); c != nil {
		if s, ok := c[key]; ok {
			return s, true
		}
	}
	if c, _ := catalog(Base); c != nil {
		if s, ok := c[key]; ok {
			return s, true
		}
	}
	return "", false
}

// T returns key's text with placeholders filled from name/value pairs:
//
//	i18n.T("cli.done", "name", item.Name, "output", item.Output)
//
// A missing key returns the key itself.
func T(key string, pairs ...any) string {
	s, ok := Lookup(key)
	if !ok {
		return key
	}
	return Format(s, pairs...)
}

// Format replaces each "{name}" in s with the value paired with name.
// Placeholders without a value are left as they are.
func Format(s string, pairs ...any) string {
	if len(pairs) < 2 || !strings.Contains(s, "{") {
		return s
	}
	args := make([]string, 0, len(pairs))
	for i := 0; i+1 < len(pairs); i += 2 {
		args = append(args, "{"+fmt.Sprint(pairs[i])+"}", fmt.Sprint(pairs[i+1]))
	}
	return strings.NewReplacer(args...).Replace(s)
}

// Error returns an error whose message is T(key, pairs...). The text is
// looked up each time Error is called, so package-level sentinel errors
// follow SetLanguage, and errors.Is still works on them.
func Error(key string, pairs ...any) error {
	return &textError{key: key, pairs: pairs}
}

// Wrap returns an error whose message is T(key, pairs..., "error", err) and
// which unwraps to err. The catalog text decides where "{error}" goes.
func Wrap(key string, err error, pairs ...any) error {
	return &textError{key: key, pairs: pairs, err: err}
}

// IsText reports whether err, or an error it wraps, came from Error or Wrap,
// meaning its message is catalog text meant for the user.
func IsText(err error) bool {
	var te *textError
	return errors.As(err, &te)
}

type textError struct {
	key   string
	pairs []any
	err   error
}

func (e *textError) Error() string {
	if e.err == nil {
		return T(e.key, e.pairs...)
	}
	return T(e.key, append(e.pairs[:len(e.pairs):len(e.pairs)], "error", e.err)...)
}

func (e *textError) Unwrap() error { return e.err }

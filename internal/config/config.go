// Package config reads and writes the settings shared with the Python app.
//
// The file lives next to the Python app's config.json so existing keys keep
// working. Only the "keys" list and "gui.mode" are interpreted here; every
// other top-level field is kept byte-for-byte when the file is written back.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/jseook11/nalmeok/internal/i18n"
	"time"
)

// DefaultFreeMinutes is the Python app's default per-key limit.
const DefaultFreeMinutes = 130

// Key is one saved API key. The JSON names match the Python app.
type Key struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Tier         string `json:"tier"`
	APIKey       string `json:"api_key"`
	LimitMinutes int    `json:"limit_minutes"`
}

// Config is the parsed config.json. Unknown top-level fields survive a save.
type Config struct {
	path string
	raw  map[string]json.RawMessage
	keys []Key
	mode string
	// keyExtra holds fields of a key entry this app does not use, by key ID;
	// keyRest holds entries that could not be read as keys. Both are written
	// back on save so nothing written by another version is lost.
	keyExtra map[string]map[string]json.RawMessage
	keyRest  []json.RawMessage
}

// Dir returns the application's config directory:
// $XDG_CONFIG_HOME/nalmeok, %APPDATA%\nalmeok on Windows, or
// ~/.config/nalmeok everywhere else.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return configDir(os.Getenv("XDG_CONFIG_HOME"), os.Getenv("APPDATA"), home, runtime.GOOS == "windows")
}

func configDir(xdg, appdata, home string, windows bool) (string, error) {
	base := strings.TrimSpace(xdg)
	if base == "" && windows {
		base = strings.TrimSpace(appdata)
	}
	if base == "" {
		if strings.TrimSpace(home) == "" {
			return "", i18n.Error("error.noConfigDir")
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "nalmeok"), nil
}

// ConfigPath returns <Dir>/config.json.
func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// ItemsPath returns <Dir>/items.json.
func ItemsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "items.json"), nil
}

// WorkDir returns <Dir>/work.
func WorkDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "work"), nil
}

// LogPath returns <Dir>/nalmeok.log.
func LogPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "nalmeok.log"), nil
}

// Load reads the default config file. A missing or unreadable file yields an
// empty configuration, matching the Python app's forgiving behaviour.
func Load() (*Config, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(path)
}

// LoadFrom reads path. Unknown top-level fields are retained for Save.
func LoadFrom(path string) (*Config, error) {
	c := &Config{
		path: path,
		raw:  make(map[string]json.RawMessage),
		keys: []Key{},
		mode: "free",
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
		return c, nil
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return c, nil
	}
	c.raw = raw
	c.mode = parseMode(raw["gui"])

	if _, ok := raw["keys"]; ok {
		c.keys, c.keyExtra, c.keyRest = parseKeys(raw["keys"])
	} else if legacy := legacyKey(raw["api_key"]); legacy != nil {
		// The Python app read a single top-level api_key as a paid key.
		c.keys = []Key{*legacy}
	}
	return c, nil
}

func legacyKey(raw json.RawMessage) *Key {
	var key string
	if err := json.Unmarshal(raw, &key); err != nil {
		return nil
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	return &Key{
		ID:           "legacy",
		Label:        i18n.T("key.defaultLabel"),
		Tier:         "paid",
		APIKey:       key,
		LimitMinutes: DefaultFreeMinutes,
	}
}

func parseMode(raw json.RawMessage) string {
	var gui map[string]json.RawMessage
	if err := json.Unmarshal(raw, &gui); err != nil {
		return "free"
	}
	if text(gui["mode"]) == "batch" {
		return "batch"
	}
	return "free"
}

// keyFields are the key fields this app reads; anything else in a key entry
// is kept as it was.
var keyFields = []string{"id", "label", "tier", "api_key", "limit_minutes"}

// parseKeys reads the "keys" list. It is forgiving about value types (a
// number written as a string, a label written as a number) because a key
// that fails to parse would otherwise vanish on the next save. Entries that
// still cannot be used are returned in rest and written back unchanged.
func parseKeys(raw json.RawMessage) (keys []Key, extra map[string]map[string]json.RawMessage, rest []json.RawMessage) {
	keys = []Key{}
	extra = map[string]map[string]json.RawMessage{}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return keys, extra, nil
	}

	for i, entry := range entries {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(entry, &obj); err != nil || obj == nil {
			rest = append(rest, entry)
			continue
		}
		apiKey := strings.TrimSpace(text(obj["api_key"]))
		if apiKey == "" {
			rest = append(rest, entry)
			continue
		}

		label := strings.TrimSpace(text(obj["label"]))
		if label == "" {
			label = fmt.Sprintf("key%d", i+1)
		}
		id := strings.TrimSpace(text(obj["id"]))
		if id == "" {
			id = fmt.Sprintf("key_%03d", i+1)
		}
		limit := DefaultFreeMinutes
		if n, ok := number(obj["limit_minutes"]); ok && n != 0 {
			limit = n
		}
		keys = append(keys, Key{
			ID:           id,
			Label:        label,
			Tier:         normalizeTier(text(obj["tier"])),
			APIKey:       apiKey,
			LimitMinutes: clampMinutes(limit),
		})
		for _, name := range keyFields {
			delete(obj, name)
		}
		if len(obj) > 0 {
			extra[id] = obj
		}
	}
	return keys, extra, rest
}

// text reads a JSON string, or the literal text of a number or boolean.
func text(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var v any
	if err := json.Unmarshal(raw, &v); err == nil {
		switch v.(type) {
		case float64, bool:
			return strings.TrimSpace(string(raw))
		}
	}
	return ""
}

// number reads a JSON number or a numeric string, rounded to an int.
func number(raw json.RawMessage) (int, bool) {
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int(math.Round(f)), true
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(text(raw)), 64); err == nil {
		return int(math.Round(f)), true
	}
	return 0, false
}

func normalizeTier(tier string) string {
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "free":
		return "free"
	default:
		return "paid"
	}
}

func clampMinutes(minutes int) int {
	if minutes < 1 {
		return 1
	}
	if minutes > 24*60 {
		return 24 * 60
	}
	return minutes
}

// Mode returns "free" or "batch".
func (c *Config) Mode() string {
	if c.mode == "batch" {
		return "batch"
	}
	return "free"
}

// SetMode stores and immediately persists the processing mode.
func (c *Config) SetMode(mode string) error {
	if mode != "free" && mode != "batch" {
		return i18n.Error("error.unknownMode", "mode", mode)
	}
	c.reload()
	previous := c.mode
	c.mode = mode
	if err := c.Save(); err != nil {
		c.mode = previous
		return err
	}
	return nil
}

// Pane reports whether the transcript pane is shown (gui.pane).
func (c *Config) Pane() bool {
	var gui map[string]json.RawMessage
	if err := json.Unmarshal(c.raw["gui"], &gui); err != nil {
		return false
	}
	var open bool
	if err := json.Unmarshal(gui["pane"], &open); err != nil {
		return false
	}
	return open
}

// SetPane stores whether the transcript pane is shown. Other gui fields are
// kept as they are.
func (c *Config) SetPane(open bool) error {
	c.reload()
	var gui map[string]json.RawMessage
	_ = json.Unmarshal(c.raw["gui"], &gui)
	if gui == nil {
		gui = make(map[string]json.RawMessage)
	}
	value, err := json.Marshal(open)
	if err != nil {
		return err
	}
	previous, had := c.raw["gui"]
	gui["pane"] = value
	data, err := json.Marshal(gui)
	if err != nil {
		return err
	}
	c.raw["gui"] = data
	if err := c.Save(); err != nil {
		if had {
			c.raw["gui"] = previous
		} else {
			delete(c.raw, "gui")
		}
		return err
	}
	return nil
}

// Tiers reports which processing tiers have at least one key.
func (c *Config) Tiers() (free, paid bool) {
	for _, key := range c.AllKeys() {
		if key.Tier == "free" {
			free = true
		} else {
			paid = true
		}
	}
	return free, paid
}

// EffectiveMode returns the mode the next item will use. When only one tier
// has keys that tier wins; when both or neither do, the stored preference
// stands. The stored preference is never changed here, so adding the other
// tier later restores the user's last choice.
func (c *Config) EffectiveMode() string {
	free, paid := c.Tiers()
	switch {
	case free && !paid:
		return "free"
	case paid && !free:
		return "batch"
	default:
		return c.Mode()
	}
}

// SavedKeys returns a copy of the keys stored in config.json, without the
// read-only environment key.
func (c *Config) SavedKeys() []Key {
	out := make([]Key, len(c.keys))
	copy(out, c.keys)
	return out
}

// AllKeys returns the environment key first, then saved keys in priority
// order, with duplicate API keys removed. It mirrors nalmeok/keys.py. The
// environment key cannot be reordered, so it always comes first within its
// tier.
func (c *Config) AllKeys() []Key {
	seen := make(map[string]bool)
	out := make([]Key, 0, len(c.keys)+1)

	if env := EnvKey(); env != nil {
		out = append(out, *env)
		seen[env.APIKey] = true
	}
	for _, key := range c.keys {
		if seen[key.APIKey] {
			continue
		}
		seen[key.APIKey] = true
		out = append(out, key)
	}
	return out
}

// EnvKey returns the read-only GEMINI_API_KEY entry, if configured.
func EnvKey() *Key {
	apiKey := strings.TrimSpace(os.Getenv("GEMINI_API_KEY"))
	if apiKey == "" {
		return nil
	}
	tier := "paid"
	if strings.EqualFold(strings.TrimSpace(os.Getenv("GEMINI_API_KEY_TIER")), "free") {
		tier = "free"
	}
	return &Key{
		ID:           "env",
		Label:        i18n.T("key.envLabel"),
		Tier:         tier,
		APIKey:       apiKey,
		LimitMinutes: DefaultFreeMinutes,
	}
}

// AddKey adds a key, or updates label/tier when the same API key already
// exists. An empty label falls back to the tier name, like the Python app.
func (c *Config) AddKey(label, tier, apiKey string) (Key, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return Key{}, i18n.Error("error.emptyKey")
	}
	tier = normalizeTier(tier)
	label = strings.TrimSpace(label)
	if label == "" {
		label = tier
	}

	c.reload()
	previous := make([]Key, len(c.keys))
	copy(previous, c.keys)

	for i := range c.keys {
		if c.keys[i].APIKey != apiKey {
			continue
		}
		c.keys[i].Label = label
		c.keys[i].Tier = tier
		if err := c.Save(); err != nil {
			c.keys = previous
			return Key{}, err
		}
		return c.keys[i], nil
	}

	label = c.uniqueLabel(label)
	key := Key{
		ID:           newKeyID(),
		Label:        label,
		Tier:         tier,
		APIKey:       apiKey,
		LimitMinutes: DefaultFreeMinutes,
	}
	c.keys = append(c.keys, key)
	if err := c.Save(); err != nil {
		c.keys = previous
		return Key{}, err
	}
	return key, nil
}

func (c *Config) uniqueLabel(label string) string {
	return c.uniqueLabelExcept(label, "")
}

// uniqueLabelExcept numbers label ("Free2") when another saved key already
// uses it. The key with ID except is ignored, so renaming a key to its own
// label keeps it.
func (c *Config) uniqueLabelExcept(label, except string) string {
	labels := make(map[string]bool, len(c.keys))
	for _, key := range c.keys {
		if except != "" && key.ID == except {
			continue
		}
		labels[key.Label] = true
	}
	if !labels[label] {
		return label
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s%d", label, i)
		if !labels[candidate] {
			return candidate
		}
	}
}

// Errors returned by the key editing methods.
var (
	ErrKeyNotFound = i18n.Error("error.noKey")
	ErrEnvKey      = i18n.Error("error.envKeyHere")
	errKeyOrder    = i18n.Error("error.keyOrder")
)

// isEnvID reports whether id names the read-only GEMINI_API_KEY entry rather
// than a saved key.
func (c *Config) isEnvID(id string) bool {
	return id == "env" && EnvKey() != nil && c.savedIndex(id) < 0
}

func (c *Config) savedIndex(id string) int {
	for i := range c.keys {
		if c.keys[i].ID == id {
			return i
		}
	}
	return -1
}

// editKey applies fn to the saved key id and persists, restoring the previous
// keys when the write fails.
func (c *Config) editKey(id string, fn func(*Key)) (Key, error) {
	if c.isEnvID(id) {
		return Key{}, ErrEnvKey
	}
	c.reload()
	index := c.savedIndex(id)
	if index < 0 {
		return Key{}, ErrKeyNotFound
	}
	previous := make([]Key, len(c.keys))
	copy(previous, c.keys)
	fn(&c.keys[index])
	if err := c.Save(); err != nil {
		c.keys = previous
		return Key{}, err
	}
	return c.keys[index], nil
}

// RenameKey changes a saved key's label. The label is trimmed and numbered
// when another key already uses it.
func (c *Config) RenameKey(id, label string) (Key, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return Key{}, i18n.Error("error.nameRequired")
	}
	return c.editKey(id, func(k *Key) {
		k.Label = c.uniqueLabelExcept(label, id)
	})
}

// SetKeyTier changes a saved key's tier to "free" or "paid". The processing
// mode is not stored here; EffectiveMode follows the new set of tiers.
func (c *Config) SetKeyTier(id, tier string) (Key, error) {
	tier = strings.ToLower(strings.TrimSpace(tier))
	if tier != "free" && tier != "paid" {
		return Key{}, i18n.Error("error.badTier")
	}
	return c.editKey(id, func(k *Key) { k.Tier = tier })
}

// SetKeyOrder reorders the saved keys. The order of "keys" in config.json is
// the priority: free work starts on the first usable free key and paid Batch
// uses the first paid key. ids must name every saved key exactly once; the
// environment key is not part of the list.
func (c *Config) SetKeyOrder(ids []string) error {
	for _, id := range ids {
		if c.isEnvID(id) {
			return ErrEnvKey
		}
	}
	c.reload()
	if len(ids) != len(c.keys) {
		return errKeyOrder
	}
	ordered := make([]Key, 0, len(ids))
	used := make(map[string]bool, len(ids))
	for _, id := range ids {
		index := c.savedIndex(id)
		if index < 0 || used[id] {
			return errKeyOrder
		}
		used[id] = true
		ordered = append(ordered, c.keys[index])
	}
	previous := c.keys
	c.keys = ordered
	if err := c.Save(); err != nil {
		c.keys = previous
		return err
	}
	return nil
}

// DeleteKey removes a saved key by ID. It reports whether a key was removed.
func (c *Config) DeleteKey(id string) (bool, error) {
	c.reload()
	previous := make([]Key, len(c.keys))
	copy(previous, c.keys)

	remaining := make([]Key, 0, len(c.keys))
	for _, key := range c.keys {
		if key.ID != id {
			remaining = append(remaining, key)
		}
	}
	if len(remaining) == len(c.keys) {
		return false, nil
	}
	c.keys = remaining
	if err := c.Save(); err != nil {
		c.keys = previous
		return false, err
	}
	return true, nil
}

// reload picks up what another process (the CLI, the Python app) wrote since
// this copy was read, so the change about to be saved does not erase it.
// Every editing method calls it right before changing and saving. A missing
// or unreadable file keeps the copy in memory.
func (c *Config) reload() {
	if c.path == "" {
		return
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		return
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return
	}
	fresh, err := LoadFrom(c.path)
	if err != nil {
		return
	}
	c.raw, c.keys, c.mode, c.keyExtra, c.keyRest = fresh.raw, fresh.keys, fresh.mode, fresh.keyExtra, fresh.keyRest
}

// Save writes the config to its original path with mode 0600.
func (c *Config) Save() error {
	if c.path == "" {
		path, err := ConfigPath()
		if err != nil {
			return err
		}
		c.path = path
	}
	return c.SaveTo(c.path)
}

// SaveTo writes the config to path atomically. Unknown top-level fields are
// preserved; the legacy top-level api_key is folded into keys.
func (c *Config) SaveTo(path string) error {
	raw := make(map[string]json.RawMessage, len(c.raw)+2)
	for name, value := range c.raw {
		raw[name] = value
	}

	keysJSON, err := c.keysJSON()
	if err != nil {
		return err
	}
	raw["keys"] = keysJSON
	delete(raw, "api_key")

	var gui map[string]json.RawMessage
	if existing, ok := raw["gui"]; ok {
		_ = json.Unmarshal(existing, &gui)
	}
	if gui == nil {
		gui = make(map[string]json.RawMessage)
	}
	modeJSON, err := json.Marshal(c.Mode())
	if err != nil {
		return err
	}
	gui["mode"] = modeJSON
	guiJSON, err := json.Marshal(gui)
	if err != nil {
		return err
	}
	raw["gui"] = guiJSON

	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(path, data, 0o600)
}

// keysJSON is the "keys" list: saved keys with their unknown fields, then the
// entries that could not be read.
func (c *Config) keysJSON() ([]byte, error) {
	entries := make([]json.RawMessage, 0, len(c.keys)+len(c.keyRest))
	for _, key := range c.keys {
		data, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		if extra := c.keyExtra[key.ID]; len(extra) > 0 {
			var obj map[string]json.RawMessage
			if err := json.Unmarshal(data, &obj); err != nil {
				return nil, err
			}
			for name, value := range extra {
				if _, ok := obj[name]; !ok {
					obj[name] = value
				}
			}
			if data, err = json.Marshal(obj); err != nil {
				return nil, err
			}
		}
		entries = append(entries, data)
	}
	entries = append(entries, c.keyRest...)
	return json.Marshal(entries)
}

func newKeyID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("key_%x", time.Now().UnixNano())
	}
	return "key_" + hex.EncodeToString(buf[:])
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".nalmeok-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if err := os.Rename(tmpName, path); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
	}
	_ = os.Remove(tmpName)
	return lastErr
}

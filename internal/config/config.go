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
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
			return "", errors.New("설정 폴더를 찾지 못했어요")
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
		c.keys = parseKeys(raw["keys"])
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
		Label:        "기본",
		Tier:         "paid",
		APIKey:       key,
		LimitMinutes: DefaultFreeMinutes,
	}
}

func parseMode(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "free"
	}
	var gui struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(raw, &gui); err != nil {
		return "free"
	}
	if gui.Mode == "batch" {
		return "batch"
	}
	return "free"
}

func parseKeys(raw json.RawMessage) []Key {
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return []Key{}
	}

	keys := make([]Key, 0, len(entries))
	for i, entry := range entries {
		var obj struct {
			ID           string `json:"id"`
			Label        string `json:"label"`
			Tier         string `json:"tier"`
			APIKey       string `json:"api_key"`
			LimitMinutes *int   `json:"limit_minutes"`
		}
		if err := json.Unmarshal(entry, &obj); err != nil {
			continue
		}
		apiKey := strings.TrimSpace(obj.APIKey)
		if apiKey == "" {
			continue
		}

		label := strings.TrimSpace(obj.Label)
		if label == "" {
			label = fmt.Sprintf("key%d", i+1)
		}
		id := strings.TrimSpace(obj.ID)
		if id == "" {
			id = fmt.Sprintf("key_%03d", i+1)
		}
		limit := DefaultFreeMinutes
		if obj.LimitMinutes != nil && *obj.LimitMinutes != 0 {
			limit = *obj.LimitMinutes
		}
		keys = append(keys, Key{
			ID:           id,
			Label:        label,
			Tier:         normalizeTier(obj.Tier),
			APIKey:       apiKey,
			LimitMinutes: clampMinutes(limit),
		})
	}
	return keys
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
		return fmt.Errorf("알 수 없는 처리 방식이에요: %s", mode)
	}
	previous := c.mode
	c.mode = mode
	if err := c.Save(); err != nil {
		c.mode = previous
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
		Label:        "환경변수",
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
		return Key{}, errors.New("API 키가 비어 있어요")
	}
	tier = normalizeTier(tier)
	label = strings.TrimSpace(label)
	if label == "" {
		label = tier
	}

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

// uniqueLabelExcept numbers label ("무료2") when another saved key already
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
	ErrKeyNotFound = errors.New("키가 없어요")
	ErrEnvKey      = errors.New("환경변수 키는 여기서 바꿀 수 없어요")
	errKeyOrder    = errors.New("키 목록이 맞지 않아요")
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
		return Key{}, errors.New("이름을 입력하세요")
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
		return Key{}, errors.New("등급이 올바르지 않아요")
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

	keys := c.keys
	if keys == nil {
		keys = []Key{}
	}
	keysJSON, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	raw["keys"] = keysJSON
	delete(raw, "api_key")

	gui := make(map[string]json.RawMessage)
	if existing, ok := raw["gui"]; ok {
		_ = json.Unmarshal(existing, &gui)
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

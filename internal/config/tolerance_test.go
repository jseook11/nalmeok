package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// savedKeyEntries returns the saved "keys" entries, each re-marshalled
// compactly so tests can compare them as strings.
func savedKeyEntries(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Keys []any `json:"keys"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("saved config is not valid JSON: %v\n%s", err, data)
	}
	out := make([]string, 0, len(file.Keys))
	for _, k := range file.Keys {
		b, _ := json.Marshal(k)
		out = append(out, string(b))
	}
	return out
}

// A value written as a string where a number is expected must not make the
// key disappear on the next save.
func TestNumericStringLimitKeepsKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	path := writeConfig(t, `{"keys": [
  {"id": "k1", "label": "무료", "tier": "free", "api_key": "fake-1", "limit_minutes": "90"},
  {"id": "k2", "label": "유료", "tier": "paid", "api_key": "fake-2", "limit_minutes": 130.0}
]}`)
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	keys := cfg.SavedKeys()
	if len(keys) != 2 {
		t.Fatalf("SavedKeys = %+v, want 2", keys)
	}
	if keys[0].LimitMinutes != 90 || keys[1].LimitMinutes != 130 {
		t.Fatalf("limits = %d, %d", keys[0].LimitMinutes, keys[1].LimitMinutes)
	}
	if err := cfg.SetMode("batch"); err != nil {
		t.Fatal(err)
	}
	if got := savedKeyEntries(t, path); len(got) != 2 {
		t.Fatalf("saved keys = %v, want 2", got)
	}
}

// Entries that cannot be read as keys, and unknown fields inside a key, are
// written back as they were.
func TestUnreadableKeyEntriesSurviveSave(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	path := writeConfig(t, `{"keys": [
  {"id": "k1", "label": "무료", "tier": "free", "api_key": "fake-1", "note": {"from": "python"}},
  {"id": "k2", "label": ["이상한"], "api_key": ""},
  "not an object"
]}`)
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(cfg.SavedKeys()); got != 1 {
		t.Fatalf("SavedKeys = %d, want 1", got)
	}
	if _, err := cfg.AddKey("새", "paid", "fake-3"); err != nil {
		t.Fatal(err)
	}
	entries := savedKeyEntries(t, path)
	if len(entries) != 4 {
		t.Fatalf("saved %d key entries, want 4: %v", len(entries), entries)
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(entries[0]), &first); err != nil {
		t.Fatal(err)
	}
	if note, _ := json.Marshal(first["note"]); string(note) != `{"from":"python"}` {
		t.Fatalf("unknown key field = %s", note)
	}
	found := map[string]bool{}
	for _, e := range entries {
		found[e] = true
	}
	if !found[`"not an object"`] || !found[`{"api_key":"","id":"k2","label":["이상한"]}`] {
		t.Fatalf("unreadable entries dropped: %v", entries)
	}

	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reloaded.SavedKeys()); got != 2 {
		t.Fatalf("reloaded keys = %d, want 2", got)
	}
}

func TestNullGUISaves(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	path := writeConfig(t, `{"gui": null, "keys": null}`)
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetMode("batch"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Mode() != "batch" {
		t.Fatalf("mode = %q", reloaded.Mode())
	}
}

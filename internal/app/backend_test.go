package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/store"
)

// A config.json and items.json written while a second transcription backend
// existed still load, and everything runs on Gemini keys as usual.
func TestRetiredBackendSettingsBehaveAsGemini(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY_TIER", "")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{
  "backend": "codex",
  "experiments": {"codex": true},
  "gui": {"mode": "free"},
  "keys": [{"id": "p1", "label": "Paid 1", "tier": "paid", "api_key": "AIza-paid"}]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Load(filepath.Join(dir, "items.json"))
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg, st, NewStubRunner(st))

	// Only a paid key: the free lane is not available, so new items go to
	// Batch as on any Gemini setup.
	state := a.ModeState()
	if state.Free || !state.Paid || state.Mode != "batch" {
		t.Fatalf("ModeState = %+v", state)
	}

	// An item stored with the old backend name takes a key like any other.
	if err := st.Add(store.Item{ID: "old", Name: "a.m4a", Mode: "free", Backend: "codex", State: "failed"}); err != nil {
		t.Fatal(err)
	}
	if err := a.SetItemKey("old", "p1"); err != nil {
		t.Fatalf("SetItemKey: %v", err)
	}
	item, _ := st.Get("old")
	if item.KeyID != "p1" || item.Mode != "batch" {
		t.Fatalf("item = %+v", item)
	}
}

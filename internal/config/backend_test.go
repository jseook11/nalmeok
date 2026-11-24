package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// A config.json written while a second transcription backend existed still
// loads, and the next save drops the retired backend settings while keeping
// everything else.
func TestRetiredBackendFieldsLoadAndAreDropped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
  "gui": {"mode": "batch", "pane": true},
  "experiments": {"codex": true},
  "backend": "codex",
  "future": 1
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Mode() != "batch" || !c.Pane() {
		t.Fatalf("gui fields: mode=%q pane=%v", c.Mode(), c.Pane())
	}
	if err := c.SetPane(false); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"backend", "experiments"} {
		if _, ok := raw[name]; ok {
			t.Fatalf("%q written back: %s", name, data)
		}
	}
	if _, ok := raw["future"]; !ok {
		t.Fatalf("unknown field lost: %s", data)
	}
	again, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.Mode() != "batch" || again.Pane() {
		t.Fatalf("after save: mode=%q pane=%v", again.Mode(), again.Pane())
	}
}

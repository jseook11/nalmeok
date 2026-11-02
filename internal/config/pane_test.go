package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPaneIsSavedNextToMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"gui":{"mode":"batch","other":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Pane() {
		t.Fatal("pane must start hidden")
	}
	if err := c.SetPane(true); err != nil {
		t.Fatal(err)
	}
	again, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Pane() || again.Mode() != "batch" {
		t.Fatalf("Pane = %v, Mode = %q", again.Pane(), again.Mode())
	}
	data, _ := os.ReadFile(path)
	var raw struct {
		GUI map[string]json.RawMessage `json:"gui"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw.GUI["other"]) != "1" {
		t.Fatalf("unknown gui field lost: %s", data)
	}
}

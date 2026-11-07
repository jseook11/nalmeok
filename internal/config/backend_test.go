package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBackendDefaultsToGemini(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Backend(); got != BackendGemini {
		t.Fatalf("Backend = %q, want %q", got, BackendGemini)
	}
}

func TestSetBackendPersistsAndKeepsOtherFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
  "gui": {"mode": "batch", "pane": true},
  "experiments": {"codex": true},
  "backend": "gemini"
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Experiment("codex") {
		t.Fatal("experiments.codex not read")
	}
	if err := c.SetBackend(BackendCodex); err != nil {
		t.Fatal(err)
	}
	again, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Backend(); got != BackendCodex {
		t.Fatalf("Backend = %q, want %q", got, BackendCodex)
	}
	if again.Mode() != "batch" || !again.Pane() {
		t.Fatalf("other gui fields changed: mode=%q pane=%v", again.Mode(), again.Pane())
	}
	if !again.Experiment("codex") {
		t.Fatal("experiments flag lost")
	}
}

func TestSetBackendRejectsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetBackend("anthropic"); err == nil {
		t.Fatal("SetBackend accepted an unknown backend")
	}
}

func TestUnknownStoredBackendIsGemini(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"backend":"anthropic"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Backend(); got != BackendGemini {
		t.Fatalf("Backend = %q, want %q", got, BackendGemini)
	}
}

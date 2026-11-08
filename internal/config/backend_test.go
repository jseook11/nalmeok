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

func TestSetExperimentPersistsAndKeepsOtherFlags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
  "experiments": {"other": true},
  "backend": "codex",
  "gui": {"mode": "batch"}
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetExperiment("codex", true); err != nil {
		t.Fatal(err)
	}
	again, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Experiment("codex") || !again.Experiment("other") {
		t.Fatal("experiments lost")
	}
	if again.Backend() != BackendCodex || again.Mode() != "batch" {
		t.Fatalf("other fields changed: backend=%q mode=%q", again.Backend(), again.Mode())
	}
	if err := again.SetExperiment("codex", false); err != nil {
		t.Fatal(err)
	}
	off, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if off.Experiment("codex") || !off.Experiment("other") {
		t.Fatal("turning codex off changed the other flag")
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

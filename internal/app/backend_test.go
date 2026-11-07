package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jseook11/nalmeok/internal/codexasr"
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
)

func validCodexAuth() (codexasr.Auth, error) {
	return codexasr.Auth{AccessToken: "token", Path: "/tmp/auth.json"}, nil
}

func expiredCodexAuth() (codexasr.Auth, error) {
	// A JWT payload with exp=1. The signature is not verified.
	return codexasr.Auth{AccessToken: "a.eyJleHAiOjF9.c", Path: "/tmp/auth.json"}, nil
}

func TestModeStateDefaultsToGemini(t *testing.T) {
	ta := newTestApp(t)
	state := ta.app.ModeState()
	if state.Backend != config.BackendGemini || state.Experiment || state.Codex != "" {
		t.Fatalf("ModeState = %+v", state)
	}
}

func TestModeStateCodexUsesLogin(t *testing.T) {
	ta := newTestApp(t)
	ta.app.CodexAuth = validCodexAuth
	ta.app.Experiment = true
	if err := ta.app.cfg.SetBackend(config.BackendCodex); err != nil {
		t.Fatal(err)
	}
	state := ta.app.ModeState()
	if state.Backend != config.BackendCodex || state.Mode != "free" || !state.Free {
		t.Fatalf("ModeState = %+v", state)
	}
	if state.Codex != "ok" || state.CodexPath != "/tmp/auth.json" || !state.Experiment {
		t.Fatalf("codex state = %+v", state)
	}
	if got := ta.app.GetMode(); got != "free" {
		t.Fatalf("GetMode = %q, want free", got)
	}
	// A stored Batch preference does not apply to the Codex free lane.
	if err := ta.app.cfg.SetMode("batch"); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.GetMode(); got != "free" {
		t.Fatalf("GetMode = %q, want free", got)
	}
}

func TestModeStateCodexExpired(t *testing.T) {
	ta := newTestApp(t)
	ta.app.CodexAuth = expiredCodexAuth
	ta.app.Experiment = true
	if err := ta.app.cfg.SetBackend(config.BackendCodex); err != nil {
		t.Fatal(err)
	}
	state := ta.app.ModeState()
	if state.Free || state.Codex != "expired" {
		t.Fatalf("ModeState = %+v", state)
	}
}

func TestCodexKeepsPaidMode(t *testing.T) {
	ta := newTestApp(t)
	ta.app.CodexAuth = validCodexAuth
	ta.app.Experiment = true
	if _, err := ta.app.cfg.AddKey("Paid 1", "paid", "AIza-paid"); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.cfg.SetBackend(config.BackendCodex); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.SetMode("batch"); err != nil {
		t.Fatal(err)
	}
	state := ta.app.ModeState()
	if !state.Free || !state.Paid || state.Mode != "batch" {
		t.Fatalf("ModeState = %+v", state)
	}
	if got := ta.app.GetMode(); got != "batch" {
		t.Fatalf("GetMode = %q, want batch", got)
	}
}

func TestCodexLoadsStoredPaidMode(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{
  "backend": "codex",
  "experiments": {"codex": true},
  "gui": {"mode": "batch"},
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
	a.CodexAuth = validCodexAuth
	a.Experiment = true
	state := a.ModeState()
	if !state.Free || !state.Paid || state.Mode != "batch" {
		t.Fatalf("ModeState = %+v", state)
	}
}

func TestSetBackendWritesConfig(t *testing.T) {
	ta := newTestApp(t)
	if err := ta.app.SetBackend(config.BackendCodex); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.Backend(); got != config.BackendCodex {
		t.Fatalf("Backend = %q", got)
	}
	again, err := config.LoadFrom(ta.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Backend(); got != config.BackendCodex {
		t.Fatalf("stored Backend = %q", got)
	}
}

func TestSetBackendHonorsForced(t *testing.T) {
	ta := newTestApp(t)
	ta.app.BackendForced = true
	err := ta.app.SetBackend(config.BackendCodex)
	if err == nil || !strings.Contains(err.Error(), i18n.T("error.backendForced")) {
		t.Fatalf("err = %v", err)
	}
}

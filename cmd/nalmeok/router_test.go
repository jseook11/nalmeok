package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jseook11/nalmeok/internal/codexasr"
	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/runner"
	"github.com/jseook11/nalmeok/internal/store"
)

func TestRouterSendsNewItemsToCurrentBackend(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Load(filepath.Join(dir, "items.json"))
	if err != nil {
		t.Fatal(err)
	}
	gemini := runner.New(runner.Options{
		Store: st,
		Keys: func() []config.Key {
			return []config.Key{
				{ID: "free-1", Label: "free", Tier: "free", APIKey: "AIza-free"},
				{ID: "paid-1", Label: "paid", Tier: "paid", APIKey: "AIza-paid"},
			}
		},
	})
	t.Cleanup(func() { _ = gemini.Close() })
	codex := runner.New(runner.Options{
		Store:   st,
		Backend: runner.BackendCodex,
		Codex: runner.CodexOptions{
			Auth: func() (codexasr.Auth, error) {
				return codexasr.Auth{AccessToken: "token"}, nil
			},
		},
	})
	t.Cleanup(func() { _ = codex.Close() })

	backend := config.BackendGemini
	router := &backendRouter{
		gemini:  gemini,
		codex:   codex,
		store:   st,
		current: func() string { return backend },
	}

	geminiPath := filepath.Join(dir, "gemini.m4a")
	codexPath := filepath.Join(dir, "codex.m4a")
	batchPath := filepath.Join(dir, "batch.m4a")
	for _, path := range []string{geminiPath, codexPath, batchPath} {
		if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := router.Add([]string{geminiPath}, "free", ""); err != nil {
		t.Fatalf("Add gemini: %v", err)
	}
	backend = config.BackendCodex
	if err := router.Add([]string{codexPath}, "free", ""); err != nil {
		t.Fatalf("Add codex: %v", err)
	}
	// Batch is Gemini-only even while Codex is the free backend.
	if err := router.Add([]string{batchPath}, "batch", ""); err != nil {
		t.Fatalf("Add batch: %v", err)
	}

	items := st.List("")
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	for _, item := range items {
		want := config.BackendGemini
		if item.Source == codexPath {
			want = config.BackendCodex
		}
		if item.Backend != want {
			t.Fatalf("%s backend = %q, want %q", item.Source, item.Backend, want)
		}
		if item.Source == batchPath && item.Mode != "batch" {
			t.Fatalf("batch item mode = %q, want batch", item.Mode)
		}
	}
}

func TestRouterRoutesExistingItemByBackend(t *testing.T) {
	st, err := store.Load(filepath.Join(t.TempDir(), "items.json"))
	if err != nil {
		t.Fatal(err)
	}
	gemini := runner.New(runner.Options{Store: st})
	t.Cleanup(func() { _ = gemini.Close() })
	codex := runner.New(runner.Options{Store: st, Backend: runner.BackendCodex})
	t.Cleanup(func() { _ = codex.Close() })
	router := &backendRouter{
		gemini:  gemini,
		codex:   codex,
		store:   st,
		current: func() string { return config.BackendGemini },
	}

	for _, item := range []store.Item{
		{ID: "gemini-item", Name: "g.m4a", Backend: ""},
		{ID: "codex-item", Name: "c.m4a", Backend: config.BackendCodex},
	} {
		if err := st.Add(item); err != nil {
			t.Fatal(err)
		}
	}

	if got, err := router.runnerFor("gemini-item"); err != nil || got != gemini {
		t.Fatalf("gemini-item runner = %v, err = %v", got, err)
	}
	if got, err := router.runnerFor("codex-item"); err != nil || got != codex {
		t.Fatalf("codex-item runner = %v, err = %v", got, err)
	}
	if _, err := router.runnerFor("missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing err = %v", err)
	}
}

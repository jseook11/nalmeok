package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jseook11/nalmeok/internal/config"
)

// While another process (the app) holds the config folder, a CLI run stops
// before touching items.json, so no item is resumed or added twice.
func TestCLIRefusesWhileFolderLocked(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("GEMINI_API_KEY", "fake-key-for-test")
	held, err := config.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	src := filepath.Join(dir, "lec.m4a")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runCLI([]string{"--batch", src}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "nalmeok", "items.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("items.json was touched: %v", err)
	}
}

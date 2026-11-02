package config

import (
	"errors"
	"path/filepath"
	"testing"
)

// Another process (the CLI, the old Python app) may write config.json while
// this one holds an older copy. A change here must not drop theirs.
func TestSaveKeepsChangesFromAnotherWriter(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	path := writeConfig(t, `{"keys": []}`)
	app, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	other, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.AddKey("다른 곳", "free", "fake-other"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.AddKey("앱", "paid", "fake-app"); err != nil {
		t.Fatal(err)
	}
	if err := app.SetMode("batch"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reloaded.SavedKeys()); got != 2 {
		t.Fatalf("saved keys = %d, want 2 (one from each writer): %+v", got, reloaded.SavedKeys())
	}
	if got := len(app.SavedKeys()); got != 2 {
		t.Fatalf("app sees %d keys, want 2", got)
	}
}

func TestLockIsExclusive(t *testing.T) {
	dir := t.TempDir()
	first, err := LockDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LockDir(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock err = %v, want ErrLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := LockDir(dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	_ = again.Close()
	nested, err := LockDir(filepath.Join(dir, "new", "sub"))
	if err != nil {
		t.Fatalf("lock creates its folder: %v", err)
	}
	_ = nested.Close()
}

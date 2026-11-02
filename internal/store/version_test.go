package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveWritesFormatVersionAndLoadsWithoutIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "items.json")
	if err := os.WriteFile(path, []byte(`{"items": [{"ID": "a", "Name": "강의"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("a"); !ok {
		t.Fatal("item without version field was not loaded")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if file.Version == nil || *file.Version != 1 {
		t.Fatalf("version missing or wrong\n%s", data)
	}
}

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSignalsDefaultOnAndSaved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"gui":{"mode":"batch"},"future":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.SignalsEnabled() {
		t.Fatal("signals must default to on when absent")
	}
	if err := c.SetSignalsEnabled(false); err != nil {
		t.Fatal(err)
	}
	if c.SignalsEnabled() {
		t.Fatal("SignalsEnabled = true after turning off")
	}
	again, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.SignalsEnabled() || again.Mode() != "batch" {
		t.Fatalf("SignalsEnabled = %v, Mode = %q", again.SignalsEnabled(), again.Mode())
	}
	data, _ := os.ReadFile(path)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["signals"]) != "false" || string(raw["future"]) != "7" {
		t.Fatalf("saved config = %s", data)
	}
	if err := again.SetSignalsEnabled(true); err != nil {
		t.Fatal(err)
	}
	if third, _ := LoadFrom(path); !third.SignalsEnabled() {
		t.Fatal("signals not turned back on")
	}
}

func TestSignalsKeepsOtherProcessChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	a, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SetMode("batch"); err != nil {
		t.Fatal(err)
	}
	if err := a.SetSignalsEnabled(false); err != nil {
		t.Fatal(err)
	}
	again, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.SignalsEnabled() || again.Mode() != "batch" {
		t.Fatalf("SignalsEnabled = %v, Mode = %q", again.SignalsEnabled(), again.Mode())
	}
}

func TestSignalsUnreadableValueCountsAsOn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"signals":"maybe"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.SignalsEnabled() {
		t.Fatal("unreadable signals value must count as on")
	}
}

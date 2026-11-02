package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jseook11/nalmeok/internal/i18n"
)

const editFixture = `{
  "keys": [
    {"id": "a", "label": "무료 1", "tier": "free", "api_key": "fake-a", "limit_minutes": 130},
    {"id": "b", "label": "무료 2", "tier": "free", "api_key": "fake-b", "limit_minutes": 130},
    {"id": "c", "label": "유료 1", "tier": "paid", "api_key": "fake-c", "limit_minutes": 130}
  ],
  "recent": ["/tmp/lecture"],
  "gui": {"mode": "free", "theme": "dark"}
}`

func loadEditFixture(t *testing.T) (*Config, string) {
	t.Helper()
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY_TIER", "")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(editFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, path
}

func ids(keys []Key) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.ID
	}
	return strings.Join(out, ",")
}

// reloadAndCheckUnknown reloads path and fails when unrelated fields were lost.
func reloadAndCheckUnknown(t *testing.T, path string) *Config {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["recent"]; !ok {
		t.Fatal("unknown field recent was dropped")
	}
	if !strings.Contains(string(raw["gui"]), "theme") {
		t.Fatalf("gui.theme was dropped: %s", raw["gui"])
	}
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestRenameKey(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		label   string
		want    string
		wantErr error
	}{
		{name: "trimmed", id: "a", label: "  집 키  ", want: "집 키"},
		{name: "same label keeps it", id: "a", label: "무료 1", want: "무료 1"},
		{name: "taken label is numbered", id: "a", label: "무료 2", want: "무료 22"},
		{name: "empty", id: "a", label: "   ", wantErr: errors.New(i18n.T("error.nameRequired"))},
		{name: "unknown id", id: "zz", label: "x", wantErr: ErrKeyNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, path := loadEditFixture(t)
			key, err := cfg.RenameKey(tt.id, tt.label)
			if tt.wantErr != nil {
				if err == nil || err.Error() != tt.wantErr.Error() {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if key.Label != tt.want {
				t.Fatalf("label = %q, want %q", key.Label, tt.want)
			}
			reloaded := reloadAndCheckUnknown(t, path)
			if got := reloaded.SavedKeys()[0].Label; got != tt.want {
				t.Fatalf("persisted label = %q", got)
			}
			if got := reloaded.SavedKeys()[0].APIKey; got != "fake-a" {
				t.Fatal("API key changed")
			}
		})
	}
}

func TestSetKeyTier(t *testing.T) {
	tests := []struct {
		name     string
		id, tier string
		wantErr  bool
		wantMode string // EffectiveMode afterwards (stored preference is free)
	}{
		{name: "free to paid", id: "a", tier: "paid", wantMode: "free"},
		{name: "paid to free leaves only free", id: "c", tier: "FREE", wantMode: "free"},
		{name: "bad tier", id: "a", tier: "gold", wantErr: true},
		{name: "unknown id", id: "zz", tier: "paid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, path := loadEditFixture(t)
			_, err := cfg.SetKeyTier(tt.id, tt.tier)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.EffectiveMode(); got != tt.wantMode {
				t.Fatalf("EffectiveMode = %q, want %q", got, tt.wantMode)
			}
			reloaded := reloadAndCheckUnknown(t, path)
			for _, k := range reloaded.SavedKeys() {
				if k.ID == tt.id && k.Tier != strings.ToLower(tt.tier) {
					t.Fatalf("persisted tier = %q", k.Tier)
				}
			}
		})
	}
}

func TestSetKeyTierOnlyPaidSwitchesMode(t *testing.T) {
	cfg, _ := loadEditFixture(t)
	for _, id := range []string{"a", "b"} {
		if _, err := cfg.SetKeyTier(id, "paid"); err != nil {
			t.Fatal(err)
		}
	}
	if got := cfg.EffectiveMode(); got != "batch" {
		t.Fatalf("EffectiveMode = %q, want batch", got)
	}
	if got := cfg.Mode(); got != "free" {
		t.Fatalf("stored preference changed to %q", got)
	}
}

func TestSetKeyOrder(t *testing.T) {
	tests := []struct {
		name    string
		ids     []string
		want    string
		wantErr bool
	}{
		{name: "reverse", ids: []string{"c", "b", "a"}, want: "c,b,a"},
		{name: "same", ids: []string{"a", "b", "c"}, want: "a,b,c"},
		{name: "missing one", ids: []string{"a", "b"}, wantErr: true},
		{name: "duplicate", ids: []string{"a", "a", "b"}, wantErr: true},
		{name: "unknown", ids: []string{"a", "b", "x"}, wantErr: true},
		{name: "extra", ids: []string{"a", "b", "c", "x"}, wantErr: true},
		{name: "empty", ids: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, path := loadEditFixture(t)
			err := cfg.SetKeyOrder(tt.ids)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if got := ids(cfg.SavedKeys()); got != "a,b,c" {
					t.Fatalf("order changed on error: %s", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := ids(cfg.AllKeys()); got != tt.want {
				t.Fatalf("AllKeys order = %s, want %s", got, tt.want)
			}
			reloaded := reloadAndCheckUnknown(t, path)
			if got := ids(reloaded.SavedKeys()); got != tt.want {
				t.Fatalf("persisted order = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestEnvKeyCannotBeEdited(t *testing.T) {
	cfg, _ := loadEditFixture(t)
	t.Setenv("GEMINI_API_KEY", "fake-env")
	if _, err := cfg.RenameKey("env", "x"); !errors.Is(err, ErrEnvKey) {
		t.Fatalf("RenameKey(env) = %v", err)
	}
	if _, err := cfg.SetKeyTier("env", "free"); !errors.Is(err, ErrEnvKey) {
		t.Fatalf("SetKeyTier(env) = %v", err)
	}
	if err := cfg.SetKeyOrder([]string{"env", "a", "b", "c"}); !errors.Is(err, ErrEnvKey) {
		t.Fatalf("SetKeyOrder(env) = %v", err)
	}
	if err := cfg.SetKeyOrder([]string{"c", "a", "b"}); err != nil {
		t.Fatal(err)
	}
	if got := ids(cfg.AllKeys()); got != "env,c,a,b" {
		t.Fatalf("AllKeys = %s, env key must stay first", got)
	}
}

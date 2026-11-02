package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestConfigDir(t *testing.T) {
	tests := []struct {
		name    string
		xdg     string
		appdata string
		home    string
		windows bool
		want    string
	}{
		{name: "xdg wins", xdg: "/x", appdata: "/a", home: "/h", want: "/x/nalmeok"},
		{name: "appdata on windows", appdata: "/a", home: "/h", windows: true, want: "/a/nalmeok"},
		{name: "home fallback on windows", home: "/h", windows: true, want: "/h/.config/nalmeok"},
		{name: "home fallback on unix", home: "/h", want: "/h/.config/nalmeok"},
		{name: "xdg wins on windows", xdg: "/x", appdata: "/a", home: "/h", windows: true, want: "/x/nalmeok"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configDir(tt.xdg, tt.appdata, tt.home, tt.windows)
			if err != nil {
				t.Fatalf("configDir: %v", err)
			}
			// The table uses '/' for readability; configDir joins with the
			// OS separator.
			if want := filepath.FromSlash(tt.want); got != want {
				t.Fatalf("configDir = %q, want %q", got, want)
			}
		})
	}
}

func TestLoadFromMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Mode() != "free" {
		t.Fatalf("Mode = %q, want free", cfg.Mode())
	}
	if keys := cfg.SavedKeys(); len(keys) != 0 {
		t.Fatalf("SavedKeys = %d, want 0", len(keys))
	}
}

func TestLoadNormalizesKeysAndPreservesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := `{
  "keys": [
    {"id": "k1", "label": "무료 키", "tier": "FREE", "api_key": "fake-free-key-1", "limit_minutes": 0},
    {"label": "", "tier": "paid", "api_key": "fake-paid-key-2", "limit_minutes": 9999},
    {"label": "빈 키", "tier": "free", "api_key": "   "},
    {"label": "잘못된 항목", "tier": "paid"}
  ],
  "recent": ["/tmp/lecture"],
  "gui": {"mode": "batch", "theme": "system"},
  "unknown": {"nested": true}
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GEMINI_API_KEY", "")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Mode() != "batch" {
		t.Fatalf("Mode = %q, want batch", cfg.Mode())
	}

	keys := cfg.SavedKeys()
	if len(keys) != 2 {
		t.Fatalf("SavedKeys = %d, want 2: %+v", len(keys), keys)
	}
	if keys[0].ID != "k1" || keys[0].Tier != "free" || keys[0].LimitMinutes != DefaultFreeMinutes {
		t.Fatalf("first key not normalized: %+v", keys[0])
	}
	if keys[1].Label != "key2" {
		t.Fatalf("second label = %q, want key2", keys[1].Label)
	}
	if keys[1].LimitMinutes != 24*60 {
		t.Fatalf("limit not clamped: %d", keys[1].LimitMinutes)
	}

	if err := cfg.SetMode("free"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if _, err := cfg.AddKey("새 키", "free", "fake-new-key-3"); err != nil {
		t.Fatalf("AddKey: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("saved config is not valid JSON: %v", err)
	}
	for _, field := range []string{"recent", "unknown"} {
		if _, ok := saved[field]; !ok {
			t.Fatalf("unknown field %q was dropped", field)
		}
	}
	var gui map[string]json.RawMessage
	if err := json.Unmarshal(saved["gui"], &gui); err != nil {
		t.Fatal(err)
	}
	if string(gui["mode"]) != `"free"` {
		t.Fatalf("gui.mode = %s, want free", gui["mode"])
	}
	if _, ok := gui["theme"]; !ok {
		t.Fatal("unknown gui field was dropped")
	}
	if _, ok := saved["api_key"]; ok {
		t.Fatal("legacy api_key field was not removed")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("perm = %o, want 600", perm)
		}
	}

	reloaded, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Mode() != "free" {
		t.Fatalf("reloaded mode = %q", reloaded.Mode())
	}
	if got := len(reloaded.SavedKeys()); got != 3 {
		t.Fatalf("reloaded keys = %d, want 3", got)
	}
}

func TestEnvKeyIsReadOnlyFirstAndDeduped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	original := `{"keys":[{"id":"k1","label":"저장됨","tier":"paid","api_key":"same-key","limit_minutes":130}]}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_API_KEY", "same-key")
	t.Setenv("GEMINI_API_KEY_TIER", "free")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	keys := cfg.AllKeys()
	if len(keys) != 1 {
		t.Fatalf("AllKeys = %d, want 1", len(keys))
	}
	if keys[0].ID != "env" || keys[0].Tier != "free" || keys[0].Label != "환경변수" {
		t.Fatalf("env key = %+v", keys[0])
	}
	if got := len(cfg.SavedKeys()); got != 1 {
		t.Fatalf("SavedKeys = %d, want 1", got)
	}
}

func TestEnvKeyDefaultTierIsPaid(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "fake-env-key")
	t.Setenv("GEMINI_API_KEY_TIER", "nonsense")
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	keys := cfg.AllKeys()
	if len(keys) != 1 || keys[0].Tier != "paid" {
		t.Fatalf("keys = %+v, want one paid env key", keys)
	}
}

func TestAddKeyUpdatesDuplicateAPIKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("GEMINI_API_KEY", "")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}

	first, err := cfg.AddKey("무료", "free", "fake-key")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := cfg.AddKey("유료", "paid", "fake-key")
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != first.ID {
		t.Fatalf("duplicate key got new ID %q, want %q", updated.ID, first.ID)
	}
	keys := cfg.SavedKeys()
	if len(keys) != 1 || keys[0].Label != "유료" || keys[0].Tier != "paid" {
		t.Fatalf("keys = %+v", keys)
	}

	deleted, err := cfg.DeleteKey(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !deleted || len(cfg.SavedKeys()) != 0 {
		t.Fatalf("DeleteKey = %v, keys = %+v", deleted, cfg.SavedKeys())
	}
}

func TestAddKeyMakesLabelsUnique(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("GEMINI_API_KEY", "")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.AddKey("키", "free", "fake-1"); err != nil {
		t.Fatal(err)
	}
	second, err := cfg.AddKey("키", "free", "fake-2")
	if err != nil {
		t.Fatal(err)
	}
	if second.Label != "키2" {
		t.Fatalf("label = %q, want 키2", second.Label)
	}
}

func TestSetModeRejectsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetMode("turbo"); err == nil {
		t.Fatal("SetMode accepted an unknown mode")
	}
	if cfg.Mode() != "free" {
		t.Fatalf("mode changed to %q after a rejected save", cfg.Mode())
	}
}

func TestEffectiveModeFollowsAvailableTiers(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY_TIER", "")

	tests := []struct {
		name   string
		stored string
		tiers  []string
		want   string
	}{
		{name: "no keys keeps free", stored: "free", want: "free"},
		{name: "no keys keeps batch", stored: "batch", want: "batch"},
		{name: "free only forces free", stored: "batch", tiers: []string{"free"}, want: "free"},
		{name: "paid only forces batch", stored: "free", tiers: []string{"paid"}, want: "batch"},
		{name: "both keeps free", stored: "free", tiers: []string{"free", "paid"}, want: "free"},
		{name: "both keeps batch", stored: "batch", tiers: []string{"free", "paid"}, want: "batch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFrom(filepath.Join(t.TempDir(), "config.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.SetMode(tt.stored); err != nil {
				t.Fatal(err)
			}
			for _, tier := range tt.tiers {
				if _, err := cfg.AddKey("", tier, "fake-"+tier); err != nil {
					t.Fatal(err)
				}
			}

			if got := cfg.EffectiveMode(); got != tt.want {
				t.Fatalf("EffectiveMode = %q, want %q", got, tt.want)
			}
			if got := cfg.Mode(); got != tt.stored {
				t.Fatalf("stored preference = %q, want %q (it must not change)", got, tt.stored)
			}
		})
	}
}

func TestEffectiveModeUsesEnvironmentKeyTier(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "fake-env-key")
	t.Setenv("GEMINI_API_KEY_TIER", "paid")

	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetMode("free"); err != nil {
		t.Fatal(err)
	}
	free, paid := cfg.Tiers()
	if free || !paid {
		t.Fatalf("Tiers = free:%v paid:%v, want paid only", free, paid)
	}
	if got := cfg.EffectiveMode(); got != "batch" {
		t.Fatalf("EffectiveMode = %q, want batch", got)
	}
	if got := cfg.Mode(); got != "free" {
		t.Fatalf("stored preference = %q, want free", got)
	}
}

func TestLegacyAPIKeyIsMigrated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"api_key":"fake-legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_API_KEY", "")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	keys := cfg.SavedKeys()
	if len(keys) != 1 || keys[0].ID != "legacy" || keys[0].Tier != "paid" {
		t.Fatalf("legacy key = %+v", keys)
	}
	if err := cfg.SetMode("batch"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if _, ok := saved["api_key"]; ok {
		t.Fatalf("legacy field still present at top level:\n%s", data)
	}
	if !strings.Contains(string(saved["keys"]), "fake-legacy") {
		t.Fatalf("legacy key was not migrated into keys:\n%s", data)
	}
}

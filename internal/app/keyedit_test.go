package app

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/store"
)

func keyIDs(views []KeyView) string {
	out := make([]string, len(views))
	for i, v := range views {
		out[i] = v.Label
	}
	return strings.Join(out, ",")
}

func addKeys(t *testing.T, ta *testApp) (free1, free2, paid1 string) {
	t.Helper()
	for _, k := range []struct{ tier, key string }{
		{"free", "AIzaFAKE-free-0001"},
		{"free", "AIzaFAKE-free-0002"},
		{"paid", "AIzaFAKE-paid-0001"},
	} {
		if err := ta.app.AddKey("", k.tier, k.key); err != nil {
			t.Fatal(err)
		}
	}
	keys := ta.app.Keys()
	return keys[0].ID, keys[1].ID, keys[2].ID
}

func TestRenameKeyBinding(t *testing.T) {
	ta := newTestApp(t)
	f1, _, _ := addKeys(t, ta)
	tests := []struct {
		label   string
		want    string
		wantErr bool
	}{
		{label: " 연구실 ", want: "연구실"},
		{label: i18n.T("tier.free") + " 2", want: i18n.T("tier.free") + " 22"},
		{label: "", wantErr: true},
	}
	for _, tt := range tests {
		err := ta.app.RenameKey(f1, tt.label)
		if tt.wantErr {
			if err == nil {
				t.Errorf("RenameKey(%q) accepted", tt.label)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if got := ta.app.Keys()[0].Label; got != tt.want {
			t.Errorf("RenameKey(%q) label = %q, want %q", tt.label, got, tt.want)
		}
	}
	if err := ta.app.RenameKey("nope", "x"); err == nil {
		t.Error("RenameKey accepted an unknown ID")
	}
}

func TestSetKeyTierBindingFollowsModeRules(t *testing.T) {
	ta := newTestApp(t)
	f1, f2, p1 := addKeys(t, ta)
	if err := ta.app.SetKeyTier(p1, "free"); err != nil {
		t.Fatal(err)
	}
	if state := ta.app.ModeState(); state.Mode != "free" || !state.Free || state.Paid {
		t.Fatalf("only-free ModeState = %+v", state)
	}
	for _, id := range []string{f1, f2, p1} {
		if err := ta.app.SetKeyTier(id, "paid"); err != nil {
			t.Fatal(err)
		}
	}
	if state := ta.app.ModeState(); state.Mode != "batch" || state.Free || !state.Paid {
		t.Fatalf("only-paid ModeState = %+v", state)
	}
	if err := ta.app.SetKeyTier(f1, "gold"); err == nil {
		t.Error("SetKeyTier accepted an invalid tier")
	}
}

func TestSetKeyOrderBinding(t *testing.T) {
	ta := newTestApp(t)
	f1, f2, p1 := addKeys(t, ta)
	if err := ta.app.SetKeyOrder([]string{p1, f2, f1}); err != nil {
		t.Fatal(err)
	}
	if got := keyIDs(ta.app.Keys()); got != i18n.T("tier.paid")+" 1,"+i18n.T("tier.free")+" 2,"+i18n.T("tier.free")+" 1" {
		t.Fatalf("Keys order = %s", got)
	}
	for _, bad := range [][]string{{p1, f2}, {p1, p1, f1}, {p1, f2, f1, "x"}} {
		if err := ta.app.SetKeyOrder(bad); err == nil {
			t.Errorf("SetKeyOrder(%v) accepted", bad)
		}
	}
}

func TestEnvKeyCannotBeEditedByBindings(t *testing.T) {
	ta := newTestApp(t)
	t.Setenv("GEMINI_API_KEY", "AIzaENV-key-9876")
	if err := ta.app.AddKey("", "free", "AIzaFAKE-free-0001"); err != nil {
		t.Fatal(err)
	}
	saved := ta.app.Keys()[1].ID
	for name, err := range map[string]error{
		"rename": ta.app.RenameKey("env", "x"),
		"tier":   ta.app.SetKeyTier("env", "free"),
		"order":  ta.app.SetKeyOrder([]string{saved, "env"}),
	} {
		if err == nil || !strings.Contains(err.Error(), i18n.T("error.envKeyLocked")) {
			t.Errorf("%s(env) = %v", name, err)
		}
	}
	if err := ta.app.SetKeyOrder([]string{saved}); err != nil {
		t.Fatalf("SetKeyOrder without env: %v", err)
	}
	if got := ta.app.Keys(); got[0].Source != "env" {
		t.Fatalf("env key must stay first: %+v", got)
	}
}

func TestPinnedItemsAndKeyLabel(t *testing.T) {
	ta := newTestApp(t)
	f1, _, p1 := addKeys(t, ta)
	path := filepath.Join(ta.dir, "a.m4a")

	if err := ta.app.AddPathsWithKey([]string{path}, "missing"); err == nil {
		t.Fatal("AddPathsWithKey accepted an unknown key")
	}
	if err := ta.app.AddPathsWithKey([]string{path}, f1); err != nil {
		t.Fatal(err)
	}
	item := ta.app.ListItems("")[0]
	if item.KeyID != f1 || item.KeyLabel != i18n.T("tier.free")+" 1" {
		t.Fatalf("pinned item = KeyID %q KeyLabel %q", item.KeyID, item.KeyLabel)
	}

	// The JSON the window sees has the stored fields flattened plus KeyLabel.
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ID", "State", "Mode", "KeyID", "KeyLabel"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("JSON lacks %s: %s", name, data)
		}
	}

	// Renaming the key shows up in the row.
	if err := ta.app.RenameKey(f1, "집"); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.ListItems("")[0].KeyLabel; got != "집" {
		t.Fatalf("KeyLabel after rename = %q", got)
	}

	// SetItemKey repins a failed item and sets its mode from the key's tier.
	if err := ta.app.SetItemKey(item.ID, p1); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.ListItems("")[0]; got.KeyID != p1 || got.Mode != "batch" {
		t.Fatalf("after SetItemKey: %+v", got)
	}
	if err := ta.app.SetItemKey(item.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.ListItems("")[0]; got.KeyID != "" || got.KeyLabel != "" {
		t.Fatalf("after unpin: %+v", got)
	}
	if err := ta.app.SetItemKey(item.ID, "missing"); err == nil {
		t.Error("SetItemKey accepted an unknown key")
	}

	// A deleted key leaves KeyID with an empty label.
	if err := ta.app.SetItemKey(item.ID, p1); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.DeleteKey(p1); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.ListItems("")[0]; got.KeyID != p1 || got.KeyLabel != "" {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestSetItemKeyRejectsActiveItems(t *testing.T) {
	ta := newTestApp(t)
	f1, _, _ := addKeys(t, ta)
	for _, state := range []string{"running", "batch_waiting", "done"} {
		if err := ta.app.store.Add(store.Item{ID: state, Name: state, State: state}); err != nil {
			t.Fatal(err)
		}
		if err := ta.app.SetItemKey(state, f1); err == nil {
			t.Errorf("SetItemKey on %s accepted", state)
		}
	}
}

func TestNewItemKeyAppliesToAddPaths(t *testing.T) {
	ta := newTestApp(t)
	f1, _, p1 := addKeys(t, ta)
	if got := ta.app.NewItemKey(); got != "" {
		t.Fatalf("default NewItemKey = %q", got)
	}
	if err := ta.app.SetNewItemKey("missing"); err == nil {
		t.Fatal("SetNewItemKey accepted an unknown key")
	}
	if err := ta.app.SetNewItemKey(f1); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.AddPaths([]string{filepath.Join(ta.dir, "a.m4a")}); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.ListItems("")[0].KeyID; got != f1 {
		t.Fatalf("dropped item KeyID = %q, want %q", got, f1)
	}
	if err := ta.app.SetNewItemKey(p1); err != nil {
		t.Fatal(err)
	}
	if err := ta.app.DeleteKey(p1); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.NewItemKey(); got != "" {
		t.Fatalf("deleted key still selected: %q", got)
	}
}

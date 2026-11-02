package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jseook11/nalmeok/internal/config"
	"github.com/jseook11/nalmeok/internal/store"
)

type testApp struct {
	app     *App
	cfgPath string
	dir     string
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY_TIER", "")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg, err := config.LoadFrom(cfgPath)
	if err != nil {
		t.Fatalf("config.LoadFrom: %v", err)
	}
	st, err := store.Load(filepath.Join(dir, "items.json"))
	if err != nil {
		t.Fatalf("store.Load: %v", err)
	}
	return &testApp{app: New(cfg, st, NewStubRunner(st)), cfgPath: cfgPath, dir: dir}
}

func TestAddPathsCreatesFailedItems(t *testing.T) {
	ta := newTestApp(t)

	file := filepath.Join(ta.dir, "강의09.m4a")
	if err := os.WriteFile(file, []byte("fake audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(ta.dir, "ch07")
	if err := os.Mkdir(folder, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := ta.app.AddPaths([]string{"  " + file + "  ", folder}); err != nil {
		t.Fatalf("AddPaths: %v", err)
	}

	items := ta.app.ListItems("")
	if len(items) != 2 {
		t.Fatalf("ListItems = %d, want 2", len(items))
	}
	for _, item := range items {
		if item.State != "failed" || item.Detail != "전사 엔진 준비 중" {
			t.Fatalf("item state = %q / %q", item.State, item.Detail)
		}
		if item.Mode != "free" {
			t.Fatalf("item mode = %q, want free", item.Mode)
		}
		if item.ID == "" {
			t.Fatal("item has no ID")
		}
	}

	found := map[string]string{}
	for _, item := range items {
		found[item.Name] = item.Kind
	}
	if found["강의09.m4a"] != "file" || found["ch07"] != "folder" {
		t.Fatalf("kinds = %+v", found)
	}

	if got := ta.app.ListItems("ch07"); len(got) != 1 || got[0].Name != "ch07" {
		t.Fatalf("search = %+v", got)
	}
	if got := ta.app.ListItems("CH07"); len(got) != 1 {
		t.Fatalf("case-insensitive search = %+v", got)
	}

	if err := ta.app.AddPaths(nil); err == nil {
		t.Fatal("AddPaths accepted an empty list")
	}
}

func TestModePersists(t *testing.T) {
	ta := newTestApp(t)

	if got := ta.app.GetMode(); got != "free" {
		t.Fatalf("default mode = %q", got)
	}
	if err := ta.app.SetMode("batch"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if got := ta.app.GetMode(); got != "batch" {
		t.Fatalf("mode = %q, want batch", got)
	}
	if err := ta.app.SetMode("turbo"); err == nil {
		t.Fatal("SetMode accepted an invalid mode")
	}

	reloaded, err := config.LoadFrom(ta.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Mode(); got != "batch" {
		t.Fatalf("persisted mode = %q, want batch", got)
	}
}

func TestKeysAddShowAndDelete(t *testing.T) {
	ta := newTestApp(t)

	if got := ta.app.Keys(); len(got) != 0 {
		t.Fatalf("initial keys = %+v", got)
	}
	if err := ta.app.AddKey("무료 키", "free", "AIzaFAKE-key-1234"); err != nil {
		t.Fatalf("AddKey: %v", err)
	}
	keys := ta.app.Keys()
	if len(keys) != 1 {
		t.Fatalf("keys = %+v", keys)
	}
	view := keys[0]
	if view.Label != "무료 키" || view.Tier != "free" || view.Masked != "••••1234" || view.Source != "config" {
		t.Fatalf("key view = %+v", view)
	}
	if strings.Contains(view.Masked, "FAKE") {
		t.Fatalf("masked key leaked the secret: %q", view.Masked)
	}
	if view.ID == "" || view.ID == "env" {
		t.Fatalf("saved key ID = %q", view.ID)
	}

	if err := ta.app.AddKey("", "paid", "   "); err == nil {
		t.Fatal("AddKey accepted an empty key")
	}
	if err := ta.app.AddKey("이름", "vip", "AIzaFAKE-other"); err == nil {
		t.Fatal("AddKey accepted an invalid tier")
	}

	if err := ta.app.DeleteKey(view.ID); err != nil {
		t.Fatalf("DeleteKey: %v", err)
	}
	if got := ta.app.Keys(); len(got) != 0 {
		t.Fatalf("keys after delete = %+v", got)
	}
	if err := ta.app.DeleteKey(view.ID); err == nil {
		t.Fatal("DeleteKey accepted a missing ID")
	}
}

func TestEnvironmentKeyIsShownButNotDeletable(t *testing.T) {
	ta := newTestApp(t)
	t.Setenv("GEMINI_API_KEY", "AIzaENV-key-9876")
	t.Setenv("GEMINI_API_KEY_TIER", "free")

	keys := ta.app.Keys()
	if len(keys) != 1 {
		t.Fatalf("keys = %+v", keys)
	}
	if keys[0].Source != "env" || keys[0].ID != "env" || keys[0].Tier != "free" {
		t.Fatalf("env view = %+v", keys[0])
	}
	if keys[0].Masked != "••••9876" {
		t.Fatalf("env mask = %q", keys[0].Masked)
	}
	if err := ta.app.DeleteKey("env"); err == nil {
		t.Fatal("DeleteKey removed the environment key")
	}
	if err := ta.app.AddKey("저장", "paid", "AIzaSAVED-key-1111"); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.Keys(); len(got) != 2 {
		t.Fatalf("keys = %+v, want env + saved", got)
	}
}

func TestValidateKeyHookBlocksSave(t *testing.T) {
	ta := newTestApp(t)
	called := false
	ta.app.ValidateKey = func(ctx context.Context, key string) error {
		called = true
		if ctx == nil {
			t.Error("validator received a nil context")
		}
		if key != "AIzaFAKE-bad" {
			t.Errorf("validator key = %q", key)
		}
		return errors.New("무효한 키")
	}

	err := ta.app.AddKey("테스트", "paid", "AIzaFAKE-bad")
	if err == nil {
		t.Fatal("AddKey succeeded despite a rejected key")
	}
	if !strings.Contains(err.Error(), "키를 확인하지 못했습니다") {
		t.Fatalf("error = %q", err)
	}
	if !called {
		t.Fatal("validator was not called")
	}
	if got := ta.app.Keys(); len(got) != 0 {
		t.Fatalf("rejected key was saved: %+v", got)
	}
}

func TestRemove(t *testing.T) {
	ta := newTestApp(t)
	if err := ta.app.AddPaths([]string{filepath.Join(ta.dir, "a.m4a")}); err != nil {
		t.Fatal(err)
	}
	items := ta.app.ListItems("")
	if len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
	if err := ta.app.Remove(items[0].ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := ta.app.ListItems(""); len(got) != 0 {
		t.Fatalf("items after remove = %+v", got)
	}
	if err := ta.app.Remove(items[0].ID); err == nil {
		t.Fatal("Remove accepted a missing ID")
	}
}

func TestStubStateTransitions(t *testing.T) {
	ta := newTestApp(t)
	if err := ta.app.AddPaths([]string{filepath.Join(ta.dir, "a.m4a")}); err != nil {
		t.Fatal(err)
	}
	id := ta.app.ListItems("")[0].ID

	if err := ta.app.Cancel(id); err != nil {
		t.Fatal(err)
	}
	if got := ta.app.ListItems("")[0].State; got != "canceled" {
		t.Fatalf("state after cancel = %q", got)
	}

	if err := ta.app.Retry(id); err != nil {
		t.Fatal(err)
	}
	item := ta.app.ListItems("")[0]
	if item.State != "failed" || item.Detail != "전사 엔진 준비 중" {
		t.Fatalf("state after retry = %+v", item)
	}

	if err := ta.app.SendToBatch(id); err != nil {
		t.Fatal(err)
	}
	item = ta.app.ListItems("")[0]
	if item.State != "batch_waiting" || item.Mode != "batch" || item.Batch == nil {
		t.Fatalf("state after SendToBatch = %+v", item)
	}
}

func TestOutputActionsReportMissingFile(t *testing.T) {
	ta := newTestApp(t)
	if err := ta.app.AddPaths([]string{filepath.Join(ta.dir, "a.m4a")}); err != nil {
		t.Fatal(err)
	}
	id := ta.app.ListItems("")[0].ID

	for name, fn := range map[string]func(string) error{
		"OpenOutput":   ta.app.OpenOutput,
		"RevealOutput": ta.app.RevealOutput,
		"CopyOutput":   ta.app.CopyOutput,
	} {
		if err := fn(id); err == nil {
			t.Fatalf("%s succeeded without an output file", name)
		}
	}
	if err := ta.app.OpenOutput("missing"); err == nil {
		t.Fatal("OpenOutput accepted a missing ID")
	}
}

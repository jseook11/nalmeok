package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jseook11/nalmeok/internal/i18n"
	"github.com/jseook11/nalmeok/internal/runner"
	"github.com/jseook11/nalmeok/internal/store"
)

func TestFilterPSNArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"finder launch", []string{"-psn_0_123456"}, nil},
		{"finder plus path", []string{"-psn_0_1", "강의09.m4a"}, []string{"강의09.m4a"}},
		{"normal args", []string{"--batch", "ch07"}, []string{"--batch", "ch07"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterPSNArgs(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("filterPSNArgs(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("filterPSNArgs(%q) = %q, want %q", tc.in, got, tc.want)
				}
			}
		})
	}
}

func TestStatusLine(t *testing.T) {
	cases := []struct {
		name string
		item store.Item
		want string
	}{
		{
			name: "running percent",
			item: store.Item{Name: "강의09", State: runner.StateRunning, Progress: 0.4, Detail: i18n.T("status.transcribing")},
			want: i18n.T("cli.transcribing", "name", "강의09", "percent", 40),
		},
		{
			name: "running preparation",
			item: store.Item{Name: "강의09", State: runner.StateRunning, Detail: i18n.T("status.preparingAudio")},
			want: "강의09: " + i18n.T("status.preparingAudio"),
		},
		{
			name: "batch waiting",
			item: store.Item{
				Name: "강의09", State: runner.StateBatchWaiting,
				Batch: &store.BatchRef{SubmittedAt: time.Now().Add(-12 * time.Minute)},
			},
			want: i18n.T("cli.waiting", "name", "강의09", "minutes", 12),
		},
		{
			name: "done",
			item: store.Item{Name: "강의09", State: runner.StateDone, Output: "/tmp/강의09.md"},
			want: i18n.T("cli.done", "name", "강의09", "output", "/tmp/강의09.md"),
		},
		{
			name: "failed",
			item: store.Item{Name: "강의09", State: runner.StateFailed, Detail: i18n.T("error.apiKeyInvalid")},
			want: i18n.T("cli.failedReason", "name", "강의09", "reason", i18n.T("error.apiKeyInvalid")),
		},
		{
			name: "free exhausted",
			item: store.Item{Name: "강의09", State: runner.StateFreeExhausted},
			want: i18n.T("cli.freeExhausted", "name", "강의09"),
		},
		{
			name: "canceled",
			item: store.Item{Name: "강의09", State: runner.StateCanceled},
			want: i18n.T("cli.canceled", "name", "강의09"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusLine(tc.item); got != tc.want {
				t.Fatalf("statusLine = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPercentClamps(t *testing.T) {
	if got := percent(-0.2); got != 0 {
		t.Errorf("percent(-0.2) = %d", got)
	}
	if got := percent(1.4); got != 100 {
		t.Errorf("percent(1.4) = %d", got)
	}
	if got := percent(0.404); got != 40 {
		t.Errorf("percent(0.404) = %d", got)
	}
}

func captureStdout(t *testing.T, fn func() int) (string, int) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	code := fn()
	_ = w.Close()
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(data), code
}

func TestDoctorReportsCountsWithoutKeys(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY_TIER", "")

	cfgDir := filepath.Join(dir, "nalmeok")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgJSON := `{"keys":[
		{"id":"k1","label":"무료 키","tier":"free","api_key":"SECRET-FREE-1111"},
		{"id":"k2","label":"유료 키","tier":"paid","api_key":"SECRET-PAID-2222"}
	]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "items.json"), []byte(`{"items":[{},{}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, code := captureStdout(t, runDoctor)
	if code != 0 {
		t.Fatalf("runDoctor = %d", code)
	}
	for _, want := range []string{
		version,
		runtime.GOOS + "/" + runtime.GOARCH,
		"설정 폴더: " + cfgDir,
		"키: 무료 1개, 유료 1개",
		"items.json 항목: 2개",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"SECRET-FREE-1111", "SECRET-PAID-2222"} {
		if strings.Contains(out, secret) {
			t.Errorf("doctor printed an API key:\n%s", out)
		}
	}
}

func TestRunCLIWithoutKeysFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY_TIER", "")

	path := filepath.Join(dir, "a.m4a")
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := runCLI([]string{path}); code != 1 {
		t.Fatalf("runCLI without keys = %d, want 1", code)
	}
}

func TestRunCLIRejectsConflictingModes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GEMINI_API_KEY_TIER", "")

	if code := runCLI([]string{"--batch", "--free", "a.m4a"}); code != 2 {
		t.Fatalf("runCLI with both modes = %d, want 2", code)
	}
}

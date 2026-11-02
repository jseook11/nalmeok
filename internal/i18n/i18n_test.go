package i18n

import (
	"encoding/json"
	"errors"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jseook11/nalmeok/frontend"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		text  string
		pairs []any
		want  string
	}{
		{"plain", nil, "plain"},
		{"{name}: {n}%", []any{"name", "a.m4a", "n", 42}, "a.m4a: 42%"},
		{"{name} {name}", []any{"name", "x"}, "x x"},
		{"{missing} stays", []any{"name", "x"}, "{missing} stays"},
		{"odd {a}", []any{"a"}, "odd {a}"},
	}
	for _, c := range cases {
		if got := Format(c.text, c.pairs...); got != c.want {
			t.Errorf("Format(%q, %v) = %q, want %q", c.text, c.pairs, got, c.want)
		}
	}
}

func TestMissingKeyReturnsKey(t *testing.T) {
	if got := T("no.such.key"); got != "no.such.key" {
		t.Fatalf("T(missing) = %q", got)
	}
}

func TestTFillsPlaceholders(t *testing.T) {
	if got := T("cli.failedReason", "name", "a", "reason", "b"); got != "a: 실패 · b" {
		t.Fatalf("T = %q", got)
	}
}

func TestErrorAndWrap(t *testing.T) {
	sentinel := Error("error.noKey")
	if sentinel.Error() != "키 없음" {
		t.Fatalf("Error() = %q", sentinel.Error())
	}
	if !IsText(sentinel) {
		t.Fatal("IsText(Error) = false")
	}
	cause := errors.New("disk full")
	wrapped := Wrap("error.copyFailed", cause)
	if wrapped.Error() != "복사 실패: disk full" {
		t.Fatalf("Wrap.Error() = %q", wrapped.Error())
	}
	if !errors.Is(wrapped, cause) {
		t.Fatal("Wrap does not unwrap to its cause")
	}
	if IsText(cause) {
		t.Fatal("IsText(plain error) = true")
	}
}

func TestSetLanguageRejectsMissingCatalog(t *testing.T) {
	if err := SetLanguage("xx"); err == nil {
		t.Fatal("SetLanguage(xx) succeeded")
	}
	if Language() != Base {
		t.Fatalf("Language() = %q after failed switch", Language())
	}
}

// TestCatalogsAreValid checks every catalog parses as a flat object without
// duplicate keys, and that other languages only use base keys with the same
// placeholders.
func TestCatalogsAreValid(t *testing.T) {
	dir, err := fs.Sub(frontend.Assets, "dist/i18n")
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(dir, "*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no catalogs: %v", err)
	}
	base, err := Load(Base)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		data, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		checkNoDuplicateKeys(t, name, data)
		lang := strings.TrimSuffix(name, ".json")
		texts, err := Load(lang)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if lang == Base {
			continue
		}
		for key, text := range texts {
			baseText, ok := base[key]
			if !ok {
				t.Errorf("%s: key %q is not in %s.json", name, key, Base)
				continue
			}
			if got, want := placeholders(text), placeholders(baseText); got != want {
				t.Errorf("%s: %q placeholders %s, %s.json has %s", name, key, got, Base, want)
			}
		}
	}
}

func checkNoDuplicateKeys(t *testing.T, name string, data []byte) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(data)))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("%s: not a JSON object", name)
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		key := tok.(string)
		if seen[key] {
			t.Errorf("%s: duplicate key %q", name, key)
		}
		seen[key] = true
		var value string
		if err := dec.Decode(&value); err != nil {
			t.Fatalf("%s: %q is not a string: %v", name, key, err)
		}
	}
}

var placeholderRe = regexp.MustCompile(`\{\w+\}`)

func placeholders(s string) string {
	found := placeholderRe.FindAllString(s, -1)
	sort.Strings(found)
	return strings.Join(slices.Compact(found), ",")
}

package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// These tests read the repository's sources to keep the catalog and the code
// in step: every key the code asks for exists, every key in the catalog is
// asked for, and no new Hangul text sneaks into code outside the catalog.

var (
	// Go: i18n.T("key", ...), i18n.Error("key", ...), i18n.Wrap("key", ...).
	goCallRe = regexp.MustCompile(`\bi18n\.(?:T|Error|Wrap)\(`)
	// JS: t("key") or t("key", { ... }).
	jsCallRe = regexp.MustCompile(`(?:^|[^\w.$])t\(`)
	// HTML: data-i18n="key", data-i18n-aria-label="key" and so on.
	htmlAttrRe = regexp.MustCompile(`\bdata-i18n(?:-[a-z-]+)?="([^"]*)"`)
	keyArgRe   = regexp.MustCompile(`^\s*"([A-Za-z0-9_.]+)"`)
)

// hangulAllowed lists source files whose Hangul string literals are not
// user-interface text. The transcription prompt is model input, not UI.
var hangulAllowed = map[string]bool{
	"internal/gemini/prompt.go": true,
}

// jsDynamicAllowed are the only t(...) calls without a literal key: the
// definition and the static-HTML filler, whose keys come from data-i18n.
var jsDynamicAllowed = []string{"key, vars)", "el.dataset.i18n)", "el.getAttribute("}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repository root not found at %s", root)
	}
	return root
}

type source struct {
	rel  string // slash-separated path from the repository root
	text string
}

// sources returns the files under cmd, internal, frontend and scripts with
// the given extension. Go test files are left out.
func sources(t *testing.T, ext string) []source {
	t.Helper()
	root := repoRoot(t)
	var out []source
	for _, top := range []string{"cmd", "internal", "frontend", "scripts"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || filepath.Ext(path) != ext || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			out = append(out, source{rel: filepath.ToSlash(rel), text: string(data)})
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no %s sources found", ext)
	}
	return out
}

func lineOf(text string, offset int) int { return strings.Count(text[:offset], "\n") + 1 }

// usedKeys collects every catalog key the code asks for, with one place it
// is used. Calls without a literal key fail the test: the scan cannot see
// them, so the catalog checks would be blind.
func usedKeys(t *testing.T) map[string]string {
	t.Helper()
	used := map[string]string{}
	add := func(key, where string) {
		if _, ok := used[key]; !ok {
			used[key] = where
		}
	}
	for _, src := range sources(t, ".go") {
		if src.rel == "internal/i18n/i18n.go" {
			continue
		}
		for _, loc := range goCallRe.FindAllStringIndex(src.text, -1) {
			where := src.rel + ":" + strconv.Itoa(lineOf(src.text, loc[0]))
			m := keyArgRe.FindStringSubmatch(src.text[loc[1]:])
			if m == nil {
				t.Errorf("%s: i18n call without a literal key", where)
				continue
			}
			add(m[1], where)
		}
	}
	for _, src := range sources(t, ".js") {
		for _, loc := range jsCallRe.FindAllStringIndex(src.text, -1) {
			rest := src.text[loc[1]:]
			where := src.rel + ":" + strconv.Itoa(lineOf(src.text, loc[0]))
			if m := keyArgRe.FindStringSubmatch(rest); m != nil {
				add(m[1], where)
				continue
			}
			if !slices.ContainsFunc(jsDynamicAllowed, func(p string) bool { return strings.HasPrefix(rest, p) }) {
				t.Errorf("%s: t() call without a literal key", where)
			}
		}
	}
	for _, src := range sources(t, ".html") {
		for _, m := range htmlAttrRe.FindAllStringSubmatchIndex(src.text, -1) {
			add(src.text[m[2]:m[3]], src.rel+":"+strconv.Itoa(lineOf(src.text, m[0])))
		}
	}
	return used
}

func TestCatalogHasEveryUsedKey(t *testing.T) {
	base, err := Load(Base)
	if err != nil {
		t.Fatal(err)
	}
	used := usedKeys(t)
	if len(used) < 50 {
		t.Fatalf("found only %d keys in use; is the scan broken?", len(used))
	}
	for key, where := range used {
		if _, ok := base[key]; !ok {
			t.Errorf("%s: key %q is not in %s", where, key, "frontend/"+Path(Base))
		}
	}
}

func TestCatalogHasNoUnusedKeys(t *testing.T) {
	base, err := Load(Base)
	if err != nil {
		t.Fatal(err)
	}
	used := usedKeys(t)
	for key := range base {
		if _, ok := used[key]; !ok {
			t.Errorf("key %q in %s is not used", key, "frontend/"+Path(Base))
		}
	}
}

func hasHangul(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return unicode.Is(unicode.Hangul, r) }) >= 0
}

// TestNoHangulOutsideCatalog flags Hangul text in Go string literals, JS, HTML
// and CSS outside comments. User-facing text belongs in the catalog; tests,
// comments and the transcription prompt are exempt.
func TestNoHangulOutsideCatalog(t *testing.T) {
	for _, src := range sources(t, ".go") {
		if hangulAllowed[src.rel] {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, src.rel, src.text, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: %v", src.rel, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && hasHangul(lit.Value) {
				t.Errorf("%s: Hangul string %s; move it to %s", fset.Position(lit.Pos()), lit.Value, "frontend/"+Path(Base))
			}
			return true
		})
	}
	for _, ext := range []string{".js", ".html", ".css"} {
		for _, src := range sources(t, ext) {
			for i, line := range strings.Split(stripComments(src.text, ext), "\n") {
				if hasHangul(line) {
					t.Errorf("%s:%d: Hangul text %q; move it to %s", src.rel, i+1, strings.TrimSpace(line), "frontend/"+Path(Base))
				}
			}
		}
	}
}

var (
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	htmlCommentRe  = regexp.MustCompile(`(?s)<!--.*?-->`)
	// A // comment starts a line or follows whitespace, which keeps "https://"
	// inside strings intact.
	lineCommentRe = regexp.MustCompile(`(?m)(^|\s)//.*$`)
)

// stripComments blanks out comments but keeps line breaks so reported line
// numbers still match the file.
func stripComments(text, ext string) string {
	blank := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '\n' {
				return r
			}
			return ' '
		}, s)
	}
	switch ext {
	case ".html":
		return htmlCommentRe.ReplaceAllStringFunc(text, blank)
	case ".js":
		text = blockCommentRe.ReplaceAllStringFunc(text, blank)
		return lineCommentRe.ReplaceAllStringFunc(text, blank)
	default:
		return blockCommentRe.ReplaceAllStringFunc(text, blank)
	}
}

package gemini

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPromptMatchesPython keeps the Go prompts byte-identical to the Python
// app's while nalmeok/constants.py still exists.
func TestPromptMatchesPython(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "nalmeok", "constants.py"))
	if err != nil {
		t.Skip("Python constants.py not present")
	}
	src := string(data)
	m := regexp.MustCompile(`(?s)PROMPT = """(.*?)""".strip\(\)`).FindStringSubmatch(src)
	if m == nil {
		t.Fatal("PROMPT not found in constants.py")
	}
	if got := strings.TrimSpace(m[1]); got != Prompt {
		t.Errorf("Prompt differs from constants.py PROMPT")
	}
	l := regexp.MustCompile(`(?s)LIVE_PROMPT = PROMPT \+ """(.*?)"""`).FindStringSubmatch(src)
	if l == nil {
		t.Fatal("LIVE_PROMPT not found in constants.py")
	}
	if Prompt+l[1] != LivePrompt {
		t.Errorf("LivePrompt differs from constants.py LIVE_PROMPT")
	}
}

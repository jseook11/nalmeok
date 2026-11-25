package gemini

import (
	"strings"
	"testing"
)

// Each rule below answers a failure seen in practice; keep it in both
// prompts. The transcript pane reads paragraphs (a single paragraph is a wall
// of text), the model sometimes wrote its plan or a slide outline before the
// transcript, attached documents must not leak into it, and spoken math is
// wanted as LaTeX.
func TestPromptKeepsTheRules(t *testing.T) {
	rules := map[string]string{
		"paragraphs":        "Separate paragraphs with one blank line",
		"start with speech": "Begin with the first spoken words",
		"documents":         "Attached documents only help recognize terms",
		"spoken only":       "Write only what is spoken",
		"LaTeX":             "in LaTeX: $...$ inline",
	}
	for name, p := range map[string]string{"Prompt": Prompt, "LivePrompt": LivePrompt} {
		for rule, text := range rules {
			if !strings.Contains(p, text) {
				t.Errorf("%s lost the %s rule", name, rule)
			}
		}
	}
}

// Restating one restriction several ways made Gemini refuse more often.
func TestPromptDoesNotStackProhibitions(t *testing.T) {
	for name, p := range map[string]string{"Prompt": Prompt, "LivePrompt": LivePrompt} {
		lower := strings.ToLower(p)
		n := strings.Count(lower, "do not") + strings.Count(lower, "never") + strings.Count(lower, "must not")
		if n > 1 {
			t.Errorf("%s has %d prohibitions; state each rule once, positively where possible", name, n)
		}
	}
}

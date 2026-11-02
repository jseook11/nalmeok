package gemini

import (
	"strings"
	"testing"
)

// The transcript pane reads paragraphs; a one-paragraph transcript is a wall
// of text. Keep the explicit paragraph rule in both prompts.
func TestPromptAsksForParagraphs(t *testing.T) {
	for name, p := range map[string]string{"Prompt": Prompt, "LivePrompt": LivePrompt} {
		if !strings.Contains(p, "Separate paragraphs with one blank line") {
			t.Errorf("%s lost the paragraph rule", name)
		}
		if !strings.Contains(p, "must not add, drop, or reorder any words") {
			t.Errorf("%s lost the fidelity guard on paragraph breaks", name)
		}
	}
}

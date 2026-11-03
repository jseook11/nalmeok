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

// The model sometimes wrote its plan, a timestamped draft or a slide outline
// before the transcript; spoken math is wanted as LaTeX.
func TestPromptStartsWithSpeechAndUsesLaTeX(t *testing.T) {
	for name, p := range map[string]string{"Prompt": Prompt, "LivePrompt": LivePrompt} {
		if !strings.Contains(p, "Begin the response with the first spoken words") {
			t.Errorf("%s lost the start-with-speech rule", name)
		}
		if !strings.Contains(p, "in LaTeX: inline as $...$") {
			t.Errorf("%s lost the LaTeX rule", name)
		}
		if !strings.Contains(p, "Write only what is spoken") {
			t.Errorf("%s lost the fidelity guard on math", name)
		}
	}
}

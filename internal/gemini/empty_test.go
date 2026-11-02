package gemini

import (
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestEmptyResponseIsTransient(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &EmptyResponseError{Finish: "", Usage: "prompt 1, candidates 0, thoughts 0"})
	if !IsTransient(err) || !IsEmptyResponse(err) {
		t.Fatalf("empty response not transient: %v", err)
	}
	if !strings.Contains(err.Error(), "finish none") || !strings.Contains(err.Error(), "candidates 0") {
		t.Errorf("message = %q", err.Error())
	}
}

func TestUsageText(t *testing.T) {
	resp := &genai.GenerateContentResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 5800, CandidatesTokenCount: 0, ThoughtsTokenCount: 120,
	}}
	if got := usageText(resp); got != "prompt 5800, candidates 0, thoughts 120" {
		t.Errorf("usageText = %q", got)
	}
	if got := usageText(nil); got != "no usage" {
		t.Errorf("usageText(nil) = %q", got)
	}
}

func TestNotFound(t *testing.T) {
	if !IsNotFound(genai.APIError{Code: 404}) || IsNotFound(genai.APIError{Code: 400}) {
		t.Error("IsNotFound")
	}
}

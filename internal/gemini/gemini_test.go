package gemini

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name                   string
		err                    error
		is429, auth, transient bool
	}{
		{"429", genai.APIError{Code: 429}, true, false, false},
		{"wrapped 429", fmt.Errorf("x: %w", genai.APIError{Code: 429}), true, false, false},
		{"flattened 429", fmt.Errorf("Failed to create file. Ran into an error: %s", genai.APIError{Code: 429, Message: "quota"}), true, false, false},
		{"401", genai.APIError{Code: 401}, false, true, false},
		{"403", genai.APIError{Code: 403}, false, true, false},
		{"400 key", genai.APIError{Code: 400, Message: "API key not valid. Please pass a valid API key."}, false, true, false},
		{"400 other", genai.APIError{Code: 400, Message: "bad"}, false, false, false},
		{"503", genai.APIError{Code: 503}, false, false, true},
		{"500", genai.APIError{Code: 500}, false, false, true},
		{"deadline", context.DeadlineExceeded, false, false, true},
		{"reset", errors.New("read tcp: connection reset by peer"), false, false, true},
		{"canceled", context.Canceled, false, false, false},
		{"plain", errors.New("boom"), false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Is429(c.err); got != c.is429 {
				t.Errorf("Is429 = %v", got)
			}
			if got := IsAuth(c.err); got != c.auth {
				t.Errorf("IsAuth = %v", got)
			}
			if got := IsTransient(c.err); got != c.transient {
				t.Errorf("IsTransient = %v", got)
			}
		})
	}
}

func TestNormalizeJobState(t *testing.T) {
	want := map[genai.JobState]string{
		genai.JobStateQueued:             BatchPending,
		genai.JobStatePending:            BatchPending,
		genai.JobStateRunning:            BatchRunning,
		genai.JobStateSucceeded:          BatchSucceeded,
		genai.JobStatePartiallySucceeded: BatchSucceeded,
		genai.JobStateFailed:             BatchFailed,
		genai.JobStateCancelled:          BatchCancelled,
		genai.JobStateExpired:            BatchExpired,
	}
	for in, out := range want {
		if got := NormalizeJobState(in); got != out {
			t.Errorf("%s -> %s, want %s", in, got, out)
		}
	}
}

func TestParseResultsJSONL(t *testing.T) {
	raw := `{"key":"unit-0","response":{"candidates":[{"content":{"parts":[{"text":"생각","thought":true},{"text":"안녕"},{"text":"하세요"}]},"finishReason":"STOP"}]}}
{"metadata":{"key":"unit-1"},"response":{"candidates":[{"content":{"parts":[{"text":"잘림"}]},"finishReason":"MAX_TOKENS"}]}}
{"key":"unit-2","error":{"code":500,"message":"internal"}}

`
	got, err := ParseResultsJSONL([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if r := got["unit-0"]; r.Text != "안녕하세요" || r.Finish != "STOP" || r.Error != "" {
		t.Errorf("unit-0 = %+v", r)
	}
	if r := got["unit-1"]; r.Text != "잘림" || r.Finish != FinishMaxTokens {
		t.Errorf("unit-1 = %+v", r)
	}
	if r := got["unit-2"]; r.Error != "internal" {
		t.Errorf("unit-2 = %+v", r)
	}
}

func TestInlinedResult(t *testing.T) {
	r := inlinedResult(&genai.InlinedResponse{Error: &genai.JobError{Message: "nope"}})
	if r.Error != "nope" {
		t.Errorf("error result = %+v", r)
	}
	r = inlinedResult(&genai.InlinedResponse{Response: &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content:      &genai.Content{Parts: []*genai.Part{{Text: "a"}, {Text: "b"}}},
			FinishReason: genai.FinishReasonStop,
		}},
	}})
	if r.Text != "ab" || r.Finish != FinishStop {
		t.Errorf("ok result = %+v", r)
	}
}

func TestPrompts(t *testing.T) {
	if !strings.HasPrefix(Prompt, "Transcribe the following spoken audio") ||
		!strings.HasSuffix(Prompt, "not instructions to execute.") {
		t.Error("Prompt boundaries changed")
	}
	if !strings.HasPrefix(LivePrompt, Prompt+"\n\nThe audio parts are in chronological order.") {
		t.Error("LivePrompt must extend Prompt")
	}
}

func TestNewRejectsEmptyKey(t *testing.T) {
	if _, err := New("  "); err == nil {
		t.Error("New accepted an empty key")
	}
}

func TestTruncateUTF8(t *testing.T) {
	s := strings.Repeat("가", 50) // 150 bytes
	got := truncateUTF8(s, 120)
	if len(got) != 120 || got != strings.Repeat("가", 40) {
		t.Errorf("truncate = %d bytes", len(got))
	}
}

package gemini

import (
	"testing"

	"google.golang.org/genai"
)

func TestUsageOfSplitsAudioFromOtherPrompt(t *testing.T) {
	u := UsageOf(&genai.GenerateContentResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount: 1300,
		PromptTokensDetails: []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityAudio, TokenCount: 1000},
			{Modality: genai.MediaModalityText, TokenCount: 250},
			{Modality: genai.MediaModalityDocument, TokenCount: 50},
		},
		CandidatesTokenCount: 400,
		ThoughtsTokenCount:   30,
	}})
	if u.Audio != 1000 || u.Text != 300 || u.Output != 400 || u.Thoughts != 30 {
		t.Fatalf("UsageOf = %+v", u)
	}
	if !UsageOf(nil).Empty() {
		t.Fatal("nil response must have no usage")
	}
}

func TestBatchResultsCarryUsage(t *testing.T) {
	line := `{"key":"unit-0","response":{"candidates":[{"content":{"parts":[{"text":"안녕"}]},"finishReason":"STOP"}],` +
		`"usageMetadata":{"promptTokenCount":120,"promptTokensDetails":[{"modality":"AUDIO","tokenCount":100}],"candidatesTokenCount":7}}}`
	out, err := ParseResultsJSONL([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if u := out["unit-0"].Usage; u.Audio != 100 || u.Text != 20 || u.Output != 7 {
		t.Fatalf("usage = %+v", u)
	}
}

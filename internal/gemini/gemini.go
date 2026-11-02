// Package gemini is a thin, testable wrapper over google.golang.org/genai.
//
// The runner only sees the Client interface so tests can use fakes. The real
// implementation targets the Gemini Developer API with one API key per client
// (uploaded file URIs only work inside the key's own project).
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jseook11/nalmeok/internal/store"
	"google.golang.org/genai"
)

// Model is the transcription model for both live and Batch requests.
const Model = "gemini-3.8-flash"

// MaxOutputTokens is the per-request output cap (live and Batch).
const MaxOutputTokens = 65536

// inlineLimit is the largest inline Batch payload; above it a JSONL file is
// uploaded instead. Requests only carry file URIs, so this is rarely hit.
const inlineLimit = 20 << 20

// requestTimeout bounds one HTTP request. A 130-minute live request can take
// several minutes, so this matches the Python app's 30-minute timeout.
const requestTimeout = 30 * time.Minute

// Normalized Batch states returned by BatchStatus.
const (
	BatchPending   = "pending"
	BatchRunning   = "running"
	BatchSucceeded = "succeeded"
	BatchFailed    = "failed"
	BatchCancelled = "cancelled"
	BatchExpired   = "expired"
)

// Finish reasons the runner cares about.
const (
	FinishStop      = "STOP"
	FinishMaxTokens = "MAX_TOKENS"
	// FinishBlocked prefixes a finish reason when Gemini refuses the request
	// (promptFeedback.blockReason). The rest of the string is the server's
	// reason, for example "BLOCKED_OTHER".
	FinishBlocked = "BLOCKED_"
)

// Client is everything the runner needs from Gemini.
type Client interface {
	// Upload sends a local file and waits until it is ACTIVE.
	Upload(ctx context.Context, path, mime string) (store.Upload, error)
	// Generate runs one live request and returns the text and finish reason.
	Generate(ctx context.Context, parts []Part) (text string, finish string, err error)
	// SubmitBatch creates a Batch job with one request per BatchRequest.
	SubmitBatch(ctx context.Context, reqs []BatchRequest, displayName string) (name string, err error)
	// BatchStatus returns pending|running|succeeded|failed|cancelled|expired.
	BatchStatus(ctx context.Context, name string) (state string, err error)
	// BatchResults returns results keyed by BatchRequest.Key.
	BatchResults(ctx context.Context, name string) (map[string]Result, error)
	// CancelBatch asks the server to cancel a Batch job.
	CancelBatch(ctx context.Context, name string) error
	// Validate makes a cheap, non-billing call to check the key.
	Validate(ctx context.Context) error
}

// Part is one request part: either Text or a file (FileURI + MIME).
type Part struct {
	Text    string
	FileURI string
	MIME    string
}

// BatchRequest is one request inside a Batch job.
type BatchRequest struct {
	Key   string
	Parts []Part
}

// Result is one Batch request's outcome. Error is set when the request failed.
type Result struct {
	Text   string
	Finish string
	Error  string
}

type client struct {
	c *genai.Client
}

// New returns a Gemini Developer API client for apiKey.
func New(apiKey string) (Client, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, errors.New("API 키가 비어 있어요")
	}
	timeout := requestTimeout
	c, err := genai.NewClient(context.Background(), &genai.ClientConfig{
		APIKey:      apiKey,
		Backend:     genai.BackendGeminiAPI,
		HTTPOptions: genai.HTTPOptions{Timeout: &timeout},
	})
	if err != nil {
		return nil, err
	}
	return &client{c: c}, nil
}

func generationConfig() *genai.GenerateContentConfig {
	return &genai.GenerateContentConfig{
		MaxOutputTokens: MaxOutputTokens,
		ThinkingConfig:  &genai.ThinkingConfig{ThinkingLevel: genai.ThinkingLevelLow},
	}
}

func contents(parts []Part) []*genai.Content {
	out := make([]*genai.Part, 0, len(parts))
	for _, p := range parts {
		if p.FileURI != "" {
			out = append(out, genai.NewPartFromURI(p.FileURI, p.MIME))
		} else {
			out = append(out, genai.NewPartFromText(p.Text))
		}
	}
	return []*genai.Content{genai.NewContentFromParts(out, genai.RoleUser)}
}

// Upload streams the file instead of using UploadFromPath: that helper puts
// the file name in an HTTP header, which breaks on non-ASCII names.
func (c *client) Upload(ctx context.Context, path, mime string) (store.Upload, error) {
	f, err := os.Open(path)
	if err != nil {
		return store.Upload{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return store.Upload{}, err
	}
	headers := make(map[string][]string)
	headers["X-Goog-Upload-Header-Content-Length"] = []string{strconv.FormatInt(info.Size(), 10)}
	file, err := c.c.Files.Upload(ctx, f, &genai.UploadFileConfig{
		MIMEType:    mime,
		DisplayName: filepath.Base(path),
		HTTPOptions: &genai.HTTPOptions{Headers: headers},
	})
	if err != nil {
		return store.Upload{}, err
	}
	if file == nil || file.Name == "" {
		return store.Upload{}, errors.New("업로드 응답이 불완전해요")
	}
	uploadedAt := time.Now()

	for file.State != genai.FileStateActive {
		if file.State == genai.FileStateFailed {
			msg := "file processing failed"
			if file.Error != nil && file.Error.Message != "" {
				msg = file.Error.Message
			}
			return store.Upload{}, fmt.Errorf("업로드한 파일을 처리하지 못했어요: %s", msg)
		}
		if time.Since(uploadedAt) > 10*time.Minute {
			return store.Upload{}, errors.New("업로드한 파일 처리가 너무 오래 걸려요")
		}
		select {
		case <-ctx.Done():
			return store.Upload{}, ctx.Err()
		case <-time.After(2 * time.Second):
		}
		file, err = c.c.Files.Get(ctx, file.Name, nil)
		if err != nil {
			return store.Upload{}, err
		}
	}
	if file.URI == "" {
		return store.Upload{}, errors.New("업로드 응답에 URI가 없어요")
	}
	return store.Upload{URI: file.URI, MIME: mime, UploadedAt: uploadedAt}, nil
}

func (c *client) Generate(ctx context.Context, parts []Part) (string, string, error) {
	resp, err := c.c.Models.GenerateContent(ctx, Model, contents(parts), generationConfig())
	if err != nil {
		return "", "", err
	}
	text, finish := responseText(resp)
	return text, finish, nil
}

func responseText(resp *genai.GenerateContentResponse) (string, string) {
	if resp == nil {
		return "", ""
	}
	if resp.PromptFeedback != nil && resp.PromptFeedback.BlockReason != "" {
		return "", FinishBlocked + string(resp.PromptFeedback.BlockReason)
	}
	if len(resp.Candidates) == 0 {
		return "", ""
	}
	cand := resp.Candidates[0]
	var texts []string
	if cand.Content != nil {
		for _, p := range cand.Content.Parts {
			if p == nil || p.Thought || p.Text == "" {
				continue
			}
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, ""), string(cand.FinishReason)
}

func (c *client) SubmitBatch(ctx context.Context, reqs []BatchRequest, displayName string) (string, error) {
	if len(reqs) == 0 {
		return "", errors.New("보낼 요청이 없어요")
	}
	if len(displayName) > 120 {
		displayName = truncateUTF8(displayName, 120)
	}
	inline := make([]*genai.InlinedRequest, 0, len(reqs))
	for _, r := range reqs {
		inline = append(inline, &genai.InlinedRequest{
			Contents: contents(r.Parts),
			Metadata: map[string]string{"key": r.Key},
			Config:   generationConfig(),
		})
	}
	size, _ := json.Marshal(inline)
	src := &genai.BatchJobSource{InlinedRequests: inline}
	if len(size) >= inlineLimit {
		name, err := c.uploadJSONL(ctx, reqs)
		if err != nil {
			return "", err
		}
		src = &genai.BatchJobSource{FileName: name}
	}
	job, err := c.c.Batches.Create(ctx, Model, src, &genai.CreateBatchJobConfig{DisplayName: displayName})
	if err != nil {
		return "", err
	}
	if job == nil || job.Name == "" {
		return "", errors.New("요청 ID를 받지 못했어요")
	}
	return job.Name, nil
}

// jsonlLine mirrors the Python app's request file rows.
type jsonlLine struct {
	Key     string `json:"key"`
	Request struct {
		Contents         []*genai.Content `json:"contents"`
		GenerationConfig map[string]any   `json:"generationConfig"`
	} `json:"request"`
}

func (c *client) uploadJSONL(ctx context.Context, reqs []BatchRequest) (string, error) {
	var buf bytes.Buffer
	for _, r := range reqs {
		var line jsonlLine
		line.Key = r.Key
		line.Request.Contents = contents(r.Parts)
		line.Request.GenerationConfig = map[string]any{
			"maxOutputTokens": MaxOutputTokens,
			"thinkingConfig":  map[string]any{"thinkingLevel": string(genai.ThinkingLevelLow)},
		}
		data, err := json.Marshal(line)
		if err != nil {
			return "", err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	headers := make(map[string][]string)
	headers["X-Goog-Upload-Header-Content-Length"] = []string{strconv.Itoa(buf.Len())}
	file, err := c.c.Files.Upload(ctx, &buf, &genai.UploadFileConfig{
		MIMEType:    "jsonl",
		DisplayName: "nalmeok-requests.jsonl",
		HTTPOptions: &genai.HTTPOptions{Headers: headers},
	})
	if err != nil {
		return "", err
	}
	if file == nil || file.Name == "" {
		return "", errors.New("업로드 응답이 불완전해요")
	}
	return file.Name, nil
}

// NormalizeJobState maps a genai JobState to the runner's vocabulary.
func NormalizeJobState(state genai.JobState) string {
	switch state {
	case genai.JobStateSucceeded, genai.JobStatePartiallySucceeded:
		return BatchSucceeded
	case genai.JobStateFailed:
		return BatchFailed
	case genai.JobStateCancelled:
		return BatchCancelled
	case genai.JobStateExpired:
		return BatchExpired
	case genai.JobStateRunning, genai.JobStateCancelling, genai.JobStateUpdating, genai.JobStatePaused:
		return BatchRunning
	default:
		return BatchPending
	}
}

func (c *client) BatchStatus(ctx context.Context, name string) (string, error) {
	job, err := c.c.Batches.Get(ctx, name, nil)
	if err != nil {
		return "", err
	}
	return NormalizeJobState(job.State), nil
}

func (c *client) BatchResults(ctx context.Context, name string) (map[string]Result, error) {
	job, err := c.c.Batches.Get(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	if job.Dest == nil {
		return nil, errors.New("서버 결과가 비어 있어요")
	}
	if len(job.Dest.InlinedResponses) > 0 {
		var srcKeys []string
		if job.Src != nil {
			for _, r := range job.Src.InlinedRequests {
				if r != nil {
					srcKeys = append(srcKeys, r.Metadata["key"])
				} else {
					srcKeys = append(srcKeys, "")
				}
			}
		}
		out := make(map[string]Result, len(job.Dest.InlinedResponses))
		for i, r := range job.Dest.InlinedResponses {
			if r == nil {
				continue
			}
			key := r.Metadata["key"]
			if key == "" && i < len(srcKeys) {
				key = srcKeys[i]
			}
			if key == "" {
				continue
			}
			out[key] = inlinedResult(r)
		}
		return out, nil
	}
	if job.Dest.FileName != "" {
		data, err := c.c.Files.Download(ctx, genai.NewDownloadURIFromFile(&genai.File{DownloadURI: job.Dest.FileName}), nil)
		if err != nil {
			return nil, err
		}
		return ParseResultsJSONL(data)
	}
	return nil, errors.New("서버 결과 위치를 찾지 못했어요")
}

func inlinedResult(r *genai.InlinedResponse) Result {
	if r.Error != nil {
		msg := r.Error.Message
		if msg == "" {
			msg = "요청 오류"
		}
		return Result{Error: msg}
	}
	text, finish := responseText(r.Response)
	return Result{Text: text, Finish: finish}
}

// ParseResultsJSONL reads a Batch result file (one JSON object per line with
// "key" or "metadata.key", and "response" or "error").
func ParseResultsJSONL(data []byte) (map[string]Result, error) {
	out := make(map[string]Result)
	for i, line := range bytes.Split(data, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var row struct {
			Key      string                         `json:"key"`
			Metadata map[string]any                 `json:"metadata"`
			Error    json.RawMessage                `json:"error"`
			Response *genai.GenerateContentResponse `json:"response"`
		}
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("결과 %d행을 읽지 못했어요: %w", i+1, err)
		}
		key := row.Key
		if key == "" {
			if k, ok := row.Metadata["key"].(string); ok {
				key = k
			}
		}
		if key == "" {
			continue
		}
		if len(row.Error) > 0 && string(row.Error) != "null" {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(row.Error, &e)
			if e.Message == "" {
				e.Message = string(row.Error)
			}
			out[key] = Result{Error: e.Message}
			continue
		}
		text, finish := responseText(row.Response)
		out[key] = Result{Text: text, Finish: finish}
	}
	return out, nil
}

func (c *client) CancelBatch(ctx context.Context, name string) error {
	return c.c.Batches.Cancel(ctx, name, nil)
}

func (c *client) Validate(ctx context.Context) error {
	_, err := c.c.Models.Get(ctx, Model, nil)
	return err
}

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

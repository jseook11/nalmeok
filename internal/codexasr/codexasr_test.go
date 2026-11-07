package codexasr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func tokenWithClaims(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	return "header." + enc + ".signature"
}

func TestParseAuthFile(t *testing.T) {
	token := tokenWithClaims(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})
	data := `{"auth_mode":"chatgpt","tokens":{"access_token":"` + token + `"}}`
	auth, err := Parse([]byte(data), "auth.json")
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccessToken != token {
		t.Fatalf("token = %q", auth.AccessToken)
	}
	if auth.AccountID != "" {
		t.Fatalf("account = %q, want empty", auth.AccountID)
	}
	if auth.Expired(time.Now()) {
		t.Fatal("fresh token reported expired")
	}
}

func TestParseAuthFileAccountAndCamelMode(t *testing.T) {
	token := tokenWithClaims(t, map[string]any{
		"exp": time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-123",
		},
	})
	data := `{"authMode":"chatgpt_auth_tokens","tokens":{"access_token":"Bearer ` + token + `","account_id":"acct-456"}}`
	auth, err := Parse([]byte(data), "auth.json")
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccessToken != token {
		t.Fatalf("token = %q", auth.AccessToken)
	}
	if auth.AccountID != "acct-456" {
		t.Fatalf("account = %q, want acct-456", auth.AccountID)
	}
}

func TestParseAuthFileFallsBackToJWTAccount(t *testing.T) {
	token := tokenWithClaims(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-123"},
	})
	auth, err := Parse([]byte(`{"tokens":{"access_token":"`+token+`"}}`), "auth.json")
	if err != nil {
		t.Fatal(err)
	}
	if auth.AccountID != "acct-123" {
		t.Fatalf("account = %q, want acct-123", auth.AccountID)
	}
}

func TestParseAuthFileRejectsOtherModes(t *testing.T) {
	_, err := Parse([]byte(`{"auth_mode":"apikey","tokens":{"access_token":"x"}}`), "auth.json")
	if !errors.Is(err, ErrNoChatGPT) {
		t.Fatalf("err = %v, want ErrNoChatGPT", err)
	}
	if _, err := Parse([]byte(`{"tokens":{}}`), "auth.json"); !errors.Is(err, ErrNoChatGPT) {
		t.Fatalf("empty token err = %v, want ErrNoChatGPT", err)
	}
}

func TestReadFileMissing(t *testing.T) {
	_, err := ReadFile(filepath.Join(t.TempDir(), "auth.json"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestExpired(t *testing.T) {
	token := tokenWithClaims(t, map[string]any{"exp": time.Now().Add(-time.Minute).Unix()})
	auth := Auth{AccessToken: token}
	if !auth.Expired(time.Now()) {
		t.Fatal("expired token reported fresh")
	}
	if (Auth{AccessToken: "not-a-jwt"}).Expired(time.Now()) {
		t.Fatal("token without exp reported expired")
	}
}

func TestTranscribe(t *testing.T) {
	dir := t.TempDir()
	audio := filepath.Join(dir, "piece-01.m4a")
	if err := os.WriteFile(audio, []byte("audio-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := tokenWithClaims(t, map[string]any{"exp": time.Now().Add(time.Hour).Unix()})

	var gotAuth, gotOriginator, gotAccount, gotFile string
	var gotMIME string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotOriginator = r.Header.Get("originator")
		gotAccount = r.Header.Get("ChatGPT-Account-Id")
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/form-data" {
			t.Errorf("content type = %q (%v)", r.Header.Get("Content-Type"), err)
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		part, err := mr.NextPart()
		if err != nil {
			t.Errorf("next part: %v", err)
			return
		}
		gotFile = part.FileName()
		gotMIME = part.Header.Get("Content-Type")
		data, _ := io.ReadAll(part)
		if string(data) != "audio-bytes" {
			t.Errorf("body = %q", data)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"안녕하세요, 강의를 시작합니다."}`))
	}))
	defer srv.Close()

	c := &Client{
		Endpoint: srv.URL,
		Auth:     Auth{AccessToken: token, AccountID: "acct-1"},
		HTTP:     srv.Client(),
		Version:  "1.2.3",
	}
	got, err := c.Transcribe(context.Background(), audio, "audio/m4a")
	if err != nil {
		t.Fatal(err)
	}
	if got != "안녕하세요, 강의를 시작합니다." {
		t.Fatalf("text = %q", got)
	}
	if gotAuth != "Bearer "+token {
		t.Errorf("authorization = %q", gotAuth)
	}
	if gotOriginator != "Codex Desktop" {
		t.Errorf("originator = %q", gotOriginator)
	}
	if gotAccount != "acct-1" {
		t.Errorf("account header = %q", gotAccount)
	}
	if gotFile != "piece-01.m4a" {
		t.Errorf("file name = %q", gotFile)
	}
	if !strings.Contains(gotMIME, "m4a") && !strings.Contains(gotMIME, "mp4") {
		t.Errorf("part type = %q", gotMIME)
	}
}

func TestTranscribeUnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "piece.aiff")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &Client{Auth: Auth{AccessToken: "token"}, Endpoint: "http://127.0.0.1:1"}
	_, err := c.Transcribe(context.Background(), path, "audio/aiff")
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("err = %v, want ErrUnsupported", err)
	}
}

func TestTranscribeExpiredToken(t *testing.T) {
	token := tokenWithClaims(t, map[string]any{"exp": time.Now().Add(-time.Hour).Unix()})
	c := &Client{Auth: Auth{AccessToken: token}, Endpoint: "http://127.0.0.1:1"}
	_, err := c.Transcribe(context.Background(), "x.wav", "audio/wav")
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestTranscribeHTTPError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "piece.wav")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad"}`, http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := &Client{
		Endpoint: srv.URL,
		Auth:     Auth{AccessToken: "token"},
		HTTP:     srv.Client(),
	}
	_, err := c.Transcribe(context.Background(), path, "audio/wav")
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != http.StatusServiceUnavailable {
		t.Fatalf("err = %v, want HTTPError 503", err)
	}
	if !IsTransient(err) {
		t.Fatal("503 should be transient")
	}
}

func TestUploadType(t *testing.T) {
	cases := []struct {
		mime, path, wantType, wantExt string
	}{
		{"audio/m4a", "a.m4a", "audio/mp4", "m4a"},
		{"audio/mp3", "a.mp3", "audio/mpeg", "mp3"},
		{"", "a.flac", "audio/flac", "flac"},
		{"application/octet-stream", "a.ogg", "audio/ogg", "ogg"},
	}
	for _, c := range cases {
		typ, ext, err := uploadType(c.mime, c.path)
		if err != nil {
			t.Fatalf("%s: %v", c.path, err)
		}
		if typ != c.wantType || ext != c.wantExt {
			t.Fatalf("%s: got %s/%s", c.path, typ, ext)
		}
	}
}

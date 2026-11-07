// Package codexasr transcribes with the ChatGPT endpoint Codex Desktop uses,
// reusing a Codex login already on this machine.
//
// This is an experiment. The endpoint is undocumented, belongs to OpenAI, and
// may change at any time. nalmeok only reads the login file: it never writes,
// refreshes, or installs anything.
//
// The protocol is the one codex-asr (MIT, https://github.com/Wangnov/codex-asr)
// documents: POST a multipart "file" to /backend-api/transcribe with the
// Codex bearer token and read {"text": "..."} back.
package codexasr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// DefaultEndpoint is where Codex Desktop uploads recordings.
	DefaultEndpoint = "https://chatgpt.com/backend-api/transcribe"

	// DefaultVersion fills the User-Agent when Codex Desktop is not installed
	// under /Applications.
	DefaultVersion = "26.429.30905"

	// UnitMaxSeconds is the longest recording one request may carry. codex-asr
	// reports 11 minutes as reliable, but a 10.5-minute Korean lecture lost a
	// contiguous ~70 s in this machine's test while the same audio cut into
	// 5-minute pieces lost almost nothing (2026-11-07), so the runner cuts
	// shorter.
	UnitMaxSeconds = 5 * 60

	// KeyID is the synthetic key id the runner uses for this backend.
	KeyID = "codex"
)

// Auth failures. Callers turn these into user-facing messages.
var (
	ErrNotFound    = errors.New("codex login not found")
	ErrNoChatGPT   = errors.New("codex login is not a ChatGPT login")
	ErrExpired     = errors.New("codex login expired")
	ErrNoText      = errors.New("codex returned no transcript")
	ErrUnsupported = errors.New("codex does not accept this audio format")
)

// Auth is a local Codex login.
type Auth struct {
	Path        string
	AccessToken string
	AccountID   string
}

// Find reads $CODEX_HOME/auth.json, falling back to ~/.codex/auth.json.
func Find() (Auth, error) {
	dir := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Auth{}, err
		}
		dir = filepath.Join(home, ".codex")
	}
	return ReadFile(filepath.Join(dir, "auth.json"))
}

// ReadFile parses one Codex auth file.
func ReadFile(path string) (Auth, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Auth{}, fmt.Errorf("%w: %s", ErrNotFound, path)
		}
		return Auth{}, err
	}
	return Parse(data, path)
}

// Parse reads the subset of auth.json this package needs. A file using an API
// key instead of a ChatGPT token is rejected: the transcribe endpoint needs
// the token.
func Parse(data []byte, path string) (Auth, error) {
	var f struct {
		AuthMode      string `json:"auth_mode"`
		AuthModeCamel string `json:"authMode"`
		Tokens        *struct {
			AccessToken string `json:"access_token"`
			AccountID   string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return Auth{}, fmt.Errorf("%s: %w", path, err)
	}
	mode := f.AuthMode
	if mode == "" {
		mode = f.AuthModeCamel
	}
	if mode != "" && mode != "chatgpt" && mode != "chatgpt_auth_tokens" {
		return Auth{}, fmt.Errorf("%w: auth mode %q (%s)", ErrNoChatGPT, mode, path)
	}
	var token, account string
	if f.Tokens != nil {
		token = stripBearer(f.Tokens.AccessToken)
		account = strings.TrimSpace(f.Tokens.AccountID)
	}
	if token == "" {
		return Auth{}, fmt.Errorf("%w: %s", ErrNoChatGPT, path)
	}
	if account == "" {
		account = accountIDFromToken(token)
	}
	return Auth{Path: path, AccessToken: token, AccountID: account}, nil
}

// Expiry returns the token's exp claim, when it has one.
func (a Auth) Expiry() (time.Time, bool) {
	exp, ok := jwtClaims(a.AccessToken)["exp"].(float64)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(int64(exp), 0).UTC(), true
}

// Expired reports an exp claim in the past. A token without exp is treated as
// usable and left for the server to reject.
func (a Auth) Expired(now time.Time) bool {
	t, ok := a.Expiry()
	return ok && !now.Before(t)
}

// Client calls the transcribe endpoint. Zero fields are filled by New.
type Client struct {
	Endpoint string
	Auth     Auth
	HTTP     *http.Client
	Version  string
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// New returns a client for auth.
func New(auth Auth) *Client {
	return &Client{
		Endpoint: DefaultEndpoint,
		Auth:     auth,
		HTTP:     &http.Client{Timeout: 5 * time.Minute},
		Version:  DesktopVersion(),
	}
}

// Transcribe uploads one audio file and returns its text.
func (c *Client) Transcribe(ctx context.Context, path, mime string) (string, error) {
	if strings.TrimSpace(c.Auth.AccessToken) == "" {
		return "", fmt.Errorf("%w: %s", ErrNoChatGPT, c.Auth.Path)
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	if c.Auth.Expired(now()) {
		return "", ErrExpired
	}
	contentType, ext, err := uploadType(mime, path)
	if err != nil {
		return "", err
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	version := c.Version
	if version == "" {
		version = DefaultVersion
	}

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	done := make(chan error, 1)
	go func() {
		err := func() error {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeQuotes(uploadName(path, ext))))
			header.Set("Content-Type", contentType)
			part, err := mw.CreatePart(header)
			if err != nil {
				return err
			}
			if _, err := io.Copy(part, f); err != nil {
				return err
			}
			return mw.Close()
		}()
		pw.CloseWithError(err)
		done <- err
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		pr.CloseWithError(err)
		<-done
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.Auth.AccessToken)
	req.Header.Set("originator", "Codex Desktop")
	req.Header.Set("User-Agent", fmt.Sprintf("Codex Desktop/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH))
	if c.Auth.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", c.Auth.AccountID)
	}

	resp, err := client.Do(req)
	// The upload goroutine ends when the request body is read or closed; the
	// response always leaves the pipe in one of those states.
	writeErr := <-done
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return "", readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", &HTTPError{Status: resp.StatusCode, Body: snippet(body)}
	}
	if writeErr != nil {
		return "", writeErr
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("codex response: %w", err)
	}
	if strings.TrimSpace(out.Text) == "" {
		return "", ErrNoText
	}
	return out.Text, nil
}

// HTTPError is a non-2xx answer from the endpoint.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("codex transcribe: HTTP %d", e.Status)
	}
	return fmt.Sprintf("codex transcribe: HTTP %d: %s", e.Status, e.Body)
}

// StatusCode lets gemini.StatusCode read the status without knowing this type.
func (e *HTTPError) StatusCode() int { return e.Status }

// IsAuth reports 401/403: the login is gone or revoked.
func IsAuth(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && (he.Status == 401 || he.Status == 403)
}

// Is429 reports a rate limit.
func Is429(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == 429
}

// IsTransient reports failures worth retrying.
func IsTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var he *HTTPError
	return errors.As(err, &he) && (he.Status == 408 || he.Status >= 500)
}

// DesktopVersion reads Codex Desktop's version from its bundle. Empty when
// the app is not installed.
func DesktopVersion() string {
	const plist = "/Applications/Codex.app/Contents/Info.plist"
	data, err := os.ReadFile(plist)
	if err != nil {
		return ""
	}
	const marker = "<key>CFBundleShortVersionString</key>"
	_, rest, ok := strings.Cut(string(data), marker)
	if !ok {
		return ""
	}
	start := strings.Index(rest, "<string>")
	if start < 0 {
		return ""
	}
	rest = rest[start+len("<string>"):]
	end := strings.Index(rest, "</string>")
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

func uploadType(mime, path string) (contentType, ext string, err error) {
	m := strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	if m == "" || m == "application/octet-stream" {
		m = mimeFromExt(filepath.Ext(path))
	}
	switch m {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "audio/wav", "wav", nil
	case "audio/mpeg", "audio/mp3":
		return "audio/mpeg", "mp3", nil
	case "audio/mp4", "audio/m4a", "audio/x-m4a":
		return "audio/mp4", "m4a", nil
	case "audio/flac", "audio/x-flac":
		return "audio/flac", "flac", nil
	case "audio/ogg":
		return "audio/ogg", "ogg", nil
	case "audio/webm":
		return "audio/webm", "webm", nil
	}
	return "", "", fmt.Errorf("%w: %s", ErrUnsupported, m)
}

func mimeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".mp4":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".webm":
		return "audio/webm"
	}
	return ""
}

func uploadName(path, ext string) string {
	name := filepath.Base(path)
	if strings.EqualFold(filepath.Ext(name), "."+ext) {
		return name
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if stem == "" {
		stem = "audio"
	}
	return stem + "." + ext
}

func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 300 {
		s = s[:300] + "…"
	}
	return s
}

// escapeQuotes makes a file name safe inside a quoted Content-Disposition
// value.
func escapeQuotes(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
}

func stripBearer(token string) string {
	t := strings.TrimSpace(token)
	if rest, ok := strings.CutPrefix(t, "Bearer "); ok {
		t = strings.TrimSpace(rest)
	} else if rest, ok := strings.CutPrefix(t, "bearer "); ok {
		t = strings.TrimSpace(rest)
	}
	return t
}

// jwtClaims decodes a JWT payload without verifying it: the token is already
// the server's, and only exp and the account id are read here.
func jwtClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload := parts[1]
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(payload)
		if err != nil {
			return nil
		}
	}
	var claims map[string]any
	if json.Unmarshal(data, &claims) != nil {
		return nil
	}
	return claims
}

func accountIDFromToken(token string) string {
	auth, ok := jwtClaims(token)["https://api.openai.com/auth"].(map[string]any)
	if !ok {
		return ""
	}
	id, _ := auth["chatgpt_account_id"].(string)
	return strings.TrimSpace(id)
}

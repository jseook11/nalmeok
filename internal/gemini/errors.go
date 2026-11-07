package gemini

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jseook11/nalmeok/internal/i18n"
	"google.golang.org/genai"
)

// The SDK sometimes flattens an APIError into a plain string (for example the
// upload path uses fmt.Errorf("...%s", err)), so the status code is also read
// back from the message.
var codeRe = regexp.MustCompile(`Error (\d{3}), Message:`)

// statusCoder is implemented by errors that carry an HTTP status without
// being a genai.APIError (the codex backend's HTTPError, for example).
type statusCoder interface{ StatusCode() int }

// StatusCode returns the HTTP status carried by err, or 0.
func StatusCode(err error) int {
	if err == nil {
		return 0
	}
	var apiErr genai.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	var apiErrPtr *genai.APIError
	if errors.As(err, &apiErrPtr) && apiErrPtr != nil {
		return apiErrPtr.Code
	}
	var sc statusCoder
	if errors.As(err, &sc) {
		return sc.StatusCode()
	}
	if m := codeRe.FindStringSubmatch(err.Error()); m != nil {
		code, _ := strconv.Atoi(m[1])
		return code
	}
	return 0
}

// Is429 reports a rate-limit or quota error.
func Is429(err error) bool {
	return StatusCode(err) == 429
}

// IsAuth reports an invalid or unauthorized key (401/403, or the 400
// API_KEY_INVALID the Gemini API returns for malformed keys).
func IsAuth(err error) bool {
	code := StatusCode(err)
	if code == 401 || code == 403 {
		return true
	}
	if code == 400 {
		msg := strings.ToLower(err.Error())
		return strings.Contains(msg, "api_key_invalid") || strings.Contains(msg, "api key not valid")
	}
	return false
}

// EmptyResponseError is a live response with no text and no reason: no
// block, no MAX_TOKENS, finish reason empty or STOP. It shows up when the
// server is overloaded, and the same request usually works when sent again,
// so it counts as transient. Usage holds token counts for the log.
type EmptyResponseError struct {
	Finish string
	Usage  string
}

func (e *EmptyResponseError) Error() string {
	finish := e.Finish
	if finish == "" {
		finish = "none"
	}
	return fmt.Sprintf("empty response (finish %s, %s)", finish, e.Usage)
}

// IsEmptyResponse reports an EmptyResponseError.
func IsEmptyResponse(err error) bool {
	var e *EmptyResponseError
	return errors.As(err, &e)
}

// IsNotFound reports a 404: the server does not know the resource.
func IsNotFound(err error) bool {
	return StatusCode(err) == 404
}

// IsTransient reports errors worth retrying: 5xx, 408, timeouts, broken
// connections and empty live responses. Context cancellation is never
// transient.
func IsTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if IsEmptyResponse(err) {
		return true
	}
	code := StatusCode(err)
	if code == 408 || code >= 500 {
		return true
	}
	if code != 0 {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{"connection reset", "broken pipe", "timeout", "eof", "no such host", "tls handshake"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// ValidateKey checks apiKey with a non-billing models.get call. Only a
// rejected key is an error; network trouble lets the key be saved, like the
// Python app's check_key.
func ValidateKey(ctx context.Context, apiKey string) error {
	c, err := New(apiKey)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := c.Validate(ctx); err != nil && IsAuth(err) {
		return i18n.Error("error.invalidKey")
	}
	return nil
}

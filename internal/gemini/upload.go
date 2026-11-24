package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// The Files API resumable upload protocol, done by hand: genai's
// Files.Upload gives up the whole upload on the first failed chunk, and every
// retry starts a new session from byte 0. On campus Wi-Fi a large lecture
// failed six times that way and the item failed after 90 minutes
// (2026-11-24). This uploader keeps one session and, after a failed chunk,
// asks the server how much it holds and continues from there.
const (
	uploadBaseURL = "https://generativelanguage.googleapis.com"
	// uploadChunk is a multiple of 256 KiB, as the protocol requires for
	// every chunk but the last.
	uploadChunk = 8 << 20
	// uploadStalls is how many failed chunks in a row (without the server
	// gaining a byte) one session survives.
	uploadStalls = 8
	// uploadChunkTimeout bounds one chunk request; 8 MiB at 0.2 Mbit/s still
	// fits.
	uploadChunkTimeout = 6 * time.Minute
)

// uploadStallDelay is the wait before retry n (1-based) of a stalled chunk:
// 2, 4, 8, 16, 32, then 60 s.
func uploadStallDelay(n int) time.Duration {
	d := 2 * time.Second << (n - 1)
	if n > 5 || d > time.Minute {
		return time.Minute
	}
	return d
}

// resumable uploads one file with the resumable protocol.
type resumable struct {
	http   *http.Client
	base   string
	apiKey string
	chunk  int64
	sleep  func(ctx context.Context, d time.Duration) error
}

// uploadedFile is the "file" object the finalize response carries.
type uploadedFile struct {
	Name     string `json:"name"`
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType"`
	State    string `json:"state"`
}

// uploadError is an upload failure with the HTTP status, when there was one.
type uploadError struct {
	code int
	msg  string
}

func (e *uploadError) Error() string {
	if e.code == 0 {
		return "upload: " + e.msg
	}
	return fmt.Sprintf("upload: HTTP %d: %s", e.code, e.msg)
}

// StatusCode lets StatusCode, IsTransient and IsAuth classify the failure.
func (e *uploadError) StatusCode() int { return e.code }

func (u *resumable) upload(ctx context.Context, path, mime string) (uploadedFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return uploadedFile{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return uploadedFile{}, err
	}
	size := info.Size()
	sessionURL, granularity, err := u.start(ctx, filepath.Base(path), mime, size)
	if err != nil {
		return uploadedFile{}, err
	}

	buf := make([]byte, chunkSize(u.chunk, granularity))
	var offset int64
	stalls := 0
	for {
		n, err := f.ReadAt(buf, offset)
		if err != nil && !errors.Is(err, io.EOF) {
			return uploadedFile{}, err
		}
		last := offset+int64(n) >= size
		file, done, cerr := u.send(ctx, sessionURL, buf[:n], offset, last)
		if cerr == nil {
			if done {
				return file, nil
			}
			offset += int64(n)
			stalls = 0
			continue
		}
		if ctx.Err() != nil {
			return uploadedFile{}, ctx.Err()
		}
		if !resumableFailure(cerr) {
			return uploadedFile{}, cerr
		}
		stalls++
		if stalls > uploadStalls {
			return uploadedFile{}, cerr
		}
		if err := u.sleep(ctx, uploadStallDelay(stalls)); err != nil {
			return uploadedFile{}, err
		}
		// The chunk may have arrived in part, or whole with the reply lost.
		got, file, final, qerr := u.query(ctx, sessionURL)
		if qerr != nil {
			if ctx.Err() != nil {
				return uploadedFile{}, ctx.Err()
			}
			if !resumableFailure(qerr) {
				return uploadedFile{}, qerr
			}
			continue // still offline: resend from the same offset
		}
		if final {
			return file, nil
		}
		if got > offset {
			stalls = 0
		}
		if got < 0 || got > size {
			return uploadedFile{}, &uploadError{msg: fmt.Sprintf("server holds %d of %d bytes", got, size)}
		}
		offset = got
	}
}

// resumableFailure reports a chunk failure worth resuming after: no answer,
// a timeout, or a 5xx/408/429. Anything else (4xx) means the session is gone.
func resumableFailure(err error) bool {
	if code := StatusCode(err); code != 0 {
		return code == 408 || code == 429 || code >= 500
	}
	return IsTransient(err) || errors.Is(err, context.DeadlineExceeded)
}

// chunkSize is want rounded down to a multiple of the server's granularity
// (8 MiB on the Files API, 2026-11), and never below one granule.
func chunkSize(want, granularity int64) int64 {
	if granularity <= 0 {
		return want
	}
	if want < granularity {
		return granularity
	}
	return want / granularity * granularity
}

// start opens a session and returns its URL and the chunk granularity the
// server asks for (0 when it names none).
func (u *resumable) start(ctx context.Context, name, mime string, size int64) (string, int64, error) {
	body, _ := json.Marshal(map[string]any{"file": map[string]string{"display_name": name}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.base+"/upload/v1beta/files", bytes.NewReader(body))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("x-goog-api-key", u.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Upload-Protocol", "resumable")
	req.Header.Set("X-Goog-Upload-Command", "start")
	req.Header.Set("X-Goog-Upload-Header-Content-Length", strconv.FormatInt(size, 10))
	req.Header.Set("X-Goog-Upload-Header-Content-Type", mime)
	resp, err := u.http.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, responseError(resp)
	}
	sessionURL := resp.Header.Get("X-Goog-Upload-URL")
	if sessionURL == "" {
		return "", 0, &uploadError{msg: "no upload URL in the start response"}
	}
	granularity, _ := strconv.ParseInt(resp.Header.Get("X-Goog-Upload-Chunk-Granularity"), 10, 64)
	return sessionURL, granularity, nil
}

// send uploads one chunk at offset. done is true once the server finalized
// the file, which the last chunk asks for.
func (u *resumable) send(ctx context.Context, sessionURL string, chunk []byte, offset int64, last bool) (uploadedFile, bool, error) {
	cctx, cancel := context.WithTimeout(ctx, uploadChunkTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, sessionURL, bytes.NewReader(chunk))
	if err != nil {
		return uploadedFile{}, false, err
	}
	command := "upload"
	if last {
		command = "upload, finalize"
	}
	req.Header.Set("x-goog-api-key", u.apiKey)
	req.Header.Set("X-Goog-Upload-Command", command)
	req.Header.Set("X-Goog-Upload-Offset", strconv.FormatInt(offset, 10))
	req.ContentLength = int64(len(chunk))
	resp, err := u.http.Do(req)
	if err != nil {
		return uploadedFile{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return uploadedFile{}, false, responseError(resp)
	}
	switch status := resp.Header.Get("X-Goog-Upload-Status"); status {
	case "final":
		file, err := decodeFile(resp.Body)
		return file, true, err
	case "active":
		if last {
			return uploadedFile{}, false, &uploadError{msg: "finalize did not finish the upload"}
		}
		return uploadedFile{}, false, nil
	default:
		return uploadedFile{}, false, &uploadError{msg: "upload status " + strconv.Quote(status)}
	}
}

// query asks how many bytes the session holds. final is true when the upload
// already finished (the finalize reply was lost); file is then the result.
func (u *resumable) query(ctx context.Context, sessionURL string) (int64, uploadedFile, bool, error) {
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, sessionURL, nil)
	if err != nil {
		return 0, uploadedFile{}, false, err
	}
	req.Header.Set("x-goog-api-key", u.apiKey)
	req.Header.Set("X-Goog-Upload-Command", "query")
	resp, err := u.http.Do(req)
	if err != nil {
		return 0, uploadedFile{}, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, uploadedFile{}, false, responseError(resp)
	}
	switch status := resp.Header.Get("X-Goog-Upload-Status"); status {
	case "final":
		file, err := decodeFile(resp.Body)
		return 0, file, true, err
	case "active":
		got, err := strconv.ParseInt(resp.Header.Get("X-Goog-Upload-Size-Received"), 10, 64)
		if err != nil {
			return 0, uploadedFile{}, false, &uploadError{msg: "query without a received size"}
		}
		return got, uploadedFile{}, false, nil
	default:
		return 0, uploadedFile{}, false, &uploadError{msg: "upload status " + strconv.Quote(status)}
	}
}

func decodeFile(r io.Reader) (uploadedFile, error) {
	var body struct {
		File uploadedFile `json:"file"`
	}
	if err := json.NewDecoder(r).Decode(&body); err != nil {
		return uploadedFile{}, &uploadError{msg: "unreadable finalize response: " + err.Error()}
	}
	if body.File.Name == "" {
		return uploadedFile{}, &uploadError{msg: "finalize response without a file"}
	}
	return body.File, nil
}

// responseError reads a non-200 reply. The message is the API's own, never
// the request (which holds the key in a header, not the URL).
func responseError(resp *http.Response) error {
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(data))
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &body) == nil && body.Error.Message != "" {
		msg = body.Error.Message
	}
	if msg == "" {
		msg = resp.Status
	}
	return &uploadError{code: resp.StatusCode, msg: msg}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

package gemini

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeUploadServer speaks the resumable protocol. failures lists what the
// next chunk requests do: "partial" keeps half the chunk and answers 503,
// "drop" closes the connection without keeping anything, "gone" answers 404.
type fakeUploadServer struct {
	t        *testing.T
	mu       sync.Mutex
	starts   int
	queries  int
	got      []byte
	final    bool
	failures []string
}

func (s *fakeUploadServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Header.Get("x-goog-api-key") != "k" {
		s.t.Errorf("missing api key on %s", r.URL.Path)
	}
	switch cmd := r.Header.Get("X-Goog-Upload-Command"); {
	case cmd == "start":
		s.starts++
		w.Header().Set("X-Goog-Upload-URL", "http://"+r.Host+"/session")
		w.Header().Set("X-Goog-Upload-Chunk-Granularity", "8")
		return
	case cmd == "query":
		s.queries++
		if s.final {
			w.Header().Set("X-Goog-Upload-Status", "final")
			io.WriteString(w, `{"file":{"name":"files/x","uri":"u","mimeType":"audio/m4a","state":"ACTIVE"}}`)
			return
		}
		w.Header().Set("X-Goog-Upload-Status", "active")
		w.Header().Set("X-Goog-Upload-Size-Received", strconv.Itoa(len(s.got)))
		return
	case strings.HasPrefix(cmd, "upload"):
		body, _ := io.ReadAll(r.Body)
		off, _ := strconv.Atoi(r.Header.Get("X-Goog-Upload-Offset"))
		if off != len(s.got) {
			s.t.Errorf("chunk at offset %d, server holds %d", off, len(s.got))
			w.WriteHeader(400)
			return
		}
		if len(s.failures) > 0 {
			f := s.failures[0]
			s.failures = s.failures[1:]
			switch f {
			case "partial":
				s.got = append(s.got, body[:len(body)/2]...)
				w.WriteHeader(503)
				return
			case "drop":
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			case "gone":
				w.WriteHeader(404)
				io.WriteString(w, `{"error":{"message":"session not found"}}`)
				return
			}
		}
		s.got = append(s.got, body...)
		if strings.Contains(cmd, "finalize") {
			s.final = true
			w.Header().Set("X-Goog-Upload-Status", "final")
			io.WriteString(w, `{"file":{"name":"files/x","uri":"u","mimeType":"audio/m4a","state":"ACTIVE"}}`)
			return
		}
		w.Header().Set("X-Goog-Upload-Status", "active")
	}
}

func uploadFixture(t *testing.T, failures ...string) (*fakeUploadServer, *resumable, string, []byte, *[]time.Duration) {
	t.Helper()
	data := []byte(strings.Repeat("0123456789", 10)) // 100 bytes, chunks of 16
	path := filepath.Join(t.TempDir(), "강의.m4a")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &fakeUploadServer{t: t, failures: failures}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	var sleeps []time.Duration
	u := &resumable{http: ts.Client(), base: ts.URL, apiKey: "k", chunk: 16, sleep: func(_ context.Context, d time.Duration) error {
		sleeps = append(sleeps, d)
		return nil
	}}
	return srv, u, path, data, &sleeps
}

func TestResumableUploadInOneGo(t *testing.T) {
	srv, u, path, data, _ := uploadFixture(t)
	file, err := u.upload(context.Background(), path, "audio/m4a")
	if err != nil {
		t.Fatal(err)
	}
	if file.Name != "files/x" || file.URI != "u" {
		t.Errorf("file = %+v", file)
	}
	if string(srv.got) != string(data) || srv.starts != 1 || srv.queries != 0 {
		t.Errorf("got %d bytes, %d starts, %d queries", len(srv.got), srv.starts, srv.queries)
	}
}

func TestResumableUploadResumesFromServerOffset(t *testing.T) {
	// A chunk arrives in part, then the connection drops twice: the same
	// session continues from what the server holds.
	srv, u, path, data, sleeps := uploadFixture(t, "partial", "drop", "drop")
	if _, err := u.upload(context.Background(), path, "audio/m4a"); err != nil {
		t.Fatal(err)
	}
	if string(srv.got) != string(data) {
		t.Errorf("server holds %q, want the file", srv.got)
	}
	if srv.starts != 1 {
		t.Errorf("starts = %d, want 1 (one session)", srv.starts)
	}
	if srv.queries != 3 {
		t.Errorf("queries = %d, want 3", srv.queries)
	}
	if len(*sleeps) != 3 {
		t.Errorf("sleeps = %v", *sleeps)
	}
}

func TestResumableUploadGivesUpAfterStalls(t *testing.T) {
	fails := make([]string, uploadStalls+1)
	for i := range fails {
		fails[i] = "drop"
	}
	srv, u, path, _, _ := uploadFixture(t, fails...)
	_, err := u.upload(context.Background(), path, "audio/m4a")
	if err == nil {
		t.Fatal("upload succeeded through endless drops")
	}
	if !IsTransient(err) {
		t.Errorf("error %v is not transient; the runner should retry it", err)
	}
	if srv.starts != 1 {
		t.Errorf("starts = %d", srv.starts)
	}
}

func TestResumableUploadStopsOnLostSession(t *testing.T) {
	_, u, path, _, sleeps := uploadFixture(t, "gone")
	_, err := u.upload(context.Background(), path, "audio/m4a")
	if StatusCode(err) != 404 {
		t.Fatalf("err = %v, want the 404", err)
	}
	if len(*sleeps) != 0 {
		t.Errorf("sleeps = %v, want none for a lost session", *sleeps)
	}
}

func TestChunkSizeFollowsGranularity(t *testing.T) {
	cases := []struct{ want, gran, got int64 }{
		{8 << 20, 0, 8 << 20},
		{8 << 20, 8 << 20, 8 << 20},
		{256 << 10, 8 << 20, 8 << 20},
		{20 << 20, 8 << 20, 16 << 20},
	}
	for _, c := range cases {
		if got := chunkSize(c.want, c.gran); got != c.got {
			t.Errorf("chunkSize(%d, %d) = %d, want %d", c.want, c.gran, got, c.got)
		}
	}
}

func TestUploadStallDelay(t *testing.T) {
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute}
	for i, w := range want {
		if got := uploadStallDelay(i + 1); got != w {
			t.Errorf("delay %d = %v, want %v", i+1, got, w)
		}
	}
}

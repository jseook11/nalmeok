// Package store keeps the processing history in items.json.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ErrNotFound is returned when an item ID does not exist.
var ErrNotFound = errors.New("항목을 찾을 수 없습니다")

// Item is one dropped file or folder. The JSON field names are the contract
// shared with the frontend and the future runner.
type Item struct {
	ID        string    `json:"ID"`
	Source    string    `json:"Source"`
	Kind      string    `json:"Kind"`
	Name      string    `json:"Name"`
	Mode      string    `json:"Mode"`
	State     string    `json:"State"`
	Progress  float64   `json:"Progress"`
	Detail    string    `json:"Detail"`
	Output    string    `json:"Output"`
	AudioSec  float64   `json:"AudioSec"`
	CreatedAt time.Time `json:"CreatedAt"`
	UpdatedAt time.Time `json:"UpdatedAt"`
	Units     []Unit    `json:"Units"`
	Batch     *BatchRef `json:"Batch"`
}

// Unit is one transcription request and its result.
type Unit struct {
	Index   int      `json:"Index"`
	Files   []string `json:"Files"`
	Seconds float64  `json:"Seconds"`
	Text    string   `json:"Text"`
}

// BatchRef identifies a submitted Gemini Batch job.
type BatchRef struct {
	KeyID       string            `json:"KeyID"`
	Name        string            `json:"Name"`
	SubmittedAt time.Time         `json:"SubmittedAt"`
	Uploads     map[string]Upload `json:"Uploads"`
}

// Upload records a file uploaded for a Batch job.
type Upload struct {
	URI        string    `json:"URI"`
	MIME       string    `json:"MIME"`
	UploadedAt time.Time `json:"UploadedAt"`
}

// Store is a concurrency-safe collection persisted atomically.
type Store struct {
	mu       sync.RWMutex
	path     string
	items    []Item
	onChange func()
}

// Load opens the store at path. A missing or unreadable file starts empty, so
// a damaged history never prevents the app from launching.
func Load(path string) (*Store, error) {
	s := &Store{path: path, items: []Item{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return s, nil
		}
		return s, nil
	}

	var file struct {
		Items []Item `json:"items"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		return s, nil
	}
	if file.Items != nil {
		s.items = file.Items
	}
	return s, nil
}

// SetOnChange registers a callback invoked after every successful mutation.
// The callback runs outside the store lock.
func (s *Store) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// Add appends an item, filling in an ID and timestamps when they are zero.
func (s *Store) Add(item Item) error {
	now := time.Now()
	if item.ID == "" {
		item.ID = NewID()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = now
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = item.CreatedAt
	}

	s.mu.Lock()
	s.items = append(s.items, item)
	err := s.saveLocked()
	if err != nil {
		s.items = s.items[:len(s.items)-1]
	}
	s.mu.Unlock()
	if err == nil {
		s.notify()
	}
	return err
}

// Update mutates one item inside the lock and persists the change. It stamps
// UpdatedAt automatically.
func (s *Store) Update(id string, fn func(*Item)) error {
	s.mu.Lock()
	index := s.indexLocked(id)
	if index < 0 {
		s.mu.Unlock()
		return ErrNotFound
	}
	previous := cloneItem(s.items[index])
	fn(&s.items[index])
	s.items[index].UpdatedAt = time.Now()
	err := s.saveLocked()
	if err != nil {
		s.items[index] = previous
	}
	s.mu.Unlock()
	if err == nil {
		s.notify()
	}
	return err
}

// Remove drops an item from the history. It never touches the source file.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	index := s.indexLocked(id)
	if index < 0 {
		s.mu.Unlock()
		return ErrNotFound
	}
	previous := s.items
	remaining := make([]Item, 0, len(s.items)-1)
	remaining = append(remaining, s.items[:index]...)
	remaining = append(remaining, s.items[index+1:]...)
	s.items = remaining
	err := s.saveLocked()
	if err != nil {
		s.items = previous
	}
	s.mu.Unlock()
	if err == nil {
		s.notify()
	}
	return err
}

// Get returns a copy of one item.
func (s *Store) Get(id string) (Item, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	index := s.indexLocked(id)
	if index < 0 {
		return Item{}, false
	}
	return cloneItem(s.items[index]), true
}

// List returns items newest first. A non-empty query filters by a
// case-insensitive substring of Name or Source.
func (s *Store) List(query string) []Item {
	query = strings.ToLower(strings.TrimSpace(query))

	s.mu.RLock()
	items := make([]Item, 0, len(s.items))
	for _, item := range s.items {
		if query != "" &&
			!strings.Contains(strings.ToLower(item.Name), query) &&
			!strings.Contains(strings.ToLower(item.Source), query) {
			continue
		}
		items = append(items, cloneItem(item))
	}
	s.mu.RUnlock()

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items
}

// Save writes the current history to disk. It is mainly useful for tests and
// callers that need to flush after batch mutations.
func (s *Store) Save() error {
	s.mu.Lock()
	err := s.saveLocked()
	s.mu.Unlock()
	return err
}

func (s *Store) indexLocked(id string) int {
	for i := range s.items {
		if s.items[i].ID == id {
			return i
		}
	}
	return -1
}

func (s *Store) saveLocked() error {
	items := s.items
	if items == nil {
		items = []Item{}
	}
	data, err := json.MarshalIndent(struct {
		Items []Item `json:"items"`
	}{Items: items}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAtomic(s.path, data, 0o600)
}

func (s *Store) notify() {
	s.mu.RLock()
	fn := s.onChange
	s.mu.RUnlock()
	if fn != nil {
		fn()
	}
}

func cloneItem(item Item) Item {
	if item.Units != nil {
		units := make([]Unit, len(item.Units))
		copy(units, item.Units)
		for i := range units {
			if units[i].Files != nil {
				units[i].Files = append([]string(nil), units[i].Files...)
			}
		}
		item.Units = units
	}
	if item.Batch != nil {
		batch := *item.Batch
		batch.Uploads = maps.Clone(item.Batch.Uploads)
		item.Batch = &batch
	}
	return item
}

var idCounter atomic.Uint64

// NewID returns a time-based unique identifier.
func NewID() string {
	return fmt.Sprintf("%d-%03d", time.Now().UTC().UnixNano(), idCounter.Add(1)%1000)
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".nalmeok-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}

	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		if err := os.Rename(tmpName, path); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
	}
	_ = os.Remove(tmpName)
	return lastErr
}

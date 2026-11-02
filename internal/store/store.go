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
	"sync"
	"sync/atomic"
	"time"

	"github.com/jseook11/nalmeok/internal/i18n"
)

// FormatVersion is written to items.json as "version". Files without it are
// version 1; new fields with empty defaults do not need a bump.
const FormatVersion = 1

// ErrNotFound is returned when an item ID does not exist.
var ErrNotFound = i18n.Error("store.notFound")

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
	// KeyID pins the item to one saved key. Empty means automatic: keys are
	// tried in priority order.
	KeyID string `json:"KeyID"`
	// SourceSig is the size and modification time of the source audio when
	// its pieces were prepared. A change means finished units and uploads
	// belong to old audio.
	SourceSig string `json:"SourceSig,omitempty"`
	// StartedAt is when the current run began; each retry starts over.
	StartedAt time.Time `json:"StartedAt,omitzero"`
	// FinishedAt is when the item was done. Renaming does not change it,
	// unlike UpdatedAt. Older items lack it; the window falls back to
	// UpdatedAt.
	FinishedAt time.Time `json:"FinishedAt,omitzero"`
	// EstimateKRW is what the whole audio would cost on the paid Batch tier,
	// rounded to won. Free items cost nothing, so the window shows it only
	// for paid items. 0 until the audio length is known.
	EstimateKRW int `json:"EstimateKRW,omitempty"`
	// Usage sums the tokens Gemini reported for the item's requests, and
	// CostKRW is the paid cost computed from it. CostKRW is nil when unknown
	// (items finished before usage was recorded).
	Usage   *Usage `json:"Usage,omitempty"`
	CostKRW *int   `json:"CostKRW,omitempty"`
	// PDFChosen marks that the user picked the folder's reference PDFs when
	// adding it; PDFs is that choice (empty = transcribe without a PDF).
	// Retries keep using it. Without a choice every PDF in the folder is
	// used.
	PDFChosen bool     `json:"PDFChosen,omitempty"`
	PDFs      []string `json:"PDFs,omitempty"`
}

// Usage is the token count Gemini reported (usageMetadata). Text is every
// prompt token that is not audio (prompt text, PDF). Output is the answer
// and Thoughts the thinking; both are billed at the output rate. Batch marks
// requests that ran on the paid Batch tier.
type Usage struct {
	Audio    int  `json:"Audio,omitempty"`
	Text     int  `json:"Text,omitempty"`
	Output   int  `json:"Output,omitempty"`
	Thoughts int  `json:"Thoughts,omitempty"`
	Batch    bool `json:"Batch,omitempty"`
}

// Empty reports whether no tokens were recorded.
func (u Usage) Empty() bool {
	return u.Audio == 0 && u.Text == 0 && u.Output == 0 && u.Thoughts == 0
}

// Unit is one transcription request and its result. MIMEs runs parallel to
// Files so uploads keep the MIME the media package chose (audio/m4a etc.).
type Unit struct {
	Index   int      `json:"Index"`
	Files   []string `json:"Files"`
	MIMEs   []string `json:"MIMEs,omitempty"`
	Seconds float64  `json:"Seconds"`
	Text    string   `json:"Text"`
	// Usage is what the request that produced Text reported. Nil while the
	// unit is unfinished or when it finished before usage was recorded.
	Usage *Usage `json:"Usage,omitempty"`
}

// BatchRef identifies a submitted Gemini Batch job.
type BatchRef struct {
	KeyID       string            `json:"KeyID"`
	Name        string            `json:"Name"`
	SubmittedAt time.Time         `json:"SubmittedAt"`
	Uploads     map[string]Upload `json:"Uploads"`
	// Marker is the display name of a job whose creation was sent but whose
	// name never came back. The job may exist, so it is looked up before
	// another one is created.
	Marker string `json:"Marker,omitempty"`
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
// substring of Name or Source, ignoring case and Unicode normalisation and
// accepting a Hangul syllable still being composed (see match.go).
func (s *Store) List(query string) []Item {
	m := newMatcher(query)

	s.mu.RLock()
	items := make([]Item, 0, len(s.items))
	for _, item := range s.items {
		if !m.empty() && !m.match(item.Name) && !m.match(item.Source) {
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
		Version int    `json:"version"`
		Items   []Item `json:"items"`
	}{Version: FormatVersion, Items: items}, "", "  ")
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
			if units[i].Usage != nil {
				u := *units[i].Usage
				units[i].Usage = &u
			}
		}
		item.Units = units
	}
	if item.PDFs != nil {
		item.PDFs = append([]string(nil), item.PDFs...)
	}
	if item.Usage != nil {
		u := *item.Usage
		item.Usage = &u
	}
	if item.CostKRW != nil {
		c := *item.CostKRW
		item.CostKRW = &c
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

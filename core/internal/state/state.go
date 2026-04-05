package state

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Entry struct {
	Offset      int64 `json:"offset"`
	LastUnixSec int64 `json:"last_unix_sec"`
}

type Data struct {
	Version string           `json:"version"`
	Files   map[string]Entry `json:"files"`
}

type Store struct {
	path string
	mu   sync.Mutex
	data Data
}

func Open(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: Data{Version: "1", Files: map[string]Entry{}},
	}
	if err := s.load(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var d Data
	if err := json.Unmarshal(b, &d); err != nil {
		// Corrupted state: recover as first-run equivalent.
		s.data = Data{Version: "1", Files: map[string]Entry{}}
		return nil
	}
	if d.Files == nil {
		d.Files = map[string]Entry{}
	}
	s.data = d
	return nil
}

func (s *Store) Get(path string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data.Files[path]
	return e, ok
}

func (s *Store) Set(path string, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Files[path] = Entry{
		Offset:      offset,
		LastUnixSec: time.Now().Unix(),
	}
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}

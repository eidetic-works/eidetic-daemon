package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// State persists per-file consumed offsets across daemon restarts.
//
// Layout:
//
//	{
//	  "claude_code": {"/abs/path/to/session.jsonl": 12345},
//	  "cursor":      {"/abs/path/to/state.json": 9182734092834}
//	}
//
// Atomic-rename pattern: write to "state.json.tmp", fsync, rename to
// "state.json". Caller controls when to flush via Save (we flush after each
// successful batch commit).
type State struct {
	mu    sync.Mutex
	data  map[string]map[string]int64
	path  string
	dirty bool
}

// LoadState reads the state file at `path`. Returns an empty State if the
// file does not exist (first run).
func LoadState(path string) (*State, error) {
	s := &State{
		data: map[string]map[string]int64{},
		path: path,
	}
	buf, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	if len(buf) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(buf, &s.data); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	return s, nil
}

// Get returns the recorded offset for (surface, file). 0 if unknown.
func (s *State) Get(surface, file string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.data[surface]; ok {
		return m[file]
	}
	return 0
}

// Set records a new offset and marks the state dirty.
func (s *State) Set(surface, file string, offset int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data[surface]; !ok {
		s.data[surface] = map[string]int64{}
	}
	if s.data[surface][file] == offset {
		return
	}
	s.data[surface][file] = offset
	s.dirty = true
}

// Save flushes to disk via atomic rename. Cheap no-op if not dirty.
func (s *State) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dirty {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

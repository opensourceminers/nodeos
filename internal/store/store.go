// Package store persists runtime-mutable state (registered miners, pool
// settings) as a single JSON file with atomic writes.
package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"nodeos/internal/config"
)

type PersistedMiner struct {
	Host   string `json:"host"`             // ip or ip:port
	Source string `json:"source"`           // "scan" | "manual" | "sim"
	Name   string `json:"name,omitempty"`   // user-assigned label
}

type State struct {
	Miners []PersistedMiner `json:"miners"`
	Pool   config.Pool      `json:"pool"`
}

type Store struct {
	mu    sync.Mutex
	path  string
	state State
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dataDir, "state.json")}
	b, err := os.ReadFile(s.path)
	if err == nil {
		_ = json.Unmarshal(b, &s.state) // corrupt state falls back to empty
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) Get() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := s.state
	cp.Miners = append([]PersistedMiner(nil), s.state.Miners...)
	return cp
}

func (s *Store) Update(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.state)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

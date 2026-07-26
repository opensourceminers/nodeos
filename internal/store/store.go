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

// WorkSettings is the user-facing configuration of the solo-mining work
// engine; edited in the UI, persisted here.
type WorkSettings struct {
	Enabled       bool   `json:"enabled"`
	PayoutAddress string `json:"payout_address"`
	// Mode is "solo" (non-pooled, blocks pay the payout address directly) or
	// "ocean" (pooled via OCEAN's DATUM protocol, templates still built here).
	Mode string `json:"mode"`
	// AutoSwitch points the whole fleet at the engine once the node is synced
	// and the gateway is healthy.
	AutoSwitch bool `json:"auto_switch"`
}

// AuthState holds the admin password hash (PBKDF2-HMAC-SHA256, hex encoded).
type AuthState struct {
	Salt string `json:"salt"`
	Hash string `json:"hash"`
	Iter int    `json:"iter"`
}

type State struct {
	Miners []PersistedMiner `json:"miners"`
	Pool   config.Pool      `json:"pool"`
	Work   WorkSettings     `json:"work"`
	// ExternalPool remembers the pool that was active before the fleet was
	// switched to the work engine, so "switch back" can restore it.
	ExternalPool *config.Pool `json:"external_pool,omitempty"`
	Auth         *AuthState   `json:"auth,omitempty"`
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
	// 0600: the state now contains the password hash and pool credentials
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Package auth protects the web UI with a single admin password. The hash is
// PBKDF2-HMAC-SHA256 (stdlib-only implementation), sessions are random tokens
// held in memory — a daemon restart logs everyone out, which is fine for an
// appliance.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"nodeos/internal/store"
)

const (
	iterations  = 210_000 // OWASP recommendation for PBKDF2-SHA256
	keyLen      = 32
	saltLen     = 16
	sessionTTL  = 7 * 24 * time.Hour
	cookieName  = "nodeos_session"
	minPassword = 8
)

// pbkdf2 is PBKDF2-HMAC-SHA256 per RFC 2898; small enough to not warrant a
// dependency.
func pbkdf2(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	blocks := (keyLen + hashLen - 1) / hashLen
	var out []byte
	buf := make([]byte, 4)
	for block := 1; block <= blocks; block++ {
		prf.Reset()
		prf.Write(salt)
		binary.BigEndian.PutUint32(buf, uint32(block))
		prf.Write(buf)
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for i := 1; i < iter; i++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for j := range t {
				t[j] ^= u[j]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

type Manager struct {
	store    *store.Store
	disabled bool

	mu       sync.Mutex
	sessions map[string]time.Time // token → expiry
}

func New(st *store.Store, disabled bool) *Manager {
	return &Manager{store: st, disabled: disabled, sessions: map[string]time.Time{}}
}

func (m *Manager) Disabled() bool { return m.disabled }

// SetupRequired reports whether no password has been set yet.
func (m *Manager) SetupRequired() bool {
	if m.disabled {
		return false
	}
	return m.store.Get().Auth == nil
}

// SetPassword hashes and persists a new password.
func (m *Manager) SetPassword(password string) error {
	if len(password) < minPassword {
		return fmt.Errorf("password must be at least %d characters", minPassword)
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash := pbkdf2([]byte(password), salt, iterations, keyLen)
	return m.store.Update(func(s *store.State) {
		s.Auth = &store.AuthState{
			Salt: hex.EncodeToString(salt),
			Hash: hex.EncodeToString(hash),
			Iter: iterations,
		}
	})
}

// CheckPassword verifies a password in constant time.
func (m *Manager) CheckPassword(password string) bool {
	a := m.store.Get().Auth
	if a == nil {
		return false
	}
	salt, err1 := hex.DecodeString(a.Salt)
	want, err2 := hex.DecodeString(a.Hash)
	if err1 != nil || err2 != nil || a.Iter <= 0 {
		return false
	}
	got := pbkdf2([]byte(password), salt, a.Iter, len(want))
	return hmac.Equal(got, want)
}

// Login verifies the password and, on success, issues a session cookie.
func (m *Manager) Login(w http.ResponseWriter, password string) error {
	if !m.CheckPassword(password) {
		time.Sleep(500 * time.Millisecond) // blunt brute-force damper
		return fmt.Errorf("wrong password")
	}
	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	token := hex.EncodeToString(tok)
	m.mu.Lock()
	m.sessions[token] = time.Now().Add(sessionTTL)
	m.pruneLocked()
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode,
		MaxAge: int(sessionTTL.Seconds()),
	})
	return nil
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		m.mu.Lock()
		delete(m.sessions, c.Value)
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1})
}

// Authenticated reports whether the request carries a valid session.
func (m *Manager) Authenticated(r *http.Request) bool {
	if m.disabled {
		return true
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(m.sessions, c.Value)
		return false
	}
	return true
}

func (m *Manager) pruneLocked() {
	now := time.Now()
	for t, exp := range m.sessions {
		if now.After(exp) {
			delete(m.sessions, t)
		}
	}
}

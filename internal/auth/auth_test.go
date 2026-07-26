package auth

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"nodeos/internal/store"
)

// RFC 6070-style known vector (PBKDF2-HMAC-SHA256, from RFC 7914 test data).
func TestPBKDF2Vector(t *testing.T) {
	got := pbkdf2([]byte("passwd"), []byte("salt"), 1, 64)
	want, _ := hex.DecodeString(
		"55ac046e56e3089fec1691c22544b605f94185216dde0465e68b9d57c20dacbc" +
			"49ca9cccf179b645991664b39d77ef317c71b845b1e30bd509112041d3a19783")
	if !bytes.Equal(got, want) {
		t.Fatalf("pbkdf2 vector mismatch\ngot  %x\nwant %x", got, want)
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(st, false)
}

func TestPasswordLifecycle(t *testing.T) {
	m := newTestManager(t)
	if !m.SetupRequired() {
		t.Fatal("fresh store must require setup")
	}
	if err := m.SetPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := m.SetPassword("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if m.SetupRequired() {
		t.Fatal("setup still required after SetPassword")
	}
	if !m.CheckPassword("correct horse battery") {
		t.Fatal("correct password rejected")
	}
	if m.CheckPassword("wrong") {
		t.Fatal("wrong password accepted")
	}
}

func TestSessionFlow(t *testing.T) {
	m := newTestManager(t)
	if err := m.SetPassword("correct horse battery"); err != nil {
		t.Fatal(err)
	}

	// login sets a cookie
	rec := httptest.NewRecorder()
	if err := m.Login(rec, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieName {
		t.Fatalf("expected session cookie, got %v", cookies)
	}

	// cookie authenticates
	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.AddCookie(cookies[0])
	if !m.Authenticated(req) {
		t.Fatal("valid session rejected")
	}

	// no cookie → rejected
	if m.Authenticated(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("request without cookie accepted")
	}

	// logout invalidates
	m.Logout(httptest.NewRecorder(), req)
	if m.Authenticated(req) {
		t.Fatal("session survived logout")
	}
}

func TestDisabledMode(t *testing.T) {
	st, _ := store.Open(t.TempDir())
	m := New(st, true)
	if m.SetupRequired() {
		t.Fatal("disabled auth must not require setup")
	}
	if !m.Authenticated(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("disabled auth must accept everything")
	}
}

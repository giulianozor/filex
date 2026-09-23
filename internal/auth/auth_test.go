package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCreateAndGet(t *testing.T) {
	s := NewStore()
	token, err := s.Create("alice")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
	sess := s.Get(token)
	if sess == nil {
		t.Fatal("expected session to be found")
	}
	if sess.Username != "alice" {
		t.Fatalf("expected alice, got %s", sess.Username)
	}
}

func TestDelete(t *testing.T) {
	s := NewStore()
	token, _ := s.Create("bob")
	s.Delete(token)
	if s.Get(token) != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestFromRequest(t *testing.T) {
	s := NewStore()
	token, _ := s.Create("carol")

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: token})

	sess := s.FromRequest(req)
	if sess == nil {
		t.Fatal("expected session from request")
	}
	if sess.Username != "carol" {
		t.Fatalf("expected carol, got %s", sess.Username)
	}
}

func TestExpiredSession(t *testing.T) {
	s := NewStore()
	token, _ := s.Create("dave")
	// Manually expire the session
	s.mu.Lock()
	s.sessions[token].CreatedAt = time.Now().Add(-DefaultSessionTTL - time.Minute)
	s.mu.Unlock()

	if s.Get(token) != nil {
		t.Fatal("expected nil for expired session")
	}
}

// TestPersistenceRoundTrip verifies that a remember-me (persistent) session
// created with NewStoreWithPath survives a simulated service restart: after
// creating the store, writing a persistent session, and re-opening the same
// file in a fresh store, the session should still be retrievable.
func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	s1 := NewStoreWithPath(path)
	token, err := s1.CreatePersistentWithTTL("eve", DefaultSessionTTL)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate restart: create a new store from the same file.
	s2 := NewStoreWithPath(path)
	sess := s2.Get(token)
	if sess == nil {
		t.Fatal("expected persistent session to survive restart")
	}
	if sess.Username != "eve" {
		t.Fatalf("expected eve, got %s", sess.Username)
	}
	if !sess.Persist {
		t.Fatal("expected Persist=true for loaded session")
	}
}

// TestNonPersistentSessionNotSaved verifies that a regular (non-remember-me)
// session is NOT written to disk and is therefore lost on restart.
func TestNonPersistentSessionNotSaved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	s1 := NewStoreWithPath(path)
	token, err := s1.CreateWithTTL("frank", DefaultSessionTTL)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate restart.
	s2 := NewStoreWithPath(path)
	if s2.Get(token) != nil {
		t.Fatal("expected non-persistent session to be absent after restart")
	}
}

// TestPersistenceDeleteRemovesFromDisk verifies that deleting a persistent
// session removes it from the on-disk store so it won't be loaded on restart.
func TestPersistenceDeleteRemovesFromDisk(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	s1 := NewStoreWithPath(path)
	token, err := s1.CreatePersistentWithTTL("grace", DefaultSessionTTL)
	if err != nil {
		t.Fatal(err)
	}

	// Delete the session and simulate restart.
	s1.Delete(token)

	s2 := NewStoreWithPath(path)
	if s2.Get(token) != nil {
		t.Fatal("expected deleted persistent session to be absent after restart")
	}
}

// TestPersistenceExpiredSessionNotLoaded verifies that an expired persistent
// session in the file is silently discarded on load.
func TestPersistenceExpiredSessionNotLoaded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	s1 := NewStoreWithPath(path)
	token, err := s1.CreatePersistentWithTTL("heidi", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// Manually backdate the creation time so the session is expired.
	s1.mu.Lock()
	s1.sessions[token].CreatedAt = time.Now().Add(-2 * time.Second)
	s1.mu.Unlock()
	// Re-save with the backdated time.
	if err := s1.save(); err != nil {
		t.Fatal(err)
	}

	// Simulate restart: the expired session should not be loaded.
	s2 := NewStoreWithPath(path)
	if s2.Get(token) != nil {
		t.Fatal("expected expired persistent session to not be loaded")
	}
}

// TestPersistenceFilePermissions checks that the sessions file is created with
// owner-only permissions (0600).
func TestPersistenceFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	s := NewStoreWithPath(path)
	if _, err := s.CreatePersistentWithTTL("ivan", DefaultSessionTTL); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("expected file mode 0600, got %o", perm)
	}
}

// TestPersistentSessionUnwritableDir verifies that a remember-me login still
// succeeds when the session file cannot be written (e.g. read-only or missing
// directory). The session is kept in memory instead of failing the login.
func TestPersistentSessionUnwritableDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "missing-dir", "sessions.json")

	s := NewStoreWithPath(path)
	token, err := s.CreatePersistentWithTTL("june", time.Hour)
	if err != nil {
		t.Fatalf("expected session creation to succeed despite unwritable dir, got %v", err)
	}
	sess := s.Get(token)
	if sess == nil {
		t.Fatal("expected in-memory session to be retrievable")
	}
	if sess.Persist {
		t.Fatal("expected fallback session to be non-persistent")
	}
}

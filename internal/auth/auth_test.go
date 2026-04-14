package auth

import (
	"net/http"
	"net/http/httptest"
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
	s.sessions[token].CreatedAt = time.Now().Add(-tokenTTL - time.Minute)
	s.mu.Unlock()

	if s.Get(token) != nil {
		t.Fatal("expected nil for expired session")
	}
}

package auth

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const cookieName = "filex_session"

// DefaultSessionTTL is used by Create() when no TTL is specified (e.g. in tests
// or when auth is called without a config-driven TTL). For production use the
// configured TTL is passed explicitly via CreateWithTTL.
const DefaultSessionTTL = 30 * 24 * time.Hour

// Session holds a logged-in user's session data.
type Session struct {
	Token     string
	Username  string
	CreatedAt time.Time
	TTL       time.Duration // effective lifetime of this session
}

// Store is an in-memory session store.
type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewStore creates a new Store.
func NewStore() *Store {
	s := &Store{sessions: make(map[string]*Session)}
	go s.cleanup()
	return s
}

// Create generates a new session token for username with the default TTL.
func (s *Store) Create(username string) (string, error) {
	return s.CreateWithTTL(username, DefaultSessionTTL)
}

// CreateWithTTL generates a new session token for username with a custom TTL.
func (s *Store) CreateWithTTL(username string, ttl time.Duration) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[token] = &Session{Token: token, Username: username, CreatedAt: time.Now(), TTL: ttl}
	s.mu.Unlock()
	return token, nil
}

// Get retrieves a session by token. Returns nil if not found or expired.
func (s *Store) Get(token string) *Session {
	s.mu.RLock()
	sess := s.sessions[token]
	s.mu.RUnlock()
	if sess == nil {
		return nil
	}
	if time.Since(sess.CreatedAt) > sess.TTL {
		s.Delete(token)
		return nil
	}
	return sess
}

// Delete removes a session by token.
func (s *Store) Delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

// cleanup periodically purges expired sessions.
func (s *Store) cleanup() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for k, v := range s.sessions {
			if time.Since(v.CreatedAt) > v.TTL {
				delete(s.sessions, k)
			}
		}
		s.mu.Unlock()
	}
}

// FromRequest extracts the session from the request cookie.
func (s *Store) FromRequest(r *http.Request) *Session {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	return s.Get(c.Value)
}

// SetCookie sets the session cookie on the response with the default TTL.
func SetCookie(w http.ResponseWriter, token string) {
	SetCookieWithTTL(w, token, DefaultSessionTTL)
}

// SetSessionCookie sets a browser-session-scoped cookie (no MaxAge / Expires).
// The browser will discard it when the window is closed.
func SetSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// MaxAge intentionally omitted → browser session cookie
	})
}

// SetCookieWithTTL sets the session cookie with a specific TTL.
func SetCookieWithTTL(w http.ResponseWriter, token string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

// ClearCookie clears the session cookie.
func ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

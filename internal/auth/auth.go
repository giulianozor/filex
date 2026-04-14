package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
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
	Persist   bool          // true for remember-me sessions that are saved to disk
}

// Store is an in-memory session store, optionally backed by a JSON file for
// remember-me persistence across service restarts.
type Store struct {
	mu          sync.RWMutex
	sessions    map[string]*Session
	sessionPath string // empty means no on-disk persistence
}

// NewStore creates a new in-memory Store with no disk persistence.
func NewStore() *Store {
	s := &Store{sessions: make(map[string]*Session)}
	go s.cleanup()
	return s
}

// NewStoreWithPath creates a Store backed by a JSON file at path.
// Existing persistent sessions (remember-me) are loaded from the file on
// startup so that they survive service restarts.  If path is empty the store
// behaves identically to NewStore.
func NewStoreWithPath(path string) *Store {
	s := &Store{sessions: make(map[string]*Session), sessionPath: path}
	_ = s.load() // best-effort: file may not exist yet
	go s.cleanup()
	return s
}

// Create generates a new session token for username with the default TTL.
func (s *Store) Create(username string) (string, error) {
	return s.CreateWithTTL(username, DefaultSessionTTL)
}

// CreateWithTTL generates a new in-memory session token for username with a custom TTL.
func (s *Store) CreateWithTTL(username string, ttl time.Duration) (string, error) {
	return s.create(username, ttl, false)
}

// CreatePersistentWithTTL generates a session token that is saved to disk so
// it survives service restarts (used for remember-me logins).
func (s *Store) CreatePersistentWithTTL(username string, ttl time.Duration) (string, error) {
	return s.create(username, ttl, true)
}

func (s *Store) create(username string, ttl time.Duration, persist bool) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[token] = &Session{Token: token, Username: username, CreatedAt: time.Now(), TTL: ttl, Persist: persist}
	s.mu.Unlock()
	if persist {
		if err := s.save(); err != nil {
			// Roll back the in-memory session so callers get a clean error.
			s.mu.Lock()
			delete(s.sessions, token)
			s.mu.Unlock()
			return "", err
		}
	}
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
	sess := s.sessions[token]
	delete(s.sessions, token)
	s.mu.Unlock()
	if sess != nil && sess.Persist {
		_ = s.save() // best-effort
	}
}

// cleanup periodically purges expired sessions.
func (s *Store) cleanup() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		anyPersist := false
		for k, v := range s.sessions {
			if time.Since(v.CreatedAt) > v.TTL {
				if v.Persist {
					anyPersist = true
				}
				delete(s.sessions, k)
			}
		}
		s.mu.Unlock()
		if anyPersist {
			_ = s.save() // best-effort
		}
	}
}

// persistedSession is the on-disk representation of a remember-me session.
type persistedSession struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	CreatedAt time.Time `json:"created_at"`
	TTLNs     int64     `json:"ttl_ns"`
}

// save writes all non-expired persistent sessions to sessionPath atomically.
// It is a no-op when sessionPath is empty.
func (s *Store) save() error {
	if s.sessionPath == "" {
		return nil
	}
	s.mu.RLock()
	var out []persistedSession
	for _, sess := range s.sessions {
		if sess.Persist && time.Since(sess.CreatedAt) <= sess.TTL {
			out = append(out, persistedSession{
				Token:     sess.Token,
				Username:  sess.Username,
				CreatedAt: sess.CreatedAt,
				TTLNs:     int64(sess.TTL),
			})
		}
	}
	s.mu.RUnlock()

	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	tmp := s.sessionPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.sessionPath)
}

// load reads persistent sessions from sessionPath and populates the store.
// It is a no-op when sessionPath is empty or the file does not exist.
func (s *Store) load() error {
	if s.sessionPath == "" {
		return nil
	}
	data, err := os.ReadFile(s.sessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var sessions []persistedSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		return err
	}
	s.mu.Lock()
	for _, ps := range sessions {
		ttl := time.Duration(ps.TTLNs)
		if time.Since(ps.CreatedAt) <= ttl {
			s.sessions[ps.Token] = &Session{
				Token:     ps.Token,
				Username:  ps.Username,
				CreatedAt: ps.CreatedAt,
				TTL:       ttl,
				Persist:   true,
			}
		}
	}
	s.mu.Unlock()
	return nil
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

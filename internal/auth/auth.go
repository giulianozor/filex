package auth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/giulianozor/filex/internal/atomicfile"
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

// newStore is the shared constructor behind NewStore and NewStoreWithPath: it
// allocates the session map, loads any disk-backed sessions saved by a previous
// run (best-effort: the file may not exist yet), and starts the periodic
// expired-session sweeper.
func newStore(path string) *Store {
	s := &Store{sessions: make(map[string]*Session), sessionPath: path}
	// Best-effort, but a malformed/unreadable session file silently logging
	// every remembered user out on restart is worth surfacing.
	if err := s.load(); err != nil {
		log.Printf("WARNING: could not load persistent sessions from %s: %v", path, err)
	}
	go s.cleanup()
	return s
}

// NewStore creates a new in-memory Store with no disk persistence.
func NewStore() *Store {
	return newStore("")
}

// NewStoreWithPath creates a Store backed by a JSON file at path.
// Existing persistent sessions (remember-me) are loaded from the file on
// startup so that they survive service restarts.  If path is empty the store
// behaves identically to NewStore.
func NewStoreWithPath(path string) *Store {
	return newStore(path)
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
	sess := &Session{Token: token, Username: username, CreatedAt: time.Now(), TTL: ttl, Persist: persist}
	s.mu.Lock()
	s.sessions[token] = sess
	// Between hourly sweeps the map can still be bounded: once it exceeds the
	// cap, drop the oldest session so a login flood cannot grow memory without
	// limit (expired entries are evicted first, then the oldest live one).
	if len(s.sessions) > maxSessions {
		var oldest *Session
		for _, existing := range s.sessions {
			if sessionExpired(existing.CreatedAt, existing.TTL) {
				delete(s.sessions, existing.Token)
				continue
			}
			if oldest == nil || existing.CreatedAt.Before(oldest.CreatedAt) {
				oldest = existing
			}
		}
		if len(s.sessions) > maxSessions && oldest != nil {
			delete(s.sessions, oldest.Token)
		}
	}
	s.mu.Unlock()
	if persist {
		if err := s.save(); err != nil {
			// Persistence is best-effort: never fail the login because the
			// session file could not be written (e.g. read-only directory).
			// Fall back to an in-memory (non-persistent) session instead.
			log.Printf("WARNING: could not persist remember-me session: %v (keeping in-memory session)", err)
			s.mu.Lock()
			sess.Persist = false
			s.mu.Unlock()
			// A concurrent save may already have snapshotted this session with
			// Persist=true before the flip; rewrite so the on-disk store stops
			// presenting it as a remembered session after the next restart.
			if serr := s.save(); serr != nil {
				log.Printf("WARNING: could not record in-memory session fallback: %v", serr)
			}
		}
	}
	return token, nil
}

// maxSessions bounds the size of the in-memory session map between sweeps. The
// cleanup goroutine only runs hourly, so without a cap a login flood could grow
// the map without limit inside a single sweep window.
const maxSessions = 100_000

// sessionExpired reports whether a session created at createdAt with lifetime
// ttl has lapsed. The comparison is deliberately exclusive (strictly greater):
// a session is live through its exact TTL boundary.
func sessionExpired(createdAt time.Time, ttl time.Duration) bool {
	return time.Since(createdAt) > ttl
}

// Get retrieves a session by token. Returns nil if not found or expired.
func (s *Store) Get(token string) *Session {
	// Fast path: most requests are for live sessions and only need a read lock.
	s.mu.RLock()
	sess := s.sessions[token]
	if sess == nil {
		s.mu.RUnlock()
		return nil
	}
	expired := sessionExpired(sess.CreatedAt, sess.TTL)
	s.mu.RUnlock()
	if !expired {
		return sess
	}
	// Drop expired sessions in-memory only. Persisting here would rewrite
	// the whole session file on every request that carries a stale cookie,
	// turning a single outdated client into a disk-write loop; the periodic
	// cleanup goroutine keeps the on-disk file eventually consistent. The
	// entry is re-checked under the write lock so a concurrent create for the
	// same token is never clobbered.
	s.mu.Lock()
	if cur := s.sessions[token]; cur == sess && sessionExpired(cur.CreatedAt, cur.TTL) {
		delete(s.sessions, token)
	}
	s.mu.Unlock()
	return nil
}

// Delete removes a session by token.
func (s *Store) Delete(token string) {
	s.mu.Lock()
	sess := s.sessions[token]
	delete(s.sessions, token)
	s.mu.Unlock()
	if sess != nil && sess.Persist {
		// Persistence is best-effort, but a failed write silently dropping a
		// remembered session is worth logging; the session is already removed
		// from memory either way.
		if err := s.save(); err != nil {
			log.Printf("WARNING: could not persist session removal: %v", err)
		}
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
			if sessionExpired(v.CreatedAt, v.TTL) {
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
	// Snapshot AND write while holding the read lock: a concurrent login could
	// otherwise insert a session between the snapshot and the rename, losing it
	// from whichever save wins the file (the session would survive in memory
	// but vanish on the next restart). Writers (create/delete/cleanup) block
	// for the brief write, which keeps the on-disk store consistent. No caller
	// invokes save() while holding the write lock, so this cannot deadlock.
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []persistedSession
	for _, sess := range s.sessions {
		if sess.Persist && !sessionExpired(sess.CreatedAt, sess.TTL) {
			out = append(out, persistedSession{
				Token:     sess.Token,
				Username:  sess.Username,
				CreatedAt: sess.CreatedAt,
				TTLNs:     int64(sess.TTL),
			})
		}
	}

	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	// Sessions hold bearer tokens, so the file must be owner-only regardless
	// of the umask or temp-file default, and the contents are flushed to disk
	// before the rename via the shared atomic writer (a crash cannot leave an
	// empty target in place). The unique temp name also means two concurrent
	// saves (e.g. two remember-me logins) never share a temp file.
	err = atomicfile.Write(s.sessionPath, data, 0o600)
	if err != nil {
		return err
	}
	return nil
}

// maxSessionFileBytes caps how large an on-disk sessions file is accepted at
// startup. The file is admin-editable (restored backups, botched hand edits),
// so an oversized or corrupt file must not be slurped into memory. Generous:
// 100k sessions × ~150 bytes each is ~15 MB.
const maxSessionFileBytes = 32 << 20

// load reads persistent sessions from sessionPath and populates the store.
// It is a no-op when sessionPath is empty or the file does not exist.
func (s *Store) load() error {
	if s.sessionPath == "" {
		return nil
	}
	f, err := os.Open(s.sessionPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSessionFileBytes+1))
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > maxSessionFileBytes {
		return fmt.Errorf("session file too large (%d bytes, cap %d)", len(data), maxSessionFileBytes)
	}
	var sessions []persistedSession
	if err := json.Unmarshal(data, &sessions); err != nil {
		return err
	}
	s.mu.Lock()
	now := time.Now()
	for _, ps := range sessions {
		// Reject crafted/tampered records: a non-positive TTL and a creation
		// time more than one TTL in the future are never valid and would
		// otherwise mint a never-expiring session.
		if ps.TTLNs <= 0 {
			continue
		}
		ttl := time.Duration(ps.TTLNs)
		if now.Add(ttl).Before(ps.CreatedAt) {
			continue
		}
		if !sessionExpired(ps.CreatedAt, ttl) {
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

// baseCookie builds the shared session cookie with common attributes.
func baseCookie(token string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// SetSessionCookie sets a browser-session-scoped cookie (no MaxAge / Expires).
// The browser will discard it when the window is closed.
// secure should be true when the connection is over HTTPS.
func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	// MaxAge intentionally omitted → browser session cookie
	http.SetCookie(w, baseCookie(token, secure))
}

// SetCookieWithTTL sets the session cookie with a specific TTL.
// secure should be true when the connection is over HTTPS.
func SetCookieWithTTL(w http.ResponseWriter, token string, ttl time.Duration, secure bool) {
	c := baseCookie(token, secure)
	// int() truncates a sub-second TTL to 0, which Go emits as *no* Max-Age
	// (a browser-session cookie, not the requested short-lived one). Keep the
	// cookie at least one second and treat a non-positive TTL as immediate
	// expiry rather than accidentally minting a session cookie.
	switch secs := int(ttl.Seconds()); {
	case ttl <= 0:
		c.MaxAge = -1
	case secs < 1:
		c.MaxAge = 1
	default:
		c.MaxAge = secs
	}
	http.SetCookie(w, c)
}

// ClearCookie clears the session cookie.
// secure should be true when the connection is over HTTPS.
func ClearCookie(w http.ResponseWriter, secure bool) {
	c := baseCookie("", secure)
	c.MaxAge = -1
	http.SetCookie(w, c)
}

package handler

import (
	"log"

	"github.com/giulianozor/filex/internal/config"
	fslib "github.com/giulianozor/filex/internal/fs"
	"github.com/giulianozor/filex/internal/prefs"
)

// BuildPrefsPaths returns the preferences file path for every configured user,
// resolved to that user's own home directory (<home>/.config/filex.yaml, see
// prefs.PathForUser), plus the process-user path under the "" key for the
// no-auth case. A user whose home cannot be resolved from the account database
// gets "" (in-memory only, with a warning) so preferences never silently land
// in the server process's home instead of the user's.
func BuildPrefsPaths(cfg *config.Config) map[string]string {
	paths := make(map[string]string, len(cfg.Users)+1)
	if p, err := prefs.DefaultPath(); err == nil {
		paths[""] = p
	}
	for i := range cfg.Users {
		u := &cfg.Users[i]
		path, err := prefs.PathForUser(u.Username, u.UID)
		if err != nil {
			log.Printf("WARNING: cannot resolve preferences path for user %q: %v (preferences kept in memory only)", u.Username, err)
			path = ""
		}
		paths[u.Username] = path
	}
	return paths
}

// BuildUserFS constructs a per-user FS map from the config.
// Users whose base_path does not exist or is not a directory are skipped with a warning.
func BuildUserFS(cfg *config.Config) map[string]*userEntry {
	if !cfg.AuthRequired() {
		return nil
	}
	m := make(map[string]*userEntry, len(cfg.Users))
	for i := range cfg.Users {
		u := &cfg.Users[i]
		if _, exists := m[u.Username]; exists {
			// A duplicate username would silently shadow the first entry,
			// which is confusing when debugging auth issues; surface it.
			log.Printf("WARNING: skipping duplicate user %q", u.Username)
			continue
		}
		uid, gid := derefID(u.UID), derefID(u.GID)
		fs, err := fslib.NewWithOwner(u.BasePath, uid, gid)
		if err != nil {
			log.Printf("WARNING: skipping user %q: %v", u.Username, err)
			continue
		}
		m[u.Username] = &userEntry{user: u, fs: fs}
	}
	return m
}

// derefID returns the pointed-to int value, or -1 if the pointer is nil.
// -1 is the sentinel value used by fs.FS to mean "do not chown".
func derefID(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

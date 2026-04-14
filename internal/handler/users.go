package handler

import (
	"log"

	"github.com/giulianozor/filex/internal/config"
	fslib "github.com/giulianozor/filex/internal/fs"
)

// BuildUserFS constructs a per-user FS map from the config.
// Users whose base_path does not exist or is not a directory are skipped with a warning.
func BuildUserFS(cfg *config.Config) map[string]*userEntry {
	if !cfg.AuthRequired() {
		return nil
	}
	m := make(map[string]*userEntry, len(cfg.Users))
	for i := range cfg.Users {
		u := &cfg.Users[i]
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

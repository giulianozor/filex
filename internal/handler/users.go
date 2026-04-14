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
		fs, err := fslib.NewWithOwner(u.BasePath, u.UID, u.GID)
		if err != nil {
			log.Printf("WARNING: skipping user %q: %v", u.Username, err)
			continue
		}
		m[u.Username] = &userEntry{user: u, fs: fs}
	}
	return m
}

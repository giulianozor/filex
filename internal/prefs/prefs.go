// Package prefs stores the per-user and global user preferences. Each
// configured user's preferences belong in that user's own home directory
// (<home>/.config/filex.yaml, see PathForUser) rather than in the server
// config.yaml or the home of the process running the daemon. config.yaml keeps
// the deployment/serving settings (host, port, per-user base_path and uid/gid
// jails); prefs keeps the things a user actually edits from the UI: favourites,
// protected paths, the remembered secure-delete (wipe) preferences and — set
// via the filex-passwd tool — the bcrypt password hash. Splitting them lets a
// read-only, root-managed config.yaml sit next to per-user home-dir
// preferences files the server can rewrite.
package prefs

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"

	"github.com/giulianozor/filex/internal/atomicfile"
	"github.com/giulianozor/filex/internal/config"
	"gopkg.in/yaml.v3"
)

// DefaultFilename is the preferences file name inside the user config dir.
const DefaultFilename = "filex.yaml"

// Deletion holds the remembered secure-delete (wipe) preferences.
type Deletion struct {
	DefaultWipe bool   `yaml:"default_wipe,omitempty" json:"default_wipe"`
	WipeMethod  string `yaml:"wipe_method,omitempty" json:"wipe_method"`
}

// User holds the preferences for one logged-in user.
type User struct {
	Username       string             `yaml:"username" json:"username"`
	PasswordHash   string             `yaml:"password_hash,omitempty" json:"-"`                       // bcrypt hash, set via filex-passwd
	ShowDotfiles   *bool              `yaml:"show_dotfiles,omitempty" json:"show_dotfiles,omitempty"` // nil = use server default
	ProtectedPaths []string           `yaml:"protected_paths,omitempty"`
	Favourites     []config.Favourite `yaml:"favourites,omitempty"`
	Deletion       Deletion           `yaml:"deletion,omitempty"`
}

// Preferences is the whole ~/.config/filex.yaml document. Deletion holds the
// global (no-auth) fallback; each entry in Users overrides it for that user.
type Preferences struct {
	Deletion Deletion `yaml:"deletion,omitempty"`
	Users    []User   `yaml:"users,omitempty"`
}

// DefaultPath returns the default preferences file path: <user config dir>/filex.yaml.
// On Linux this resolves to $XDG_CONFIG_HOME/filex.yaml or ~/.config/filex.yaml.
// Resolution intentionally survives environments where a service manager or
// container runtime strips HOME and XDG_CONFIG_HOME (systemd units, Docker):
// it falls back to the account database and, for a root process, a fixed
// location, so a stripped environment never silently degrades to in-memory-only
// preferences that are lost on every restart.
func DefaultPath() (string, error) {
	dir, err := userConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefaultFilename), nil
}

// PathForUser resolves the preferences file for a configured server user to
// that user's own home directory instead of the server process's:
// <home>/.config/filex.yaml, where <home> comes from the account database — by
// username first, then by the numeric uid when supplied. This keeps per-user
// preferences (password hash, favourites, protected paths, show_dotfiles, wipe
// defaults) under the user's own home even when the daemon runs as root, which
// would otherwise resolve every user's prefs to ~root/.config/filex.yaml.
//
// Resolution fails when the account has no passwd entry it can find (e.g. a
// bare container image with synthetic /etc/passwd): the caller then decides
// between disabling persistence (server, with a warning) or failing loudly
// (filex-passwd, which points at -prefs).
func PathForUser(username string, uid *int) (string, error) {
	if u, err := user.Lookup(username); err == nil && u.HomeDir != "" {
		return filepath.Join(u.HomeDir, ".config", DefaultFilename), nil
	}
	if uid != nil {
		if u, err := user.LookupId(strconv.Itoa(*uid)); err == nil && u.HomeDir != "" {
			return filepath.Join(u.HomeDir, ".config", DefaultFilename), nil
		}
	}
	return "", fmt.Errorf("cannot resolve home directory for user %q: no account entry (use an explicit -prefs path)", username)
}

// userConfigDir resolves the user config directory with a cascade of
// fallbacks: $XDG_CONFIG_HOME, then $HOME/.config, then the home directory
// from the account database (os/user, which reads /etc/passwd and works even
// when the environment is empty), then a fixed location for a root process.
// Relying on the environment alone made preferences non-persistent whenever
// the process had no HOME/XDG_CONFIG_HOME: the server then kept all user
// settings (favourites, protected paths, show_dotfiles, wipe prefs) in memory
// and every change was lost on restart.
func userConfigDir() (string, error) {
	if dir, err := os.UserConfigDir(); err == nil {
		return dir, nil
	}
	if dir, err := os.UserHomeDir(); err == nil {
		return filepath.Join(dir, ".config"), nil
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return filepath.Join(u.HomeDir, ".config"), nil
	}
	if os.Geteuid() == 0 {
		// Root always has a canonical home even without an account entry or a
		// HOME variable (e.g. a minimal container image).
		return filepath.Join("/root", ".config"), nil
	}
	return "", errors.New("cannot determine user config dir: $XDG_CONFIG_HOME, $HOME and the account database all resolve nothing")
}

// Load reads the preferences file at path. A missing file is not an error: the
// caller gets an empty Preferences so first-run saves start from defaults.
// An empty path also returns an empty Preferences (no persistence configured).
func Load(path string) (*Preferences, error) {
	p := &Preferences{}
	if path == "" {
		return p, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, p); err != nil {
		return nil, err
	}
	return p, nil
}

// OwnerForUser resolves the uid/gid a configured user's preferences file should
// be chowned to so it is owned by the user rather than by whatever process user
// wrote it (a root daemon, filex-passwd). An explicitly configured uid/gid wins
// over the account database; otherwise the numeric uid/gid from the account
// database is used. A field that cannot be resolved is -1, which the atomic
// writer treats as "leave unchanged".
func OwnerForUser(username string, uid, gid *int) (int, int) {
	if uid != nil && gid != nil {
		return *uid, *gid
	}
	var acct *user.User
	if username != "" {
		if u, err := user.Lookup(username); err == nil {
			acct = u
		}
	}
	oUID, oGID := -1, -1
	if acct != nil {
		if v, err := strconv.Atoi(acct.Uid); err == nil {
			oUID = v
		}
		if v, err := strconv.Atoi(acct.Gid); err == nil {
			oGID = v
		}
	}
	if uid != nil {
		oUID = *uid
	}
	if gid != nil {
		oGID = *gid
	}
	return oUID, oGID
}

// Save writes the preferences to path without changing ownership.
func (p *Preferences) Save(path string) error {
	return p.SaveOwned(path, -1, -1)
}

// SaveOwned atomically writes the preferences to path and chowns the result to
// uid/gid (values of -1 leave the corresponding field unchanged, matching the
// fs.FS sentinel), so a root daemon leaves each user's preferences file owned
// by that user instead of by root. It is a no-op when path is empty. The parent
// directory is created if needed.
func (p *Preferences) SaveOwned(path string, uid, gid int) error {
	if path == "" {
		return nil
	}
	data, err := yaml.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// The file can be shared across several tools (server + filex-passwd), so
	// keep any existing group/other bits an admin set on purpose, defaulting to
	// 0600 for a fresh file. Password hashes are still the only real secret, so
	// 0600 is the right default.
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return atomicfile.WriteOwned(path, data, mode, uid, gid)
}

// FindUser returns the User pref, or nil.
func (p *Preferences) FindUser(username string) *User {
	for i := range p.Users {
		if p.Users[i].Username == username {
			return &p.Users[i]
		}
	}
	return nil
}

// UpsertUser returns the User pref for username, creating it if it does not
// exist yet (so an update-only caller never has to remember to append).
func (p *Preferences) UpsertUser(username string) *User {
	if u := p.FindUser(username); u != nil {
		return u
	}
	p.Users = append(p.Users, User{Username: username})
	return &p.Users[len(p.Users)-1]
}

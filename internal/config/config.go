package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/giulianozor/filex/internal/atomicfile"
	"gopkg.in/yaml.v3"
)

// ErrConfigNotWritable is returned by Save when the config file cannot be
// written due to a permissions error (EACCES, EPERM, EROFS). Callers may
// choose to keep in-memory state rather than reverting.
var ErrConfigNotWritable = errors.New("config file is not writable")

type Favourite struct {
	Name string `yaml:"name" json:"name"`
	Path string `yaml:"path" json:"path"`
}

// User defines a per-user directory configuration in config.yaml. Each user
// gets their own base_path jail. UID/GID are used to chown files
// created/uploaded by this user. A nil UID or GID means "do not chown"
// (ownership is not changed). Use uid: 0 / gid: 0 to explicitly chown to root.
//
// Per-user preferences (password hash, favourites, protected paths, secure
// wipe defaults, show_dotfiles) are intentionally NOT part of config.yaml: they
// are stored in the user preferences file (~/.config/filex.yaml), see the
// internal/prefs package. config.yaml keeps the serving and directory settings
// so it can stay read-only and root-managed while the running server rewrites
// prefs freely.
type User struct {
	Username string `yaml:"username"`
	BasePath string `yaml:"base_path"`
	UID      *int   `yaml:"uid,omitempty"`
	GID      *int   `yaml:"gid,omitempty"`
}

type Config struct {
	Host                   string      `yaml:"host"`
	Port                   int         `yaml:"port"`
	BasePath               string      `yaml:"base_path"` // global default (used when no users configured)
	ShowDotfiles           bool        `yaml:"show_dotfiles"`
	SessionTTLDays         int         `yaml:"session_ttl_days"` // session lifetime in days; 0 = use default (30)
	ProtectedPaths         []string    `yaml:"protected_paths"`
	Favourites             []Favourite `yaml:"favourites"`
	Users                  []User      `yaml:"users"`
	ThumbnailParallelism   int         `yaml:"thumbnail_parallelism"`    // parallel ffmpeg processes for thumbnail generation; 0 = auto (half CPU threads)
	ThumbnailThreads       int         `yaml:"thumbnail_threads"`        // threads per ffmpeg process; 0 = single-threaded decode (safe default)
	ThumbnailHardwareAccel string      `yaml:"thumbnail_hardware_accel"` // "auto", "qsv", or "off"
}

// DefaultSessionTTLDays is used when a config does not set SessionTTLDays.
const DefaultSessionTTLDays = 30

// maxSessionTTLDays caps a configured session TTL at roughly a year: more is
// practically never desirable and keeps the duration well inside int64 range.
const maxSessionTTLDays = 3660

// SessionTTL returns the effective session duration derived from SessionTTLDays.
// When SessionTTLDays is 0 or negative the default of 30 days is used. The
// value is clamped so a huge number in the YAML cannot overflow the int64
// nanosecond range and wrap to a negative TTL.
func (c *Config) SessionTTL() time.Duration {
	days := c.SessionTTLDays
	if days <= 0 {
		days = DefaultSessionTTLDays
	}
	if days > maxSessionTTLDays {
		days = maxSessionTTLDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// isPermissionErr reports whether err is a filesystem permission error
// (EACCES, EPERM, EROFS) that callers should treat as "config not writable".
func isPermissionErr(err error) bool {
	return errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS)
}

// Save writes the current config to path as YAML.
// If path is empty, Save is a no-op and returns nil.
// The write is performed atomically: the data is first written to a temporary
// file next to the target and then renamed into place.
// When the write fails due to a permissions error (EACCES, EPERM, EROFS),
// Save wraps the error with ErrConfigNotWritable so callers can decide
// whether to revert in-memory state or keep it.
func (c *Config) Save(path string) error {
	if path == "" {
		return nil
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	// Preserve any group/other read bits an admin set on an existing config
	// (a shared or group-readable file must not silently become owner-only
	// after the first favourites edit), defaulting to 0600 for a new file.
	// The write itself is atomic and umask-independent.
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	wrapPerm := func(err error) error {
		if isPermissionErr(err) {
			return errors.Join(ErrConfigNotWritable, err)
		}
		return err
	}
	return wrapPerm(atomicfile.Write(path, data, mode))
}

func Load(path string) (*Config, error) {
	cfg := &Config{
		Host:         "0.0.0.0",
		Port:         8080,
		ShowDotfiles: false,
	}

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, err
		}
	}

	if cfg.Port < 1 || cfg.Port > 65535 {
		return nil, fmt.Errorf("invalid port %d: must be between 1 and 65535", cfg.Port)
	}
	if cfg.SessionTTLDays < 0 {
		return nil, fmt.Errorf("invalid session_ttl_days %d: must be >= 0 (0 uses the default)", cfg.SessionTTLDays)
	}
	if cfg.ThumbnailParallelism < 0 {
		return nil, fmt.Errorf("invalid thumbnail_parallelism %d: must be >= 0 (0 = auto)", cfg.ThumbnailParallelism)
	}
	if cfg.ThumbnailThreads < 0 {
		return nil, fmt.Errorf("invalid thumbnail_threads %d: must be >= 0 (0 = default)", cfg.ThumbnailThreads)
	}

	// filepath.Abs already resolves "" against the working directory, so the
	// global default and the per-user resolution share one code path.
	abs, err := filepath.Abs(cfg.BasePath)
	if err != nil {
		return nil, err
	}
	cfg.BasePath = abs

	for i := range cfg.Users {
		u := &cfg.Users[i]
		if u.BasePath == "" {
			u.BasePath = cfg.BasePath
		}
		uAbs, err := filepath.Abs(u.BasePath)
		if err != nil {
			return nil, err
		}
		u.BasePath = uAbs
	}

	return cfg, nil
}

// AuthRequired reports whether the server requires authentication
// (i.e. at least one user is configured).
func (c *Config) AuthRequired() bool {
	return len(c.Users) > 0
}

// FindUser returns the User with the given username, or nil.
func (c *Config) FindUser(username string) *User {
	for i := range c.Users {
		if c.Users[i].Username == username {
			return &c.Users[i]
		}
	}
	return nil
}

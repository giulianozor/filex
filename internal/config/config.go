package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Favourite struct {
	Name string `yaml:"name" json:"name"`
	Path string `yaml:"path" json:"path"`
}

// User defines a per-user configuration. Each user gets their own base_path
// jail. UID/GID are used to chown files created/uploaded by this user.
// A nil UID or GID means "do not chown" (ownership is not changed).
// Use uid: 0 / gid: 0 to explicitly chown to root.
type User struct {
	Username     string      `yaml:"username"`
	PasswordHash string      `yaml:"password_hash"` // bcrypt hash
	BasePath     string      `yaml:"base_path"`
	UID          *int        `yaml:"uid,omitempty"`
	GID          *int        `yaml:"gid,omitempty"`
	ShowDotfiles *bool       `yaml:"show_dotfiles,omitempty"` // nil = use global default
	Favourites   []Favourite `yaml:"favourites,omitempty"`
}

type Config struct {
	Host         string      `yaml:"host"`
	Port         int         `yaml:"port"`
	BasePath     string      `yaml:"base_path"`      // global default (used when no users configured)
	ShowDotfiles bool        `yaml:"show_dotfiles"`
	Favourites   []Favourite `yaml:"favourites"`
	Users        []User      `yaml:"users"`
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

	if cfg.BasePath == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		cfg.BasePath = wd
	}

	abs, err := filepath.Abs(cfg.BasePath)
	if err != nil {
		return nil, err
	}
	cfg.BasePath = abs

	// Resolve each user's base_path to absolute.
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

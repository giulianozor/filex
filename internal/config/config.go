package config

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Favourite struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

type Config struct {
	Host         string      `yaml:"host"`
	Port         int         `yaml:"port"`
	BasePath     string      `yaml:"base_path"`
	ShowDotfiles bool        `yaml:"show_dotfiles"`
	Favourites   []Favourite `yaml:"favourites"`
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

	return cfg, nil
}

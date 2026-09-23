package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "0.0.0.0" {
		t.Errorf("Host = %q, want 0.0.0.0", cfg.Host)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.ShowDotfiles {
		t.Error("ShowDotfiles should default to false")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "host: example.com\nport: 9000\nshow_dotfiles: true\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != "example.com" || cfg.Port != 9000 || !cfg.ShowDotfiles {
		t.Errorf("parsed config = %+v, want host=example.com port=9000 dotfiles", cfg)
	}
}

func TestSessionTTLClamp(t *testing.T) {
	if got := (&Config{SessionTTLDays: -5}).SessionTTL(); got != 30*24*time.Hour {
		t.Errorf("negative TTL = %v, want default 30d", got)
	}
	if got := (&Config{SessionTTLDays: 1_000_000}).SessionTTL(); got > maxSessionTTLDays*24*time.Hour {
		t.Errorf("huge TTL = %v, want clamped", got)
	}
}

func TestSavePreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{Host: "0.0.0.0", Port: 8081}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %o, want 644 preserved", got)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Port != 8081 {
		t.Errorf("reloaded Port = %d, want 8081", reloaded.Port)
	}
}

func TestSaveWrapsPermissionError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission tests are meaningless as root")
	}
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(locked, "config.yaml")
	cfg := &Config{Port: 8080}
	err := cfg.Save(path)
	if err == nil {
		t.Fatal("expected Save to fail inside a read-only directory")
	}
	if !errors.Is(err, ErrConfigNotWritable) {
		t.Fatalf("expected ErrConfigNotWritable, got %v", err)
	}
}

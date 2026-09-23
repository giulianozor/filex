package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSampleBytes_NoAuthParses(t *testing.T) {
	data := SampleBytes(false)
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("no-auth sample does not parse as YAML: %v", err)
	}
	if len(cfg.Users) != 0 {
		t.Fatalf("no-auth sample has users, want none")
	}
	if cfg.AuthRequired() {
		t.Fatal("no-auth sample reports AuthRequired()=true")
	}
	if cfg.Port != 8080 || cfg.Host != "0.0.0.0" {
		t.Fatalf("sample defaults wrong: host=%q port=%d", cfg.Host, cfg.Port)
	}
}

func TestSampleBytes_AuthParses(t *testing.T) {
	data := SampleBytes(true)
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("auth sample does not parse as YAML: %v", err)
	}
	if len(cfg.Users) != 1 {
		t.Fatalf("auth sample has %d users, want 1", len(cfg.Users))
	}
	if cfg.Users[0].Username != "alice" {
		t.Fatalf("auth sample username = %q, want alice", cfg.Users[0].Username)
	}
	if !cfg.AuthRequired() {
		t.Fatal("auth sample reports AuthRequired()=false")
	}
}

func TestWriteSample_BacksUpExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("host: old\nport: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	backup, err := WriteSample(path, false)
	if err != nil {
		t.Fatalf("WriteSample: %v", err)
	}
	if backup == "" {
		t.Fatal("expected a backup path, got empty")
	}
	if !strings.HasPrefix(backup, path+".bak.") {
		t.Fatalf("backup %q does not look like <path>.bak.<timestamp>", backup)
	}
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(orig), "no-auth mode") {
		t.Error("new config at path is not the sample")
	}
	old, err := os.ReadFile(backup)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(old), "host: old") {
		t.Error("backup does not contain original contents")
	}
}

func TestWriteSample_NoBackupWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	backup, err := WriteSample(path, true)
	if err != nil {
		t.Fatalf("WriteSample: %v", err)
	}
	if backup != "" {
		t.Fatalf("backup = %q, want empty when no existing file", backup)
	}
}

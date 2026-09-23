package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "filex.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUpdatePrefsHash_RoundTrip(t *testing.T) {
	path := writeConfig(t, `# lead comment kept
deletion:
  default_wipe: true
  wipe_method: fast

users:
  - username: alice
    password_hash: "$2a$10$oldhashvalue"
    favourites:
      - name: Home
        path: /
  - username: bob
    protected_paths:
      - /keep
`)
	newHash := "$2a$10$newhashvalue"
	if err := updatePrefsHash(path, "alice", newHash); err != nil {
		t.Fatalf("updatePrefsHash: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "# lead comment kept") {
		t.Error("lead comment was dropped")
	}
	if !strings.Contains(out, newHash) {
		t.Error("alice's hash was not updated")
	}
	if strings.Contains(out, "$2a$10$oldhashvalue") {
		t.Error("old hash still present")
	}
	if !strings.Contains(out, "bob") {
		t.Error("bob's entry was lost")
	}
	// The other preference data must survive the round-trip.
	if !strings.Contains(out, "default_wipe") || !strings.Contains(out, "/keep") {
		t.Error("other preference sections were dropped")
	}
}

func TestUpdatePrefsHash_AddsMissingHashKey(t *testing.T) {
	path := writeConfig(t, "users:\n  - username: bob\n    favourites:\n      - name: Home\n        path: /\n")
	if err := updatePrefsHash(path, "bob", "$2a$10$x"); err != nil {
		t.Fatalf("updatePrefsHash: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "$2a$10$x") {
		t.Error("hash key/value not added")
	}
}

func TestUpdatePrefsHash_UnknownUser(t *testing.T) {
	path := writeConfig(t, "users:\n  - username: alice\n")
	err := updatePrefsHash(path, "carol", "$2a$10$x")
	if err == nil || !strings.Contains(err.Error(), `user "carol" not found`) {
		t.Errorf("expected unknown-user error, got %v", err)
	}
}

func TestUpdatePrefsHash_MissingUsersSection(t *testing.T) {
	path := writeConfig(t, "deletion:\n  default_wipe: false\n")
	err := updatePrefsHash(path, "alice", "$2a$10$x")
	if err == nil || !strings.Contains(err.Error(), "no 'users' section") {
		t.Errorf("expected missing-users error, got %v", err)
	}
}

func TestUpdatePrefsHash_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	if err := updatePrefsHash(path, "alice", "$2a$10$x"); err != nil {
		t.Fatalf("updatePrefsHash: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "alice") || !strings.Contains(string(data), "$2a$10$x") {
		t.Errorf("fresh preferences file not bootstrapped: %s", string(data))
	}
}

// TestUpdatePrefsHash_CreatesMissingConfigDir pinpoints the other half of the
// first-run path: on a fresh machine ~/.config usually does not exist yet, and
// the atomic write into it failed with "no such file or directory", leaving the
// operator unable to set a password — which on an auth-enabled deployment means
// nobody can ever log in. The parent directory must be created first.
func TestUpdatePrefsHash_CreatesMissingConfigDir(t *testing.T) {
	// Nested path with a deliberately absent parent directory.
	path := filepath.Join(t.TempDir(), "does-not-exist", "filex.yaml")
	if err := updatePrefsHash(path, "alice", "$2a$10$x"); err != nil {
		t.Fatalf("updatePrefsHash into missing dir: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("preferences file not written: %v", err)
	}
	if !strings.Contains(string(data), "alice") || !strings.Contains(string(data), "$2a$10$x") {
		t.Errorf("preferences file not bootstrapped: %s", string(data))
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("config dir mode = %o, want 0700", got)
	}
}

func TestUpdatePrefsHash_EmptyFile(t *testing.T) {
	path := writeConfig(t, "")
	if err := updatePrefsHash(path, "alice", "$2a$10$x"); err != nil {
		t.Fatalf("updatePrefsHash: %v", err)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "alice") || !strings.Contains(string(data), "$2a$10$x") {
		t.Errorf("empty preferences file not bootstrapped: %s", string(data))
	}
}

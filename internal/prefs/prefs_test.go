package prefs

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/giulianozor/filex/internal/config"
)

func TestLoadMissingOrEmptyPath(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "nope.yaml")} {
		p, err := Load(path)
		if err != nil {
			t.Fatalf("Load(%q): %v", path, err)
		}
		if len(p.Users) != 0 || p.Deletion.DefaultWipe {
			t.Errorf("Load(%q) = %+v, want empty prefs", path, p)
		}
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "filex.yaml")
	content := "deletion:\n  default_wipe: true\n  wipe_method: zero\nusers:\n" +
		"  - username: alice\n    password_hash: \"$2a$12$x\"\n" +
		"    protected_paths:\n      - /docs/keep\n" +
		"    favourites:\n      - name: Docs\n        path: /docs\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !p.Deletion.DefaultWipe || p.Deletion.WipeMethod != "zero" {
		t.Errorf("global deletion = %+v, want default_wipe/method", p.Deletion)
	}
	u := p.FindUser("alice")
	if u == nil {
		t.Fatal("alice user not found")
	}
	if u.PasswordHash != "$2a$12$x" {
		t.Errorf("hash = %q, want $2a$12$x", u.PasswordHash)
	}
	if len(u.ProtectedPaths) != 1 || u.ProtectedPaths[0] != "/docs/keep" {
		t.Errorf("protected = %v", u.ProtectedPaths)
	}
	if len(u.Favourites) != 1 || u.Favourites[0].Path != "/docs" {
		t.Errorf("favourites = %+v", u.Favourites)
	}
}

func TestSaveRoundTripAndMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "filex.yaml")
	p := &Preferences{
		Deletion: Deletion{DefaultWipe: true, WipeMethod: "fast"},
		Users: []User{
			{Username: "bob",
				PasswordHash:   "$2a$12$h",
				ProtectedPaths: []string{"/keep"},
				Favourites:     []config.Favourite{{Name: "Home", Path: "/"}},
				Deletion:       Deletion{WipeMethod: "zero"},
			},
		},
	}
	if err := p.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 0600", got)
	}
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Deletion != p.Deletion {
		t.Errorf("deletion = %+v, want %+v", reloaded.Deletion, p.Deletion)
	}
	u := reloaded.FindUser("bob")
	if u == nil || u.PasswordHash != "$2a$12$h" {
		t.Errorf("bob prefs = %+v", reloaded.Users)
	}
	if u.Deletion.WipeMethod != "zero" {
		t.Errorf("bob deletion = %+v", u.Deletion)
	}
}

func TestUpsertUser(t *testing.T) {
	p := &Preferences{}
	u := p.UpsertUser("alice")
	if u == nil || u.Username != "alice" {
		t.Fatalf("UpsertUser = %+v", u)
	}
	if p.UpsertUser("alice") != u {
		t.Error("UpsertUser on an existing user did not return the same pointer")
	}
	if len(p.Users) != 1 {
		t.Errorf("users = %+v, want 1 entry", p.Users)
	}
}

// TestPathForUserUsesAccountHome locks in that per-user preferences resolve to
// the user's own home directory from the account database — the fix that keeps
// a root-managed server from writing every user's prefs into ~root/.config.
func TestPathForUserUsesAccountHome(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skipf("cannot determine current user: %v", err)
	}
	want := filepath.Join(u.HomeDir, ".config", DefaultFilename)
	got, err := PathForUser(u.Username, nil)
	if err != nil {
		t.Fatalf("PathForUser: %v", err)
	}
	if got != want {
		t.Errorf("PathForUser(%q) = %q, want %q", u.Username, got, want)
	}
}

// TestPathForUserUsesUIDFallback locks in the numeric-uid fallback used when a
// username lookup fails (e.g. a config user whose passwd login differs).
func TestPathForUserUsesUIDFallback(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skipf("cannot determine current user: %v", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		t.Skipf("cannot parse uid %q: %v", u.Uid, err)
	}
	want := filepath.Join(u.HomeDir, ".config", DefaultFilename)
	got, err := PathForUser("filex-no-such-username", &uid)
	if err != nil {
		t.Fatalf("PathForUser via uid: %v", err)
	}
	if got != want {
		t.Errorf("PathForUser(uid=%d) = %q, want %q", uid, got, want)
	}
}

func TestPathForUserUnknownUser(t *testing.T) {
	if _, err := PathForUser("filex-no-such-user-xyz", nil); err == nil {
		t.Fatal("PathForUser with unknown user = nil error, want error")
	}
}

// TestOwnerForUserUsesAccount locks in that preferences files are owned by the
// account user's uid/gid when the server config sets none explicitly: a root
// daemon writing alice's <home>/.config/filex.yaml must leave it alice-owned,
// not root-owned.
func TestOwnerForUserUsesAccount(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skipf("cannot determine current user: %v", err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		t.Skipf("cannot parse uid %q: %v", u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		t.Skipf("cannot parse gid %q: %v", u.Gid, err)
	}
	gotUID, gotGID := OwnerForUser(u.Username, nil, nil)
	if gotUID != uid || gotGID != gid {
		t.Errorf("OwnerForUser(%q) = %d:%d, want %d:%d", u.Username, gotUID, gotGID, uid, gid)
	}
}

// TestOwnerForUserConfigWins locks in that a configured uid/gid overrides the
// account database (e.g. a jail owner that differs from the passwd login).
func TestOwnerForUserConfigWins(t *testing.T) {
	u, err := user.Current()
	if err != nil {
		t.Skipf("cannot determine current user: %v", err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		t.Skipf("cannot parse gid %q: %v", u.Gid, err)
	}
	cUID := 4242
	gotUID, gotGID := OwnerForUser(u.Username, &cUID, nil)
	if gotUID != cUID {
		t.Errorf("uid = %d, want configured %d", gotUID, cUID)
	}
	// The unconfigured field falls back to the account value.
	if gotGID != gid {
		t.Errorf("gid = %d, want account %d", gotGID, gid)
	}
	if bothUID, bothGID := OwnerForUser(u.Username, &cUID, &gotGID); bothUID != cUID || bothGID != gotGID {
		t.Errorf("both configured = %d:%d, want %d:%d", bothUID, bothGID, cUID, gotGID)
	}
}

// TestOwnerForUserUnknown is -1/-1 ("leave ownership unchanged") for a user
// with no account entry, so a container root writing /root/.config/filex.yaml
// does not fail.
func TestOwnerForUserUnknown(t *testing.T) {
	uid, gid := OwnerForUser("filex-no-such-user-xyz", nil, nil)
	if uid != -1 || gid != -1 {
		t.Errorf("OwnerForUser(unknown) = %d:%d, want -1:-1", uid, gid)
	}
}

// TestSaveOwnedChownsFile verifies SaveOwned chowns the on-disk preferences to
// the requested owner, so the file is owned by the user (not the root daemon
// that wrote it).
func TestSaveOwnedChownsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filex.yaml")
	p := &Preferences{Users: []User{{Username: "alice"}}}
	if err := p.SaveOwned(path, os.Getuid(), os.Getgid()); err != nil {
		t.Fatalf("SaveOwned: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skipf("no syscall.Stat_t on this platform (got %T)", info.Sys())
	}
	if st.Uid != uint32(os.Getuid()) || st.Gid != uint32(os.Getgid()) {
		t.Errorf("owner = %d:%d, want %d:%d", st.Uid, st.Gid, os.Getuid(), os.Getgid())
	}
}

// TestSaveKeepsOwnerWithoutUID verifies Save never changes ownership (the
// temp-file default of the calling process is kept).
func TestSaveKeepsOwnerWithoutUID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "filex.yaml")
	p := &Preferences{}
	if err := p.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skipf("no syscall.Stat_t on this platform (got %T)", info.Sys())
	}
	if st.Uid != uint32(os.Getuid()) {
		t.Errorf("owner = %d, want process uid %d", st.Uid, os.Getuid())
	}
}

func TestDefaultPathHonorsXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", t.TempDir())
	p, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if want := filepath.Join(dir, DefaultFilename); p != want {
		t.Errorf("DefaultPath = %q, want %q", p, want)
	}
}

func TestDefaultPathHonorsHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	p, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if want := filepath.Join(home, ".config", DefaultFilename); p != want {
		t.Errorf("DefaultPath = %q, want %q", p, want)
	}
}

// TestDefaultPathWithoutHomeEnv locks in the fallback that keeps preferences
// persisted when a service manager or container strips HOME and XDG_CONFIG_HOME
// (a stripped environment previously resolved to "" here and the server
// silently kept every user setting — favourites, protected paths, dotfiles,
// wipe prefs — in memory only). The path must still resolve to a writable,
// absolute location on all platforms.
func TestDefaultPathWithoutHomeEnv(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	p, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath with HOME/XDG_CONFIG_HOME empty: %v", err)
	}
	if !filepath.IsAbs(p) {
		t.Errorf("DefaultPath = %q, want absolute path", p)
	}
	if filepath.Base(p) != DefaultFilename {
		t.Errorf("DefaultPath = %q, want basename %q", p, DefaultFilename)
	}
	if dir := filepath.Dir(p); dir == "" {
		t.Errorf("DefaultPath = %q, want a non-empty parent dir", p)
	}
}

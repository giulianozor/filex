package atomicfile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := Write(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}
}

func TestWriteAppliesExactMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	// A lax umask must not widen the requested permission bits.
	old := umaskForTest(t, 0)
	defer old()
	if err := Write(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600", got)
	}
}

func TestWriteOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "new" {
		t.Errorf("content = %q, want %q", data, "new")
	}
}

func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.bin")
	if err := Write(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("Write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// TestWriteOwnedChownsFile verifies that WriteOwned leaves the final file
// owned by the requested uid/gid rather than by the process (a root daemon
// writing a user's preferences must not leave it root-owned). Chowning to the
// current user's own uid/gid requires no privileges, so the test works as a
// non-root test runner.
func TestWriteOwnedChownsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "owned")
	uid := os.Getuid()
	gid := os.Getgid()
	if err := WriteOwned(path, []byte("x"), 0o600, uid, gid); err != nil {
		t.Fatalf("WriteOwned: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skipf("no syscall.Stat_t on this platform (got %T)", info.Sys())
	}
	if st.Uid != uint32(uid) || st.Gid != uint32(gid) {
		t.Errorf("owner = %d:%d, want %d:%d", st.Uid, st.Gid, uid, gid)
	}
}

// umaskForTest pins the umask for the duration of a test and returns a
// restore function. Tests must not run in parallel.
func umaskForTest(t *testing.T, mask int) func() {
	t.Helper()
	old := syscall.Umask(mask)
	return func() { syscall.Umask(old) }
}

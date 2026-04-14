package fs

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setup(t *testing.T) (*FS, string) {
	t.Helper()
	dir := t.TempDir()
	fsys, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return fsys, dir
}

func TestNew(t *testing.T) {
	dir := t.TempDir()
	fsys, err := New(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fsys.BasePath() != dir {
		t.Errorf("BasePath = %q, want %q", fsys.BasePath(), dir)
	}
}

func TestNew_NotDir(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "file")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err = New(f.Name())
	if err == nil {
		t.Fatal("expected error for non-directory base path")
	}
}

func TestResolve_EscapeAttempts(t *testing.T) {
	fsys, _ := setup(t)
	// With the safe resolve implementation, paths that would normally escape
	// are re-rooted within basePath rather than returned as errors.
	// Verify that the resolved paths stay within the base.
	cases := []string{
		"../etc/passwd",
		"../../etc/shadow",
		"/etc/passwd",
		"subdir/../../etc/passwd",
	}
	for _, c := range cases {
		resolved, err := fsys.resolve(c)
		if err != nil {
			// Errors are also acceptable (belt-and-suspenders), but not required.
			continue
		}
		base := fsys.BasePath()
		if resolved != base && !strings.HasPrefix(resolved, base+string(filepath.Separator)) {
			t.Errorf("resolve(%q) = %q, which is outside base %q", c, resolved, base)
		}
	}
}

func TestListDir(t *testing.T) {
	fsys, dir := setup(t)
	// create some files
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi"), 0o644)
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("h"), 0o644)
	os.Mkdir(filepath.Join(dir, "subdir"), 0o755)

	entries, err := fsys.ListDir("/", false)
	if err != nil {
		t.Fatalf("ListDir: %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["hello.txt"] {
		t.Error("expected hello.txt in listing")
	}
	if !names["subdir"] {
		t.Error("expected subdir in listing")
	}
	if names[".hidden"] {
		t.Error("hidden file should be filtered when showDotfiles=false")
	}

	// With dotfiles shown
	entries, err = fsys.ListDir("/", true)
	if err != nil {
		t.Fatalf("ListDir dotfiles: %v", err)
	}
	names = map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names[".hidden"] {
		t.Error("expected .hidden when showDotfiles=true")
	}
}

func TestMkDir(t *testing.T) {
	fsys, dir := setup(t)
	if err := fsys.MkDir("/", "newdir"); err != nil {
		t.Fatalf("MkDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "newdir")); err != nil {
		t.Errorf("newdir not created: %v", err)
	}
}

func TestMkDir_EscapeAttempt(t *testing.T) {
	fsys, _ := setup(t)
	// An attempt to create a dir with a path separator in the name should fail.
	if err := fsys.MkDir("/", "../evil"); err == nil {
		t.Error("expected error for escape attempt in mkdir name")
	}
}

func TestDelete(t *testing.T) {
	fsys, dir := setup(t)
	path := filepath.Join(dir, "todelete.txt")
	os.WriteFile(path, []byte("bye"), 0o644)

	if err := fsys.Delete("/todelete.txt"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("file should have been deleted")
	}
}

func TestDelete_BaseDir(t *testing.T) {
	fsys, _ := setup(t)
	if err := fsys.Delete("/"); err == nil {
		t.Error("expected error when deleting base directory")
	}
}

func TestRename(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "old.txt"), []byte("x"), 0o644)
	if err := fsys.Rename("/old.txt", "new.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Errorf("renamed file not found: %v", err)
	}
}

func TestMove(t *testing.T) {
	fsys, dir := setup(t)
	os.Mkdir(filepath.Join(dir, "dest"), 0o755)
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644)

	if err := fsys.Move("/file.txt", "/dest"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dest", "file.txt")); err != nil {
		t.Errorf("moved file not found: %v", err)
	}
}

func TestReadWriteFile(t *testing.T) {
	fsys, _ := setup(t)
	content := []byte("hello, filex!")
	if err := fsys.WriteFile("/test.txt", content); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := fsys.ReadFile("/test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}
}

func TestFileInfo(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "info.txt"), []byte("data"), 0o644)
	entry, err := fsys.FileInfo("/info.txt")
	if err != nil {
		t.Fatalf("FileInfo: %v", err)
	}
	if entry.Name != "info.txt" {
		t.Errorf("Name = %q, want info.txt", entry.Name)
	}
	if entry.Size != 4 {
		t.Errorf("Size = %d, want 4", entry.Size)
	}
}

func TestCopy_File(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "src.txt"), []byte("hello"), 0o644)

	if err := fsys.Copy("/src.txt", "/dst.txt"); err != nil {
		t.Fatalf("Copy: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "dst.txt"))
	if err != nil {
		t.Fatalf("dst.txt not found: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", string(data), "hello")
	}
	// src must still exist
	if _, err := os.Stat(filepath.Join(dir, "src.txt")); err != nil {
		t.Error("src.txt should still exist after copy")
	}
}

func TestCopy_DirIntoExisting(t *testing.T) {
	fsys, dir := setup(t)
	os.Mkdir(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("a"), 0o644)
	os.Mkdir(filepath.Join(dir, "dest"), 0o755)

	if err := fsys.Copy("/src", "/dest"); err != nil {
		t.Fatalf("Copy dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dest", "src", "a.txt")); err != nil {
		t.Errorf("dest/src/a.txt not found: %v", err)
	}
}

func TestZipPaths_File(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("world"), 0o644)

	if err := fsys.ZipPaths(io.Discard, []string{"/hello.txt"}); err != nil {
		t.Fatalf("ZipPaths: %v", err)
	}
}

func TestZipPaths_Dir(t *testing.T) {
	fsys, dir := setup(t)
	os.Mkdir(filepath.Join(dir, "mydir"), 0o755)
	os.WriteFile(filepath.Join(dir, "mydir", "f.txt"), []byte("data"), 0o644)

	if err := fsys.ZipPaths(io.Discard, []string{"/mydir"}); err != nil {
		t.Fatalf("ZipPaths dir: %v", err)
	}
}

func TestZipPaths_Multiple(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644)

	if err := fsys.ZipPaths(io.Discard, []string{"/a.txt", "/b.txt"}); err != nil {
		t.Fatalf("ZipPaths multiple: %v", err)
	}
}

func TestDiskUsage(t *testing.T) {
	fsys, _ := setup(t)
	usage, err := fsys.DiskUsage()
	if err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
	if usage.Total == 0 {
		t.Error("total disk size should be > 0")
	}
}

func TestMimeHint(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"photo.jpg", "image"},
		{"clip.mp4", "video"},
		{"song.mp3", "audio"},
		{"doc.pdf", "pdf"},
		{"archive.zip", "archive"},
		{"main.go", "code"},
		{"readme.txt", "text"},
		{"unknown.xyz", "file"},
	}
	for _, c := range cases {
		got := mimeHint(c.name, false)
		if got != c.want {
			t.Errorf("mimeHint(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	if mimeHint("anything", true) != "dir" {
		t.Error("mimeHint with isDir=true should return 'dir'")
	}
}

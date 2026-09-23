package fs

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
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

func TestCreateFile(t *testing.T) {
	fsys, dir := setup(t)
	vpath, err := fsys.CreateFile("/", "new.txt")
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}
	if vpath != "/new.txt" {
		t.Errorf("vpath = %q, want /new.txt", vpath)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Errorf("new.txt not created: %v", err)
	}
}

func TestCreateFile_AlreadyExists(t *testing.T) {
	fsys, dir := setup(t)
	if err := os.WriteFile(filepath.Join(dir, "exists.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fsys.CreateFile("/", "exists.txt"); err == nil {
		t.Error("expected error creating an existing file")
	}
}

func TestCreateFile_PathSeparatorName(t *testing.T) {
	fsys, _ := setup(t)
	if _, err := fsys.CreateFile("/", "../evil.txt"); err == nil {
		t.Error("expected error for path separator in file name")
	}
}

func TestCreateFile_MissingParent(t *testing.T) {
	fsys, _ := setup(t)
	if _, err := fsys.CreateFile("/no/such/dir", "x.txt"); err == nil {
		t.Error("expected error when parent directory does not exist")
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

func TestWipe_MethodDefault(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "x.bin"), make([]byte, 1<<20), 0o644)

	if err := fsys.Wipe("/x.bin"); err != nil {
		t.Fatalf("Wipe: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.bin")); !os.IsNotExist(err) {
		t.Error("file should have been wiped and removed")
	}
}

func TestWipe_ReportsProgress(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "x.bin"), make([]byte, 1<<20), 0o644)

	var pcts []float64
	if err := fsys.WipeContext(context.Background(), "/x.bin", "fast", func(p float64) {
		pcts = append(pcts, p)
	}); err != nil {
		t.Fatalf("WipeContext: %v", err)
	}
	if len(pcts) == 0 {
		t.Fatal("no progress reported")
	}
	for _, p := range pcts {
		if p < 0 || p > 100 {
			t.Errorf("pct = %v, want within [0,100]", p)
		}
	}
	if pcts[len(pcts)-1] != 100 {
		t.Errorf("final pct = %v, want 100", pcts[len(pcts)-1])
	}
}

func TestWipe_DirectoryTree(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "sub/deep"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "a.bin"), make([]byte, 64*1024), 0o644)
	os.WriteFile(filepath.Join(dir, "sub", "deep", "b.bin"), make([]byte, 64*1024), 0o644)

	if err := fsys.WipeContext(context.Background(), "/sub", "fast", nil); err != nil {
		t.Fatalf("WipeContext: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Error("directory tree should be removed")
	}
}

func TestWipe_UnknownMethod(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "keep.bin"), []byte("x"), 0o644)

	if err := fsys.WipeContext(context.Background(), "/keep.bin", "nope", nil); err == nil {
		t.Error("expected error for unknown wipe method")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.bin")); err != nil {
		t.Error("file must not be touched for unknown method")
	}
}

func TestWipe_RefusesSymlink(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "target.bin"), []byte("precious"), 0o644)
	os.Symlink(filepath.Join(dir, "target.bin"), filepath.Join(dir, "link.bin"))

	if err := fsys.WipeContext(context.Background(), "/link.bin", "fast", nil); err == nil {
		t.Error("wiping a symlink must fail without following it")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "target.bin")); err != nil || string(data) != "precious" {
		t.Errorf("symlink target must be untouched, got %q, %v", data, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "link.bin")); err != nil {
		t.Errorf("symlink itself must remain, got: %v", err)
	}
}

func TestWipe_Cancelled(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, 32<<20), 0o644)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := fsys.WipeContext(ctx, "/big.bin", "fast", nil); err == nil {
		t.Error("expected cancellation error on an already-cancelled context")
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

func TestMoveWithConflict_Error(t *testing.T) {
	fsys, dir := setup(t)
	if err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dst.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := fsys.MoveWithConflict("/src.txt", "/dst.txt", ConflictError)
	if !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("expected ErrDestinationExists, got %v", err)
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

func TestCopyWithConflict_Rename(t *testing.T) {
	fsys, dir := setup(t)
	if err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dst.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fsys.CopyWithConflict("/src.txt", "/dst.txt", ConflictRename); err != nil {
		t.Fatalf("CopyWithConflict rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dst (copy).txt")); err != nil {
		t.Fatalf("renamed destination missing: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "dst.txt")); err != nil {
		t.Fatal(err)
	} else if string(data) != "old" {
		t.Fatalf("dst.txt content = %q, want old", string(data))
	}
}

func TestCopyWithConflict_SamePath(t *testing.T) {
	fsys, dir := setup(t)
	if err := os.WriteFile(filepath.Join(dir, "src.txt"), []byte("same"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fsys.CopyWithConflict("/src.txt", "/src.txt", ConflictError); err == nil {
		t.Fatal("expected same-path copy to fail")
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
		{"readme.md", "markdown"},
		{"README.md", "markdown"},
		{"unknown.xyz", "file"},
		// qBittorrent incomplete downloads: strip .!qB and use underlying ext
		{"clip.mp4.!qB", "video"},
		{"readme.txt.!qB", "text"},
		{"photo.jpg.!qB", "image"},
		{"readme.md.!qB", "markdown"},
		{"song.mp3.!qB", "audio"},
		{"doc.pdf.!qB", "pdf"},
		{"archive.zip.!qB", "archive"},
		{"main.go.!qB", "code"},
		{"unknown.xyz.!qB", "file"},
		// case-insensitive suffix match
		{"clip.mp4.!QB", "video"},
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

// ── MoveBatch / CopyBatch ───────────────────────────────────────────────────

func TestMoveBatch_Basic(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("B"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)

	conflicts, err := fsys.MoveBatch([]string{"/a.txt", "/b.txt"}, "/dst", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", conflicts)
	}
	if _, err := os.Stat(filepath.Join(dir, "dst", "a.txt")); err != nil {
		t.Error("a.txt not moved")
	}
	if _, err := os.Stat(filepath.Join(dir, "dst", "b.txt")); err != nil {
		t.Error("b.txt not moved")
	}
}

func TestMoveBatch_Empty(t *testing.T) {
	fsys, _ := setup(t)
	conflicts, err := fsys.MoveBatch(nil, "/", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatal("unexpected conflicts")
	}
}

func TestMoveBatch_SelfMove(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte("x"), 0o644)

	conflicts, err := fsys.MoveBatch([]string{"/sub/f.txt"}, "/sub", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatal("unexpected conflicts")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "sub", "f.txt"))
	if string(data) != "x" {
		t.Errorf("content = %q, want x", data)
	}
}

func TestMoveBatch_ConflictError(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "dst", "a.txt"), []byte("X"), 0o644)

	conflicts, err := fsys.MoveBatch([]string{"/a.txt"}, "/dst", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0] != "/dst/a.txt" {
		t.Errorf("conflict = %q, want /dst/a.txt", conflicts[0])
	}
}

func TestMoveBatch_Overwrite(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("NEW"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "dst", "a.txt"), []byte("OLD"), 0o644)

	conflicts, err := fsys.MoveBatch([]string{"/a.txt"}, "/dst", ConflictOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatal("unexpected conflicts")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "dst", "a.txt"))
	if string(data) != "NEW" {
		t.Errorf("content = %q, want NEW", data)
	}
}

func TestMoveBatch_Rename(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "dst", "a.txt"), []byte("OLD"), 0o644)

	conflicts, err := fsys.MoveBatch([]string{"/a.txt"}, "/dst", ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatal("unexpected conflicts")
	}
	if _, err := os.Stat(filepath.Join(dir, "dst", "a (copy).txt")); err != nil {
		t.Error("renamed file not found")
	}
	data, _ := os.ReadFile(filepath.Join(dir, "dst", "a (copy).txt"))
	if string(data) != "A" {
		t.Errorf("content = %q, want A", data)
	}
}

func TestMoveBatch_DuplicateBasenames_Rename(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "x"), 0o755)
	os.MkdirAll(filepath.Join(dir, "y"), 0o755)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "x", "a.txt"), []byte("first"), 0o644)
	os.WriteFile(filepath.Join(dir, "y", "a.txt"), []byte("second"), 0o644)

	conflicts, err := fsys.MoveBatch([]string{"/x/a.txt", "/y/a.txt"}, "/dst", ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatal("unexpected conflicts")
	}
	data1, err := os.ReadFile(filepath.Join(dir, "dst", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(filepath.Join(dir, "dst", "a (copy).txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data1) != "first" || string(data2) != "second" {
		t.Errorf("got %q and %q, want first and second", data1, data2)
	}
}

func TestMoveBatch_DuplicateBasenames_Error(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "x"), 0o755)
	os.MkdirAll(filepath.Join(dir, "y"), 0o755)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "x", "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "y", "a.txt"), []byte("b"), 0o644)

	conflicts, err := fsys.MoveBatch([]string{"/x/a.txt", "/y/a.txt"}, "/dst", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %d: %v", len(conflicts), conflicts)
	}
}

// TestMoveBatch_RenameNoCollision verifies that within a single batch, an
// on-disk rename does not reuse a destination name already claimed by an
// earlier source of the same batch (regression for claimed-map propagation).
func TestMoveBatch_RenameNoCollision(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "x"), 0o755)
	os.MkdirAll(filepath.Join(dir, "y"), 0o755)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "x", "a.txt"), []byte("first"), 0o644)
	os.WriteFile(filepath.Join(dir, "y", "a.txt"), []byte("second"), 0o644)
	os.WriteFile(filepath.Join(dir, "dst", "a.txt"), []byte("OLD"), 0o644)

	conflicts, err := fsys.MoveBatch([]string{"/x/a.txt", "/y/a.txt"}, "/dst", ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", conflicts)
	}
	// The on-disk a.txt is renamed to a (copy).txt, and the second source must
	// NOT collide with that claimed name; it should get a (copy 2).txt.
	data1, err := os.ReadFile(filepath.Join(dir, "dst", "a (copy).txt"))
	if err != nil {
		t.Fatalf("a (copy).txt missing: %v", err)
	}
	data2, err := os.ReadFile(filepath.Join(dir, "dst", "a (copy 2).txt"))
	if err != nil {
		t.Fatalf("a (copy 2).txt missing: %v", err)
	}
	// Which source lands where depends on iteration order, but all three distinct
	// destination files must exist and hold distinct content.
	contents := map[string]bool{string(data1): true, string(data2): true}
	if !contents["first"] || !contents["second"] {
		t.Errorf("missing distinct content, got %v", contents)
	}
}

// ── MoveReloc / MoveBatchReloc ───────────────────────────────────────────────

func TestMoveReloc_ReportsDestination(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("A"), 0o644)

	dest, err := fsys.MoveReloc("/src", "/dst", ConflictOverwrite)
	if err != nil {
		t.Fatal(err)
	}
	if dest != "/dst/src" {
		t.Errorf("dest = %q, want /dst/src", dest)
	}
	if _, err := os.Stat(filepath.Join(dir, "dst", "src", "a.txt")); err != nil {
		t.Error("src tree not moved into dst")
	}
	if _, err := os.Stat(filepath.Join(dir, "src")); !os.IsNotExist(err) {
		t.Error("original src should be gone")
	}
}

func TestMoveReloc_ConflictRenameReportsCopyPath(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("NEW"), 0o644)
	os.WriteFile(filepath.Join(dir, "a (copy).txt"), []byte("TAKEN"), 0o644)

	dest, err := fsys.MoveReloc("/a.txt", "/a (copy).txt", ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if dest != "/a (copy) (copy).txt" {
		t.Errorf("dest = %q, want /a (copy) (copy).txt", dest)
	}
	if _, err := os.Stat(filepath.Join(dir, "a (copy) (copy).txt")); err != nil {
		t.Error("renamed destination missing")
	}
}

func TestMoveBatchReloc_ReportsEachDestination(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("B"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)

	conflicts, relocs, err := fsys.MoveBatchReloc([]string{"/a.txt", "/b.txt"}, "/dst", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", conflicts)
	}
	want := map[string]string{"/a.txt": "/dst/a.txt", "/b.txt": "/dst/b.txt"}
	if len(relocs) != len(want) {
		t.Fatalf("relocs = %v, want %d entries", relocs, len(want))
	}
	for _, rel := range relocs {
		if w, ok := want[rel[0]]; !ok || rel[1] != w {
			t.Errorf("reloc %v not in %v", rel, want)
		}
	}
}

func TestMoveBatchReloc_NoRelocsOnConflict(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "dst", "a.txt"), []byte("X"), 0o644)

	conflicts, relocs, err := fsys.MoveBatchReloc([]string{"/a.txt"}, "/dst", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %v", conflicts)
	}
	if len(relocs) != 0 {
		t.Errorf("relocs = %v, want none when nothing moved", relocs)
	}
}

func TestCopyBatch_Basic(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("B"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)

	conflicts, err := fsys.CopyBatch([]string{"/a.txt", "/b.txt"}, "/dst", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", conflicts)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt")); err != nil {
		t.Error("a.txt should still exist after copy")
	}
	if _, err := os.Stat(filepath.Join(dir, "dst", "a.txt")); err != nil {
		t.Error("a.txt not copied")
	}
	if _, err := os.Stat(filepath.Join(dir, "dst", "b.txt")); err != nil {
		t.Error("b.txt not copied")
	}
}

func TestCopyBatch_Empty(t *testing.T) {
	fsys, _ := setup(t)
	conflicts, err := fsys.CopyBatch(nil, "/", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatal("unexpected conflicts")
	}
}

func TestCopyBatch_DuplicateBasenames_Rename(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "x"), 0o755)
	os.MkdirAll(filepath.Join(dir, "y"), 0o755)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "x", "a.txt"), []byte("first"), 0o644)
	os.WriteFile(filepath.Join(dir, "y", "a.txt"), []byte("second"), 0o644)

	conflicts, err := fsys.CopyBatch([]string{"/x/a.txt", "/y/a.txt"}, "/dst", ConflictRename)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatal("unexpected conflicts")
	}
	data1, err := os.ReadFile(filepath.Join(dir, "dst", "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	data2, err := os.ReadFile(filepath.Join(dir, "dst", "a (copy).txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data1) != "first" || string(data2) != "second" {
		t.Errorf("got %q and %q, want first and second", data1, data2)
	}
}

func TestCopyBatch_DuplicateBasenames_Error(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "x"), 0o755)
	os.MkdirAll(filepath.Join(dir, "y"), 0o755)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "x", "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "y", "a.txt"), []byte("b"), 0o644)

	conflicts, err := fsys.CopyBatch([]string{"/x/a.txt", "/y/a.txt"}, "/dst", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("want 1 conflict, got %d: %v", len(conflicts), conflicts)
	}
}

// errReader fails after yielding a prefix, simulating a mid-upload I/O error.
type errReader struct {
	prefix []byte
	pos    int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.prefix) {
		return 0, errors.New("simulated upload failure")
	}
	n := copy(p, r.prefix[r.pos:])
	r.pos += n
	return n, nil
}

func TestSaveUpload_RemovesPartialFileOnError(t *testing.T) {
	fsys, dir := setup(t)
	src := &errReader{prefix: []byte("ABCDEFGHIJ")}
	err := fsys.SaveUpload("/", "partial.bin", src)
	if err == nil {
		t.Fatal("expected an error from SaveUpload")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "partial.bin")); statErr == nil {
		t.Error("partial file should have been removed on error")
	}
}

func TestSaveUploadChunk_OffsetsAndTruncates(t *testing.T) {
	fsys, dir := setup(t)
	// Out-of-order chunks into a fresh 11-byte file.
	if err := fsys.SaveUploadChunk("/", "c.bin", strings.NewReader("efgh"), 4, 11); err != nil {
		t.Fatal(err)
	}
	if err := fsys.SaveUploadChunk("/", "c.bin", strings.NewReader("abc"), 0, 3); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "c.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abc" {
		t.Errorf("offset-0 truncate + write = %q, want %q (stale tail must be truncated)", got, "abc")
	}
}

func TestCopyBatch_Progress(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.bin"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.bin"), make([]byte, 8192), 0o644)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)

	fileStarts := 0
	maxPct := 0.0
	names := map[string]bool{}
	_, err := fsys.CopyBatch([]string{"/a.bin", "/b.bin"}, "/dst", ConflictError, func(p TransferProgress) {
		if p.FilePct == nil {
			fileStarts++
			names[p.Current] = true
			return
		}
		if *p.FilePct > maxPct {
			maxPct = *p.FilePct
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if fileStarts != 2 {
		t.Errorf("file-level progress events = %d, want 2", fileStarts)
	}
	if !names["a.bin"] || !names["b.bin"] {
		t.Errorf("reported names = %v, want a.bin and b.bin", names)
	}
	if maxPct <= 0 || maxPct > 1 {
		t.Errorf("max byte pct = %f, want within (0,1]", maxPct)
	}
}

func TestMoveBatch_Progress(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "x.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "y.txt"), []byte("y"), 0o644)

	fileStarts := 0
	_, err := fsys.MoveBatch([]string{"/src/x.txt", "/src/y.txt"}, "/dst", ConflictError, func(p TransferProgress) {
		if p.FilePct == nil {
			fileStarts++
			if p.FileTotal != 2 {
				t.Errorf("file total = %d, want 2", p.FileTotal)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if fileStarts != 2 {
		t.Errorf("file-level progress events = %d, want 2", fileStarts)
	}
}

func TestCopyWithConflict_ByteProgress(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, 65536), 0o644)

	seen := false
	if err := fsys.CopyWithConflict("/big.bin", "/big-copy.bin", ConflictError, func(pct float64) {
		seen = true
		if pct < 0 || pct > 1 {
			t.Errorf("byte pct = %f, want within [0,1]", pct)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Error("no byte progress reported")
	}
	if _, err := os.Stat(filepath.Join(dir, "big-copy.bin")); err != nil {
		t.Errorf("copy did not complete: %v", err)
	}
}

func TestMove_IntoOwnSubdirectory(t *testing.T) {
	fsys, dir := setup(t)
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "a", "f.txt"), []byte("x"), 0o644)

	// Moving a directory into one of its own subdirectories cannot be
	// expressed as a rename and must fail with a clear message (not a cryptic
	// syscall.EINVAL leaking to the client).
	if err := fsys.Move("/a", "/a/b"); err == nil {
		t.Fatal("expected move into own subdirectory to fail")
	} else if !strings.Contains(err.Error(), "own subdirectory") {
		t.Errorf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a", "f.txt")); err != nil {
		t.Errorf("source tree should be untouched: %v", err)
	}
}

func TestMoveBatch_NestedSourceSkipped(t *testing.T) {
	fsys, dir := setup(t)
	os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755)
	os.WriteFile(filepath.Join(dir, "a", "b", "f.txt"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(dir, "dst"), 0o755)

	// "/a/b" lives inside "/a"; staging both must drop the nested one so the
	// wildcard-style move does not double-apply or fail on already-moved paths.
	conflicts, err := fsys.MoveBatch([]string{"/a", "/a/b"}, "/dst", ConflictError)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", conflicts)
	}
	if _, err := os.Stat(filepath.Join(dir, "dst", "a", "b", "f.txt")); err != nil {
		t.Errorf("file not moved to dst/a/b/f.txt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dst", "b")); err == nil {
		t.Error("nested source '/a/b' was staged on its own")
	}
}

func TestWipeContext_DirWithSymlink(t *testing.T) {
	fsys, dir := setup(t)
	target := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "wipe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "wipe", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wipe", "data.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wiping a directory that contains a symlink must not follow the link
	// (host file must survive) or abort the wipe.
	if err := fsys.WipeContext(context.Background(), "/wipe", "dod", nil); err != nil {
		t.Fatalf("WipeContext: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "wipe")); !os.IsNotExist(err) {
		t.Error("wiped directory should be gone")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "keep" {
		t.Errorf("symlink target must be untouched, err=%v data=%q", err, data)
	}
}

func TestSafeArchiveMember(t *testing.T) {
	cases := []struct {
		member string
		want   bool
	}{
		{"foo.txt", true},
		{"dir/bar.txt", true},
		{"dir/", true},
		{"/etc/passwd", false},
		{"../escape", false},
		{"a/../../escape", false},
		{"C:/windows", false},
		{"..\\win", false},
		{"dir\\sub", false},
		{"", true},
	}
	for _, c := range cases {
		if got := safeArchiveMember(c.member); got != c.want {
			t.Errorf("safeArchiveMember(%q) = %v, want %v", c.member, got, c.want)
		}
	}
}

func TestZipPaths_StoresRelativeEntries(t *testing.T) {
	fsys, dir := setup(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0o644)

	var buf bytes.Buffer
	if err := fsys.ZipPaths(&buf, []string{"/a.txt", "/sub"}); err != nil {
		t.Fatalf("ZipPaths: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	if len(zr.File) == 0 {
		t.Fatal("zip has no entries")
	}
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") {
			t.Errorf("zip contains escaping entry: %q", f.Name)
		}
	}
}

package fs

import (
"archive/zip"
"errors"
"fmt"
"io"
"os"
"path/filepath"
"strings"
"syscall"
"time"
)

var ErrOutsideBase = errors.New("path is outside the allowed base directory")

type FileEntry struct {
Name     string      `json:"name"`
Path     string      `json:"path"`
Size     int64       `json:"size"`
ModTime  time.Time   `json:"mod_time"`
IsDir    bool        `json:"is_dir"`
Mode     os.FileMode `json:"mode"`
MimeHint string      `json:"mime_hint"`
}

type DiskUsage struct {
Total   uint64  `json:"total"`
Free    uint64  `json:"free"`
Used    uint64  `json:"used"`
UsedPct float64 `json:"used_pct"`
}

// FS is a path-jailed filesystem helper.
// uid and gid are used to chown newly created files; use -1 to disable chown.
type FS struct {
basePath string
uid      int
gid      int
}

func New(basePath string) (*FS, error) {
return NewWithOwner(basePath, -1, -1)
}

func NewWithOwner(basePath string, uid, gid int) (*FS, error) {
abs, err := filepath.Abs(basePath)
if err != nil {
return nil, err
}
info, err := os.Stat(abs)
if err != nil {
return nil, err
}
if !info.IsDir() {
return nil, fmt.Errorf("base path %q is not a directory", abs)
}
return &FS{basePath: abs, uid: uid, gid: gid}, nil
}

func (f *FS) BasePath() string { return f.basePath }

// chown sets ownership of path for fields that have been set (>= 0).
// A value of -1 means "do not change this field" (passed directly to os.Lchown).
func (f *FS) chown(path string) {
// Only call Lchown when at least one of uid/gid is specified.
if f.uid < 0 && f.gid < 0 {
return
}
_ = os.Lchown(path, f.uid, f.gid)
}

// resolve treats path as relative to basePath ("/" means the base root).
func (f *FS) resolve(path string) (string, error) {
rel := strings.TrimLeft(filepath.Clean("/"+path), "/")
target := filepath.Join(f.basePath, rel)
return f.validateAbs(target)
}

// validateAbs checks that an already-absolute path is inside basePath.
func (f *FS) validateAbs(abs string) (string, error) {
clean := filepath.Clean(abs)
base := filepath.Clean(f.basePath)
if clean != base && !strings.HasPrefix(clean, base+string(filepath.Separator)) {
return "", ErrOutsideBase
}
return clean, nil
}

// ListDir returns entries in the given directory path.
func (f *FS) ListDir(path string, showDotfiles bool) ([]FileEntry, error) {
abs, err := f.resolve(path)
if err != nil {
return nil, err
}
entries, err := os.ReadDir(abs)
if err != nil {
return nil, err
}
result := make([]FileEntry, 0, len(entries))
for _, e := range entries {
if !showDotfiles && strings.HasPrefix(e.Name(), ".") {
continue
}
info, err := e.Info()
if err != nil {
continue
}
relPath := strings.TrimPrefix(filepath.Join(abs, e.Name()), f.basePath)
if relPath == "" {
relPath = "/"
}
result = append(result, FileEntry{
Name:     e.Name(),
Path:     relPath,
Size:     info.Size(),
ModTime:  info.ModTime(),
IsDir:    e.IsDir(),
Mode:     info.Mode(),
MimeHint: mimeHint(e.Name(), e.IsDir()),
})
}
return result, nil
}

// MkDir creates a directory inside parent.
func (f *FS) MkDir(parent, name string) error {
absParent, err := f.resolve(parent)
if err != nil {
return err
}
newDir := filepath.Join(absParent, name)
validDir, err := f.validateAbs(newDir)
if err != nil {
return err
}
if err := os.MkdirAll(validDir, 0o755); err != nil {
return err
}
f.chown(validDir)
return nil
}

// Delete removes a file or directory (recursive).
func (f *FS) Delete(path string) error {
abs, err := f.resolve(path)
if err != nil {
return err
}
if abs == filepath.Clean(f.basePath) {
return errors.New("cannot delete the base directory")
}
return os.RemoveAll(abs)
}

// Rename renames src to newName within the same directory.
func (f *FS) Rename(path, newName string) error {
abs, err := f.resolve(path)
if err != nil {
return err
}
newPath := filepath.Join(filepath.Dir(abs), newName)
validPath, err := f.validateAbs(newPath)
if err != nil {
return err
}
return os.Rename(abs, validPath)
}

// Move moves src to dst (dst is directory or new name).
func (f *FS) Move(src, dst string) error {
absSrc, err := f.resolve(src)
if err != nil {
return err
}
absDst, err := f.resolve(dst)
if err != nil {
return err
}
info, err := os.Stat(absDst)
if err == nil && info.IsDir() {
candidate := filepath.Join(absDst, filepath.Base(absSrc))
absDst, err = f.validateAbs(candidate)
if err != nil {
return err
}
}
return os.Rename(absSrc, absDst)
}

// ReadFile returns contents of a text file (max 10 MB).
func (f *FS) ReadFile(path string) ([]byte, error) {
abs, err := f.resolve(path)
if err != nil {
return nil, err
}
info, err := os.Stat(abs)
if err != nil {
return nil, err
}
const maxSize = 10 * 1024 * 1024
if info.Size() > maxSize {
return nil, fmt.Errorf("file too large to edit (%d bytes)", info.Size())
}
return os.ReadFile(abs)
}

// WriteFile writes content to a file, creating it if necessary.
func (f *FS) WriteFile(path string, content []byte) error {
abs, err := f.resolve(path)
if err != nil {
return err
}
if err := os.WriteFile(abs, content, 0o644); err != nil {
return err
}
f.chown(abs)
return nil
}

// FileInfo returns metadata for a single path.
func (f *FS) FileInfo(path string) (*FileEntry, error) {
abs, err := f.resolve(path)
if err != nil {
return nil, err
}
info, err := os.Stat(abs)
if err != nil {
return nil, err
}
relPath := strings.TrimPrefix(abs, f.basePath)
if relPath == "" {
relPath = "/"
}
return &FileEntry{
Name:     info.Name(),
Path:     relPath,
Size:     info.Size(),
ModTime:  info.ModTime(),
IsDir:    info.IsDir(),
Mode:     info.Mode(),
MimeHint: mimeHint(info.Name(), info.IsDir()),
}, nil
}

// OpenForDownload returns a ReadCloser for the file at path.
func (f *FS) OpenForDownload(path string) (*os.File, error) {
abs, err := f.resolve(path)
if err != nil {
return nil, err
}
return os.Open(abs)
}

// SaveUpload writes the uploaded content to path/filename.
func (f *FS) SaveUpload(dirPath, filename string, src io.Reader) error {
	absDir, err := f.resolve(dirPath)
	if err != nil {
		return err
	}
	destPath := filepath.Join(absDir, filepath.Base(filename))
	validDest, err := f.validateAbs(destPath)
	if err != nil {
		return err
	}
	dst, err := os.Create(validDest)
	if err != nil {
		return err
	}
	if _, err = io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	if err = dst.Close(); err != nil {
		return err
	}
	f.chown(validDest)
	return nil
}

// DiskUsage returns disk usage statistics for the base path.
func (f *FS) DiskUsage() (*DiskUsage, error) {
return f.diskUsageFor(f.basePath)
}

// DiskUsageAt returns disk usage statistics for the filesystem containing
// the given path (which must be within the base jail).
func (f *FS) DiskUsageAt(path string) (*DiskUsage, error) {
abs, err := f.resolve(path)
if err != nil {
return nil, err
}
return f.diskUsageFor(abs)
}

func (f *FS) diskUsageFor(abs string) (*DiskUsage, error) {
var stat syscall.Statfs_t
if err := syscall.Statfs(abs, &stat); err != nil {
return nil, err
}
total := stat.Blocks * uint64(stat.Bsize)
free := stat.Bfree * uint64(stat.Bsize)
used := total - free
var usedPct float64
if total > 0 {
usedPct = float64(used) / float64(total) * 100
}
return &DiskUsage{
Total:   total,
Free:    free,
Used:    used,
UsedPct: usedPct,
}, nil
}

// Copy recursively copies src to dst within the base jail.
// If dst is an existing directory, src is placed inside it.
func (f *FS) Copy(src, dst string) error {
	absSrc, err := f.resolve(src)
	if err != nil {
		return err
	}
	absDst, err := f.resolve(dst)
	if err != nil {
		return err
	}
	// If dst is an existing directory, copy src inside it.
	if info, err2 := os.Stat(absDst); err2 == nil && info.IsDir() {
		candidate := filepath.Join(absDst, filepath.Base(absSrc))
		absDst, err = f.validateAbs(candidate)
		if err != nil {
			return err
		}
	}
	return f.copyAll(absSrc, absDst)
}

func (f *FS) copyAll(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, info.Mode()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := f.copyAll(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		f.chown(dst)
		return nil
	}
	return f.copyFile(src, dst, info.Mode())
}

func (f *FS) copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	f.chown(dst)
	return nil
}

// ZipPaths streams a zip archive containing all specified paths to dst.
// Each path may be a file or a directory (archived recursively).
// The zip entries are named relative to the parent directory of each path.
func (f *FS) ZipPaths(dst io.Writer, paths []string) error {
	zw := zip.NewWriter(dst)
	for _, path := range paths {
		abs, err := f.resolve(path)
		if err != nil {
			zw.Close()
			return err
		}
		if err := zipAdd(zw, abs, filepath.Dir(abs)); err != nil {
			zw.Close()
			return err
		}
	}
	return zw.Close()
}

// zipAdd recursively adds abs (relative to base) into zw.
func zipAdd(zw *zip.Writer, abs, base string) error {
	return filepath.Walk(abs, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(base, filePath)
		if err != nil {
			return err
		}
		zipPath := filepath.ToSlash(relPath)
		if info.IsDir() {
			if zipPath != "." {
				_, err = zw.Create(zipPath + "/")
			}
			return err
		}
		w, err := zw.Create(zipPath)
		if err != nil {
			return err
		}
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.Copy(w, file)
		return err
	})
}

// mimeHint returns a short string categorising the file type.
func mimeHint(name string, isDir bool) string {
if isDir {
return "dir"
}
ext := strings.ToLower(filepath.Ext(name))
switch ext {
case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".webp", ".svg", ".ico":
return "image"
case ".mp4", ".mkv", ".avi", ".mov", ".webm", ".ogv":
return "video"
case ".mp3", ".ogg", ".wav", ".flac", ".aac", ".m4a":
return "audio"
case ".pdf":
return "pdf"
case ".zip", ".tar", ".gz", ".bz2", ".xz", ".rar", ".7z":
return "archive"
case ".go", ".py", ".js", ".ts", ".html", ".css", ".json",
".yaml", ".yml", ".toml", ".sh", ".bash", ".c", ".cpp",
".h", ".java", ".rb", ".rs", ".php", ".xml", ".sql":
return "code"
case ".txt", ".md", ".log", ".csv", ".ini", ".env", ".conf":
return "text"
default:
return "file"
}
}

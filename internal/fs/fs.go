package fs

import (
"archive/zip"
"errors"
"fmt"
"io"
"os"
"path/filepath"
"runtime"
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

// runAs executes fn with the process's effective UID/GID temporarily switched
// to f.uid/f.gid so that all file-system operations inside fn run with the
// login user's identity (respecting ownership and permission checks).
//
// When both uid and gid are -1 (not configured) fn is called directly.
//
// The goroutine is pinned to its OS thread for the duration of the call
// because Linux per-thread credentials (setreuid/setregid) only affect the
// calling thread.  The server must be started as root (or hold CAP_SETUID /
// CAP_SETGID) for the credential switch to succeed.
func (f *FS) runAs(fn func() error) error {
if f.uid < 0 && f.gid < 0 {
return fn()
}

runtime.LockOSThread()

origEUID := syscall.Geteuid()
origEGID := syscall.Getegid()

// Drop to target GID first while we still hold the original (root) UID,
// because once the UID is dropped we may lack the privilege to change GID.
if f.gid >= 0 {
if err := syscall.Setregid(-1, f.gid); err != nil {
runtime.UnlockOSThread()
return fmt.Errorf("setegid %d: %w", f.gid, err)
}
}
if f.uid >= 0 {
if err := syscall.Setreuid(-1, f.uid); err != nil {
// Best-effort: restore GID before surfacing the error.
if f.gid >= 0 {
_ = syscall.Setregid(-1, origEGID)
}
runtime.UnlockOSThread()
return fmt.Errorf("seteuid %d: %w", f.uid, err)
}
}

err := fn()

// Restore UID first (regain elevated privileges) then GID.
if f.uid >= 0 {
if restoreErr := syscall.Setreuid(-1, origEUID); restoreErr != nil {
// This should never happen when the server started as root.
// Panic rather than leave this OS thread with the wrong identity.
panic(fmt.Sprintf("fs: failed to restore eUID to %d: %v", origEUID, restoreErr))
}
}
if f.gid >= 0 {
if restoreErr := syscall.Setregid(-1, origEGID); restoreErr != nil {
panic(fmt.Sprintf("fs: failed to restore eGID to %d: %v", origEGID, restoreErr))
}
}

runtime.UnlockOSThread()
return err
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
var result []FileEntry
err := f.runAs(func() error {
abs, err := f.resolve(path)
if err != nil {
return err
}
entries, err := os.ReadDir(abs)
if err != nil {
return err
}
result = make([]FileEntry, 0, len(entries))
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
return nil
})
return result, err
}

// MkDir creates a directory inside parent.
func (f *FS) MkDir(parent, name string) error {
return f.runAs(func() error {
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
})
}

// Delete removes a file or directory (recursive).
func (f *FS) Delete(path string) error {
return f.runAs(func() error {
abs, err := f.resolve(path)
if err != nil {
return err
}
if abs == filepath.Clean(f.basePath) {
return errors.New("cannot delete the base directory")
}
return os.RemoveAll(abs)
})
}

// Rename renames src to newName within the same directory.
func (f *FS) Rename(path, newName string) error {
return f.runAs(func() error {
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
})
}

// Move moves src to dst (dst is directory or new name).
// If src and dst are on different filesystems (EXDEV), it falls back to a
// recursive copy followed by removal of the source.
func (f *FS) Move(src, dst string) error {
	return f.runAs(func() error {
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
		if err := os.Rename(absSrc, absDst); err != nil {
			var errno syscall.Errno
			if errors.As(err, &errno) && errno == syscall.EXDEV {
				return f.moveXDev(absSrc, absDst)
			}
			return err
		}
		return nil
	})
}

// moveXDev performs a cross-device move by copying src to dst then removing src.
func (f *FS) moveXDev(src, dst string) error {
	if err := f.copyAll(src, dst); err != nil {
		_ = os.RemoveAll(dst) // clean up partial copy
		return fmt.Errorf("cross-device move failed: %w", err)
	}
	return os.RemoveAll(src)
}

// ReadFile returns contents of a text file (max 10 MB).
func (f *FS) ReadFile(path string) ([]byte, error) {
var data []byte
err := f.runAs(func() error {
abs, err := f.resolve(path)
if err != nil {
return err
}
info, err := os.Stat(abs)
if err != nil {
return err
}
const maxSize = 10 * 1024 * 1024
if info.Size() > maxSize {
return fmt.Errorf("file too large to edit (%d bytes)", info.Size())
}
data, err = os.ReadFile(abs)
return err
})
return data, err
}

// WriteFile writes content to a file, creating it if necessary.
func (f *FS) WriteFile(path string, content []byte) error {
return f.runAs(func() error {
abs, err := f.resolve(path)
if err != nil {
return err
}
if err := os.WriteFile(abs, content, 0o644); err != nil {
return err
}
f.chown(abs)
return nil
})
}

// FileInfo returns metadata for a single path.
func (f *FS) FileInfo(path string) (*FileEntry, error) {
var entry *FileEntry
err := f.runAs(func() error {
abs, err := f.resolve(path)
if err != nil {
return err
}
info, err := os.Stat(abs)
if err != nil {
return err
}
relPath := strings.TrimPrefix(abs, f.basePath)
if relPath == "" {
relPath = "/"
}
entry = &FileEntry{
Name:     info.Name(),
Path:     relPath,
Size:     info.Size(),
ModTime:  info.ModTime(),
IsDir:    info.IsDir(),
Mode:     info.Mode(),
MimeHint: mimeHint(info.Name(), info.IsDir()),
}
return nil
})
return entry, err
}

// OpenForDownload returns an open file handle for the file at path.
// Access is checked at open time (inside runAs) using the login user's
// credentials; the returned *os.File is safe to read after runAs returns
// because Unix file descriptors retain their access rights for the lifetime
// of the handle regardless of later credential changes on the thread.
func (f *FS) OpenForDownload(path string) (*os.File, error) {
var file *os.File
err := f.runAs(func() error {
abs, err := f.resolve(path)
if err != nil {
return err
}
file, err = os.Open(abs)
return err
})
return file, err
}

// SaveUpload writes the uploaded content to path/filename.
func (f *FS) SaveUpload(dirPath, filename string, src io.Reader) error {
	return f.runAs(func() error {
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
	})
}

// DirSize returns the total size in bytes and the number of files in a directory tree.
func (f *FS) DirSize(path string) (totalBytes int64, fileCount int64, err error) {
err = f.runAs(func() error {
abs, err := f.resolve(path)
if err != nil {
return err
}
return filepath.Walk(abs, func(_ string, info os.FileInfo, err error) error {
if err != nil {
return nil // skip unreadable entries
}
if !info.IsDir() {
totalBytes += info.Size()
fileCount++
}
return nil
})
})
return
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
	return f.runAs(func() error {
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
	})
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
// All file opens, reads, and zip writing happen inside runAs so that every
// I/O operation is performed with the login user's credentials.
func (f *FS) ZipPaths(dst io.Writer, paths []string) error {
	return f.runAs(func() error {
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
	})
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
// Handle incomplete-download extensions starting with "!" (e.g. ".!qB" from
// qBittorrent) by stripping that suffix and inspecting the real extension.
if strings.HasPrefix(ext, ".!") {
name = name[:len(name)-len(ext)]
ext = strings.ToLower(filepath.Ext(name))
}
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

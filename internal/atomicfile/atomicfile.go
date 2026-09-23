// Package atomicfile provides a single, reusable implementation of
// "write data to a temporary file next to the target, then rename into place",
// so every writer (config, sessions, thumbnail manifests, editor saves, CLI
// hash updates) shares the same crash-safety guarantees instead of re-rolling
// the temp-file/chmod/fsync/rename dance with subtle differences.
package atomicfile

import (
	"io"
	"os"
	"path/filepath"
)

// Write atomically replaces path with data. The data is written to a unique
// temp file in the same directory (so concurrent writes never share a temp
// name), chmod'ed to mode regardless of the umask, flushed to disk, and only
// then renamed over path. A crash at any point therefore leaves either the old
// file or the complete new file in place — never a truncated or partial one.
// A short write is rejected rather than renaming a corrupt file into place.
func Write(path string, data []byte, mode os.FileMode) error {
	return write(path, data, mode, -1, -1)
}

// WriteOwned is Write with an extra ownership step: when at least one of uid or
// gid is set (>= 0) the temp file is chowned to that owner before it is renamed
// into place, so the final file is owned by uid/gid rather than by the process
// (e.g. a root daemon writing a user's preferences file must leave it owned by
// the user, not root). A value of -1 for a field leaves that field unchanged,
// matching the sentinel used by fs.FS.
func WriteOwned(path string, data []byte, mode os.FileMode, uid, gid int) error {
	return write(path, data, mode, uid, gid)
}

func write(path string, data []byte, mode os.FileMode, uid, gid int) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Single cleanup point: whatever fails below, close the descriptor and
	// remove the temp name. After the successful rename the name is gone and
	// the double close is a harmless no-op.
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}()

	n, err := tmp.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	if err := tmp.Chmod(mode); err != nil {
		return err
	}
	if uid >= 0 || gid >= 0 {
		if err := tmp.Chown(uid, gid); err != nil {
			return err
		}
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	// Flush the parent directory so the rename itself survives a crash; on
	// some filesystems a rename can otherwise be lost after power loss, making
	// an older version of the file reappear. Best-effort: a dir-sync failure
	// should not turn a successful rename into an API error for the caller.
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

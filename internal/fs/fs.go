package fs

import (
	"archive/zip"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	wiper "github.com/0x9ef/go-wiper/wipe"

	"github.com/giulianozor/filex/internal/atomicfile"
)

var ErrOutsideBase = errors.New("path is outside the allowed base directory")
var ErrDestinationExists = errors.New("destination already exists")

type ConflictStrategy string

const (
	ConflictError     ConflictStrategy = "error"
	ConflictOverwrite ConflictStrategy = "overwrite"
	ConflictRename    ConflictStrategy = "rename"
)

func ParseConflictStrategy(v string) (ConflictStrategy, error) {
	normalized := ConflictStrategy(strings.TrimSpace(strings.ToLower(v)))
	switch normalized {
	case "":
		return ConflictError, nil
	case ConflictError, ConflictOverwrite, ConflictRename:
		return normalized, nil
	default:
		return "", fmt.Errorf("invalid on_conflict value %q", v)
	}
}

// TransferProgress describes the current state of a move/copy batch so the UI
// can show which item is being processed and how far along it is. FileDone and
// FileTotal are the top-level source counts; Current is the item being
// processed right now. FilePct, when non-nil, is byte progress (0..1) of the
// current item (only meaningful for copies and cross-device moves).
type TransferProgress struct {
	FileDone  int
	FileTotal int
	Current   string
	FilePct   *float64
}

type transferReporter struct {
	onProgress []func(p TransferProgress)
	done       int
	total      int
	name       string
}

// broadcast delivers prog to every registered progress callback (not just the
// first). All callers pass exactly one, but honouring the full slice keeps the
// contract honest if parallel consumers are ever added.
func (tr *transferReporter) broadcast(prog TransferProgress) {
	for _, cb := range tr.onProgress {
		if cb != nil {
			cb(prog)
		}
	}
}

func (tr *transferReporter) file(done, total int, name string) {
	tr.done, tr.total, tr.name = done, total, name
	tr.broadcast(TransferProgress{FileDone: done, FileTotal: total, Current: name})
}

// cur only updates the current-item state without emitting a file-level event
// (used when a later phase, e.g. the actual copy, should keep reporting the
// same item). FileTotal is preserved.
func (tr *transferReporter) cur(done int, name string) {
	tr.done, tr.name = done, name
}

func (tr *transferReporter) byter(pct float64) {
	if len(tr.onProgress) == 0 {
		return
	}
	p := pct
	tr.broadcast(TransferProgress{FileDone: tr.done, FileTotal: tr.total, Current: tr.name, FilePct: &p})
}

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

// UID returns the configured file-owner UID, or -1 when ownership is disabled.
func (f *FS) UID() int { return f.uid }

// GID returns the configured file-owner GID, or -1 when ownership is disabled.
func (f *FS) GID() int { return f.gid }

// chown sets ownership of path for fields that have been set (>= 0).
// A value of -1 means "do not change this field" (passed directly to os.Lchown).
func (f *FS) chown(path string) error {
	// Only call Lchown when at least one of uid/gid is specified.
	if f.uid < 0 && f.gid < 0 {
		return nil
	}
	return os.Lchown(path, f.uid, f.gid)
}

// RunAs executes fn with the process's effective UID/GID temporarily switched
// to f.uid/f.gid so that all file-system operations inside fn run with the
// login user's identity (respecting ownership and permission checks).
//
// When both uid and gid are -1 (not configured) fn is called directly.
//
// The goroutine is pinned to its OS thread for the duration of the call
// because Linux per-thread credentials (setreuid/setregid) only affect the
// calling thread.  The server must be started as root (or hold CAP_SETUID /
// CAP_SETGID) for the credential switch to succeed.
func (f *FS) RunAs(fn func() error) (err error) {
	if f.uid < 0 && f.gid < 0 {
		return fn()
	}

	// Pin the goroutine to its OS thread because Linux per-thread credentials
	// (setreuid/setregid) only affect the calling thread. Both the pin release
	// and the credential restore are deferred so they also run when fn panics;
	// without this the pinned thread would keep the login user's identity and
	// be reused by the runtime for unrelated work.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	origEUID := syscall.Geteuid()
	origEGID := syscall.Getegid()

	restore := func() {
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
	}
	defer restore()

	// Drop to target GID first while we still hold the original (root) UID,
	// because once the UID is dropped we may lack the privilege to change GID.
	if f.gid >= 0 {
		if err := syscall.Setregid(-1, f.gid); err != nil {
			return fmt.Errorf("setegid %d: %w", f.gid, err)
		}
	}
	if f.uid >= 0 {
		if err := syscall.Setreuid(-1, f.uid); err != nil {
			// Best-effort: restore GID before surfacing the error.
			if f.gid >= 0 {
				_ = syscall.Setregid(-1, origEGID)
			}
			return fmt.Errorf("seteuid %d: %w", f.uid, err)
		}
	}

	return fn()
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
	// When the jail is the filesystem root every absolute path is inside it.
	if base == string(filepath.Separator) {
		return clean, nil
	}
	if !isSubPath(base, clean) {
		return "", ErrOutsideBase
	}
	return clean, nil
}

// validNamePart reports whether name is usable as a single new file or
// directory entry: non-empty, not "." or "..", and free of path separators so
// it cannot sneak into a nested or absolute path.
func validNamePart(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\`)
}

// newFileEntry builds a FileEntry from an os.FileInfo, resolving the jailed
// virtual path and mime hint. Shared by ListDir and FileInfo.
func (f *FS) newFileEntry(info os.FileInfo, absPath string) FileEntry {
	return FileEntry{
		Name:     info.Name(),
		Path:     f.toJailPath(absPath),
		Size:     info.Size(),
		ModTime:  info.ModTime(),
		IsDir:    info.IsDir(),
		Mode:     info.Mode(),
		MimeHint: mimeHint(info.Name(), info.IsDir()),
	}
}

// ListDir returns entries in the given directory path.
func (f *FS) ListDir(path string, showDotfiles bool) ([]FileEntry, error) {
	var result []FileEntry
	abs, err := f.resolve(path)
	if err != nil {
		return nil, err
	}
	err = f.RunAs(func() error {
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
			result = append(result, f.newFileEntry(info, filepath.Join(abs, e.Name())))
		}
		return nil
	})
	return result, err
}

// resolveChild validates that name is a single entry name, resolves it relative
// to the already-resolved absolute parent directory, and checks that the result
// stays inside the jail. It is the shared preamble of MkDir and CreateFile.
func (f *FS) resolveChild(absParent, name string) (string, error) {
	if !validNamePart(name) {
		return "", fmt.Errorf("invalid name %q", name)
	}
	return f.validateAbs(filepath.Join(absParent, name))
}

// resolveChildInDir resolves parent (a virtual path) and verifies it exists
// and is a directory, then returns the abs path of name inside it. MkDir and
// CreateFile share this so both fail identically on a missing or non-directory
// parent instead of each re-implementing the same LookupPath-less dance. Call
// under RunAs, like every other resolve-based helper.
func (f *FS) resolveChildInDir(parent, name string) (absParent string, child string, err error) {
	absParent, err = f.resolve(parent)
	if err != nil {
		return "", "", err
	}
	info, err := os.Lstat(absParent)
	if err != nil {
		return "", "", err
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("parent %q is not a directory", parent)
	}
	child, err = f.resolveChild(absParent, name)
	if err != nil {
		return "", "", err
	}
	return absParent, child, nil
}

// MkDir creates a directory inside parent. The parent must already exist and
// be a directory; creation is strict (single level, never follows symlinks),
// so a typo in a path cannot silently fabricate a chain of phantom parents.
func (f *FS) MkDir(parent, name string) error {
	return f.RunAs(func() error {
		_, validDir, err := f.resolveChildInDir(parent, name)
		if err != nil {
			return err
		}
		if err := os.Mkdir(validDir, 0o755); err != nil {
			return err
		}
		return f.chown(validDir)
	})
}

// resolveForDelete resolves a virtual path to an absolute path, guarding
// against deletion of the base directory itself. It performs no filesystem
// access, so it is safe (and cheaper) to call outside RunAs.
func (f *FS) resolveForDelete(path string) (string, error) {
	abs, err := f.resolve(path)
	if err != nil {
		return "", err
	}
	if abs == f.basePath {
		return "", errors.New("cannot delete the base directory")
	}
	return abs, nil
}

// Delete removes a file or directory (recursive).
func (f *FS) Delete(path string) error {
	abs, err := f.resolveForDelete(path)
	if err != nil {
		return err
	}
	return f.RunAs(func() error {
		return os.RemoveAll(abs)
	})
}

// WipeMethod describes a selectable secure-delete algorithm backed by the
// go-wiper library.
type WipeMethod struct {
	ID          string // machine-readable identifier sent over the API
	Name        string
	Description string
	Rule        *wiper.Rule
}

// WipeMethodFast is the identifier of the default secure-delete algorithm, the
// one selected when no method is specified.
const WipeMethodFast = "fast"

// WipeMethods are the secure-delete algorithms offered to users.
var WipeMethods = []WipeMethod{
	{ID: WipeMethodFast, Name: "Fast", Description: "overwrite with zeroes (1 pass)", Rule: wiper.RuleFast},
	{ID: "vsitr", Name: "VSITR", Description: "German VSITR standard (7 passes)", Rule: wiper.RuleVSITR},
	{ID: "dod", Name: "DoD 5220.22-M", Description: "US DoD 5220.22-M (3 passes)", Rule: wiper.RuleUsDod5220_22_M},
	{ID: "gutmann", Name: "Gutmann", Description: "Peter Gutmann method (35 passes)", Rule: wiper.RuleGutmann},
}

// WipeMethodByID returns the registered wipe method for id, or false when the
// identifier is unknown. An empty id resolves to the default "fast" method.
func WipeMethodByID(id string) (WipeMethod, bool) {
	if id == "" {
		id = WipeMethodFast
	}
	for _, m := range WipeMethods {
		if m.ID == id {
			return m, true
		}
	}
	return WipeMethod{}, false
}

// Wipe securely deletes a file or directory using the go-wiper library with
// the default "fast" method. No progress is reported.
func (f *FS) Wipe(path string) error {
	return f.WipeContext(context.Background(), path, WipeMethodFast, nil)
}

// WipeContext securely deletes path (file or directory tree) using the
// go-wiper library and the method selected by methodID, reporting per-file
// overwrite progress (0–100) to onPct when non-nil and aborting as soon as ctx
// is cancelled. Secure deletes overwrite every byte before unlinking, so they
// are O(file size × passes) and can be slow for large files; the progress
// callback lets callers render a real percentage.
func (f *FS) WipeContext(ctx context.Context, path, methodID string, onPct func(pct float64)) error {
	method, ok := WipeMethodByID(methodID)
	if !ok {
		return fmt.Errorf("unknown wipe method %q", methodID)
	}
	return f.RunAs(func() error {
		abs, err := f.resolveForDelete(path)
		if err != nil {
			return err
		}
		return wipeTree(ctx, abs, method.Rule, onPct)
	})
}

// wipeChunkSize matches go-wiper's 2 MiB overwrite granularity.
const wipeChunkSize = 2 * (1 << 20)

// isSymlink reports whether an os.FileMode is a symbolic link. The wipe/extract
// security checks test this inline with a mode&os.ModeSymlink expression in
// many places; the helper keeps the intent readable and the condition single.
func isSymlink(mode os.FileMode) bool { return mode&os.ModeSymlink != 0 }

// wipeTree securely overwrites and removes the file at root, or every file
// beneath a directory tree (removing now-empty directories afterwards). onPct
// receives the current file's wipe percentage.
func wipeTree(ctx context.Context, root string, rule *wiper.Rule, onPct func(pct float64)) error {
	// Lstat (not Stat): a symlink root must not be followed, otherwise the
	// wipe would overwrite whatever sits outside the jail on the other side.
	st, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if isSymlink(st.Mode()) {
		return fmt.Errorf("refusing to wipe symlink %q", root)
	}
	if !st.IsDir() {
		if err := wipeFile(ctx, root, rule, onPct); err != nil {
			return err
		}
		return os.Remove(root)
	}
	var dirs []string
	err = filepath.WalkDir(root, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return nil
		}
		// Symlink entries are removed in place (never followed), then the
		// parent directory removal below takes them with it. Failing the whole
		// wipe here would leave the tree half-deleted after earlier files have
		// already been overwritten and unlinked.
		if isSymlink(d.Type()) {
			return os.Remove(path)
		}
		if err := wipeFile(ctx, path, rule, onPct); err != nil {
			return err
		}
		return os.Remove(path)
	})
	if err != nil {
		return err
	}
	// Remove directories deepest-first now that their contents are gone.
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Remove(dirs[i]); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// wipeFile overwrites every byte of the file at path following rule, then
// leaves unlinking to the caller. Progress is reported as completed bytes over
// the total bytes (file size × number of passes); ctx cancellation aborts the
// overwrite early.
func wipeFile(ctx context.Context, path string, rule *wiper.Rule, onPct func(pct float64)) error {
	// Refuse symlinks: opening would follow the link and overwrite whatever is
	// on the other side, possibly outside the jail.
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if isSymlink(st.Mode()) {
		return fmt.Errorf("refusing to wipe symlink %q", path)
	}
	// O_WRONLY (not O_RDWR): a secure wipe only overwrites, and a read-only
	// file that the jailed user owns must still be shreddable instead of
	// aborting the whole tree wipe after earlier files were already destroyed.
	// O_WRONLY + O_NOFOLLOW: wipeFile refuses symlinks up front, but between
	// that Lstat and this open an attacker in the jail could swap the entry for
	// a symlink pointing at a host file; O_NOFOLLOW turns that race into a
	// clean ELOOP instead of silently overwriting whatever the link targets.
	fd, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer fd.Close()
	st, err = fd.Stat()
	if err != nil {
		return err
	}
	size := st.Size()
	passes := *rule
	if size == 0 || len(passes) == 0 {
		return nil
	}

	total := float64(size) * float64(len(passes))
	written := int64(0)
	lastPct := -1.0
	var buf []byte
	var pattern []byte
	for _, pass := range passes {
		pat, err := wipePassPattern(pass)
		if err != nil {
			return err
		}
		pattern = pat
		for off := int64(0); off < size; off += wipeChunkSize {
			// Check cancellation per chunk, not once per pass, so a multi-GB
			// wipe aborts within one chunk instead of finishing the pass.
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			n := size - off
			if n > wipeChunkSize {
				n = wipeChunkSize
			}
			if cap(buf) < int(n) {
				buf = make([]byte, n)
			} else {
				buf = buf[:n]
			}
			writePattern(buf, pattern)
			if _, err := fd.WriteAt(buf, off); err != nil {
				return err
			}
			written += n
			pct := float64(written) / total * 100
			if pct-lastPct >= 1 || pct >= 100 {
				lastPct = pct
				if onPct != nil {
					onPct(pct)
				}
			}
		}
	}
	if lastPct < 100 && onPct != nil {
		onPct(100)
	}
	// The caller (wipeTree / WipeContext) unlinks the file right after this
	// returns. Force the overwritten bytes to stable storage first: buffered
	// dirty pages flushed only later would let a crash after the unlink
	// resurrect the original data, defeating the entire point of secure delete.
	return fd.Sync()
}

// wipePassPattern resolves a single pass into the byte pattern repeated across
// its chunk writes: a random byte for the random flags, the pass's explicit
// data otherwise. Passes that request neither are a misuse and are rejected.
func wipePassPattern(pass wiper.Pass) ([]byte, error) {
	if pass.Random&(wiper.FlagNative|wiper.FlagCrypto) != 0 {
		b, err := wiper.GetRandomByte(255, pass.Random)
		if err != nil {
			return nil, err
		}
		return []byte{b}, nil
	}
	if len(pass.Data) > 0 {
		return pass.Data, nil
	}
	return nil, errors.New("wipe: empty pass has no data")
}

// writePattern fills dst with the repeating pattern (single byte or short run),
// doubling the copied region each iteration so a long wipe chunk takes O(log n)
// copies instead of one byte-at-a-time write. The empty branch of pattern is
// intentionally absent: wipeFile rejects empty passes, so pattern is always at
// least one byte long.
func writePattern(dst, pattern []byte) {
	if len(dst) == 0 {
		return
	}
	n := copy(dst, pattern)
	for n < len(dst) {
		n += copy(dst[n:], dst[:n])
	}
}

// Rename renames src to newName within the same directory. It refuses to
// replace an existing destination: an accidental rename must never silently
// destroy a file or directory.
func (f *FS) Rename(path, newName string) error {
	return f.RunAs(func() error {
		abs, err := f.resolve(path)
		if err != nil {
			return err
		}
		validPath, err := f.resolveChild(filepath.Dir(abs), newName)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(validPath); err == nil {
			return fmt.Errorf("destination %q already exists", f.toJailPath(validPath))
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Rename(abs, validPath)
	})
}

// Move moves src to dst (dst is directory or new name).
// If src and dst are on different filesystems (EXDEV), it falls back to a
// recursive copy followed by removal of the source.
func (f *FS) Move(src, dst string) error {
	return f.MoveWithConflict(src, dst, ConflictOverwrite)
}

// resolveMoveCopyDest resolves the concrete single-item move/copy destination:
// when the requested destination is an existing directory the source is placed
// inside it, then the on-conflict strategy is applied. requireDifferent (used
// by copies) rejects moving a path onto itself; moves treat that as a no-op.
func (f *FS) resolveMoveCopyDest(absSrc, absDst string, onConflict ConflictStrategy, requireDifferent bool) (string, error) {
	dst, err := f.resolveDirDest(absSrc, absDst)
	if err != nil {
		return "", err
	}
	if requireDifferent && absSrc == dst {
		return "", fmt.Errorf("source and destination are the same path")
	}
	// Relocating a directory into one of its own subdirectories cannot work:
	// os.Rename fails with EINVAL (or worse, moves the parents around) and a
	// recursive copy would loop forever. Reject it with a clear message instead
	// of surfacing a cryptic EINVAL that aborts the whole batch.
	if absSrc != dst && isSubPath(absSrc, dst) {
		return "", fmt.Errorf("cannot move/copy %q into its own subdirectory %q", f.toJailPath(absSrc), f.toJailPath(dst))
	}
	return f.resolveConflictDestination(absSrc, dst, onConflict)
}

func (f *FS) MoveWithConflict(src, dst string, onConflict ConflictStrategy, onPct ...func(pct float64)) error {
	_, err := f.MoveReloc(src, dst, onConflict, onPct...)
	return err
}

// MoveReloc moves a single source and returns its resulting jail path.
// Unlike MoveWithConflict it reports where the file actually ended up (the
// destination may be a directory, so base(src) is appended, or the on-conflict
// strategy may have renamed it), letting callers update path-keyed metadata
// such as delete-protection entries to follow the relocated item.
// renameOrXDev renames absSrc to absDst, falling back to a cross-device copy
// when the rename fails with EXDEV (source and destination on different
// mounts). Byter receives percentage progress for the fallback copy; on a plain
// rename no progress is reported. Shared by MoveReloc and MoveBatchReloc, which
// otherwise duplicate the EXDEV dance.
func (f *FS) renameOrXDev(absSrc, absDst string, byter ...func(pct float64)) error {
	if err := os.Rename(absSrc, absDst); err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) && errno == syscall.EXDEV {
			// Cross-device fallback copy: the source may end up elsewhere,
			// but the resolved destination is set once the move lands even
			// though no literal rename happened.
			return f.moveXDev(absSrc, absDst, byter...)
		}
		return err
	}
	return nil
}

func (f *FS) MoveReloc(src, dst string, onConflict ConflictStrategy, onPct ...func(pct float64)) (string, error) {
	var jailDst string
	err := f.resolveMoveCopy(src, dst, onConflict, false, func(absSrc, absDst string) error {
		if err := f.renameOrXDev(absSrc, absDst, onPct...); err != nil {
			return err
		}
		jailDst = f.toJailPath(absDst)
		return nil
	})
	return jailDst, err
}

// resolveMoveCopy runs the src→dst→dest resolution prelude shared by MoveReloc
// and CopyWithConflict inside RunAs, then hands the fully resolved absolute
// paths to run (still executing as the jailed user). The destination must be
// resolved under RunAs because resolveMoveCopyDest inspects and possibly
// removes filesystem entries with the caller's effective privileges.
func (f *FS) resolveMoveCopy(src, dst string, onConflict ConflictStrategy, wantCopy bool, run func(absSrc, absDst string) error) error {
	return f.RunAs(func() error {
		absSrc, err := f.resolve(src)
		if err != nil {
			return err
		}
		absDst, err := f.resolve(dst)
		if err != nil {
			return err
		}
		absDst, err = f.resolveMoveCopyDest(absSrc, absDst, onConflict, wantCopy)
		if err != nil {
			return err
		}
		return run(absSrc, absDst)
	})
}

// moveOrCopy is the per-item apply step used by applyBatch: it moves or copies
// one resolved source onto its destination, reporting byte progress via byter.
type moveOrCopy func(item batchItem, byter func(pct float64)) error

// applyBatch is the shared implementation behind MoveBatch and CopyBatch: it
// resolves every source against the destination directory dst using the
// on-conflict strategy (never mutating the filesystem for ConflictError) and
// then applies the resolved moves/copies in a single RunAs call. It returns
// the list of applied source/destination pairs (for callers that need to know
// where each item ended up), the list of conflicting jail paths, or an error
// when the operation failed.
func (f *FS) applyBatch(srcs []string, dst string, onConflict ConflictStrategy, onProgress []func(p TransferProgress), apply moveOrCopy) (applied []batchItem, conflicts []string, err error) {
	if len(srcs) == 0 {
		return nil, nil, nil
	}
	tr := transferReporter{onProgress: onProgress}
	err = f.RunAs(func() error {
		absDst, err := f.resolve(dst)
		if err != nil {
			return err
		}

		items, c, err := f.resolveBatch(srcs, absDst, onConflict, &tr)
		conflicts = c
		if err != nil || len(conflicts) > 0 {
			return err
		}

		for i, m := range items {
			if m.overwrite {
				if err := os.RemoveAll(m.absDst); err != nil {
					return err
				}
			}
			tr.file(i, len(srcs), filepath.Base(m.absSrc))
			if err := apply(m, tr.byter); err != nil {
				return err
			}
			applied = append(applied, m)
		}
		return nil
	})
	return applied, conflicts, err
}

// MoveBatch moves multiple sources into the directory dst in a single
// RunAs call (unlike MoveWithConflict, dst is always treated as a
// destination directory).  If onConflict is ConflictError and any
// destination already exists, it returns the list of conflicting
// destination paths without modifying the filesystem.  For other
// strategies (overwrite/rename) all moves are applied unconditionally.
// When provided, onProgress reports the item currently being processed
// (and byte progress for cross-device moves).
func (f *FS) MoveBatch(srcs []string, dst string, onConflict ConflictStrategy, onProgress ...func(p TransferProgress)) ([]string, error) {
	conflicts, _, err := f.MoveBatchReloc(srcs, dst, onConflict, onProgress...)
	return conflicts, err
}

// MoveBatchReloc moves like MoveBatch but also returns the source→destination
// jail-path relocations for every item that was actually moved, so callers can
// update path-keyed metadata (e.g. delete-protection entries) to follow the
// relocated files.
func (f *FS) MoveBatchReloc(srcs []string, dst string, onConflict ConflictStrategy, onProgress ...func(p TransferProgress)) (conflicts []string, relocs [][2]string, err error) {
	applied, conflicts, err := f.applyBatch(srcs, dst, onConflict, onProgress, func(m batchItem, byter func(pct float64)) error {
		return f.renameOrXDev(m.absSrc, m.absDst, byter)
	})
	if err == nil && len(conflicts) == 0 {
		for _, it := range applied {
			relocs = append(relocs, [2]string{f.toJailPath(it.absSrc), f.toJailPath(it.absDst)})
		}
	}
	return conflicts, relocs, err
}

// batchItem is a single resolved source/destination pair in a move/copy batch.
type batchItem struct {
	absSrc    string
	absDst    string
	overwrite bool
}

// resolveBatch resolves every source in srcs against absDst, applying the
// on-conflict strategy and de-duplicating destinations within the batch.
// It returns the resolved items and the list of jail paths that conflicted.
func (f *FS) resolveBatch(srcs []string, absDst string, onConflict ConflictStrategy, tr *transferReporter) ([]batchItem, []string, error) {
	var conflicts []string
	var items []batchItem
	claimed := make(map[string]bool)
	// sourceRoots holds the absolute paths already staged for the batch. A
	// source inside one of them (e.g. a batch of "/a" together with "/a/b") is
	// dropped: moving/copying the parent carries the descendant along, and
	// resolving it upfront would make applyBatch rename a path that no longer
	// exists (the parent moved first), failing the whole batch mid-way.
	var sourceRoots []string

	for i, src := range srcs {
		tr.cur(i, filepath.Base(src))
		absSrc, err := f.resolve(src)
		if err != nil {
			return nil, conflicts, err
		}
		if nestedUnder(absSrc, sourceRoots) {
			continue
		}
		candidate := filepath.Join(absDst, filepath.Base(absSrc))
		candidate, err = f.validateAbs(candidate)
		if err != nil {
			return nil, conflicts, err
		}
		if absSrc == candidate {
			continue
		}
		if claimed[candidate] {
			if onConflict == ConflictError {
				conflicts = append(conflicts, f.toJailPath(candidate))
				continue
			}
			candidate, err = f.nextAvailablePathSkipping(candidate, claimed)
			if err != nil {
				return nil, conflicts, err
			}
		}
		preCandidate := candidate
		overwrite := false
		candidate, overwrite, err = f.resolveDestination(absSrc, candidate, onConflict, claimed)
		if err != nil {
			if onConflict == ConflictError && errors.Is(err, ErrDestinationExists) {
				conflicts = append(conflicts, f.toJailPath(preCandidate))
				continue
			}
			return nil, conflicts, err
		}
		claimed[candidate] = true
		sourceRoots = append(sourceRoots, absSrc)
		items = append(items, batchItem{absSrc: absSrc, absDst: candidate, overwrite: overwrite})
	}
	return items, conflicts, nil
}

// isSubPath reports whether p equals base or lives beneath it. Both paths
// must be absolute; it is the shared "is me or a descendant" test used by the
// batch nesting guard, the move/copy into-own-subdirectory rejection and the
// recursive-copy loop guard.
func isSubPath(base, p string) bool {
	return p == base || strings.HasPrefix(p, base+string(filepath.Separator))
}

// relEscapes reports whether a cleaned relative path points at or above the
// directory it is rooted in (".." or "../..."). Shared by the jail boundary
// checks and the archive-member guard so all of them agree on what "escapes"
// means.
func relEscapes(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// nestedUnder reports whether abs sits inside any of the absolute roots
// (i.e. under a parent path already staged for this batch).
func nestedUnder(abs string, roots []string) bool {
	for _, r := range roots {
		if isSubPath(r, abs) {
			return true
		}
	}
	return false
}

// moveXDev performs a cross-device move by copying src to dst then removing src.
func (f *FS) moveXDev(src, dst string, onPct ...func(pct float64)) error {
	if err := f.copyAll(src, dst, onPct...); err != nil {
		_ = os.RemoveAll(dst) // clean up partial copy
		return fmt.Errorf("cross-device move failed: %w", err)
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("cross-device move: remove source %q: %w", f.toJailPath(src), err)
	}
	return nil
}

// maxEditorSizeBytes caps how large a text file the editor may read into
// memory. It pairs with the larger 20 MB write cap enforced by the WebDAV
// writer middleware so an edited file always round-trips.
const maxEditorSizeBytes = 10 * 1024 * 1024

// ReadFile returns contents of a text file (max maxEditorSizeBytes).
func (f *FS) ReadFile(path string) ([]byte, error) {
	var data []byte
	abs, err := f.resolve(path)
	if err != nil {
		return nil, err
	}
	err = f.RunAs(func() error {
		// Open first, then bound the read on the descriptor itself. A stat-and-
		// then-read would race on a growing file and read far more than 10 MB.
		fd, err := os.Open(abs)
		if err != nil {
			return err
		}
		defer fd.Close()
		b, err := io.ReadAll(io.LimitReader(fd, maxEditorSizeBytes+1))
		if err != nil {
			return err
		}
		if len(b) > maxEditorSizeBytes {
			return fmt.Errorf("file too large to edit (more than %d bytes)", maxEditorSizeBytes)
		}
		data = b
		return nil
	})
	return data, err
}

// CreateFile creates an empty file named name inside parent. It fails if the
// file already exists (O_EXCL) or if the directory is not writable/permission
// denied. Returns the virtual path of the created file.
func (f *FS) CreateFile(parent, name string) (string, error) {
	var virtPath string
	err := f.RunAs(func() error {
		absParent, validFile, err := f.resolveChildInDir(parent, name)
		if err != nil {
			return err
		}
		dst, err := os.OpenFile(validFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				return fmt.Errorf("file %q already exists in %s", name, f.toJailPath(absParent))
			}
			return err
		}
		if err := dst.Close(); err != nil {
			return err
		}
		if err := f.chown(validFile); err != nil {
			return err
		}
		virtPath = f.toJailPath(validFile)
		return nil
	})
	return virtPath, err
}

// WriteFile writes content to a file, creating it if necessary. The write goes
// through the shared atomic writer (unique temp file + rename), so a crash
// mid-write never leaves a truncated file behind, concurrent saves cannot race
// on a fixed temp name, and an existing file keeps its permission bits instead
// of being silently reset to 0644.
func (f *FS) WriteFile(path string, content []byte) error {
	abs, err := f.resolve(path)
	if err != nil {
		return err
	}
	return f.RunAs(func() error {
		mode := os.FileMode(0o644)
		if info, err := os.Stat(abs); err == nil {
			mode = info.Mode().Perm()
		}
		if err := atomicfile.Write(abs, content, mode); err != nil {
			return err
		}
		return f.chown(abs)
	})
}

// FileInfo returns metadata for a single path.
func (f *FS) FileInfo(path string) (*FileEntry, error) {
	var entry *FileEntry
	abs, err := f.resolve(path)
	if err != nil {
		return nil, err
	}
	err = f.RunAs(func() error {
		info, err := os.Stat(abs)
		if err != nil {
			return err
		}
		e := f.newFileEntry(info, abs)
		entry = &e
		return nil
	})
	return entry, err
}

// ResolvePath resolves a virtual path to its absolute path within the base
// jail without opening the underlying file.
func (f *FS) ResolvePath(path string) (string, error) {
	return f.resolve(path)
}

// VirtualPath converts an absolute path within the jail to its slash-separated
// virtual form (e.g. "/photos/vacation.mp4"), or "/" for the base directory.
func (f *FS) VirtualPath(absPath string) string {
	return f.toJailPath(absPath)
}

// OpenForDownload returns an open file handle for the file at path.
// Access is checked at open time (inside runAs) using the login user's
// credentials; the returned *os.File is safe to read after runAs returns
// because Unix file descriptors retain their access rights for the lifetime
// of the handle regardless of later credential changes on the thread.
func (f *FS) OpenForDownload(path string) (*os.File, error) {
	var file *os.File
	abs, err := f.resolve(path)
	if err != nil {
		return nil, err
	}
	err = f.RunAs(func() error {
		file, err = os.Open(abs)
		return err
	})
	return file, err
}

// resolveUploadDest resolves dirPath, joins filename (base name only) and
// validates the result stays inside the jail. Returns the absolute destination.
func (f *FS) resolveUploadDest(dirPath, filename string) (string, error) {
	absDir, err := f.resolve(dirPath)
	if err != nil {
		return "", err
	}
	destPath := filepath.Join(absDir, filepath.Base(filename))
	return f.validateAbs(destPath)
}

// writeUpload copies src into w and fixes up ownership. w is usually dst itself
// but may be an offset writer for chunked uploads. When the destination was
// created by this call (created true) any failure removes it so no partial file
// is left behind.
func (f *FS) writeUpload(dst *os.File, w io.Writer, src io.Reader, created bool) error {
	_, copyErr := io.Copy(w, src)
	// Surface both the copy and the close failure (errors.Join keeps them
	// retrievable via errors.Is) instead of silently dropping one.
	if err := errors.Join(copyErr, dst.Close()); err != nil {
		return f.rollbackPartialUpload(dst, created, err)
	}
	return f.chown(dst.Name())
}

// rollbackPartialUpload removes a partial upload destination that a failed
// write created. Surface a failed cleanup so a half-written upload that could
// not be removed does not linger silently in the jail.
func (f *FS) rollbackPartialUpload(dst *os.File, created bool, cause error) error {
	if !created {
		return cause
	}
	if rmErr := os.Remove(dst.Name()); rmErr != nil {
		return errors.Join(cause, fmt.Errorf("cleanup partial upload: %w", rmErr))
	}
	return cause
}

// SaveUpload writes the uploaded content to path/filename.
func (f *FS) SaveUpload(dirPath, filename string, src io.Reader) error {
	validDest, err := f.resolveUploadDest(dirPath, filename)
	if err != nil {
		return err
	}
	return f.RunAs(func() error {
		dst, err := os.Create(validDest)
		if err != nil {
			return err
		}
		return f.writeUpload(dst, dst, src, true)
	})
}

type offsetWriter struct {
	w   *os.File
	off int64
}

func (w *offsetWriter) Write(p []byte) (int, error) {
	n, err := w.w.WriteAt(p, w.off)
	w.off += int64(n)
	return n, err
}

// SaveUploadChunk writes src to path/filename at the given byte offset.
// Each HTTP request uploads one non-overlapping chunk of a file; the first
// chunk (offset 0) creates/truncates the destination and pre-allocates it to
// total bytes so chunks arriving out of order can still be written.
func (f *FS) SaveUploadChunk(dirPath, filename string, src io.Reader, offset, total int64) error {
	// The fs layer is the security boundary, so re-assert the chunk bounds the
	// HTTP handler enforces: a negative offset would WriteAt before the start
	// and an offset past total would grow the file beyond its declared size.
	if offset < 0 || total <= 0 || offset > total {
		return fmt.Errorf("invalid chunk bounds: offset=%d total=%d", offset, total)
	}
	validDest, err := f.resolveUploadDest(dirPath, filename)
	if err != nil {
		return err
	}
	return f.RunAs(func() error {
		// Distinguish "no existing file yet" (created by this call, must be
		// removed on failure) from a genuine stat failure, which should abort
		// instead of proceeding into a doomed write.
		exists := true
		if _, statErr := os.Stat(validDest); statErr != nil {
			if os.IsNotExist(statErr) {
				exists = false
			} else {
				return statErr
			}
		}
		dst, err := os.OpenFile(validDest, os.O_WRONLY|os.O_CREATE, 0o644)
		if err != nil {
			return err
		}
		// The offset-0 chunk is authoritative: always (re)size to the declared
		// total, even if a stale or previously-matching file already exists.
		if offset == 0 && total > 0 {
			if err := dst.Truncate(total); err != nil {
				// A partially pre-allocated destination is worse than no file:
				// remove it so the failed upload is retried cleanly.
				return f.rollbackPartialUpload(dst, !exists, errors.Join(dst.Close(), err))
			}
		}
		return f.writeUpload(dst, &offsetWriter{w: dst, off: offset}, src, !exists)
	})
}

// walkTree accumulates the total size in bytes and the number of files for the
// tree rooted at root. Unreadable entries are skipped so a single unreadable
// file does not abort the whole walk.
func walkTree(root string) (totalBytes int64, fileCount int64, err error) {
	err = filepath.WalkDir(root, func(_ string, d iofs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			return nil
		}
		// WalkDir only stats regular entries on demand, so directories are not
		// stat'ed at all; a stat failure on a file just leaves its size out.
		if info, infoErr := d.Info(); infoErr == nil {
			totalBytes += info.Size()
		}
		fileCount++
		return nil
	})
	return
}

// DirSize returns the total size in bytes and the number of files in a directory tree.
func (f *FS) DirSize(path string) (totalBytes int64, fileCount int64, err error) {
	abs, err := f.resolve(path)
	if err != nil {
		return 0, 0, err
	}
	err = f.RunAs(func() error {
		totalBytes, fileCount, err = walkTree(abs)
		return err
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
	// Free is Bavail (not Bfree): excludes blocks reserved for the root user,
	// which is what the process can actually write to under RunAs. Used is
	// derived from Bfree (like df) so the reserved blocks are not double
	// counted as used; Used+Free therefore stays <= Total.
	free := stat.Bavail * uint64(stat.Bsize)
	used := total - stat.Bfree*uint64(stat.Bsize)
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
	return f.CopyWithConflict(src, dst, ConflictOverwrite)
}

func (f *FS) CopyWithConflict(src, dst string, onConflict ConflictStrategy, onPct ...func(pct float64)) error {
	return f.resolveMoveCopy(src, dst, onConflict, true, func(absSrc, absDst string) error {
		return f.copyAll(absSrc, absDst, onPct...)
	})
}

// resolveDirDest resolves absDst into the concrete target when absDst is an
// existing directory: the destination becomes base(absSrc) joined inside it
// (shared by MoveWithConflict and CopyWithConflict).
func (f *FS) resolveDirDest(absSrc, absDst string) (string, error) {
	// Lstat, not Stat: a symlink that happens to point at a directory must not
	// be silently treated as a directory destination (the caller asked to place
	// the source *into* dst only when dst is a real directory). Either way the
	// chosen destination is re-validated against the jail.
	if info, err := os.Lstat(absDst); err == nil && info.IsDir() {
		candidate := filepath.Join(absDst, filepath.Base(absSrc))
		return f.validateAbs(candidate)
	}
	return f.validateAbs(absDst)
}

// CopyBatch copies multiple sources into the directory dst in a single
// RunAs call (unlike CopyWithConflict, dst is always treated as a
// destination directory).  If onConflict is ConflictError and any
// destination already exists, it returns the list of conflicting
// destination paths without modifying the filesystem.  For other
// strategies (overwrite/rename) all copies are applied unconditionally.
// When provided, onProgress reports the item currently being processed
// (and byte progress for the current item).
func (f *FS) CopyBatch(srcs []string, dst string, onConflict ConflictStrategy, onProgress ...func(p TransferProgress)) ([]string, error) {
	_, conflicts, err := f.applyBatch(srcs, dst, onConflict, onProgress, func(c batchItem, byter func(pct float64)) error {
		return f.copyAll(c.absSrc, c.absDst, byter)
	})
	return conflicts, err
}

// resolveDestination resolves the on-conflict strategy for a destination that
// already exists. It never mutates the filesystem. For ConflictOverwrite it
// reports overwrite=true so the caller decides whether to remove absDst now
// (single items) or defer to the batch apply phase. When claimed is non-nil,
// rename attempts skip destinations already claimed within the batch.
func (f *FS) resolveDestination(absSrc, absDst string, onConflict ConflictStrategy, claimed map[string]bool) (string, bool, error) {
	if absSrc == absDst {
		// Moving a path onto itself is a no-op (used by move operations only).
		return absDst, false, nil
	}
	if _, err := os.Stat(absDst); os.IsNotExist(err) {
		return absDst, false, nil
	} else if err != nil {
		return "", false, err
	}
	switch onConflict {
	case ConflictError:
		return "", false, fmt.Errorf("%w: %s", ErrDestinationExists, f.toJailPath(absDst))
	case ConflictOverwrite:
		return absDst, true, nil
	case ConflictRename:
		// claimed may be nil here; nextAvailablePathSkipping treats nil the
		// same as "nothing claimed" (a lookup on a nil map is legal), so the
		// renamed destination derives from the filesystem alone.
		next, err := f.nextAvailablePathSkipping(absDst, claimed)
		return next, false, err
	default:
		return "", false, fmt.Errorf("invalid conflict strategy %q", onConflict)
	}
}

func (f *FS) resolveConflictDestination(absSrc, absDst string, onConflict ConflictStrategy) (string, error) {
	dst, overwrite, err := f.resolveDestination(absSrc, absDst, onConflict, nil)
	if err != nil {
		return "", err
	}
	// Single-item operations remove the conflicting destination immediately.
	if overwrite {
		if err := os.RemoveAll(absDst); err != nil {
			return "", err
		}
	}
	return dst, nil
}

// nextAvailablePathSkipping finds the next free destination name for absPath,
// appending " (copy)", " (copy 2)", etc. when the path is occupied. Paths in
// claimed are also treated as occupied (used to avoid collisions between
// sources that share a basename within a single MoveBatch/CopyBatch call).
func (f *FS) nextAvailablePathSkipping(absPath string, claimed map[string]bool) (string, error) {
	dir := filepath.Dir(absPath)
	base := filepath.Base(absPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	const maxRenameAttempts = 1000
	for i := 1; i <= maxRenameAttempts; i++ {
		suffix := " (copy)"
		if i > 1 {
			suffix = fmt.Sprintf(" (copy %d)", i)
		}
		candidate := filepath.Join(dir, stem+suffix+ext)
		if claimed[candidate] {
			continue
		}
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not find an available destination name for %q after %d attempts", f.toJailPath(absPath), maxRenameAttempts)
}

// NextFreePath returns absPath itself when nothing occupies it yet, else the
// next available " (copy)"-style variant. Used for outputs that must never
// clobber an existing file (e.g. the video-extract scene cut). Call under
// RunAs, like every other resolve-based helper.
func (f *FS) NextFreePath(absPath string) (string, error) {
	if _, err := os.Lstat(absPath); os.IsNotExist(err) {
		return absPath, nil
	} else if err != nil {
		return "", err
	}
	return f.nextAvailablePathSkipping(absPath, nil)
}

func (f *FS) toJailPath(absPath string) string {
	rel, err := filepath.Rel(f.basePath, absPath)
	if err != nil || rel == "." {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

func (f *FS) copyAll(src, dst string, onPct ...func(pct float64)) error {
	// Guard against copying a directory into one of its own subdirectories,
	// which would otherwise recurse forever through the tree being written.
	if isSubPath(src, dst) && src != dst {
		return fmt.Errorf("cannot copy %q into its own subdirectory", f.toJailPath(src))
	}
	total, err := treeSize(src)
	if err != nil {
		return err
	}
	var copied int64
	if len(onPct) > 0 && onPct[0] != nil {
		var lastPct float64
		cb := func() {
			if total <= 0 {
				return
			}
			pct := float64(copied) / float64(total)
			// Throttle byte reports to ~1% steps so large files do not flood
			// the progress stream; always report completion.
			if pct-lastPct >= 0.01 || copied >= total {
				lastPct = pct
				onPct[0](pct)
			}
		}
		defer cb()
		return f.copyTree(src, dst, &copied, cb)
	}
	return f.copyTree(src, dst, &copied, nil)
}

func (f *FS) copyTree(src, dst string, copied *int64, onPct func()) error {
	// Lstat (never Stat) so symlinks are replicated as links instead of being
	// followed: a link inside the jailed tree must not pull content from
	// outside the jail into the copy destination.
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if isSymlink(info.Mode()) {
		if err := f.copySymlink(src, dst); err != nil {
			return err
		}
		// walkTree counts link sizes in the progress total, so count them here
		// too or symlink-heavy copies never reach 100%.
		*copied += info.Size()
		if onPct != nil {
			onPct()
		}
		return nil
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
			if err := f.copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name()), copied, onPct); err != nil {
				return err
			}
		}
		return f.chown(dst)
	}
	return f.copyFile(src, dst, info.Mode(), copied, onPct)
}

// copySymlink recreates a symlink in the destination. The target is stored
// verbatim; it is never dereferenced here, so copies stay inside the jail no
// matter where the link points.
func (f *FS) copySymlink(src, dst string) error {
	target, err := os.Readlink(src)
	if err != nil {
		return err
	}
	// A leftover destination from a partially failed copy would otherwise
	// make the create fail with EEXIST.
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(target, dst); err != nil {
		return err
	}
	return f.chown(dst)
}

func (f *FS) copyFile(src, dst string, mode os.FileMode, copied *int64, onPct func()) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	// OpenFile's mode is masked by the process umask, so set it explicitly to
	// preserve the source's permissions bit for bit (mode.Perm drops the
	// setuid/setgid/sticky bits, which a copy must not smuggle across).
	if err := out.Chmod(mode.Perm()); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	_, copyErr := io.Copy(out, &countingReader{r: in, copied: copied, onPct: onPct})
	// Surface both the copy and the close failure; remove the partial
	// destination so failed copies never leave a truncated file where the
	// full one is expected.
	if err := errors.Join(copyErr, out.Close()); err != nil {
		os.Remove(dst)
		return err
	}
	return f.chown(dst)
}

// countingReader wraps a reader and reports cumulative byte progress through
// onPct (which observes the shared *copied counter) as data is consumed.
type countingReader struct {
	r      io.Reader
	copied *int64
	onPct  func()
}

func (cr *countingReader) Read(p []byte) (int, error) {
	n, err := cr.r.Read(p)
	if n > 0 {
		*cr.copied += int64(n)
		if cr.onPct != nil {
			cr.onPct()
		}
	}
	return n, err
}

// treeSize returns the total size in bytes of the file tree rooted at root.
func treeSize(root string) (int64, error) {
	total, _, err := walkTree(root)
	return total, err
}

// ZipPaths streams a zip archive containing all specified paths to dst.
// Each path may be a file or a directory (archived recursively).
// The zip entries are named relative to the parent directory of each path.
// All file opens, reads, and zip writing happen inside runAs so that every
// I/O operation is performed with the login user's credentials.
func (f *FS) ZipPaths(dst io.Writer, paths []string) error {
	return f.RunAs(func() (err error) {
		zw := zip.NewWriter(dst)
		// Close exactly once, on every path. The archive is finalized even
		// when an early return leaves entries half-written, and on the success
		// path a Close failure (e.g. a broken client connection) is surfaced.
		defer func() {
			if cerr := zw.Close(); err == nil && cerr != nil {
				err = cerr
			}
		}()
		for _, path := range paths {
			abs, rerr := f.resolve(path)
			if rerr != nil {
				return rerr
			}
			if rerr := zipAdd(zw, abs, filepath.Dir(abs)); rerr != nil {
				return rerr
			}
		}
		return nil
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
		// filepath.Walk reports symlinks themselves (it uses Lstat), so links
		// are archived as Unix symlink entries instead of dereferencing them.
		// Following a link here could otherwise stream data living outside the
		// jail into the archive.
		if isSymlink(info.Mode()) {
			target, err := os.Readlink(filePath)
			if err != nil {
				return err
			}
			hdr := &zip.FileHeader{
				Name:     zipPath,
				Method:   zip.Deflate,
				Modified: info.ModTime(),
			}
			hdr.SetMode(info.Mode())
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			_, err = w.Write([]byte(target))
			return err
		}
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
		// Close explicitly: defer here would run only when the whole Walk
		// finishes, keeping every opened file descriptor open for large trees.
		_, copyErr := io.Copy(w, file)
		return errors.Join(copyErr, file.Close())
	})
}

// archivePasswordArgs prefixes the tool-specific password switch onto base
// args. Listing and extraction must assemble commands identically, so a single
// helper keeps the two paths from drifting apart.
func archivePasswordArgs(toolName, password string, base []string) []string {
	if password == "" {
		return base
	}
	var pw []string
	switch toolName {
	case "unzip":
		pw = []string{"-P", password}
	case "unrar", "7z":
		pw = []string{"-p" + password}
	default:
		return base
	}
	return append(pw, base...)
}

// validateArchiveMembers lists an archive's members with the same tool that
// will extract it and refuses the extraction when any member could escape the
// destination directory (zip-slip: "..", absolute or drive-letter paths, or
// backslash separators). Listing before writing means a crafted archive can
// never plant files outside the destination, even for members that are only
// reached late in the extraction.
func validateArchiveMembers(ctx context.Context, toolName, absArchive, password string) error {
	var cmd *exec.Cmd
	switch toolName {
	case "unzip":
		cmd = exec.CommandContext(ctx, "unzip", archivePasswordArgs(toolName, password, []string{"-Z1", absArchive})...)
	case "unrar":
		cmd = exec.CommandContext(ctx, "unrar", archivePasswordArgs(toolName, password, []string{"lb", absArchive})...)
	case "7z":
		cmd = exec.CommandContext(ctx, "7z", archivePasswordArgs(toolName, password, []string{"l", "-slt", absArchive})...)
	case "tar":
		cmd = exec.CommandContext(ctx, "tar", "-tf", absArchive)
	default:
		return nil
	}
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("could not list archive members: %w", err)
	}

	// -slt prints multi-line blocks per file; only "Path = ..." lines
	// carry member names. List them, then validate all members with the
	// shared loop below.
	if toolName == "7z" {
		var names []string
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "Path = ") {
				names = append(names, strings.TrimSpace(strings.TrimPrefix(line, "Path = ")))
			}
		}
		return rejectEscapingMembers(names)
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return rejectEscapingMembers(names)
}

// rejectEscapingMembers returns the first bad-archive-member error for names,
// or nil when every name is safe to extract into the jail destination.
func rejectEscapingMembers(names []string) error {
	for _, name := range names {
		if !safeArchiveMember(name) {
			return fmt.Errorf("archive member %q is not allowed: path escapes the destination", name)
		}
	}
	return nil
}

// safeArchiveMember reports whether an archive member can be extracted into the
// jail destination without escaping it (no "..", absolute path, drive letter or
// backslash separator). A trailing slash for directory entries is fine.
func safeArchiveMember(member string) bool {
	name := strings.TrimSpace(member)
	if name == "" {
		return true
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") ||
		(len(name) > 1 && name[1] == ':') {
		return false
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if relEscapes(clean) {
		return false
	}
	if strings.ContainsRune(name, '\\') {
		return false
	}
	return true
}

// stripSuffixFold removes suffix from s when it is present case-insensitively,
// preserving the original casing of the remainder (e.g. "Foo.TAR.GZ" stripped
// of ".tar.gz" yields "Foo"). Archive filenames arrive with unpredictable
// casing, so extension matching must not be case-sensitive.
func stripSuffixFold(s, suffix string) string {
	if len(s) < len(suffix) {
		return s
	}
	if strings.EqualFold(s[len(s)-len(suffix):], suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// archiveMultiSuffixes are the archive dual extensions collapsed into one
// suffix when deriving an extraction destination name, ordered so the longest
// suffix ("tar.gz") wins over its component (".gz").
var archiveMultiSuffixes = []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tgz", ".tbz2"}

// TarSuffixes are the archive extensions handled by the tar tool (usable by
// callers to pick an extraction tool); see DetectToolForArchive.
var TarSuffixes = []string{".tar", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tar.xz"}

// stripArchiveExt removes the archive extension family from baseName (whose
// lowercased form is lower), collapsing dual extensions so the extraction
// destination of "name.tar.gz" is "name" and not "name.tar". Matching is
// case-insensitive via stripSuffixFold.
func stripArchiveExt(baseName, lower string) string {
	for _, suffix := range archiveMultiSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return stripSuffixFold(baseName, suffix)
		}
	}
	return strings.TrimSuffix(baseName, filepath.Ext(baseName))
}

// archiveDestName returns the directory name an archive should extract into.
// A crafted name like "..tar.gz" strips to "..", which joined with the
// archive's parent directory would escape the jail (or resolve to the jail
// root), so such derived names are rejected before any write happens.
func archiveDestName(absArchive string) (string, error) {
	base := filepath.Base(absArchive)
	baseName := stripArchiveExt(base, strings.ToLower(base))
	// validNamePart rejects empty, "."/".." and either path separator (a
	// backslash is an ordinary filename byte on Linux but a separator on
	// Windows, so it is rejected uniformly).
	if !validNamePart(baseName) {
		return "", fmt.Errorf("archive name %q has no usable extraction directory", base)
	}
	return baseName, nil
}

// ExtractArchive extracts the given archive into a directory next to it.
// The progress writer receives each line of output from the extraction tool.
// Returns the virtual path of the destination directory. When ctx is cancelled
// (e.g. the client disconnects mid-extraction) the tool process is killed so
// no orphaned extraction keeps running after the request is gone.
func (f *FS) ExtractArchive(ctx context.Context, archivePath, password string, progress io.Writer, toolName string) (string, error) {
	var destVirtual string
	err := f.RunAs(func() error {
		absArchive, err := f.resolve(archivePath)
		if err != nil {
			return err
		}

		info, err := os.Stat(absArchive)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("path is a directory, not an archive")
		}

		baseName, err := archiveDestName(absArchive)
		if err != nil {
			return err
		}
		absDest, err := f.validateAbs(filepath.Join(filepath.Dir(absArchive), baseName))
		if err != nil {
			return err
		}
		destVirtual = f.toJailPath(absDest)

		var cmd *exec.Cmd
		switch toolName {
		case "unzip":
			cmd = exec.CommandContext(ctx, "unzip",
				archivePasswordArgs(toolName, password, []string{"-o", absArchive, "-d", absDest})...)
		case "unrar":
			cmd = exec.CommandContext(ctx, "unrar",
				archivePasswordArgs(toolName, password, []string{"x", "-o+", absArchive, absDest + string(filepath.Separator)})...)
		case "7z":
			cmd = exec.CommandContext(ctx, "7z",
				archivePasswordArgs(toolName, password, []string{"x", absArchive, "-o" + absDest, "-y"})...)
		case "tar":
			cmd = exec.CommandContext(ctx, "tar", "-xf", absArchive, "-C", absDest)
		default:
			return fmt.Errorf("unsupported archive format or tool: %s", toolName)
		}

		// Reject archives whose members could escape the destination
		// (zip-slip) before any of their content is written to disk.
		if err := validateArchiveMembers(ctx, toolName, absArchive, password); err != nil {
			return err
		}

		if err := os.MkdirAll(absDest, 0755); err != nil {
			return fmt.Errorf("failed to create destination directory: %w", err)
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("failed to create stderr pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		if err := cmd.Start(); err != nil {
			return fmt.Errorf("failed to start extraction: %w", err)
		}

		var wg sync.WaitGroup
		wg.Add(2)

		scanLines := func(r io.Reader) {
			defer wg.Done()
			scanner := bufio.NewScanner(r)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for scanner.Scan() {
				line := scanner.Text()
				if progress != nil {
					io.WriteString(progress, line+"\n")
				}
			}
			if scanner.Err() != nil {
				// A line exceeded the scanner buffer. Drain the rest of the
				// stream so the extraction tool's pipe never fills up, which
				// would otherwise block cmd.Wait() indefinitely.
				if progress != nil {
					io.Copy(progress, r)
				} else {
					io.Copy(io.Discard, r)
				}
			}
		}

		go scanLines(stdout)
		go scanLines(stderr)
		wg.Wait()

		waitErr := cmd.Wait()
		// Extraction tools restore symlink members verbatim, and the member-name
		// check above cannot see a link's *target*. A crafted archive can
		// therefore plant "evil -> /etc" or "evil -> ../../../x" inside the
		// jail, which would then leak host files through every read/download
		// path. Quarantine such links (even if the run failed, in case a
		// partial extraction planted them) so nothing escaping the destination
		// survives on disk. absDest is jail-validated above; only walk it when
		// that still holds, so a bug here can never escape the jail.
		var stripErr error
		if _, err := f.validateAbs(absDest); err == nil {
			stripErr = stripEscapingLinks(absDest)
		}
		if waitErr != nil {
			return errors.Join(fmt.Errorf("extraction failed: %w", waitErr), stripErr)
		}
		return stripErr
	})
	return destVirtual, err
}

// stripEscapingLinks walks root removing every symlink whose target points
// outside root (an absolute path, or a relative ".." climb out of the tree).
// Internal links that stay inside root are harmless and are left intact.
func stripEscapingLinks(root string) error {
	return filepath.WalkDir(root, func(path string, d iofs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !isSymlink(d.Type()) {
			return nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		if filepath.IsAbs(target) || linkEscapes(root, path, target) {
			return os.Remove(path)
		}
		return nil
	})
}

// linkEscapes reports whether a relative symlink target, resolved from the
// directory containing the link, would climb out of root.
func linkEscapes(root, fromPath, target string) bool {
	joined := filepath.Join(filepath.Dir(fromPath), target)
	rel, err := filepath.Rel(root, joined)
	if err != nil {
		return true
	}
	return relEscapes(rel)
}

// mimeHint returns a short string categorising the file type.
func mimeHint(name string, isDir bool) string {
	if isDir {
		return "dir"
	}
	// Strip qBittorrent incomplete-download suffix so that, e.g.,
	// "video.mp4.!qB" is treated as "video.mp4".
	if strings.HasSuffix(strings.ToLower(name), ".!qb") {
		name = name[:len(name)-4]
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
	case ".zip", ".tar", ".gz", ".bz2", ".xz", ".rar", ".7z", ".iso":
		return "archive"
	case ".go", ".py", ".js", ".ts", ".html", ".css", ".json",
		".yaml", ".yml", ".toml", ".sh", ".bash", ".c", ".cpp",
		".h", ".java", ".rb", ".rs", ".php", ".xml", ".sql", ".vue":
		return "code"
	case ".md":
		return "markdown"
	case ".txt", ".log", ".csv", ".ini", ".env", ".conf", ".nfo", ".url":
		return "text"
	default:
		return "file"
	}
}

package handler

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	hlsSegmentLen      = 10             // seconds per HLS segment
	hlsSessionTTL      = 2 * time.Hour  // how long to keep cached sessions
	hlsPlaylistTimeout = 60 * time.Second // max wait for first playlist to appear
	hlsSegmentTimeout  = 120 * time.Second // max wait for a segment to be committed
	hlsCleanupInterval = 30 * time.Minute  // interval between session cleanup sweeps
)

// hlsTempDir is the base directory for HLS temp files.
// Using os.TempDir() instead of a hardcoded path works in all environments.
var hlsTempDir = filepath.Join(os.TempDir(), "filex-hls")

// hlsSession tracks a single HLS transcoding session.
type hlsSession struct {
	dir      string       // temp directory containing segments + playlist
	done     chan struct{} // closed when transcoding goroutine exits (success or failure)
	err      error        // nil = success; set before done is closed
	mu       sync.Mutex
	lastUsed time.Time
}

var (
	hlsMu        sync.Mutex
	hlsSessions  = map[string]*hlsSession{}
	hlsCleanOnce sync.Once
)

// validSegName matches safe segment file names produced by ffmpeg (e.g. "seg000.ts").
var validSegName = regexp.MustCompile(`^seg\d{3,}\.ts$`)

// validSessionID matches a 64-character lowercase hex SHA-256 string.
var validSessionID = regexp.MustCompile(`^[0-9a-f]{64}$`)

// hlsSessionKey produces a stable cache key from the real file path + mtime.
// SHA-256 is used to minimise collision probability in large deployments.
func hlsSessionKey(realPath string, mtime time.Time) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%d", realPath, mtime.UnixNano())
	return fmt.Sprintf("%x", h.Sum(nil))
}

// startHLSCleanup starts a background goroutine (once) that removes stale HLS sessions.
func startHLSCleanup() {
	hlsCleanOnce.Do(func() {
		go func() {
			for {
				time.Sleep(hlsCleanupInterval)
				hlsMu.Lock()
				for k, s := range hlsSessions {
					s.mu.Lock()
					lu := s.lastUsed
					s.mu.Unlock()
					if time.Since(lu) > hlsSessionTTL {
						if err := os.RemoveAll(s.dir); err != nil {
							log.Printf("hls: cleanup of %s failed: %v", s.dir, err)
						}
						delete(hlsSessions, k)
					}
				}
				hlsMu.Unlock()
			}
		}()
	})
}

// startTranscoding launches ffmpeg in a background goroutine.
// It tries stream-copy first (fast for H.264/H.265); if that fails it
// falls back to re-encoding with libx264 (for VP9, AV1, etc.).
// sess.done is closed when the goroutine exits; sess.err is set on failure.
func startTranscoding(sess *hlsSession, realPath string) {
	go func() {
		defer func() {
			if p := recover(); p != nil {
				sess.mu.Lock()
				sess.err = fmt.Errorf("transcoding panic for %q: %v", realPath, p)
				sess.mu.Unlock()
			}
			close(sess.done)
		}()

		dir := sess.dir
		playlist := filepath.Join(dir, "playlist.m3u8")
		segPattern := filepath.Join(dir, "seg%03d.ts")

		if err := os.MkdirAll(dir, 0700); err != nil {
			sess.mu.Lock()
			sess.err = fmt.Errorf("create HLS dir: %w", err)
			sess.mu.Unlock()
			return
		}

		// Attempt 1: stream-copy video (fast for H.264/H.265).
		copyArgs := []string{
			"-i", realPath,
			"-c:v", "copy", "-c:a", "aac",
			"-f", "hls",
			"-hls_time", fmt.Sprintf("%d", hlsSegmentLen),
			"-hls_list_size", "0",
			"-hls_segment_filename", segPattern,
			"-y", playlist,
		}
		if err := exec.Command("ffmpeg", copyArgs...).Run(); err == nil {
			return // copy succeeded
		}

		// Attempt 2: re-encode to H.264 for codecs incompatible with MPEG-TS.
		_ = os.RemoveAll(dir)
		if err := os.MkdirAll(dir, 0700); err != nil {
			sess.mu.Lock()
			sess.err = fmt.Errorf("create HLS dir (re-encode): %w", err)
			sess.mu.Unlock()
			return
		}
		encodeArgs := []string{
			"-i", realPath,
			"-c:v", "libx264", "-preset", "ultrafast", "-crf", "23",
			"-c:a", "aac",
			"-f", "hls",
			"-hls_time", fmt.Sprintf("%d", hlsSegmentLen),
			"-hls_list_size", "0",
			"-hls_segment_filename", segPattern,
			"-y", playlist,
		}
		if err := exec.Command("ffmpeg", encodeArgs...).Run(); err != nil {
			sess.mu.Lock()
			sess.err = fmt.Errorf("ffmpeg transcoding of %q: %w", realPath, err)
			sess.mu.Unlock()
		}
	}()
}

// waitForPlaylist polls until playlist.m3u8 contains at least one segment entry
// (#EXTINF: line) or the deadline is exceeded.
//
// Merely waiting for the file to exist is not sufficient: ffmpeg creates the
// playlist header (EXTM3U, EXT-X-VERSION, etc.) before any segment is written.
// If we return the header-only playlist to hls.js it sees a stream with no
// segments, reports duration 0 and the video player appears broken.
func waitForPlaylist(dir string, done <-chan struct{}, timeout time.Duration) error {
	playlistPath := filepath.Join(dir, "playlist.m3u8")
	deadline := time.Now().Add(timeout)
	for {
		if data, err := os.ReadFile(playlistPath); err == nil && bytes.Contains(data, []byte("#EXTINF:")) {
			return nil
		}
		select {
		case <-done:
			// Transcoding finished — do a final check.
			if data, err := os.ReadFile(playlistPath); err == nil && bytes.Contains(data, []byte("#EXTINF:")) {
				return nil
			}
			return fmt.Errorf("transcoding finished without producing a playable playlist")
		default:
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for HLS playlist")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// isSegmentCommitted reports whether segName has been fully written by ffmpeg.
// ffmpeg appends a segment's name to playlist.m3u8 only after closing the
// segment file, so its presence in the playlist guarantees the file is complete.
// Once ffmpeg has exited (done closed), all remaining segments are also safe.
func isSegmentCommitted(dir, segName string, done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
	}
	data, err := os.ReadFile(filepath.Join(dir, "playlist.m3u8"))
	if err != nil {
		return false
	}
	// Split by "\n" and strip "\r" per line, consistent with rewritePlaylist.
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimRight(line, "\r") == segName {
			return true
		}
	}
	return false
}

// waitForSegment polls until segName is committed or the deadline is exceeded.
func waitForSegment(dir, segName string, done <-chan struct{}, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if isSegmentCommitted(dir, segName, done) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for segment %s", segName)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// rewritePlaylist replaces bare segment filenames in an m3u8 with authenticated
// /api/hls/segment URIs so every segment download goes through the handler.
func rewritePlaylist(data []byte, sessionKey string) string {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if validSegName.MatchString(trimmed) {
			lines[i] = "/api/hls/segment?session=" + sessionKey + "&name=" + trimmed
		}
	}
	return strings.Join(lines, "\n")
}

// handleHLSPlaylist starts background transcoding and returns the m3u8 playlist
// as soon as the first segment is ready — without waiting for the full transcode.
// Repeated requests hit the session cache and return instantly.
func (h *Handler) handleHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}

	// Check ffmpeg availability before doing any work.
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		writeError(w, http.StatusNotImplemented, "ffmpeg not available")
		return
	}

	fs := h.fsForRequest(r)

	// Resolve the virtual path to a real OS path (needed by ffmpeg).
	realPath, err := fs.RealPath(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	info, err := os.Stat(realPath)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusBadRequest, "path must be a file, not a directory")
		return
	}

	key := hlsSessionKey(realPath, info.ModTime())
	startHLSCleanup()

	hlsMu.Lock()
	sess, exists := hlsSessions[key]
	if !exists {
		sess = &hlsSession{
			dir:      filepath.Join(hlsTempDir, key),
			done:     make(chan struct{}),
			lastUsed: time.Now(),
		}
		hlsSessions[key] = sess
		startTranscoding(sess, realPath)
	}
	sess.mu.Lock()
	sess.lastUsed = time.Now()
	sess.mu.Unlock()
	hlsMu.Unlock()

	// Wait only until the playlist file appears on disk (non-blocking transcoding).
	// For H.264 copy this typically takes < 1 s; for re-encode a few seconds.
	if err := waitForPlaylist(sess.dir, sess.done, hlsPlaylistTimeout); err != nil {
		sess.mu.Lock()
		tErr := sess.err
		sess.mu.Unlock()
		if tErr != nil {
			writeError(w, http.StatusInternalServerError, "HLS transcoding failed: "+tErr.Error())
		} else {
			writeError(w, http.StatusGatewayTimeout, err.Error())
		}
		return
	}

	data, err := os.ReadFile(filepath.Join(sess.dir, "playlist.m3u8"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read playlist: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, rewritePlaylist(data, key))
}

// handleHLSSegment waits until the requested segment has been fully written
// by ffmpeg, then streams it to the client.
func (h *Handler) handleHLSSegment(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "GET required")
		return
	}

	sessionID := r.URL.Query().Get("session")
	segName := r.URL.Query().Get("name")

	// Validate both inputs strictly to prevent path traversal.
	if !validSessionID.MatchString(sessionID) {
		writeError(w, http.StatusBadRequest, "invalid session id")
		return
	}
	if !validSegName.MatchString(segName) {
		writeError(w, http.StatusBadRequest, "invalid segment name")
		return
	}

	hlsMu.Lock()
	sess, ok := hlsSessions[sessionID]
	if ok {
		sess.mu.Lock()
		sess.lastUsed = time.Now()
		sess.mu.Unlock()
	}
	hlsMu.Unlock()

	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}

	// Wait until this specific segment has been committed to disk by ffmpeg.
	if err := waitForSegment(sess.dir, segName, sess.done, hlsSegmentTimeout); err != nil {
		sess.mu.Lock()
		tErr := sess.err
		sess.mu.Unlock()
		if tErr != nil {
			writeError(w, http.StatusInternalServerError, "transcoding failed")
		} else {
			writeError(w, http.StatusGatewayTimeout, "timed out waiting for segment")
		}
		return
	}

	segPath := filepath.Join(sess.dir, segName)
	f, err := os.Open(segPath)
	if err != nil {
		writeError(w, http.StatusNotFound, "segment not found")
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "segment stat failed")
		return
	}
	// Use ServeContent (not ServeFile) so our Content-Type is not overridden.
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeContent(w, r, segName, stat.ModTime(), f)
}


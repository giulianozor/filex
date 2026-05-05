package handler

import (
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
	hlsSegmentLen    = 10              // seconds per HLS segment
	hlsSessionTTL    = 2 * time.Hour  // how long to keep cached sessions
	hlsReadyTimeout  = 5 * time.Minute
	hlsCleanupInterval = 30 * time.Minute // interval between session cleanup sweeps
)

// hlsTempDir returns the base directory for HLS temp files.
// Using os.TempDir() instead of a hardcoded path works in all environments.
var hlsTempDir = filepath.Join(os.TempDir(), "filex-hls")

// hlsSession tracks a single HLS transcoding session.
type hlsSession struct {
	dir      string       // temp directory containing segments + playlist
	ready    chan struct{} // closed when transcoding finishes (ok or err)
	err      error
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

// transcodeHLS runs ffmpeg to convert realPath into HLS segments inside dir.
// The output playlist is dir/playlist.m3u8 and segments are dir/segNNN.ts.
// It first attempts to copy the video stream (fast, no re-encoding). If that
// fails — e.g. because the source uses a codec incompatible with MPEG-TS (such
// as VP9 or AV1) — it falls back to re-encoding with H.264/AAC which is
// universally supported by HLS players.
func transcodeHLS(realPath, dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create HLS dir: %w", err)
	}
	playlist := filepath.Join(dir, "playlist.m3u8")
	segPattern := filepath.Join(dir, "seg%03d.ts")

	// Attempt 1: copy video stream (fastest, preserves quality).
	copyArgs := []string{
		"-i", realPath,
		"-c:v", "copy",
		"-c:a", "aac",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentLen),
		"-hls_list_size", "0",
		"-hls_segment_filename", segPattern,
		"-y",
		playlist,
	}
	if err := exec.Command("ffmpeg", copyArgs...).Run(); err == nil {
		return nil
	}

	// Attempt 2: re-encode to H.264 for codecs incompatible with MPEG-TS
	// (e.g. VP9, AV1). ultrafast preset minimises CPU time at the cost of
	// slightly larger files.
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create HLS dir (re-encode): %w", err)
	}
	encodeArgs := []string{
		"-i", realPath,
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-crf", "23",
		"-c:a", "aac",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", hlsSegmentLen),
		"-hls_list_size", "0",
		"-hls_segment_filename", segPattern,
		"-y",
		playlist,
	}
	if err := exec.Command("ffmpeg", encodeArgs...).Run(); err != nil {
		return fmt.Errorf("ffmpeg transcoding of %q: %w", realPath, err)
	}
	return nil
}

// handleHLSPlaylist transcodes the requested video to HLS and returns the m3u8 playlist.
// Results are cached by file path + mtime so subsequent requests are instant.
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
			ready:    make(chan struct{}),
			lastUsed: time.Now(),
		}
		hlsSessions[key] = sess
		go func() {
			defer func() {
				if p := recover(); p != nil {
					sess.err = fmt.Errorf("transcoding panic for %q: %v", realPath, p)
				}
				close(sess.ready)
			}()
			sess.err = transcodeHLS(realPath, sess.dir)
		}()
	}
	sess.mu.Lock()
	sess.lastUsed = time.Now()
	sess.mu.Unlock()
	hlsMu.Unlock()

	// Wait for transcoding to finish (or time out).
	select {
	case <-sess.ready:
	case <-time.After(hlsReadyTimeout):
		writeError(w, http.StatusGatewayTimeout, "HLS transcoding timed out")
		return
	}

	if sess.err != nil {
		writeError(w, http.StatusInternalServerError, "HLS transcoding failed: "+sess.err.Error())
		return
	}

	// Read the generated playlist and rewrite segment URIs so they go through
	// our authenticated /api/hls/segment endpoint.
	playlistPath := filepath.Join(sess.dir, "playlist.m3u8")
	data, err := os.ReadFile(playlistPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read playlist: "+err.Error())
		return
	}

	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if validSegName.MatchString(strings.TrimSpace(line)) {
			lines[i] = "/api/hls/segment?session=" + key + "&name=" + strings.TrimSpace(line)
		}
	}
	playlist := strings.Join(lines, "\n")

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprint(w, playlist)
}

// handleHLSSegment serves a single HLS transport-stream segment file.
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

	// Wait for transcoding to be ready.
	select {
	case <-sess.ready:
	case <-time.After(hlsReadyTimeout):
		writeError(w, http.StatusGatewayTimeout, "HLS transcoding timed out")
		return
	}

	if sess.err != nil {
		writeError(w, http.StatusInternalServerError, "transcoding failed")
		return
	}

	segPath := filepath.Join(sess.dir, segName)
	w.Header().Set("Content-Type", "video/mp2t")
	http.ServeFile(w, r, segPath)
}


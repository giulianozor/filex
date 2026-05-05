package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHandleHLSPlaylistMissingPath(t *testing.T) {
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/hls/playlist", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleHLSPlaylistFFmpegUnavailable(t *testing.T) {
	// This test relies on ffmpeg not being present in the test environment.
	// If ffmpeg is installed, the response will be different (possibly 404 because
	// the path doesn't resolve to a real video file in the temp dir).
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/hls/playlist?path=/nonexistent.mp4", nil)
	// Either 501 (no ffmpeg) or 404 (ffmpeg present but file missing) are acceptable.
	if rr.Code != http.StatusNotImplemented && rr.Code != http.StatusNotFound {
		t.Fatalf("expected 501 or 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleHLSSegmentInvalidSession(t *testing.T) {
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/hls/segment?session=../../etc/passwd&name=seg000.ts", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleHLSSegmentInvalidName(t *testing.T) {
	h, _ := setupHandler(t)
	// Valid 64-char hex session ID but invalid segment name.
	rr := doRequest(t, h, http.MethodGet, "/api/hls/segment?session=aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899&name=../../etc/passwd", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleHLSSegmentSessionNotFound(t *testing.T) {
	h, _ := setupHandler(t)
	// Valid 64-char hex session ID format but session does not exist.
	rr := doRequest(t, h, http.MethodGet, "/api/hls/segment?session=aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899&name=seg000.ts", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestWaitForPlaylistRequiresEXTINF verifies that waitForPlaylist does NOT
// return early when the playlist file exists but contains only an HLS header
// (which is what ffmpeg writes before any segment is committed). This prevents
// the video player from receiving a header-only manifest and showing duration 0.
func TestWaitForPlaylistRequiresEXTINF(t *testing.T) {
	dir := t.TempDir()
	playlistPath := filepath.Join(dir, "playlist.m3u8")
	done := make(chan struct{})

	// Write an HLS header-only playlist (no #EXTINF lines) — this is exactly
	// what ffmpeg creates immediately after starting, before the first segment.
	headerOnly := "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n#EXT-X-MEDIA-SEQUENCE:0\n"
	if err := os.WriteFile(playlistPath, []byte(headerOnly), 0600); err != nil {
		t.Fatal(err)
	}

	// waitForPlaylist must NOT return yet — no #EXTINF present.
	// We run it in a goroutine and check that it hasn't returned after
	// notReturnedAfter. This must be > hlsPollInterval (200ms) so at least
	// one poll cycle completes.
	const notReturnedAfter = 350 * time.Millisecond
	result := make(chan error, 1)
	go func() {
		result <- waitForPlaylist(dir, done, 2*time.Second)
	}()

	select {
	case err := <-result:
		t.Fatalf("waitForPlaylist returned early (err=%v); should wait for #EXTINF", err)
	case <-time.After(notReturnedAfter):
		// Good: still waiting.
	}

	// Now write a valid playlist with a segment entry.
	validPlaylist := headerOnly + "#EXTINF:10.000000,\nseg000.ts\n#EXT-X-ENDLIST\n"
	if err := os.WriteFile(playlistPath, []byte(validPlaylist), 0600); err != nil {
		t.Fatal(err)
	}

	// waitForPlaylist should return nil now.
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("waitForPlaylist returned error after valid playlist written: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForPlaylist did not return after valid playlist was written")
	}
}

// TestWaitForPlaylistErrorsWhenDoneWithNoSegments verifies that waitForPlaylist
// returns an error when the done channel is closed but no #EXTINF was ever written.
func TestWaitForPlaylistErrorsWhenDoneWithNoSegments(t *testing.T) {
	dir := t.TempDir()
	playlistPath := filepath.Join(dir, "playlist.m3u8")
	done := make(chan struct{})

	// Write header-only playlist.
	if err := os.WriteFile(playlistPath, []byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n"), 0600); err != nil {
		t.Fatal(err)
	}

	// Signal done immediately (simulating ffmpeg failing without writing any segments).
	close(done)

	err := waitForPlaylist(dir, done, 5*time.Second)
	if err == nil {
		t.Fatal("expected error when done closes without any #EXTINF, got nil")
	}
}


package handler

import (
	"net/http"
	"testing"
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
	// Valid hex session ID but invalid segment name.
	rr := doRequest(t, h, http.MethodGet, "/api/hls/segment?session=aabbccddeeff00112233445566778899&name=../../etc/passwd", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleHLSSegmentSessionNotFound(t *testing.T) {
	h, _ := setupHandler(t)
	// Valid session ID format but session does not exist.
	rr := doRequest(t, h, http.MethodGet, "/api/hls/segment?session=aabbccddeeff00112233445566778899&name=seg000.ts", nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rr.Code, rr.Body.String())
	}
}

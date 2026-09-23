package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	authlib "github.com/giulianozor/filex/internal/auth"
	"github.com/giulianozor/filex/internal/config"
	fslib "github.com/giulianozor/filex/internal/fs"
	"github.com/giulianozor/filex/internal/prefs"
	"golang.org/x/crypto/bcrypt"
)

func setupHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:         "0.0.0.0",
		Port:         8080,
		BasePath:     dir,
		ShowDotfiles: false,
		Favourites: []config.Favourite{
			{Name: "Home", Path: "/"},
		},
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")
	return h, dir
}

func doRequest(t *testing.T, h http.Handler, method, path string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if body != nil && method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// changePasswordRequest posts to /api/change-password with the given cookie
// token (empty for none) and returns the recorder.
func changePasswordRequest(t *testing.T, h http.Handler, cookieToken, current, newPw string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"current_password": current, "new_password": newPw})
	req := httptest.NewRequest(http.MethodPost, "/api/change-password", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if cookieToken != "" {
		req.AddCookie(&http.Cookie{Name: "filex_session", Value: cookieToken})
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandleList(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("x"), 0o644)

	rr := doRequest(t, h, http.MethodGet, "/api/list?path=/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var entries []map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&entries); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, e := range entries {
		if e["name"] == "test.txt" {
			found = true
		}
	}
	if !found {
		t.Error("test.txt not in listing")
	}
}

func TestHandleMkdir(t *testing.T) {
	h, dir := setupHandler(t)
	body := `{"path":"/","name":"newdir"}`
	rr := doRequest(t, h, http.MethodPost, "/api/mkdir", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "newdir")); err != nil {
		t.Errorf("newdir not created: %v", err)
	}
}

func TestHandleCreateFile(t *testing.T) {
	h, dir := setupHandler(t)
	body := `{"path":"/","name":"new.txt"}`
	rr := doRequest(t, h, http.MethodPost, "/api/create-file", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["path"] != "/new.txt" {
		t.Errorf("path = %q, want /new.txt", resp["path"])
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Errorf("new.txt not created: %v", err)
	}
}

func TestHandleCreateFile_AlreadyExists(t *testing.T) {
	h, dir := setupHandler(t)
	if err := os.WriteFile(filepath.Join(dir, "exists.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `{"path":"/","name":"exists.txt"}`
	rr := doRequest(t, h, http.MethodPost, "/api/create-file", strings.NewReader(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "already exists") {
		t.Fatalf("expected 'already exists' in message, got %s", rr.Body.String())
	}
}

func TestHandleCreateFile_InvalidName(t *testing.T) {
	h, _ := setupHandler(t)
	for _, body := range []string{
		`{"path":"/","name":""}`,
		`{"path":"/","name":"../evil.txt"}`,
	} {
		rr := doRequest(t, h, http.MethodPost, "/api/create-file", strings.NewReader(body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("body %s: status = %d, want 400", body, rr.Code)
		}
	}
}

func TestHandleCreateFile_MissingParent(t *testing.T) {
	h, _ := setupHandler(t)
	body := `{"path":"/no/such/dir","name":"x.txt"}`
	rr := doRequest(t, h, http.MethodPost, "/api/create-file", strings.NewReader(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleDelete(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "del.txt"), []byte("x"), 0o644)

	body := `{"path":"/del.txt"}`
	rr := doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "del.txt")); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestHandleDelete_PlainDeleteJSON(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "fast.txt"), []byte("x"), 0o644)

	rr := doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/fast.txt","wipe":false}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("plain delete content-type = %q, want JSON not NDJSON", ct)
	}
}

func TestHandleDelete_UnknownWipeMethod(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644)

	rr := doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/keep.txt","wipe":true,"method":"bogus"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (unknown wipe method)", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Error("file must not be touched for an unknown wipe method")
	}
}

func TestHandleDelete_WipeStreamsProgress(t *testing.T) {
	h, dir := setupHandler(t)
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i)
	}
	os.WriteFile(filepath.Join(dir, "wipe.bin"), data, 0o644)

	rr := doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/wipe.bin","wipe":true,"method":"fast"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("wipe content-type = %q, want NDJSON stream", ct)
	}
	lines := strings.Split(strings.TrimSpace(rr.Body.String()), "\n")
	var sawProgress, sawResult bool
	for _, line := range lines {
		if line == "" {
			continue
		}
		var msg struct {
			Type string  `json:"type"`
			Pct  float64 `json:"filePct"`
		}
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("bad NDJSON line %q: %v", line, err)
		}
		switch msg.Type {
		case "progress":
			sawProgress = true
			// filePct is a 0-1 fraction shared by wipe/move/copy streams; the
			// frontend multiplies by 100. A value above 1 would mean the wipe
			// percentage was sent 100x inflated.
			if msg.Pct < 0 || msg.Pct > 1 {
				t.Errorf("filePct = %v, want within [0,1]", msg.Pct)
			}
		case "result":
			sawResult = true
		}
	}
	if !sawProgress || !sawResult {
		t.Errorf("expected progress+result events, saw progress=%v result=%v", sawProgress, sawResult)
	}
	if _, err := os.Stat(filepath.Join(dir, "wipe.bin")); !os.IsNotExist(err) {
		t.Error("wipe.bin should have been securely deleted")
	}
}

func TestHandleDelete_ProtectedPath(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:           "0.0.0.0",
		Port:           8080,
		BasePath:       dir,
		ShowDotfiles:   false,
		ProtectedPaths: []string{"/keep.txt", "/protected"},
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")

	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write keep.txt: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "protected"), 0o755); err != nil {
		t.Fatalf("mkdir protected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "protected", "child.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write child.txt: %v", err)
	}

	for body, blockedPath := range map[string]string{
		`{"path":"/keep.txt"}`:            "/keep.txt",
		`{"path":"/protected/child.txt"}`: "/protected/child.txt",
	} {
		rr := doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(body))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
		}
		if !strings.Contains(rr.Body.String(), blockedPath) {
			t.Fatalf("expected response body %q to mention blocked path %q", rr.Body.String(), blockedPath)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.txt")); err != nil {
		t.Fatalf("keep.txt should still exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "protected", "child.txt")); err != nil {
		t.Fatalf("protected/child.txt should still exist: %v", err)
	}
}

func TestHandleRename(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "old.txt"), []byte("x"), 0o644)

	body := `{"path":"/old.txt","newname":"new.txt"}`
	rr := doRequest(t, h, http.MethodPost, "/api/rename", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); err != nil {
		t.Errorf("renamed file not found: %v", err)
	}
}

func protectedList(t *testing.T, h http.Handler) []string {
	t.Helper()
	rr := doRequest(t, h, http.MethodGet, "/api/protected", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var list []string
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode protected: %v", err)
	}
	return list
}

// TestHandleProtectedRemove_Persists locks in that removing a protected path
// actually removes it: the handler previously fed removeByPath's "present"
// result straight into the early/no-persist flag, so an unprotect click was a
// silent no-op that left the path protected forever.
func TestHandleProtectedRemove_Persists(t *testing.T) {
	h, dir := setupHandler(t)
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	rr := doRequest(t, h, http.MethodPost, "/api/protected/add", strings.NewReader(`{"path":"/docs"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("add status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if got := protectedList(t, h); !slices.Equal(got, []string{"/docs"}) {
		t.Fatalf("protected after add = %v, want [/docs]", got)
	}

	rr = doRequest(t, h, http.MethodPost, "/api/protected/remove", strings.NewReader(`{"path":"/docs"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("remove status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if got := protectedList(t, h); len(got) != 0 {
		t.Fatalf("protected after remove = %v, want []", got)
	}

	// The path must be deletable again once unprotected.
	rr = doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/docs"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 (unprotected), body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleRename_ProtectionFollows(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Host:           "0.0.0.0",
		Port:           8080,
		BasePath:       dir,
		ShowDotfiles:   false,
		ProtectedPaths: []string{"/docs"},
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}

	rr := doRequest(t, h, http.MethodPost, "/api/rename", strings.NewReader(`{"path":"/docs","newname":"news"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", rr.Code, rr.Body.String())
	}
	got := protectedList(t, h)
	if !slices.Equal(got, []string{"/news"}) {
		t.Fatalf("protected = %v, want [/news]", got)
	}
	// The renamed folder keeps its protection.
	rr = doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/news"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("delete /news status = %d, want 400 (protected), body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleRename_RelocatesProtectedSubtree(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Host:           "0.0.0.0",
		Port:           8080,
		BasePath:       dir,
		ShowDotfiles:   false,
		ProtectedPaths: []string{"/docs/keep.txt"},
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rr := doRequest(t, h, http.MethodPost, "/api/rename", strings.NewReader(`{"path":"/docs","newname":"archive"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("rename status = %d, body = %s", rr.Code, rr.Body.String())
	}
	got := protectedList(t, h)
	if !slices.Equal(got, []string{"/archive/keep.txt"}) {
		t.Fatalf("protected = %v, want [/archive/keep.txt]", got)
	}
	// Deleting the renamed parent is still blocked (it contains the protected file).
	rr = doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/archive"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("delete /archive status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleMove_ProtectionFollows(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Host:           "0.0.0.0",
		Port:           8080,
		BasePath:       dir,
		ShowDotfiles:   false,
		ProtectedPaths: []string{"/src"},
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}

	rr := doRequest(t, h, http.MethodPost, "/api/move", strings.NewReader(`{"src":"/src","dst":"/dest"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("move status = %d, body = %s", rr.Code, rr.Body.String())
	}
	got := protectedList(t, h)
	if !slices.Equal(got, []string{"/dest/src"}) {
		t.Fatalf("protected = %v, want [/dest/src]", got)
	}
	rr = doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/dest/src"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("delete /dest/src status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleMoveBatch_ProtectionFollows(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Host:           "0.0.0.0",
		Port:           8080,
		BasePath:       dir,
		ShowDotfiles:   false,
		ProtectedPaths: []string{"/src/keep.txt"},
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rr := doRequest(t, h, http.MethodPost, "/api/move", strings.NewReader(`{"srcs":["/src/keep.txt"],"dst":"/dest"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("move status = %d, body = %s", rr.Code, rr.Body.String())
	}
	got := protectedList(t, h)
	if !slices.Equal(got, []string{"/dest/keep.txt"}) {
		t.Fatalf("protected = %v, want [/dest/keep.txt]", got)
	}
	rr = doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/dest/keep.txt"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("delete /dest/keep.txt status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleDelete_StaleProtectedEntryIgnored(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Host:           "0.0.0.0",
		Port:           8080,
		BasePath:       dir,
		ShowDotfiles:   false,
		ProtectedPaths: []string{"/docs/keep.txt"},
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")
	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The protected file is relocated outside the handler (e.g. an external
	// rename or an earlier move before rewrite support): the recorded protected
	// entry is now stale. The parent folder must not stay undeletable forever —
	// this was the "no lock icon but cannot delete" bug.
	if err := os.Rename(filepath.Join(dir, "docs", "keep.txt"), filepath.Join(dir, "docs", "keep2.txt")); err != nil {
		t.Fatal(err)
	}

	rr := doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/docs"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("delete /docs status = %d, want 200 (stale entry must not block), body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleMove(t *testing.T) {
	h, dir := setupHandler(t)
	os.Mkdir(filepath.Join(dir, "dest"), 0o755)
	os.WriteFile(filepath.Join(dir, "move.txt"), []byte("x"), 0o644)

	body := `{"src":"/move.txt","dst":"/dest"}`
	rr := doRequest(t, h, http.MethodPost, "/api/move", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "dest", "move.txt")); err != nil {
		t.Errorf("moved file not found: %v", err)
	}
}

func TestHandleMoveBatch(t *testing.T) {
	h, dir := setupHandler(t)
	os.Mkdir(filepath.Join(dir, "src"), 0o755)
	os.Mkdir(filepath.Join(dir, "dest"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "b.txt"), []byte("b"), 0o644)

	body := `{"srcs":["/src/a.txt","/src/b.txt"],"dst":"/dest"}`
	rr := doRequest(t, h, http.MethodPost, "/api/move", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "dest", "a.txt")); err != nil {
		t.Errorf("a.txt not moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dest", "b.txt")); err != nil {
		t.Errorf("b.txt not moved: %v", err)
	}
	var progress []struct {
		Type    string `json:"type"`
		Done    int    `json:"done"`
		Total   int    `json:"total"`
		Current string `json:"current"`
	}
	lines := strings.Split(strings.TrimSpace(rr.Body.String()), "\n")
	for _, ln := range lines {
		var ev struct {
			Type    string `json:"type"`
			Done    int    `json:"done"`
			Total   int    `json:"total"`
			Current string `json:"current"`
		}
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatalf("decode line %q: %v", ln, err)
		}
		if ev.Type == "progress" {
			progress = append(progress, ev)
		}
	}
	if len(progress) != 2 || progress[0].Done != 0 || progress[1].Done != 1 || progress[1].Current != "b.txt" {
		t.Errorf("progress = %+v, want 2 events (0-indexed b.txt last)", progress)
	}
}

func TestHandleMoveBatch_ConflictReturnsPaths(t *testing.T) {
	h, dir := setupHandler(t)
	os.Mkdir(filepath.Join(dir, "src"), 0o755)
	os.Mkdir(filepath.Join(dir, "dest"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "b.txt"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(dir, "dest", "a.txt"), []byte("old"), 0o644)

	body := `{"srcs":["/src/a.txt","/src/b.txt"],"dst":"/dest","on_conflict":"error"}`
	rr := doRequest(t, h, http.MethodPost, "/api/move", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Type      string   `json:"type"`
		Code      string   `json:"code"`
		Conflicts []string `json:"conflicts"`
	}
	lines := strings.Split(strings.TrimSpace(rr.Body.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("no ndjson lines, body = %s", rr.Body.String())
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Type != "error" || payload.Code != "destination_exists" {
		t.Errorf("last line = %q, want error/destination_exists", lines[len(lines)-1])
	}
	if len(payload.Conflicts) != 1 || payload.Conflicts[0] != "/dest/a.txt" {
		t.Errorf("conflicts = %v, want [/dest/a.txt]", payload.Conflicts)
	}
}

func TestHandleMoveConflictInvalidOnConflict(t *testing.T) {
	h, _ := setupHandler(t)
	body := `{"src":"/a.txt","dst":"/b.txt","on_conflict":"invalid"}`
	rr := doRequest(t, h, http.MethodPost, "/api/move", strings.NewReader(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleReadWrite(t *testing.T) {
	h, dir := setupHandler(t)

	writeBody := `{"path":"/rw.txt","content":"hello world"}`
	rr := doRequest(t, h, http.MethodPost, "/api/write", strings.NewReader(writeBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("write status = %d, body = %s", rr.Code, rr.Body.String())
	}

	rr = doRequest(t, h, http.MethodGet, "/api/read?path=/rw.txt", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("read status = %d", rr.Code)
	}
	var resp map[string]string
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["content"] != "hello world" {
		t.Errorf("content = %q, want %q", resp["content"], "hello world")
	}
	_ = dir
}

func TestHandleUpload(t *testing.T) {
	h, dir := setupHandler(t)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", "upload.txt")
	fw.Write([]byte("uploaded content"))
	w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/upload?path=/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "upload.txt")); err != nil {
		t.Errorf("upload.txt not found: %v", err)
	}
}

func uploadChunk(t *testing.T, h *Handler, dirPath, filename string, offset, total int64, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, _ := w.CreateFormFile("file", filename)
	fw.Write(data)
	w.Close()
	u := "/api/upload?path=" + dirPath + "&offset=" + strconv.FormatInt(offset, 10) + "&total=" + strconv.FormatInt(total, 10)
	req := httptest.NewRequest(http.MethodPost, u, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestHandleUploadChunked(t *testing.T) {
	h, dir := setupHandler(t)
	const total = 11
	// Out-of-order chunks; chunk 0 arrives last to prove offset assembly.
	order := []struct {
		offset int64
		data   []byte
	}{
		{4, []byte("efgh")},
		{8, []byte("ijk")},
		{0, []byte("abcd")},
	}
	for _, c := range order {
		rr := uploadChunk(t, h, "/", "chunked.bin", c.offset, total, c.data)
		if rr.Code != http.StatusOK {
			t.Fatalf("chunk at offset %d status = %d, body = %s", c.offset, rr.Code, rr.Body.String())
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, "chunked.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "abcdefghijk" {
		t.Errorf("assembled content = %q, want %q", got, "abcdefghijk")
	}
}

func TestHandleUploadChunked_InvalidOffset(t *testing.T) {
	h, _ := setupHandler(t)
	rr := uploadChunk(t, h, "/", "bad.bin", -1, 100, []byte("x"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHandleUploadChunked_OffsetExceedsTotal(t *testing.T) {
	h, dir := setupHandler(t)
	rr := uploadChunk(t, h, "/", "sparse.bin", 5, 4, []byte("x"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (offset must not exceed total)", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "sparse.bin")); err == nil {
		t.Error("no file should have been created for an invalid chunk")
	}
}

func TestHandleUploadChunked_ReuploadTruncates(t *testing.T) {
	h, dir := setupHandler(t)
	// First upload a 10-byte file.
	if rr := uploadChunk(t, h, "/", "file.bin", 0, 10, []byte("abcdefghij")); rr.Code != http.StatusOK {
		t.Fatalf("initial upload status = %d", rr.Code)
	}
	// Re-upload a shorter 4-byte file with offset 0; stale tail must be gone.
	if rr := uploadChunk(t, h, "/", "file.bin", 0, 4, []byte("wxyz")); rr.Code != http.StatusOK {
		t.Fatalf("re-upload status = %d, body = %s", rr.Code, rr.Body.String())
	}
	got, err := os.ReadFile(filepath.Join(dir, "file.bin"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "wxyz" {
		t.Errorf("re-uploaded content = %q, want %q (stale tail not truncated)", got, "wxyz")
	}
}

func TestHandleFavourites(t *testing.T) {
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/favourites", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var favs []map[string]string
	json.NewDecoder(rr.Body).Decode(&favs)
	if len(favs) == 0 {
		t.Error("expected at least one favourite")
	}
}

func TestHandleFavourites_AddsHomeWhenMissing(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:         "0.0.0.0",
		Port:         8080,
		BasePath:     dir,
		ShowDotfiles: false,
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")
	rr := doRequest(t, h, http.MethodGet, "/api/favourites", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var favs []config.Favourite
	if err := json.NewDecoder(rr.Body).Decode(&favs); err != nil {
		t.Fatalf("decode favourites: %v", err)
	}
	if len(favs) == 0 || favs[0].Path != "/" {
		t.Fatalf("home favourite missing: %#v", favs)
	}
}

func TestHandleInfo(t *testing.T) {
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/info?path=/", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["file"] == nil {
		t.Error("expected 'file' field in info response")
	}
}

func TestHandleCopy(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "orig.txt"), []byte("x"), 0o644)

	body := `{"src":"/orig.txt","dst":"/copy.txt"}`
	rr := doRequest(t, h, http.MethodPost, "/api/copy", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "copy.txt")); err != nil {
		t.Errorf("copy.txt not found: %v", err)
	}
	// original must still exist
	if _, err := os.Stat(filepath.Join(dir, "orig.txt")); err != nil {
		t.Error("orig.txt should still exist after copy")
	}
}

func TestHandleCopyConflictDefaultErrors(t *testing.T) {
	h, dir := setupHandler(t)
	if err := os.WriteFile(filepath.Join(dir, "orig.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copy.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := `{"src":"/orig.txt","dst":"/copy.txt"}`
	rr := doRequest(t, h, http.MethodPost, "/api/copy", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(rr.Body.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("no ndjson lines, body = %s", rr.Body.String())
	}
	last := lines[len(lines)-1]
	if !strings.Contains(last, "destination already exists") {
		t.Fatalf("expected conflict message, got %s", last)
	}
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal([]byte(last), &payload); err != nil {
		t.Fatalf("decode error payload: %v", err)
	}
	if payload.Code != "destination_exists" {
		t.Fatalf("code = %q, want destination_exists", payload.Code)
	}
}

func TestHandleCopyConflictRename(t *testing.T) {
	h, dir := setupHandler(t)
	if err := os.WriteFile(filepath.Join(dir, "orig.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "copy.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	body := `{"src":"/orig.txt","dst":"/copy.txt","on_conflict":"rename"}`
	rr := doRequest(t, h, http.MethodPost, "/api/copy", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "copy (copy).txt")); err != nil {
		t.Fatalf("copy (copy).txt not found: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "copy (copy).txt"))
	if err != nil {
		t.Fatalf("read copy (copy).txt: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("copy (copy).txt content = %q, want %q", string(data), "new")
	}
}

func TestHandleCopyBatch(t *testing.T) {
	h, dir := setupHandler(t)
	os.Mkdir(filepath.Join(dir, "src"), 0o755)
	os.Mkdir(filepath.Join(dir, "dest"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "b.txt"), []byte("b"), 0o644)

	body := `{"srcs":["/src/a.txt","/src/b.txt"],"dst":"/dest"}`
	rr := doRequest(t, h, http.MethodPost, "/api/copy", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "dest", "a.txt")); err != nil {
		t.Errorf("a.txt not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dest", "b.txt")); err != nil {
		t.Errorf("b.txt not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "src", "a.txt")); err != nil {
		t.Error("a.txt should still exist after copy")
	}
	progress := 0
	for _, ln := range strings.Split(strings.TrimSpace(rr.Body.String()), "\n") {
		var ev struct {
			Type    string   `json:"type"`
			FilePct *float64 `json:"filePct"`
		}
		if err := json.Unmarshal([]byte(ln), &ev); err != nil {
			t.Fatalf("decode line %q: %v", ln, err)
		}
		if ev.Type == "progress" && ev.FilePct == nil {
			progress++
		}
	}
	if progress != 2 {
		t.Errorf("file progress events = %d, want 2", progress)
	}
}

func TestHandleCopyBatch_ConflictReturnsPaths(t *testing.T) {
	h, dir := setupHandler(t)
	os.Mkdir(filepath.Join(dir, "src"), 0o755)
	os.Mkdir(filepath.Join(dir, "dest"), 0o755)
	os.WriteFile(filepath.Join(dir, "src", "a.txt"), []byte("a"), 0o644)
	os.WriteFile(filepath.Join(dir, "src", "b.txt"), []byte("b"), 0o644)
	os.WriteFile(filepath.Join(dir, "dest", "a.txt"), []byte("old"), 0o644)

	body := `{"srcs":["/src/a.txt","/src/b.txt"],"dst":"/dest","on_conflict":"error"}`
	rr := doRequest(t, h, http.MethodPost, "/api/copy", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var payload struct {
		Type      string   `json:"type"`
		Code      string   `json:"code"`
		Conflicts []string `json:"conflicts"`
	}
	lines := strings.Split(strings.TrimSpace(rr.Body.String()), "\n")
	if len(lines) == 0 {
		t.Fatalf("no ndjson lines, body = %s", rr.Body.String())
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Type != "error" || payload.Code != "destination_exists" {
		t.Errorf("last line = %q, want error/destination_exists", lines[len(lines)-1])
	}
	if len(payload.Conflicts) != 1 || payload.Conflicts[0] != "/dest/a.txt" {
		t.Errorf("conflicts = %v, want [/dest/a.txt]", payload.Conflicts)
	}
}

func TestHandleZipDownload_Single(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "dl.txt"), []byte("zip me"), 0o644)

	body := `{"paths":["/dl.txt"]}`
	rr := doRequest(t, h, http.MethodPost, "/api/zip-download", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if ct != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", ct)
	}
}

func TestHandleZipDownload_Multiple(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "y.txt"), []byte("y"), 0o644)

	body := `{"paths":["/x.txt","/y.txt"]}`
	rr := doRequest(t, h, http.MethodPost, "/api/zip-download", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleZipDownload_Dir(t *testing.T) {
	h, dir := setupHandler(t)
	os.Mkdir(filepath.Join(dir, "mydir"), 0o755)
	os.WriteFile(filepath.Join(dir, "mydir", "inside.txt"), []byte("hi"), 0o644)

	body := `{"paths":["/mydir"]}`
	rr := doRequest(t, h, http.MethodPost, "/api/zip-download", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	cd := rr.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "mydir.zip") {
		t.Errorf("Content-Disposition = %q, expected mydir.zip", cd)
	}
}

func TestFavouritesPersistence(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	// Write a minimal config file.
	cfgPath := filepath.Join(dir, "config.yaml")
	initialCfg := &config.Config{
		Host:         "0.0.0.0",
		Port:         8080,
		BasePath:     dir,
		ShowDotfiles: false,
		Favourites:   []config.Favourite{{Name: "Home", Path: "/"}},
	}
	if err := initialCfg.Save(cfgPath); err != nil {
		t.Fatalf("write config: %v", err)
	}
	// Create a subdirectory to add as a favourite.
	subdir := filepath.Join(dir, "docs")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	h := New(fsys, initialCfg, http.Dir(dir), nil, nil, cfgPath, nil, "test")

	// Add a favourite.
	body := `{"path":"/docs","name":"Docs"}`
	rr := doRequest(t, h, http.MethodPost, "/api/favourites/add", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("add status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// Verify the config file was updated.
	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	found := false
	for _, f := range saved.Favourites {
		if f.Path == "/docs" {
			found = true
		}
	}
	if !found {
		t.Error("added favourite not persisted to config file")
	}

	// Remove the favourite.
	rmBody := `{"path":"/docs"}`
	rr = doRequest(t, h, http.MethodPost, "/api/favourites/remove", strings.NewReader(rmBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("remove status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// Verify removal was persisted.
	saved, err = config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config after remove: %v", err)
	}
	for _, f := range saved.Favourites {
		if f.Path == "/docs" {
			t.Error("removed favourite still present in config file")
		}
	}
}

func TestFavouritesUpdatePersistenceAndAlphabeticalOrder(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	initialCfg := &config.Config{
		Host:         "0.0.0.0",
		Port:         8080,
		BasePath:     dir,
		ShowDotfiles: false,
		Favourites: []config.Favourite{
			{Name: "Home", Path: "/"},
			{Name: "Docs", Path: "/docs"},
			{Name: "Media", Path: "/media"},
		},
	}
	if err := initialCfg.Save(cfgPath); err != nil {
		t.Fatalf("write config: %v", err)
	}
	for _, d := range []string{"docs", "media"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	h := New(fsys, initialCfg, http.Dir(dir), nil, nil, cfgPath, nil, "test")

	body := `{"favourites":[{"name":"Media","path":"/media"},{"name":"Documents","path":"/docs"},{"name":"Home","path":"/"}]}`
	rr := doRequest(t, h, http.MethodPost, "/api/favourites/update", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", rr.Code, rr.Body.String())
	}

	rr = doRequest(t, h, http.MethodGet, "/api/favourites", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var favs []config.Favourite
	if err := json.NewDecoder(rr.Body).Decode(&favs); err != nil {
		t.Fatalf("decode favourites: %v", err)
	}
	if len(favs) != 3 {
		t.Fatalf("len favourites = %d, want 3", len(favs))
	}
	if favs[0].Path != "/" {
		t.Fatalf("home favourite should stay first, got %+v", favs)
	}
	if favs[1].Name != "Documents" || favs[2].Name != "Media" {
		t.Fatalf("favourites should be alphabetical after home: %+v", favs)
	}

	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if len(saved.Favourites) != 3 || saved.Favourites[1].Name != "Documents" || saved.Favourites[2].Name != "Media" {
		t.Fatalf("persisted favourites mismatch: %+v", saved.Favourites)
	}
}

func TestHandleConfig(t *testing.T) {
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/config", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var resp map[string]any
	json.NewDecoder(rr.Body).Decode(&resp)
	if resp["port"] == nil {
		t.Error("expected 'port' in config response")
	}
}

func TestHandleDelete_Bulk(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x"), 0o644)

	body := `{"paths":["/a.txt","/b.txt"]}`
	rr := doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(body))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	for _, f := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("%s should be deleted", f)
		}
	}
}

func TestHandleDelete_BulkProtectedIsAtomic(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:           "0.0.0.0",
		Port:           8080,
		BasePath:       dir,
		ShowDotfiles:   false,
		ProtectedPaths: []string{"/b.txt"},
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")

	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	body := `{"paths":["/a.txt","/b.txt"]}`
	rr := doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(body))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rr.Code, http.StatusBadRequest, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "/b.txt") {
		t.Fatalf("expected response body %q to mention blocked path %q", rr.Body.String(), "/b.txt")
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s should still exist: %v", name, err)
		}
	}
}

func TestHandleStream_ServesFileInline(t *testing.T) {
	h, dir := setupHandler(t)
	content := []byte("video data")
	os.WriteFile(filepath.Join(dir, "sample.mp4"), content, 0o644)

	rr := doRequest(t, h, http.MethodGet, "/api/stream?path=/sample.mp4", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	// Must NOT force a download attachment.
	cd := rr.Header().Get("Content-Disposition")
	if strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, must not contain 'attachment' for inline streaming", cd)
	}
	// Body must contain the full file content.
	if got := rr.Body.Bytes(); string(got) != string(content) {
		t.Errorf("body = %q, want %q", got, content)
	}
}

func TestHandleStream_MissingPath(t *testing.T) {
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodGet, "/api/stream", nil)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleStream_RangeRequest(t *testing.T) {
	h, dir := setupHandler(t)
	content := []byte("abcdefghij") // 10 bytes
	os.WriteFile(filepath.Join(dir, "audio.mp3"), content, 0o644)

	req := httptest.NewRequest(http.MethodGet, "/api/stream?path=/audio.mp3", nil)
	req.Header.Set("Range", "bytes=2-5")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 for range request", rr.Code)
	}
	if got := rr.Body.String(); got != "cdef" {
		t.Errorf("range body = %q, want %q", got, "cdef")
	}
}

func TestContentDispositionAttachment_Sanitizes(t *testing.T) {
	// Simple names keep their (optionally quoted) form, never with raw CR/LF.
	got := contentDispositionAttachment("plain.txt")
	if !strings.Contains(got, "plain.txt") {
		t.Errorf("simple name not preserved: %q", got)
	}
	if strings.Contains(got, "\r\n") {
		t.Errorf("unexpected raw CR/LF in header: %q", got)
	}
	// A quote in the name must be escaped as \", not appear raw.
	if got := contentDispositionAttachment(`a"b;c.txt`); !strings.Contains(got, `a\"b;c.txt`) {
		t.Errorf("quote not escaped: %q", got)
	}
	// Control characters must never appear literally (header injection).
	for _, name := range []string{"evil\r\nX-Injected: 1", "bad\x00file", "trail\x0a"} {
		got := contentDispositionAttachment(name)
		if strings.ContainsAny(got, "\r\n\x00") {
			t.Errorf("control character leaked into header for %q: %q", name, got)
		}
	}
}

func TestReadThumbnailEntries_CorruptTimestamps(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "timestamps.json"), []byte(`[{"name":"1.jpg","time":"oops"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Must not panic on a non-float time field.
	entries, manifestExists := readThumbnailEntries(dir)
	if !manifestExists {
		t.Fatal("expected manifest to parse")
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if v, ok := entries[0]["time"].(float64); !ok || v != 0 {
		t.Errorf("expected time reset to 0, got %#v", entries[0]["time"])
	}
}

func TestHandleList_RejectsNonGET(t *testing.T) {
	h, dir := setupHandler(t)
	os.WriteFile(filepath.Join(dir, "test.txt"), []byte("x"), 0o644)

	rr := doRequest(t, h, http.MethodPost, "/api/list?path=/", bytes.NewBufferString(`{}`))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/list status = %d, want 405", rr.Code)
	}
	rr = doRequest(t, h, http.MethodPut, "/api/list?path=/", nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT /api/list status = %d, want 405", rr.Code)
	}
}

func TestHandleMoveTooLargeBody_413(t *testing.T) {
	h, _ := setupHandler(t)
	// /api/move is wrapped with withBodyLimit(maxJSONBodyBytes); an oversized
	// body must surface as 413, not 400. The payload must be valid JSON so the
	// decoder actually runs past the byte limit.
	big := strings.Repeat("a", maxJSONBodyBytes)
	rr := doRequest(t, h, http.MethodPost, "/api/move", bytes.NewBufferString(`{"path":"`+big+`"}`))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rr.Code)
	}
}

func TestHandleDelete_ProtectedAncestor(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:           "0.0.0.0",
		Port:           8080,
		BasePath:       dir,
		ShowDotfiles:   false,
		ProtectedPaths: []string{"/docs/keep.txt"},
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", nil, "test")

	if err := os.Mkdir(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docs", "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Deleting the directory that contains a protected path must be rejected
	// too, otherwise removing the parent silently deletes the protected file.
	rr := doRequest(t, h, http.MethodPost, "/api/delete", strings.NewReader(`{"path":"/docs"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "/docs") {
		t.Fatalf("expected body to mention /docs, got %s", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "keep.txt")); err != nil {
		t.Error("protected file was deleted anyway")
	}
}

func TestHandleUploadChunked_TotalTooLarge(t *testing.T) {
	h, dir := setupHandler(t)
	rr := uploadChunk(t, h, "/", "big.bin", 0, maxUploadRequestBody+1, []byte("x"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (total exceeds cap)", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "big.bin")); err == nil {
		t.Error("no file should be created for an oversized total")
	}
}

func TestHandleUploadChunked_ChunkExceedsTotal(t *testing.T) {
	h, dir := setupHandler(t)
	// total=4 but the chunk holds 10 bytes: offset+len must not exceed total.
	rr := uploadChunk(t, h, "/", "overflow.bin", 0, 4, []byte("abcdefghij"))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (chunk exceeds total)", rr.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "overflow.bin")); err == nil {
		t.Error("no file should be created for an oversized chunk")
	}
}

func TestReadThumbnailEntries_EmptyManifest(t *testing.T) {
	dir := t.TempDir()
	// A valid but empty manifest must be treated as an existing manifest
	// (negative cache): no thumbnails to show, but do not force a regeneration
	// followed by nothing, which previously returned hasManifest=false.
	if err := os.WriteFile(filepath.Join(dir, "timestamps.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, manifestExists := readThumbnailEntries(dir)
	if !manifestExists {
		t.Error("expected manifestExists=true for an empty manifest")
	}
	if len(entries) != 0 {
		t.Errorf("entries = %v, want none", entries)
	}
}

func TestCleanFavourite_TruncatesByRunes(t *testing.T) {
	// A 64-byte cap would split a 3-byte rune and persist invalid UTF-8 into
	// the config; truncation must happen on rune boundaries.
	name := strings.Repeat("é", 30) // 30 runes, 60 bytes
	p, got := cleanFavourite("  /sub  ", name)
	if p != "/sub" {
		t.Errorf("path = %q, want /sub", p)
	}
	if r := len([]rune(got)); r > maxFavouriteNameLen {
		t.Errorf("name runes = %d, want <= %d (got %q)", r, maxFavouriteNameLen, got)
	}
	if got != strings.Repeat("é", 30) {
		t.Errorf("untruncated name mangled: %q", got)
	}
	// A name longer than the cap must still terminate on a rune boundary.
	long := strings.Repeat("é", maxFavouriteNameLen+1)
	_, got = cleanFavourite("  /sub  ", long)
	if len([]rune(got)) != maxFavouriteNameLen {
		t.Errorf("capped name runes = %d, want %d", len([]rune(got)), maxFavouriteNameLen)
	}
	if !strings.Contains(got, "\uFFFD") {
		// A byte-slice would have introduced a replacement character.
		t.Logf("capped name = %q", got)
	}
}

func TestDecodeBody_RejectsTrailingData(t *testing.T) {
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodPost, "/api/mkdir", strings.NewReader(`{"name":"sub"} GARBAGE`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleProtectedAdd_RequiresExistingPath(t *testing.T) {
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodPost, "/api/protected/add", strings.NewReader(`{"path":"/does-not-exist"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleFavourites_RejectsWhitespacePath(t *testing.T) {
	h, _ := setupHandler(t)
	rr := doRequest(t, h, http.MethodPost, "/api/favourites/add", strings.NewReader(`{"path":"   "}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rr.Code, rr.Body.String())
	}
}

func TestHandleFavourites_TrimsAndCapsName(t *testing.T) {
	h, dir := setupHandler(t)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 100)
	rr := doRequest(t, h, http.MethodPost, "/api/favourites/add",
		strings.NewReader(`{"path":"  /sub  ","name":"  `+long+`  "}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("add status = %d, body = %s", rr.Code, rr.Body.String())
	}

	rr = doRequest(t, h, http.MethodGet, "/api/favourites", nil)
	var favs []config.Favourite
	if err := json.NewDecoder(rr.Body).Decode(&favs); err != nil {
		t.Fatalf("decode favourites: %v", err)
	}
	for _, f := range favs {
		if f.Path == "/sub" {
			if len(f.Name) != 64 {
				t.Errorf("name length = %d, want 64 (capped/trimmed)", len(f.Name))
			}
			if f.Name != strings.TrimSpace(f.Name) {
				t.Errorf("name %q has surrounding whitespace", f.Name)
			}
			return
		}
	}
	t.Error("favourite /sub not found after add")
}

// TestNoAuthTokenIgnored locks in the contract that a client-supplied auth
// token/cookie is ignored when the server runs without any configured users:
// protected API endpoints must succeed even when a bogus (or stale, carried
// over from a previous authenticated deployment) session cookie is present.
func TestNoAuthTokenIgnored(t *testing.T) {
	h, dir := setupHandler(t)
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/list?path=/", nil)
	req.AddCookie(&http.Cookie{Name: "filex_session", Value: "bogus-token-that-must-be-ignored"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/list with bogus token = %d, want 200 (auth token must be ignored when no users are configured)", rr.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(&http.Cookie{Name: "filex_session", Value: "bogus-token-that-must-be-ignored"})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/logout with bogus token = %d, want 200", rr.Code)
	}
}

// TestDeletePrefs_RoundTripInMemory exercises the no-auth secure-delete
// preference endpoints against an in-memory prefs (no prefsPath): defaults are
// returned, an update sticks, and an unknown wipe method is rejected.
func TestDeletePrefs_RoundTripInMemory(t *testing.T) {
	h, _ := setupHandler(t)

	rr := doRequest(t, h, http.MethodGet, "/api/delete-prefs", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/delete-prefs = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}
	var got prefs.Deletion
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.DefaultWipe || got.WipeMethod != "" {
		t.Fatalf("defaults = %+v, want empty", got)
	}

	rr = doRequest(t, h, http.MethodPost, "/api/delete-prefs",
		strings.NewReader(`{"default_wipe":true,"wipe_method":"dod"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/delete-prefs (dod) = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}

	rr = doRequest(t, h, http.MethodGet, "/api/delete-prefs", nil)
	var after prefs.Deletion
	if err := json.NewDecoder(rr.Body).Decode(&after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !after.DefaultWipe || after.WipeMethod != "dod" {
		t.Fatalf("after update = %+v, want {true dod}", after)
	}

	rr = doRequest(t, h, http.MethodPost, "/api/delete-prefs",
		strings.NewReader(`{"default_wipe":true,"wipe_method":"nope"}`))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown wipe method = %d, want 400", rr.Code)
	}
}

// TestDeletePrefs_PersistedToPrefsFile locks in that the prefs document written
// on /api/delete-prefs can be reloaded and mirrors the runtime state.
func TestDeletePrefs_PersistedToPrefsFile(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{Host: "0.0.0.0", Port: 8080, BasePath: dir}
	prefsPath := filepath.Join(t.TempDir(), "filex.yaml")
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", map[string]string{"": prefsPath}, "test")

	rr := doRequest(t, h, http.MethodPost, "/api/delete-prefs",
		strings.NewReader(`{"default_wipe":true,"wipe_method":"fast"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}

	reloaded, err := prefs.Load(prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Deletion.DefaultWipe || reloaded.Deletion.WipeMethod != "fast" {
		t.Fatalf("persisted deletion = %+v, want {true fast}", reloaded.Deletion)
	}
}

// TestLogin_ReloadsPrefsAfterStart locks in that a running server picks up a
// password set after startup (e.g. by `filex-passwd` writing ~/.config/filex.yaml)
// without needing a restart: the prefs file is re-read when it changes on disk.
func TestLogin_ReloadsPrefsAfterStart(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:  "0.0.0.0",
		Port:  8080,
		Users: []config.User{{Username: "alice", BasePath: dir}},
	}
	users := BuildUserFS(cfg)
	if len(users) != 1 {
		t.Fatalf("expected one user, got %d", len(users))
	}

	prefsPath := filepath.Join(t.TempDir(), "filex.yaml")
	// The prefs file does not exist yet: the server starts with an empty doc,
	// exactly like the deployed flow where filex-passwd runs after boot.
	h := New(fsys, cfg, http.Dir(dir), authlib.NewStore(), users, "", map[string]string{"alice": prefsPath}, "test")

	login := func(password string) int {
		body := strings.NewReader(`{"username":"alice","password":"s3cret"}`)
		return doRequest(t, h, http.MethodPost, "/api/login", body).Code
	}

	// Before any password is set, every login must fail with 401.
	if got := login("s3cret"); got != http.StatusUnauthorized {
		t.Fatalf("login with no password set = %d, want 401", got)
	}

	// Simulate `filex-passwd -user alice`: write the hash into the prefs file
	// while the server keeps running.
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := prefs.Load(prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	loaded.Users = append(loaded.Users, prefs.User{Username: "alice", PasswordHash: string(hash)})
	if err := loaded.Save(prefsPath); err != nil {
		t.Fatal(err)
	}

	// The next login must succeed without a server restart.
	if got := login("s3cret"); got != http.StatusOK {
		t.Fatalf("login after external prefs write = %d, want 200", got)
	}
}

// TestChangePassword locks in the self-service password update: the current
// password must verify before the stored hash is replaced, only the
// authenticated user can change their own password, and the new password takes
// effect for subsequent logins while the existing session stays valid.
func TestChangePassword(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:  "0.0.0.0",
		Port:  8080,
		Users: []config.User{{Username: "alice", BasePath: dir}},
	}
	users := BuildUserFS(cfg)
	hash, err := bcrypt.GenerateFromPassword([]byte("old-pass"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	prefsPath := filepath.Join(t.TempDir(), "filex.yaml")
	prefsDoc := &prefs.Preferences{Users: []prefs.User{{Username: "alice", PasswordHash: string(hash)}}}
	if err := prefsDoc.Save(prefsPath); err != nil {
		t.Fatal(err)
	}
	h := New(fsys, cfg, http.Dir(dir), authlib.NewStore(), users, "", map[string]string{"alice": prefsPath}, "test")

	// Log in to obtain a session cookie (the change-password request carries it).
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"alice","password":"old-pass"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginRR := httptest.NewRecorder()
	h.ServeHTTP(loginRR, loginReq)
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200", loginRR.Code)
	}
	cookies := loginRR.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set by login")
	}
	cookie := cookies[0]

	changePassword := func(current, newPw string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"current_password": current, "new_password": newPw})
		req := httptest.NewRequest(http.MethodPost, "/api/change-password", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(cookie)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}

	// Wrong current password: rejected, hash unchanged.
	if rr := changePassword("wrong", "new-pass"); rr.Code != http.StatusBadRequest {
		t.Fatalf("change with wrong current = %d, want 400", rr.Code)
	}
	if rr := changePassword("", "new-pass"); rr.Code != http.StatusBadRequest {
		t.Fatalf("change with empty current = %d, want 400", rr.Code)
	}
	// Empty/oversized new password: rejected.
	if rr := changePassword("old-pass", ""); rr.Code != http.StatusBadRequest {
		t.Fatalf("change with empty new = %d, want 400", rr.Code)
	}
	if rr := changePassword("old-pass", strings.Repeat("x", 73)); rr.Code != http.StatusBadRequest {
		t.Fatalf("change with 73-byte new = %d, want 400", rr.Code)
	}

	// Correct current password: succeeds.
	if rr := changePassword("old-pass", "new-pass"); rr.Code != http.StatusOK {
		t.Fatalf("change with correct current = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}

	// The current session must remain valid (no forced logout).
	meReq := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	meReq.AddCookie(cookie)
	meRR := httptest.NewRecorder()
	h.ServeHTTP(meRR, meReq)
	if meRR.Code != http.StatusOK {
		t.Fatalf("/api/me after change = %d, want 200", meRR.Code)
	}

	// The old password no longer logs in; the new one does.
	loginAs := func(password string) int {
		body := strings.NewReader(fmt.Sprintf(`{"username":"alice","password":%q}`, password))
		return doRequest(t, h, http.MethodPost, "/api/login", body).Code
	}
	if got := loginAs("old-pass"); got != http.StatusUnauthorized {
		t.Fatalf("login with old password = %d, want 401", got)
	}
	if got := loginAs("new-pass"); got != http.StatusOK {
		t.Fatalf("login with new password = %d, want 200", got)
	}
}

// TestChangePassword_RequiresAuth locks in that /api/change-password needs a
// valid session: without one (or in no-auth mode) it is rejected.
func TestChangePassword_RequiresAuth(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:  "0.0.0.0",
		Port:  8080,
		Users: []config.User{{Username: "alice", BasePath: dir}},
	}
	users := BuildUserFS(cfg)
	hash, _ := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.DefaultCost)
	prefsPath := filepath.Join(t.TempDir(), "filex.yaml")
	prefsDoc := &prefs.Preferences{Users: []prefs.User{{Username: "alice", PasswordHash: string(hash)}}}
	if err := prefsDoc.Save(prefsPath); err != nil {
		t.Fatal(err)
	}
	h := New(fsys, cfg, http.Dir(dir), authlib.NewStore(), users, "", map[string]string{"alice": prefsPath}, "test")

	rr := changePasswordRequest(t, h, "", "old", "new")
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("change without session = %d, want 401", rr.Code)
	}
}

// TestLogin_UsesPrefsPasswordHash locks in the auth flow against the preferences
// file: the bcrypt hash comes from prefsVal (never config.yaml), a good set of
// credentials succeeds, and a user without a hash is rejected with the same
// 401 as a wrong password.
func TestLogin_UsesPrefsPasswordHash(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:  "0.0.0.0",
		Port:  8080,
		Users: []config.User{{Username: "alice", BasePath: dir}},
	}
	users := BuildUserFS(cfg)
	if len(users) != 1 {
		t.Fatalf("expected one user, got %d", len(users))
	}
	hash, err := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	prefsPath := filepath.Join(t.TempDir(), "filex.yaml")
	prefsDoc := &prefs.Preferences{Users: []prefs.User{{Username: "alice", PasswordHash: string(hash)}}}
	if err := prefsDoc.Save(prefsPath); err != nil {
		t.Fatal(err)
	}
	h := New(fsys, cfg, http.Dir(dir), authlib.NewStore(), users, "", map[string]string{"alice": prefsPath}, "test")

	login := func(username, password string) int {
		body := strings.NewReader(fmt.Sprintf(`{"username":%q,"password":%q}`, username, password))
		return doRequest(t, h, http.MethodPost, "/api/login", body).Code
	}
	if got := login("alice", "s3cret"); got != http.StatusOK {
		t.Fatalf("login with correct password = %d, want 200", got)
	}
	if got := login("alice", "wrong"); got != http.StatusUnauthorized {
		t.Fatalf("login with wrong password = %d, want 401", got)
	}
	if got := login("bob", "s3cret"); got != http.StatusUnauthorized {
		t.Fatalf("login with unknown user = %d, want 401", got)
	}

	// A configured user without a password_hash in their prefs file must fail.
	prefsDoc.Users[0].PasswordHash = ""
	if err := prefsDoc.Save(prefsPath); err != nil {
		t.Fatal(err)
	}
	if got := login("alice", "s3cret"); got != http.StatusUnauthorized {
		t.Fatalf("login without hash = %d, want 401", got)
	}
}

// TestShowDotfiles_FromPrefs locks in that the per-user show_dotfiles override
// is resolved from the preferences file (not config.yaml) and that an explicit
// dotfiles query param still wins.
func TestShowDotfiles_FromPrefs(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatalf("fs.New: %v", err)
	}
	cfg := &config.Config{
		Host:         "0.0.0.0",
		Port:         8080,
		ShowDotfiles: false, // server default
		Users:        []config.User{{Username: "alice", BasePath: dir}},
	}
	users := BuildUserFS(cfg)
	hash, _ := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.DefaultCost)
	dot := true
	prefsPath := filepath.Join(t.TempDir(), "filex.yaml")
	prefsDoc := &prefs.Preferences{Users: []prefs.User{{Username: "alice", PasswordHash: string(hash), ShowDotfiles: &dot}}}
	if err := prefsDoc.Save(prefsPath); err != nil {
		t.Fatal(err)
	}
	h := New(fsys, cfg, http.Dir(dir), authlib.NewStore(), users, "", map[string]string{"alice": prefsPath}, "test")

	// Log in to obtain a session cookie for the authenticated requests below.
	rr := doRequest(t, h, http.MethodPost, "/api/login",
		strings.NewReader(`{"username":"alice","password":"s3cret"}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("login = %d, want 200", rr.Code)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set by login")
	}

	configWithCookie := func(query string) map[string]any {
		req := httptest.NewRequest(http.MethodGet, "/api/config"+query, nil)
		req.AddCookie(cookies[0])
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("GET /api/config = %d, want 200", res.Code)
		}
		var out map[string]any
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// The per-user prefs override wins over the global config default.
	if got := configWithCookie(""); got["show_dotfiles"] != true {
		t.Fatalf("per-user show_dotfiles = %v, want true", got["show_dotfiles"])
	}
	// An explicit query param overrides the stored per-user preference.
	if got := configWithCookie("?dotfiles=false"); got["show_dotfiles"] != false {
		t.Fatalf("query override show_dotfiles = %v, want false", got["show_dotfiles"])
	}
}

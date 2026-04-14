package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/giulianozor/filex/internal/config"
	fslib "github.com/giulianozor/filex/internal/fs"
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
	h := New(fsys, cfg, http.Dir(dir))
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

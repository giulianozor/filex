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
	h := New(fsys, cfg, http.Dir(dir), nil, nil, "", "test")
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

	h := New(fsys, initialCfg, http.Dir(dir), nil, nil, cfgPath, "test")

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

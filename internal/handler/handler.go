package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/giulianozor/filex/internal/config"
	fslib "github.com/giulianozor/filex/internal/fs"
)

type Handler struct {
	fs  *fslib.FS
	cfg *config.Config
	mux *http.ServeMux
}

func New(fs *fslib.FS, cfg *config.Config, staticFS http.FileSystem) *Handler {
	h := &Handler{fs: fs, cfg: cfg, mux: http.NewServeMux()}
	h.registerRoutes(staticFS)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

func (h *Handler) registerRoutes(staticFS http.FileSystem) {
	h.mux.HandleFunc("/api/list", h.handleList)
	h.mux.HandleFunc("/api/download", h.handleDownload)
	h.mux.HandleFunc("/api/upload", h.handleUpload)
	h.mux.HandleFunc("/api/mkdir", h.handleMkdir)
	h.mux.HandleFunc("/api/rename", h.handleRename)
	h.mux.HandleFunc("/api/delete", h.handleDelete)
	h.mux.HandleFunc("/api/move", h.handleMove)
	h.mux.HandleFunc("/api/read", h.handleRead)
	h.mux.HandleFunc("/api/write", h.handleWrite)
	h.mux.HandleFunc("/api/info", h.handleInfo)
	h.mux.HandleFunc("/api/favourites", h.handleFavourites)
	h.mux.HandleFunc("/api/config", h.handleConfig)

	fileServer := http.FileServer(staticFS)
	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve static assets; fall back to index.html for SPA routes
		if r.URL.Path != "/" {
			// Try to open the file; if it fails serve index.html
			f, err := staticFS.Open(r.URL.Path)
			if err != nil {
				serveIndex(w, r, staticFS)
				return
			}
			f.Close()
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, fs http.FileSystem) {
	f, err := fs.Open("/index.html")
	if err != nil {
		http.Error(w, "index.html not found", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "stat error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", stat.ModTime(), f.(io.ReadSeeker))
}

// ---- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *Handler) showDotfiles(r *http.Request) bool {
	q := r.URL.Query().Get("dotfiles")
	if q == "true" {
		return true
	}
	if q == "false" {
		return false
	}
	return h.cfg.ShowDotfiles
}

// ---- API handlers -----------------------------------------------------------

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	entries, err := h.fs.ListDir(path, h.showDotfiles(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, entries)
}

func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}
	f, err := h.fs.OpenForDownload(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(stat.Name())))
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	dirPath := r.URL.Query().Get("path")
	if dirPath == "" {
		dirPath = "/"
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no file provided")
		return
	}
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		err = h.fs.SaveUpload(dirPath, fh.Filename, f)
		f.Close()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	if strings.ContainsAny(req.Name, `/\`) {
		writeError(w, http.StatusBadRequest, "name must not contain path separators")
		return
	}
	if err := h.fs.MkDir(req.Path, req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleRename(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Path    string `json:"path"`
		NewName string `json:"newname"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.NewName == "" {
		writeError(w, http.StatusBadRequest, "newname required")
		return
	}
	if strings.ContainsAny(req.NewName, `/\`) {
		writeError(w, http.StatusBadRequest, "newname must not contain path separators")
		return
	}
	if err := h.fs.Rename(req.Path, req.NewName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Path  string   `json:"path"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	targets := req.Paths
	if len(targets) == 0 && req.Path != "" {
		targets = []string{req.Path}
	}
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, "path or paths required")
		return
	}
	for _, p := range targets {
		if err := h.fs.Delete(p); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleMove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Src string `json:"src"`
		Dst string `json:"dst"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Src == "" || req.Dst == "" {
		writeError(w, http.StatusBadRequest, "src and dst required")
		return
	}
	if err := h.fs.Move(req.Src, req.Dst); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleRead(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}
	content, err := h.fs.ReadFile(path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"content": string(content)})
}

func (h *Handler) handleWrite(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return
	}
	if err := h.fs.WriteFile(req.Path, []byte(req.Content)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleInfo(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	entry, err := h.fs.FileInfo(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	usage, _ := h.fs.DiskUsage()
	writeJSON(w, map[string]any{"file": entry, "disk": usage})
}

func (h *Handler) handleFavourites(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, h.cfg.Favourites)
}

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"host":          h.cfg.Host,
		"port":          h.cfg.Port,
		"show_dotfiles": h.cfg.ShowDotfiles,
	})
}

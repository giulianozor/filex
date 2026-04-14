package handler

import (
"encoding/json"
"fmt"
"io"
"net/http"
"path/filepath"
"strconv"
"strings"

authlib "github.com/giulianozor/filex/internal/auth"
"github.com/giulianozor/filex/internal/config"
fslib "github.com/giulianozor/filex/internal/fs"
"golang.org/x/crypto/bcrypt"
)

// userEntry holds a user's resolved FS and resolved config.
type userEntry struct {
user *config.User
fs   *fslib.FS
}

// Handler is the main HTTP handler for filex.
type Handler struct {
cfg       *config.Config
globalFS  *fslib.FS
users     map[string]*userEntry // nil when auth disabled
authStore *authlib.Store        // nil when auth disabled
mux       *http.ServeMux
}

// New creates a Handler. If cfg.AuthRequired(), users must contain one FS per user.
func New(globalFS *fslib.FS, cfg *config.Config, staticFS http.FileSystem, authStore *authlib.Store, users map[string]*userEntry) *Handler {
h := &Handler{
cfg:       cfg,
globalFS:  globalFS,
users:     users,
authStore: authStore,
mux:       http.NewServeMux(),
}
h.registerRoutes(staticFS)
return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
h.mux.ServeHTTP(w, r)
}

// ---- static assets that are always public -----------------------------------

var publicPaths = map[string]bool{
"/login":      true,
"/login.html": true,
"/style.css":  true,
"/app.js":     true,
}

func isPublicPath(path string) bool {
return publicPaths[path]
}

func (h *Handler) registerRoutes(staticFS http.FileSystem) {
// Auth endpoints (always accessible)
h.mux.HandleFunc("/api/login", h.handleLogin)
h.mux.HandleFunc("/api/logout", h.handleLogout)

// Protected API endpoints
h.mux.HandleFunc("/api/me", h.authMiddleware(h.handleMe))
h.mux.HandleFunc("/api/list", h.authMiddleware(h.handleList))
h.mux.HandleFunc("/api/download", h.authMiddleware(h.handleDownload))
h.mux.HandleFunc("/api/upload", h.authMiddleware(h.handleUpload))
h.mux.HandleFunc("/api/mkdir", h.authMiddleware(h.handleMkdir))
h.mux.HandleFunc("/api/rename", h.authMiddleware(h.handleRename))
h.mux.HandleFunc("/api/delete", h.authMiddleware(h.handleDelete))
h.mux.HandleFunc("/api/move", h.authMiddleware(h.handleMove))
h.mux.HandleFunc("/api/read", h.authMiddleware(h.handleRead))
h.mux.HandleFunc("/api/write", h.authMiddleware(h.handleWrite))
h.mux.HandleFunc("/api/info", h.authMiddleware(h.handleInfo))
h.mux.HandleFunc("/api/favourites", h.authMiddleware(h.handleFavourites))
h.mux.HandleFunc("/api/config", h.authMiddleware(h.handleConfig))

fileServer := http.FileServer(staticFS)
h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
// Static assets are always public.
if isPublicPath(r.URL.Path) {
fileServer.ServeHTTP(w, r)
return
}

// "/" needs auth check — redirect to login if needed.
if r.URL.Path == "/" && h.cfg.AuthRequired() {
if h.authStore.FromRequest(r) == nil {
http.Redirect(w, r, "/login", http.StatusFound)
return
}
}

// Try to serve the static file; fall back to index.html for SPA routes.
if r.URL.Path != "/" {
f, err := staticFS.Open(r.URL.Path)
if err != nil {
// Unknown path — serve index.html (SPA) but check auth first.
if h.cfg.AuthRequired() && h.authStore.FromRequest(r) == nil {
http.Redirect(w, r, "/login", http.StatusFound)
return
}
serveIndex(w, r, staticFS)
return
}
f.Close()
}
fileServer.ServeHTTP(w, r)
})
}

// authMiddleware wraps a handler to enforce authentication when required.
func (h *Handler) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
return func(w http.ResponseWriter, r *http.Request) {
if h.cfg.AuthRequired() && h.authStore.FromRequest(r) == nil {
if strings.HasPrefix(r.URL.Path, "/api/") {
writeError(w, http.StatusUnauthorized, "not authenticated")
return
}
http.Redirect(w, r, "/login", http.StatusFound)
return
}
next(w, r)
}
}

// fsForRequest returns the FS for the current request's user (or global FS).
func (h *Handler) fsForRequest(r *http.Request) *fslib.FS {
if !h.cfg.AuthRequired() {
return h.globalFS
}
sess := h.authStore.FromRequest(r)
if sess == nil {
return h.globalFS
}
if e, ok := h.users[sess.Username]; ok {
return e.fs
}
return h.globalFS
}

// userForRequest returns the config.User for the current session, or nil.
func (h *Handler) userForRequest(r *http.Request) *config.User {
if !h.cfg.AuthRequired() {
return nil
}
sess := h.authStore.FromRequest(r)
if sess == nil {
return nil
}
if e, ok := h.users[sess.Username]; ok {
return e.user
}
return nil
}

// favouritesForRequest returns the favourites for the current user.
func (h *Handler) favouritesForRequest(r *http.Request) []config.Favourite {
u := h.userForRequest(r)
if u != nil && len(u.Favourites) > 0 {
return u.Favourites
}
return h.cfg.Favourites
}

// showDotfilesForRequest resolves whether to show dot files for this request.
func (h *Handler) showDotfilesForRequest(r *http.Request) bool {
q := r.URL.Query().Get("dotfiles")
if q == "true" {
return true
}
if q == "false" {
return false
}
u := h.userForRequest(r)
if u != nil && u.ShowDotfiles != nil {
return *u.ShowDotfiles
}
return h.cfg.ShowDotfiles
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

// ---- Auth handlers ----------------------------------------------------------

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
if r.Method != http.MethodPost {
writeError(w, http.StatusMethodNotAllowed, "POST required")
return
}

// If auth is not required, immediately return ok.
if !h.cfg.AuthRequired() {
writeJSON(w, map[string]string{"status": "ok", "username": "anonymous"})
return
}

var req struct {
Username string `json:"username"`
Password string `json:"password"`
}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
writeError(w, http.StatusBadRequest, "invalid request body")
return
}

u := h.cfg.FindUser(req.Username)
if u == nil {
writeError(w, http.StatusUnauthorized, "invalid credentials")
return
}

if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)); err != nil {
writeError(w, http.StatusUnauthorized, "invalid credentials")
return
}

token, err := h.authStore.Create(req.Username)
if err != nil {
writeError(w, http.StatusInternalServerError, "could not create session")
return
}

authlib.SetCookie(w, token)
writeJSON(w, map[string]string{"status": "ok", "username": req.Username})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
if sess := h.authStore.FromRequest(r); sess != nil {
h.authStore.Delete(sess.Token)
}
authlib.ClearCookie(w)
writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
if !h.cfg.AuthRequired() {
writeJSON(w, map[string]any{"username": "anonymous", "auth_required": false})
return
}
sess := h.authStore.FromRequest(r)
if sess == nil {
writeError(w, http.StatusUnauthorized, "not authenticated")
return
}
writeJSON(w, map[string]any{"username": sess.Username, "auth_required": true})
}

// ---- API handlers -----------------------------------------------------------

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
path := r.URL.Query().Get("path")
if path == "" {
path = "/"
}
entries, err := h.fsForRequest(r).ListDir(path, h.showDotfilesForRequest(r))
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
f, err := h.fsForRequest(r).OpenForDownload(path)
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
fs := h.fsForRequest(r)
for _, fh := range files {
f, err := fh.Open()
if err != nil {
writeError(w, http.StatusInternalServerError, err.Error())
return
}
err = fs.SaveUpload(dirPath, fh.Filename, f)
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
if err := h.fsForRequest(r).MkDir(req.Path, req.Name); err != nil {
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
if err := h.fsForRequest(r).Rename(req.Path, req.NewName); err != nil {
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
fs := h.fsForRequest(r)
for _, p := range targets {
if err := fs.Delete(p); err != nil {
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
if err := h.fsForRequest(r).Move(req.Src, req.Dst); err != nil {
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
content, err := h.fsForRequest(r).ReadFile(path)
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
if err := h.fsForRequest(r).WriteFile(req.Path, []byte(req.Content)); err != nil {
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
entry, err := h.fsForRequest(r).FileInfo(path)
if err != nil {
writeError(w, http.StatusNotFound, err.Error())
return
}
usage, _ := h.fsForRequest(r).DiskUsage()
writeJSON(w, map[string]any{"file": entry, "disk": usage})
}

func (h *Handler) handleFavourites(w http.ResponseWriter, r *http.Request) {
writeJSON(w, h.favouritesForRequest(r))
}

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
writeJSON(w, map[string]any{
"host":          h.cfg.Host,
"port":          h.cfg.Port,
"show_dotfiles": h.showDotfilesForRequest(r),
"auth_required": h.cfg.AuthRequired(),
})
}

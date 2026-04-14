package handler

import (
"encoding/json"
"fmt"
"io"
"net/http"
"path/filepath"
"strconv"
"strings"
"sync"

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
favMu       sync.RWMutex
runtimeFavs map[string][]config.Favourite // key: username or "" for global
cfgMu      sync.Mutex // protects cfg.Favourites / cfg.Users[i].Favourites during save
configPath string     // path to the loaded config.yaml; empty = no persistence
version    string     // application version string set at build time
}

// New creates a Handler. If cfg.AuthRequired(), users must contain one FS per user.
func New(globalFS *fslib.FS, cfg *config.Config, staticFS http.FileSystem, authStore *authlib.Store, users map[string]*userEntry, configPath string, version string) *Handler {
// Initialize runtime favourites from config, preserving the existing
// fallback: per-user favs take priority; users without their own fall back
// to the global list.
runtimeFavs := make(map[string][]config.Favourite)
globalFavs := make([]config.Favourite, len(cfg.Favourites))
copy(globalFavs, cfg.Favourites)
runtimeFavs[""] = globalFavs
for _, u := range cfg.Users {
var uf []config.Favourite
if len(u.Favourites) > 0 {
uf = make([]config.Favourite, len(u.Favourites))
copy(uf, u.Favourites)
} else {
uf = make([]config.Favourite, len(cfg.Favourites))
copy(uf, cfg.Favourites)
}
runtimeFavs[u.Username] = uf
}

h := &Handler{
cfg:         cfg,
globalFS:    globalFS,
users:       users,
authStore:   authStore,
mux:         http.NewServeMux(),
runtimeFavs: runtimeFavs,
configPath:  configPath,
version:     version,
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
h.mux.HandleFunc("/api/version", h.handleVersion)

// Protected API endpoints
h.mux.HandleFunc("/api/me", h.authMiddleware(h.handleMe))
h.mux.HandleFunc("/api/list", h.authMiddleware(h.handleList))
h.mux.HandleFunc("/api/download", h.authMiddleware(h.handleDownload))
h.mux.HandleFunc("/api/upload", h.authMiddleware(h.handleUpload))
h.mux.HandleFunc("/api/mkdir", h.authMiddleware(h.handleMkdir))
h.mux.HandleFunc("/api/rename", h.authMiddleware(h.handleRename))
h.mux.HandleFunc("/api/delete", h.authMiddleware(h.handleDelete))
h.mux.HandleFunc("/api/move", h.authMiddleware(h.handleMove))
h.mux.HandleFunc("/api/copy", h.authMiddleware(h.handleCopy))
h.mux.HandleFunc("/api/zip-download", h.authMiddleware(h.handleZipDownload))
h.mux.HandleFunc("/api/read", h.authMiddleware(h.handleRead))
h.mux.HandleFunc("/api/write", h.authMiddleware(h.handleWrite))
h.mux.HandleFunc("/api/info", h.authMiddleware(h.handleInfo))
h.mux.HandleFunc("/api/folder-info", h.authMiddleware(h.handleFolderInfo))
h.mux.HandleFunc("/api/favourites", h.authMiddleware(h.handleFavourites))
h.mux.HandleFunc("/api/favourites/add", h.authMiddleware(h.handleFavouritesAdd))
h.mux.HandleFunc("/api/favourites/remove", h.authMiddleware(h.handleFavouritesRemove))
h.mux.HandleFunc("/api/config", h.authMiddleware(h.handleConfig))

// /login is always public — serves login.html.
h.mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
serveLogin(w, r, staticFS)
})

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

// favKeyForRequest returns the map key used to look up runtime favourites.
// When auth is disabled the single global bucket ("") is used.
func (h *Handler) favKeyForRequest(r *http.Request) string {
if !h.cfg.AuthRequired() {
return ""
}
sess := h.authStore.FromRequest(r)
if sess == nil {
return ""
}
return sess.Username
}

// favouritesForRequest returns the favourites for the current user.
func (h *Handler) favouritesForRequest(r *http.Request) []config.Favourite {
key := h.favKeyForRequest(r)
h.favMu.RLock()
favs := h.runtimeFavs[key]
h.favMu.RUnlock()
return favs
}

// persistFavourites writes the favourites for key back to the config file.
// It is a no-op when no config file was loaded (configPath == "").
func (h *Handler) persistFavourites(key string, favs []config.Favourite) {
if h.configPath == "" {
return
}
h.cfgMu.Lock()
defer h.cfgMu.Unlock()
if key == "" {
h.cfg.Favourites = favs
} else {
for i := range h.cfg.Users {
if h.cfg.Users[i].Username == key {
h.cfg.Users[i].Favourites = favs
break
}
}
}
_ = h.cfg.Save(h.configPath)
}

// copyFavs returns a copy of the favourites slice for key, called with favMu held.
func (h *Handler) copyFavs(key string) []config.Favourite {
src := h.runtimeFavs[key]
dst := make([]config.Favourite, len(src))
copy(dst, src)
return dst
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

func serveLogin(w http.ResponseWriter, r *http.Request, staticFS http.FileSystem) {
f, err := staticFS.Open("/login.html")
if err != nil {
http.Error(w, "login.html not found", http.StatusInternalServerError)
return
}
defer f.Close()
stat, err := f.Stat()
if err != nil {
http.Error(w, "stat error", http.StatusInternalServerError)
return
}
w.Header().Set("Content-Type", "text/html; charset=utf-8")
http.ServeContent(w, r, "login.html", stat.ModTime(), f.(io.ReadSeeker))
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

// ---- Version handler --------------------------------------------------------

func (h *Handler) handleVersion(w http.ResponseWriter, r *http.Request) {
writeJSON(w, map[string]string{"version": h.version})
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
Username   string `json:"username"`
Password   string `json:"password"`
RememberMe bool   `json:"remember_me"`
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

sessionTTL := h.cfg.SessionTTL()

var token string
var err error
if req.RememberMe {
// Persistent session: saved to disk so it survives service restarts.
// A persistent cookie is set so the browser retains it across restarts too.
token, err = h.authStore.CreatePersistentWithTTL(req.Username, sessionTTL)
} else {
// In-memory session: lost on service restart (intentional).
// A session cookie is set so the browser discards it when closed.
token, err = h.authStore.CreateWithTTL(req.Username, sessionTTL)
}
if err != nil {
writeError(w, http.StatusInternalServerError, "could not create session")
return
}

if req.RememberMe {
// Persistent cookie: survives browser restarts for the full session TTL.
authlib.SetCookieWithTTL(w, token, sessionTTL)
} else {
// Session cookie: browser discards it when the window/tab is closed.
authlib.SetSessionCookie(w, token)
}
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

func (h *Handler) handleCopy(w http.ResponseWriter, r *http.Request) {
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
if err := h.fsForRequest(r).Copy(req.Src, req.Dst); err != nil {
writeError(w, http.StatusBadRequest, err.Error())
return
}
writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleZipDownload(w http.ResponseWriter, r *http.Request) {
if r.Method != http.MethodPost {
writeError(w, http.StatusMethodNotAllowed, "POST required")
return
}
var req struct {
Paths []string `json:"paths"`
}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
writeError(w, http.StatusBadRequest, err.Error())
return
}
if len(req.Paths) == 0 {
writeError(w, http.StatusBadRequest, "paths required")
return
}
name := "download"
if len(req.Paths) == 1 {
base := filepath.Base(req.Paths[0])
if base != "/" && base != "." && base != "" {
name = base
}
}
w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, name))
w.Header().Set("Content-Type", "application/zip")
if err := h.fsForRequest(r).ZipPaths(w, req.Paths); err != nil {
// Response headers already sent; cannot write JSON error.
return
}
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
usage, _ := h.fsForRequest(r).DiskUsageAt(path)
writeJSON(w, map[string]any{"file": entry, "disk": usage})
}

func (h *Handler) handleFolderInfo(w http.ResponseWriter, r *http.Request) {
path := r.URL.Query().Get("path")
if path == "" {
writeError(w, http.StatusBadRequest, "path required")
return
}
entry, err := h.fsForRequest(r).FileInfo(path)
if err != nil {
writeError(w, http.StatusNotFound, err.Error())
return
}
if !entry.IsDir {
writeError(w, http.StatusBadRequest, "path is not a directory")
return
}
totalSize, fileCount, _ := h.fsForRequest(r).DirSize(path)
writeJSON(w, map[string]any{
"name":       entry.Name,
"path":       entry.Path,
"mod_time":   entry.ModTime,
"total_size": totalSize,
"file_count": fileCount,
})
}

func (h *Handler) handleFavourites(w http.ResponseWriter, r *http.Request) {
if r.Method != http.MethodGet {
writeError(w, http.StatusMethodNotAllowed, "GET required")
return
}
writeJSON(w, h.favouritesForRequest(r))
}

func (h *Handler) handleFavouritesAdd(w http.ResponseWriter, r *http.Request) {
if r.Method != http.MethodPost {
writeError(w, http.StatusMethodNotAllowed, "POST required")
return
}
var req struct {
Path string `json:"path"`
Name string `json:"name"`
}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
writeError(w, http.StatusBadRequest, "invalid request body")
return
}
if req.Path == "" {
writeError(w, http.StatusBadRequest, "path required")
return
}
// Validate that the path is an existing directory.
entry, err := h.fsForRequest(r).FileInfo(req.Path)
if err != nil {
writeError(w, http.StatusBadRequest, err.Error())
return
}
if !entry.IsDir {
writeError(w, http.StatusBadRequest, "path is not a directory")
return
}
// Derive a display name from the path when none is provided.
if req.Name == "" {
req.Name = filepath.Base(req.Path)
if req.Name == "/" || req.Name == "." {
req.Name = "Root"
}
}
key := h.favKeyForRequest(r)
h.favMu.Lock()
favs := h.runtimeFavs[key]
// Skip duplicates.
for _, f := range favs {
if f.Path == req.Path {
h.favMu.Unlock()
writeJSON(w, map[string]string{"status": "ok"})
return
}
}
h.runtimeFavs[key] = append(favs, config.Favourite{Name: req.Name, Path: req.Path})
updated := h.copyFavs(key)
h.favMu.Unlock()
h.persistFavourites(key, updated)
writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleFavouritesRemove(w http.ResponseWriter, r *http.Request) {
if r.Method != http.MethodPost {
writeError(w, http.StatusMethodNotAllowed, "POST required")
return
}
var req struct {
Path string `json:"path"`
}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
writeError(w, http.StatusBadRequest, "invalid request body")
return
}
if req.Path == "" {
writeError(w, http.StatusBadRequest, "path required")
return
}
key := h.favKeyForRequest(r)
h.favMu.Lock()
favs := h.runtimeFavs[key]
updated := make([]config.Favourite, 0, len(favs))
for _, f := range favs {
if f.Path != req.Path {
updated = append(updated, f)
}
}
h.runtimeFavs[key] = updated
saveFavs := h.copyFavs(key)
h.favMu.Unlock()
h.persistFavourites(key, saveFavs)
writeJSON(w, map[string]string{"status": "ok"})
}

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
writeJSON(w, map[string]any{
"host":             h.cfg.Host,
"port":             h.cfg.Port,
"show_dotfiles":    h.showDotfilesForRequest(r),
"auth_required":    h.cfg.AuthRequired(),
"session_ttl_days": h.cfg.SessionTTLDays,
})
}

package handler

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime/debug"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/giulianozor/filex/internal/atomicfile"
	authlib "github.com/giulianozor/filex/internal/auth"
	"github.com/giulianozor/filex/internal/config"
	fslib "github.com/giulianozor/filex/internal/fs"
	"github.com/giulianozor/filex/internal/prefs"
	"golang.org/x/crypto/bcrypt"
)

// userEntry holds a user's resolved FS and resolved config.
type userEntry struct {
	user *config.User
	fs   *fslib.FS
}

// Handler is the main HTTP handler for filex.
type Handler struct {
	cfg              *config.Config
	globalFS         *fslib.FS
	users            map[string]*userEntry // nil when auth disabled
	authStore        *authlib.Store        // nil when auth disabled
	mux              *http.ServeMux
	favMu            sync.RWMutex
	runtimeFavs      map[string][]config.Favourite // key: username or "" for global
	protectedMu      sync.RWMutex
	runtimeProtected map[string][]string // key: username or "" for global
	deletePrefsMu    sync.RWMutex
	runtimeDelete    map[string]prefs.Deletion // key: username or "" for global (no-auth)
	prefsMu          sync.Mutex                // guards prefsFiles (per-user preferences)
	prefsFiles       map[string]*prefsFile     // key: username, or "" for the no-auth process-user file
	cfgMu            sync.Mutex                // protects cfg global favourites/protected during save
	configPath       string                    // path to the loaded config.yaml; empty = no persistence
	version          string                    // application version string set at build time
	tools            map[string]bool           // external binary availability, keyed by exec.LookPath name
	thumbGenMu       sync.RWMutex
	thumbGenerations map[string]*thumbGeneration
	batchQueue       map[string]map[string]struct{} // requestKey -> hash -> queued, so batches of different users don't collide
	batchQueueMu     sync.Mutex
	batchActive      map[string]batchActive // requestKey -> current batch, for resuming after page reload
	batchActiveMu    sync.Mutex
}

// batchActive tracks one background batch with an ownership marker: a later
// batch for the same user updates the entry (new paths, new generation), and
// the earlier batch's completion defer must not clear the newer entry.
type batchActive struct {
	pending []string
	gen     int
}

// prefsFile tracks one on-disk preferences file together with its in-memory
// copy. One file exists per configured user (under that user's own home
// directory, see prefs.PathForUser), plus a single process-user file for the
// no-auth ("" key) case. An empty path means in-memory only: nothing is ever
// written and every change is lost on restart.
// uid/gid are the owner the file should be chowned to on save (-1 = leave
// ownership unchanged), so a root daemon leaves each user's preferences owned
// by that user rather than by root.
type prefsFile struct {
	path string             // preferences file path; empty = no persistence
	p    *prefs.Preferences // this user's (or the "": process-user) preferences
	uid  int                // desired file owner UID; -1 = leave unchanged
	gid  int                // desired file owner GID; -1 = leave unchanged
	size int64              // on-disk size at last load/save, for detecting external edits
	mod  time.Time          // on-disk mtime at last load/save, for detecting external edits
}

// loadPrefsFile reads the preferences at path into a prefsFile. A missing file
// or an empty path yields an empty Preferences (first-save starts from
// defaults); a corrupt file is logged and also yields an empty copy rather than
// failing startup, matching the loader's historical tolerance.
func loadPrefsFile(path string, uid, gid int) *prefsFile {
	f := &prefsFile{path: path, p: &prefs.Preferences{}, uid: uid, gid: gid}
	if path == "" {
		return f
	}
	p, err := prefs.Load(path)
	if err != nil {
		log.Printf("WARNING: cannot load preferences from %s: %v (starting empty)", path, err)
	} else {
		f.p = p
	}
	if info, err := os.Stat(path); err == nil {
		f.size, f.mod = info.Size(), info.ModTime()
	}
	return f
}

type thumbGeneration struct {
	done       chan struct{}
	cancelled  chan struct{}
	cancel     context.CancelFunc
	cancelOnce sync.Once // guards close(cancelled) so repeated cancels do not panic
	err        error
	total      int          // estimated total thumbnails, -1 if unknown
	completed  atomic.Int64 // number of thumbnails written so far
}

// getThumbGeneration returns the in-flight generation registered for hashHex,
// or nil when none is running. It is safe for concurrent use.
func (h *Handler) getThumbGeneration(hashHex string) *thumbGeneration {
	h.thumbGenMu.RLock()
	gen := h.thumbGenerations[hashHex]
	h.thumbGenMu.RUnlock()
	return gen
}

// cancelThumbnailGeneration removes the generation for hashHex, signals it
// (idempotently) and waits for its goroutine to fully stop before returning.
// It returns the generation and whether it had already completed before this
// call, in which case nothing was cancelled.
func (h *Handler) cancelThumbnailGeneration(hashHex string) (gen *thumbGeneration, finished bool) {
	h.thumbGenMu.Lock()
	gen = h.thumbGenerations[hashHex]
	delete(h.thumbGenerations, hashHex)
	h.thumbGenMu.Unlock()
	if gen == nil {
		return nil, true
	}
	select {
	case <-gen.done:
		return gen, true
	default:
	}
	gen.cancelOnce.Do(func() {
		close(gen.cancelled)
		if gen.cancel != nil {
			gen.cancel()
		}
	})
	<-gen.done
	return gen, false
}

// New creates a Handler. If cfg.AuthRequired(), users must contain one FS per user.
// prefsPaths maps each configured username to its preferences file, and the ""
// key to the process-user file used in no-auth mode; an entry with an empty
// string is in-memory only. Per-user favourites and protected paths override
// the global (config.yaml) lists, which stay under the "" bucket.
func New(globalFS *fslib.FS, cfg *config.Config, staticFS http.FileSystem, authStore *authlib.Store, users map[string]*userEntry, configPath string, prefsPaths map[string]string, version string) *Handler {
	prefsFiles := make(map[string]*prefsFile, len(cfg.Users)+1)
	// The no-auth file belongs to the process user, whose ownership the temp
	// file inherits automatically; -1 keeps it that way.
	prefsFiles[""] = loadPrefsFile(prefsPaths[""], -1, -1)
	for i := range cfg.Users {
		u := &cfg.Users[i]
		uid, gid := prefs.OwnerForUser(u.Username, u.UID, u.GID)
		prefsFiles[u.Username] = loadPrefsFile(prefsPaths[u.Username], uid, gid)
	}
	runtimeFavs := initUserMap(cfg.Users, func(u *config.User) []config.Favourite {
		if pu := prefsFiles[u.Username].p.FindUser(u.Username); pu != nil {
			return pu.Favourites
		}
		return nil
	}, cfg.Favourites, normalizeFavourites)
	runtimeProtected := initUserMap(cfg.Users, func(u *config.User) []string {
		if pu := prefsFiles[u.Username].p.FindUser(u.Username); pu != nil {
			return pu.ProtectedPaths
		}
		return nil
	}, cfg.ProtectedPaths, func(v []string) []string { return v })
	runtimeDelete := initDeleteMap(cfg.Users, prefsFiles)

	toolAvailable := func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	}
	toolNames := []string{"ffmpeg", "ffprobe", "unzip", "7z", "unrar", "tar"}
	h := &Handler{
		cfg:              cfg,
		globalFS:         globalFS,
		users:            users,
		authStore:        authStore,
		mux:              http.NewServeMux(),
		runtimeFavs:      runtimeFavs,
		runtimeProtected: runtimeProtected,
		runtimeDelete:    runtimeDelete,
		prefsFiles:       prefsFiles,
		configPath:       configPath,
		version:          version,
		tools:            make(map[string]bool, len(toolNames)),
		thumbGenerations: make(map[string]*thumbGeneration),
		batchQueue:       make(map[string]map[string]struct{}),
		batchActive:      make(map[string]batchActive),
	}
	for _, name := range toolNames {
		h.tools[name] = toolAvailable(name)
	}
	h.registerRoutes(staticFS)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A panic in any handler (e.g. an OS credential restore failure inside
	// FS.RunAs) must not kill the whole server or leak a stack to the client.
	defer func() {
		if p := recover(); p != nil {
			log.Printf("panic serving %s %s: %v\n%s", r.Method, r.URL.Path, p, debug.Stack())
			writeError(w, http.StatusInternalServerError, "internal server error")
		}
	}()
	h.mux.ServeHTTP(w, r)
}

// initUserMap builds the per-user runtime state map from config (global
// defaults) plus a per-user provider (e.g. the prefs file). Global defaults are
// stored under the "" key; a user without their own value falls back to the
// global list. getUser selects the per-user slice, and normalize is applied to
// every list before it is stored.
func initUserMap[T any](users []config.User, getUser func(u *config.User) []T, global []T, normalize func([]T) []T) map[string][]T {
	out := make(map[string][]T, len(users)+1)
	out[""] = normalize(slices.Clone(global))
	for i := range users {
		u := &users[i]
		val := getUser(u)
		if len(val) == 0 {
			val = global
		}
		out[u.Username] = normalize(slices.Clone(val))
	}
	return out
}

// initDeleteMap builds the per-user secure-delete preference map. The global
// (no-auth) entry comes from the process-user prefs file; a user without their
// own deletion entry inherits the global one.
func initDeleteMap(users []config.User, files map[string]*prefsFile) map[string]prefs.Deletion {
	out := make(map[string]prefs.Deletion, len(users)+1)
	global := files[""].p.Deletion
	out[""] = global
	for i := range users {
		u := &users[i]
		d := global
		if f, ok := files[u.Username]; ok {
			if pu := f.p.FindUser(u.Username); pu != nil {
				if pu.Deletion.DefaultWipe || pu.Deletion.WipeMethod != "" {
					d = pu.Deletion
				}
			}
		}
		out[u.Username] = d
	}
	return out
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

// maxWriteBodyBytes caps the size of a /api/write request body to bound memory
// usage. It is intentionally larger than the read/editor cap (10 MB) so saved
// files round-trip, while still blocking egregiously large uploads.
const maxWriteBodyBytes = 20 << 20 // 20 MB

// maxUploadRequestBody caps a single /api/upload multipart request. The
// frontend switches to chunked uploads above 32 MiB, so non-chunked requests
// are small; this bound blocks unbounded single-shot uploads without breaking
// chunking (chunks are at most 16 MiB each).
const maxUploadRequestBody = 16 << 30 // 16 GiB

// maxJSONBodyBytes caps JSON request bodies served from /api (login, move/copy
// path lists, favourites updates, interval lists, ...). Without a bound a
// single request can force the decoder to buffer arbitrarily large payloads;
// uploads and file writes are exempted via their own dedicated limits.
const maxJSONBodyBytes = 8 << 20 // 8 MiB

// maxFavouriteEntries bounds the number of favourites a single update request
// may submit, so an abusive payload cannot balloon the persisted config file.
const maxFavouriteEntries = 1000

// maxExtractSegments bounds how many segments one video-extract request may
// contain. Each segment spawns an ffmpeg process and a temp file, so an
// unbounded list lets a single request run the host out of processes/disk.
const maxExtractSegments = 100

// maxBatchThumbnailPaths bounds how many videos one batch-thumbnail request may
// queue. Each path spawns thumbnail work on the shared worker pool, so an
// unbounded list would let a single request flood the queue.
const maxBatchThumbnailPaths = 500

// maxVideoIntervals bounds how many interval markers a video may store: the
// editor renders each marker as a DOM element, so an unbounded list would let
// one request bloat both the persisted file and every subsequent page render.
const maxVideoIntervals = 1000

// maxIntervalsBytes caps how large a persisted intervals file may be when it is
// read back. The save path already bounds it (maxVideoIntervals entries), but
// the file lives in the user-writable jail, so it is re-checked on load to stop
// a pre-seeded oversized file from being slurped into memory.
const maxIntervalsBytes = 8 << 20 // 8 MiB

// maxThumbManifestBytes caps how large a persisted thumbnail manifest
// (timestamps.json) may be when read back, for the same reason as
// maxIntervalsBytes: the cache lives in the user-writable jail.
const maxThumbManifestBytes = 8 << 20 // 8 MiB

// withBodyLimit caps the request body to n bytes before delegating to next.
// Oversized bodies abort with a 413 before reaching the handler, so small JSON
// handlers never buffer attacker-controlled payloads beyond the bound.
func withBodyLimit(n int64, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, n)
		next(w, r)
	}
}

// readBoundedFile reads up to maxBytes+1 bytes from path and rejects the file
// when it exceeds maxBytes, so an oversized attacker-seeded file in the jail
// can never balloon memory. Absent or oversized/corrupt files surface their
// error as-is; callers distinguish "not generated yet" from "corrupt".
func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("file too large (more than %d bytes)", maxBytes)
	}
	return data, nil
}

// route registers an authenticated JSON API endpoint whose body is bounded by
// limit. It collapses the (once repeated across ~20 routes) wrap in
// authMiddleware + withBodyLimit around every handler.
func (h *Handler) route(pattern string, limit int64, handler http.HandlerFunc) {
	h.mux.HandleFunc(pattern, h.authMiddleware(withBodyLimit(limit, handler)))
}

// routePlain registers an authenticated API endpoint with no body bound
// (GET-only or streaming handlers that set their own limits).
func (h *Handler) routePlain(pattern string, handler http.HandlerFunc) {
	h.mux.HandleFunc(pattern, h.authMiddleware(handler))
}

// routePublic registers a public API endpoint (no authMiddleware) with an
// optional bounded body. Only /api/login (bounded) and /api/version (no body)
// use it.
func (h *Handler) routePublic(pattern string, limit int64, handler http.HandlerFunc) {
	if limit > 0 {
		handler = withBodyLimit(limit, handler)
	}
	h.mux.HandleFunc(pattern, handler)
}

// registerEndpoint registers one catalog route, or a method-dispatching
// handler when several routes share a path (a same-URL GET/POST pair). The few
// shared-path endpoints are all authenticated and carry at most maxJSONBodyBytes,
// so the group is wrapped once and each inner handler re-checks its method.
func (h *Handler) registerEndpoint(routes []apiRoute) {
	if len(routes) == 1 {
		rt := routes[0]
		switch {
		case rt.public:
			h.routePublic(rt.pattern, rt.bodyLimit, h.handlerFor(rt.name))
		case rt.bodyLimit > 0:
			h.route(rt.pattern, rt.bodyLimit, h.handlerFor(rt.name))
		default:
			h.routePlain(rt.pattern, h.handlerFor(rt.name))
		}
		return
	}
	h.mux.HandleFunc(routes[0].pattern, h.authMiddleware(withBodyLimit(maxJSONBodyBytes, func(w http.ResponseWriter, r *http.Request) {
		for _, rt := range routes {
			if rt.method == r.Method {
				h.handlerFor(rt.name)(w, r)
				return
			}
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	})))
}

// apiRoute describes one registered JSON/streaming API endpoint. It is the
// single source of truth for both the ServeMux registration below and the
// generated OpenAPI document (tools/gen-openapi, `make openapi`), so the
// shipped spec can never drift from the routes actually served.
type apiRoute struct {
	method    string
	pattern   string
	name      string // *Handler method serving the route, e.g. "handleList"
	public    bool
	bodyLimit int64 // 0 = no implicit bound; GET/streaming handlers set their own
}

// APIRoute is the exported, documentation-facing view of one registered API
// endpoint, as consumed by the OpenAPI generator.
type APIRoute struct {
	Method    string
	Pattern   string
	Handler   string // handler method name, e.g. "handleList"
	Public    bool
	BodyLimit int64
}

// APIRoutes returns a snapshot of every registered API endpoint in catalog
// order. It backs the OpenAPI document generator so the documentation cannot
// grow a second, hand-maintained route list that drifts out of sync.
func APIRoutes() []APIRoute {
	routes := make([]APIRoute, len(apiRouteCatalog))
	for i, r := range apiRouteCatalog {
		routes[i] = APIRoute{Method: r.method, Pattern: r.pattern, Handler: r.name, Public: r.public, BodyLimit: r.bodyLimit}
	}
	return routes
}

// apiRouteCatalog enumerates every /api endpoint. Public routes come first,
// then the rest, in the same order the registration used to hardcode them.
var apiRouteCatalog = []apiRoute{
	{method: http.MethodPost, pattern: "/api/login", name: "handleLogin", public: true, bodyLimit: maxJSONBodyBytes},
	{method: http.MethodGet, pattern: "/api/version", name: "handleVersion", public: true},
	{method: http.MethodPost, pattern: "/api/logout", name: "handleLogout"},
	{method: http.MethodPost, pattern: "/api/change-password", name: "handleChangePassword", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodGet, pattern: "/api/me", name: "handleMe"},
	{method: http.MethodGet, pattern: "/api/list", name: "handleList"},
	{method: http.MethodGet, pattern: "/api/download", name: "handleDownload"},
	{method: http.MethodGet, pattern: "/api/stream", name: "handleStream"},
	{method: http.MethodPost, pattern: "/api/upload", name: "handleUpload"},
	{method: http.MethodPost, pattern: "/api/mkdir", name: "handleMkdir", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/create-file", name: "handleCreateFile", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/rename", name: "handleRename", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/delete", name: "handleDelete", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/move", name: "handleMove", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/copy", name: "handleCopy", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/zip-download", name: "handleZipDownload", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodGet, pattern: "/api/read", name: "handleRead"},
	{method: http.MethodPost, pattern: "/api/write", name: "handleWrite"},
	{method: http.MethodGet, pattern: "/api/info", name: "handleInfo"},
	{method: http.MethodGet, pattern: "/api/folder-info", name: "handleFolderInfo"},
	{method: http.MethodGet, pattern: "/api/favourites", name: "handleFavourites"},
	{method: http.MethodPost, pattern: "/api/favourites/add", name: "handleFavouritesAdd", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/favourites/remove", name: "handleFavouritesRemove", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/favourites/update", name: "handleFavouritesUpdate", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodGet, pattern: "/api/protected", name: "handleProtected"},
	{method: http.MethodPost, pattern: "/api/protected/add", name: "handleProtectedAdd", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/protected/remove", name: "handleProtectedRemove", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodGet, pattern: "/api/delete-prefs", name: "handleDeletePrefs"},
	{method: http.MethodPost, pattern: "/api/delete-prefs", name: "handleDeletePrefsUpdate", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/show-dotfiles", name: "handleShowDotfiles", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodGet, pattern: "/api/config", name: "handleConfig"},
	{method: http.MethodGet, pattern: "/api/video-thumbnails", name: "handleVideoThumbnails"},
	{method: http.MethodGet, pattern: "/api/video-thumbnail-image", name: "handleVideoThumbnailImage"},
	{method: http.MethodPost, pattern: "/api/video-extract", name: "handleVideoExtract", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/video-delete-thumbnails", name: "handleVideoDeleteThumbnails", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/video-cancel-thumbnails", name: "handleVideoCancelThumbnails", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/video-save-intervals", name: "handleVideoSaveIntervals", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodGet, pattern: "/api/video-load-intervals", name: "handleVideoLoadIntervals"},
	{method: http.MethodPost, pattern: "/api/video-snap-time", name: "handleVideoSnapTime", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodGet, pattern: "/api/video-batch-status", name: "handleVideoBatchStatus"},
	{method: http.MethodPost, pattern: "/api/video-batch-thumbnails", name: "handleVideoBatchThumbnails", bodyLimit: maxJSONBodyBytes},
	{method: http.MethodPost, pattern: "/api/extract-archive", name: "handleExtractArchive", bodyLimit: maxJSONBodyBytes},
}

// handlerFor resolves a catalog handler name to the bound method. The explicit
// switch keeps the catalog usable without reflect and fails loudly at startup
// if a route ever names a handler that does not exist.
func (h *Handler) handlerFor(name string) http.HandlerFunc {
	switch name {
	case "handleLogin":
		return h.handleLogin
	case "handleVersion":
		return h.handleVersion
	case "handleLogout":
		return h.handleLogout
	case "handleChangePassword":
		return h.handleChangePassword
	case "handleMe":
		return h.handleMe
	case "handleList":
		return h.handleList
	case "handleDownload":
		return h.handleDownload
	case "handleStream":
		return h.handleStream
	case "handleUpload":
		return h.handleUpload
	case "handleMkdir":
		return h.handleMkdir
	case "handleCreateFile":
		return h.handleCreateFile
	case "handleRename":
		return h.handleRename
	case "handleDelete":
		return h.handleDelete
	case "handleMove":
		return h.handleMove
	case "handleCopy":
		return h.handleCopy
	case "handleZipDownload":
		return h.handleZipDownload
	case "handleRead":
		return h.handleRead
	case "handleWrite":
		return h.handleWrite
	case "handleInfo":
		return h.handleInfo
	case "handleFolderInfo":
		return h.handleFolderInfo
	case "handleFavourites":
		return h.handleFavourites
	case "handleFavouritesAdd":
		return h.handleFavouritesAdd
	case "handleFavouritesRemove":
		return h.handleFavouritesRemove
	case "handleFavouritesUpdate":
		return h.handleFavouritesUpdate
	case "handleProtected":
		return h.handleProtected
	case "handleProtectedAdd":
		return h.handleProtectedAdd
	case "handleProtectedRemove":
		return h.handleProtectedRemove
	case "handleDeletePrefs":
		return h.handleDeletePrefs
	case "handleDeletePrefsUpdate":
		return h.handleDeletePrefsUpdate
	case "handleShowDotfiles":
		return h.handleShowDotfiles
	case "handleConfig":
		return h.handleConfig
	case "handleVideoThumbnails":
		return h.handleVideoThumbnails
	case "handleVideoThumbnailImage":
		return h.handleVideoThumbnailImage
	case "handleVideoExtract":
		return h.handleVideoExtract
	case "handleVideoDeleteThumbnails":
		return h.handleVideoDeleteThumbnails
	case "handleVideoCancelThumbnails":
		return h.handleVideoCancelThumbnails
	case "handleVideoSaveIntervals":
		return h.handleVideoSaveIntervals
	case "handleVideoLoadIntervals":
		return h.handleVideoLoadIntervals
	case "handleVideoSnapTime":
		return h.handleVideoSnapTime
	case "handleVideoBatchStatus":
		return h.handleVideoBatchStatus
	case "handleVideoBatchThumbnails":
		return h.handleVideoBatchThumbnails
	case "handleExtractArchive":
		return h.handleExtractArchive
	}
	panic("handlerFor: unknown route handler " + name)
}

func (h *Handler) registerRoutes(staticFS http.FileSystem) {
	// Register the /api catalog. Public routes skip authMiddleware (a valid
	// session is required everywhere else whenever auth is enabled); JSON-payload
	// routes are wrapped with a body size bound so no handler decodes unbounded
	// input from a client. /api/version is public because the Docker HEALTHCHECK
	// probes it without a session; keeping it behind auth would mark an
	// auth-enabled deployment permanently unhealthy.
	//
	// Several endpoints share one URL with different methods (e.g. GET and POST
	// /api/delete-prefs). Go's ServeMux path patterns cannot host two handlers
	// on the same path, so same-path entries are grouped and served by a single
	// dispatcher that fans out on the request method.
	grouped := make(map[string][]apiRoute)
	for _, rt := range apiRouteCatalog {
		grouped[rt.pattern] = append(grouped[rt.pattern], rt)
	}
	for _, routes := range grouped {
		h.registerEndpoint(routes)
	}

	// /login is always public — serves login.html.
	h.mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if !requireGet(w, r) {
			return
		}
		serveHTMLFile(w, r, staticFS, "login.html")
	})

	fileServer := http.FileServer(staticFS)
	h.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Static assets are always public.
		if isPublicPath(r.URL.Path) {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Redirect to the login page when auth is required and the visitor is
		// not logged in. Returns true when a redirect happened.
		requireLogin := func() bool {
			if h.cfg.AuthRequired() && h.authContext(r) == nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return true
			}
			return false
		}

		// "/" needs auth check — redirect to login if needed.
		if r.URL.Path == "/" && requireLogin() {
			return
		}

		// Try to serve the static file; fall back to index.html for SPA routes.
		if r.URL.Path != "/" {
			f, err := staticFS.Open(r.URL.Path)
			if err != nil {
				// Unknown API endpoint: never fall through to the SPA shell,
				// which would return index.html with HTTP 200 for a typo.
				if strings.HasPrefix(r.URL.Path, "/api/") {
					writeError(w, http.StatusNotFound, "unknown endpoint")
					return
				}
				// Unknown path — serve index.html (SPA) but check auth first.
				if requireLogin() {
					return
				}
				serveHTMLFile(w, r, staticFS, "index.html")
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
		// Resolve the session once and cache it so fsForRequest and requestKey
		// do not re-parse the cookie and re-do the store lookup for every helper
		// call.
		var entry *userEntry
		needAuth := h.cfg.AuthRequired()
		if needAuth {
			entry = h.authContext(r)
		}
		r = r.WithContext(context.WithValue(r.Context(), authCtxKey{}, authCtxValue{entry: entry}))
		if needAuth && entry == nil {
			// A stale session means the auth cookie points at a user that no
			// longer exists (or an expired/wiped store); clear it so the
			// browser stops resending it on every request.
			authlib.ClearCookie(w, isSecureRequest(r))
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

// authContext returns the user entry bound to the current request's session,
// or nil when auth is disabled, the session is missing, or the session's user
// no longer exists in the config. A session for a deleted user is dropped
// so a remembered browser cookie cannot silently fall back to the global FS.
func (h *Handler) authContext(r *http.Request) *userEntry {
	if !h.cfg.AuthRequired() {
		return nil
	}
	sess := h.authStore.FromRequest(r)
	if sess == nil {
		return nil
	}
	e := h.users[sess.Username]
	if e == nil {
		h.authStore.Delete(sess.Token)
		return nil
	}
	return e
}

// authCtxKey is the request-context key under which authMiddleware stores the
// resolved user entry so downstream helpers do not re-parse the session cookie
// and re-do the store lookup for every helper that needs the current user.
type authCtxKey struct{}

// authCtxValue carries the cached resolution; the wrapper type distinguishes
// "already resolved to nil" from "never resolved" so the fallback below still
// runs for requests that bypassed the middleware.
type authCtxValue struct{ entry *userEntry }

// userEntryFor returns the user entry for the current request, reusing the
// resolution performed by authMiddleware when available and falling back to a
// fresh authContext lookup otherwise.
func (h *Handler) userEntryFor(r *http.Request) *userEntry {
	if v, ok := r.Context().Value(authCtxKey{}).(authCtxValue); ok {
		return v.entry
	}
	return h.authContext(r)
}

// fsForRequest returns the FS for the current request's user (or global FS).
func (h *Handler) fsForRequest(r *http.Request) *fslib.FS {
	if e := h.userEntryFor(r); e != nil {
		return e.fs
	}
	return h.globalFS
}

// requestKey returns the per-user bucket key for runtime state (favourites,
// protected paths, etc.). When auth is disabled the single global bucket ("")
// is used.
func (h *Handler) requestKey(r *http.Request) string {
	if e := h.userEntryFor(r); e != nil {
		return e.user.Username
	}
	return ""
}

// runtimeList reads the per-user entry from a shared runtime map under its
// mutex. It backs favouritesForRequest and protectedPathsForRequest, which
// otherwise duplicate the lock/read/unlock dance.
func runtimeList[T any](mu *sync.RWMutex, m map[string]T, key string) T {
	mu.RLock()
	v := m[key]
	mu.RUnlock()
	return v
}

// favouritesForRequest returns the favourites for the current user.
func (h *Handler) favouritesForRequest(r *http.Request) []config.Favourite {
	return runtimeList(&h.favMu, h.runtimeFavs, h.requestKey(r))
}

// saveConfigFile persists a config.Config to path, treating a not-writable file
// as a session-only change and reverting nothing. No-op when path is empty.
func saveConfigFile(path string, cfg *config.Config) error {
	if path == "" {
		return nil
	}
	err := cfg.Save(path)
	if errors.Is(err, config.ErrConfigNotWritable) {
		log.Printf("WARNING: cannot persist to %s: %v (in-memory changes kept for this session)", path, err)
		return nil
	}
	return err
}

// persistFavourites writes the favourites for key. The no-auth bucket "" is
// persisted into config.yaml; per-user favourites belong to the user's own
// preferences file under their home directory (see prefs.PathForUser), so a
// read-only config stays untouched.
func (h *Handler) persistFavourites(key string, favs []config.Favourite) error {
	if key == "" {
		h.cfgMu.Lock()
		defer h.cfgMu.Unlock()
		h.cfg.Favourites = favs
		return saveConfigFile(h.configPath, h.cfg)
	}
	h.prefsMu.Lock()
	defer h.prefsMu.Unlock()
	f := h.prefsFileFor(key)
	h.reloadPrefsIfChanged(f)
	f.p.UpsertUser(key).Favourites = favs
	return h.savePrefs(f)
}

// persistProtectedPaths writes the protected paths for key. Like favourites,
// the "" bucket goes to config.yaml while per-user entries go to the user's
// own preferences file.
func (h *Handler) persistProtectedPaths(key string, paths []string) error {
	if key == "" {
		h.cfgMu.Lock()
		defer h.cfgMu.Unlock()
		h.cfg.ProtectedPaths = paths
		return saveConfigFile(h.configPath, h.cfg)
	}
	h.prefsMu.Lock()
	defer h.prefsMu.Unlock()
	f := h.prefsFileFor(key)
	h.reloadPrefsIfChanged(f)
	f.p.UpsertUser(key).ProtectedPaths = paths
	return h.savePrefs(f)
}

// prefsForRequest returns the resolved secure-delete preferences for the
// current request (per-user entry in auth mode, global entry otherwise).
func (h *Handler) prefsForRequest(r *http.Request) prefs.Deletion {
	return runtimeList(&h.deletePrefsMu, h.runtimeDelete, h.requestKey(r))
}

// persistDeletePrefs writes the secure-delete preferences for key into that
// user's own preferences file. Both the no-auth ("") and per-user buckets live
// in the preferences files: the wipe default is a user preference, not a server
// setting.
func (h *Handler) persistDeletePrefs(key string, d prefs.Deletion) error {
	h.prefsMu.Lock()
	defer h.prefsMu.Unlock()
	f := h.prefsFileFor(key)
	h.reloadPrefsIfChanged(f)
	if key == "" {
		f.p.Deletion = d
	} else {
		f.p.UpsertUser(key).Deletion = d
	}
	return h.savePrefs(f)
}

// persistShowDotfiles writes the show-dotfiles preference for key. The no-auth
// ("") bucket updates the server default in config.yaml; per-user settings go
// to the user's own preferences file so a read-only config stays untouched.
func (h *Handler) persistShowDotfiles(key string, show bool) error {
	if key == "" {
		h.cfgMu.Lock()
		defer h.cfgMu.Unlock()
		h.cfg.ShowDotfiles = show
		return saveConfigFile(h.configPath, h.cfg)
	}
	h.prefsMu.Lock()
	defer h.prefsMu.Unlock()
	f := h.prefsFileFor(key)
	h.reloadPrefsIfChanged(f)
	val := show
	f.p.UpsertUser(key).ShowDotfiles = &val
	return h.savePrefs(f)
}

// prefsFileFor returns the preferences file tracked for key (a username, or ""
// for the no-auth process-user file), creating an in-memory-only entry on first
// access when no path was configured. Callers must hold prefsMu.
func (h *Handler) prefsFileFor(key string) *prefsFile {
	if f, ok := h.prefsFiles[key]; ok {
		return f
	}
	f := &prefsFile{p: &prefs.Preferences{}, uid: -1, gid: -1}
	h.prefsFiles[key] = f
	return f
}

// savePrefs persists the preferences file f, tolerant of a not-writable home
// dir (same session-only semantics as an unwritable config file). A caller
// must hold prefsMu.
func (h *Handler) savePrefs(f *prefsFile) error {
	if f.path == "" {
		return nil
	}
	err := f.p.SaveOwned(f.path, f.uid, f.gid)
	if errors.Is(err, os.ErrPermission) {
		log.Printf("WARNING: cannot persist preferences to %s: %v (in-memory changes kept for this session)", f.path, err)
		return nil
	}
	// Refresh the on-disk snapshot so a later reload does not mistake the
	// server's own write for an external edit.
	if info, statErr := os.Stat(f.path); statErr == nil {
		f.size = info.Size()
		f.mod = info.ModTime()
	}
	return err
}

// reloadPrefsIfChanged re-reads the preferences file f from disk when it has
// been modified outside the server (e.g. by the filex-passwd tool setting a
// password hash, or by the operator editing it directly). The server persists
// every change it makes, so an unchanged mtime/size means disk and memory are
// in sync; only an external write triggers a reload. This keeps a long-running
// server able to pick up a newly set password without a restart.
//
// Callers must hold prefsMu. The password hash is the only field filex-passwd
// edits, so the separate runtime maps (favourites, protected paths, delete
// prefs) do not need refreshing; they are kept in sync by the API handlers.
func (h *Handler) reloadPrefsIfChanged(f *prefsFile) {
	if f.path == "" {
		return
	}
	info, err := os.Stat(f.path)
	if err != nil {
		// File missing or unreadable: leave the in-memory copy untouched.
		return
	}
	if info.Size() == f.size && info.ModTime().Equal(f.mod) {
		return
	}
	p, err := prefs.Load(f.path)
	if err != nil {
		log.Printf("WARNING: cannot reload preferences from %s: %v (keeping in-memory copy)", f.path, err)
		return
	}
	f.p = p
	f.size = info.Size()
	f.mod = info.ModTime()
}

// persistRuntimeMutation is a generic helper behind the shared pattern of the
// favourites/protected add/remove/update handlers: lock the runtime map, apply
// a mutation under the lock, persist a copy, and revert on persistence failure.
// If mutate reports early=true the operation is a successful no-op (nothing is
// persisted). On success (or no-op) it writes {"status":"ok"}; on persistence
// failure it reverts the change and writes a 500 error response.
func persistRuntimeMutation[T any](
	mu *sync.RWMutex,
	runtime map[string]T,
	key string,
	mutate func(current T) (next T, early bool),
	copyValue func(T) T,
	persist func(T) error,
	w http.ResponseWriter,
) {
	mu.Lock()
	old := runtime[key]
	next, early := mutate(old)
	if early {
		mu.Unlock()
		writeOK(w)
		return
	}
	runtime[key] = next
	saved := copyValue(next)
	mu.Unlock()
	if err := persist(saved); err != nil {
		mu.Lock()
		runtime[key] = old
		mu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w)
}

// updateFavouriteEntry uses persistRuntimeMutation with the favourites runtime map.
func (h *Handler) updateFavouriteEntry(key string, w http.ResponseWriter, mutate func(current []config.Favourite) ([]config.Favourite, bool)) {
	persistRuntimeMutation(&h.favMu, h.runtimeFavs, key, mutate,
		func(v []config.Favourite) []config.Favourite { return slices.Clone(v) },
		func(v []config.Favourite) error { return h.persistFavourites(key, v) },
		w)
}

// updateProtectedEntry uses persistRuntimeMutation with the protected paths runtime map.
func (h *Handler) updateProtectedEntry(key string, w http.ResponseWriter, mutate func(current []string) ([]string, bool)) {
	persistRuntimeMutation(&h.protectedMu, h.runtimeProtected, key, mutate,
		func(v []string) []string { return slices.Clone(v) },
		func(v []string) error { return h.persistProtectedPaths(key, v) },
		w)
}

// addUnique appends item to list when match finds no equal element already
// present. Returns early=true (a no-op the caller should not persist) when the
// item is a duplicate.
func addUnique[T any](list []T, item T, match func(T) bool) ([]T, bool) {
	for _, v := range list {
		if match(v) {
			return nil, true
		}
	}
	return append(list, item), false
}

// keepOnly returns the subset of list for which keep reports true.
func keepOnly[T any](list []T, keep func(T) bool) []T {
	out := make([]T, 0, len(list))
	for _, v := range list {
		if keep(v) {
			out = append(out, v)
		}
	}
	return out
}

// showDotfilesForRequest resolves whether to show dot files for this request.
// Precedence: an explicit `dotfiles` query param, then the per-user preference
// (stored in ~/.config/filex.yaml), then the global config.yaml default.
func (h *Handler) showDotfilesForRequest(r *http.Request) bool {
	q := r.URL.Query().Get("dotfiles")
	if q == "true" {
		return true
	}
	if q == "false" {
		return false
	}
	key := h.requestKey(r)
	if key != "" {
		h.prefsMu.Lock()
		f := h.prefsFileFor(key)
		h.reloadPrefsIfChanged(f)
		if pu := f.p.FindUser(key); pu != nil && pu.ShowDotfiles != nil {
			v := *pu.ShowDotfiles
			h.prefsMu.Unlock()
			return v
		}
		h.prefsMu.Unlock()
	}
	return h.cfg.ShowDotfiles
}

func normalizeVirtualPath(p string) string {
	return pathpkg.Clean("/" + p)
}

// pathMatchesOrIsWithin reports whether targetPath is protectedPath itself or a
// descendant of it. Both inputs must already be normalized (see
// normalizeVirtualPath); a protected "/" disables deletion for the whole tree.
func pathMatchesOrIsWithin(targetPath, protectedPath string) bool {
	if targetPath == protectedPath {
		return true
	}
	if protectedPath == "/" {
		return true
	}
	return strings.HasPrefix(targetPath, protectedPath+"/")
}

func (h *Handler) protectedPathsForRequest(r *http.Request) []string {
	return runtimeList(&h.protectedMu, h.runtimeProtected, h.requestKey(r))
}

func (h *Handler) protectedDeleteTarget(r *http.Request, targets []string) string {
	protectedPaths := h.protectedPathsForRequest(r)
	if len(protectedPaths) == 0 {
		return ""
	}
	fs := h.fsForRequest(r)
	// Drop stale entries up front: an entry that no longer exists cannot be
	// destroyed by a delete and must not keep blocking deletion of its
	// ancestors, which is what left folders looking unprotected (open-lock
	// icon) yet refusing to be deleted until the folder was renamed. This also
	// runs each existence check once instead of once per protected-path/target
	// pair.
	live := make([]string, 0, len(protectedPaths))
	for _, protectedPath := range protectedPaths {
		np := normalizeVirtualPath(protectedPath)
		if _, err := fs.FileInfo(np); err != nil {
			continue
		}
		live = append(live, np)
	}
	for _, target := range targets {
		targetPath := normalizeVirtualPath(target)
		for _, np := range live {
			if pathMatchesOrIsWithin(targetPath, np) ||
				targetContainsProtected(targetPath, np) {
				return targetPath
			}
		}
	}
	return ""
}

// targetContainsProtected reports whether deleting target would recursively
// destroy protectedPath because it lives beneath target. Both inputs must
// already be normalized (see normalizeVirtualPath).
func targetContainsProtected(targetPath, protectedPath string) bool {
	return targetPath == "/" || strings.HasPrefix(protectedPath, targetPath+"/")
}

// relocateProtectedPaths rewrites protected-path entries after a path is
// renamed or moved from oldRoot to newRoot so delete protection follows the
// relocated file or directory: entries equal to oldRoot and entries beneath it
// are remapped to their new location. Without this a protected folder silently
// lost its protection on rename/move while its stale old entry kept blocking
// deletion of its ancestors ("unprotected folder that will not delete").
func (h *Handler) relocateProtectedPaths(r *http.Request, oldRoot, newRoot string) {
	key := h.requestKey(r)
	oldRoot = normalizeVirtualPath(oldRoot)
	newRoot = normalizeVirtualPath(newRoot)
	if oldRoot == newRoot {
		return
	}

	reloc := func(p string) string {
		pNorm := normalizeVirtualPath(p)
		switch {
		case pNorm == oldRoot:
			return newRoot
		case strings.HasPrefix(pNorm, oldRoot+"/"):
			return newRoot + pNorm[len(oldRoot):]
		default:
			return p
		}
	}

	h.protectedMu.Lock()
	cur := h.runtimeProtected[key]
	next := make([]string, 0, len(cur))
	changed := false
	for _, p := range cur {
		n := reloc(p)
		if n != p {
			changed = true
		}
		next = append(next, n)
	}
	if !changed {
		h.protectedMu.Unlock()
		return
	}
	h.runtimeProtected[key] = next
	saved := slices.Clone(next)
	h.protectedMu.Unlock()

	// Persisting a copy externally never wrote back (persistProtectedPaths
	// treats the not-writable case as a session-only change and returns nil),
	// so only a genuine persistence failure reverts the in-memory relocation.
	if err := h.persistProtectedPaths(key, saved); err != nil {
		log.Printf("relocateProtectedPaths: revert %v -> %v: %v", oldRoot, newRoot, err)
		h.protectedMu.Lock()
		h.runtimeProtected[key] = cur
		h.protectedMu.Unlock()
	}
}

func serveHTMLFile(w http.ResponseWriter, r *http.Request, staticFS http.FileSystem, name string) {
	f, err := staticFS.Open("/" + name)
	if err != nil {
		http.Error(w, name+" not found", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		http.Error(w, "stat error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, name, stat.ModTime(), f.(io.ReadSeeker))
}

// ---- helpers ----------------------------------------------------------------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON encode error: %v", err)
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	// Content-Type must be set before WriteHeader: WriteHeader snapshots the
	// header map, so a header set afterwards would never reach the wire and
	// every error would go out sniffed (usually text/plain).
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

var okStatus = map[string]string{"status": "ok"}

// writeOK writes the shared {"status":"ok"} success response. The map is
// shared because json.Encoder never mutates its input.
func writeOK(w http.ResponseWriter) {
	writeJSON(w, okStatus)
}

// conflictPathFromErr extracts the jailed destination path embedded in an
// ErrDestinationExists error (formatted as "destination already exists: /path")
// so single-item move/copy reports the same conflicting path that batch
// operations resolve to, rather than the source path.
func conflictPathFromErr(err error) string {
	return strings.TrimPrefix(err.Error(), fslib.ErrDestinationExists.Error()+": ")
}

// startNDJSON initializes a streaming NDJSON response. It returns the flusher
// and false when the response writer does not support streaming (after writing
// a single error line so the client sees a well-formed NDJSON stream).
func startNDJSON(w http.ResponseWriter) (http.Flusher, bool) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher, ok := w.(http.Flusher)
	if !ok {
		w.Write([]byte(`{"type":"error","message":"streaming not supported"}` + "\n"))
		return nil, false
	}
	return flusher, true
}

// ndjsonWriter initializes an NDJSON stream and returns a send function that
// encodes v as a JSON line, flushes it, and reports whether it succeeded. When
// the request context is cancelled or encoding fails, send returns false.
func ndjsonWriter(w http.ResponseWriter, r *http.Request) (func(v any) bool, bool) {
	flusher, ok := startNDJSON(w)
	if !ok {
		return nil, false
	}
	return func(v any) bool {
		if r.Context().Err() != nil {
			return false
		}
		if err := json.NewEncoder(w).Encode(v); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}, true
}

// ndjsonError streams a plain error line on an NDJSON response and reports
// whether the flush succeeded (false = client gone).
func ndjsonError(flush func(v any) bool, message string) bool {
	return flush(map[string]any{"type": "error", "message": message})
}

// progressReporter wraps an NDJSON sender into the standard transfer-progress
// shape (done/total/current/filePct) used by wipe, move and copy streams.
func progressReporter(flushJSON func(v any) bool) func(done, total int, name string, filePct *float64) {
	return func(done, total int, name string, filePct *float64) {
		flushJSON(map[string]any{
			"type":    "progress",
			"done":    done,
			"total":   total,
			"current": name,
			"filePct": filePct,
		})
	}
}

// queryPath returns the "path" query parameter, or def when it is empty.
func queryPath(r *http.Request, def string) string {
	p := r.URL.Query().Get("path")
	if p == "" {
		return def
	}
	return p
}

// requireQueryPath returns the "path" query parameter, writing a 400 error and
// returning "" when it is missing.
func requireQueryPath(w http.ResponseWriter, r *http.Request) string {
	p := r.URL.Query().Get("path")
	if p == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return ""
	}
	return p
}

// requirePathField writes a 400 "path required" error and returns false when a
// decoded request-path field is empty or whitespace. Every JSON handler that
// requires a path field uses this so error responses stay identical.
func requirePathField(w http.ResponseWriter, path string) bool {
	if strings.TrimSpace(path) == "" {
		writeError(w, http.StatusBadRequest, "path required")
		return false
	}
	return true
}

// contentDispositionAttachment returns a safe Content-Disposition header value
// for the given filename, escaping quotes and stripping control characters that
// could otherwise inject or corrupt HTTP headers.
func contentDispositionAttachment(filename string) string {
	d := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if d != "" {
		return d
	}
	// FormatMediaType returns "" if the filename renders an empty value; fall
	// back to a bare attachment disposition.
	return "attachment"
}

// openAndServeFile opens a jailed path and serves its content, optionally with
// a Content-Disposition: attachment header for download semantics.
func openAndServeFile(w http.ResponseWriter, r *http.Request, fs *fslib.FS, filePath string, asDownload bool) {
	f, err := fs.OpenForDownload(filePath)
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
	if stat.IsDir() {
		writeError(w, http.StatusNotFound, "path is a directory")
		return
	}
	if asDownload {
		w.Header().Set("Content-Disposition", contentDispositionAttachment(filepath.Base(stat.Name())))
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	}
	http.ServeContent(w, r, stat.Name(), stat.ModTime(), f)
}

// hashHexPath returns the SHA-256 hex hash of an absolute path, used as the
// cache key for thumbnails and intervals.
func hashHexPath(absPath string) string {
	h := sha256.Sum256([]byte(absPath))
	return hex.EncodeToString(h[:])
}

// resolveVideoPathHash resolves a virtual path to an absolute host path and
// returns it along with the SHA-256 hex hash of the absolute path.
func resolveVideoPathHash(fs *fslib.FS, virtualPath string) (absPath, hashHex string, err error) {
	absPath, err = fs.ResolvePath(virtualPath)
	if err != nil {
		return "", "", err
	}
	return absPath, hashHexPath(absPath), nil
}

// resolveVideoPathOrErr resolves a virtual path to an absolute path with its
// hash, writing a 404 error and returning ok=false when the path is invalid
// (shared preamble of every video handler).
func resolveVideoPathOrErr(w http.ResponseWriter, fs *fslib.FS, virtualPath string) (absPath, hashHex string, ok bool) {
	absPath, hashHex, err := resolveVideoPathHash(fs, virtualPath)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return "", "", false
	}
	return absPath, hashHex, true
}

// thumbDirForVideo resolves a video path to its thumbnail cache directory,
// writing a 404 and returning ok=false when the path is invalid.
func thumbDirForVideo(w http.ResponseWriter, fs *fslib.FS, videoPath string) (absPath, hashHex, tDir string, ok bool) {
	absPath, hashHex, ok = resolveVideoPathOrErr(w, fs, videoPath)
	if !ok {
		return "", "", "", false
	}
	return absPath, hashHex, thumbDir(absPath, hashHex), true
}

// thumbPathForRequest resolves the decoded request path to its thumbnail cache
// directory under the request's per-user FS, writing error responses and
// returning ok=false on failure. It collapses the shared preamble of the
// thumbnail DELETE/cancel handlers.
func (h *Handler) thumbPathForRequest(w http.ResponseWriter, r *http.Request) (fs *fslib.FS, absPath, hashHex, tDir string, ok bool) {
	videoPath, ok := decodePathRequest(w, r)
	if !ok {
		return nil, "", "", "", false
	}
	fs = h.fsForRequest(r)
	absPath, hashHex, tDir, ok = thumbDirForVideo(w, fs, videoPath)
	if !ok {
		return nil, "", "", "", false
	}
	return fs, absPath, hashHex, tDir, true
}

// intervalsFilesForVideo resolves a video path to its intervals cache dir/file,
// writing a 404 and returning ok=false when the path is invalid.
func intervalsFilesForVideo(w http.ResponseWriter, fs *fslib.FS, videoPath string) (absPath, iDir, iFile string, ok bool) {
	absPath, hashHex, ok := resolveVideoPathOrErr(w, fs, videoPath)
	if !ok {
		return "", "", "", false
	}
	return absPath, intervalsDir(absPath), intervalsFile(absPath, hashHex), true
}

// bodyErrStatus reports the HTTP status appropriate for a request-body
// reading error: bodies exceeding the configured limit are 413, everything
// else is a plain 400 malformed-body error.
func bodyErrStatus(err error) int {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

// decodeBody decodes a JSON request body into v, writing an error response and
// returning the error on failure. It is the shared preamble of every handler
// that accepts a JSON payload.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		writeError(w, bodyErrStatus(err), "invalid request body: "+err.Error())
		return err
	}
	// Reject bodies that carry data after the JSON value (e.g. "json garbage"
	// or a second JSON document): decoding silently stops at the first value,
	// so without this check malformed requests would go through as valid.
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid request body: unexpected trailing data")
		return fmt.Errorf("trailing data after request body: %w", err)
	}
	return nil
}

// decodePostBody is the shared preamble of every JSON-POST handler: it enforces
// the POST method and decodes the bounded body into v, writing the appropriate
// error response on failure. Handlers that need a pre-decode step (e.g. the
// upload body bound) keep their own requirePost call.
func decodePostBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if !requirePost(w, r) {
		return false
	}
	if err := decodeBody(w, r, v); err != nil {
		return false
	}
	return true
}

// decodePathRequest decodes a JSON request body expecting a single "path" field.
// Returns the path and true on success. On failure it writes the error response
// and returns ("", false).
func decodePathRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		return "", false
	}
	if !requirePathField(w, req.Path) {
		return "", false
	}
	return req.Path, true
}

// decodeNameRequest is the "name"-keyed form of decodeNameRequestBody, used
// by mkdir and create-file which reuse the shared decoder for callers of the
// original name.
func decodeNameRequest(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	return decodeNameRequestBody(w, r, "name")
}

// decodeNameRequestBody decodes a JSON body expecting a "path" plus a name
// field keyed by nameKey ("name" for mkdir/create-file, "newname" for rename),
// validating both are present and the name is free of path separators. All
// three handlers go through this one decoder rather than duplicating the body
// shape and validation.
func decodeNameRequestBody(w http.ResponseWriter, r *http.Request, nameKey string) (string, string, bool) {
	var body map[string]json.RawMessage
	if err := decodeBody(w, r, &body); err != nil {
		return "", "", false
	}
	path, ok := decodeJSONStringField(body, "path")
	if !ok {
		writeError(w, http.StatusBadRequest, "path required")
		return "", "", false
	}
	name, ok := decodeJSONStringField(body, nameKey)
	if !ok {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%s required", nameKey))
		return "", "", false
	}
	if !validateName(w, name) {
		return "", "", false
	}
	return path, name, true
}

// decodeJSONStringField extracts a JSON string field from a raw body map.
// It fails when the key is missing, is not a string, or is empty.
func decodeJSONStringField(body map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := body[key]
	if !ok {
		return "", false
	}
	var v string
	if err := json.Unmarshal(raw, &v); err != nil || v == "" {
		return "", false
	}
	return v, true
}

// validateName checks that name is non-empty and free of path separators,
// writing a 400 error and returning false when invalid.
func validateName(w http.ResponseWriter, name string) bool {
	if name == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return false
	}
	if strings.ContainsAny(name, `/\`) {
		writeError(w, http.StatusBadRequest, "name must not contain path separators")
		return false
	}
	return true
}

// isSecureRequest reports whether the connection to the client is HTTPS.
func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// cacheRoot returns the temporary cache base directory for a given absolute
// video path, used for both thumbnails and intervals.
func cacheRoot(absPath string) string {
	return filepath.Join(filepath.Dir(absPath), "tmp")
}

// thumbDir returns the thumbnail cache directory for a given absolute video path.
func thumbDir(absPath, hashHex string) string {
	return filepath.Join(cacheRoot(absPath), "filex_thumbs", hashHex)
}

// intervalsFile returns the intervals JSON file path for a given absolute video path.
func intervalsFile(absPath, hashHex string) string {
	return filepath.Join(cacheRoot(absPath), "filex_intervals", hashHex+".json")
}

// intervalsDir returns the intervals directory for a given absolute video path.
func intervalsDir(absPath string) string {
	return filepath.Join(cacheRoot(absPath), "filex_intervals")
}

// ffmpegStreamCopyArgs returns the stream-copy output flags shared by the
// keyframe/segment extraction commands: copy without re-encoding, map the first
// stream, and move the moov atom to the front so the output can be
// streamed/seeked immediately.
func ffmpegStreamCopyArgs() []string {
	return []string{"-c", "copy", "-map", "0", "-movflags", "+faststart"}
}

// ffmpegExtractCmd builds an ffmpeg command that extracts a segment from absPath
// writing to outputPath, using stream copy with faststart. The process is bound
// to ctx so it is killed when the client disconnects mid-extraction.
func ffmpegExtractCmd(ctx context.Context, absPath, outputPath, startStr, durStr string) *exec.Cmd {
	args := []string{
		"-y",
		"-ss", startStr,
		"-i", absPath,
		"-t", durStr,
	}
	args = append(args, ffmpegStreamCopyArgs()...)
	args = append(args, outputPath)
	return exec.CommandContext(ctx, "ffmpeg", args...)
}

// requireMethod checks that the request method matches expected; on mismatch
// it writes a 405 error and returns false.
func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, method+" required")
		return false
	}
	return true
}

func requireGet(w http.ResponseWriter, r *http.Request) bool {
	// HEAD is served by every GET route: proxies, health checks and `curl -I`
	// expect it to exist and net/http already discards the response body for it.
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, http.MethodGet+" required")
		return false
	}
	return true
}

func requirePost(w http.ResponseWriter, r *http.Request) bool {
	return requireMethod(w, r, http.MethodPost)
}

// requireTool writes a 500 error and returns false when the named binary is
// unavailable.
func (h *Handler) requireTool(w http.ResponseWriter, name string) bool {
	if h.tools[name] {
		return true
	}
	writeError(w, http.StatusInternalServerError, name+" not available")
	return false
}

// requireFFmpeg writes a 500 error and returns false when ffmpeg is unavailable.
func (h *Handler) requireFFmpeg(w http.ResponseWriter) bool {
	return h.requireTool(w, "ffmpeg")
}

// requireFFprobe writes a 500 error and returns false when ffprobe is unavailable.
func (h *Handler) requireFFprobe(w http.ResponseWriter) bool {
	return h.requireTool(w, "ffprobe")
}

// ---- Version handler --------------------------------------------------------

func (h *Handler) handleVersion(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	writeJSON(w, map[string]string{"version": h.version})
}

// ---- Auth handlers ----------------------------------------------------------

// dummyBcryptEqual compares password against a once-generated dummy bcrypt
// hash. It exists so handleLogin can burn the same CPU cost for unknown
// usernames as it does for real ones, hiding which usernames exist behind the
// timing of a bcrypt comparison. Cost matches filex-passwd's DefaultCost.
// dummyHash is a once-generated bcrypt hash used to equalise login timing for
// unknown usernames (see dummyBcryptEqual).
var dummyHash = sync.OnceValues(func() ([]byte, error) {
	// Cost 12 matches filex-passwd's bcryptCost: hashing the equalizer with the
	// weaker default cost (10) made unknown-usernames compare ~4x faster than
	// real ones, defeating the timing-equaliser the comparison exists for.
	return bcrypt.GenerateFromPassword([]byte("filex-timing-equalizer"), bcryptEqualizerCost)
})

// bcryptEqualizerCost is the work factor used for the unknown-username timing
// equalizer, kept in sync with filex-passwd's hash cost.
const bcryptEqualizerCost = 12

func dummyBcryptEqual(password string) error {
	hash, err := dummyHash()
	if err != nil {
		return err
	}
	return bcrypt.CompareHashAndPassword(hash, []byte(password))
}

// passwordHashFor returns the bcrypt password hash stored for username in that
// user's own preferences file (under their home directory). Hashes are never
// held in config.yaml; filex-passwd writes them into <home>/.config/filex.yaml.
func (h *Handler) passwordHashFor(username string) string {
	h.prefsMu.Lock()
	defer h.prefsMu.Unlock()
	f := h.prefsFileFor(username)
	h.reloadPrefsIfChanged(f)
	u := f.p.FindUser(username)
	if u == nil {
		return ""
	}
	return u.PasswordHash
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
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
	if err := decodeBody(w, r, &req); err != nil {
		return
	}

	u := h.cfg.FindUser(req.Username)
	// Reject login when the user's jail failed to build (e.g. base_path missing
	// or not a directory). Otherwise fsForRequest would silently fall back to
	// the global file tree, escalating privileges beyond the user's jail.
	if u == nil || h.users[req.Username] == nil {
		// Run a real bcrypt comparison anyway so unknown usernames take the
		// same amount of time as known ones, making username enumeration via
		// response timing impractical.
		_ = dummyBcryptEqual(req.Password)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// The password hash lives in the preferences file (~/.config/filex.yaml),
	// not in config.yaml. A missing/empty hash means the admin has not set a
	// password for this user yet: burn the equalizer comparison and fail.
	hash := h.passwordHashFor(u.Username)
	if hash == "" {
		_ = dummyBcryptEqual(req.Password)
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	sessionTTL := h.cfg.SessionTTL()

	// A persistent (disk-backed remember-me) session fans out to a persistent
	// cookie of the full TTL; a plain session fans out to a session cookie. The
	// flag maps to the matching session creation here so the token and the
	// cookie type stay in sync in exactly one place.
	var token string
	var err error
	if req.RememberMe {
		token, err = h.authStore.CreatePersistentWithTTL(req.Username, sessionTTL)
	} else {
		token, err = h.authStore.CreateWithTTL(req.Username, sessionTTL)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}

	isSecure := isSecureRequest(r)
	if req.RememberMe {
		// Persistent cookie: survives browser restarts for the full session TTL.
		authlib.SetCookieWithTTL(w, token, sessionTTL, isSecure)
	} else {
		// Session cookie: browser discards it when the window/tab is closed.
		authlib.SetSessionCookie(w, token, isSecure)
	}
	writeJSON(w, map[string]string{"status": "ok", "username": req.Username})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	// authStore is nil when authentication is disabled.
	if h.authStore != nil {
		if sess := h.authStore.FromRequest(r); sess != nil {
			h.authStore.Delete(sess.Token)
		}
	}
	isSecure := isSecureRequest(r)
	authlib.ClearCookie(w, isSecure)
	writeOK(w)
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	if !h.cfg.AuthRequired() {
		writeJSON(w, map[string]any{"username": "anonymous", "auth_required": false})
		return
	}
	if e := h.userEntryFor(r); e != nil {
		writeJSON(w, map[string]any{"username": e.user.Username, "auth_required": true})
		return
	}
	writeError(w, http.StatusUnauthorized, "not authenticated")
}

// handleChangePassword lets the authenticated user replace their own login
// password. The current password must be supplied and verified first so a
// stolen session alone cannot silently rotate credentials. The new hash is
// stored in the preferences file (~/.config/filex.yaml) via the same path the
// filex-passwd tool writes, so a later login uses the new password. The
// current session (and any other sessions for this user) stay valid.
func (h *Handler) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		return
	}
	e := h.userEntryFor(r)
	if e == nil {
		// In no-auth mode there is no identity to change a password for.
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}

	hash := h.passwordHashFor(e.user.Username)
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.CurrentPassword)); err != nil {
		writeError(w, http.StatusBadRequest, "current password is incorrect")
		return
	}

	if req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "new password must not be empty")
		return
	}
	if len(req.NewPassword) > 72 {
		// bcrypt silently truncates beyond 72 bytes; reject explicitly so the
		// user knows the stored password would not be what they typed.
		writeError(w, http.StatusBadRequest, "new password is too long (max 72 bytes)")
		return
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcryptEqualizerCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash new password")
		return
	}

	h.prefsMu.Lock()
	f := h.prefsFileFor(e.user.Username)
	h.reloadPrefsIfChanged(f)
	f.p.UpsertUser(e.user.Username).PasswordHash = string(newHash)
	saveErr := h.savePrefs(f)
	h.prefsMu.Unlock()
	if saveErr != nil {
		writeError(w, http.StatusInternalServerError, "could not persist password change")
		return
	}
	writeJSON(w, map[string]string{"status": "ok"})
}

// ---- API handlers -----------------------------------------------------------

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	entries, err := h.fsForRequest(r).ListDir(queryPath(r, "/"), h.showDotfilesForRequest(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, entries)
}

// handleDownload serves a file as a download attachment.
func (h *Handler) handleDownload(w http.ResponseWriter, r *http.Request) {
	h.handleFileServe(w, r, true)
}

// handleStream serves a file for inline browser playback (e.g. video/audio).
// It does not set Content-Disposition: attachment, so the browser can render
// the file directly. http.ServeContent handles range requests, ETags, and
// Last-Modified automatically, enabling efficient seeking in large media files.
func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	h.handleFileServe(w, r, false)
}

// handleFileServe serves the file at the query "path", optionally as an
// attachment download (asDownload=true).
func (h *Handler) handleFileServe(w http.ResponseWriter, r *http.Request, asDownload bool) {
	if !requireGet(w, r) {
		return
	}
	filePath := requireQueryPath(w, r)
	if filePath == "" {
		return
	}
	openAndServeFile(w, r, h.fsForRequest(r), filePath, asDownload)
}

// errUploadPartOpen wraps an error that escaped a multipart file part open,
// so handlers can distinguish an open failure (server-side, 500) from a
// save failure (bad request, 400) after saveMultipartPart collapses both.
var errUploadPartOpen = errors.New("upload part open failed")

// saveMultipartPart opens one multipart file part, runs save with its reader,
// and guarantees the part handle is closed on every path (including panics).
func saveMultipartPart(fh *multipart.FileHeader, save func(io.Reader) error) error {
	f, err := fh.Open()
	if err != nil {
		return fmt.Errorf("%w: %v", errUploadPartOpen, err)
	}
	defer f.Close()
	return save(f)
}

// writeUploadError maps a saveMultipartPart error to the client: an open
// failure or a server-side permission problem is 500 (the server could not
// perform the write under the user's credentials), anything else is a bad
// request (400).
func writeUploadError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, errUploadPartOpen) || errors.Is(err, os.ErrPermission) {
		status = http.StatusInternalServerError
	}
	writeError(w, status, err.Error())
}

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadRequestBody)
	dirPath := queryPath(r, "/")
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, bodyErrStatus(err), err.Error())
		return
	}
	// ParseMultipartForm buffers part data above 64 MiB to temp files on disk;
	// RemoveAll releases them even when an early return (validation errors,
	// a rejected chunk, an aborted write) skips the normal upload flow.
	defer r.MultipartForm.RemoveAll()
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "no file provided")
		return
	}
	fs := h.fsForRequest(r)

	// Chunked upload: an optional offset query parameter selects this mode.
	// Each request carries one chunk of a file, written at the given byte
	// offset, so a single file can be split across concurrent sessions.
	offsetStr := r.URL.Query().Get("offset")
	if offsetStr != "" {
		if len(files) != 1 {
			writeError(w, http.StatusBadRequest, "chunked upload expects exactly one file part")
			return
		}
		offset, err := strconv.ParseInt(offsetStr, 10, 64)
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		var total int64
		ts := r.URL.Query().Get("total")
		if ts == "" {
			writeError(w, http.StatusBadRequest, "chunked upload requires total")
			return
		}
		total, err = strconv.ParseInt(ts, 10, 64)
		if err != nil || total <= 0 {
			writeError(w, http.StatusBadRequest, "invalid total")
			return
		}
		// The declared total is client-supplied; cap it at the same 16 GiB bound
		// as a whole-file upload so "total" cannot be abused to enlarge a file
		// (offset 0 + truncate-to-total) past the intended limit.
		if total > maxUploadRequestBody {
			writeError(w, http.StatusBadRequest, "total exceeds maximum upload size")
			return
		}
		if offset > total {
			writeError(w, http.StatusBadRequest, "offset exceeds total")
			return
		}
		fh := files[0]
		// Reject chunks that would extend the file past the declared total so
		// a malformed client cannot create a file larger than advertised.
		if offset+fh.Size > total {
			writeError(w, http.StatusBadRequest, "chunk exceeds total file size")
			return
		}
		err = saveMultipartPart(fh, func(src io.Reader) error {
			return fs.SaveUploadChunk(dirPath, fh.Filename, src, offset, total)
		})
		if err != nil {
			writeUploadError(w, err)
			return
		}
		writeOK(w)
		return
	}

	for _, fh := range files {
		err := saveMultipartPart(fh, func(src io.Reader) error {
			return fs.SaveUpload(dirPath, fh.Filename, src)
		})
		if err != nil {
			writeUploadError(w, err)
			return
		}
	}
	writeOK(w)
}

func (h *Handler) handleMkdir(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	path, name, ok := decodeNameRequest(w, r)
	if !ok {
		return
	}
	if err := h.fsForRequest(r).MkDir(path, name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w)
}

func (h *Handler) handleCreateFile(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	path, name, ok := decodeNameRequest(w, r)
	if !ok {
		return
	}
	filePath, err := h.fsForRequest(r).CreateFile(path, name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]string{"status": "ok", "path": filePath})
}

func (h *Handler) handleRename(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	path, newName, ok := decodeNameRequestBody(w, r, "newname")
	if !ok {
		return
	}
	if err := h.fsForRequest(r).Rename(path, newName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Carry delete protection over to the new name so a protected folder does
	// not silently lose its lock (and leave a stale entry behind) on rename.
	newPath := normalizeVirtualPath(pathpkg.Dir(path) + "/" + newName)
	h.relocateProtectedPaths(r, path, newPath)
	writeOK(w)
}

// wipeMethodsJSON builds the selectable secure-delete algorithms as plain maps
// ready for JSON encoding. The catalog is static for the process lifetime, and
// /api/config serves it on every page load, so it is computed exactly once.
var wipeMethodsJSON = sync.OnceValue(func() []map[string]string {
	out := make([]map[string]string, 0, len(fslib.WipeMethods))
	for _, m := range fslib.WipeMethods {
		out = append(out, map[string]string{
			"id":          m.ID,
			"name":        m.Name,
			"description": m.Description,
		})
	}
	return out
})

func (h *Handler) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path   string   `json:"path"`
		Paths  []string `json:"paths"`
		Wipe   bool     `json:"wipe"`
		Method string   `json:"method"` // secure-delete algorithm, one of fs.WipeMethods
	}
	if !decodePostBody(w, r, &req) {
		return
	}
	targets, ok := singleOrList(w, req.Path, req.Paths, "path or paths required")
	if !ok {
		return
	}
	method := req.Method
	if req.Wipe {
		if _, ok := fslib.WipeMethodByID(method); !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown wipe method %q", method))
			return
		}
	}
	// Protected-target check runs first so the user never has to wait for a
	// (possibly long) wipe to learn a path was blocked.
	if blocked := h.protectedDeleteTarget(r, targets); blocked != "" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("path %q is protected and cannot be deleted", blocked))
		return
	}
	fs := h.fsForRequest(r)

	// Plain deletes are metadata-only unlinks (os.RemoveAll), so they complete
	// in ~microseconds regardless of file size. Secure deletes overwrite every
	// byte first and are O(file size × passes) — multi-GB files take minutes —
	// so wipe must be an explicit per-delete choice and it streams its own
	// percentage.
	if !req.Wipe {
		for _, p := range targets {
			select {
			case <-r.Context().Done():
				writeError(w, http.StatusRequestTimeout, "operation cancelled")
				return
			default:
			}
			if err := fs.Delete(p); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		writeOK(w)
		return
	}

	// Secure delete: stream wipe's progress as NDJSON so the client renders a
	// live percentage instead of a stuck "starting" indicator. The wipe loop
	// aborts as soon as the request is cancelled (client abort or timeout).
	flushJSON, ok := ndjsonWriter(w, r)
	if !ok {
		return
	}
	report := progressReporter(flushJSON)
	pct := new(float64)
	for i, p := range targets {
		if r.Context().Err() != nil {
			ndjsonError(flushJSON, "operation cancelled")
			return
		}
		report(i, len(targets), filepath.Base(p), nil)
		if err := fs.WipeContext(r.Context(), p, method, func(v float64) {
			// WipeContext reports 0–100 like a classic percent; the shared
			// filePct field of the progress stream uses a 0–1 fraction (as
			// move/copy do), so normalise here before the frontend multiplies
			// by 100. Without this a wipe would render percentages like 5730%.
			*pct = v / 100
			report(i, len(targets), filepath.Base(p), pct)
		}); err != nil {
			ndjsonError(flushJSON, err.Error())
			return
		}
	}
	report(len(targets), len(targets), filepath.Base(targets[len(targets)-1]), nil)
	flushJSON(map[string]any{"type": "result", "status": "ok"})
}

// singleOrList collapses the "single + list" pair of variants for the same
// logical field (path/paths, src/srcs) into one slice. It writes an error and
// returns false when neither variant was supplied.
func singleOrList(w http.ResponseWriter, single string, list []string, missingErr string) ([]string, bool) {
	targets := list
	if len(targets) == 0 && single != "" {
		targets = []string{single}
	}
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, missingErr)
		return nil, false
	}
	return targets, true
}

// parseSrcDstRequest decodes the shared move/copy request shape (src or srcs,
// dst, on_conflict) and validates it. It writes the error response and returns
// (nil, "", false) on failure.
func parseSrcDstRequest(w http.ResponseWriter, r *http.Request) ([]string, string, fslib.ConflictStrategy, bool) {
	var req struct {
		Src        string   `json:"src"`
		Srcs       []string `json:"srcs"`
		Dst        string   `json:"dst"`
		OnConflict string   `json:"on_conflict"`
	}
	if !decodePostBody(w, r, &req) {
		return nil, "", "", false
	}
	sources, ok := singleOrList(w, req.Src, req.Srcs, "src (or srcs) required")
	if !ok {
		return nil, "", "", false
	}
	if req.Dst == "" {
		writeError(w, http.StatusBadRequest, "dst required")
		return nil, "", "", false
	}
	onConflict, err := fslib.ParseConflictStrategy(req.OnConflict)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, "", "", false
	}
	return sources, req.Dst, onConflict, true
}

func (h *Handler) handleMove(w http.ResponseWriter, r *http.Request) {
	sources, dst, onConflict, ok := parseSrcDstRequest(w, r)
	if !ok {
		return
	}
	h.streamFileOp(w, r, sources, dst, onConflict, "move")
}

func (h *Handler) handleCopy(w http.ResponseWriter, r *http.Request) {
	sources, dst, onConflict, ok := parseSrcDstRequest(w, r)
	if !ok {
		return
	}
	h.streamFileOp(w, r, sources, dst, onConflict, "copy")
}

// streamFileOp streams move/copy progress as NDJSON so the client can show the
// currently processed file and the overall percentage. Each processed source
// emits a "progress" line; the final line is either a "result" (ok) or an
// "error" (e.g. destination_exists with the conflicting paths).
func (h *Handler) streamFileOp(w http.ResponseWriter, r *http.Request, sources []string, dst string, onConflict fslib.ConflictStrategy, op string) {
	flushJSON, ok := ndjsonWriter(w, r)
	if !ok {
		return
	}

	report := progressReporter(flushJSON)

	total := len(sources)
	fsys := h.fsForRequest(r)

	var conflicts []string
	var ferr error
	// Routes register both move and copy here; binding the branch once makes
	// the single-item and batch paths share the same op selection.
	isMove := op == "move"
	var movedRelocs [][2]string
	applyOne := func(pct func(float64)) error {
		if isMove {
			dest, err := fsys.MoveReloc(sources[0], dst, onConflict, pct)
			if err == nil {
				movedRelocs = append(movedRelocs, [2]string{sources[0], dest})
			}
			return err
		}
		return fsys.CopyWithConflict(sources[0], dst, onConflict, pct)
	}
	applyBatch := func(cb func(fslib.TransferProgress)) ([]string, error) {
		if isMove {
			conflicts, relocs, err := fsys.MoveBatchReloc(sources, dst, onConflict, cb)
			movedRelocs = append(movedRelocs, relocs...)
			return conflicts, err
		}
		return fsys.CopyBatch(sources, dst, onConflict, cb)
	}
	if total == 1 {
		report(0, total, filepath.Base(sources[0]), nil)
		reportPct := func(pct float64) {
			p := pct
			report(0, total, filepath.Base(sources[0]), &p)
		}
		if err := applyOne(reportPct); err != nil {
			if errors.Is(err, fslib.ErrDestinationExists) {
				conflicts = []string{conflictPathFromErr(err)}
			} else {
				ferr = err
			}
		} else {
			report(total, total, filepath.Base(sources[0]), nil)
		}
	} else {
		conflicts, ferr = applyBatch(func(p fslib.TransferProgress) {
			report(p.FileDone, p.FileTotal, p.Current, p.FilePct)
		})
	}

	if ferr != nil {
		ndjsonError(flushJSON, ferr.Error())
		return
	}
	if len(conflicts) > 0 {
		flushJSON(map[string]any{
			"type":      "error",
			"code":      "destination_exists",
			"message":   "destination already exists",
			"conflicts": conflicts,
		})
		return
	}
	// Rewrite delete-protection entries to follow any relocated items so a
	// moved protected folder does not silently lose its lock.
	if isMove {
		for _, rel := range movedRelocs {
			h.relocateProtectedPaths(r, rel[0], rel[1])
		}
	}
	flushJSON(map[string]any{
		"type":   "result",
		"status": "ok",
	})
}

func (h *Handler) handleZipDownload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if !decodePostBody(w, r, &req) {
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
	fsys := h.fsForRequest(r)
	// Verify every requested path exists before committing to the download,
	// so a typo or stale selection fails with a clean JSON error instead of a
	// truncated zip (once the 200 + headers are sent, ZipPaths errors cannot
	// reach the client as an error response).
	for _, p := range req.Paths {
		if _, err := fsys.FileInfo(p); err != nil {
			if os.IsNotExist(err) {
				writeError(w, http.StatusNotFound, "file not found: "+p)
			} else {
				writeError(w, http.StatusInternalServerError, "cannot read "+p+": "+err.Error())
			}
			return
		}
	}
	w.Header().Set("Content-Disposition", contentDispositionAttachment(name+".zip"))
	w.Header().Set("Content-Type", "application/zip")
	if err := fsys.ZipPaths(w, req.Paths); err != nil {
		// Response headers already sent; cannot write JSON error.
		log.Printf("zip download failed: %v", err)
		return
	}
}

func (h *Handler) handleRead(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	path := requireQueryPath(w, r)
	if path == "" {
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
	if !requirePost(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxWriteBodyBytes)
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		return
	}
	if !requirePathField(w, req.Path) {
		return
	}
	if err := h.fsForRequest(r).WriteFile(req.Path, []byte(req.Content)); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w)
}

// fileInfoForRequest resolves a path's metadata with the request's FS, writing
// a 404 (not found) or 403 (exists but denied) and returning nil on failure.
func (h *Handler) fileInfoForRequest(w http.ResponseWriter, r *http.Request, path string) (fs *fslib.FS, entry *fslib.FileEntry) {
	fs = h.fsForRequest(r)
	entry, err := fs.FileInfo(path)
	if err != nil {
		// Conflating "exists but not readable" with "not found" both lies to
		// the client and hides the difference behind a single status code.
		if errors.Is(err, os.ErrPermission) {
			writeError(w, http.StatusForbidden, err.Error())
		} else {
			writeError(w, http.StatusNotFound, err.Error())
		}
		return nil, nil
	}
	return fs, entry
}

func (h *Handler) handleInfo(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	path := queryPath(r, "/")
	fs, entry := h.fileInfoForRequest(w, r, path)
	if entry == nil {
		return
	}
	var diskErr string
	usage, err := fs.DiskUsageAt(path)
	if err != nil {
		// Non-fatal: the file entry is still useful, but surface the failure
		// so a broken statfs (e.g. a path on an unmounted volume) is not
		// silently hidden behind a null disk field. The frontend hides the
		// disk widget when it is absent anyway; the extra field lets callers
		// distinguish "no stats" from a genuinely broken volume.
		diskErr = err.Error()
		log.Printf("disk usage for %q: %v", path, err)
	}
	writeJSON(w, map[string]any{"file": entry, "disk": usage, "disk_error": diskErr})
}

func (h *Handler) handleFolderInfo(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	path := requireQueryPath(w, r)
	if path == "" {
		return
	}
	fs, entry := h.fileInfoForRequest(w, r, path)
	if entry == nil {
		return
	}
	if !entry.IsDir {
		writeError(w, http.StatusBadRequest, "path is not a directory")
		return
	}
	totalSize, fileCount, err := fs.DirSize(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"name":       entry.Name,
		"path":       entry.Path,
		"mod_time":   entry.ModTime,
		"total_size": totalSize,
		"file_count": fileCount,
	})
}

func (h *Handler) handleFavourites(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	writeJSON(w, h.favouritesForRequest(r))
}

func normalizeFavourites(favs []config.Favourite) []config.Favourite {
	out := make([]config.Favourite, 0, len(favs)+1)
	var home config.Favourite
	hasHome := false
	seen := make(map[string]struct{}, len(favs)+1)
	for _, fav := range favs {
		p := normalizeVirtualPath(fav.Path)
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		name := strings.TrimSpace(fav.Name)
		if p == "/" {
			hasHome = true
			home = config.Favourite{Name: name, Path: "/"}
			if home.Name == "" {
				home.Name = "Home"
			}
			continue
		}
		if name == "" {
			name = filepath.Base(p)
		}
		out = append(out, config.Favourite{Name: name, Path: p})
	}
	// Sort by display name (case-insensitive, ignoring surrounding space),
	// falling back to the path so a stable ordering holds for equal names. The
	// sort keys are precomputed once instead of re-normalising inside every
	// comparator call.
	keys := make([]string, len(out))
	for i, f := range out {
		keys[i] = strings.ToLower(strings.TrimSpace(f.Name))
	}
	sort.SliceStable(out, func(i, j int) bool {
		if keys[i] == keys[j] {
			return out[i].Path < out[j].Path
		}
		return keys[i] < keys[j]
	})
	if !hasHome {
		home = config.Favourite{Name: "Home", Path: "/"}
	}
	return append([]config.Favourite{home}, out...)
}

// validateFavouriteDir resolves a favourite path and reports whether it is an
// existing directory, so favourites Add and Update share one check and one
// error format instead of duplicating the stat loop.
func validateFavouriteDir(fsys *fslib.FS, p string) error {
	entry, err := fsys.FileInfo(p)
	if err != nil {
		return err
	}
	if !entry.IsDir {
		return fmt.Errorf("path is not a directory: %s", p)
	}
	return nil
}

// maxFavouriteNameLen bounds the display name so user-supplied names cannot
// bloat the persisted config or the sidebar DOM without limit.
const maxFavouriteNameLen = 64

// cleanFavourite normalises a favourite path and name: the path is trimmed and
// virtualised, and an empty name falls back to the basename (or "Home" for the
// base directory) with a length cap. Returns the cleaned (path, name).
func cleanFavourite(p, name string) (string, string) {
	p = normalizeVirtualPath(strings.TrimSpace(p))
	name = strings.TrimSpace(name)
	if name == "" {
		name = filepath.Base(p)
		if name == "/" || name == "." {
			name = "Home"
		}
	}
	if len([]rune(name)) > maxFavouriteNameLen {
		// Truncate by runes, not bytes: slicing raw bytes could split a UTF-8
		// sequence and persist an invalid string into the config JSON.
		name = string([]rune(name)[:maxFavouriteNameLen])
	}
	return p, name
}

// cleanValidateFavourite normalises and validates a single favourite entry the
// same way for both add and update: path must be present, and the dir must
// exist in the user's jail. It writes the error response and returns
// (path, name, false) on failure.
func cleanValidateFavourite(w http.ResponseWriter, fsys *fslib.FS, path, name string) (string, string, bool) {
	if !requirePathField(w, path) {
		return "", "", false
	}
	path, name = cleanFavourite(path, name)
	if err := validateFavouriteDir(fsys, path); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return "", "", false
	}
	return path, name, true
}

func (h *Handler) handleFavouritesAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if !decodePostBody(w, r, &req) {
		return
	}
	path, name, ok := cleanValidateFavourite(w, h.fsForRequest(r), req.Path, req.Name)
	if !ok {
		return
	}
	key := h.requestKey(r)
	// Apply the same cap as the bulk update so repeated single adds cannot
	// grow the config (and every render) without bound. Re-adding an existing
	// favourite is still allowed since it does not grow the list.
	if current := runtimeList(&h.favMu, h.runtimeFavs, key); len(current) >= maxFavouriteEntries &&
		!slices.ContainsFunc(current, func(f config.Favourite) bool { return f.Path == path }) {
		writeError(w, http.StatusBadRequest, "too many favourites")
		return
	}
	h.updateFavouriteEntry(key, w, func(current []config.Favourite) ([]config.Favourite, bool) {
		next, early := addUnique(current, config.Favourite{Name: name, Path: path},
			func(f config.Favourite) bool { return f.Path == path })
		return normalizeFavourites(next), early
	})
}

// removeByPath filters list down to the items whose path differs from reqPath
// and reports whether any item actually matched (was removed). It is the shared
// mutation behind the favourites/protected REMOVE handlers, which differ only
// in the per-item type and the re-normalisation step applied afterwards.
func removeByPath[T any](list []T, reqPath string, pathOf func(T) string) ([]T, bool) {
	// Single pass: keepOnly + slices.ContainsFunc walked the slice twice,
	// calling pathOf on every element both times.
	out := make([]T, 0, len(list))
	present := false
	for _, v := range list {
		if pathOf(v) == reqPath {
			present = true
			continue
		}
		out = append(out, v)
	}
	return out, present
}

func (h *Handler) handleFavouritesRemove(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	reqPath, ok := decodePathRequest(w, r)
	if !ok {
		return
	}
	reqPath = normalizeVirtualPath(reqPath)
	h.updateFavouriteEntry(h.requestKey(r), w, func(current []config.Favourite) ([]config.Favourite, bool) {
		// Removing a non-existent favourite is a no-op: report early so the
		// config file is not rewritten for nothing.
		next, present := removeByPath(current, reqPath, func(f config.Favourite) string { return f.Path })
		return normalizeFavourites(next), !present
	})
}

func (h *Handler) handleFavouritesUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Favourites []config.Favourite `json:"favourites"`
	}
	if !decodePostBody(w, r, &req) {
		return
	}
	if len(req.Favourites) > maxFavouriteEntries {
		writeError(w, http.StatusBadRequest, "too many favourites")
		return
	}
	// Validate every submitted path the same way handleFavouritesAdd does so a
	// client cannot persist arbitrary junk that later errors on every render.
	fsys := h.fsForRequest(r)
	for i := range req.Favourites {
		fav := &req.Favourites[i]
		path, name, ok := cleanValidateFavourite(w, fsys, fav.Path, fav.Name)
		if !ok {
			return
		}
		fav.Path, fav.Name = path, name
	}
	h.updateFavouriteEntry(h.requestKey(r), w, func(current []config.Favourite) ([]config.Favourite, bool) {
		return normalizeFavourites(req.Favourites), false
	})
}

func (h *Handler) handleProtected(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	writeJSON(w, h.protectedPathsForRequest(r))
}

func (h *Handler) handleProtectedAdd(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	reqPath, ok := decodePathRequest(w, r)
	if !ok {
		return
	}
	reqPath = normalizeVirtualPath(reqPath)
	// Mirror the favourites validation: protecting a path that does not exist
	// gives a false sense of safety (nothing is actually protected), so reject
	// it before it is persisted. Surface the real error so a permission
	// failure (or an out-of-jail path) is distinguishable from a plain missing
	// entry.
	if _, err := h.fsForRequest(r).FileInfo(reqPath); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.updateProtectedEntry(h.requestKey(r), w, func(current []string) ([]string, bool) {
		return addUnique(current, reqPath, func(p string) bool { return p == reqPath })
	})
}

func (h *Handler) handleProtectedRemove(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	reqPath, ok := decodePathRequest(w, r)
	if !ok {
		return
	}
	reqPath = normalizeVirtualPath(reqPath)
	h.updateProtectedEntry(h.requestKey(r), w, func(current []string) ([]string, bool) {
		// Removing a protected path is persisted; only a path that is NOT
		// protected is a no-op (reported early so the config file is not
		// rewritten for nothing).
		next, present := removeByPath(current, reqPath, func(p string) string { return p })
		return next, !present
	})
}

// handleDeletePrefs returns the remembered secure-delete preferences for the
// current user (the global entry in no-auth mode).
func (h *Handler) handleDeletePrefs(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	writeJSON(w, h.prefsForRequest(r))
}

// handleDeletePrefsUpdate persists the remembered secure-delete preferences for
// the current user.
func (h *Handler) handleDeletePrefsUpdate(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	var req prefs.Deletion
	if !decodePostBody(w, r, &req) {
		return
	}
	if req.WipeMethod != "" {
		if _, ok := fslib.WipeMethodByID(req.WipeMethod); !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown wipe method %q", req.WipeMethod))
			return
		}
	}
	persistRuntimeMutation(&h.deletePrefsMu, h.runtimeDelete, h.requestKey(r),
		func(current prefs.Deletion) (prefs.Deletion, bool) { return req, false },
		func(v prefs.Deletion) prefs.Deletion { return v },
		func(v prefs.Deletion) error { return h.persistDeletePrefs(h.requestKey(r), v) },
		w)
}

// handleShowDotfiles persists the show-dotfiles preference for the current
// user (the global default in no-auth mode) so the sidebar toggle survives
// reloads. Per-user values are stored in ~/.config/filex.yaml; the no-auth
// value updates the server default in config.yaml.
func (h *Handler) handleShowDotfiles(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	var req struct {
		ShowDotfiles bool `json:"show_dotfiles"`
	}
	if err := decodeBody(w, r, &req); err != nil {
		return
	}
	if err := h.persistShowDotfiles(h.requestKey(r), req.ShowDotfiles); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w)
}

func (h *Handler) handleConfig(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	writeJSON(w, map[string]any{
		"host":               h.cfg.Host,
		"port":               h.cfg.Port,
		"show_dotfiles":      h.showDotfilesForRequest(r),
		"auth_required":      h.cfg.AuthRequired(),
		"session_ttl_days":   h.cfg.SessionTTLDays,
		"wipe_methods":       wipeMethodsJSON(),
		"ffmpeg_available":   h.tools["ffmpeg"],
		"ffprobe_available":  h.tools["ffprobe"],
		"unzip_available":    h.tools["unzip"],
		"sevenzip_available": h.tools["7z"],
		"unrar_available":    h.tools["unrar"],
		"tar_available":      h.tools["tar"],
	})
}

// makeThumbDirWritable walks the thumbnail directory tree and sets permissions
// so that the server process can delete all files and directories inside it.
// This is needed because ffmpeg may create files with a restrictive umask, or
// the parent tmp/ directory may have been created by another user/process.
func makeThumbDirWritable(root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible entries
		}
		mode := os.FileMode(0666)
		if d.IsDir() {
			mode = 0777
		}
		if err := os.Chmod(p, mode); err != nil {
			log.Printf("makeThumbDirWritable: chmod %s: %v", p, err)
		}
		return nil
	})
}

// removeThumbDir makes a thumbnail cache directory tree removable (its files
// may be jailed behind index-only permission masks) and wipes it. Generated
// frames and the manifest are unwritable-on-purpose once owned by the jailed
// user, so chmodding first is required for cleanup to succeed.
func removeThumbDir(tDir string) error {
	makeThumbDirWritable(tDir)
	return os.RemoveAll(tDir)
}

// resetThumbDir wipes and recreates a thumbnail cache directory. Errors are
// reported so a stuck generation surfaces to the caller instead of silently
// failing every subsequent thumbnail write.
func resetThumbDir(tDir string) error {
	if err := removeThumbDir(tDir); err != nil {
		return err
	}
	return os.MkdirAll(tDir, 0755)
}

// ---- Video editing handlers -------------------------------------------------

// handleVideoThumbnails returns thumbnails for a video, generating them in the
// background if they are not already cached. When generation is in progress the
// response includes "generating": true and a "total" estimate so the frontend
// can show incremental progress.
func (h *Handler) handleVideoThumbnails(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	videoPath := requireQueryPath(w, r)
	if videoPath == "" {
		return
	}

	if !h.requireFFmpeg(w) {
		return
	}

	fs := h.fsForRequest(r)
	absPath, hashHex, tDir, ok := thumbDirForVideo(w, fs, videoPath)
	if !ok {
		return
	}

	// Read thumbnails already on disk
	existingThumbs, _ := readThumbnailEntries(tDir)

	// Check whether a generation is already running for this video
	gen := h.getThumbGeneration(hashHex)
	if gen != nil {
		select {
		case <-gen.done:
			// Generation finished while we were reading the thumbnails. Refresh
			// the disk state once and fall through to the cached/error path.
			existingThumbs, _ = readThumbnailEntries(tDir)
			if gen.err != nil {
				writeJSON(w, map[string]any{
					"thumbnails": existingThumbs,
					"generating": false,
					"error":      gen.err.Error(),
				})
				return
			}
		default:
			writeJSON(w, map[string]any{
				"thumbnails": existingThumbs,
				"generating": true,
				"total":      gen.total,
				"completed":  gen.completed.Load(),
			})
			return
		}
	}

	// Fully cached – return immediately
	if len(existingThumbs) > 0 {
		writeJSON(w, map[string]any{
			"thumbnails": existingThumbs,
			"generating": false,
		})
		return
	}

	// Check if this file is queued in an active batch by the current user –
	// don't auto-start it (a batch queued by another user must not block this
	// request).
	if h.batchQueued(h.requestKey(r), hashHex) {
		writeJSON(w, map[string]any{
			"thumbnails": existingThumbs,
			"generating": false,
			"waiting":    true,
		})
		return
	}

	// Start background generation using the shared helper
	_, count, generating, err := h.ensureThumbnailGeneration(fs, absPath, hashHex)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var completed int64
	if generating {
		if gen := h.getThumbGeneration(hashHex); gen != nil {
			completed = gen.completed.Load()
		}
	}

	writeJSON(w, map[string]any{
		"thumbnails": existingThumbs,
		"generating": generating,
		"total":      count,
		"completed":  completed,
	})
}

// ffprobeFrameTimes runs ffprobe to list keyframe presentation timestamps
// (PTS) for a video, optionally passing extra args (e.g. "-ss" plus
// "-read_intervals" to scan only a window around a target time). The probe
// runs under the user's credentials via RunAs and is bound to ctx so the
// process is killed when the request is cancelled or the client disconnects.
func ffprobeFrameTimes(ctx context.Context, fs *fslib.FS, path string, extraArgs ...string) ([]float64, error) {
	var times []float64
	err := fs.RunAs(func() error {
		args := []string{
			"-v", "error",
			"-select_streams", "v:0",
			"-skip_frame", "nokey",
			"-show_entries", "frame=pts_time",
			"-of", "csv=p=0",
		}
		args = append(args, extraArgs...)
		args = append(args, path)
		output, err := exec.CommandContext(ctx, "ffprobe", args...).Output()
		if err != nil {
			return err
		}
		times = parseFFprobePTS(output)
		return nil
	})
	return times, err
}

// parseFFprobePTS parses the CSV output lines of a pts_time ffprobe query,
// skipping blanks and unparseable lines. Non-finite values (strconv happily
// parses "NaN"/"Inf") are skipped too so a malformed probe can never inject a
// NaN timestamp into downstream math.
func parseFFprobePTS(output []byte) []float64 {
	var times []float64
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if t, err := strconv.ParseFloat(line, 64); err == nil && !math.IsNaN(t) && !math.IsInf(t, 0) {
			times = append(times, t)
		}
	}
	return times
}

// normalizeThumbEntry fills in the derived fields of one thumbnail entry read
// from timestamps.json: index is taken from the 1-based frame number embedded
// in the filename (falling back to array position), and a time that could not
// decode as a number is pinned to 0 instead of leaking a raw JSON value.
func normalizeThumbEntry(i int, e map[string]any) {
	name, _ := e["name"].(string)
	frameNum, err := strconv.Atoi(strings.TrimSuffix(name, ".jpg"))
	if err != nil || frameNum < 1 {
		frameNum = i + 1
	}
	e["index"] = frameNum
	if _, ok := e["time"].(float64); !ok {
		e["time"] = float64(0)
	}
}

// readThumbnailEntries reads the cached thumbnail manifest for a video. The
// manifest lives inside the jail and could be hand-crafted without bound, so it
// is read through a cap: an oversized manifest is discarded and the legacy
// filename-order fallback is used instead of ballooning memory on every poll.
// A present-but-empty manifest authoritatively means "this video has no
// extractable keyframes", which lets the caller skip regeneration; the absence
// of a manifest means "not generated yet" and must be re-attempted.
func readThumbnailEntries(thumbDir string) (entries []map[string]any, manifestExists bool) {
	// Try reading timestamps.json first for accurate timestamps. Its size is
	// bounded (the manifest lives in the user-writable jail and could be
	// hand-crafted); an oversized or corrupt manifest is discarded and the
	// legacy filename-order fallback is used instead of ballooning memory.
	data, err := readBoundedFile(filepath.Join(thumbDir, thumbTimestampsFile), maxThumbManifestBytes)
	if err == nil && json.Unmarshal(data, &entries) == nil {
		for i, e := range entries {
			normalizeThumbEntry(i, e)
		}
		return entries, true
	}

	// Fallback: compute timestamps from file order for legacy cache dirs that
	// predate the always-written manifest. Times are derived from frame order
	// at the sampling interval and may drift from the real keyframe PTS.
	dirEntries, err := os.ReadDir(thumbDir)
	if err != nil {
		return nil, false
	}
	var thumbs []map[string]any
	sort.Slice(dirEntries, func(i, j int) bool {
		return dirEntries[i].Name() < dirEntries[j].Name()
	})
	for _, e := range dirEntries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jpg") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".jpg")
		frameNum, _ := strconv.Atoi(name)
		if frameNum < 1 {
			frameNum = len(thumbs) + 1
		}
		entry := thumbEntryMap(e.Name(), float64(frameNum-1)*thumbFrameInterval)
		entry["index"] = frameNum
		thumbs = append(thumbs, entry)
	}
	return thumbs, false
}

// handleVideoThumbnailImage serves a specific thumbnail image for a video.
func (h *Handler) handleVideoThumbnailImage(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}
	// requireQueryPath already writes the "path required" response, so return
	// immediately rather than emitting a second error for the same request.
	videoPath := requireQueryPath(w, r)
	if videoPath == "" {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}

	fs := h.fsForRequest(r)
	_, _, tDir, ok := thumbDirForVideo(w, fs, videoPath)
	if !ok {
		return
	}

	// id must be a strictly positive integer (note the length limit is enforced
	// by withBodyLimit on the body, and requireQueryPath above only limits
	// "path"); strconv.Atoi rejects any slash, dot-dot or path separator that
	// would otherwise need to be filtered out here.
	frameNum, err := strconv.Atoi(id)
	if err != nil || frameNum < 1 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	thumbFile := filepath.Join(tDir, thumbFrameName(frameNum))

	// Serve through OpenForDownload so open+stat happen atomically under the
	// user's credentials. This avoids the race between an os.Stat existence
	// check and a later open/serve (the file can be removed or replaced in
	// between), and ServeContent still handles range requests.
	w.Header().Set("Cache-Control", "no-cache")
	openAndServeFile(w, r, fs, fs.VirtualPath(thumbFile), false)
}

func formatDuration(seconds float64) string {
	// Guard against non-finite or negative input (a malformed request time
	// would otherwise make the int() conversions below yield garbage).
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		seconds = 0
	}
	h := int(seconds) / 3600
	m := (int(seconds) % 3600) / 60
	s := int(seconds) % 60
	ms := int(math.Mod(seconds, 1.0) * 1000)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

// handleVideoExtract extracts specified segments from a video using ffmpeg
// with stream copy (no re-encoding) and concatenates them into a single file.
// Progress is streamed as ndjson lines.
func (h *Handler) handleVideoExtract(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string     `json:"path"`
		Segments []timeSpan `json:"segments"`
	}
	if !decodePostBody(w, r, &req) {
		return
	}
	if !requirePathField(w, req.Path) {
		return
	}
	if len(req.Segments) == 0 {
		writeError(w, http.StatusBadRequest, "segments required")
		return
	}
	if len(req.Segments) > maxExtractSegments {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many segments (max %d)", maxExtractSegments))
		return
	}
	if idx := firstInvalidSpan(req.Segments); idx != -1 {
		writeError(w, http.StatusBadRequest, "invalid segment: start must be non-negative and end must be a finite value after start")
		return
	}

	if !h.requireFFmpeg(w) {
		return
	}

	fs := h.fsForRequest(r)
	absPath, _, ok := resolveVideoPathOrErr(w, fs, req.Path)
	if !ok {
		return
	}

	// Switch to streaming response
	flushJSON, ok := ndjsonWriter(w, r)
	if !ok {
		return
	}

	// Create temp directory for segment files alongside the video file
	tmpParent := cacheRoot(absPath)
	var tmpDir string
	var err error
	err = fs.RunAs(func() error {
		if err := os.MkdirAll(tmpParent, 0755); err != nil {
			return err
		}
		tmpDir, err = os.MkdirTemp(tmpParent, "filex_extract_*")
		return err
	})
	if err != nil {
		ndjsonError(flushJSON, "could not create temp directory")
		return
	}
	defer func() {
		if err := fs.RunAs(func() error {
			return os.RemoveAll(tmpDir)
		}); err != nil {
			log.Printf("video extract: cleanup of temp dir %s: %v", tmpDir, err)
		}
	}()

	ext := filepath.Ext(absPath)
	// Pick an output name that can never clobber an existing file: the user may
	// already have "name_cut.mp4", and two concurrent extractions of the same
	// video must not fight over one path. NextFreePath returns the base when it
	// is free and a " (copy)" variant otherwise, and the failure cleanup below
	// only ever removes the name this request actually owns.
	var outputPath string
	if err := fs.RunAs(func() error {
		var e error
		outputPath, e = fs.NextFreePath(strings.TrimSuffix(absPath, ext) + "_cut" + ext)
		return e
	}); err != nil {
		ndjsonError(flushJSON, "could not prepare output path")
		return
	}

	// Clean up a partial output on every failure path. outputOK is flipped only
	// right before the success line is streamed.
	outputOK := false
	defer func() {
		if !outputOK {
			if err := fs.RunAs(func() error { return os.RemoveAll(outputPath) }); err != nil {
				log.Printf("video extract: cleanup of partial output %s: %v", outputPath, err)
			}
		}
	}()

	runFFmpeg := func(cmd *exec.Cmd) ([]byte, error) {
		var output []byte
		err := fs.RunAs(func() error {
			out, cmdErr := cmd.CombinedOutput()
			output = out
			return cmdErr
		})
		return output, err
	}

	// extractSegment runs ffmpeg for one segment, streams a progress line, and
	// reports a formatted error line on failure. It returns ok=false when the
	// stream is broken (client disconnected) or ffmpeg failed.
	extractSegment := func(i int, total int, seg timeSpan, outPath string) (ok bool) {
		cmd := ffmpegExtractCmd(r.Context(), absPath, outPath, formatDuration(seg.Start), formatDuration(seg.End-seg.Start))
		if !flushJSON(map[string]any{
			"type":    "progress",
			"segment": i + 1,
			"total":   total,
			"command": cmd.String(),
		}) {
			return false
		}
		if output, err := runFFmpeg(cmd); err != nil {
			ndjsonError(flushJSON, fmt.Sprintf("ffmpeg segment %d failed: %s: %s",
				i+1, err.Error(), string(output)))
			return false
		}
		return true
	}

	// Single segment: extract directly to output, skip concat
	if len(req.Segments) == 1 {
		if !extractSegment(0, 1, req.Segments[0], outputPath) {
			return
		}
	} else {
		total := len(req.Segments)

		// Stream every source track with -c copy into temp segments; the
		// container must match the source (a hardcoded .mp4 would make .mkv,
		// .webm or .avi sources with non-MP4-compatible streams fail).
		segExt := ext
		if segExt == "" {
			segExt = ".mp4"
		}

		// Extract each segment as a separate temp file; the names are computed
		// once and reused for the concat manifest so the two cannot drift.
		segFiles := make([]string, len(req.Segments))
		for i := range req.Segments {
			segFiles[i] = filepath.Join(tmpDir, fmt.Sprintf("seg_%d%s", i, segExt))
		}
		for i, seg := range req.Segments {
			if !extractSegment(i, total, seg, segFiles[i]) {
				return
			}
		}

		// Create concat file
		concatLines := make([]string, len(segFiles))
		for i, path := range segFiles {
			concatLines[i] = "file '" + filepath.Base(path) + "'"
		}
		concatFile := filepath.Join(tmpDir, "concat.txt")
		err = fs.RunAs(func() error {
			return os.WriteFile(concatFile, []byte(strings.Join(concatLines, "\n")), 0644)
		})
		if err != nil {
			ndjsonError(flushJSON, "could not write concat file")
			return
		}

		// -safe 1 keeps concat relocatable: the entries are relative names we
		// generated ourselves (seg_%d.mp4), so absolute paths are not needed.
		args := []string{"-y", "-f", "concat", "-safe", "1", "-i", concatFile}
		args = append(args, ffmpegStreamCopyArgs()...)
		args = append(args, outputPath)
		cmd := exec.CommandContext(r.Context(), "ffmpeg", args...)
		cmd.Dir = tmpDir

		// Concat step
		flushJSON(map[string]any{
			"type":    "progress",
			"status":  "concatenating",
			"command": cmd.String(),
		})
		if output, err := runFFmpeg(cmd); err != nil {
			ndjsonError(flushJSON, fmt.Sprintf("ffmpeg concat failed: %s: %s", err.Error(), string(output)))
			return
		}
	}

	// Return virtual path for the output file
	outputOK = true
	flushJSON(map[string]any{
		"type":   "result",
		"status": "ok",
		"output": fs.VirtualPath(outputPath),
	})
}

// ensureThumbnailGeneration starts background thumbnail generation for a video
// if it is not already cached or in progress. hashHex is the SHA-256 hash of
// absPath (cache key). Returns the thumbDir, count, and whether generation is
// in progress.
func (h *Handler) ensureThumbnailGeneration(fs *fslib.FS, absPath, hashHex string) (tDir string, count int, generating bool, err error) {
	tDir = thumbDir(absPath, hashHex)

	existingThumbs, manifestExists := readThumbnailEntries(tDir)
	// Anything cached is served as-is; a present-but-empty manifest is the
	// negative cache for "scanned, no keyframes found" and must not re-trigger
	// generation on every visit.
	if len(existingThumbs) > 0 || manifestExists {
		return tDir, len(existingThumbs), false, nil
	}

	if gen := h.getThumbGeneration(hashHex); gen != nil {
		return tDir, gen.total, true, nil
	}

	var thumbnailCount int
	err = fs.RunAs(func() error {
		thumbnailCount = countThumbnails(absPath, thumbFrameInterval)
		return os.MkdirAll(tDir, 0755)
	})
	if err != nil {
		log.Printf("thumbnail count/prepare failed for %s: %v", absPath, err)
		return tDir, 0, false, err
	}
	if thumbnailCount < 0 {
		// Total unknown (e.g. the scanner hit its guard limit); surface 0
		// rather than a nonsensical negative count to the client.
		thumbnailCount = 0
	}

	ctx, cancel := context.WithCancel(context.Background())
	newGen := &thumbGeneration{
		done:      make(chan struct{}),
		cancelled: make(chan struct{}),
		cancel:    cancel,
		total:     thumbnailCount,
	}

	// Check-and-insert under one lock so two racing requests cannot start two
	// concurrent generations for the same video (which would fight over the
	// same cache directory).
	h.thumbGenMu.Lock()
	if gen, hasGen := h.thumbGenerations[hashHex]; hasGen {
		h.thumbGenMu.Unlock()
		cancel()
		return tDir, gen.total, true, nil
	}
	h.thumbGenerations[hashHex] = newGen
	h.thumbGenMu.Unlock()
	gen := newGen

	go func() {
		defer cancel()
		// close(gen.done) is registered first; the recover below is registered
		// after it, so it runs first (LIFO) and waiters unblocked by close see
		// gen.err already set instead of a nil error.
		defer close(gen.done)
		defer func() {
			if p := recover(); p != nil {
				log.Printf("thumbnail generation: panic for %s: %v", absPath, p)
				if gen.err == nil {
					gen.err = fmt.Errorf("thumbnail generation panicked: %v", p)
				}
			}
		}()

		if err := fs.RunAs(func() error {
			select {
			case <-gen.cancelled:
				return nil
			case <-ctx.Done():
				return nil
			default:
			}

			if err := resetThumbDir(tDir); err != nil {
				gen.err = fmt.Errorf("prepare thumbnail dir: %w", err)
				log.Printf("thumbnail generation: %v for %s", err, absPath)
				return nil
			}

			threads := clampThumbnailThreads(h.cfg.ThumbnailThreads)
			timestamps, err := generateKeyframeThumbnailsSample(ctx, absPath, tDir, thumbFrameInterval, effectiveParallelism(h.cfg.ThumbnailParallelism), threads, fs.UID(), fs.GID(), func(written, total int) bool {
				gen.completed.Store(int64(written))
				return true
			})
			if err != nil {
				log.Printf("thumbnail generation: %s for %s", err, absPath)
				if rmErr := removeThumbDir(tDir); rmErr != nil {
					log.Printf("thumbnail generation: failed to clean up %s: %v", tDir, rmErr)
				}
				gen.err = err
				return nil
			}

			// Always persist a manifest on success — even with zero frames, so
			// the empty manifest becomes an authoritative negative cache and the
			// next request does not regenerate forever. The partial manifests
			// flushed during generation are replaced atomically by this one.
			if err := writeThumbTimestamps(tDir, timestamps, fs.UID(), fs.GID()); err != nil {
				gen.err = err
				return nil
			}

			log.Printf("thumbnail generation: generated %d thumbnails for %s", len(timestamps), absPath)
			return nil
		}); err != nil {
			// runAs failure (e.g. credential switch EPERM): the closure never
			// ran, so surface it instead of letting waiters see a success.
			gen.err = fmt.Errorf("thumbnail generation: %w", err)
			log.Printf("thumbnail generation: %v for %s", err, absPath)
		}

		// Keep the cache entry a short while AFTER generation completes so
		// polling requests don't spin up a second concurrent generation. The
		// delete is guarded so a replacement entry (e.g. after a cancel) is
		// never removed by a stale timer.
		time.AfterFunc(5*time.Minute, func() {
			h.thumbGenMu.Lock()
			if h.thumbGenerations[hashHex] == gen {
				delete(h.thumbGenerations, hashHex)
			}
			h.thumbGenMu.Unlock()
		})
	}()

	return tDir, thumbnailCount, true, nil
}

// batchQueued reports whether hashHex is currently queued in key's active
// batch bucket (background thumbnail batching).
func (h *Handler) batchQueued(key, hashHex string) bool {
	h.batchQueueMu.Lock()
	defer h.batchQueueMu.Unlock()
	_, inBatch := h.batchQueue[key][hashHex]
	return inBatch
}

// enqueueBatch adds the hashes to key's batch bucket, creating the per-user
// bucket when it does not exist yet.
func (h *Handler) enqueueBatch(key string, hashes []string) {
	h.batchQueueMu.Lock()
	defer h.batchQueueMu.Unlock()
	bucket := h.batchQueue[key]
	if bucket == nil {
		bucket = make(map[string]struct{}, len(hashes))
		h.batchQueue[key] = bucket
	}
	for _, hh := range hashes {
		bucket[hh] = struct{}{}
	}
}

// removeFromBatchQueue drops hashHex from key's batch bucket, deleting the
// bucket entirely when it becomes empty so drained batches do not linger as
// empty map entries. It reports whether the hash was actually queued.
func (h *Handler) removeFromBatchQueue(key, hashHex string) bool {
	h.batchQueueMu.Lock()
	defer h.batchQueueMu.Unlock()
	if bucket := h.batchQueue[key]; bucket != nil {
		_, was := bucket[hashHex]
		delete(bucket, hashHex)
		if len(bucket) == 0 {
			delete(h.batchQueue, key)
		}
		return was
	}
	return false
}

// handleVideoBatchStatus returns the list of paths in the current batch,
// so the frontend can resume the progress UI after a page reload.
func (h *Handler) handleVideoBatchStatus(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}

	// Per-user batch state, so a user querying status never sees the paths of
	// another user's concurrent batch.
	key := h.requestKey(r)
	h.batchActiveMu.Lock()
	paths := make([]string, len(h.batchActive[key].pending))
	copy(paths, h.batchActive[key].pending)
	h.batchActiveMu.Unlock()

	writeJSON(w, map[string]any{
		"active": len(paths) > 0,
		"paths":  paths,
	})
}

// handleVideoBatchThumbnails starts background thumbnail generation for all
// selected videos and returns immediately. The frontend polls individual
// /api/video-thumbnails endpoints for progress.
func (h *Handler) handleVideoBatchThumbnails(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if !decodePostBody(w, r, &req) {
		return
	}
	if len(req.Paths) == 0 {
		writeError(w, http.StatusBadRequest, "paths required")
		return
	}
	if len(req.Paths) > maxBatchThumbnailPaths {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many paths (max %d)", maxBatchThumbnailPaths))
		return
	}

	if !h.requireFFmpeg(w) {
		return
	}

	fs := h.fsForRequest(r)

	type jobItem struct {
		absPath string
		hashHex string
	}
	skipped := make(map[string]string)
	hashes := make([]string, 0, len(req.Paths))
	jobs := make([]jobItem, 0, len(req.Paths))
	// The same video may legitimately appear twice in the selection; without
	// dedup it would be queued and generated twice (and double-counted).
	seen := make(map[string]struct{}, len(req.Paths))
	for _, videoPath := range req.Paths {
		absPath, hashHex, err := resolveVideoPathHash(fs, videoPath)
		if err != nil {
			// Skipped paths are reported back (with the reason) so the
			// frontend can mark them failed immediately instead of polling
			// /api/video-thumbnails forever and tripping its poll-error limit.
			skipped[videoPath] = err.Error()
			log.Printf("batch thumbnails: skipping %q: %v", videoPath, err)
			continue
		}
		if _, dup := seen[hashHex]; dup {
			continue
		}
		seen[hashHex] = struct{}{}
		jobs = append(jobs, jobItem{absPath, hashHex})
		hashes = append(hashes, hashHex)
	}

	// Per-user bucket, so different users' batches never collide and a user
	// cannot observe another user's active batch.
	key := h.requestKey(r)
	h.enqueueBatch(key, hashes)

	// Store the batch paths so the frontend can resume after page reload. Each
	// batch carries a generation marker so the first batch's completion defer
	// cannot clear a newer batch started for the same user in the meantime.
	h.batchActiveMu.Lock()
	gen := h.batchActive[key].gen + 1
	h.batchActive[key] = batchActive{pending: req.Paths, gen: gen}
	h.batchActiveMu.Unlock()

	writeJSON(w, struct {
		Status  string            `json:"status"`
		Paths   []string          `json:"paths"`
		Skipped map[string]string `json:"skipped"`
		Total   int               `json:"total"`
	}{Status: "started", Paths: req.Paths, Skipped: skipped, Total: len(jobs)})

	go func() {
		defer func() {
			// This goroutine runs detached from any request, so a panic here
			// (e.g. in FS.RunAs) would otherwise take down the whole server.
			if p := recover(); p != nil {
				log.Printf("panic in batch thumbnail worker: %v\n%s", p, debug.Stack())
			}
			// Only clear the active-batch entry if it still belongs to this
			// batch: a newer batch for the same user raised the generation.
			h.batchActiveMu.Lock()
			if cur := h.batchActive[key]; cur.gen == gen {
				delete(h.batchActive, key)
			}
			h.batchActiveMu.Unlock()
		}()
		for _, j := range jobs {
			_, _, generating, err := h.ensureThumbnailGeneration(fs, j.absPath, j.hashHex)

			// Removing the hash drains the per-user bucket (and deletes it when
			// empty); a false result means the user cancelled the batch while
			// this job was starting.
			wasQueued := h.removeFromBatchQueue(key, j.hashHex)

			// If the file was removed from the queue while we were starting
			// generation (user cancelled batch), cancel the gen we just started.
			if !wasQueued {
				if gen := h.getThumbGeneration(j.hashHex); gen != nil {
					gen.cancel()
					<-gen.done
				}
				continue
			}

			if err != nil || !generating {
				continue
			}

			if gen := h.getThumbGeneration(j.hashHex); gen != nil {
				<-gen.done
			}
		}
	}()
}

// timeSpan is a single [start, end) time range in seconds. It is the shared
// shape of extract segments and saved interval markers, which were originally
// two identical copy-pasted structs.
type timeSpan struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// firstInvalidSpan returns the index of the first span whose [start, end) is
// not a usable time range, or -1 when every span is valid.
func firstInvalidSpan(spans []timeSpan) int {
	for i, s := range spans {
		if !validVideoSpan(s.Start, s.End) {
			return i
		}
	}
	return -1
}

// validVideoSpan reports whether [start, end) is a usable time span: both
// bounds are finite real numbers (rejecting NaN/±Inf decoded from JSON) and
// the end lies strictly after the (non-negative) start.
func validVideoSpan(start, end float64) bool {
	return start >= 0 && !math.IsInf(start, 0) && !math.IsInf(end, 0) && end > start
}

// handleVideoDeleteThumbnails deletes the thumbnail cache for a video.
func (h *Handler) handleVideoDeleteThumbnails(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}

	fs, absPath, hashHex, tDir, ok := h.thumbPathForRequest(w, r)
	if !ok {
		return
	}
	iFile := intervalsFile(absPath, hashHex)

	// Cancel any in-flight generation and wait for it to finish before wiping
	// the cache directory, otherwise the old generator can keep writing into
	// (or after) the freshly deleted directory and resurrect thumbnails.
	h.cancelThumbnailGeneration(hashHex)

	// A concurrent request may have started a fresh generation between the
	// cancel above and this wipe. Deleting now would destroy its freshly
	// created directory mid-flight (the new generator then fails and retries
	// forever), so refuse instead of racing it.
	if gen := h.getThumbGeneration(hashHex); gen != nil {
		writeError(w, http.StatusConflict, "thumbnail generation is in progress; try again")
		return
	}

	err := fs.RunAs(func() error {
		if err := os.RemoveAll(iFile); err != nil {
			return err
		}
		return removeThumbDir(tDir)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not delete thumbnails: "+err.Error())
		return
	}

	writeOK(w)
}

// handleVideoCancelThumbnails cancels any running thumbnail generation for a video.
func (h *Handler) handleVideoCancelThumbnails(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}

	fs, _, hashHex, tDir, ok := h.thumbPathForRequest(w, r)
	if !ok {
		return
	}

	// Remove from batch queue if queued (only the current user's bucket)
	h.removeFromBatchQueue(h.requestKey(r), hashHex)

	gen, finished := h.cancelThumbnailGeneration(hashHex)
	// Only delete partial thumbnails if generation never finished; a finished
	// (or never-started) generation leaves valid thumbnails in place.
	var cleanupDir string
	if gen != nil && !finished {
		cleanupDir = tDir
	}

	if cleanupDir != "" {
		if err := fs.RunAs(func() error {
			return removeThumbDir(cleanupDir)
		}); err != nil {
			log.Printf("video cancel: could not remove partial thumbnails %s: %v", tDir, err)
		}
	}

	writeOK(w)
}

// handleVideoSaveIntervals saves interval definitions for a video to a temp file.
func (h *Handler) handleVideoSaveIntervals(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path      string     `json:"path"`
		Intervals []timeSpan `json:"intervals"`
	}
	if !decodePostBody(w, r, &req) {
		return
	}
	if !requirePathField(w, req.Path) {
		return
	}

	fs := h.fsForRequest(r)
	_, iDir, iFile, ok := intervalsFilesForVideo(w, fs, req.Path)
	if !ok {
		return
	}

	if len(req.Intervals) == 0 {
		// No intervals — delete the file
		err := fs.RunAs(func() error {
			return os.RemoveAll(iFile)
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "could not delete intervals")
			return
		}
		writeOK(w)
		return
	}

	if len(req.Intervals) > maxVideoIntervals {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("too many intervals (max %d)", maxVideoIntervals))
		return
	}

	// Validate every interval up front so a corrupt or malformed request can
	// never be persisted and later crash the extraction path.
	if idx := firstInvalidSpan(req.Intervals); idx != -1 {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid interval %d: start must be non-negative and end must be a finite value after start", idx+1))
		return
	}

	data, err := json.Marshal(req.Intervals)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not encode intervals")
		return
	}
	err = fs.RunAs(func() error {
		// Write intervals via the shared atomic writer (temp + fsync + rename) so a
		// crash mid-write can never leave a truncated intervals file that would
		// later corrupt the extraction path.
		if err := os.MkdirAll(iDir, 0755); err != nil {
			return err
		}
		return atomicfile.Write(iFile, data, 0o644)
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not save intervals")
		return
	}

	writeOK(w)
}

// handleVideoLoadIntervals loads saved interval definitions for a video.
func (h *Handler) handleVideoLoadIntervals(w http.ResponseWriter, r *http.Request) {
	if !requireGet(w, r) {
		return
	}

	videoPath := requireQueryPath(w, r)
	if videoPath == "" {
		return
	}

	fs := h.fsForRequest(r)
	_, _, iFile, ok := intervalsFilesForVideo(w, fs, videoPath)
	if !ok {
		return
	}

	// No intervals saved yet (os.IsNotExist) is not an error: respond with an
	// empty list so the editor renders cleanly on the first visit. Any other
	// load failure is a real error.
	intervals := []timeSpan{}
	err := fs.RunAs(func() error {
		data, err := readBoundedFile(iFile, maxIntervalsBytes)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, &intervals)
	})
	if err != nil && !os.IsNotExist(err) {
		writeError(w, http.StatusInternalServerError, "could not load intervals")
		return
	}
	if intervals == nil {
		intervals = []timeSpan{}
	}

	writeJSON(w, map[string]any{"intervals": intervals})
}

// snapKeyframeTimes finds the nearest keyframe timestamps before and after the
// given time by scanning only a small window around the target, avoiding a
// full-file ffprobe scan that would be very slow on large videos.
func snapKeyframeTimes(ctx context.Context, fs *fslib.FS, path string, t float64) (prev, next float64, err error) {
	seekTo := math.Max(0, t-snapKeyframeWindow)
	readEnd := t + snapKeyframeSpread

	times, err := ffprobeFrameTimes(ctx, fs, path,
		"-ss", formatDuration(seekTo),
		"-read_intervals", fmt.Sprintf("%s-%s", formatDuration(seekTo), formatDuration(readEnd)),
	)
	if err != nil || len(times) == 0 {
		return t, t, err
	}

	// Keyframe PTS arrive in ascending decode order, so the nearest keyframe
	// at/before t is the rightmost entry not past t+0.001, and the nearest
	// keyframe at/after t is the leftmost entry not before t-0.001. Binary
	// search keeps both at O(log n) instead of a full scan.
	i := sort.Search(len(times), func(i int) bool { return times[i] > t+0.001 })
	if i > 0 {
		prev = times[i-1]
	} else {
		prev = times[0]
	}
	j := sort.Search(len(times), func(j int) bool { return times[j] >= t-0.001 })
	if j < len(times) {
		next = times[j]
	} else {
		next = times[len(times)-1]
	}
	return prev, next, nil
}

// handleExtractArchive extracts an archive file into a directory next to it.
// Streams NDJSON progress lines from the extraction tool.
func (h *Handler) handleExtractArchive(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path     string `json:"path"`
		Password string `json:"password"`
	}
	if !decodePostBody(w, r, &req) {
		return
	}
	if !requirePathField(w, req.Path) {
		return
	}

	// Check that at least one extraction tool is available and the format is supported
	fs := h.fsForRequest(r)
	absPath, err := fs.ResolvePath(req.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	toolName, err := h.detectArchiveTool(strings.ToLower(absPath))
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, errUnsupportedArchiveFormat) {
			code = http.StatusBadRequest
		}
		writeError(w, code, err.Error())
		return
	}

	// Streaming NDJSON response
	flushJSON, ok := ndjsonWriter(w, r)
	if !ok {
		return
	}

	pr, pw := io.Pipe()

	type extractResult struct {
		dest string
		err  error
	}
	extractDone := make(chan extractResult, 1)

	go func() {
		// pw is closed here so the scanner below sees EOF once extraction ends.
		// On client disconnect the main goroutine closes it earlier to unblock
		// this Writer; the request context then kills the extraction process.
		dest, err := fs.ExtractArchive(r.Context(), req.Path, req.Password, pw, toolName)
		pw.Close()
		extractDone <- extractResult{dest: dest, err: err}
	}()

	scanner := bufio.NewScanner(pr)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !flushJSON(map[string]any{
			"type":     "progress",
			"line":     line,
			"tool":     toolName,
			"filename": filepath.Base(req.Path),
		}) {
			// Client disconnected: close the writer so the extraction
			// goroutine's scan loop unblocks, then wait for it to finish so
			// neither the goroutine nor the extraction process is leaked.
			pw.Close()
			<-extractDone
			return
		}
	}

	if scanner.Err() != nil {
		// A huge probe line exceeded the scanner buffer. Keep draining pr so
		// the extraction goroutine (which writes into pw) can reach
		// extractDone; otherwise its Write blocks forever on a full pipe.
		io.Copy(io.Discard, pr)
	}

	result := <-extractDone
	if result.err != nil {
		flushJSON(map[string]any{
			"type":    "error",
			"message": result.err.Error(),
			"tool":    toolName,
		})
		return
	}

	flushJSON(map[string]any{
		"type":   "result",
		"status": "ok",
		"dest":   result.dest,
		"tool":   toolName,
	})
}

// errUnsupportedArchiveFormat marks archives whose format cannot be extracted.
var errUnsupportedArchiveFormat = errors.New("unsupported archive format")

// detectArchiveTool picks the extraction tool for an archive filename (already
// lowercased). It prefers the dedicated tool (unzip/unrar/tar) and falls back
// to 7z when available. The tar suffixes live in fslib (shared with the fs
// package's own destination-name logic) so the tools stay in sync.
func (h *Handler) detectArchiveTool(lower string) (string, error) {
	// A dedicated tool wins; 7z is the universal fallback for zip/rar, and a
	// double-checked label keeps the "not installable" message meaningful.
	tool := func(preferred bool, label string) (string, error) {
		if preferred {
			return label, nil
		}
		if h.tools["7z"] {
			return "7z", nil
		}
		return "", fmt.Errorf("no tool available for %s extraction (install %s or 7z)", label, label)
	}
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return tool(h.tools["unzip"], "unzip")
	case strings.HasSuffix(lower, ".rar"):
		return tool(h.tools["unrar"], "unrar")
	case strings.HasSuffix(lower, ".7z"):
		return tool(h.tools["7z"], "7z")
	case hasAnySuffix(lower, fslib.TarSuffixes):
		return tool(h.tools["tar"], "tar")
	}
	return "", errUnsupportedArchiveFormat
}

func hasAnySuffix(s string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

// handleVideoSnapTime snaps a time value to the nearest keyframe for clean -c copy extraction.
// Returns the snapped-to-previous-keyframe time (for start) and snapped-to-next-keyframe time (for end).
func (h *Handler) handleVideoSnapTime(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string  `json:"path"`
		Time float64 `json:"time"`
	}
	if !decodePostBody(w, r, &req) {
		return
	}
	if !requirePathField(w, req.Path) {
		return
	}
	// Reject NaN and ±Inf as well as negative times (defense in depth: those
	// literals cannot normally reach here because Go's JSON decoder rejects
	// them, but a non-finite time would flow through formatDuration whose
	// int() conversions then yield garbage).
	if req.Time < 0 || math.IsNaN(req.Time) || math.IsInf(req.Time, 0) {
		writeError(w, http.StatusBadRequest, "time must be a finite non-negative number")
		return
	}

	if !h.requireFFprobe(w) {
		return
	}

	fs := h.fsForRequest(r)
	absPath, _, ok := resolveVideoPathOrErr(w, fs, req.Path)
	if !ok {
		return
	}

	prev, next, err := snapKeyframeTimes(r.Context(), fs, absPath, req.Time)
	if err != nil {
		// Degrade gracefully (snap back to an unmodified time) but surface the
		// failure instead of swallowing it silently.
		log.Printf("snap keyframes for %s at %.3f: %v", req.Path, req.Time, err)
		writeJSON(w, map[string]any{
			"prev": req.Time,
			"next": req.Time,
		})
		return
	}

	writeJSON(w, map[string]any{
		"prev": prev,
		"next": next,
	})
}

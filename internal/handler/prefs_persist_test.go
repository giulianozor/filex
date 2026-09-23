package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/giulianozor/filex/internal/auth"
	"github.com/giulianozor/filex/internal/config"
	fslib "github.com/giulianozor/filex/internal/fs"
	"github.com/giulianozor/filex/internal/prefs"
	"golang.org/x/crypto/bcrypt"
)

// intPtr returns a pointer to v (for the optional uid/gid config fields).
func intPtr(v int) *int { return &v }

// TestPrefsPersistedToPrefsFile locks in that every UI-editable preference —
// favourites, show_dotfiles, protected paths and the secure-delete settings —
// is written into the user preferences file (~/.config/filex.yaml) under the
// logged-in user during an authenticated session, so the choices survive
// reloads and restarts.
func TestPrefsPersistedToPrefsFile(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	subdir := filepath.Join(dir, "docs")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Host: "0.0.0.0", Port: 8080, ShowDotfiles: false,
		Users: []config.User{{Username: "alice", BasePath: dir}},
	}
	users := BuildUserFS(cfg)
	hash, _ := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.DefaultCost)
	prefsPath := filepath.Join(t.TempDir(), "filex.yaml")
	prefsDoc := &prefs.Preferences{Users: []prefs.User{{Username: "alice", PasswordHash: string(hash)}}}
	if err := prefsDoc.Save(prefsPath); err != nil {
		t.Fatal(err)
	}
	h := New(fsys, cfg, http.Dir(dir), auth.NewStore(), users, "", map[string]string{"alice": prefsPath}, "test")

	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"alice","password":"s3cret"}`))
	login.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, login)
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set by login")
	}
	cookie := cookies[0]

	do := func(method, path, body string) int {
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		req.AddCookie(cookie)
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out.Code
	}

	for _, call := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/favourites/add", `{"path":"/docs","name":"Docs"}`},
		{http.MethodPost, "/api/protected/add", `{"path":"/docs"}`},
		{http.MethodPost, "/api/delete-prefs", `{"default_wipe":true,"wipe_method":"dod"}`},
		{http.MethodPost, "/api/show-dotfiles", `{"show_dotfiles":true}`},
	} {
		if c := do(call.method, call.path, call.body); c != http.StatusOK {
			t.Fatalf("%s %s = %d, want 200", call.method, call.path, c)
		}
	}

	reloaded, err := prefs.Load(prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	alice := reloaded.FindUser("alice")
	if alice == nil {
		t.Fatal("alice user missing from prefs file")
	}
	if !slices.ContainsFunc(alice.Favourites, func(f config.Favourite) bool { return f.Path == "/docs" }) {
		t.Errorf("favourites not persisted to prefs file: %+v", alice.Favourites)
	}
	if len(alice.ProtectedPaths) != 1 || alice.ProtectedPaths[0] != "/docs" {
		t.Errorf("protected paths not persisted to prefs file: %v", alice.ProtectedPaths)
	}
	if !alice.Deletion.DefaultWipe || alice.Deletion.WipeMethod != "dod" {
		t.Errorf("delete prefs not persisted to prefs file: %+v", alice.Deletion)
	}
	if alice.ShowDotfiles == nil || !*alice.ShowDotfiles {
		t.Errorf("show_dotfiles not persisted to prefs file: %v", alice.ShowDotfiles)
	}
}

// TestPrefsFileOwnedByConfiguredUser locks in that a root daemon writing a
// user's preferences leaves the file owned by that user's uid/gid rather than
// by root.
func TestPrefsFileOwnedByConfiguredUser(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Host: "0.0.0.0", Port: 8080, ShowDotfiles: false,
		Users: []config.User{{Username: "alice", BasePath: dir, UID: intPtr(os.Getuid()), GID: intPtr(os.Getgid())}},
	}
	users := BuildUserFS(cfg)
	prefsPath := filepath.Join(t.TempDir(), "filex.yaml")
	hash, _ := bcrypt.GenerateFromPassword([]byte("s3cret"), bcrypt.DefaultCost)
	if err := (&prefs.Preferences{Users: []prefs.User{{Username: "alice", PasswordHash: string(hash)}}}).Save(prefsPath); err != nil {
		t.Fatal(err)
	}
	h := New(fsys, cfg, http.Dir(dir), auth.NewStore(), users, "", map[string]string{"alice": prefsPath}, "test")

	login := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"alice","password":"s3cret"}`))
	login.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, login)
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set by login")
	}
	req := httptest.NewRequest(http.MethodPost, "/api/favourites/add", strings.NewReader(`{"path":"/","name":"Home"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(cookies[0])
	h.ServeHTTP(httptest.NewRecorder(), req)

	info, err := os.Stat(prefsPath)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skipf("no syscall.Stat_t on this platform (got %T)", info.Sys())
	}
	if st.Uid != uint32(os.Getuid()) || st.Gid != uint32(os.Getgid()) {
		t.Errorf("prefs owner = %d:%d, want %d:%d", st.Uid, st.Gid, os.Getuid(), os.Getgid())
	}
}

// TestShowDotfiles_NoAuthPersistsToConfig locks in that in no-auth mode the
// show-dotfiles toggle updates the server default in config.yaml (there is no
// per-user identity to attach the preference to).
func TestShowDotfiles_NoAuthPersistsToConfig(t *testing.T) {
	dir := t.TempDir()
	fsys, err := fslib.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config.yaml")
	cfg := &config.Config{Host: "0.0.0.0", Port: 8080, BasePath: dir, ShowDotfiles: false}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	h := New(fsys, cfg, http.Dir(dir), nil, nil, cfgPath, nil, "test")

	rr := doRequest(t, h, http.MethodPost, "/api/show-dotfiles", strings.NewReader(`{"show_dotfiles":true}`))
	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/show-dotfiles = %d, want 200 (%s)", rr.Code, rr.Body.String())
	}

	saved, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !saved.ShowDotfiles {
		t.Error("show_dotfiles not persisted to config.yaml in no-auth mode")
	}
}

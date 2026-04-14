package main

import (
"flag"
"fmt"
"log"
"net/http"
"os"
"path/filepath"

authlib "github.com/giulianozor/filex/internal/auth"
"github.com/giulianozor/filex/internal/config"
fslib "github.com/giulianozor/filex/internal/fs"
"github.com/giulianozor/filex/internal/handler"
"github.com/giulianozor/filex/web"
)

// Version is set at build time via -ldflags.
var Version = "dev"

// checkPrivileges warns if per-user UID/GID switching is configured but the
// process is not running as root.  setreuid/setregid require root (or
// CAP_SETUID/CAP_SETGID) and will fail with EPERM otherwise, which results in
// users seeing a "setegid: operation not permitted" error after login.
func checkPrivileges(cfg *config.Config) {
	if os.Geteuid() == 0 {
		return
	}
	var affected []string
	for _, u := range cfg.Users {
		if u.UID != nil || u.GID != nil {
			affected = append(affected, u.Username)
		}
	}
	if len(affected) > 0 {
		log.Printf("WARNING: users %v have uid/gid configured but filex is not running as root (euid=%d). "+
			"Per-user credential switching (setreuid/setregid) will fail with EPERM. "+
			"Start filex as root when per-user uid/gid is configured.", affected, os.Geteuid())
	}
}

func main() {
configPath := flag.String("config", "", "path to config.yaml (optional)")
showVersion := flag.Bool("version", false, "print version and exit")
flag.Parse()

if *showVersion {
fmt.Println("filex", Version)
os.Exit(0)
}

cfg, err := config.Load(*configPath)
if err != nil {
log.Fatalf("Failed to load config: %v", err)
}

checkPrivileges(cfg)

// Build global FS (used when auth is disabled).
globalFS, err := fslib.New(cfg.BasePath)
if err != nil {
log.Fatalf("Failed to initialize filesystem: %v", err)
}

// Build per-user FS map.
userFSMap := handler.BuildUserFS(cfg)

staticFS, err := web.StaticFS()
if err != nil {
log.Fatalf("Failed to load static assets: %v", err)
}

// Derive the session file path from the config path so persistent (remember-me)
// sessions survive service restarts.
sessionPath := ""
if *configPath != "" {
sessionPath = filepath.Join(filepath.Dir(*configPath), "filex_sessions.json")
}
authStore := authlib.NewStoreWithPath(sessionPath)

h := handler.New(globalFS, cfg, staticFS, authStore, userFSMap, *configPath)

addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
if cfg.AuthRequired() {
log.Printf("filex %s starting on http://%s  base=%s  auth=enabled  users=%d",
Version, addr, cfg.BasePath, len(cfg.Users))
} else {
log.Printf("filex %s starting on http://%s  base=%s  auth=disabled",
Version, addr, cfg.BasePath)
}

if err := http.ListenAndServe(addr, h); err != nil {
log.Fatalf("Server error: %v", err)
}
}

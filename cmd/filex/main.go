package main

import (
"flag"
"fmt"
"log"
"net/http"
"os"

authlib "github.com/giulianozor/filex/internal/auth"
"github.com/giulianozor/filex/internal/config"
fslib "github.com/giulianozor/filex/internal/fs"
"github.com/giulianozor/filex/internal/handler"
"github.com/giulianozor/filex/web"
)

// Version is set at build time via -ldflags.
var Version = "dev"

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

authStore := authlib.NewStore()

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

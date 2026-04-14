package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

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

	filesystem, err := fslib.New(cfg.BasePath)
	if err != nil {
		log.Fatalf("Failed to initialize filesystem: %v", err)
	}

	staticFS, err := web.StaticFS()
	if err != nil {
		log.Fatalf("Failed to load static assets: %v", err)
	}

	h := handler.New(filesystem, cfg, staticFS)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("filex %s starting on http://%s  base=%s", Version, addr, cfg.BasePath)

	if err := http.ListenAndServe(addr, h); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

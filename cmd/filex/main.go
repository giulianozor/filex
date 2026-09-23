package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

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
	sampleConfig := flag.String("sample-config", "", "write a no-auth sample config to this path and exit")
	sampleConfigAuth := flag.String("sample-config-auth", "", "write an auth sample config to this path and exit")
	flag.Parse()

	if flag.NArg() > 0 {
		fmt.Fprintln(os.Stderr, "usage: filex [-config path] [-version] [-sample-config path] [-sample-config-auth path]")
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println("filex", Version)
		os.Exit(0)
	}

	if *sampleConfig != "" || *sampleConfigAuth != "" {
		if *sampleConfig != "" && *sampleConfigAuth != "" {
			fmt.Fprintln(os.Stderr, "-sample-config and -sample-config-auth are mutually exclusive")
			os.Exit(2)
		}
		path, mode := *sampleConfig, "no-auth"
		if *sampleConfigAuth != "" {
			path, mode = *sampleConfigAuth, "auth"
		}
		backup, err := config.WriteSample(path, *sampleConfigAuth != "")
		if err != nil {
			log.Fatalf("Failed to write sample config: %v", err)
		}
		if backup != "" {
			log.Printf("Existing config backed up to %s", backup)
		}
		log.Printf("Sample %s config written to %s", mode, path)
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

	// Per-user preferences (password hashes, favourites, protected paths, wipe
	// defaults) live in each user's own <home>/.config/filex.yaml so config.yaml
	// can stay read-only and a root-managed daemon never writes into its own
	// ~root home. A missing preferences file is fine; the server creates each
	// user's file on first save.
	prefsPaths := handler.BuildPrefsPaths(cfg)

	h := handler.New(globalFS, cfg, staticFS, authStore, userFSMap, *configPath, prefsPaths, Version)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	authMode := "auth=disabled"
	if cfg.AuthRequired() {
		authMode = fmt.Sprintf("auth=enabled  users=%d", len(cfg.Users))
	}
	log.Printf("filex %s starting on http://%s  base=%s  %s", Version, addr, cfg.BasePath, authMode)

	// Standard timeouts only guard the request header and idle connections;
	// the request-read and response-write phases are unbounded on purpose.
	// Uploads support files up to 16 GiB (chunked in 16 MiB parts) and NDJSON
	// operations (wipe, move/copy, archive extract, zip download) stream for
	// minutes — a fixed ReadTimeout/WriteTimeout would abort all of them
	// mid-flight on slower connections.
	server := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	log.Printf("listening on %s", addr)

	// Install the signal handler before the listener starts so a signal that
	// arrives in the startup window is not lost.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server error: %v", err)
		}
	case <-ctx.Done():
		log.Println("Shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("Graceful shutdown failed: %v", err)
		}
		// Drain the listener error if it already arrived; never block here, or
		// a Shutdown timeout (which leaves the listener running) would turn a
		// failed shutdown into a hang.
		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("Server error during shutdown: %v", err)
			}
		default:
		}
	}
}

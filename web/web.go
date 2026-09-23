// Package web exposes the embedded static web assets.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var Files embed.FS

// StaticFS returns an http.FileSystem rooted at the static/ directory.
func StaticFS() (http.FileSystem, error) {
	sub, err := fs.Sub(Files, "static")
	if err != nil {
		return nil, err
	}
	return http.FS(sub), nil
}

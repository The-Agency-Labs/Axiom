// Package web embeds the built SPA. The Docker build copies frontend/build
// into web/dist; in development an empty placeholder is used.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Files returns the SPA build.
func Files() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

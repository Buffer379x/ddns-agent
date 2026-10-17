package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// FS contains only compiled assets; source files and dependencies are never served.
var FS = func() fs.FS {
	root, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return root
}()

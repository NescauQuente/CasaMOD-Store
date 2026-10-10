// Package web holds the static web UI, embedded in the binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS is the web UI root.
var FS, _ = fs.Sub(files, "static")

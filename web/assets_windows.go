package webassets

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

func Frontend() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}

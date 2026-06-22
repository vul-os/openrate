// Package web embeds the built Vite/React app so the openrate binary serves its
// UI with no separate Node runtime. Run `npm --prefix web run build` to refresh
// dist/. A placeholder dist/index.html is committed so the binary builds and
// runs a working UI even before the React app is built.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

// FS returns the built site rooted at dist/.
func FS() (fs.FS, error) {
	return fs.Sub(assets, "dist")
}

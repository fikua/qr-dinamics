// Package brand serves the Fikua visual identity shared by every page: the
// colour tokens and base styles (brand.css), the self-hosted Inter font
// (no third-party font requests) and the Fikua icon.
package brand

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed assets
var assets embed.FS

// FontLicense is the Inter font licence (SIL OFL 1.1), shown on the
// third-party licences page as the licence requires.
func FontLicense() string {
	b, _ := assets.ReadFile("assets/inter-LICENSE.txt")
	return string(b)
}

// CSS returns brand.css, for tests.
func CSS() []byte {
	b, _ := assets.ReadFile("assets/brand.css")
	return b
}

// Routes serves the assets under /brand/.
func Routes(mux *http.ServeMux) {
	sub, _ := fs.Sub(assets, "assets")
	files := http.StripPrefix("/brand/", http.FileServerFS(sub))
	mux.Handle("GET /brand/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		files.ServeHTTP(w, r)
	}))
}

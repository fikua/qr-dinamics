// Package brand serves the Fikua visual identity shared by every page: the
// colour tokens and base styles (brand.css), the self-hosted Inter font
// (no third-party font requests) and the Fikua icon.
package brand

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"html/template"
	"io/fs"
	"net/http"
)

//go:embed assets
var assets embed.FS

// Version changes whenever any brand asset changes. Pages add it to asset
// URLs (?v=...) so browsers and the Cloudflare edge never serve stale files.
var Version = func() string {
	var parts [][]byte
	fs.WalkDir(assets, "assets", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := assets.ReadFile(path)
			parts = append(parts, []byte(path), b)
		}
		return nil
	})
	return Hash(parts...)
}()

// Hash returns a short content hash for cache-busting asset URLs.
func Hash(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))[:10]
}

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

// TemplateFuncs are shared by every page template. emailOff/emailOn wrap the
// page body in Cloudflare's markers so its Email Obfuscation leaves our
// addresses alone: it replaces them with "[email protected]" and a script
// that our Content-Security-Policy blocks, hiding the contact address.
// html/template strips comments, so the markers are emitted as trusted HTML.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"emailOff": func() template.HTML { return "<!--email_off-->" },
		"emailOn":  func() template.HTML { return "<!--/email_off-->" },
	}
}

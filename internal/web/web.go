// Package web renders the panel's HTML pages and serves its static files.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"qrdinamics/internal/brand"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Renderer renders pages: each page template is parsed together with the layout.
type Renderer struct {
	pages        map[string]*template.Template
	contactEmail string
}

// appCSSVersion versions /static/app.css in page links (cache busting).
var appCSSVersion = func() string {
	b, _ := staticFS.ReadFile("static/app.css")
	return brand.Hash(b)
}()

var funcs = template.FuncMap{
	"brandv": func() string { return brand.Version },
	"appv":   func() string { return appCSSVersion },
	"date":   func(t time.Time) string { return t.Format("02/01/2006") },
	"datetime": func(t time.Time) string {
		return t.Local().Format("02/01/2006 15:04")
	},
	"day": func(t time.Time) string { return t.Format("02/01") },
	"mul": func(a, b int) int { return a * b },
	"sub": func(a, b int64) int64 { return a - b },
	"pct": func(n, max int64) int64 {
		if max == 0 {
			return 0
		}
		return n * 100 / max
	},
}

// NewRenderer parses every page template. contactEmail goes in every footer.
func NewRenderer(contactEmail string) (*Renderer, error) {
	layout, err := template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}
	names, err := fs.Glob(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	r := &Renderer{pages: map[string]*template.Template{}, contactEmail: contactEmail}
	for _, n := range names {
		name := strings.TrimSuffix(strings.TrimPrefix(n, "templates/"), ".html")
		if name == "layout" {
			continue
		}
		t, err := template.Must(layout.Clone()).ParseFS(templateFS, n)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", n, err)
		}
		r.pages[name] = t
	}
	return r, nil
}

// Page is the data every page gets.
type Page struct {
	Title        string
	ContactEmail string // set by Render, shown in the footer
	Email        string // signed-in user, empty when anonymous
	Error        string
	Notice       string
	Data         any
}

// Render writes a page with the given status.
func (r *Renderer) Render(w http.ResponseWriter, status int, name string, p Page) {
	t, ok := r.pages[name]
	if !ok {
		slog.Error("unknown template", "name", name)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	p.ContactEmail = r.contactEmail
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", p); err != nil {
		slog.Error("render template", "name", name, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// Static serves the embedded static files under /static/.
func Static() http.Handler {
	sub, _ := fs.Sub(staticFS, "static")
	return http.StripPrefix("/static/", http.FileServerFS(sub))
}

// SecurityHeaders sets the headers every panel response carries.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

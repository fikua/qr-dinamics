// Package landing serves the product's public landing page at the root of the
// redirect host. Every claim on it must be true of the running product: no
// testimonials, counters, countdowns or promises the code does not keep.
package landing

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"

	"qrdinamics/internal/brand"
	"qrdinamics/internal/qrcodes"
)

//go:embed landing.html landing.css
var files embed.FS

// Info configures the page.
type Info struct {
	PublicURL    string // this host, e.g. https://qr.fikua.com
	SignupURL    string // panel sign-up; empty while the beta is closed
	ContactEmail string
}

// Handler serves /, /landing/landing.css and /landing/qr.svg.
type Handler struct {
	page   []byte
	css    []byte
	demoQR []byte
}

type pageData struct {
	Info
	BetaMailto   string
	BrandV, CSSV string // asset versions for cache busting
}

// NewHandler renders the page once; it never changes at runtime.
func NewHandler(info Info) (*Handler, error) {
	t, err := template.New("landing.html").Funcs(brand.TemplateFuncs()).ParseFS(files, "landing.html")
	if err != nil {
		return nil, err
	}
	mailto := fmt.Sprintf("mailto:%s?subject=%s&body=%s", info.ContactEmail,
		url.PathEscape("Accés a la beta de QR dinàmics"),
		url.PathEscape("Hola! M'agradaria provar QR dinàmics.\n\nNegoci:\nPer a què el faria servir:\n"))
	css, err := files.ReadFile("landing.css")
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, pageData{Info: info, BetaMailto: mailto, BrandV: brand.Version, CSSV: brand.Hash(css)}); err != nil {
		return nil, err
	}
	// A real, scannable code: it encodes the landing page itself.
	demo, err := qrcodes.RenderSVG(info.PublicURL)
	if err != nil {
		return nil, err
	}
	return &Handler{page: buf.Bytes(), css: css, demoQR: demo}, nil
}

// Routes registers the landing routes.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.serve("text/html; charset=utf-8", h.page, "public, max-age=300"))
	mux.HandleFunc("GET /landing/landing.css", h.serve("text/css; charset=utf-8", h.css, "public, max-age=3600"))
	mux.HandleFunc("GET /landing/qr.svg", h.serve("image/svg+xml", h.demoQR, "public, max-age=86400"))
}

func (h *Handler) serve(ctype string, body []byte, cache string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Content-Type", ctype)
		hd.Set("Cache-Control", cache)
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		hd.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		if _, err := w.Write(body); err != nil {
			slog.Debug("write landing", "err", err)
		}
	}
}

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
	"qrdinamics/internal/i18n"
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
	pages  map[i18n.Lang][]byte // the page, rendered once per language
	css    []byte
	demoQR []byte
}

type pageData struct {
	Info
	Lang         i18n.Lang
	BetaMailto   string
	BrandV, CSSV string // asset versions for cache busting
}

// NewHandler renders the page once per language; it never changes at runtime.
func NewHandler(info Info) (*Handler, error) {
	css, err := files.ReadFile("landing.css")
	if err != nil {
		return nil, err
	}
	h := &Handler{pages: map[i18n.Lang][]byte{}, css: css}
	for _, lang := range i18n.All {
		t, err := template.New("landing.html").Funcs(brand.TemplateFuncs()).Funcs(i18n.Funcs(lang)).ParseFS(files, "landing.html")
		if err != nil {
			return nil, err
		}
		mailto := fmt.Sprintf("mailto:%s?subject=%s&body=%s", info.ContactEmail,
			url.PathEscape(i18n.T(lang, "Accés a la beta de QR dinàmics")),
			url.PathEscape(i18n.T(lang, "Hola! M'agradaria provar QR dinàmics.")+"\n\n"+i18n.T(lang, "Negoci:")+"\n"+i18n.T(lang, "Per a què el faria servir:")+"\n"))
		var buf bytes.Buffer
		if err := t.Execute(&buf, pageData{Info: info, Lang: lang, BetaMailto: mailto, BrandV: brand.Version, CSSV: brand.Hash(css)}); err != nil {
			return nil, err
		}
		h.pages[lang] = buf.Bytes()
	}
	// A real, scannable code: it encodes the landing page itself.
	h.demoQR, err = qrcodes.RenderSVG(info.PublicURL)
	if err != nil {
		return nil, err
	}
	return h, nil
}

// Routes registers the landing routes.
func (h *Handler) Routes(mux *http.ServeMux) {
	// The page depends on the visitor's language, so shared caches must not keep it.
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Vary", "Cookie, Accept-Language")
		h.serve("text/html; charset=utf-8", h.pages[i18n.FromRequest(r)], "private, max-age=300")(w, r)
	})
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

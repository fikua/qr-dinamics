// Package legal serves the legal pages (legal notice, privacy policy, terms,
// cookie policy, third-party licences). Both binaries mount it: the panel
// for its users and the public redirect host for people who scan a code.
package legal

import (
	"bytes"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"qrdinamics/internal/brand"
)

// Version is the version of the terms and privacy policy. Keep it equal to
// accounts.LegalVersion: accounts record which version each owner accepted.
const Version = "2026-11-12"

// Retention values shown in the privacy policy. They mirror the constants
// in internal/privacy (tested there).
const (
	ScanRetentionMonths   = 24
	UnverifiedAccountDays = 30
	SessionDays           = 30
	BackupDays            = 30
)

// Holder identifies who provides the service (LSSI art. 10). While any field
// is empty the pages say the data is pending, and the panel must stay
// closed (config refuses a public panel without it).
type Holder struct {
	Name    string
	TaxID   string
	Address string
}

// Complete reports whether every mandatory field is set.
func (h Holder) Complete() bool { return h.Name != "" && h.TaxID != "" && h.Address != "" }

// Info is what the pages need to know about the deployment.
type Info struct {
	Holder              Holder
	ContactEmail        string
	PanelURL            string
	RedirectURL         string
	SafeBrowsingEnabled bool
}

//go:embed templates/*.html
var templateFS embed.FS

//go:embed legal.css
var css []byte

//go:embed third_party_licenses.txt
var thirdPartyLicenses string

// Page is one legal page.
type Page struct {
	Slug  string
	Title string
}

// Pages lists the legal pages in footer order.
var Pages = []Page{
	{"avis-legal", "Avís legal"},
	{"privacitat", "Política de privacitat"},
	{"termes", "Termes i condicions"},
	{"cookies", "Política de cookies"},
	{"llicencies", "Llicències de tercers"},
}

// Handler serves /legal/{page} and /legal/legal.css.
type Handler struct {
	info  Info
	pages map[string]*template.Template
}

type pageData struct {
	Info
	Page               Page
	Pages              []Page
	Version            string
	Retention          map[string]int
	ThirdPartyLicenses string
	FontLicense        string
	Year               int
}

// NewHandler parses the page templates.
func NewHandler(info Info) (*Handler, error) {
	h := &Handler{info: info, pages: map[string]*template.Template{}}
	for _, p := range Pages {
		t, err := template.ParseFS(templateFS, "templates/layout.html", "templates/"+p.Slug+".html")
		if err != nil {
			return nil, err
		}
		h.pages[p.Slug] = t
	}
	return h, nil
}

// Routes registers the legal routes.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /legal/legal.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Write(css)
	})
	mux.HandleFunc("GET /legal/{page}", h.page)
	mux.HandleFunc("GET /legal/{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/legal/avis-legal", http.StatusSeeOther)
	})
}

func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("page")
	t, ok := h.pages[slug]
	if !ok {
		http.NotFound(w, r)
		return
	}
	var page Page
	for _, p := range Pages {
		if p.Slug == slug {
			page = p
		}
	}
	data := pageData{
		Info: h.info, Page: page, Pages: Pages, Version: Version,
		Retention: map[string]int{
			"ScanMonths": ScanRetentionMonths, "UnverifiedDays": UnverifiedAccountDays,
			"SessionDays": SessionDays, "BackupDays": BackupDays,
		},
		ThirdPartyLicenses: thirdPartyLicenses,
		FontLicense:        brand.FontLicense(),
		Year:               time.Now().Year(),
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "layout.html", data); err != nil {
		slog.Error("render legal page", "page", slug, "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; font-src 'self'; img-src 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	buf.WriteTo(w)
}

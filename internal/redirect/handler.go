package redirect

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"qrdinamics/internal/audit"
	"qrdinamics/internal/brand"
	"qrdinamics/internal/health"
	"qrdinamics/internal/i18n"
	"qrdinamics/internal/qrcodes"
)

// Handler answers scans.
type Handler struct {
	Table         *Table
	Recorder      *Recorder
	CountryHeader string
	ContactEmail  string
	Now           func() time.Time
}

// Routes registers the redirect routes.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /{code}", h.redirect)
}

// redirect answers GET /{code} with a 302 to the current destination (RF-01);
// any other case gets the neutral page (RF-02).
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !qrcodes.ValidCode(code) {
		audit.Count(r.Context(), "scan", "unknown", "")
		h.neutral(w, r, http.StatusNotFound)
		return
	}
	e, ok := h.Table.Lookup(code)
	if !ok || e.Status != string(qrcodes.Active) {
		outcome := "unknown"
		if ok && (e.Status == string(qrcodes.Paused) || e.Status == string(qrcodes.Blocked)) {
			outcome = e.Status
		}
		audit.Count(r.Context(), "scan", outcome, "")
		h.neutral(w, r, http.StatusNotFound)
		return
	}
	if r.Method == http.MethodGet {
		if device := DeviceType(r.UserAgent()); device != "bot" {
			country := ""
			if h.CountryHeader != "" {
				country = Country(r.Header.Get(h.CountryHeader))
			}
			h.Recorder.Record(Scan{QRID: e.ID, At: h.Now().UTC(), Country: country, Device: device})
		}
	}
	audit.Count(r.Context(), "scan", "redirected", "")
	// 302, never 301, and no-store: browsers must not remember the destination.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, e.Destination, http.StatusFound)
}

// health: healthy once the table was loaded from the database or a snapshot,
// even with no codes yet; never loaded means it cannot answer for any code.
// It does not depend on the database on purpose (codes keep resolving while
// it is down).
func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	health.Handler("", map[string]health.Check{"table:loaded": func(context.Context) error {
		if !h.Table.Loaded() {
			return errNotLoaded
		}
		return nil
	}})(w, r)
}

var errNotLoaded = errors.New("redirect table not loaded")

var neutralPage = template.Must(template.New("neutral").Funcs(brand.TemplateFuncs()).Funcs(i18n.Funcs(i18n.CA)).Parse(`<!doctype html>
<html lang="{{.Lang.Locale}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{t "Codi QR no disponible"}}</title>
<link rel="icon" href="/brand/fikua-icon.svg" type="image/svg+xml">
<link rel="stylesheet" href="/brand/brand.css?v={{.BrandV}}">
<style>
main{min-height:calc(100vh - 4px);display:grid;place-items:center;padding:16px}
.box{max-width:440px;text-align:center}
.box img{width:56px;height:56px}
h1{font-size:24px;margin:16px 0 8px}
p{color:var(--muted)}
nav.lang{font-size:14px}nav.lang a{color:var(--muted)}nav.lang a[aria-current]{color:var(--text);font-weight:600;text-decoration:none}
</style>
</head>
<body>
{{emailOff}}
<main><div class="box">
<img src="/brand/fikua-icon.svg" alt="">
<h1>{{t "Aquest codi QR no està disponible"}}</h1>
<p>{{t "El codi que has escanejat no porta enlloc ara mateix. Pot ser que l'hagin pausat o retirat."}}</p>
<p><small><a href="mailto:{{.Email}}?subject={{.Subject}}">{{t "Denunciar un ús abusiu"}}</a> · <a href="/legal/privacitat">{{t "Privacitat"}}</a> · <a href="/legal/avis-legal">{{t "Avís legal"}}</a></small></p>
<p><small>{{t "Aquesta pàgina no fa servir cookies."}}</small></p>
<nav class="lang" aria-label="{{t "Idioma"}}">{{range langs}}<a href="/lang/{{.}}" lang="{{.}}" hreflang="{{.}}"{{if eq . $.Lang}} aria-current="true"{{end}}>{{.Name}}</a> {{end}}</nav>
</div></main>
{{emailOn}}
</body>
</html>`))

func (h *Handler) neutral(w http.ResponseWriter, r *http.Request, status int) {
	lang := i18n.FromRequest(r)
	t, err := neutralPage.Clone()
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; font-src 'self'; img-src 'self'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	t.Funcs(i18n.Funcs(lang)).Execute(w, struct {
		Email, BrandV, Subject string
		Lang                   i18n.Lang
	}{h.ContactEmail, brand.Version, url.PathEscape(i18n.T(lang, "Denúncia QR")), lang})
}

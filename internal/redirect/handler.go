package redirect

import (
	"html/template"
	"net/http"
	"time"

	"qrdinamics/internal/qrcodes"
)

// Handler answers scans.
type Handler struct {
	Table         *Table
	Recorder      *Recorder
	CountryHeader string
	AbuseEmail    string
	Now           func() time.Time
}

// Routes registers the redirect routes.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.home)
	mux.HandleFunc("GET /healthz", h.health)
	mux.HandleFunc("GET /{code}", h.redirect)
}

// redirect answers GET /{code} with a 302 to the current destination (RF-01);
// any other case gets the neutral page (RF-02).
func (h *Handler) redirect(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if !qrcodes.ValidCode(code) {
		h.neutral(w, http.StatusNotFound)
		return
	}
	e, ok := h.Table.Lookup(code)
	if !ok || e.Status != string(qrcodes.Active) {
		h.neutral(w, http.StatusNotFound)
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
	// 302, never 301, and no-store: browsers must not remember the destination.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, e.Destination, http.StatusFound)
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request) {
	h.neutral(w, http.StatusOK)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Table.Len() == 0 {
		http.Error(w, "redirect table empty", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok"))
}

var neutralPage = template.Must(template.New("neutral").Parse(`<!doctype html>
<html lang="ca">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Codi QR no disponible</title>
<style>
body{margin:0;font:16px/1.5 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;background:#f6f7f9;color:#1b1f24;display:grid;place-items:center;min-height:100vh;padding:16px;box-sizing:border-box}
main{max-width:420px;text-align:center}
h1{font-size:22px}
p{color:#5c6670}
a{color:#1f5fd1}
@media (prefers-color-scheme:dark){body{background:#111418;color:#e7eaee}p{color:#9aa4ae}a{color:#5b93f0}}
</style>
</head>
<body>
<main>
<h1>Aquest codi QR no està disponible</h1>
<p>El codi que has escanejat no porta enlloc ara mateix. Pot ser que l'hagin pausat o retirat.</p>
<p lang="es">Este código QR no está disponible en este momento.</p>
<p><small><a href="mailto:{{.}}?subject=Denuncia%20QR">Denunciar un ús abusiu</a></small></p>
</main>
</body>
</html>`))

func (h *Handler) neutral(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	neutralPage.Execute(w, h.AbuseEmail)
}

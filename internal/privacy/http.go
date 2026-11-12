package privacy

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"qrdinamics/internal/accounts"
	"qrdinamics/internal/web"
)

// Handler serves the account page: data export, account deletion and
// marketing consent.
type Handler struct {
	svc             *Service
	accounts        *accounts.Service
	view            *web.Renderer
	redirectBaseURL string
	logout          func(http.ResponseWriter)
}

// NewHandler builds the handler. logout clears the session cookie after the
// account is deleted.
func NewHandler(svc *Service, acc *accounts.Service, view *web.Renderer, redirectBaseURL string, logout func(http.ResponseWriter)) *Handler {
	return &Handler{svc: svc, accounts: acc, view: view, redirectBaseURL: redirectBaseURL, logout: logout}
}

// Routes registers the account routes behind auth.
func (h *Handler) Routes(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	mux.Handle("GET /compte", auth(http.HandlerFunc(h.page)))
	mux.Handle("POST /compte/marketing", auth(http.HandlerFunc(h.marketing)))
	mux.Handle("GET /compte/dades.json", auth(http.HandlerFunc(h.exportJSON)))
	mux.Handle("GET /compte/qr.csv", auth(http.HandlerFunc(h.exportCSV)))
	mux.Handle("POST /compte/esborra", auth(http.HandlerFunc(h.deleteAccount)))
	mux.HandleFunc("GET /compte/esborrat", func(w http.ResponseWriter, r *http.Request) {
		h.view.Render(w, http.StatusOK, "account_deleted", web.Page{Title: "Compte esborrat"})
	})
}

type pageData struct {
	Account   accounts.Account
	Marketing bool
	Consents  []accounts.Consent
}

func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, "", noticeFor(r))
}

func noticeFor(r *http.Request) string {
	switch r.URL.Query().Get("saved") {
	case "marketing-on":
		return "Rebràs les novetats del producte. Pots canviar-ho quan vulguis."
	case "marketing-off":
		return "No rebràs més novetats del producte."
	}
	return ""
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, errMsg, notice string) {
	a, _ := accounts.FromContext(r.Context())
	marketing, err := h.accounts.Marketing(r.Context(), a.ID)
	if err != nil {
		h.fail(w, "load marketing consent", err)
		return
	}
	consents, err := h.accounts.Consents(r.Context(), a.ID)
	if err != nil {
		h.fail(w, "load consents", err)
		return
	}
	h.view.Render(w, status, "account", web.Page{
		Title: "El teu compte", Email: a.Email, Error: errMsg, Notice: notice,
		Data: pageData{Account: a, Marketing: marketing, Consents: consents},
	})
}

func (h *Handler) marketing(w http.ResponseWriter, r *http.Request) {
	a, _ := accounts.FromContext(r.Context())
	on := r.PostFormValue("marketing") == "on"
	if err := h.accounts.SetMarketing(r.Context(), a.ID, on); err != nil {
		h.fail(w, "set marketing consent", err)
		return
	}
	flag := "marketing-off"
	if on {
		flag = "marketing-on"
	}
	http.Redirect(w, r, "/compte?saved="+flag, http.StatusSeeOther)
}

func (h *Handler) export(w http.ResponseWriter, r *http.Request) (Export, bool) {
	a, _ := accounts.FromContext(r.Context())
	e, err := h.svc.Export(r.Context(), a.ID, h.redirectBaseURL)
	if err != nil {
		h.fail(w, "export data", err)
		return Export{}, false
	}
	return e, true
}

func (h *Handler) exportJSON(w http.ResponseWriter, r *http.Request) {
	e, ok := h.export(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="qr-dinamics-dades-`+time.Now().Format("2006-01-02")+`.json"`)
	w.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil {
		slog.Error("write export", "err", err)
	}
}

func (h *Handler) exportCSV(w http.ResponseWriter, r *http.Request) {
	e, ok := h.export(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="qr-dinamics-codis-`+time.Now().Format("2006-01-02")+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	if err := e.WriteCSV(w); err != nil {
		slog.Error("write csv export", "err", err)
	}
}

// deleteAccount asks for the password and the word ESBORRA, so a stolen
// session or a slip cannot erase an account.
func (h *Handler) deleteAccount(w http.ResponseWriter, r *http.Request) {
	a, _ := accounts.FromContext(r.Context())
	if strings.TrimSpace(r.PostFormValue("confirm")) != "ESBORRA" {
		h.render(w, r, http.StatusUnprocessableEntity, "Escriu ESBORRA per confirmar que vols esborrar el compte.", "")
		return
	}
	err := h.accounts.CheckPassword(r.Context(), a.ID, r.PostFormValue("password"))
	if errors.Is(err, accounts.ErrInvalidCredentials) {
		h.render(w, r, http.StatusUnprocessableEntity, "La contrasenya no és correcta.", "")
		return
	}
	if err != nil {
		h.fail(w, "check password", err)
		return
	}
	if err := h.svc.DeleteAccount(r.Context(), a.ID); err != nil {
		h.fail(w, "delete account", err)
		return
	}
	h.logout(w)
	http.Redirect(w, r, "/compte/esborrat", http.StatusSeeOther)
}

func (h *Handler) fail(w http.ResponseWriter, op string, err error) {
	slog.Error(op, "err", err)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

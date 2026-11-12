package qrcodes

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"qrdinamics/internal/accounts"
	"qrdinamics/internal/safety"
	"qrdinamics/internal/web"
)

const maxNameLength = 80

// Handler serves the QR pages of the panel.
type Handler struct {
	svc             *Service
	view            *web.Renderer
	redirectBaseURL string
}

// NewHandler builds the handler. redirectBaseURL is what every QR encodes.
func NewHandler(svc *Service, view *web.Renderer, redirectBaseURL string) *Handler {
	return &Handler{svc: svc, view: view, redirectBaseURL: redirectBaseURL}
}

// Routes registers the QR routes behind auth.
func (h *Handler) Routes(mux *http.ServeMux, auth func(http.Handler) http.Handler) {
	handle := func(pattern string, f http.HandlerFunc) { mux.Handle(pattern, auth(f)) }
	handle("GET /qr", h.list)
	handle("GET /qr/new", h.newForm)
	handle("POST /qr/new", h.create)
	handle("GET /qr/{id}", h.detail)
	handle("POST /qr/{id}/destination", h.changeDestination)
	handle("POST /qr/{id}/name", h.rename)
	handle("POST /qr/{id}/pause", h.pause(true))
	handle("POST /qr/{id}/resume", h.pause(false))
	handle("POST /qr/{id}/delete", h.delete)
	handle("GET /qr/{id}/qr.png", h.download("png"))
	handle("GET /qr/{id}/qr.svg", h.download("svg"))
}

func owner(r *http.Request) (accounts.Account, Owner) {
	a, _ := accounts.FromContext(r.Context())
	return a, Owner{ID: a.ID, Plan: a.Plan, CreatedAt: a.CreatedAt}
}

// ShortURL is the URL a QR encodes.
func (h *Handler) ShortURL(code string) string { return h.redirectBaseURL + "/" + code }

type listData struct {
	Items    []QR
	Query    string
	Verified bool
	Used     int
	Quota    int
	ShortURL func(string) string
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	a, o := owner(r)
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	items, err := h.svc.List(r.Context(), o.ID, q)
	if err != nil {
		h.fail(w, "list qr", err)
		return
	}
	used := len(items)
	if q != "" {
		all, err := h.svc.List(r.Context(), o.ID, "")
		if err != nil {
			h.fail(w, "count qr", err)
			return
		}
		used = len(all)
	}
	p := web.Page{Title: "Els meus QR", Email: a.Email, Data: listData{
		Items: items, Query: q, Verified: a.Verified(), Used: used, Quota: h.svc.Quota(o), ShortURL: h.ShortURL,
	}}
	if r.URL.Query().Get("sent") == "1" {
		p.Notice = "T'hem enviat un correu nou de confirmació."
	}
	if r.URL.Query().Get("deleted") == "1" {
		p.Notice = "QR esborrat."
	}
	h.view.Render(w, http.StatusOK, "qr_list", p)
}

type formData struct {
	Name        string
	Destination string
}

func (h *Handler) newForm(w http.ResponseWriter, r *http.Request) {
	a, _ := owner(r)
	if !a.Verified() {
		http.Redirect(w, r, "/qr", http.StatusSeeOther)
		return
	}
	h.view.Render(w, http.StatusOK, "qr_new", web.Page{Title: "Nou QR", Email: a.Email, Data: formData{}})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	a, o := owner(r)
	if !a.Verified() {
		http.Redirect(w, r, "/qr", http.StatusSeeOther)
		return
	}
	f := formData{Name: strings.TrimSpace(r.PostFormValue("name")), Destination: r.PostFormValue("destination")}
	if len([]rune(f.Name)) > maxNameLength {
		h.view.Render(w, http.StatusUnprocessableEntity, "qr_new", web.Page{Title: "Nou QR", Email: a.Email, Error: "El nom pot tenir com a màxim 80 caràcters.", Data: f})
		return
	}
	q, err := h.svc.Create(r.Context(), o, f.Name, f.Destination)
	if err != nil {
		msg, ok := userMessage(err)
		if !ok {
			h.fail(w, "create qr", err)
			return
		}
		h.view.Render(w, http.StatusUnprocessableEntity, "qr_new", web.Page{Title: "Nou QR", Email: a.Email, Error: msg, Data: f})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/qr/%d?created=1", q.ID), http.StatusSeeOther)
}

type detailData struct {
	QR       QR
	ShortURL string
	History  []Change
	Days     []DayCount
	MaxDay   int64
	Last30   int64
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request) {
	h.renderDetail(w, r, http.StatusOK, "", noticeFor(r))
}

func noticeFor(r *http.Request) string {
	switch {
	case r.URL.Query().Has("created"):
		return "QR creat. Descarrega'l i imprimeix-lo: podràs canviar-ne el destí sempre que vulguis."
	case r.URL.Query().Has("updated"):
		return "Destí actualitzat. El QR imprès ja porta al nou destí (en menys d'un minut)."
	case r.URL.Query().Has("paused"):
		return "QR pausat. Qui l'escanegi veurà una pàgina neutra."
	case r.URL.Query().Has("resumed"):
		return "QR reactivat."
	}
	return ""
}

func (h *Handler) renderDetail(w http.ResponseWriter, r *http.Request, status int, errMsg, notice string) {
	a, o := owner(r)
	id, ok := pathID(r)
	if !ok {
		h.notFound(w, a.Email)
		return
	}
	q, err := h.svc.Get(r.Context(), o.ID, id)
	if errors.Is(err, ErrNotFound) {
		h.notFound(w, a.Email)
		return
	}
	if err != nil {
		h.fail(w, "get qr", err)
		return
	}
	history, err := h.svc.History(r.Context(), q.ID)
	if err != nil {
		h.fail(w, "qr history", err)
		return
	}
	days, err := h.svc.DailyScans(r.Context(), q.ID)
	if err != nil {
		h.fail(w, "daily scans", err)
		return
	}
	d := detailData{QR: q, ShortURL: h.ShortURL(q.Code), History: history, Days: days}
	for _, day := range days {
		d.Last30 += day.Count
		d.MaxDay = max(d.MaxDay, day.Count)
	}
	title := q.Name
	if title == "" {
		title = q.Code
	}
	h.view.Render(w, status, "qr_detail", web.Page{Title: title, Email: a.Email, Error: errMsg, Notice: notice, Data: d})
}

func (h *Handler) changeDestination(w http.ResponseWriter, r *http.Request) {
	_, o := owner(r)
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	err := h.svc.ChangeDestination(r.Context(), o, id, r.PostFormValue("destination"))
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/qr/%d?updated=1", id), http.StatusSeeOther)
}

func (h *Handler) rename(w http.ResponseWriter, r *http.Request) {
	_, o := owner(r)
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if len([]rune(name)) > maxNameLength {
		h.renderDetail(w, r, http.StatusUnprocessableEntity, "El nom pot tenir com a màxim 80 caràcters.", "")
		return
	}
	if err := h.svc.Rename(r.Context(), o.ID, id, name); err != nil {
		h.actionError(w, r, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/qr/%d", id), http.StatusSeeOther)
}

func (h *Handler) pause(paused bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, o := owner(r)
		id, ok := pathID(r)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if err := h.svc.SetPaused(r.Context(), o.ID, id, paused); err != nil {
			h.actionError(w, r, err)
			return
		}
		flag := "resumed"
		if paused {
			flag = "paused"
		}
		http.Redirect(w, r, fmt.Sprintf("/qr/%d?%s=1", id, flag), http.StatusSeeOther)
	}
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	_, o := owner(r)
	id, ok := pathID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if err := h.svc.Delete(r.Context(), o.ID, id); err != nil {
		h.actionError(w, r, err)
		return
	}
	http.Redirect(w, r, "/qr?deleted=1", http.StatusSeeOther)
}

func (h *Handler) download(format string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, o := owner(r)
		id, ok := pathID(r)
		if !ok {
			h.notFound(w, a.Email)
			return
		}
		q, err := h.svc.Get(r.Context(), o.ID, id)
		if errors.Is(err, ErrNotFound) {
			h.notFound(w, a.Email)
			return
		}
		if err != nil {
			h.fail(w, "get qr", err)
			return
		}
		var body []byte
		var ctype string
		if format == "png" {
			body, err = RenderPNG(h.ShortURL(q.Code))
			ctype = "image/png"
		} else {
			body, err = RenderSVG(h.ShortURL(q.Code))
			ctype = "image/svg+xml"
		}
		if err != nil {
			h.fail(w, "render qr", err)
			return
		}
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("Cache-Control", "private, max-age=3600")
		if !r.URL.Query().Has("inline") {
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="qr-%s.%s"`, q.Code, format))
		}
		w.Write(body)
	}
}

// actionError re-renders the detail page with a user message, or fails.
func (h *Handler) actionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		a, _ := owner(r)
		h.notFound(w, a.Email)
		return
	}
	msg, ok := userMessage(err)
	if !ok {
		h.fail(w, "qr action", err)
		return
	}
	h.renderDetail(w, r, http.StatusUnprocessableEntity, msg, "")
}

// userMessage maps an expected error to the message the owner sees.
func userMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, safety.ErrInvalid):
		return "La URL ha de començar per http:// o https:// i ser una adreça web vàlida.", true
	case errors.Is(err, safety.ErrUnsafe):
		return "Aquesta URL apareix en llistes de llocs perillosos i no es pot fer servir.", true
	case errors.Is(err, ErrCheckUnavailable):
		return "Ara mateix no podem comprovar la seguretat de la URL. Torna-ho a provar d'aquí a una estona.", true
	case errors.Is(err, ErrQuota):
		return "Has arribat al límit de QR del teu pla. Esborra'n un o canvia de pla.", true
	case errors.Is(err, ErrEditLimit):
		return "Els comptes nous poden fer 10 canvis de destí al dia durant la primera setmana. Torna-ho a provar demà.", true
	case errors.Is(err, ErrNotEditable):
		return "Aquest QR no es pot modificar en el seu estat actual.", true
	}
	return "", false
}

func (h *Handler) notFound(w http.ResponseWriter, email string) {
	h.view.Render(w, http.StatusNotFound, "not_found", web.Page{Title: "No trobat", Email: email})
}

func (h *Handler) fail(w http.ResponseWriter, op string, err error) {
	slog.Error(op, "err", err)
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

package accounts

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"qrdinamics/internal/web"
)

const sessionCookie = "sid"

type ctxKey struct{}

// FromContext returns the signed-in account set by RequireAccount.
func FromContext(ctx context.Context) (Account, bool) {
	a, ok := ctx.Value(ctxKey{}).(Account)
	return a, ok
}

// Handler serves the account pages.
type Handler struct {
	svc          *Service
	view         *web.Renderer
	secureCookie bool
}

// NewHandler builds the handler; cookies are Secure when the panel is served over https.
func NewHandler(svc *Service, view *web.Renderer, panelBaseURL string) *Handler {
	return &Handler{svc: svc, view: view, secureCookie: strings.HasPrefix(panelBaseURL, "https://")}
}

// Routes registers the account routes.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /register", h.registerForm)
	mux.HandleFunc("POST /register", h.register)
	mux.HandleFunc("GET /login", h.loginForm)
	mux.HandleFunc("POST /login", h.login)
	mux.HandleFunc("POST /logout", h.logout)
	mux.HandleFunc("GET /verify", h.verify)
	mux.Handle("POST /verify/resend", h.RequireAccount(http.HandlerFunc(h.resend)))
}

// RequireAccount lets the request through only with a valid session.
func (h *Handler) RequireAccount(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		a, err := h.svc.BySession(r.Context(), c.Value)
		if errors.Is(err, ErrInvalidToken) {
			h.clearCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		if err != nil {
			slog.Error("load session", "err", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
	})
}

func (h *Handler) registerForm(w http.ResponseWriter, r *http.Request) {
	h.view.Render(w, http.StatusOK, "register", web.Page{Title: "Crea un compte"})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	in := Signup{
		Email:       r.PostFormValue("email"),
		Password:    r.PostFormValue("password"),
		AcceptTerms: r.PostFormValue("accept_terms") == "on",
		Marketing:   r.PostFormValue("marketing") == "on",
	}
	_, err := h.svc.Register(r.Context(), in)
	if err != nil {
		msg := ""
		switch {
		case errors.Is(err, ErrInvalidEmail):
			msg = "El correu no és vàlid."
		case errors.Is(err, ErrWeakPassword):
			msg = "La contrasenya ha de tenir almenys 10 caràcters."
		case errors.Is(err, ErrTermsNotAccepted):
			msg = "Per crear el compte has d'acceptar els termes i la política de privacitat."
		case errors.Is(err, ErrEmailTaken):
			msg = "Ja hi ha un compte amb aquest correu. Inicia la sessió."
		default:
			slog.Error("register", "err", err)
			msg = "No s'ha pogut crear el compte. Torna-ho a provar."
		}
		h.view.Render(w, http.StatusUnprocessableEntity, "register", web.Page{Title: "Crea un compte", Error: msg,
			Data: map[string]any{"Email": in.Email, "Marketing": in.Marketing}})
		return
	}
	// Sign the new owner in straight away: they land on the "check your e-mail" page.
	token, err := h.svc.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		slog.Error("login after register", "err", err)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	h.setCookie(w, token)
	http.Redirect(w, r, "/qr", http.StatusSeeOther)
}

func (h *Handler) loginForm(w http.ResponseWriter, r *http.Request) {
	h.view.Render(w, http.StatusOK, "login", web.Page{Title: "Inicia la sessió"})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	email := r.PostFormValue("email")
	token, err := h.svc.Login(r.Context(), email, r.PostFormValue("password"))
	if err != nil {
		msg := "Correu o contrasenya incorrectes."
		if !errors.Is(err, ErrInvalidCredentials) {
			slog.Error("login", "err", err)
			msg = "No s'ha pogut iniciar la sessió. Torna-ho a provar."
		}
		h.view.Render(w, http.StatusUnauthorized, "login", web.Page{Title: "Inicia la sessió", Error: msg, Data: map[string]string{"Email": email}})
		return
	}
	h.setCookie(w, token)
	http.Redirect(w, r, "/qr", http.StatusSeeOther)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := h.svc.Logout(r.Context(), c.Value); err != nil {
			slog.Error("logout", "err", err)
		}
	}
	h.clearCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *Handler) verify(w http.ResponseWriter, r *http.Request) {
	err := h.svc.VerifyEmail(r.Context(), r.URL.Query().Get("token"))
	switch {
	case err == nil:
		h.view.Render(w, http.StatusOK, "verified", web.Page{Title: "Correu confirmat"})
	case errors.Is(err, ErrInvalidToken):
		h.view.Render(w, http.StatusBadRequest, "verified", web.Page{Title: "Enllaç no vàlid", Error: "L'enllaç no és vàlid o ha caducat. Inicia la sessió i demana'n un de nou."})
	default:
		slog.Error("verify email", "err", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (h *Handler) resend(w http.ResponseWriter, r *http.Request) {
	a, _ := FromContext(r.Context())
	if !a.Verified() {
		if err := h.svc.SendVerification(r.Context(), a); err != nil {
			slog.Error("resend verification", "err", err)
		}
	}
	http.Redirect(w, r, "/qr?sent=1", http.StatusSeeOther)
}

// Session cookies are Secure whenever the panel is served over https
// (PANEL_BASE_URL); only the local http dev stack gets them without it.
func (h *Handler) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{ // NOSONAR go:S2092 — Secure follows the panel's scheme, see above
		Name: sessionCookie, Value: token, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteLaxMode,
	})
}

// ClearSession removes the session cookie (after the account is deleted).
func (h *Handler) ClearSession(w http.ResponseWriter) { h.clearCookie(w) }

func (h *Handler) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{ // NOSONAR go:S2092 — Secure follows the panel's scheme, see setCookie
		Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: h.secureCookie, SameSite: http.SameSiteLaxMode,
	})
}

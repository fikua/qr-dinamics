// Package panel assembles the management panel's HTTP handler from the
// domain packages. cmd/panel only reads config and runs it.
package panel

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"qrdinamics/internal/access"
	"qrdinamics/internal/accounts"
	"qrdinamics/internal/brand"
	"qrdinamics/internal/config"
	"qrdinamics/internal/legal"
	"qrdinamics/internal/mail"
	"qrdinamics/internal/privacy"
	"qrdinamics/internal/qrcodes"
	"qrdinamics/internal/safety"
	"qrdinamics/internal/web"
)

// Deps are the panel's outside collaborators, replaceable in tests.
type Deps struct {
	DB      *pgxpool.Pool
	Mail    mail.Sender
	Checker safety.Checker
}

// Panel is the assembled panel.
type Panel struct {
	Handler http.Handler
	Privacy *privacy.Service // runs the retention purge
}

// New wires every route and middleware.
func New(cfg config.Panel, d Deps) (*Panel, error) {
	view, err := web.NewRenderer(cfg.ContactEmail)
	if err != nil {
		return nil, err
	}
	legalPages, err := legal.NewHandler(legal.Info{
		Holder:              legal.Holder(cfg.Legal),
		ContactEmail:        cfg.ContactEmail,
		PanelURL:            cfg.PanelBaseURL,
		RedirectURL:         cfg.RedirectBaseURL,
		SafeBrowsingEnabled: cfg.SafeBrowsingKey != "",
	})
	if err != nil {
		return nil, err
	}
	accountSvc := accounts.NewService(d.DB, d.Mail, cfg.PanelBaseURL)
	accountHandler := accounts.NewHandler(accountSvc, view, cfg.PanelBaseURL)
	privacySvc := privacy.NewService(d.DB)
	privacyHandler := privacy.NewHandler(privacySvc, accountSvc, view, cfg.RedirectBaseURL, accountHandler.ClearSession)
	qrHandler := qrcodes.NewHandler(qrcodes.NewService(d.DB, d.Checker), view, cfg.RedirectBaseURL)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", web.Static())
	brand.Routes(mux)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/qr", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := d.DB.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	legalPages.Routes(mux)
	accountHandler.Routes(mux)
	qrHandler.Routes(mux, accountHandler.RequireAccount)
	privacyHandler.Routes(mux, accountHandler.RequireAccount)

	// CrossOriginProtection rejects cross-site POSTs (CSRF) using Sec-Fetch-Site / Origin.
	handler := web.SecurityHeaders(http.NewCrossOriginProtection().Handler(mux))
	if cfg.AccessTeamDomain != "" {
		handler = access.NewVerifier(cfg.AccessTeamDomain, cfg.AccessAudience).Middleware(handler)
	}
	return &Panel{Handler: handler, Privacy: privacySvc}, nil
}

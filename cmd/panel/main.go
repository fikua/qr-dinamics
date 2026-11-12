// Command panel serves the management panel of dynamic QR codes.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"qrdinamics/internal/access"
	"qrdinamics/internal/accounts"
	"qrdinamics/internal/config"
	"qrdinamics/internal/db"
	"qrdinamics/internal/mail"
	"qrdinamics/internal/qrcodes"
	"qrdinamics/internal/safety"
	"qrdinamics/internal/web"
)

func main() {
	if err := run(); err != nil {
		slog.Error("panel stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadPanel()
	if err != nil {
		return err
	}
	if cfg.Env == config.Prod {
		return errors.New("prod needs an SMTP mailer, not implemented yet")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	view, err := web.NewRenderer()
	if err != nil {
		return err
	}
	accountSvc := accounts.NewService(pool, mail.Log{}, cfg.PanelBaseURL)
	accountHandler := accounts.NewHandler(accountSvc, view, cfg.PanelBaseURL)
	qrHandler := qrcodes.NewHandler(qrcodes.NewService(pool, safety.New(cfg.SafeBrowsingKey)), view, cfg.RedirectBaseURL)

	mux := http.NewServeMux()
	mux.Handle("GET /static/", web.Static())
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/qr", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	accountHandler.Routes(mux)
	qrHandler.Routes(mux, accountHandler.RequireAccount)

	// CrossOriginProtection rejects cross-site POSTs (CSRF) using Sec-Fetch-Site / Origin.
	handler := web.SecurityHeaders(http.NewCrossOriginProtection().Handler(mux))
	if cfg.AccessTeamDomain != "" {
		handler = access.NewVerifier(cfg.AccessTeamDomain, cfg.AccessAudience).Middleware(handler)
		slog.Info("panel restricted to Cloudflare Access", "team", cfg.AccessTeamDomain)
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("panel listening", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

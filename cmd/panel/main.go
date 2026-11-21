// Command panel serves the management panel of dynamic QR codes.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"qrdinamics/internal/audit"
	"qrdinamics/internal/config"
	"qrdinamics/internal/db"
	"qrdinamics/internal/health"
	"qrdinamics/internal/mail"
	"qrdinamics/internal/panel"
	"qrdinamics/internal/privacy"
	"qrdinamics/internal/safety"
	"qrdinamics/internal/telemetry"
)

func main() {
	if health.IsHealthcheck() {
		os.Exit(health.Probe(config.PanelAddr()))
	}
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

	shutdownTelemetry, err := telemetry.Setup(ctx, "qr-dinamics-panel")
	if err != nil {
		return fmt.Errorf("telemetry: %w", err)
	}
	defer func() {
		flush, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTelemetry(flush)
	}()
	audit.SetEmailKey(os.Getenv("AUDIT_EMAIL_HMAC_KEY"))

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	p, err := panel.New(cfg, panel.Deps{DB: pool, Mail: mail.Log{}, Checker: safety.New(cfg.SafeBrowsingKey)})
	if err != nil {
		return err
	}
	if cfg.AccessTeamDomain != "" {
		slog.Info("panel restricted to Cloudflare Access", "team", cfg.AccessTeamDomain)
	}

	// Retention policy (published in the privacy policy): once at start, then daily.
	var wg sync.WaitGroup
	bg, stopBG := context.WithCancel(context.Background())
	defer func() { stopBG(); wg.Wait() }()
	wg.Go(func() {
		p.Privacy.RunPurge(bg, 24*time.Hour,
			func(r privacy.PurgeResult) {
				slog.Info("retention purge", "scans", r.Scans, "sessions", r.Sessions, "tokens", r.Tokens, "unverified_accounts", r.UnverifiedAccounts)
			},
			func(err error) { slog.Error("retention purge", "err", err) })
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           telemetry.HTTPHandler(p.Handler),
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

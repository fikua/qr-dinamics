// Command redirect serves the short codes encoded in printed QR codes. It
// starts and keeps answering from its snapshot even when the database is down.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"qrdinamics/internal/config"
	"qrdinamics/internal/redirect"
)

func main() {
	if err := run(); err != nil {
		slog.Error("redirect stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadRedirect()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Fail fast if the snapshot cannot be written: without it a restart during
	// a database outage would leave every printed QR on the neutral page.
	if err := checkWritable(filepath.Dir(cfg.SnapshotPath)); err != nil {
		return fmt.Errorf("snapshot directory not writable: %w", err)
	}

	table := redirect.NewTable()
	if ok, err := table.Load(cfg.SnapshotPath); err != nil {
		slog.Error("load snapshot (starting empty)", "path", cfg.SnapshotPath, "err", err)
	} else if ok {
		slog.Info("snapshot loaded", "codes", table.Len())
	}

	// pgxpool connects lazily: the service starts even if the database is down.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	logErr := func(op string, err error) { slog.Error(op, "err", err) }
	syncer := &redirect.Syncer{
		Table: table, Source: redirect.PGSource{DB: pool},
		SnapshotPath: cfg.SnapshotPath, Interval: cfg.SyncInterval, OnError: logErr,
	}
	recorder := redirect.NewRecorder(redirect.PGSink{DB: pool}, logErr)

	var wg sync.WaitGroup
	bg, stopBG := context.WithCancel(context.Background())
	wg.Go(func() { syncer.Run(bg) })
	wg.Go(func() { recorder.Run(bg) })

	mux := http.NewServeMux()
	(&redirect.Handler{
		Table: table, Recorder: recorder, CountryHeader: cfg.CountryHeader,
		AbuseEmail: cfg.AbuseEmail, Now: time.Now,
	}).Routes(mux)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		slog.Info("redirect listening", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err = <-errc:
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err = srv.Shutdown(shutdown)
		cancel()
	}
	// Stop the background loops only after the server stopped taking scans,
	// so the recorder's final flush includes them.
	stopBG()
	wg.Wait()
	return err
}

func checkWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}

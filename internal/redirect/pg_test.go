package redirect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"qrdinamics/internal/testdb"
)

func TestPostgresSourceSinkAndRun(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	var acc, qrID int64
	pool.QueryRow(ctx, `INSERT INTO accounts (email, password_hash) VALUES ('a@example.com', 'x') RETURNING id`).Scan(&acc)
	pool.QueryRow(ctx, `INSERT INTO qr_codes (account_id, code, destination) VALUES ($1, 'abc2345', 'https://example.com') RETURNING id`, acc).Scan(&qrID)

	src := PGSource{DB: pool}
	all, err := src.All(ctx)
	if err != nil || len(all) != 1 || all[0].Entry.ID != qrID {
		t.Fatalf("All = %+v, %v", all, err)
	}
	changed, err := src.Changed(ctx, time.Now().Add(time.Hour))
	if err != nil || len(changed) != 0 {
		t.Fatalf("Changed(future) = %+v, %v", changed, err)
	}

	if err := (PGSink{DB: pool}).Store(ctx, []Scan{{QRID: qrID, At: time.Now(), Country: "ES", Device: "mobile"}}); err != nil {
		t.Fatal(err)
	}
	var n int
	pool.QueryRow(ctx, `SELECT count(*) FROM scans WHERE qr_id = $1 AND country = 'ES'`, qrID).Scan(&n)
	if n != 1 {
		t.Fatalf("%d scans stored, want 1", n)
	}

	// Run: the first pass is a full reload; health turns green once loaded.
	table := NewTable()
	h := &Handler{Table: table, Recorder: NewRecorder(&fakeSink{}, func(string, error) {}), Now: time.Now}
	mux := http.NewServeMux()
	h.Routes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty table health = %d, want 503", w.Code)
	}

	runCtx, cancel := context.WithCancel(ctx)
	s := &Syncer{Table: table, Source: src, SnapshotPath: filepath.Join(t.TempDir(), "s.json"), Interval: 10 * time.Millisecond, OnError: func(string, error) {}}
	done := make(chan struct{})
	go func() { s.Run(runCtx); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for table.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if table.Len() != 1 {
		t.Fatal("Run did not load the table")
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("health = %d, want 200", w.Code)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("home = %d", w.Code)
	}
}

func TestRunReportsErrors(t *testing.T) {
	src := &fakeSource{err: context.DeadlineExceeded}
	var errs int
	ctx, cancel := context.WithCancel(context.Background())
	s := &Syncer{Table: NewTable(), Source: src, SnapshotPath: filepath.Join(t.TempDir(), "s.json"), Interval: time.Hour,
		OnError: func(string, error) { errs++; cancel() }}
	s.Run(ctx)
	if errs != 1 {
		t.Fatalf("%d errors reported, want 1", errs)
	}
}

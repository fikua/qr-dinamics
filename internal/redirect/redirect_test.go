package redirect

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeSink struct {
	mu    sync.Mutex
	scans []Scan
	err   error
}

func (f *fakeSink) Store(_ context.Context, b []Scan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.scans = append(f.scans, b...)
	return nil
}

func newServer(t *testing.T, rows ...Row) (*http.ServeMux, *Recorder) {
	t.Helper()
	table := NewTable()
	table.Apply(rows)
	rec := NewRecorder(&fakeSink{}, func(string, error) {})
	mux := http.NewServeMux()
	(&Handler{Table: table, Recorder: rec, CountryHeader: "CF-IPCountry", ContactEmail: "abus@example.com",
		Now: func() time.Time { return time.Unix(0, 0) }}).Routes(mux)
	return mux, rec
}

const iphone = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148"

func TestRedirectActive(t *testing.T) {
	mux, rec := newServer(t, Row{Code: "abc2345", Entry: Entry{ID: 7, Destination: "https://example.com/carta", Status: "active"}})
	req := httptest.NewRequest("GET", "/abc2345", nil)
	req.Header.Set("User-Agent", iphone)
	req.Header.Set("CF-IPCountry", "es")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "https://example.com/carta" {
		t.Fatalf("Location = %q", loc)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	if w.Header().Get("Set-Cookie") != "" {
		t.Fatal("redirect must not set cookies")
	}
	select {
	case s := <-rec.ch:
		if s.QRID != 7 || s.Country != "ES" || s.Device != "mobile" {
			t.Fatalf("scan = %+v", s)
		}
	default:
		t.Fatal("scan not recorded")
	}
}

func TestNeutralPage(t *testing.T) {
	mux, rec := newServer(t,
		Row{Code: "paus234", Entry: Entry{ID: 1, Destination: "https://example.com", Status: "paused"}},
		Row{Code: "dele234", Entry: Entry{ID: 2, Destination: "https://example.com", Status: "deleted"}},
		Row{Code: "bloc234", Entry: Entry{ID: 3, Destination: "https://example.com", Status: "blocked"}},
	)
	for _, path := range []string{"/paus234", "/dele234", "/bloc234", "/unkn234", "/not-a-code", "/abc0345"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusNotFound || w.Header().Get("Location") != "" {
			t.Errorf("%s: status %d, Location %q; want the neutral page", path, w.Code, w.Header().Get("Location"))
		}
		if !strings.Contains(w.Body.String(), "/brand/brand.css?v=") || !strings.Contains(w.Body.String(), "<!--email_off-->") {
			t.Errorf("%s: neutral page stylesheet must be versioned", path)
		}
		if !strings.Contains(w.Body.String(), "mailto:abus@example.com") {
			t.Errorf("%s: neutral page lacks the abuse link (RF-11)", path)
		}
	}
	if len(rec.ch) != 0 {
		t.Fatal("no scan must be recorded for a non-active code")
	}
}

func TestBotsAndHEADAreNotCounted(t *testing.T) {
	mux, rec := newServer(t, Row{Code: "abc2345", Entry: Entry{ID: 7, Destination: "https://example.com", Status: "active"}})
	bot := httptest.NewRequest("GET", "/abc2345", nil)
	bot.Header.Set("User-Agent", "WhatsApp link preview bot")
	mux.ServeHTTP(httptest.NewRecorder(), bot)
	head := httptest.NewRequest("HEAD", "/abc2345", nil)
	head.Header.Set("User-Agent", iphone)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, head)
	if w.Code != http.StatusFound {
		t.Fatalf("HEAD status = %d, want 302", w.Code)
	}
	if len(rec.ch) != 0 {
		t.Fatalf("%d scans recorded, want 0", len(rec.ch))
	}
}

func TestDeviceType(t *testing.T) {
	tests := map[string]string{
		iphone: "mobile",
		"Mozilla/5.0 (Linux; Android 14; Pixel 8) Mobile Safari/537.36": "mobile",
		"Mozilla/5.0 (Linux; Android 13; SM-X200) Safari/537.36":        "tablet",
		"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)":                 "tablet",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) Safari/605.1.15":  "desktop",
		"Googlebot/2.1 (+http://www.google.com/bot.html)":               "bot",
		"curl/8.4.0": "bot",
		"":           "bot",
	}
	for ua, want := range tests {
		if got := DeviceType(ua); got != want {
			t.Errorf("DeviceType(%q) = %q, want %q", ua, got, want)
		}
	}
}

func TestCountry(t *testing.T) {
	for in, want := range map[string]string{"ES": "ES", "fr": "FR", " de ": "DE", "XX": "", "T1": "", "E": "", "E1": "", "": ""} {
		if got := Country(in); got != want {
			t.Errorf("Country(%q) = %q, want %q", in, got, want)
		}
	}
}

type fakeSource struct {
	mu    sync.Mutex
	rows  []Row
	err   error
	since []time.Time
}

func (f *fakeSource) Changed(_ context.Context, since time.Time) ([]Row, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.since = append(f.since, since)
	if f.err != nil {
		return nil, f.err
	}
	var out []Row
	for _, r := range f.rows {
		if !r.UpdatedAt.Before(since) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeSource) All(ctx context.Context) ([]Row, error) { return f.Changed(ctx, time.Time{}) }

func TestSyncPersistsAndSurvivesDatabaseOutage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	t0 := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	src := &fakeSource{rows: []Row{{Code: "abc2345", Entry: Entry{ID: 1, Destination: "https://a.example", Status: "active"}, UpdatedAt: t0}}}
	s := &Syncer{Table: NewTable(), Source: src, SnapshotPath: path, Interval: time.Second, OnError: func(string, error) {}}

	if err := s.syncOnce(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	// The owner edits the destination; an incremental sync picks it up.
	src.rows[0].Entry.Destination = "https://b.example"
	src.rows[0].UpdatedAt = t0.Add(time.Minute)
	if err := s.syncOnce(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if got := src.since[len(src.since)-1]; !got.Equal(t0.Add(-overlap)) {
		t.Fatalf("incremental sync since = %v, want newest change minus overlap", got)
	}
	if e, _ := s.Table.Lookup("abc2345"); e.Destination != "https://b.example" {
		t.Fatalf("destination = %q after sync", e.Destination)
	}

	// Database down: the sync fails and the table keeps answering.
	src.err = errors.New("connection refused")
	if err := s.syncOnce(context.Background(), false); err == nil {
		t.Fatal("want an error while the database is down")
	}
	if _, ok := s.Table.Lookup("abc2345"); !ok {
		t.Fatal("table lost its entries during the outage")
	}

	// A restart during the outage loads the snapshot.
	restarted := NewTable()
	if ok, err := restarted.Load(path); err != nil || !ok {
		t.Fatalf("Load = %v, %v", ok, err)
	}
	if e, _ := restarted.Lookup("abc2345"); e.Destination != "https://b.example" {
		t.Fatalf("snapshot destination = %q", e.Destination)
	}
	if !restarted.Since().Equal(t0.Add(time.Minute)) {
		t.Fatalf("snapshot since = %v", restarted.Since())
	}
}

func TestFullReloadDropsRemovedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	src := &fakeSource{rows: []Row{
		{Code: "abc2345", Entry: Entry{ID: 1, Status: "active"}},
		{Code: "xyz2345", Entry: Entry{ID: 2, Status: "active"}},
	}}
	s := &Syncer{Table: NewTable(), Source: src, SnapshotPath: path, Interval: time.Second, OnError: func(string, error) {}}
	s.syncOnce(context.Background(), true)
	src.rows = src.rows[:1]
	s.syncOnce(context.Background(), true)
	if _, ok := s.Table.Lookup("xyz2345"); ok {
		t.Fatal("full reload kept a removed row")
	}
}

func TestLoadMissingSnapshot(t *testing.T) {
	ok, err := NewTable().Load(filepath.Join(t.TempDir(), "none.json"))
	if ok || err != nil {
		t.Fatalf("Load = %v, %v; want false, nil", ok, err)
	}
}

func TestRecorderFlushesOnShutdown(t *testing.T) {
	sink := &fakeSink{}
	rec := NewRecorder(sink, func(string, error) {})
	for i := range 3 {
		rec.Record(Scan{QRID: int64(i)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { rec.Run(ctx); close(done) }()
	cancel()
	<-done
	if len(sink.scans) != 3 {
		t.Fatalf("stored %d scans, want 3", len(sink.scans))
	}
}

func TestRecorderDropsWhenFull(t *testing.T) {
	rec := NewRecorder(&fakeSink{}, func(string, error) {})
	for range recorderBuffer {
		if !rec.Record(Scan{}) {
			t.Fatal("dropped before the buffer was full")
		}
	}
	if rec.Record(Scan{}) {
		t.Fatal("Record must not block or accept when the buffer is full")
	}
}

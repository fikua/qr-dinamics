package redirect

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Scan is one recorded scan: no IP, no cookie (RF-03).
type Scan struct {
	QRID    int64
	At      time.Time
	Country string
	Device  string
}

// Sink stores a batch of scans.
type Sink interface {
	Store(ctx context.Context, batch []Scan) error
}

// PGSink stores scans in Postgres with COPY.
type PGSink struct{ DB *pgxpool.Pool }

func (s PGSink) Store(ctx context.Context, batch []Scan) error {
	_, err := s.DB.CopyFrom(ctx, pgx.Identifier{"scans"}, []string{"qr_id", "scanned_at", "country", "device"},
		pgx.CopyFromSlice(len(batch), func(i int) ([]any, error) {
			b := batch[i]
			return []any{b.QRID, b.At, b.Country, b.Device}, nil
		}))
	return err
}

const (
	recorderBuffer = 10_000
	flushEvery     = 2 * time.Second
	flushSize      = 500
)

// Recorder buffers scans and stores them in batches off the request path.
// A redirect never waits for the database: when the buffer is full, or the
// database is down, scans are dropped and counted.
type Recorder struct {
	ch      chan Scan
	sink    Sink
	onError func(op string, err error)
}

// NewRecorder builds a recorder.
func NewRecorder(sink Sink, onError func(op string, err error)) *Recorder {
	return &Recorder{ch: make(chan Scan, recorderBuffer), sink: sink, onError: onError}
}

// Record queues a scan without blocking; it reports false when it was dropped.
func (r *Recorder) Record(s Scan) bool {
	select {
	case r.ch <- s:
		return true
	default:
		return false
	}
}

// Run flushes queued scans until ctx is cancelled, then flushes what is left.
func (r *Recorder) Run(ctx context.Context) {
	tick := time.NewTicker(flushEvery)
	defer tick.Stop()
	batch := make([]Scan, 0, flushSize)
	flush := func(ctx context.Context) {
		if len(batch) == 0 {
			return
		}
		fctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := r.sink.Store(fctx, batch); err != nil {
			r.onError("store scans (batch dropped)", err)
		}
		batch = batch[:0]
	}
	for {
		select {
		case s := <-r.ch:
			batch = append(batch, s)
			if len(batch) >= flushSize {
				flush(ctx)
			}
		case <-tick.C:
			flush(ctx)
		case <-ctx.Done():
			for {
				select {
				case s := <-r.ch:
					batch = append(batch, s)
				default:
					flush(context.WithoutCancel(ctx))
					return
				}
			}
		}
	}
}

// DeviceType classifies a User-Agent as mobile, tablet, desktop or bot.
func DeviceType(ua string) string {
	l := strings.ToLower(ua)
	switch {
	case l == "" || strings.Contains(l, "bot") || strings.Contains(l, "spider") || strings.Contains(l, "crawl") ||
		strings.Contains(l, "preview") || strings.Contains(l, "curl/") || strings.Contains(l, "wget/"):
		return "bot"
	case strings.Contains(l, "ipad") || strings.Contains(l, "tablet") ||
		(strings.Contains(l, "android") && !strings.Contains(l, "mobile")):
		return "tablet"
	case strings.Contains(l, "mobi") || strings.Contains(l, "iphone") || strings.Contains(l, "android"):
		return "mobile"
	default:
		return "desktop"
	}
}

// Country returns a valid ISO 3166-1 alpha-2 code from the edge header, or "".
func Country(v string) string {
	v = strings.ToUpper(strings.TrimSpace(v))
	if len(v) != 2 || v == "XX" || v == "T1" {
		return ""
	}
	for i := range 2 {
		if v[i] < 'A' || v[i] > 'Z' {
			return ""
		}
	}
	return v
}

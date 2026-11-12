// Package redirect serves GET /{code}. It answers from an in-memory table kept
// in sync with the database and saved to a local snapshot, so redirects keep
// working while the panel or the database are down.
package redirect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Entry is what the redirect needs to know about one code.
type Entry struct {
	ID          int64  `json:"id"`
	Destination string `json:"d"`
	Status      string `json:"s"`
}

// Row is one changed QR read from the database.
type Row struct {
	Code      string
	Entry     Entry
	UpdatedAt time.Time
}

// Table is the in-memory redirect table.
type Table struct {
	mu      sync.RWMutex
	entries map[string]Entry
	since   time.Time // newest updated_at applied
}

// NewTable returns an empty table.
func NewTable() *Table { return &Table{entries: map[string]Entry{}} }

// Lookup returns the entry of a code.
func (t *Table) Lookup(code string) (Entry, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	e, ok := t.entries[code]
	return e, ok
}

// Len returns the number of codes in the table.
func (t *Table) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.entries)
}

// Since returns the newest updated_at applied.
func (t *Table) Since() time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.since
}

// Apply merges changed rows into the table.
func (t *Table) Apply(rows []Row) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range rows {
		t.entries[r.Code] = r.Entry
		if r.UpdatedAt.After(t.since) {
			t.since = r.UpdatedAt
		}
	}
}

// Replace swaps the whole table for rows (a full reload).
func (t *Table) Replace(rows []Row) {
	entries := make(map[string]Entry, len(rows))
	var since time.Time
	for _, r := range rows {
		entries[r.Code] = r.Entry
		if r.UpdatedAt.After(since) {
			since = r.UpdatedAt
		}
	}
	t.mu.Lock()
	t.entries, t.since = entries, since
	t.mu.Unlock()
}

type snapshot struct {
	Since   time.Time        `json:"since"`
	Entries map[string]Entry `json:"entries"`
}

// Save writes the table to path atomically (temp file + rename).
func (t *Table) Save(path string) error {
	t.mu.RLock()
	b, err := json.Marshal(snapshot{Since: t.since, Entries: t.entries})
	t.mu.RUnlock()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Load reads a snapshot written by Save. A missing file is not an error.
func (t *Table) Load(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var s snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return false, fmt.Errorf("decode snapshot: %w", err)
	}
	if s.Entries == nil {
		s.Entries = map[string]Entry{}
	}
	t.mu.Lock()
	t.entries, t.since = s.Entries, s.Since
	t.mu.Unlock()
	return true, nil
}

// Source reads QR rows from the database.
type Source interface {
	// Changed returns the rows updated at or after since.
	Changed(ctx context.Context, since time.Time) ([]Row, error)
	// All returns every row.
	All(ctx context.Context) ([]Row, error)
}

// PGSource reads from Postgres.
type PGSource struct{ DB *pgxpool.Pool }

func (s PGSource) Changed(ctx context.Context, since time.Time) ([]Row, error) {
	return s.query(ctx, `SELECT code, id, destination, status, updated_at FROM qr_codes WHERE updated_at >= $1`, since)
}

func (s PGSource) All(ctx context.Context) ([]Row, error) {
	return s.query(ctx, `SELECT code, id, destination, status, updated_at FROM qr_codes`)
}

func (s PGSource) query(ctx context.Context, sql string, args ...any) ([]Row, error) {
	rows, err := s.DB.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Row, error) {
		var x Row
		err := r.Scan(&x.Code, &x.Entry.ID, &x.Entry.Destination, &x.Entry.Status, &x.UpdatedAt)
		return x, err
	})
}

// overlap re-reads a window before the newest change seen, so a transaction
// that committed after a later one (with an older updated_at) is not missed.
const overlap = 30 * time.Second

// fullReloadEvery rebuilds the whole table now and then, to drop rows removed
// from the database (account deletion).
const fullReloadEvery = time.Hour

// Syncer keeps a Table up to date and persisted.
type Syncer struct {
	Table        *Table
	Source       Source
	SnapshotPath string
	Interval     time.Duration
	OnError      func(op string, err error)
}

// Run syncs until ctx is cancelled. The first sync is a full reload.
func (s *Syncer) Run(ctx context.Context) {
	lastFull := time.Time{}
	tick := time.NewTicker(s.Interval)
	defer tick.Stop()
	for {
		full := time.Since(lastFull) >= fullReloadEvery
		if err := s.syncOnce(ctx, full); err != nil {
			if ctx.Err() != nil {
				return
			}
			s.OnError("sync redirect table", err)
		} else if full {
			lastFull = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Syncer) syncOnce(ctx context.Context, full bool) error {
	qctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var rows []Row
	var err error
	if full {
		rows, err = s.Source.All(qctx)
	} else {
		rows, err = s.Source.Changed(qctx, s.Table.Since().Add(-overlap))
	}
	if err != nil {
		return err
	}
	if full {
		s.Table.Replace(rows)
	} else if len(rows) > 0 {
		s.Table.Apply(rows)
	}
	if full || len(rows) > 0 {
		if err := s.Table.Save(s.SnapshotPath); err != nil {
			return fmt.Errorf("save snapshot: %w", err)
		}
	}
	return nil
}

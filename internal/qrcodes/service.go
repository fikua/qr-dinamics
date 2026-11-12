// Package qrcodes manages the dynamic QR codes of an account: creation,
// destination changes, pause, deletion, rendering and scan statistics.
package qrcodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"qrdinamics/internal/safety"
)

var (
	ErrNotFound         = errors.New("qr not found")
	ErrQuota            = errors.New("qr quota reached")
	ErrEditLimit        = errors.New("daily edit limit reached")
	ErrCheckUnavailable = errors.New("url safety check unavailable")
	ErrNotEditable      = errors.New("qr not editable")
)

// Status of a QR code.
type Status string

const (
	Active  Status = "active"
	Paused  Status = "paused"
	Blocked Status = "blocked"
	Deleted Status = "deleted"
)

// planQuota is the number of non-deleted QR codes each plan allows (RF-04).
// The free quota is still an open decision in the PRD; 3 is the proposal.
var planQuota = map[string]int{"free": 3}

// New-account limits (RF-10).
const (
	newAccountPeriod   = 7 * 24 * time.Hour
	newAccountMaxQR    = 3
	newAccountMaxEdits = 10
)

// Owner is the account acting on its QR codes.
type Owner struct {
	ID        int64
	Plan      string
	CreatedAt time.Time
}

// QR is a dynamic QR code.
type QR struct {
	ID          int64
	Code        string
	Name        string
	Destination string
	Status      Status
	CreatedAt   time.Time
	UpdatedAt   time.Time
	TotalScans  int64
}

// Change is one entry of a QR's destination history (RF-05).
type Change struct {
	Destination string
	Initial     bool
	ChangedAt   time.Time
}

// DayCount is the number of scans on one day.
type DayCount struct {
	Day   time.Time
	Count int64
}

// Service implements the QR use cases on Postgres.
type Service struct {
	db      *pgxpool.Pool
	checker safety.Checker
	now     func() time.Time
}

// NewService builds the service.
func NewService(db *pgxpool.Pool, checker safety.Checker) *Service {
	return &Service{db: db, checker: checker, now: time.Now}
}

// Quota returns how many QR codes the owner may keep.
func (s *Service) Quota(o Owner) int {
	q, ok := planQuota[o.Plan]
	if !ok {
		q = planQuota["free"]
	}
	if s.isNew(o) && q > newAccountMaxQR {
		q = newAccountMaxQR
	}
	return q
}

func (s *Service) isNew(o Owner) bool { return s.now().Sub(o.CreatedAt) < newAccountPeriod }

// checkDestination normalizes the URL and runs it through the safety checker.
// A checker failure rejects the URL: nothing unchecked is stored.
func (s *Service) checkDestination(ctx context.Context, raw string) (string, error) {
	dest, err := safety.Normalize(raw)
	if err != nil {
		return "", err
	}
	if err := s.checker.Check(ctx, dest); err != nil {
		if errors.Is(err, safety.ErrUnsafe) {
			return "", err
		}
		return "", fmt.Errorf("%w: %w", ErrCheckUnavailable, err)
	}
	return dest, nil
}

// Create creates a QR code pointing at destination (US-02).
func (s *Service) Create(ctx context.Context, o Owner, name, destination string) (QR, error) {
	dest, err := s.checkDestination(ctx, destination)
	if err != nil {
		return QR{}, err
	}
	var q QR
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		// Lock the account row so concurrent creations cannot overrun the quota.
		if _, err := tx.Exec(ctx, `SELECT 1 FROM accounts WHERE id = $1 FOR UPDATE`, o.ID); err != nil {
			return err
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM qr_codes WHERE account_id = $1 AND status <> 'deleted'`, o.ID).Scan(&count); err != nil {
			return err
		}
		if count >= s.Quota(o) {
			return ErrQuota
		}
		for attempt := 0; ; attempt++ {
			code, err := NewCode()
			if err != nil {
				return err
			}
			err = tx.QueryRow(ctx, `
				INSERT INTO qr_codes (account_id, code, name, destination)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (code) DO NOTHING
				RETURNING id, code, name, destination, status, created_at, updated_at`,
				o.ID, code, name, dest).Scan(&q.ID, &q.Code, &q.Name, &q.Destination, &q.Status, &q.CreatedAt, &q.UpdatedAt)
			if errors.Is(err, pgx.ErrNoRows) && attempt < 5 {
				continue // code collision: try another one
			}
			if err != nil {
				return err
			}
			break
		}
		_, err := tx.Exec(ctx, `INSERT INTO destination_changes (qr_id, destination, is_initial) VALUES ($1, $2, true)`, q.ID, dest)
		return err
	})
	return q, err
}

// List returns the owner's QR codes, newest first, filtered by name or
// destination when query is not empty (US-05).
func (s *Service) List(ctx context.Context, ownerID int64, query string) ([]QR, error) {
	rows, err := s.db.Query(ctx, `
		SELECT q.id, q.code, q.name, q.destination, q.status, q.created_at, q.updated_at,
		       (SELECT count(*) FROM scans s WHERE s.qr_id = q.id)
		FROM qr_codes q
		WHERE q.account_id = $1 AND q.status <> 'deleted'
		  AND ($2 = '' OR q.name ILIKE '%' || $2 || '%' OR q.destination ILIKE '%' || $2 || '%')
		ORDER BY q.created_at DESC`, ownerID, escapeLike(query))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (QR, error) {
		var q QR
		err := r.Scan(&q.ID, &q.Code, &q.Name, &q.Destination, &q.Status, &q.CreatedAt, &q.UpdatedAt, &q.TotalScans)
		return q, err
	})
}

// Get returns one of the owner's QR codes.
func (s *Service) Get(ctx context.Context, ownerID, id int64) (QR, error) {
	var q QR
	err := s.db.QueryRow(ctx, `
		SELECT id, code, name, destination, status, created_at, updated_at,
		       (SELECT count(*) FROM scans WHERE qr_id = qr_codes.id)
		FROM qr_codes WHERE id = $1 AND account_id = $2 AND status <> 'deleted'`, id, ownerID).
		Scan(&q.ID, &q.Code, &q.Name, &q.Destination, &q.Status, &q.CreatedAt, &q.UpdatedAt, &q.TotalScans)
	if errors.Is(err, pgx.ErrNoRows) {
		return QR{}, ErrNotFound
	}
	return q, err
}

// ChangeDestination points the QR at a new URL; the short code never changes (US-04).
func (s *Service) ChangeDestination(ctx context.Context, o Owner, id int64, destination string) error {
	dest, err := s.checkDestination(ctx, destination)
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var status Status
		var current string
		err := tx.QueryRow(ctx, `SELECT status, destination FROM qr_codes WHERE id = $1 AND account_id = $2 AND status <> 'deleted' FOR UPDATE`, id, o.ID).Scan(&status, &current)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if status == Blocked {
			return ErrNotEditable
		}
		if current == dest {
			return nil
		}
		if s.isNew(o) {
			var edits int
			if err := tx.QueryRow(ctx, `
				SELECT count(*) FROM destination_changes c JOIN qr_codes q ON q.id = c.qr_id
				WHERE q.account_id = $1 AND NOT c.is_initial AND c.changed_at > $2`,
				o.ID, s.now().Add(-24*time.Hour)).Scan(&edits); err != nil {
				return err
			}
			if edits >= newAccountMaxEdits {
				return ErrEditLimit
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE qr_codes SET destination = $1, updated_at = now() WHERE id = $2`, dest, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO destination_changes (qr_id, destination) VALUES ($1, $2)`, id, dest)
		return err
	})
}

// Rename changes the internal name of a QR.
func (s *Service) Rename(ctx context.Context, ownerID, id int64, name string) error {
	return s.exec1(ctx, `UPDATE qr_codes SET name = $3 WHERE id = $1 AND account_id = $2 AND status <> 'deleted'`, id, ownerID, name)
}

// SetPaused pauses or resumes a QR (US-06). A blocked QR stays blocked.
func (s *Service) SetPaused(ctx context.Context, ownerID, id int64, paused bool) error {
	from, to := Active, Paused
	if !paused {
		from, to = Paused, Active
	}
	tag, err := s.db.Exec(ctx, `UPDATE qr_codes SET status = $3, updated_at = now() WHERE id = $1 AND account_id = $2 AND status = $4`, id, ownerID, to, from)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.Get(ctx, ownerID, id); err != nil {
			return err
		}
		return ErrNotEditable
	}
	return nil
}

// Delete frees the QR's quota slot and erases its data (name, destination,
// history, scans). The short code is kept as a tombstone and never reused (US-06).
func (s *Service) Delete(ctx context.Context, ownerID, id int64) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE qr_codes SET status = 'deleted', name = '', destination = '', updated_at = now()
			WHERE id = $1 AND account_id = $2 AND status IN ('active', 'paused')`, id, ownerID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM destination_changes WHERE qr_id = $1`, id); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM scans WHERE qr_id = $1`, id)
		return err
	})
}

// History returns the destination changes of a QR, newest first.
func (s *Service) History(ctx context.Context, id int64) ([]Change, error) {
	rows, err := s.db.Query(ctx, `SELECT destination, is_initial, changed_at FROM destination_changes WHERE qr_id = $1 ORDER BY changed_at DESC, id DESC`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Change, error) {
		var c Change
		err := r.Scan(&c.Destination, &c.Initial, &c.ChangedAt)
		return c, err
	})
}

// DailyScans returns the scans of each of the last 30 days, oldest first,
// including days without scans (US-07). Days are UTC.
func (s *Service) DailyScans(ctx context.Context, id int64) ([]DayCount, error) {
	rows, err := s.db.Query(ctx, `
		SELECT d.day, count(s.id)
		FROM generate_series((now() AT TIME ZONE 'UTC')::date - 29, (now() AT TIME ZONE 'UTC')::date, interval '1 day') AS d(day)
		LEFT JOIN scans s ON s.qr_id = $1
		     AND s.scanned_at >= d.day AT TIME ZONE 'UTC'
		     AND s.scanned_at <  (d.day + interval '1 day') AT TIME ZONE 'UTC'
		GROUP BY d.day ORDER BY d.day`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (DayCount, error) {
		var c DayCount
		err := r.Scan(&c.Day, &c.Count)
		return c, err
	})
}

func (s *Service) exec1(ctx context.Context, sql string, args ...any) error {
	tag, err := s.db.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func escapeLike(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '%' || r == '_' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

// Package privacy implements the data-subject rights the service offers
// self-service (export and erasure) and the retention policy published in
// the privacy policy.
package privacy

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Retention periods. The privacy policy (internal/legal) states the same
// values: change both together.
const (
	ScanRetention       = 24 * 30 * 24 * time.Hour // 24 months
	UnverifiedRetention = 30 * 24 * time.Hour      // accounts that never confirmed their e-mail
	TokenRetention      = 7 * 24 * time.Hour       // used or expired e-mail tokens, after expiry
)

// Service implements export, erasure and retention on Postgres.
type Service struct {
	db *pgxpool.Pool
}

// NewService builds the service.
func NewService(db *pgxpool.Pool) *Service { return &Service{db: db} }

// Export is everything the service holds about an account (portability).
type Export struct {
	ExportedAt time.Time     `json:"exported_at"`
	Account    AccountData   `json:"account"`
	Consents   []ConsentData `json:"consents"`
	QRCodes    []QRData      `json:"qr_codes"`
}

type AccountData struct {
	Email           string     `json:"email"`
	Plan            string     `json:"plan"`
	CreatedAt       time.Time  `json:"created_at"`
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
}

type ConsentData struct {
	Kind      string    `json:"kind"`
	Version   string    `json:"version"`
	Granted   bool      `json:"granted"`
	CreatedAt time.Time `json:"created_at"`
}

type QRData struct {
	Code         string       `json:"code"`
	ShortURL     string       `json:"short_url"`
	Name         string       `json:"name"`
	Destination  string       `json:"destination"`
	Status       string       `json:"status"`
	CreatedAt    time.Time    `json:"created_at"`
	Destinations []ChangeData `json:"destination_history"`
	Scans        []ScanData   `json:"scans"`
}

type ChangeData struct {
	Destination string    `json:"destination"`
	ChangedAt   time.Time `json:"changed_at"`
}

type ScanData struct {
	ScannedAt time.Time `json:"scanned_at"`
	Country   string    `json:"country,omitempty"`
	Device    string    `json:"device"`
}

// Export collects the account's data. redirectBaseURL builds each short URL.
func (s *Service) Export(ctx context.Context, accountID int64, redirectBaseURL string) (Export, error) {
	e := Export{ExportedAt: time.Now().UTC(), Consents: []ConsentData{}, QRCodes: []QRData{}}
	err := pgx.BeginTxFunc(ctx, s.db, pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT email, plan, created_at, email_verified_at FROM accounts WHERE id = $1`, accountID).
			Scan(&e.Account.Email, &e.Account.Plan, &e.Account.CreatedAt, &e.Account.EmailVerifiedAt); err != nil {
			return fmt.Errorf("account: %w", err)
		}

		rows, _ := tx.Query(ctx, `SELECT kind, version, granted, created_at FROM consents WHERE account_id = $1 ORDER BY created_at, id`, accountID)
		consents, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ConsentData])
		if err != nil {
			return fmt.Errorf("consents: %w", err)
		}
		e.Consents = append(e.Consents, consents...)

		rows, _ = tx.Query(ctx, `SELECT id, code, name, destination, status, created_at FROM qr_codes WHERE account_id = $1 AND status <> 'deleted' ORDER BY created_at, id`, accountID)
		type qrRow struct {
			ID int64
			QRData
		}
		qrs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (qrRow, error) {
			var q qrRow
			err := r.Scan(&q.ID, &q.Code, &q.Name, &q.Destination, &q.Status, &q.CreatedAt)
			return q, err
		})
		if err != nil {
			return fmt.Errorf("qr codes: %w", err)
		}
		for _, q := range qrs {
			q.ShortURL = redirectBaseURL + "/" + q.Code
			rows, _ := tx.Query(ctx, `SELECT destination, changed_at FROM destination_changes WHERE qr_id = $1 ORDER BY changed_at, id`, q.ID)
			if q.Destinations, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ChangeData]); err != nil {
				return fmt.Errorf("destination history: %w", err)
			}
			rows, _ = tx.Query(ctx, `SELECT scanned_at, country, device FROM scans WHERE qr_id = $1 ORDER BY scanned_at, id`, q.ID)
			if q.Scans, err = pgx.CollectRows(rows, pgx.RowToStructByPos[ScanData]); err != nil {
				return fmt.Errorf("scans: %w", err)
			}
			e.QRCodes = append(e.QRCodes, q.QRData)
		}
		return nil
	})
	return e, err
}

// WriteCSV writes one row per QR code (US-10: "Exportació CSV de QR i destins").
func (e Export) WriteCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	cw.Write([]string{"code", "short_url", "name", "destination", "status", "created_at", "total_scans", "destination_changes"})
	for _, q := range e.QRCodes {
		cw.Write([]string{
			cell(q.Code), cell(q.ShortURL), cell(q.Name), cell(q.Destination), cell(q.Status),
			q.CreatedAt.UTC().Format(time.RFC3339), strconv.Itoa(len(q.Scans)), strconv.Itoa(len(q.Destinations)),
		})
	}
	cw.Flush()
	return cw.Error()
}

// cell neutralizes spreadsheet formula injection in user-supplied text.
func cell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// DeleteAccount erases an account and everything attached to it. Its short
// codes survive only as anonymous tombstones (status deleted, no owner, name
// or destination) so they are never reused; the redirect service picks the
// change up on its next sync and shows the neutral page.
func (s *Service) DeleteAccount(ctx context.Context, accountID int64) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM scans WHERE qr_id IN (SELECT id FROM qr_codes WHERE account_id = $1)`, accountID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM destination_changes WHERE qr_id IN (SELECT id FROM qr_codes WHERE account_id = $1)`, accountID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE qr_codes SET status = 'deleted', name = '', destination = '', account_id = NULL, updated_at = now()
			WHERE account_id = $1`, accountID); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID) // cascades sessions, tokens, consents
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return pgx.ErrNoRows
		}
		return nil
	})
}

// PurgeResult counts what one retention run removed.
type PurgeResult struct {
	Scans, Sessions, Tokens, UnverifiedAccounts int64
}

// Purge applies the retention policy once.
func (s *Service) Purge(ctx context.Context, now time.Time) (PurgeResult, error) {
	var r PurgeResult
	steps := []struct {
		n    *int64
		sql  string
		args []any
	}{
		{&r.Scans, `DELETE FROM scans WHERE scanned_at < $1`, []any{now.Add(-ScanRetention)}},
		{&r.Sessions, `DELETE FROM sessions WHERE expires_at < $1`, []any{now}},
		{&r.Tokens, `DELETE FROM email_tokens WHERE expires_at < $1`, []any{now.Add(-TokenRetention)}},
		// Unverified accounts cannot create QR codes, so there is nothing to tombstone.
		{&r.UnverifiedAccounts, `DELETE FROM accounts WHERE email_verified_at IS NULL AND created_at < $1`, []any{now.Add(-UnverifiedRetention)}},
	}
	for _, st := range steps {
		tag, err := s.db.Exec(ctx, st.sql, st.args...)
		if err != nil {
			return r, err
		}
		*st.n = tag.RowsAffected()
	}
	return r, nil
}

// RunPurge applies the retention policy now and then every interval until
// ctx is cancelled. Failures are reported to onError and retried next time.
func (s *Service) RunPurge(ctx context.Context, interval time.Duration, onResult func(PurgeResult), onError func(error)) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		r, err := s.Purge(ctx, time.Now())
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			onError(err)
		} else {
			onResult(r)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

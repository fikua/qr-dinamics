package privacy

import (
	"bytes"
	"context"
	"encoding/csv"
	"testing"
	"time"

	"qrdinamics/internal/testdb"
)

func seed(t *testing.T, s *Service, email string, verified bool, created time.Time) int64 {
	t.Helper()
	var id int64
	var verifiedAt *time.Time
	if verified {
		verifiedAt = &created
	}
	if err := s.db.QueryRow(context.Background(),
		`INSERT INTO accounts (email, password_hash, email_verified_at, created_at) VALUES ($1, 'x', $2, $3) RETURNING id`,
		email, verifiedAt, created).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestDeleteAccountLeavesAnonymousTombstones(t *testing.T) {
	ctx := context.Background()
	s := NewService(testdb.Open(t))
	id := seed(t, s, "bar@example.com", true, time.Now())
	other := seed(t, s, "altre@example.com", true, time.Now())
	var qrID int64
	s.db.QueryRow(ctx, `INSERT INTO qr_codes (account_id, code, name, destination) VALUES ($1, 'abc2345', 'Carta', 'https://example.com') RETURNING id`, id).Scan(&qrID)
	s.db.Exec(ctx, `INSERT INTO qr_codes (account_id, code, destination) VALUES ($1, 'xyz2345', 'https://other.example')`, other)
	s.db.Exec(ctx, `INSERT INTO destination_changes (qr_id, destination, is_initial) VALUES ($1, 'https://example.com', true)`, qrID)
	s.db.Exec(ctx, `INSERT INTO scans (qr_id, scanned_at) VALUES ($1, now())`, qrID)
	s.db.Exec(ctx, `INSERT INTO consents (account_id, kind, version, granted) VALUES ($1, 'terms', 'v', true)`, id)

	if err := s.DeleteAccount(ctx, id); err != nil {
		t.Fatal(err)
	}
	var status, name, dest string
	var owner *int64
	s.db.QueryRow(ctx, `SELECT status, name, destination, account_id FROM qr_codes WHERE code = 'abc2345'`).Scan(&status, &name, &dest, &owner)
	if status != "deleted" || name != "" || dest != "" || owner != nil {
		t.Fatalf("tombstone = %q %q %q %v", status, name, dest, owner)
	}
	for table, want := range map[string]int{"accounts": 1, "consents": 0, "scans": 0, "destination_changes": 0} {
		var n int
		s.db.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n)
		if n != want {
			t.Errorf("%s: %d rows left, want %d", table, n, want)
		}
	}
	s.db.QueryRow(ctx, `SELECT destination FROM qr_codes WHERE code = 'xyz2345'`).Scan(&dest)
	if dest != "https://other.example" {
		t.Fatal("another account's QR was touched")
	}
	if err := s.DeleteAccount(ctx, id); err == nil {
		t.Fatal("deleting a missing account must fail")
	}
}

func TestPurge(t *testing.T) {
	ctx := context.Background()
	s := NewService(testdb.Open(t))
	now := time.Now()
	keep := seed(t, s, "keep@example.com", true, now.Add(-400*24*time.Hour))
	seed(t, s, "old-unverified@example.com", false, now.Add(-31*24*time.Hour))
	seed(t, s, "new-unverified@example.com", false, now.Add(-24*time.Hour))
	var qrID int64
	s.db.QueryRow(ctx, `INSERT INTO qr_codes (account_id, code, destination) VALUES ($1, 'abc2345', 'https://example.com') RETURNING id`, keep).Scan(&qrID)
	s.db.Exec(ctx, `INSERT INTO scans (qr_id, scanned_at) VALUES ($1, $2), ($1, $3)`, qrID, now.Add(-ScanRetention-time.Hour), now.Add(-time.Hour))
	s.db.Exec(ctx, `INSERT INTO sessions (token_hash, account_id, expires_at) VALUES ('\x01', $1, $2), ('\x02', $1, $3)`, keep, now.Add(-time.Hour), now.Add(time.Hour))
	s.db.Exec(ctx, `INSERT INTO email_tokens (token_hash, account_id, purpose, expires_at) VALUES ('\x01', $1, 'verify_email', $2), ('\x02', $1, 'verify_email', $3)`,
		keep, now.Add(-TokenRetention-time.Hour), now.Add(time.Hour))

	r, err := s.Purge(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if r != (PurgeResult{Scans: 1, Sessions: 1, Tokens: 1, UnverifiedAccounts: 1}) {
		t.Fatalf("purge = %+v", r)
	}
	if r, _ := s.Purge(ctx, now); r != (PurgeResult{}) {
		t.Fatalf("second purge = %+v, want nothing", r)
	}

	done := make(chan struct{})
	runCtx, cancel := context.WithCancel(ctx)
	var got int
	go func() {
		s.RunPurge(runCtx, time.Hour, func(PurgeResult) { got++; cancel() }, func(error) {})
		close(done)
	}()
	<-done
	if got != 1 {
		t.Fatalf("RunPurge reported %d results before stopping, want 1", got)
	}
}

func TestExportAndCSV(t *testing.T) {
	ctx := context.Background()
	s := NewService(testdb.Open(t))
	id := seed(t, s, "bar@example.com", true, time.Now())
	var qrID int64
	s.db.QueryRow(ctx, `INSERT INTO qr_codes (account_id, code, name, destination) VALUES ($1, 'abc2345', '=HYPERLINK("x")', 'https://example.com') RETURNING id`, id).Scan(&qrID)
	s.db.Exec(ctx, `INSERT INTO qr_codes (account_id, code, destination, status) VALUES ($1, 'del2345', '', 'deleted')`, id)
	s.db.Exec(ctx, `INSERT INTO scans (qr_id, scanned_at, country, device) VALUES ($1, now(), 'ES', 'mobile')`, qrID)

	e, err := s.Export(ctx, id, "https://qr.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.QRCodes) != 1 || e.QRCodes[0].ShortURL != "https://qr.test/abc2345" || len(e.QRCodes[0].Scans) != 1 || e.QRCodes[0].Scans[0].Country != "ES" {
		t.Fatalf("export = %+v", e)
	}
	var buf bytes.Buffer
	if err := e.WriteCSV(&buf); err != nil {
		t.Fatal(err)
	}
	rows, _ := csv.NewReader(&buf).ReadAll()
	if rows[1][2] != `'=HYPERLINK("x")` {
		t.Fatalf("formula not neutralized: %q", rows[1][2])
	}
	if _, err := s.Export(ctx, 999999, ""); err == nil {
		t.Fatal("exporting a missing account must fail")
	}
}

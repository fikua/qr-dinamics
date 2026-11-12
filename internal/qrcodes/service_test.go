package qrcodes

import (
	"context"
	"errors"
	"testing"
	"time"

	"qrdinamics/internal/safety"
	"qrdinamics/internal/testdb"
)

type denyList map[string]bool

func (d denyList) Check(_ context.Context, u string) error {
	if d[u] {
		return safety.ErrUnsafe
	}
	return nil
}

func newOwner(t *testing.T, s *Service, age time.Duration) Owner {
	t.Helper()
	created := time.Now().Add(-age)
	var id int64
	if err := s.db.QueryRow(context.Background(),
		`INSERT INTO accounts (email, password_hash, email_verified_at, created_at) VALUES ($1, 'x', now(), $2) RETURNING id`,
		time.Now().Format("150405.000000")+"@example.com", created).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return Owner{ID: id, Plan: "free", CreatedAt: created}
}

func TestServiceLifecycle(t *testing.T) {
	ctx := context.Background()
	s := NewService(testdb.Open(t), denyList{"https://phish.example/": true})
	o := newOwner(t, s, 30*24*time.Hour)

	q, err := s.Create(ctx, o, "Carta", "https://example.com/estiu")
	if err != nil {
		t.Fatal(err)
	}
	if !ValidCode(q.Code) || q.Status != Active {
		t.Fatalf("created %+v", q)
	}

	if _, err := s.Create(ctx, o, "", "https://phish.example/"); !errors.Is(err, safety.ErrUnsafe) {
		t.Fatalf("unsafe destination: err = %v", err)
	}
	if _, err := s.Create(ctx, o, "", "nope"); !errors.Is(err, safety.ErrInvalid) {
		t.Fatalf("invalid destination: err = %v", err)
	}

	if err := s.ChangeDestination(ctx, o, q.ID, "https://example.com/hivern"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, o.ID, q.ID)
	if got.Code != q.Code || got.Destination != "https://example.com/hivern" {
		t.Fatalf("after edit: %+v (code must never change)", got)
	}
	h, err := s.History(ctx, q.ID)
	if err != nil || len(h) != 2 || h[0].Destination != "https://example.com/hivern" || !h[1].Initial {
		t.Fatalf("history = %+v, %v", h, err)
	}

	if err := s.SetPaused(ctx, o.ID, q.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPaused(ctx, o.ID, q.ID, true); !errors.Is(err, ErrNotEditable) {
		t.Fatalf("pausing twice: err = %v", err)
	}
	if err := s.SetPaused(ctx, o.ID, q.ID, false); err != nil {
		t.Fatal(err)
	}

	other := newOwner(t, s, 30*24*time.Hour)
	if _, err := s.Get(ctx, other.ID, q.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner read the QR: err = %v", err)
	}
	if err := s.ChangeDestination(ctx, other, q.ID, "https://evil.example"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another owner edited the QR: err = %v", err)
	}

	list, err := s.List(ctx, o.ID, "cart")
	if err != nil || len(list) != 1 {
		t.Fatalf("search by name: %v, %v", list, err)
	}
	list, _ = s.List(ctx, o.ID, "hivern")
	if len(list) != 1 {
		t.Fatalf("search by url: %d results", len(list))
	}
	list, _ = s.List(ctx, o.ID, "%")
	if len(list) != 0 {
		t.Fatalf("a %% search must be literal, got %d results", len(list))
	}

	if err := s.Delete(ctx, o.ID, q.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, o.ID, q.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted QR still visible: %v", err)
	}
	var status string
	s.db.QueryRow(ctx, `SELECT status FROM qr_codes WHERE code = $1`, q.Code).Scan(&status)
	if status != "deleted" {
		t.Fatalf("deleted QR row status = %q; the code must be kept so it is never reused", status)
	}
}

func TestQuotaAndDeleteFreesSlot(t *testing.T) {
	ctx := context.Background()
	s := NewService(testdb.Open(t), denyList{})
	o := newOwner(t, s, 30*24*time.Hour)
	var first QR
	for i := range 3 {
		q, err := s.Create(ctx, o, "", "https://example.com")
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		if i == 0 {
			first = q
		}
	}
	if _, err := s.Create(ctx, o, "", "https://example.com"); !errors.Is(err, ErrQuota) {
		t.Fatalf("4th QR: err = %v, want ErrQuota", err)
	}
	if err := s.Delete(ctx, o.ID, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, o, "", "https://example.com"); err != nil {
		t.Fatalf("after delete: %v", err)
	}
}

func TestNewAccountEditLimit(t *testing.T) {
	ctx := context.Background()
	s := NewService(testdb.Open(t), denyList{})
	o := newOwner(t, s, 2*24*time.Hour)
	q, err := s.Create(ctx, o, "", "https://example.com/0")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= newAccountMaxEdits; i++ {
		if err := s.ChangeDestination(ctx, o, q.ID, "https://example.com/"+string(rune('a'+i))); err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
	}
	if err := s.ChangeDestination(ctx, o, q.ID, "https://example.com/z"); !errors.Is(err, ErrEditLimit) {
		t.Fatalf("edit 11: err = %v, want ErrEditLimit", err)
	}
	// Setting the same destination again is not an edit.
	if err := s.ChangeDestination(ctx, o, q.ID, "https://example.com/"+string(rune('a'+newAccountMaxEdits))); err != nil {
		t.Fatalf("no-op edit: %v", err)
	}
}

func TestDailyScans(t *testing.T) {
	ctx := context.Background()
	s := NewService(testdb.Open(t), denyList{})
	o := newOwner(t, s, 30*24*time.Hour)
	q, _ := s.Create(ctx, o, "", "https://example.com")
	now := time.Now().UTC()
	for _, at := range []time.Time{now, now, now.Add(-24 * time.Hour), now.Add(-40 * 24 * time.Hour)} {
		s.db.Exec(ctx, `INSERT INTO scans (qr_id, scanned_at) VALUES ($1, $2)`, q.ID, at)
	}
	days, err := s.DailyScans(ctx, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 30 {
		t.Fatalf("%d days, want 30", len(days))
	}
	if days[29].Count != 2 || days[28].Count != 1 {
		t.Fatalf("today = %d, yesterday = %d", days[29].Count, days[28].Count)
	}
	got, _ := s.Get(ctx, o.ID, q.ID)
	if got.TotalScans != 4 {
		t.Fatalf("total = %d, want 4", got.TotalScans)
	}
}

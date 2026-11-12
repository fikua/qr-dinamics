// Package testdb gives integration tests a migrated, empty database.
// Tests using it are skipped unless TEST_DATABASE_URL is set.
package testdb

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"qrdinamics/internal/db"
)

// Open returns a pool on a clean, migrated database, or skips the test.
func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE accounts, sessions, email_tokens, qr_codes, destination_changes, scans RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	return pool
}

// Package dbtest gives integration tests a connection to the test database,
// or skips them when it is not available. It never starts a service.
package dbtest

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/florianorpeliere/grimoire/internal/db"
)

// Name is the only database integration tests may run against.
const Name = "grimoire_test"

// Open connects to the database named by DATABASE_URL and returns it.
// The test is skipped when DATABASE_URL is unset, when the database is
// unreachable, or when it is not the grimoire_test database.
func Open(t testing.TB) *sql.DB {
	t.Helper()
	url, err := db.URLFromEnv(os.Getenv)
	if err != nil {
		t.Skipf("skipping integration test: %s is not set (point it at the %s database to run it)", db.EnvURL, Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := db.Open(ctx, url)
	if err != nil {
		t.Skipf("skipping integration test: database at %s is unreachable: %v", db.EnvURL, err)
	}
	var name string
	if err := conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil {
		conn.Close()
		t.Skipf("skipping integration test: cannot read the database name from %s: %v", db.EnvURL, err)
	}
	if name != Name {
		conn.Close()
		t.Skipf("skipping integration test: %s points at database %q, integration tests only run against %q", db.EnvURL, name, Name)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// Reset empties the test database by recreating the public schema,
// which also drops the extensions installed in it.
func Reset(t testing.TB, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"DROP SCHEMA IF EXISTS public CASCADE",
		"CREATE SCHEMA public",
	} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("reset test database: %s: %v", stmt, err)
		}
	}
}

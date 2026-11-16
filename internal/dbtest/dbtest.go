// Package dbtest gives integration tests a connection to the test database,
// or skips them when it is not available. It never starts a service.
package dbtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/florianorpeliere/grimoire/internal/db"
)

// Name is the only database integration tests may run against.
const Name = "grimoire_test"

// Open connects to the database named by DATABASE_URL and returns it.
// The test is skipped when DATABASE_URL is unset or incomplete, when the
// database is unreachable, or when it is not the grimoire_test database.
func Open(t testing.TB) *sql.DB {
	t.Helper()
	url, err := db.URLFromEnv(os.Getenv)
	if errors.Is(err, db.ErrNoURL) {
		t.Skipf("skipping integration test: %s is not set (point it at the %s database to run it)", db.EnvURL, Name)
	}
	if err != nil {
		t.Skipf("skipping integration test: %v", err)
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
	if err := checkName(name); err != nil {
		conn.Close()
		t.Skipf("skipping integration test: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// checkName accepts only the test database.
func checkName(name string) error {
	if name != Name {
		return fmt.Errorf("%s points at database %q, integration tests only run against %q", db.EnvURL, name, Name)
	}
	return nil
}

// Reset empties the test database by recreating the public schema, which
// also drops the extensions installed in it.
//
// The database check and the drop are one statement, so they run on the same
// connection and Reset refuses any database but grimoire_test, whatever
// connection it is given.
func Reset(t testing.TB, conn *sql.DB) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), resetStatement(Name)); err != nil {
		t.Fatalf("reset test database: %v", err)
	}
}

// resetStatement drops and recreates the public schema, but raises an error
// instead when the current database is not want. want is a trusted constant.
// pg_catalog is explicit so a function planted earlier in search_path cannot
// stand in for current_database().
func resetStatement(want string) string {
	return fmt.Sprintf(`DO $reset$
BEGIN
	IF pg_catalog.current_database() <> '%s' THEN
		RAISE EXCEPTION 'refusing to reset database %%: only %s may be reset', pg_catalog.current_database();
	END IF;
	DROP SCHEMA IF EXISTS public CASCADE;
	CREATE SCHEMA public;
END
$reset$`, want, want)
}

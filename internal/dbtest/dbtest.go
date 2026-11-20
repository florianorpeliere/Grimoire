// Package dbtest gives integration tests a connection to the test database.
//
// The connection string is read from TEST_DATABASE_URL only, never from
// DATABASE_URL, which belongs to the application: a loaded development
// environment cannot point the tests at the development database.
//
//   - TEST_DATABASE_URL unset: the integration test is skipped. This is the
//     only case that skips.
//   - TEST_DATABASE_URL set: anything that prevents the test from running
//     fails it, with a message naming TEST_DATABASE_URL and the case: empty,
//     incomplete or malformed URL, unreachable server, missing database,
//     authentication failure, or a database other than grimoire_test.
//
// Reset only ever empties grimoire_test. The package never starts a service.
package dbtest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/florianorpeliere/grimoire/internal/db"
)

// EnvURL is the only variable integration tests read their connection string from.
const EnvURL = "TEST_DATABASE_URL"

// Name is the only database integration tests may run against.
const Name = "grimoire_test"

// SQLSTATE codes the server returns when it refuses a connection.
const (
	codeInvalidCatalogName = "3D000" // the database does not exist
	codeInvalidAuthSpec    = "28000" // e.g. the role does not exist
	codeInvalidPassword    = "28P01"
)

// Open connects to the database named by TEST_DATABASE_URL and returns it.
// The test is skipped when TEST_DATABASE_URL is unset, and fails for any
// other reason the database cannot be used (see the package comment).
func Open(t testing.TB) *sql.DB {
	t.Helper()
	raw, ok := os.LookupEnv(EnvURL)
	if !ok {
		t.Skipf("skipping integration test: %s is not set (point it at the %s database to run it)", EnvURL, Name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := connect(ctx, raw)
	if err != nil {
		t.Fatalf("integration test setup: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// connect opens raw and checks it is the test database. Its errors name
// TEST_DATABASE_URL and the case, and never echo the URL.
func connect(ctx context.Context, raw string) (*sql.DB, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%s is set but empty: unset it to skip integration tests, or point it at the %s database", EnvURL, Name)
	}
	if err := db.CheckURL(raw); err != nil {
		return nil, fmt.Errorf("%s is incomplete or malformed: %w", EnvURL, err)
	}
	conn, err := db.Open(ctx, raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %s: %w", EnvURL, connectCase(err), err)
	}
	var name string
	if err := conn.QueryRowContext(ctx, "SELECT current_database()").Scan(&name); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%s: cannot read the database name: %w", EnvURL, err)
	}
	if err := checkName(name); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// connectCase names why a connection failed, from the server's SQLSTATE.
// Without one, the connection failed before the server refused it: the
// server is unreachable, or e.g. the TLS negotiation failed.
func connectCase(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "cannot connect"
	}
	switch pgErr.Code {
	case codeInvalidCatalogName:
		return "database does not exist"
	case codeInvalidAuthSpec, codeInvalidPassword:
		return "authentication failed"
	default:
		return "connection refused by the server"
	}
}

// checkName accepts only the test database.
func checkName(name string) error {
	if name != Name {
		return fmt.Errorf("%s points at database %q, integration tests only run against %q", EnvURL, name, Name)
	}
	return nil
}

// lockKey identifies the advisory lock that serialises the tests calling Reset.
const lockKey = 0x67726d74 // "grmt"

// lockWait bounds how long Reset waits for another test to release the lock.
const lockWait = 2 * time.Minute

// Reset empties the test database by recreating the public schema, which
// also drops the extensions installed in it.
//
// go test runs the packages in parallel, and they share grimoire_test, so
// Reset first takes a PostgreSQL advisory lock that it holds until the test
// and its subtests end: the tests calling Reset run one at a time. Call it
// once per test, from the top-level test.
//
// The database check and the drop are one statement, so they run on the same
// connection and Reset refuses any database but grimoire_test, whatever
// connection it is given.
func Reset(t testing.TB, conn *sql.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	defer cancel()
	lock, err := conn.Conn(ctx)
	if err != nil {
		t.Fatalf("reset test database: %v", err)
	}
	if _, err := lock.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		lock.Close()
		t.Fatalf("reset test database: wait for the other integration tests: %v", err)
	}
	t.Cleanup(func() {
		lock.ExecContext(context.Background(), "SELECT pg_advisory_unlock($1)", lockKey)
		lock.Close()
	})
	if _, err := conn.ExecContext(ctx, resetStatement(Name)); err != nil {
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

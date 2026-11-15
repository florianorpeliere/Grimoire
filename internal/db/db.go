// Package db opens the PostgreSQL connection and applies the schema migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// EnvURL is the only place the connection string is read from.
const EnvURL = "DATABASE_URL"

var (
	// ErrNoURL is returned when DATABASE_URL is unset or blank.
	ErrNoURL = errors.New(EnvURL + " is not set")
	// ErrIncompleteURL is returned when DATABASE_URL does not state where to connect.
	ErrIncompleteURL = errors.New(EnvURL + " must be a postgres:// URL with an explicit host, port and database name")
)

// locationParams are query parameters that would override the host, port or
// database written in the URL, or pull them from a service file.
var locationParams = []string{"host", "hostaddr", "port", "dbname", "database", "service", "servicefile"}

// URLFromEnv returns the connection string from DATABASE_URL.
// There is deliberately no default: an unset variable is an error.
//
// The URL must name a single host, a port and a database explicitly.
// Otherwise the driver would fill the missing parts from libpq defaults
// (port 5432, a Unix socket) or from PGHOST, PGPORT, PGDATABASE and
// PGSERVICE, and could connect to another PostgreSQL instance.
// Parts given explicitly in the URL take precedence over those variables.
func URLFromEnv(getenv func(string) string) (string, error) {
	raw := strings.TrimSpace(getenv(EnvURL))
	if raw == "" {
		return "", ErrNoURL
	}
	if err := checkLocation(raw); err != nil {
		return "", err
	}
	return raw, nil
}

// checkLocation reports whether raw states host, port and database itself.
// Its errors never echo the URL, which may contain a password.
func checkLocation(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: it cannot be parsed as a URL", ErrIncompleteURL)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return fmt.Errorf("%w: the scheme is not postgres://", ErrIncompleteURL)
	}
	if strings.Contains(u.Host, ",") {
		return fmt.Errorf("%w: it names several hosts", ErrIncompleteURL)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("%w: the host is missing", ErrIncompleteURL)
	}
	if u.Port() == "" {
		return fmt.Errorf("%w: the port is missing", ErrIncompleteURL)
	}
	if strings.TrimPrefix(u.Path, "/") == "" {
		return fmt.Errorf("%w: the database name is missing", ErrIncompleteURL)
	}
	q := u.Query()
	for _, p := range locationParams {
		if q.Has(p) {
			return fmt.Errorf("%w: the %q parameter would override it", ErrIncompleteURL, p)
		}
	}
	return nil
}

// Open connects to the database at url and checks it is reachable.
func Open(ctx context.Context, url string) (*sql.DB, error) {
	conn, err := sql.Open("pgx", url)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("reach database: %w", err)
	}
	return conn, nil
}

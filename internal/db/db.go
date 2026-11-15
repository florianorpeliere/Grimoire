// Package db opens the PostgreSQL connection and applies the schema migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	// Registers the "pgx" database/sql driver.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// EnvURL is the only place the connection string is read from.
const EnvURL = "DATABASE_URL"

// ErrNoURL is returned when DATABASE_URL is unset or blank.
var ErrNoURL = errors.New(EnvURL + " is not set")

// URLFromEnv returns the connection string from DATABASE_URL.
// There is deliberately no default: an unset variable is an error.
func URLFromEnv(getenv func(string) string) (string, error) {
	url := strings.TrimSpace(getenv(EnvURL))
	if url == "" {
		return "", ErrNoURL
	}
	return url, nil
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

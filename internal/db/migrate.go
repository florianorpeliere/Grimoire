package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedded embed.FS

// newProvider builds a goose provider over the embedded migrations.
// The caller keeps ownership of conn: do not call Close on the provider.
func newProvider(conn *sql.DB) (*goose.Provider, error) {
	migrations, err := fs.Sub(embedded, "migrations")
	if err != nil {
		return nil, err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, conn, migrations)
	if err != nil {
		return nil, fmt.Errorf("load migrations: %w", err)
	}
	return p, nil
}

// Migrate applies every pending migration and returns the versions it applied.
// When the schema is already up to date it applies nothing and returns no error.
func Migrate(ctx context.Context, conn *sql.DB) ([]int64, error) {
	p, err := newProvider(conn)
	if err != nil {
		return nil, err
	}
	results, err := p.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("apply migrations: %w", err)
	}
	applied := make([]int64, 0, len(results))
	for _, r := range results {
		applied = append(applied, r.Source.Version)
	}
	return applied, nil
}

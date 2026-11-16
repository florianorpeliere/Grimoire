package db_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/dbtest"
)

func TestURLFromEnv(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr error
	}{
		{name: "unset", env: map[string]string{}, wantErr: db.ErrNoURL},
		{name: "blank", env: map[string]string{"DATABASE_URL": "   "}, wantErr: db.ErrNoURL},
		{
			name: "valid URL",
			env:  map[string]string{"DATABASE_URL": "postgres://user@db.example:5433/grimoire?sslmode=disable"},
			want: "postgres://user@db.example:5433/grimoire?sslmode=disable",
		},
		{
			name: "postgresql scheme, trimmed",
			env:  map[string]string{"DATABASE_URL": " postgresql://db.example:5433/grimoire_test\n"},
			want: "postgresql://db.example:5433/grimoire_test",
		},
		{
			name:    "other variables are ignored",
			env:     map[string]string{"PGHOST": "db.example", "PGPORT": "5433", "PGDATABASE": "grimoire"},
			wantErr: db.ErrNoURL,
		},
		// What a shell leaves after sourcing an unquoted <user> placeholder.
		{name: "empty authority", env: map[string]string{"DATABASE_URL": "postgres://"}, wantErr: db.ErrIncompleteURL},
		{name: "empty host", env: map[string]string{"DATABASE_URL": "postgres://:5433/grimoire"}, wantErr: db.ErrIncompleteURL},
		{name: "missing port", env: map[string]string{"DATABASE_URL": "postgres://db.example/grimoire"}, wantErr: db.ErrIncompleteURL},
		{name: "missing database", env: map[string]string{"DATABASE_URL": "postgres://db.example:5433"}, wantErr: db.ErrIncompleteURL},
		{name: "database slash only", env: map[string]string{"DATABASE_URL": "postgres://db.example:5433/"}, wantErr: db.ErrIncompleteURL},
		{name: "several hosts", env: map[string]string{"DATABASE_URL": "postgres://a.example:5433,b.example:5433/grimoire"}, wantErr: db.ErrIncompleteURL},
		{name: "host parameter overrides", env: map[string]string{"DATABASE_URL": "postgres://db.example:5433/grimoire?host=/tmp"}, wantErr: db.ErrIncompleteURL},
		{name: "port parameter overrides", env: map[string]string{"DATABASE_URL": "postgres://db.example:5433/grimoire?port=5432"}, wantErr: db.ErrIncompleteURL},
		{name: "dbname parameter overrides", env: map[string]string{"DATABASE_URL": "postgres://db.example:5433/grimoire?dbname=other"}, wantErr: db.ErrIncompleteURL},
		{name: "service parameter", env: map[string]string{"DATABASE_URL": "postgres://db.example:5433/grimoire?service=other"}, wantErr: db.ErrIncompleteURL},
		{name: "keyword/value form", env: map[string]string{"DATABASE_URL": "host=db.example port=5433 dbname=grimoire"}, wantErr: db.ErrIncompleteURL},
		{name: "unparsable", env: map[string]string{"DATABASE_URL": "postgres://%zz"}, wantErr: db.ErrIncompleteURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := db.URLFromEnv(func(k string) string { return tt.env[k] })
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("url = %q, want %q", got, tt.want)
			}
		})
	}
}

// Open checks the URL before connecting: none of these reach a server.
func TestOpenRefusesIncompleteURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{name: "empty authority", url: "postgres://"},
		{name: "missing port", url: "postgres://db.example/grimoire"},
		{name: "missing database", url: "postgres://db.example:5433"},
		{name: "unix socket", url: "postgres:///grimoire?host=/tmp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := db.Open(context.Background(), tt.url)
			if conn != nil {
				conn.Close()
			}
			if !errors.Is(err, db.ErrIncompleteURL) {
				t.Fatalf("err = %v, want %v", err, db.ErrIncompleteURL)
			}
		})
	}
}

func TestMigrate(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()

	all, err := db.Versions(conn)
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("no embedded migrations")
	}

	steps := []struct {
		name        string
		wantApplied []int64
	}{
		{name: "empty database applies all migrations", wantApplied: all},
		{name: "second run changes nothing", wantApplied: []int64{}},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			applied, err := db.Migrate(ctx, conn)
			if err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if !slices.Equal(applied, s.wantApplied) {
				t.Errorf("applied = %v, want %v", applied, s.wantApplied)
			}
			var hasVector bool
			err = conn.QueryRowContext(ctx,
				"SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector')").Scan(&hasVector)
			if err != nil {
				t.Fatalf("query pg_extension: %v", err)
			}
			if !hasVector {
				t.Error("vector extension is not installed")
			}
		})
	}
}

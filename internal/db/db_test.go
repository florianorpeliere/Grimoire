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
			name: "set",
			env:  map[string]string{"DATABASE_URL": "postgres://user@db.example:5433/grimoire"},
			want: "postgres://user@db.example:5433/grimoire",
		},
		{
			name: "trimmed",
			env:  map[string]string{"DATABASE_URL": " postgres://db.example/grimoire\n"},
			want: "postgres://db.example/grimoire",
		},
		{
			name:    "other variables are ignored",
			env:     map[string]string{"PGHOST": "db.example", "PGPORT": "5433"},
			wantErr: db.ErrNoURL,
		},
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

func TestMigrate(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()

	steps := []struct {
		name        string
		wantApplied []int64
	}{
		{name: "empty database applies all migrations", wantApplied: []int64{1}},
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

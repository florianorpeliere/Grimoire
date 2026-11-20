package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// These cases never reach a database: they fail before connecting.
func TestRunErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr error
	}{
		{name: "no command", args: nil, wantErr: errUsage},
		{name: "unknown command", args: []string{"serve"}, wantErr: errUsage},
		{name: "migrate with extra argument", args: []string{"migrate", "down"}, wantErr: errUsage},
		{name: "migrate without DATABASE_URL", args: []string{"migrate"}, env: map[string]string{}, wantErr: db.ErrNoURL},
		{name: "srd without DATABASE_URL", args: []string{"srd", "missing.md"}, env: map[string]string{}, wantErr: db.ErrNoURL},
		{name: "srd with an incomplete DATABASE_URL", args: []string{"srd", "missing.md"}, env: map[string]string{"DATABASE_URL": "postgres://db.example/grimoire"}, wantErr: db.ErrIncompleteURL},
		{name: "srd with two files, storing", args: []string{"srd", "a.md", "b.md"}, wantErr: errUsage},
		{name: "srd storing a missing file", args: []string{"srd", "missing.md"}, env: map[string]string{"DATABASE_URL": "postgres://127.0.0.1:1/grimoire_test?sslmode=disable"}, wantErr: os.ErrNotExist},
		{name: "srd -dry-run with -allow-large-delete", args: []string{"srd", "-dry-run", "-allow-large-delete"}, wantErr: errUsage},
		{name: "srd with an unknown flag", args: []string{"srd", "-store"}, wantErr: errUsage},
		{name: "srd with two files", args: []string{"srd", "-dry-run", "a.md", "b.md"}, wantErr: errUsage},
		{name: "srd with a missing file", args: []string{"srd", "-dry-run", "missing.md"}, wantErr: os.ErrNotExist},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(context.Background(), tt.args, func(k string) string { return tt.env[k] }, &out)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// The usage text states the delete limit, which lives in internal/srd.
func TestUsageStatesDeleteShare(t *testing.T) {
	want := fmt.Sprintf("more than %g%% of the stored chunks", srd.MaxDeleteShare*100)
	if !strings.Contains(usage, want) {
		t.Errorf("usage does not contain %q:\n%s", want, usage)
	}
}

package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/db"
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

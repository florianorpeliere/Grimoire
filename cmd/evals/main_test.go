package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/evalset"
)

func TestParseArgs(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCmd  string
		wantFile string
		wantErr  error
	}{
		{name: "default file", args: []string{"retrieval"}, wantCmd: cmdRetrieval, wantFile: defaultSmokeFile},
		{name: "file given", args: []string{"retrieval", "other.txt"}, wantCmd: cmdRetrieval, wantFile: "other.txt"},
		{name: "check, default file", args: []string{"check"}, wantCmd: cmdCheck, wantFile: evalset.DefaultFile},
		{name: "check, file given", args: []string{"check", "other.txt"}, wantCmd: cmdCheck, wantFile: "other.txt"},
		{name: "help", args: []string{"-h"}, wantErr: flag.ErrHelp},
		{name: "subcommand help", args: []string{"retrieval", "-h"}, wantErr: flag.ErrHelp},
		{name: "check help", args: []string{"check", "--help"}, wantErr: flag.ErrHelp},
		{name: "no subcommand", args: nil, wantErr: errUsage},
		{name: "unknown subcommand", args: []string{"answers"}, wantErr: errUsage},
		{name: "unknown flag", args: []string{"retrieval", "-json"}, wantErr: errUsage},
		{name: "two files", args: []string{"retrieval", "a.txt", "b.txt"}, wantErr: errUsage},
		{name: "check, two files", args: []string{"check", "a.txt", "b.txt"}, wantErr: errUsage},
		{name: "check, unknown flag", args: []string{"check", "-v"}, wantErr: errUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, file, err := parseArgs(tt.args)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if cmd != tt.wantCmd {
				t.Errorf("subcommand = %q, want %q", cmd, tt.wantCmd)
			}
			if file != tt.wantFile {
				t.Errorf("file = %q, want %q", file, tt.wantFile)
			}
		})
	}
}

// TestRunFailsBeforeConnecting checks the errors found before any
// connection: nothing is printed and no embedder is created.
func TestRunFailsBeforeConnecting(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.txt")
	good := filepath.Join(dir, "good.txt")
	badSet := filepath.Join(dir, "bad-set.txt")
	if err := os.WriteFile(badSet, []byte("1 | rules | q | a | x.y\n2 | trivia | q2 | a | x.y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("1 | named | exact | q\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(good, []byte("1 | named | exact | q | x.y\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	noEmbedder := func(func(string) string) (embed.Embedder, error) {
		t.Error("the embedder was created, want none")
		return nil, errors.New("no embedder")
	}
	noEnv := func(string) string { return "" }
	tests := []struct {
		name    string
		args    []string
		wantErr error
		wantMsg string
	}{
		{name: "missing file", args: []string{"retrieval", filepath.Join(dir, "none.txt")}, wantErr: os.ErrNotExist},
		{name: "malformed file", args: []string{"retrieval", bad}, wantMsg: "line 1: want 5 fields"},
		{name: "DATABASE_URL unset", args: []string{"retrieval", good}, wantErr: db.ErrNoURL},
		{name: "check, missing file", args: []string{"check", filepath.Join(dir, "none.txt")}, wantErr: os.ErrNotExist},
		{name: "check, malformed record", args: []string{"check", badSet}, wantMsg: `line 2: category "trivia"`},
		{name: "check, DATABASE_URL unset", args: []string{"check", filepath.Join(repoRoot, evalset.DefaultFile)}, wantErr: db.ErrNoURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(context.Background(), tt.args, noEnv, &out, noEmbedder)
			if err == nil {
				t.Fatal("err = nil, want an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantMsg)
			}
			if out.Len() > 0 {
				t.Errorf("printed %q, want nothing", out.String())
			}
		})
	}
}

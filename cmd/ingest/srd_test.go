package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/dbtest"
	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
	"github.com/florianorpeliere/grimoire/internal/embed/ollama"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// noEmbedder fails the test if the command asks for an embedding provider.
func noEmbedder(t *testing.T) newEmbedder {
	return func(func(string) string) (embed.Embedder, error) {
		t.Helper()
		t.Fatal("the command asked for an embedding provider")
		return nil, nil
	}
}

// fakeEmbedder makes the command use f.
func fakeEmbedder(f *embedtest.Fake) newEmbedder {
	return func(func(string) string) (embed.Embedder, error) { return f, nil }
}

// writeFixture writes dryRunFixture to a temporary file and returns its path.
func writeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "srd.md")
	if err := os.WriteFile(path, []byte(dryRunFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The provider is configured before the database is opened: these cases
// fail without connecting (DATABASE_URL points at a closed port).
func TestSRDEmbedderConfig(t *testing.T) {
	path := writeFixture(t)
	tests := []struct {
		name    string
		host    string
		wantErr error
	}{
		{name: "OLLAMA_HOST unset", host: "", wantErr: ollama.ErrNoHost},
		{name: "OLLAMA_HOST without scheme", host: "localhost:11434", wantErr: ollama.ErrBadHost},
		{name: "OLLAMA_HOST with another scheme", host: "ftp://localhost:11434", wantErr: ollama.ErrBadHost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := map[string]string{
				"DATABASE_URL": "postgres://127.0.0.1:1/grimoire_test?sslmode=disable",
				ollama.EnvHost: tt.host,
			}
			var out bytes.Buffer
			err := run(context.Background(), []string{"srd", path}, func(k string) string { return env[k] }, &out, ollamaFromEnv)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// The fixture is a short excerpt of the SRD 5.2.1 (CC-BY-4.0, see ATTRIBUTION.md):
// every line is a whole line of the pinned SRD file, as written; some lines
// are left out. TestDryRunFixtureIsSRDText checks it against that file.
const dryRunFixture = `# Spells

## Spell Descriptions

### Fireball

_Level 3 Evocation (Sorcerer, Wizard)_

## Casting Spells

### Spell Level

Every spell has a level from 0 to 9, which is indicated in a spell's description. A spell's level is an indicator of how powerful it is. Cantrips — simple spells that can be cast almost by rote — are level 0. The rules for each spellcasting class say when its members gain access to spells of certain levels.
`

// TestDryRunFixtureIsSRDText checks that every non-blank line of dryRunFixture
// is a whole line of the SRD file downloaded by scripts/srd-download.sh. It is
// skipped when the file is absent; internal/srd's TestFullFile checks that the
// file is the pinned one.
func TestDryRunFixtureIsSRDText(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", srd.DefaultPath))
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("skipping: %s not found; run scripts/srd-download.sh to get the SRD Markdown", srd.DefaultPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		lines[l] = true
	}
	// This proves each line exists in the file, not that it sits under the right heading.
	for i, l := range strings.Split(dryRunFixture, "\n") {
		if strings.TrimSpace(l) != "" && !lines[l] {
			t.Errorf("fixture line %d is not a line of %s: %q", i+1, srd.DefaultPath, l)
		}
	}
}

func TestSRDDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "srd.md")
	if err := os.WriteFile(path, []byte(dryRunFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// No DATABASE_URL nor embedding provider: the dry run needs neither.
	if err := run(context.Background(), []string{"srd", "-dry-run", path}, func(string) string { return "" }, &out, noEmbedder(t)); err != nil {
		t.Fatalf("run: %v", err)
	}
	tests := []struct{ name, line string }{
		{"total", "chunks: 2\n"},
		{"rule count", "  rule             1\n"},
		{"spell count", "  spell            1\n"},
		{"size summary", "  min 10, median 10, p95 77, max 77\n"},
		{"first bucket", "      1-100       2\n"},
		{"large chunks", "chunks above 4000 estimated tokens: 0\n"},
		{"spell path", "  spells.fireball\tspell\t10\tSpells > Fireball\n"},
		{"rule path", "  spells.casting-spells.spell-level\trule\t77\tSpells > Casting Spells > Spell Level\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(out.String(), tt.line) {
				t.Errorf("report has no line %q:\n%s", tt.line, out.String())
			}
		})
	}
}

// TestSRDStore stores and embeds the fixture in grimoire_test through the
// command, as "ingest srd" does with DATABASE_URL, with a fake provider.
func TestSRDStore(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	path := writeFixture(t)
	// The fixture with the Fireball text changed: one chunk to embed again.
	changed := filepath.Join(t.TempDir(), "changed.md")
	edited := strings.Replace(dryRunFixture, "_Level 3 Evocation (Sorcerer, Wizard)_", "_Level 3 Evocation (Wizard)_", 1)
	if err := os.WriteFile(changed, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	// The changed fixture without its "Casting Spells" section: one chunk of two.
	partial := filepath.Join(t.TempDir(), "partial.md")
	cut := strings.Index(edited, "## Casting Spells")
	if err := os.WriteFile(partial, []byte(edited[:cut]), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"DATABASE_URL": strings.TrimSpace(os.Getenv(dbtest.EnvURL))}
	getenv := func(k string) string { return env[k] }

	fake := &embedtest.Fake{ModelName: "fake-model", Dim: 1024}
	failing := &embedtest.Fake{ModelName: "fake-model", Dim: 1024, Err: errors.New("model exploded")}
	narrow := &embedtest.Fake{ModelName: "narrow-model", Dim: 8}

	// Each step runs on the database left by the previous one.
	steps := []struct {
		name     string
		args     []string
		embedder *embedtest.Fake // fake when nil
		wantErr  string
		wantOut  []string
		// wantEmbedded lists the ids whose input the provider received, in order.
		wantEmbedded []string
	}{
		{
			name:    "before migrate: names the missing step",
			args:    []string{"srd", path},
			wantErr: `run "ingest migrate" first`,
		},
		{name: "migrate", args: []string{"migrate"}, wantOut: []string{"migrate: applied version"}},
		{
			name:     "persistent provider error: the run fails with its message, chunks stay stored",
			args:     []string{"srd", path},
			embedder: failing,
			wantErr:  "model exploded",
		},
		{
			name:     "provider dimension differs from the column: refused before embedding",
			args:     []string{"srd", path},
			embedder: narrow,
			wantErr:  "model narrow-model returns 8 dimensions, srd_chunks.embedding is vector(1024)",
		},
		{
			name:         "first full run: every chunk embedded",
			args:         []string{"srd", path},
			wantOut:      []string{"chunks: 2\n", "inserted 0, updated 0, unchanged 2, deleted 0\n", "rows stored: 2\n", "embeddings: model fake-model, 1024 dimensions\n", "embedded 2, kept 0, in "},
			wantEmbedded: []string{"spells.casting-spells.spell-level", "spells.fireball"},
		},
		{
			name:    "second run: nothing re-inserted nor re-embedded",
			args:    []string{"srd", path},
			wantOut: []string{"chunks: 2\n", "inserted 0, updated 0, unchanged 2, deleted 0\n", "rows stored: 2\n", "embedded 0, kept 2, in "},
		},
		{
			name:         "changed chunk: only it is embedded again",
			args:         []string{"srd", changed},
			wantOut:      []string{"inserted 0, updated 1, unchanged 1, deleted 0\n", "embedded 1, kept 1, in "},
			wantEmbedded: []string{"spells.fireball"},
		},
		{
			name:    "partial file: refused, the flag is named",
			args:    []string{"srd", partial},
			wantErr: "1 of 2 (more than 5%); nothing was changed (check the file; pass -allow-large-delete",
		},
		{
			name:    "partial file with -allow-large-delete: the removed chunk is deleted",
			args:    []string{"srd", "-allow-large-delete", partial},
			wantOut: []string{"chunks: 1\n", "inserted 0, updated 0, unchanged 1, deleted 1\n", "rows stored: 1\n", "embedded 0, kept 1, in "},
		},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			e := s.embedder
			if e == nil {
				e = fake
			}
			fake.Reset()
			var out bytes.Buffer
			err := run(ctx, s.args, getenv, &out, fakeEmbedder(e))
			if s.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), s.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, s.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			for _, line := range s.wantOut {
				if !strings.Contains(out.String(), line) {
					t.Errorf("output has no %q:\n%s", line, out.String())
				}
			}
			var got []string
			for _, text := range fake.Texts() {
				got = append(got, embeddedID(t, conn, text))
			}
			if !slices.Equal(got, s.wantEmbedded) {
				t.Errorf("embedded %v, want %v", got, s.wantEmbedded)
			}
		})
	}
	var missing int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM srd_chunks WHERE embedding IS NULL").Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if missing != 0 {
		t.Errorf("%d chunks have no embedding after the runs", missing)
	}
}

// embeddedID returns the id of the stored chunk whose embedding input is text.
func embeddedID(t *testing.T, conn *sql.DB, text string) string {
	t.Helper()
	var id string
	err := conn.QueryRowContext(context.Background(),
		"SELECT id FROM srd_chunks WHERE path || E'\\n\\n' || text = $1", text).Scan(&id)
	if err != nil {
		t.Fatalf("no stored chunk has the embedded input %q: %v", text, err)
	}
	return id
}

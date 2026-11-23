package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/dbtest"
	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// fixtures are shaped like the chunker output.
var fixtures = []srd.Chunk{
	{
		ID: "spells.fireball", Path: []string{"Spells", "Fireball"},
		FullPath: []string{"Spells", "Spell Descriptions", "Fireball"},
		Title:    "Fireball", Kind: srd.KindSpell,
		Text: "A bright streak flashes to a point you choose and blossoms into an explosion of flame.",
	},
	{
		ID: "rules-glossary.grappled", Path: []string{"Rules Glossary", "Grappled"},
		FullPath: []string{"Rules Glossary", "Rules Definitions", "Grappled"},
		Title:    "Grappled", Kind: srd.KindCondition,
		Text: "While you have this condition, your Speed is 0 and can't increase.",
	},
}

// testEnv points DATABASE_URL at the test database.
func testEnv(k string) string {
	if k == db.EnvURL {
		return os.Getenv(dbtest.EnvURL)
	}
	return ""
}

func writeSmoke(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "smoke.txt")
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// TestRetrievalDatabase runs the command against the test database, from no
// schema to embedded chunks, with a fake embedder. The steps depend on each
// other, so a failure stops the test.
func TestRetrievalDatabase(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	fake := &embedtest.Fake{ModelName: "fake-model", Dim: 1024}
	factory := func(func(string) string) (embed.Embedder, error) { return fake, nil }
	smoke := writeSmoke(t, "1 | named | exact | Fireball | spells.fireball\n"+
		"2 | described | - | my speed is 0 | rules-glossary.grappled spells.fireball\n")

	fails := func(t *testing.T, file, wantMsg string, factory newEmbedder) {
		t.Helper()
		var out bytes.Buffer
		err := run(ctx, []string{"retrieval", file}, testEnv, &out, factory)
		if err == nil || !strings.Contains(err.Error(), wantMsg) {
			t.Fatalf("err = %v, want it to contain %q", err, wantMsg)
		}
		if out.Len() > 0 {
			t.Fatalf("printed %q, want nothing", out.String())
		}
	}

	fails(t, smoke, "no srd_chunks table", factory)
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	fails(t, smoke, "no SRD chunk in the database", factory)

	chunks := append([]srd.Chunk(nil), fixtures...)
	for i := range chunks {
		chunks[i].Hash = "hash " + chunks[i].ID
	}
	if _, err := srd.Store(ctx, conn, srd.Version, chunks, srd.StoreOptions{}); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if _, err := srd.Embed(ctx, conn, fake, 0); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	fake.Reset()

	// Unknown ids fail the run before any search, each listed once.
	fails(t, writeSmoke(t, "1 | named | exact | Fireball | spells.fireball spells.nope\n"+
		"2 | named | exact | Mage Armor | spells.mage-armor spells.nope\n"),
		"2 expected chunk id(s) are not stored chunks (check them against ingest srd -dry-run): spells.mage-armor, spells.nope",
		factory)
	if texts := fake.Texts(); len(texts) > 0 {
		t.Errorf("embedded %q before the ids were checked, want nothing", texts)
	}

	t.Run("every configuration", func(t *testing.T) {
		var out bytes.Buffer
		if err := run(ctx, []string{"retrieval", smoke}, testEnv, &out, factory); err != nil {
			t.Fatalf("run: %v", err)
		}
		got := out.String()
		for _, want := range []string{"Embedding model: fake-model", "keyword", "vector", "hybrid-prefix-off", "Misses"} {
			if !strings.Contains(got, want) {
				t.Errorf("report has no %q:\n%s", want, got)
			}
		}
		if strings.Contains(got, "skipped") {
			t.Errorf("report has a skipped configuration:\n%s", got)
		}
		// Keyword ranks the exact title first.
		if !strings.Contains(strings.Join(strings.Fields(got), " "), "keyword named 1/1 (100%) 1/1 (100%)") {
			t.Errorf("report has no keyword hit for Fireball:\n%s", got)
		}
	})

	// Whatever the embedding error, keyword still runs and is reported, the
	// Ollama configurations are skipped with the error, and the run fails.
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "Ollama unreachable", err: errRefused},
		{name: "Ollama returns an error", err: errModelMissing},
	} {
		t.Run(tt.name, func(t *testing.T) {
			down := &embedtest.Fake{ModelName: "fake-model", Dim: 1024, Err: tt.err}
			var out bytes.Buffer
			err := run(ctx, []string{"retrieval", smoke}, testEnv, &out,
				func(func(string) string) (embed.Embedder, error) { return down, nil })
			if err == nil || !strings.Contains(err.Error(), "cannot use the Ollama server at OLLAMA_HOST") ||
				!errors.Is(err, tt.err) {
				t.Fatalf("err = %v, want the Ollama server named and %v", err, tt.err)
			}
			got := strings.Join(strings.Fields(out.String()), " ")
			for _, want := range []string{"keyword named 1/1 (100%)", "hybrid (skipped)", "vector (skipped)",
				"Skipped because: question 1: embed the question: " + tt.err.Error()} {
				if !strings.Contains(got, strings.Join(strings.Fields(want), " ")) {
					t.Errorf("report has no %q:\n%s", want, out.String())
				}
			}
			if strings.Contains(got, "Embedding model") {
				t.Errorf("report names the embedding model although Ollama was not reached:\n%s", out.String())
			}
		})
	}
}

// TestCheckDatabase runs the check subcommand against the test database, from
// no schema to stored chunks. It never creates an embedder: check needs no
// Ollama.
func TestCheckDatabase(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	noEmbedder := func(func(string) string) (embed.Embedder, error) {
		t.Error("the embedder was created, want none")
		return nil, errors.New("no embedder")
	}
	valid := "1 | rules | What does Fireball do? | Fire damage. | spells.fireball\n" +
		"2 | interaction | Fireball on a grappled foe? | Same save. | rules-glossary.grappled spells.fireball\n" +
		"3 | refusal | Eldritch Knight? | The SRD does not say. | -\n"
	check := func(t *testing.T, content string) (string, error) {
		t.Helper()
		var out bytes.Buffer
		err := run(ctx, []string{"check", writeSmoke(t, content)}, testEnv, &out, noEmbedder)
		return out.String(), err
	}
	fails := func(t *testing.T, content, wantMsg string) {
		t.Helper()
		out, err := check(t, content)
		if err == nil || !strings.Contains(err.Error(), wantMsg) {
			t.Fatalf("err = %v, want it to contain %q", err, wantMsg)
		}
		if out != "" {
			t.Fatalf("printed %q, want nothing", out)
		}
	}

	fails(t, valid, "no srd_chunks table")
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	fails(t, valid, "no SRD chunk in the database")
	chunks := append([]srd.Chunk(nil), fixtures...)
	for i := range chunks {
		chunks[i].Hash = "hash " + chunks[i].ID
	}
	if _, err := srd.Store(ctx, conn, srd.Version, chunks, srd.StoreOptions{}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	tests := []struct {
		name    string
		content string
		wantErr string   // empty for a valid file
		want    []string // output, spaces collapsed
	}{
		{
			name:    "valid, counts below plan",
			content: valid,
			want: []string{"3 question(s), every record well formed, every expected chunk id stored.",
				"rules 1 80 79 below plan", "interaction 1 40 39 below plan", "refusal 1 15 14 below plan",
				"campaign 0 15 15 below plan", "total 3 150 147 below plan"},
		},
		{
			name:    "no question yet",
			content: "# nothing yet\n",
			want:    []string{"0 question(s)", "total 0 150 150 below plan"},
		},
		{
			name: "unknown ids, all listed once",
			content: "1 | rules | Fireball? | Fire. | spells.fireball spells.nope\n" +
				"2 | interaction | Mage Armor and grappled? | No. | spells.mage-armor spells.nope\n",
			wantErr: "2 expected chunk id(s) are not stored chunks (check them against ingest srd -dry-run): spells.mage-armor, spells.nope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantErr != "" {
				fails(t, tt.content, tt.wantErr)
				return
			}
			out, err := check(t, tt.content)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			got := strings.Join(strings.Fields(out), " ")
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("output has no %q:\n%s", w, out)
				}
			}
		})
	}
}

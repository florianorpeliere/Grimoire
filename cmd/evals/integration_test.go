package main

import (
	"bytes"
	"context"
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

	t.Run("Ollama unreachable", func(t *testing.T) {
		down := &embedtest.Fake{ModelName: "fake-model", Dim: 1024, Err: errRefused}
		var out bytes.Buffer
		err := run(ctx, []string{"retrieval", smoke}, testEnv, &out,
			func(func(string) string) (embed.Embedder, error) { return down, nil })
		if err == nil || !strings.Contains(err.Error(), "cannot use the Ollama server at OLLAMA_HOST") {
			t.Fatalf("err = %v, want the Ollama server named", err)
		}
		got := strings.Join(strings.Fields(out.String()), " ")
		for _, want := range []string{"keyword named 1/1 (100%)", "hybrid (skipped)", "vector (skipped)"} {
			if !strings.Contains(got, want) {
				t.Errorf("report has no %q:\n%s", want, out.String())
			}
		}
	})
}

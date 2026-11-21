package srd_test

import (
	"context"
	"database/sql"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/dbtest"
	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// embedMeta is the embedding metadata of a stored row; Model is "" when the
// row has no embedding.
type embedMeta struct {
	Model string
	Dim   int
}

func readEmbedMeta(t *testing.T, conn *sql.DB) map[string]embedMeta {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), `
SELECT id, coalesce(embedding_model, ''), coalesce(embedding_dim, 0),
       embedding IS NULL OR vector_dims(embedding) = embedding_dim
FROM srd_chunks`)
	if err != nil {
		t.Fatalf("read embeddings: %v", err)
	}
	defer rows.Close()
	m := map[string]embedMeta{}
	for rows.Next() {
		var id string
		var e embedMeta
		var consistent bool
		if err := rows.Scan(&id, &e.Model, &e.Dim, &consistent); err != nil {
			t.Fatalf("scan embeddings: %v", err)
		}
		if !consistent {
			t.Errorf("row %q: embedding length differs from embedding_dim", id)
		}
		m[id] = e
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read embeddings: %v", err)
	}
	return m
}

func TestEmbed(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	fireball := spell("fireball", "Fireball", "A bright streak flashes from you to a point you choose.")
	shield := spell("shield", "Shield", "An imperceptible barrier of magical force protects you.")
	light := spell("light", "Light", "You touch one Large or smaller object.")
	newText := shield
	newText.Text = "An invisible barrier of magical force protects you."
	newText.Hash = "hash of " + newText.Text
	newPath := light // same text and hash, other citation path
	newPath.Path = []string{"Spells", "Cantrips", "Light"}
	newFullPath := fireball // same text, path and hash, other full heading path
	newFullPath.FullPath = []string{"Spells", "Evocation", "Fireball"}
	long := spell("wish", "Wish", strings.Repeat("Wish is the mightiest spell. ", 20))
	web := spell("web", "Web", "You conjure a mass of sticky webbing.") // in the batch of long

	v1 := &embedtest.Fake{ModelName: "model-a", Dim: 1024}
	v2 := &embedtest.Fake{ModelName: "model-b", Dim: 1024}
	narrow := &embedtest.Fake{ModelName: "model-c", Dim: 512}
	short := &embedtest.Fake{ModelName: "model-b", Dim: 1024, MaxRunes: 200}
	down := &embedtest.Fake{ModelName: "model-b", Dim: 1024, Err: errors.New("connection refused")}

	a := embedMeta{"model-a", 1024}
	b := embedMeta{"model-b", 1024}

	// Each step stores chunks (when set), then embeds with embedder, on the
	// table left by the previous step.
	steps := []struct {
		name     string
		chunks   []srd.Chunk
		version  string
		embedder *embedtest.Fake
		want     srd.EmbedResult
		wantErr  error
		// wantErrText is part of the error message.
		wantErrText string
		// wantSent lists the ids whose input reached the provider, sorted.
		wantSent []string
		stored   map[string]embedMeta
	}{
		{
			name: "new chunks: every chunk embedded", chunks: []srd.Chunk{fireball, shield, light},
			embedder: v1, want: srd.EmbedResult{Embedded: 3},
			wantSent: []string{"spells.fireball", "spells.light", "spells.shield"},
			stored:   map[string]embedMeta{"spells.fireball": a, "spells.light": a, "spells.shield": a},
		},
		{
			name: "same chunks stored again: nothing re-embedded", chunks: []srd.Chunk{fireball, shield, light},
			embedder: v1, want: srd.EmbedResult{Kept: 3},
			stored: map[string]embedMeta{"spells.fireball": a, "spells.light": a, "spells.shield": a},
		},
		{
			name: "new SRD version, same texts: nothing re-embedded", chunks: []srd.Chunk{fireball, shield, light},
			version: "5.2.2", embedder: v1, want: srd.EmbedResult{Kept: 3},
			stored: map[string]embedMeta{"spells.fireball": a, "spells.light": a, "spells.shield": a},
		},
		{
			name:   "changed text or path re-embedded, changed full path alone kept",
			chunks: []srd.Chunk{newFullPath, newText, newPath}, embedder: v1,
			want:     srd.EmbedResult{Embedded: 2, Kept: 1},
			wantSent: []string{"spells.light", "spells.shield"},
			stored:   map[string]embedMeta{"spells.fireball": a, "spells.light": a, "spells.shield": a},
		},
		{
			name: "provider dimension differs from the column: refused, nothing sent", embedder: narrow,
			wantErr: srd.ErrDimensionMismatch, wantErrText: "model model-c returns 512 dimensions, srd_chunks.embedding is vector(1024)",
			stored: map[string]embedMeta{"spells.fireball": a, "spells.light": a, "spells.shield": a},
		},
		{
			name: "persistent provider error: returned, embeddings unchanged", embedder: down,
			wantErr: nil, wantErrText: "connection refused",
			wantSent: []string{"spells.fireball", "spells.light"}, // the first batch,
			stored:   map[string]embedMeta{"spells.fireball": a, "spells.light": a, "spells.shield": a},
		},
		{
			name: "new model: every chunk re-embedded", embedder: v2, want: srd.EmbedResult{Embedded: 3},
			wantSent: []string{"spells.fireball", "spells.light", "spells.shield"},
			stored:   map[string]embedMeta{"spells.fireball": b, "spells.light": b, "spells.shield": b},
		},
		{
			name:   "too-long chunk: reported with its size, not truncated, the other chunk of its batch embedded",
			chunks: []srd.Chunk{newFullPath, newText, newPath, long, web}, embedder: short,
			want:    srd.EmbedResult{Embedded: 1, Kept: 3},
			wantErr: embed.ErrInputTooLong, wantErrText: "1 chunks not embedded (id, estimated tokens = characters / 4): spells.wish (149)",
			// The batch of web and wish, refused, then each chunk alone.
			wantSent: []string{"spells.web", "spells.web", "spells.wish", "spells.wish"},
			stored:   map[string]embedMeta{"spells.fireball": b, "spells.light": b, "spells.shield": b, "spells.web": b, "spells.wish": {}},
		},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			if s.chunks != nil {
				version := s.version
				if version == "" {
					version = srd.Version
				}
				if _, err := srd.Store(ctx, conn, version, s.chunks, srd.StoreOptions{AllowLargeDelete: true}); err != nil {
					t.Fatalf("Store: %v", err)
				}
			}
			s.embedder.Reset()
			got, err := srd.Embed(ctx, conn, s.embedder, 2)
			if got != s.want {
				t.Errorf("result = %+v, want %+v", got, s.want)
			}
			switch {
			case s.wantErr == nil && s.wantErrText == "":
				if err != nil {
					t.Fatalf("Embed: %v", err)
				}
			case err == nil:
				t.Fatalf("Embed: no error, want %v %q", s.wantErr, s.wantErrText)
			case s.wantErr != nil && !errors.Is(err, s.wantErr):
				t.Fatalf("Embed: err = %v, want %v", err, s.wantErr)
			case !strings.Contains(err.Error(), s.wantErrText):
				t.Fatalf("Embed: err = %v, want it to contain %q", err, s.wantErrText)
			}
			if sent := sentIDs(t, conn, s.embedder.Texts()); !slices.Equal(sent, s.wantSent) {
				t.Errorf("sent %v, want %v", sent, s.wantSent)
			}
			if meta := readEmbedMeta(t, conn); !maps.Equal(meta, s.stored) {
				t.Errorf("stored embeddings %v, want %v", meta, s.stored)
			}
		})
	}
}

// sentIDs maps the texts sent to the provider back to the stored ids, sorted.
func sentIDs(t *testing.T, conn *sql.DB, texts []string) []string {
	t.Helper()
	var ids []string
	for _, text := range texts {
		var id string
		err := conn.QueryRowContext(context.Background(),
			`SELECT id FROM srd_chunks WHERE path || E'\n\n' || text = $1`, text).Scan(&id)
		if err != nil {
			t.Fatalf("no stored chunk has the input %q: %v", text, err)
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// The embedded input starts with the short citation path, so a chunk's own
// heading, absent from its text, is embedded too.
func TestEmbedInputHasHeading(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	c := spell("fireball", "Fireball", "A bright streak flashes from you.")
	if _, err := srd.Store(ctx, conn, srd.Version, []srd.Chunk{c}, srd.StoreOptions{}); err != nil {
		t.Fatalf("Store: %v", err)
	}
	f := &embedtest.Fake{ModelName: "m", Dim: 1024}
	if _, err := srd.Embed(ctx, conn, f, 0); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	want := []string{"Spells > Fireball\n\nA bright streak flashes from you."}
	if got := f.Texts(); !slices.Equal(got, want) {
		t.Errorf("sent %q, want %q", got, want)
	}
}

// Without the embedding migration, Embed names the missing column.
func TestEmbedWithoutColumn(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "CREATE TABLE srd_chunks (id text PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	_, err := srd.Embed(ctx, conn, &embedtest.Fake{ModelName: "m", Dim: 1024}, 0)
	if !errors.Is(err, srd.ErrNoEmbeddingColumn) {
		t.Fatalf("err = %v, want %v", err, srd.ErrNoEmbeddingColumn)
	}
}

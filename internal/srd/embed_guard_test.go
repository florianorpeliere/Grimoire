package srd_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/dbtest"
	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// racingEmbedder stores a new version of a chunk while it embeds, as a
// concurrent "ingest srd" would, so the vectors it returns are stale.
type racingEmbedder struct {
	*embedtest.Fake
	store func()
}

func (r racingEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	r.store()
	return r.Fake.Embed(ctx, texts)
}

// A vector computed from a chunk's old path or text is not stored over the
// new version: the chunk is left without an embedding, for the next run.
func TestEmbedStaleChunk(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	fireball := spell("fireball", "Fireball", "A bright streak flashes from you to a point you choose.")
	newText := fireball
	newText.Text = "A bright streak flashes from your finger."
	newText.Hash = "hash of " + newText.Text
	newPath := fireball
	newPath.Path = []string{"Spells", "Evocation", "Fireball"}

	tests := []struct {
		name   string
		during srd.Chunk // stored while the provider embeds the old version
	}{
		{name: "text changed during the call", during: newText},
		{name: "path changed during the call", during: newPath},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := conn.ExecContext(ctx, "DELETE FROM srd_chunks"); err != nil {
				t.Fatal(err)
			}
			if _, err := srd.Store(ctx, conn, srd.Version, []srd.Chunk{fireball}, srd.StoreOptions{}); err != nil {
				t.Fatalf("Store: %v", err)
			}
			fake := &embedtest.Fake{ModelName: "m", Dim: 1024}
			racing := racingEmbedder{Fake: fake, store: func() {
				if _, err := srd.Store(ctx, conn, srd.Version, []srd.Chunk{tt.during}, srd.StoreOptions{}); err != nil {
					t.Errorf("Store during Embed: %v", err)
				}
			}}
			got, err := srd.Embed(ctx, conn, racing, 0)
			if err != nil {
				t.Fatalf("Embed: %v", err)
			}
			if want := (srd.EmbedResult{}); got != want {
				t.Errorf("result = %+v, want %+v", got, want)
			}
			if meta := readEmbedMeta(t, conn); meta["spells.fireball"] != (embedMeta{}) {
				t.Errorf("stale vector stored: %+v", meta["spells.fireball"])
			}

			// The next run embeds the new version.
			fake.Reset()
			got, err = srd.Embed(ctx, conn, fake, 0)
			if err != nil {
				t.Fatalf("second Embed: %v", err)
			}
			if want := (srd.EmbedResult{Embedded: 1}); got != want {
				t.Errorf("second result = %+v, want %+v", got, want)
			}
			want := []string{tt.during.PathString() + "\n\n" + tt.during.Text}
			if sent := fake.Texts(); !slices.Equal(sent, want) {
				t.Errorf("second run sent %q, want %q", sent, want)
			}
		})
	}
}

// The constraint keeps an embedding, its model and its dimension together.
func TestEmbeddingConstraint(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	vec := "[" + strings.TrimSuffix(strings.Repeat("0.5,", 1024), ",") + "]"
	tests := []struct {
		name      string
		embedding any // nil for NULL
		model     any
		dim       any
		wantErr   bool
	}{
		{name: "no embedding"},
		{name: "embedding, model and dimension", embedding: vec, model: "m", dim: 1024},
		{name: "embedding and model without dimension", embedding: vec, model: "m", wantErr: true},
		{name: "embedding and dimension without model", embedding: vec, dim: 1024, wantErr: true},
		{name: "dimension other than the vector's", embedding: vec, model: "m", dim: 512, wantErr: true},
		{name: "model without embedding", model: "m", wantErr: true},
		{name: "dimension without embedding", dim: 1024, wantErr: true},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := conn.ExecContext(ctx, `
INSERT INTO srd_chunks (id, path, full_path, title, kind, text, content_hash, srd_version,
                        embedding, embedding_model, embedding_dim)
VALUES ($1, 'P', '{P}', 'T', 'rule', 'text', 'h', '5.2.1', $2::vector, $3, $4)`,
				fmt.Sprintf("c%d", i), tt.embedding, tt.model, tt.dim)
			if !tt.wantErr {
				if err != nil {
					t.Errorf("insert: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "srd_chunks_embedding_meta") {
				t.Errorf("err = %v, want a violation of srd_chunks_embedding_meta", err)
			}
		})
	}
}

// EmbeddingModels counts rows per model, "" for the rows without embedding.
func TestEmbeddingModels(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	a, b, c := spell("a", "A", "a"), spell("b", "B", "b"), spell("c", "C", "c")
	if _, err := srd.Store(ctx, conn, srd.Version, []srd.Chunk{a, b, c}, srd.StoreOptions{}); err != nil {
		t.Fatalf("Store: %v", err)
	}
	steps := []struct {
		name  string
		setup string // SQL run before counting
		want  []srd.ModelRows
	}{
		{name: "nothing embedded", want: []srd.ModelRows{{Model: "", Rows: 3}}},
		{
			name: "two models and a missing embedding",
			setup: `UPDATE srd_chunks SET embedding = array_fill(0.5, ARRAY[1024])::vector, embedding_dim = 1024,
			        embedding_model = CASE id WHEN 'spells.a' THEN 'old' ELSE 'new' END WHERE id <> 'spells.c'`,
			want: []srd.ModelRows{{Model: "", Rows: 1}, {Model: "new", Rows: 1}, {Model: "old", Rows: 1}},
		},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			if s.setup != "" {
				if _, err := conn.ExecContext(ctx, s.setup); err != nil {
					t.Fatal(err)
				}
			}
			got, err := srd.EmbeddingModels(ctx, conn)
			if err != nil {
				t.Fatalf("EmbeddingModels: %v", err)
			}
			if !slices.Equal(got, s.want) {
				t.Errorf("got %v, want %v", got, s.want)
			}
		})
	}
}

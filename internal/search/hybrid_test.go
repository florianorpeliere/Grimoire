package search_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/dbtest"
	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
	"github.com/florianorpeliere/grimoire/internal/search"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// legalFixture is shaped like a chunk of the Legal Information chapter.
var legalFixture = srd.Chunk{
	ID: "legal-information", Path: []string{"Legal Information"},
	FullPath: []string{"Legal Information"},
	Title:    "Legal Information", Kind: srd.KindLegal,
	Text: "This work is licensed under the Creative Commons Attribution 4.0 International License.",
}

// Queries made of words no fixture contains, so the keyword search finds
// nothing and only the vector search can return a chunk.
const (
	vectorOnlyQuery = "xyzzy plugh"
	otherModelQuery = "frobozz quendor"
)

func TestHybrid(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	chunks := append(slices.Clone(fixtures), legalFixture)
	for i := range chunks {
		chunks[i].Hash = "hash " + chunks[i].ID
	}
	if _, err := srd.Store(ctx, conn, srd.Version, chunks, srd.StoreOptions{}); err != nil {
		t.Fatalf("Store: %v", err)
	}
	fake := &embedtest.Fake{ModelName: "fake-model", Dim: 1024}
	if _, err := srd.Embed(ctx, conn, fake, 0); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	for _, text := range fake.Texts() {
		if strings.HasPrefix(text, "Instruct:") {
			t.Fatalf("a document was embedded with the query prefix: %q", text)
		}
	}

	// setVector gives chunk id the fake vector of text, from model.
	setVector := func(id, text, model string) {
		t.Helper()
		vecs, err := fake.Embed(ctx, []string{text})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx,
			"UPDATE srd_chunks SET embedding = $1::vector, embedding_model = $2 WHERE id = $3",
			db.VectorLiteral(vecs[0]), model, id); err != nil {
			t.Fatalf("set vector of %s: %v", id, err)
		}
	}
	// Collapsing Roof is the closest chunk to vectorOnlyQuery, with or without prefix.
	setVector("gameplay-toolbox.traps.collapsing-roof", vectorOnlyQuery, fake.ModelName)
	setVector("spells.shield", search.QueryText(vectorOnlyQuery, true), fake.ModelName)
	// The legal chunk is the closest chunk to its own words.
	const legalQuery = "Creative Commons license"
	setVector(legalFixture.ID, legalQuery, fake.ModelName)
	// Escaping is the closest chunk to otherModelQuery, but by another model.
	setVector("playing-the-game.combat.escaping", otherModelQuery, "other-model")

	tests := []struct {
		name        string
		question    string
		opts        search.Options
		wantFirst   string
		wantFoundBy search.Method
		notWant     string // id that must not be in the hits
	}{
		{name: "exact spell name", question: "Fireball", wantFirst: "spells.fireball", wantFoundBy: search.ByKeyword},
		{name: "exact condition name", question: "Grappled", wantFirst: "rules-glossary.grappled", wantFoundBy: search.ByKeyword},
		{name: "closest fake vector", question: vectorOnlyQuery, wantFirst: "gameplay-toolbox.traps.collapsing-roof", wantFoundBy: search.ByVector},
		{name: "closest fake vector, with prefix", question: vectorOnlyQuery, opts: search.Options{Prefix: true},
			wantFirst: "spells.shield", wantFoundBy: search.ByVector},
		{name: "legal chunk never returned", question: legalQuery, notWant: legalFixture.ID},
		{name: "legal chunk never returned, by its title", question: "Legal Information", notWant: legalFixture.ID},
		{name: "rows of another model ignored", question: otherModelQuery, notWant: "playing-the-game.combat.escaping"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.Reset()
			got, err := search.Hybrid(ctx, conn, fake, tt.question, tt.opts)
			if err != nil {
				t.Fatalf("Hybrid: %v", err)
			}
			if texts := fake.Texts(); len(texts) != 1 || texts[0] != search.QueryText(tt.question, tt.opts.Prefix) {
				t.Errorf("embedded %q, want only %q", texts, search.QueryText(tt.question, tt.opts.Prefix))
			}
			if len(got) == 0 {
				t.Fatal("no hit")
			}
			if len(got) > search.DefaultLimit {
				t.Errorf("got %d hits, want at most %d", len(got), search.DefaultLimit)
			}
			if tt.wantFirst != "" {
				if got[0].ID != tt.wantFirst {
					t.Errorf("first hit = %q, want %q (all: %v)", got[0].ID, tt.wantFirst, hitIDs(got))
				}
				if got[0].FoundBy()&tt.wantFoundBy == 0 {
					t.Errorf("first hit found by %v, want %v among its methods", got[0].FoundBy(), tt.wantFoundBy)
				}
			}
			seen := map[string]bool{}
			for _, h := range got {
				if h.ID == tt.notWant {
					t.Errorf("hit %q returned (all: %v)", h.ID, hitIDs(got))
				}
				if h.Kind == string(srd.KindLegal) {
					t.Errorf("legal chunk %q returned", h.ID)
				}
				if seen[h.ID] {
					t.Errorf("hit %q returned twice", h.ID)
				}
				seen[h.ID] = true
			}
		})
	}

	t.Run("hit fields", func(t *testing.T) {
		got, err := search.Hybrid(ctx, conn, fake, "Fireball", search.Options{Limit: 1})
		if err != nil {
			t.Fatalf("Hybrid: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d hits with limit 1, want 1", len(got))
		}
		h := got[0]
		if h.ID != "spells.fireball" || h.Path != "Spells > Fireball" || h.Kind != "spell" || h.Text != fixtures[0].Text ||
			h.KeywordRank != 1 || h.Score <= 0 {
			t.Errorf("hit = %+v", h)
		}
	})

	t.Run("legal chunk excluded from each method", func(t *testing.T) {
		kw, err := search.Keyword(ctx, conn, legalQuery, 50)
		if err != nil {
			t.Fatalf("Keyword: %v", err)
		}
		vecs, _ := fake.Embed(ctx, []string{legalQuery})
		vec, err := search.Vector(ctx, conn, fake.ModelName, vecs[0], 50)
		if err != nil {
			t.Fatalf("Vector: %v", err)
		}
		for _, r := range append(kw, vec...) {
			if r.ID == legalFixture.ID {
				t.Errorf("legal chunk returned by a single method")
			}
		}
		if len(vec) != len(chunks)-2 {
			t.Errorf("vector search returned %d chunks, want %d (all but the legal and other-model ones)", len(vec), len(chunks)-2)
		}
	})

	t.Run("no row with the configured model", func(t *testing.T) {
		missing := &embedtest.Fake{ModelName: "missing-model", Dim: 1024}
		got, err := search.Hybrid(ctx, conn, missing, "Fireball", search.Options{})
		if !errors.Is(err, search.ErrNoEmbeddings) {
			t.Fatalf("err = %v, want ErrNoEmbeddings", err)
		}
		if !strings.Contains(err.Error(), "missing-model") {
			t.Errorf("error %q does not name the model", err)
		}
		if got != nil {
			t.Errorf("got hits %v, want none", hitIDs(got))
		}
	})
}

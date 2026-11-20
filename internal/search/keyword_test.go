package search_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/dbtest"
	"github.com/florianorpeliere/grimoire/internal/search"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// fixtures are shaped like the chunker output: a chunk's own heading is in
// its title and short path, never in its text. Their text is written for the
// test. The distractors use the spell and condition names in their text, more
// than once, so a name match must come from the title to rank first.
var fixtures = []srd.Chunk{
	{
		ID: "spells.fireball", Path: []string{"Spells", "Fireball"},
		FullPath: []string{"Spells", "Spell Descriptions", "Fireball"},
		Title:    "Fireball", Kind: srd.KindSpell,
		Text: "A bright streak flashes to a point you choose and blossoms into an explosion of flame.",
	},
	{
		// Same rank as Fireball for "Fireball", and an id that sorts before it.
		ID: "spells.delayed-blast-fireball", Path: []string{"Spells", "Delayed Blast Fireball"},
		FullPath: []string{"Spells", "Spell Descriptions", "Delayed Blast Fireball"},
		Title:    "Delayed Blast Fireball", Kind: srd.KindSpell,
		Text: "A beam of yellow light flashes from you, then condenses at a chosen point as a glowing bead.",
	},
	{
		ID: "rules-glossary.grappled", Path: []string{"Rules Glossary", "Grappled"},
		FullPath: []string{"Rules Glossary", "Rules Definitions", "Grappled"},
		Title:    "Grappled", Kind: srd.KindCondition,
		Text: "While you have this condition, your Speed is 0 and can't increase.",
	},
	{
		ID: "gameplay-toolbox.traps.collapsing-roof", Path: []string{"Gameplay Toolbox", "Traps", "Collapsing Roof"},
		FullPath: []string{"Gameplay Toolbox", "Traps", "Collapsing Roof"},
		Title:    "Collapsing Roof", Kind: srd.KindRule,
		Text: "Stones fall on every creature beneath the weakened ceiling.",
	},
	{
		ID: "playing-the-game.combat.escaping", Path: []string{"Playing the Game", "Combat", "Escaping"},
		FullPath: []string{"Playing the Game", "Combat", "Escaping"},
		Title:    "Escaping", Kind: srd.KindRule,
		Text: "A grappled creature can try to escape. A creature grappled by a monster that casts Fireball " +
			"is still grappled after the fireball explodes, and every fireball blast leaves it grappled.",
	},
}

func TestKeyword(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	for i := range fixtures {
		fixtures[i].Hash = "hash " + fixtures[i].ID
	}
	if _, err := srd.Store(ctx, conn, srd.Version, fixtures); err != nil {
		t.Fatalf("Store: %v", err)
	}

	tests := []struct {
		name  string
		query string
		// wantFirst is the id of the first result; "" means no result.
		wantFirst string
		// notInText is a word the query matches but the first result's text
		// does not contain, to show the match comes from the title or path.
		notInText string
	}{
		{name: "exact spell name", query: "Fireball", wantFirst: "spells.fireball", notInText: "fireball"},
		{name: "exact condition name", query: "Grappled", wantFirst: "rules-glossary.grappled", notInText: "grappled"},
		{name: "name in lower case", query: "grappled", wantFirst: "rules-glossary.grappled", notInText: "grappled"},
		{name: "exact spell name, punctuated", query: "fireball?", wantFirst: "spells.fireball", notInText: "fireball"},
		{name: "longer spell name", query: "Delayed Blast Fireball", wantFirst: "spells.delayed-blast-fireball", notInText: "fireball"},
		{name: "name in a question", query: "What happens when I am Grappled?", wantFirst: "rules-glossary.grappled", notInText: "grappled"},
		{name: "word only in the short path", query: "toolbox", wantFirst: "gameplay-toolbox.traps.collapsing-roof", notInText: "toolbox"},
		{name: "plural of a body word", query: "explosions", wantFirst: "spells.fireball", notInText: "explosions"},
		{name: "singular of a body word", query: "stone", wantFirst: "gameplay-toolbox.traps.collapsing-roof", notInText: "stone"},
		{name: "no match", query: "teleportation", wantFirst: ""},
		{name: "only stop words", query: "the and of", wantFirst: ""},
		{name: "blank", query: "  ", wantFirst: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := search.Keyword(ctx, conn, tt.query, 10)
			if err != nil {
				t.Fatalf("Keyword: %v", err)
			}
			if tt.wantFirst == "" {
				if len(got) != 0 {
					t.Fatalf("got %d results, first %q, want none", len(got), got[0].ID)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("no result, want %q first", tt.wantFirst)
			}
			if got[0].ID != tt.wantFirst {
				t.Errorf("first result = %q, want %q (all: %v)", got[0].ID, tt.wantFirst, ids(got))
			}
			if hasWord(got[0].Text, tt.notInText) {
				t.Errorf("fixture text of %q contains %q: the test does not show what it claims", got[0].ID, tt.notInText)
			}
			// No fixture title is shared, so only the first result can be
			// placed ahead of its rank by an exact title match.
			for i := 2; i < len(got); i++ {
				if got[i].Rank > got[i-1].Rank {
					t.Errorf("results not ranked: %q (%g) after %q (%g)", got[i].ID, got[i].Rank, got[i-1].ID, got[i-1].Rank)
				}
			}
		})
	}

	t.Run("result fields", func(t *testing.T) {
		got, err := search.Keyword(ctx, conn, "Fireball", 1)
		if err != nil {
			t.Fatalf("Keyword: %v", err)
		}
		want := search.Result{ID: "spells.fireball", Path: "Spells > Fireball", Kind: "spell", Text: fixtures[0].Text}
		if len(got) != 1 {
			t.Fatalf("got %d results with limit 1, want 1", len(got))
		}
		got[0].Rank = 0
		if got[0] != want {
			t.Errorf("result = %+v, want %+v", got[0], want)
		}
	})
}

func TestKeywordBadLimit(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if _, err := search.Keyword(context.Background(), nil, "Fireball", limit); !errors.Is(err, search.ErrBadLimit) {
			t.Errorf("limit %d: err = %v, want %v", limit, err, search.ErrBadLimit)
		}
	}
}

// hasWord reports whether text has word as a whole word, ignoring case.
func hasWord(text, word string) bool {
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if strings.EqualFold(w, word) {
			return true
		}
	}
	return false
}

func ids(rs []search.Result) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

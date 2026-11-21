package search_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
	"github.com/florianorpeliere/grimoire/internal/search"
)

func results(ids ...string) []search.Result {
	out := make([]search.Result, len(ids))
	for i, id := range ids {
		out[i] = search.Result{ID: id, Path: "path " + id, Kind: "rule", Text: "text " + id}
	}
	return out
}

func TestFuse(t *testing.T) {
	type want struct {
		id           string
		kwRank       int
		vecRank      int
		foundBy      search.Method
		scoreDivisor []int // score = sum of 1/d
	}
	tests := []struct {
		name    string
		keyword []search.Result
		vector  []search.Result
		k       int
		limit   int
		want    []want
	}{
		{
			name:    "found only by keyword",
			keyword: results("a"),
			vector:  results("b"),
			k:       60, limit: 8,
			want: []want{
				{id: "a", kwRank: 1, foundBy: search.ByKeyword, scoreDivisor: []int{61}},
				{id: "b", vecRank: 1, foundBy: search.ByVector, scoreDivisor: []int{61}},
			},
		},
		{
			name:    "found only by vector, next to a chunk found by both",
			keyword: results("a", "b"),
			vector:  results("c", "a"),
			k:       60, limit: 8,
			want: []want{
				{id: "a", kwRank: 1, vecRank: 2, foundBy: search.ByBoth, scoreDivisor: []int{61, 62}},
				{id: "c", vecRank: 1, foundBy: search.ByVector, scoreDivisor: []int{61}},
				{id: "b", kwRank: 2, foundBy: search.ByKeyword, scoreDivisor: []int{62}},
			},
		},
		{
			name:    "found by both appears once, above single finds",
			keyword: results("x", "both"),
			vector:  results("y", "both"),
			k:       60, limit: 8,
			want: []want{
				{id: "both", kwRank: 2, vecRank: 2, foundBy: search.ByBoth, scoreDivisor: []int{62, 62}},
				{id: "x", kwRank: 1, foundBy: search.ByKeyword, scoreDivisor: []int{61}},
				{id: "y", vecRank: 1, foundBy: search.ByVector, scoreDivisor: []int{61}},
			},
		},
		{
			name:    "ties: keyword rank first, then vector rank, then id",
			keyword: results("k1", "k2"),
			vector:  results("v1", "v2"),
			k:       60, limit: 8,
			want: []want{
				{id: "k1", kwRank: 1, foundBy: search.ByKeyword, scoreDivisor: []int{61}},
				{id: "v1", vecRank: 1, foundBy: search.ByVector, scoreDivisor: []int{61}},
				{id: "k2", kwRank: 2, foundBy: search.ByKeyword, scoreDivisor: []int{62}},
				{id: "v2", vecRank: 2, foundBy: search.ByVector, scoreDivisor: []int{62}},
			},
		},
		{
			name:    "keyword returns nothing",
			keyword: nil,
			vector:  results("a", "b"),
			k:       60, limit: 8,
			want: []want{
				{id: "a", vecRank: 1, foundBy: search.ByVector, scoreDivisor: []int{61}},
				{id: "b", vecRank: 2, foundBy: search.ByVector, scoreDivisor: []int{62}},
			},
		},
		{
			name:    "vector returns nothing",
			keyword: results("a", "b"),
			vector:  nil,
			k:       60, limit: 8,
			want: []want{
				{id: "a", kwRank: 1, foundBy: search.ByKeyword, scoreDivisor: []int{61}},
				{id: "b", kwRank: 2, foundBy: search.ByKeyword, scoreDivisor: []int{62}},
			},
		},
		{
			name: "both return nothing",
			k:    60, limit: 8,
			want: []want{},
		},
		{
			name:    "limit cuts the fused list",
			keyword: results("a", "b", "c"),
			vector:  results("c", "d"),
			k:       60, limit: 2,
			want: []want{
				{id: "c", kwRank: 3, vecRank: 1, foundBy: search.ByBoth, scoreDivisor: []int{63, 61}},
				{id: "a", kwRank: 1, foundBy: search.ByKeyword, scoreDivisor: []int{61}},
			},
		},
		{
			name:    "k = 0 is accepted",
			keyword: results("a"),
			vector:  results("b", "a"),
			k:       0, limit: 1,
			want: []want{
				{id: "a", kwRank: 1, vecRank: 2, foundBy: search.ByBoth, scoreDivisor: []int{1, 2}},
			},
		},
		{
			name:    "k changes the order",
			keyword: results("a", "b", "c"),
			vector:  results("c"),
			k:       1, limit: 8,
			// k = 1: a scores 1/2, c scores 1/4 + 1/2.
			want: []want{
				{id: "c", kwRank: 3, vecRank: 1, foundBy: search.ByBoth, scoreDivisor: []int{4, 2}},
				{id: "a", kwRank: 1, foundBy: search.ByKeyword, scoreDivisor: []int{2}},
				{id: "b", kwRank: 2, foundBy: search.ByKeyword, scoreDivisor: []int{3}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := search.Fuse(tt.keyword, tt.vector, tt.k, tt.limit)
			if err != nil {
				t.Fatalf("Fuse: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d hits %v, want %d", len(got), hitIDs(got), len(tt.want))
			}
			for i, w := range tt.want {
				h := got[i]
				var score float64
				for _, d := range w.scoreDivisor {
					score += 1 / float64(d)
				}
				if h.ID != w.id || h.KeywordRank != w.kwRank || h.VectorRank != w.vecRank || h.FoundBy() != w.foundBy || h.Score != score {
					t.Errorf("hit %d = {%s kw %d vec %d %v %g}, want {%s kw %d vec %d %v %g} (all: %v)",
						i, h.ID, h.KeywordRank, h.VectorRank, h.FoundBy(), h.Score,
						w.id, w.kwRank, w.vecRank, w.foundBy, score, hitIDs(got))
				}
				if h.Path != "path "+w.id || h.Text != "text "+w.id || h.Kind != "rule" {
					t.Errorf("hit %d fields = %q %q %q, not those of %s", i, h.Path, h.Kind, h.Text, w.id)
				}
			}
		})
	}
}

func TestFuseRefuses(t *testing.T) {
	tests := []struct {
		name     string
		k, limit int
		wantErr  error
	}{
		{name: "negative k", k: -1, limit: 8, wantErr: search.ErrBadOptions},
		{name: "k below minus one", k: -61, limit: 8, wantErr: search.ErrBadOptions},
		{name: "zero limit", k: 60, limit: 0, wantErr: search.ErrBadLimit},
		{name: "negative limit", k: 60, limit: -1, wantErr: search.ErrBadLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := search.Fuse(results("a"), results("b"), tt.k, tt.limit)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if got != nil {
				t.Errorf("got hits %v, want none", hitIDs(got))
			}
		})
	}
}

func TestMethodString(t *testing.T) {
	tests := []struct {
		m    search.Method
		want string
	}{
		{search.ByKeyword, "keyword"},
		{search.ByVector, "vector"},
		{search.ByBoth, "both"},
		{search.Method(0), "Method(0)"},
	}
	for _, tt := range tests {
		if got := tt.m.String(); got != tt.want {
			t.Errorf("%d.String() = %q, want %q", int(tt.m), got, tt.want)
		}
	}
}

func TestQueryText(t *testing.T) {
	tests := []struct {
		name     string
		question string
		prefix   bool
		want     string
	}{
		{name: "off", question: "What is Fireball?", want: "What is Fireball?"},
		{name: "on", question: "What is Fireball?", prefix: true,
			want: "Instruct: " + search.QueryInstruction + "\nQuery:What is Fireball?"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := search.QueryText(tt.question, tt.prefix); got != tt.want {
				t.Errorf("QueryText = %q, want %q", got, tt.want)
			}
		})
	}
}

// These cases end before the database is used: conn is nil.
func TestHybridWithoutDatabase(t *testing.T) {
	embedErr := errors.New("embedding server down")
	tests := []struct {
		name      string
		question  string
		opts      search.Options
		embedErr  error
		wantErr   error
		wantTexts []string // texts sent to the embedder
	}{
		{name: "blank question", question: " \n ", wantTexts: nil},
		{name: "question too long", question: strings.Repeat("a", search.MaxQueryBytes+1), wantErr: search.ErrQueryTooLong},
		{name: "negative option", question: "Fireball", opts: search.Options{Limit: -1}, wantErr: search.ErrBadOptions},
		{name: "embedder fails, prefix off", question: "Fireball", embedErr: embedErr, wantErr: embedErr,
			wantTexts: []string{"Fireball"}},
		{name: "embedder fails, prefix on", question: "Fireball", opts: search.Options{Prefix: true}, embedErr: embedErr, wantErr: embedErr,
			wantTexts: []string{search.QueryText("Fireball", true)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &embedtest.Fake{ModelName: "fake", Dim: 4, Err: tt.embedErr}
			got, err := search.Hybrid(context.Background(), nil, e, tt.question, tt.opts)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if len(got) != 0 {
				t.Errorf("got hits %v, want none", hitIDs(got))
			}
			if texts := e.Texts(); strings.Join(texts, "|") != strings.Join(tt.wantTexts, "|") || len(texts) != len(tt.wantTexts) {
				t.Errorf("embedded %q, want %q", texts, tt.wantTexts)
			}
		})
	}
}

func hitIDs(hs []search.Hit) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.ID
	}
	return out
}

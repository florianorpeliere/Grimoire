package search

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/florianorpeliere/grimoire/internal/embed"
)

// Defaults of Options. They are first choices, to be tuned on the eval set
// (step 2), not measured values.
const (
	DefaultK          = 60 // reciprocal rank fusion constant
	DefaultCandidates = 20 // results asked from each method
	DefaultLimit      = 8  // results returned
)

// ErrBadOptions is returned for a negative option.
var ErrBadOptions = errors.New("search options must not be negative")

// Options configures Hybrid. A zero field takes its default.
type Options struct {
	K          int  // reciprocal rank fusion constant; default DefaultK
	Candidates int  // results asked from each method; default DefaultCandidates
	Limit      int  // results returned; default DefaultLimit
	Prefix     bool // embed the question with the Qwen3 instruction (see QueryText)
}

func (o Options) withDefaults() (Options, error) {
	if o.K < 0 || o.Candidates < 0 || o.Limit < 0 {
		return o, ErrBadOptions
	}
	if o.K == 0 {
		o.K = DefaultK
	}
	if o.Candidates == 0 {
		o.Candidates = DefaultCandidates
	}
	if o.Limit == 0 {
		o.Limit = DefaultLimit
	}
	return o, nil
}

// Method says which search found a chunk.
type Method int

const (
	ByKeyword Method = 1 << iota
	ByVector
	ByBoth = ByKeyword | ByVector
)

func (m Method) String() string {
	switch m {
	case ByKeyword:
		return "keyword"
	case ByVector:
		return "vector"
	case ByBoth:
		return "both"
	}
	return fmt.Sprintf("Method(%d)", int(m))
}

// Hit is one chunk of a hybrid search.
type Hit struct {
	ID   string
	Path string // short citation path, e.g. "Spells > Fireball"
	Kind string
	Text string
	// Score is the reciprocal rank fusion score: the sum over the methods
	// that found the chunk of 1 / (k + rank).
	Score float64
	// KeywordRank and VectorRank are the 1-based ranks in each method's
	// list, 0 when that method did not find the chunk.
	KeywordRank int
	VectorRank  int
}

// FoundBy says which method(s) found the chunk.
func (h Hit) FoundBy() Method {
	var m Method
	if h.KeywordRank > 0 {
		m |= ByKeyword
	}
	if h.VectorRank > 0 {
		m |= ByVector
	}
	return m
}

// Fuse merges a keyword and a vector ranking, each best first, by reciprocal
// rank fusion with constant k, and returns at most limit hits. A chunk found
// by both methods appears once, with both ranks.
//
// Order: score, highest first. Equal scores are ordered by keyword rank (found
// by keyword before not found, better rank first), then vector rank, then id.
// Keyword goes first because it ranks an exact title first (see Keyword):
// with "Fireball", the keyword first and a different vector first tie at
// 1/(k+1), and the named spell should win.
func Fuse(keyword, vector []Result, k, limit int) []Hit {
	byID := make(map[string]*Hit, len(keyword)+len(vector))
	var hits []*Hit
	add := func(r Result, rank int, set func(*Hit)) {
		h, ok := byID[r.ID]
		if !ok {
			h = &Hit{ID: r.ID, Path: r.Path, Kind: r.Kind, Text: r.Text}
			byID[r.ID] = h
			hits = append(hits, h)
		}
		set(h)
		h.Score += 1 / float64(k+rank)
	}
	for i, r := range keyword {
		add(r, i+1, func(h *Hit) { h.KeywordRank = i + 1 })
	}
	for i, r := range vector {
		add(r, i+1, func(h *Hit) { h.VectorRank = i + 1 })
	}
	slices.SortFunc(hits, func(a, b *Hit) int {
		return cmp.Or(
			cmp.Compare(b.Score, a.Score),
			compareRank(a.KeywordRank, b.KeywordRank),
			compareRank(a.VectorRank, b.VectorRank),
			strings.Compare(a.ID, b.ID),
		)
	})
	out := make([]Hit, 0, min(limit, len(hits)))
	for _, h := range hits[:min(limit, len(hits))] {
		out = append(out, *h)
	}
	return out
}

// compareRank orders 1-based ranks, best first, with 0 (not found) last.
func compareRank(a, b int) int {
	switch {
	case a == b:
		return 0
	case a == 0:
		return 1
	case b == 0:
		return -1
	}
	return cmp.Compare(a, b)
}

// Hybrid searches the SRD chunks for question by keyword (Keyword) and by
// meaning (Vector), and merges both rankings with Fuse. Legal Information
// chunks are never returned. The question is embedded by e, whose Model
// selects the stored embeddings compared against; documents were embedded
// without prefix, the question gets one if opts.Prefix is set.
//
// A blank question returns no hit, without calling e. A question longer
// than MaxQueryBytes is refused with ErrQueryTooLong. It fails with
// ErrNoEmbeddings when no chunk is embedded by e's model.
func Hybrid(ctx context.Context, conn *sql.DB, e embed.Embedder, question string, opts Options) ([]Hit, error) {
	opts, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}
	if len(question) > MaxQueryBytes {
		return nil, ErrQueryTooLong
	}
	if strings.TrimSpace(question) == "" {
		return nil, nil
	}
	vecs, err := e.Embed(ctx, []string{QueryText(question, opts.Prefix)})
	if err != nil {
		return nil, fmt.Errorf("embed the question: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embed the question: got %d vectors, want 1", len(vecs))
	}
	vector, err := Vector(ctx, conn, e.Model(), vecs[0], opts.Candidates)
	if err != nil {
		return nil, err
	}
	keyword, err := Keyword(ctx, conn, question, opts.Candidates)
	if err != nil {
		return nil, err
	}
	return Fuse(keyword, vector, opts.K, opts.Limit), nil
}

// Package search finds SRD chunks for a question.
package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Result is one chunk found by a search.
type Result struct {
	ID   string
	Path string // short citation path, e.g. "Spells > Fireball"
	Kind string
	Text string
	Rank float64 // full-text relevance; only comparable within one search
}

// ErrBadLimit is returned for a limit below 1.
var ErrBadLimit = errors.New("limit must be at least 1")

// Rank weights of the keyword index labels (see the srd_chunks migration),
// in the order ts_rank expects them: {D, C, B, A}. D is unused.
//
//   - A, the title: 1.0, so an exact spell or condition name comes first.
//   - B, the short citation path: 0.2, the same as the text. Chapter names
//     such as "Spells" are in the path of hundreds of chunks, so a higher
//     weight would let them crowd out text matches. This is a default to
//     revisit after the evals, not a tuned value.
//   - C, the text: 0.2, the PostgreSQL default for C.
const rankWeights = "{0.1, 0.2, 0.2, 1.0}"

// keywordQuery ranks the srd_chunks rows matching any word of $1.
//
// Every word of the question is looked up both as written ("simple", which
// indexes the title and path) and stemmed ("english", which indexes the
// text), so "Grappled" matches the title "Grappled" and "grappled" in a text,
// and "explosions" matches "explosion". Words are joined with OR: a question
// in plain English rarely has all its words in one chunk, and the rank orders
// chunks by how many words match and where. English stop words ("the", "can")
// are dropped, including from the title and path lookup.
//
// A chunk whose title is the whole query (same words, any case or
// punctuation) comes first: "Fireball" and "Delayed Blast Fireball" match
// the title equally and get the same rank, and neither text says "Fireball".
const keywordQuery = `
WITH words AS (
	SELECT plainto_tsquery('simple', w) || plainto_tsquery('english', w) AS q
	FROM unnest(tsvector_to_array(to_tsvector('simple', $1))) AS w
	WHERE numnode(plainto_tsquery('english', w)) > 0
), query AS (
	SELECT string_agg('(' || q::text || ')', ' | ')::tsquery AS q FROM words
)
SELECT c.id, c.path, c.kind, c.text, ts_rank('` + rankWeights + `', c.search, query.q) AS rank
FROM srd_chunks c, query
WHERE c.search @@ query.q
ORDER BY to_tsvector('simple', c.title) = to_tsvector('simple', $1) DESC, rank DESC, c.id
LIMIT $2`

// Keyword returns at most limit SRD chunks matching the words of query, best
// first: a chunk whose title is exactly the query, then by full-text rank,
// then by id. A query with no searchable word (empty, or only stop words)
// returns no result.
func Keyword(ctx context.Context, conn *sql.DB, query string, limit int) ([]Result, error) {
	if limit < 1 {
		return nil, ErrBadLimit
	}
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	rows, err := conn.QueryContext(ctx, keywordQuery, query, limit)
	if err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	defer rows.Close()
	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ID, &r.Path, &r.Kind, &r.Text, &r.Rank); err != nil {
			return nil, fmt.Errorf("keyword search: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("keyword search: %w", err)
	}
	return results, nil
}

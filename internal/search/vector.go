package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/florianorpeliere/grimoire/internal/db"
)

// ErrNoEmbeddings is returned, wrapped with the model name, when no stored
// chunk has an embedding from the configured model.
var ErrNoEmbeddings = errors.New("no SRD chunk has an embedding from the configured model")

// QueryInstruction is the task description put before a question when the
// query prefix is on (see QueryText). Qwen3 embedding models are trained with
// an instruction on the query side only; documents are embedded as they are.
const QueryInstruction = "Given a question about the rules of a tabletop roleplaying game, retrieve the rules passages that answer it"

// QueryText returns the text embedded for question. With prefix off it is
// the question itself. With prefix on it is the Qwen3 query format:
//
//	Instruct: <QueryInstruction>
//	Query:<question>
//
// Documents never get a prefix (see srd.Embed).
func QueryText(question string, prefix bool) string {
	if !prefix {
		return question
	}
	return "Instruct: " + QueryInstruction + "\nQuery:" + question
}

// vectorQuery ranks the chunks embedded by model $2 by cosine distance to
// $1, closest first, then by id. Rows from another model are ignored: their
// vectors are not comparable (a model switch interrupted halfway leaves both).
// Exact scan, no ANN index (see the srd_embeddings migration).
const vectorQuery = `
SELECT id, path, kind, text, 1 - (embedding <=> $1::vector) AS similarity
FROM srd_chunks
WHERE embedding_model = $2 AND kind <> '` + excludedKind + `'
ORDER BY embedding <=> $1::vector, id
LIMIT $3`

// Vector returns at most limit SRD chunks embedded by model, except Legal
// Information chunks, closest to vec first. Result.Rank is the cosine
// similarity. It fails with ErrNoEmbeddings when no stored chunk has an
// embedding from model, rather than return nothing.
func Vector(ctx context.Context, conn *sql.DB, model string, vec []float32, limit int) ([]Result, error) {
	if limit < 1 {
		return nil, ErrBadLimit
	}
	rows, err := conn.QueryContext(ctx, vectorQuery, db.VectorLiteral(vec), model, limit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()
	var results []Result
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.ID, &r.Path, &r.Kind, &r.Text, &r.Rank); err != nil {
			return nil, fmt.Errorf("vector search: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	if len(results) == 0 {
		var embedded bool
		if err := conn.QueryRowContext(ctx,
			"SELECT EXISTS (SELECT 1 FROM srd_chunks WHERE embedding_model = $1)", model).Scan(&embedded); err != nil {
			return nil, fmt.Errorf("vector search: %w", err)
		}
		if !embedded {
			return nil, fmt.Errorf("%w: model %s (embed the SRD with it first: ingest srd)", ErrNoEmbeddings, model)
		}
	}
	return results, nil
}

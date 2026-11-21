package srd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/florianorpeliere/grimoire/internal/embed"
)

// EmbedBatchSize is the number of chunks sent to the provider per call.
const EmbedBatchSize = 32

var (
	// ErrNoEmbeddingColumn is returned by Embed when srd_chunks has no
	// embedding column: a migration is missing.
	ErrNoEmbeddingColumn = errors.New("srd_chunks has no embedding column")
	// ErrDimensionMismatch is returned by Embed, which then changes nothing,
	// when the provider's dimension differs from the embedding column's.
	ErrDimensionMismatch = errors.New("embedding dimension mismatch")
)

// EmbedResult counts the stored chunks by what Embed did with them.
type EmbedResult struct {
	Embedded int // embedded by this run
	Kept     int // already embedded by the same model, from the same input
}

// embedInput is the text embedded for a chunk: its short citation path, which
// ends with its own heading, then its text, which does not contain it.
func embedInput(path, text string) string { return path + "\n\n" + text }

// Embed computes the embedding of every stored chunk that has none, or one
// from another model or dimension, and stores it with the provider's model
// name and dimension. A chunk keeps its embedding while its path, text hash
// and model are unchanged: Store clears it when the path or text changes.
//
// It fails with ErrDimensionMismatch, before calling the provider, when the
// provider's dimension is not the column's. Batches are committed as they
// are embedded, so a failed run keeps its progress. Chunks too long for the
// model are not truncated: they are reported together in the error, with
// their estimated size, after the other chunks are embedded.
//
// Because batches commit one at a time, a run interrupted during a change of
// model leaves rows of both models side by side, until a run succeeds. Their
// vectors are not comparable: a search must only use the rows whose
// embedding_model is the configured model (see EmbeddingModels).
func Embed(ctx context.Context, conn *sql.DB, e embed.Embedder, batchSize int) (EmbedResult, error) {
	if batchSize < 1 {
		batchSize = EmbedBatchSize
	}
	if err := checkDimension(ctx, conn, e); err != nil {
		return EmbedResult{}, err
	}

	todo, err := chunksToEmbed(ctx, conn, e)
	if err != nil {
		return EmbedResult{}, err
	}
	var total int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM srd_chunks").Scan(&total); err != nil {
		return EmbedResult{}, fmt.Errorf("count stored chunks: %w", err)
	}
	res := EmbedResult{Kept: total - len(todo)}

	var tooLong []string
	for start := 0; start < len(todo); start += batchSize {
		batch := todo[start:min(start+batchSize, len(todo))]
		n, long, err := embedBatch(ctx, conn, e, batch)
		res.Embedded += n
		tooLong = append(tooLong, long...)
		if err != nil {
			if len(tooLong) > 0 {
				err = fmt.Errorf("%w (also too long for the model, not embedded: %s)", err, strings.Join(tooLong, ", "))
			}
			return res, err
		}
	}
	if len(tooLong) > 0 {
		return res, fmt.Errorf("%w: %d chunks not embedded (id, estimated tokens = characters / 4): %s",
			embed.ErrInputTooLong, len(tooLong), strings.Join(tooLong, ", "))
	}
	return res, nil
}

// ModelRows is the number of stored chunks embedded by one model. Model is ""
// for the chunks without an embedding.
type ModelRows struct {
	Model string
	Rows  int
}

// EmbeddingModels counts the stored chunks per embedding model, by model name.
func EmbeddingModels(ctx context.Context, conn *sql.DB) ([]ModelRows, error) {
	rows, err := conn.QueryContext(ctx, `
SELECT coalesce(embedding_model, ''), count(*) FROM srd_chunks GROUP BY 1 ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("count embeddings per model: %w", err)
	}
	defer rows.Close()
	var out []ModelRows
	for rows.Next() {
		var m ModelRows
		if err := rows.Scan(&m.Model, &m.Rows); err != nil {
			return nil, fmt.Errorf("count embeddings per model: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("count embeddings per model: %w", err)
	}
	return out, nil
}

// pending is a stored chunk to embed.
type pending struct{ id, path, text, hash string }

func (p pending) input() string { return embedInput(p.path, p.text) }

// checkDimension compares the provider's dimension with the column type.
// The type modifier of a vector column is its dimension.
func checkDimension(ctx context.Context, conn *sql.DB, e embed.Embedder) error {
	var dim int
	err := conn.QueryRowContext(ctx, `
SELECT atttypmod FROM pg_attribute
WHERE attrelid = 'srd_chunks'::regclass AND attname = 'embedding' AND NOT attisdropped`).Scan(&dim)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoEmbeddingColumn
	}
	if err != nil {
		return fmt.Errorf("read the embedding column: %w", err)
	}
	if dim != e.Dimension() {
		return fmt.Errorf("%w: model %s returns %d dimensions, srd_chunks.embedding is vector(%d); nothing was embedded",
			ErrDimensionMismatch, e.Model(), e.Dimension(), dim)
	}
	return nil
}

func chunksToEmbed(ctx context.Context, conn *sql.DB, e embed.Embedder) ([]pending, error) {
	rows, err := conn.QueryContext(ctx, `
SELECT id, path, text, content_hash FROM srd_chunks
WHERE embedding IS NULL OR embedding_model <> $1 OR embedding_dim <> $2
ORDER BY id`, e.Model(), e.Dimension())
	if err != nil {
		return nil, fmt.Errorf("list chunks to embed: %w", err)
	}
	defer rows.Close()
	var todo []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.path, &p.text, &p.hash); err != nil {
			return nil, fmt.Errorf("list chunks to embed: %w", err)
		}
		todo = append(todo, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list chunks to embed: %w", err)
	}
	return todo, nil
}

// embedBatch embeds and stores batch. When the provider refuses the batch
// because an input is too long, it embeds the chunks one by one and returns
// the too-long ones, formatted for the report, instead of failing.
func embedBatch(ctx context.Context, conn *sql.DB, e embed.Embedder, batch []pending) (int, []string, error) {
	inputs := make([]string, len(batch))
	for i, p := range batch {
		inputs[i] = p.input()
	}
	vecs, err := e.Embed(ctx, inputs)
	if err == nil {
		n, err := save(ctx, conn, e, batch, vecs)
		return n, nil, err
	}
	if !errors.Is(err, embed.ErrInputTooLong) {
		return 0, nil, fmt.Errorf("embed chunks %s to %s: %w", batch[0].id, batch[len(batch)-1].id, err)
	}
	var done int
	var tooLong []string
	for _, p := range batch {
		vecs, err := e.Embed(ctx, []string{p.input()})
		if errors.Is(err, embed.ErrInputTooLong) {
			tokens := (utf8.RuneCountInString(p.input()) + 3) / 4
			tooLong = append(tooLong, fmt.Sprintf("%s (%d)", p.id, tokens))
			continue
		}
		if err != nil {
			return done, tooLong, fmt.Errorf("embed chunk %s: %w", p.id, err)
		}
		n, err := save(ctx, conn, e, []pending{p}, vecs)
		done += n
		if err != nil {
			return done, tooLong, err
		}
	}
	return done, tooLong, nil
}

// save stores the vectors of batch in one transaction. A row whose path or
// text changed since it was read is left alone, to be embedded next run.
func save(ctx context.Context, conn *sql.DB, e embed.Embedder, batch []pending, vecs [][]float32) (int, error) {
	if len(vecs) != len(batch) {
		return 0, fmt.Errorf("embedding provider returned %d vectors for %d chunks", len(vecs), len(batch))
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store embeddings: %w", err)
	}
	defer tx.Rollback() // no-op after Commit
	stmt, err := tx.PrepareContext(ctx, `
UPDATE srd_chunks SET embedding = $1::vector, embedding_model = $2, embedding_dim = $3
WHERE id = $4 AND path = $5 AND content_hash = $6`)
	if err != nil {
		return 0, fmt.Errorf("store embeddings: %w", err)
	}
	defer stmt.Close()
	var n int
	for i, p := range batch {
		if len(vecs[i]) != e.Dimension() {
			return 0, fmt.Errorf("%w: chunk %s got %d dimensions from %s, want %d",
				ErrDimensionMismatch, p.id, len(vecs[i]), e.Model(), e.Dimension())
		}
		r, err := stmt.ExecContext(ctx, vectorLiteral(vecs[i]), e.Model(), e.Dimension(), p.id, p.path, p.hash)
		if err != nil {
			return 0, fmt.Errorf("store embedding of %s: %w", p.id, err)
		}
		if k, err := r.RowsAffected(); err == nil {
			n += int(k)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store embeddings: %w", err)
	}
	return n, nil
}

// vectorLiteral formats v as pgvector's text input, e.g. [0.1,-2,3e-05].
func vectorLiteral(v []float32) string {
	b := make([]byte, 0, len(v)*12)
	b = append(b, '[')
	for i, f := range v {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendFloat(b, float64(f), 'g', -1, 32)
	}
	return string(append(b, ']'))
}

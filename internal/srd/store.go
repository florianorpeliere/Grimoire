package srd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNoChunks is returned by Store for an empty input, which would
	// otherwise delete every stored chunk.
	ErrNoChunks = errors.New("no chunks to store")
	// ErrNoVersion is returned by Store when the SRD version is blank.
	ErrNoVersion = errors.New("SRD version is blank")
	// ErrLargeDelete is returned by Store, which then changes nothing, when
	// the input would delete more than MaxDeleteShare of the stored rows and
	// StoreOptions.AllowLargeDelete is not set.
	ErrLargeDelete = errors.New("too many stored chunks would be deleted")
)

// MaxDeleteShare is the share of the stored rows Store deletes without
// StoreOptions.AllowLargeDelete. Above it the input is more likely a
// truncated or wrong file than a new SRD release, and the deleted rows would
// have to be embedded again.
const MaxDeleteShare = 0.05

// StoreOptions changes what Store accepts.
type StoreOptions struct {
	// AllowLargeDelete lets Store delete more than MaxDeleteShare of the
	// stored rows, e.g. for a new SRD release that drops many sections.
	AllowLargeDelete bool
}

// StoreResult counts what Store did, per chunk of the input and per row deleted.
type StoreResult struct {
	Inserted  int // new ids
	Updated   int // existing ids whose stored row differed from the input
	Unchanged int // existing ids whose stored row already matched the input
	Deleted   int // stored ids absent from the input
}

// upsertChunk inserts a chunk, or updates the row with the same id when any
// column differs. It returns no row when the stored row already matches.
// xmax is 0 only on a row version created by an insert, which tells a new
// row from an updated one.
const upsertChunk = `
INSERT INTO srd_chunks (id, path, full_path, title, kind, text, content_hash, srd_version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET
	path = EXCLUDED.path,
	full_path = EXCLUDED.full_path,
	title = EXCLUDED.title,
	kind = EXCLUDED.kind,
	text = EXCLUDED.text,
	content_hash = EXCLUDED.content_hash,
	srd_version = EXCLUDED.srd_version
WHERE (srd_chunks.path, srd_chunks.full_path, srd_chunks.title, srd_chunks.kind,
       srd_chunks.text, srd_chunks.content_hash, srd_chunks.srd_version)
	IS DISTINCT FROM
      (EXCLUDED.path, EXCLUDED.full_path, EXCLUDED.title, EXCLUDED.kind,
       EXCLUDED.text, EXCLUDED.content_hash, EXCLUDED.srd_version)
RETURNING xmax = 0`

// deleteMissing deletes the rows whose id is not in $1.
const deleteMissing = `DELETE FROM srd_chunks WHERE id <> ALL($1)`

// Store makes the srd_chunks table hold exactly chunks, tagged with the SRD
// version: it upserts every chunk by id and deletes the rows whose id is not
// in chunks, in one transaction. Running it twice on the same input changes
// nothing the second time. The short citation path is stored as PathString.
//
// It refuses an empty input or duplicate ids before touching the database,
// and rolls back, with ErrLargeDelete, a run that would delete more than
// MaxDeleteShare of the stored rows unless opts.AllowLargeDelete is set.
func Store(ctx context.Context, conn *sql.DB, version string, chunks []Chunk, opts StoreOptions) (StoreResult, error) {
	var res StoreResult
	if strings.TrimSpace(version) == "" {
		return StoreResult{}, ErrNoVersion
	}
	if len(chunks) == 0 {
		return StoreResult{}, ErrNoChunks
	}
	ids := make([]string, len(chunks))
	seen := make(map[string]bool, len(chunks))
	for i, c := range chunks {
		if seen[c.ID] {
			return StoreResult{}, fmt.Errorf("%w: %q", ErrDuplicateID, c.ID)
		}
		seen[c.ID] = true
		ids[i] = c.ID
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return StoreResult{}, fmt.Errorf("store chunks: %w", err)
	}
	defer tx.Rollback() // no-op after Commit

	var stored int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM srd_chunks").Scan(&stored); err != nil {
		return StoreResult{}, fmt.Errorf("store chunks: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, upsertChunk)
	if err != nil {
		return StoreResult{}, fmt.Errorf("store chunks: %w", err)
	}
	defer stmt.Close()
	for _, c := range chunks {
		var inserted bool
		err := stmt.QueryRowContext(ctx, c.ID, c.PathString(), c.FullPath, c.Title,
			string(c.Kind), c.Text, c.Hash, version).Scan(&inserted)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			res.Unchanged++
		case err != nil:
			return StoreResult{}, fmt.Errorf("store chunk %q: %w", c.ID, err)
		case inserted:
			res.Inserted++
		default:
			res.Updated++
		}
	}

	r, err := tx.ExecContext(ctx, deleteMissing, ids)
	if err != nil {
		return StoreResult{}, fmt.Errorf("delete removed chunks: %w", err)
	}
	deleted, err := r.RowsAffected()
	if err != nil {
		return StoreResult{}, fmt.Errorf("delete removed chunks: %w", err)
	}
	res.Deleted = int(deleted)
	if !opts.AllowLargeDelete && float64(res.Deleted) > MaxDeleteShare*float64(stored) {
		return StoreResult{}, fmt.Errorf("%w: %d of %d (more than %g%%); nothing was changed",
			ErrLargeDelete, res.Deleted, stored, MaxDeleteShare*100)
	}

	if err := tx.Commit(); err != nil {
		return StoreResult{}, fmt.Errorf("store chunks: %w", err)
	}
	return res, nil
}

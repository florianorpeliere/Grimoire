package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// largeChunkTokens is the estimated size above which a chunk is listed by the
// dry run: the embedding model's default context in Ollama is 4,096 tokens.
const largeChunkTokens = 4000

// sizeBuckets are the upper bounds (estimated tokens) of the size distribution.
var sizeBuckets = []int{100, 250, 500, 1000, 2000, 4000}

// SQLSTATEs of a query on a missing table or column: a migration is missing.
const (
	codeUndefinedTable  = "42P01"
	codeUndefinedColumn = "42703"
)

// srdCommand runs "ingest srd": with -dry-run it prints the chunk report,
// otherwise it stores the chunks in the database at DATABASE_URL and embeds
// them with the provider newEmbedder returns.
func srdCommand(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer, newEmbedder newEmbedder) error {
	fs := flag.NewFlagSet("srd", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dryRun := fs.Bool("dry-run", false, "print the chunk report without touching the database")
	allowLargeDelete := fs.Bool("allow-large-delete", false,
		fmt.Sprintf("let the run delete more than %g%% of the stored chunks", srd.MaxDeleteShare*100))
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("%w: srd: %v", errUsage, err)
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("%w: srd takes at most one file", errUsage)
	}
	path := srd.DefaultPath
	if fs.NArg() == 1 {
		path = fs.Arg(0)
	}
	if *dryRun && *allowLargeDelete {
		return fmt.Errorf("%w: srd: -allow-large-delete has no effect with -dry-run", errUsage)
	}
	if *dryRun {
		chunks, err := srd.ReadFile(path)
		if err != nil {
			return err
		}
		writeReport(stdout, path, chunks)
		return nil
	}

	url, err := db.URLFromEnv(getenv)
	if err != nil {
		return err
	}
	chunks, err := srd.ReadFile(path)
	if err != nil {
		return err
	}
	embedder, err := newEmbedder(getenv)
	if err != nil {
		return err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := db.Open(pingCtx, url)
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := storeChunks(ctx, conn, path, chunks, srd.StoreOptions{AllowLargeDelete: *allowLargeDelete}, stdout); err != nil {
		return err
	}
	return embedChunks(ctx, conn, embedder, stdout)
}

// embedChunks embeds the stored chunks that need it and prints what it did
// and how long it took.
func embedChunks(ctx context.Context, conn *sql.DB, e embed.Embedder, stdout io.Writer) error {
	start := time.Now()
	res, err := srd.Embed(ctx, conn, e, srd.EmbedBatchSize)
	elapsed := time.Since(start).Round(time.Millisecond)
	if errors.Is(err, srd.ErrNoEmbeddingColumn) {
		return fmt.Errorf("%w (run \"ingest migrate\" first)", err)
	}
	if err != nil {
		if res.Embedded > 0 {
			fmt.Fprintf(stdout, "embeddings: %d chunks embedded before the error, kept for the next run\n", res.Embedded)
		}
		return err
	}
	fmt.Fprintf(stdout, "embeddings: model %s, %d dimensions\n", e.Model(), e.Dimension())
	fmt.Fprintf(stdout, "embedded %d, kept %d, in %s\n", res.Embedded, res.Kept, elapsed)
	return nil
}

// storeChunks stores chunks and prints what changed and the rows stored.
func storeChunks(ctx context.Context, conn *sql.DB, path string, chunks []srd.Chunk, opts srd.StoreOptions, stdout io.Writer) error {
	res, err := srd.Store(ctx, conn, srd.Version, chunks, opts)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == codeUndefinedTable || pgErr.Code == codeUndefinedColumn) {
		return fmt.Errorf("%w (run \"ingest migrate\" first)", err)
	}
	if errors.Is(err, srd.ErrLargeDelete) {
		return fmt.Errorf("%w (check the file; pass -allow-large-delete if the deletions are intended)", err)
	}
	if err != nil {
		return err
	}
	var rows int
	if err := conn.QueryRowContext(ctx, "SELECT count(*) FROM srd_chunks").Scan(&rows); err != nil {
		return fmt.Errorf("count stored chunks: %w", err)
	}
	fmt.Fprintf(stdout, "srd: %s, SRD %s\n", path, srd.Version)
	fmt.Fprintf(stdout, "chunks: %d\n", len(chunks))
	fmt.Fprintf(stdout, "inserted %d, updated %d, unchanged %d, deleted %d\n",
		res.Inserted, res.Updated, res.Unchanged, res.Deleted)
	fmt.Fprintf(stdout, "rows stored: %d\n", rows)
	return nil
}

// writeReport prints the chunk counts per kind, the size distribution with
// the chunks above largeChunkTokens, and the list of ids and short paths.
func writeReport(w io.Writer, path string, chunks []srd.Chunk) {
	fmt.Fprintf(w, "srd dry run: %s (database not touched)\n", path)
	fmt.Fprintf(w, "chunks: %d\n", len(chunks))

	perKind := map[srd.Kind]int{}
	for _, c := range chunks {
		perKind[c.Kind]++
	}
	kinds := make([]string, 0, len(perKind))
	for k := range perKind {
		kinds = append(kinds, string(k))
	}
	sort.Strings(kinds)
	fmt.Fprintln(w, "chunks per kind:")
	for _, k := range kinds {
		fmt.Fprintf(w, "  %-12s %5d\n", k, perKind[srd.Kind(k)])
	}

	fmt.Fprintln(w, "size (estimated tokens = characters / 4, rounded up; an estimate, not a tokenizer count):")
	if len(chunks) > 0 {
		sizes := make([]int, len(chunks))
		for i, c := range chunks {
			sizes[i] = c.EstimateTokens()
		}
		sort.Ints(sizes)
		fmt.Fprintf(w, "  min %d, median %d, p95 %d, max %d\n",
			sizes[0], sizes[(len(sizes)-1)/2], sizes[(len(sizes)*95+99)/100-1], sizes[len(sizes)-1])
		lo := 1
		for _, hi := range append(sizeBuckets, 0) {
			n := 0
			for _, s := range sizes {
				if s >= lo && (hi == 0 || s <= hi) {
					n++
				}
			}
			if hi == 0 {
				fmt.Fprintf(w, "  %5d+       %5d\n", lo, n)
			} else {
				fmt.Fprintf(w, "  %5d-%-5d %5d\n", lo, hi, n)
			}
			lo = hi + 1
		}
	}
	var large []srd.Chunk
	for _, c := range chunks {
		if c.EstimateTokens() > largeChunkTokens {
			large = append(large, c)
		}
	}
	fmt.Fprintf(w, "chunks above %d estimated tokens: %d\n", largeChunkTokens, len(large))
	for _, c := range large {
		fmt.Fprintf(w, "  %6d  %s\n", c.EstimateTokens(), c.PathString())
	}

	fmt.Fprintln(w, "chunks (id, kind, estimated tokens, short path):")
	for _, c := range chunks {
		fmt.Fprintf(w, "  %s\t%s\t%d\t%s\n", c.ID, c.Kind, c.EstimateTokens(), c.PathString())
	}
}

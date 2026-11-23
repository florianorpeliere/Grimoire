// Command evals runs the evaluation sets. Its one subcommand, retrieval,
// runs the retrieval smoke set (evals/retrieval-smoke.txt) through the SRD
// search and reports, per configuration and group, how often an expected
// chunk comes first and how often one is in the top results. It measures
// only: a low score never makes it fail. It never calls a text-generation
// API: the only remote calls are to the database at DATABASE_URL and the
// Ollama embedding server at OLLAMA_HOST.
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/ollama"
	"github.com/florianorpeliere/grimoire/internal/search"
)

// defaultSmokeFile is the retrieval smoke set, relative to the repository root.
const defaultSmokeFile = "evals/retrieval-smoke.txt"

// top is the number of results scored per question: the ask command's
// default limit.
const top = search.DefaultLimit

var usage = fmt.Sprintf(`usage: evals retrieval [file]

Runs every question of the retrieval smoke set (default %[1]s; format in
evals/README.md) through the SRD search in the database at DATABASE_URL, and
prints, per configuration and group, first-result and recall@%[2]d, the rank
of each expected chunk id, and the misses. Configurations:
  hybrid             hybrid search with the ask command's defaults
  keyword            keyword search only (needs no Ollama)
  vector             vector search only, prefix as the ask command's default
  hybrid-prefix-...  hybrid search with the prefix flipped, other defaults kept
Questions are embedded by the Ollama server at OLLAMA_HOST.

Exit status: 0 when every configuration ran, whatever the scores; 1 on error:
an expected id that is not a stored chunk, an unreachable database, or an
unreachable Ollama (the keyword results are printed first); 2 on a usage
error.
`, defaultSmokeFile, top)

// errUsage marks command-line mistakes, reported with exit code 2.
var errUsage = errors.New("invalid usage")

// newEmbedder returns the embedding provider configured by the environment.
type newEmbedder func(getenv func(string) string) (embed.Embedder, error)

func ollamaFromEnv(getenv func(string) string) (embed.Embedder, error) {
	return ollama.FromEnv(getenv)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout, ollamaFromEnv)
	switch {
	case err == nil:
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(os.Stdout, usage)
	case errors.Is(err, errUsage):
		fmt.Fprintln(os.Stderr, "evals:", err)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "evals:", err)
		os.Exit(1)
	}
}

// parseArgs returns the smoke set file named by the command line.
func parseArgs(args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("%w: the subcommand is missing", errUsage)
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		return "", flag.ErrHelp
	case "retrieval":
	default:
		return "", fmt.Errorf("%w: unknown subcommand %q (want retrieval)", errUsage, args[0])
	}
	switch rest := args[1:]; len(rest) {
	case 0:
		return defaultSmokeFile, nil
	case 1:
		if strings.HasPrefix(rest[0], "-") {
			if rest[0] == "-h" || rest[0] == "-help" || rest[0] == "--help" {
				return "", flag.ErrHelp
			}
			return "", fmt.Errorf("%w: unknown flag %q", errUsage, rest[0])
		}
		return rest[0], nil
	default:
		return "", fmt.Errorf("%w: expected at most one file, got %d arguments", errUsage, len(rest))
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer, newEmbedder newEmbedder) error {
	file, err := parseArgs(args)
	if err != nil {
		return err
	}
	qs, err := readSmoke(file)
	if err != nil {
		return err
	}
	dbURL, err := db.URLFromEnv(getenv)
	if err != nil {
		return err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := db.Open(pingCtx, dbURL)
	if err != nil {
		return explainDB(err)
	}
	defer conn.Close()
	known, err := storedIDs(ctx, conn)
	if err != nil {
		return err
	}
	if unknown := unknownIDs(qs, known); len(unknown) > 0 {
		return fmt.Errorf("%s: %d expected chunk id(s) are not stored chunks (check them against ingest srd -dry-run): %s",
			file, len(unknown), strings.Join(unknown, ", "))
	}

	// Without Ollama, keyword still runs; the others are skipped and the run
	// fails after printing the report.
	e, ollamaErr := newEmbedder(getenv)
	h := header{File: file, Top: top}
	if ollamaErr == nil {
		h.Model = e.Model()
	}
	results, err := runAll(ctx, qs, configs(conn, e), top, ollamaErr)
	if err != nil {
		return err
	}
	if err := writeReport(stdout, h, qs, results); err != nil {
		return err
	}
	if err := skipped(results); err != nil {
		return fmt.Errorf("only the configurations without Ollama ran: cannot use the Ollama server at %s: %w", ollama.EnvHost, err)
	}
	return nil
}

func readSmoke(file string) ([]question, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	qs, err := parseSmoke(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return qs, nil
}

// configs returns the configurations of the run, in report order. The
// hybrid defaults are the ask command's: search.DefaultPrefix, DefaultK,
// DefaultCandidates and a limit of top.
func configs(conn *sql.DB, e embed.Embedder) []config {
	hybrid := func(prefix bool) searchFunc {
		return func(ctx context.Context, q string) ([]string, error) {
			hits, err := search.Hybrid(ctx, conn, e, q, search.Options{
				K: search.DefaultK, Candidates: search.DefaultCandidates, Limit: top, Prefix: prefix})
			ids := make([]string, len(hits))
			for i, h := range hits {
				ids[i] = h.ID
			}
			return ids, err
		}
	}
	keyword := func(ctx context.Context, q string) ([]string, error) {
		rs, err := search.Keyword(ctx, conn, q, top)
		return resultIDs(rs), err
	}
	vector := func(ctx context.Context, q string) ([]string, error) {
		vecs, err := e.Embed(ctx, []string{search.QueryText(q, search.DefaultPrefix)})
		if err != nil {
			return nil, fmt.Errorf("embed the question: %w", err)
		}
		if len(vecs) != 1 {
			return nil, fmt.Errorf("embed the question: got %d vectors, want 1", len(vecs))
		}
		rs, err := search.Vector(ctx, conn, e.Model(), vecs[0], top)
		return resultIDs(rs), err
	}
	return []config{
		{Name: "hybrid", Ollama: true, Search: hybrid(search.DefaultPrefix),
			Desc: fmt.Sprintf("hybrid, ask defaults: prefix %s, k %d, candidates %d, limit %d",
				onOff(search.DefaultPrefix), search.DefaultK, search.DefaultCandidates, top)},
		{Name: "keyword", Search: keyword,
			Desc: fmt.Sprintf("keyword only (full-text), limit %d", top)},
		{Name: "vector", Ollama: true, Search: vector,
			Desc: fmt.Sprintf("vector only, prefix %s, limit %d", onOff(search.DefaultPrefix), top)},
		{Name: "hybrid-prefix-" + onOff(!search.DefaultPrefix), Ollama: true, Search: hybrid(!search.DefaultPrefix),
			Desc: fmt.Sprintf("hybrid, prefix %s, other options as hybrid", onOff(!search.DefaultPrefix))},
	}
}

func resultIDs(rs []search.Result) []string {
	ids := make([]string, len(rs))
	for i, r := range rs {
		ids[i] = r.ID
	}
	return ids
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// storedIDs returns the ids of the stored SRD chunks. It fails when the
// schema or the chunks are missing, so that they cannot pass for unknown ids.
func storedIDs(ctx context.Context, conn *sql.DB) (map[string]bool, error) {
	var exists bool
	if err := conn.QueryRowContext(ctx, "SELECT to_regclass('srd_chunks') IS NOT NULL").Scan(&exists); err != nil {
		return nil, fmt.Errorf("check the SRD chunks: %w", err)
	}
	if !exists {
		return nil, errors.New("the database has no srd_chunks table: apply the migrations first (ingest migrate)")
	}
	rows, err := conn.QueryContext(ctx, "SELECT id FROM srd_chunks")
	if err != nil {
		return nil, fmt.Errorf("read the SRD chunk ids: %w", err)
	}
	defer rows.Close()
	ids := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("read the SRD chunk ids: %w", err)
		}
		ids[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read the SRD chunk ids: %w", err)
	}
	if len(ids) == 0 {
		return nil, errors.New("no SRD chunk in the database: ingest the SRD first (ingest srd)")
	}
	return ids, nil
}

// explainDB adds a hint to a failed connection, as the ask command does.
func explainDB(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("the database server at %s refused the connection: %w", db.EnvURL, err)
	}
	return fmt.Errorf("cannot reach the database at %s (is PostgreSQL running?): %w", db.EnvURL, err)
}

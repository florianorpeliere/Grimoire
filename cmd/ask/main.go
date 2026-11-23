// Command ask searches the SRD chunks for a rules question and prints the
// passages found, so retrieval can be judged before any answer generation
// exists. It never calls a text-generation API: the only remote calls are to
// the database at DATABASE_URL and, unless -method keyword, to the Ollama
// embedding server at OLLAMA_HOST.
//
// The question is embedded with the Qwen3 query instruction by default
// (search.DefaultPrefix, see search.QueryText); -no-prefix turns it off.
// See usage for the flags and the JSON output.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/ollama"
	"github.com/florianorpeliere/grimoire/internal/search"
)

// excerptRunes is the length of the passage excerpt in the plain-text output.
// The JSON output always has the full text.
const excerptRunes = 300

// Search methods of the -method flag.
const (
	methodHybrid  = "hybrid"
	methodKeyword = "keyword"
	methodVector  = "vector"
)

var usage = fmt.Sprintf(`usage: ask [flags] "question"

Searches the SRD chunks in the database at DATABASE_URL for the question and
prints the passages found, best first. The question is embedded by the
Ollama server at OLLAMA_HOST (model %[1]s);
-method keyword needs no Ollama. Flags go before the question; quote the
question.

flags:
  -limit n      number of passages (default %[2]d). Hybrid returns at most
                %[3]d (2 x %[5]d candidates): each method contributes at most
                %[5]d chunks
  -no-prefix    embed the question as it is; by default it is embedded with
                the Qwen3 query instruction (prefix on)
  -method m     hybrid (default), keyword or vector; keyword and vector run
                one method only, for debugging
  -json         print JSON instead of plain text

JSON output (one object):
  question         the question
  method           "hybrid", "keyword" or "vector"
  searched         the methods that ran: ["keyword", "vector"] for hybrid,
                   ["keyword"] or ["vector"] otherwise
  prefix           true if the question was embedded with the query
                   instruction (always false for keyword, which embeds nothing)
  model            the embedding model, "" for keyword
  limit            the number of passages asked for (-limit)
  effective_limit  the most passages the search can return: limit, or for
                   hybrid min(limit, 2 x candidates)
  k                reciprocal rank fusion constant (%[4]d); 0 unless hybrid
  candidates       results asked from each method (%[5]d); 0 unless hybrid
  latency_ms       time taken by the search, in milliseconds (embedding the
                   question included, connecting to the database excluded)
  results          the passages, best first; [] when none was found. Each has:
    rank          1-based position in the results
    id            chunk id, e.g. "spells.fireball"
    path          citation path, e.g. "Spells > Fireball"
    kind          chunk kind, e.g. "spell", "rule", "condition"
    found_by      "keyword", "vector" or "both"
    keyword_rank  1-based rank in the keyword list; 0 if not found by it, or
                  if keyword is not in searched
    vector_rank   1-based rank in the vector list; 0 if not found by it, or
                  if vector is not in searched
    score         hybrid: reciprocal rank fusion score; keyword: full-text
                  rank; vector: cosine similarity. Comparable only within
                  one search.
    text          the full passage text

Errors go to stderr as plain text, also with -json: check the exit status.
Exit status: 0 on success, also when no passage is found; 1 on error; 2 on
a usage error.
`, ollama.Model, search.DefaultLimit, 2*search.DefaultCandidates, search.DefaultK, search.DefaultCandidates)

// errUsage marks command-line mistakes, reported with exit code 2.
var errUsage = errors.New("invalid usage")

var (
	// errNoSchema is returned when the srd_chunks table does not exist.
	errNoSchema = errors.New("the database has no srd_chunks table: apply the migrations first (ingest migrate)")
	// errNoChunks is returned when no SRD chunk has been stored.
	errNoChunks = errors.New("no SRD chunk in the database: ingest the SRD first (ingest srd)")
)

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
		fmt.Fprintln(os.Stderr, "ask:", err)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	default:
		fmt.Fprintln(os.Stderr, "ask:", err)
		os.Exit(1)
	}
}

// config is the parsed command line.
type config struct {
	question string
	limit    int
	prefix   bool
	method   string
	json     bool
}

// parseArgs reads the flags and the question. It checks the question before
// anything connects: empty, or longer than search.MaxQueryBytes.
func parseArgs(args []string) (config, error) {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var c config
	var noPrefix bool
	fs.IntVar(&c.limit, "limit", search.DefaultLimit, "")
	fs.BoolVar(&noPrefix, "no-prefix", !search.DefaultPrefix, "")
	fs.StringVar(&c.method, "method", methodHybrid, "")
	fs.BoolVar(&c.json, "json", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return config{}, err
		}
		return config{}, fmt.Errorf("%w: %v", errUsage, err)
	}
	c.prefix = !noPrefix
	switch c.method {
	case methodHybrid, methodKeyword, methodVector:
	default:
		return config{}, fmt.Errorf("%w: unknown method %q (want hybrid, keyword or vector)", errUsage, c.method)
	}
	if c.limit < 1 {
		return config{}, fmt.Errorf("%w: -limit must be at least 1, got %d", errUsage, c.limit)
	}
	switch fs.NArg() {
	case 0:
		return config{}, fmt.Errorf("%w: the question is missing", errUsage)
	case 1:
	default:
		return config{}, fmt.Errorf("%w: expected one question, got %d arguments (quote the question, and put the flags before it)", errUsage, fs.NArg())
	}
	c.question = fs.Arg(0)
	if strings.TrimSpace(c.question) == "" {
		return config{}, fmt.Errorf("%w: the question is empty", errUsage)
	}
	if len(c.question) > search.MaxQueryBytes {
		return config{}, fmt.Errorf("%w (this one is %d bytes): ask a shorter question", search.ErrQueryTooLong, len(c.question))
	}
	if c.method == methodKeyword {
		c.prefix = false // nothing is embedded
	}
	return c, nil
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer, newEmbedder newEmbedder) error {
	c, err := parseArgs(args)
	if err != nil {
		return err
	}
	dbURL, err := db.URLFromEnv(getenv)
	if err != nil {
		return err
	}
	var e embed.Embedder
	if c.method != methodKeyword {
		if e, err = newEmbedder(getenv); err != nil {
			return err
		}
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := db.Open(pingCtx, dbURL)
	if err != nil {
		return explainDB(err)
	}
	defer conn.Close()
	if err := checkChunks(ctx, conn); err != nil {
		return err
	}

	var model string
	if e != nil {
		model = e.Model()
	}
	r := newReport(c, model)
	start := time.Now()
	hits, err := searchHits(ctx, conn, e, r)
	r.latency = time.Since(start)
	if err != nil {
		return explain(ctx, err)
	}
	r.Results = toResults(hits)
	if c.json {
		return writeJSON(stdout, r)
	}
	return writeText(stdout, r)
}

// checkChunks fails with errNoSchema or errNoChunks when there is nothing to
// search. Without it, an empty table would look like a question with no
// passage, or like missing embeddings.
func checkChunks(ctx context.Context, conn *sql.DB) error {
	var exists bool
	if err := conn.QueryRowContext(ctx, "SELECT to_regclass('srd_chunks') IS NOT NULL").Scan(&exists); err != nil {
		return fmt.Errorf("check the SRD chunks: %w", err)
	}
	if !exists {
		return errNoSchema
	}
	if err := conn.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM srd_chunks)").Scan(&exists); err != nil {
		return fmt.Errorf("check the SRD chunks: %w", err)
	}
	if !exists {
		return errNoChunks
	}
	return nil
}

// searchHits runs the search described by r. A single method's results
// become hits with that method's rank only, and its own relevance as score.
func searchHits(ctx context.Context, conn *sql.DB, e embed.Embedder, r report) ([]search.Hit, error) {
	switch r.Method {
	case methodKeyword:
		rs, err := search.Keyword(ctx, conn, r.Question, r.Limit)
		return single(rs, search.ByKeyword), err
	case methodVector:
		vecs, err := e.Embed(ctx, []string{search.QueryText(r.Question, r.Prefix)})
		if err != nil {
			return nil, fmt.Errorf("embed the question: %w", err)
		}
		if len(vecs) != 1 {
			return nil, fmt.Errorf("embed the question: got %d vectors, want 1", len(vecs))
		}
		rs, err := search.Vector(ctx, conn, e.Model(), vecs[0], r.Limit)
		return single(rs, search.ByVector), err
	default:
		return search.Hybrid(ctx, conn, e, r.Question,
			search.Options{K: r.K, Candidates: r.Candidates, Limit: r.Limit, Prefix: r.Prefix})
	}
}

func single(rs []search.Result, m search.Method) []search.Hit {
	hits := make([]search.Hit, len(rs))
	for i, r := range rs {
		hits[i] = search.Hit{ID: r.ID, Path: r.Path, Kind: r.Kind, Text: r.Text, Score: r.Rank}
		if m == search.ByKeyword {
			hits[i].KeywordRank = i + 1
		} else {
			hits[i].VectorRank = i + 1
		}
	}
	return hits
}

// explain adds a hint to the search errors that need one.
func explain(ctx context.Context, err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && ctx.Err() == nil {
		return fmt.Errorf("cannot reach the Ollama server at %s (is it running?): %w", ollama.EnvHost, err)
	}
	return err
}

// explainDB adds a hint to a failed connection when the server could not be
// reached. When the server answered and refused (a missing database, failed
// authentication), its error says why, and PostgreSQL is running.
func explainDB(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Errorf("the database server at %s refused the connection: %w", db.EnvURL, err)
	}
	return fmt.Errorf("cannot reach the database at %s (is PostgreSQL running?): %w", db.EnvURL, err)
}

// report is the output of one search, printed as text or JSON. Its JSON
// form is documented in usage: keep both in step.
type report struct {
	Question       string   `json:"question"`
	Method         string   `json:"method"`
	Searched       []string `json:"searched"`
	Prefix         bool     `json:"prefix"`
	Model          string   `json:"model"`
	Limit          int      `json:"limit"`
	EffectiveLimit int      `json:"effective_limit"`
	K              int      `json:"k"`
	Candidates     int      `json:"candidates"`
	LatencyMS      float64  `json:"latency_ms"`
	Results        []result `json:"results"`

	latency time.Duration
}

// newReport describes the search c asks for, with every option explicit:
// hybrid runs with search.DefaultK and search.DefaultCandidates.
func newReport(c config, model string) report {
	r := report{Question: c.question, Method: c.method, Prefix: c.prefix, Model: model,
		Limit: c.limit, EffectiveLimit: c.limit}
	switch c.method {
	case methodKeyword:
		r.Searched = []string{methodKeyword}
	case methodVector:
		r.Searched = []string{methodVector}
	default:
		r.Searched = []string{methodKeyword, methodVector}
		r.K = search.DefaultK
		r.Candidates = search.DefaultCandidates
		r.EffectiveLimit = min(c.limit, 2*r.Candidates)
	}
	return r
}

// searched says whether method ran.
func (r report) searched(method string) bool {
	return slices.Contains(r.Searched, method)
}

type result struct {
	Rank        int     `json:"rank"`
	ID          string  `json:"id"`
	Path        string  `json:"path"`
	Kind        string  `json:"kind"`
	FoundBy     string  `json:"found_by"`
	KeywordRank int     `json:"keyword_rank"`
	VectorRank  int     `json:"vector_rank"`
	Score       float64 `json:"score"`
	Text        string  `json:"text"`
}

func toResults(hits []search.Hit) []result {
	out := make([]result, len(hits))
	for i, h := range hits {
		out[i] = result{
			Rank: i + 1, ID: h.ID, Path: h.Path, Kind: h.Kind,
			FoundBy:     h.FoundBy().String(),
			KeywordRank: h.KeywordRank, VectorRank: h.VectorRank,
			Score: h.Score, Text: h.Text,
		}
	}
	return out
}

func writeJSON(w io.Writer, r report) error {
	r.LatencyMS = math.Round(float64(r.latency.Microseconds())/100) / 10
	if r.Results == nil {
		r.Results = []result{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

func writeText(w io.Writer, r report) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\n", r.Question)
	limit := fmt.Sprintf("limit %d", r.Limit)
	if r.EffectiveLimit != r.Limit {
		limit += fmt.Sprintf(" (at most %d: 2 x %d candidates)", r.EffectiveLimit, r.Candidates)
	}
	switch r.Method {
	case methodKeyword:
		fmt.Fprintf(&b, "Method: keyword, %s\n", limit)
	default:
		prefix := "off"
		if r.Prefix {
			prefix = "on"
		}
		fmt.Fprintf(&b, "Method: %s, prefix %s, model %s, %s", r.Method, prefix, r.Model, limit)
		if r.Method == methodHybrid {
			fmt.Fprintf(&b, ", k %d, candidates %d", r.K, r.Candidates)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Search latency: %d ms\n", r.latency.Milliseconds())
	if len(r.Results) == 0 {
		b.WriteString("\nNo passage found.\n")
	}
	for _, res := range r.Results {
		fmt.Fprintf(&b, "\n%d. %s\n", res.Rank, res.ID)
		fmt.Fprintf(&b, "   Path: %s\n", res.Path)
		fmt.Fprintf(&b, "   Kind: %s\n", res.Kind)
		fmt.Fprintf(&b, "   Found by: %s (keyword rank: %s, vector rank: %s)\n",
			res.FoundBy, rankText(res.KeywordRank, r.searched(methodKeyword)),
			rankText(res.VectorRank, r.searched(methodVector)))
		fmt.Fprintf(&b, "   %s\n", excerpt(res.Text, excerptRunes))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// rankText shows a 1-based rank, "not found" for 0, or "not searched" when
// the method did not run.
func rankText(rank int, searched bool) string {
	switch {
	case !searched:
		return "not searched"
	case rank == 0:
		return "not found"
	}
	return fmt.Sprint(rank)
}

// excerpt returns text on one line, whitespace collapsed, cut to at most n
// runes at a word boundary, with "..." when cut.
func excerpt(text string, n int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	runes := []rune(text)
	cut := string(runes[:n])
	if runes[n] != ' ' { // the cut is inside a word: drop it
		if i := strings.LastIndexByte(cut, ' '); i > 0 {
			cut = cut[:i]
		}
	}
	return cut + "..."
}

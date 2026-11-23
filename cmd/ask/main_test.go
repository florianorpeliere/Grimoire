package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
	"github.com/florianorpeliere/grimoire/internal/embed/ollama"
	"github.com/florianorpeliere/grimoire/internal/search"
)

func TestParseArgs(t *testing.T) {
	long := strings.Repeat("a", search.MaxQueryBytes+1)
	full := strings.Repeat("a", search.MaxQueryBytes)
	tests := []struct {
		name    string
		args    []string
		want    config
		wantErr error
		wantMsg string // substring of the error message
	}{
		{name: "defaults", args: []string{"Fireball"},
			want: config{question: "Fireball", limit: search.DefaultLimit, prefix: search.DefaultPrefix, method: methodHybrid}},
		{name: "no prefix", args: []string{"-no-prefix", "Fireball"},
			want: config{question: "Fireball", limit: search.DefaultLimit, prefix: false, method: methodHybrid}},
		{name: "limit and json, double dash", args: []string{"--limit", "3", "--json", "Fireball"},
			want: config{question: "Fireball", limit: 3, prefix: search.DefaultPrefix, method: methodHybrid, json: true}},
		{name: "vector keeps the prefix", args: []string{"-method", "vector", "Fireball"},
			want: config{question: "Fireball", limit: search.DefaultLimit, prefix: search.DefaultPrefix, method: methodVector}},
		{name: "keyword embeds nothing", args: []string{"-method", "keyword", "Fireball"},
			want: config{question: "Fireball", limit: search.DefaultLimit, prefix: false, method: methodKeyword}},
		{name: "question of exactly the limit", args: []string{full},
			want: config{question: full, limit: search.DefaultLimit, prefix: search.DefaultPrefix, method: methodHybrid}},
		{name: "help", args: []string{"-h"}, wantErr: flag.ErrHelp},
		{name: "no question", args: nil, wantErr: errUsage, wantMsg: "the question is missing"},
		{name: "empty question", args: []string{""}, wantErr: errUsage, wantMsg: "the question is empty"},
		{name: "blank question", args: []string{" \t\n"}, wantErr: errUsage, wantMsg: "the question is empty"},
		{name: "two arguments", args: []string{"Fireball", "-json"}, wantErr: errUsage, wantMsg: "quote the question"},
		{name: "question too long", args: []string{long}, wantErr: search.ErrQueryTooLong,
			wantMsg: fmt.Sprintf("longer than %d bytes", search.MaxQueryBytes)},
		{name: "limit zero", args: []string{"-limit", "0", "Fireball"}, wantErr: errUsage, wantMsg: "at least 1"},
		{name: "negative limit", args: []string{"-limit", "-2", "Fireball"}, wantErr: errUsage, wantMsg: "at least 1"},
		{name: "unknown method", args: []string{"-method", "bm25", "Fireball"}, wantErr: errUsage, wantMsg: "unknown method"},
		{name: "unknown flag", args: []string{"-k", "60", "Fireball"}, wantErr: errUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseArgs(tt.args)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				if !strings.Contains(err.Error(), tt.wantMsg) {
					t.Errorf("err = %q, want it to contain %q", err, tt.wantMsg)
				}
				return
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// countingEmbedder returns a factory for fake and counts its calls.
func countingEmbedder(fake embed.Embedder, calls *int) newEmbedder {
	return func(func(string) string) (embed.Embedder, error) {
		*calls++
		return fake, nil
	}
}

// These cases never reach a database or an embedding server.
func TestRunErrors(t *testing.T) {
	const unreachableDB = "postgres://127.0.0.1:1/grimoire_test?sslmode=disable"
	tests := []struct {
		name     string
		args     []string
		env      map[string]string
		real     bool // use the Ollama provider instead of the fake
		wantErr  error
		wantMsg  string
		embedder bool // whether the embedder factory may be called
	}{
		{name: "empty question", args: []string{""}, env: map[string]string{"DATABASE_URL": unreachableDB}, wantErr: errUsage},
		{name: "question too long", args: []string{strings.Repeat("word ", 300)},
			env: map[string]string{"DATABASE_URL": unreachableDB}, wantErr: search.ErrQueryTooLong},
		{name: "no DATABASE_URL", args: []string{"Fireball"}, env: map[string]string{}, wantErr: db.ErrNoURL},
		{name: "incomplete DATABASE_URL", args: []string{"Fireball"},
			env: map[string]string{"DATABASE_URL": "postgres://db.example/grimoire"}, wantErr: db.ErrIncompleteURL},
		{name: "no OLLAMA_HOST", args: []string{"Fireball"}, real: true,
			env: map[string]string{"DATABASE_URL": unreachableDB}, wantErr: ollama.ErrNoHost},
		{name: "bad OLLAMA_HOST", args: []string{"Fireball"}, real: true,
			env: map[string]string{"DATABASE_URL": unreachableDB, "OLLAMA_HOST": "localhost:11434"}, wantErr: ollama.ErrBadHost},
		{name: "database unreachable", args: []string{"Fireball"}, embedder: true,
			env: map[string]string{"DATABASE_URL": unreachableDB}, wantMsg: "cannot reach the database at DATABASE_URL"},
		{name: "keyword needs no Ollama", args: []string{"-method", "keyword", "Fireball"}, real: true,
			env: map[string]string{"DATABASE_URL": unreachableDB}, wantMsg: "cannot reach the database at DATABASE_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &embedtest.Fake{ModelName: "fake-model", Dim: 4}
			calls := 0
			factory := countingEmbedder(fake, &calls)
			if tt.real {
				factory = ollamaFromEnv
			}
			var out bytes.Buffer
			err := run(context.Background(), tt.args, func(k string) string { return tt.env[k] }, &out, factory)
			if err == nil {
				t.Fatal("err = nil, want an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %q, want it to contain %q", err, tt.wantMsg)
			}
			if !tt.embedder && calls > 0 {
				t.Errorf("the embedder was created %d times, want none", calls)
			}
			if texts := fake.Texts(); len(texts) > 0 {
				t.Errorf("embedded %q, want nothing", texts)
			}
			if out.Len() > 0 {
				t.Errorf("printed %q, want nothing on error", out.String())
			}
		})
	}
}

func TestExplain(t *testing.T) {
	refused := fmt.Errorf("embed the question: ollama: %w (tried 4 times)",
		&url.Error{Op: "Post", URL: "http://127.0.0.1:1/api/embed", Err: errors.New("connect: connection refused")})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	noEmbeddings := fmt.Errorf("%w: model fake-model (embed the SRD with it first: ingest srd)", search.ErrNoEmbeddings)
	tests := []struct {
		name    string
		ctx     context.Context
		err     error
		wantMsg string
	}{
		{name: "Ollama unreachable", ctx: context.Background(), err: refused,
			wantMsg: "cannot reach the Ollama server at OLLAMA_HOST (is it running?): embed the question"},
		{name: "interrupted, no hint", ctx: canceled, err: refused, wantMsg: refused.Error()},
		{name: "no embeddings, unchanged", ctx: context.Background(), err: noEmbeddings, wantMsg: noEmbeddings.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := explain(tt.ctx, tt.err)
			if !errors.Is(got, tt.err) {
				t.Errorf("explain does not wrap %v", tt.err)
			}
			if !strings.HasPrefix(got.Error(), tt.wantMsg) {
				t.Errorf("err = %q, want it to start with %q", got, tt.wantMsg)
			}
		})
	}
}

func TestExcerpt(t *testing.T) {
	tests := []struct {
		name string
		text string
		n    int
		want string
	}{
		{name: "short", text: "Speed 0.", n: 20, want: "Speed 0."},
		{name: "whitespace collapsed", text: "a\n\n  b\tc ", n: 20, want: "a b c"},
		{name: "exactly n", text: "abcde", n: 5, want: "abcde"},
		{name: "cut at a word", text: "one two three four", n: 10, want: "one two..."},
		{name: "one long word", text: "abcdefghij", n: 4, want: "abcd..."},
		{name: "runes, not bytes", text: "éé éé", n: 5, want: "éé éé"},
		{name: "cut just before a space", text: "éé éé éé", n: 5, want: "éé éé..."},
		{name: "runes, cut", text: "éé éé éé", n: 6, want: "éé éé..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := excerpt(tt.text, tt.n); got != tt.want {
				t.Errorf("excerpt(%q, %d) = %q, want %q", tt.text, tt.n, got, tt.want)
			}
		})
	}
}

var sampleHits = []search.Hit{
	{ID: "spells.fireball", Path: "Spells > Fireball", Kind: "spell", Text: "A bright streak\nflashes.",
		Score: 0.0325, KeywordRank: 1, VectorRank: 2},
	{ID: "rules-glossary.grappled", Path: "Rules Glossary > Grappled", Kind: "condition", Text: "Speed 0.",
		Score: 0.0161, VectorRank: 1},
}

func TestNewReport(t *testing.T) {
	maxHybrid := 2 * search.DefaultCandidates
	tests := []struct {
		name string
		c    config
		want report
	}{
		{name: "hybrid", c: config{question: "q", method: methodHybrid, limit: 8, prefix: true},
			want: report{Question: "q", Method: methodHybrid, Searched: []string{"keyword", "vector"}, Prefix: true, Model: "m",
				Limit: 8, EffectiveLimit: 8, K: search.DefaultK, Candidates: search.DefaultCandidates}},
		{name: "hybrid, limit at the cap", c: config{question: "q", method: methodHybrid, limit: maxHybrid},
			want: report{Question: "q", Method: methodHybrid, Searched: []string{"keyword", "vector"}, Model: "m",
				Limit: maxHybrid, EffectiveLimit: maxHybrid, K: search.DefaultK, Candidates: search.DefaultCandidates}},
		{name: "hybrid, limit above the cap", c: config{question: "q", method: methodHybrid, limit: 1000},
			want: report{Question: "q", Method: methodHybrid, Searched: []string{"keyword", "vector"}, Model: "m",
				Limit: 1000, EffectiveLimit: maxHybrid, K: search.DefaultK, Candidates: search.DefaultCandidates}},
		{name: "keyword, no cap", c: config{question: "q", method: methodKeyword, limit: 1000},
			want: report{Question: "q", Method: methodKeyword, Searched: []string{"keyword"}, Model: "m",
				Limit: 1000, EffectiveLimit: 1000}},
		{name: "vector, no cap", c: config{question: "q", method: methodVector, limit: 1000, prefix: true},
			want: report{Question: "q", Method: methodVector, Searched: []string{"vector"}, Prefix: true, Model: "m",
				Limit: 1000, EffectiveLimit: 1000}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newReport(tt.c, "m"); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

// withResults returns r with hits as its results and a latency.
func withResults(r report, hits []search.Hit, latency time.Duration) report {
	r.Results = toResults(hits)
	r.latency = latency
	return r
}

func TestWriteText(t *testing.T) {
	keywordHit := []search.Hit{{ID: "spells.fireball", Path: "Spells > Fireball", Kind: "spell", Text: "x", KeywordRank: 1}}
	vectorHit := []search.Hit{{ID: "spells.fireball", Path: "Spells > Fireball", Kind: "spell", Text: "x", VectorRank: 1}}
	tests := []struct {
		name    string
		r       report
		want    []string
		notWant []string
	}{
		{
			name: "hybrid",
			r: withResults(newReport(config{question: "Fireball", method: methodHybrid, limit: 8, prefix: true}, "m"),
				sampleHits, 42*time.Millisecond),
			want: []string{
				"Question: Fireball\n",
				fmt.Sprintf("Method: hybrid, prefix on, model m, limit 8, k %d, candidates %d\n", search.DefaultK, search.DefaultCandidates),
				"Search latency: 42 ms\n",
				"\n1. spells.fireball\n   Path: Spells > Fireball\n   Kind: spell\n" +
					"   Found by: both (keyword rank: 1, vector rank: 2)\n   A bright streak flashes.\n",
				"\n2. rules-glossary.grappled\n",
				"   Found by: vector (keyword rank: not found, vector rank: 1)\n",
			},
			notWant: []string{"No passage found.", "not searched", "at most"},
		},
		{
			name: "hybrid, limit above the cap",
			r:    newReport(config{question: "q", method: methodHybrid, limit: 1000, prefix: true}, "m"),
			want: []string{fmt.Sprintf("limit 1000 (at most %d: 2 x %d candidates), k %d",
				2*search.DefaultCandidates, search.DefaultCandidates, search.DefaultK)},
		},
		{
			name: "vector, prefix off, keyword not searched",
			r:    withResults(newReport(config{question: "q", method: methodVector, limit: 3}, "m"), vectorHit, 0),
			want: []string{"Method: vector, prefix off, model m, limit 3\n",
				"   Found by: vector (keyword rank: not searched, vector rank: 1)\n"},
			notWant: []string{"not found", "candidates"},
		},
		{
			name:    "keyword, vector not searched",
			r:       withResults(newReport(config{question: "q", method: methodKeyword, limit: 1000}, ""), keywordHit, 0),
			want:    []string{"Method: keyword, limit 1000\n", "   Found by: keyword (keyword rank: 1, vector rank: not searched)\n"},
			notWant: []string{"prefix", "model", "not found", "at most"},
		},
		{
			name: "no passage",
			r:    newReport(config{question: "q", method: methodKeyword, limit: 8}, ""),
			want: []string{"\nNo passage found.\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := writeText(&b, tt.r); err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !strings.Contains(b.String(), w) {
					t.Errorf("output does not contain %q:\n%s", w, b.String())
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(b.String(), w) {
					t.Errorf("output contains %q:\n%s", w, b.String())
				}
			}
		})
	}
}

func TestWriteJSON(t *testing.T) {
	tests := []struct {
		name string
		r    report
		want string
	}{
		{
			name: "hybrid",
			r: withResults(newReport(config{question: "Fireball", method: methodHybrid, limit: 1000, prefix: true}, "m"),
				sampleHits[:1], 42*time.Millisecond+360*time.Microsecond),
			want: fmt.Sprintf(`{"question":"Fireball","method":"hybrid","searched":["keyword","vector"],"prefix":true,"model":"m",`+
				`"limit":1000,"effective_limit":%d,"k":%d,"candidates":%d,"latency_ms":42.4,`+
				`"results":[{"rank":1,"id":"spells.fireball","path":"Spells > Fireball","kind":"spell","found_by":"both",`+
				`"keyword_rank":1,"vector_rank":2,"score":0.0325,"text":"A bright streak\nflashes."}]}`,
				2*search.DefaultCandidates, search.DefaultK, search.DefaultCandidates),
		},
		{
			name: "keyword, no hit is an empty list",
			r:    newReport(config{question: "q", method: methodKeyword, limit: 8}, ""),
			want: `{"question":"q","method":"keyword","searched":["keyword"],"prefix":false,"model":"",` +
				`"limit":8,"effective_limit":8,"k":0,"candidates":0,"latency_ms":0,"results":[]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			if err := writeJSON(&b, tt.r); err != nil {
				t.Fatal(err)
			}
			var got, want any
			if err := json.Unmarshal(b.Bytes(), &got); err != nil {
				t.Fatalf("invalid JSON: %v\n%s", err, b.String())
			}
			if err := json.Unmarshal([]byte(tt.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got  %s\nwant %s", b.String(), tt.want)
			}
		})
	}
}

func TestExplainDB(t *testing.T) {
	dial := fmt.Errorf("reach database: %w", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connect: connection refused")})
	tests := []struct {
		name    string
		err     error
		wantMsg string
		hint    bool
	}{
		{name: "unreachable", err: dial, wantMsg: "cannot reach the database at DATABASE_URL (is PostgreSQL running?)", hint: true},
		{name: "missing database", err: fmt.Errorf("reach database: %w", &pgconn.PgError{Code: "3D000", Message: `database "x" does not exist`}),
			wantMsg: "the database server at DATABASE_URL refused the connection"},
		{name: "failed authentication", err: fmt.Errorf("reach database: %w", &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}),
			wantMsg: "the database server at DATABASE_URL refused the connection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := explainDB(tt.err)
			if !errors.Is(got, tt.err) {
				t.Errorf("explainDB does not wrap %v", tt.err)
			}
			if !strings.HasPrefix(got.Error(), tt.wantMsg) {
				t.Errorf("err = %q, want it to start with %q", got, tt.wantMsg)
			}
			if hint := strings.Contains(got.Error(), "is PostgreSQL running?"); hint != tt.hint {
				t.Errorf("hint shown = %v, want %v: %q", hint, tt.hint, got)
			}
		})
	}
}

// The usage states the defaults and documents every JSON field.
func TestUsage(t *testing.T) {
	want := []string{
		fmt.Sprintf("number of passages (default %d)", search.DefaultLimit),
		"by default it is embedded with\n                the Qwen3 query instruction (prefix on)",
		"hybrid (default)",
		ollama.Model,
		fmt.Sprintf("Hybrid returns at most\n                %d (2 x %d candidates)", 2*search.DefaultCandidates, search.DefaultCandidates),
		"Errors go to stderr as plain text, also with -json: check the exit status.",
	}
	for _, w := range want {
		if !strings.Contains(usage, w) {
			t.Errorf("usage does not contain %q", w)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[report](), reflect.TypeFor[result]()} {
		for f := range typ.Fields() {
			tag := f.Tag.Get("json")
			if tag == "" {
				continue
			}
			if !regexp.MustCompile(`(?m)^ +` + tag + ` `).MatchString(usage) {
				t.Errorf("usage does not document the JSON field %q", tag)
			}
		}
	}
}

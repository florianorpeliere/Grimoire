package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/florianorpeliere/grimoire/internal/db"
	"github.com/florianorpeliere/grimoire/internal/dbtest"
	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
	"github.com/florianorpeliere/grimoire/internal/embed/ollama"
	"github.com/florianorpeliere/grimoire/internal/search"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// fixtures are shaped like the chunker output.
var fixtures = []srd.Chunk{
	{
		ID: "spells.fireball", Path: []string{"Spells", "Fireball"},
		FullPath: []string{"Spells", "Spell Descriptions", "Fireball"},
		Title:    "Fireball", Kind: srd.KindSpell,
		Text: "A bright streak flashes to a point you choose and blossoms into an explosion of flame.",
	},
	{
		ID: "rules-glossary.grappled", Path: []string{"Rules Glossary", "Grappled"},
		FullPath: []string{"Rules Glossary", "Rules Definitions", "Grappled"},
		Title:    "Grappled", Kind: srd.KindCondition,
		Text: "While you have this condition, your Speed is 0 and can't increase.",
	},
	{
		ID: "rules-glossary.opportunity-attacks", Path: []string{"Rules Glossary", "Opportunity Attacks"},
		FullPath: []string{"Rules Glossary", "Rules Definitions", "Opportunity Attacks"},
		Title:    "Opportunity Attacks", Kind: srd.KindGlossary,
		Text: "You can make an Opportunity Attack when a creature that you can see leaves your reach " +
			"using its action, its Bonus Action, its Reaction, or one of its speeds.",
	},
}

// testEnv returns a getenv that points DATABASE_URL at the test database.
func testEnv(extra map[string]string) func(string) string {
	return func(k string) string {
		if k == db.EnvURL {
			return os.Getenv(dbtest.EnvURL)
		}
		return extra[k]
	}
}

func fixedEmbedder(e embed.Embedder) newEmbedder {
	return func(func(string) string) (embed.Embedder, error) { return e, nil }
}

func noEmbedder(t *testing.T) newEmbedder {
	return func(func(string) string) (embed.Embedder, error) {
		t.Error("the embedder was created, want none")
		return nil, errors.New("no embedder")
	}
}

// askJSON runs the command with -json and decodes its output.
func askJSON(t *testing.T, factory newEmbedder, args ...string) report {
	t.Helper()
	var out bytes.Buffer
	if err := run(context.Background(), append([]string{"-json"}, args...), testEnv(nil), &out, factory); err != nil {
		t.Fatalf("run: %v", err)
	}
	var r report
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	return r
}

// errCase is a run expected to fail without printing anything.
type errCase struct {
	name    string
	args    []string
	factory newEmbedder
	wantErr error  // nil: any error
	wantMsg string // substring of the error message
}

// TestAskDatabase walks the database through its states, from no schema to
// embedded chunks, and checks the command at each one. The steps depend on
// each other, so a failure stops the test.
func TestAskDatabase(t *testing.T) {
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	fake := &embedtest.Fake{ModelName: "fake-model", Dim: 1024}

	errCases := func(t *testing.T, cases []errCase) {
		t.Helper()
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				var out bytes.Buffer
				err := run(ctx, tt.args, testEnv(nil), &out, tt.factory)
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Errorf("err = %v, want %v", err, tt.wantErr)
				}
				if err == nil || !strings.Contains(err.Error(), tt.wantMsg) {
					t.Errorf("err = %v, want it to contain %q", err, tt.wantMsg)
				}
				if out.Len() > 0 {
					t.Errorf("printed %q, want nothing on error", out.String())
				}
			})
		}
	}

	// Before the migrations, then before any chunk is stored: the embedder
	// is never called.
	for _, state := range []struct {
		name    string
		wantErr error
		setup   func()
	}{
		{name: "no schema", wantErr: errNoSchema, setup: func() {}},
		{name: "no chunk", wantErr: errNoChunks, setup: func() {
			if _, err := db.Migrate(ctx, conn); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
		}},
	} {
		state.setup()
		fake.Reset()
		errCases(t, []errCase{
			{name: state.name + ", hybrid", args: []string{"Fireball"}, factory: fixedEmbedder(fake), wantErr: state.wantErr},
			{name: state.name + ", keyword", args: []string{"-method", "keyword", "Fireball"}, factory: noEmbedder(t), wantErr: state.wantErr},
		})
		if texts := fake.Texts(); len(texts) > 0 {
			t.Errorf("%s: embedded %q, want nothing", state.name, texts)
		}
	}

	chunks := append([]srd.Chunk(nil), fixtures...)
	for i := range chunks {
		chunks[i].Hash = "hash " + chunks[i].ID
	}
	if _, err := srd.Store(ctx, conn, srd.Version, chunks, srd.StoreOptions{}); err != nil {
		t.Fatalf("Store: %v", err)
	}

	// Stored but not embedded: the vector search refuses, naming the model.
	wantNoEmb := "model fake-model (embed the SRD with it first: ingest srd)"
	unreachable := &embedtest.Fake{ModelName: "fake-model", Dim: 1024,
		Err: fmt.Errorf("ollama: %w (tried 4 times)",
			&url.Error{Op: "Post", URL: "http://127.0.0.1:1/api/embed", Err: errors.New("connect: connection refused")})}
	errCases(t, []errCase{
		{name: "no embedding, hybrid", args: []string{"Fireball"}, factory: fixedEmbedder(fake),
			wantErr: search.ErrNoEmbeddings, wantMsg: wantNoEmb},
		{name: "no embedding, vector", args: []string{"-method", "vector", "Fireball"}, factory: fixedEmbedder(fake),
			wantErr: search.ErrNoEmbeddings, wantMsg: wantNoEmb},
		{name: "Ollama unreachable", args: []string{"Fireball"}, factory: fixedEmbedder(unreachable),
			wantMsg: "cannot reach the Ollama server at OLLAMA_HOST (is it running?)"},
		{name: "Ollama unreachable, vector", args: []string{"-method", "vector", "Fireball"}, factory: fixedEmbedder(unreachable),
			wantMsg: "cannot reach the Ollama server at OLLAMA_HOST (is it running?)"},
	})

	t.Run("keyword without embeddings", func(t *testing.T) {
		r := askJSON(t, noEmbedder(t), "-method", "keyword", "Fireball")
		if len(r.Results) == 0 || r.Results[0].ID != "spells.fireball" {
			t.Fatalf("results = %+v, want spells.fireball first", r.Results)
		}
		if got := r.Results[0]; got.FoundBy != "keyword" || got.KeywordRank != 1 || got.VectorRank != 0 {
			t.Errorf("first result = %+v, want found by keyword at rank 1 only", got)
		}
		if r.Prefix || r.Model != "" || !slices.Equal(r.Searched, []string{"keyword"}) || r.K != 0 || r.Candidates != 0 {
			t.Errorf("report = %+v, want no prefix, no model, searched [keyword], k and candidates 0", r)
		}
		var out bytes.Buffer
		if err := run(ctx, []string{"-method", "keyword", "Fireball"}, testEnv(nil), &out, noEmbedder(t)); err != nil {
			t.Fatalf("run: %v", err)
		}
		if w := "Found by: keyword (keyword rank: 1, vector rank: not searched)"; !strings.Contains(out.String(), w) {
			t.Errorf("output does not contain %q:\n%s", w, out.String())
		}
	})

	if _, err := srd.Embed(ctx, conn, fake, 0); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	tests := []struct {
		name       string
		args       []string
		wantMethod string
		wantPrefix bool
		wantLimit  int
		wantFirst  string
		wantFound  string // FoundBy of the first result, "" for any
	}{
		{name: "hybrid, prefix on by default", args: []string{"Fireball"},
			wantMethod: methodHybrid, wantPrefix: true, wantLimit: search.DefaultLimit, wantFirst: "spells.fireball"},
		{name: "hybrid, no prefix", args: []string{"-no-prefix", "Fireball"},
			wantMethod: methodHybrid, wantPrefix: false, wantLimit: search.DefaultLimit, wantFirst: "spells.fireball"},
		{name: "hybrid, limit 1", args: []string{"-limit", "1", "Grappled"},
			wantMethod: methodHybrid, wantPrefix: true, wantLimit: 1, wantFirst: "rules-glossary.grappled"},
		{name: "hybrid, limit above 2 x candidates", args: []string{"-limit", "1000", "Fireball"},
			wantMethod: methodHybrid, wantPrefix: true, wantLimit: 1000, wantFirst: "spells.fireball"},
		{name: "vector", args: []string{"-method", "vector", "Fireball"},
			wantMethod: methodVector, wantPrefix: true, wantLimit: search.DefaultLimit, wantFound: "vector"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake.Reset()
			r := askJSON(t, fixedEmbedder(fake), tt.args...)
			question := tt.args[len(tt.args)-1]
			if texts := fake.Texts(); len(texts) != 1 || texts[0] != search.QueryText(question, tt.wantPrefix) {
				t.Errorf("embedded %q, want only %q", texts, search.QueryText(question, tt.wantPrefix))
			}
			if r.Question != question || r.Method != tt.wantMethod || r.Prefix != tt.wantPrefix ||
				r.Model != fake.ModelName || r.Limit != tt.wantLimit {
				t.Errorf("report = %+v, want question %q, method %s, prefix %v, model %s, limit %d",
					r, question, tt.wantMethod, tt.wantPrefix, fake.ModelName, tt.wantLimit)
			}
			c, err := parseArgs(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			want := newReport(c, fake.ModelName)
			if !slices.Equal(r.Searched, want.Searched) || r.EffectiveLimit != want.EffectiveLimit ||
				r.K != want.K || r.Candidates != want.Candidates {
				t.Errorf("searched %v, effective_limit %d, k %d, candidates %d; want %v, %d, %d, %d",
					r.Searched, r.EffectiveLimit, r.K, r.Candidates, want.Searched, want.EffectiveLimit, want.K, want.Candidates)
			}
			if r.LatencyMS < 0 {
				t.Errorf("latency_ms = %v, want 0 or more", r.LatencyMS)
			}
			if len(r.Results) == 0 || len(r.Results) > tt.wantLimit {
				t.Fatalf("got %d results, want 1 to %d", len(r.Results), tt.wantLimit)
			}
			if tt.wantFirst != "" && r.Results[0].ID != tt.wantFirst {
				t.Errorf("first result = %s, want %s", r.Results[0].ID, tt.wantFirst)
			}
			for i, res := range r.Results {
				if res.Rank != i+1 {
					t.Errorf("result %d has rank %d", i, res.Rank)
				}
				if res.Path == "" || res.Kind == "" || res.Text == "" {
					t.Errorf("result %d lacks path, kind or text: %+v", i, res)
				}
				if tt.wantFound != "" && res.FoundBy != tt.wantFound {
					t.Errorf("result %d found by %s, want %s", i, res.FoundBy, tt.wantFound)
				}
				if want := (res.KeywordRank > 0 && res.VectorRank > 0 && res.FoundBy == "both") ||
					(res.KeywordRank > 0 && res.VectorRank == 0 && res.FoundBy == "keyword") ||
					(res.KeywordRank == 0 && res.VectorRank > 0 && res.FoundBy == "vector"); !want {
					t.Errorf("result %d: found_by %q does not match ranks %d and %d", i, res.FoundBy, res.KeywordRank, res.VectorRank)
				}
			}
		})
	}

	t.Run("plain text", func(t *testing.T) {
		var out bytes.Buffer
		if err := run(ctx, []string{"Fireball"}, testEnv(nil), &out, fixedEmbedder(fake)); err != nil {
			t.Fatalf("run: %v", err)
		}
		for _, w := range []string{
			"Question: Fireball\n", fmt.Sprintf("Method: hybrid, prefix on, model fake-model, limit 8, k %d, candidates %d\n", search.DefaultK, search.DefaultCandidates), "Search latency: ",
			"\n1. spells.fireball\n   Path: Spells > Fireball\n   Kind: spell\n   Found by: ",
		} {
			if !strings.Contains(out.String(), w) {
				t.Errorf("output does not contain %q:\n%s", w, out.String())
			}
		}
	})
}

// A server that answers and refuses (here, a missing database) gets no
// "is PostgreSQL running?" hint. Only a connection is attempted: nothing is
// created.
func TestAskDatabaseRefused(t *testing.T) {
	dbtest.Open(t) // skips without TEST_DATABASE_URL, fails if it is unusable
	u, err := url.Parse(os.Getenv(dbtest.EnvURL))
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/grimoire_test_does_not_exist"
	env := func(k string) string {
		if k == db.EnvURL {
			return u.String()
		}
		return ""
	}
	var out bytes.Buffer
	err = run(context.Background(), []string{"-method", "keyword", "Fireball"}, env, &out, noEmbedder(t))
	if err == nil {
		t.Fatal("err = nil, want a refused connection")
	}
	if want := "the database server at DATABASE_URL refused the connection"; !strings.HasPrefix(err.Error(), want) {
		t.Errorf("err = %q, want it to start with %q", err, want)
	}
	if strings.Contains(err.Error(), "is PostgreSQL running?") {
		t.Errorf("err = %q, want no hint that PostgreSQL is down", err)
	}
}

// TestAskLive runs the command with the real Ollama server on the fixtures.
func TestAskLive(t *testing.T) {
	liveOllama(t)
	conn := dbtest.Open(t)
	dbtest.Reset(t, conn)
	ctx := context.Background()
	if _, err := db.Migrate(ctx, conn); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	chunks := append([]srd.Chunk(nil), fixtures...)
	for i := range chunks {
		chunks[i].Hash = "hash " + chunks[i].ID
	}
	if _, err := srd.Store(ctx, conn, srd.Version, chunks, srd.StoreOptions{}); err != nil {
		t.Fatalf("Store: %v", err)
	}
	client, err := ollama.FromEnv(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srd.Embed(ctx, conn, client, 0); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	tests := []struct {
		question  string
		wantFirst string
	}{
		{question: "Fireball", wantFirst: "spells.fireball"},
		{question: "can I hit someone who walks away from me", wantFirst: "rules-glossary.opportunity-attacks"},
	}
	for _, tt := range tests {
		t.Run(tt.question, func(t *testing.T) {
			var out bytes.Buffer
			env := testEnv(map[string]string{ollama.EnvHost: os.Getenv(ollama.EnvHost)})
			if err := run(ctx, []string{"-json", tt.question}, env, &out, ollamaFromEnv); err != nil {
				t.Fatalf("run: %v", err)
			}
			var r report
			if err := json.Unmarshal(out.Bytes(), &r); err != nil {
				t.Fatal(err)
			}
			if r.Model != ollama.Model || !r.Prefix {
				t.Errorf("model %q, prefix %v, want %s with prefix", r.Model, r.Prefix, ollama.Model)
			}
			if len(r.Results) == 0 || r.Results[0].ID != tt.wantFirst {
				t.Errorf("results = %+v, want %s first", r.Results, tt.wantFirst)
			}
		})
	}
}

// liveOllama skips the test unless the Ollama server at OLLAMA_HOST is
// reachable and has the model. It never pulls the model.
func liveOllama(t *testing.T) {
	t.Helper()
	base := strings.TrimRight(strings.TrimSpace(os.Getenv(ollama.EnvHost)), "/")
	if base == "" {
		t.Skipf("skipping Ollama test: %s is not set", ollama.EnvHost)
	}
	hc := &http.Client{Timeout: 3 * time.Second}
	resp, err := hc.Get(base + "/api/tags")
	if err != nil {
		t.Skipf("skipping Ollama test: the server at %s is unreachable: %v", ollama.EnvHost, err)
	}
	defer resp.Body.Close()
	var tags struct{ Models []struct{ Name string } }
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		t.Skipf("skipping Ollama test: cannot list the models at %s: %v", ollama.EnvHost, err)
	}
	for _, m := range tags.Models {
		if m.Name == ollama.Model {
			return
		}
	}
	t.Skipf("skipping Ollama test: model %s is not pulled on the server (not pulled by tests)", ollama.Model)
}

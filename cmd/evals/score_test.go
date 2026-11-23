package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/embed"
	"github.com/florianorpeliere/grimoire/internal/embed/embedtest"
)

func TestScore(t *testing.T) {
	tests := []struct {
		name      string
		expected  []string
		got       []string
		top       int
		wantRanks []int
		wantTop   []string
		wantHit   bool
		wantFirst bool
	}{
		{name: "first", expected: []string{"a"}, got: []string{"a", "b"}, top: 8,
			wantRanks: []int{1}, wantTop: []string{"a", "b"}, wantHit: true, wantFirst: true},
		{name: "in the top, not first", expected: []string{"b"}, got: []string{"a", "b", "c"}, top: 8,
			wantRanks: []int{2}, wantTop: []string{"a", "b", "c"}, wantHit: true},
		{name: "beyond the top", expected: []string{"c"}, got: []string{"a", "b", "c"}, top: 2,
			wantRanks: []int{0}, wantTop: []string{"a", "b"}},
		{name: "rank 8 of top 8: hit", expected: []string{"h"}, got: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, top: 8,
			wantRanks: []int{8}, wantTop: []string{"a", "b", "c", "d", "e", "f", "g", "h"}, wantHit: true},
		{name: "rank 9 of top 8: miss", expected: []string{"i"}, got: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, top: 8,
			wantRanks: []int{0}, wantTop: []string{"a", "b", "c", "d", "e", "f", "g", "h"}},
		{name: "not found", expected: []string{"z"}, got: []string{"a"}, top: 8,
			wantRanks: []int{0}, wantTop: []string{"a"}},
		{name: "no result", expected: []string{"a"}, got: nil, top: 8,
			wantRanks: []int{0}, wantTop: []string{}},
		{name: "several ids: any one is a hit", expected: []string{"z", "b"}, got: []string{"a", "b"}, top: 8,
			wantRanks: []int{0, 2}, wantTop: []string{"a", "b"}, wantHit: true},
		{name: "several ids: second one first", expected: []string{"b", "a"}, got: []string{"a", "b"}, top: 8,
			wantRanks: []int{2, 1}, wantTop: []string{"a", "b"}, wantHit: true, wantFirst: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := score(tt.expected, tt.got, tt.top)
			if !reflect.DeepEqual(o.Ranks, tt.wantRanks) {
				t.Errorf("ranks = %v, want %v", o.Ranks, tt.wantRanks)
			}
			if len(o.Top) != len(tt.wantTop) || (len(o.Top) > 0 && !reflect.DeepEqual(o.Top, tt.wantTop)) {
				t.Errorf("top = %v, want %v", o.Top, tt.wantTop)
			}
			if o.hit() != tt.wantHit {
				t.Errorf("hit = %v, want %v", o.hit(), tt.wantHit)
			}
			if o.first() != tt.wantFirst {
				t.Errorf("first = %v, want %v", o.first(), tt.wantFirst)
			}
		})
	}
}

// fakeQuestions has two named questions (one per type) and two described ones.
var fakeQuestions = []question{
	{Num: 1, Group: groupNamed, Type: typeExact, Text: "q1", Expected: []string{"a"}},
	{Num: 2, Group: groupNamed, Type: typeParaphrase, Text: "q2", Expected: []string{"b", "c"}},
	{Num: 3, Group: groupDescribed, Type: typeNone, Text: "q3", Expected: []string{"d"}},
	{Num: 4, Group: groupDescribed, Type: typeNone, Text: "q4", Expected: []string{"e"}},
}

// answers returns a search that answers each question text from m.
func answers(m map[string][]string) searchFunc {
	return func(_ context.Context, q string) ([]string, error) { return m[q], nil }
}

func TestTallies(t *testing.T) {
	outs := []outcome{
		score([]string{"a"}, []string{"a"}, 8),           // first
		score([]string{"b", "c"}, []string{"x", "c"}, 8), // hit, not first
		score([]string{"d"}, []string{"x"}, 8),           // miss
		score([]string{"e"}, []string{"e"}, 8),           // first
	}
	want := []tally{
		{Label: "named", N: 2, First: 1, Hit: 2},
		{Label: "  exact", N: 1, First: 1, Hit: 1},
		{Label: "  paraphrase", N: 1, First: 0, Hit: 1},
		{Label: "described", N: 2, First: 1, Hit: 1},
	}
	if got := tallies(fakeQuestions, outs); !reflect.DeepEqual(got, want) {
		t.Errorf("tallies =\n%+v\nwant\n%+v", got, want)
	}

	// A group without questions is left out.
	if got := tallies(fakeQuestions[2:], outs[2:]); len(got) != 1 || got[0].Label != groupDescribed {
		t.Errorf("tallies without named questions = %+v, want only the described group", got)
	}
}

func TestShare(t *testing.T) {
	tests := []struct {
		n, total int
		want     string
	}{
		{0, 19, "0/19 (0%)"},
		{11, 20, "11/20 (55%)"},
		{2, 3, "2/3 (67%)"},
		{8, 8, "8/8 (100%)"},
	}
	for _, tt := range tests {
		if got := share(tt.n, tt.total); got != tt.want {
			t.Errorf("share(%d, %d) = %q, want %q", tt.n, tt.total, got, tt.want)
		}
	}
}

var errRefused = fmt.Errorf("ollama: %w (tried 4 times)",
	&url.Error{Op: "Post", URL: "http://127.0.0.1:1/api/embed", Err: errors.New("connect: connection refused")})

var errModelMissing = errors.New(`ollama: HTTP 404: model "qwen3-embedding:0.6b" not found, try pulling it first`)

// embedding returns a search that embeds the question with e through
// taggedEmbedder, as the Ollama configurations do, and finds "a".
func embedding(e embed.Embedder) searchFunc {
	te := taggedEmbedder{e}
	return func(ctx context.Context, q string) ([]string, error) {
		if _, err := te.Embed(ctx, []string{q}); err != nil {
			return nil, fmt.Errorf("embed the question: %w", err)
		}
		return []string{"a"}, nil
	}
}

func TestOllamaFailed(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{name: "connection refused", ctx: context.Background(), err: &embedError{errRefused}, want: true},
		{name: "model missing", ctx: context.Background(), err: fmt.Errorf("embed the question: %w", &embedError{errModelMissing}), want: true},
		{name: "database error", ctx: context.Background(), err: errors.New("relation does not exist"), want: false},
		{name: "not from the embedder", ctx: context.Background(), err: errRefused, want: false},
		{name: "interrupted", ctx: canceled, err: &embedError{errRefused}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ollamaFailed(tt.ctx, tt.err); got != tt.want {
				t.Errorf("ollamaFailed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTaggedEmbedder(t *testing.T) {
	ctx := context.Background()
	ok := taggedEmbedder{&embedtest.Fake{ModelName: "m", Dim: 4}}
	if vecs, err := ok.Embed(ctx, []string{"q"}); err != nil || len(vecs) != 1 {
		t.Errorf("Embed = %d vectors, %v; want 1, nil", len(vecs), err)
	}
	failing := taggedEmbedder{&embedtest.Fake{ModelName: "m", Dim: 4, Err: errModelMissing}}
	_, err := failing.Embed(ctx, []string{"q"})
	var ee *embedError
	if !errors.As(err, &ee) || !errors.Is(err, errModelMissing) || err.Error() != errModelMissing.Error() {
		t.Errorf("Embed error = %#v, want an embedError wrapping %v with its message", err, errModelMissing)
	}
}

func TestRunAll(t *testing.T) {
	ctx := context.Background()
	good := answers(map[string][]string{"q1": {"a"}, "q2": {"c"}, "q3": {"d"}, "q4": {"x"}})
	refused := embedding(&embedtest.Fake{ModelName: "m", Dim: 4, Err: errRefused})
	missing := embedding(&embedtest.Fake{ModelName: "m", Dim: 4, Err: errModelMissing})
	broken := func(context.Context, string) ([]string, error) { return nil, errors.New("relation does not exist") }
	mustNotRun := func(t *testing.T) searchFunc {
		return func(context.Context, string) ([]string, error) {
			t.Error("a skipped configuration ran")
			return nil, nil
		}
	}

	tests := []struct {
		name        string
		cfgs        func(t *testing.T) []config
		ollamaErr   error
		wantSkipped []string // names of the skipped configurations
		wantSkipErr bool
		wantErr     string
	}{
		{
			name: "every configuration runs",
			cfgs: func(*testing.T) []config {
				return []config{{Name: "hybrid", Ollama: true, Search: good}, {Name: "keyword", Search: good}}
			},
		},
		{
			name: "Ollama not configured: only keyword runs",
			cfgs: func(t *testing.T) []config {
				return []config{{Name: "hybrid", Ollama: true, Search: mustNotRun(t)}, {Name: "keyword", Search: good},
					{Name: "vector", Ollama: true, Search: mustNotRun(t)}}
			},
			ollamaErr:   errors.New("OLLAMA_HOST is not set"),
			wantSkipped: []string{"hybrid", "vector"},
			wantSkipErr: true,
		},
		{
			name: "Ollama unreachable: the next Ollama configurations are skipped",
			cfgs: func(t *testing.T) []config {
				return []config{{Name: "hybrid", Ollama: true, Search: refused}, {Name: "keyword", Search: good},
					{Name: "vector", Ollama: true, Search: mustNotRun(t)}}
			},
			wantSkipped: []string{"hybrid", "vector"},
			wantSkipErr: true,
		},
		{
			name: "Ollama returns an error: the next Ollama configurations are skipped",
			cfgs: func(t *testing.T) []config {
				return []config{{Name: "hybrid", Ollama: true, Search: missing}, {Name: "keyword", Search: good},
					{Name: "vector", Ollama: true, Search: mustNotRun(t)}}
			},
			wantSkipped: []string{"hybrid", "vector"},
			wantSkipErr: true,
		},
		{
			name: "a database error in an Ollama configuration stops the run",
			cfgs: func(*testing.T) []config {
				return []config{{Name: "keyword", Search: good}, {Name: "hybrid", Ollama: true, Search: broken}}
			},
			wantErr: "hybrid: question 1: relation does not exist",
		},
		{
			name: "another search error stops the run",
			cfgs: func(*testing.T) []config {
				return []config{{Name: "keyword", Search: broken}}
			},
			wantErr: "keyword: question 1: relation does not exist",
		},
		{
			name: "an embedding error in a configuration without Ollama stops the run",
			cfgs: func(*testing.T) []config {
				return []config{{Name: "keyword", Search: missing}}
			},
			wantErr: "keyword: question 1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := runAll(ctx, fakeQuestions, tt.cfgs(t), 8, tt.ollamaErr)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("runAll: %v", err)
			}
			var gotSkipped []string
			for _, r := range results {
				if r.Skipped != nil {
					gotSkipped = append(gotSkipped, r.Config.Name)
					continue
				}
				if len(r.Outcomes) != len(fakeQuestions) {
					t.Errorf("%s: %d outcomes, want %d", r.Config.Name, len(r.Outcomes), len(fakeQuestions))
				}
			}
			if !reflect.DeepEqual(gotSkipped, tt.wantSkipped) {
				t.Errorf("skipped = %v, want %v", gotSkipped, tt.wantSkipped)
			}
			if got := skipped(results) != nil; got != tt.wantSkipErr {
				t.Errorf("skipped() != nil is %v, want %v", got, tt.wantSkipErr)
			}
		})
	}
}

func TestWriteReport(t *testing.T) {
	ctx := context.Background()
	cfgs := []config{
		{Name: "hybrid", Desc: "hybrid desc", Ollama: true,
			Search: answers(map[string][]string{"q1": {"a"}, "q2": {"x", "c"}, "q3": {"d"}, "q4": {"y"}})},
		{Name: "keyword", Desc: "keyword desc",
			Search: answers(map[string][]string{"q1": {"z", "a"}, "q2": {"b"}})},
	}
	results, err := runAll(ctx, fakeQuestions, cfgs, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	results = append(results, result{Config: config{Name: "vector", Desc: "vector desc"}, Skipped: errors.New("OLLAMA_HOST is not set")})

	var out bytes.Buffer
	if err := writeReport(&out, header{File: "evals/x.txt", Model: "m", Top: 8}, fakeQuestions, results); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	// Rows are compared with their spaces collapsed: tabwriter aligns them.
	var lines []string
	for l := range strings.SplitSeq(got, "\n") {
		lines = append(lines, strings.Join(strings.Fields(l), " "))
	}
	for _, want := range []string{
		"Retrieval smoke set evals/x.txt: 4 questions (named 2, described 2)",
		"Embedding model: m",
		"hybrid hybrid desc",
		"vector vector desc [skipped]",
		"Skipped because: OLLAMA_HOST is not set",
		"configuration group first-result recall@8",
		"hybrid named 1/2 (50%) 2/2 (100%)",
		"hybrid exact 1/1 (100%) 1/1 (100%)",
		"hybrid paraphrase 0/1 (0%) 1/1 (100%)",
		"hybrid described 1/2 (50%) 1/2 (50%)",
		"keyword named 1/2 (50%) 2/2 (100%)",
		"keyword described 0/2 (0%) 0/2 (0%)",
		"vector (skipped)",
		"# group type expected id hybrid keyword vector",
		"1 named exact a 1 2 skipped",
		"2 named paraphrase b not found 1 skipped",
		"c 2 not found skipped",
		"4 described - e not found not found skipped",
		"hybrid: 1",
		"4. [described] q4",
		"expected e; first result y",
		"keyword: 2",
		"expected d; first result no result",
		"vector: skipped",
	} {
		found := false
		for _, l := range lines {
			if l == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("report has no line %q:\n%s", want, got)
		}
	}
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/florianorpeliere/grimoire/internal/embed"
)

// searchFunc returns the ids of the chunks found for a question, best first.
type searchFunc func(ctx context.Context, question string) ([]string, error)

// config is one search configuration of the run.
type config struct {
	Name   string // short name, a column of the report
	Desc   string // what it runs, for the report header
	Ollama bool   // embeds the question, so needs the Ollama server
	Search searchFunc
}

// outcome is the result of one question under one configuration.
type outcome struct {
	Ranks []int    // per expected id: 1-based rank within the top results, 0 if not there
	Top   []string // the ids returned, at most top, best first
}

// hit reports whether any expected id is in the top results.
func (o outcome) hit() bool { return slices.ContainsFunc(o.Ranks, func(r int) bool { return r > 0 }) }

// first reports whether the first result is an expected id.
func (o outcome) first() bool { return slices.Contains(o.Ranks, 1) }

// score ranks the expected ids within the first top ids of got.
func score(expected, got []string, top int) outcome {
	got = got[:min(len(got), top)]
	o := outcome{Ranks: make([]int, len(expected)), Top: got}
	for i, id := range expected {
		o.Ranks[i] = slices.Index(got, id) + 1
	}
	return o
}

// result is the run of one configuration over every question.
type result struct {
	Config   config
	Outcomes []outcome // one per question, in file order; nil when skipped
	Skipped  error     // why the configuration did not run
}

// embedError marks an error returned by the embedding provider, so that the
// run can tell it from a database error.
type embedError struct{ err error }

func (e *embedError) Error() string { return e.err.Error() }
func (e *embedError) Unwrap() error { return e.err }

// taggedEmbedder wraps every Embed error in an embedError.
type taggedEmbedder struct{ embed.Embedder }

func (t taggedEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := t.Embedder.Embed(ctx, texts)
	if err != nil {
		return nil, &embedError{err}
	}
	return vecs, nil
}

// ollamaFailed reports whether err came from the embedding provider:
// unreachable, model missing, server error. An interrupted run is not one.
func ollamaFailed(ctx context.Context, err error) bool {
	var ee *embedError
	return errors.As(err, &ee) && ctx.Err() == nil
}

// ollamaReached reports whether a configuration that needs Ollama ran.
func ollamaReached(results []result) bool {
	return slices.ContainsFunc(results, func(r result) bool { return r.Config.Ollama && r.Skipped == nil })
}

// runAll runs every question through every configuration. ollamaErr, when
// not nil, says why Ollama cannot be used: the configurations that need it
// are skipped. An Ollama configuration whose embedding fails (see
// ollamaFailed) is skipped too, with the ones after it (see skipped). Any
// other search error stops the run.
func runAll(ctx context.Context, qs []question, cfgs []config, top int, ollamaErr error) ([]result, error) {
	results := make([]result, 0, len(cfgs))
	for _, c := range cfgs {
		if c.Ollama && ollamaErr != nil {
			results = append(results, result{Config: c, Skipped: ollamaErr})
			continue
		}
		outs, err := runConfig(ctx, qs, c, top)
		if err != nil {
			if c.Ollama && ollamaFailed(ctx, err) {
				ollamaErr = err
				results = append(results, result{Config: c, Skipped: err})
				continue
			}
			return nil, fmt.Errorf("%s: %w", c.Name, err)
		}
		results = append(results, result{Config: c, Outcomes: outs})
	}
	return results, nil
}

// skipped returns why configurations were skipped, nil if every one ran.
func skipped(results []result) error {
	for _, r := range results {
		if r.Skipped != nil {
			return r.Skipped
		}
	}
	return nil
}

func runConfig(ctx context.Context, qs []question, c config, top int) ([]outcome, error) {
	outs := make([]outcome, len(qs))
	for i, q := range qs {
		got, err := c.Search(ctx, q.Text)
		if err != nil {
			return nil, fmt.Errorf("question %d: %w", q.Num, err)
		}
		outs[i] = score(q.Expected, got, top)
	}
	return outs, nil
}

// tally counts the questions of one slice of the set.
type tally struct {
	Label string
	N     int // questions
	First int // first result is an expected id
	Hit   int // an expected id is in the top results
}

// tallies returns, for one configuration, a tally per group and, for the
// named group, per query type, in report order.
func tallies(qs []question, outs []outcome) []tally {
	var ts []tally
	add := func(label string, keep func(question) bool) {
		t := tally{Label: label}
		for i, q := range qs {
			if !keep(q) {
				continue
			}
			t.N++
			if outs[i].first() {
				t.First++
			}
			if outs[i].hit() {
				t.Hit++
			}
		}
		if t.N > 0 {
			ts = append(ts, t)
		}
	}
	for _, g := range groups {
		add(g, func(q question) bool { return q.Group == g })
		if g == groupNamed {
			for _, typ := range namedTypes {
				add("  "+typ, func(q question) bool { return q.Group == g && q.Type == typ })
			}
		}
	}
	return ts
}

// share formats n of total with its percentage.
func share(n, total int) string {
	return fmt.Sprintf("%d/%d (%.0f%%)", n, total, 100*float64(n)/float64(total))
}

// header describes the run, above the report.
type header struct {
	File  string // the smoke set file, as given on the command line
	Model string // embedding model, "" when Ollama was not used
	Top   int
}

// writeReport prints the summary per configuration and group, the rank of
// every expected id per configuration, and the misses per configuration.
func writeReport(w io.Writer, h header, qs []question, results []result) error {
	var b strings.Builder
	perGroup := map[string]int{}
	for _, q := range qs {
		perGroup[q.Group]++
	}
	var counts []string
	for _, g := range groups {
		counts = append(counts, fmt.Sprintf("%s %d", g, perGroup[g]))
	}
	fmt.Fprintf(&b, "Retrieval smoke set %s: %d questions (%s)\n", h.File, len(qs), strings.Join(counts, ", "))
	fmt.Fprintf(&b, "Top %d results per question. A question is a hit if any expected id is in the top %d.\n", h.Top, h.Top)
	if h.Model != "" {
		fmt.Fprintf(&b, "Embedding model: %s\n", h.Model)
	}
	b.WriteString("\nConfigurations:\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, r := range results {
		desc := r.Config.Desc
		if r.Skipped != nil {
			desc += " [skipped]"
		}
		fmt.Fprintf(tw, "  %s\t%s\n", r.Config.Name, desc)
	}
	tw.Flush()
	if err := skipped(results); err != nil {
		fmt.Fprintf(&b, "Skipped because: %v\n", err)
	}

	recall := fmt.Sprintf("recall@%d", h.Top)
	b.WriteString("\nSummary (first-result: the first result is an expected id; " + recall + ": a hit in the top " + fmt.Sprint(h.Top) + ")\n")
	tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "configuration\tgroup\tfirst-result\t%s\n", recall)
	for _, r := range results {
		if r.Skipped != nil {
			fmt.Fprintf(tw, "%s\t(skipped)\t\t\n", r.Config.Name)
			continue
		}
		for _, t := range tallies(qs, r.Outcomes) {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", r.Config.Name, t.Label, share(t.First, t.N), share(t.Hit, t.N))
		}
	}
	tw.Flush()

	fmt.Fprintf(&b, "\nRank of each expected id in the top %d, per question and configuration\n", h.Top)
	tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	cols := []string{"#", "group", "type", "expected id"}
	for _, r := range results {
		cols = append(cols, r.Config.Name)
	}
	fmt.Fprintln(tw, strings.Join(cols, "\t"))
	for i, q := range qs {
		for j, id := range q.Expected {
			row := []string{"", "", "", id}
			if j == 0 {
				row = []string{fmt.Sprint(q.Num), q.Group, q.Type, id}
			}
			for _, r := range results {
				row = append(row, rankText(r, i, j))
			}
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
	}
	tw.Flush()

	fmt.Fprintf(&b, "\nMisses (no expected id in the top %d), per configuration\n", h.Top)
	for _, r := range results {
		if r.Skipped != nil {
			fmt.Fprintf(&b, "%s: skipped\n", r.Config.Name)
			continue
		}
		var misses []int
		for i, o := range r.Outcomes {
			if !o.hit() {
				misses = append(misses, i)
			}
		}
		fmt.Fprintf(&b, "%s: %d\n", r.Config.Name, len(misses))
		for _, i := range misses {
			q := qs[i]
			first := "no result"
			if top := r.Outcomes[i].Top; len(top) > 0 {
				first = top[0]
			}
			fmt.Fprintf(&b, "  %d. [%s] %s\n     expected %s; first result %s\n",
				q.Num, groupLabel(q), q.Text, strings.Join(q.Expected, " or "), first)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// rankText shows the rank of expected id j of question i under r.
func rankText(r result, i, j int) string {
	if r.Skipped != nil {
		return "skipped"
	}
	if rank := r.Outcomes[i].Ranks[j]; rank > 0 {
		return fmt.Sprint(rank)
	}
	return "not found"
}

func groupLabel(q question) string {
	if q.Type == typeNone {
		return q.Group
	}
	return q.Group + ", " + q.Type
}

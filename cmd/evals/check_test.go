package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/evalset"
)

// examplePrefix marks the commented-out format examples of the eval set; one
// space after it is part of the prefix.
const examplePrefix = "#>"

// readEvalSet parses the committed eval set.
func readEvalSet(t *testing.T) []evalset.Question {
	t.Helper()
	qs, err := evalset.Read(filepath.Join(repoRoot, evalset.DefaultFile))
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

// readExamples parses the format examples commented out in the committed
// eval set, so that they stay valid records.
func readExamples(t *testing.T) []evalset.Question {
	t.Helper()
	f, err := os.Open(filepath.Join(repoRoot, evalset.DefaultFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if s, ok := strings.CutPrefix(sc.Text(), examplePrefix); ok {
			lines = append(lines, strings.TrimPrefix(s, " "))
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	qs, err := evalset.Parse(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("format examples: %v", err)
	}
	return qs
}

// TestEvalSetExamples checks that the format examples of the committed file,
// if any are left, are valid records and are not questions of the set.
func TestEvalSetExamples(t *testing.T) {
	examples := readExamples(t)
	for _, q := range readEvalSet(t) {
		for _, ex := range examples {
			if q.Text == ex.Text {
				t.Errorf("question %d is the format example %d: examples must stay commented out", q.Num, ex.Num)
			}
		}
	}
}

// TestEvalSetIDsInChunkerOutput checks every expected id of the committed
// eval set, format examples included, against the chunker output. It is
// skipped when the SRD Markdown is absent; the check command checks the ids
// against the database anyway.
func TestEvalSetIDsInChunkerOutput(t *testing.T) {
	known := chunkerIDs(t)
	tests := []struct {
		name string
		qs   []evalset.Question
	}{
		{name: "questions", qs: readEvalSet(t)},
		{name: "format examples", qs: readExamples(t)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if unknown := evalset.UnknownIDs(tt.qs, known); len(unknown) > 0 {
				t.Errorf("expected ids not in the chunker output: %v", unknown)
			}
		})
	}
}

func TestWriteCounts(t *testing.T) {
	q := func(c evalset.Category) evalset.Question { return evalset.Question{Category: c} }
	many := func(c evalset.Category, n int) []evalset.Question {
		qs := make([]evalset.Question, n)
		for i := range qs {
			qs[i] = q(c)
		}
		return qs
	}
	var full []evalset.Question
	for _, c := range evalset.Categories {
		full = append(full, many(c, evalset.Planned[c])...)
	}
	tests := []struct {
		name string
		qs   []evalset.Question
		want []string // rows, spaces collapsed
	}{
		{
			name: "no question",
			want: []string{
				"f.txt: 0 question(s), every record well formed, every expected chunk id stored.",
				"category questions planned",
				"rules 0 80 80 below plan",
				"interaction 0 40 40 below plan",
				"refusal 0 15 15 below plan",
				"campaign 0 15 15 below plan",
				"total 0 150 150 below plan",
			},
		},
		{
			name: "below, at and above plan",
			qs:   append(append(many(evalset.Rules, 3), many(evalset.Interaction, 40)...), many(evalset.Refusal, 16)...),
			want: []string{
				"f.txt: 59 question(s)",
				"rules 3 80 77 below plan",
				"interaction 40 40 as planned",
				"refusal 16 15 1 above plan",
				"campaign 0 15 15 below plan",
				"total 59 150 91 below plan",
			},
		},
		{name: "full plan", qs: full, want: []string{"total 150 150 as planned"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := writeCounts(&out, "f.txt", tt.qs); err != nil {
				t.Fatalf("writeCounts: %v", err)
			}
			var rows []string
			for _, l := range strings.Split(out.String(), "\n") {
				rows = append(rows, strings.Join(strings.Fields(l), " "))
			}
			got := strings.Join(rows, "\n")
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("output has no row %q:\n%s", w, out.String())
				}
			}
		})
	}
}

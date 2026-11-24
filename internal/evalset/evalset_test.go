package evalset

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/search"
)

// rec builds a record from its lines.
func rec(lines ...string) string { return strings.Join(lines, "\n") + "\n" }

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []Question
		wantErr []string // substrings of the error, in any order
	}{
		{
			name: "comments, continuations, groups and spaces",
			in: "# header\n\n" +
				rec("1 rules", "Q: What does Fireball do?", "A: Fire damage", "   in a sphere.", "cite: spells.fireball") +
				"\n  # indented comment\n\n\n" +
				rec("12  interaction  ", "Q:   Can I Misty Step away", "\tto avoid an", "   Opportunity Attack?  ",
					"# a comment inside a record", "A: Yes.", "cite: a.b", "      c.d  e.f", "cite:", "  g.h") +
				"\n" +
				rec("40 refusal", "Q: Eldritch Knight?", "A: The SRD does not say.", "cite: -"),
			want: []Question{
				{Num: 1, Line: 3, Category: Rules, Text: "What does Fireball do?", Answer: "Fire damage in a sphere.",
					Expected: [][]string{{"spells.fireball"}}},
				{Num: 12, Line: 12, Category: Interaction, Text: "Can I Misty Step away to avoid an Opportunity Attack?", Answer: "Yes.",
					Expected: [][]string{{"a.b", "c.d", "e.f"}, {"g.h"}}},
				{Num: 40, Line: 23, Category: Refusal, Text: "Eldritch Knight?", Answer: "The SRD does not say."},
			},
		},
		{
			name: "keys in any order, CRLF line endings",
			in:   "7 rules\r\ncite: x.y\r\nA: a\r\nQ: q\r\n",
			want: []Question{{Num: 7, Line: 1, Category: Rules, Text: "q", Answer: "a", Expected: [][]string{{"x.y"}}}},
		},
		{
			name: "a UTF-8 BOM is ignored",
			in:   "\uFEFF# header\n" + rec("1 rules", "Q: q", "A: a", "cite: x.y"),
			want: []Question{{Num: 1, Line: 2, Category: Rules, Text: "q", Answer: "a", Expected: [][]string{{"x.y"}}}},
		},
		{
			name: "a BOM before a header",
			in:   "\uFEFF" + rec("1 rules", "Q: q", "A: a", "cite: x.y"),
			want: []Question{{Num: 1, Line: 1, Category: Rules, Text: "q", Answer: "a", Expected: [][]string{{"x.y"}}}},
		},
		{
			name: "a pipe is plain text",
			in:   rec("1 rules", "Q: a | b?", "A: c | d", "cite: x.y"),
			want: []Question{{Num: 1, Line: 1, Category: Rules, Text: "a | b?", Answer: "c | d", Expected: [][]string{{"x.y"}}}},
		},
		{name: "no record", in: "# nothing yet\n\n"},
		{name: "empty file", in: ""},
		{name: "header with one field", in: rec("1", "Q: q", "A: a", "cite: x.y"), wantErr: []string{`line 1: header "1"`}},
		{name: "header with three fields", in: rec("1 rules extra", "Q: q", "A: a", "cite: x.y"), wantErr: []string{"want <number> <category>"}},
		{name: "number not an integer", in: rec("one rules", "Q: q", "A: a", "cite: x.y"), wantErr: []string{`question number "one"`}},
		{name: "number zero", in: rec("0 rules", "Q: q", "A: a", "cite: x.y"), wantErr: []string{`question number "0"`}},
		{name: "indented header", in: rec("  1 rules", "Q: q", "A: a", "cite: x.y"), wantErr: []string{"line 1: a record starts with an unindented header"}},
		{name: "duplicate number", in: rec("1 rules", "Q: q", "A: a", "cite: x.y") + "\n" + rec("1 rules", "Q: r", "A: a", "cite: x.y"),
			wantErr: []string{"line 6: question number 1 already used on line 1"}},
		{name: "duplicate question", in: rec("1 rules", "Q: Can I  grapple?", "A: a", "cite: x.y") + "\n" +
			rec("2 interaction", "Q: can i", "  grapple?", "A: b", "cite: x.y"),
			wantErr: []string{"line 6: same question as on line 1"}},
		{name: "unknown category", in: rec("1 named", "Q: q", "A: a", "cite: x.y"),
			wantErr: []string{`line 1: category "named"`, "rules, interaction, refusal, campaign"}},
		{name: "category is case-sensitive", in: rec("1 Rules", "Q: q", "A: a", "cite: x.y"), wantErr: []string{`category "Rules"`}},
		{name: "campaign is reserved", in: rec("1 campaign", "Q: What do we know about the baron?", "A: a", "cite: x.y"),
			wantErr: []string{"category campaign is reserved"}},
		{name: "two records without a blank line", in: rec("1 rules", "Q: q", "A: a", "cite: x.y", "2 rules", "Q: r", "A: a", "cite: x.y"),
			wantErr: []string{"line 5: want a line starting with Q:, A: or cite: (records are separated by a blank line)"}},
		{name: "unknown key", in: rec("1 rules", "Question: q", "A: a", "cite: x.y"), wantErr: []string{"line 2: want a line starting with Q:"}},
		{name: "key is case-sensitive", in: rec("1 rules", "q: q", "A: a", "cite: x.y"), wantErr: []string{"line 2: want a line starting with Q:"}},
		{name: "continuation before any key", in: rec("1 rules", "  q", "Q: q", "A: a", "cite: x.y"),
			wantErr: []string{"line 2: an indented line continues"}},
		{name: "second Q", in: rec("1 rules", "Q: q", "A: a", "Q: r", "cite: x.y"), wantErr: []string{"line 4: second Q: line, the first is on line 2"}},
		{name: "second A", in: rec("1 rules", "Q: q", "A: a", "cite: x.y", "A: b"), wantErr: []string{"line 5: second A: line, the first is on line 3"}},
		{name: "no Q", in: rec("1 rules", "A: a", "cite: x.y"), wantErr: []string{"line 1: no Q: line"}},
		{name: "empty question", in: rec("1 rules", "Q:", "A: a", "cite: x.y"), wantErr: []string{"line 2: the question is empty"}},
		{name: "question too long", in: rec("1 rules", "Q: "+strings.Repeat("a", search.MaxQueryBytes), "   b", "A: a", "cite: x.y"),
			wantErr: []string{"line 2: the question is longer than 1000 bytes"}},
		{name: "no A", in: rec("1 rules", "Q: q", "cite: x.y"), wantErr: []string{"line 1: no A: line"}},
		{name: "empty answer", in: rec("1 rules", "Q: q", "A:   ", "cite: x.y"), wantErr: []string{"line 3: the expected answer is empty"}},
		{name: "no cite", in: rec("1 rules", "Q: q", "A: a"), wantErr: []string{"line 1: no cite: line"}},
		{name: "refusal without cite", in: rec("1 refusal", "Q: q", "A: a"), wantErr: []string{"no cite: line", "cite: -"}},
		{name: "empty cite line", in: rec("1 rules", "Q: q", "A: a", "cite: x.y", "cite:"), wantErr: []string{"line 5: the cite: line lists no chunk id"}},
		{name: "rules with -", in: rec("1 rules", "Q: q", "A: a", "cite: -"), wantErr: []string{"line 4: - is only for refusal questions"}},
		{name: "interaction with - in a group", in: rec("1 interaction", "Q: q", "A: a", "cite: x.y", "cite: a.b", "  -"),
			wantErr: []string{"line 5: - is only for refusal", "category interaction cites at least one chunk id"}},
		{name: "refusal with an id", in: rec("1 refusal", "Q: q", "A: a", "cite: x.y"), wantErr: []string{"line 4: a refusal question cites no chunk"}},
		{name: "refusal with - and an alternative", in: rec("1 refusal", "Q: q", "A: a", "cite: -", "  x.y"),
			wantErr: []string{"a refusal question cites no chunk"}},
		{name: "refusal with two cite lines", in: rec("1 refusal", "Q: q", "A: a", "cite: -", "cite: -"),
			wantErr: []string{"a refusal question cites no chunk"}},
		{name: "inline comment on a cite line", in: rec("1 rules", "Q: q", "A: a", "cite: x.y # TODO check"),
			wantErr: []string{"line 4: no comment on a cite line"}},
		{name: "id twice in a group", in: rec("1 rules", "Q: q", "A: a", "cite: x.y x.y"), wantErr: []string{`line 4: chunk id "x.y" listed twice`}},
		{name: "id twice across groups", in: rec("1 interaction", "Q: q", "A: a", "cite: x.y", "cite: a.b", "  x.y"),
			wantErr: []string{`line 5: chunk id "x.y" listed twice in the record`}},
		{
			name:    "a line over 64 KB is reported with its number",
			in:      rec("1 rules", "Q: q", "A: "+strings.Repeat("a", 70_000), "cite: x.y") + "\n" + rec("2 rules", "Q: "+strings.Repeat("b", 70_000), "A: a", "cite: x.y"),
			wantErr: []string{"line 7: the question is longer than 1000 bytes"},
		},
		{
			name: "every bad record is reported",
			in: rec("1 rules", "Q: q", "A: a", "cite: x.y") + "\n" +
				rec("2 nope", "Q: q2", "A: a", "cite: x.y") + "\n" +
				rec("3 rules", "Q: q3", "A: a") + "\n" +
				rec("4 rules", "Q: q4", "A:", "cite: x.y") + "\n" +
				rec("1 rules", "Q: q5", "A: a", "cite: x.y"),
			wantErr: []string{"line 6: category", "line 11: no cite: line", "line 17: the expected answer is empty",
				"line 20: question number 1 already used on line 1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tt.in))
			if len(tt.wantErr) > 0 {
				if err == nil {
					t.Fatalf("err = nil, want one containing %q", tt.wantErr)
				}
				for _, s := range tt.wantErr {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("err = %q, want it to contain %q", err, s)
					}
				}
				if got != nil {
					t.Errorf("questions = %v, want none on error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("questions =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestRead(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(bad, []byte("1 rules\nQ: q\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		file    string
		wantErr error
		wantMsg string
	}{
		{name: "missing file", file: filepath.Join(dir, "none.txt"), wantErr: os.ErrNotExist},
		{name: "malformed file names the file", file: bad, wantMsg: bad + ": line 1: no A: line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Read(tt.file)
			if err == nil {
				t.Fatal("err = nil, want an error")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("err = %v, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}

func TestCitationHelpers(t *testing.T) {
	two := Question{Expected: [][]string{{"melee", "teleport"}, {"misty"}}}
	one := Question{Expected: [][]string{{"fireball"}}}
	refusal := Question{Category: Refusal}
	tests := []struct {
		name        string
		q           Question
		ids         []string
		wantGroups  int
		wantCovered bool
		wantAny     bool
	}{
		{name: "two groups, both covered", q: two, ids: []string{"x", "teleport", "misty"}, wantGroups: 2, wantCovered: true, wantAny: true},
		{name: "two groups, first alternative", q: two, ids: []string{"misty", "melee"}, wantGroups: 2, wantCovered: true, wantAny: true},
		{name: "two groups, both alternatives count once", q: two, ids: []string{"melee", "teleport"}, wantGroups: 1, wantAny: true},
		{name: "two groups, one covered", q: two, ids: []string{"misty"}, wantGroups: 1, wantAny: true},
		{name: "two groups, none", q: two, ids: []string{"x", "y"}},
		{name: "two groups, no ids", q: two},
		{name: "one group, covered", q: one, ids: []string{"fireball"}, wantGroups: 1, wantCovered: true, wantAny: true},
		{name: "refusal is never covered", q: refusal, ids: []string{"fireball"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.GroupsCovered(tt.ids); got != tt.wantGroups {
				t.Errorf("GroupsCovered = %d, want %d", got, tt.wantGroups)
			}
			if got := tt.q.Covered(tt.ids); got != tt.wantCovered {
				t.Errorf("Covered = %v, want %v", got, tt.wantCovered)
			}
			if got := tt.q.AnyExpected(tt.ids); got != tt.wantAny {
				t.Errorf("AnyExpected = %v, want %v", got, tt.wantAny)
			}
		})
	}
}

func TestIDs(t *testing.T) {
	tests := []struct {
		name string
		q    Question
		want []string
	}{
		{name: "groups flattened in order", q: Question{Expected: [][]string{{"a", "b"}, {"c"}}}, want: []string{"a", "b", "c"}},
		{name: "refusal", q: Question{Category: Refusal}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.IDs(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("IDs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnknownIDs(t *testing.T) {
	qs := []Question{
		{Num: 1, Expected: [][]string{{"a.b"}, {"z.z"}}},
		{Num: 2, Expected: [][]string{{"c.d"}}},
		{Num: 3, Category: Refusal},
		{Num: 4, Expected: [][]string{{"z.z", "e.f"}}},
	}
	tests := []struct {
		name  string
		known map[string]bool
		want  []string
	}{
		{name: "all known", known: map[string]bool{"a.b": true, "c.d": true, "e.f": true, "z.z": true}},
		{name: "sorted, each once", known: map[string]bool{"c.d": true}, want: []string{"a.b", "e.f", "z.z"}},
		{name: "nothing known", known: map[string]bool{}, want: []string{"a.b", "c.d", "e.f", "z.z"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnknownIDs(qs, tt.known); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UnknownIDs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCount(t *testing.T) {
	tests := []struct {
		name string
		qs   []Question
		want map[Category]int
	}{
		{name: "none", want: map[Category]int{}},
		{name: "per category", qs: []Question{{Category: Rules}, {Category: Refusal}, {Category: Rules}},
			want: map[Category]int{Rules: 2, Refusal: 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Count(tt.qs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Count = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPlanned checks the plan against CLAUDE.md: 150 questions, one count per
// category.
func TestPlanned(t *testing.T) {
	total := 0
	for _, c := range Categories {
		n, ok := Planned[c]
		if !ok || n <= 0 {
			t.Errorf("Planned[%s] = %d, %v; want a positive count", c, n, ok)
		}
		total += n
	}
	if len(Planned) != len(Categories) || total != 150 {
		t.Errorf("Planned = %v, want 150 questions over the %d categories", Planned, len(Categories))
	}
}

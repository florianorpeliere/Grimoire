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

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []Question
		wantErr []string // substrings of the error, in any order
	}{
		{
			name: "comments, blank lines and spaces around fields",
			in: "# header\n\n" +
				"1 | rules | What does Fireball do? | Fire damage in a sphere. | spells.fireball\n" +
				"  # indented comment\n" +
				"2|interaction|Misty Step out of reach?|No attack.|a.b  c.d\n" +
				"40 | refusal | Eldritch Knight? | The SRD does not say. | -\n",
			want: []Question{
				{Num: 1, Category: Rules, Text: "What does Fireball do?", Answer: "Fire damage in a sphere.", Expected: []string{"spells.fireball"}},
				{Num: 2, Category: Interaction, Text: "Misty Step out of reach?", Answer: "No attack.", Expected: []string{"a.b", "c.d"}},
				{Num: 40, Category: Refusal, Text: "Eldritch Knight?", Answer: "The SRD does not say."},
			},
		},
		{name: "no record", in: "# nothing yet\n\n"},
		{name: "empty file", in: ""},
		{name: "too few fields", in: "1 | rules | q | a\n", wantErr: []string{"line 1: want 5 fields", "got 4"}},
		{name: "a | in the answer", in: "1 | rules | q | a | b | x.y\n", wantErr: []string{"got 6"}},
		{name: "number not an integer", in: "one | rules | q | a | x.y\n", wantErr: []string{`question number "one"`}},
		{name: "number zero", in: "0 | rules | q | a | x.y\n", wantErr: []string{`question number "0"`}},
		{name: "duplicate number", in: "1 | rules | q | a | x.y\n\n1 | rules | r | a | x.y\n",
			wantErr: []string{"line 3: question number 1 already used on line 1"}},
		{name: "duplicate question", in: "1 | rules | Can I  grapple? | a | x.y\n2 | interaction | can i grapple? | b | x.y\n",
			wantErr: []string{"line 2: same question as on line 1"}},
		{name: "unknown category", in: "1 | named | q | a | x.y\n",
			wantErr: []string{`line 1: category "named"`, "rules, interaction, refusal, campaign"}},
		{name: "category is case-sensitive", in: "1 | Rules | q | a | x.y\n", wantErr: []string{`category "Rules"`}},
		{name: "campaign is reserved", in: "1 | campaign | What do we know about the baron? | a | x.y\n",
			wantErr: []string{"category campaign is reserved"}},
		{name: "empty question", in: "1 | rules |  | a | x.y\n", wantErr: []string{"the question is empty"}},
		{name: "question too long", in: "1 | rules | " + strings.Repeat("a", search.MaxQueryBytes+1) + " | a | x.y\n",
			wantErr: []string{"longer than 1000 bytes"}},
		{name: "empty answer", in: "1 | rules | q |  | x.y\n", wantErr: []string{"the expected answer is empty"}},
		{name: "rules without ids", in: "1 | rules | q | a |  \n", wantErr: []string{"the expected chunk ids are empty"}},
		{name: "refusal without ids", in: "1 | refusal | q | a |\n", wantErr: []string{"the expected chunk ids are empty", "write -"}},
		{name: "rules with -", in: "1 | rules | q | a | -\n", wantErr: []string{"category rules needs at least one expected chunk id"}},
		{name: "interaction with - and an id", in: "1 | interaction | q | a | x.y -\n",
			wantErr: []string{"category interaction needs", "only for refusal"}},
		{name: "refusal with an id", in: "1 | refusal | q | a | x.y\n", wantErr: []string{"a refusal question lists no chunk id"}},
		{name: "refusal with - and an id", in: "1 | refusal | q | a | - x.y\n", wantErr: []string{"a refusal question lists no chunk id"}},
		{name: "id listed twice", in: "1 | rules | q | a | x.y x.y\n", wantErr: []string{`"x.y" listed twice`}},
		{
			name: "every bad record is reported",
			in: "1 | rules | q | a | x.y\n2 | nope | q2 | a | x.y\n3 | rules | q3 | a\n" +
				"4 | rules | q4 |  | x.y\n1 | rules | q5 | a | x.y\n",
			wantErr: []string{"line 2: category", "line 3: want 5 fields", "line 4: the expected answer is empty",
				"line 5: question number 1 already used on line 1"},
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
	if err := os.WriteFile(bad, []byte("1 | rules | q\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		file    string
		wantErr error
		wantMsg string
	}{
		{name: "missing file", file: filepath.Join(dir, "none.txt"), wantErr: os.ErrNotExist},
		{name: "malformed file names the file", file: bad, wantMsg: bad + ": line 1: want 5 fields"},
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

func TestUnknownIDs(t *testing.T) {
	qs := []Question{
		{Num: 1, Expected: []string{"a.b", "z.z"}},
		{Num: 2, Expected: []string{"c.d"}},
		{Num: 3, Category: Refusal},
		{Num: 4, Expected: []string{"z.z", "e.f"}},
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

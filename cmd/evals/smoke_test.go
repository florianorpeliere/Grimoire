package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/search"
	"github.com/florianorpeliere/grimoire/internal/srd"
)

// repoRoot is the repository root, seen from this package's directory.
const repoRoot = "../.."

func TestParseSmoke(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []question
		wantErr []string // substrings of the error, in any order
	}{
		{
			name: "comments, blank lines and spaces around fields",
			in: "# header\n\n" +
				"1 | named | exact | What does Fireball do? | spells.fireball\n" +
				"  # indented comment\n" +
				"2|named|paraphrase|Who can't see?|rules-glossary.blinded  other.id\n" +
				"21 | described | - | can I hit someone who walks away from me | rules-glossary.opportunity-attacks\n",
			want: []question{
				{Num: 1, Group: groupNamed, Type: typeExact, Text: "What does Fireball do?", Expected: []string{"spells.fireball"}},
				{Num: 2, Group: groupNamed, Type: typeParaphrase, Text: "Who can't see?", Expected: []string{"rules-glossary.blinded", "other.id"}},
				{Num: 21, Group: groupDescribed, Type: typeNone, Text: "can I hit someone who walks away from me", Expected: []string{"rules-glossary.opportunity-attacks"}},
			},
		},
		{name: "empty file", in: "# nothing\n\n", wantErr: []string{"no question"}},
		{name: "too few fields", in: "1 | named | exact | q\n", wantErr: []string{"line 1: want 5 fields", "got 4"}},
		{name: "a | in the question", in: "1 | named | exact | a | b | x.y\n", wantErr: []string{"got 6"}},
		{name: "number not an integer", in: "one | named | exact | q | x.y\n", wantErr: []string{`question number "one"`}},
		{name: "number zero", in: "0 | named | exact | q | x.y\n", wantErr: []string{`question number "0"`}},
		{name: "duplicate number", in: "1 | named | exact | q | x.y\n\n1 | named | exact | r | x.y\n",
			wantErr: []string{"line 3: question number 1 already used on line 1"}},
		{name: "unknown group", in: "1 | rules | exact | q | x.y\n", wantErr: []string{`group "rules"`}},
		{name: "named without a query type", in: "1 | named | - | q | x.y\n", wantErr: []string{`query type "-"`, "exact or paraphrase"}},
		{name: "described with a query type", in: "1 | described | exact | q | x.y\n", wantErr: []string{`query type "exact"`, `want "-"`}},
		{name: "empty question", in: "1 | named | exact |  | x.y\n", wantErr: []string{"the question is empty"}},
		{name: "question too long", in: "1 | named | exact | " + strings.Repeat("a", search.MaxQueryBytes+1) + " | x.y\n",
			wantErr: []string{"longer than 1000 bytes"}},
		{name: "no expected id", in: "1 | named | exact | q |  \n", wantErr: []string{"no expected chunk id"}},
		{name: "id listed twice", in: "1 | named | exact | q | x.y x.y\n", wantErr: []string{`"x.y" listed twice`}},
		{
			name:    "every bad line is reported",
			in:      "1 | named | exact | q | x.y\n2 | nope | exact | q | x.y\n3 | named | exact | q\n",
			wantErr: []string{"line 2: group", "line 3: want 5 fields"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSmoke(strings.NewReader(tt.in))
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
				t.Fatalf("parseSmoke: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("questions =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestUnknownIDs(t *testing.T) {
	qs := []question{
		{Num: 1, Expected: []string{"a.b", "z.z"}},
		{Num: 2, Expected: []string{"c.d"}},
		{Num: 3, Expected: []string{"z.z", "e.f"}},
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
			if got := unknownIDs(qs, tt.known); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("unknownIDs = %v, want %v", got, tt.want)
			}
		})
	}
}

// readCommitted parses the committed smoke set.
func readCommitted(t *testing.T) []question {
	t.Helper()
	qs, err := readSmoke(filepath.Join(repoRoot, defaultSmokeFile))
	if err != nil {
		t.Fatal(err)
	}
	return qs
}

// TestCommittedSmokeSet checks the shape of the committed file: 20 named
// questions (8 exact, 12 paraphrase) and 19 described ones, numbered 1 to 39.
func TestCommittedSmokeSet(t *testing.T) {
	qs := readCommitted(t)
	count := map[string]int{}
	for i, q := range qs {
		if q.Num != i+1 {
			t.Errorf("question %d is numbered %d, want %d", i+1, q.Num, i+1)
		}
		count[q.Group]++
		count[q.Group+"/"+q.Type]++
	}
	want := map[string]int{
		groupNamed: 20, groupNamed + "/" + typeExact: 8, groupNamed + "/" + typeParaphrase: 12,
		groupDescribed: 19, groupDescribed + "/" + typeNone: 19,
	}
	if !reflect.DeepEqual(count, want) {
		t.Errorf("counts = %v, want %v", count, want)
	}
}

// TestCommittedIDsInChunkerOutput checks every expected id of the committed
// file against the chunker output on the SRD Markdown downloaded by
// scripts/srd-download.sh. It is skipped when the file is absent (it is
// never committed); the runner checks the ids against the database anyway.
func TestCommittedIDsInChunkerOutput(t *testing.T) {
	if unknown := unknownIDs(readCommitted(t), chunkerIDs(t)); len(unknown) > 0 {
		t.Errorf("expected ids not in the chunker output: %v", unknown)
	}
}

// chunkerIDs returns the ids of the chunker output on the SRD Markdown
// downloaded by scripts/srd-download.sh. It skips the test when the file is
// absent (it is never committed).
func chunkerIDs(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(filepath.Join(repoRoot, srd.DefaultPath))
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("skipping: %s not found; run scripts/srd-download.sh to get the SRD Markdown", srd.DefaultPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	chunks, err := srd.Split(f)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	known := map[string]bool{}
	for _, c := range chunks {
		known[c.ID] = true
	}
	return known
}

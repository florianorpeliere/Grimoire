// Package evalset reads the step 2 eval set: hand-written questions, each with
// a category, the expected answer in our own words and the SRD chunks an answer
// should cite. The format is documented in evals/README.md. The package only
// parses and checks the file; it never searches or generates.
package evalset

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/florianorpeliere/grimoire/internal/search"
)

// DefaultFile is the eval set, relative to the repository root.
const DefaultFile = "evals/eval-set.txt"

// Category is the kind of a question.
type Category string

// Categories of the eval set.
const (
	Rules       Category = "rules"       // a simple rules question
	Interaction Category = "interaction" // an interaction between rules
	Refusal     Category = "refusal"     // out of the SRD or ambiguous: "the SRD does not say"
	Campaign    Category = "campaign"    // a question on the fictional campaign's notes
)

// Categories lists the categories in report order.
var Categories = []Category{Rules, Interaction, Refusal, Campaign}

// Planned is the number of questions planned per category (CLAUDE.md). A
// count below plan is not an error: questions are added in batches.
var Planned = map[Category]int{Rules: 80, Interaction: 40, Refusal: 15, Campaign: 15}

// None is written in the ids field of a question that cites no chunk.
const None = "-"

// Question is one record of the eval set.
type Question struct {
	Num      int
	Category Category
	Text     string   // the question, as asked
	Answer   string   // the expected answer, short, in our own words
	Expected []string // SRD chunk ids; citing any one of them counts. Empty for Refusal.
}

// fields is the number of "|"-separated fields of a record.
const fields = 5

// Read parses the eval set file. An error names the file.
func Read(file string) ([]Question, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	qs, err := Parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return qs, nil
}

// Parse reads an eval set (format in evals/README.md). It reports every
// malformed record with its line number, not only the first. A file with no
// record is valid and returns no question.
func Parse(r io.Reader) ([]Question, error) {
	var (
		qs    []Question
		errs  []error
		nums  = map[int]int{}    // question number -> line
		texts = map[string]int{} // normalized question -> line
	)
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		q, err := parseRecord(s)
		if err == nil {
			key := strings.ToLower(strings.Join(strings.Fields(q.Text), " "))
			if prev, dup := nums[q.Num]; dup {
				err = fmt.Errorf("question number %d already used on line %d", q.Num, prev)
			} else if prev, dup := texts[key]; dup {
				err = fmt.Errorf("same question as on line %d", prev)
			} else {
				nums[q.Num], texts[key] = line, line
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("line %d: %w", line, err))
			continue
		}
		qs = append(qs, q)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return qs, nil
}

func parseRecord(s string) (Question, error) {
	parts := strings.Split(s, "|")
	if len(parts) != fields {
		return Question{}, fmt.Errorf("want %d fields separated by |, got %d", fields, len(parts))
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 1 {
		return Question{}, fmt.Errorf("question number %q is not a positive integer", parts[0])
	}
	q := Question{Num: n, Category: Category(parts[1]), Text: parts[2], Answer: parts[3]}
	if !slices.Contains(Categories, q.Category) {
		return Question{}, fmt.Errorf("category %q: want one of %s", q.Category, categoryList())
	}
	if q.Category == Campaign {
		// Notes ingestion is step 4: note chunk ids, and so the citation of a
		// session, are not defined yet (open question on issue #28).
		return Question{}, errors.New("category campaign is reserved: campaign questions are written in step 4, once notes have chunk ids")
	}
	if q.Text == "" {
		return Question{}, errors.New("the question is empty")
	}
	if len(q.Text) > search.MaxQueryBytes {
		return Question{}, fmt.Errorf("the question is longer than %d bytes", search.MaxQueryBytes)
	}
	if q.Answer == "" {
		return Question{}, errors.New("the expected answer is empty")
	}
	ids := strings.Fields(parts[4])
	switch {
	case len(ids) == 0:
		return Question{}, fmt.Errorf("the expected chunk ids are empty: list them, or write %s for a refusal question", None)
	case q.Category == Refusal:
		if len(ids) != 1 || ids[0] != None {
			return Question{}, fmt.Errorf("a refusal question lists no chunk id: write %s", None)
		}
		return q, nil
	case slices.Contains(ids, None):
		return Question{}, fmt.Errorf("category %s needs at least one expected chunk id; %s is only for refusal questions", q.Category, None)
	}
	for i, id := range ids {
		if slices.Contains(ids[:i], id) {
			return Question{}, fmt.Errorf("expected chunk id %q listed twice", id)
		}
	}
	q.Expected = ids
	return q, nil
}

func categoryList() string {
	s := make([]string, len(Categories))
	for i, c := range Categories {
		s[i] = string(c)
	}
	return strings.Join(s, ", ")
}

// UnknownIDs returns the expected ids that are not in known, sorted, each
// once.
func UnknownIDs(qs []Question, known map[string]bool) []string {
	var out []string
	for _, q := range qs {
		for _, id := range q.Expected {
			if !known[id] && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	slices.Sort(out)
	return out
}

// Count returns the number of questions per category.
func Count(qs []Question) map[Category]int {
	n := map[Category]int{}
	for _, q := range qs {
		n[q.Category]++
	}
	return n
}

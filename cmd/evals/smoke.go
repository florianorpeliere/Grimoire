package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/florianorpeliere/grimoire/internal/search"
)

// Groups of the smoke set, in report order.
const (
	groupNamed     = "named"     // named rules
	groupDescribed = "described" // described situations, no title words
)

var groups = []string{groupNamed, groupDescribed}

// Query types of the named group, in report order. The described group has
// none, written "-".
const (
	typeExact      = "exact"
	typeParaphrase = "paraphrase"
	typeNone       = "-"
)

var namedTypes = []string{typeExact, typeParaphrase}

// question is one line of the smoke set file.
type question struct {
	Num      int
	Group    string
	Type     string // typeExact or typeParaphrase for groupNamed, typeNone otherwise
	Text     string
	Expected []string // chunk ids; any one in the top results is a hit
}

// fields is the number of "|"-separated fields of a question line.
const fields = 5

// parseSmoke reads a smoke set file (format in evals/README.md). It reports
// every malformed line, with its line number, not only the first.
func parseSmoke(r io.Reader) ([]question, error) {
	var (
		qs   []question
		errs []error
		nums = map[int]int{} // question number -> line
	)
	sc := bufio.NewScanner(r)
	for line := 1; sc.Scan(); line++ {
		s := strings.TrimSpace(sc.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		q, err := parseLine(s)
		if err == nil {
			if prev, dup := nums[q.Num]; dup {
				err = fmt.Errorf("question number %d already used on line %d", q.Num, prev)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("line %d: %w", line, err))
			continue
		}
		nums[q.Num] = line
		qs = append(qs, q)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	if len(qs) == 0 {
		return nil, errors.New("no question in the file")
	}
	return qs, nil
}

func parseLine(s string) (question, error) {
	parts := strings.Split(s, "|")
	if len(parts) != fields {
		return question{}, fmt.Errorf("want %d fields separated by |, got %d", fields, len(parts))
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	var q question
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 1 {
		return question{}, fmt.Errorf("question number %q is not a positive integer", parts[0])
	}
	q.Num = n
	q.Group, q.Type, q.Text = parts[1], parts[2], parts[3]
	switch q.Group {
	case groupNamed:
		if !slices.Contains(namedTypes, q.Type) {
			return question{}, fmt.Errorf("query type %q: want %s for group %s", q.Type, strings.Join(namedTypes, " or "), groupNamed)
		}
	case groupDescribed:
		if q.Type != typeNone {
			return question{}, fmt.Errorf("query type %q: want %q for group %s", q.Type, typeNone, groupDescribed)
		}
	default:
		return question{}, fmt.Errorf("group %q: want %s", q.Group, strings.Join(groups, " or "))
	}
	if q.Text == "" {
		return question{}, errors.New("the question is empty")
	}
	if len(q.Text) > search.MaxQueryBytes {
		return question{}, fmt.Errorf("the question is longer than %d bytes", search.MaxQueryBytes)
	}
	q.Expected = strings.Fields(parts[4])
	if len(q.Expected) == 0 {
		return question{}, errors.New("no expected chunk id")
	}
	for i, id := range q.Expected {
		if slices.Contains(q.Expected[:i], id) {
			return question{}, fmt.Errorf("expected chunk id %q listed twice", id)
		}
	}
	return q, nil
}

// unknownIDs returns the expected ids that are not in known, sorted, each
// once.
func unknownIDs(qs []question, known map[string]bool) []string {
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

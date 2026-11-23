// Package evalset reads the step 2 eval set: hand-written questions, each with
// a category, the expected answer in our own words and the SRD chunks an answer
// should cite. The format is documented in evals/README.md. The package only
// parses and checks the file; it never searches or generates.
package evalset

import (
	"bytes"
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

// None is the only value of the cite line of a refusal question.
const None = "-"

// Keys of the lines of a record.
const (
	keyQuestion = "Q:"
	keyAnswer   = "A:"
	keyCite     = "cite:"
)

// Question is one record of the eval set.
type Question struct {
	Num      int
	Line     int // line of the record's header in the file
	Category Category
	Text     string // the question, as asked
	Answer   string // the expected answer, short, in our own words
	// Expected holds the citation groups: one per cite line, each listing
	// alternative SRD chunk ids. A correct citation has at least one id of
	// every group. Empty for Refusal.
	Expected [][]string
}

// IDs returns every expected chunk id, all groups together, in file order.
func (q Question) IDs() []string {
	var ids []string
	for _, g := range q.Expected {
		ids = append(ids, g...)
	}
	return ids
}

// GroupsCovered returns how many citation groups have at least one of their
// ids in ids.
func (q Question) GroupsCovered(ids []string) int {
	n := 0
	for _, g := range q.Expected {
		if slices.ContainsFunc(g, func(id string) bool { return slices.Contains(ids, id) }) {
			n++
		}
	}
	return n
}

// Covered reports whether ids hold at least one id of every citation group:
// a correct citation. It is false for a question with no group (Refusal),
// which has nothing to cite.
func (q Question) Covered(ids []string) bool {
	return len(q.Expected) > 0 && q.GroupsCovered(ids) == len(q.Expected)
}

// AnyExpected reports whether ids hold at least one expected id, of any
// group.
func (q Question) AnyExpected(ids []string) bool {
	return q.GroupsCovered(ids) > 0
}

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

// line is a non-comment line of the file, with its number.
type line struct {
	num  int
	text string // without the line ending and trailing spaces
}

// Parse reads an eval set (format in evals/README.md). It reports every
// malformed record with the line number of the problem, not only the first
// record. A file with no record is valid and returns no question.
func Parse(r io.Reader) ([]Question, error) {
	// The whole file is read at once, so that no line is too long to be
	// reported with its number.
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\uFEFF"))
	var (
		qs     []Question
		errs   []error
		nums   = map[int]int{}    // question number -> line
		texts  = map[string]int{} // normalized question -> line
		record []line
	)
	flush := func() {
		if len(record) == 0 {
			return
		}
		q, err := parseRecord(record)
		record = nil
		if err == nil {
			key := strings.ToLower(strings.Join(strings.Fields(q.Text), " "))
			if prev, dup := nums[q.Num]; dup {
				err = lineError(q.Line, fmt.Errorf("question number %d already used on line %d", q.Num, prev))
			} else if prev, dup := texts[key]; dup {
				err = lineError(q.Line, fmt.Errorf("same question as on line %d", prev))
			} else {
				nums[q.Num], texts[key] = q.Line, q.Line
			}
		}
		if err != nil {
			errs = append(errs, err)
			return
		}
		qs = append(qs, q)
	}
	for i, s := range strings.Split(string(data), "\n") {
		s = strings.TrimRight(s, " \t\r")
		switch t := strings.TrimSpace(s); {
		case t == "":
			flush()
		case strings.HasPrefix(t, "#"):
			// A comment line is ignored and does not end the record.
		default:
			record = append(record, line{num: i + 1, text: s})
		}
	}
	flush()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return qs, nil
}

func lineError(n int, err error) error {
	return fmt.Errorf("line %d: %w", n, err)
}

func indented(s string) bool {
	return s != "" && (s[0] == ' ' || s[0] == '\t')
}

// parseRecord parses the lines of one record, blank lines and comments
// removed. The error gives the line of the first problem found.
func parseRecord(lines []line) (Question, error) {
	head := lines[0]
	if indented(head.text) {
		return Question{}, lineError(head.num, errors.New("a record starts with an unindented header: <number> <category>"))
	}
	q, err := parseHeader(head.text)
	if err != nil {
		return Question{}, lineError(head.num, err)
	}
	q.Line = head.num

	var (
		key              string // key of the line being continued
		text, answer     []string
		textAt, answerAt int // lines of the Q: and A: lines
		cites            [][]string
		citeAt           []int // line of each cite line
	)
	for _, l := range lines[1:] {
		if indented(l.text) {
			v := strings.TrimSpace(l.text)
			switch key {
			case keyQuestion:
				text = append(text, v)
			case keyAnswer:
				answer = append(answer, v)
			case keyCite:
				cites[len(cites)-1] = append(cites[len(cites)-1], strings.Fields(v)...)
			default:
				return Question{}, lineError(l.num, errors.New("an indented line continues a Q:, A: or cite: line, and there is none above it"))
			}
			continue
		}
		k, v, ok := splitKey(l.text)
		switch {
		case !ok:
			return Question{}, lineError(l.num, fmt.Errorf("want a line starting with %s, %s or %s (records are separated by a blank line)", keyQuestion, keyAnswer, keyCite))
		case k == keyQuestion && textAt > 0:
			return Question{}, lineError(l.num, fmt.Errorf("second %s line, the first is on line %d", keyQuestion, textAt))
		case k == keyAnswer && answerAt > 0:
			return Question{}, lineError(l.num, fmt.Errorf("second %s line, the first is on line %d", keyAnswer, answerAt))
		}
		key = k
		switch k {
		case keyQuestion:
			textAt, text = l.num, []string{v}
		case keyAnswer:
			answerAt, answer = l.num, []string{v}
		case keyCite:
			cites, citeAt = append(cites, strings.Fields(v)), append(citeAt, l.num)
		}
	}

	q.Text = strings.TrimSpace(strings.Join(text, " "))
	q.Answer = strings.TrimSpace(strings.Join(answer, " "))
	switch {
	case textAt == 0:
		return Question{}, lineError(head.num, fmt.Errorf("no %s line", keyQuestion))
	case q.Text == "":
		return Question{}, lineError(textAt, errors.New("the question is empty"))
	case len(q.Text) > search.MaxQueryBytes:
		return Question{}, lineError(textAt, fmt.Errorf("the question is longer than %d bytes", search.MaxQueryBytes))
	case answerAt == 0:
		return Question{}, lineError(head.num, fmt.Errorf("no %s line", keyAnswer))
	case q.Answer == "":
		return Question{}, lineError(answerAt, errors.New("the expected answer is empty"))
	case len(cites) == 0:
		return Question{}, lineError(head.num, fmt.Errorf("no %s line: list the chunk ids, or write %s %s for a refusal question", keyCite, keyCite, None))
	}
	if q.Category == Refusal {
		if len(cites) != 1 || !slices.Equal(cites[0], []string{None}) {
			return Question{}, lineError(citeAt[0], fmt.Errorf("a refusal question cites no chunk: write one line %s %s", keyCite, None))
		}
		return q, nil
	}
	var seen []string
	for i, g := range cites {
		if len(g) == 0 {
			return Question{}, lineError(citeAt[i], fmt.Errorf("the %s line lists no chunk id", keyCite))
		}
		for _, id := range g {
			switch {
			case id == None:
				return Question{}, lineError(citeAt[i], fmt.Errorf("%s is only for refusal questions; category %s cites at least one chunk id", None, q.Category))
			case strings.HasPrefix(id, "#"):
				return Question{}, lineError(citeAt[i], errors.New("no comment on a cite line: a comment is a whole line starting with #"))
			case slices.Contains(seen, id):
				return Question{}, lineError(citeAt[i], fmt.Errorf("chunk id %q listed twice in the record", id))
			}
			seen = append(seen, id)
		}
	}
	q.Expected = cites
	return q, nil
}

// parseHeader parses "<number> <category>".
func parseHeader(s string) (Question, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return Question{}, fmt.Errorf("header %q: want <number> <category>", s)
	}
	n, err := strconv.Atoi(f[0])
	if err != nil || n < 1 {
		return Question{}, fmt.Errorf("question number %q is not a positive integer", f[0])
	}
	q := Question{Num: n, Category: Category(f[1])}
	if !slices.Contains(Categories, q.Category) {
		return Question{}, fmt.Errorf("category %q: want one of %s", q.Category, categoryList())
	}
	if q.Category == Campaign {
		// Notes ingestion is step 4: note chunk ids, and so the citation of a
		// session, are not defined yet (decided on issue #28).
		return Question{}, errors.New("category campaign is reserved: campaign questions are written in step 4, once notes have chunk ids")
	}
	return q, nil
}

// splitKey splits a "Q: text" line into its key and value.
func splitKey(s string) (key, value string, ok bool) {
	for _, k := range []string{keyQuestion, keyAnswer, keyCite} {
		if v, found := strings.CutPrefix(s, k); found {
			return k, strings.TrimSpace(v), true
		}
	}
	return "", "", false
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
		for _, id := range q.IDs() {
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

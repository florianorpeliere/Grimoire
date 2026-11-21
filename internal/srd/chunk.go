// Package srd splits the SRD 5.2.1 Markdown file into chunks: one per rule
// section, spell, glossary entry (including conditions), magic item, feat,
// class section, background, species, item of equipment and monster.
//
// The split follows the document headings, chapter by chapter, as described
// by the split table in table.go. It is a pure function of the input: the
// same file always gives the same chunks, ids and order.
//
// # Ids
//
// A chunk id is the slug of its short citation path: each path segment is
// lower-cased, apostrophes are removed, every other run of characters that are
// not ASCII letters or digits becomes one "-", leading and trailing "-" are
// trimmed, and the segments are joined with ".". For example the path
// "Playing the Game > Actions > Reactions" has the id
// "playing-the-game.actions.reactions", and "Spells > Fireball" has the id
// "spells.fireball". A segment holds only [a-z0-9-], so "." cannot occur
// inside one, and an id needs no encoding in a URL path. Ids are unique in
// the corpus: a collision is an error.
package srd

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode/utf8"
)

// DefaultPath is where scripts/srd-download.sh writes the SRD Markdown,
// relative to the repository root.
const DefaultPath = "data/srd/SRD_CC_v5.2.1.md"

// Version is the SRD version of the pinned source, stored with each chunk.
const Version = "5.2.1"

var (
	// ErrUnknownChapter is returned for a chapter (h1) title missing from the split table.
	ErrUnknownChapter = errors.New("chapter title not in the split table")
	// ErrDuplicateID is returned when two chunks get the same id.
	ErrDuplicateID = errors.New("duplicate chunk id")
	// ErrTextBeforeChapter is returned for text before the first chapter heading.
	ErrTextBeforeChapter = errors.New("text before the first chapter heading")
	// ErrEmptySlug is returned when a path segment has no letter or digit.
	ErrEmptySlug = errors.New("heading gives an empty id segment")
	// ErrUnmatchedPattern is returned when an entity or container pattern of
	// the split table matches no heading of its chapter, e.g. after a heading
	// was renamed in a new pin of the source.
	ErrUnmatchedPattern = errors.New("split table pattern matches no heading")
)

// Chunk is one passage of the SRD.
type Chunk struct {
	ID       string   // slug of Path, see the package documentation
	Path     []string // short citation path: chapter, meaningful headings, own heading
	FullPath []string // every heading from the chapter down to the chunk's own, as written in the source
	Title    string   // the chunk's own heading, without tag or Markdown escapes
	Kind     Kind
	Text     string // the text under the heading, deeper headings included, trimmed of blank lines
	Hash     string // hex SHA-256 of Text
	// StartLine is the 1-based line of the chunk's heading; EndLine is the
	// last line of its text. Both refer to the source file.
	StartLine, EndLine int
}

// PathString returns the short citation path, e.g. "Spells > Fireball".
func (c Chunk) PathString() string { return strings.Join(c.Path, " > ") }

// EstimateTokens returns an estimate of the number of tokens of the chunk
// text: its number of characters (Unicode code points) divided by 4, rounded
// up. It is not a tokenizer count.
func (c Chunk) EstimateTokens() int { return (utf8.RuneCountInString(c.Text) + 3) / 4 }

// conditionTag marks a Rules Glossary entry that is a condition (only in a
// chapter whose table entry sets Conditions). The source escapes the brackets.
const conditionTag = `\[Condition\]`

// heading is one heading of the source.
type heading struct {
	level int
	raw   string // text after the "#" marks, as written
	title string // raw without Markdown escapes (and without the condition tag, for a condition)
}

// ReadFile splits the SRD Markdown file at path.
func ReadFile(path string) ([]Chunk, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	chunks, err := Split(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return chunks, nil
}

// Split reads the SRD Markdown and returns its chunks in document order.
func Split(r io.Reader) ([]Chunk, error) { return split(r, chapters) }

// split is Split with the split table as a parameter, so that tests can use
// their own table without changing the package-level one.
func split(r io.Reader, table map[string]chapter) ([]Chunk, error) {
	s := splitter{table: table, seen: map[string][]string{}, used: map[string]bool{}}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		if err := s.line(n, sc.Text()); err != nil {
			return nil, err
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if err := s.flush(); err != nil {
		return nil, err
	}
	if err := s.checkPatterns(); err != nil {
		return nil, err
	}
	return s.chunks, nil
}

// checkPatterns fails when an entity rule or a container of a chapter found in
// the input matched no heading of that chapter. It reports every such pattern.
func (s *splitter) checkPatterns() error {
	var errs []error
	for _, title := range s.chapterTitles {
		ch := s.table[title]
		for i, r := range ch.Entities {
			if !s.used[patternKey(title, "entity", i)] {
				errs = append(errs, fmt.Errorf("%w: chapter %q, entity rule (h%d under %q)", ErrUnmatchedPattern,
					title, r.level(), strings.Join(append([]string{title}, r.Under...), " > ")))
			}
		}
		for i, c := range ch.Containers {
			if !s.used[patternKey(title, "container", i)] {
				errs = append(errs, fmt.Errorf("%w: chapter %q, container %q", ErrUnmatchedPattern,
					title, strings.Join(append([]string{title}, c...), " > ")))
			}
		}
	}
	return errors.Join(errs...)
}

func patternKey(chapter, what string, i int) string {
	return fmt.Sprintf("%s\x00%s\x00%d", chapter, what, i)
}

type splitter struct {
	table   map[string]chapter // the split table, keyed on the chapter title
	chunks  []Chunk
	seen    map[string][]string // id -> full path, to report collisions
	stack   [7]*heading         // open headings by level (1-6)
	chapter chapter
	cur     *open // chunk being read, nil before the first chapter

	chapterTitle  string          // title of the current chapter
	chapterTitles []string        // chapters seen, in order, without repeats
	used          map[string]bool // patternKey of the table patterns that matched a heading
}

// open is a chunk whose text is still being read.
type open struct {
	head      *heading
	kind      Kind
	path      []string
	fullPath  []string
	startLine int
	lines     []string
}

func (s *splitter) line(n int, text string) error {
	h, ok := parseHeading(text)
	if !ok {
		if s.cur == nil {
			if strings.TrimSpace(text) != "" {
				return fmt.Errorf("line %d: %w", n, ErrTextBeforeChapter)
			}
			return nil
		}
		s.cur.lines = append(s.cur.lines, text)
		return nil
	}

	if s.cur == nil && h.level > 1 && s.stack[1] == nil {
		return fmt.Errorf("line %d: %w", n, ErrTextBeforeChapter)
	}
	if h.level == 1 {
		ch, ok := s.table[h.title]
		if !ok {
			return fmt.Errorf("line %d: %w: %q", n, ErrUnknownChapter, h.title)
		}
		s.chapter = ch
		if !slices.Contains(s.chapterTitles, h.title) {
			s.chapterTitles = append(s.chapterTitles, h.title)
		}
		s.chapterTitle = h.title
	}
	s.stack[h.level] = &h
	for l := h.level + 1; l < len(s.stack); l++ {
		s.stack[l] = nil
	}
	for i, c := range s.chapter.Containers {
		if len(c) == h.level-1 && s.matches(c) {
			s.used[patternKey(s.chapterTitle, "container", i)] = true
		}
	}

	kind, entity := s.entityKind(h.level)
	if entity && s.chapter.Conditions && strings.HasSuffix(h.raw, conditionTag) {
		// The tag is kept out of the title, hence out of the short path.
		h.title = unescape(strings.TrimSpace(strings.TrimSuffix(h.raw, conditionTag)))
		kind = KindCondition
	}
	if !entity && h.level > s.chapter.Split {
		// A deeper heading stays inside the current chunk.
		s.cur.lines = append(s.cur.lines, text)
		return nil
	}
	if err := s.flush(); err != nil {
		return err
	}
	if !entity {
		kind = s.chapter.Kind
	}
	s.cur = &open{head: &h, kind: kind, path: s.shortPath(h.level), fullPath: s.fullPath(h.level), startLine: n}
	return nil
}

// entityKind reports whether the heading at level, now on top of the stack,
// is an entity heading of the current chapter, and its kind.
func (s *splitter) entityKind(level int) (Kind, bool) {
	for i, r := range s.chapter.Entities {
		if r.level() == level && s.matches(r.Under) {
			s.used[patternKey(s.chapterTitle, "entity", i)] = true
			return r.Kind, true
		}
	}
	return "", false
}

// matches reports whether the open headings from level 2 down match pattern.
func (s *splitter) matches(pattern []string) bool {
	for i, want := range pattern {
		h := s.stack[i+2]
		if h == nil || (want != wildcard && want != h.title) {
			return false
		}
	}
	return true
}

func (s *splitter) isContainer(level int) bool {
	for _, c := range s.chapter.Containers {
		if len(c) == level-1 && s.matches(c) {
			return true
		}
	}
	return false
}

func (s *splitter) shortPath(level int) []string {
	var p []string
	for l := 1; l <= level; l++ {
		h := s.stack[l]
		if h == nil || (l > 1 && l < level && s.isContainer(l)) {
			continue
		}
		p = append(p, h.title)
	}
	return p
}

func (s *splitter) fullPath(level int) []string {
	var p []string
	for l := 1; l <= level; l++ {
		if h := s.stack[l]; h != nil {
			p = append(p, h.raw)
		}
	}
	return p
}

// flush turns the open chunk into a Chunk, unless it has no text.
func (s *splitter) flush() error {
	o := s.cur
	s.cur = nil
	if o == nil {
		return nil
	}
	first, last := 0, len(o.lines)
	for first < last && strings.TrimSpace(o.lines[first]) == "" {
		first++
	}
	for last > first && strings.TrimSpace(o.lines[last-1]) == "" {
		last--
	}
	if first == last {
		return nil
	}
	text := strings.Join(o.lines[first:last], "\n")
	id, err := slugPath(o.path)
	if err != nil {
		return fmt.Errorf("line %d: %w", o.startLine, err)
	}
	if prev, dup := s.seen[id]; dup {
		return fmt.Errorf("%w %q: %q and %q", ErrDuplicateID, id,
			strings.Join(prev, " > "), strings.Join(o.fullPath, " > "))
	}
	s.seen[id] = o.fullPath
	sum := sha256.Sum256([]byte(text))
	s.chunks = append(s.chunks, Chunk{
		ID:        id,
		Path:      o.path,
		FullPath:  o.fullPath,
		Title:     o.head.title,
		Kind:      o.kind,
		Text:      text,
		Hash:      hex.EncodeToString(sum[:]),
		StartLine: o.startLine,
		EndLine:   o.startLine + last,
	})
	return nil
}

// parseHeading parses an ATX heading line ("## Title").
func parseHeading(line string) (heading, bool) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
		return heading{}, false
	}
	raw := strings.TrimSpace(line[level:])
	return heading{level: level, raw: raw, title: unescape(raw)}, true
}

// unescape removes Markdown backslash escapes before ASCII punctuation.
func unescape(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) && isASCIIPunct(s[i+1]) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func isASCIIPunct(c byte) bool {
	return c >= '!' && c <= '/' || c >= ':' && c <= '@' || c >= '[' && c <= '`' || c >= '{' && c <= '~'
}

// slugPath returns the id of a short path (see the package documentation).
func slugPath(path []string) (string, error) {
	segs := make([]string, len(path))
	for i, p := range path {
		segs[i] = slug(p)
		if segs[i] == "" {
			return "", fmt.Errorf("%w: %q", ErrEmptySlug, p)
		}
	}
	return strings.Join(segs, "."), nil
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '\'' || r == '’':
			continue
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	return b.String()
}

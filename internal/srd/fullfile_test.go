package srd

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// repoRoot is the repository root, seen from this package's directory.
const repoRoot = "../.."

// TestFullFile splits the pinned SRD file downloaded by scripts/srd-download.sh.
// It is skipped when the file is absent (it is never committed).
//
// The expected spell, monster and condition counts are those printed by
// scripts/srd-validate.sh for the pinned file: 339 headings under
// "## Spell Descriptions"; 330 stat blocks (235 in Monsters A-Z, 95 in
// Animals); 15 headings ending "[Condition]".
func TestFullFile(t *testing.T) {
	data := readPinnedFile(t)
	chunks, err := Split(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	// Every kind is counted, so that an entity heading renamed, moved or at
	// an unexpected level cannot silently become (part of) a rule chunk.
	// spell, monster and condition are the scripts/srd-validate.sh counts;
	// the others were printed by "ingest srd -dry-run" on this file. When the
	// pin changes, check them against an independent count before changing
	// them: see "Changing the source pin" in docs/srd-chunking.md.
	if got, want := len(chunks), 1508; got != want {
		t.Errorf("chunks = %d, want %d", got, want)
	}
	perKind := map[Kind]int{}
	for _, c := range chunks {
		perKind[c.Kind]++
	}
	wantPerKind := map[Kind]int{
		KindBackground: 4,
		KindClass:      59,
		KindCondition:  15,
		KindEquipment:  106,
		KindFeat:       17,
		KindGlossary:   140,
		KindLegal:      1,
		KindMagicItem:  258,
		KindMonster:    330,
		KindRule:       230,
		KindSpecies:    9,
		KindSpell:      339,
	}
	for kind := range perKind {
		if _, ok := wantPerKind[kind]; !ok {
			t.Errorf("unexpected kind %q (%d chunks)", kind, perKind[kind])
		}
	}
	for kind, want := range wantPerKind {
		if got := perKind[kind]; got != want {
			t.Errorf("%s chunks = %d, want %d", kind, got, want)
		}
	}

	// The citation examples required by the story, on the real file.
	byID := map[string]Chunk{}
	for _, c := range chunks {
		byID[c.ID] = c
	}
	examples := []struct {
		id   string
		kind Kind
		path string
	}{
		{"spells.fireball", KindSpell, "Spells > Fireball"},
		{"rules-glossary.prone", KindCondition, "Rules Glossary > Prone"},
		{"monsters-a-z.goblin-warrior", KindMonster, "Monsters A-Z > Goblin Warrior"},
		{"animals.wolf", KindMonster, "Animals > Wolf"},
		{"playing-the-game.actions.reactions", KindRule, "Playing the Game > Actions > Reactions"},
		{"classes.barbarian.core-barbarian-traits", KindClass, "Classes > Barbarian > Core Barbarian Traits"},
		{"equipment.adventuring-gear.alchemists-fire", KindEquipment, "Equipment > Adventuring Gear > Alchemist's Fire"},
	}
	for _, ex := range examples {
		c, ok := byID[ex.id]
		if !ok {
			t.Errorf("no chunk with id %q", ex.id)
			continue
		}
		if c.Kind != ex.kind || c.PathString() != ex.path {
			t.Errorf("%s: kind %q, path %q; want %q, %q", ex.id, c.Kind, c.PathString(), ex.kind, ex.path)
		}
	}

	checkCoverage(t, string(data), chunks)
}

// srdFixtures are the fixtures of chunk_test.go presented as SRD excerpts.
// A fixture that is not SRD text must not be listed here, and must say so.
var srdFixtures = map[string]string{
	"spellFixture":        spellFixture,
	"spellRulesFixture":   spellRulesFixture,
	"monsterFixture":      monsterFixture,
	"animalFixture":       animalFixture,
	"conditionFixture":    conditionFixture,
	"featFixture":         featFixture,
	"rulesFixture":        rulesFixture,
	"chapterIntroFixture": chapterIntroFixture,
	"tableFixture":        tableFixture,
	"classFixture":        classFixture,
	"originsFixture":      originsFixture,
	"legalFixture":        legalFixture,
}

// TestFixturesAreSRDText checks that every non-blank line of each SRD fixture
// is a whole line of the pinned SRD file, as written, so that no test presents
// invented or altered rules text as SRD text. It is skipped when the file is
// absent.
func TestFixturesAreSRDText(t *testing.T) {
	data := readPinnedFile(t)
	lines := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		lines[l] = true
	}
	tests := make([]string, 0, len(srdFixtures))
	for name := range srdFixtures {
		tests = append(tests, name)
	}
	slices.Sort(tests)
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			// This proves each line exists in the file, not that it sits under the right heading.
			for i, l := range strings.Split(srdFixtures[name], "\n") {
				if strings.TrimSpace(l) != "" && !lines[l] {
					t.Errorf("fixture line %d is not a line of %s: %q", i+1, DefaultPath, l)
				}
			}
		})
	}
}

// readPinnedFile returns the SRD file downloaded by scripts/srd-download.sh.
// It skips the test when the file is absent, and fails it when the file is
// not the pinned one.
func readPinnedFile(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, DefaultPath))
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("skipping: %s not found; run scripts/srd-download.sh to get the SRD Markdown", DefaultPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	pin := pinnedSHA256(t)
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != pin {
		t.Fatalf("%s has SHA-256 %s, not the pinned %s; run scripts/srd-download.sh again", DefaultPath, got, pin)
	}
	return data
}

// pinnedSHA256 reads SRD_MARKDOWN_SHA256 from scripts/srd-source.env.
func pinnedSHA256(t *testing.T) string {
	t.Helper()
	f, err := os.Open(filepath.Join(repoRoot, "scripts", "srd-source.env"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "SRD_MARKDOWN_SHA256="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	t.Fatal("SRD_MARKDOWN_SHA256 not found in scripts/srd-source.env")
	return ""
}

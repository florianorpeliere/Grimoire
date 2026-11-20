package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/florianorpeliere/grimoire/internal/srd"
)

// The fixture is a short excerpt of the SRD 5.2.1 (CC-BY-4.0, see ATTRIBUTION.md):
// every line is a whole line of the pinned SRD file, as written; some lines
// are left out. TestDryRunFixtureIsSRDText checks it against that file.
const dryRunFixture = `# Spells

## Spell Descriptions

### Fireball

_Level 3 Evocation (Sorcerer, Wizard)_

## Casting Spells

### Spell Level

Every spell has a level from 0 to 9, which is indicated in a spell's description. A spell's level is an indicator of how powerful it is. Cantrips — simple spells that can be cast almost by rote — are level 0. The rules for each spellcasting class say when its members gain access to spells of certain levels.
`

// TestDryRunFixtureIsSRDText checks that every non-blank line of dryRunFixture
// is a whole line of the SRD file downloaded by scripts/srd-download.sh. It is
// skipped when the file is absent; internal/srd's TestFullFile checks that the
// file is the pinned one.
func TestDryRunFixtureIsSRDText(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", srd.DefaultPath))
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("skipping: %s not found; run scripts/srd-download.sh to get the SRD Markdown", srd.DefaultPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]bool{}
	for _, l := range strings.Split(string(data), "\n") {
		lines[l] = true
	}
	for i, l := range strings.Split(dryRunFixture, "\n") {
		if strings.TrimSpace(l) != "" && !lines[l] {
			t.Errorf("fixture line %d is not a line of %s: %q", i+1, srd.DefaultPath, l)
		}
	}
}

func TestSRDDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "srd.md")
	if err := os.WriteFile(path, []byte(dryRunFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	// No DATABASE_URL: the dry run must not need the database.
	if err := run(context.Background(), []string{"srd", "-dry-run", path}, func(string) string { return "" }, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	tests := []struct{ name, line string }{
		{"total", "chunks: 2\n"},
		{"rule count", "  rule             1\n"},
		{"spell count", "  spell            1\n"},
		{"size summary", "  min 10, median 10, p95 77, max 77\n"},
		{"first bucket", "      1-100       2\n"},
		{"large chunks", "chunks above 4000 estimated tokens: 0\n"},
		{"spell path", "  spells.fireball\tspell\t10\tSpells > Fireball\n"},
		{"rule path", "  spells.casting-spells.spell-level\trule\t77\tSpells > Casting Spells > Spell Level\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(out.String(), tt.line) {
				t.Errorf("report has no line %q:\n%s", tt.line, out.String())
			}
		})
	}
}

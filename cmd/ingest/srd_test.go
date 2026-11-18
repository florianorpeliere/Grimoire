package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture is a short excerpt of the SRD 5.2.1 (CC-BY-4.0, see ATTRIBUTION.md).
const dryRunFixture = `# Spells

## Spell Descriptions

### Fireball

_Level 3 Evocation (Sorcerer, Wizard)_

## Casting Spells

### Spell Level

Every spell has a level from 0 to 9.
`

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
		{"size summary", "  min 9, median 9, p95 10, max 10\n"},
		{"first bucket", "      1-100       2\n"},
		{"large chunks", "chunks above 4000 estimated tokens: 0\n"},
		{"spell path", "  spells/fireball\tspell\t10\tSpells > Fireball\n"},
		{"rule path", "  spells/casting-spells/spell-level\trule\t9\tSpells > Casting Spells > Spell Level\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !strings.Contains(out.String(), tt.line) {
				t.Errorf("report has no line %q:\n%s", tt.line, out.String())
			}
		})
	}
}

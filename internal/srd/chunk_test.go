package srd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// The fixtures below are short excerpts of the SRD 5.2.1 in the form of the
// single Markdown file, some sentences cut. The SRD 5.2.1 is by Wizards of
// the Coast LLC, licensed under CC-BY-4.0: see ATTRIBUTION.md.

const spellFixture = `# Spells

## Spell Descriptions

### Fireball

_Level 3 Evocation (Sorcerer, Wizard)_

- **Casting Time:** Action
- **Range:** 150 feet

_**Using a Higher-Level Spell Slot.**_ The damage increases by 1d6 for each spell slot level above 3.
`

// "Spell Level" sits at the split level but not under "Spell Descriptions".
const spellRulesFixture = `# Spells

## Spell Descriptions

### Light

_Evocation Cantrip (Bard, Cleric, Sorcerer, Wizard)_

## Casting Spells

### Spell Level

Every spell has a level from 0 to 9.

#### Spell Slots

Spellcasting is taxing.
`

const monsterFixture = `# Monsters A-Z

## Goblins

### Goblin Warrior

_Small Fey (Goblinoid), Chaotic Neutral_

- **AC** 15

#### Actions

_**Scimitar.**_ _Melee Attack Roll:_ +4, reach 5 ft.

#### Bonus Actions

_**Nimble Escape.**_ The goblin takes the Disengage or Hide action.
`

const animalFixture = `# Animals

## Wolf

_Medium Beast, Unaligned_

- **AC** 12

### Traits

_**Pack Tactics.**_ The wolf has Advantage on attack rolls.
`

const conditionFixture = `# Rules Glossary

## Rules Definitions

### Prone \[Condition\]

While you have the Prone condition, you experience the following effects.

### Ready \[Action\]

You take the Ready action to wait for a particular circumstance before you act.
`

const featFixture = `# Feats

## Feat Descriptions

The feats that follow are organized by category.

### Origin Feats

#### Alert

_Origin Feat_

You gain the following benefits.
`

const rulesFixture = `# Playing the Game

## D20 Tests

When the outcome of an action is uncertain, the game uses a d20 roll to determine success or failure.

### Ability Checks

An ability check represents a creature using talent and training.

#### Ability Modifier

An ability check is named for the ability modifier it uses.

## Actions

### Reactions

Certain special abilities, spells, and situations allow you to take a special action called a Reaction.
`

const chapterIntroFixture = `# Magic Items

Magic items are gleaned from the hoards of felled monsters.

## Magic Items A-Z

### Bag of Holding

_Wondrous Item, Uncommon_
`

const tableFixture = `# Equipment

## Coins

A coin weighs about a third of an ounce, so fifty coins weigh a pound.

| Coin                | Value in GP   |
|---------------------|---------------|
| Copper Piece (CP)   | 1/100         |

## Tools

### Artisan's Tools

#### Smith's Tools

**Ability:** Strength

### Other Tools

#### Thieves' Tools

**Ability:** Dexterity

## Adventuring Gear

### Rope

Rope has 2 HP and can be burst with a DC 20 Strength (Athletics) check.
`

const classFixture = `# Classes

## Barbarian

### Core Barbarian Traits

| Primary Ability            | Strength                |
|----------------------------|-------------------------|
| Hit Point Die              | D12 per Barbarian level |
`

const originsFixture = `# Character Origins

## Character Backgrounds

### Background Descriptions

#### Acolyte

- **Feat:** Magic Initiate (Cleric) (see "Feats")

## Character Species

### Species Descriptions

#### Dwarf

- **Speed:** 30 feet
`

// Not SRD text: the condition tag on a heading outside the Rules Glossary.
const conditionElsewhereFixture = `# Playing the Game

## Foo \[Condition\]

Text.
`

const legalFixture = `# Legal Information

The System Reference Document 5.2.1 ("SRD 5.2.1") is provided to you free of charge.
`

// want is the expected part of a chunk; Hash and line numbers are checked
// by checkCoverage and TestHash.
type want struct {
	id, kind, title, path, fullPath, text string
}

func TestSplit(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []want
	}{
		{name: "spell", input: spellFixture, want: []want{{
			id: "spells/fireball", kind: "spell", title: "Fireball",
			path: "Spells > Fireball", fullPath: "Spells > Spell Descriptions > Fireball",
			text: "_Level 3 Evocation (Sorcerer, Wizard)_\n\n- **Casting Time:** Action\n- **Range:** 150 feet\n\n" +
				"_**Using a Higher-Level Spell Slot.**_ The damage increases by 1d6 for each spell slot level above 3.",
		}}},
		{name: "rules section of an entity chapter is a rule", input: spellRulesFixture, want: []want{{
			id: "spells/light", kind: "spell", title: "Light",
			path: "Spells > Light", fullPath: "Spells > Spell Descriptions > Light",
			text: "_Evocation Cantrip (Bard, Cleric, Sorcerer, Wizard)_",
		}, {
			id: "spells/casting-spells/spell-level", kind: "rule", title: "Spell Level",
			path: "Spells > Casting Spells > Spell Level", fullPath: "Spells > Casting Spells > Spell Level",
			text: "Every spell has a level from 0 to 9.\n\n#### Spell Slots\n\nSpellcasting is taxing.",
		}}},
		{name: "monster under a group heading, with sub-headings", input: monsterFixture, want: []want{{
			id: "monsters-a-z/goblin-warrior", kind: "monster", title: "Goblin Warrior",
			path: "Monsters A-Z > Goblin Warrior", fullPath: "Monsters A-Z > Goblins > Goblin Warrior",
			text: "_Small Fey (Goblinoid), Chaotic Neutral_\n\n- **AC** 15\n\n#### Actions\n\n" +
				"_**Scimitar.**_ _Melee Attack Roll:_ +4, reach 5 ft.\n\n#### Bonus Actions\n\n" +
				"_**Nimble Escape.**_ The goblin takes the Disengage or Hide action.",
		}}},
		{name: "animal with sub-headings", input: animalFixture, want: []want{{
			id: "animals/wolf", kind: "monster", title: "Wolf",
			path: "Animals > Wolf", fullPath: "Animals > Wolf",
			text: "_Medium Beast, Unaligned_\n\n- **AC** 12\n\n### Traits\n\n_**Pack Tactics.**_ The wolf has Advantage on attack rolls.",
		}}},
		{name: "condition and other glossary entry", input: conditionFixture, want: []want{
			{
				id: "rules-glossary/prone", kind: "condition", title: "Prone",
				path: "Rules Glossary > Prone", fullPath: `Rules Glossary > Rules Definitions > Prone \[Condition\]`,
				text: "While you have the Prone condition, you experience the following effects.",
			},
			{
				id: "rules-glossary/ready-action", kind: "glossary", title: "Ready [Action]",
				path: "Rules Glossary > Ready [Action]", fullPath: `Rules Glossary > Rules Definitions > Ready \[Action\]`,
				text: "You take the Ready action to wait for a particular circumstance before you act.",
			},
		}},
		{name: "feat under its category, container text kept", input: featFixture, want: []want{
			{
				id: "feats/feat-descriptions", kind: "rule", title: "Feat Descriptions",
				path: "Feats > Feat Descriptions", fullPath: "Feats > Feat Descriptions",
				text: "The feats that follow are organized by category.",
			},
			{
				id: "feats/origin-feats/alert", kind: "feat", title: "Alert",
				path: "Feats > Origin Feats > Alert", fullPath: "Feats > Feat Descriptions > Origin Feats > Alert",
				text: "_Origin Feat_\n\nYou gain the following benefits.",
			},
		}},
		{name: "rules h2 with text before its first h3 and a nested h4", input: rulesFixture, want: []want{
			{
				id: "playing-the-game/d20-tests", kind: "rule", title: "D20 Tests",
				path: "Playing the Game > D20 Tests", fullPath: "Playing the Game > D20 Tests",
				text: "When the outcome of an action is uncertain, the game uses a d20 roll to determine success or failure.",
			},
			{
				id: "playing-the-game/d20-tests/ability-checks", kind: "rule", title: "Ability Checks",
				path: "Playing the Game > D20 Tests > Ability Checks", fullPath: "Playing the Game > D20 Tests > Ability Checks",
				text: "An ability check represents a creature using talent and training.\n\n#### Ability Modifier\n\n" +
					"An ability check is named for the ability modifier it uses.",
			},
			{
				id: "playing-the-game/actions/reactions", kind: "rule", title: "Reactions",
				path: "Playing the Game > Actions > Reactions", fullPath: "Playing the Game > Actions > Reactions",
				text: "Certain special abilities, spells, and situations allow you to take a special action called a Reaction.",
			},
		}},
		{name: "chapter text before its first h2", input: chapterIntroFixture, want: []want{
			{
				id: "magic-items", kind: "rule", title: "Magic Items",
				path: "Magic Items", fullPath: "Magic Items",
				text: "Magic items are gleaned from the hoards of felled monsters.",
			},
			{
				id: "magic-items/bag-of-holding", kind: "magic-item", title: "Bag of Holding",
				path: "Magic Items > Bag of Holding", fullPath: "Magic Items > Magic Items A-Z > Bag of Holding",
				text: "_Wondrous Item, Uncommon_",
			},
		}},
		{name: "table stays in its section; tool under a container", input: tableFixture, want: []want{
			{
				id: "equipment/coins", kind: "rule", title: "Coins",
				path: "Equipment > Coins", fullPath: "Equipment > Coins",
				text: "A coin weighs about a third of an ounce, so fifty coins weigh a pound.\n\n" +
					"| Coin                | Value in GP   |\n|---------------------|---------------|\n| Copper Piece (CP)   | 1/100         |",
			},
			{
				id: "equipment/tools/artisans-tools/smiths-tools", kind: "equipment", title: "Smith's Tools",
				path:     "Equipment > Tools > Artisan's Tools > Smith's Tools",
				fullPath: "Equipment > Tools > Artisan's Tools > Smith's Tools",
				text:     "**Ability:** Strength",
			},
			{
				id: "equipment/tools/thieves-tools", kind: "equipment", title: "Thieves' Tools",
				path: "Equipment > Tools > Thieves' Tools", fullPath: "Equipment > Tools > Other Tools > Thieves' Tools",
				text: "**Ability:** Dexterity",
			},
			{
				id: "equipment/adventuring-gear/rope", kind: "equipment", title: "Rope",
				path: "Equipment > Adventuring Gear > Rope", fullPath: "Equipment > Adventuring Gear > Rope",
				text: "Rope has 2 HP and can be burst with a DC 20 Strength (Athletics) check.",
			},
		}},
		{name: "condition tag outside the Rules Glossary is kept as written", input: conditionElsewhereFixture, want: []want{{
			id: "playing-the-game/foo-condition", kind: "rule", title: "Foo [Condition]",
			path: "Playing the Game > Foo [Condition]", fullPath: `Playing the Game > Foo \[Condition\]`,
			text: "Text.",
		}}},
		{name: "class section", input: classFixture, want: []want{{
			id: "classes/barbarian/core-barbarian-traits", kind: "class", title: "Core Barbarian Traits",
			path: "Classes > Barbarian > Core Barbarian Traits", fullPath: "Classes > Barbarian > Core Barbarian Traits",
			text: "| Primary Ability            | Strength                |\n|----------------------------|-------------------------|\n" +
				"| Hit Point Die              | D12 per Barbarian level |",
		}}},
		{name: "background and species", input: originsFixture, want: []want{
			{
				id: "character-origins/character-backgrounds/acolyte", kind: "background", title: "Acolyte",
				path:     "Character Origins > Character Backgrounds > Acolyte",
				fullPath: "Character Origins > Character Backgrounds > Background Descriptions > Acolyte",
				text:     `- **Feat:** Magic Initiate (Cleric) (see "Feats")`,
			},
			{
				id: "character-origins/character-species/dwarf", kind: "species", title: "Dwarf",
				path:     "Character Origins > Character Species > Dwarf",
				fullPath: "Character Origins > Character Species > Species Descriptions > Dwarf",
				text:     "- **Speed:** 30 feet",
			},
		}},
		{name: "legal information", input: legalFixture, want: []want{{
			id: "legal-information", kind: "legal", title: "Legal Information",
			path: "Legal Information", fullPath: "Legal Information",
			text: `The System Reference Document 5.2.1 ("SRD 5.2.1") is provided to you free of charge.`,
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks, err := Split(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			if len(chunks) != len(tt.want) {
				t.Fatalf("got %d chunks %v, want %d", len(chunks), ids(chunks), len(tt.want))
			}
			for i, c := range chunks {
				got := want{c.ID, string(c.Kind), c.Title, c.PathString(), strings.Join(c.FullPath, " > "), c.Text}
				if got != tt.want[i] {
					t.Errorf("chunk %d:\n got %+v\nwant %+v", i, got, tt.want[i])
				}
			}
			checkCoverage(t, tt.input, chunks)
		})
	}
}

func TestSplitErrors(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantErr  error
		mentions []string // the error message must name these
	}{
		{
			name:     "unknown chapter",
			input:    "# Spells\n\n## Gaining Spells\n\nText.\n\n# Psionics\n\nText.\n",
			wantErr:  ErrUnknownChapter,
			mentions: []string{`"Psionics"`, "line 7"},
		},
		{
			name: "id collision",
			input: "# Spells\n\n## Spell Descriptions\n\n### Fireball\n\nOne.\n\n" +
				"## Other Spells\n\n### Fireball\n\nTwo.\n\n# Spells\n\n## Spell Descriptions\n\n### Fireball\n\nThree.\n",
			wantErr:  ErrDuplicateID,
			mentions: []string{`"spells/fireball"`, `"Spells > Spell Descriptions > Fireball"`},
		},
		{
			name:     "text before the first chapter",
			input:    "Stray text.\n# Spells\n",
			wantErr:  ErrTextBeforeChapter,
			mentions: []string{"line 1"},
		},
		{
			name:     "heading before the first chapter",
			input:    "## Spell Descriptions\n# Spells\n",
			wantErr:  ErrTextBeforeChapter,
			mentions: []string{"line 1"},
		},
		{
			name:     "renamed entity parent",
			input:    "# Spells\n\n## Spell Description List\n\n### Fireball\n\nText.\n",
			wantErr:  ErrUnmatchedPattern,
			mentions: []string{`chapter "Spells"`, `h3 under "Spells > Spell Descriptions"`},
		},
		{
			name:     "renamed entity parent among others",
			input:    strings.Replace(tableFixture, "### Other Tools", "### Misc Tools", 1),
			wantErr:  ErrUnmatchedPattern,
			mentions: []string{`chapter "Equipment"`, `h4 under "Equipment > Tools > Other Tools"`},
		},
		{
			name:     "entity at an unexpected level",
			input:    "# Animals\n\n### Wolf\n\nText.\n",
			wantErr:  ErrUnmatchedPattern,
			mentions: []string{`chapter "Animals"`, `h2 under "Animals"`},
		},
		{
			name:     "heading with no letter or digit",
			input:    "# Spells\n\n## Spell Descriptions\n\n### …\n\nText.\n",
			wantErr:  ErrEmptySlug,
			mentions: []string{"line 5"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Split(strings.NewReader(tt.input))
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			for _, m := range tt.mentions {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("error %q does not mention %s", err, m)
				}
			}
		})
	}
}

func TestSplitDeterministic(t *testing.T) {
	input := rulesFixture + "\n" + spellFixture
	first, err := Split(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Split(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ids(first), ",") != strings.Join(ids(second), ",") {
		t.Fatalf("ids differ between runs: %v and %v", ids(first), ids(second))
	}
}

func TestHash(t *testing.T) {
	chunks, err := Split(strings.NewReader(spellFixture))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(chunks[0].Text))
	if got, want := chunks[0].Hash, hex.EncodeToString(sum[:]); got != want {
		t.Errorf("Hash = %s, want %s", got, want)
	}
}

func TestSlug(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Fireball", "fireball"},
		{"Monsters A-Z", "monsters-a-z"},
		{"Alchemist's Fire", "alchemists-fire"},
		{"Thieves’ Tools", "thieves-tools"},
		{"Bottle, Glass", "bottle-glass"},
		{"Ready [Action]", "ready-action"},
		{"Advantage/Disadvantage", "advantage-disadvantage"},
		{`"The Next Dawn"`, "the-next-dawn"},
		{"Becoming a Barbarian …", "becoming-a-barbarian"},
		{"Step 1: Choose Class", "step-1-choose-class"},
		{"…", ""},
	}
	for _, tt := range tests {
		if got := slug(tt.in); got != tt.want {
			t.Errorf("slug(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseHeading(t *testing.T) {
	tests := []struct {
		line   string
		ok     bool
		level  int
		title  string
		rawEnd string
	}{
		{line: "# Spells", ok: true, level: 1, title: "Spells"},
		{line: `### Prone \[Condition\]`, ok: true, level: 3, title: "Prone [Condition]", rawEnd: `\[Condition\]`},
		{line: `### Hide \[Action\]`, ok: true, level: 3, title: "Hide [Action]"},
		{line: "#### Alert", ok: true, level: 4, title: "Alert"},
		{line: "#Spells", ok: false},
		{line: "####### Seven", ok: false},
		{line: "- **AC** 15", ok: false},
		{line: "", ok: false},
	}
	for _, tt := range tests {
		h, ok := parseHeading(tt.line)
		if ok != tt.ok || h.level != tt.level || h.title != tt.title || !strings.HasSuffix(h.raw, tt.rawEnd) {
			t.Errorf("parseHeading(%q) = %+v, %v", tt.line, h, ok)
		}
	}
}

// TestUnmatchedContainer uses a test-only table entry: in the real table,
// every container is matched whenever the entity rule beside it is.
func TestUnmatchedContainer(t *testing.T) {
	saved := chapters["Monsters"]
	t.Cleanup(func() { chapters["Monsters"] = saved })
	chapters["Monsters"] = chapter{Split: 3, Kind: KindRule, Containers: [][]string{{"Stat Block Index"}}}

	_, err := Split(strings.NewReader("# Monsters\n\n## Stat Block Overview\n\nText.\n"))
	if !errors.Is(err, ErrUnmatchedPattern) {
		t.Fatalf("err = %v, want %v", err, ErrUnmatchedPattern)
	}
	if want := `container "Monsters > Stat Block Index"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not mention %s", err, want)
	}
}

// TestTable checks the split table itself: 14 chapters, and no entity level
// above the chapter's split level (an entity must start its own chunk).
func TestTable(t *testing.T) {
	if len(chapters) != 14 {
		t.Errorf("split table has %d chapters, want 14", len(chapters))
	}
	for name, ch := range chapters {
		if ch.Split < 2 || ch.Split > 6 || ch.Kind == "" {
			t.Errorf("%s: invalid split level %d or kind %q", name, ch.Split, ch.Kind)
		}
		for _, r := range ch.Entities {
			if r.level() < ch.Split || r.Kind == "" {
				t.Errorf("%s: entity rule %v has level %d above split level %d or no kind", name, r.Under, r.level(), ch.Split)
			}
		}
	}
}

// checkCoverage checks that no chunk is empty, that each chunk text is the
// trimmed text of its line range, and that every non-heading, non-blank line
// of the input lands in exactly one chunk.
func checkCoverage(t *testing.T, input string, chunks []Chunk) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(input, "\n"), "\n")
	owners := make([]int, len(lines)+1) // by 1-based line number
	for _, c := range chunks {
		if strings.TrimSpace(c.Text) == "" {
			t.Errorf("%s: empty text", c.ID)
		}
		if _, ok := parseHeading(lines[c.StartLine-1]); !ok {
			t.Errorf("%s: line %d is not a heading", c.ID, c.StartLine)
		}
		if got := strings.TrimSpace(strings.Join(lines[c.StartLine:c.EndLine], "\n")); got != c.Text {
			t.Errorf("%s: text is not lines %d-%d of the input", c.ID, c.StartLine+1, c.EndLine)
		}
		for n := c.StartLine + 1; n <= c.EndLine; n++ {
			owners[n]++
		}
	}
	for i, l := range lines {
		n := i + 1
		if _, heading := parseHeading(l); heading || strings.TrimSpace(l) == "" {
			continue
		}
		if owners[n] != 1 {
			t.Errorf("line %d (%.40q) is in %d chunks, want 1", n, l, owners[n])
		}
	}
}

func ids(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.ID
	}
	return out
}

package srd

// Kind says what a chunk holds. It is derived from the chapter and heading the
// chunk sits under (see chapters below), except for KindCondition, which comes
// from the "[Condition]" tag of a Rules Glossary entry.
type Kind string

// Chunk kinds. Every chunk that is not an entity of its chapter is a rule
// (or legal text, in the Legal Information chapter).
const (
	KindRule       Kind = "rule"       // general rules text, and any text above an entity level
	KindLegal      Kind = "legal"      // the Legal Information chapter
	KindSpell      Kind = "spell"      // Spells > Spell Descriptions
	KindMonster    Kind = "monster"    // stat blocks of Monsters A-Z and Animals
	KindCondition  Kind = "condition"  // Rules Glossary entries tagged [Condition]
	KindGlossary   Kind = "glossary"   // other Rules Glossary entries
	KindMagicItem  Kind = "magic-item" // Magic Items > Magic Items A-Z
	KindFeat       Kind = "feat"       // Feats > Feat Descriptions > <category>
	KindClass      Kind = "class"      // Classes > <class> > <section>
	KindBackground Kind = "background" // Character Origins > Background Descriptions
	KindSpecies    Kind = "species"    // Character Origins > Species Descriptions
	KindEquipment  Kind = "equipment"  // Equipment: adventuring gear and tools
)

// wildcard matches any heading title in a heading pattern.
const wildcard = "*"

// entityRule says that the headings found exactly under the heading pattern
// Under are entities of the given kind. Under lists the titles of the
// headings between the chapter (h1) and the entity, so the entity heading
// level is len(Under)+2: an empty Under means the h2 of the chapter.
type entityRule struct {
	Under []string
	Kind  Kind
}

// level returns the heading level of the entities matched by the rule.
func (r entityRule) level() int { return len(r.Under) + 2 }

// chapter describes how one chapter (h1) of the SRD is split.
//
// A heading starts a chunk when its level is at most Split, or when it is an
// entity heading (see entityRule). A chunk runs to the next heading that starts
// a chunk; deeper headings, tables and lists stay inside it. A heading with no
// text of its own before the next chunk start produces no chunk.
//
// Containers are heading patterns (titles below the chapter, "*" for any
// title) whose headings are dropped from the short citation path of the chunks
// below them. A chunk's own heading is always kept in its path.
type chapter struct {
	Split      int
	Kind       Kind // kind of the chunks that are not entities
	Entities   []entityRule
	Containers [][]string
}

// chapters is the split table, keyed on the chapter (h1) title. A chapter
// title that is not in the table makes the chunker fail.
//
// Heading counts measured on the pinned SRD file are given for reference.
var chapters = map[string]chapter{
	// No sub-headings: the whole chapter is one chunk.
	"Legal Information": {Split: 3, Kind: KindLegal},
	// Rules chapters: one chunk per h3, h2 kept in the path.
	"Playing the Game":   {Split: 3, Kind: KindRule},
	"Character Creation": {Split: 3, Kind: KindRule},
	"Gameplay Toolbox":   {Split: 3, Kind: KindRule},
	"Monsters":           {Split: 3, Kind: KindRule},
	// 12 h2 (one per class), 59 h3 class sections; features (h4) stay inside.
	"Classes": {Split: 3, Kind: KindRule, Entities: []entityRule{
		{Under: []string{wildcard}, Kind: KindClass},
	}},
	// Backgrounds (4) and species (9) are h4 headings under an h3
	// "... Descriptions" heading; the "Parts of ..." h3 sections are rules.
	"Character Origins": {Split: 3, Kind: KindRule,
		Entities: []entityRule{
			{Under: []string{"Character Backgrounds", "Background Descriptions"}, Kind: KindBackground},
			{Under: []string{"Character Species", "Species Descriptions"}, Kind: KindSpecies},
		},
		Containers: [][]string{
			{"Character Backgrounds", "Background Descriptions"},
			{"Character Species", "Species Descriptions"},
		},
	},
	// 17 feats, h4 under their category (h3); the category is kept in the path.
	"Feats": {Split: 3, Kind: KindRule,
		Entities:   []entityRule{{Under: []string{"Feat Descriptions", wildcard}, Kind: KindFeat}},
		Containers: [][]string{{"Feat Descriptions"}},
	},
	// Adventuring gear items are h3 under "Adventuring Gear"; tools are h4
	// under "Artisan's Tools" and "Other Tools". Other h3 sections are rules,
	// with their h4 (weapon properties, masteries, ...) inside. "Other Tools"
	// adds nothing to a path; "Artisan's Tools" is kept as a category.
	"Equipment": {Split: 3, Kind: KindRule,
		Entities: []entityRule{
			{Under: []string{"Adventuring Gear"}, Kind: KindEquipment},
			{Under: []string{"Tools", "Artisan's Tools"}, Kind: KindEquipment},
			{Under: []string{"Tools", "Other Tools"}, Kind: KindEquipment},
		},
		Containers: [][]string{{"Tools", "Other Tools"}},
	},
	// 339 spells, h3 under "Spell Descriptions".
	"Spells": {Split: 3, Kind: KindRule,
		Entities:   []entityRule{{Under: []string{"Spell Descriptions"}, Kind: KindSpell}},
		Containers: [][]string{{"Spell Descriptions"}},
	},
	// 155 entries, h3 under "Rules Definitions"; 15 are tagged [Condition].
	"Rules Glossary": {Split: 3, Kind: KindRule,
		Entities:   []entityRule{{Under: []string{"Rules Definitions"}, Kind: KindGlossary}},
		Containers: [][]string{{"Rules Definitions"}},
	},
	// 258 magic items, h3 under "Magic Items A-Z".
	"Magic Items": {Split: 3, Kind: KindRule,
		Entities:   []entityRule{{Under: []string{"Magic Items A-Z"}, Kind: KindMagicItem}},
		Containers: [][]string{{"Magic Items A-Z"}},
	},
	// 235 stat blocks, h3 under 177 group headings (h2, no text, dropped
	// from the path); actions and traits (h4) stay inside.
	"Monsters A-Z": {Split: 3, Kind: KindRule,
		Entities:   []entityRule{{Under: []string{wildcard}, Kind: KindMonster}},
		Containers: [][]string{{wildcard}},
	},
	// 95 stat blocks, h2; their h3 sub-headings stay inside.
	"Animals": {Split: 2, Kind: KindRule,
		Entities: []entityRule{{Under: nil, Kind: KindMonster}},
	},
}

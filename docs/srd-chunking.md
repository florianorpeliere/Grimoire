# SRD chunking

The package `internal/srd` splits the SRD Markdown file (`data/srd/SRD_CC_v5.2.1.md`, see
[srd-source.md](srd-source.md)) into chunks: one per rule section, spell, glossary entry,
condition, magic item, feat, class section, background, species, item of equipment and monster.
The split table is in [`internal/srd/table.go`](../internal/srd/table.go); this page explains it.

## Rule

- A chunk starts at a heading whose level is at most the chapter's **split level**, or at an
  **entity heading** (a heading at the entity level, under the entity parent headings listed below).
- A chunk runs to the next heading that starts a chunk. Deeper headings (the actions of a stat
  block, the features of a class section), lists and tables stay inside it.
- Text above the split level (a chapter introduction, an h2's text before its first h3) is a chunk
  of its own, attached to its heading. A heading with no text of its own produces no chunk.
- A chapter (h1) title that is not in the table is an error. Text before the first chapter is an
  error.
- Chunks are not split further: the dry run lists any chunk above 4,000 estimated tokens.

## Split table

| Chapter | Split level | Entities (level, under) | Kind | Containers dropped from paths |
|---|---|---|---|---|
| Legal Information | h3 | none | `legal` | |
| Playing the Game | h3 | none | `rule` | |
| Character Creation | h3 | none | `rule` | |
| Classes | h3 | h3 under any class (h2) | `class` | |
| Character Origins | h3 | h4 under Character Backgrounds > Background Descriptions | `background` | Background Descriptions |
| | | h4 under Character Species > Species Descriptions | `species` | Species Descriptions |
| Feats | h3 | h4 under Feat Descriptions > any category | `feat` | Feat Descriptions |
| Equipment | h3 | h3 under Adventuring Gear | `equipment` | |
| | | h4 under Tools > Artisan's Tools, Tools > Other Tools | `equipment` | Other Tools |
| Spells | h3 | h3 under Spell Descriptions | `spell` | Spell Descriptions |
| Rules Glossary | h3 | h3 under Rules Definitions | `glossary`, or `condition` | Rules Definitions |
| Gameplay Toolbox | h3 | none | `rule` | |
| Magic Items | h3 | h3 under Magic Items A-Z | `magic-item` | Magic Items A-Z |
| Monsters | h3 | none | `rule` | |
| Monsters A-Z | h3 | h3 under any group heading (h2) | `monster` | the group headings |
| Animals | h2 | h2 | `monster` | |

Every chunk that is not an entity has the kind `rule` (`legal` in Legal Information). A Rules
Glossary entry whose heading ends with the `\[Condition\]` tag has the kind `condition`; the tag is
kept out of its title and short path. Other glossary tags (`[Action]`, `[Hazard]`,
`[Area of Effect]`, `[Attitude]`) are kept, unescaped.

## Paths and ids

- **Short path** (the citation): the chapter, the headings that carry meaning, then the chunk's
  own heading, joined with ` > `. Container headings are dropped from the path of the chunks below
  them; a chunk's own heading is always kept. Examples: `Spells > Fireball`,
  `Rules Glossary > Prone`, `Monsters A-Z > Goblin Warrior`, `Animals > Wolf`,
  `Playing the Game > Actions > Reactions`, `Classes > Barbarian > Core Barbarian Traits`,
  `Feats > Origin Feats > Alert`, `Character Origins > Character Species > Dwarf`.
- **Full path**: every heading from the chapter down, as written in the source, stored alongside.
- **Id**: the slug of the short path. Each segment is lower-cased, apostrophes are removed, every
  other run of characters that are not ASCII letters or digits becomes one `-`, leading and
  trailing `-` are trimmed, and the segments are joined with `/`. Examples: `spells/fireball`,
  `playing-the-game/actions/reactions`, `equipment/adventuring-gear/alchemists-fire`. Ids are
  deterministic and unique in the corpus; a collision is an error naming both full paths. An id
  changes when a heading of its short path is renamed; the content hash (SHA-256 of the text)
  detects text changes.

## Dry run

```sh
scripts/srd-download.sh
make srd-dry-run        # or: go run ./cmd/ingest srd -dry-run [file]
```

Prints the number of chunks per kind, the size distribution and the chunks above 4,000 estimated
tokens, then every chunk (id, kind, estimated tokens, short path). It does not use the database.
Tokens are **estimated** as characters (Unicode code points) divided by 4, rounded up; this is not
a tokenizer count.

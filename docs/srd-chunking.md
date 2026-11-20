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
- Headings are ATX headings only: 1 to 6 `#` at column 0, then one space, then the title (no tab,
  no indent, no closing `#`, no setext underline). Any other line is text. The pinned file has
  only this form.
- A chapter (h1) title that is not in the table is an error. Text before the first chapter is an
  error. An entity rule or a container of a chapter present in the file that matches no heading
  of that chapter (e.g. a heading renamed in a new pin) is an error naming the chapter and pattern.
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
kept out of its title and short path. Outside the Rules Glossary entries, the tag has no effect and
is kept as written. Other glossary tags (`[Action]`, `[Hazard]`,
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
  trailing `-` are trimmed, and the segments are joined with `.`. Examples: `spells.fireball`,
  `playing-the-game.actions.reactions`, `rules-glossary.prone`,
  `equipment.adventuring-gear.alchemists-fire`. A segment holds only `[a-z0-9-]`, so `.` cannot
  occur inside one, and an id can be used in a URL path without encoding. Ids are
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

## Changing the source pin

`TestFullFile` (`internal/srd/fullfile_test.go`) asserts the total chunk count and the count of
every kind on the pinned file. These numbers are the check on the chunker, so when the pin in
`scripts/srd-source.env` changes (see [srd-source.md](srd-source.md)), they are **not** copied from
the new dry run, which would only repeat what the chunker does:

1. Run `scripts/srd-download.sh`, `scripts/srd-validate.sh` and `make srd-dry-run` on the new pin.
2. `spell`, `monster` and `condition` are counted independently by `scripts/srd-validate.sh`
   (headings under Spell Descriptions against the PDF school and level lines; stat block headings
   against the PDF Index of Stat Blocks and its `AC` lines; headings ending `[Condition]` against
   the PDF). The dry run must give these counts.
3. `background`, `class`, `equipment`, `feat`, `glossary`, `magic-item` and `species` are **not**
   validated by the script. Count their entity headings in the Markdown file directly (for
   example with `grep` on the heading level and parent heading given in the split table above),
   and check the dry run against that count. A difference must be explained, e.g. a heading with no
   text of its own, which produces no chunk.
4. `rule` and `legal` have no content marker. Compare the chunk lists of the dry run before and
   after the pin change, and check each added, removed or renamed id against the source.
5. Only then update the counts in `TestFullFile`, the counts in the comments of
   `internal/srd/table.go`, and the PR description, with the independent count next to each.

`scripts/srd-validate.sh` also checks the chapters against the PDF table of contents; it does not
check the split inside a chapter.

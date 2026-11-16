# SRD 5.2.1 source text

Grimoire answers rules questions only from the System Reference Document 5.2.1 (SRD 5.2.1). This page
explains where the Markdown text comes from, how to get it, the one scripted change applied to it,
and how it was checked against the official PDF.

Attribution: see [`ATTRIBUTION.md`](../ATTRIBUTION.md).

## Source

- Community conversion [`your5e/5e-srd-markdown`](https://github.com/your5e/5e-srd-markdown), pinned to
  one commit. The pin (repository, commit, file, expected checksums) is recorded in one place:
  [`scripts/srd-source.env`](../scripts/srd-source.env).
- The file used is `dnd/521/SRD_CC_v5.2.1.md`, the upstream "cleaned, one file" conversion, in
  document order. Its top-level headings are the PDF chapters and its heading levels follow the
  document (chapter > section > subsection). The same repository also has `dnd/521/markdown/`, the
  same text split into one file per entity; we do not use it, because the split loses the chapter
  level (folder names such as `Playing` or `Services` do not match the PDF chapters, and
  `Backgrounds/Creating a Background.md` belongs to the Gameplay Toolbox chapter). Its file counts
  are still used as a cross-check by the validation script.
- The source repository has no licence file. Florian accepted this risk on 2026-11-15; the fallback
  is our own conversion of the official PDF. For that reason, and because the PDF is not ours to
  redistribute, the downloaded Markdown and the PDF are **never committed**: they live in `data/srd/`,
  which is git-ignored.
- The downloaded repository is treated as untrusted data: the script fetches only the pinned commit,
  with git hooks disabled, reads the one Markdown file as a git blob (no working tree, so no
  checkout filter or symlink is involved, and the script checks it is a regular file), and never
  runs anything from it.

## Getting the Markdown

```sh
scripts/srd-download.sh
```

Requires `git`, `awk` and `shasum` or `sha256sum`. Produces `data/srd/SRD_CC_v5.2.1.md`. The script:

1. removes any previous output, fetches the pinned commit only (`--depth 1`) and checks that the
   fetched commit is the pinned hash;
2. reads the file and applies the scripted cleanup below;
3. checks that the SHA-256 of the result equals `SRD_MARKDOWN_SHA256` from `scripts/srd-source.env`,
   so the output is byte-identical on every run. Only then is the result moved to
   `data/srd/SRD_CC_v5.2.1.md`. On a mismatch it fails, nothing is left at that path, and the result
   is kept as `data/srd/SRD_CC_v5.2.1.md.unverified` for review.

`scripts/srd-validate.sh` also refuses a Markdown file whose SHA-256 is not the pinned one, so an
edited or unverified file is never measured or reported under the pinned commit.

### Scripted cleanup

One transformation, and no hand-edited files:

- **"Animals" becomes a chapter.** In the PDF, "Animals" (page 344) is printed at chapter size, the
  same as "Monsters" and "Monsters A–Z", and is listed as a chapter in the table of contents. The
  conversion nests it as `## Animals` under `# Monsters A-Z`. The script turns it into `# Animals`
  and moves every heading after it (the animal stat blocks and their sub-headings) up one level.
  The script fails if it does not find exactly one such heading.

Nothing else is changed. Other differences with the PDF (below) are left as the conversion made them.

### Changing the pin

Edit `SRD_COMMIT` in `scripts/srd-source.env`, run `scripts/srd-download.sh` (it will fail on the
checksum, print the new one and keep the result as `SRD_CC_v5.2.1.md.unverified`), review that
file, then update `SRD_MARKDOWN_SHA256`, re-run the download, update the commit in
`ATTRIBUTION.md`, and re-run the validation below. Any change of source is a change of the rules
corpus and must be replayed against the eval set.

## Validation against the official PDF

```sh
scripts/srd-validate.sh
```

Requires `pdftotext`, from [poppler](https://poppler.freedesktop.org/) (free, open source; for
example `brew install poppler` or `apt install poppler-utils`). It is needed by this script only;
the script stops with a clear message if it is missing. It also needs `curl`. The PDF
(https://media.dndbeyond.com/compendium-images/srd/5.2/SRD_CC_v5.2.1.pdf) is downloaded into
`data/srd/` and checked against `SRD_PDF_SHA256` (if an existing PDF fails the check, delete it and
re-run). The Markdown is checked against `SRD_MARKDOWN_SHA256`.

This is a **measurement**, not a test: there is no target and no pass/fail threshold on fidelity. The
script only fails when it cannot measure (missing tool or file, Markdown or PDF checksum not the
pinned one).

### What it measures, and how

All comparisons work on the text extracted by `pdftotext` (reading order, not layout). Titles and
names are compared after normalising typographic quotes and dashes (the conversion writes
"Monsters A-Z" where the PDF prints "Monsters A–Z"). Running headers and page-number lines are
dropped from the PDF text.

- **Attribution.** The statement in `ATTRIBUTION.md` must appear in the text of PDF page 1, compared
  with all whitespace removed (the PDF breaks the licence URL across two lines).
- **Chapters.** The 14 chapter titles of the PDF table of contents, with their first page, are listed
  in the script (transcribed from the contents page; a title counts as a chapter when the PDF prints
  it at chapter size, which is how "Monsters A–Z" and "Animals" qualify). For each, the script checks
  that it appears with leader dots on a contents page, that it is a line of its first page, and that
  the Markdown has it as a top-level heading. It also reports extra top-level headings and heading
  levels that skip a level.
- **Spells.** PDF: lines that start a spell's school line (`Level 3 Evocation (…` or
  `Evocation Cantrip (…`) on the Spells chapter pages. Markdown: headings under
  `## Spell Descriptions`, and the same school lines. Each Markdown spell name is also looked up as
  a whole line of the PDF Spells pages. These are pattern counts that agree with each other; the PDF
  has no separate list of spells to count against, so they equal the number of spells only if every
  school line starts a line in the extracted text and nothing else does. The upstream folder count
  is not independent (same conversion). Monsters are better supported, because the PDF has its own
  index.
- **Monsters.** PDF: two independent counts, the entries of the "Index of Stat Blocks" (contents
  pages) and the lines starting `AC <number>` on the Monsters A–Z and Animals pages (one per stat
  block). Markdown: stat-block headings and `- **AC**` lines in the Monsters A-Z and Animals chapters.
  The index names are compared with the Markdown headings both ways.
- **Conditions.** Lines ending `[Condition]` in the PDF Rules Glossary pages and headings ending
  `[Condition]` in the Markdown, compared by name.
- **Sampled sections.** One general rule (Advantage/Disadvantage), one spell (Fireball), one condition
  (Prone), one monster stat block (Goblin Warrior) and one table (Experience Points by Challenge
  Rating). Both sides are reduced to lower-case word tokens (punctuation and Markdown syntax
  removed; a `+` or `-` sign directly before a number is kept). The PDF columns, sidebars and
  side-by-side tables do not follow the Markdown order, so the section is aligned piece by piece:
  starting where its heading appears in its chapter's pages, the next Markdown words are looked up at
  the closest position in the PDF text and matched as far as they agree. Markdown words that cannot
  be found count as Markdown-only; PDF words skipped by a short forward jump (at most 20 words) and
  never matched elsewhere count as PDF-only. The script prints both counts and the differing words.
- **Whole-document overlap.** Share of 8-word sequences found on the other side, per chapter, in both
  directions, with the Markdown paragraphs below 50% and the longest PDF passages with no match.
  In the Markdown-to-PDF direction only text lines of 8 tokens or more are scored; the script
  prints how many tokens fall in each part (scored text lines, short or blank lines, table rows,
  headings). Table rows, short lines (every stat-block bullet, every spell component line) and
  headings are not scored; headings are covered only partly, by the spell, monster and condition
  name checks.

### What it does not catch

- It compares words, not formatting: bold, italics, list structure and table layout are not checked;
  punctuation differences are invisible.
- Tables are laid out differently in the PDF (side-by-side halves, rotated ability-score blocks), so
  their words come in another order. The sampled alignment copes with simple tables, but short
  sequences can match another stat block inside its search window (2,000 tokens around the
  chapter's pages), so the counts for a stat-block sample are mostly noise; the monster sample was
  checked by hand instead (below).
- Nothing in the Markdown that is not scored (about 23% of its tokens: tables, short lines,
  headings) is compared with the PDF in the Markdown-to-PDF direction, except the five samples and
  the name checks. A wrong number in a table row or an invented stat-block bullet would not be seen.
- The whole-document overlap is a coarse signal: any table, column break or sidebar breaks 8-word
  sequences, which is why the PDF-to-Markdown figures are lowest for stat-block chapters. A low
  figure says "look here", not "text is missing".
- Only five sections are compared word by word. A wrong number in a section that was not sampled
  would only show up in the overlap figures, and only if it falls inside running text.
- The chapter list is transcribed by hand from the contents page; the script checks it against the
  PDF text but cannot discover a chapter missing from the list.

## Results

Measured on 2026-11-16 with the pinned commit `f1f5060fd975aa2ffc3e4b336560ded479934d80`, the pinned
PDF (364 pages) and pdftotext 26.11.0.

| Check | Markdown | PDF |
|---|---|---|
| Chapters (top-level headings / contents chapters) | 14, none extra | 14, all found in the contents and on their first page |
| Heading levels skipping a level | 0 | |
| Spells | 339 headings, 339 school lines | 339 school lines; all 339 Markdown names found as PDF lines |
| Monster stat blocks (Monsters A–Z + Animals) | 330 headings, 330 AC lines (235 + 95) | 330 index entries, 330 AC lines; names identical both ways |
| Other stat blocks (spells, magic items) | 6 | 6 |
| Conditions | 15 | 15, names identical |
| Attribution statement verbatim on page 1 | | yes |

Cross-check with the per-file folder of the same commit: 330 files in `Monsters/`, 340 `.md` files in
`Spells/` of which `Spells/Spells.md` is the chapter introduction, so 339 spells.

Chapters of the PDF contents next to the Markdown top-level headings:

| PDF chapter (first page) | Markdown heading |
|---|---|
| Legal Information (1) | `# Legal Information` |
| Playing the Game (5) | `# Playing the Game` |
| Character Creation (19) | `# Character Creation` |
| Classes (28) | `# Classes` |
| Character Origins (83) | `# Character Origins` |
| Feats (87) | `# Feats` |
| Equipment (89) | `# Equipment` |
| Spells (104) | `# Spells` |
| Rules Glossary (176) | `# Rules Glossary` |
| Gameplay Toolbox (192) | `# Gameplay Toolbox` |
| Magic Items (204) | `# Magic Items` |
| Monsters (254) | `# Monsters` |
| Monsters A–Z (258) | `# Monsters A-Z` |
| Animals (344) | `# Animals` (after the scripted cleanup) |

The PDF's "Index of Stat Blocks" (contents pages) has no Markdown counterpart: the conversion drops
the table of contents and the index, as its README states.

Sampled sections (Markdown words / differences):

| Sample | Words | Markdown-only | PDF-only | Notes |
|---|---|---|---|---|
| Advantage/Disadvantage (rule) | 429 | 0 | 0 | identical |
| Fireball (spell) | 115 | 0 | 0 | identical |
| Prone (condition) | 84 | 0 | 0 | identical |
| Goblin Warrior (stat block) | 136 | 14 | 11 | layout only, see below |
| Experience Points (table) | 161 | 0 | 13 | the PDF table caption and column headers were removed by the conversion, see below |

Goblin Warrior, checked by hand against the PDF page 290 layout: AC 15, Initiative +2 (12), HP 10
(3d6), Speed 30 ft., Str 8/−1/−1, Dex 15/+2/+2, Con 10/+0/+0, Int 10/+0/+0, Wis 8/−1/−1,
Cha 8/−1/−1, skills, gear, senses, languages, CR, actions and bonus action all match. The reported
differences are alignment artefacts: the Markdown-only tokens are the ability scores, which the
conversion rotates into one table (the PDF prints `Str 8 −1 −1` and so on in three columns), and
the PDF-only tokens (`+8 +15 +10 +10 +8 +8`, which do not occur on page 290) come from another
stat block inside the search window.

Whole-document overlap (8-word sequences). Coverage of the Markdown-to-PDF direction, as printed
by the script:

| Part of the Markdown | Lines | Tokens | Share of tokens | Scored |
|---|---|---|---|---|
| Text lines of 8 tokens or more | 5,499 | 183,477 | 77.2% | yes |
| Table rows | 4,630 | 26,955 | 11.3% | no |
| Short or blank text lines | 15,882 | 20,954 | 8.8% | no |
| Headings | 2,963 | 6,334 | 2.7% | no |

99.3% of the 8-word sequences of the scored text lines are found in the PDF (9 of the 5,499 lines
are below 50%), and 86.2% of the PDF sequences are found in the Markdown.
By chapter, Markdown to PDF ranges from 96.0% (Character Creation) to 100% (Legal Information,
Feats); PDF to Markdown ranges from 53.2% (Animals) and 74.0% (Monsters A–Z), where stat-block
layout breaks the sequences, to 100% (Feats). The contents and index pages (2–4) match 0%, as
expected. Of the 39 PDF passages of 60 or more unmatched sequences, the longest are the contents
pages, then tables on class spell-list pages (pages 33, 39, 44, 67, 68, 80, 81) and the
Experience Points table, whose words come in a different order in the PDF text.

### Differences found

Known deviations of the conversion from the PDF, left as converted (the cleanup changes none of
them). Within the scored text (77.2% of the Markdown tokens), no passage from another source or
from SRD 5.1 was found: every scored line below 50% is explained below. The unscored 22.8% (tables,
short lines, headings) was not checked this way.

- The table of contents and the Index of Stat Blocks are not in the Markdown.
- Table captions and references to them were removed, e.g. "as detailed in the Experience Points by
  Challenge Rating table" became "as detailed in the table" (documented by the conversion: tables
  were moved next to the text that uses them). Of the 9 scored Markdown lines below 50%, 5 are
  sentences of this kind ("The tables in this section show…", "using this table"), 2 are the
  conversion differences below ("Components:", "10 foot"), and 2 are reformatted lines: the Spell
  Scroll costs ("Spell Scroll (Cantrip, 30 GP; …" in the PDF) and the fast travel pace formula
  ("1⅓" written "1 1/3").
- "Component:" is written "Components:". 12 lines of the PDF text start with `Component:` (not
  tied to the number of components: one is `Component: V, S, M (…)`); the Markdown has 0 `**Component:**` and 339 `**Components:**`. Only one
  of the 12 shows in the overlap check: the others are lines under 8 tokens, which are not scored.
- "10-foot" is written "10 foot" in 4 places. The PDF prints "10-foot" with the hyphen at a line
  break (`10-` / `foot`); the conversion lost the hyphen. (pdftotext also drops it and gives
  "10foot", which is why the overlap check flags the line.)
- The D20 Tests steps are numbered 1–3 instead of the PDF's 4–6 (documented upstream).
- Monster ability scores are one Markdown table per stat block instead of the PDF's three blocks.
- In the Legal Information chapter, the licence URL is split as `…/by/4.0/ legalcode` (a space from
  the PDF line break). `ATTRIBUTION.md` has the correct URL; the Markdown is left as converted.
- The conversion writes straight quotes, a hyphen for the minus sign, and "A-Z" for "A–Z".

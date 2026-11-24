# Evals

## Eval set

`eval-set.txt` is the step 2 eval set (issue #28): hand-written questions, each with the
expected answer and the SRD chunks an answer must cite. It is filled in batches, one story per
category; it holds no question yet, only format examples commented out. The retrieval smoke set
below is not part of it.

| Category | Planned | What it tests |
|---|---|---|
| `rules` | 80 | a simple rules question |
| `interaction` | 40 | an interaction between rules |
| `refusal` | 15 | out of the SRD or ambiguous: the expected answer is "the SRD does not say" |
| `campaign` | 15 | a question on the fictional 10-session campaign (reserved, see below) |

### Check it

After the [local setup](../README.md#local-setup-macos-homebrew), with the SRD ingested
(`make srd`) and `DATABASE_URL` set:

```sh
make eval-check            # or: go run ./cmd/evals check [file]
```

The command parses the file and reports every malformed record with its line number, then
checks every expected chunk id against the chunks stored in the database and lists all the
unknown ones. On success it prints the number of questions per category next to the plan:

```
evals/eval-set.txt: 0 question(s), every record well formed, every expected chunk id stored.

category     questions  planned
rules                0       80  80 below plan
interaction          0       40  40 below plan
refusal              0       15  15 below plan
campaign             0       15  15 below plan
total                0      150  150 below plan
```

A count below (or above) plan is reported, never an error, so batches can be merged one by one.
It exits 1 on a malformed record, an unknown chunk id, or the database unreachable, not migrated
or not ingested; 2 on a usage error. It needs no Ollama and calls no text-generation API.

The ids are checked against the database, the same check as `cmd/evals retrieval`, because the
harness will search those stored chunks: an id that is not stored could never be retrieved or
cited. The SRD Markdown is not committed, so the test `TestEvalSetIDsInChunkerOutput` in
`cmd/evals` also checks them against the chunker output when it has been downloaded
(`scripts/srd-download.sh`), and is skipped otherwise.

### File format

A UTF-8 text file (a byte order mark is ignored), editable in any text editor. One record per
question; records are separated by one or more blank lines:

```
1 rules
Q: What saving throw does Fireball call for, and how much damage does it
   deal?
A: A Dexterity saving throw; 8d6 Fire damage on a failed save, half as much
   on a success.
cite: spells.fireball

2 interaction
Q: Can I Misty Step away to avoid an Opportunity Attack?
A: Yes. Misty Step teleports you, and teleportation never provokes an
   Opportunity Attack.
cite: playing-the-game.combat.melee-attacks
      rules-glossary.teleportation
cite: spells.misty-step

3 refusal
Q: How does the Eldritch Knight fighter subclass work?
A: The SRD does not say: the Eldritch Knight subclass is not in the SRD.
cite: -
```

- **Header**, the first line, not indented: `<number> <category>`.
  - `number`: a positive integer, unique in the file; reports refer to it.
  - `category`: `rules`, `interaction` or `refusal` (lower case). `campaign` is reserved: notes
    ingestion is step 4, so a note has no chunk id to cite yet, and a `campaign` record is
    rejected until then.
- **`Q:`** the question as a GM would ask it; not empty, at most 1,000 bytes (the search limit).
  The same question (ignoring case and spacing) cannot appear twice in the file.
- **`A:`** the expected answer: short, in our own words, never copied from the SRD; not empty.
- **`cite:`** one citation group per line: the chunk ids that hold one passage the answer needs,
  separated by spaces. Several ids on a line are alternatives (chunks that each hold that
  passage). A **correct citation cites at least one id of every group**: in record 2, the answer
  must cite `spells.misty-step` and one of `playing-the-game.combat.melee-attacks` or
  `rules-glossary.teleportation`. `rules` and `interaction` records have one or more `cite:`
  lines; a `refusal` record has exactly one line, `cite: -`, and `-` is allowed nowhere else. Ids
  are those of the chunker (`make srd-dry-run` prints them), e.g. `spells.fireball`,
  `rules-glossary.grappled`. An id cannot appear twice in a record, even in two groups.

Each record has one `Q:`, one `A:` and its `cite:` lines, in any order; keys are case-sensitive.
A line that starts with spaces or tabs continues the line above it: for `Q:` and `A:` it is
joined with a single space, for `cite:` it adds alternatives to the same group. Text may contain
any character, `|` included.

A line whose first non-blank character is `#` is a comment, ignored anywhere, even inside a
record (it does not end the record). There are no inline comments: `#` after an id on a `cite:`
line is an error.

A record carries no review mark: each batch is validated by Florian in its pull request.

The three records above are in `eval-set.txt`, commented out with the prefix `#>`, so the command
ignores them; the test `TestEvalSetExamples` removes the prefix and parses them to keep them
valid. They can be removed with the first batch.

From Go, `internal/evalset` reads the file (`evalset.Read`, `evalset.Parse`) and returns the
questions with their number, header line, category, text, answer and citation groups
(`Question.Expected`, one slice of alternative ids per group). `Question.Covered(ids)` tells
whether the ids cover every group (false for a refusal, which has no group),
`Question.GroupsCovered(ids)` how many groups they cover, `Question.AnyExpected(ids)` whether
they hold any expected id, and `Question.IDs()` lists every expected id.

## Retrieval smoke set

`retrieval-smoke.txt` holds 39 hand-written rules questions, each with the SRD chunk(s) that
should come back (issue #10). It measures retrieval only: no answer is generated, no
text-generation API is called. It is not the step 2 eval set.

- Group `named` (20): questions that name the rule, spell, condition or monster, with a query
  type: `exact` (the question uses the name) or `paraphrase` (it describes it).
- Group `described` (19): situations described without the words of the chunk title.

### Run it

After the [local setup](../README.md#local-setup-macos-homebrew), with the SRD ingested and
embedded (`make srd`) and `DATABASE_URL` and `OLLAMA_HOST` set:

```sh
make eval-retrieval        # or: go run ./cmd/evals retrieval [file]
```

Every question runs through four configurations, each keeping the top 8 results (the default
limit of `cmd/ask`):

| Configuration | Search |
|---|---|
| `hybrid` | `search.Hybrid` with the defaults of `cmd/ask`: prefix as `search.DefaultPrefix` (on), `DefaultK`, `DefaultCandidates` |
| `keyword` | `search.Keyword` only |
| `vector` | `search.Vector` only, question embedded with the default prefix |
| `hybrid-prefix-off` | `search.Hybrid` with the prefix flipped, other options as `hybrid` |

A question is a **hit** when any of its expected ids is in the top 8. The report gives, per
configuration and group (and per query type for `named`):

- **first-result**: the questions whose first result is an expected id;
- **recall@8**: the questions with a hit.

Then the rank of each expected id per question and configuration ("not found" when it is not
in the top 8), and the misses of each configuration with their first result.

There is no target and no threshold: a low score never fails the run. It exits 1 only on an
error: a malformed file, an expected id that is not a stored chunk (all of them are listed), the
database unreachable or not ingested, or Ollama not configured, unreachable or returning an error
(such as a model that is not pulled). In that last case the keyword configuration still runs, its
report is printed with the other configurations marked skipped, then the run fails.

### File format

A UTF-8 text file, one question per line. Blank lines and lines starting with `#` are ignored.
A question line has five fields separated by `|` (spaces around the fields are ignored):

```
number | group | query type | question | expected chunk ids
1 | named | exact | What does Fireball do, and which saving throw does it use? | spells.fireball
21 | described | - | can I hit someone who walks away from me | rules-glossary.opportunity-attacks
```

- `number`: a positive integer, unique in the file; the report refers to it.
- `group`: `named` or `described`.
- `query type`: `exact` or `paraphrase` for `named`, `-` for `described`.
- `question`: the text searched, as is; it cannot contain `|` and is at most 1,000 bytes.
- `expected chunk ids`: one or more chunk ids separated by spaces; any one of them in the top 8
  is a hit. Ids are those of the chunker (`make srd-dry-run` prints them), e.g.
  `spells.fireball`, `rules-glossary.grappled`.

Every malformed line is reported with its line number. Before searching, the runner checks every
expected id against the chunks stored in the database and fails, listing them, if any is
unknown; an unknown id is never counted as a miss. The test `TestCommittedIDsInChunkerOutput`
in `cmd/evals` also checks them against the chunker output when the SRD Markdown has been
downloaded.

# Evals

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
database unreachable or not ingested, or Ollama unreachable or not configured. In that last case
the keyword configuration still runs and its report is printed before the error.

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

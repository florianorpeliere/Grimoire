# Grimoire

Grimoire is a game-master assistant for 5e-compatible tabletop RPGs.
V1 answers rules questions with citations from the SRD 5.2.1, and answers questions about a
campaign from the GM's session notes.

Working name: Grimoire. Language for V1: English (French later).

## V1 scope

In scope:
- Rules questions, answered only from the SRD 5.2.1, with a citation to the exact section.
- An explicit "the SRD does not say" answer when the rule is missing or ambiguous. Never invent a rule.
- Session notes: the GM pastes or imports notes (text, Markdown) per campaign.
- Campaign questions ("What do we know about the baron?"), answered from the notes, citing the session.
- User accounts, one or more campaigns per account. Notes are private to their author.

Out of scope for V1 (do not build, flag it if a task drifts there):
- Structured extraction of NPCs, places, timeline (V2).
- Character creation, narrative suggestions (V3).
- Images, maps, voice, mobile app.
- Any content from official books (Player's Handbook, etc.).

## Legal constraints

- Rules corpus = SRD 5.2.1 only, licensed CC-BY-4.0. Keep the attribution statement from the SRD
  PDF in the footer and on a legal page.
- "Dungeons & Dragons" and "D&D" are trademarks: never in the product name, UI copy or domain.
  Use "compatible with 5e".
- Never ingest or reproduce text from paid books.
- GM notes are private: never shared across accounts, deletable on request.

## Architecture

- Backend: Go, REST API, accounts and campaigns. Runs locally for now (see "Local-first").
- Storage: PostgreSQL with pgvector (embeddings) and full-text search (keywords).
- Embeddings: a local model served by Ollama, behind a Go interface so the provider can change.
  The model name and dimension are stored with each embedded row.
- Search: hybrid (vector + keyword). Spell and condition names are found better by keyword.
- Generation: Claude API. Answer only from the retrieved passages, cite their ids,
  say clearly when the answer is not in them. Stream the answer.
- Ingestion:
  - SRD: one chunk per rule, spell, condition or monster, following the document headings.
    Each chunk keeps its path (chapter > section) for the citation.
  - Notes: split per session then per paragraph, with session number and date as metadata.
- Frontend: React, streaming answers, clickable citations.
- Contract between backend and frontend: `api/openapi.yaml`. It is written first and changed only
  with the GM-owner's approval (Florian).

### Local-first (until Florian says otherwise)

Everything runs on Florian's machine (Apple M2 Pro, 16 GB RAM, macOS). There is no hosting budget yet.

- No hosted or paid service by default: no Cloud Run, no managed database, no hosted embedding API.
  A story that needs one, or implies a recurring cost, is flagged to Florian before any work.
- Services are native, not containers: Go toolchain, PostgreSQL and Ollama installed on the machine.
  Docker is not required and must not be assumed to be running.
- All endpoints come from environment variables (`DATABASE_URL`, `OLLAMA_HOST`), so the same
  binaries can later point at hosted services without code changes.
- Tests that need Postgres or Ollama skip with a clear message when the service is unreachable;
  they never fail for that reason and never start services themselves.
- Mind the 16 GB of RAM: keep local models small, and do not load several models at once.
- The Claude API (generation, step 2) is the one planned external, paid dependency.
  Its budget is an open decision.

Proposed layout:

```
cmd/server/        API entrypoint
cmd/ingest/        SRD and notes ingestion
cmd/evals/         eval harness
internal/...       Go packages (search, llm, campaigns, auth)
api/openapi.yaml   API contract
web/               React app
evals/             eval questions and results
docs/              product and architecture notes
scripts/           repository tooling
```

User stories are GitHub issues, not files (see "Backlog" below).

## Quality and evals

- Eval set (`evals/`), written by hand, about 150 questions:
  80 simple rules questions, 40 rule interactions, 15 out-of-SRD or ambiguous questions
  (expected answer: "the SRD does not say"), 15 questions on a fictional 10-session campaign.
- Metrics: correct citation, correct answer (LLM judge + manual sample), justified refusal,
  latency (time to first token, p50 and p95), cost per question.
- Targets are set after the first measurement. Never invent numbers.
- Every change to prompts, chunking, search or model is replayed against the eval set before merge.

## Conventions

- Go: standard layout, `go vet` and tests pass before any commit. Table-driven tests.
- No new dependency without a one-line reason in the PR description.
- Small PRs, one issue each. Commit messages in English, imperative mood.
- Secrets only from environment variables, never committed.

## How we work: the agent team

The main Claude Code session is the **orchestrator** (run on Fable). Subagents (run on Opus)
cannot start other subagents, so delegation always goes through the main session.
Run `/orchestrate <step number or issue numbers>` to start.

| Agent | Role | Writes |
|---|---|---|
| `product` | Drafts user stories as GitHub issues, checks PRs against their acceptance criteria | GitHub issues, comments, Draft column only |
| `api` | Go backend, Postgres, search, Claude calls, ingestion, eval harness | backend code, PRs |
| `ui` | React frontend, built against `api/openapi.yaml` | `web/` only, PRs |
| `reviewer` | Runs tests and evals, reviews every PR before merge | PR review comments only |

### Backlog: GitHub issues

The backlog is the GitHub issues of this repository, read and written with the `gh` CLI.
**Status lives on the project board** ([Grimoire project](https://github.com/users/florianorpeliere/projects/6),
owner `florianorpeliere`, number 6), in its single-select **Status** field. It is the only source of truth
for status: there are no status labels.

| Board column (Status) | Meaning | Moved there by |
|---|---|---|
| Draft | Written, not refined yet | product |
| Ready | Refined, can be built | **Florian only** (drag the card) |
| In progress | An agent is building it | api / ui |
| In review | PR open, under review | api / ui |
| Done | Issue closed (PR merged) | the board's "Item closed" workflow |

Agents read and move cards only through two scripts (they need `gh auth refresh -s project` once):
- `scripts/project-items.sh "<Status>"` lists the issues in a column.
- `scripts/project-status.sh <issue> "<Status>"` moves an issue (and adds it to the board if missing).

Labels, used for routing and filtering only:

| Label | Meaning | Set by |
|---|---|---|
| `story` | A user story (template: `.github/ISSUE_TEMPLATE/story.md`) | product |
| `area/api`, `area/ui`, `area/evals` | Which agent builds it | product |
| `step/1` … `step/6` | Week of the V1 plan | product |

Labels are created once with `scripts/setup-github-labels.sh`.
Branches are named `<issue number>-short-title`; every PR starts with `Closes #<issue number>`.

### Flow for each step

1. `product` drafts or updates the issues for the step (Draft column).
2. The orchestrator lists them for Florian. **Stop**: Florian refines them on GitHub and drags
   the ones to build to the Ready column.
3. The orchestrator hands each Ready issue to `api` or `ui` by its `area/` label. When both work
   at once, each uses its own git worktree, and they meet only on `api/openapi.yaml`.
4. `reviewer` reviews every PR. Blocking findings go back to the author agent (two rounds max).
5. `product` checks each PR against its issue's acceptance criteria.
6. The orchestrator presents a summary to Florian. **Stop**: Florian merges.

## Six-week plan

1. Foundations: Go project, local Postgres + pgvector, SRD chunked and indexed, CLI questions.
2. Quality: cited answers, eval set of 150 questions, first measurement, targets set.
3. Web UI: accounts, streaming answers, clickable citations, running locally
   (hosting is an open decision, see below).
4. Session notes: import per campaign, campaign questions, fictional campaign in the evals.
5. Private beta: Florian's table + 5 GMs, "wrong answer" button, fixes, evals replayed.
6. Launch: open beta (Reddit, Discord), README and article published.

## Open decisions

- Claude model for answers, and a lighter one for chunking and eval judging (after first cost measurement).
- Monthly budget cap for the API and hosting during the beta.
- Hosting: where and when Grimoire is deployed. The private beta (step 5) needs it; Cloud Run was
  the original target. Until decided, everything is local-first.
- Embedding model: local model via Ollama for now; compare with a hosted model on the eval set in step 2.
- Free beta with a per-account quota; pricing later.
- Final product name: check domain and existing RPG products named "Grimoire".

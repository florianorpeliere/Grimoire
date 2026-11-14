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

- Backend: Go, REST API, accounts and campaigns. Deployed on Cloud Run (GCP).
- Storage: PostgreSQL with pgvector (embeddings) and full-text search (keywords).
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

Proposed layout:

```
cmd/server/        API entrypoint
cmd/ingest/        SRD and notes ingestion
cmd/evals/         eval harness
internal/...       Go packages (search, llm, campaigns, auth)
api/openapi.yaml   API contract
web/               React app
evals/             eval questions and results
docs/stories/      user stories and acceptance criteria
```

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
- Small PRs, one story each. Commit messages in English, imperative mood.
- Secrets only from environment variables, never committed.

## How we work: the agent team

The main Claude Code session is the **orchestrator**. Subagents cannot start other subagents,
so delegation always goes through the main session. Run `/orchestrate <step or story>` to start.

| Agent | Role | Writes |
|---|---|---|
| `product` | Turns the plan into user stories with acceptance criteria, checks results against them | `docs/` only |
| `api` | Go backend, Postgres, search, Claude calls, ingestion, eval harness | backend code |
| `ui` | React frontend, built against `api/openapi.yaml` | `web/` only |
| `reviewer` | Runs tests and evals, reviews every change before merge | nothing (reports only) |

Flow for each step:
1. `product` writes or updates the stories in `docs/stories/`.
2. The orchestrator presents the plan to Florian. **Wait for approval.**
3. The orchestrator delegates to `api` and/or `ui`. When both work at once, each uses its own
   git worktree, and they meet only on `api/openapi.yaml`.
4. `reviewer` reviews every change. Blocking findings go back to the author agent.
5. `product` checks the result against the acceptance criteria.
6. The orchestrator presents a summary to Florian. **Wait for approval before merging.**

## Six-week plan

1. Foundations: Go project, Postgres + pgvector, SRD chunked and indexed, CLI questions.
2. Quality: cited answers, eval set of 150 questions, first measurement, targets set.
3. Web UI: accounts, streaming answers, clickable citations, deployed on Cloud Run.
4. Session notes: import per campaign, campaign questions, fictional campaign in the evals.
5. Private beta: Florian's table + 5 GMs, "wrong answer" button, fixes, evals replayed.
6. Launch: open beta (Reddit, Discord), README and article published.

## Open decisions

- Claude model for answers, and a lighter one for chunking and eval judging (after first cost measurement).
- Monthly budget cap for the API and hosting during the beta.
- Free beta with a per-account quota; pricing later.
- Final product name: check domain and existing RPG products named "Grimoire".

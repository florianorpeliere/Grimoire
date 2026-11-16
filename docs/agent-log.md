# Agent log

A running journal of how the agent team builds Grimoire: what each agent did, the round trips
with the reviewer, what blocked, and token estimates when the harness reports them.
Written by the orchestrator (main Claude Code session, Fable). Subagents run on Opus.

## Step 1 — Foundations

### 2026-11-15 — Tooling check (orchestrator)

- `gh` authenticated as `florianorpeliere`, token scopes include `project`. OK.
- `scripts/project-items.sh "Draft"`: exit 0, empty output (board empty, 0 issues in the repo). OK.
- Board Status options match CLAUDE.md: Draft, Ready, In progress, In review, Done.
- Labels `story`, `area/*`, `step/1..6` already exist.
- Go 1.27.1, Docker 29.4.3, Compose v5.1.4 installed.
- **Missing**: the Docker daemon is not running (socket not found). Not blocking for drafting
  issues; blocking as soon as an agent has to run Postgres.
- No `.gitignore` yet; `docs/stories/.gitkeep` is a leftover from before stories moved to issues.

### 2026-11-15 — Backlog drafting (phase 1 of `/orchestrate 1`)

Two agents launched in parallel:

| Agent | Task | Why |
|---|---|---|
| `product` (Opus) | Draft the step 1 stories as GitHub issues in the Draft column | Phase 1 of the flow |
| `general-purpose` (research) | Embedding model candidates with sourced prices; how to obtain SRD 5.2.1 as Markdown | Two open decisions that shape the ingestion stories; prices must be sourced, not recalled |

Orchestrator notes:
- The product agent was told not to pick the embedding model and to put every undecided point
  under "Open questions" in the issue rather than assume.
- The research agent is not one of the four team roles. It is a one-off, read-only side task:
  `product` has no web access and should not own a technical comparison.

#### `product` result

Seven stories created, #3 to #9, all `story,step/1,area/api`, all in Draft. One pass, no retry.
Harness-reported usage: 27,417 subagent tokens, 11 tool calls, about 3.6 minutes.

| # | Title | Depends on |
|---|---|---|
| 3 | Go project skeleton with local Postgres + pgvector | none |
| 4 | SRD 5.2.1 converted to Markdown, with CC-BY attribution | none |
| 5 | Chunk the SRD Markdown per rule, spell, condition and monster | #3, #4 |
| 6 | Store SRD chunks in Postgres with a full-text (keyword) index | #3, #5 |
| 7 | Embedding provider interface and SRD embedding ingestion | #6 |
| 8 | Hybrid search over SRD chunks (vector + keyword) | #6, #7 |
| 9 | CLI: ask a rules question and print the retrieved SRD passages | #8 |

Observations for the article:
- The agent followed "do not assume": it produced about 30 open questions across 7 issues.
  Good discipline, but it moves the cost to the human at the refinement stop. Many of them are
  engineering defaults (migration tool, RRF parameters) rather than product decisions.
- Its definition says "write the body to a temporary file", but it has no Write tool. It worked
  around this with `gh issue create --body-file -` and a heredoc. The agent definition and its
  tool list disagree.
- It split keyword search into #6 so that #8 only adds vector search and rank fusion: a sensible
  seam the orchestrator had not suggested.
- It did not create a story for a retrieval sanity check before the step 2 eval set (left as an
  open question on #9), as instructed.
- The dependency chain is linear from #5 on, which conflicts with "one stop before merge":
  either PRs are stacked, or the build runs in waves with a merge stop after each.

#### Research agent result

One pass. Harness-reported usage: 101,019 subagent tokens, 61 tool calls, about 6.4 minutes. Every number below was sourced
by the agent from vendor pages on 2026-11-15; the orchestrator did not re-verify them.

Embeddings:
- Price is a non-factor at SRD size: the PDF is 364 pages, about 1.38M characters; the agent
  budgets 0.6M tokens (estimate, no tokenizer run), i.e. under $0.15 with any hosted model.
- Anthropic still offers no embedding model; its docs point to Voyage AI (now under MongoDB).
- pgvector indexes `vector` up to 2,000 dimensions (`halfvec` up to 4,000).
- Two options put to Florian: (A) Voyage `voyage-4-large` at 1024 dimensions, hosted, REST only
  from Go, training opt-out to set before private notes are embedded; (B) Qwen3-Embedding-0.6B
  via Ollama, self-hosted, private by construction, but an extra service and unmeasured CPU
  latency and cold start on Cloud Run. Agent's pick: A.
- All benchmark figures found are vendor or model-card reported; no French-specific score exists.

SRD source:
- Official SRD 5.2.1 is PDF only (English, plus French, German, Spanish, Italian since 2025-12).
  The attribution statement is on page 1 of the PDF, not on the web page.
- Four community Markdown conversions inspected. Best fit: `your5e/5e-srd-markdown` (one file per
  rule, spell, monster; reproducible pipeline), but it has no licence file, 5 stars, one
  maintainer, last push 2025-10-29. Two spot checks (Fireball, Aboleth) match the PDF.
- Agent's recommendation: start from that repo pinned to a commit, validate against the PDF.

Observations for the article:
- The research changed the framing of the question asked: "which model, at what cost" became
  "cost is irrelevant, decide on quality, privacy and operations".
- The agent kept an explicit list of unverified claims (17 items), which is what makes the
  report usable for a decision.
- It surfaced a fact nobody asked for that affects the roadmap: an official French SRD exists.

### 2026-11-15 — Stop 1: refinement

Orchestrator presented to Florian: the seven drafts, default answers to the open questions, the
two embedding options, the SRD sourcing options, and the wave-versus-stacked-PR question.
Waiting for Florian to refine and move issues to Ready.

### 2026-11-15 — Stop 1, continued: the plan goes local-first

Florian's answer at the stop changed the frame: no hosting budget, everything must run on his
machine, and Docker is not required since Go, PostgreSQL and Ollama are already installed.

What the orchestrator did (no subagent: machine inspection and installs stay with the orchestrator):

- Inspected the machine: Apple M2 Pro (8 performance + 4 efficiency cores), 16 GB RAM, 94 GB free,
  Ollama 0.40.14, Go 1.27.1.
- Found that the "Postgres already installed" assumption did not hold as is: the local instance is
  PostgreSQL 13.18 (end of life), is already in use, has no pgvector, and Homebrew
  only bottles pgvector for PostgreSQL 17 and 18. Asked Florian; he chose a separate PostgreSQL 17.
- Installed `postgresql@17` (17.12) and `pgvector` (0.8.7) with Homebrew, on port 5433, created
  the `grimoire` and `grimoire_test` databases, checked that `CREATE EXTENSION vector` and a
  cosine distance query work. The PostgreSQL 13 instance on 5432 was not touched.
- Embedding model flipped from the hosted option (Voyage) to the local one: the only argument
  against self-hosting was the Cloud Run operations cost, which disappears when everything is local.
- Pulled `qwen3-embedding:0.6b` (639 MB) and measured it on this machine through Ollama's
  `/api/embed`: 1024 dimensions, 100% GPU (Metal), 1.2 GB loaded; cold load plus one embedding
  1.65 s; one warm query 0.21 s; 32 chunks of about 1,500 characters in 2.59 s (0.08 s per chunk).
  One run, not a benchmark. Ollama loaded the model with a 4,096-token context: longer chunks
  would be truncated unless the context is raised, which went into issue #7.
- Edited `CLAUDE.md` (new "Local-first" section, Cloud Run moved to open decisions), the four
  agent definitions and `/orchestrate` (local service preflight, build in waves, keep this log).
  Also fixed the `product` definition, which asked for a temporary file the agent cannot write.

Agents:

| Agent | Task |
|---|---|
| `product` (Opus), second pass | Apply Florian's decisions to issues #3 to #9, and draft a retrieval smoke-set story (`area/evals`) |

Observations for the article:
- The biggest change of the day came from a constraint the human had not written down (budget),
  not from any agent. The written brief said "Docker Compose" and "Cloud Run"; both are gone.
- "I already have Postgres" was true and still not sufficient. Checking the machine before
  rewriting the stories avoided a story built on a wrong premise.
- The orchestrator kept system-level installs to itself and wrote into the agent definitions
  that agents never install software and only use the project's own database.

Result of the second `product` pass: issues #3 to #9 updated with a comment each, and a new
story #10 "Retrieval smoke set: 20 rules questions with expected SRD chunks" (`area/evals`,
depends on #9 and #5) created in Draft. One pass, no gh or script problem.
Harness-reported usage: 42,294 subagent tokens, 14 tool calls, about 3.5 minutes.

Things the agent raised that nobody had asked for:
- In SRD 5.2.1, conditions are probably Rules Glossary entries tagged "[Condition]", not a chapter,
  so "kind derived from the chapter" (an orchestrator default) may not tell conditions apart.
  Unverified by the agent; open question on #5, to check against the Markdown from #4.
- Qwen3 embedding models are documented as expecting an instruction prefix on queries, not on
  documents. Unverified; open question on #8.
- It added criteria beyond the brief (tests only against `grimoire_test`, no default connection
  string on port 5432, duplicate chunk ids fail loudly, dimension mismatch fails loudly). Useful,
  but it is scope the human did not write: listed for Florian at the stop.

Remaining open questions: missing licence on the SRD source repo (#4); heading level for "one
rule" and condition detection (#5); dimension change on model swap (#7); query instruction
prefix (#8); monster names and runner form for the smoke set (#10).

Budget note for step 2: generation goes through the Claude API, billed per token. Rough
estimate of one 150-question eval run: between about $0.20 (Haiku 5.5) and about $7 (Opus 5.5),
from list prices and assumed token counts, not measured.

Stop 1 is still open: Florian moves issues to Ready; wave 1 is #3 and #4.

### 2026-11-15 — Stop 1, continued: chunking rule and public-repository hygiene

Florian validated the chunking rule, dropped the licence follow-up on the SRD source for now
(accepted risk), and asked for care about what goes into the public repository.

Orchestrator:
- Answered two open questions by reading the already-downloaded 5.2.1 Markdown instead of asking
  again: the four smoke-set monsters exist under the proposed names, and conditions are Rules
  Glossary entries tagged in their title, as `product` had suspected. The orchestrator's earlier
  default ("kind derived from the chapter") was wrong for conditions.
- Chunking rule validated by Florian: one chunk per file for entities; one chunk per level-2
  heading for rules chapters and classes.
- Audited tracked files, git history, PR and issue bodies for secrets. None found. Secret
  scanning and push protection were already enabled on the repository.
- Added a "Public repository" section to `CLAUDE.md`, matching rules in the four agent
  definitions (a leak is a blocking review finding), and a `.gitignore`.

| Agent | Task |
|---|---|
| `product` (Opus), third pass | Apply the chunking rule to #5, close the monster question on #10, record the licence decision on #4, make #3's README criteria generic |

Observation for the article: three `product` passes for one refinement stop. Each pass was
cheap, but the pattern is "human decides one thing, an agent rewrites issues". Batching the
decisions before the first rewrite would have saved two passes.

Result of the third `product` pass: #3, #4, #5 and #10 updated, one comment each, all eight
issues still in Draft. Harness-reported usage: 32,687 subagent tokens, 9 tool calls, about 2 minutes.
The agent also removed machine details from #3 on its own initiative under the new
"Public repository" rules, and flagged that it had only checked the four issues it touched.

Remaining open questions, none of them blocking wave 1: scripted cleanup budget (#4), dimension
change on model swap (#7), query instruction prefix (#8), form of the smoke-set runner (#10).

Observation for the article: PR #11 was merged by Florian while the orchestrator was still
working on its branch. The orchestrator noticed only because the remote `main` hash did not
match its expectation just before a push. Checking remote state before any
destructive git operation is not optional when a human and an agent share a repository.

### 2026-11-15 and 2026-11-16 — Wave 1: issues #3 and #4

Florian merged the rules PR and moved #3 and #4 to Ready. Preflight by the orchestrator: local
PostgreSQL reachable, embedding model present; `pdftotext` was missing for #4's validation and was
installed by the orchestrator (agents never install software).

Two `api` agents ran in parallel, each in its own git worktree, with a file ownership split written
into their briefs to avoid merge conflicts (#3 owns the Go module, README, Makefile; #4 only shell
scripts, its own doc and the attribution file).

#### #3 Go skeleton — PR #13

| Step | Agent | Outcome | Tokens (harness) |
|---|---|---|---|
| Build | `api` | Code written and tested, then blocked at commit (see below) | 57k |
| Resume | `api` | Commit, push, PR opened | 63k cumulative |
| Review 1 | `reviewer` | No blocking finding, 2 "should fix", 7 "nice to have" | 64k |
| Fix 1 | `api` | One commit | 98k cumulative |
| Acceptance | `product` | 7 of 7 criteria met | 42k |
| Review 2 | `reviewer` | Ready to merge; 2 one-line hardening suggestions, stale PR description | 97k cumulative |
| Final touches | `api` | One commit (33 lines), description updated | 110k cumulative |

- Blocked: commit signing was required by the developer's git settings and cannot prompt in an
  agent's non-interactive shell. The agent stopped and reported instead of bypassing it. Florian
  decided commits in this repository are unsigned.
- The orchestrator escalated two "should fix" findings to must-fix, because both could lead to
  statements on a database other than the project's: an `.env.example` placeholder that broke
  shell sourcing and let the driver fall back to its defaults, and a test reset helper that was
  safe only by convention.
- The author claimed, from reading the driver's source, that the URL wins over `PG*` environment
  variables. The reviewer was asked to prove it by running, and did.
- The reviewer bypassed the first version of the reset guard with a planted function and a custom
  search path; fixed by qualifying the call with `pg_catalog`.
- The last commit was read and tested by the orchestrator, not by the reviewer (two rounds max).
- Deferred, to raise with Florian: setup errors that look like skipped tests, migration locking,
  database tests across packages, the Down migration.
- Not verified by anyone: the README on a clean install.

#### #4 SRD Markdown — PR #14

| Step | Agent | Outcome | Tokens (harness) |
|---|---|---|---|
| Build | `api` | PR opened; scripts run end to end | 213k |
| Acceptance | `product` | 5 of 6 criteria met (documentation not in the README) | 55k |
| Review 1 | `reviewer` | Not ready: 1 blocking, 4 "should fix", 6 "nice to have" | 136k |
| Fix 1 | `api` | In progress | |

- The author deviated from the brief: it used the upstream single-file Markdown (PDF chapters as
  top-level headings) instead of the per-entity folder. Better citation paths, but it invalidates
  the chunking rule of #5, which was written for the folder. The reviewer supplied the heading
  counts per chapter; the decision is Florian's.
- Reported validation: 339 spells, 330 monster stat blocks, 15 conditions, 14 chapters on both
  sides; 99.3% of the Markdown's 8-word sequences found in the PDF. The reviewer reproduced every
  number, then showed which claims were stronger than the method: the overlap check scores about
  77% of the tokens, two "PDF typos fixed by the conversion" were in fact conversion differences,
  and the spell count has no independent list in the PDF.
- Blocking finding: a failed hash check left the mismatched file in place, and the validator did
  not check the hash of what it validated.

#### Observations for the article

- Parallel agents hit a usage limit mid-wave; two of them stopped and were resumed later with
  their context intact. Up to three agents were running at once.
- The reviewer earned its cost on both PRs by running things: it reproduced a path to the wrong
  database, broke a guard, and found that a "verified" corpus could be validated after tampering.
  None of these were visible in the authors' reports, which were accurate about what they had run.
- Authors were reliable about facts and optimistic about interpretation ("PDF typo", "no text
  from another source"). Asking the reviewer to judge whether a number is presented more
  confidently than its method supports was the most useful line in the brief.
- An agent that deviates from its brief for a good reason still creates work: one deviation in #4
  forces a rewrite of #5. The brief should have said which choices were load-bearing for later issues.
- `main` was rewritten twice during the wave for repository hygiene, while agents held branches on
  the old history. Each agent had to be told to re-base before committing. History rewrites and
  parallel agents do not mix well; do them between waves.
- The acceptance check by `product` cannot run code; it relies on the PR and the reviewer. Running
  it after the review, not in parallel, would give it better evidence.

---
description: Run one step of the Grimoire plan with the agent team (product, api, ui, reviewer), driven by GitHub issues on the project board.
argument-hint: <step number, or issue numbers>
---
You are the orchestrator of Grimoire. Read CLAUDE.md, then run: $ARGUMENTS

1. Backlog. Ask the `product` agent to draft or update the GitHub issues for this step
   (labels `story`, `step/N`, `area/...`; board column Draft).
2. Refinement. Show Florian the list of draft issues (number, title, area, dependencies):
   `scripts/project-items.sh "Draft"`.
   Stop. Florian refines them on GitHub and drags the ones he wants built to the Ready column.
   Do not continue until he tells you to.
3. Delegation. First check the local services the ready issues need (see "Local-first" in CLAUDE.md):
   `pg_isready` on the project's `DATABASE_URL`, and `ollama list` for the embedding model.
   Report anything missing to Florian instead of installing it or letting an agent discover it.
   List ready issues: `scripts/project-items.sh "Ready"`, keep those labelled `step/N`
   (or the issue numbers given). Hand each one to `api` or `ui` according to its `area/` label,
   respecting dependencies. When issues depend on each other, build in waves: run phases 3 to 6
   for the issues whose dependencies are merged, stop for Florian to merge, then start the next wave.
   Do not stack PRs on unmerged branches. If `api` and `ui` work at the same time, each uses its own git worktree;
   they meet only on `api/openapi.yaml`, which must exist and be approved before parallel work starts.
4. Review. Send every PR to the `reviewer` agent. Send blocking findings back to the author agent,
   then review again. At most two rounds; after that, escalate to Florian.
5. Acceptance. Ask the `product` agent to check each PR against its issue's acceptance criteria.
6. Summary. Present to Florian: PRs opened, test and eval results, reviewer verdicts, open questions.
   Stop. Florian merges; the board moves the card to Done when the issue closes.

Keep `docs/agent-log.md` up to date at every phase: what each agent did, reviewer round trips,
what blocked, and token usage when the harness reports it.

Never skip the two stops (the merge stop happens once per wave). Never work on an issue that is not in the Ready column.
Never expand the scope beyond the issues and CLAUDE.md.

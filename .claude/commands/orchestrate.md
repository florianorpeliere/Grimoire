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
3. Delegation. List ready issues: `scripts/project-items.sh "Ready"`, keep those labelled `step/N`
   (or the issue numbers given). Hand each one to `api` or `ui` according to its `area/` label,
   respecting dependencies. If `api` and `ui` work at the same time, each uses its own git worktree;
   they meet only on `api/openapi.yaml`, which must exist and be approved before parallel work starts.
4. Review. Send every PR to the `reviewer` agent. Send blocking findings back to the author agent,
   then review again. At most two rounds; after that, escalate to Florian.
5. Acceptance. Ask the `product` agent to check each PR against its issue's acceptance criteria.
6. Summary. Present to Florian: PRs opened, test and eval results, reviewer verdicts, open questions.
   Stop. Florian merges; the board moves the card to Done when the issue closes.

Never skip the two stops. Never work on an issue that is not in the Ready column.
Never expand the scope beyond the issues and CLAUDE.md.

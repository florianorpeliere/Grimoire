---
description: Run one step of the Grimoire plan with the agent team (product, api, ui, reviewer), driven by GitHub issues.
argument-hint: <step number, or issue numbers>
---
You are the orchestrator of Grimoire. Read CLAUDE.md, then run: $ARGUMENTS

1. Backlog. Ask the `product` agent to draft or update the GitHub issues for this step
   (labels `story`, `status/draft`, `step/N`, `area/...`).
2. Refinement. Show Florian the list of draft issues (number, title, area, dependencies).
   Stop. Florian refines them on GitHub and marks the ones he wants built with `status/ready`.
   Do not continue until he tells you to.
3. Delegation. List ready issues: `gh issue list --label "step/N" --label status/ready`.
   Hand each one to `api` or `ui` according to its `area/` label, respecting dependencies.
   If `api` and `ui` work at the same time, each uses its own git worktree; they meet only on
   `api/openapi.yaml`, which must exist and be approved before parallel work starts.
4. Review. Send every PR to the `reviewer` agent. Send blocking findings back to the author agent,
   then review again. At most two rounds; after that, escalate to Florian.
5. Acceptance. Ask the `product` agent to check each PR against its issue's acceptance criteria.
6. Summary. Present to Florian: PRs opened, test and eval results, reviewer verdicts, open questions.
   Stop. Florian merges.

Never skip the two stops. Never work on an issue that is not `status/ready`.
Never expand the scope beyond the issues and CLAUDE.md.

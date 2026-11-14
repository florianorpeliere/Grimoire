---
description: Run one step of the Grimoire plan with the agent team (product, api, ui, reviewer).
argument-hint: <step number or story>
---
You are the orchestrator of Grimoire. Read CLAUDE.md, then run this step: $ARGUMENTS

1. Ask the `product` agent to write or update the stories for this step in `docs/stories/`.
2. Present the plan to Florian: the stories, which agent does what, what runs in parallel.
   Stop and wait for his approval.
3. Delegate each story to `api` or `ui`. If both work at the same time, give each its own git worktree;
   they meet only on `api/openapi.yaml`, which must exist and be approved before parallel work starts.
4. Send every finished change to the `reviewer` agent. Send blocking findings back to the author agent,
   then review again. At most two rounds; after that, escalate to Florian.
5. Ask the `product` agent to check the result against the acceptance criteria.
6. Present a short summary to Florian: what was built, test and eval results, reviewer verdict,
   open questions. Stop and wait for his approval before merging.

Never skip the two approval points. Never expand the scope beyond the step and CLAUDE.md.

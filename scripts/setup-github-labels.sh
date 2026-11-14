#!/usr/bin/env bash
# Creates the labels the agent team relies on. Safe to re-run (--force updates existing labels).
# Status is not a label: it is the Status column of the project board (see CLAUDE.md).
set -euo pipefail

gh label create "story"      --color "5319e7" --description "User story"           --force
gh label create "area/api"   --color "c5def5" --description "Go backend"           --force
gh label create "area/ui"    --color "c5def5" --description "React frontend"       --force
gh label create "area/evals" --color "c5def5" --description "Eval set and harness" --force

for step in 1 2 3 4 5 6; do
  gh label create "step/${step}" --color "ededed" --description "Week ${step} of the V1 plan" --force
done

# Remove the old status labels if they exist (status now lives on the project board).
for old in "status/draft" "status/ready" "status/in-progress" "status/in-review"; do
  gh label delete "$old" --yes 2>/dev/null || true
done

echo "Labels ready."

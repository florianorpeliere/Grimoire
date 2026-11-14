#!/usr/bin/env bash
# Creates the labels the agent team relies on. Safe to re-run (--force updates existing labels).
set -euo pipefail

gh label create "story"              --color "5319e7" --description "User story"                    --force
gh label create "status/draft"       --color "d4c5f9" --description "Written by product, not ready"  --force
gh label create "status/ready"       --color "0e8a16" --description "Refined by Florian, can be built" --force
gh label create "status/in-progress" --color "fbca04" --description "Being built by an agent"        --force
gh label create "status/in-review"   --color "1d76db" --description "PR open, under review"          --force
gh label create "area/api"           --color "c5def5" --description "Go backend"                     --force
gh label create "area/ui"            --color "c5def5" --description "React frontend"                 --force
gh label create "area/evals"         --color "c5def5" --description "Eval set and harness"           --force

for step in 1 2 3 4 5 6; do
  gh label create "step/${step}" --color "ededed" --description "Week ${step} of the V1 plan" --force
done

echo "Labels ready."

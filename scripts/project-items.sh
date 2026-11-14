#!/usr/bin/env bash
# Lists the issues in one column of the Grimoire project board, one per line: "#<number> <title>".
# Usage: scripts/project-items.sh "<Draft|Ready|In progress|In review|Done>"
# Requires: gh auth refresh -s project
set -euo pipefail

OWNER="${GRIMOIRE_PROJECT_OWNER:-florianorpeliere}"
NUMBER="${GRIMOIRE_PROJECT_NUMBER:-6}"

if [ "$#" -ne 1 ]; then
  echo "Usage: $0 \"<Draft|Ready|In progress|In review|Done>\"" >&2
  exit 1
fi
status="$1"

gh project item-list "$NUMBER" --owner "$OWNER" --format json --limit 500 \
  --jq ".items[] | select(.content.type == \"Issue\" and .status == \"$status\") | \"#\(.content.number) \(.content.title)\""

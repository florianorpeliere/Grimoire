#!/usr/bin/env bash
# Moves an issue to a column of the Grimoire project board (adds it to the board if needed).
# Usage: scripts/project-status.sh <issue-number> "<Draft|Ready|In progress|In review|Done>"
# Requires: gh auth refresh -s project
set -euo pipefail

OWNER="${GRIMOIRE_PROJECT_OWNER:-florianorpeliere}"
NUMBER="${GRIMOIRE_PROJECT_NUMBER:-6}"

if [ "$#" -ne 2 ]; then
  echo "Usage: $0 <issue-number> \"<Draft|Ready|In progress|In review|Done>\"" >&2
  exit 1
fi
issue="$1"
status="$2"

repo="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"
project_id="$(gh project view "$NUMBER" --owner "$OWNER" --format json --jq .id)"
field_id="$(gh project field-list "$NUMBER" --owner "$OWNER" --format json \
  --jq '.fields[] | select(.name == "Status") | .id')"
option_id="$(gh project field-list "$NUMBER" --owner "$OWNER" --format json \
  --jq ".fields[] | select(.name == \"Status\") | .options[] | select(.name == \"$status\") | .id")"

if [ -z "$option_id" ]; then
  echo "Unknown status \"$status\". Check the Status column names on the board." >&2
  exit 1
fi

item_id="$(gh project item-list "$NUMBER" --owner "$OWNER" --format json --limit 500 \
  --jq ".items[] | select(.content.type == \"Issue\" and .content.number == $issue) | .id")"

if [ -z "$item_id" ]; then
  item_id="$(gh project item-add "$NUMBER" --owner "$OWNER" \
    --url "https://github.com/$repo/issues/$issue" --format json --jq .id)"
fi

gh project item-edit --id "$item_id" --project-id "$project_id" \
  --field-id "$field_id" --single-select-option-id "$option_id" > /dev/null

echo "#$issue -> $status"

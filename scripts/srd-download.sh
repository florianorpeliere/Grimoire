#!/usr/bin/env bash
# Downloads the SRD 5.2.1 Markdown conversion at the pinned commit and applies the scripted cleanup.
# Usage: scripts/srd-download.sh
# Output: data/srd/SRD_CC_v5.2.1.md (git-ignored, never committed). See docs/srd-source.md.
# Requires: git, awk, shasum or sha256sum.
# The downloaded repository is untrusted data: nothing from it is executed, only one Markdown file is read.
set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck source=scripts/srd-source.env
. scripts/srd-source.env

DEST="data/srd"
SRC="$DEST/source"
OUT="$DEST/SRD_CC_v5.2.1.md"

fail() { echo "srd-download: $*" >&2; exit 1; }

sha256() {
  if command -v shasum > /dev/null 2>&1; then shasum -a 256 "$1" | cut -d ' ' -f 1
  elif command -v sha256sum > /dev/null 2>&1; then sha256sum "$1" | cut -d ' ' -f 1
  else fail "neither shasum nor sha256sum is installed"
  fi
}

command -v git > /dev/null 2>&1 || fail "git is not installed"
git check-ignore -q "$OUT" || fail "$DEST is not git-ignored; refusing to download third-party content there"

# A previous output is removed first: whatever happens below, data/srd/ never keeps a Markdown file
# that was not verified against the pin.
rm -f "$OUT" "$OUT.tmp" "$OUT.unverified"

# 1. Fetch only the pinned commit, without hooks, and read the one file as a git blob: no working
#    tree, so no checkout filters and no symlink can be followed.
rm -rf "$SRC"
mkdir -p "$SRC"
git init -q "$SRC"
git -C "$SRC" remote add origin "$SRD_REPO_URL"
git -C "$SRC" -c core.hooksPath=/dev/null fetch -q --depth 1 --filter=blob:none origin "$SRD_COMMIT" \
  || fail "could not fetch commit $SRD_COMMIT from $SRD_REPO_URL"

actual="$(git -C "$SRC" rev-parse FETCH_HEAD)"
[ "$actual" = "$SRD_COMMIT" ] || fail "fetched commit is $actual, expected $SRD_COMMIT"
mode="$(git -C "$SRC" ls-tree "$SRD_COMMIT" -- "$SRD_REPO_FILE" | cut -d ' ' -f 1)"
[ "$mode" = "100644" ] || fail "$SRD_REPO_FILE is not a regular file at commit $SRD_COMMIT (mode '$mode')"

# 2. Scripted cleanup (the only transformation, documented in docs/srd-source.md):
#    the PDF has "Animals" as a chapter of its own (same heading size as "Monsters A–Z", listed as a
#    chapter in the table of contents), but the conversion nests it as "## Animals" under
#    "# Monsters A-Z". Promote "## Animals" and every heading after it by one level.
#    The file has no code blocks, so every line starting with "#" is a heading.
git -C "$SRC" cat-file blob "$SRD_COMMIT:$SRD_REPO_FILE" | awk '
  /^# Monsters A-Z$/ { in_az = 1 }
  in_az && $0 == "## Animals" { found++; promote = 1 }
  promote && /^#+ / {
    if ($0 ~ /^# /) { print "unexpected chapter after Animals: " $0 > "/dev/stderr"; bad = 1 }
    sub(/^#/, "")
  }
  { print }
  END {
    if (found != 1) { print "expected exactly one \"## Animals\" heading under \"# Monsters A-Z\", found " found + 0 > "/dev/stderr"; bad = 1 }
    exit bad
  }
' > "$OUT.tmp" || { rm -f "$OUT.tmp"; fail "reading or cleaning $SRD_REPO_FILE failed"; }

# 3. Reproducibility check: the result must be byte-identical on every run. It is moved into place
#    only on a match; otherwise it is kept as .unverified (for reviewing a pin change) and nothing
#    is left at the canonical path.
digest="$(sha256 "$OUT.tmp")"
if [ "$digest" != "$SRD_MARKDOWN_SHA256" ]; then
  mv "$OUT.tmp" "$OUT.unverified"
  fail "SHA-256 of the cleaned Markdown is $digest, expected $SRD_MARKDOWN_SHA256. Nothing written to $OUT; the result is kept as $OUT.unverified for review (see docs/srd-source.md)"
fi
mv "$OUT.tmp" "$OUT"

echo "Source:     $SRD_REPO_URL"
echo "Commit:     $actual (matches the pin)"
echo "Read file:  $SRD_REPO_FILE"
echo "Cleanup:    \"Animals\" promoted to a chapter heading"
echo "Output:     $OUT ($(wc -l < "$OUT" | tr -d ' ') lines, $(wc -c < "$OUT" | tr -d ' ') bytes)"
echo "SHA-256:    $digest (matches the pin)"
echo "Chapters:   $(grep -c '^# ' "$OUT") top-level headings"

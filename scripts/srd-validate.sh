#!/usr/bin/env bash
# Compares the SRD 5.2.1 Markdown with the official PDF and prints the measurements.
# Measurement only: there is no target and no pass/fail threshold on fidelity.
# Usage: scripts/srd-validate.sh   (run scripts/srd-download.sh first)
# Requires: pdftotext (from poppler, needed by this script only), curl, awk, diff, comm, sort,
# shasum or sha256sum. Downloads the PDF to data/srd/ (git-ignored) if it is not there yet.
# Exits non-zero only when it cannot measure (missing tool or file, Markdown or PDF checksum not the pinned one).
set -euo pipefail

cd "$(dirname "$0")/.."
# shellcheck source=scripts/srd-source.env
. scripts/srd-source.env

DEST="data/srd"
MD="$DEST/SRD_CC_v5.2.1.md"
PDF="$DEST/SRD_CC_v5.2.1.pdf"
WORK="$DEST/validate"
export LC_ALL=C

# Chapters of the PDF table of contents (page 2) with their first page. A title counts as a chapter
# when the PDF prints it at chapter size (31 pt heading, versus 21.5 pt for sections); "Monsters A–Z"
# and "Animals" are printed and listed at that level.
CHAPTERS='1|Legal Information
5|Playing the Game
19|Character Creation
28|Classes
83|Character Origins
87|Feats
89|Equipment
104|Spells
176|Rules Glossary
192|Gameplay Toolbox
204|Magic Items
254|Monsters
258|Monsters A–Z
344|Animals'

# Sampled sections: label|Markdown chapter|exact Markdown heading line.
SAMPLES='General rule|Playing the Game|### Advantage/Disadvantage
Spell|Spells|### Fireball
Condition|Rules Glossary|### Prone \[Condition\]
Monster stat block|Monsters A-Z|### Goblin Warrior
Table (XP by CR)|Monsters|#### Experience Points'

fail() { echo "srd-validate: $*" >&2; exit 1; }

sha256() {
  if command -v shasum > /dev/null 2>&1; then shasum -a 256 "$1" | cut -d ' ' -f 1
  elif command -v sha256sum > /dev/null 2>&1; then sha256sum "$1" | cut -d ' ' -f 1
  else fail "neither shasum nor sha256sum is installed"
  fi
}

command -v pdftotext > /dev/null 2>&1 || fail "pdftotext not found. It is part of poppler: install poppler with your package manager (for example 'brew install poppler' or 'apt install poppler-utils') and re-run."
command -v curl > /dev/null 2>&1 || fail "curl not found"
[ -f "$MD" ] || fail "$MD not found: run scripts/srd-download.sh first"
md_digest="$(sha256 "$MD")"
[ "$md_digest" = "$SRD_MARKDOWN_SHA256" ] || fail "SHA-256 of $MD is $md_digest, expected $SRD_MARKDOWN_SHA256: it is not the pinned output. Run scripts/srd-download.sh again."

if [ ! -f "$PDF" ]; then
  curl -fsSL -o "$PDF.tmp" "$SRD_PDF_URL" || fail "could not download $SRD_PDF_URL"
  mv "$PDF.tmp" "$PDF"
fi
digest="$(sha256 "$PDF")"
[ "$digest" = "$SRD_PDF_SHA256" ] || fail "SHA-256 of $PDF is $digest, expected $SRD_PDF_SHA256. Delete $PDF and re-run to download it again."

rm -rf "$WORK"
mkdir -p "$WORK"
pdftotext -enc UTF-8 "$PDF" "$WORK/pdf.txt"
printf '%s\n' "$CHAPTERS" > "$WORK/chapters.txt"

# Shared awk helpers. name(): typographic normalisation for titles and names (quotes, dashes,
# Markdown escapes, spacing). tok(): lower-case word tokens; a sign directly before a digit is kept.
AWK_LIB='
function name(s) {
  gsub(/\r|\f/, "", s); gsub(/\\/, "", s)
  gsub(/’|‘/, "\047", s); gsub(/“|”/, "\"", s); gsub(/–|—|−/, "-", s); gsub(/…/, "...", s)
  gsub(/[ \t]+/, " ", s); sub(/^ /, "", s); sub(/ $/, "", s)
  return s
}
function tok(s,   c) {
  s = " " tolower(name(s))
  while (match(s, /[^a-z0-9][-+][0-9]/)) {
    c = substr(s, RSTART + 1, 1)
    s = substr(s, 1, RSTART) (c == "+" ? "P" : "M") substr(s, RSTART + 2)
  }
  gsub(/[^a-zA-Z0-9]+/, " ", s); gsub(/P/, "+", s); gsub(/M/, "-", s)
  sub(/^ /, "", s); sub(/ $/, "", s)
  return s
}
function level(s) { match(s, /^#+/); return RLENGTH }
'

# PDF text as "page<TAB>line". The running header and the page number line are dropped.
awk 'BEGIN { p = 1 }
  { n = split($0, part, "\f")
    for (k = 1; k <= n; k++) {
      if (k > 1) { p++; seen = 0 }
      l = part[k]; t = l; gsub(/^[ \t]+|[ \t]+$/, "", t)
      if (t == "System Reference Document 5.2.1") continue
      if (!seen && t == p "") { seen = 1; continue }
      print p "\t" l
    } }' "$WORK/pdf.txt" > "$WORK/pdf.lines"
pages="$(awk -F '\t' '$2 ~ /[^ \t]/ { p = $1 } END { print p }' "$WORK/pdf.lines")"

# Markdown as "line<TAB>chapter<TAB>kind<TAB>text", kind = heading, table or text.
awk -v OFS='\t' '
  /^# / { ch = substr($0, 3) }
  { kind = /^#+ / ? "heading" : (/^\|/ ? "table" : "text"); print NR, ch, kind, $0 }' "$MD" > "$WORK/md.lines"

echo "SRD 5.2.1 Markdown vs official PDF (measurement only, no threshold)"
echo "Markdown: $MD ($(wc -l < "$MD" | tr -d ' ') lines, SHA-256 matches the pin), output of commit $SRD_COMMIT"
echo "PDF:      $PDF ($pages pages with text, SHA-256 matches the pin), text extracted with $(pdftotext -v 2>&1 | head -1)"

echo
echo "== Attribution (ATTRIBUTION.md vs PDF page 1, compared with all whitespace removed)"
pdf_p1="$(awk -F '\t' '$1 == 1 { printf "%s", $2 }' "$WORK/pdf.lines" | tr -d ' \t\n')"
statement="$(grep '^This work includes material' ATTRIBUTION.md | tr -d ' \t\n')"
[ -n "$statement" ] || fail "no attribution statement found in ATTRIBUTION.md"
case "$pdf_p1" in
  *"$statement"*) echo "statement verbatim in PDF page 1: yes" ;;
  *) echo "statement verbatim in PDF page 1: NO" ;;
esac
if grep -q "$SRD_COMMIT" ATTRIBUTION.md; then echo "pinned commit named in ATTRIBUTION.md: yes"; else echo "pinned commit named in ATTRIBUTION.md: NO"; fi

echo
echo "== Chapters (PDF table of contents vs Markdown top-level headings)"
awk -F '\t' "$AWK_LIB"'
  FILENAME ~ /chapters/ { split($0, c, "|"); n++; page[n] = c[1]; t[n] = name(c[2]); next }
  FILENAME ~ /pdf.lines/ { l = name($2)
    if ($1 >= 2 && $1 <= 4) toc[l] = 1
    pl[$1 SUBSEP l] = 1; if ($1 == pp) pl[$1 SUBSEP prev " " l] = 1
    pp = $1; prev = l; next }
  $3 == "heading" && $4 ~ /^# / { h = name(substr($4, 3)); h1[h] = $1; order[++m] = h }
  END {
    printf "%-5s %-7s %-8s %-28s %s\n", "page", "in TOC", "on page", "Markdown H1 (line)", "PDF chapter"
    for (i = 1; i <= n; i++) {
      intoc = "no"; for (l in toc) if (l ~ ("(^|[0-9] )" t[i] "[ .]*\\.\\.")) intoc = "yes"
      onp = ((page[i] SUBSEP t[i]) in pl) ? "yes" : "no"
      md = (t[i] in h1) ? "# " t[i] " (" h1[t[i]] ")" : "MISSING"
      printf "%-5s %-7s %-8s %-28s %s\n", page[i], intoc, onp, md, t[i]
      want[t[i]] = 1
    }
    extra = 0; for (j = 1; j <= m; j++) if (!(order[j] in want)) { print "extra Markdown H1: " order[j]; extra++ }
    print "Markdown H1 headings: " m ", PDF chapters listed: " n ", extra H1: " extra
    print "(titles compared after normalising dashes and quotes; in TOC = followed by leader dots on a"
    print " contents line; on page = a whole line, or two consecutive lines, of its first page)"
  }' "$WORK/chapters.txt" "$WORK/pdf.lines" "$WORK/md.lines"
jumps="$(awk -F '\t' "$AWK_LIB"'$3 == "heading" { l = level($4); if (l > prev + 1) n++; prev = l } END { print n + 0 }' "$WORK/md.lines")"
echo "heading level jumps of more than one level (e.g. # then ###): $jumps"

echo
echo "== Entity counts"
SCHOOL='(Abjuration|Conjuration|Divination|Enchantment|Evocation|Illusion|Necromancy|Transmutation)'
# Markdown side.
awk -F '\t' "$AWK_LIB"'
  $3 == "heading" && level($4) == 2 { sd = ($2 == "Spells" && $4 == "## Spell Descriptions") }
  $3 == "heading" && level($4) == 1 { sd = 0 }
  sd && $3 == "heading" && level($4) == 3 { print name(substr($4, 5)) > "'"$WORK"'/md.spells" }
  $2 == "Monsters A-Z" && $3 == "heading" && level($4) == 3 { print name(substr($4, 5)) > "'"$WORK"'/md.monsters" }
  $2 == "Animals" && $3 == "heading" && level($4) == 2 { print name(substr($4, 4)) > "'"$WORK"'/md.monsters" }
  $3 == "heading" && $4 ~ /\\\[Condition\\\]$/ { print name(substr($4, level($4) + 2)) > "'"$WORK"'/md.conditions" }
' "$WORK/md.lines"
md_school="$(awk -F '\t' '$2 == "Spells"' "$WORK/md.lines" | cut -f 4 | grep -cE "^_(Level [1-9] $SCHOOL|$SCHOOL Cantrip) \(" || true)"
md_ac_mon="$(awk -F '\t' '$2 == "Monsters A-Z" || $2 == "Animals"' "$WORK/md.lines" | cut -f 4 | grep -c '^- \*\*AC\*\* ' || true)"
md_ac_other="$(awk -F '\t' '$2 != "Monsters A-Z" && $2 != "Animals"' "$WORK/md.lines" | cut -f 4 | grep -c '^- \*\*AC\*\* ' || true)"
md_ac_a="$(awk -F '\t' '$2 == "Monsters A-Z"' "$WORK/md.lines" | cut -f 4 | grep -c '^- \*\*AC\*\* ' || true)"
md_ac_b="$(awk -F '\t' '$2 == "Animals"' "$WORK/md.lines" | cut -f 4 | grep -c '^- \*\*AC\*\* ' || true)"
# PDF side.
pdf_pages() { awk -F '\t' -v a="$1" -v b="$2" '$1 >= a && $1 <= b { print $2 }' "$WORK/pdf.lines"; }
pdf_school="$(pdf_pages 104 175 | grep -cE "^(Level [1-9] $SCHOOL|$SCHOOL Cantrip) \(" || true)"
pdf_ac_mon="$(pdf_pages 258 "$pages" | grep -cE '^AC [0-9]+([[:space:]]|$)' || true)"
pdf_ac_other="$(pdf_pages 5 257 | grep -cE '^AC [0-9]+([[:space:]]|$)' || true)"
awk -F '\t' "$AWK_LIB"'$1 >= 2 && $1 <= 4 && name($2) == "Index of Stat" { on = 1; next }
  on && $1 >= 2 && $1 <= 4 && $2 ~ /\.\.+[ \t]*[0-9]+[ \t]*$/ { l = name($2); sub(/[ .]*\.\.+ ?[0-9]+$/, "", l); sub(/\.$/, "", l); print l }' \
  "$WORK/pdf.lines" | sort > "$WORK/pdf.index"
pdf_pages 104 175 | awk "$AWK_LIB"'{ print name($0) }' | sort -u > "$WORK/pdf.spell-lines"
pdf_pages 176 191 | awk "$AWK_LIB"'/\[Condition\][ \t]*$/ { print name($0) }' | sort > "$WORK/pdf.conditions"
sort "$WORK/md.monsters" -o "$WORK/md.monsters"
sort "$WORK/md.conditions" -o "$WORK/md.conditions"
sort -u "$WORK/md.spells" > "$WORK/md.spells.sorted"

echo "Spells"
echo "  Markdown: $(wc -l < "$WORK/md.spells" | tr -d ' ') headings under '## Spell Descriptions', $md_school school/level lines"
echo "  PDF:      $pdf_school school/level lines (e.g. 'Level 3 Evocation (' or 'Evocation Cantrip (') on pages 104-175"
echo "  Markdown spell names not found as a whole line on PDF pages 104-175: $(comm -23 "$WORK/md.spells.sorted" "$WORK/pdf.spell-lines" | wc -l | tr -d ' ')"
comm -23 "$WORK/md.spells.sorted" "$WORK/pdf.spell-lines" | sed 's/^/    /'
echo "Monsters (stat blocks in 'Monsters A–Z' and 'Animals')"
echo "  Markdown: $(wc -l < "$WORK/md.monsters" | tr -d ' ') stat block headings, $md_ac_mon '- **AC**' lines ($md_ac_a in Monsters A-Z, $md_ac_b in Animals)"
echo "  PDF:      $(wc -l < "$WORK/pdf.index" | tr -d ' ') entries in the 'Index of Stat Blocks' (pages 2-4), $pdf_ac_mon lines starting 'AC <number>' on pages 258-$pages"
echo "  Names in the PDF index but not in the Markdown headings: $(comm -23 "$WORK/pdf.index" "$WORK/md.monsters" | wc -l | tr -d ' ')"
comm -23 "$WORK/pdf.index" "$WORK/md.monsters" | sed 's/^/    /'
echo "  Names in the Markdown headings but not in the PDF index: $(comm -13 "$WORK/pdf.index" "$WORK/md.monsters" | wc -l | tr -d ' ')"
comm -13 "$WORK/pdf.index" "$WORK/md.monsters" | sed 's/^/    /'
echo "  Other stat blocks (spells, magic items, toolbox): Markdown $md_ac_other '- **AC**' lines, PDF $pdf_ac_other 'AC <number>' lines on pages 5-257"
echo "Conditions"
echo "  Markdown: $(wc -l < "$WORK/md.conditions" | tr -d ' ') headings ending '[Condition]'; PDF: $(wc -l < "$WORK/pdf.conditions" | tr -d ' ') lines ending '[Condition]' on pages 176-191"
echo "  Name differences: $(comm -3 "$WORK/pdf.conditions" "$WORK/md.conditions" | wc -l | tr -d ' ')"
if [ -d "$DEST/source/.git" ]; then
  tree() { git -C "$DEST/source" ls-tree -r --name-only HEAD -- "$1"; }
  echo "Cross-check with the per-file folder of the same commit (git tree only, not downloaded)"
  echo "  dnd/521/markdown/Monsters: $(tree dnd/521/markdown/Monsters | grep -c '\.md$') files"
  echo "  dnd/521/markdown/Spells:   $(tree dnd/521/markdown/Spells | grep -c '\.md$') .md files, of which Spells/Spells.md is the introduction"
fi

echo
echo "== Sampled sections (word tokens, lower case, punctuation and Markdown syntax removed)"
awk -F '\t' "$AWK_LIB"'{ n = split(tok($2), w, " "); for (i = 1; i <= n; i++) print $1 "\t" w[i] }' "$WORK/pdf.lines" > "$WORK/pdf.tok"
i=0
while IFS='|' read -r label chapter heading; do
  i=$((i + 1))
  s="$WORK/sample$i"
  # The Markdown section runs from its heading to the next heading of the same or a higher level.
  # Output: "<Markdown line number><TAB><token>".
  CH="$chapter" HD="$heading" awk -F '\t' "$AWK_LIB"'
    $2 != ENVIRON["CH"] { next }
    on && $3 == "heading" && level($4) <= lv { exit }
    $4 == ENVIRON["HD"] && !on { on = 1; lv = level($4) }
    on && $4 !~ /^\|[-:| ]+\|$/ { n = split(tok($4), w, " "); for (j = 1; j <= n; j++) print $1 "\t" w[j] }
  ' "$WORK/md.lines" > "$s.mdl"
  echo
  echo "-- $label: '$heading' in '# $chapter'"
  if [ ! -s "$s.mdl" ]; then echo "   Markdown section not found"; continue; fi
  # Alignment with the PDF text. The PDF columns, sidebars and side-by-side tables do not follow the
  # Markdown order, so the section is aligned piece by piece instead of as one block:
  # - the section starts where its heading and first words (8 tokens, down to 2) first appear
  #   within the pages of its chapter;
  # - from there, the next unaligned Markdown tokens (8, down to 2) are looked up at the position
  #   closest to the current one, then matched token by token as far as they agree;
  # - PDF tokens skipped by a jump of at most 20 tokens forward, and never matched elsewhere, count
  #   as PDF-only (a real difference); longer or backward jumps are layout and are not counted;
  # - Markdown tokens that cannot be found count as Markdown-only.
  range="$(CH="$chapter" awk -F '|' -v last="$pages" '{ t = $2; gsub(/–/, "-", t); s[NR] = $1; n[NR] = t }
    END { for (i = 1; i <= NR; i++) if (n[i] == ENVIRON["CH"]) print s[i], (i < NR ? s[i + 1] - 1 : last) }' "$WORK/chapters.txt")"
  : > "$s.pdf"
  if ! awk -F '\t' -v out="$s" -v range="$range" '
    NR == FNR { m[++M] = $2; next }
    { p[++N] = $2; pg[N] = $1 }
    function at(i, from, k,   j) { for (j = 0; j < k; j++) if (p[i + j] != m[from + j]) return 0; return 1 }
    function near(from, k, expect,   i, best, d, bd) {
      bd = -1
      for (i = lo; i <= hi - k + 1; i++) if (at(i, from, k)) { d = i - expect; if (d < 0) d = -d; if (bd < 0 || d < bd) { bd = d; best = i } }
      return best + 0
    }
    END {
      split(range, r, " ")
      for (i = 1; i <= N; i++) { if (!a && pg[i] >= r[1]) a = i; if (pg[i] <= r[2]) b = i }
      for (K = (M < 8 ? M : 8); K >= 2 && !s0; K--) for (i = a; i <= b - K + 1; i++) if (at(i, 1, K)) { s0 = i; break }
      if (!s0) exit 2
      lo = a - 2000; if (lo < 1) lo = 1
      hi = b + 2000; if (hi > N) hi = N
      # Pass 1: align, recording events (gap = skipped PDF positions, md = unmatched Markdown token,
      # match = Markdown token t matched to PDF position).
      cur = s0; t = 1
      while (t <= M) {
        R = M - t + 1; K = (R < 8) ? R : 8; pos = 0
        for (k = K; k >= (R < 2 ? R : 2) && !pos; k--) pos = near(t, k, cur)
        if (!pos) { ev[++ne] = "md " t; t++; continue }
        if (pos > cur && pos - cur <= 20) ev[++ne] = "gap " cur " " pos - 1
        else if (pos != cur) jumps++
        while (t <= M && pos <= N && p[pos] == m[t]) { ev[++ne] = "match " t " " pos; used[pos] = 1; pos++; t++ }
        cur = pos
      }
      # Pass 2: write both token streams in alignment order.
      for (q = 1; q <= ne; q++) {
        split(ev[q], e, " ")
        if (e[1] == "md") print m[e[2]] > (out ".md")
        else if (e[1] == "match") { print m[e[2]] > (out ".md"); print p[e[3]] > (out ".pdf") }
        else for (x = e[2]; x <= e[3]; x++) if (!used[x]) print p[x] > (out ".pdf")
      }
      printf "starts on PDF page %d; %d layout jumps\n", pg[s0], jumps > (out ".how")
    }' "$s.mdl" "$WORK/pdf.tok"; then
    echo "   section start not located in the PDF text"
    continue
  fi
  touch "$s.md"
  diff "$s.md" "$s.pdf" > "$s.diff" || true
  echo "   $(cat "$s.how")"
  echo "   tokens: Markdown $(wc -l < "$s.md" | tr -d ' '), aligned PDF $(wc -l < "$s.pdf" | tr -d ' ')"
  echo "   differences: $(grep -c '^<' "$s.diff" || true) Markdown-only, $(grep -c '^>' "$s.diff" || true) PDF-only tokens"
  echo "   difference hunks (at most 12, each side cut at 25 tokens):"
  awk '
    function cut(s,   n, w, r, k) { n = split(s, w, " "); r = ""; for (k = 1; k <= n && k <= 25; k++) r = r (k > 1 ? " " : "") w[k]; return r (n > 25 ? " ..." : "") }
    function flush() { if (h) print "     md: [" cut(a) "]  pdf: [" cut(b) "]" }
    /^[0-9]/ { flush(); h++; a = b = ""; next }
    /^</ { a = a " " substr($0, 3) } /^>/ { b = b " " substr($0, 3) }
    END { flush() }' "$s.diff" | head -12
done <<< "$SAMPLES"

echo
echo "== Whole-document overlap (8-word sequences; tables reorder words, so they lower the figures)"
awk -F '\t' "$AWK_LIB"'
  NR == FNR { n = split(tok($2), w, " "); for (i = 1; i <= n; i++) { P[++np] = w[i]; pg[np] = $1 }; next }
  { n = split(tok($4), w, " "); for (i = 1; i <= n; i++) { Mt[++nm] = w[i]; ml[nm] = FNR }
    lch[FNR] = $2; lkind[FNR] = $3; ltext[FNR] = $4; lfirst[FNR] = nm - n + 1; lcount[FNR] = n; nl = FNR }
  function gram(T, i,   g, k) { g = T[i]; for (k = 1; k < 8; k++) g = g " " T[i + k]; return g }
  END {
    for (i = 1; i + 7 <= np; i++) inpdf[gram(P, i)] = 1
    for (i = 1; i + 7 <= nm; i++) inmd[gram(Mt, i)] = 1
    # Markdown text paragraphs (not headings, not tables) with at least 8 tokens.
    for (l = 1; l <= nl; l++) {
      cat = (lkind[l] == "text") ? (lcount[l] < 8 ? "short or blank text lines (not scored)" : "text lines of 8+ tokens (scored)") : (lkind[l] == "table" ? "table rows (not scored)" : "headings (not scored)")
      if (!(cat in clines)) cord[++ncat] = cat
      clines[cat]++; ctok[cat] += lcount[l]; alltok += lcount[l]
      if (lkind[l] != "text" || lcount[l] < 8) continue
      c = lch[l]; if (!(c in seen)) { seen[c] = 1; chs[++nc] = c }
      tot = found = 0
      for (i = lfirst[l]; i + 7 < lfirst[l] + lcount[l]; i++) { tot++; if (gram(Mt, i) in inpdf) found++ }
      T[c] += tot; F[c] += found; para[c]++
      if (found / tot < 0.5) { low[c]++; nlow++; if (nlow <= 15) ex[nlow] = sprintf("line %d (%d%%): %s", l, 100 * found / tot, substr(ltext[l], 1, 110)) }
    }
    print "Markdown -> PDF coverage: which Markdown tokens this check scores (tokens as in the samples)"
    printf "  %-40s %8s %9s %s\n", "part of the Markdown", "lines", "tokens", "share of tokens"
    for (j = 1; j <= ncat; j++) { c = cord[j]; printf "  %-40s %8d %9d %6.1f%%\n", c, clines[c], ctok[c], 100 * ctok[c] / alltok }
    printf "  %-40s %8d %9d\n", "all lines (blank lines have 0 tokens)", nl, alltok
    print "Markdown -> PDF: share of the 8-word sequences of each scored Markdown paragraph found in the PDF text"
    printf "  %-20s %10s %10s %s\n", "Markdown chapter", "paragraphs", "found", "paragraphs under 50%"
    for (j = 1; j <= nc; j++) { c = chs[j]; printf "  %-20s %10d %9.1f%% %d\n", c, para[c], 100 * F[c] / T[c], low[c]; TT += T[c]; FF += F[c]; PP += para[c] }
    printf "  %-20s %10d %9.1f%% %d\n", "all", PP, 100 * FF / TT, nlow
    print "  First paragraphs under 50% (Markdown line, share found, start of text):"
    for (k = 1; k <= nlow && k <= 15; k++) print "    " ex[k]
    # PDF -> Markdown, per chapter page range.
    while ((getline line < "'"$WORK"'/chapters.txt") > 0) { split(line, c2, "|"); nchap++; cp[nchap] = c2[1]; cn[nchap] = c2[2] }
    print "PDF -> Markdown: share of the PDF 8-word sequences found in the Markdown, by chapter page range"
    for (i = 1; i + 7 <= np; i++) {
      p = pg[i]; ch = "Legal Information"; if (p >= 2 && p <= 4) ch = "(contents and index, pages 2-4)"
      else for (j = 1; j <= nchap; j++) if (p >= cp[j]) ch = cn[j]
      if (!(ch in pseen)) { pseen[ch] = 1; pch[++npc] = ch }
      PT[ch]++; hit = (gram(P, i) in inmd); if (hit) PF[ch]++
      if (!hit) { if (!run) rs = i; run++ } else if (run) { if (run >= 60) { nr++; rl[nr] = run; rstart[nr] = rs }; run = 0 }
    }
    printf "  %-34s %10s %s\n", "PDF chapter (pages)", "sequences", "found"
    for (j = 1; j <= npc; j++) { ch = pch[j]; printf "  %-34s %10d %9.1f%%\n", ch, PT[ch], 100 * PF[ch] / PT[ch]; A += PT[ch]; B += PF[ch] }
    printf "  %-34s %10d %9.1f%%\n", "all", A, 100 * B / A
    print "  PDF passages of 60 or more consecutive unmatched sequences: " nr + 0 " (longest first, at most 10):"
    for (k = 1; k <= nr; k++) for (q = k + 1; q <= nr; q++) if (rl[q] > rl[k]) { t = rl[k]; rl[k] = rl[q]; rl[q] = t; t = rstart[k]; rstart[k] = rstart[q]; rstart[q] = t }
    for (k = 1; k <= nr && k <= 10; k++) { s = ""; for (q = 0; q < 12; q++) s = s " " P[rstart[k] + q]; printf "    page %d, %d tokens:%s ...\n", pg[rstart[k]], rl[k], s }
  }' "$WORK/pdf.lines" "$WORK/md.lines"

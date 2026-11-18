package srd

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the repository root, seen from this package's directory.
const repoRoot = "../.."

// TestFullFile splits the pinned SRD file downloaded by scripts/srd-download.sh.
// It is skipped when the file is absent (it is never committed).
//
// The expected spell, monster and condition counts are those printed by
// scripts/srd-validate.sh for the pinned file: 339 headings under
// "## Spell Descriptions"; 330 stat blocks (235 in Monsters A-Z, 95 in
// Animals); 15 headings ending "[Condition]".
func TestFullFile(t *testing.T) {
	path := filepath.Join(repoRoot, DefaultPath)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("skipping: %s not found; run scripts/srd-download.sh to get the SRD Markdown", DefaultPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	pin := pinnedSHA256(t)
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != pin {
		t.Fatalf("%s has SHA-256 %s, not the pinned %s; run scripts/srd-download.sh again", DefaultPath, got, pin)
	}

	chunks, err := Split(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	perKind := map[Kind]int{}
	for _, c := range chunks {
		perKind[c.Kind]++
	}
	for _, tt := range []struct {
		kind Kind
		want int
	}{
		{KindSpell, 339},
		{KindMonster, 330},
		{KindCondition, 15},
	} {
		if got := perKind[tt.kind]; got != tt.want {
			t.Errorf("%s chunks = %d, want %d (scripts/srd-validate.sh)", tt.kind, got, tt.want)
		}
	}
	checkCoverage(t, string(data), chunks)
}

// pinnedSHA256 reads SRD_MARKDOWN_SHA256 from scripts/srd-source.env.
func pinnedSHA256(t *testing.T) string {
	t.Helper()
	f, err := os.Open(filepath.Join(repoRoot, "scripts", "srd-source.env"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "SRD_MARKDOWN_SHA256="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	t.Fatal("SRD_MARKDOWN_SHA256 not found in scripts/srd-source.env")
	return ""
}

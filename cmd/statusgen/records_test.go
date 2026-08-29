package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// chdirRepoRoot moves the working directory to the module root (where go.mod
// lives) so the doc-path-relative generators (docs/tdd, docs/adr, docs/status)
// resolve, and restores it after the test.
func chdirRepoRoot(t *testing.T) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := orig
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %s", orig)
		}
		dir = parent
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
}

func TestLeadingStatus(t *testing.T) {
	cases := map[string]string{
		"Not Started":                   "Not Started",
		"In Progress":                   "In Progress",
		"Partially Implemented":         "Partially Implemented",
		"Implemented":                   "Implemented",
		"Superseded":                    "Superseded",
		"Implemented (Stages 1–4)":      "Implemented",
		"Implemented — all deferrals":   "Implemented",
		"Partially Implemented — Phase": "Partially Implemented",
	}
	for in, want := range cases {
		got, err := leadingStatus(in)
		if err != nil || got != want {
			t.Errorf("leadingStatus(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := leadingStatus("Bogus status"); err == nil {
		t.Error("leadingStatus should error on an unknown status")
	}
}

func TestNormalizeRelations(t *testing.T) {
	in := "- **Relations**: Extends ADR-00012, [ADR-00037](ADR-00037.md). Implements TDD-00009"
	want := "Extends [ADR-00012](ADR-00012.md), [ADR-00037](ADR-00037.md). Implements [TDD-00009](../tdd/TDD-00009.md)"
	if got := normalizeRelations(in); got != want {
		t.Errorf("normalizeRelations:\n got=%q\nwant=%q", got, want)
	}
	// A half-linked reference (`[ADR-00083]`) is canonicalized too.
	if got := normalizeRelations("- **Relations**: Extends [ADR-00083]"); got != "Extends [ADR-00083](ADR-00083.md)" {
		t.Errorf("half-linked ref not canonicalized: %q", got)
	}
	// No Relations bullet → empty.
	if got := normalizeRelations(""); got != "" {
		t.Errorf("empty bullet should normalize to empty, got %q", got)
	}
}

func TestRelationsBulletStopsAtNextField(t *testing.T) {
	body := "# ADR-00089: x\n\n- **Relations**: Extends [ADR-00083](ADR-00083.md).\n- **Date**: 2026-08-03\n\n## Context\n"
	got := relationsBullet(body)
	if strings.Contains(got, "Date") || strings.Contains(got, "Context") {
		t.Errorf("relationsBullet bled past its own bullet: %q", got)
	}
	if !strings.Contains(got, "ADR-00083") {
		t.Errorf("relationsBullet dropped its content: %q", got)
	}
}

func TestSplitEscapedPipes(t *testing.T) {
	got := splitEscapedPipes(`| a | b \| c | d |`)
	want := []string{"a", "b | c", "d"}
	if len(got) != len(want) {
		t.Fatalf("splitEscapedPipes gave %d cells (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("cell %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseTDDBacklogTable(t *testing.T) {
	table := "| TDD | Status | Notes |\n|---|---|---|\n" +
		"| [00003](../tdd/TDD-00003.md) Alt fetch backend | Not Started | Low priority |\n" +
		"| [00011](../tdd/TDD-00011.md) IndexedDB `a \\| b` | Not Started | |"
	seg, err := parseTDDBacklogTable(table)
	if err != nil {
		t.Fatal(err)
	}
	if e := seg.Editorial["3"]; e == nil || e.Title != "Alt fetch backend" || e.Notes != "Low priority" {
		t.Errorf("row 3 parsed wrong: %+v", e)
	}
	// Escaped pipe in the title survives unescaped in the editorial value.
	if e := seg.Editorial["11"]; e == nil || e.Title != "IndexedDB `a | b`" || e.Notes != "" {
		t.Errorf("row 11 parsed wrong: %+v", e)
	}
}

// TestGeneratorsMatchCommitted is the regression guard: the generators must
// reproduce the committed index READMEs exactly (the same invariant `make
// status-check` enforces, but as a package-local unit test).
func TestGeneratorsMatchCommitted(t *testing.T) {
	chdirRepoRoot(t)
	for _, tc := range []struct {
		name string
		gen  func() (string, error)
		path string
	}{
		{"tdd", generateTDDIndex, filepath.Join(tddDir, "README.md")},
		{"adr", generateADRIndex, filepath.Join(adrDir, "README.md")},
	} {
		got, err := tc.gen()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		committed, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got != string(committed) {
			t.Errorf("%s index generation does not match committed %s (run `make status`)", tc.name, tc.path)
		}
	}
}

// TestBacklogRoundTrip parses the committed status-README backlog table back
// into an editorial map and re-renders it, asserting byte-identity — the
// import→export direction parseTDDBacklogTable exists for.
func TestBacklogRoundTrip(t *testing.T) {
	chdirRepoRoot(t)
	readme, err := os.ReadFile(filepath.Join(statusDir, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(readme), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "| TDD | Status | Notes |") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatal("backlog table not found in status README")
	}
	end := start
	for end < len(lines) && strings.HasPrefix(lines[end], "|") {
		end++
	}
	table := strings.Join(lines[start:end], "\n")
	seg, err := parseTDDBacklogTable(table)
	if err != nil {
		t.Fatal(err)
	}
	got, err := renderTDDBacklog(seg)
	if err != nil {
		t.Fatal(err)
	}
	if got != table {
		t.Errorf("backlog round-trip differs:\n got=%q\nwant=%q", got, table)
	}
}

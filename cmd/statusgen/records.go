package main

// Generators for the ADR and TDD *index* READMEs, and the status index's TDD
// backlog. These indexes are projections of the individual record files
// (docs/adr/ADR-*.md, docs/tdd/TDD-*.md), the same generated-from-source model
// docs/status/*.md already follows (TDD-00149). The preamble prose above each
// "## Index" table stays hand-editable; only the table rows are generated.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const tddDir = "docs/tdd"
const adrDir = "docs/adr"

// tddStatuses, longest-first so "Partially Implemented" wins over "Implemented".
var tddStatuses = []string{"Partially Implemented", "Not Started", "In Progress", "Implemented", "Superseded"}

// notYetDone are the statuses that keep a TDD in the status-README backlog.
var notYetDoneStatus = map[string]bool{"Not Started": true, "In Progress": true, "Partially Implemented": true}

type tddRecord struct {
	Num    int
	Title  string
	Status string
	Notes  string
}

var (
	tddHeadingRe = regexp.MustCompile(`^# TDD-(\d+):\s*(.*)$`)
	adrHeadingRe = regexp.MustCompile(`^# ADR-(\d+):\s*(.*)$`)
	statusLineRe = regexp.MustCompile(`^- \*\*Status\*\*:\s*(.*)$`)
	notesLineRe  = regexp.MustCompile(`^- \*\*Notes\*\*:\s*(.*)$`)
	tddTokenRe   = regexp.MustCompile(`TDD-(\d+)`)
)

// escapeCell escapes a raw markdown value for a table cell (a literal pipe would
// otherwise start a new column).
func escapeCell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

// tableRow renders a markdown table row from already-escaped cells, using the
// same empty-cell convention as the category-table renderer (`| |`, a lone
// space, not `|  |`).
func tableRow(cells ...string) string {
	var b strings.Builder
	b.WriteByte('|')
	for _, c := range cells {
		if c == "" {
			b.WriteString(" |")
		} else {
			fmt.Fprintf(&b, " %s |", c)
		}
	}
	return b.String()
}

func recordNums(dir, prefix string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`^` + prefix + `-(\d+)\.md$`)
	var nums []int
	for _, e := range entries {
		if m := re.FindStringSubmatch(e.Name()); m != nil {
			n, _ := strconv.Atoi(m[1])
			nums = append(nums, n)
		}
	}
	sort.Ints(nums)
	return nums, nil
}

// leadingStatus extracts the bare enum word(s) from a Status line's text,
// ignoring any trailing prose (which the convention forbids but a few files
// carry). Errors if the line doesn't start with a known status.
func leadingStatus(text string) (string, error) {
	for _, s := range tddStatuses {
		if text == s || strings.HasPrefix(text, s+" ") || strings.HasPrefix(text, s+"—") || strings.HasPrefix(text, s+" —") {
			return s, nil
		}
	}
	return "", fmt.Errorf("unrecognized status %q", text)
}

// loadTDDRecords parses every docs/tdd/TDD-*.md header (heading title, bare
// status, optional Notes field), sorted by number.
func loadTDDRecords() ([]tddRecord, error) {
	nums, err := recordNums(tddDir, "TDD")
	if err != nil {
		return nil, err
	}
	var recs []tddRecord
	for _, n := range nums {
		path := filepath.Join(tddDir, fmt.Sprintf("TDD-%05d.md", n))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rec := tddRecord{Num: n}
		var sawStatus bool
		for i, line := range strings.Split(string(raw), "\n") {
			if i == 0 {
				m := tddHeadingRe.FindStringSubmatch(line)
				if m == nil {
					return nil, fmt.Errorf("%s: first line is not a `# TDD-NNNNN:` heading", path)
				}
				rec.Title = m[2]
				continue
			}
			if m := statusLineRe.FindStringSubmatch(line); m != nil && !sawStatus {
				st, err := leadingStatus(m[1])
				if err != nil {
					return nil, fmt.Errorf("%s: %v", path, err)
				}
				rec.Status = st
				sawStatus = true
			}
			if m := notesLineRe.FindStringSubmatch(line); m != nil {
				rec.Notes = m[1]
			}
			if strings.HasPrefix(line, "## ") {
				break // past the header region
			}
		}
		if rec.Status == "" {
			return nil, fmt.Errorf("%s: no **Status** line", path)
		}
		recs = append(recs, rec)
	}
	return recs, nil
}

// relationsBullet returns an ADR file's `- **Relations**:` bullet text, joined
// across any continuation lines (up to the next blank line), or "" if absent.
func relationsBullet(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "- **Relations**") {
			parts := []string{l}
			// Join only true wrapped continuations (indented lines); stop at a
			// blank line, the next header field (`- **Date**:` etc.), or a
			// heading — those are not part of the Relations bullet.
			for j := i + 1; j < len(lines); j++ {
				nx := lines[j]
				if strings.TrimSpace(nx) == "" || strings.HasPrefix(nx, "- ") || strings.HasPrefix(nx, "#") {
					break
				}
				parts = append(parts, strings.TrimSpace(nx))
			}
			return strings.Join(parts, " ")
		}
	}
	return ""
}

// deriveRelatedADRs builds, for each TDD number, the set of ADRs whose
// Relations bullet references it (any verb — the canonical ADR→TDD link
// direction). This is the sole source for the TDD index's Related ADRs column.
func deriveRelatedADRs() (map[int][]int, error) {
	nums, err := recordNums(adrDir, "ADR")
	if err != nil {
		return nil, err
	}
	byTDD := map[int]map[int]bool{}
	for _, a := range nums {
		path := filepath.Join(adrDir, fmt.Sprintf("ADR-%05d.md", a))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rel := relationsBullet(string(raw))
		for _, m := range tddTokenRe.FindAllStringSubmatch(rel, -1) {
			t, _ := strconv.Atoi(m[1])
			if byTDD[t] == nil {
				byTDD[t] = map[int]bool{}
			}
			byTDD[t][a] = true
		}
	}
	out := map[int][]int{}
	for t, set := range byTDD {
		var as []int
		for a := range set {
			as = append(as, a)
		}
		sort.Ints(as)
		out[t] = as
	}
	return out, nil
}

func adrLinks(nums []int) string {
	var parts []string
	for _, a := range nums {
		parts = append(parts, fmt.Sprintf("[ADR-%05d](../adr/ADR-%05d.md)", a, a))
	}
	return strings.Join(parts, ", ")
}

// renderTDDIndexRows renders the `# | Title | Status | Related ADRs | Notes`
// data rows for the TDD index.
func renderTDDIndexRows(recs []tddRecord, related map[int][]int) string {
	var b strings.Builder
	for _, r := range recs {
		fmt.Fprintf(&b, "%s\n", tableRow(
			fmt.Sprintf("[%05d](TDD-%05d.md)", r.Num, r.Num),
			escapeCell(r.Title), r.Status, adrLinks(related[r.Num]), escapeCell(r.Notes)))
	}
	return b.String()
}

// spliceIndexTable replaces the data rows of the "## Index" table in a record
// README with freshly-rendered rows, preserving the preamble and the table
// header/separator byte-for-byte. headerCells is the exact header row text used
// to locate the table.
func spliceIndexTable(committed, headerRow, newRows string) (string, error) {
	lines := strings.Split(committed, "\n")
	hdr := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == headerRow {
			hdr = i
			break
		}
	}
	if hdr == -1 || hdr+1 >= len(lines) {
		return "", fmt.Errorf("index table header %q not found", headerRow)
	}
	// hdr+1 is the |---|…| separator; data rows follow until a non-row line.
	end := hdr + 2
	for end < len(lines) && strings.HasPrefix(lines[end], "| [") {
		end++
	}
	before := strings.Join(lines[:hdr+2], "\n") + "\n"
	after := strings.Join(lines[end:], "\n")
	return before + newRows + after, nil
}

const tddIndexHeader = "| # | Title | Status | Related ADRs | Notes |"

// splitEscapedPipes splits a markdown table row on unescaped '|', returning the
// interior cells (dropping the empty leading/trailing ones) with `\|` unescaped.
func splitEscapedPipes(row string) []string {
	var cells []string
	var cur strings.Builder
	for i := 0; i < len(row); i++ {
		if row[i] == '\\' && i+1 < len(row) && row[i+1] == '|' {
			cur.WriteByte('|')
			i++
			continue
		}
		if row[i] == '|' {
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(row[i])
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	// Drop the empty cells produced by the leading and trailing pipes.
	if len(cells) >= 2 {
		cells = cells[1 : len(cells)-1]
	}
	return cells
}

var backlogTDDCellRe = regexp.MustCompile(`^\[(\d+)\]\(\.\./tdd/TDD-\d+\.md\)\s*(.*)$`)

// parseTDDBacklogTable reconstructs a tddBacklog segment's editorial map from a
// rendered backlog table (the README→JSON direction, for export/roundtrip). The
// Status column is derived at render time, so it is parsed only to validate the
// row shape, never stored.
func parseTDDBacklogTable(text string) (*IndexSegment, error) {
	editorial := map[string]*tddBacklogEntry{}
	for _, line := range strings.Split(text, "\n") {
		if !strings.HasPrefix(line, "| [") {
			continue
		}
		cells := splitEscapedPipes(line)
		if len(cells) != 3 {
			return nil, fmt.Errorf("backlog row has %d cells, want 3: %q", len(cells), line)
		}
		m := backlogTDDCellRe.FindStringSubmatch(cells[0])
		if m == nil {
			return nil, fmt.Errorf("backlog row TDD cell unparseable: %q", cells[0])
		}
		editorial[strings.TrimLeft(m[1], "0")] = &tddBacklogEntry{Title: m[2], Notes: cells[2]}
	}
	return &IndexSegment{Kind: "tddBacklog", Editorial: editorial}, nil
}

// renderTDDBacklog renders the status-README "Design Documents (TDDs)" backlog:
// the `TDD | Status | Notes` table listing every not-yet-done TDD. Which TDDs
// appear and their Status are derived from the TDD files (so a shipped TDD
// drops out automatically); the seg's editorial map supplies each row's
// status-page-specific title and notes. A not-yet-done TDD with no editorial
// entry is a hard error (its backlog title/notes must be authored).
func renderTDDBacklog(seg *IndexSegment) (string, error) {
	recs, err := loadTDDRecords()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("| TDD | Status | Notes |\n|---|---|---|")
	for _, r := range recs {
		if !notYetDoneStatus[r.Status] {
			continue
		}
		ed := seg.Editorial[strconv.Itoa(r.Num)]
		if ed == nil {
			return "", fmt.Errorf("TDD-%05d is %q (not-yet-done) but has no status-README backlog editorial entry — add its title/notes to the tddBacklog segment in _index.json", r.Num, r.Status)
		}
		tddCell := fmt.Sprintf("[%05d](../tdd/TDD-%05d.md) %s", r.Num, r.Num, escapeCell(ed.Title))
		fmt.Fprintf(&b, "\n%s", tableRow(tddCell, r.Status, escapeCell(ed.Notes)))
	}
	return b.String(), nil
}

// refRe matches an ADR/TDD reference in any form — bare (`ADR-00083`), linked
// (`[ADR-00083](ADR-00083.md)`), or half-linked (`[ADR-00083]`) — so it can be
// rewritten to one canonical link form.
var refRe = regexp.MustCompile(`\[?(ADR|TDD)-(\d+)\]?(\([^)]*\))?`)

// normalizeRelations turns an ADR's freeform Relations bullet into the index
// cell: the prefix stripped, wraps already joined by relationsBullet, and every
// ADR/TDD reference rewritten to a canonical markdown link (relative to
// docs/adr/: ADR links are same-dir, TDD links reach ../tdd/).
func normalizeRelations(bullet string) string {
	text := bullet
	if i := strings.Index(text, "**Relations**"); i >= 0 {
		text = text[i+len("**Relations**"):]
		text = strings.TrimLeft(text, " :")
	}
	text = strings.Join(strings.Fields(text), " ") // collapse any internal runs of whitespace
	return refRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := refRe.FindStringSubmatch(m)
		n, _ := strconv.Atoi(sub[2])
		if sub[1] == "ADR" {
			return fmt.Sprintf("[ADR-%05d](ADR-%05d.md)", n, n)
		}
		return fmt.Sprintf("[TDD-%05d](../tdd/TDD-%05d.md)", n, n)
	})
}

type adrRecord struct {
	Num       int
	Title     string
	Relations string
}

func loadADRRecords() ([]adrRecord, error) {
	nums, err := recordNums(adrDir, "ADR")
	if err != nil {
		return nil, err
	}
	var recs []adrRecord
	for _, n := range nums {
		path := filepath.Join(adrDir, fmt.Sprintf("ADR-%05d.md", n))
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		body := string(raw)
		first := body
		if i := strings.IndexByte(first, '\n'); i >= 0 {
			first = first[:i]
		}
		m := adrHeadingRe.FindStringSubmatch(first)
		if m == nil {
			return nil, fmt.Errorf("%s: first line is not a `# ADR-NNNNN:` heading", path)
		}
		recs = append(recs, adrRecord{Num: n, Title: m[2], Relations: normalizeRelations(relationsBullet(body))})
	}
	return recs, nil
}

func renderADRIndexRows(recs []adrRecord) string {
	var b strings.Builder
	for _, r := range recs {
		fmt.Fprintf(&b, "%s\n", tableRow(fmt.Sprintf("[%05d](ADR-%05d.md)", r.Num, r.Num), escapeCell(r.Title), escapeCell(r.Relations)))
	}
	return b.String()
}

const adrIndexHeader = "| # | Title | Relations |"

func generateADRIndex() (string, error) {
	recs, err := loadADRRecords()
	if err != nil {
		return "", err
	}
	committed, err := os.ReadFile(filepath.Join(adrDir, "README.md"))
	if err != nil {
		return "", err
	}
	return spliceIndexTable(string(committed), adrIndexHeader, renderADRIndexRows(recs))
}

func generateTDDIndex() (string, error) {
	recs, err := loadTDDRecords()
	if err != nil {
		return "", err
	}
	related, err := deriveRelatedADRs()
	if err != nil {
		return "", err
	}
	committed, err := os.ReadFile(filepath.Join(tddDir, "README.md"))
	if err != nil {
		return "", err
	}
	return spliceIndexTable(string(committed), tddIndexHeader, renderTDDIndexRows(recs, related))
}

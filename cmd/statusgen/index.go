package main

// The README (index) model: the same ordered-segment idea as a detail page,
// with two extra derived segment kinds — a section's feature total, and the
// per-section category table whose Coverage/Strict cells are computed from
// the referenced detail pages. Everything else (TOC, gap/roadmap/format
// sections, the TDD backlog table) stays verbatim blocks.

import (
	"fmt"
	"regexp"
	"strings"
)

type StatusIndex struct {
	File     string         `json:"file"` // "README.md"
	Title    string         `json:"title"`
	Segments []IndexSegment `json:"segments"`
}

// Kinds: "block" (verbatim), "sectionTotal" (derived from the next
// categoryTable), "categoryTable".
type IndexSegment struct {
	Kind    string     `json:"kind"`
	Text    string     `json:"text,omitempty"`
	Columns []string   `json:"columns,omitempty"` // categoryTable header cells
	Rows    []IndexRow `json:"rows,omitempty"`
}

type IndexRow struct {
	Category string `json:"category"`
	// PageFile links the row to a detail page; its Coverage/Strict cells are
	// then derived from that page (the table whose heading matches Category
	// on a multi-category page). PageText is the verbatim cell when it is
	// not a plain page link ("—").
	PageFile string `json:"pageFile,omitempty"`
	PageText string `json:"pageText,omitempty"`
	// Authored cell text where a derived "n/d, p%" doesn't apply: the
	// non-fraction statuses ("2/3 modes (`manual`, `gc`)", "✅ 2 modes"),
	// and Strict for a page whose table has no Caveats column.
	CoverageOverride string `json:"coverageOverride,omitempty"`
	StrictOverride   string `json:"strictOverride,omitempty"`
	// Curated summary caveats — authored, verbatim cell.
	Caveats string `json:"caveats,omitempty"`
}

var totalLineRe = regexp.MustCompile(`^\*\*(\d+) / (\d+) features, (~?)(\d+)% coverage\.\*\*$`)
var fracCellRe = regexp.MustCompile(`^(\d+)/(\d+), (~?)(\d+)%$`)
var pageLinkRe = regexp.MustCompile(`^\[([A-Z0-9-]+\.md)\]\(([A-Z0-9-]+\.md)\)$`)

func importIndex(path, src string, areas map[string]*StatusArea) (*StatusIndex, error) {
	if !strings.HasSuffix(src, "\n") {
		return nil, fmt.Errorf("%s: no trailing newline", path)
	}
	chunks, err := splitChunks(strings.TrimSuffix(src, "\n"))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	chunks, err = stripBanner(chunks, path, "_index.json")
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 || !strings.HasPrefix(chunks[0].text, "# ") || strings.Contains(chunks[0].text, "\n") {
		return nil, fmt.Errorf("%s: expected a single '# Title' first block", path)
	}
	idx := &StatusIndex{File: "README.md", Title: strings.TrimPrefix(chunks[0].text, "# ")}

	type obsTotal struct {
		n, d, pct int
		approx    bool
		line      int
		seg       int // index into idx.Segments
	}
	var pending []obsTotal

	for _, c := range chunks[1:] {
		switch {
		case totalLineRe.MatchString(c.text):
			m := totalLineRe.FindStringSubmatch(c.text)
			pending = append(pending, obsTotal{
				n: atoi(m[1]), d: atoi(m[2]), approx: m[3] == "~", pct: atoi(m[4]),
				line: c.line, seg: len(idx.Segments),
			})
			idx.Segments = append(idx.Segments, IndexSegment{Kind: "sectionTotal"})

		case strings.HasPrefix(c.text, "|") && isCategoryTable(c.text):
			seg, err := parseCategoryTable(c.text, c.line, path, areas)
			if err != nil {
				return nil, err
			}
			idx.Segments = append(idx.Segments, *seg)
			// Verify any pending section total against this table.
			for _, t := range pending {
				n, d := sumCategoryTable(seg, areas)
				if t.n != n || t.d != d {
					return nil, fmt.Errorf("%s:%d: section total %d / %d disagrees with the table's derived sum %d / %d",
						path, t.line, t.n, t.d, n, d)
				}
				p, exact := pctExact(n, d)
				if t.pct != p {
					return nil, fmt.Errorf("%s:%d: section total percent %d%% disagrees with derived %d%%", path, t.line, t.pct, p)
				}
				if t.approx == exact {
					return nil, fmt.Errorf("%s:%d: section total tilde styling violates the exact-percent rule (%d/%d)", path, t.line, n, d)
				}
			}
			pending = nil

		default:
			// Non-category tables (the NOT-implemented, fidelity-gaps, and
			// TDD-backlog tables) stay verbatim blocks like any prose.
			idx.Segments = append(idx.Segments, IndexSegment{Kind: "block", Text: c.text})
		}
	}
	if len(pending) > 0 {
		return nil, fmt.Errorf("%s:%d: section total with no category table after it", path, pending[0].line)
	}
	return idx, nil
}

// isCategoryTable: a 5-column table whose 4th column is Page.
func isCategoryTable(text string) bool {
	cells, err := splitRow(strings.Split(text, "\n")[0])
	return err == nil && len(cells) == 5 && cells[3] == "Page"
}

func parseCategoryTable(text string, base int, path string, areas map[string]*StatusArea) (*IndexSegment, error) {
	lines := strings.Split(text, "\n")
	lineNo := base
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", path, lineNo, fmt.Sprintf(format, args...))
	}
	header, _ := splitRow(lines[0])
	seg := &IndexSegment{Kind: "categoryTable", Columns: header}
	if len(lines) < 3 || lines[1] != strings.Repeat("|---", 5)+"|" {
		lineNo = base + 1
		return nil, fail("unexpected separator row")
	}
	for li, l := range lines[2:] {
		lineNo = base + 2 + li
		cells, err := splitRow(l)
		if err != nil || len(cells) != 5 {
			return nil, fail("bad category row (%d cells, want 5): %q", len(cells), l)
		}
		row := IndexRow{Category: cells[0], Caveats: cells[4]}
		if m := pageLinkRe.FindStringSubmatch(cells[3]); m != nil && m[1] == m[2] {
			row.PageFile = m[1]
		} else {
			row.PageText = cells[3]
		}

		var tbl *Table
		if row.PageFile != "" {
			area, ok := areas[row.PageFile]
			if !ok {
				return nil, fail("row %q links to unknown page %s", row.Category, row.PageFile)
			}
			if tbl, err = resolveCategoryTableRef(area, row.Category); err != nil {
				return nil, fail("row %q: %v", row.Category, err)
			}
		}

		// Coverage cell: derived when it matches the fraction shape and a
		// table backs it; authored override otherwise.
		if tbl != nil && fracCellRe.MatchString(cells[1]) {
			impl, _, total := countTable(tbl)
			if cells[1] != fmtFracCell(impl, total) {
				return nil, fail("row %q coverage %q disagrees with derived %q", row.Category, cells[1], fmtFracCell(impl, total))
			}
		} else {
			row.CoverageOverride = cells[1]
		}
		// Strict cell: derivable only when the backing table has a Caveats
		// column to define it.
		if tbl != nil && tbl.HasCaveats && fracCellRe.MatchString(cells[2]) {
			_, strict, total := countTable(tbl)
			if cells[2] != fmtFracCell(strict, total) {
				return nil, fail("row %q strict %q disagrees with derived %q", row.Category, cells[2], fmtFracCell(strict, total))
			}
		} else {
			row.StrictOverride = cells[2]
		}
		seg.Rows = append(seg.Rows, row)
	}
	return seg, nil
}

// resolveCategoryTableRef picks the page table a category row refers to: the
// parity table whose heading equals the category on a multi-table page, or
// the page's single parity table.
func resolveCategoryTableRef(area *StatusArea, category string) (*Table, error) {
	var parity []*Table
	for _, s := range area.Segments {
		if s.Table != nil && s.Table.CountsTowardParity {
			parity = append(parity, s.Table)
		}
	}
	if len(parity) == 1 {
		return parity[0], nil
	}
	for _, t := range parity {
		if t.Heading == category {
			return t, nil
		}
	}
	return nil, fmt.Errorf("page %s has %d parity tables and none is headed %q", area.File, len(parity), category)
}

func sumCategoryTable(seg *IndexSegment, areas map[string]*StatusArea) (n, d int) {
	for _, r := range seg.Rows {
		if r.PageFile == "" || r.CoverageOverride != "" {
			continue
		}
		area, ok := areas[r.PageFile]
		if !ok {
			continue
		}
		tbl, err := resolveCategoryTableRef(area, r.Category)
		if err != nil {
			continue
		}
		impl, _, total := countTable(tbl)
		n, d = n+impl, d+total
	}
	return
}

func fmtFracCell(n, d int) string {
	return fmt.Sprintf("%d/%d, %s", n, d, fmtPct(n, d))
}

func generateIndex(idx *StatusIndex, areas map[string]*StatusArea) (string, error) {
	parts := []string{banner("_index.json"), "# " + idx.Title}
	for si, s := range idx.Segments {
		switch s.Kind {
		case "block":
			parts = append(parts, s.Text)
		case "sectionTotal":
			var tbl *IndexSegment
			for j := si + 1; j < len(idx.Segments); j++ {
				if idx.Segments[j].Kind == "categoryTable" {
					tbl = &idx.Segments[j]
					break
				}
			}
			if tbl == nil {
				return "", fmt.Errorf("README.md: sectionTotal with no category table after it")
			}
			n, d := sumCategoryTable(tbl, areas)
			parts = append(parts, fmt.Sprintf("**%d / %d features, %s coverage.**", n, d, fmtPct(n, d)))
		case "categoryTable":
			out, err := renderCategoryTable(&s, areas)
			if err != nil {
				return "", err
			}
			parts = append(parts, out)
		default:
			return "", fmt.Errorf("README.md: unknown segment kind %q", s.Kind)
		}
	}
	return strings.Join(parts, "\n\n") + "\n", nil
}

func renderCategoryTable(seg *IndexSegment, areas map[string]*StatusArea) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "| %s |\n", strings.Join(seg.Columns, " | "))
	fmt.Fprintf(&b, "%s|", strings.Repeat("|---", 5))
	for _, r := range seg.Rows {
		var tbl *Table
		if r.PageFile != "" {
			area, ok := areas[r.PageFile]
			if !ok {
				return "", fmt.Errorf("README.md: row %q links to unknown page %s", r.Category, r.PageFile)
			}
			var err error
			if tbl, err = resolveCategoryTableRef(area, r.Category); err != nil {
				return "", fmt.Errorf("README.md: row %q: %v", r.Category, err)
			}
		}
		coverage := r.CoverageOverride
		if coverage == "" {
			impl, _, total := countTable(tbl)
			coverage = fmtFracCell(impl, total)
		}
		strict := r.StrictOverride
		if strict == "" {
			_, st, total := countTable(tbl)
			strict = fmtFracCell(st, total)
		}
		page := r.PageText
		if r.PageFile != "" {
			page = fmt.Sprintf("[%s](%s)", r.PageFile, r.PageFile)
		}
		b.WriteString("\n|")
		for _, c := range []string{r.Category, coverage, strict, page, r.Caveats} {
			if c == "" {
				b.WriteString(" |")
			} else {
				fmt.Fprintf(&b, " %s |", c)
			}
		}
	}
	return b.String(), nil
}

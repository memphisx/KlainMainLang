package main

// Markdown → StatusArea importer. Deliberately strict: any construct outside
// the modeled shape is an error, never a silent guess — an importer that
// tolerates drift would defeat the byte-identical round-trip gate. It also
// audits: committed coverage numbers that disagree with the rows, and tilde
// styling that violates the exact-percent rule, are hard errors to fix on
// the page, not to model.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var stdCoverageRe = regexp.MustCompile(
	`^\*\*Coverage\*\*: (\d+)/(\d+) \((~?)(\d+)%\) · \*\*Strict Coverage\*\*: (\d+)/(\d+) \((~?)(\d+)%\)\.$`)

// One category inside a multi-category coverage line: "Name n/d (p%)".
var catRe = regexp.MustCompile(`^(.+?) (\d+)/(\d+) \((~?)(\d+)%\)$`)

const strictSentence = "A row counts toward Strict only when its **Caveats** column is empty."

type catFig struct {
	name       string
	n, d, pct  int
	approx     bool
}

// stripBanner requires the generated-file banner as the first chunk and
// drops it; the generator re-emits it, so it carries no information.
func stripBanner(chunks []chunk, path, dataFile string) ([]chunk, error) {
	want := banner(dataFile)
	if len(chunks) == 0 || chunks[0].text != want {
		return nil, fmt.Errorf("%s: expected the generated-file banner as the first line: %s", path, want)
	}
	return chunks[1:], nil
}

func importPage(path, src string) (*StatusArea, error) {
	if !strings.HasSuffix(src, "\n") {
		return nil, fmt.Errorf("%s: no trailing newline", path)
	}
	file := filepath.Base(path)
	area := &StatusArea{ID: strings.ToLower(strings.TrimSuffix(file, ".md")), File: file}

	chunks, err := splitChunks(strings.TrimSuffix(src, "\n"))
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	chunks, err = stripBanner(chunks, path, area.ID+".json")
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 || !strings.HasPrefix(chunks[0].text, "# ") || strings.Contains(chunks[0].text, "\n") {
		return nil, fmt.Errorf("%s: expected a single '# Title' first block", path)
	}
	area.Title = strings.TrimPrefix(chunks[0].text, "# ")
	chunks = chunks[1:]

	// Observed coverage figures, verified against the rows after all tables
	// are parsed.
	var obsStd []catFig            // standard: [loose, strict] (name "")
	var obsCats, obsStrictCats []catFig // multiCategory

	for ci := 0; ci < len(chunks); ci++ {
		c := chunks[ci]
		fail := func(format string, args ...any) error {
			return fmt.Errorf("%s:%d: %s", path, c.line, fmt.Sprintf(format, args...))
		}
		switch {
		case strings.HasPrefix(c.text, "|"):
			tbl, err := parseTable(c.text, path, c.line)
			if err != nil {
				return nil, err
			}
			// Attach a "## …" heading block that directly precedes the table.
			if n := len(area.Segments); n > 0 {
				last := area.Segments[n-1]
				if last.Kind == "block" && strings.HasPrefix(last.Text, "## ") && !strings.Contains(last.Text, "\n") {
					tbl.Heading = strings.TrimPrefix(last.Text, "## ")
					area.Segments = area.Segments[:n-1]
				}
			}
			area.Segments = append(area.Segments, Segment{Kind: "table", Table: tbl})

		case stdCoverageRe.MatchString(c.text):
			if obsStd != nil || obsCats != nil {
				return nil, fail("second coverage line on one page")
			}
			m := stdCoverageRe.FindStringSubmatch(c.text)
			obsStd = []catFig{
				{n: atoi(m[1]), d: atoi(m[2]), approx: m[3] == "~", pct: atoi(m[4])},
				{n: atoi(m[5]), d: atoi(m[6]), approx: m[7] == "~", pct: atoi(m[8])},
			}
			area.Segments = append(area.Segments, Segment{Kind: "coverage", Style: "standard"})

		case isMultiCoverage(c.text):
			if obsStd != nil || obsCats != nil {
				return nil, fail("second coverage line on one page")
			}
			obsCats, err = parseCats(strings.TrimSuffix(strings.TrimPrefix(c.text, "**Coverage**: "), "."))
			if err != nil {
				return nil, fail("coverage line: %v", err)
			}
			// The Strict line is the next chunk.
			ci++
			if ci >= len(chunks) {
				return nil, fail("multi-category Coverage line with no Strict line")
			}
			s := chunks[ci].text
			body, ok := strings.CutPrefix(s, "**Strict Coverage**: ")
			if !ok {
				return nil, fail("expected the Strict Coverage line after the Coverage line, got %q", s)
			}
			body, ok = strings.CutSuffix(body, ". "+strictSentence)
			if !ok {
				return nil, fail("strict line must end with the standard Strict-definition sentence")
			}
			obsStrictCats, err = parseCats(body)
			if err != nil {
				return nil, fail("strict line: %v", err)
			}
			area.Segments = append(area.Segments, Segment{Kind: "coverage", Style: "multiCategory"})

		default:
			area.Segments = append(area.Segments, Segment{Kind: "block", Text: c.text})
		}
	}

	if err := verifyCoverage(path, area, obsStd, obsCats, obsStrictCats); err != nil {
		return nil, err
	}
	return area, nil
}

// verifyCoverage checks the committed figures against the rows and marks
// parity-excluded tables. It never stores a number — the generator re-derives.
func verifyCoverage(path string, area *StatusArea, obsStd, obsCats, obsStrictCats []catFig) error {
	named := 0
	for si := range area.Segments {
		if t := area.Segments[si].Table; t != nil {
			switch {
			case obsStd != nil:
				t.CountsTowardParity = true
			case obsCats != nil:
				// Only tables named as a category count toward parity.
				for _, c := range obsCats {
					if c.name == t.Heading {
						t.CountsTowardParity = true
					}
				}
			default:
				// Custom/untracked page: parity figures are not derived.
				t.CountsTowardParity = true
			}
			if t.CountsTowardParity && obsCats != nil {
				named++
			}
		}
	}
	checkFig := func(what string, obs catFig, n, d int) error {
		if obs.n != n || obs.d != d {
			return fmt.Errorf("%s: %s %d/%d disagrees with rows %d/%d", path, what, obs.n, obs.d, n, d)
		}
		p, exact := pctExact(n, d)
		if obs.pct != p {
			return fmt.Errorf("%s: %s percent %d%% disagrees with derived %d%%", path, what, obs.pct, p)
		}
		if obs.approx == exact {
			return fmt.Errorf("%s: %s tilde styling violates the exact-percent rule (%d/%d)", path, what, n, d)
		}
		return nil
	}
	switch {
	case obsStd != nil:
		impl, strict, total := countArea(area)
		if err := checkFig("coverage", obsStd[0], impl, total); err != nil {
			return err
		}
		return checkFig("strict", obsStd[1], strict, total)
	case obsCats != nil:
		if len(obsCats) != named {
			return fmt.Errorf("%s: coverage line names %d categories but %d tables match by heading", path, len(obsCats), named)
		}
		if len(obsStrictCats) != len(obsCats) {
			return fmt.Errorf("%s: coverage and strict lines disagree on category count", path)
		}
		i := 0
		for _, s := range area.Segments {
			if s.Table == nil || !s.Table.CountsTowardParity {
				continue
			}
			if obsCats[i].name != s.Table.Heading || obsStrictCats[i].name != s.Table.Heading {
				return fmt.Errorf("%s: category %q out of order with table heading %q", path, obsCats[i].name, s.Table.Heading)
			}
			impl, strict, total := countTable(s.Table)
			if err := checkFig("coverage "+s.Table.Heading, obsCats[i], impl, total); err != nil {
				return err
			}
			if err := checkFig("strict "+s.Table.Heading, obsStrictCats[i], strict, total); err != nil {
				return err
			}
			i++
		}
	}
	return nil
}

// isMultiCoverage: a Coverage line whose body is "Name n/d (p%) · …".
func isMultiCoverage(text string) bool {
	body, ok := strings.CutPrefix(text, "**Coverage**: ")
	if !ok || strings.Contains(text, "\n") {
		return false
	}
	_, err := parseCats(strings.TrimSuffix(body, "."))
	return err == nil && strings.HasSuffix(body, ".")
}

func parseCats(body string) ([]catFig, error) {
	var cats []catFig
	for _, part := range strings.Split(body, " · ") {
		m := catRe.FindStringSubmatch(part)
		if m == nil {
			return nil, fmt.Errorf("unparsable category %q", part)
		}
		cats = append(cats, catFig{name: m[1], n: atoi(m[2]), d: atoi(m[3]), approx: m[4] == "~", pct: atoi(m[5])})
	}
	return cats, nil
}

type chunk struct {
	text string
	line int // 1-based line number of the chunk's first line
}

// splitChunks splits the page into blank-line-separated verbatim chunks;
// consecutive blank lines are a hard error (the renderer joins with exactly
// one blank line).
func splitChunks(src string) ([]chunk, error) {
	lines := strings.Split(src, "\n")
	var chunks []chunk
	for i := 0; i < len(lines); {
		if lines[i] == "" {
			return nil, fmt.Errorf("line %d: unexpected blank line (double blank, or leading blank)", i+1)
		}
		start := i
		for i < len(lines) && lines[i] != "" {
			i++
		}
		chunks = append(chunks, chunk{text: strings.Join(lines[start:i], "\n"), line: start + 1})
		if i < len(lines) {
			i++ // the single separating blank
			if i == len(lines) {
				return nil, fmt.Errorf("line %d: trailing blank line", i)
			}
		}
	}
	return chunks, nil
}

func parseTable(text, path string, base int) (*Table, error) {
	lines := strings.Split(text, "\n")
	lineNo := base
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%s:%d: %s", path, lineNo, fmt.Sprintf(format, args...))
	}
	cells, err := splitRow(lines[0])
	if err != nil || len(cells) < 2 {
		return nil, fail("expected a table header row, got %q", lines[0])
	}
	tbl := &Table{FeatureColumn: cells[0]}
	if cells[1] != "Status" {
		return nil, fail("expected a Status column second, got %q", cells[1])
	}
	for _, c := range cells[2:] {
		switch c {
		case "Caveats":
			tbl.HasCaveats = true
		case "Notes":
			tbl.HasNotes = true
		default:
			return nil, fail("unknown column %q", c)
		}
	}
	ncols := len(cells)
	if len(lines) < 3 {
		return nil, fail("table has no rows")
	}
	lineNo = base + 1
	if lines[1] != strings.Repeat("|---", ncols)+"|" {
		return nil, fail("unexpected separator row %q", lines[1])
	}
	for li, l := range lines[2:] {
		lineNo = base + 2 + li
		rc, err := splitRow(l)
		if err != nil || len(rc) != ncols {
			return nil, fail("bad table row (%d cells, want %d): %q", len(rc), ncols, l)
		}
		row := Row{Feature: rc[0]}
		switch rc[1] {
		case "✅":
			row.Status = "implemented"
		case "❌":
			row.Status = "missing"
		default:
			return nil, fail("unknown status %q", rc[1])
		}
		col := 2
		if tbl.HasCaveats {
			row.Caveats, row.CaveatsPlain = splitBullets(rc[col])
			col++
		}
		if tbl.HasNotes {
			row.Notes, row.NotesPlain = splitBullets(rc[col])
		}
		tbl.Rows = append(tbl.Rows, row)
	}
	return tbl, nil
}

// splitRow splits "| a | b | |" on unescaped pipes into trimmed cells,
// keeping \| escapes verbatim so the generator reproduces them exactly.
func splitRow(l string) ([]string, error) {
	if !strings.HasPrefix(l, "|") || !strings.HasSuffix(l, "|") {
		return nil, fmt.Errorf("not a table row")
	}
	var cells []string
	var cur strings.Builder
	body := l[1 : len(l)-1]
	for j := 0; j < len(body); j++ {
		switch {
		case body[j] == '\\' && j+1 < len(body):
			cur.WriteByte(body[j])
			cur.WriteByte(body[j+1])
			j++
		case body[j] == '|':
			cells = append(cells, strings.TrimSpace(cur.String()))
			cur.Reset()
		default:
			cur.WriteByte(body[j])
		}
	}
	cells = append(cells, strings.TrimSpace(cur.String()))
	return cells, nil
}

// splitBullets turns "• a<br>• b" into fragments; a non-bulleted cell is a
// single plain fragment; "" is nil.
func splitBullets(cell string) (frags []string, plain bool) {
	if cell == "" {
		return nil, false
	}
	if !strings.HasPrefix(cell, "• ") {
		return []string{cell}, true
	}
	parts := strings.Split(cell, "<br>• ")
	parts[0] = strings.TrimPrefix(parts[0], "• ")
	return parts, false
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

package main

// StatusArea → Markdown generator. Every coverage number and percentage is
// computed here from the rows; the source never stores them.

import (
	"fmt"
	"strings"
)

func countTable(t *Table) (impl, strict, total int) {
	for _, r := range t.Rows {
		total++
		if r.Status == "implemented" {
			impl++
			if len(r.Caveats) == 0 {
				strict++
			}
		}
	}
	return
}

// countArea sums the parity-counting tables of the whole page.
func countArea(a *StatusArea) (impl, strict, total int) {
	for _, s := range a.Segments {
		if s.Table != nil && s.Table.CountsTowardParity {
			i, st, t := countTable(s.Table)
			impl, strict, total = impl+i, strict+st, total+t
		}
	}
	return
}

func roundPct(n, d int) int {
	if d == 0 {
		return 0
	}
	// Round half away from zero, matching the pages' hand-rounding.
	return (200*n + d) / (2 * d)
}

// pctExact reports whether 100·n/d is an integer (→ bare percent, no tilde).
func pctExact(n, d int) (int, bool) {
	return roundPct(n, d), d != 0 && (100*n)%d == 0
}

func fmtPct(n, d int) string {
	p, exact := pctExact(n, d)
	if !exact {
		return fmt.Sprintf("~%d%%", p)
	}
	return fmt.Sprintf("%d%%", p)
}

// banner is the do-not-edit header every generated page opens with. It is
// fully derived from the page's data file name, so it round-trips: the
// importer requires and discards it, the generator re-emits it.
func banner(dataFile string) string {
	return fmt.Sprintf("<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/%s; edit the JSON, then run `make status`. -->", dataFile)
}

func generatePage(a *StatusArea) (string, error) {
	parts := []string{banner(a.ID + ".json"), "# " + a.Title}
	for _, s := range a.Segments {
		switch s.Kind {
		case "block":
			parts = append(parts, s.Text)
		case "coverage":
			cov, err := renderCoverage(a, s.Style)
			if err != nil {
				return "", fmt.Errorf("%s: %v", a.File, err)
			}
			parts = append(parts, cov)
		case "table":
			parts = append(parts, renderTable(s.Table))
		default:
			return "", fmt.Errorf("%s: unknown segment kind %q", a.File, s.Kind)
		}
	}
	return strings.Join(parts, "\n\n") + "\n", nil
}

func renderCoverage(a *StatusArea, style string) (string, error) {
	switch style {
	case "standard":
		impl, strict, total := countArea(a)
		return fmt.Sprintf("**Coverage**: %d/%d (%s) · **Strict Coverage**: %d/%d (%s).",
			impl, total, fmtPct(impl, total), strict, total, fmtPct(strict, total)), nil
	case "multiCategory":
		var loose, strict []string
		for _, s := range a.Segments {
			if s.Table == nil || !s.Table.CountsTowardParity {
				continue
			}
			i, st, t := countTable(s.Table)
			loose = append(loose, fmt.Sprintf("%s %d/%d (%s)", s.Table.Heading, i, t, fmtPct(i, t)))
			strict = append(strict, fmt.Sprintf("%s %d/%d (%s)", s.Table.Heading, st, t, fmtPct(st, t)))
		}
		if len(loose) == 0 {
			return "", fmt.Errorf("multiCategory coverage with no parity tables")
		}
		return "**Coverage**: " + strings.Join(loose, " · ") + ".\n\n" +
			"**Strict Coverage**: " + strings.Join(strict, " · ") + ". " + strictSentence, nil
	default:
		return "", fmt.Errorf("unknown coverage style %q", style)
	}
}

func renderTable(t *Table) string {
	var b strings.Builder
	if t.Heading != "" {
		fmt.Fprintf(&b, "## %s\n\n", t.Heading)
	}
	cols := []string{t.FeatureColumn, "Status"}
	if t.HasCaveats {
		cols = append(cols, "Caveats")
	}
	if t.HasNotes {
		cols = append(cols, "Notes")
	}
	fmt.Fprintf(&b, "| %s |\n", strings.Join(cols, " | "))
	fmt.Fprintf(&b, "%s|", strings.Repeat("|---", len(cols)))
	for _, r := range t.Rows {
		cells := []string{r.Feature, statusGlyph(r.Status)}
		if t.HasCaveats {
			cells = append(cells, joinBullets(r.Caveats, r.CaveatsPlain))
		}
		if t.HasNotes {
			cells = append(cells, joinBullets(r.Notes, r.NotesPlain))
		}
		b.WriteString("\n|")
		for _, c := range cells {
			if c == "" {
				b.WriteString(" |")
			} else {
				fmt.Fprintf(&b, " %s |", c)
			}
		}
	}
	return b.String()
}

func statusGlyph(s string) string {
	if s == "implemented" {
		return "✅"
	}
	return "❌"
}

func joinBullets(frags []string, plain bool) string {
	if len(frags) == 0 {
		return ""
	}
	if plain {
		return frags[0]
	}
	return "• " + strings.Join(frags, "<br>• ")
}

package main

// statusgen — status-tracking source-of-truth tooling.
//
// docs/status/data/*.json is the source of truth; the docs/status/*.md pages
// (README included) are generated projections of it. Routine workflow:
//
//	statusgen generate              render data/*.json → docs/status/*.md (make status)
//	statusgen check                 like generate, but diff against the committed
//	                                pages and fail on any difference (the CI guard)
//
// One-time / re-derivation tools:
//
//	statusgen export                parse the committed .md corpus into data/*.json
//	statusgen import <page.md>      parse one status page, print its JSON source
//	statusgen gen <area.json>       render one JSON source to stdout
//	statusgen roundtrip <page.md>…  verify generate(import(page)) == page, byte-exact
//
// The round-trip gate was the acceptance criterion for the flip: an empty
// diff proves the schema loses nothing in either direction.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const statusDir = "docs/status"
const dataDir = "docs/status/data"

func main() {
	if len(os.Args) < 2 || (len(os.Args) < 3 && os.Args[1] != "export" && os.Args[1] != "generate" && os.Args[1] != "check") {
		fmt.Fprintln(os.Stderr, "usage: statusgen export|generate|check | import|gen|roundtrip <file>…")
		os.Exit(2)
	}
	cmd, files := os.Args[1], os.Args[2:]
	switch cmd {
	case "export":
		check(exportData())
	case "generate":
		check(generateAll(true))
	case "check":
		check(generateAll(false))
	case "import":
		raw, err := os.ReadFile(files[0])
		check(err)
		var doc any
		if filepath.Base(files[0]) == "README.md" {
			areas, err := loadSiblingAreas(files[0])
			check(err)
			doc, err = importIndex(files[0], string(raw), areas)
			check(err)
		} else {
			doc, err = importPage(files[0], string(raw))
			check(err)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false) // the values are Markdown; keep >, <br> literal
		enc.SetIndent("", "  ")
		check(enc.Encode(doc))
	case "gen":
		raw, err := os.ReadFile(files[0])
		check(err)
		var md string
		if strings.Contains(string(raw), `"file": "README.md"`) {
			var idx StatusIndex
			check(json.Unmarshal(raw, &idx))
			// Pre-flip, the index derives from the committed detail pages.
			areas, err := loadSiblingAreas(filepath.Join("docs", "status", "README.md"))
			check(err)
			md, err = generateIndex(&idx, areas)
			check(err)
		} else {
			var area StatusArea
			check(json.Unmarshal(raw, &area))
			md, err = generatePage(&area)
			check(err)
		}
		fmt.Print(md)
	case "roundtrip":
		failed := 0
		for _, f := range files {
			raw, err := os.ReadFile(f)
			check(err)
			var md string
			if filepath.Base(f) == "README.md" {
				areas, err := loadSiblingAreas(f)
				if err == nil {
					var idx *StatusIndex
					if idx, err = importIndex(f, string(raw), areas); err == nil {
						md, err = generateIndex(idx, areas)
					}
				}
				if err != nil {
					fmt.Fprintf(os.Stderr, "✗ %s: %v\n", f, err)
					failed++
					continue
				}
			} else {
				area, err := importPage(f, string(raw))
				if err != nil {
					fmt.Fprintf(os.Stderr, "✗ %s: import: %v\n", f, err)
					failed++
					continue
				}
				if md, err = generatePage(area); err != nil {
					fmt.Fprintf(os.Stderr, "✗ %s: generate: %v\n", f, err)
					failed++
					continue
				}
			}
			if md != string(raw) {
				fmt.Fprintf(os.Stderr, "✗ %s: round-trip differs:\n%s", f, firstDiff(string(raw), md))
				failed++
				continue
			}
			fmt.Printf("✓ %s round-trips byte-identical (%d bytes)\n", f, len(raw))
		}
		if failed > 0 {
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		os.Exit(2)
	}
}

// loadSiblingAreas parses every detail page next to the README, keyed by
// file name — the derivation source for the index's numbers.
func loadSiblingAreas(readmePath string) (map[string]*StatusArea, error) {
	dir := filepath.Dir(readmePath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	areas := map[string]*StatusArea{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		area, err := importPage(filepath.Join(dir, name), string(raw))
		if err != nil {
			return nil, fmt.Errorf("sibling page: %v", err)
		}
		areas[name] = area
	}
	return areas, nil
}

func firstDiff(want, got string) string {
	wl, gl := splitKeep(want), splitKeep(got)
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Sprintf("  line %d\n  - %q\n  + %q\n", i+1, w, g)
		}
	}
	return "  (identical lines, byte difference elsewhere)\n"
}

func splitKeep(s string) []string {
	var out []string
	for len(s) > 0 {
		j := 0
		for j < len(s) && s[j] != '\n' {
			j++
		}
		out = append(out, s[:j])
		if j < len(s) {
			j++
		}
		s = s[j:]
	}
	return out
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

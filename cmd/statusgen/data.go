package main

// The forward pipeline around docs/status/data/: export (one-time MD → JSON
// derivation), generate (JSON → MD, the routine direction), and check (the
// CI guard — generate in memory and fail on any diff against the committed
// pages).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func writeJSON(path string, doc any) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false) // the values are Markdown; keep >, <br> literal
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

// exportData re-derives the JSON source from the committed Markdown corpus.
// Only valid while the corpus round-trips cleanly (the pre-flip state, or a
// deliberate re-derivation after hand-editing a generated page).
func exportData() error {
	areas, err := loadSiblingAreas(filepath.Join(statusDir, "README.md"))
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(statusDir, "README.md"))
	if err != nil {
		return err
	}
	idx, err := importIndex(filepath.Join(statusDir, "README.md"), string(raw), areas)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	for _, area := range areas {
		if err := writeJSON(filepath.Join(dataDir, area.ID+".json"), area); err != nil {
			return err
		}
	}
	if err := writeJSON(filepath.Join(dataDir, "_index.json"), idx); err != nil {
		return err
	}
	fmt.Printf("exported %d areas + _index.json to %s\n", len(areas), dataDir)
	return nil
}

func loadData() (map[string]*StatusArea, *StatusIndex, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("reading %s (run 'statusgen export' first?): %w", dataDir, err)
	}
	areas := map[string]*StatusArea{}
	var idx *StatusIndex
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dataDir, e.Name()))
		if err != nil {
			return nil, nil, err
		}
		if e.Name() == "_index.json" {
			idx = &StatusIndex{}
			if err := json.Unmarshal(raw, idx); err != nil {
				return nil, nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			continue
		}
		area := &StatusArea{}
		if err := json.Unmarshal(raw, area); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		areas[area.File] = area
	}
	if idx == nil {
		return nil, nil, fmt.Errorf("%s: no _index.json", dataDir)
	}
	return areas, idx, nil
}

// generateAll renders every page from the data source. write=true writes the
// .md files (make status); write=false diffs against the committed files and
// fails on any difference (the CI guard).
func generateAll(write bool) error {
	areas, idx, err := loadData()
	if err != nil {
		return err
	}
	outputs := map[string]string{}
	var files []string
	for _, area := range areas {
		md, err := generatePage(area)
		if err != nil {
			return err
		}
		outputs[area.File] = md
		files = append(files, area.File)
	}
	md, err := generateIndex(idx, areas)
	if err != nil {
		return err
	}
	outputs["README.md"] = md
	files = append(files, "README.md")
	sort.Strings(files)

	dirty := 0
	for _, f := range files {
		path := filepath.Join(statusDir, f)
		committed, err := os.ReadFile(path)
		upToDate := err == nil && string(committed) == outputs[f]
		switch {
		case write && !upToDate:
			if err := os.WriteFile(path, []byte(outputs[f]), 0o644); err != nil {
				return err
			}
			fmt.Printf("  wrote %s\n", path)
			dirty++
		case !write && !upToDate:
			fmt.Fprintf(os.Stderr, "  ✗ %s differs from its data/ source — run 'make status' (or edit the JSON, never the page)\n", path)
			dirty++
		}
	}
	if write {
		fmt.Printf("status: %d page(s) regenerated, %d up to date\n", dirty, len(files)-dirty)
		return nil
	}
	if dirty > 0 {
		return fmt.Errorf("status check: %d page(s) out of sync with %s", dirty, dataDir)
	}
	fmt.Printf("status check OK — %d page(s) match %s\n", len(files), dataDir)
	return nil
}

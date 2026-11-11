package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Dump mode (-dump-manifest): each Node or WPT test's program, assembled as
// a run compiles it (its shims written beside it), is kept under the work
// directory and listed in the manifest as `program\tlane\ttest`, one line
// per lane, for the shadow report (tools/shadow -manifest) to compile.
// Nothing is compiled or run.
var (
	dumpManifest string
	dumpMu       sync.Mutex
	dumpSeq      int
	dumpUnits    = map[string]string{} // worker tag → the test it is assembling
)

// noteDumpUnit records the test the worker tagged tag assembles next.
func noteDumpUnit(tag, path string) {
	if dumpManifest == "" {
		return
	}
	dumpMu.Lock()
	dumpUnits[tag] = path
	dumpMu.Unlock()
}

// dumpProgram keeps src as its own entry file in entryDir and appends it to
// the manifest under the current lane.
func dumpProgram(src, entryDir, tag string) error {
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		return err
	}
	dumpMu.Lock()
	defer dumpMu.Unlock()
	dumpSeq++
	entry, err := filepath.Abs(filepath.Join(entryDir, fmt.Sprintf("dump%06d.ts", dumpSeq)))
	if err != nil {
		return err
	}
	if err := os.WriteFile(entry, []byte(src), 0o644); err != nil {
		return err
	}
	unit := dumpUnits[tag]
	if unit == "" {
		unit = entry
	}
	f, err := os.OpenFile(dumpManifest, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	lane := "strict"
	if laneCompat == "js" {
		lane = "js"
	}
	_, err = fmt.Fprintf(f, "%s\t%s\t%s\n", entry, lane, unit)
	return err
}

// reportOrDump names where a lane's results went.
func reportOrDump(report string) string {
	if dumpManifest != "" {
		return "programs listed in " + dumpManifest
	}
	return "report written to " + report
}

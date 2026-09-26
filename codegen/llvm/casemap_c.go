package llvm

import (
	_ "embed"
	"strings"
)

// casemap_c.go — the embedded Unicode case-mapping runtime behind
// String.prototype.toUpperCase/toLowerCase (full Default Case Conversion:
// simple mappings, SpecialCasing expansions, Final_Sigma). Compiled alongside
// the program only when one of the two methods is used.

//go:embed casemapsrc/casemap.c
var casemapSource string

//go:embed casemapsrc/casemap_tables.h
var casemapTables string

// CasemapSource returns the C source implementing __kml_str_toupper/tolower
// as one translation unit: the generated tables are spliced in place of the
// `#include "casemap_tables.h"` line, so the sidecar builds from a single
// file wherever the build writes it (main.go / the test builders).
func CasemapSource() string {
	return strings.Replace(casemapSource, "#include \"casemap_tables.h\"", casemapTables, 1)
}

// UsesCasemap reports whether toUpperCase/toLowerCase reached codegen, so the
// build knows to compile+link the case-mapping C file.
func (e *Emitter) UsesCasemap() bool { return e.usedCasemap }

// ensureCasemap marks the program as needing the C file compiled in (the
// `@link casemap` of String's toUpperCase/toLowerCase declarations, which
// declare their own entry points).
func (e *Emitter) ensureCasemap() {
	if e.usedCasemap {
		return
	}
	e.usedCasemap = true
	e.ensureStrHeaderRuntime() // the header runtime the C file allocates through
}

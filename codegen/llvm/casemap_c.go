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

// ensureCasemap declares the two entry points exactly once and marks the
// program as needing the C file compiled in.
func (e *Emitter) ensureCasemap() {
	if e.usedCasemap {
		return
	}
	e.usedCasemap = true
	e.ensureStrHeaderRuntime() // @__kml_str_len for the header length
	e.emitGlobal(`declare ptr @__kml_str_toupper(ptr, i64)`)
	e.emitGlobal(`declare ptr @__kml_str_tolower(ptr, i64)`)
}

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

//go:embed casemapsrc/normalize_tables.h
var normalizeTables string

// CasemapSource returns the C source implementing __kml_str_toupper/tolower
// as one translation unit: the generated tables are spliced in place of the
// `#include "casemap_tables.h"` line, so the sidecar builds from a single
// file wherever the build writes it (main.go / the test builders).
func CasemapSource() string {
	src := strings.Replace(casemapSource, "#include \"casemap_tables.h\"", casemapTables, 1)
	return strings.Replace(src, "#include \"normalize_tables.h\"", normalizeTables, 1)
}

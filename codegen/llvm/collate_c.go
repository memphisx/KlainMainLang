package llvm

import (
	_ "embed"
	"strings"
)

// collate_c.go — the embedded collation runtime behind
// String.prototype.localeCompare (three-level comparison over weights
// generated from ICU's root order). Compiled alongside the program only when
// localeCompare is used.

//go:embed collatesrc/collate.c
var collateSource string

//go:embed collatesrc/collate_tables.h
var collateTables string

// CollateSource returns collate.c with its generated tables spliced in place
// of the `#include "collate_tables.h"` line, one translation unit.
func CollateSource() string {
	return strings.Replace(collateSource, "#include \"collate_tables.h\"", collateTables, 1)
}

// UsesCollate reports whether localeCompare reached codegen.
func (e *Emitter) UsesCollate() bool { return e.usedCollate }

// ensureCollate declares __kml_collate and marks the C file for linking.
func (e *Emitter) ensureCollate() {
	if e.usedCollate {
		return
	}
	e.usedCollate = true
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare i64 @__kml_collate(ptr, ptr, i64)")
}

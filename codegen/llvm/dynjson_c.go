package llvm

import _ "embed"

// dynjson_c.go — the embedded stringify/join runtime for D1 dynamic values
// (TDD-00155 Stage 2): JSON.stringify over tag-10/11 trees (cycle-checked,
// dtoa-backed) and the Array toString join. Compiled alongside the program
// only when a dynamic value is stringified.

//go:embed dynjsonsrc/dynjson.c
var dynJSONSource string

// DynJSONSource returns the C source implementing __kml_dynjson_stringify and
// __kml_dynarr_join. main.go compiles it when UsesDynJSON() is set (libc +
// the dtoa helper, which ensureDynJSONC forces in).
func DynJSONSource() string { return layoutHeader() + dynJSONSource }

// ensureDynJSONC declares the dynjson ABI exactly once and marks the program
// as needing the C file (and its dtoa dependency) compiled in.
func (e *Emitter) ensureDynJSONC() {
	if e.usedDynJSONC {
		return
	}
	e.usedDynJSONC = true
	// The any-array element helpers (ADR-01059) call ToPrimitive/ToNumber and
	// the dynamic-object getter; the C object references them unconditionally,
	// so they must be defined whenever the file is linked in.
	e.ensureDynJSONC()
	// ADR-01059: element access through an `any` holding a boxed static array.
}

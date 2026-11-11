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
func DynJSONSource() string { return dynJSONSource }

// ensureDynJSONC declares the dynjson ABI exactly once and marks the program
// as needing the C file (and its dtoa dependency) compiled in.
func (e *Emitter) ensureDynJSONC() {
	if e.usedDynJSONC {
		return
	}
	e.usedDynJSONC = true
	e.ensureErrorNames()
	e.ensureShapeRuntime()     // a static object renders through its layout row (TDD-00233)
	e.ensureStrHeaderRuntime() // and indexes a string by UTF-16 units (strheader.c)
	e.ensureInspectReduce()    // and the util.inspect layout/quote helpers (ADR-01067)
	// The any-array element helpers (ADR-01059) call ToPrimitive/ToNumber and
	// the dynamic-object getter; the C object references them unconditionally,
	// so they must be defined whenever the file is linked in.
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureStrHeaderRuntime()
	e.ensureFnMeta()           // dynjson.c renders tag-12 functions through fnmeta.c
	e.ensureBoxedBigIntHooks() // and boxed bigint cells through the finalize-time hook
	// ADR-01059: element access through an `any` holding a boxed static array.
}

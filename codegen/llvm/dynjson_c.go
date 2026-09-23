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

// UsesDynJSON reports whether dynamic-value stringify/join reached codegen,
// so the build knows to compile+link the dynjson C file.
func (e *Emitter) UsesDynJSON() bool { return e.usedDynJSONC }

// ensureDynJSONC declares the dynjson ABI exactly once and marks the program
// as needing the C file (and its dtoa dependency) compiled in.
func (e *Emitter) ensureDynJSONC() {
	if e.usedDynJSONC {
		return
	}
	e.usedDynJSONC = true
	e.ensureDtoa()          // dynjson.c calls __kml_dtoa for float rendering
	e.ensureInspectReduce() // and the util.inspect layout/quote helpers (ADR-01067)
	// The any-array element helpers (ADR-01059) call ToPrimitive/ToNumber and
	// the dynamic-object getter; the C object references them unconditionally,
	// so they must be defined whenever the file is linked in.
	e.ensureAnyOps()
	e.ensureAnyToPrimitive()
	e.ensureDynObj()
	e.emitGlobal(`declare ptr @__kml_dynjson_stringify(i64, i64, ptr, ptr)`)
	e.emitGlobal(`declare ptr @__kml_dynarr_join(ptr)`)
	e.emitGlobal(`declare ptr @__kml_array_join(ptr)`)             // TDD-00212; takes the box (ADR-01059)
	e.emitGlobal(`declare ptr @__kml_array_inspect_at(ptr, i64)`)  // TDD-00212 Stage 2; takes the box + nesting depth (ADR-01067)
	e.emitGlobal(`declare ptr @__kml_dynarr_inspect_at(ptr, i64)`) // TDD-00212 Stage 2
	e.emitGlobal(`declare ptr @__kml_dynobj_inspect_at(ptr, i64)`) // dynamic-object console.log form
	// ADR-01059: element access through an `any` holding a boxed static array.
	e.emitGlobal(`declare i64 @__kml_anyarr_get_by_key(ptr, ptr)`)
	e.emitGlobal(`declare ptr @__kml_any_arraylike_f64(i64, ptr)`)
}

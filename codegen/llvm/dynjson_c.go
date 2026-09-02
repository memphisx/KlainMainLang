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
	e.ensureDtoa() // dynjson.c calls __kml_dtoa for float rendering
	e.emitGlobal(`declare ptr @__kml_dynjson_stringify(i64, i64, ptr)`)
	e.emitGlobal(`declare ptr @__kml_dynarr_join(ptr)`)
}

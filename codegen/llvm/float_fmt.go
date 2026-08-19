package llvm

import _ "embed"

// float_fmt.go — the embedded JS-faithful float formatter (TDD-00080). A
// self-contained C helper (libc only) implementing shortest-round-trip
// double-to-string per ECMAScript Number::toString, compiled alongside the
// program (the bigint/JSON embedded-C pattern) only when a program prints a
// float. Replaces the old bare-%g formatting at the scalar, dynamic, and
// printf-format print sites.

//go:embed dtoasrc/dtoa.c
var dtoaSource string

// DtoaSource returns the C source implementing __kml_dtoa. main.go writes it
// next to the .ll and compiles it when UsesFloatFmt() is set (no external
// library — libc only).
func DtoaSource() string { return dtoaSource }

// UsesFloatFmt reports whether any float print reached codegen, so main.go knows
// to compile+link the dtoa C file.
func (e *Emitter) UsesFloatFmt() bool { return e.usesFloatFmt }

// ensureDtoa declares @__kml_dtoa exactly once and marks the program as needing
// the C file compiled in. Call it before every use of the helper.
func (e *Emitter) ensureDtoa() {
	e.usesFloatFmt = true
	if e.declaredDtoa {
		return
	}
	e.declaredDtoa = true
	e.emitGlobal("declare void @__kml_dtoa(ptr, double)")
}

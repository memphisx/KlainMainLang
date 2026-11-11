package llvm

import _ "embed"

// float_fmt.go — the embedded JS-faithful float formatter (TDD-00080). A
// self-contained C helper (libc only) implementing shortest-round-trip
// double-to-string per ECMAScript Number::toString, linked when a program
// names __kml_dtoa (runtime_resolve.go). Replaces the old bare-%g formatting
// at the scalar, dynamic, and printf-format print sites.

//go:embed dtoasrc/dtoa.c
var dtoaSource string

// DtoaSource returns the C source implementing __kml_dtoa (libc only).
func DtoaSource() string { return dtoaSource }

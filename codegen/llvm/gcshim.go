package llvm

import _ "embed"

// GCShimSource is the C source of the GC-mode allocator shim (gcsrc/gcshim.c
// — kept in its own subdirectory since a .c file directly inside a Go
// package directory makes `go build` demand cgo, even when nothing else
// about it uses cgo). main.go writes it out next to the generated .ll file
// and compiles it alongside that file only when -mm=gc is set.
//
//go:embed gcsrc/gcshim.c
var GCShimSource string

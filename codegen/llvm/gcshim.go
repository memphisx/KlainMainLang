package llvm

import (
	_ "embed"
	"fmt"
)

// GCShimSource is the C source of the GC-mode allocator shim (gcsrc/gcshim.c
// — kept in its own subdirectory since a .c file directly inside a Go
// package directory makes `go build` demand cgo, even when nothing else
// about it uses cgo). main.go writes it out next to the generated .ll file
// and compiles it alongside that file only when -mm=gc is set.
//
// The layout-table constants it shares with the emitter come first.
var GCShimSource = fmt.Sprintf("#define KML_UNIT_LAYOUT %d\n#define KML_WORD_TRACED %d /* this kind and above hold an address */\n", unitKindLayout, wordObject) + gcShimC

//go:embed gcsrc/gcshim.c
var gcShimC string

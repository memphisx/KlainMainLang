package llvm

import _ "embed"

// runtime_dynarr.go — the D1 dynamic array (TDD-00155 Stage 2), box tag 11
// (kmlTagDynArray). Layout:
//
//	DynArr header (24 bytes):
//	  offset 0:  i64 len
//	  offset 8:  i64 cap
//	  offset 16: ptr data — element array (realloc-grown, doubling)
//
//	Element (16 bytes): { i64 tag, i64 payload } — the { i8, i64 } any box
//	with the tag widened to i64, same convention as a dynobj entry.
//
// This is the element universe untyped JSON.parse needs (every element is a
// self-describing box) — deliberately distinct from tag 7, which boxes a
// *statically typed* array whose element width/type lives at the call sites.
// dynjsonsrc/dynjson.c reads this layout directly (kept in sync by the
// comment there); all memory is plain malloc/realloc for the -mm=gc shim. The
// routines live in dynobjsrc/dynarr.c (TDD-00240).

//go:embed dynobjsrc/dynarr.c
var dynArrSource string

// DynArrSource is the dynamic-array runtime's C source.
func DynArrSource() string { return dynArrSource }

//go:embed dynobjsrc/dynjsonnode.c
var dynJSONNodeSource string

// DynJSONNodeSource is the JSON-tree -> dynamic-value converter's C source.
func DynJSONNodeSource() string { return dynJSONNodeSource }

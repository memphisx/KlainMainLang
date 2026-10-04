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

// UsesDynArr reports whether the program links the dynamic-array runtime.
func (e *Emitter) UsesDynArr() bool { return e.usedDynArr }

//go:embed dynobjsrc/dynjsonnode.c
var dynJSONNodeSource string

// DynJSONNodeSource is the JSON-tree -> dynamic-value converter's C source.
func DynJSONNodeSource() string { return dynJSONNodeSource }

// UsesDynJSONNode reports whether the program links the converter.
func (e *Emitter) UsesDynJSONNode() bool { return e.usedDynJSONFromNode }

func (e *Emitter) ensureDynArr() {
	if e.usedDynArr {
		return
	}
	e.usedDynArr = true
	e.ensureNanBox()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureStrcmp()
	e.ensureStrtoll()
	e.ensureSprintf()
	e.ensureStrlen()
	e.emitGlobal(`declare ptr @__kml_dynarr_new(i64)
declare i64 @__kml_dynarr_len(ptr)
declare void @__kml_dynarr_grow(ptr, i64)
declare void @__kml_dynarr_push(ptr, i64)
declare void @__kml_dynarr_store(ptr, i64, i64)
declare i64 @__kml_dynarr_at(ptr, i64)
declare void @__kml_dynarr_put(ptr, i64, i64)
declare i64 @__kml_dynarr_index(ptr)
declare i64 @__kml_dynarr_get_by_key(ptr, ptr)
declare zeroext i1 @__kml_dynarr_set_by_key(ptr, ptr, i64)
declare ptr @__kml_index_keys_c(i64)`)
	// {ptr,i64} returns go through IR wrappers (aggregate return ABIs differ
	// between C and IR on Windows).
	e.emitGlobal(`
; Index strings "0".."len-1" (Object.keys / for...in over a dynamic array).
define { ptr, i64 } @__kml_dynarr_keys(ptr %a) {
entry:
  %n = load i64, ptr %a, align 8
  %r = call { ptr, i64 } @__kml_index_keys(i64 %n)
  ret { ptr, i64 } %r
}

; The array index keys "0".."len-1", as Object.keys lists an array's.
define { ptr, i64 } @__kml_index_keys(i64 %len) {
entry:
  %arr = call ptr @__kml_index_keys_c(i64 %len)
  %r0 = insertvalue { ptr, i64 } undef, ptr %arr, 0
  %r1 = insertvalue { ptr, i64 } %r0, i64 %len, 1
  ret { ptr, i64 } %r1
}`)
}

// ensureDynJSONFromNode declares the recursive KmlJsonNode -> dynamic-value
// converter behind untyped JSON.parse (TDD-00155 Stage 2); it lives in
// dynobjsrc/dynjsonnode.c.
func (e *Emitter) ensureDynJSONFromNode() {
	if e.usedDynJSONFromNode {
		return
	}
	e.usedDynJSONFromNode = true
	e.ensureJSONParseTree()
	e.ensureDynObj()
	e.ensureDynArr()
	e.emitGlobal(`declare ptr @__kml_json_key(ptr, i64)`)
	e.emitGlobal(`declare ptr @__kml_json_val(ptr, i64)`)
	e.emitGlobal(`declare i64 @__kml_dynjson_from_node(ptr)`)
}

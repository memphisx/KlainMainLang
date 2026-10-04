package llvm

import _ "embed"

// runtime_dynobj.go — the D1 dynamic object runtime (TDD-00155 Stage 1): a
// per-instance property bag behind box tag 10 (kmlTagDynObject). Layout:
//
//	DynObj header (48 bytes):
//	  offset 0:  i64 flags   — low 32 bits magic 0x444C4D4B ("KMLD"); bit 32
//	                           non-extensible, bit 33 Proxy, bit 34 NULLPROTO
//	                           (prototype explicitly null — set_proto, TDD-00229)
//	  offset 8:  ptr proto   — prototype pointer (live since Stage 3: get and
//	                           the `in` operator walk it; set/delete/keys stay
//	                           own-table; set_proto keeps chains acyclic)
//	  offset 16: ptr props   — property entry array (realloc-grown, doubling)
//	  offset 24: i64 count
//	  offset 32: i64 cap
//	  offset 40: i64 classtag — instanceof TagID when this bag was widened from a
//	                           class instance (ADR-00997), 0 otherwise
//
//	Property entry (32 bytes):
//	  offset 0:  ptr key     — owned NUL-terminated UTF-8 copy
//	  offset 8:  i64 tag     — the logical any-box tag, widened to i64
//	  offset 16: i64 payload — the decoded any-box payload; for an ACCESSOR
//	                           entry: ptr to a 16-byte { getterRec, setterRec }
//	                           pair of raw dynamic-function records (either
//	                           slot null when absent)
//	  offset 24: i64 attrs   — WRITABLE(1)|ENUMERABLE(2)|CONFIGURABLE(4)|
//	                           ACCESSOR(8); =7 on plain assignment. Enforced
//	                           since Stage 5: get/set invoke accessors (with
//	                           the original receiver as `this`), set honors
//	                           WRITABLE + the header's non-extensible bit
//	                           (1<<32), delete honors CONFIGURABLE, and
//	                           Object.keys/for...in enumerate ENUMERABLE only
//
// The entry array is insertion-ordered — that IS JS string-key enumeration
// order for objects built this way — and lookup is a linear strcmp scan
// (correctness-first; a per-entry hash word and D2 hidden classes are later,
// caller-invisible optimizations). Values are self-describing any boxes: no
// static type exists at a dynamic read site, so the per-site-typed
// __kml_map_str_* store cannot back this. All memory comes from plain
// calloc/malloc/realloc so -mm=gc inherits it through the allocator shim.
//
// A deleted entry's key copy is not freed: under -mm=gc it is collected once
// unreferenced; under -mm=manual it leaks, the same contract closures and
// boxes already have. The routines live in dynobjsrc/dynobj.c (TDD-00240).

//go:embed dynobjsrc/dynobj.c
var dynObjSource string

// DynObjSource is the dynamic-object runtime's C source.
func DynObjSource() string { return dynObjSource }

// UsesDynObj reports whether the program links the dynamic-object runtime.
func (e *Emitter) UsesDynObj() bool { return e.usedDynObj }

func (e *Emitter) ensureDynObj() {
	if e.usedDynObj {
		return
	}
	e.usedDynObj = true
	e.ensureNanBox()
	e.ensureAnyOps() // dynobj.c calls __kml_any_tobool for Proxy trap results
	e.ensureCalloc()
	e.ensureMalloc()
	e.ensureFree()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureMemmove()
	e.ensureStrcmp()
	e.ensureStrlen()
	e.emitGlobal(`declare ptr @__kml_dynobj_new()
declare void @__kml_dynobj_set_classtag(ptr, i64)
declare i64 @__kml_dynobj_classtag(ptr)
declare i64 @__kml_dynobj_find(ptr, ptr)
declare void @__kml_dynobj_set(ptr, ptr, i64)
declare i64 @__kml_dynobj_get(ptr, ptr)
declare i1 @__kml_dynobj_ordinary(ptr)
declare i64 @__kml_dynobj_attrs_at(ptr, i64)
declare i64 @__kml_dynobj_rawtag_at(ptr, i64)
declare i64 @__kml_dynobj_rawpay_at(ptr, i64)
declare i64 @__kml_dynobj_get_at(ptr, i64)
declare i64 @__kml_dynobj_setv(ptr, ptr, i64)
declare zeroext i1 @__kml_dynobj_has(ptr, ptr)
declare zeroext i1 @__kml_dynobj_has_chain(ptr, ptr)
declare ptr @__kml_dynobj_get_proto(ptr)
declare ptr @__kml_object_prototype()
declare zeroext i1 @__kml_dynobj_set_proto(ptr, ptr)
declare zeroext i1 @__kml_dynobj_delete(ptr, ptr)
declare i64 @__kml_dynobj_count(ptr)
declare ptr @__kml_dynobj_key_at(ptr, i64)
declare i64 @__kml_dynobj_is_index(ptr)
declare void @__kml_dynobj_es_reorder(ptr, i64)
declare zeroext i1 @__kml_key_is_symbol(ptr)
declare ptr @__kml_dynobj_keys_c(ptr, ptr)
declare ptr @__kml_dynobj_keys_enum_c(ptr, ptr)
declare void @__kml_dynobj_merge(ptr, ptr)
declare void @__kml_dynobj_defacc(ptr, ptr, ptr, ptr)
declare void @__kml_dynobj_patch(ptr, i64, i64, i64, i64)
declare void @__kml_dynobj_prevent(ptr, i64)
declare zeroext i1 @__kml_dynobj_flags_test(ptr, i64)`)
	// The key lists return {ptr,i64}: an aggregate return differs between the
	// C and IR ABIs on Windows, so C hands back the array and its length
	// through an out-parameter and these wrappers build the pair.
	e.emitGlobal(`
; getOwnPropertyNames: every own string key, in ES order.
define { ptr, i64 } @__kml_dynobj_keys(ptr %o) {
entry:
  %np = alloca i64, align 8
  %arr = call ptr @__kml_dynobj_keys_c(ptr %o, ptr %np)
  %n = load i64, ptr %np, align 8
  %r0 = insertvalue { ptr, i64 } undef, ptr %arr, 0
  %r1 = insertvalue { ptr, i64 } %r0, i64 %n, 1
  ret { ptr, i64 } %r1
}

; Object.keys / for...in / JSON: own enumerable string keys, in ES order.
define { ptr, i64 } @__kml_dynobj_keys_enum(ptr %o) {
entry:
  %np = alloca i64, align 8
  %arr = call ptr @__kml_dynobj_keys_enum_c(ptr %o, ptr %np)
  %n = load i64, ptr %np, align 8
  %r0 = insertvalue { ptr, i64 } undef, ptr %arr, 0
  %r1 = insertvalue { ptr, i64 } %r0, i64 %n, 1
  ret { ptr, i64 } %r1
}`)
}

// wellKnownMemberKeys are the well-known symbols a `[Symbol.x]` member name
// compiles to "@@x" for (parser_classes.go), on classes and object literals
// alike; a bag keys them the same way (emitSymbolPropertyKey).
// wellKnownMemberKeys is mirrored by __kml_key_is_symbol (dynobjsrc/dynobj.c).
var wellKnownMemberKeys = []string{"iterator", "asyncIterator", "toPrimitive", "toStringTag", "dispose", "asyncDispose"}

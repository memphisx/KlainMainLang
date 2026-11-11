package llvm

import "fmt"

import _ "embed"

// runtime_dynobj.go — the D1 dynamic object runtime (TDD-00155 Stage 1): a
// per-instance property bag behind box tag 10 (kmlTagDynObject). Layout:
//
//	DynObj header (56 bytes):
//	  offset 0:  i64 header  — kmlBagHdr (the header magic, type id 1) and
//	                           the bag flags (objheader.go, kml_heap.h
//	                           KML_BAG_*: non-extensible, Proxy, null
//	                           prototype — set_proto, TDD-00229 — and more)
//	  offset 8:  ptr proto   — prototype pointer (live since Stage 3: get and
//	                           the `in` operator walk it; set/delete/keys stay
//	                           own-table; set_proto keeps chains acyclic)
//	  offset 16: ptr props   — property entry array (realloc-grown, doubling)
//	  offset 24: i64 count
//	  offset 32: i64 cap
//	  offset 40: i64 classtag — instanceof TagID when this bag was widened from a
//	                           class instance (ADR-00997), 0 otherwise
//	  offset 48: i64 shape   — reserved for hidden classes (TDD-00220), 0
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
//	                           WRITABLE + the header's non-extensible flag,
//	                           delete honors CONFIGURABLE, and
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

// emitDynObjKeyList is a bag's own string keys in ES order as a {ptr, i64}
// aggregate register: every one (getOwnPropertyNames), or the enumerable
// ones (Object.keys, for...in, JSON) when enumerable. C hands back the
// array and writes its length through an out-parameter, since an aggregate
// return differs between the C and IR ABIs on Windows.
func (e *Emitter) emitDynObjKeyList(bag string, enumerable bool) string {
	fn := "__kml_dynobj_keys_c"
	if enumerable {
		fn = "__kml_dynobj_keys_enum_c"
	}
	return e.emitKeyListCall(fn, bag)
}

// emitKeyListCall calls key-list function fn (`char **fn(void *, i64 *n)`)
// on bag and builds its {ptr, i64} aggregate.
func (e *Emitter) emitKeyListCall(fn, bag string) string {
	np := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", np))
	arr, n, r0, r := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @%s("+irPtr+" %s, "+irPtr+" %s)", arr, fn, bag, np))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", n, np))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { "+irPtr+", i64 } undef, "+irPtr+" %s, 0", r0, arr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { "+irPtr+", i64 } %s, i64 %s, 1", r, r0, n))
	return r
}

// wellKnownMemberKeys are the well-known symbols a `[Symbol.x]` member name
// compiles to "@@x" for (parser_classes.go), on classes and object literals
// alike; a bag keys them the same way (emitSymbolPropertyKey).
// wellKnownMemberKeys is mirrored by __kml_key_is_symbol (dynobjsrc/dynobj.c).
var wellKnownMemberKeys = []string{"iterator", "asyncIterator", "toPrimitive", "toStringTag", "dispose", "asyncDispose", "species", "hasInstance"}

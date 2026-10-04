// runtime_collections.go — the Map/Set runtime, Object.groupBy's group map, the
// sort comparators and Object.freeze's frozen-object set. The routines live in
// collectionssrc/ (TDD-00240): collections.c holds the string- and number-keyed
// maps (the hash table is collections_engine.h), the iterator list, the group
// map, the comparators and the frozen set; collections_any.c the any-keyed map,
// linked only by programs that use one. What stays here as IR text: the
// {ptr, i64}-returning keys()/vals()/get() wrappers (a C struct return is not
// the IR aggregate return on every target), and the comparators that call a
// generated or separately linked routine (the any-element and float default
// sorts, the any custom-comparator trampoline).
package llvm

import (
	"KlainMainLang/ast"
	_ "embed"
	"fmt"
	"strings"
)

//go:embed collectionssrc/collections_engine.h
var collectionsEngineSource string

//go:embed collectionssrc/arrayguard.c
var arrayGuardSource string

// ArrayGuardSource is the array integrity runtime (Object.freeze of an array).
func ArrayGuardSource() string { return arrayGuardSource }

// UsesArrayGuard reports whether the program links arrayguard.c.
func (e *Emitter) UsesArrayGuard() bool { return e.usedArrayGuard }

// ensureArrayGuard declares the array integrity runtime: the level an
// array's header carries in the frozen set, and the guard every mutation
// calls (arrayguard.c).
func (e *Emitter) ensureArrayGuard() {
	if e.usedArrayGuard {
		return
	}
	e.usedArrayGuard = true
	e.ensureFrozenSet()
	e.ensureNullDerefThrow()
	e.emitGlobal(`declare void @__kml_array_guard(ptr, i64, i64, i64)
declare i32 @__kml_array_integrity(ptr, i64)
declare void @__kml_array_set_level(ptr, i64)
@__kml_array_integrity_any = external global i8`)
}

// Array mutation kinds __kml_array_guard checks (arrayguard.c).
const (
	arrOpSet     = 0
	arrOpAdd     = 1
	arrOpDelete  = 2
	arrOpLength  = 3
	arrOpShift   = 4
	arrOpSplice  = 5
	arrOpReorder = 6
)

// emitArrayGuard checks a mutation of the array whose header is hdr against
// the array's integrity level: a no-op unless the program freezes, seals or
// makes an array non-extensible.
func (e *Emitter) emitArrayGuard(hdr string, op int, a, b string) {
	e.ensureArrayGuard()
	flag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr @__kml_array_integrity_any, align 1", flag))
	any := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", any, flag))
	checkL := e.freshLabel("arrguard.check")
	okL := e.freshLabel("arrguard.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", any, checkL, okL))
	e.emitLabel(checkL)
	e.emitInstr(fmt.Sprintf("call void @__kml_array_guard(ptr %s, i64 %d, i64 %s, i64 %s)", hdr, op, a, b))
	e.emitTerminator(fmt.Sprintf("br label %%%s", okL))
	e.emitLabel(okL)
}

// emitArrayGuardFor guards an in-place write to the array objExpr names
// when it names one without side effects (a binding, or a field of one): a
// fresh array has no integrity level. idx is the written element's index
// expression, when there is one without side effects.
func (e *Emitter) emitArrayGuardFor(objExpr ast.Expression, op int, idx ast.Expression) {
	if !pureArrayRef(objExpr) {
		return
	}
	if t := e.inferExprType(objExpr); !t.IsArray || t.IsTypedArray || t.IsFlatArray {
		return
	}
	hdr, _, _, err := e.resolveArrayMutLoc(objExpr, "write", objExpr.GetPos())
	if err != nil {
		return
	}
	a := "0"
	switch idx.(type) {
	case *ast.Identifier, *ast.NumberLiteral:
		if v, err := e.emitExpr(idx); err == nil {
			if v, err = e.arrayIndexToI64(v, idx.GetPos()); err == nil {
				a = v.Ref
			}
		}
	}
	e.emitArrayGuard(hdr, op, a, "0")
}

// pureArrayRef reports an expression naming an array without side effects.
func pureArrayRef(x ast.Expression) bool {
	switch x := x.(type) {
	case *ast.Identifier:
		return true
	case *ast.NonNullExpression:
		return pureArrayRef(x.Arg)
	case *ast.MemberExpression:
		if _, ok := x.Object.(*ast.ThisExpression); ok {
			return !x.Optional
		}
		return !x.Optional && pureArrayRef(x.Object)
	}
	return false
}

// emitArraySetLevel records an array's integrity level (staticIntegrity*).
func (e *Emitter) emitArraySetLevel(hdr string, level int) {
	e.ensureArrayGuard()
	e.emitInstr(fmt.Sprintf("call void @__kml_array_set_level(ptr %s, i64 %d)", hdr, level))
}

//go:embed collectionssrc/collections.c
var collectionsSource string

//go:embed collectionssrc/collections_any.c
var collectionsAnySource string

// CollectionsSource is the Map/Set/sort/frozen-set runtime's C source, behind
// kml_layout.h. gc selects the -mm=gc allocation of the frozen set's header.
func CollectionsSource(gc bool) string {
	pre := ""
	if gc {
		pre = "#define KML_GC_MODE 1\n"
	}
	return layoutHeader() + pre + collectionsEngineSource + collectionsSource
}

// CollectionsAnySource is the any-keyed map's C source; it needs the box
// constants the IR hash used.
func CollectionsAnySource() string {
	return layoutHeader() + fmt.Sprintf(
		"#define KML_BIGINT_MAGIC %dLL\n#define KML_HDR_MAGIC_HOST_MASK %dLL\n#define KML_HDR_MAGIC_HOST %dLL\n",
		kmlBoxedBigIntMagic, kmlHdrMagicMask|hostTypeIDFlag, kmlHdrMagic|hostTypeIDFlag) +
		collectionsEngineSource + collectionsAnySource
}

// UsesCollections reports whether the program links collections.c.
func (e *Emitter) UsesCollections() bool { return e.usedCollections }

// UsesCollectionsAny reports whether the program links collections_any.c.
func (e *Emitter) UsesCollectionsAny() bool { return e.usedCollectionsAny }

// --- Sort helpers ---

// ensureSortClosGlobal declares the closure a custom-comparator sort calls
// (the global lives in collections.c).
func (e *Emitter) ensureSortClosGlobal() {
	if !e.usedSortClosGlobal {
		e.usedCollections = true
		e.emitGlobal("@__kml_sort_clos = external thread_local global ptr")
		e.usedSortClosGlobal = true
	}
}

// ensureSortCmpI64Lex is the JS-faithful DEFAULT (no-comparator) integer sort:
// real Array.prototype.sort with no comparator converts every element to a
// string and compares lexicographically — even for numbers — so [10,1,21,2]
// sorts to [1,10,2,21], NOT the numeric [1,2,10,21]. (A custom comparator
// still routes through the numeric trampoline; only the default changes.)
func (e *Emitter) ensureSortCmpI64Lex() {
	if e.usedSortCmpI64Lex {
		return
	}
	e.usedSortCmpI64Lex = true
	e.usedCollections = true
	e.emitGlobal("declare i32 @__kml_cmp_i64_lex(ptr, ptr)")
}

// ensureSortCmpF64Lex is the float counterpart of ensureSortCmpI64Lex — the
// default no-comparator sort renders each double via the JS-faithful
// shortest-round-trip formatter (__kml_dtoa) and strcmp's the results.
func (e *Emitter) ensureSortCmpF64Lex() {
	if e.usedSortCmpF64Lex {
		return
	}
	e.usedSortCmpF64Lex = true
	e.ensureDtoa()
	e.ensureStrcmp()
	e.emitGlobal(`define i32 @__kml_cmp_f64_lex(ptr %pa, ptr %pb) {
  %a = load double, ptr %pa, align 8
  %b = load double, ptr %pb, align 8
  %ba = alloca [32 x i8], align 1
  %bb = alloca [32 x i8], align 1
  call void @__kml_dtoa(ptr %ba, double %a)
  call void @__kml_dtoa(ptr %bb, double %b)
  %r = call i32 @strcmp(ptr %ba, ptr %bb)
  ret i32 %r
}`)
}

func (e *Emitter) ensureSortCmpStr() {
	if e.usedSortCmpStr {
		return
	}
	e.usedSortCmpStr = true
	e.usedCollections = true
	e.emitGlobal("declare i32 @__kml_cmp_str(ptr, ptr)")
}

// ensureAnyToCStr emits `ptr @__kml_any_to_cstr(i64 %v)` — the callable
// runtime counterpart of the inline emitDynamicToString: it converts one
// NaN-boxed `any` value to its JS String() form as a null-terminated heap
// string. A callable function (not the inline emitter helper) is needed by
// runtime comparators that qsort invokes — the default-comparator sort of a
// boxed-element array (`any[]`, TDD-00200), which is lexicographic over each
// element's string form, exactly like the concrete-typed default sort. Built
// with the builder-swap idiom (cf. emitClassStaticInit) so it can reuse the
// full tag-dispatch of emitDynamicToString rather than duplicating it as raw
// IR.
func (e *Emitter) ensureAnyToCStr() {
	if e.usedAnyToCStr {
		return
	}
	e.usedAnyToCStr = true

	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = TypePtr

	// %v is the incoming boxed value; the first fresh reg is %1, so reserve
	// the parameter name explicitly in the signature below.
	sv, err := e.emitDynamicToString(Value{Ref: "%v", Ty: TypeAny})
	if err != nil {
		// emitDynamicToString only errors on unsupported shapes it never hits
		// for a bare any; treat as unreachable but keep the builder consistent.
		panic(err)
	}
	e.emitTerminator(fmt.Sprintf("ret ptr %s", sv.Ref))

	e.functions.WriteString("\ndefine ptr @__kml_any_to_cstr(i64 %v) {\nentry:\n")
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
	e.blockDone = savedBlockDone
}

// ensureSortCmpAnyLex emits the default (no-comparator) sort comparator for a
// boxed-element array (`any[]`): each slot is a NaN box, converted to its JS
// String() form and compared lexicographically — matching JS's default sort,
// which ToString's every element regardless of type.
func (e *Emitter) ensureSortCmpAnyLex() {
	if e.usedSortCmpAnyLex {
		return
	}
	e.usedSortCmpAnyLex = true
	e.ensureStrcmp()
	e.ensureAnyToCStr()
	e.emitGlobal(`define i32 @__kml_cmp_any_lex(ptr %pa, ptr %pb) {
  %a = load i64, ptr %pa, align 8
  %b = load i64, ptr %pb, align 8
  %as = call ptr @__kml_any_to_cstr(i64 %a)
  %bs = call ptr @__kml_any_to_cstr(i64 %b)
  %r = call i32 @strcmp(ptr %as, ptr %bs)
  ret i32 %r
}`)
}

// ensureSortTrampolineAny is the custom-comparator trampoline for a boxed-
// element array (`any[]`): elements are NaN boxes (i64 slots) passed straight
// to the closure (whose params are `any`), and the closure returns a boxed
// `any` number (e.g. `(x, y) => x - y` under -compat=js any-arithmetic). The
// i64 result is a NaN box, so it is reduced to a double via __kml_any_tonum
// before taking qsort's -1/0/1 sign — reading the box bits as an integer
// (the i64 trampoline) made the sort a silent no-op.
func (e *Emitter) ensureSortTrampolineAny() {
	if e.usedSortTrampolineAny {
		return
	}
	e.usedSortTrampolineAny = true
	e.ensureSortClosGlobal()
	e.ensureAnyOps() // __kml_any_tonum
	e.emitGlobal(`define i32 @__kml_sort_tramp_any(ptr %pa, ptr %pb) {
  %clos = load ptr, ptr @__kml_sort_clos, align 8
  %a = load i64, ptr %pa, align 8
  %b = load i64, ptr %pb, align 8
  %fp_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 0
  %fp = load ptr, ptr %fp_slot, align 8
  %ep_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 1
  %ep = load ptr, ptr %ep_slot, align 8
  %r = call i64 (ptr, i64, i64) %fp(ptr %ep, i64 %a, i64 %b)
  %rd = call double @__kml_any_tonum(i64 %r)
  %neg = fcmp olt double %rd, 0.0
  %pos = fcmp ogt double %rd, 0.0
  %s1 = select i1 %pos, i32 1, i32 0
  %ri = select i1 %neg, i32 -1, i32 %s1
  ret i32 %ri
}`)
}

// ensureSortTrampolineI64 declares the trampoline for a custom comparator
// over integer elements; the trampoline and the closure global are in
// collections.c.
func (e *Emitter) ensureSortTrampolineI64() {
	if e.usedSortTrampolineI64 {
		return
	}
	e.usedSortTrampolineI64 = true
	e.ensureSortClosGlobal()
	e.emitGlobal("declare i32 @__kml_sort_tramp_i64(ptr, ptr)")
}

// ensureSortTrampolineF64: the comparator returns a `number` (double,
// TDD-00123); the trampoline reduces it to qsort's -1/0/1 sign.
func (e *Emitter) ensureSortTrampolineF64() {
	if e.usedSortTrampolineF64 {
		return
	}
	e.usedSortTrampolineF64 = true
	e.ensureSortClosGlobal()
	e.emitGlobal("declare i32 @__kml_sort_tramp_f64(ptr, ptr)")
}

// ensureSortTrampolineObj is the comparator trampoline for an object/class
// element array (ADR-00681): elements are object pointers and the comparator
// returns a `number` (double).
func (e *Emitter) ensureSortTrampolineObj() {
	if e.usedSortTrampolineObj {
		return
	}
	e.usedSortTrampolineObj = true
	e.ensureSortClosGlobal()
	e.emitGlobal("declare i32 @__kml_sort_tramp_obj(ptr, ptr)")
}

// ensureSortTrampolineStr: a string comparator also returns a `number`
// (double, TDD-00123).
func (e *Emitter) ensureSortTrampolineStr() {
	if e.usedSortTrampolineStr {
		return
	}
	e.usedSortTrampolineStr = true
	e.ensureSortClosGlobal()
	e.emitGlobal("declare i32 @__kml_sort_tramp_str(ptr, ptr)")
}

// --- Map / Set helpers ---
//
// A Map/Set header is 72 bytes (collections_engine.h): size, cap, keys, vals,
// the hash index, its capacity, the occupied+tombstone count, flags, the open
// iterators. keys/vals stay dense and insertion-ordered (JS Map iteration
// order); every lookup goes through the open-addressing index. Set reuses the
// layout; elements are stored as keys and vals is ignored.

// ensureMapClear declares __kml_map_clear, shared by Map.clear/Set.clear for
// every key kind: resets size, the hash index and the open iterators.
func (e *Emitter) ensureMapClear() {
	if e.usedMapClear {
		return
	}
	e.usedMapClear = true
	e.usedCollections = true
	e.ensureMapIters()
	e.emitGlobal("declare void @__kml_map_clear(ptr)")
}

// ensureMapIters declares the live iteration of a Map or Set: each open
// iterator is a node on the map's list, so a removal before its position
// shifts it back and clear() rewinds it (collections.c).
func (e *Emitter) ensureMapIters() {
	if e.usedMapIters {
		return
	}
	e.usedMapIters = true
	e.usedCollections = true
	e.emitGlobal(`declare ptr @__kml_map_iter_open(ptr)
declare void @__kml_map_iter_close(ptr)`)
}

// emitMapFlavor declares one key flavor's C entry points (sfx: str/num/any;
// key: the IR key type) and defines its keys()/vals() wrappers.
func (e *Emitter) emitMapFlavor(sfx, key string) {
	e.declareFn("__kml_map_copy_keys", "declare ptr @__kml_map_copy_keys(ptr)")
	e.declareFn("__kml_map_copy_vals", "declare ptr @__kml_map_copy_vals(ptr)")
	var b strings.Builder
	fmt.Fprintf(&b, `declare ptr @__kml_map_%[1]s_create()
declare void @__kml_map_%[1]s_set(ptr, %[2]s, i64)
declare i64 @__kml_map_%[1]s_get(ptr, %[2]s)
declare zeroext i1 @__kml_map_%[1]s_has(ptr, %[2]s)
declare zeroext i1 @__kml_map_%[1]s_delete(ptr, %[2]s)
`, sfx, key)
	for _, part := range []string{"keys", "vals"} {
		fmt.Fprintf(&b, `
define {ptr, i64} @__kml_map_%[1]s_%[2]s(ptr %%map) {
entry:
  %%size = load i64, ptr %%map, align 8
  %%arr = call ptr @__kml_map_copy_%[2]s(ptr %%map)
  %%r0 = insertvalue {ptr, i64} undef, ptr %%arr, 0
  %%r1 = insertvalue {ptr, i64} %%r0, i64 %%size, 1
  ret {ptr, i64} %%r1
}
`, sfx, part)
	}
	e.emitGlobal(b.String())
}

// ensureMapStrHelpers declares the string-keyed Map/Set (FNV-1a, content
// compare, a null key hashing as the empty string).
func (e *Emitter) ensureMapStrHelpers() {
	if e.usedMapStrHelpers {
		return
	}
	e.usedMapStrHelpers = true
	e.usedCollections = true
	e.ensureMapIters()
	e.emitMapFlavor("str", "ptr")
}

// ensureMapNumHelpers declares the number-keyed Map/Set (raw i64 keys, an
// xor-fold multiply hash).
func (e *Emitter) ensureMapNumHelpers() {
	if e.usedMapNumHelpers {
		return
	}
	e.usedMapNumHelpers = true
	e.usedCollections = true
	e.ensureMapIters()
	e.emitMapFlavor("num", "i64")
}

// ensureFrozenSet declares the global frozen-object tracker Object.freeze(obj)
// uses: a Set<number>-shaped structure (the number-keyed map Set<T> itself is
// built on, keyed on ptrtoint(obj)). __kml_frozen_set_get() lazily creates the
// one per-thread set on first use and returns it — called both by
// Object.freeze (to add) and by every object-field write site (to check), so
// a program that never calls Object.freeze never pays for the lazy-init branch
// beyond one null check, but any object mutation still pays one lookup against
// the (possibly empty) frozen set — an unconditional correctness cost, the
// same trade-off ADR-00044's array bounds check already made. Under -mm=gc the
// header is allocated uncollectable (Boehm does not scan thread-local storage).
func (e *Emitter) ensureFrozenSet() {
	if e.usedFrozenSet {
		return
	}
	e.usedFrozenSet = true
	e.usedCollections = true
	e.ensureMapNumHelpers()
	e.emitGlobal("declare ptr @__kml_frozen_set_get()")
}

// ensureMapAnyHelpers declares the any-keyed Map/Set (TDD-00211): keys and
// values are NaN-boxed i64 words, hashed consistently with JS SameValueZero
// (collections_any.c). get returns the undefined box on a miss.
func (e *Emitter) ensureMapAnyHelpers() {
	if e.usedMapAnyHelpers {
		return
	}
	e.usedMapAnyHelpers = true
	e.usedCollections = true
	e.usedCollectionsAny = true
	e.ensureMapIters()
	e.ensureAnyEq()            // __kml_any_eq, for SameValueZero
	e.ensureFnMeta()           // __kml_fn_identity_dyn: a function key hashes by identity
	e.ensureBoxedBigIntHooks() // __kml_boxed_bigint_str: a bigint key hashes by value
	e.emitMapFlavor("any", "i64")
}

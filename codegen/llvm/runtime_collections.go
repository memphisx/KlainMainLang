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
)

//go:embed collectionssrc/collections_engine.h
var collectionsEngineSource string

//go:embed collectionssrc/arrayguard.c
var arrayGuardSource string

// ArrayGuardSource is the array integrity runtime (Object.freeze of an array).
func ArrayGuardSource() string { return layoutHeader() + arrayGuardSource }

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
	e.ensureErrorNames()
	flag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" @__kml_array_integrity_any, align 1", flag))
	any := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", any, flag))
	checkL := e.freshLabel("arrguard.check")
	okL := e.freshLabel("arrguard.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", any, checkL, okL))
	e.emitLabel(checkL)
	e.emitInstr(fmt.Sprintf("call void @__kml_array_guard("+irPtr+" %s, i64 %d, i64 %s, i64 %s)", hdr, op, a, b))
	e.emitTerminator(fmt.Sprintf("br label %%%s", okL))
	e.emitLabel(okL)
}

// emitArrayWriteGuard is emitArrayGuard for a direct element or length
// write: in sloppy code a refused write branches to skipL, doing nothing.
func (e *Emitter) emitArrayWriteGuard(hdr string, op int, a, skipL string) {
	if !e.sloppyWrite {
		e.emitArrayGuard(hdr, op, a, "0")
		return
	}
	e.ensureErrorNames()
	flag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" @__kml_array_integrity_any, align 1", flag))
	any := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", any, flag))
	checkL, okL := e.freshLabel("arrguard.check"), e.freshLabel("arrguard.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", any, checkL, okL))
	e.emitLabel(checkL)
	r, refused := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_array_refuses("+irPtr+" %s, i64 %d, i64 %s)", r, hdr, op, a))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", refused, r))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", refused, skipL, okL))
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
	if t := e.typeOfExpr(objExpr); !t.IsArray() || t.IsTypedArray() || t.IsFlatArray() {
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
	e.ensureErrorNames()
	e.emitInstr(fmt.Sprintf("call void @__kml_array_set_level("+irPtr+" %s, i64 %d)", hdr, level))
}

//go:embed collectionssrc/collections.c
var collectionsSource string

//go:embed collectionssrc/collections_any.c
var collectionsAnySource string

// CollectionsSource is the Map/Set/sort/frozen-set runtime's C source, behind
// kml_layout.h.
func CollectionsSource() string {
	return layoutHeader() + fmt.Sprintf("#define KML_SYMBOL_TYPEID %dLL\n", symbolTypeIDFlag) + collectionsEngineSource + collectionsSource
}

// CollectionsAnySource is the any-keyed map's C source; it needs the box
// constants the IR hash used.
func CollectionsAnySource() string {
	return layoutHeader() + fmt.Sprintf(
		"#define KML_BIGINT_MAGIC %dLL\n#define KML_HDR_MAGIC_HOST_MASK %dLL\n#define KML_HDR_MAGIC_HOST %dLL\n",
		kmlBoxedBigIntMagic, kmlHdrMagicMask|hostTypeIDFlag, kmlHdrMagic|hostTypeIDFlag) +
		collectionsEngineSource + collectionsAnySource
}

// --- Sort helpers ---

// ensureSortCmpAnyLex emits the default (no-comparator) sort comparator for a
// boxed-element array (`any[]`): each slot is a NaN box, converted to its JS
// String() form and compared in JavaScript's string order, as JS's default
// sort ToString's every element regardless of type.
func (e *Emitter) ensureSortCmpAnyLex() {
	if e.usedSortCmpAnyLex {
		return
	}
	e.usedSortCmpAnyLex = true
	e.ensureStrHeaderRuntime() // __kml_str_cmp: JavaScript's string order
	restore := e.beginDetachedFunc()
	var str [2]string
	for i, p := range []string{"%pa", "%pb"} {
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", v, p))
		s, err := e.emitDynamicToString(Value{Ref: v, Ty: TypeAny})
		if err != nil {
			panic(err) // a bare any always renders
		}
		str[i] = s.Ref
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_str_cmp("+irPtr+" %s, "+irPtr+" %s)", r, str[0], str[1]))
	e.emitTerminator("ret i32 " + r)
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("i32 @__kml_cmp_any_lex("+irPtr+" %pa, "+irPtr+" %pb)", body)
}

// --- Map / Set helpers ---
//
// A Map/Set header is 72 bytes (collections_engine.h): size, cap, keys, vals,
// the hash index, its capacity, the occupied+tombstone count, flags, the open
// iterators. keys/vals stay dense and insertion-ordered (JS Map iteration
// order); every lookup goes through the open-addressing index. Set reuses the
// layout; elements are stored as keys and vals is ignored.

// emitMapFlavor declares one key flavor's C entry points (sfx: str/num/any;
// key: the IR key type).
func (e *Emitter) emitMapFlavor(sfx, key string) {
	e.declareFn("__kml_map_copy_keys", "declare "+irPtr+" @__kml_map_copy_keys("+irPtr+")")
	e.declareFn("__kml_map_copy_vals", "declare "+irPtr+" @__kml_map_copy_vals("+irPtr+")")
	e.emitGlobal(fmt.Sprintf(`declare `+irPtr+` @__kml_map_%[1]s_create()
declare `+irPtr+` @__kml_map_%[1]s_create_h(i64)
declare void @__kml_map_%[1]s_set(`+irPtr+`, %[2]s, i64)
declare i64 @__kml_map_%[1]s_get(`+irPtr+`, %[2]s)
declare zeroext i1 @__kml_map_%[1]s_has(`+irPtr+`, %[2]s)
declare zeroext i1 @__kml_map_%[1]s_delete(`+irPtr+`, %[2]s)`, sfx, key))
}

// emitMapCopy is a Map's or Set's keys or values (part "keys"/"vals") copied
// into a fresh array, as a {ptr, i64} aggregate register: the length is the
// map's size, its first word.
func (e *Emitter) emitMapCopy(part, mapPtr string) string {
	e.declareFn("__kml_map_copy_"+part, "declare "+irPtr+" @__kml_map_copy_"+part+"("+irPtr+")")
	size, arr, r0, r := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", size, mapPtr))
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_map_copy_%s("+irPtr+" %s)", arr, part, mapPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {"+irPtr+", i64} undef, "+irPtr+" %s, 0", r0, arr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {"+irPtr+", i64} %s, i64 %s, 1", r, r0, size))
	return r
}

// ensureMapStrHelpers declares the string-keyed Map/Set (FNV-1a, content
// compare, a null key hashing as the empty string).
func (e *Emitter) ensureMapStrHelpers() {
	if e.usedMapStrHelpers {
		return
	}
	e.usedMapStrHelpers = true
	e.emitMapFlavor("str", irPtr)
}

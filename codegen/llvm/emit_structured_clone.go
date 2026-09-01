package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emit_structured_clone.go — global structuredClone(obj): a genuine
// recursive deep copy, dispatched entirely on obj's static Type (this
// compiler's types are fully known at compile time, so — unlike real JS's
// runtime-shaped structured-clone algorithm — the whole recursion can be
// unrolled into straight-line codegen instead of needing a generic runtime
// walker). See docs/status/GLOBAL-FUNCTIONS.md.
//
// Scalars (numbers, booleans, strings, Date's i64 timestamp, null/
// undefined) are value types already — "cloning" one is just using the
// same SSA value again, no allocation needed. Arrays (including nested
// arrays and TypedArrays, all IsArray under the hood), plain objects, and
// Maps/Sets (ADR-00574, with scalar/string/object key & value element
// types) are heap-allocated and genuinely copied, recursing into every
// element/field/entry. Anything with reference-like or non-trivial-identity
// semantics (EventEmitter, URL, URLSearchParams, ArrayBuffer, functions,
// class instances, Error, Promise, any/unknown) is rejected at compile
// time rather than silently aliased — the correctness bug a shallow
// pass-through would otherwise introduce (a "clone" that still shares the
// original's mutable backing storage).

// emitStructuredClone implements the global structuredClone(obj) function.
func (e *Emitter) emitStructuredClone(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: structuredClone takes exactly 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	return e.emitDeepClone(val, val.Ty, pos)
}

// structuredCloneUnsupportedKind names the first reference-like flag it
// finds set on ty, or "" if ty is a plain scalar/array/object structuredClone
// knows how to copy. URL/URLSearchParams/Headers are checked ahead of the
// generic IsObject/IsMap checks they also happen to set, purely so the error
// names the type the user actually wrote rather than its storage
// representation.
func structuredCloneUnsupportedKind(ty Type) string {
	switch {
	case ty.IsSymbol:
		return "Symbol"
	case ty.IsURL:
		// Node itself throws DataCloneError for a URL (not a serializable type),
		// so refusing it here matches — as a compile-time rejection.
		return "URL"
	case ty.IsURLSearchParams:
		return "URLSearchParams"
	case ty.IsHeaders:
		return "Headers"
	// Map/Set are cloneable (ADR-00574) — validated in emitDeepCloneMap/Set,
	// which reject only a nested-collection/array element type.
	case ty.IsEventEmitter:
		return "EventEmitter"
	case ty.IsBroadcastChannel:
		return "a BroadcastChannel"
	case ty.IsMessageChannel:
		// The pair box crosses nowhere; a MessagePort (either half alone)
		// deliberately passes — it is shared by reference, like an SAB
		// (TDD-00099).
		return "a MessageChannel"
	// ArrayBuffer (plain and shared) is handled in emitDeepClone: a plain one is
	// byte-copied (ADR-00591), a SharedArrayBuffer passes by reference
	// (share-not-copy, TDD-00099) — neither is rejected here.
	case ty.IsFunc:
		return "a function"
	case ty.IsResponse:
		return "Response"
	case ty.IsRequest:
		return "Request"
	case ty.IsFetchRequest:
		return "Request"
	case ty.IsXHR:
		return "XMLHttpRequest"
	case ty.IsClass:
		return "a class instance"
	// Error is handled in emitDeepClone (its kind/message/name are copied,
	// ADR-00592).
	case ty.IsPromise:
		return "a Promise"
	case ty.IsDynamic:
		return "an any/unknown value"
	case ty.IsDynamicObject:
		return "a computed-key object"
	}
	return ""
}

// emitDeepClone recursively clones val (of static type ty). See the file
// doc comment above for the exact scope: array and plain-object values
// recurse; everything else with reference/identity semantics is rejected.
func (e *Emitter) emitDeepClone(val Value, ty Type, pos ast.Pos) (Value, error) {
	if kind := structuredCloneUnsupportedKind(ty); kind != "" {
		return Value{}, fmt.Errorf("%d:%d: structuredClone does not yet support %s values", pos.Line, pos.Col, kind)
	}
	switch {
	case ty.IsError:
		return e.emitDeepCloneError(val, ty)
	case ty.IsArrayBuffer && ty.IsSharedArrayBuffer:
		// SharedArrayBuffer: share-not-copy — the header pointer passes through.
		return val, nil
	case ty.IsArrayBuffer:
		return e.emitDeepCloneArrayBuffer(val)
	case ty.IsArray:
		return e.emitDeepCloneArray(val, ty, pos)
	case ty.IsMap:
		return e.emitDeepCloneMap(val, ty, pos)
	case ty.IsSet:
		return e.emitDeepCloneSet(val, ty, pos)
	case ty.IsObject:
		return e.emitDeepCloneObject(val, ty, pos)
	default:
		// A scalar (number/boolean/string/Date/null/undefined) — already a
		// value type, nothing to copy.
		return val, nil
	}
}

// emitDeepCloneError clones an Error (or built-in subtype) by copying its
// kind/message/name into a fresh object. The destination is allocated at the
// AggregateError size (40 B, zeroed) so a cloned AggregateError never reads its
// trailing errors fields out of bounds — those degrade to an empty `.errors`
// rather than crashing (the message/name/subtype identity, which real Node
// preserves, is copied exactly). message/name are immutable strings, so copying
// the pointers is a correct deep copy.
func (e *Emitter) emitDeepCloneError(val Value, ty Type) (Value, error) {
	e.ensureCalloc()
	dst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", dst, aggregateErrorStructSize))
	structIR := errorObjType.StructIR()
	for _, name := range []string{"kind", "message", "name"} {
		idx, fieldTy, _ := errorObjType.FieldIndex(name)
		sgep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", sgep, structIR, val.Ref, idx))
		fv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", fv, fieldTy.IR, sgep, fieldTy.Align()))
		dgep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dgep, structIR, dst, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, fv, dgep, fieldTy.Align()))
	}
	return Value{Ref: dst, Ty: ty}, nil
}

// emitDeepCloneArrayBuffer copies a plain ArrayBuffer: read its byteLength (word
// 0 of the { i64 byteLength, ptr data } header), allocate a fresh header + data
// buffer, and memcpy the bytes — the same copy `.slice()` performs.
func (e *Emitter) emitDeepCloneArrayBuffer(val Value) (Value, error) {
	lenVal, err := e.emitArrayBufferByteLength(val)
	if err != nil {
		return Value{}, err
	}
	e.ensureCalloc()
	e.ensureMalloc()
	e.ensureMemcpy()
	newData := e.freshReg()
	newHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 1)", newData, lenVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", newHdr))
	srcDataSlot := e.freshReg()
	srcData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", srcDataSlot, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", srcData, srcDataSlot))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newData, srcData, lenVal.Ref))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenVal.Ref, newHdr))
	newDataSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", newDataSlot, newHdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newData, newDataSlot))
	return Value{Ref: newHdr, Ty: ArrayBufferType()}, nil
}

// emitDeepCloneArray clones an array aggregate ({ptr,i64} Value, ty.IsArray)
// element-by-element into a freshly allocated backing buffer, recursing into
// each element via emitDeepClone. Reuses loadArrayElem/storeArrayElem so a
// nested-array element (TDD-00029's boxed representation) is transparently
// unboxed/reboxed exactly like every other array operation already does —
// this function needs no boxing awareness of its own for that to work.
func (e *Emitter) emitDeepCloneArray(val Value, ty Type, pos ast.Pos) (Value, error) {
	elemTy := *ty.ElemType

	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))

	e.ensureMalloc()
	byteCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteCount, lenReg, elemTy.Align()))
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, byteCount))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("clone.arr.cond")
	bodyL := e.freshLabel("clone.arr.body")
	doneL := e.freshLabel("clone.arr.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	srcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcGep, elemTy.IR, ptrReg, idxVal))
	elemVal := e.loadArrayElem(srcGep, elemTy)
	clonedElem, err := e.emitDeepClone(elemVal, elemTy, pos)
	if err != nil {
		return Value{}, err
	}
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstGep, elemTy.IR, dataReg, idxVal))
	e.storeArrayElem(dstGep, elemTy, clonedElem)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ty}, nil
}

// emitDeepCloneObject clones a plain heap-struct object (ty.IsObject) field
// by field, recursing into each field via emitDeepClone — the same
// GEP-by-index/StructFieldIR shape emitObjectLiteralWithHint's own spread
// (`{...obj}`) copy loop already uses, extended with a recursive clone step
// per field instead of a flat load-then-store.
func (e *Emitter) emitDeepCloneObject(val Value, ty Type, pos ast.Pos) (Value, error) {
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, ty.StructSize()))
	structIR := ty.StructIR()

	for _, f := range ty.VisibleFields() {
		idx, fieldTy, _ := ty.FieldIndex(f.Name)
		srcGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", srcGep, structIR, val.Ref, idx))
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(fieldTy), srcGep, fieldTy.Align()))

		cloned, err := e.emitDeepClone(Value{Ref: loadReg, Ty: fieldTy}, fieldTy, pos)
		if err != nil {
			return Value{}, err
		}

		dstGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dstGep, structIR, dataReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), cloned.Ref, dstGep, fieldTy.Align()))
	}
	return Value{Ref: dataReg, Ty: ty}, nil
}

// cloneableCollectionElem reports whether a Map/Set key or value element type
// can be deep-cloned by the collection cloners below (ADR-00574): scalars,
// strings, and plain objects. A nested array/Map/Set element is rejected —
// the runtime clone loop loads each element at its scalar/pointer slot width,
// which doesn't match an array's {ptr,i64} aggregate shape.
func cloneableCollectionElem(ty Type) bool {
	if ty.IsArray || ty.IsMap || ty.IsSet {
		return false
	}
	return structuredCloneUnsupportedKind(ty) == ""
}

// emitDeepCloneMap deep-copies a Map into a fresh one, cloning each value (and
// object keys) — real structuredClone semantics for a Map (ADR-00574).
func (e *Emitter) emitDeepCloneMap(val Value, ty Type, pos ast.Pos) (Value, error) {
	keyTy, valTy := *ty.MapKey, *ty.MapVal
	if !cloneableCollectionElem(keyTy) || !cloneableCollectionElem(valTy) {
		return Value{}, fmt.Errorf("%d:%d: structuredClone of a Map with an array/Map/Set key or value type is not supported", pos.Line, pos.Col)
	}
	strKey := isStringTy(keyTy)
	newMap := e.emitMapOrSetCreate(keyTy)
	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(val.Ref, strKey)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
	condL := e.freshLabel("clone.map.cond")
	bodyL := e.freshLabel("clone.map.body")
	doneL := e.freshLabel("clone.map.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, keysLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	kGep, kElem := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", kGep, keyTy.IR, keysPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", kElem, keyTy.IR, kGep, keyTy.Align()))
	vGep, vElem := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", vGep, valTy.IR, valsPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", vElem, valTy.IR, vGep, valTy.Align()))

	clonedKey, err := e.emitDeepClone(Value{Ref: kElem, Ty: keyTy}, keyTy, pos)
	if err != nil {
		return Value{}, err
	}
	clonedVal, err := e.emitDeepClone(Value{Ref: vElem, Ty: valTy}, valTy, pos)
	if err != nil {
		return Value{}, err
	}
	kRef := e.valueToMapKey(clonedKey, keyTy)
	vRef := e.valueToMapVal(clonedVal, valTy)
	if strKey {
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", newMap, kRef, vRef))
	} else {
		e.emitInstr(fmt.Sprintf("call void @__kml_map_num_set(ptr %s, i64 %s, i64 %s)", newMap, kRef, vRef))
	}
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return Value{Ref: newMap, Ty: ty}, nil
}

// emitDeepCloneSet deep-copies a Set into a fresh one, cloning each element
// (ADR-00574).
func (e *Emitter) emitDeepCloneSet(val Value, ty Type, pos ast.Pos) (Value, error) {
	elemTy := *ty.MapKey
	if !cloneableCollectionElem(elemTy) {
		return Value{}, fmt.Errorf("%d:%d: structuredClone of a Set with an array/Map/Set element type is not supported", pos.Line, pos.Col)
	}
	strElem := isStringTy(elemTy)
	newSet := e.emitMapOrSetCreate(elemTy)
	keysPtr, keysLen, _ := e.mapKeysAndVals(val.Ref, strElem)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))
	condL := e.freshLabel("clone.set.cond")
	bodyL := e.freshLabel("clone.set.body")
	doneL := e.freshLabel("clone.set.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, keysLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	eGep, eElem := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", eGep, elemTy.IR, keysPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", eElem, elemTy.IR, eGep, elemTy.Align()))
	clonedElem, err := e.emitDeepClone(Value{Ref: eElem, Ty: elemTy}, elemTy, pos)
	if err != nil {
		return Value{}, err
	}
	kRef := e.valueToMapKey(clonedElem, elemTy)
	if strElem {
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 0)", newSet, kRef))
	} else {
		e.emitInstr(fmt.Sprintf("call void @__kml_map_num_set(ptr %s, i64 %s, i64 0)", newSet, kRef))
	}
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return Value{Ref: newSet, Ty: ty}, nil
}

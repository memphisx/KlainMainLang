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
// arrays and TypedArrays, all IsArray under the hood) and plain objects
// are heap-allocated and genuinely copied, recursing into every element/
// field. Anything with reference-like or non-trivial-identity semantics
// (Map, Set, EventEmitter, URL, URLSearchParams, ArrayBuffer, functions,
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
		return "URL"
	case ty.IsURLSearchParams:
		return "URLSearchParams"
	case ty.IsHeaders:
		return "Headers"
	case ty.IsMap:
		return "Map"
	case ty.IsSet:
		return "Set"
	case ty.IsEventEmitter:
		return "EventEmitter"
	case ty.IsArrayBuffer:
		return "ArrayBuffer"
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
	case ty.IsError:
		return "an Error"
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
	case ty.IsArray:
		return e.emitDeepCloneArray(val, ty, pos)
	case ty.IsObject:
		return e.emitDeepCloneObject(val, ty, pos)
	default:
		// A scalar (number/boolean/string/Date/null/undefined) — already a
		// value type, nothing to copy.
		return val, nil
	}
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

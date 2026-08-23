package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emit_arraybuffer.go — ArrayBuffer construction/.byteLength, and
// TypedArray construction (3 forms) + .set()/.subarray()/.byteLength. See
// docs/tdd/TDD-00018.md for the full design; everything else a TypedArray
// supports (indexing, .length, .fill, .slice, .reverse, .at, .indexOf,
// .includes, .map/.filter/.reduce/.forEach/.some/.every, for-of,
// .keys()/.values()/.entries()) needs no code here at all — it already
// works via emit_arrays_*.go's existing elemTy-generic machinery.

// emitNewArrayBufferExpression implements `new ArrayBuffer(byteLength)`. A
// general expression (unlike TypedArrays, not restricted to a variable
// declaration) — the result is a ptr to a hidden 2-word heap struct
// { i64 byteLength, ptr data }, never exposed via the generic object-field
// path (see IsArrayBuffer's doc comment in types.go). The data buffer
// itself is calloc'd, matching real ArrayBuffer's zero-fill guarantee.
func (e *Emitter) emitNewArrayBufferExpression(ex *ast.NewArrayBufferExpression) (Value, error) {
	sizeVal, err := e.emitExpr(ex.ByteLength)
	if err != nil {
		return Value{}, err
	}
	sizeVal = e.coerce(sizeVal, TypeI64)

	// A SharedArrayBuffer under -mm=gc must survive the window where its
	// only live reference is on another thread (or inside a pipe envelope)
	// — invisible to this thread's Boehm scan — so header and data are
	// GC_malloc_uncollectable (zeroed, never collected, still scanned).
	// TDD-00099.
	dataReg := e.freshReg()
	hdrReg := e.freshReg()
	if ex.Shared && e.isGCMode() {
		e.ensureGCUncollectable()
		e.emitInstr(fmt.Sprintf("%s = call ptr @GC_malloc_uncollectable(i64 %s)", dataReg, sizeVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = call ptr @GC_malloc_uncollectable(i64 16)", hdrReg))
	} else {
		e.ensureCalloc()
		e.ensureMalloc()
		e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 1)", dataReg, sizeVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdrReg))
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sizeVal.Ref, hdrReg))
	dataSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, hdrReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, dataSlot))

	if ex.Shared {
		return Value{Ref: hdrReg, Ty: SharedArrayBufferType()}, nil
	}
	return Value{Ref: hdrReg, Ty: ArrayBufferType()}, nil
}

// emitArrayBufferByteLength reads word 0 of an ArrayBuffer's hidden header
// struct — bufVal.Ref is already the header pointer (loaded from the
// variable's own alloca/expression the same way any other ptr-shaped value
// is), so this is just a direct load, no GEP needed for offset 0.
func (e *Emitter) emitArrayBufferByteLength(bufVal Value) (Value, error) {
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, bufVal.Ref))
	return Value{Ref: result, Ty: TypeI64}, nil
}

// emitNewTypedArrayVarDecl implements `new Int8Array(...)`/.../
// `new Float64Array(...)` as a variable declaration's initializer (the only
// place these are allowed — see docs/tdd/TDD-00018.md). Dispatches on the
// argument's inferred type to pick one of three construction forms.
func (e *Emitter) emitNewTypedArrayVarDecl(nta *ast.NewTypedArrayExpression, ptrName, lenName string, elemTy Type) error {
	// An inline array literal (`new Uint8Array([1, 2, 3])`) can't go through
	// the generic emitExpr/resolveArrayForHOF path below at all — array
	// literals in this compiler can only ever be evaluated directly inside
	// a variable declaration's own initializer, not as a general
	// sub-expression (emitExpr rejects them outright everywhere else, the
	// same restriction that already applies to a bare `[1,2,3]` passed as a
	// function argument). Handled as its own case, evaluating each element
	// directly, rather than losing this common, natural construction form.
	if lit, ok := nta.Arg.(*ast.ArrayLiteral); ok {
		return e.emitTypedArrayFromArrayLiteral(lit, ptrName, lenName, elemTy)
	}
	argTy := e.inferExprType(nta.Arg)
	switch {
	case argTy.IsArrayBuffer:
		return e.emitTypedArrayFromBuffer(nta, ptrName, lenName, elemTy)
	case argTy.IsArray:
		return e.emitTypedArrayFromArrayLike(nta, ptrName, lenName, elemTy)
	default:
		return e.emitTypedArrayFromSize(nta, ptrName, lenName, elemTy)
	}
}

// emitTypedArrayFromArrayLiteral handles `new XArray([e1, e2, ...])` —
// evaluates each element expression directly and coerces it into elemTy,
// the same "malloc, then per-element GEP+store" shape emitArrayVarDecl's
// own array-literal branch already uses for a plain `number[]`.
func (e *Emitter) emitTypedArrayFromArrayLiteral(lit *ast.ArrayLiteral, ptrName, lenName string, elemTy Type) error {
	for _, elem := range lit.Elements {
		if _, ok := elem.(*ast.SpreadElement); ok {
			return fmt.Errorf("%d:%d: spread elements in a TypedArray literal are not yet supported", elem.GetPos().Line, elem.GetPos().Col)
		}
	}
	n := int64(len(lit.Elements))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(elemTy.Align())))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, ptrName))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", n, lenName))

	for i, elem := range lit.Elements {
		val, err := e.emitExpr(elem)
		if err != nil {
			return err
		}
		val = e.coerce(val, elemTy)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
	}
	return nil
}

// emitTypedArrayFromSize handles `new XArray(n)` — an implicit, own
// zero-initialized buffer. Identical to NewArrayExpression's own
// calloc-based construction (emit_arrays_core.go), just with elemTy fixed
// by the constructor name instead of a type annotation.
func (e *Emitter) emitTypedArrayFromSize(nta *ast.NewTypedArrayExpression, ptrName, lenName string, elemTy Type) error {
	sizeVal, err := e.emitExpr(nta.Arg)
	if err != nil {
		return err
	}
	sizeVal = e.coerce(sizeVal, TypeI64)
	e.ensureCalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 %d)", dataReg, sizeVal.Ref, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, ptrName))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sizeVal.Ref, lenName))
	return nil
}

// emitTypedArrayFromBuffer handles `new XArray(buffer)` — a view sharing
// the buffer's own memory, no allocation. Throws a catchable Error if the
// buffer's byteLength isn't evenly divisible by the element size (matching
// the same "surface a bad combination as a catchable Error" convention
// Invalid-URL/array-out-of-bounds already use), rather than silently
// truncating or reading past the buffer.
func (e *Emitter) emitTypedArrayFromBuffer(nta *ast.NewTypedArrayExpression, ptrName, lenName string, elemTy Type) error {
	bufVal, err := e.emitExpr(nta.Arg)
	if err != nil {
		return err
	}

	byteLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", byteLenReg, bufVal.Ref))
	dataSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, bufVal.Ref))
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dataSlot))

	remReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = srem i64 %s, %d", remReg, byteLenReg, elemTy.Align()))
	badReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", badReg, remReg))
	badL := e.freshLabel("typedarray.badlen")
	okL := e.freshLabel("typedarray.oklen")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badReg, badL, okL))

	e.emitLabel(badL)
	e.emitInternalThrow(e.internString("ArrayBuffer length is not a multiple of the element size"))

	e.emitLabel(okL)
	elemCountReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, %d", elemCountReg, byteLenReg, elemTy.Align()))

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, ptrName))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", elemCountReg, lenName))
	return nil
}

// emitTypedArrayFromArrayLike handles `new XArray(numberArrayOrTypedArray)`
// — copy-constructs a fresh buffer, coercing each source element via the
// same e.coerce truncation/wraparound path plain assignment already uses
// (so e.g. new Uint8Array([-1, 300]) correctly becomes [255, 44] with no
// clamping-specific code).
func (e *Emitter) emitTypedArrayFromArrayLike(nta *ast.NewTypedArrayExpression, ptrName, lenName string, elemTy Type) error {
	srcPtrReg, srcLenReg, srcElemTy, err := e.resolveArrayForHOF(nta.Arg, nta.GetPos())
	if err != nil {
		return err
	}

	e.ensureCalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 %d)", dataReg, srcLenReg, elemTy.Align()))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("typedarray.copy.cond")
	bodyL := e.freshLabel("typedarray.copy.body")
	doneL := e.freshLabel("typedarray.copy.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, srcLenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	srcGep := e.freshReg()
	srcElemReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcGep, srcElemTy.IR, srcPtrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", srcElemReg, srcElemTy.IR, srcGep, srcElemTy.Align()))
	coerced := e.coerce(Value{Ref: srcElemReg, Ty: srcElemTy}, elemTy)
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstGep, elemTy.IR, dataReg, idxVal))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, coerced.Ref, dstGep, elemTy.Align()))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, ptrName))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", srcLenReg, lenName))
	return nil
}

// emitTypedArrayByteLength implements a TypedArray's .byteLength: element
// count * elemTy.Align(), alongside the array machinery's own .length.
func (e *Emitter) emitTypedArrayByteLength(lenReg string, elemTy Type) (Value, error) {
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", result, lenReg, elemTy.Align()))
	return Value{Ref: result, Ty: TypeI64}, nil
}

// emitTypedArraySet implements typedArray.set(source, offset?): copies
// source's elements into this TypedArray starting at offset (default 0),
// coercing each one the same way construction does, and throws a catchable
// Error if source doesn't fit starting at that offset.
func (e *Emitter) emitTypedArraySet(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: set takes 1 or 2 arguments (source, offset?)", pos.Line, pos.Col)
	}
	dstPtrReg, dstLenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	srcPtrReg, srcLenReg, srcElemTy, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}

	offsetReg := "0"
	if len(args) == 2 {
		offRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		offsetReg = e.coerce(offRaw, TypeI64).Ref
	}

	endReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", endReg, offsetReg, srcLenReg))
	tooBig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooBig, endReg, dstLenReg))
	badL := e.freshLabel("typedarray.set.bad")
	okL := e.freshLabel("typedarray.set.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", tooBig, badL, okL))

	e.emitLabel(badL)
	e.emitInternalThrow(e.internString("source is too large for set()'s target, starting at the given offset"))

	e.emitLabel(okL)
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("typedarray.set.cond")
	bodyL := e.freshLabel("typedarray.set.body")
	doneL := e.freshLabel("typedarray.set.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, srcLenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	srcGep := e.freshReg()
	srcElemReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcGep, srcElemTy.IR, srcPtrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", srcElemReg, srcElemTy.IR, srcGep, srcElemTy.Align()))
	coerced := e.coerce(Value{Ref: srcElemReg, Ty: srcElemTy}, elemTy)
	dstIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", dstIdx, offsetReg, idxVal))
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstGep, elemTy.IR, dstPtrReg, dstIdx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, coerced.Ref, dstGep, elemTy.Align()))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// emitTypedArraySubarray implements typedArray.subarray(start?, end?): a
// VIEW into the same underlying memory (no allocation, no copy) — unlike
// .slice(), which emit_arrays_sort.go's emitArraySlice already gives every
// TypedArray for free as a real copy. Reuses emitNormalizeSliceIdx for the
// same negative-index/clamping rules .slice() already applies.
func (e *Emitter) emitTypedArraySubarray(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: subarray takes 0, 1, or 2 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	startN := "0"
	if len(args) >= 1 {
		startRaw, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(e.coerce(startRaw, TypeI64).Ref, lenReg)
	}
	endN := lenReg
	if len(args) == 2 {
		endRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.coerce(endRaw, TypeI64).Ref, lenReg)
	}

	rawLen := e.freshReg()
	isNegLen := e.freshReg()
	viewLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawLen, endN, startN))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNegLen, rawLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", viewLen, isNegLen, rawLen))

	viewPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", viewPtr, elemTy.IR, ptrReg, startN))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, viewPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, viewLen))

	ty := ArrayOf(elemTy)
	ty.IsTypedArray = true
	return Value{Ref: r1, Ty: ty}, nil
}

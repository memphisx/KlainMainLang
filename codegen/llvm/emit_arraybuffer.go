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
	// A growable buffer (`{maxByteLength}`, ADR-00494) reserves its maximum
	// upfront — views hold raw data pointers, so the data block can never
	// move; grow() only bumps the length word. Header word 2 holds the max,
	// or -1 for a fixed-size buffer (feeds `.growable`/`.maxByteLength`).
	allocRef := sizeVal.Ref
	maxRef := "-1"
	if ex.MaxByteLength != nil {
		maxVal, err := e.emitExpr(ex.MaxByteLength)
		if err != nil {
			return Value{}, err
		}
		maxVal = e.coerce(maxVal, TypeI64)
		maxRef = maxVal.Ref
		allocRef = maxVal.Ref
	}
	dataReg := e.freshReg()
	hdrReg := e.freshReg()
	hdrBytes := 16
	if ex.MaxByteLength != nil {
		hdrBytes = 24
	}
	if ex.Shared && e.isGCMode() {
		e.ensureGCUncollectable()
		e.emitInstr(fmt.Sprintf("%s = call ptr @GC_malloc_uncollectable(i64 %s)", dataReg, allocRef))
		e.emitInstr(fmt.Sprintf("%s = call ptr @GC_malloc_uncollectable(i64 %d)", hdrReg, hdrBytes))
	} else {
		e.ensureCalloc()
		e.ensureMalloc()
		e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 1)", dataReg, allocRef))
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", hdrReg, hdrBytes))
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sizeVal.Ref, hdrReg))
	dataSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, i64 }, ptr %s, i32 0, i32 1", dataSlot, hdrReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, dataSlot))
	if ex.MaxByteLength != nil {
		maxSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, i64 }, ptr %s, i32 0, i32 2", maxSlot, hdrReg))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", maxRef, maxSlot))
	}

	if ex.Shared {
		ty := SharedArrayBufferType()
		ty.BufferGrowable = ex.MaxByteLength != nil
		return Value{Ref: hdrReg, Ty: ty}, nil
	}
	ty := ArrayBufferType()
	ty.BufferGrowable = ex.MaxByteLength != nil
	return Value{Ref: hdrReg, Ty: ty}, nil
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

// ensureRoundEven declares llvm.roundeven.f64 once — round-half-to-even,
// the rounding mode ToUint8Clamp requires (llvm.round would give
// round-half-away-from-zero).
func (e *Emitter) ensureRoundEven() {
	if e.usedRoundEven {
		return
	}
	e.usedRoundEven = true
	e.emitGlobal("declare double @llvm.roundeven.f64(double)")
}

// coerceTypedArrayStore converts a language-level value into a TypedArray's
// raw stored scalar — the store half of TDD-00101's conversion layer. For a
// BigIntElem array the value must already be a bigint (compile-time error,
// the spec's TypeError) and is unwrapped through the bigint ABI; for a
// Clamped array the spec's ToUint8Clamp applies (clamp [0,255]; floats
// NaN→0 and round-half-to-even); every other kind is the plain e.coerce
// wrap/trunc path unchanged.
func (e *Emitter) coerceTypedArrayStore(v Value, taTy Type, pos ast.Pos) (Value, error) {
	elemTy := *taTy.ElemType
	switch {
	case taTy.BigIntElem:
		if !v.Ty.IsBigInt {
			return Value{}, fmt.Errorf("%d:%d: a BigInt64Array/BigUint64Array element must be a bigint (e.g. 1n), not a number", pos.Line, pos.Col)
		}
		e.ensureBigInt()
		unwrap := "@__kml_bigint_to_i64"
		if !elemTy.Signed {
			unwrap = "@__kml_bigint_to_u64"
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s)", r, unwrap, v.Ref))
		return Value{Ref: r, Ty: elemTy}, nil
	case taTy.Clamped:
		if v.Ty.Float {
			f := e.coerce(v, TypeF64)
			// ToUint8Clamp: NaN → 0 (the fcmp ult 0.0 check is
			// unordered-true, so NaN takes the 0 branch of the low clamp),
			// clamp to [0, 255], round half to even, then convert.
			e.ensureRoundEven()
			lo := e.freshReg()
			clampedLo := e.freshReg()
			hi := e.freshReg()
			clamped := e.freshReg()
			rounded := e.freshReg()
			asInt := e.freshReg()
			narrow := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fcmp ult double %s, 0.0", lo, f.Ref))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0.0, double %s", clampedLo, lo, f.Ref))
			e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, 255.0", hi, clampedLo))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 255.0, double %s", clamped, hi, clampedLo))
			e.emitInstr(fmt.Sprintf("%s = call double @llvm.roundeven.f64(double %s)", rounded, clamped))
			e.emitInstr(fmt.Sprintf("%s = fptoui double %s to i64", asInt, rounded))
			e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i8", narrow, asInt))
			return Value{Ref: narrow, Ty: elemTy}, nil
		}
		i := e.coerce(v, TypeI64)
		neg := e.freshReg()
		clampedLo := e.freshReg()
		big := e.freshReg()
		clamped := e.freshReg()
		narrow := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, i.Ref))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", clampedLo, neg, i.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 255", big, clampedLo))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 255, i64 %s", clamped, big, clampedLo))
		e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i8", narrow, clamped))
		return Value{Ref: narrow, Ty: elemTy}, nil
	default:
		return e.coerce(v, elemTy), nil
	}
}

// wrapTypedArrayLoad converts a raw stored scalar back into the
// language-level element — the load half of TDD-00101's conversion layer.
// Only BigIntElem arrays differ from identity: the i64/u64 becomes a
// heap-allocated bigint handle.
func (e *Emitter) wrapTypedArrayLoad(raw Value, taTy Type) Value {
	if !taTy.BigIntElem {
		return raw
	}
	e.ensureBigInt()
	wrap := "@__kml_bigint_from_i64"
	if !taTy.ElemType.Signed {
		wrap = "@__kml_bigint_from_u64"
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr %s(i64 %s)", r, wrap, raw.Ref))
	return Value{Ref: r, Ty: BigIntType()}
}

// bigIntElemRejectedMethods lists the array methods a BigInt64Array/
// BigUint64Array does NOT support — a compile-time rejection (see
// TDD-00101's method policy: the HOF/search/sort/mutator machinery passes
// raw i64 scalars into callbacks and comparisons, so an unguarded method
// would silently surface a raw scalar as if it were a bigint). Supported:
// indexing r/w, .length/.byteLength, .at/.set/.subarray/.slice/.fill/
// .reverse, for-of, Atomics.*.
var bigIntElemRejectedMethods = map[string]bool{
	"map": true, "filter": true, "reduce": true, "reduceRight": true,
	"forEach": true, "some": true, "every": true, "find": true,
	"findIndex": true, "findLast": true, "findLastIndex": true,
	"indexOf": true, "lastIndexOf": true, "includes": true, "sort": true,
	"toSorted": true, "join": true, "keys": true, "values": true,
	"entries": true, "push": true, "pop": true, "shift": true,
	"unshift": true, "splice": true, "toSpliced": true, "concat": true,
	"flat": true, "flatMap": true, "with": true, "copyWithin": true,
	"toReversed": true,
}

// emitArrayBufferSlice implements arrayBuffer.slice(start?, end?) /
// sharedArrayBuffer.slice(start?, end?) — a copy of the byte sub-range into
// a brand-new buffer of the receiver's own kind (spec: SharedArrayBuffer's
// slice returns a new SharedArrayBuffer), with the same negative-index/
// clamping rules array .slice() uses via emitNormalizeSliceIdx.
func (e *Emitter) emitArrayBufferSlice(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: slice takes 0, 1, or 2 arguments", pos.Line, pos.Col)
	}
	bufVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	shared := bufVal.Ty.IsSharedArrayBuffer

	byteLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", byteLenReg, bufVal.Ref))
	dataSlot := e.freshReg()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, bufVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dataSlot))

	startN := "0"
	if len(args) >= 1 {
		startRaw, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(e.coerce(startRaw, TypeI64).Ref, byteLenReg)
	}
	endN := byteLenReg
	if len(args) == 2 {
		endRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.coerce(endRaw, TypeI64).Ref, byteLenReg)
	}

	rawLen := e.freshReg()
	isNegLen := e.freshReg()
	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawLen, endN, startN))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNegLen, rawLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", newLen, isNegLen, rawLen))

	newData := e.freshReg()
	newHdr := e.freshReg()
	if shared && e.isGCMode() {
		e.ensureGCUncollectable()
		e.emitInstr(fmt.Sprintf("%s = call ptr @GC_malloc_uncollectable(i64 %s)", newData, newLen))
		e.emitInstr(fmt.Sprintf("%s = call ptr @GC_malloc_uncollectable(i64 16)", newHdr))
	} else {
		e.ensureCalloc()
		e.ensureMalloc()
		e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 1)", newData, newLen))
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", newHdr))
	}
	srcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", srcGep, dataReg, startN))
	e.ensureMemcpy()
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newData, srcGep, newLen))

	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, newHdr))
	newDataSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", newDataSlot, newHdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newData, newDataSlot))

	if shared {
		return Value{Ref: newHdr, Ty: SharedArrayBufferType()}, nil
	}
	return Value{Ref: newHdr, Ty: ArrayBufferType()}, nil
}

// emitNewTypedArrayAggregate builds a `new XArray(...)` as a general
// expression, returning the `{ptr, i64}` array aggregate every array consumer
// already understands — so a TypedArray construction can appear as a function
// argument, a return value, an object field, a ternary arm, etc., not only a
// variable declaration's initializer (TDD-00018 Stage 5 / ADR-00512). It reuses
// the four var-decl construction forms verbatim by giving them two temporary
// entry-block allocas, then loading those into the aggregate — a TypedArray IS
// an array (IsTypedArray sets IsArray/ElemType), so the value form is identical
// to a plain array's.
func (e *Emitter) emitNewTypedArrayAggregate(nta *ast.NewTypedArrayExpression) (Value, error) {
	taTy := TypedArrayType(nta.ElemKind)
	elemTy := *taTy.ElemType
	ptrA := e.freshReg()
	lenA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrA))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenA))
	if err := e.emitNewTypedArrayVarDecl(nta, ptrA, lenA, elemTy); err != nil {
		return Value{}, err
	}
	ptrReg := e.freshReg()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, ptrA))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenA))
	a0 := e.freshReg()
	agg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", a0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", agg, a0, lenReg))
	return Value{Ref: agg, Ty: taTy}, nil
}

// emitNewTypedArrayVarDecl implements `new Int8Array(...)`/.../
// `new Float64Array(...)` as a variable declaration's initializer (see
// docs/tdd/TDD-00018.md). Dispatches on the argument's inferred type to pick
// one of three construction forms. General-expression use goes through
// emitNewTypedArrayAggregate above, which reuses these same forms.
func (e *Emitter) emitNewTypedArrayVarDecl(nta *ast.NewTypedArrayExpression, ptrName, lenName string, elemTy Type) error {
	// An inline array literal (`new Uint8Array([1, 2, 3])`) can't go through
	// the generic emitExpr/resolveArrayForHOF path below at all — array
	// literals in this compiler can only ever be evaluated directly inside
	// a variable declaration's own initializer, not as a general
	// sub-expression (emitExpr rejects them outright everywhere else, the
	// same restriction that already applies to a bare `[1,2,3]` passed as a
	// function argument). Handled as its own case, evaluating each element
	// directly, rather than losing this common, natural construction form.
	taTy := TypedArrayType(nta.ElemKind)
	if lit, ok := nta.Arg.(*ast.ArrayLiteral); ok {
		if nta.ByteOffset != nil {
			return fmt.Errorf("%d:%d: the (buffer, byteOffset, length?) constructor form requires an ArrayBuffer first argument", nta.GetPos().Line, nta.GetPos().Col)
		}
		return e.emitTypedArrayFromArrayLiteral(lit, ptrName, lenName, taTy)
	}
	argTy := e.inferExprType(nta.Arg)
	if nta.ByteOffset != nil && !argTy.IsArrayBuffer {
		return fmt.Errorf("%d:%d: the (buffer, byteOffset, length?) constructor form requires an ArrayBuffer first argument", nta.GetPos().Line, nta.GetPos().Col)
	}
	switch {
	case argTy.IsArrayBuffer:
		return e.emitTypedArrayFromBuffer(nta, ptrName, lenName, elemTy)
	case argTy.IsArray:
		return e.emitTypedArrayFromArrayLike(nta, ptrName, lenName, taTy)
	default:
		return e.emitTypedArrayFromSize(nta, ptrName, lenName, elemTy)
	}
}

// emitTypedArrayFromArrayLiteral handles `new XArray([e1, e2, ...])` —
// evaluates each element expression directly and coerces it into elemTy,
// the same "malloc, then per-element GEP+store" shape emitArrayVarDecl's
// own array-literal branch already uses for a plain `number[]`.
func (e *Emitter) emitTypedArrayFromArrayLiteral(lit *ast.ArrayLiteral, ptrName, lenName string, taTy Type) error {
	elemTy := *taTy.ElemType
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
		val, err = e.coerceTypedArrayStore(val, taTy, elem.GetPos())
		if err != nil {
			return err
		}
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

// emitTypedArrayFromBuffer handles `new XArray(buffer)` and the sub-range
// form `new XArray(buffer, byteOffset, length?)` — a view sharing the
// buffer's own memory, no allocation. Throws a catchable RangeError for a
// negative/misaligned/out-of-bounds offset or an over-long explicit length,
// and (whole-buffer or no-explicit-length forms) if the viewed byte span
// isn't evenly divisible by the element size — rather than silently
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

	elemSize := int64(elemTy.Align())

	offRef := "0"
	if nta.ByteOffset != nil {
		offVal, err := e.emitExpr(nta.ByteOffset)
		if err != nil {
			return err
		}
		offRef = e.coerce(offVal, TypeI64).Ref

		// Spec RangeErrors for the view's start: negative or misaligned
		// offsets are rejected (unaligned element access is never allowed —
		// same line real JS draws), and the offset must lie inside the
		// buffer.
		offNeg := e.freshReg()
		offRem := e.freshReg()
		offMis := e.freshReg()
		offBig := e.freshReg()
		b1 := e.freshReg()
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", offNeg, offRef))
		e.emitInstr(fmt.Sprintf("%s = srem i64 %s, %d", offRem, offRef, elemSize))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", offMis, offRem))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", offBig, offRef, byteLenReg))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", b1, offNeg, offMis))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, b1, offBig))
		badOffL := e.freshLabel("typedarray.badoff")
		okOffL := e.freshLabel("typedarray.okoff")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badOffL, okOffL))
		e.emitLabel(badOffL)
		e.emitInternalThrow(e.internString("RangeError: start offset is outside the bounds of the buffer or not a multiple of the element size"))
		e.emitLabel(okOffL)
	}

	var elemCountReg string
	if nta.Length != nil {
		lenVal, err := e.emitExpr(nta.Length)
		if err != nil {
			return err
		}
		lenRef := e.coerce(lenVal, TypeI64).Ref
		// offset + length*elemSize must fit inside the buffer.
		lenBytes := e.freshReg()
		endReg := e.freshReg()
		lenNeg := e.freshReg()
		tooBig := e.freshReg()
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", lenBytes, lenRef, elemSize))
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", endReg, offRef, lenBytes))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", lenNeg, lenRef))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooBig, endReg, byteLenReg))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, lenNeg, tooBig))
		badLenL := e.freshLabel("typedarray.badviewlen")
		okLenL := e.freshLabel("typedarray.okviewlen")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badLenL, okLenL))
		e.emitLabel(badLenL)
		e.emitInternalThrow(e.internString("RangeError: length is outside the bounds of the buffer, starting at the given offset"))
		e.emitLabel(okLenL)
		elemCountReg = lenRef
	} else {
		remBytes := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", remBytes, byteLenReg, offRef))
		remReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = srem i64 %s, %d", remReg, remBytes, elemSize))
		badReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", badReg, remReg))
		badL := e.freshLabel("typedarray.badlen")
		okL := e.freshLabel("typedarray.oklen")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badReg, badL, okL))

		e.emitLabel(badL)
		e.emitInternalThrow(e.internString("ArrayBuffer length is not a multiple of the element size"))

		e.emitLabel(okL)
		cnt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, %d", cnt, remBytes, elemSize))
		elemCountReg = cnt
	}

	viewData := dataReg
	if nta.ByteOffset != nil {
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", v, dataReg, offRef))
		viewData = v
	}

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", viewData, ptrName))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", elemCountReg, lenName))
	return nil
}

// emitTypedArrayFromArrayLike handles `new XArray(numberArrayOrTypedArray)`
// — copy-constructs a fresh buffer, coercing each source element via the
// same e.coerce truncation/wraparound path plain assignment already uses
// (so e.g. new Uint8Array([-1, 300]) correctly becomes [255, 44] with no
// clamping-specific code).
func (e *Emitter) emitTypedArrayFromArrayLike(nta *ast.NewTypedArrayExpression, ptrName, lenName string, taTy Type) error {
	elemTy := *taTy.ElemType
	srcTy := e.inferExprType(nta.Arg)
	// TDD-00101: a bigint-element TypedArray copy-constructs only from
	// another bigint-element TypedArray (raw i64 copy) — mixing with number
	// arrays is the spec's TypeError. And copy-constructing a plain
	// TypedArray FROM a bigint-element one would surface raw scalars.
	if taTy.BigIntElem != srcTy.BigIntElem {
		if taTy.BigIntElem {
			return fmt.Errorf("%d:%d: a BigInt64Array/BigUint64Array can only copy-construct from another BigInt64Array/BigUint64Array (or a bigint literal list)", nta.GetPos().Line, nta.GetPos().Col)
		}
		return fmt.Errorf("%d:%d: a number-element TypedArray cannot copy-construct from a BigInt64Array/BigUint64Array", nta.GetPos().Line, nta.GetPos().Col)
	}
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
	var coerced Value
	if taTy.Clamped {
		coerced, err = e.coerceTypedArrayStore(Value{Ref: srcElemReg, Ty: srcElemTy}, taTy, nta.GetPos())
		if err != nil {
			return err
		}
	} else {
		coerced = e.coerce(Value{Ref: srcElemReg, Ty: srcElemTy}, elemTy)
	}
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
	dstTy := e.inferExprType(mem.Object)
	if dstTy.BigIntElem != e.inferExprType(args[0]).BigIntElem {
		return Value{}, fmt.Errorf("%d:%d: set()'s source and target must both (or neither) be BigInt64Array/BigUint64Array", pos.Line, pos.Col)
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
	var coerced Value
	if dstTy.Clamped {
		coerced, err = e.coerceTypedArrayStore(Value{Ref: srcElemReg, Ty: srcElemTy}, dstTy, pos)
		if err != nil {
			return Value{}, err
		}
	} else {
		coerced = e.coerce(Value{Ref: srcElemReg, Ty: srcElemTy}, elemTy)
	}
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
	// Preserve the receiver's element semantics (TDD-00101) — a
	// BigInt64Array's subarray is still a BigInt64Array.
	recvTy := e.inferExprType(mem.Object)
	ty.BigIntElem = recvTy.BigIntElem
	ty.Clamped = recvTy.Clamped
	return Value{Ref: r1, Ty: ty}, nil
}

// emitBufferGrowableProps reads `.growable`/`.maxByteLength` off a buffer's
// header (ADR-00494). Word 2 is -1 for fixed-size buffers: growable=false,
// and maxByteLength falls back to the current byteLength (the spec's value
// for a non-growable buffer).
func (e *Emitter) emitBufferGrowableProps(bufVal Value, prop string) (Value, error) {
	if !bufVal.Ty.BufferGrowable {
		// Fixed-size buffer (16-byte header — word 2 must not be read):
		// growable/resizable is false; maxByteLength is the byteLength.
		if prop == "growable" || prop == "resizable" {
			return Value{Ref: "false", Ty: TypeBool}, nil
		}
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, bufVal.Ref))
		return Value{Ref: lenReg, Ty: TypeI64}, nil
	}
	maxSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, i64 }, ptr %s, i32 0, i32 2", maxSlot, bufVal.Ref))
	maxReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", maxReg, maxSlot))
	isG := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", isG, maxReg))
	if prop == "growable" || prop == "resizable" {
		return Value{Ref: isG, Ty: TypeBool}, nil
	}
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, bufVal.Ref))
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", res, isG, maxReg, lenReg))
	return Value{Ref: res, Ty: TypeI64}, nil
}

// emitBufferGrow implements sab.grow(newLength) (ADR-00494): bounds-check
// against the reserved max (and against shrinking — growable buffers only
// grow), then bump the header's length word. The data block was reserved at
// max size up front, so no view is ever invalidated.
func (e *Emitter) emitBufferGrow(bufVal Value, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: grow() requires 1 argument (newByteLength)", pos.Line, pos.Col)
	}
	if !bufVal.Ty.BufferGrowable {
		return Value{}, fmt.Errorf("%d:%d: grow()/resize() requires a buffer constructed with {maxByteLength}", pos.Line, pos.Col)
	}
	nVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	nVal = e.coerce(nVal, TypeI64)
	maxSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr, i64 }, ptr %s, i32 0, i32 2", maxSlot, bufVal.Ref))
	maxReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", maxReg, maxSlot))
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, bufVal.Ref))
	tooBig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooBig, nVal.Ref, maxReg))
	shrinks := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", shrinks, nVal.Ref, lenReg))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, tooBig, shrinks))
	throwL := e.freshLabel("sab.grow.throw")
	okL := e.freshLabel("sab.grow.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, throwL, okL))
	e.emitLabel(throwL)
	e.emitInternalThrow(e.internString("RangeError: SharedArrayBuffer.grow: new length is below the current length or above maxByteLength"))
	e.emitLabel(okL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nVal.Ref, bufVal.Ref))
	return Value{Ty: TypeVoid}, nil
}

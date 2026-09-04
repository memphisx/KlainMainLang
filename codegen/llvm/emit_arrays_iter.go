package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitArrayKeys(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: keys takes no arguments", pos.Line, pos.Col)
	}
	_, lenReg, _, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureMalloc()
	byteCount := e.freshReg()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", byteCount, lenReg))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newPtr, byteCount))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("keys.cond")
	bodyL := e.freshLabel("keys.body")
	doneL := e.freshLabel("keys.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", slot, newPtr, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxVal, slot))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(TypeI64)}, nil
}

// emitArrayValues implements arr.values(): returns a fresh copy of arr's
// elements — same materialized-array convention as emitArrayKeys.
func (e *Emitter) emitArrayValues(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: values takes no arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	newPtr := e.emitArrayCopy(ptrReg, lenReg, elemTy)
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayEntries implements arr.entries() → {index: number, value: T}[] —
// this compiler has no tuple type, so a real JS [index, value] pair isn't
// representable; the same heap-allocated-entry-object convention
// Map.entries()/Object.entries() already use. Iterate with
// `for (const e of arr.entries())` then read `e.index`/`e.value`.
func (e *Emitter) emitArrayEntries(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: entries takes no arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	// Each entry is a real [number, T] tuple (TDD-00066) — field 0 the index,
	// field 1 the value — positionally destructurable as
	// `for (const [i, v] of arr.entries())`.
	entryTy := TupleType([]Type{TypeI64, elemTy})
	entrySize := entryTy.StructSize()

	e.ensureMalloc()
	outBytes := e.freshReg()
	outPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", outBytes, lenReg))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", outPtr, outBytes))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("entries.cond")
	bodyL := e.freshLabel("entries.body")
	doneL := e.freshLabel("entries.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	elemGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", elemGep, elemTy.IR, ptrReg, idxVal))
	elemVal := e.loadArrayElem(elemGep, elemTy)

	entryReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", entryReg, entrySize))
	idxSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", idxSlot, entryTy.StructIR(), entryReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxVal, idxSlot))
	valSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", valSlot, entryTy.StructIR(), entryReg))
	// The entry object's "value" field is a struct field, not an array
	// backing-buffer slot — an array-typed field already uses the {ptr,i64}
	// aggregate convention (StructFieldIR, ADR-00061), which is exactly what
	// loadArrayElem's unboxed elemVal already is, so no further boxing is
	// needed here.
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(elemTy), elemVal.Ref, valSlot, elemTy.Align()))

	slotReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slotReg, outPtr, idxVal))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", entryReg, slotReg))

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	r0, r1 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, outPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(entryTy)}, nil
}

// emitArrayOf implements Array.of(...items): builds a fresh array directly
// from the given argument expressions — unlike an array literal `[...]`
// (which currently can only appear in variable-declaration position),
// Array.of is a plain call expression usable anywhere. Element type is
// inferred from the first argument, mirroring inferArrayType's own
// first-element rule for `[...]` literals; an empty call defaults to
// number[], same as an empty literal.
func (e *Emitter) emitArrayOf(args []ast.Expression, pos ast.Pos) (Value, error) {
	elemTy := TypeI64
	if len(args) > 0 {
		elemTy = e.inferExprType(args[0])
	}
	n := int64(len(args))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(elemTy.Align())))
	for i, argExpr := range args {
		val, err := e.emitExpr(argExpr)
		if err != nil {
			return Value{}, err
		}
		val = e.coerce(val, elemTy)
		// Array.of(...) builds a homogeneous array, same as an `[...]` literal:
		// an argument whose type coerce couldn't convert to the element type
		// (`Array.of(undefined, false, null)` — a mixed set that would need
		// `any[]`) is rejected cleanly, mirroring emitArrayLiteralData's own
		// guard, rather than emitting an invalid `store <elemTy> <mismatchedRef>`.
		if val.Ty.IR != elemTy.IR && !elemTy.IsArray && !elemTy.IsDynamic && !isNullableScalar(elemTy) {
			return Value{}, fmt.Errorf("%d:%d: Array.of(...) elements must share one type — argument %d does not match the element type inferred from the first argument (a heterogeneous array is not supported)", argExpr.GetPos().Line, argExpr.GetPos().Col, i)
		}
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", slot, elemTy.IR, dataReg, i))
		e.storeArrayElem(slot, elemTy, val)
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %d, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitStringToCharArray materializes a string Value into a `string[]` whose
// elements are its characters — one per byte, this compiler's string model
// (ADR-00482). Shared by Array.from(string) and `for (const ch of str)`
// (ADR-00535). Each character is a length-prefixed 1-byte string via
// emitStringExtract.
func (e *Emitter) emitStringToCharArray(v Value) Value {
	e.ensureStrlen()
	e.ensureMalloc()
	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, v.Ref))
	bytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, sLen))
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, bytes))
	idxPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))
	condL := e.freshLabel("strfrom.cond")
	bodyL := e.freshLabel("strfrom.body")
	endL := e.freshLabel("strfrom.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i1r, c := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i1r, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", c, i1r, sLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", c, bodyL, endL))
	e.emitLabel(bodyL)
	i2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i2, idxPtr))
	ch := e.emitStringExtract(v.Ref, i2, "1")
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", slot, buf, i2))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ch.Ref, slot))
	i3 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i3, i2))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i3, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	r0, r1 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, buf))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, sLen))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}
}

// emitArrayFrom implements Array.from(iterable): the array-like overload
// only (a plain array, or a class instance implementing the Stage 1a
// iterator protocol — next(): T | null, TDD-00009/ADR-00063) — real JS's
// second mapFn/thisArg arguments and iterating a string/Map/Set are not
// built here, see docs/status/ARRAY-METHODS.md.
//
// A plain array is a straight copy into a freshly malloc'd buffer of the
// same, already-known length (mirroring `.slice()`'s own no-arg shape). A
// class iterator has no length known upfront, so it's drained by repeated
// next() calls exactly like emitForOfClassIterator's loop shape, growing the
// result via the same realloc-per-element append emitPush already uses —
// realloc(NULL, n) is a valid, ordinary malloc(n) on the first element, so
// the initial ptr can start as a bare null with no special-cased first step.
func (e *Emitter) emitArrayFrom(args []ast.Expression, pos ast.Pos) (Value, error) {
	// Array.from(iterable, mapFn) desugars to Array.from(iterable).map(mapFn)
	// (ADR-00491) — semantically exact here, since the one observable
	// difference in real JS (mapFn seeing the source's holes/live length)
	// can't arise for this compiler's dense, materialized arrays. thisArg
	// stays unsupported.
	if len(args) == 2 {
		fromCall := ast.NewCallExpression(
			ast.NewMemberExpression(ast.NewIdentifier("Array", pos), "from", pos),
			args[:1], pos)
		mapCall := ast.NewCallExpression(
			ast.NewMemberExpression(fromCall, "map", pos),
			args[1:2], pos)
		return e.emitExpr(mapCall)
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Array.from takes 1 argument (iterable) or 2 (iterable, mapFn)", pos.Line, pos.Col)
	}
	srcTy := e.inferExprType(args[0])

	if srcTy.IsArray {
		ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		e.ensureMalloc()
		e.ensureMemcpy()
		bytesReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", bytesReg, lenReg, elemTy.Align()))
		newPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newPtr, bytesReg))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, ptrReg, bytesReg))
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
		return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
	}

	// Map → entries ([K, V][] tuple array), Set → elements, string → its
	// characters (per byte, this compiler's string model) — ADR-00482.
	if srcTy.IsMap && !srcTy.IsSet {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitMapCall(srcTy, v.Ref, "entries", nil, pos)
	}
	if srcTy.IsSet {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.mapOrSetValuesArray(srcTy, v.Ref)
	}
	if isStringTy(srcTy) && !srcTy.IsClass && !srcTy.IsObject {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		return e.emitStringToCharArray(v), nil
	}

	if srcTy.IsClass {
		info, ok := e.classes[srcTy.ClassName]
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: Array.from argument is not iterable", pos.Line, pos.Col)
		}
		sig, ok := info.MethodSigs["next"]
		if !ok || len(sig.ParamTypes) != 0 || !sig.RetType.Nullable ||
			sig.RetType.IsArray || sig.RetType.IsMap || sig.RetType.IsSet {
			return Value{}, fmt.Errorf("%d:%d: Array.from argument is not iterable (class '%s' has no 'next(): T | null' method)", pos.Line, pos.Col, srcTy.ClassName)
		}
		elemTy := sig.RetType.withoutNullable()
		// See emitForOfClassIterator: a non-pointer scalar `next(): T | null`
		// now returns a { i1, T } aggregate whose false presence bit means
		// done, so a legitimately-yielded 0 no longer ends iteration (bug #2).
		scalarOptional := isNullableScalar(sig.RetType)

		recvVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}

		dataPtr := e.freshReg()
		lenPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dataPtr))
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenPtr))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", dataPtr))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lenPtr))

		condL := e.freshLabel("arrayfrom.cond")
		bodyL := e.freshLabel("arrayfrom.body")
		endL := e.freshLabel("arrayfrom.end")

		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		nextVal, err := e.emitClassCall(srcTy, recvVal, "next", nil, pos, false)
		if err != nil {
			return Value{}, err
		}
		doneReg := e.freshReg()
		if scalarOptional {
			var present string
			present, nextVal = e.nullableScalarAggParts(nextVal)
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", doneReg, present))
		} else {
			zero := "null"
			if nextVal.Ty.IR != "ptr" {
				zero = "0"
			}
			e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, %s", doneReg, nextVal.Ty.IR, nextVal.Ref, zero))
		}
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doneReg, endL, bodyL))

		e.emitLabel(bodyL)
		e.ensureRealloc()
		curPtr := e.freshReg()
		curLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, dataPtr))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLen, lenPtr))
		newLen := e.freshReg()
		newBytes := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newLen, curLen))
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", newBytes, newLen, elemTy.Align()))
		newPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", newPtr, curPtr, newBytes))
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", slot, elemTy.IR, newPtr, curLen))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, nextVal.Ref, slot, elemTy.Align()))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newPtr, dataPtr))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLen, lenPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

		e.emitLabel(endL)
		finalPtr := e.freshReg()
		finalLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", finalPtr, dataPtr))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLen, lenPtr))
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, finalPtr))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, finalLen))
		return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
	}

	return Value{}, fmt.Errorf("%d:%d: Array.from argument is not iterable (must be an array or a class implementing 'next(): T | null')", pos.Line, pos.Col)
}

// emitArrayCopyWithin implements arr.copyWithin(target, start?, end?):
// copies the sequence [start, end) to position target, in place, within the
// same backing buffer — arr's length never changes. Negative indices count
// from the end (same normalization as .at()/.slice()). Source and
// destination ranges can overlap (e.g. copyWithin(0, 2) on a 5-element
// array), so this uses memmove, not memcpy — the same overlap-safety
// already relied on by shift()/unshift()/splice()'s own tail shifts.
func (e *Emitter) emitArrayCopyWithin(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: copyWithin takes 1–3 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	targetRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	targetN := e.emitNormalizeSliceIdx(e.arrayIndexToI64(targetRaw).Ref, lenReg)

	startN := "0"
	if len(args) >= 2 {
		startRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(e.arrayIndexToI64(startRaw).Ref, lenReg)
	}

	endN := lenReg
	if len(args) == 3 {
		endRaw, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.arrayIndexToI64(endRaw).Ref, lenReg)
	}

	// count = min(max(end - start, 0), len - target) — never reads past
	// [start, end) and never writes past the array's own end.
	rawCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawCount, endN, startN))
	negCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", negCount, rawCount))
	clampedCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", clampedCount, negCount, rawCount))
	avail := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", avail, lenReg, targetN))
	tooBig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooBig, clampedCount, avail))
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", count, tooBig, avail, clampedCount))

	srcGep := e.freshReg()
	dstGep := e.freshReg()
	byteCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcGep, elemTy.IR, ptrReg, startN))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", dstGep, elemTy.IR, ptrReg, targetN))
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteCount, count, elemTy.Align()))
	e.ensureMemmove()
	e.emitInstr(fmt.Sprintf("call ptr @memmove(ptr %s, ptr %s, i64 %s)", dstGep, srcGep, byteCount))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

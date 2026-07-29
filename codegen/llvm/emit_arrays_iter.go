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

	entryTy := ObjectType([]Field{{Name: "index", Ty: TypeI64}, {Name: "value", Ty: elemTy}})
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
	elemGep, elemVal := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", elemGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elemVal, elemTy.IR, elemGep, elemTy.Align()))

	entryReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", entryReg, entrySize))
	idxSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", idxSlot, entryTy.StructIR(), entryReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxVal, idxSlot))
	valSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", valSlot, entryTy.StructIR(), entryReg))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, elemVal, valSlot, elemTy.Align()))

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
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", slot, elemTy.IR, dataReg, i))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, slot, elemTy.Align()))
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %d, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
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
	targetN := e.emitNormalizeSliceIdx(e.coerce(targetRaw, TypeI64).Ref, lenReg)

	startN := "0"
	if len(args) >= 2 {
		startRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(e.coerce(startRaw, TypeI64).Ref, lenReg)
	}

	endN := lenReg
	if len(args) == 3 {
		endRaw, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.coerce(endRaw, TypeI64).Ref, lenReg)
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

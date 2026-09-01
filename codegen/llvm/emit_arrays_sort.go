package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitArrayJoin(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: join takes 0 or 1 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	var sepVal Value
	if len(args) == 0 {
		sepVal = Value{Ref: e.internString(","), Ty: TypePtr}
	} else {
		sepVal, err = e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
	}
	return e.emitArrayJoinCore(ptrReg, lenReg, elemTy, sepVal)
}

// emitArrayJoinCore is the shared join loop: stringify each element (via
// emitValueToString, which handles nested-array elements by recursing through
// this same routine) and concatenate them separated by sepVal. Shared by
// arr.join(sep) and array-to-string coercion (String(arr) / `${arr}` /
// emitValueToString's IsArray branch, which passes the default "," separator).
// Elements are loaded via loadArrayElem so a nested-array element's box pointer
// is transparently unboxed to a real array aggregate before stringification.
func (e *Emitter) emitArrayJoinCore(ptrReg, lenReg string, elemTy Type, sepVal Value) (Value, error) {
	emptyStr := e.internString("")
	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", emptyStr, accAlloca))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("join.cond")
	bodyL := e.freshLabel("join.body")
	firstL := e.freshLabel("join.first")
	restL := e.freshLabel("join.rest")
	incL := e.freshLabel("join.inc")
	doneL := e.freshLabel("join.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	inGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", inGep, elemTy.IR, ptrReg, idxVal))
	elemVal := e.loadArrayElem(inGep, elemTy)
	elemStrVal, err := e.emitValueToString(elemVal)
	if err != nil {
		return Value{}, err
	}
	isFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isFirst, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFirst, firstL, restL))

	e.emitLabel(firstL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", elemStrVal.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(restL)
	accCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accCur, accAlloca))
	withSep, err := e.emitStringConcat(Value{Ref: accCur, Ty: TypePtr}, sepVal)
	if err != nil {
		return Value{}, err
	}
	newAcc, err := e.emitStringConcat(withSep, elemStrVal)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, accAlloca))
	return Value{Ref: result, Ty: TypePtr}, nil
}

// emitArraySort implements arr.sort() and arr.sort(compareFn).
// Sorts in-place using qsort and returns the same array (ptr+len aggregate).
func (e *Emitter) emitArraySort(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: sort takes 0 or 1 arguments", pos.Line, pos.Col)
	}

	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}

	if err := e.emitQsortCall(ptrReg, lenReg, elemTy, args, pos); err != nil {
		return Value{}, err
	}

	// Return the array as an aggregate (same ptr+len as input)
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	retTy := ArrayOf(elemTy)
	return Value{Ref: r1, Ty: retTy}, nil
}

// emitQsortCall resolves a sort comparator (default or custom) and issues the
// qsort() call against ptrReg[0:lenReg] in place — shared by emitArraySort
// (sorts the caller's own array) and emitArrayToSorted (sorts a fresh copy,
// leaving the original untouched).
func (e *Emitter) emitQsortCall(ptrReg, lenReg string, elemTy Type, args []ast.Expression, pos ast.Pos) error {
	if err := e.rejectNestedArrayElem(elemTy, "sort", pos); err != nil {
		return err
	}
	e.ensureQsort()

	var cmpFnRef string

	if len(args) == 0 {
		// Default (no-comparator) sort is JS-faithful: every element is
		// converted to a string and compared lexicographically, even numbers
		// — [10,1,21,2].sort() is [1,10,2,21], not the numeric [1,2,10,21]
		// (ADR-00546). A string element is already a lexicographic strcmp.
		switch {
		case elemTy.Float:
			e.ensureSortCmpF64Lex()
			cmpFnRef = "@__kml_cmp_f64_lex"
		case isStringTy(elemTy):
			e.ensureSortCmpStr()
			cmpFnRef = "@__kml_cmp_str"
		default:
			e.ensureSortCmpI64Lex()
			cmpFnRef = "@__kml_cmp_i64_lex"
		}
	} else {
		// Custom comparator: resolve closure and store in global, use trampoline
		cb, err2 := e.resolveCallbackWithHints(args[0], []Type{elemTy, elemTy})
		if err2 != nil {
			return err2
		}
		if cb.kind != cbClosure {
			return fmt.Errorf("%d:%d: sort comparator must be an arrow function or closure", pos.Line, pos.Col)
		}

		e.ensureSortClosGlobal()
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_sort_clos, align 8", cb.hdrPtr))

		switch {
		case elemTy.Float:
			e.ensureSortTrampolineF64()
			cmpFnRef = "@__kml_sort_tramp_f64"
		case isStringTy(elemTy):
			e.ensureSortTrampolineStr()
			cmpFnRef = "@__kml_sort_tramp_str"
		default:
			e.ensureSortTrampolineI64()
			cmpFnRef = "@__kml_sort_tramp_i64"
		}
	}

	elemSize := int64(8)
	if elemTy.IR == "i1" {
		elemSize = 1
	} else if elemTy.IR == "i8" {
		elemSize = 1
	} else if elemTy.IR == "i16" {
		elemSize = 2
	} else if elemTy.IR == "i32" {
		elemSize = 4
	}

	e.emitInstr(fmt.Sprintf("call void @qsort(ptr %s, i64 %s, i64 %d, ptr %s)", ptrReg, lenReg, elemSize, cmpFnRef))
	return nil
}

// emitArrayToSorted implements arr.toSorted(cmp?): same comparator logic as
// .sort(), but sorts a fresh copy and leaves arr untouched.
func (e *Emitter) emitArrayToSorted(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: toSorted takes 0 or 1 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	newPtr := e.emitArrayCopy(ptrReg, lenReg, elemTy)
	if err := e.emitQsortCall(newPtr, lenReg, elemTy, args, pos); err != nil {
		return Value{}, err
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(elemTy)}, nil
}

// emitArrayCopy mallocs a new buffer sized for lenReg elements of elemTy and
// memcpy's ptrReg[0:lenReg] into it, returning the new data pointer register.
// Shared by every array method that needs to return a mutated copy without
// touching the original (toSorted, toReversed, with, values, toSpliced).

func (e *Emitter) emitArraySlice(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: slice takes 1 or 2 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureMalloc()
	e.ensureMemcpy()

	startRaw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	startN := e.emitNormalizeSliceIdx(e.coerce(startRaw, TypeI64).Ref, lenReg)

	var endN string
	if len(args) == 2 {
		endRaw, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		endN = e.emitNormalizeSliceIdx(e.coerce(endRaw, TypeI64).Ref, lenReg)
	} else {
		endN = lenReg
	}

	rawLen := e.freshReg()
	isNegLen := e.freshReg()
	sliceLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", rawLen, endN, startN))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNegLen, rawLen))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", sliceLen, isNegLen, rawLen))

	byteCount := e.freshReg()
	newPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteCount, sliceLen, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", newPtr, byteCount))

	srcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcGep, elemTy.IR, ptrReg, startN))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, srcGep, byteCount))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, newPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, sliceLen))
	// A TypedArray's slice is the same TypedArray kind (flags preserved so
	// e.g. a BigInt64Array's copy still wraps elements — TDD-00101).
	ty := ArrayOf(elemTy)
	recvTy := e.inferExprType(mem.Object)
	ty.IsTypedArray = recvTy.IsTypedArray
	ty.BigIntElem = recvTy.BigIntElem
	ty.Clamped = recvTy.Clamped
	return Value{Ref: r1, Ty: ty}, nil
}

// emitElemEq emits an i1 register for (a == b) where a and b have type elemTy.
// Strings use strcmp; floats use fcmp oeq; all other types use icmp eq.

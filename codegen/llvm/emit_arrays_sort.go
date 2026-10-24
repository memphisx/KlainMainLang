package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitArrayJoin(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: join takes 0 or 1 arguments", pos.Line, pos.Col)
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	hdr = e.hofHolesHeader(mem.Object, hdr)

	var sepVal Value
	if len(args) == 0 {
		sepVal = Value{Ref: e.internString(","), Ty: TypePtr}
	} else {
		sepVal, err = e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		// ToString the separator so a non-string arg (an object with toString, a
		// number, a NaN-boxed `any`) never reaches emitArrayJoinCore's `ptr`-typed
		// separator slot as a raw word (invalid IR). `undefined` is the one value
		// that means "use the default ','," (`[1,2].join(undefined) === "1,2"`); a
		// bare string passes through, and `null` is left for the core's own
		// null → "null" select.
		switch {
		case sepVal.Ty.IsUndefined() || sepVal.Ty.IR == "void":
			sepVal = Value{Ref: e.internString(","), Ty: TypePtr}
		case isStringTy(sepVal.Ty):
			// already a ptr string (incl. a null literal, handled downstream)
		default:
			if sepVal, err = e.emitValueToString(sepVal); err != nil {
				return Value{}, err
			}
		}
	}
	e.joinHoles = hdr
	return e.emitArrayJoinCore(ptrReg, lenReg, elemTy, sepVal)
}

// emitArrayJoinCore is the shared join loop: stringify each element (via
// emitValueToString, which handles nested-array elements by recursing through
// this same routine) and concatenate them separated by sepVal. Shared by
// arr.join(sep) and array-to-string coercion (String(arr) / `${arr}` /
// emitValueToString's IsArray branch, which passes the default "," separator).
// Elements are loaded via loadArrayElem so a nested-array element's box pointer
// is transparently unboxed to a real array aggregate before stringification.
//
// A hole of the array whose header the caller set in e.joinHoles joins as "".
func (e *Emitter) emitArrayJoinCore(ptrReg, lenReg string, elemTy Type, sepVal Value) (Value, error) {
	holesHdr := e.joinHoles
	e.joinHoles = ""
	// Each element's string goes into a scratch array, joined in one
	// allocation at the end (__kml_str_join).
	e.ensureStrHeaderRuntime()
	sepVal = e.emitStringNullToLiteral(sepVal)
	partsBytes, parts := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = shl i64 %s, 3", partsBytes, lenReg))
	e.heapMallocInto(parts, partsBytes)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("join.cond")
	bodyL := e.freshLabel("join.body")
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
	var elemStrVal Value
	if elemVal.Ty.IR == "ptr" && !elemVal.Ty.IsDynamic() && !elemVal.Ty.IsArray() {
		// A null or undefined element (a null reference) joins as "".
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(""), slot))
		isNull := e.isAbsentPtr(elemVal.Ref, elemVal.Ty) // three-state null too
		convL, joinL := e.freshLabel("join.conv"), e.freshLabel("join.str")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, joinL, convL))
		e.emitLabel(convL)
		conv, err := e.emitValueToString(elemVal)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", conv.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
		e.emitLabel(joinL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
		elemStrVal = Value{Ref: r, Ty: TypePtr}
	} else {
		var err error
		if elemStrVal, err = e.emitValueToString(elemVal); err != nil {
			return Value{}, err
		}
		// A null or undefined element joins as "" (Array.prototype.join).
		var absent string
		switch {
		case isUnconstrainedDynamic(elemVal.Ty) || elemVal.Ty.IsDynamic() && len(elemVal.Ty.UnionMembers) > 0:
			tag, _ := e.emitUnboxTagPayload(elemVal)
			isNull, isUndef := e.freshReg(), e.freshReg()
			absent = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absent, isNull, isUndef))
		case isNullableScalar(elemVal.Ty):
			present, _ := e.nullableScalarAggParts(elemVal)
			absent = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", absent, present))
		}
		if absent != "" {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, absent, e.internString(""), elemStrVal.Ref))
			elemStrVal = Value{Ref: r, Ty: TypePtr}
		}
	}
	if holesHdr != "" {
		hole, r := e.emitHoleTest(holesHdr, idxVal), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, hole, e.internString(""), elemStrVal.Ref))
		elemStrVal = Value{Ref: r, Ty: TypePtr}
	}
	partSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", partSlot, parts, idxVal))
	e.heapStore("ptr", e.emitStringNullToLiteral(elemStrVal).Ref, partSlot, 8)
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_join(ptr %s, i64 %s, ptr %s)", result, parts, lenReg, sepVal.Ref))
	e.heapFree(parts)
	return Value{Ref: result, Ty: TypePtr}, nil
}

// emitArraySort implements arr.sort() and arr.sort(compareFn).
// Sorts in-place, stably (__kml_stable_sort), and returns the same array (ptr+len aggregate).
func (e *Emitter) emitArraySort(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: sort takes 0 or 1 arguments", pos.Line, pos.Col)
	}
	if err := bigIntSortComparatorErr(e.inferExprType(mem.Object), "sort", args, pos); err != nil {
		return Value{}, err
	}

	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitArrayGuardFor(mem.Object, arrOpReorder, nil)

	sortLen := lenReg
	if hh := e.hofHolesHeader(mem.Object, hdr); hh != "" {
		// The set elements are sorted; the holes go last, as Node leaves them.
		e.ensureHoles()
		sortLen = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_holes_compact(ptr %s, i64 %d, i64 %s)", sortLen, hh, elemTy.Align(), lenReg))
		e.emitFillHoles(ptrReg, sortLen, lenReg, elemTy)
	}
	if err := e.emitQsortCall(ptrReg, sortLen, elemTy, args, e.inferExprType(mem.Object).IsTypedArray(), pos); err != nil {
		return Value{}, err
	}

	// Return the array value (same ptr+len as input)
	r1 := e.receiverArrayHeader(hdr, ptrReg, lenReg)
	retTy := ArrayOf(elemTy)
	return Value{Ref: r1, Ty: retTy}, nil
}

// defaultObjectComparator is SortCompare's no-comparator case as a
// comparator: (a, b) => { const x = `${a}`; const y = `${b}`; return x < y ?
// -1 : x > y ? 1 : 0 }.
func defaultObjectComparator(pos ast.Pos) ast.Expression {
	const a, b, x, y = "__kml_sort_a", "__kml_sort_b", "__kml_sort_x", "__kml_sort_y"
	str := func(n string) ast.Expression {
		return ast.NewTemplateLiteral([]string{"", ""}, []ast.Expression{ast.NewIdentifier(n, pos)}, pos)
	}
	id := func(n string) ast.Expression { return ast.NewIdentifier(n, pos) }
	ret := ast.NewConditionalExpression(ast.NewBinaryExpression("<", id(x), id(y), pos),
		ast.NewNumberLiteral("-1", pos),
		ast.NewConditionalExpression(ast.NewBinaryExpression(">", id(x), id(y), pos),
			ast.NewNumberLiteral("1", pos), ast.NewNumberLiteral("0", pos), pos), pos)
	body := ast.NewBlockStatement([]ast.Statement{
		ast.NewVarDeclaration("const", x, nil, str(a), pos),
		ast.NewVarDeclaration("const", y, nil, str(b), pos),
		ast.NewReturnStatement(ret, pos),
	}, pos)
	return ast.NewArrowFunction([]ast.Param{{Name: a}, {Name: b}}, nil, nil, body, pos)
}

// sortResultKind is how the sort's trampoline reads a comparator's result
// (collections.c SORT_RES_*): by the comparator's own return type, whatever
// the elements are.
func sortResultKind(t Type) int {
	switch {
	case t.IsDynamic():
		return 2
	case t.IR == "double":
		return 1
	case t.IR == "float":
		return 5
	case t.IR == "i1":
		return 3
	case t.IR == "i32":
		return 4
	case t.IR == "i16":
		return 7
	case t.IR == "i8":
		return 8
	case t.IR == "void" || t.IR == "":
		return 6
	}
	return 0
}

// emitQsortCall resolves a sort comparator (default or custom) and issues the
// qsort() call against ptrReg[0:lenReg] in place — shared by emitArraySort
// (sorts the caller's own array) and emitArrayToSorted (sorts a fresh copy,
// leaving the original untouched).
// A TypedArray's (typed) default order is numeric, -0 before +0 and NaN
// last (TypedArray.prototype.sort), over its elements' own width.
func (e *Emitter) emitQsortCall(ptrReg, lenReg string, elemTy Type, args []ast.Expression, typed bool, pos ast.Pos) error {
	e.ensureQsort()

	var cmpFnRef string

	if len(args) == 0 && !elemTy.IsDynamic() && elemTy.IR == "ptr" && !isForOfStringTy(elemTy.withoutNullable()) {
		// An object array's (a class's, a Date's, …) default order is its
		// elements' strings
		// (SortCompare: ToString each side once per comparison), so a
		// toString() decides and plain objects, all "[object Object]", keep
		// their order.
		args = []ast.Expression{defaultObjectComparator(pos)}
	}
	if len(args) == 0 && typed {
		suffix := map[string]string{"i8": "i8", "i16": "i16", "i32": "i32", "i64": "i64", "float": "f32", "double": "f64"}[elemTy.IR]
		if suffix != "" && !elemTy.Float && !elemTy.Signed && elemTy.IR != "i64" {
			suffix = "u" + suffix[1:]
		} else if elemTy.IR == "i64" && !elemTy.Signed && !elemTy.BigIntElem {
			suffix = "u64"
		}
		if suffix != "" {
			cmpFnRef = "@__kml_cmp_typed_" + suffix
			e.usedCollections = true
			e.declareFn(cmpFnRef[1:], fmt.Sprintf("declare i32 %s(ptr, ptr)", cmpFnRef))
		}
	}
	if cmpFnRef != "" {
	} else if len(args) == 0 {
		// Default (no-comparator) sort is JS-faithful: every element is
		// converted to a string and compared lexicographically, even numbers
		// — [10,1,21,2].sort() is [1,10,2,21], not the numeric [1,2,10,21]
		// (ADR-00546). A string element is already a lexicographic strcmp.
		switch {
		case elemTy.IsDynamic():
			// Boxed-element array (`any[]`): lexicographic over each element's
			// JS String() form, like JS's type-agnostic default sort.
			e.ensureSortCmpAnyLex()
			cmpFnRef = "@__kml_cmp_any_lex"
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
		e.emitInstr(fmt.Sprintf("store i32 %d, ptr @__kml_sort_res, align 4", sortResultKind(cb.retType())))

		switch {
		case elemTy.IR == "float" || elemTy.IR == "i1" || elemTy.IR == "i8" || elemTy.IR == "i16" || elemTy.IR == "i32":
			// A narrower element: read at its own width (an 8-byte read
			// would run past the buffer's end).
			name := map[string]string{"float": "f32", "i1": "u8", "i8": "i8", "i16": "i16", "i32": "i32"}[elemTy.IR]
			if elemTy.IR != "float" && elemTy.IR != "i1" && !elemTy.Signed {
				name = "u" + name[1:]
			}
			// The comparator's own parameter type picks the call: an
			// integer takes the widened integer, a double the number.
			ps := cb.paramTypes()
			switch {
			case len(ps) > 0 && ps[0].IR == "double":
				name += "_d"
			case len(ps) > 0 && ps[0].IR == "float" && elemTy.IR != "float",
				len(ps) > 0 && elemTy.IR == "float" && ps[0].IR != "float":
				// Another pairing: the comparator adapted to numbers.
				ft := cb.ty
				ft.FuncParams = []Type{TypeF64, TypeF64}
				ad := e.convert(Value{Ref: cb.hdrPtr, Ty: cb.ty}, ft)
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_sort_clos, align 8", ad.Ref))
				name += "_d"
			}
			e.ensureSortClosGlobal()
			e.usedCollections = true
			cmpFnRef = "@__kml_sort_tramp_" + name
			e.declareFn(cmpFnRef[1:], fmt.Sprintf("declare i32 %s(ptr, ptr)", cmpFnRef))
		case elemTy.IsDynamic():
			// Boxed-element array (`any[]`): the closure takes/returns boxed
			// `any`; the trampoline reduces the boxed-number result to a sign.
			e.ensureSortTrampolineAny()
			cmpFnRef = "@__kml_sort_tramp_any"
		case elemTy.Float:
			e.ensureSortTrampolineF64()
			cmpFnRef = "@__kml_sort_tramp_f64"
		case isStringTy(elemTy):
			e.ensureSortTrampolineStr()
			cmpFnRef = "@__kml_sort_tramp_str"
		case elemTy.IsArray():
			// An array of arrays: header pointers, each passed with its length.
			e.ensureSortTrampolineArr()
			cmpFnRef = "@__kml_sort_tramp_arr"
		case elemTy.IsObject() || elemTy.IsClass():
			// An object/class comparator (`(a, b) => a.k - b.k`) returns a
			// `number` (double); the i64 trampoline would read that double's bits
			// as i64 and truncate to garbage, making the sort a silent no-op
			// (ADR-00681). The obj trampoline loads ptr elements and reads a
			// double result, like the string one.
			e.ensureSortTrampolineObj()
			cmpFnRef = "@__kml_sort_tramp_obj"
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
	} else if elemTy.IR == "i32" || elemTy.IR == "float" {
		elemSize = 4
	}

	if elemTy.IsDynamic() {
		// undefined elements go last, unsorted and never compared.
		e.emitInstr(fmt.Sprintf("call void @__kml_sort_any(ptr %s, i64 %s, ptr %s)", ptrReg, lenReg, cmpFnRef))
		return nil
	}
	if elemTy.IR == "ptr" {
		// A string or object array: its undefined (a null pointer) goes last
		// the same way; its null (nullRef) is compared, as "null" by the
		// default string sort.
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_sort_nullref, align 8", e.nullRef()))
		e.emitInstr(fmt.Sprintf("call void @__kml_sort_ptr(ptr %s, i64 %s, ptr %s)", ptrReg, lenReg, cmpFnRef))
		return nil
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_stable_sort(ptr %s, i64 %s, i64 %d, ptr %s)", ptrReg, lenReg, elemSize, cmpFnRef))
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
	if err := bigIntSortComparatorErr(e.inferExprType(mem.Object), "toSorted", args, pos); err != nil {
		return Value{}, err
	}
	newPtr := e.emitArrayCopy(ptrReg, lenReg, elemTy)
	if err := e.emitQsortCall(newPtr, lenReg, elemTy, args, e.inferExprType(mem.Object).IsTypedArray(), pos); err != nil {
		return Value{}, err
	}
	r1 := e.newArrayHeader(newPtr, lenReg)
	return Value{Ref: r1, Ty: e.copyResultType(mem.Object, elemTy)}, nil
}

// emitArrayCopy mallocs a new buffer sized for lenReg elements of elemTy and
// memcpy's ptrReg[0:lenReg] into it, returning the new data pointer register.
// Shared by every array method that needs to return a mutated copy without
// touching the original (toSorted, toReversed, with, values, toSpliced).

func (e *Emitter) emitArraySlice(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: slice takes 0 to 2 arguments", pos.Line, pos.Col)
	}
	hdr, ptrReg, lenReg, elemTy, err := e.resolveArrayHOF(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	hdr = e.hofHolesHeader(mem.Object, hdr)
	e.ensureMalloc()
	e.ensureMemcpy()

	// Zero-arg slice() is a full shallow copy: start = 0.
	startN := "0"
	if len(args) >= 1 {
		startRaw, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		startIdx, err := e.arrayIndexToI64(startRaw, args[0].GetPos())
		if err != nil {
			return Value{}, err
		}
		startN = e.emitNormalizeSliceIdx(startIdx.Ref, lenReg)
	}

	var endN string
	if len(args) == 2 {
		if endN, err = e.emitEndIndex(args[1], lenReg); err != nil {
			return Value{}, err
		}
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
	e.heapMallocInto(newPtr, byteCount)

	srcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", srcGep, elemTy.IR, ptrReg, startN))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", newPtr, srcGep, byteCount))

	r1 := e.newArrayHeader(newPtr, sliceLen)
	e.emitHolesCopy(r1, "0", hdr, startN, sliceLen)
	return Value{Ref: r1, Ty: e.copyResultType(mem.Object, elemTy)}, nil
}

// emitEndIndex is a range's end index from its argument (slice, subarray,
// fill, copyWithin): relative to lenReg when negative, clamped to
// [0, lenReg], and lenReg itself when the argument is undefined.
func (e *Emitter) emitEndIndex(arg ast.Expression, lenReg string) (string, error) {
	raw, err := e.emitExpr(arg)
	if err != nil {
		return "", err
	}
	undef := e.emitArgIsUndefined(raw)
	idx, err := e.arrayIndexToI64(raw, arg.GetPos())
	if err != nil {
		return "", err
	}
	end := e.emitNormalizeSliceIdx(idx.Ref, lenReg)
	if undef == "false" {
		return end, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, undef, lenReg, end))
	return r, nil
}

// copyResultType is the type of a copy of recv's elements (slice, toSorted,
// toReversed, with): a TypedArray's is the same TypedArray kind (flags
// preserved so e.g. a BigInt64Array's copy still wraps elements —
// TDD-00101), any other's an array of elemTy.
func (e *Emitter) copyResultType(recv ast.Expression, elemTy Type) Type {
	ty := ArrayOf(elemTy)
	recvTy := e.inferExprType(recv)
	ty.setHost(hostTypedArray, recvTy.IsTypedArray())
	ty.BigIntElem = recvTy.BigIntElem
	ty.Clamped = recvTy.Clamped
	return ty
}

// emitElemEq emits an i1 register for (a == b) where a and b have type elemTy.
// Strings use strcmp; floats use fcmp oeq; all other types use icmp eq.

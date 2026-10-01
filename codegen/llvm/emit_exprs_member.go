package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/checker"
	"fmt"
	"sort"
	"strings"
)

// emitOptionalMember emits `obj?.property`. For ptr-typed objects it emits a
// null check; a null object yields the zero value for the property's type.
// Supports: string `.length` → i64; object fields → field type.
func (e *Emitter) emitOptionalMember(ex *ast.MemberExpression) (Value, error) {
	// `m?.index` on an exec() result that may be null: undefined then.
	if ty, off, ok := execArrayMemberType(ex.Property); ok && e.inferExprType(ex.Object).ExecArray {
		v, err := e.emitExpr(ex.Object)
		if err != nil {
			return Value{}, err
		}
		if v.ArrayHeader != "" {
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, ty.IR))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ty.IR, zeroRef(ty), slot))
			present := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", present, v.ArrayHeader))
			readL, doneL := e.freshLabel("execm.read"), e.freshLabel("execm.done")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, readL, doneL))
			e.emitLabel(readL)
			gep, r := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", gep, v.ArrayHeader, off))
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, ty.IR, gep))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ty.IR, r, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(doneL)
			out := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", out, ty.IR, slot))
			if ty.IR != "ptr" {
				return e.wrapUndefinedable(Value{Ref: out, Ty: ty}, present), nil
			}
			res := ty
			res.Nullable, res.IsUndefined = true, true
			return Value{Ref: out, Ty: res}, nil
		}
	}
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	if objVal.Ty.NullAndUndef {
		objVal = e.fromThreeState(objVal) // either absence short-circuits
	}

	// Non-ptr types cannot be a null pointer; fall back to a regular
	// (non-optional) access. Arrays report IR "ptr" but are actually {ptr, i64}
	// aggregate values, not bare pointers — an `icmp eq ptr` null check against
	// one is invalid IR (ADR-00539). A non-nullable array is never null; a
	// nullable array uses the {null, 0} value sentinel whose own `.length` is 0,
	// so plain access is the right behavior either way.
	// `xs?.length` on a nullable array: `undefined` when the binding holds no
	// array. The value in hand is already null-safe ({null,0} when absent), so
	// the length comes straight off the aggregate.
	if objVal.Ty.IsArray && objVal.Ty.Nullable && ex.Property == "length" {
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", present, e.emitArrayIsAbsent(objVal)))
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, objVal.Ref))
		return e.wrapUndefinedable(e.countToNumber(Value{Ref: lenReg, Ty: TypeI64}), present), nil
	}
	if isUnconstrainedDynamic(objVal.Ty) && objVal.Ty.DynPropTy == nil {
		// `x?.p` on an `any`: undefined where x holds null or undefined.
		tag, _ := e.emitUnboxTagPayload(objVal)
		isNull, isUndef, nullish := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isNull, isUndef))
		res := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, res))
		getL, doneL := e.freshLabel("optany.get"), e.freshLabel("optany.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, doneL, getL))
		e.emitLabel(getL)
		v, err := e.emitDynAnyMemberGetNamed(objVal, e.internString(ex.Property), ex.Property, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v.Ref, res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, res))
		return Value{Ref: r, Ty: TypeAny}, nil
	}
	if objVal.Ty.IR != "ptr" || objVal.Ty.IsArray {
		plain := &ast.MemberExpression{Object: ex.Object, Property: ex.Property}
		return e.emitMemberUnguarded(plain)
	}

	// Determine the result type before emitting branches. TDD-00030: a
	// class accessor (getter/setter) is checked before the plain-field
	// FieldIndex path — an accessor-only property name is never a real
	// Field, so FieldIndex would otherwise report "no field" for it.
	var resultTy Type
	isAccessor := false
	// viaMember is the plain access the non-null branch emits for a handle
	// whose members are dispatched rather than stored as fields (a cluster
	// Worker's `.id`): the checked object bound to a temporary.
	var viaMember *ast.MemberExpression
	if ex.Property == "length" && !objVal.Ty.IsObject {
		resultTy = TypeI64
	} else if objVal.Ty.IsClass {
		if getter, _, ok := e.classAccessorSigs(objVal.Ty.ClassName, ex.Property); ok {
			if getter == nil {
				return Value{}, fmt.Errorf("%d:%d: property '%s' has no getter", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
			}
			resultTy = getter.RetType
			isAccessor = true
		} else {
			_, fieldTy, ok := objVal.Ty.FieldIndex(ex.Property)
			if !ok {
				return Value{}, fmt.Errorf("%d:%d: no field '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
			}
			resultTy = e.canonicalizeClassTy(fieldTy)
		}
	} else if objVal.Ty.IsObject || objVal.Ty.IsDynamicObject {
		if _, fieldTy, ok := objVal.Ty.FieldIndex(ex.Property); ok && !objVal.Ty.IsDynamicObject {
			resultTy = e.canonicalizeClassTy(fieldTy)
		} else {
			recvTy := objVal.Ty
			recvTy.Nullable, recvTy.IsUndefined = false, false
			name := fmt.Sprintf("__kml_optrecv_%d", e.optRecvCtr)
			e.optRecvCtr++
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objVal.Ref, slot))
			e.define(name, Symbol{Ptr: slot, Ty: recvTy})
			viaMember = ast.NewMemberExpression(ast.NewIdentifier(name, ex.GetPos()), ex.Property, ex.GetPos())
			resultTy = e.inferExprType(viaMember)
			if objVal.Ty.IsDynamicObject && objVal.Ty.MapVal != nil {
				resultTy = *objVal.Ty.MapVal // a dictionary's entry
			}
			if resultTy.IR == "" || resultTy.IR == "void" {
				return Value{}, fmt.Errorf("%d:%d: no field '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
			}
		}
	} else {
		return Value{}, fmt.Errorf("%d:%d: optional chaining '?.' does not support property '%s' on type %s",
			ex.GetPos().Line, ex.GetPos().Col, ex.Property, objVal.Ty.IR)
	}

	// `o?.x` is `PropType | undefined` — a short-circuit on a null object yields
	// a real `undefined`, as in TS (TDD-00187). A scalar prop rides the `{ i1, T }`
	// optional; a pointer prop the null pointer with the static type flagged
	// Nullable|IsUndefined; an array/tuple prop has no spare absent state
	// (ADR-00246) so it keeps the zero-shaped value. Must mirror inferExprType's
	// `Optional` member case.
	undefTy := undefinedableElem(resultTy)
	wrapUndef := undefTy.Nullable && !resultTy.IsArray
	// The result buffer holds the optional's VALUE. For an array that value is the
	// {ptr,i64} aggregate, not the header-pointer field storage StructFieldIR now
	// reports (TDD-00213 Stage 2) — size/type the buffer for the aggregate.
	resIR := StructFieldIR(undefTy)
	if resultTy.IsArray {
		resIR = "{ptr, i64}"
	}

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, resIR, undefTy.Align()))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, objVal.Ref))

	nullL := e.freshLabel("optc.null")
	noNullL := e.freshLabel("optc.nn")
	mergeL := e.freshLabel("optc.merge")

	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, noNullL))

	// null branch: store the absent value — `undefined` for a scalar/pointer prop
	// (a {present=false} optional, or the null pointer), or the zero-shaped
	// {null,0} for an array prop (no absent state; real JS's empty-shaped zero).
	e.emitLabel(nullL)
	if resultTy.IsArray {
		z0 := e.freshReg()
		z1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr null, 0", z0))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 0, 1", z1, z0))
		e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align %d", z1, resPtr, undefTy.Align()))
	} else if isNullableScalar(undefTy) {
		agg := e.makeNullableScalarAgg(undefTy, "false", zeroRef(resultTy))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, agg, resPtr, undefTy.Align()))
	} else {
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, zeroRef(resultTy), resPtr, undefTy.Align()))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// non-null branch: perform the property access on objVal
	e.emitLabel(noNullL)
	var propVal Value
	if viaMember != nil {
		v, err := e.emitMemberUnguarded(viaMember)
		if err != nil {
			return Value{}, err
		}
		propVal = v
	} else if ex.Property == "length" {
		e.ensureStrlen()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", r, objVal.Ref))
		propVal = Value{Ref: r, Ty: TypeI64}
	} else if isAccessor {
		v, err := e.emitClassCall(objVal.Ty, objVal, accessorMethodName("get", ex.Property), nil, ex.GetPos(), false)
		if err != nil {
			return Value{}, err
		}
		propVal = v
	} else {
		idx, fieldTy, _ := objVal.Ty.FieldIndex(ex.Property)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d",
			gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
		if isRecordView(objVal.Ty) {
			// Another layout behind a structural type (TDD-00233).
			gepReg, _ = e.emitRecordFieldSlot(objVal, gepReg, fieldTy, ex.Property, true)
		}
		if fieldTy.IsArray {
			propVal = e.loadArrayFieldValue(gepReg, fieldTy) // header-pointer slot (TDD-00213 S2)
		} else {
			loadReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
				loadReg, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
			propVal = Value{Ref: loadReg, Ty: fieldTy}
		}
	}
	propVal = e.coerce(propVal, resultTy)
	presentRef := propVal.Ref
	if isNullableScalar(undefTy) && !isNullableScalar(resultTy) {
		// (An optional field's value is already the { i1, T } aggregate.)
		presentRef = e.makeNullableScalarAgg(undefTy, "true", propVal.Ref)
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, presentRef, resPtr, undefTy.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, resIR, resPtr, undefTy.Align()))
	if wrapUndef {
		return Value{Ref: result, Ty: undefTy}, nil
	}
	return Value{Ref: result, Ty: resultTy}, nil
}

// signedIntMin returns the minimum representable value for a signed
// integer IR width as an LLVM literal — used by emitDivZeroGuard's second
// UB check below. Callers only ever pass one of these four widths (every
// integer type this compiler has, per types.go's TypeI8/.../TypeI64), so
// the default case covers i64 rather than needing its own explicit case.
func signedIntMin(ir string) string {
	switch ir {
	case "i8":
		return "-128"
	case "i16":
		return "-32768"
	case "i32":
		return "-2147483648"
	default: // "i64"
		return "-9223372036854775808"
	}
}

// emitDivZeroGuard emits runtime checks that throw a catchable Error before
// an integer sdiv/udiv/srem/urem, covering both of LLVM's documented UB
// cases for these instructions:
//   - a zero divisor (any integer type, signed or unsigned);
//   - signed types only — dividing that type's minimum representable value
//     by -1. The mathematical result (e.g. i64 MIN / -1 = 2^63) doesn't fit
//     back into the same width, the mirror-image overflow of the zero-
//     divisor case. Unsigned division has no such case: there's no negative
//     divisor to trigger it. Found by inspection while scoping TDD-00014's
//     codegen fuzzer, not by an actual repro (reaching this exact dividend
//     by chance is astronomically unlikely) — added once actually picked up
//     rather than left as a documented gap indefinitely.
//
// Under -O2 both were observed to silently produce garbage output rather
// than a defined crash or exception, on top of being genuinely platform-
// dependent (traps on x86, doesn't on arm64). No-op for float types, where
// JS's Infinity/NaN semantics already fall out of IEEE-754 fdiv/frem
// without a guard. Must be called after both operands' Values are
// available and before emitting the actual div/rem instruction; leaves the
// emitter inside a fresh "ok" block, mirroring emitIndexPtr's bounds-check
// pattern below.
func (e *Emitter) emitDivZeroGuard(ty Type, left, right Value) {
	if ty.Float {
		return
	}
	zeroReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, 0", zeroReg, ty.IR, right.Ref))
	zeroL := e.freshLabel("div.zero")
	nonZeroL := e.freshLabel("div.nonzero")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", zeroReg, zeroL, nonZeroL))

	e.emitLabel(zeroL)
	e.emitInternalThrow(e.internString("Division by zero"))

	e.emitLabel(nonZeroL)
	if !ty.Signed {
		return
	}

	negOneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, -1", negOneReg, ty.IR, right.Ref))
	minReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, %s", minReg, ty.IR, left.Ref, signedIntMin(ty.IR)))
	overflowReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", overflowReg, negOneReg, minReg))
	overflowL := e.freshLabel("div.overflow")
	okL := e.freshLabel("div.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", overflowReg, overflowL, okL))

	e.emitLabel(overflowL)
	e.emitInternalThrow(e.internString("Division overflow"))

	e.emitLabel(okL)
}

// emitIndexPtr computes and returns the GEP register pointing to arr[index].
// The array object may be a named variable (Symbol path) or any expression
// that returns a {ptr, i64} aggregate (extractvalue path). Emits a runtime
// bounds check that throws a catchable Error on out-of-range access (index
// treated as unsigned so a negative index and index >= length are caught by
// a single comparison).
func (e *Emitter) emitIndexPtr(ex *ast.IndexExpression) (gepReg string, elemTy Type, err error) {
	dataPtrReg, lenReg, idxRef, elemTy, err := e.emitIndexBase(ex)
	if err != nil {
		return "", TypeVoid, err
	}

	oobReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp uge i64 %s, %s", oobReg, idxRef, lenReg))
	oobL := e.freshLabel("arr.oob")
	okL := e.freshLabel("arr.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", oobReg, oobL, okL))

	e.emitLabel(oobL)
	e.emitInternalThrow(e.internString("Array index out of bounds"))

	e.emitLabel(okL)
	return e.indexElemGEP(dataPtrReg, idxRef, elemTy), elemTy, nil
}

// indexElemGEP addresses element idxRef of an array backing buffer (a flat
// value-type element strides by its struct size).
func (e *Emitter) indexElemGEP(dataPtrReg, idxRef string, elemTy Type) string {
	gepReg := e.freshReg()
	gepTy := elemTy.IR
	if elemTy.Inline {
		gepTy = elemTy.StructIR()
	}
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, gepTy, dataPtrReg, idxRef))
	return gepReg
}

// emitIndexRead implements the element read `a[i]`: an in-range index loads
// the element, an out-of-range one (negative included — the index compares
// unsigned) reads `undefined`, as in Node. The result is the element's
// `T | undefined` form (indexReadType): a scalar rides the { i1, T } aggregate
// (a missing float payload is NaN, what Node's arithmetic on `undefined`
// gives), a pointer is null, a nested array the {null,0} aggregate with a null
// header, a dynamic element the `undefined` box.
func (e *Emitter) emitIndexRead(ex *ast.IndexExpression) (Value, error) {
	dataPtrReg, lenReg, idxRef, elemTy, err := e.emitIndexBase(ex)
	if err != nil {
		return Value{}, err
	}
	bigElem := e.inferExprType(ex.Object)
	resTy := elemTy
	if elemTy.Inline {
		resTy.Inline = false
	}
	if bigElem.BigIntElem {
		resTy = BigIntType()
	}
	resIR := resTy.IR
	miss := missRef(resTy)
	switch {
	case resTy.IsArray:
		resIR, miss = "{ptr, i64}", "zeroinitializer"
	case resTy.Float && !resTy.IsDynamic:
		miss = "0x7FF8000000000000"
	}
	result := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", result, resIR))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", resIR, miss, result))
	var headerSlot string
	if resTy.IsArray {
		headerSlot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", headerSlot))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", headerSlot))
	}

	inb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, %s", inb, idxRef, lenReg))
	loadL := e.freshLabel("idx.load")
	doneL := e.freshLabel("idx.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inb, loadL, doneL))

	e.emitLabel(loadL)
	elem := e.loadArrayElem(e.indexElemGEP(dataPtrReg, idxRef, elemTy), elemTy)
	// TDD-00101: a BigInt64Array/BigUint64Array element surfaces as a bigint
	// handle, not the raw stored i64.
	if bigElem.BigIntElem {
		elem = e.wrapTypedArrayLoad(elem, bigElem)
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", resIR, elem.Ref, result))
	if headerSlot != "" && elem.ArrayHeader != "" {
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", elem.ArrayHeader, headerSlot))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", out, resIR, result))
	v := Value{Ref: out, Ty: elem.Ty}
	if headerSlot != "" {
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, headerSlot))
		v.ArrayHeader = h
	}
	rty := indexReadType(v.Ty)
	if isNullableScalar(rty) && !isNullableScalar(v.Ty) {
		return Value{Ref: e.makeNullableScalarAgg(rty, inb, v.Ref), Ty: rty}, nil
	}
	v.Ty = rty
	return v, nil
}

// emitIndexBase evaluates the array and index operands of `a[i]`, returning the
// backing-buffer pointer, the length, the index as an i64 and the element type.
func (e *Emitter) emitIndexBase(ex *ast.IndexExpression) (dataPtrReg, lenReg, idxRef string, elemTy Type, err error) {
	if id, ok := ex.Object.(*ast.Identifier); ok && !e.isDynamicBinding(id.Name) {
		sym, ok := e.lookup(id.Name)
		if !ok {
			return "", "", "", TypeVoid, fmt.Errorf("%d:%d: undefined variable '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name)
		}
		if !sym.Ty.IsArray && !sym.Ty.IsFlatArray {
			return "", "", "", TypeVoid, fmt.Errorf("%d:%d: '%s' is not an array", ex.GetPos().Line, ex.GetPos().Col, id.Name)
		}
		elemTy = *sym.Ty.ElemType
		if sym.Ty.IsFlatArray {
			// Flat value-type array (TDD-00134 Stage 2): elements are inline
			// structs — the Inline marker makes the final GEP below stride by
			// StructSize and tells loadArrayElem/storeArrayElem the slot IS
			// the value.
			elemTy = flatElemView(elemTy)
		}
		if !ex.Optional {
			key := ""
			if lit, ok := ex.Index.(*ast.NumberLiteral); ok {
				key = " (reading '" + lit.Value + "')"
			}
			e.emitArrayBindingGuard(sym, "Cannot read properties of undefined"+key)
		}
		dataSlot, lenSlot := e.arrayDataLenSlots(sym)
		dataPtrReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtrReg, dataSlot))
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
	} else {
		// Expression producing a {ptr, i64} aggregate (e.g. arr.slice(1), Object.keys(obj)).
		arrVal, evalErr := e.emitExpr(ex.Object)
		if evalErr != nil {
			return "", "", "", TypeVoid, evalErr
		}
		if !arrVal.Ty.IsArray || arrVal.Ty.ElemType == nil {
			return "", "", "", TypeVoid, fmt.Errorf("%d:%d: cannot index a non-array expression", ex.GetPos().Line, ex.GetPos().Col)
		}
		elemTy = *arrVal.Ty.ElemType
		dataPtrReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtrReg, arrVal.Ref))
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, arrVal.Ref))
	}

	idxVal, err := e.emitExpr(ex.Index)
	if err != nil {
		return "", "", "", TypeVoid, err
	}
	idxVal, err = e.arrayIndexToI64(idxVal, ex.Index.GetPos())
	if err != nil {
		return "", "", "", TypeVoid, err
	}

	return dataPtrReg, lenReg, idxVal.Ref, elemTy, nil
}

// emitTupleElemAssign implements `t[i] = val` for a constant i (TDD-00066): GEP
// the matching struct field and store, reusing the object-field store path (so
// scalar, nullable, and array element types all work). Only plain `=` for V1.
func (e *Emitter) emitTupleElemAssign(ex *ast.IndexExpression, tupleTy Type, op string, rhs ast.Expression) (Value, error) {
	idx, ok := tupleConstIndex(ex.Index)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: a tuple can only be indexed by a constant integer literal", ex.GetPos().Line, ex.GetPos().Col)
	}
	if idx < 0 || idx >= int64(len(tupleTy.Fields)) {
		return Value{}, fmt.Errorf("%d:%d: tuple index %d is out of range (the tuple has %d element(s))", ex.GetPos().Line, ex.GetPos().Col, idx, len(tupleTy.Fields))
	}
	if op != "=" {
		return Value{}, fmt.Errorf("%d:%d: compound assignment to a tuple element is not yet supported", ex.GetPos().Line, ex.GetPos().Col)
	}
	fieldTy := tupleTy.Fields[idx].Ty
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, tupleTy.StructIR(), objVal.Ref, idx))
	if err := e.storeScalarOrNullableFieldExpr(gepReg, fieldTy, rhs); err != nil {
		return Value{}, err
	}
	return e.loadScalarOrNullableField(gepReg, fieldTy), nil
}

// constObjectKey returns the field name a compile-time-constant bracket key
// denotes for a fixed-shape object (`o["a"]` → "a", `o[0]` → "0"), and whether
// the index is such a constant (ADR-00608). An integer numeric literal matches
// the field-name text the object literal stored for a numeric key; a bigint or
// non-integer literal is not a valid object key here.
func constObjectKey(index ast.Expression) (string, bool) {
	switch k := index.(type) {
	case *ast.StringLiteral:
		return k.Value, true
	case *ast.NumberLiteral:
		if k.IsBigInt || strings.ContainsAny(k.Value, ".eExX") {
			return "", false
		}
		return k.Value, true
	}
	return "", false
}

// optionalIndexGuards reports whether `a?.[k]` needs a run-time null guard: only
// a pointer receiver can be nullish. An array is a value aggregate that is never
// a null pointer (its absent form is the empty {null,0}, whose elements read
// `undefined` anyway), and a scalar cannot be null — both use plain access,
// exactly as `a?.x` does (emitOptionalMember).
func optionalIndexGuards(objTy Type) bool {
	return objTy.IR == "ptr" && !objTy.IsArray
}

// emitOptionalIndex implements `a?.[k]`: the receiver is evaluated once; when it
// is null/undefined the access short-circuits to `undefined` without evaluating
// `k`, otherwise the ordinary element access runs on the bound receiver.
func (e *Emitter) emitOptionalIndex(ex *ast.IndexExpression) (Value, error) {
	plain := *ex
	plain.Optional = false
	if !optionalIndexGuards(e.inferExprType(ex.Object)) {
		return e.emitIndexUnguarded(&plain)
	}
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	if !optionalIndexGuards(objVal.Ty) {
		return e.emitIndexUnguarded(&plain)
	}
	e.optionalCallCtr++
	recvName := fmt.Sprintf("__optc_idx_%d", e.optionalCallCtr)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objVal.Ref, slot))
	recvTy := objVal.Ty
	recvTy.Nullable = false
	recvTy.IsUndefined = false
	e.define(recvName, Symbol{Ptr: slot, Ty: recvTy})
	through := ast.NewIndexExpression(ast.NewIdentifier(recvName, ex.Object.GetPos()), ex.Index, ex.GetPos())
	return e.emitNullGuardedExpr(e.isAbsentPtr(objVal.Ref, objVal.Ty), through)
}

func (e *Emitter) emitIndex(ex *ast.IndexExpression) (Value, error) {
	if ex.Optional {
		return e.emitOptionalIndex(ex)
	}
	// An element read off a run-time undefined/null base throws (emit_nullderef.go).
	undo, err := e.guardIndexBase(ex.Object, ex.Index, false)
	if err != nil {
		return Value{}, err
	}
	defer undo()
	return e.emitIndexUnguarded(ex)
}

// emitIndexUnguarded is emitIndex without the absent-base TypeError (see
// emitMemberUnguarded).
func (e *Emitter) emitIndexUnguarded(ex *ast.IndexExpression) (Value, error) {
	// Enum bracket access (ADR-00480): `E["B"]` with a literal string key is
	// the member's value; `E[0]` / `E[expr]` with a numeric key is the
	// *reverse* mapping (value → member name string), resolved at compile
	// time for a literal and via a chain of compares for a runtime value.
	if id, ok := ex.Object.(*ast.Identifier); ok && !e.isShadowedByLocal(id.Name) {
		if members, found := e.enums[id.Name]; found {
			if sl, ok := ex.Index.(*ast.StringLiteral); ok {
				if val, ok := members[sl.Value]; ok {
					return val, nil
				}
				return Value{}, fmt.Errorf("%d:%d: no member '%s' in enum '%s'", ex.GetPos().Line, ex.GetPos().Col, sl.Value, id.Name)
			}
			// Reverse mapping is numeric-enum only (a string enum's values
			// aren't i64 comparands) — a string enum's numeric index stays a
			// clean rejection.
			allNumeric := true
			names := make([]string, 0, len(members))
			for name, mv := range members {
				names = append(names, name)
				if mv.Ty.IR != "i64" {
					allNumeric = false
				}
			}
			if !allNumeric {
				return Value{}, fmt.Errorf("%d:%d: a numeric reverse lookup is only supported on a numeric enum", ex.GetPos().Line, ex.GetPos().Col)
			}
			sort.Strings(names)
			idxVal, err := e.emitExpr(ex.Index)
			if err != nil {
				return Value{}, err
			}
			idxVal = e.coerce(idxVal, TypeI64)
			// Reverse mapping: compare against each member's value; an
			// unmatched value yields "undefined" (JS: E[99] === undefined).
			result := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", result))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("undefined"), result))
			doneL := e.freshLabel("enumrev.done")
			for _, name := range names {
				mv := members[name]
				matchL := e.freshLabel("enumrev.hit")
				nextL := e.freshLabel("enumrev.next")
				cmp := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", cmp, idxVal.Ref, mv.Ref))
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cmp, matchL, nextL))
				e.emitLabel(matchL)
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(name), result))
				e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
				e.emitLabel(nextL)
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(doneL)
			out := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, result))
			return Value{Ref: out, Ty: TypePtr}, nil
		}
	}
	// process.env[key] (lib/node/internal_process_methods.ts).
	if r, ok := e.processEnvIndexRewrite(ex); ok {
		return e.emitExpr(r)
	}
	// Group map access: grouped["key"] → sub-array.
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsGroupMap {
			return e.emitGroupMapIndex(sym, ex.Index, ex.GetPos())
		}
	}
	// String-keyed Map bracket access: map[key] reads like Node's plain-object
	// header records (`headers[':path']`, `req.headers['host']`) — sugar for
	// .get(key), yielding the value string or null when absent (TDD-00139
	// Stage 2 surfaced it; generally useful wherever a Map models an object).
	if objTy := e.inferExprType(ex.Object); objTy.IsMap && objTy.MapKey != nil && isPlainStringType(*objTy.MapKey) {
		objVal, err := e.emitExpr(ex.Object)
		if err != nil {
			return Value{}, err
		}
		keyVal, err := e.emitExpr(ex.Index)
		if err != nil {
			return Value{}, err
		}
		if keyVal.Ty.IR != "ptr" {
			// A numeric key stringifies (JS object keys are strings) —
			// what a number index signature reads through (ADR-00461).
			if keyVal.Ty.IR == "double" || keyVal.Ty.IR == "i64" || keyVal.Ty.IR == "i32" || keyVal.Ty.IR == "i16" || keyVal.Ty.IR == "i8" {
				keyVal, err = e.emitValueToString(keyVal)
				if err != nil {
					return Value{}, err
				}
			} else {
				return Value{}, fmt.Errorf("%d:%d: a Map<string, …> bracket index must be a string", ex.GetPos().Line, ex.GetPos().Col)
			}
		}
		e.ensureMapStrHelpers()
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, objVal.Ref, keyVal.Ref))
		valTy := TypePtr
		if objTy.MapVal != nil {
			valTy = *objTy.MapVal
		}
		if valTy.IsArray {
			// The slot holds the array's shared header (null on a miss).
			v := e.mapValFromI64(raw, valTy)
			v.Ty.Nullable = true
			if ft, ok := dictFieldType(objTy, ex.Index); ok {
				return e.coerce(v, ft), nil
			}
			return v, nil
		}
		if valTy.IR == "ptr" {
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, raw))
			if ft, ok := dictFieldType(objTy, ex.Index); ok {
				return e.coerce(Value{Ref: p, Ty: valTy}, ft), nil
			}
			if dictMissReadsUndefined(valTy) {
				// A missing key is undefined: the null pointer, typed so.
				return Value{Ref: p, Ty: indexReadType(valTy)}, nil
			}
			return Value{Ref: p, Ty: valTy}, nil
		}
		if valTy.IR == "double" {
			// Number values are stored as the double's BIT PATTERN in the
			// i64 slot — reinterpret, never numerically convert.
			d := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", d, raw))
			if ft, ok := dictFieldType(objTy, ex.Index); ok {
				return e.coerce(Value{Ref: d, Ty: valTy}, ft), nil
			}
			if dictMissReadsUndefined(valTy) {
				// A missing key is undefined: { present, value }.
				has := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", has, objVal.Ref, keyVal.Ref))
				nt := indexReadType(valTy)
				return Value{Ref: e.makeNullableScalarAgg(nt, has, d), Ty: nt}, nil
			}
			return Value{Ref: d, Ty: valTy}, nil
		}
		if valTy.IsDynamic {
			// The slot holds the box itself; a missing key is undefined.
			has := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", has, objVal.Ref, keyVal.Ref))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", sel, has, raw, nbUndefined))
			v := Value{Ref: sel, Ty: valTy}
			if ft, ok := dictFieldType(objTy, ex.Index); ok {
				return e.coerce(v, ft), nil
			}
			return v, nil
		}
		out := e.coerce(Value{Ref: raw, Ty: TypeI64}, valTy)
		return out, nil
	}
	// Dynamic object bracket access: obj[key] — a computed-key object literal
	// is a real Map<string,V> under the hood, see docs/tdd/TDD-00012.md. Must
	// run before the generic string-indexing check below, since a dynamic
	// object's Ty is ptr-shaped and isStringTy's ptr-catch-all would
	// otherwise misclassify it as a string (mirrors GroupMap's own ordering).
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsDynamicObject {
			mapPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, sym.Ptr))
			return e.emitDynamicObjectGet(sym.Ty, mapPtr, ex.Index, ex.GetPos())
		}
	} else if objTy := e.inferExprType(ex.Object); objTy.IsDynamicObject {
		objVal, err := e.emitExpr(ex.Object)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynamicObjectGet(objVal.Ty, objVal.Ref, ex.Index, ex.GetPos())
	}
	// Bracket read on a bare any/unknown base: runtime-keyed read from the D1
	// dynamic object model (TDD-00155 Stage 1).
	if baseTy := e.inferExprType(ex.Object); isUnconstrainedDynamic(baseTy) {
		objVal, err := e.emitExpr(ex.Object)
		if err != nil {
			return Value{}, err
		}
		keyRef, err := e.dynAnyKeyRef(ex.Index, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		v, err := e.emitDynAnyMemberGet(objVal, keyRef, ex.GetPos())
		if err != nil || baseTy.DynPropTy == nil {
			return v, err
		}
		return e.coerce(v, *baseTy.DynPropTy), nil
	}
	// Bracket read of a function's own property (TDD-00229): its boxed
	// function object's bag.
	if baseTy := e.inferExprType(ex.Object); baseTy.IsFunc && !baseTy.IsDynamic {
		objVal, err := e.emitExpr(ex.Object)
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitBoxValue(objVal)
		if err != nil {
			return Value{}, err
		}
		keyRef, err := e.dynAnyKeyRef(ex.Index, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		v, err := e.emitDynAnyMemberGet(boxed, keyRef, ex.GetPos())
		if err != nil {
			return v, err
		}
		if pt, ok := e.checkerPrimitive(ex); ok {
			return e.coerce(v, pt), nil
		}
		return v, nil
	}
	// String indexing: s[i] returns a single-character string.
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && isStringTy(sym.Ty) {
			strPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", strPtr, sym.Ptr))
			return e.emitStringCharAt(strPtr, ex.Index)
		}
	}
	// Any other string-typed base (`s![i]`, `o.name[i]`, `f()[i]`) indexes
	// the same way.
	if _, isID := ex.Object.(*ast.Identifier); !isID {
		if objTy := e.inferExprType(ex.Object); isPlainStringType(objTy) {
			strVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitStringCharAt(strVal.Ref, ex.Index)
		}
	}
	// Tuple constant-index access: t[0] -> field "0" (TDD-00066). A tuple is a
	// struct with no array backing buffer, so a compile-time-constant index
	// maps to the matching field; checked before array indexing since a tuple's
	// Ty is ptr-shaped and would otherwise fall into emitIndexPtr.
	if objTy := e.inferExprType(ex.Object); objTy.IsTuple {
		return e.emitTupleIndex(ex, objTy)
	}
	// Fixed-object constant-key access: `o["a"]` / `o[0]` on a static-shape
	// object (or class instance) maps a compile-time-constant key to the matching
	// field, exactly like `o.a` (ADR-00608). A dynamic (map-backed) object was
	// already handled above; arrays/tuples/strings are not IsObject here.
	if objTy := e.inferExprType(ex.Object); objTy.IsObject && !objTy.IsArray {
		if key, ok := constObjectKey(ex.Index); ok {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			if objVal.Ty.IsClass {
				if getter, _, ok := e.classAccessorSigs(objVal.Ty.ClassName, key); ok && getter != nil {
					return e.emitClassCall(objVal.Ty, objVal, accessorMethodName("get", key), nil, ex.GetPos(), false)
				}
			}
			idx, fieldTy, found := objVal.Ty.FieldIndex(key)
			if !found {
				return Value{}, fmt.Errorf("%d:%d: object has no field '%s'", ex.GetPos().Line, ex.GetPos().Col, key)
			}
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
			if fieldTy.IsArray {
				fv := e.loadArrayFieldValue(gepReg, fieldTy) // header-pointer slot (TDD-00213 S2)
				fv.Ty = e.canonicalizeClassTy(fieldTy)
				return fv, nil
			}
			loadReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
			return Value{Ref: loadReg, Ty: e.canonicalizeClassTy(fieldTy)}, nil
		}
	}
	// Array indexing.
	return e.emitIndexRead(ex)
}

// unwrapGlobalThis rewrites a `globalThis.X` member chain into a bare
// reference to X, so the ambient global is reached through the same dispatch as
// writing X directly. `globalThis` is the standard alias for the global object;
// this native single-file model has no dynamic global record, so only *known*
// globals resolve — an unknown `globalThis.foo` falls through to the same
// "unknown identifier" error a bare `foo` would give, never a runtime lookup.
// Recurses, so `globalThis.JSON.stringify(...)` and `globalThis.setTimeout(...)`
// both desugar (the leading `globalThis.` peels off, the rest dispatches
// normally). Computed access (`globalThis["x"]`, an IndexExpression) and a bare
// `globalThis` used as a standalone object value are not covered. A pathological
// local shadow of `globalThis` is respected — the rewrite is skipped then.
func (e *Emitter) unwrapGlobalThis(expr ast.Expression) ast.Expression {
	mem, ok := expr.(*ast.MemberExpression)
	if !ok {
		return expr
	}
	newObj := e.unwrapGlobalThis(mem.Object)
	// `parentPort!.postMessage(v)` (ADR-01058): a non-null assertion on an
	// ambient/virtual binding — one that is not a declared local, so its
	// meaning lives in the by-name dispatch tables (`parentPort`, `process`,
	// `self`, …) — is erased here, the way `globalThis.` is peeled. The
	// identifier itself has no local type to strip nullability from; left in
	// place it inferred as the bare-scalar default and `.postMessage` became
	// "a number has no method". A `!` on a declared local keeps its own path.
	if nn, ok := newObj.(*ast.NonNullExpression); ok {
		if id, isID := nn.Arg.(*ast.Identifier); isID && !e.isShadowedByLocal(id.Name) {
			newObj = id
		}
	}
	if id, ok := newObj.(*ast.Identifier); ok && id.Name == "globalThis" && !e.isShadowedByLocal("globalThis") {
		return ast.NewIdentifier(mem.Property, mem.GetPos())
	}
	if newObj == mem.Object {
		return expr
	}
	m := ast.NewMemberExpression(newObj, mem.Property, mem.GetPos())
	m.Optional = mem.Optional
	return m
}

func (e *Emitter) emitMember(ex *ast.MemberExpression) (Value, error) {
	if id, ok := ex.Object.(*ast.Identifier); ok && !ex.Optional {
		if target, ok := e.identClassAlias(id); ok {
			// `K.x` after `const K = C` reads C's static x.
			ex = ast.NewMemberExpression(ast.NewIdentifier(target, id.GetPos()), ex.Property, ex.GetPos())
		}
	}
	v, err := e.emitMemberRaw(ex)
	if err != nil || !v.Ty.IsDynamic || len(v.Ty.UnionMembers) == 0 {
		return v, err
	}
	// A union-typed property read the checker narrows reads as that member.
	if nt, ok := e.checkerNarrowedUnion(ex, v.Ty); ok {
		return e.coerce(v, nt), nil
	}
	return v, nil
}

// processStdioGetter is the stdio module's getter (TDD-00235) that
// `process.stdin`, `process.stdout` or `process.stderr` reads, when ex is one.
func (e *Emitter) processStdioGetter(ex *ast.MemberExpression) (string, bool) {
	id, ok := ex.Object.(*ast.Identifier)
	if !ok || id.Name != "process" || e.isShadowedByLocal(id.Name) || ex.Optional {
		return "", false
	}
	var name string
	switch ex.Property {
	case "stdin":
		name = "_kmlStdin"
	case "stdout":
		name = "_kmlStdout"
	case "stderr":
		name = "_kmlStderr"
	default:
		return "", false
	}
	m, ok := e.libExports["internal_process_stdio:"+name]
	return m, ok
}

// processEmitterMembers are the members of `process` its emitter object
// holds (lib/node/internal_process.ts): EventEmitter's, and the fork
// channel's.
var processEmitterMembers = map[string]bool{
	"on": true, "once": true, "off": true, "addListener": true, "removeListener": true,
	"prependListener": true, "prependOnceListener": true, "emit": true, "listenerCount": true,
	"listeners": true, "rawListeners": true, "removeAllListeners": true, "setMaxListeners": true,
	"getMaxListeners": true, "eventNames": true,
	"send": true, "disconnect": true, "connected": true, "channel": true,
}

// processMethodExports are the methods of `process` a TypeScript module
// implements (lib/node/internal_process_methods.ts, internal_process_env.ts).
var processMethodExports = map[string]string{
	"cwd": "internal_process_methods:cwd", "chdir": "internal_process_methods:chdir",
	"uptime": "internal_process_methods:uptime", "hrtime": "internal_process_hrtime:hrtime",
	"kill": "internal_process_methods:kill", "memoryUsage": "internal_process_methods:memoryUsage",
	"umask": "internal_process_methods:umask", "exit": "internal_process_env:exit",
	"getuid": "internal_process_methods:getuid", "geteuid": "internal_process_methods:geteuid",
	"getgid": "internal_process_methods:getgid", "getegid": "internal_process_methods:getegid",
}

// processPropertyExports are the properties of `process` a TypeScript
// module reads through a function of the same name.
var processPropertyExports = map[string]string{
	"pid": "internal_process_methods:pid", "ppid": "internal_process_methods:ppid",
	"argv": "internal_process_env:argv", "argv0": "internal_process_env:argv0",
	"execPath": "internal_process_env:execPath", "version": "internal_process_env:version",
	"versions": "internal_process_env:versions", "execArgv": "internal_process_env:execArgv",
}

// processEnvCall is a call to one of internal_process_env.ts's functions,
// when the program has the module.
func (e *Emitter) processEnvCall(name string, pos ast.Pos, args ...ast.Expression) (ast.Expression, bool) {
	m, ok := e.libExports["internal_process_env:"+name]
	if !ok {
		return nil, false
	}
	return ast.NewCallExpression(ast.NewIdentifier(m, pos), args, pos), true
}

// processEnvIndexRewrite is `process.env[key]` read: envGet(key).
func (e *Emitter) processEnvIndexRewrite(ix *ast.IndexExpression) (ast.Expression, bool) {
	if ix.Optional || !e.isProcessEnvExpr(ix.Object) {
		return nil, false
	}
	return e.processEnvCall("envGet", ix.GetPos(), ix.Index)
}

// processCredentialReads are the methods Node defines only on POSIX.
var processCredentialReads = map[string]bool{"getuid": true, "geteuid": true, "getgid": true, "getegid": true}

// processEmitterValue is the call that makes (or returns) the process
// emitter object, when the program has its module.
func (e *Emitter) processEmitterValue(pos ast.Pos) (ast.Expression, bool) {
	m, ok := e.libExports["internal_process:_kmlProcess"]
	if !ok {
		return nil, false
	}
	return ast.NewCallExpression(ast.NewIdentifier(m, pos), nil, pos), true
}

// processIsValue reports whether id is `process` read as a value: its
// emitter object (lib/node/internal_process.ts).
func (e *Emitter) processIsValue(id *ast.Identifier) bool {
	if id.Name != "process" || e.isShadowedByLocal(id.Name) {
		return false
	}
	_, ok := e.processEmitterValue(id.GetPos())
	return ok
}

// processMemberRewrite is what `process.<name>` reads when a TypeScript
// module implements that member: the emitter object's member, or
// emitWarning's function (lib/node/internal_process_warning.ts).
func (e *Emitter) processMemberRewrite(ex *ast.MemberExpression) (ast.Expression, bool) {
	// process.env.KEY: envGet("KEY").
	if e.isProcessEnvExpr(ex.Object) {
		return e.processEnvCall("envGet", ex.GetPos(), ast.NewStringLiteral(ex.Property, ex.GetPos()))
	}
	// process.hrtime.bigint, process.memoryUsage.rss.
	if inner, ok := ex.Object.(*ast.MemberExpression); ok && !inner.Optional {
		if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
			name := ""
			switch {
			case inner.Property == "hrtime" && ex.Property == "bigint":
				name = "hrtimeBigint"
			case inner.Property == "memoryUsage" && ex.Property == "rss":
				name = "memoryUsageRss"
			}
			mod := "internal_process_methods:"
			if name == "hrtimeBigint" {
				mod = "internal_process_hrtime:"
			}
			if m, ok := e.libExports[mod+name]; ok && name != "" {
				return ast.NewIdentifier(m, ex.GetPos()), true
			}
		}
	}
	id, ok := ex.Object.(*ast.Identifier)
	if !ok || id.Name != "process" || e.isShadowedByLocal(id.Name) {
		return nil, false
	}
	if key, ok := processPropertyExports[ex.Property]; ok {
		m, ok := e.libExports[key]
		if !ok {
			return nil, false
		}
		return ast.NewCallExpression(ast.NewIdentifier(m, ex.GetPos()), nil, ex.GetPos()), true
	}
	if ex.Property == "exitCode" {
		return e.processEnvCall("getExitCode", ex.GetPos())
	}
	if name, ok := processMethodExports[ex.Property]; ok {
		if e.opts.Target.OS() == "windows" && processCredentialReads[ex.Property] {
			// Node defines no credential reads on Windows.
			return nil, false
		}
		m, ok := e.libExports[name]
		if !ok {
			return nil, false
		}
		return ast.NewIdentifier(m, ex.GetPos()), true
	}
	if ex.Property == "emitWarning" {
		m, ok := e.libExports["internal_process_warning:emitWarning"]
		if !ok {
			return nil, false
		}
		return ast.NewIdentifier(m, ex.GetPos()), true
	}
	if !processEmitterMembers[ex.Property] {
		return nil, false
	}
	obj, ok := e.processEmitterValue(ex.GetPos())
	if !ok {
		return nil, false
	}
	m := ast.NewMemberExpression(obj, ex.Property, ex.GetPos())
	m.Optional = ex.Optional
	return m, true
}

func (e *Emitter) emitMemberRaw(ex *ast.MemberExpression) (Value, error) {
	// process.stdin/stdout/stderr: the stream, made on first use
	// (lib/node/internal_process_stdio.ts).
	if getter, ok := e.processStdioGetter(ex); ok {
		return e.emitCall(ast.NewCallExpression(ast.NewIdentifier(getter, ex.GetPos()), nil, ex.GetPos()))
	}
	if r, ok := e.processMemberRewrite(ex); ok {
		return e.emitExpr(r)
	}
	if unwrapped := e.unwrapGlobalThis(ex); unwrapped != ast.Expression(ex) {
		return e.emitExpr(unwrapped)
	}
	if ex.Optional {
		return e.emitOptionalMember(ex)
	}
	// A read off a run-time undefined/null base throws (emit_nullderef.go).
	undo, err := e.guardBase(ex.Object, ex.Property, false)
	if err != nil {
		return Value{}, err
	}
	defer undo()
	return e.emitMemberUnguarded(ex)
}

// emitMemberUnguarded is emitMember without the absent-base TypeError — the
// access `?.` falls back to for a base it has already established the shape of.
func (e *Emitter) emitMemberUnguarded(ex *ast.MemberExpression) (Value, error) {
	if v, ok, err := e.emitExecArrayMember(ex); ok || err != nil {
		return v, err
	}
	// A namespace-qualified type-member chain (`X.Color.Red`,
	// `X.C.staticField` — ADR-00480): drop the namespace qualifier up
	// front — a pure AST rewrite — so every dispatch below sees the bare
	// desugared name.
	if bare := e.stripNSTypeQualifier(ex.Object); bare != nil {
		return e.emitMember(&ast.MemberExpression{Object: bare, Property: ex.Property})
	}
	// `Object.prototype`: the shared null-prototype bag every ordinary
	// object's [[Prototype]] reads as (TDD-00229).
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "Object" && ex.Property == "prototype" && !e.isShadowedByLocal("Object") {
		e.ensureDynObj()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_object_prototype()", r))
		return e.emitDynObjBox(r), nil
	}
	// A function value's `name` / `length` (TDD-00229): read from the
	// code-pointer metadata table.
	if ex.Property == "name" || ex.Property == "length" {
		if ot := e.inferExprType(ex.Object); ot.IsFunc {
			fv, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			e.ensureFnMeta()
			r := e.freshReg()
			if ex.Property == "name" {
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_name_hdr(ptr %s)", r, fv.Ref))
				return Value{Ref: r, Ty: TypePtr}, nil
			}
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_fn_length_hdr(ptr %s)", r, fv.Ref))
			return e.countToNumber(Value{Ref: r, Ty: TypeI64}), nil
		}
	}
	// DataView properties (byteLength/byteOffset/buffer) — dedicated reads
	// over the hidden header struct, same pattern ArrayBuffer's .byteLength
	// uses below.
	if ex.Property == "stdout" || ex.Property == "stderr" || ex.Property == "stdin" || ex.Property == "pid" {
		if objTy := e.inferExprType(ex.Object); objTy.IsChildProcess {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitChildProcessMember(objVal, ex.Property, ex.GetPos())
		}
	}
	// TextEncoder/TextDecoder `.encoding` — always "utf-8" (the only encoding
	// this compiler's byte-string model supports; a non-UTF-8 TextDecoder label
	// is rejected at construction, ADR-00567). The receiver is stateless, so the
	// value is a constant; evaluate the object for its side effects only.
	if ex.Property == "encoding" {
		if objTy := e.inferExprType(ex.Object); objTy.IsTextEncoder || objTy.IsTextDecoder {
			if _, err := e.emitExpr(ex.Object); err != nil {
				return Value{}, err
			}
			return Value{Ref: e.internString("utf-8"), Ty: TypePtr}, nil
		}
	}
	if ex.Property == "byteLength" || ex.Property == "byteOffset" || ex.Property == "buffer" {
		if objTy := e.inferExprType(ex.Object); objTy.IsDataView {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitDataViewProp(objVal, ex.Property, ex.GetPos())
		}
	}
	// Blob properties (size/type, TDD-00102) — same dedicated-read pattern.
	if ex.Property == "size" || ex.Property == "type" {
		if objTy := e.inferExprType(ex.Object); objTy.IsBlob {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitBlobProp(objVal, ex.Property, ex.GetPos())
		}
	}
	// CryptoKeyPair properties (publicKey/privateKey, TDD-00104).
	if ex.Property == "publicKey" || ex.Property == "privateKey" {
		if objTy := e.inferExprType(ex.Object); objTy.IsCryptoKeyPair {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitCryptoKeyPairProp(objVal, ex.Property)
		}
	}
	// CryptoKey properties (type/extractable, TDD-00104) — same pattern.
	if ex.Property == "type" || ex.Property == "extractable" {
		if objTy := e.inferExprType(ex.Object); objTy.IsCryptoKey {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitCryptoKeyProp(objVal, ex.Property)
		}
	}
	// TS namespace member in value position (`X.member`, TDD-00095):
	// resolve through the desugared flat declaration. A local binding
	// shadowing the namespace name wins.
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if members, nsName := e.namespaceMembers(id.Name); members != nil {
			if exported, present := members[ex.Property]; present && !e.isShadowedByLocal(id.Name) {
				if !exported && e.curNamespace != nsName {
					return Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name, ex.Property, nsName)
				}
				return e.emitIdent(ast.NewIdentifier(ast.NamespaceMangle(nsName, ex.Property), ex.GetPos()))
			}
		}
	}
	// Nested-namespace member in value position `A.B.member` (TDD-00148 V3).
	if members, nsName := e.namespaceByChain(ex.Object); members != nil {
		if exported, present := members[ex.Property]; present {
			if !exported && e.curNamespace != nsName {
				return Value{}, fmt.Errorf("%d:%d: '%s.%s' is not exported from namespace '%s'", ex.GetPos().Line, ex.GetPos().Col, nsName, ex.Property, nsName)
			}
			return e.emitIdent(ast.NewIdentifier(ast.NamespaceMangle(nsName, ex.Property), ex.GetPos()))
		}
	}
	// A well-known symbol (`Symbol.iterator`, `Symbol.asyncIterator`, …).
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "Symbol" && !e.isShadowedByLocal(id.Name) && wellKnownSymbols[ex.Property] {
		return Value{Ref: e.wellKnownSymbol(ex.Property), Ty: SymbolType()}, nil
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "Number" && !e.isShadowedByLocal(id.Name) {
		switch ex.Property {
		case "MAX_SAFE_INTEGER":
			return Value{Ref: "9007199254740991", Ty: TypeI64}, nil
		case "MIN_SAFE_INTEGER":
			return Value{Ref: "-9007199254740991", Ty: TypeI64}, nil
		case "EPSILON":
			return Value{Ref: "2.220446049250313e-16", Ty: TypeF64}, nil
		case "MAX_VALUE":
			return Value{Ref: "1.7976931348623157e+308", Ty: TypeF64}, nil
		case "MIN_VALUE":
			return Value{Ref: "5.0e-324", Ty: TypeF64}, nil
		case "POSITIVE_INFINITY":
			return Value{Ref: "0x7FF0000000000000", Ty: TypeF64}, nil
		case "NEGATIVE_INFINITY":
			return Value{Ref: "0xFFF0000000000000", Ty: TypeF64}, nil
		case "NaN":
			return Value{Ref: "0x7FF8000000000000", Ty: TypeF64}, nil
		}
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "Math" && !e.isShadowedByLocal(id.Name) {
		switch ex.Property {
		case "PI":
			return Value{Ref: "3.141592653589793e+00", Ty: TypeF64}, nil
		case "E":
			return Value{Ref: "2.718281828459045e+00", Ty: TypeF64}, nil
		case "LN2":
			return Value{Ref: "6.931471805599453e-01", Ty: TypeF64}, nil
		case "LN10":
			return Value{Ref: "2.302585092994046e+00", Ty: TypeF64}, nil
		case "SQRT2":
			return Value{Ref: "1.4142135623730951e+00", Ty: TypeF64}, nil
		case "LOG2E":
			return Value{Ref: "1.4426950408889634e+00", Ty: TypeF64}, nil
		case "LOG10E":
			return Value{Ref: "4.342944819032518e-01", Ty: TypeF64}, nil
		}
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
		// The rest of process is internal_process_methods.ts's
		// (processMemberRewrite); platform and arch are the target's,
		// compile-time constants.
		switch ex.Property {
		case "platform":
			return Value{Ref: e.internString(e.nodePlatformName()), Ty: TypePtr}, nil
		case "arch":
			return Value{Ref: e.internString(e.nodeArchName()), Ty: TypePtr}, nil
		}
	}
	// Bare `process.env` (not a keyed read): the enumerable environment object.
	if e.isProcessEnvExpr(ex) {
		return e.emitProcessEnvValue()
	}
	// HttpRequest.body under streaming dispatch (TDD-00097 Stage 5b):
	// Node's `req.url` (IncomingMessage, TDD-00131) is this request's path —
	// aliased onto the existing `path` field so the same request object serves
	// both the Node property name and the bespoke one.
	if ex.Property == "url" {
		if objTy := e.inferExprType(ex.Object); objTy.IsRequest {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			idx, fieldTy, _ := objVal.Ty.FieldIndex("path")
			return e.loadFieldValue(objVal, idx, fieldTy), nil
		}
	}
	// res.statusCode (ServerResponse, TDD-00131) reads the `status` field —
	// Node names the property `statusCode`, the object field is `status`.
	if ex.Property == "statusCode" {
		if objTy := e.inferExprType(ex.Object); objTy.IsServerResponse {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			idx, fieldTy, _ := objVal.Ty.FieldIndex("status")
			return e.loadFieldValue(objVal, idx, fieldTy), nil
		}
	}
	// complete the buffer in place before the plain field read below.
	if ex.Property == "body" {
		if objTy := e.inferExprType(ex.Object); objTy.IsRequest {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			e.emitRequestBodyDrain(objVal)
			bodyIdx, bodyFieldTy, _ := objVal.Ty.FieldIndex("body")
			return e.loadFieldValue(objVal, bodyIdx, bodyFieldTy), nil
		}
	}
	// Response.body as a ReadableStream<Uint8Array> (TDD-00097 Stage 4) —
	// dispatched ahead of the generic object-field read that would otherwise
	// surface the internal buffered-body string field.
	if ex.Property == "body" {
		if objTy := e.inferExprType(ex.Object); hasBodyMixin(objTy) {
			return e.emitResponseBodyStream(ex)
		}
	}
	if ex.Property == "bodyUsed" {
		if objTy := e.inferExprType(ex.Object); hasBodyMixin(objTy) {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return Value{Ref: e.emitResponseBodyUsed(objVal), Ty: TypeBool}, nil
		}
	}
	// Response.type: a constructed Response's own, else "basic".
	if ex.Property == "type" {
		if objTy := e.inferExprType(ex.Object); objTy.IsResponse {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			if idx, fty, ok := objVal.Ty.FieldIndex("__kml_type"); ok {
				own := e.loadFieldValue(objVal, idx, fty)
				has := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, own.Ref))
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, has, own.Ref, e.internString("basic")))
				return Value{Ref: r, Ty: TypePtr}, nil
			}
		}
	}
	// Response.statusText: a constructed Response's own, else the reason
	// phrase of a fetched one's status line.
	if ex.Property == "statusText" {
		if objTy := e.inferExprType(ex.Object); objTy.IsResponse {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			pendIdx, pendTy, ok1 := objVal.Ty.FieldIndex("__kml_pending")
			stIdx, stTy, ok2 := objVal.Ty.FieldIndex("__kml_status_text")
			if ok1 && ok2 {
				own := e.loadFieldValue(objVal, stIdx, stTy)
				hasOwn := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasOwn, own.Ref))
				ownL, fetchL, doneL := e.freshLabel("resp.st.own"), e.freshLabel("resp.st.fetch"), e.freshLabel("resp.st.done")
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasOwn, ownL, fetchL))
				e.emitLabel(ownL)
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", own.Ref, slot))
				e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
				e.emitLabel(fetchL)
				pend := e.loadFieldValue(objVal, pendIdx, pendTy)
				e.ensureFetchStatusText()
				st := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_status_text(ptr %s)", st, pend.Ref))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", st, slot))
				e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
				e.emitLabel(doneL)
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
				return Value{Ref: r, Ty: TypePtr}, nil
			}
		}
	}
	// Response.headers (ADR-00490): lazily parse the raw header text the
	// fetch runtime captured (CURLOPT_HEADERFUNCTION side buffer) into a
	// Map<string,string> with lowercased keys. Combinator-built Responses
	// have a null __kml_pending and yield an empty map.
	if ex.Property == "headers" {
		if objTy := e.inferExprType(ex.Object); objTy.IsResponse {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			pendIdx, pendTy, ok := objVal.Ty.FieldIndex("__kml_pending")
			hIdx, hTy, okH := objVal.Ty.FieldIndex("__kml_headers")
			if ok && okH {
				// A constructed Response keeps its own Headers.
				own := e.loadFieldValue(objVal, hIdx, hTy)
				pend := e.loadFieldValue(objVal, pendIdx, pendTy)
				e.ensureFetchHeadersMap()
				m := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_headers_map(ptr %s)", m, pend.Ref))
				hasOwn := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasOwn, own.Ref))
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, hasOwn, own.Ref, m))
				return Value{Ref: r, Ty: HeadersType()}, nil
			}
		}
	}
	// ReadableStream/reader/controller properties (TDD-00097 Stage 1) —
	// dedicated reads over the hidden %kml.rstream struct, same pattern
	// Map/Set's .size uses below.
	if ex.Property == "locked" || ex.Property == "desiredSize" || ex.Property == "closed" || ex.Property == "ready" {
		if objTy := e.inferExprType(ex.Object); objTy.IsReadableStream || objTy.IsStreamReader || objTy.IsRSController {
			return e.emitStreamProperty(ex, objTy)
		}
		if objTy := e.inferExprType(ex.Object); objTy.IsWritableStream || objTy.IsStreamWriter || objTy.IsWSController {
			return e.emitWStreamProperty(ex)
		}
	}
	if ex.Property == "readable" || ex.Property == "writable" {
		if objTy := e.inferExprType(ex.Object); objTy.IsTransformStream {
			return e.emitTransformStreamProperty(ex, objTy)
		}
	}
	if ex.Property == "size" {
		if objTy := e.inferExprType(ex.Object); objTy.IsURLSearchParams {
			// The ordered pair-list's count (TDD-00203) — a number, not the Map
			// header read below.
			return e.emitURLSearchParamsSize(ex.Object)
		}
		if id, ok := ex.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && (sym.Ty.IsMap || sym.Ty.IsSet) {
				mapPtr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, sym.Ptr))
				result := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, mapPtr))
				return Value{Ref: result, Ty: TypeI64}, nil
			}
		} else if objTy := e.inferExprType(ex.Object); objTy.IsMap || objTy.IsSet {
			// Not a named variable — a field access, array index, or call
			// result (e.g. `c.scores.size` where `scores: Map<K,V>`).
			// Evaluating it already yields the map/set's heap pointer
			// directly, no separate alloca indirection to unwrap first.
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			result := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, objVal.Ref))
			return Value{Ref: result, Ty: TypeI64}, nil
		}
	}
	// Growable-buffer properties (ADR-00494).
	if ex.Property == "growable" || ex.Property == "resizable" || ex.Property == "maxByteLength" {
		if objTy := e.inferExprType(ex.Object); objTy.IsArrayBuffer {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitBufferGrowableProps(objVal, ex.Property)
		}
	}
	if ex.Property == "byteLength" {
		// ArrayBuffer: read word 0 of its hidden header struct — same
		// named-variable-vs-arbitrary-expression split `.size` uses above.
		if id, ok := ex.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsArrayBuffer {
				bufPtr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bufPtr, sym.Ptr))
				return e.emitArrayBufferByteLength(Value{Ref: bufPtr, Ty: sym.Ty})
			}
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsTypedArray {
				_, lenSlot := e.arrayDataLenSlots(sym)
				lenReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
				bl, err := e.emitTypedArrayByteLength(lenReg, *sym.Ty.ElemType)
				if err != nil {
					return Value{}, err
				}
				return e.countToNumber(bl), nil
			}
		} else if objTy := e.inferExprType(ex.Object); objTy.IsArrayBuffer {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			bl, err := e.emitArrayBufferByteLength(objVal)
			if err != nil {
				return Value{}, err
			}
			return e.countToNumber(bl), nil
		} else if objTy.IsTypedArray {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			lenReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, objVal.Ref))
			bl, err := e.emitTypedArrayByteLength(lenReg, *objTy.ElemType)
			if err != nil {
				return Value{}, err
			}
			return e.countToNumber(bl), nil
		}
	}
	if ex.Property == "length" && !hasLengthField(e.inferExprType(ex.Object)) {
		// Named array variable: load length from its LenPtr alloca.
		if id, ok := ex.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found {
				// A tuple has a fixed, compile-time-known arity (TDD-00066).
				if sym.Ty.IsTuple {
					return e.countToNumber(Value{Ref: fmt.Sprintf("%d", len(sym.Ty.Fields)), Ty: TypeI64}), nil
				}
				if sym.Ty.IsArray || sym.Ty.IsFlatArray {
					if sym.Ty.IsArray && !sym.Ty.IsFlatArray {
						e.emitArrayBindingGuard(sym, "Cannot read properties of undefined (reading 'length')")
					}
					_, lenSlot := e.arrayDataLenSlots(sym)
					reg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", reg, lenSlot))
					return e.countToNumber(Value{Ref: reg, Ty: TypeI64}), nil
				}
			}
		}
		// Any other expression: evaluate it, then dispatch on the result type.
		objVal, err := e.emitExpr(ex.Object)
		if err != nil {
			return Value{}, err
		}
		// Tuple value (fixed arity).
		if objVal.Ty.IsTuple {
			return e.countToNumber(Value{Ref: fmt.Sprintf("%d", len(objVal.Ty.Fields)), Ty: TypeI64}), nil
		}
		// Array aggregate (e.g. from Object.keys(), arr.slice(), call result): extract field 1.
		if objVal.Ty.IsArray {
			reg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", reg, objVal.Ref))
			return e.countToNumber(Value{Ref: reg, Ty: TypeI64}), nil
		}
		// String: call strlen.
		if objVal.Ty.IR == "ptr" && !objVal.Ty.IsObject && !objVal.Ty.IsFunc {
			e.ensureStrlen()
			reg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", reg, objVal.Ref))
			return e.countToNumber(Value{Ref: reg, Ty: TypeI64}), nil
		}
		// A dynamic value answers `.length` at runtime — a tag-11 dynamic
		// array's element count via the same by-key path brackets use
		// (TDD-00155 Stage 2), a boxed string/primitive per the Stage-1 rules.
		if isUnconstrainedDynamic(objVal.Ty) {
			return e.emitDynAnyMemberGetNamed(objVal, e.internString("length"), "length", ex.GetPos())
		}
		// A union of strings and arrays (`string | string[]`): its box answers
		// at run time, a number.
		if objVal.Ty.IsDynamic && unionAllHaveLength(objVal.Ty) {
			n, err := e.emitDynAnyMemberGetNamed(Value{Ref: objVal.Ref, Ty: TypeAny}, e.internString("length"), "length", ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			return e.coerce(n, TypeF64), nil
		}
		return Value{}, fmt.Errorf("%d:%d: .length is only supported on arrays and strings", ex.GetPos().Line, ex.GetPos().Col)
	}
	// `F.prototype` on a recognized vanilla-JS constructor function
	// (TDD-00155 Stage 4, `-compat=js`): the boxed prototype bag.
	if id, ok := ex.Object.(*ast.Identifier); ok && e.compatJS() && e.jsProtoCtor[id.Name] && ex.Property == "prototype" {
		return e.emitProtoBagRead(id.Name), nil
	}
	// Static field read: ClassName.staticField (TDD-00009 Stage 4) — a bare
	// class-name identifier is a compile-time namespace, never a real
	// runtime value, so this must be checked before any attempt to
	// e.emitExpr(ex.Object) generically (same reasoning Math/JSON/enum
	// dispatch above already follows).
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if info, found := e.classes[id.Name]; found {
			return e.emitStaticFieldRead(info, id.Name, ex.Property, ex.GetPos())
		}
	}
	// Enum member access: EnumName.MemberName → compile-time constant.
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if members, found := e.enums[id.Name]; found {
			if val, ok := members[ex.Property]; ok {
				return val, nil
			}
			return Value{}, fmt.Errorf("%d:%d: no member '%s' in enum '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.Property, id.Name)
		}
	}

	// General object field read: evaluate the object expression then GEP into it.
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	if objVal.Ty.IsDynamicObject {
		keyExpr := ast.NewStringLiteral(ex.Property, ex.GetPos())
		return e.emitDynamicObjectGet(objVal.Ty, objVal.Ref, keyExpr, ex.GetPos())
	}
	// Discriminant read on an un-narrowed discriminated union (TDD-00116): the
	// only field readable before narrowing is the shared first-position tag. All
	// members hold it as their first field, right after the header word (a
	// string), so unbox the tag-6 pointer and load it. Every other field is
	// member-specific and needs narrowing first.
	if objVal.Ty.IsDynamic && len(objVal.Ty.UnionMembers) > 0 {
		if name, dTy, ok := unionDiscriminantField(objVal.Ty); ok && ex.Property == name {
			_, payload := e.emitUnboxTagPayload(objVal)
			objptr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", objptr, payload))
			tagp := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", tagp, objptr))
			val := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", val, tagp))
			return Value{Ref: val, Ty: dTy}, nil
		}
		if v, ok, err := e.emitUnionSoleObjectMemberRead(objVal, ex.Property, ex.GetPos()); ok || err != nil {
			return v, err
		}
		// A member every object member has (`x.name` on `A | B`): read by
		// name through the layout rows, as the member type all declare.
		if ft, ok := unionCommonMember(objVal.Ty, ex.Property); ok {
			v, err := e.emitDynAnyMemberGetNamed(Value{Ref: objVal.Ref, Ty: TypeAny}, e.internString(ex.Property), ex.Property, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			if ft.IsDynamic {
				return v, nil
			}
			return e.coerce(v, ft), nil
		}
		return Value{}, fmt.Errorf("%d:%d: '%s' can't be read on an un-narrowed union — narrow it first (e.g. `if (x.%s === ...)` or `typeof`)", ex.GetPos().Line, ex.GetPos().Col, ex.Property, ex.Property)
	}
	// A property read on a caught value (TypeCaught ≈ `unknown`, TDD-00202):
	// a caught Error yields its field; anything else goes through the dynamic
	// member path (primitives → undefined).
	if objVal.Ty.IsCaught {
		return e.emitCaughtMemberGet(objVal, ex.Property, ex.GetPos())
	}
	// A property read on a bare any/unknown value is a runtime tag dispatch
	// into the D1 dynamic object model (TDD-00155 Stage 1).
	if isUnconstrainedDynamic(objVal.Ty) {
		v, err := e.emitDynAnyMemberGetNamed(objVal, e.internString(ex.Property), ex.Property, ex.GetPos())
		if err != nil {
			return v, err
		}
		if objVal.Ty.DynPropTy == nil {
			// A property the checker knows to be a primitive reads as one.
			if pt, ok := e.checkerPrimitive(ex); ok {
				return e.coerce(v, pt), nil
			}
			return v, nil
		}
		// An index-signature view (DynPropTy): the read has the declared type.
		return e.coerce(v, *objVal.Ty.DynPropTy), nil
	}
	// A function's own property (`f.custom`, a TypeScript expando on a
	// function declaration): its boxed function object's bag (TDD-00229).
	if objVal.Ty.IsFunc && !objVal.Ty.IsDynamic {
		boxed, err := e.emitBoxValue(objVal)
		if err != nil {
			return Value{}, err
		}
		v, err := e.emitDynAnyMemberGetNamed(boxed, e.internString(ex.Property), ex.Property, ex.GetPos())
		if err != nil {
			return v, err
		}
		if pt, ok := e.checkerPrimitive(ex); ok {
			return e.coerce(v, pt), nil
		}
		return v, nil
	}
	if !objVal.Ty.IsObject {
		return Value{}, fmt.Errorf("%d:%d: field access on non-object (no field '%s')", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
	}
	// AggregateError.errors — the shared errorObjType has no `errors` field, so
	// this is intercepted before FieldIndex. Kind-guarded: only an actual
	// AggregateError carries the trailing errors array (TDD-00083).
	if objVal.Ty.IsError && ex.Property == "errors" {
		return e.emitErrorErrorsAccess(objVal.Ref), nil
	}
	// TDD-00030: a class accessor (getter/setter) is checked before the
	// plain-field FieldIndex path below — an accessor-only property name
	// is never a real Field, so FieldIndex would otherwise report "no
	// field" for it. Every non-accessor class, and every non-class object,
	// falls through unchanged.
	if objVal.Ty.IsClass {
		if getter, _, ok := e.classAccessorSigs(objVal.Ty.ClassName, ex.Property); ok {
			if getter == nil {
				return Value{}, fmt.Errorf("%d:%d: property '%s' has no getter", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
			}
			return e.emitClassCall(objVal.Ty, objVal, accessorMethodName("get", ex.Property), nil, ex.GetPos(), false)
		}
	}
	// A class or interface type captured before its fields were registered
	// (a type alias's function type naming it): its live shape.
	if len(objVal.Ty.UserFields()) == 0 {
		objVal.Ty = e.canonicalizeClassTy(objVal.Ty)
	}
	idx, fieldTy, ok := objVal.Ty.FieldIndex(ex.Property)
	if !ok {
		if objVal.Ty.IsClass {
			if v, ok := e.optionalMethodPresence(objVal, ex.Property); ok {
				return v, nil
			}
		}
		return Value{}, fmt.Errorf("%d:%d: no field '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
	}
	fieldTy = e.canonicalizeClassTy(fieldTy)
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
	// A structural type's value may have another layout (TDD-00233).
	if isRecordView(objVal.Ty) {
		gepReg, _ = e.emitRecordFieldSlot(objVal, gepReg, fieldTy, ex.Property, true)
	}
	// An array field slot holds a shared header pointer (TDD-00213 Stage 2) — deref
	// it into the {ptr,i64} aggregate, carrying the live header so `let x = obj.arr`
	// aliases and a mutation through `obj.arr` is visible through x.
	if fieldTy.IsArray {
		return e.loadArrayFieldValue(gepReg, fieldTy), nil
	}
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
	if e.isErrorOptionalNumber(objVal.Ty, ex.Property) {
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp une double %s, 0.0", present, result))
		return e.wrapUndefinedable(Value{Ref: result, Ty: fieldTy}, present), nil
	}
	return Value{Ref: result, Ty: fieldTy}, nil
}

// isErrorOptionalNumber is errorOptionalNumber for a builtin error value.
func (e *Emitter) isErrorOptionalNumber(objTy Type, name string) bool {
	return objTy.IsError && !objTy.IsClass && errorOptionalNumber(objTy, name)
}

// hasLengthField reports whether t is an object or class instance declaring
// its own `length` field, which `.length` then reads like any other field.
func hasLengthField(t Type) bool {
	if !t.IsObject || t.IsArray || t.IsTuple {
		return false
	}
	_, _, ok := t.FieldIndex("length")
	return ok
}

// unionAllHaveLength reports whether every member of the union u is a string
// or an array.
func unionAllHaveLength(u Type) bool {
	if len(u.UnionMembers) == 0 {
		return false
	}
	for _, m := range u.UnionMembers {
		if !m.IsArray && !(isForOfStringTy(m) && !m.IsClass) {
			return false
		}
	}
	return true
}

// emitUnionSoleObjectMemberRead reads a property of a union whose one object
// member declares it (`server.address().port` on `AddressInfo | string |
// null`, JavaScript's read): the member's field when the box holds an object,
// JavaScript's TypeError on null or undefined, else the primitive's property
// (undefined for most). The result is any.
func (e *Emitter) emitUnionSoleObjectMemberRead(u Value, prop string, pos ast.Pos) (Value, bool, error) {
	var obj Type
	n := 0
	for _, m := range u.Ty.UnionMembers {
		if unionMemberTag(m) == "object" {
			obj = m
			n++
		}
	}
	if n != 1 {
		return Value{}, false, nil
	}
	obj = e.canonicalizeClassTy(obj)
	idx, fty, ok := obj.FieldIndex(prop)
	if !ok {
		return Value{}, false, nil
	}
	tag, payload := e.emitUnboxTagPayload(Value{Ref: u.Ref, Ty: TypeAny})
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	objL, restL, doneL := e.freshLabel("uread.obj"), e.freshLabel("uread.rest"), e.freshLabel("uread.done")
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, restL))
	e.emitLabel(objL)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, payload))
	g := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, obj.StructIR(), p, idx))
	fv := e.loadScalarOrNullableField(g, fty)
	boxed, err := e.emitBoxValue(fv)
	if err != nil {
		return Value{}, true, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(restL)
	nullishL, primL := e.freshLabel("uread.nullish"), e.freshLabel("uread.prim")
	isNull, isUndef, nullish := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isNull, isUndef))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, nullishL, primL))
	e.emitLabel(nullishL)
	nullMsg := e.internString("Cannot read properties of null (reading '" + prop + "')")
	undefMsg := e.internString("Cannot read properties of undefined (reading '" + prop + "')")
	msg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", msg, isNull, nullMsg, undefMsg))
	e.emitThrowTypeErrorValue(msg)
	e.emitLabel(primL)
	pv, err := e.emitDynAnyMemberGetNamed(Value{Ref: u.Ref, Ty: TypeAny}, e.internString(prop), prop, pos)
	if err != nil {
		return Value{}, true, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pv.Ref, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, res))
	return Value{Ref: out, Ty: TypeAny}, true, nil
}

// unionCommonMember is the type of member prop when every member of union u
// is a self-identifying object declaring it with one representation
// (`any` when the declared types differ).
func unionCommonMember(u Type, prop string) (Type, bool) {
	var ft Type
	for i, m := range u.UnionMembers {
		if !selfIdentifyingObjects([]Type{m}) {
			return Type{}, false
		}
		_, t, ok := m.FieldIndex(prop)
		if !ok {
			return Type{}, false
		}
		if i == 0 {
			ft = t
		} else if StructFieldIR(t) != StructFieldIR(ft) || t.IsDynamic != ft.IsDynamic {
			ft = TypeAny
		}
	}
	return ft, len(u.UnionMembers) > 0
}

// checkerPrimitive is the string, number or boolean type the checker gives
// expr, when it gives exactly one of those; false otherwise (and under
// -compat=js, where the checker does not type the program).
func (e *Emitter) checkerPrimitive(expr ast.Expression) (Type, bool) {
	c := e.front()
	if c == nil || e.compatJS() {
		return Type{}, false
	}
	t := c.TypeOf(expr)
	if call, ok := expr.(*ast.CallExpression); ok && c.Unanswered(t) {
		// An argument the checker cannot type (a codegen-only form): the
		// result the overloads the call may resolve to agree on.
		if fn := c.TypeOf(call.Callee); !c.Unanswered(fn) && fn.Kind == checker.Function && len(fn.Overloads) > 0 {
			if r := c.OverloadResult(call, fn); r != nil {
				t = r
			}
		}
	}
	if c.Unanswered(t) || t.Flags&(checker.Union|checker.Any|checker.Unknown) != 0 {
		return Type{}, false
	}
	switch {
	case t.Flags&(checker.String|checker.StringLiteral) != 0:
		// Not converted: a declared string reached through `any` may be
		// absent at run time (null or undefined, which a string can't tell
		// apart); its methods dispatch on its tag (emitDynStringMethodCall).
		return Type{}, false
	case t.Flags&(checker.Number|checker.NumberLiteral) != 0:
		return TypeF64, true
	case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
		return TypeBool, true
	case t.Symbol != nil && t.Symbol.Name == "Buffer" && (t.Kind == checker.Instance || t.Kind == checker.Interface):
		// A Buffer-returning overload (zlib's `gzipSync`). Only a call: a
		// property declared Buffer may hold null at run time (spawnSync's
		// `stdout` with inherited stdio), which the box keeps.
		if _, ok := expr.(*ast.CallExpression); ok {
			return BufferType(), true
		}
	}
	return Type{}, false
}

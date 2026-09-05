package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"runtime"
	"sort"
	"strings"
)

// emitOptionalMember emits `obj?.property`. For ptr-typed objects it emits a
// null check; a null object yields the zero value for the property's type.
// Supports: string `.length` → i64; object fields → field type.
func (e *Emitter) emitOptionalMember(ex *ast.MemberExpression) (Value, error) {
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}

	// Non-ptr types cannot be a null pointer; fall back to a regular
	// (non-optional) access. Arrays report IR "ptr" but are actually {ptr, i64}
	// aggregate values, not bare pointers — an `icmp eq ptr` null check against
	// one is invalid IR (ADR-00539). A non-nullable array is never null; a
	// nullable array uses the {null, 0} value sentinel whose own `.length` is 0,
	// so plain access is the right behavior either way.
	if objVal.Ty.IR != "ptr" || objVal.Ty.IsArray {
		plain := &ast.MemberExpression{Object: ex.Object, Property: ex.Property}
		return e.emitMember(plain)
	}

	// Determine the result type before emitting branches. TDD-00030: a
	// class accessor (getter/setter) is checked before the plain-field
	// FieldIndex path — an accessor-only property name is never a real
	// Field, so FieldIndex would otherwise report "no field" for it.
	var resultTy Type
	isAccessor := false
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
			if err := e.checkFieldVisibility(objVal.Ty.ClassName, ex.Property, ex.GetPos()); err != nil {
				return Value{}, err
			}
			resultTy = e.canonicalizeClassTy(fieldTy)
		}
	} else if objVal.Ty.IsObject {
		_, fieldTy, ok := objVal.Ty.FieldIndex(ex.Property)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: no field '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
		}
		resultTy = e.canonicalizeClassTy(fieldTy)
	} else {
		return Value{}, fmt.Errorf("%d:%d: optional chaining '?.' does not support property '%s' on type %s",
			ex.GetPos().Line, ex.GetPos().Col, ex.Property, objVal.Ty.IR)
	}

	// Array-typed results need the same {ptr, i64} aggregate slot struct
	// fields do (resultTy.IR alone is just "ptr", with nowhere for the
	// length to go) — see StructFieldIR's doc comment and docs/adr/ADR-00061.md.
	resIR := StructFieldIR(resultTy)

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, resIR, resultTy.Align()))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, objVal.Ref))

	nullL := e.freshLabel("optc.null")
	noNullL := e.freshLabel("optc.nn")
	mergeL := e.freshLabel("optc.merge")

	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, noNullL))

	// null branch: store zero value (a zero-length {null, 0} array for an
	// array-typed result, matching real JS's own "nullish arrayField reads
	// as an empty-shaped zero value" intuition — not a special case, just
	// what an array's own zero value actually looks like).
	e.emitLabel(nullL)
	if resultTy.IsArray {
		z0 := e.freshReg()
		z1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr null, 0", z0))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 0, 1", z1, z0))
		e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align %d", z1, resPtr, resultTy.Align()))
	} else {
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, zeroRef(resultTy), resPtr, resultTy.Align()))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// non-null branch: perform the property access on objVal
	e.emitLabel(noNullL)
	var propVal Value
	if ex.Property == "length" {
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
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d",
			gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
			loadReg, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
		propVal = Value{Ref: loadReg, Ty: fieldTy}
	}
	propVal = e.coerce(propVal, resultTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resIR, propVal.Ref, resPtr, resultTy.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, resIR, resPtr, resultTy.Align()))
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
	var dataPtrReg string
	var lenReg string

	if id, ok := ex.Object.(*ast.Identifier); ok {
		sym, ok := e.lookup(id.Name)
		if !ok {
			return "", TypeVoid, fmt.Errorf("%d:%d: undefined variable '%s'", ex.GetPos().Line, ex.GetPos().Col, id.Name)
		}
		if !sym.Ty.IsArray && !sym.Ty.IsFlatArray {
			return "", TypeVoid, fmt.Errorf("%d:%d: '%s' is not an array", ex.GetPos().Line, ex.GetPos().Col, id.Name)
		}
		elemTy = *sym.Ty.ElemType
		if sym.Ty.IsFlatArray {
			// Flat value-type array (TDD-00134 Stage 2): elements are inline
			// structs — the Inline marker makes the final GEP below stride by
			// StructSize and tells loadArrayElem/storeArrayElem the slot IS
			// the value.
			elemTy = flatElemView(elemTy)
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
			return "", TypeVoid, evalErr
		}
		if !arrVal.Ty.IsArray || arrVal.Ty.ElemType == nil {
			return "", TypeVoid, fmt.Errorf("%d:%d: cannot index a non-array expression", ex.GetPos().Line, ex.GetPos().Col)
		}
		elemTy = *arrVal.Ty.ElemType
		dataPtrReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtrReg, arrVal.Ref))
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, arrVal.Ref))
	}

	idxVal, err := e.emitExpr(ex.Index)
	if err != nil {
		return "", TypeVoid, err
	}
	idxVal = e.arrayIndexToI64(idxVal)

	oobReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp uge i64 %s, %s", oobReg, idxVal.Ref, lenReg))
	oobL := e.freshLabel("arr.oob")
	okL := e.freshLabel("arr.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", oobReg, oobL, okL))

	e.emitLabel(oobL)
	e.emitInternalThrow(e.internString("Array index out of bounds"))

	e.emitLabel(okL)
	gepReg = e.freshReg()
	gepTy := elemTy.IR
	if elemTy.Inline {
		gepTy = elemTy.StructIR()
	}
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, gepTy, dataPtrReg, idxVal.Ref))
	return gepReg, elemTy, nil
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

func (e *Emitter) emitIndex(ex *ast.IndexExpression) (Value, error) {
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
	// process.env["KEY"]: dynamic-key environment variable lookup.
	if e.isProcessEnvExpr(ex.Object) {
		return e.emitProcessEnvGetDynamic(ex.Index)
	}
	// process.argv[i]: an out-of-range read yields "" instead of the general
	// array bounds throw — Node yields undefined there, and the ubiquitous
	// `process.argv[2] === 'child'` / `if (!process.argv[2])` branching
	// (child_process.fork self-fork files, TDD-00141) relies on a falsy,
	// non-matching value rather than a crash.
	if mem, ok := ex.Object.(*ast.MemberExpression); ok && mem.Property == "argv" {
		if id, ok := mem.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
			idxVal, err := e.emitExpr(ex.Index)
			if err != nil {
				return Value{}, err
			}
			idxVal = e.coerce(idxVal, TypeI64)
			dataReg := e.freshReg()
			lenReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__argv_ptr, align 8", dataReg))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__argv_len, align 8", lenReg))
			inb := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %s, %s", inb, idxVal.Ref, lenReg))
			inL := e.freshLabel("argv.in")
			outL := e.freshLabel("argv.out")
			doneL := e.freshLabel("argv.done")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inb, inL, outL))
			e.emitLabel(inL)
			gep := e.freshReg()
			elem := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", gep, dataReg, idxVal.Ref))
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", elem, gep))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(outL)
			empty := e.internString("")
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(doneL)
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", r, elem, inL, empty, outL))
			return Value{Ref: r, Ty: TypePtr}, nil
		}
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
		raw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, objVal.Ref, keyVal.Ref))
		valTy := TypePtr
		if objTy.MapVal != nil {
			valTy = *objTy.MapVal
		}
		if valTy.IR == "ptr" {
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, raw))
			return Value{Ref: p, Ty: valTy}, nil
		}
		if valTy.IR == "double" {
			// Number values are stored as the double's BIT PATTERN in the
			// i64 slot — reinterpret, never numerically convert.
			d := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", d, raw))
			return Value{Ref: d, Ty: valTy}, nil
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
		return e.emitDynAnyMemberGet(objVal, keyRef, ex.GetPos())
	}
	// String indexing: s[i] returns a single-character string.
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && isStringTy(sym.Ty) {
			strPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", strPtr, sym.Ptr))
			return e.emitStringCharAt(strPtr, ex.Index)
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
			if objVal.Ty.IsClass {
				if err := e.checkFieldVisibility(objVal.Ty.ClassName, key, ex.GetPos()); err != nil {
					return Value{}, err
				}
			}
			gepReg := e.freshReg()
			loadReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
			return Value{Ref: loadReg, Ty: e.canonicalizeClassTy(fieldTy)}, nil
		}
	}
	// Array indexing.
	gepReg, elemTy, err := e.emitIndexPtr(ex)
	if err != nil {
		return Value{}, err
	}
	raw := e.loadArrayElem(gepReg, elemTy)
	// TDD-00101: a BigInt64Array/BigUint64Array element surfaces as a bigint
	// handle, not the raw stored i64.
	if taTy := e.inferExprType(ex.Object); taTy.BigIntElem {
		return e.wrapTypedArrayLoad(raw, taTy), nil
	}
	return raw, nil
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
	if unwrapped := e.unwrapGlobalThis(ex); unwrapped != ast.Expression(ex) {
		return e.emitExpr(unwrapped)
	}
	if ex.Optional {
		return e.emitOptionalMember(ex)
	}
	// A namespace-qualified type-member chain (`X.Color.Red`,
	// `X.C.staticField` — ADR-00480): drop the namespace qualifier up
	// front — a pure AST rewrite — so every dispatch below sees the bare
	// desugared name.
	if bare := e.stripNSTypeQualifier(ex.Object); bare != nil {
		return e.emitMember(&ast.MemberExpression{Object: bare, Property: ex.Property})
	}
	// http2.constants members are compile-time literals (TDD-00139 Stage 4);
	// `http2.constants` itself binds as a flagged namespace value.
	if e.isH2ConstantsExpr(ex.Object) {
		return e.emitH2Constant(ex.Property, ex.GetPos())
	}
	if ex.Property == "constants" {
		if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "http2__kml_builtin" {
			ty := TypeI64
			ty.IsH2Constants = true
			return Value{Ref: "0", Ty: ty}, nil
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
	if ex.Property == "byteLength" || ex.Property == "byteOffset" || ex.Property == "buffer" {
		if objTy := e.inferExprType(ex.Object); objTy.IsDataView {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitDataViewProp(objVal, ex.Property, ex.GetPos())
		}
	}
	// diagnostics_channel Channel properties (hasSubscribers/name).
	if ex.Property == "hasSubscribers" || ex.Property == "name" {
		if objTy := e.inferExprType(ex.Object); objTy.IsDCChannel {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitDiagChannelMember(objVal, ex.Property, ex.GetPos())
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
	// process.stdout/.stderr/.stdin `.isTTY` — a nested two-level member chain
	// (process.<stdio> is a pseudo-namespace, not a bindable value), same
	// shape check as process.stdout.write in emit_call.go.
	if ex.Property == "isTTY" {
		if inner, ok := ex.Object.(*ast.MemberExpression); ok {
			if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
				switch inner.Property {
				case "stdin":
					return e.emitProcessStreamIsTTY(0), nil
				case "stdout":
					return e.emitProcessStreamIsTTY(1), nil
				case "stderr":
					return e.emitProcessStreamIsTTY(2), nil
				}
			}
		}
	}
	// process.stdout/.stderr `.columns` / `.rows` — TDD-00031, a live
	// ioctl(TIOCGWINSZ) read, same nested pseudo-namespace shape as isTTY.
	if ex.Property == "columns" || ex.Property == "rows" {
		if inner, ok := ex.Object.(*ast.MemberExpression); ok {
			if id, ok := inner.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
				switch inner.Property {
				case "stdout":
					return e.emitProcessWinSize(1, ex.Property), nil
				case "stderr":
					return e.emitProcessWinSize(2, ex.Property), nil
				}
			}
		}
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
		switch ex.Property {
		case "argv":
			return e.emitProcessArgv()
		case "pid":
			return e.emitProcessPid()
		case "platform":
			return Value{Ref: e.internString(nodePlatformName()), Ty: TypePtr}, nil
		case "arch":
			return Value{Ref: e.internString(nodeArchName()), Ty: TypePtr}, nil
		case "execPath":
			return e.emitProcessExecPath()
		case "version":
			return e.emitProcessVersion()
		case "versions":
			return e.emitProcessVersions(ex.GetPos())
		case "exitCode":
			e.usedProcessLifecycle = true
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_process_exit_code, align 8", r))
			return Value{Ref: r, Ty: TypeI64}, nil
		case "stdin":
			// The streaming process.stdin handle (.on('data'|'end')). Idempotent:
			// every access returns the one active handle (runtime_stdin.go).
			e.ensureStdinRuntime()
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_stdin_create()", r))
			return Value{Ref: r, Ty: StdinType()}, nil
		case "send":
			// Bare `process.send` (not a call) is the corpus's forked-child
			// probe (`if (process.send) …`): a boolean "was this process
			// forked with an IPC channel" (TDD-00141). Node's value is the
			// function-or-undefined; the truthiness use is what matters here.
			e.ensureIPCChildRuntime()
			fd := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_ipcc_fd()", fd))
			b := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp sgt i32 %s, 0", b, fd))
			return Value{Ref: b, Ty: TypeBool}, nil
		}
	}
	if e.isProcessEnvExpr(ex.Object) {
		return e.emitProcessEnvGetStatic(ex.Property)
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "cluster__kml_builtin" {
		switch ex.Property {
		case "isPrimary":
			return e.emitClusterIsPrimary()
		case "isWorker":
			return e.emitClusterIsWorker()
		case "workerId":
			return e.emitClusterWorkerID()
		}
	}
	if e.inferExprType(ex.Object).IsClusterWorker {
		return e.emitClusterWorkerMember(ex.Object, ex.Property, ex.GetPos())
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "path__kml_builtin" {
		switch ex.Property {
		case "sep":
			return Value{Ref: e.internString("/"), Ty: TypePtr}, nil
		case "delimiter":
			return Value{Ref: e.internString(":"), Ty: TypePtr}, nil
		}
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "test__kml_builtin" {
		// Environment probes (TDD-00122) — constant booleans. This compiler is
		// POSIX-only; hasCrypto/hasIntl reflect the built-in surface.
		switch ex.Property {
		case "isWindows":
			return Value{Ref: "0", Ty: TypeBool}, nil
		case "isLinux":
			return Value{Ref: testHostBool(runtime.GOOS == "linux"), Ty: TypeBool}, nil
		case "isMacOS":
			return Value{Ref: testHostBool(runtime.GOOS == "darwin"), Ty: TypeBool}, nil
		case "hasCrypto":
			return Value{Ref: "1", Ty: TypeBool}, nil
		case "hasIntl":
			return Value{Ref: "0", Ty: TypeBool}, nil
		case "isMainThread":
			return Value{Ref: "1", Ty: TypeBool}, nil
		}
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "os__kml_builtin" {
		switch ex.Property {
		case "EOL":
			// Always "\n" — this compiler is POSIX-only (no Windows target,
			// TDD-00020 not started), so there's no real "\r\n" case.
			return Value{Ref: e.internString("\n"), Ty: TypePtr}, nil
		}
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
	// node:sqlite computed properties (ADR-00540): db.isTransaction reads the
	// live autocommit state; stmt.expandedSQL renders the SQL with bound values.
	if ex.Property == "isTransaction" {
		if objTy := e.inferExprType(ex.Object); objTy.IsSQLiteDatabase {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			e.ensureSQLite3()
			h := e.loadFieldValue(objVal, e.sqliteFieldIdx(objVal.Ty, "__kml_handle"), TypePtr).Ref
			ac := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @sqlite3_get_autocommit(ptr %s)", ac, h))
			// autocommit == 0 means a transaction is open.
			b := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", b, ac))
			return Value{Ref: b, Ty: TypeBool}, nil
		}
	}
	if ex.Property == "expandedSQL" {
		if objTy := e.inferExprType(ex.Object); objTy.IsSQLiteStatement {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			e.ensureSQLite3()
			h := e.loadFieldValue(objVal, e.sqliteFieldIdx(objVal.Ty, "__kml_handle"), TypePtr).Ref
			raw := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @sqlite3_expanded_sql(ptr %s)", raw, h))
			s := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", s, raw))
			// sqlite3_expanded_sql returns a sqlite3_malloc'd buffer; free it now
			// that we've copied into a KML string.
			e.emitInstr(fmt.Sprintf("call void @sqlite3_free(ptr %s)", raw))
			return Value{Ref: s, Ty: TypePtr}, nil
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
		if objTy := e.inferExprType(ex.Object); objTy.IsResponse {
			return e.emitResponseBodyStream(ex)
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
			if ok {
				pend := e.loadFieldValue(objVal, pendIdx, pendTy)
				e.ensureFetchHeadersMap()
				m := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_headers_map(ptr %s)", m, pend.Ref))
				return Value{Ref: m, Ty: HeadersType()}, nil
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
	if ex.Property == "port1" || ex.Property == "port2" {
		// TDD-00099: `ch.port1` / `ch.port2` off a MessageChannel pair.
		if objTy := e.inferExprType(ex.Object); objTy.IsMessageChannel {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitMessageChannelPortRead(objVal, ex.Property)
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
	if ex.Property == "length" {
		// Named array variable: load length from its LenPtr alloca.
		if id, ok := ex.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found {
				// A tuple has a fixed, compile-time-known arity (TDD-00066).
				if sym.Ty.IsTuple {
					return e.countToNumber(Value{Ref: fmt.Sprintf("%d", len(sym.Ty.Fields)), Ty: TypeI64}), nil
				}
				if sym.Ty.IsArray || sym.Ty.IsFlatArray {
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
	// members hold it at offset 0 (a string), so unbox the tag-6 pointer and load
	// field 0. Every other field is member-specific and needs narrowing first.
	if objVal.Ty.IsDynamic && len(objVal.Ty.UnionMembers) > 0 {
		if name, dTy, ok := unionDiscriminantField(objVal.Ty); ok && ex.Property == name {
			_, payload := e.emitUnboxTagPayload(objVal)
			objptr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", objptr, payload))
			val := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", val, objptr))
			return Value{Ref: val, Ty: dTy}, nil
		}
		return Value{}, fmt.Errorf("%d:%d: '%s' can't be read on an un-narrowed union — narrow it first (e.g. `if (x.%s === ...)` or `typeof`)", ex.GetPos().Line, ex.GetPos().Col, ex.Property, ex.Property)
	}
	// A property read on a bare any/unknown value is a runtime tag dispatch
	// into the D1 dynamic object model (TDD-00155 Stage 1).
	if isUnconstrainedDynamic(objVal.Ty) {
		return e.emitDynAnyMemberGetNamed(objVal, e.internString(ex.Property), ex.Property, ex.GetPos())
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
	idx, fieldTy, ok := objVal.Ty.FieldIndex(ex.Property)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: no field '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
	}
	if objVal.Ty.IsClass {
		if err := e.checkFieldVisibility(objVal.Ty.ClassName, ex.Property, ex.GetPos()); err != nil {
			return Value{}, err
		}
	}
	fieldTy = e.canonicalizeClassTy(fieldTy)
	gepReg := e.freshReg()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
	return Value{Ref: result, Ty: fieldTy}, nil
}

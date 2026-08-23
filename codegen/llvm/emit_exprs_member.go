package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitOptionalMember emits `obj?.property`. For ptr-typed objects it emits a
// null check; a null object yields the zero value for the property's type.
// Supports: string `.length` → i64; object fields → field type.
func (e *Emitter) emitOptionalMember(ex *ast.MemberExpression) (Value, error) {
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}

	// Non-ptr types cannot be null; fall back to a regular (non-optional) access.
	if objVal.Ty.IR != "ptr" {
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
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", r, objVal.Ref))
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
		if !sym.Ty.IsArray {
			return "", TypeVoid, fmt.Errorf("%d:%d: '%s' is not an array", ex.GetPos().Line, ex.GetPos().Col, id.Name)
		}
		elemTy = *sym.Ty.ElemType
		dataPtrReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtrReg, sym.Ptr))
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, sym.LenPtr))
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
	idxVal = e.coerce(idxVal, TypeI64)

	oobReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp uge i64 %s, %s", oobReg, idxVal.Ref, lenReg))
	oobL := e.freshLabel("arr.oob")
	okL := e.freshLabel("arr.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", oobReg, oobL, okL))

	e.emitLabel(oobL)
	e.emitInternalThrow(e.internString("Array index out of bounds"))

	e.emitLabel(okL)
	gepReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, elemTy.IR, dataPtrReg, idxVal.Ref))
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

func (e *Emitter) emitIndex(ex *ast.IndexExpression) (Value, error) {
	// process.env["KEY"]: dynamic-key environment variable lookup.
	if e.isProcessEnvExpr(ex.Object) {
		return e.emitProcessEnvGetDynamic(ex.Index)
	}
	// Group map access: grouped["key"] → sub-array.
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsGroupMap {
			return e.emitGroupMapIndex(sym, ex.Index, ex.GetPos())
		}
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

func (e *Emitter) emitMember(ex *ast.MemberExpression) (Value, error) {
	if ex.Optional {
		return e.emitOptionalMember(ex)
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
		if members, nsName := e.namespaceMembers(id.Name); members != nil && members[ex.Property] {
			if !e.isShadowedByLocal(id.Name) {
				return e.emitIdent(ast.NewIdentifier(ast.NamespaceMangle(nsName, ex.Property), ex.GetPos()))
			}
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
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "process" && !e.isShadowedByLocal(id.Name) {
		switch ex.Property {
		case "argv":
			return e.emitProcessArgv()
		case "pid":
			return e.emitProcessPid()
		case "platform":
			return Value{Ref: e.internString(nodePlatformName()), Ty: TypePtr}, nil
		}
	}
	if e.isProcessEnvExpr(ex.Object) {
		return e.emitProcessEnvGetStatic(ex.Property)
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "cluster__kml_builtin" {
		switch ex.Property {
		case "isPrimary":
			return e.emitClusterIsPrimary()
		case "workerId":
			return e.emitClusterWorkerID()
		}
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "path__kml_builtin" {
		switch ex.Property {
		case "sep":
			return Value{Ref: e.internString("/"), Ty: TypePtr}, nil
		case "delimiter":
			return Value{Ref: e.internString(":"), Ty: TypePtr}, nil
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
				lenReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, sym.LenPtr))
				return e.emitTypedArrayByteLength(lenReg, *sym.Ty.ElemType)
			}
		} else if objTy := e.inferExprType(ex.Object); objTy.IsArrayBuffer {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitArrayBufferByteLength(objVal)
		} else if objTy.IsTypedArray {
			objVal, err := e.emitExpr(ex.Object)
			if err != nil {
				return Value{}, err
			}
			lenReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, objVal.Ref))
			return e.emitTypedArrayByteLength(lenReg, *objTy.ElemType)
		}
	}
	if ex.Property == "length" {
		// Named array variable: load length from its LenPtr alloca.
		if id, ok := ex.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found {
				// A tuple has a fixed, compile-time-known arity (TDD-00066).
				if sym.Ty.IsTuple {
					return Value{Ref: fmt.Sprintf("%d", len(sym.Ty.Fields)), Ty: TypeI64}, nil
				}
				if sym.Ty.IsArray {
					reg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", reg, sym.LenPtr))
					return Value{Ref: reg, Ty: TypeI64}, nil
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
			return Value{Ref: fmt.Sprintf("%d", len(objVal.Ty.Fields)), Ty: TypeI64}, nil
		}
		// Array aggregate (e.g. from Object.keys(), arr.slice(), call result): extract field 1.
		if objVal.Ty.IsArray {
			reg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", reg, objVal.Ref))
			return Value{Ref: reg, Ty: TypeI64}, nil
		}
		// String: call strlen.
		if objVal.Ty.IR == "ptr" && !objVal.Ty.IsObject && !objVal.Ty.IsFunc {
			e.ensureStrlen()
			reg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", reg, objVal.Ref))
			return Value{Ref: reg, Ty: TypeI64}, nil
		}
		return Value{}, fmt.Errorf("%d:%d: .length is only supported on arrays and strings", ex.GetPos().Line, ex.GetPos().Col)
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

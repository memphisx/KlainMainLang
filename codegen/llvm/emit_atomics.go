// emit_atomics.go — the Atomics namespace (TDD-00099). The plain operations
// lower directly to LLVM atomic instructions (seq_cst throughout, the spec's
// ordering); wait/notify go through runtime_atomics.go's portable futex
// substitute. Receivers are integer TypedArrays (any of Int8..Uint32 for the
// plain ops; Int32Array only for wait, per spec) — float TypedArrays are a
// compile-time rejection, exactly as the spec's ValidateIntegerTypedArray.
// Works on any TypedArray, but is only cross-thread-meaningful over a
// SharedArrayBuffer. Indexing is bounds-unchecked, matching ordinary
// TypedArray indexing.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// atomicsRMWOps maps the Atomics method name to the atomicrmw operation.
var atomicsRMWOps = map[string]string{
	"add":      "add",
	"sub":      "sub",
	"and":      "and",
	"or":       "or",
	"xor":      "xor",
	"exchange": "xchg",
}

// emitAtomicsElemPtr resolves args[0] as an integer TypedArray and emits the
// element-address computation for args[1], returning (elemPtr, elemTy).
func (e *Emitter) emitAtomicsElemPtr(method string, args []ast.Expression, pos ast.Pos) (string, Type, Type, error) {
	if len(args) < 2 {
		return "", Type{}, Type{}, fmt.Errorf("%d:%d: Atomics.%s takes (typedArray, index, ...)", pos.Line, pos.Col, method)
	}
	taTy := e.inferExprType(args[0])
	if !taTy.IsTypedArray {
		return "", Type{}, Type{}, fmt.Errorf("%d:%d: Atomics.%s requires a TypedArray receiver", pos.Line, pos.Col, method)
	}
	elemTy := *taTy.ElemType
	switch elemTy.IR {
	case "i8", "i16", "i32":
	case "i64":
		// Only BigInt64Array/BigUint64Array store i64 — spec-included
		// (TDD-00101); operands/results cross as bigint handles.
	default:
		return "", Type{}, Type{}, fmt.Errorf("%d:%d: Atomics.%s requires an integer TypedArray (Int8/Uint8/Int16/Uint16/Int32/Uint32/BigInt64/BigUint64Array)", pos.Line, pos.Col, method)
	}
	ptrReg, _, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return "", Type{}, Type{}, err
	}
	idxVal, err := e.emitExpr(args[1])
	if err != nil {
		return "", Type{}, Type{}, err
	}
	idxVal = e.coerce(idxVal, TypeI64)
	elemPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", elemPtr, elemTy.IR, ptrReg, idxVal.Ref))
	return elemPtr, elemTy, taTy, nil
}

// emitAtomicsOperand converts an Atomics value operand into the raw stored
// scalar (bigint unwrap for a BigIntElem receiver, plain coerce otherwise).
func (e *Emitter) emitAtomicsOperand(expr ast.Expression, elemTy, taTy Type, pos ast.Pos) (Value, error) {
	val, err := e.emitExpr(expr)
	if err != nil {
		return Value{}, err
	}
	if taTy.BigIntElem {
		return e.coerceTypedArrayStore(val, taTy, pos)
	}
	return e.coerce(val, elemTy), nil
}

// wrapAtomicsResult wraps a raw loaded/old scalar back into the language
// value (a bigint handle for BigIntElem receivers).
func (e *Emitter) wrapAtomicsResult(v Value, taTy Type) Value {
	if taTy.BigIntElem {
		return e.wrapTypedArrayLoad(v, taTy)
	}
	return v
}

// emitAtomicsCall dispatches Atomics.<method>(...).
func (e *Emitter) emitAtomicsCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if op, ok := atomicsRMWOps[method]; ok {
		elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(method, args, pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 3 {
			return Value{}, fmt.Errorf("%d:%d: Atomics.%s takes (typedArray, index, value)", pos.Line, pos.Col, method)
		}
		val, err := e.emitAtomicsOperand(args[2], elemTy, taTy, pos)
		if err != nil {
			return Value{}, err
		}
		old := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = atomicrmw %s ptr %s, %s %s seq_cst", old, op, elemPtr, elemTy.IR, val.Ref))
		return e.wrapAtomicsResult(Value{Ref: old, Ty: elemTy}, taTy), nil
	}

	switch method {
	case "isLockFree":
		// Every supported width (1/2/4/8 bytes) lowers to a native LLVM
		// atomic on both host targets, so the answer depends only on the
		// byte-size argument.
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: Atomics.isLockFree takes (size)", pos.Line, pos.Col)
		}
		szVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		szVal = e.coerce(szVal, TypeI64)
		var cmps [4]string
		for i, n := range []string{"1", "2", "4", "8"} {
			cmps[i] = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", cmps[i], szVal.Ref, n))
		}
		or1 := e.freshReg()
		or2 := e.freshReg()
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", or1, cmps[0], cmps[1]))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", or2, cmps[2], cmps[3]))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", res, or1, or2))
		return Value{Ref: res, Ty: TypeBool}, nil

	case "load":
		elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(method, args, pos)
		if err != nil {
			return Value{}, err
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load atomic %s, ptr %s seq_cst, align %d", r, elemTy.IR, elemPtr, elemTy.Align()))
		return e.wrapAtomicsResult(Value{Ref: r, Ty: elemTy}, taTy), nil

	case "store":
		elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(method, args, pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 3 {
			return Value{}, fmt.Errorf("%d:%d: Atomics.store takes (typedArray, index, value)", pos.Line, pos.Col)
		}
		val, err := e.emitAtomicsOperand(args[2], elemTy, taTy, pos)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store atomic %s %s, ptr %s seq_cst, align %d", elemTy.IR, val.Ref, elemPtr, elemTy.Align()))
		// Spec: store returns the stored value.
		return e.wrapAtomicsResult(Value{Ref: val.Ref, Ty: elemTy}, taTy), nil

	case "compareExchange":
		elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(method, args, pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 4 {
			return Value{}, fmt.Errorf("%d:%d: Atomics.compareExchange takes (typedArray, index, expected, replacement)", pos.Line, pos.Col)
		}
		expVal, err := e.emitAtomicsOperand(args[2], elemTy, taTy, pos)
		if err != nil {
			return Value{}, err
		}
		repVal, err := e.emitAtomicsOperand(args[3], elemTy, taTy, pos)
		if err != nil {
			return Value{}, err
		}
		pair := e.freshReg()
		old := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = cmpxchg ptr %s, %s %s, %s %s seq_cst seq_cst", pair, elemPtr, elemTy.IR, expVal.Ref, elemTy.IR, repVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue { %s, i1 } %s, 0", old, elemTy.IR, pair))
		return e.wrapAtomicsResult(Value{Ref: old, Ty: elemTy}, taTy), nil

	case "wait":
		elemPtr, elemTy, _, err := e.emitAtomicsElemPtr(method, args, pos)
		if err != nil {
			return Value{}, err
		}
		if elemTy.IR != "i32" || !elemTy.Signed {
			return Value{}, fmt.Errorf("%d:%d: Atomics.wait requires an Int32Array", pos.Line, pos.Col)
		}
		if len(args) != 3 && len(args) != 4 {
			return Value{}, fmt.Errorf("%d:%d: Atomics.wait takes (int32Array, index, expected, timeoutMs?)", pos.Line, pos.Col)
		}
		expVal, err := e.emitExpr(args[2])
		if err != nil {
			return Value{}, err
		}
		expVal = e.coerce(expVal, TypeI32)
		tmoRef := "-1.0"
		if len(args) == 4 {
			tmoVal, err := e.emitExpr(args[3])
			if err != nil {
				return Value{}, err
			}
			tmoVal = e.coerce(tmoVal, TypeF64)
			tmoRef = tmoVal.Ref
		}
		e.ensureAtomicsRuntime()
		code := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_atomics_wait(ptr %s, i32 %s, double %s)", code, elemPtr, expVal.Ref, tmoRef))
		// Map 0/1/2 to the spec's result strings.
		isok := e.freshReg()
		isne := e.freshReg()
		s1 := e.freshReg()
		s2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isok, code))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", isne, code))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", s1, isne, e.internString("not-equal"), e.internString("timed-out")))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", s2, isok, e.internString("ok"), s1))
		return Value{Ref: s2, Ty: TypePtr}, nil

	case "notify":
		elemPtr, elemTy, _, err := e.emitAtomicsElemPtr(method, args, pos)
		if err != nil {
			return Value{}, err
		}
		if elemTy.IR != "i32" || !elemTy.Signed {
			return Value{}, fmt.Errorf("%d:%d: Atomics.notify requires an Int32Array", pos.Line, pos.Col)
		}
		countRef := "9223372036854775807"
		if len(args) == 3 {
			cVal, err := e.emitExpr(args[2])
			if err != nil {
				return Value{}, err
			}
			cVal = e.coerce(cVal, TypeI64)
			countRef = cVal.Ref
		} else if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: Atomics.notify takes (int32Array, index, count?)", pos.Line, pos.Col)
		}
		e.ensureAtomicsRuntime()
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_atomics_notify(ptr %s, i64 %s)", n, elemPtr, countRef))
		return Value{Ref: n, Ty: TypeI64}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Atomics method '%s' (load/store/add/sub/and/or/xor/exchange/compareExchange/wait/notify/isLockFree)", pos.Line, pos.Col, method)
}

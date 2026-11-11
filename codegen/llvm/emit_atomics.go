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
		return "", Type{}, Type{}, internalAt(pos, "Atomics.%s takes (typedArray, index, ...)", method)
	}
	taTy := e.typeOfExpr(args[0])
	if !taTy.IsTypedArray() {
		return "", Type{}, Type{}, internalAt(pos, "Atomics.%s requires a TypedArray receiver", method)
	}
	elemTy := *taTy.ElemType
	switch elemTy.IR {
	case "i8", "i16", "i32":
	case "i64":
		// Only BigInt64Array/BigUint64Array store i64 — spec-included
		// (TDD-00101); operands/results cross as bigint handles.
	default:
		return "", Type{}, Type{}, internalAt(pos, "Atomics.%s element", method)
	}
	// ValidateAtomicAccess: the length is read BEFORE the index is coerced (a
	// `valueOf` that grows the buffer must not widen the check), the index goes
	// through ToIndex (ToNumber → integer, so an object's valueOf runs and a
	// string converts), and an out-of-range index is a RangeError — not an
	// unchecked address, which made `Atomics.wait` block on foreign memory
	// forever (ADR-01061).
	ptrReg, lenReg, _, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return "", Type{}, Type{}, err
	}
	if method == "wait" {
		// Wait blocks only on shared memory: a TypeError before the index
		// is coerced.
		sh, isSh := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_view_is_shared("+irPtr+" %s)", sh, ptrReg))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isSh, sh))
		okL, badL := e.freshLabel("atomics.shared"), e.freshLabel("atomics.unshared")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSh, okL, badL))
		e.emitLabel(badL)
		e.emitThrowTypeError("[object " + typedArrayConstructorName(e.typeOfExpr(args[0])) + "] is not a shared typed array.")
		e.emitLabel(okL)
	}
	idxRaw, err := e.emitExpr(args[1])
	if err != nil {
		return "", Type{}, Type{}, err
	}
	idxNum, err := e.emitUnaryPlus(idxRaw, args[1].GetPos())
	if err != nil {
		return "", Type{}, Type{}, err
	}
	idxNum = e.convert(idxNum, TypeF64)
	if e.blockDone {
		// ToNumber threw (a Symbol index): what follows is unreachable.
		e.emitLabel(e.freshLabel("dead"))
	}
	// ToIntegerOrInfinity: NaN → 0, else truncate.
	isNaN := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", isNaN, idxNum.Ref, idxNum.Ref))
	noNaN := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0.0, double %s", noNaN, isNaN, idxNum.Ref))
	truncD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call double @trunc(double %s)", truncD, noNaN))
	lenD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", lenD, lenReg))
	isNeg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 0.0", isNeg, truncD))
	beyond := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp oge double %s, %s", beyond, truncD, lenD))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, isNeg, beyond))
	badL := e.freshLabel("atomics.badidx")
	okL := e.freshLabel("atomics.okidx")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	e.emitInternalThrowKind("RangeError", e.internString("Invalid atomic access index"))
	e.emitLabel(okL)
	idxReg := e.emitFloatToI64("double", truncD)
	elemPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %s", elemPtr, elemTy.IR, ptrReg, idxReg))
	return elemPtr, elemTy, taTy, nil
}

// emitAtomicsNumber is ToNumber for an Atomics value/timeout operand: an
// object's valueOf runs (and may throw), a string converts, a number is itself.
func (e *Emitter) emitAtomicsNumber(expr ast.Expression) (Value, error) {
	v, err := e.emitExpr(expr)
	if err != nil {
		return Value{}, err
	}
	n, err := e.emitUnaryPlus(v, expr.GetPos())
	if err != nil {
		return Value{}, err
	}
	n = e.convert(n, TypeF64)
	if e.blockDone {
		// ToNumber threw (a Symbol): what follows is unreachable.
		e.emitLabel(e.freshLabel("dead"))
	}
	return n, nil
}

// emitAtomicsOperand converts an Atomics value operand into the raw stored
// scalar (bigint unwrap for a BigIntElem receiver, plain coerce otherwise).
func (e *Emitter) emitAtomicsOperand(expr ast.Expression, elemTy, taTy Type, pos ast.Pos) (Value, error) {
	val, err := e.emitExpr(expr)
	if err != nil {
		return Value{}, err
	}
	if taTy.BigIntElem {
		return e.toTypedArrayElem(val, taTy, pos)
	}
	return e.convert(val, elemTy), nil
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
	if method != "isLockFree" && len(args) > 0 {
		if _, spread := args[0].(*ast.SpreadElement); !spread && e.typeOfExpr(args[0]).IsDynamic() && !e.omittedArg(args[0]) {
			return e.emitAtomicsDynamicReceiver(method, args, pos)
		}
	}
	if v, thrown, err := e.atomicsRefusedReceiver(method, args, pos); thrown || err != nil {
		return v, err
	}
	if op, ok := atomicsRMWOps[method]; ok {
		elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(method, args, pos)
		if err != nil {
			return Value{}, err
		}
		if len(args) != 3 {
			return Value{}, internalAt(pos, "Atomics.%s takes (typedArray, index, value)", method)
		}
		val, err := e.emitAtomicsOperand(args[2], elemTy, taTy, pos)
		if err != nil {
			return Value{}, err
		}
		old := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = atomicrmw %s "+irPtr+" %s, %s %s seq_cst", old, op, elemPtr, elemTy.IR, val.Ref))
		return e.wrapAtomicsResult(Value{Ref: old, Ty: elemTy}, taTy), nil
	}

	c := &atomicsCall{method: method, args: args, pos: pos}
	if f, ok := atomicsMethods[method]; ok {
		return f(e, c)
	}
	return Value{}, internalAt(pos, "unknown Atomics method '%s' (load/store/add/sub/and/or/xor/exchange/compareExchange/wait/notify/isLockFree)", method)
}

type atomicsCall struct {
	method string
	args   []ast.Expression
	pos    ast.Pos
}

// atomicsMethods are the Atomics methods other than the read-modify-write operations, by name.
var atomicsMethods map[string]func(e *Emitter, c *atomicsCall) (Value, error)

func init() {
	atomicsMethods = map[string]func(e *Emitter, c *atomicsCall) (Value, error){
		"isLockFree":      (*Emitter).atomicsIsLockFree,
		"load":            (*Emitter).atomicsLoad,
		"store":           (*Emitter).atomicsStore,
		"compareExchange": (*Emitter).atomicsCompareExchange,
		"wait":            (*Emitter).atomicsWait,
		"notify":          (*Emitter).atomicsNotify,
	}
}

// atomicsIsLockFree is isLockFree().
func (e *Emitter) atomicsIsLockFree(c *atomicsCall) (Value, error) {
	// Every supported width (1/2/4/8 bytes) lowers to a native LLVM
	// atomic on both host targets, so the answer depends only on the
	// byte-size argument.
	if len(c.args) != 1 {
		return Value{}, internalAt(c.pos, "Atomics.isLockFree takes (size)")
	}
	szVal, err := e.emitExpr(c.args[0])
	if err != nil {
		return Value{}, err
	}
	// TS types isLockFree as (size: number); a non-numeric argument (e.g. a
	// string) is a type error there. Reject it cleanly rather than letting
	// coerce reinterpret a pointer as i64 and emit invalid IR
	// (icmp eq i64 <ptr>, 1).
	szVal, err = e.convertChecked(szVal, TypeI64, c.args[0].GetPos(), "Atomics.isLockFree size")
	if err != nil {
		return Value{}, err
	}
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
}

// atomicsLoad is load().
func (e *Emitter) atomicsLoad(c *atomicsCall) (Value, error) {
	elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(c.method, c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load atomic %s, "+irPtr+" %s seq_cst, align %d", r, elemTy.IR, elemPtr, elemTy.Align()))
	return e.wrapAtomicsResult(Value{Ref: r, Ty: elemTy}, taTy), nil
}

// atomicsStore is store().
func (e *Emitter) atomicsStore(c *atomicsCall) (Value, error) {
	elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(c.method, c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	if len(c.args) != 3 {
		return Value{}, internalAt(c.pos, "Atomics.store takes (typedArray, index, value)")
	}
	val, err := e.emitAtomicsOperand(c.args[2], elemTy, taTy, c.pos)
	if err != nil {
		return Value{}, err
	}
	e.heapStoreAtomic(elemTy.IR, val.Ref, elemPtr, elemTy.Align())
	// Spec: store returns the stored value.
	return e.wrapAtomicsResult(Value{Ref: val.Ref, Ty: elemTy}, taTy), nil
}

// atomicsCompareExchange is compareExchange().
func (e *Emitter) atomicsCompareExchange(c *atomicsCall) (Value, error) {
	elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(c.method, c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	if len(c.args) != 4 {
		return Value{}, internalAt(c.pos, "Atomics.compareExchange takes (typedArray, index, expected, replacement)")
	}
	expVal, err := e.emitAtomicsOperand(c.args[2], elemTy, taTy, c.pos)
	if err != nil {
		return Value{}, err
	}
	repVal, err := e.emitAtomicsOperand(c.args[3], elemTy, taTy, c.pos)
	if err != nil {
		return Value{}, err
	}
	pair := e.freshReg()
	old := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = cmpxchg "+irPtr+" %s, %s %s, %s %s seq_cst seq_cst", pair, elemPtr, elemTy.IR, expVal.Ref, elemTy.IR, repVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { %s, i1 } %s, 0", old, elemTy.IR, pair))
	return e.wrapAtomicsResult(Value{Ref: old, Ty: elemTy}, taTy), nil
}

// atomicsWait is wait().
func (e *Emitter) atomicsWait(c *atomicsCall) (Value, error) {
	elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(c.method, c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	wide := taTy.BigIntElem && elemTy.IR == "i64" && elemTy.Signed
	if !wide && (elemTy.IR != "i32" || !elemTy.Signed) {
		return Value{}, internalAt(c.pos, "Atomics.wait element")
	}
	if len(c.args) != 3 && len(c.args) != 4 {
		return Value{}, internalAt(c.pos, "Atomics.wait takes (typedArray, index, expected, timeoutMs?)")
	}
	// Spec order: index (above), then ToInt32 / ToBigInt64(value), then
	// ToNumber(timeout).
	var expVal Value
	if wide {
		expVal, err = e.emitAtomicsOperand(c.args[2], elemTy, taTy, c.pos)
	} else {
		expVal, err = e.emitAtomicsNumber(c.args[2])
		expVal = e.convert(expVal, TypeI32)
	}
	if err != nil {
		return Value{}, err
	}
	tmoRef := "0x7FF8000000000000" // absent → NaN → wait forever
	if len(c.args) == 4 {
		tmoVal, err := e.emitAtomicsNumber(c.args[3])
		if err != nil {
			return Value{}, err
		}
		tmoRef = tmoVal.Ref
	}
	code := e.freshReg()
	if wide {
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_atomics_wait64("+irPtr+" %s, i64 %s, double %s)", code, elemPtr, expVal.Ref, tmoRef))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_atomics_wait("+irPtr+" %s, i32 %s, double %s)", code, elemPtr, expVal.Ref, tmoRef))
	}
	// Map 0/1/2 to the spec's result strings.
	isok := e.freshReg()
	isne := e.freshReg()
	s1 := e.freshReg()
	s2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isok, code))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", isne, code))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", s1, isne, e.internString("not-equal"), e.internString("timed-out")))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", s2, isok, e.internString("ok"), s1))
	return Value{Ref: s2, Ty: TypePtr}, nil
}

// atomicsNotify is notify().
func (e *Emitter) atomicsNotify(c *atomicsCall) (Value, error) {
	elemPtr, elemTy, taTy, err := e.emitAtomicsElemPtr(c.method, c.args, c.pos)
	if err != nil {
		return Value{}, err
	}
	if !(elemTy.IR == "i32" && elemTy.Signed) && !(taTy.BigIntElem && elemTy.IR == "i64" && elemTy.Signed) {
		return Value{}, internalAt(c.pos, "Atomics.notify element")
	}
	countRef := "9223372036854775807"
	if len(c.args) == 3 && !isUndefinedLiteral(e, c.args[2]) {
		// ToIntegerOrInfinity(ToNumber(count)), at least 0: NaN and a
		// negative count are 0, +Infinity every waiter.
		cNum, err := e.emitAtomicsNumber(c.args[2])
		if err != nil {
			return Value{}, err
		}
		isNaN := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", isNaN, cNum.Ref, cNum.Ref))
		noNaN := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0.0, double %s", noNaN, isNaN, cNum.Ref))
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 0.0", neg, noNaN))
		nonNeg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0.0, double %s", nonNeg, neg, noNaN))
		countRef = e.emitFloatToI64("double", nonNeg) // saturating: +Infinity is every waiter
	} else if len(c.args) != 2 && len(c.args) != 3 {
		return Value{}, internalAt(c.pos, "Atomics.notify takes (int32Array, index, count?)")
	}
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_atomics_notify("+irPtr+" %s, i64 %s)", n, elemPtr, countRef))
	return Value{Ref: n, Ty: TypeI64}, nil
}

// atomicsRefusedReceiver is ValidateIntegerTypedArray for a receiver whose
// static type fails it: the arguments evaluated, then V8's TypeError (`[object
// Float64Array] is not an integer typed array.`; wait/notify want an Int32Array
// or a BigInt64Array). thrown reports the throw; the value stands in for the
// call's result on the dead path after it.
func (e *Emitter) atomicsRefusedReceiver(method string, args []ast.Expression, pos ast.Pos) (Value, bool, error) {
	if method == "isLockFree" {
		return Value{}, false, nil
	}
	var recvTy Type
	if len(args) > 0 {
		recvTy = e.typeOfExpr(args[0])
	}
	undef := len(args) == 0 || e.omittedArg(args[0]) || isUndefinedLiteral(e, stripAsAny(args[0]))
	if !undef && (recvTy.IsDynamic() || recvTy.Nullable() && recvTy.IsTypedArray()) {
		return Value{}, false, nil // (decided by the value)
	}
	desc := "undefined"
	if !undef {
		switch {
		case recvTy.IsTypedArray():
			desc = "[object " + typedArrayConstructorName(recvTy) + "]"
		case recvTy.IsArray():
			desc = "[object Array]"
		case recvTy.IsNull():
			desc = "null"
		default:
			desc = ""
		}
	}
	ok := false
	waitLike := method == "wait" || method == "notify"
	if recvTy.IsTypedArray() && recvTy.ElemType != nil && len(args) > 0 {
		el := *recvTy.ElemType
		switch {
		case waitLike:
			ok = el.IR == "i32" && el.Signed || recvTy.BigIntElem && el.IR == "i64" && el.Signed
		default:
			ok = (el.IR == "i8" || el.IR == "i16" || el.IR == "i32" || el.IR == "i64") && !recvTy.Clamped
		}
	}
	if ok {
		return Value{}, false, nil
	}
	var first Value
	for i, a := range args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, true, err
		}
		if i == 0 {
			first = v
		}
	}
	msg := " is not an integer typed array."
	if waitLike && recvTy.IsTypedArray() {
		msg = " is not an int32 or BigInt64 typed array."
	}
	if desc != "" {
		e.emitThrowTypeError(desc + msg)
	} else {
		boxed, err := e.emitBoxValue(first)
		if err != nil {
			return Value{}, true, err
		}
		d, err := e.emitValueToString(boxed)
		if err != nil {
			return Value{}, true, err
		}
		m, err := e.emitStringConcat(d, Value{Ref: e.internString(msg), Ty: TypePtr})
		if err != nil {
			return Value{}, true, err
		}
		e.emitThrowTypeErrorValue(m.Ref)
	}
	e.emitLabel(e.freshLabel("atomics.dead"))
	rt := e.atomicsResultType(method, ast.NewCallExpression(ast.NewIdentifier("Atomics", pos), args, pos))
	return Value{Ref: zeroOf(rt), Ty: rt}, true, nil
}

// atomicsReceiverKinds are the typed arrays an Atomics receiver held in any
// is tested against: each branch is the call with its static kind, which
// refuses a kind the operation does not take with V8's own message.
var atomicsReceiverKinds = []string{"Int8Array", "Uint8Array", "Int16Array", "Uint16Array", "Int32Array", "Uint32Array",
	"BigInt64Array", "BigUint64Array", "Uint8ClampedArray", "Float32Array", "Float64Array"}

// emitAtomicsDynamicReceiver is Atomics.<method>(ta, …) for a ta held in any:
// the arguments evaluated once, in order, then the operation for ta's own
// kind of typed array (ValidateIntegerTypedArray at run time), its result
// boxed; a value that is no typed array throws V8's TypeError.
func (e *Emitter) emitAtomicsDynamicReceiver(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	temps := make([]ast.Expression, len(args))
	for i, a := range args {
		v, err := e.emitExpr(a)
		if err != nil {
			return Value{}, err
		}
		if v, err = e.emitBoxValue(v); err != nil {
			return Value{}, err
		}
		temps[i] = e.anyTemp(v.Ref, pos)
	}
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	end := e.freshLabel("atomics.dyn.end")
	for _, kind := range atomicsReceiverKinds {
		test, err := e.emitExpr(ast.NewBinaryExpression("instanceof", temps[0], ast.NewIdentifier(kind, pos), pos))
		if err != nil {
			return Value{}, err
		}
		yes, no := e.freshLabel("atomics.dyn.is"), e.freshLabel("atomics.dyn.not")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", test.Ref, yes, no))
		e.emitLabel(yes)
		typed := append([]ast.Expression{ast.NewAsExpression(temps[0], nameAnnotation(kind), pos)}, temps[1:]...)
		v, err := e.emitAtomicsCall(method, typed, pos)
		if err != nil {
			return Value{}, err
		}
		if !e.blockDone {
			bv, err := e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", bv.Ref, res))
			e.emitTerminator("br label %" + end)
		}
		e.emitLabel(no)
	}
	recv, err := e.emitExpr(temps[0])
	if err != nil {
		return Value{}, err
	}
	d, err := e.emitV8ValueText(recv)
	if err != nil {
		return Value{}, err
	}
	msg := " is not an integer typed array."
	if method == "wait" || method == "notify" || method == "waitAsync" {
		msg = " is not an int32 or BigInt64 typed array."
	}
	m, err := e.emitStringConcat(d, Value{Ref: e.internString(msg), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.emitThrowTypeErrorValue(m.Ref)
	e.emitLabel(end)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", out, res))
	return Value{Ref: out, Ty: TypeAny}, nil
}

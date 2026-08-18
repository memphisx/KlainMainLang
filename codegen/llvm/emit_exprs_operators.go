package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

func (e *Emitter) emitBinary(ex *ast.BinaryExpression) (Value, error) {
	// && / || must short-circuit: the right operand is only evaluated when the
	// left doesn't already decide the result. This has to happen before the
	// eager both-operands evaluation below, so it's intercepted here.
	if ex.Op == "&&" || ex.Op == "||" {
		return e.emitShortCircuit(ex)
	}

	// `nullableScalar === null` / `!== null` (and ==/!=) must be answered from
	// the stored presence bit before either operand is evaluated — emitExpr on
	// the scalar would auto-unwrap to its payload, reintroducing the 0-sentinel
	// collision this representation removes. See emit_nullable_scalar.go.
	if ex.Op == "==" || ex.Op == "!=" || ex.Op == "===" || ex.Op == "!==" {
		if v, ok, err := e.emitNullableScalarNullCompare(ex); err != nil {
			return Value{}, err
		} else if ok {
			return v, nil
		}
	}

	left, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}
	right, err := e.emitExpr(ex.Right)
	if err != nil {
		return Value{}, err
	}

	// A nullable-scalar aggregate operand (a T|null return/field value) that
	// reached here is not a `=== null` comparison (those returned above) — it
	// is ordinary arithmetic/comparison, which operates on the bare payload
	// (a null reads as its zero, the same lenient collapse a local read does).
	if isNullableScalar(left.Ty) {
		left = e.nullableScalarPayloadOf(left)
	}
	if isNullableScalar(right.Ty) {
		right = e.nullableScalarPayloadOf(right)
	}

	if left.Ty.IsDynamic || right.Ty.IsDynamic {
		switch ex.Op {
		case "===", "==":
			return e.emitAnyEquals(left, right, false)
		case "!==", "!=":
			return e.emitAnyEquals(left, right, true)
		default:
			return Value{}, fmt.Errorf("%d:%d: operator '%s' on any/unknown is not yet supported", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
		}
	}

	// Symbol (TDD-00044): only identity comparison is meaningful — real JS
	// throws TypeError on every other operator applied to a Symbol operand.
	// === / !== fall through to the generic icmp-ptr-identity path below
	// (Symbol reuses IsObject's struct representation, which that path
	// already handles), so only the reject needs to be explicit here.
	if left.Ty.IsSymbol || right.Ty.IsSymbol {
		switch ex.Op {
		case "==", "!=", "===", "!==":
		default:
			return Value{}, fmt.Errorf("%d:%d: operator '%s' is not supported on symbol — only ===/!== are meaningful", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
		}
	}

	// BigInt (TDD-00074): its own operator set, and deliberately NOT
	// interoperable with number — mixing them in an arithmetic/bitwise operator
	// is a TypeError in JS, here a clean compile error. Across types only
	// ===/!== are defined (a bigint is never === a non-bigint), so those resolve
	// to a constant; everything else with exactly one bigint operand is rejected.
	// `string + bigint` / `bigint + string` is concatenation (the bigint
	// stringifies to its digits), handled by the string-concat path below — not
	// bigint arithmetic. Everything else with a bigint operand is.
	bigintConcat := ex.Op == "+" && (isStringTy(left.Ty) || isStringTy(right.Ty))
	if (left.Ty.IsBigInt || right.Ty.IsBigInt) && !bigintConcat {
		if left.Ty.IsBigInt != right.Ty.IsBigInt {
			return e.emitBigIntMixed(ex.Op, left, right, ex.GetPos())
		}
		return e.emitBigIntBinary(ex.Op, left, right, ex.GetPos())
	}

	// An array compared against null/undefined (e.g. RegExp.exec()'s
	// `T[] | null` — emitRegexExec's null-array sentinel, {ptr: null,
	// len: 0}) needs its own path: an array value is a {ptr,i64}
	// aggregate, which the generic icmp-based comparison further down
	// (keyed on ty.IR, "ptr" for an array type) cannot compare directly —
	// LLVM's icmp only ever accepts int/ptr/float operands, never an
	// aggregate, a hard clang-stage failure otherwise. Found as a real,
	// pre-existing gap (not RegExp-specific — any `T[] | null` comparison
	// would have hit this) while wiring Stage 2's `.exec()`. See
	// ADR-00116. Only the ptr half of the aggregate is ever compared;
	// general array-vs-array equality (a separate, still-unsupported gap)
	// is untouched.
	if (left.Ty.IsArray && right.Ty.IsNull) || (left.Ty.IsNull && right.Ty.IsArray) {
		arrVal := left
		if left.Ty.IsNull {
			arrVal = right
		}
		var cmpOp string
		switch ex.Op {
		case "==", "===":
			cmpOp = "eq"
		case "!=", "!==":
			cmpOp = "ne"
		default:
			return Value{}, fmt.Errorf("%d:%d: operator '%s' is not supported between an array and null", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
		}
		ptrReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, arrVal.Ref))
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp %s ptr %s, null", reg, cmpOp, ptrReg))
		return Value{Ref: reg, Ty: TypeBool}, nil
	}

	// "+" with exactly one string-typed operand is string concatenation
	// with the other operand implicitly stringified, matching real JS
	// (e.g. `"tick " + count`, `count + " tick"`). Must be handled before
	// the generic coerce step below: that step assumes both operands are
	// already the same representation and just reinterprets one as the
	// other's type, which silently produces invalid IR here instead —
	// e.g. `"x" + 5` would try to pass the raw i64 5 to strlen() as if it
	// were already a string pointer. Both-string and neither-string cases
	// fall through unchanged to the existing logic below.
	if ex.Op == "+" && isStringTy(left.Ty) != isStringTy(right.Ty) {
		if !isStringTy(left.Ty) {
			left, err = e.emitValueToString(left)
			if err != nil {
				return Value{}, err
			}
		}
		if !isStringTy(right.Ty) {
			right, err = e.emitValueToString(right)
			if err != nil {
				return Value{}, err
			}
		}
		return e.emitStringBinary(ex.Op, left, right, ex.GetPos())
	}

	// Captured before coerce (below) overwrites right.Ty with left.Ty — needed
	// to tell "Date + Date" apart from "Date + number"/"number + Date" for
	// the Date-arithmetic rules right after.
	leftIsDate := left.Ty.IsDate
	rightIsDate := right.Ty.IsDate

	// Unify types (promote right to left's type for now)
	right = e.coerce(right, left.Ty)
	ty := left.Ty

	// String-specific operations: ptr that is not an object, array, closure, or null check.
	// Null/undefined comparisons fall through to icmp eq/ne below.
	isNullCheck := left.Ty.IsNull || right.Ty.IsNull
	if ty.IR == "ptr" && !ty.IsObject && !ty.IsArray && !ty.IsFunc && !isNullCheck {
		return e.emitStringBinary(ex.Op, left, right, ex.GetPos())
	}

	reg := e.freshReg()

	switch ex.Op {
	case "+":
		// Date arithmetic: exactly one side a Date means "add a duration (in
		// ms) to a timestamp", producing a new Date — a deliberate deviation
		// from real JS, where `+` on a Date coerces it to a string (its
		// default ToPrimitive hint) rather than adding numerically; that
		// quirk is far less useful than treating this compiler's Date (a
		// plain i64 under the hood) as plain numeric duration arithmetic.
		// Adding two Dates together has no sensible meaning (summing two
		// absolute timestamps), so it's rejected outright rather than
		// silently producing a nonsense sum.
		if leftIsDate && rightIsDate {
			return Value{}, fmt.Errorf("%d:%d: cannot add two Dates together; use 'a.getTime() - b.getTime()' (or 'a - b') for the difference in milliseconds", ex.GetPos().Line, ex.GetPos().Col)
		}
		resultTy := ty
		if leftIsDate || rightIsDate {
			resultTy = TypeDate
		}
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fadd %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = add %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		}
		return Value{Ref: reg, Ty: resultTy}, nil
	case "-":
		// Date - Date is a real, meaningful operation (real JS does this
		// too, via numeric ToPrimitive) — the difference in milliseconds,
		// a plain number, not a Date. Date - number subtracts a duration,
		// producing a new (earlier) Date — the same deliberate deviation
		// from real JS's string-coercing `-`... except `-` in real JS
		// actually always uses numeric ToPrimitive regardless of operand
		// order, so "number - Date" IS valid JS there (giving a number) —
		// but it has no sensible "duration" meaning in this compiler's
		// Date-arithmetic model (there's no such thing as "a number minus
		// an absolute timestamp, produce a new Date"), so it's rejected.
		if rightIsDate && !leftIsDate {
			return Value{}, fmt.Errorf("%d:%d: cannot subtract a Date from a number; write 'dateVar - amount' to subtract a duration, or 'a.getTime() - b.getTime()' for a difference", ex.GetPos().Line, ex.GetPos().Col)
		}
		resultTy := ty
		if leftIsDate && rightIsDate {
			resultTy = TypeI64
		} else if leftIsDate {
			resultTy = TypeDate
		}
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fsub %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = sub %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		}
		return Value{Ref: reg, Ty: resultTy}, nil
	case "*":
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fmul %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = mul %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		}
		return Value{Ref: reg, Ty: ty}, nil
	case "/":
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fdiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitDivZeroGuard(ty, left, right)
			if ty.Signed {
				e.emitInstr(fmt.Sprintf("%s = sdiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			} else {
				e.emitInstr(fmt.Sprintf("%s = udiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			}
		}
		return Value{Ref: reg, Ty: ty}, nil
	case "%":
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = frem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitDivZeroGuard(ty, left, right)
			if ty.Signed {
				e.emitInstr(fmt.Sprintf("%s = srem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			} else {
				e.emitInstr(fmt.Sprintf("%s = urem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			}
		}
		return Value{Ref: reg, Ty: ty}, nil
	case "**":
		// Exponentiation. Float operands (either side) use libm's pow() and
		// yield a float, matching Math.pow. Integer operands use an exact
		// i64 exponentiation-by-squaring helper and stay i64 — consistent with
		// this compiler's integer-arithmetic model for `number` (like `/`,
		// which truncates for i64 rather than producing JS's float). A negative
		// integer exponent yields 0 (1/base^|n| truncated), the integer-model
		// analogue of that same truncation; use an explicitly float-typed
		// operand for real fractional results.
		if ty.Float {
			f1 := e.coerce(left, TypeF64)
			f2 := e.coerce(right, TypeF64)
			e.ensureMathFuncs()
			e.emitInstr(fmt.Sprintf("%s = call double @pow(double %s, double %s)", reg, f1.Ref, f2.Ref))
			return Value{Ref: reg, Ty: TypeF64}, nil
		}
		e.ensureIPow()
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ipow(i64 %s, i64 %s)", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil

	case "<", ">", "<=", ">=", "==", "!=", "===", "!==":
		boolTy := TypeBool
		if ty.Float {
			// `!=`/`!==` use the *unordered* predicate `une` so that `NaN != x`
			// (including `NaN != NaN`) is true, matching JS — `one` (ordered)
			// wrongly returns false whenever either operand is NaN. `==`/`===`
			// stay ordered `oeq` (`NaN === NaN` is correctly false), and the
			// relational `< > <= >=` stay ordered (any NaN comparison is false),
			// both already matching JS.
			fop := map[string]string{
				"<": "olt", ">": "ogt", "<=": "ole", ">=": "oge",
				"==": "oeq", "!=": "une", "===": "oeq", "!==": "une",
			}[ex.Op]
			e.emitInstr(fmt.Sprintf("%s = fcmp %s %s %s, %s", reg, fop, ty.IR, left.Ref, right.Ref))
		} else if ty.Signed {
			iop := map[string]string{
				"<": "slt", ">": "sgt", "<=": "sle", ">=": "sge",
				"==": "eq", "!=": "ne", "===": "eq", "!==": "ne",
			}[ex.Op]
			e.emitInstr(fmt.Sprintf("%s = icmp %s %s %s, %s", reg, iop, ty.IR, left.Ref, right.Ref))
		} else {
			iop := map[string]string{
				"<": "ult", ">": "ugt", "<=": "ule", ">=": "uge",
				"==": "eq", "!=": "ne", "===": "eq", "!==": "ne",
			}[ex.Op]
			e.emitInstr(fmt.Sprintf("%s = icmp %s %s %s, %s", reg, iop, ty.IR, left.Ref, right.Ref))
		}
		return Value{Ref: reg, Ty: boolTy}, nil

	// && / || are handled up-front by emitShortCircuit (they short-circuit and
	// so must not fall through to the eager both-operands path above).

	// Bitwise — operands coerced to i64
	case "&":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case "|":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case "^":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = xor i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case "<<", ">>", ">>>":
		return e.emitBitShift(ex.Op, left, right)
	}

	return Value{}, fmt.Errorf("unknown binary operator '%s'", ex.Op)
}

// emitBitShift implements JS's shift-operator semantics (<<, >>, >>>), which
// operate on 32-bit integers, not this compiler's native 64-bit `number`:
// both operands are truncated to i32 (matching ToInt32/ToUint32's mod-2^32
// wraparound — trunc keeps exactly the low 32 bits regardless of sign), the
// shift count is masked to 0-31 (ToUint32(right) & 0x1F, which trunc+and
// gives directly since masking only depends on the low 5 bits), and the i32
// shift result is extended back to i64: sign-extended for << and >> (JS
// results are Int32, e.g. 1 << 31 === -2147483648), zero-extended for >>>
// (JS results are always a non-negative Uint32, e.g. -1 >>> 0 === 4294967295).
func (e *Emitter) emitBitShift(op string, left, right Value) (Value, error) {
	li := e.coerce(left, TypeI64)
	ri := e.coerce(right, TypeI64)

	l32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", l32, li.Ref))
	r32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", r32, ri.Ref))
	shiftAmt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i32 %s, 31", shiftAmt, r32))

	res32 := e.freshReg()
	switch op {
	case "<<":
		e.emitInstr(fmt.Sprintf("%s = shl i32 %s, %s", res32, l32, shiftAmt))
	case ">>":
		e.emitInstr(fmt.Sprintf("%s = ashr i32 %s, %s", res32, l32, shiftAmt))
	case ">>>":
		e.emitInstr(fmt.Sprintf("%s = lshr i32 %s, %s", res32, l32, shiftAmt))
	default:
		return Value{}, fmt.Errorf("unknown shift operator '%s'", op)
	}

	result := e.freshReg()
	if op == ">>>" {
		e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", result, res32))
	} else {
		e.emitInstr(fmt.Sprintf("%s = sext i32 %s to i64", result, res32))
	}
	return Value{Ref: result, Ty: TypeI64}, nil
}

// typeofString maps a compiled type to its TypeScript typeof string.
func typeofString(ty Type) string {
	switch {
	case ty.IsFunc:
		return "function"
	case ty.IsBigInt:
		return "bigint"
	case ty.IsSymbol:
		return "symbol"
	case ty.IsObject, ty.IsArray:
		return "object"
	case ty.IR == "i1":
		return "boolean"
	case ty.IR == "ptr":
		return "string"
	default:
		return "number"
	}
}

func (e *Emitter) emitUnary(ex *ast.UnaryExpression) (Value, error) {
	// typeof is resolved purely from the inferred type — no code emitted for the
	// argument — EXCEPT for any/unknown, where the concrete type can change at
	// runtime, so it must become a genuine runtime tag dispatch instead.
	if ex.Op == "typeof" {
		ty := e.inferExprType(ex.Arg)
		if ty.IsDynamic {
			val, err := e.emitExpr(ex.Arg)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynamicTypeof(val)
		}
		ptr := e.internString(typeofString(ty))
		return Value{Ref: ptr, Ty: TypePtr}, nil
	}

	arg, err := e.emitExpr(ex.Arg)
	if err != nil {
		return Value{}, err
	}
	if arg.Ty.IsBigInt {
		switch ex.Op {
		case "-":
			return e.emitBigIntUnary("neg", arg), nil
		case "~":
			return e.emitBigIntUnary("not", arg), nil
		case "+":
			return Value{}, fmt.Errorf("%d:%d: unary + is not defined on BigInt (a TypeError in JS) — use Number(x)", ex.GetPos().Line, ex.GetPos().Col)
		}
		// "!" falls through to the generic path below, whose toBool now handles
		// bigint truthiness (0n falsy, else truthy).
	}
	reg := e.freshReg()
	switch ex.Op {
	case "-":
		if arg.Ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fneg %s %s", reg, arg.Ty.IR, arg.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = sub %s 0, %s", reg, arg.Ty.IR, arg.Ref))
		}
		return Value{Ref: reg, Ty: arg.Ty}, nil
	case "!":
		b := e.toBool(arg)
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", reg, b.Ref))
		return Value{Ref: reg, Ty: TypeBool}, nil
	case "~":
		v := e.coerce(arg, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = xor i64 %s, -1", reg, v.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	}
	return Value{}, fmt.Errorf("unknown unary operator '%s'", ex.Op)
}

func (e *Emitter) emitUpdate(ex *ast.UpdateExpression) (Value, error) {
	ident, ok := ex.Arg.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("update expression requires an identifier")
	}
	sym, ok := e.lookup(ident.Name)
	if !ok {
		return Value{}, fmt.Errorf("undefined variable '%s'", ident.Name)
	}

	if sym.isNullableScalarLocal() {
		return e.emitNullableScalarUpdate(sym, ex)
	}

	oldReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", oldReg, sym.Ty.IR, sym.Ptr, sym.Ty.Align()))

	if sym.Ty.IsBigInt {
		e.ensureBigInt()
		one := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_from_i64(i64 1)", one))
		fn := "add"
		if ex.Op == "--" {
			fn = "sub"
		}
		nr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_bigint_%s(ptr %s, ptr %s)", nr, fn, oldReg, one))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nr, sym.Ptr))
		if ex.Prefix {
			return Value{Ref: nr, Ty: sym.Ty}, nil
		}
		return Value{Ref: oldReg, Ty: sym.Ty}, nil
	}

	newReg := e.freshReg()
	if ex.Op == "++" {
		if sym.Ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fadd %s %s, 1.0", newReg, sym.Ty.IR, oldReg))
		} else {
			e.emitInstr(fmt.Sprintf("%s = add %s %s, 1", newReg, sym.Ty.IR, oldReg))
		}
	} else {
		if sym.Ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fsub %s %s, 1.0", newReg, sym.Ty.IR, oldReg))
		} else {
			e.emitInstr(fmt.Sprintf("%s = sub %s %s, 1", newReg, sym.Ty.IR, oldReg))
		}
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, newReg, sym.Ptr, sym.Ty.Align()))

	if ex.Prefix {
		return Value{Ref: newReg, Ty: sym.Ty}, nil
	}
	return Value{Ref: oldReg, Ty: sym.Ty}, nil
}

// dateCompoundAssignGuard rejects compound-assigning one Date into another
// Date-typed storage location (e.g. `d += otherDate`). The natural result of
// Date +/- Date is a plain number (a duration or difference, see emitBinary),
// which doesn't fit back into a Date-typed variable/field/element. Must be
// called by the caller with the RHS's type captured BEFORE it gets coerced
// to the target type — coercing a plain-number RHS to a Date-typed target
// (as emitAssign's compound-assignment paths already do before calling
// emitArith) would otherwise stamp it with IsDate too, indistinguishable
// from a genuinely Date-typed RHS.
func dateCompoundAssignGuard(op string, targetIsDate, rhsIsDate bool) error {
	if targetIsDate && rhsIsDate && (op == "+=" || op == "-=") {
		return fmt.Errorf("cannot compound-assign a Date with '%s' — the result of Date +/- Date is a plain number (a duration), not a Date; use '.getTime()' on both sides instead", op)
	}
	return nil
}

func (e *Emitter) emitArith(op string, left, right Value, ty Type, pos ast.Pos) (Value, error) {
	// A string-typed compound-assignment target (`s += ...`) never reaches
	// emitBinary's own top-of-function string handling — every caller here
	// is a compound-assign path (emit_exprs_assign.go/emit_objects.go/
	// emit_classes.go) that computes its own cur/rhsVal and calls straight
	// into this function. Without this check, "+" fell through to the
	// generic `add`/`fadd` case below unconditionally — a hard clang-stage
	// "invalid operand type" on a `ptr` operand for even the plainest
	// `let s = "a"; s += "b"`, not just a mixed string/number case. Found
	// while building TDD-00059's own tagged-template example/tests, which
	// exercises a tag function accumulating a string result via `+=`. Only
	// "+" is meaningful for strings; every other arithmetic compound-assign
	// operator on a string target still gets emitStringBinary's own clean
	// "operator '%s' is not supported for strings" rejection instead of
	// silently emitting invalid IR the way "+" itself used to.
	if ty.IsBigInt {
		// Compound assignment on a bigint target (acc += 100n, x <<= 4n, …).
		// A non-bigint RHS would have been coerced to bigint by the caller,
		// producing an invalid handle — guard against that mixed case, which is
		// a TypeError in JS.
		if !right.Ty.IsBigInt {
			return Value{}, fmt.Errorf("%d:%d: cannot mix BigInt and other types in compound assignment '%s=' — convert explicitly", pos.Line, pos.Col, op)
		}
		return e.emitBigIntBinary(op, left, right, pos)
	}
	if isStringTy(ty) {
		l, r := left, right
		if op == "+" {
			var err error
			if !isStringTy(l.Ty) {
				if l, err = e.emitValueToString(l); err != nil {
					return Value{}, err
				}
			}
			if !isStringTy(r.Ty) {
				if r, err = e.emitValueToString(r); err != nil {
					return Value{}, err
				}
			}
		}
		return e.emitStringBinary(op, l, r, pos)
	}
	reg := e.freshReg()
	switch op {
	case "+":
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fadd %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = add %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		}
	case "-":
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fsub %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = sub %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		}
	case "*":
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fmul %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = mul %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		}
	case "/":
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fdiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitDivZeroGuard(ty, left, right)
			if ty.Signed {
				e.emitInstr(fmt.Sprintf("%s = sdiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			} else {
				e.emitInstr(fmt.Sprintf("%s = udiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			}
		}
	case "%":
		// Missing entirely until now — %= (PERCENT_ASSIGN) wasn't even a
		// lexer token before this same pass, so this case was simply never
		// reachable; see the lexer/token.go and lexer/lexer.go changes
		// alongside this one.
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = frem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitDivZeroGuard(ty, left, right)
			if ty.Signed {
				e.emitInstr(fmt.Sprintf("%s = srem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			} else {
				e.emitInstr(fmt.Sprintf("%s = urem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			}
		}
	case "&":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case "|":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case "^":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = xor i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case "**":
		// Backs `**=`; mirrors emitBinary's `**` — libm pow() for float, exact
		// i64 exponentiation-by-squaring otherwise. See emitBinary for the
		// integer-model rationale (negative exponent → 0, result stays i64).
		if ty.Float {
			f1 := e.coerce(left, TypeF64)
			f2 := e.coerce(right, TypeF64)
			e.ensureMathFuncs()
			e.emitInstr(fmt.Sprintf("%s = call double @pow(double %s, double %s)", reg, f1.Ref, f2.Ref))
			return Value{Ref: reg, Ty: TypeF64}, nil
		}
		e.ensureIPow()
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_ipow(i64 %s, i64 %s)", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case "<<", ">>", ">>>":
		return e.emitBitShift(op, left, right)
	default:
		return Value{}, fmt.Errorf("unknown arithmetic operator '%s'", op)
	}
	return Value{Ref: reg, Ty: ty}, nil
}

// emitShortCircuit emits a logical `&&`/`||` with real short-circuit semantics:
// the right operand is only evaluated when the left doesn't already decide the
// result (left falsy for `&&`, left truthy for `||`). Result type is i1 — the
// value-preserving form (`x || "default"` yielding the operand itself) would
// need a union result type this compiler doesn't have here; both operands are
// coerced to bool, matching typed TS where `&&`/`||` over booleans yield a
// boolean. Uses the alloca+store/load pattern (same as emitConditional/
// emitNullCoalesce) to avoid hand-tracking phi predecessor blocks — the left or
// right operand may itself span multiple blocks (a nested `&&`/`||`/ternary).
func (e *Emitter) emitShortCircuit(ex *ast.BinaryExpression) (Value, error) {
	// -compat=js (TDD-00075): `&&`/`||` are value-preserving — `a && b` yields
	// `b` (or the falsy `a`), `a || b` yields `a` (or `b`) — not a bool. Only
	// when both operands share a simple type, since a different-typed result
	// would be a union this compiler can't represent; mixed types fall through
	// to the bool form below. inferExprType already returns the left operand's
	// type for `&&`/`||`, which is exactly this value-preserving result type.
	if e.compatJS() {
		lt := e.inferExprType(ex.Left)
		if sameShortCircuitType(lt, e.inferExprType(ex.Right)) {
			return e.emitShortCircuitValue(ex, lt)
		}
	}
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))

	left, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}
	l := e.toBool(left)
	// The result defaults to the left operand's bool; it's overwritten only on
	// the path that evaluates the right operand.
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", l.Ref, resPtr))

	rhsL := e.freshLabel("sc.rhs")
	mergeL := e.freshLabel("sc.merge")

	// `&&`: evaluate rhs only when left is true. `||`: only when left is false.
	if ex.Op == "&&" {
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", l.Ref, rhsL, mergeL))
	} else {
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", l.Ref, mergeL, rhsL))
	}

	e.emitLabel(rhsL)
	right, err := e.emitExpr(ex.Right)
	if err != nil {
		return Value{}, err
	}
	r := e.toBool(right)
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", r.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resPtr))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// sameShortCircuitType reports whether two operands share a simple, single-slot
// type that -compat=js value-preserving `&&`/`||` can store either of into one
// alloca. Excludes aggregates (arrays/tuples/nullable-scalar/dynamic) and
// requires the same kind, so the value-preserving result (typed as the left
// operand) is sound.
func sameShortCircuitType(a, b Type) bool {
	if a.IR != b.IR {
		return false
	}
	if len(a.IR) > 0 && a.IR[0] == '{' { // an aggregate struct IR ({ptr,i64}, {i1,T}, …)
		return false
	}
	if a.IsArray || a.IsTuple || a.IsDynamic || isNullableScalar(a) {
		return false
	}
	return a.IsBigInt == b.IsBigInt && isStringTy(a) == isStringTy(b) &&
		a.IsObject == b.IsObject && a.Float == b.Float && a.ClassName == b.ClassName
}

// emitShortCircuitValue is emitShortCircuit's -compat=js value-preserving form:
// the result is the actual left or right operand value (type ty), not a bool,
// with the right operand still evaluated only on the non-short-circuit path.
func (e *Emitter) emitShortCircuitValue(ex *ast.BinaryExpression, ty Type) (Value, error) {
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, ty.IR, ty.Align()))

	left, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}
	left = e.coerce(left, ty)
	l := e.toBool(left)
	// Result defaults to the left value; overwritten only when the right runs.
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, left.Ref, resPtr, ty.Align()))

	rhsL := e.freshLabel("scv.rhs")
	mergeL := e.freshLabel("scv.merge")
	if ex.Op == "&&" {
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", l.Ref, rhsL, mergeL))
	} else {
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", l.Ref, mergeL, rhsL))
	}

	e.emitLabel(rhsL)
	right, err := e.emitExpr(ex.Right)
	if err != nil {
		return Value{}, err
	}
	right = e.coerce(right, ty)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, right.Ref, resPtr, ty.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, ty.IR, resPtr, ty.Align()))
	return Value{Ref: result, Ty: ty}, nil
}

// emitConditional emits a ternary expression cond ? consequent : alternate.
// Uses an alloca+store/load pattern so both branches can produce a single result.
func (e *Emitter) emitConditional(ex *ast.ConditionalExpression) (Value, error) {
	ty := e.inferExprType(ex.Consequent)
	if ty.IsArray {
		return Value{}, fmt.Errorf("%d:%d: ternary operator is not supported for array types", ex.GetPos().Line, ex.GetPos().Col)
	}

	thenL := e.freshLabel("ternary.then")
	elseL := e.freshLabel("ternary.else")
	mergeL := e.freshLabel("ternary.merge")

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, ty.IR, ty.Align()))

	cond, err := e.emitExpr(ex.Test)
	if err != nil {
		return Value{}, err
	}
	cond = e.toBool(cond)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, thenL, elseL))

	e.emitLabel(thenL)
	thenVal, err := e.emitExpr(ex.Consequent)
	if err != nil {
		return Value{}, err
	}
	thenVal = e.coerce(thenVal, ty)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, thenVal.Ref, resPtr, ty.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(elseL)
	elseVal, err := e.emitExpr(ex.Alternate)
	if err != nil {
		return Value{}, err
	}
	elseVal = e.coerce(elseVal, ty)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, elseVal.Ref, resPtr, ty.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, ty.IR, resPtr, ty.Align()))
	return Value{Ref: result, Ty: ty}, nil
}

// zeroRef returns the LLVM IR zero/null constant for a type.
func zeroRef(ty Type) string {
	switch {
	case ty.IsDynamic:
		return "zeroinitializer"
	case ty.IR == "ptr":
		return "null"
	case ty.IR == "i1":
		return "false"
	case ty.Float:
		return "0.0"
	default:
		return "0"
	}
}

// emitNullCoalesce emits `left ?? right`. For ptr types it emits a null check
// so the right side is only evaluated when left is null. For non-ptr types left
// can never be null, so right is never evaluated.
func (e *Emitter) emitNullCoalesce(ex *ast.BinaryExpression) (Value, error) {
	// Nullable non-pointer scalar left operand (TDD-00064): it carries no null
	// pointer to test, so consult its stored presence bit and only fall
	// through to the right side when it is absent. A value flow analysis has
	// already narrowed to non-null (Stage 2) is known present, so the right
	// side is skipped entirely.
	if sym, ok := e.nullableScalarLValue(ex.Left); ok {
		payloadReg := e.loadNullableScalarPayload(sym.Ptr, sym.Ty)
		payload := Value{Ref: payloadReg, Ty: sym.Ty.withoutNullable()}
		if sym.NarrowedNonNull {
			return payload, nil
		}
		present := e.loadNullableScalarPresent(sym.Ptr, sym.Ty)
		return e.emitNullCoalesceScalar(present, payload, ex.Right)
	}
	left, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}
	// A nullable-scalar aggregate left operand (a T|null return/field value):
	// branch on its presence bit, same as the lvalue path above.
	if isNullableScalar(left.Ty) {
		present, payload := e.nullableScalarAggParts(left)
		return e.emitNullCoalesceScalar(present, payload, ex.Right)
	}
	if left.Ty.IR != "ptr" {
		return left, nil
	}

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, left.Ref))

	nullL := e.freshLabel("nullc.null")
	noNullL := e.freshLabel("nullc.nn")
	mergeL := e.freshLabel("nullc.merge")

	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, noNullL))

	e.emitLabel(nullL)
	right, err := e.emitExpr(ex.Right)
	if err != nil {
		return Value{}, err
	}
	right = e.coerce(right, TypePtr)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", right.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(noNullL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", left.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: TypePtr}, nil
}

package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// isConstLiteralExpr reports whether expr is a compile-time primitive literal
// (null/undefined, number, string, boolean) — used to gate the null/undefined
// equality constant-fold to operands that are genuinely known at compile time,
// never a runtime value that merely typed as nullish.
func isConstLiteralExpr(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.NullLiteral, *ast.NumberLiteral, *ast.StringLiteral, *ast.BooleanLiteral:
		return true
	}
	return false
}

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

	// An operand that unconditionally terminates the block — e.g. a throwing
	// IIFE in `(function(){throw…})() & x` (Test262 bitwise `order-of-evaluation`)
	// — leaves e.blockDone set and its own Value empty. Everything below this
	// point (coercion, the bitwise `trunc`, the operator dispatch) would emit
	// into dead space; because a later statement's label reopens a live block,
	// those dropped instructions leave dangling operands (`trunc i64  to i32`).
	// The whole binary expression is unreachable here, so short-circuit with a
	// placeholder rather than emit anything.
	if e.blockDone {
		return Value{Ref: "0", Ty: TypeI64}, nil
	}

	// A caught value (TypeCaught ≈ `unknown`, TDD-00202) in an equality compares
	// by packing to `any` and reusing the any equality; the caught side may be
	// either operand (`e === "x"` or `"x" === e`).
	if (left.Ty.IsCaught || right.Ty.IsCaught) &&
		(ex.Op == "==" || ex.Op == "!=" || ex.Op == "===" || ex.Op == "!==") {
		negate := ex.Op == "!=" || ex.Op == "!=="
		loose := ex.Op == "==" || ex.Op == "!="
		if left.Ty.IsCaught {
			return e.emitCaughtEquals(left, right, negate, loose)
		}
		return e.emitCaughtEquals(right, left, negate, loose)
	}

	// TDD-00201: an object operand coerces via the ToPrimitive ladder
	// (@@toPrimitive/valueOf/toString) before the operator logic runs. Bitwise/
	// shift ops already coerce operands to i64 (running the same ladder through
	// coerce), so they are not listed here. Reference `===`/`!==` never coerces.
	switch ex.Op {
	case "-", "*", "/", "%", "**", "<", ">", "<=", ">=", "&", "|", "^", "<<", ">>", ">>>":
		// Stage 1 — number hint: the result is used numerically, coerce to double.
		// Bitwise/shift ops (ToInt32/ToUint32) share the number hint: an object
		// operand must run the ToPrimitive ladder before being truncated to i32,
		// otherwise toInt32/emitBitShift would `trunc` a raw object pointer to i32
		// (invalid IR). A method-less object falls through to the strict reject /
		// compat=js dispatch below, exactly like arithmetic.
		if objectMayToPrimitive(left.Ty) {
			if r, ok, perr := e.emitObjectToPrimitive(left, "number"); perr == nil && ok {
				left = e.coerce(r, TypeF64)
			}
		}
		if objectMayToPrimitive(right.Ty) {
			if r, ok, perr := e.emitObjectToPrimitive(right, "number"); perr == nil && ok {
				right = e.coerce(r, TypeF64)
			}
		}
	case "+":
		// Stage 3 — default hint: `+` overloads to string concat or numeric add.
		// ToPrimitive with the "default" hint (valueOf→toString) yields the raw
		// primitive (number OR string); the existing +overload logic below then
		// decides concat vs add (`{valueOf(){return 5}}+3===8`,
		// `{toString(){return "a"}}+"b"==="ab"`).
		if objectMayToPrimitive(left.Ty) {
			if r, ok, perr := e.emitObjectToPrimitive(left, "default"); perr == nil && ok {
				left = r
			}
		}
		if objectMayToPrimitive(right.Ty) {
			if r, ok, perr := e.emitObjectToPrimitive(right, "default"); perr == nil && ok {
				right = r
			}
		}
	case "==", "!=":
		// Stage 3 — loose equality: `obj == primitive` runs ToPrimitive on the
		// object (default hint). Only when the OTHER side is a number/string/
		// bigint — `obj == null`/`undefined` is false without coercion, and
		// `obj == obj` is reference equality, both left untouched.
		if objectMayToPrimitive(left.Ty) && isLooseEqPrimitive(right.Ty) {
			if r, ok, perr := e.emitObjectToPrimitive(left, "default"); perr == nil && ok {
				left = r
			}
		} else if objectMayToPrimitive(right.Ty) && isLooseEqPrimitive(left.Ty) {
			if r, ok, perr := e.emitObjectToPrimitive(right, "default"); perr == nil && ok {
				right = r
			}
		}
	}

	// A nullable-scalar aggregate operand (a T|null return/field value) that
	// reached here is not a `=== null` comparison (those returned above) — it
	// is ordinary arithmetic/comparison, which operates on the bare payload
	// (a null reads as its zero, the same lenient collapse a local read does).
	// The exception is string concatenation (`"x" + (n: number|null)`): the
	// operand must reach emitValueToString as the full aggregate so a null
	// renders "null" (real JS), not the payload's zero (ADR-00537). Keep it
	// intact here and let the concat branch below stringify it.
	// `-compat=js` (TDD-00076 A2): `null/undefined + number` is numeric in JS
	// (`null + 1 === 1`, `undefined + 1 → NaN`) — route it to the dynamic
	// dispatch before the concat decision below would stringify the nullish
	// side. A nullish + *string* pair stays concatenation, as in JS.
	if e.compatJS() && ex.Op == "+" {
		lNullish := (left.Ty.IsNull || left.Ty.IsUndefined) && !left.Ty.UncheckedIndex
		rNullish := (right.Ty.IsNull || right.Ty.IsUndefined) && !right.Ty.UncheckedIndex
		if (lNullish && scalarTypeKind(right.Ty) == "number") || (rNullish && scalarTypeKind(left.Ty) == "number") {
			return e.emitAnyBinary("+", left, right, ex.GetPos())
		}
	}
	// Presence-aware equality on a nullable-scalar aggregate: `arr.pop() === 0`
	// on an empty array must be false (the value is undefined, not the payload
	// zero the lenient collapse below would compare). Two aggregates compare
	// equal when both absent or both present with equal payloads.
	if (ex.Op == "==" || ex.Op == "===" || ex.Op == "!=" || ex.Op == "!==") &&
		(isNullableScalar(left.Ty) || isNullableScalar(right.Ty)) &&
		!left.Ty.IsNull && !right.Ty.IsNull &&
		nullableScalarEqComparable(left.Ty) && nullableScalarEqComparable(right.Ty) {
		return e.emitNullableScalarValueEq(ex.Op, left, right)
	}

	strConcatToStr := ex.Op == "+" && (isStringTy(left.Ty) || isStringTy(right.Ty))
	if isNullableScalar(left.Ty) && !strConcatToStr {
		left = e.nullableScalarOperand(left, ex.Op)
	}
	if isNullableScalar(right.Ty) && !strConcatToStr {
		right = e.nullableScalarOperand(right, ex.Op)
	}
	// A `T | undefined` *local* read auto-unwraps to its payload in emitIdent,
	// so its absence is invisible here. Reload the aggregate from
	// storage (a side-effect-free lvalue re-load) and let nullableScalarOperand
	// turn an absent operand into NaN where JS's ToNumber(undefined) would.
	if !strConcatToStr {
		left = e.jsUndefinedLocalOperand(ex.Left, left, ex.Op)
		right = e.jsUndefinedLocalOperand(ex.Right, right, ex.Op)
	}

	if left.Ty.IsDynamic || right.Ty.IsDynamic {
		// String concatenation with a dynamic operand goes through the JS
		// ToString coercion (TDD-00155 Stage 4) — `"hi " + anyVal` and
		// `anyVal + "!"` render the runtime value via the tag dispatch,
		// exactly like a template literal already does. Arithmetic between
		// two dynamic operands stays rejected (real operator dispatch is
		// TDD-00076's NaN-box territory).
		if ex.Op == "+" && (isStringTy(left.Ty) || isStringTy(right.Ty)) {
			ls, err := e.emitValueToString(left)
			if err != nil {
				return Value{}, err
			}
			rs, err := e.emitValueToString(right)
			if err != nil {
				return Value{}, err
			}
			return e.emitStringConcat(ls, rs)
		}
		switch ex.Op {
		case "===":
			return e.emitAnyEquals(left, right, false)
		case "!==":
			return e.emitAnyEquals(left, right, true)
		case "==":
			// Loose equality coerces (TDD-00201 Stage 4): the JS Abstract
			// Equality Comparison, incl. ToPrimitive on an object operand.
			return e.emitAnyLooseEquals(left, right, false)
		case "!=":
			return e.emitAnyLooseEquals(left, right, true)
		default:
			// Real runtime operator dispatch on the NaN-boxed word
			// (TDD-00076 A2): TypeScript allows every operator on `any`, and
			// the checker rejects one on `unknown`.
			return e.emitAnyBinary(ex.Op, left, right, ex.GetPos())
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
	// A *nullable* bigint compared against null/undefined is a null-pointer check
	// (a bigint value is a heap pointer, null when the T|null slot is empty) —
	// not the constant-false the general bigint-vs-non-bigint rule below would
	// give. Without this a `bigint | null` that actually holds null never tests
	// `=== null` true. Same shape as the array-vs-null case below.
	if ((left.Ty.IsBigInt && left.Ty.Nullable && right.Ty.IsNull) ||
		(right.Ty.IsBigInt && right.Ty.Nullable && left.Ty.IsNull)) &&
		(ex.Op == "==" || ex.Op == "===" || ex.Op == "!=" || ex.Op == "!==") {
		biVal := left
		if left.Ty.IsNull {
			biVal = right
		}
		cmpOp := "eq"
		if ex.Op == "!=" || ex.Op == "!==" {
			cmpOp = "ne"
		}
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp %s ptr %s, null", reg, cmpOp, biVal.Ref))
		return Value{Ref: reg, Ty: TypeBool}, nil
	}

	bigintConcat := ex.Op == "+" && (isStringTy(left.Ty) || isStringTy(right.Ty))
	if (left.Ty.IsBigInt || right.Ty.IsBigInt) && !bigintConcat {
		if left.Ty.IsBigInt != right.Ty.IsBigInt {
			return e.emitBigIntMixed(ex.Op, left, right, ex.GetPos())
		}
		return e.emitBigIntBinary(ex.Op, left, right, ex.GetPos())
	}

	// Bare null/undefined in an arithmetic/relational operator. Both literals are
	// `ptr`-typed, so the coerce/string paths below would either stringify them or
	// emit `add ptr null, null` (invalid IR). JS applies ToNumber (null→0,
	// undefined→NaN); TypeScript rejects the operator on these types (ADR-00901).
	// Placed before the string-concat and coerce steps; equality (`== === != !==`)
	// is excluded here and handled by the null-check/array-null paths, and a
	// string operand (`"x" + null`) keeps its concatenation branch.
	// `isStringTy` matches ANY bare `ptr`, so it wrongly classifies a null/
	// undefined literal as a string. For a *real* string operand the distinction
	// matters: `"x" + null` concatenates ("xnull"), while `null + undefined` is
	// arithmetic. A real string is a string-typed operand that is not itself
	// null/undefined.
	// A void-typed operand (a call to a spec-undefined-returning method used in a
	// value position) is `undefined` (ADR-00900), so it counts as nullish here —
	// `set.clear() === undefined` is true, not a comparison of a concrete value.
	// A *nullableScalar* (`T | null`) is excluded: it is a runtime aggregate with a
	// presence bit, not a compile-time null/undefined literal, and is handled by
	// emitNullableScalarNullCompare / the presence-aware paths — folding it here
	// would treat a possibly-present value as constant-null (regression found in
	// `writer.desiredSize === null`).
	lNullish := isNullishLiteralTy(left.Ty) && !isNullableScalar(left.Ty)
	rNullish := isNullishLiteralTy(right.Ty) && !isNullableScalar(right.Ty)
	lRealStr := isStringTy(left.Ty) && !lNullish
	rRealStr := isStringTy(right.Ty) && !rNullish
	if (lNullish || rNullish) && !lRealStr && !rRealStr &&
		!isNullableScalar(left.Ty) && !isNullableScalar(right.Ty) &&
		!left.Ty.IsObject && !right.Ty.IsObject && !left.Ty.IsArray && !right.Ty.IsArray {
		// Arithmetic/relational on bare null/undefined: JS applies ToNumber
		// (null→0, undefined→NaN); TypeScript rejects the operator on these types
		// (ADR-00901). Both are `ptr`, so the string/coerce paths below would emit
		// `add ptr null, null` (invalid IR). Equality and logical ops fall through
		// to the null-check paths; a real-string operand keeps its concat branch.
		switch ex.Op {
		case "+", "-", "*", "/", "%", "**", "<", ">", "<=", ">=":
			if e.compatJS() {
				return e.emitAnyBinary(ex.Op, left, right, ex.GetPos())
			}
			return Value{}, fmt.Errorf("%d:%d: operator '%s' on null/undefined is not supported in strict mode — TypeScript reports the same error; compile with -compat=js to apply JS ToNumber coercion (null→0, undefined→NaN)", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
		case "==", "!=", "===", "!==":
			// null/undefined is equal (loosely or strictly) only to null/undefined.
			// With one nullish side and a concrete non-nullish scalar (`undefined ==
			// 0`, `null === true`) the result is a constant: false for ==/===, true
			// for !=/!==. Both-nullish: loose == treats null and undefined as equal;
			// strict === requires the SAME nullish kind. Folding this here avoids the
			// generic path's `icmp eq ptr null, <number>` (invalid IR — ADR-00909).
			//
			// Restricted to LITERAL operands: a runtime value that merely typed as
			// nullish (e.g. a `T|null` nullableScalar already collapsed to its bare
			// payload by the time it reaches here) must NOT be folded to a compile-
			// time constant — that wrongly made `writer.desiredSize === null` false.
			// A genuine `null`/`undefined`/number/string/bool literal is safe.
			if isConstLiteralExpr(ex.Left) && isConstLiteralExpr(ex.Right) {
				equal := false
				if lNullish && rNullish {
					if ex.Op == "==" || ex.Op == "!=" {
						equal = true // null == undefined (loose) is true
					} else {
						equal = left.Ty.IsNull == right.Ty.IsNull // strict: same kind
					}
				}
				if ex.Op == "!=" || ex.Op == "!==" {
					equal = !equal
				}
				res := "0"
				if equal {
					res = "1"
				}
				return Value{Ref: res, Ty: TypeBool}, nil
			}
		}
	}

	// Non-additive arithmetic with a real-string operand (`"b" * null`,
	// `"5" - 1`): JS applies ToNumber to each operand (a non-numeric string →
	// NaN), so the result is numeric, not a concatenation. The numeric coerce
	// path below would emit `mul ptr <str>, …` (invalid IR) since a string is a
	// bare `ptr`. Route through the NaN-boxed runtime ToNumber arithmetic under
	// compat=js; strict rejects (TypeScript does too). `+` stays concatenation,
	// and relational (`<`, `>`, …) stays a string comparison — both excluded.
	if (lRealStr || rRealStr) && !left.Ty.IsDynamic && !right.Ty.IsDynamic {
		switch ex.Op {
		case "-", "*", "/", "%", "**":
			if e.compatJS() {
				return e.emitAnyBinary(ex.Op, left, right, ex.GetPos())
			}
			return Value{}, fmt.Errorf("%d:%d: arithmetic operator '%s' on a string operand is not supported in strict mode — TypeScript reports the same error; compile with -compat=js to apply JS ToNumber coercion", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
		}
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
	// The bare-nullish side is null, undefined, or a void-typed (spec-undefined)
	// operand — an array-valued Map miss reads as `T[] | undefined` (a {null,0}
	// aggregate), so `map.get(miss) === undefined` must test the data pointer
	// for null exactly as `=== null` does, rather than falling through to the
	// aggregate-hostile generic path.
	leftArrNullish := right.Ty.IsNull || right.Ty.IsUndefined || right.Ty.IR == "void"
	rightArrNullish := left.Ty.IsNull || left.Ty.IsUndefined || left.Ty.IR == "void"
	if (left.Ty.IsArray && leftArrNullish) || (rightArrNullish && right.Ty.IsArray) {
		arrVal := left
		if !left.Ty.IsArray {
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
		// A non-nullable transient (a literal, a slice/HOF result) is never
		// null/undefined — an empty one included, whatever its data pointer.
		if !arrVal.Ty.Nullable && arrVal.ArrayHeader == "" {
			if cmpOp == "eq" {
				return Value{Ref: "false", Ty: TypeBool}, nil
			}
			return Value{Ref: "true", Ty: TypeBool}, nil
		}
		// Absence is the null header (a `null` binding, an omitted argument, a Map
		// miss, an absent field), or a null data pointer for a header-less
		// transient (a RegExp miss) — emitArrayIsAbsent.
		reg := e.emitArrayIsAbsent(arrVal)
		if cmpOp == "ne" {
			ne := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", ne, reg))
			reg = ne
		}
		return Value{Ref: reg, Ty: TypeBool}, nil
	}

	// Array-vs-array identity `===`/`!==` (TDD-00213 Stage 1): arrays are
	// reference types, so identity is the shared header cell — `let a = b`
	// aliases and stays `===`; two distinct arrays (even equal contents) differ.
	// The identity pointer is the value's live header (Value.ArrayHeader, the
	// object-reference model TDD-00127) when it has one, else the transient's own
	// data pointer. Previously this emitted `icmp eq ptr <{ptr,i64} aggregate>`
	// (invalid IR) — a known gap; a bare register compared directly.
	if left.Ty.IsArray && right.Ty.IsArray {
		var cmpOp string
		switch ex.Op {
		case "==", "===":
			cmpOp = "eq"
		case "!=", "!==":
			cmpOp = "ne"
		default:
			return Value{}, fmt.Errorf("%d:%d: operator '%s' is not supported between two arrays (only identity ===/!==)", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
		}
		idPtr := func(v Value) string {
			if v.ArrayHeader != "" {
				return v.ArrayHeader
			}
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", r, v.Ref))
			return r
		}
		reg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp %s ptr %s, %s", reg, cmpOp, idPtr(left), idPtr(right)))
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
	// Use the real-string test (not isStringTy, which matches bare null/undefined
	// too): `"x" + null` must concatenate ("xnull"), stringifying the nullish side
	// via emitConcatOperandString, rather than reading as string==string and
	// falling through to the arithmetic `add ptr` (ADR-00901).
	if ex.Op == "+" && lRealStr != rRealStr {
		if !lRealStr {
			left, err = e.emitConcatOperandString(ex.Left, left)
			if err != nil {
				return Value{}, err
			}
		}
		if !rRealStr {
			right, err = e.emitConcatOperandString(ex.Right, right)
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

	// Numeric promotion first: for arithmetic and ordering/equality with one
	// float and one integer operand, BOTH promote to double — real JS
	// arithmetic is double, and the old left-biased unification silently
	// `fptosi`'d a float RIGHT operand into the left's integer type
	// (`i * 1.5` computed `i * 1`, `3 === 3.5` compared `3 === 3`).
	// Bitwise/shift ops are deliberately excluded: JS ToInt32-truncates
	// there, which the integer path already approximates. Dates keep their
	// own duration-arithmetic rules below.
	if !leftIsDate && !rightIsDate && left.Ty.Float != right.Ty.Float &&
		(left.Ty.Float || left.Ty.IsInteger() || left.Ty.IR == "i64") &&
		(right.Ty.Float || right.Ty.IsInteger() || right.Ty.IR == "i64") {
		switch ex.Op {
		case "+", "-", "*", "/", "%", "**", "<", ">", "<=", ">=", "==", "!=", "===", "!==":
			left = e.coerce(left, TypeF64)
			right = e.coerce(right, TypeF64)
		}
	}

	// Date duration arithmetic runs in the i64 millisecond domain regardless of
	// operand order — a `number` operand is a float64 (TDD-00123), so coerce
	// both sides to i64 so `500 + d1` computes with `add i64`, not `fadd double`
	// (which would then be mislabeled as a Date). The resultTy below still
	// becomes TypeDate from the leftIsDate/rightIsDate flags captured above.
	if leftIsDate || rightIsDate {
		left = e.coerce(left, TypeI64)
		right = e.coerce(right, TypeI64)
	}

	// -compat=js loose `==`/`!=` between two static operands of different storage
	// kinds (`"" == 0`, `null == 0`, `5 == "5"`) is JS Abstract Equality — box
	// both and run the loose comparison, rather than the string/null/reject paths
	// below which assume matching kinds. Placed after numeric promotion so an
	// int/float pair (already unified to double) is not diverted; dynamic operands
	// were handled earlier. (TDD-00201 Stage 4.)
	if e.compatJS() && (ex.Op == "==" || ex.Op == "!=") && left.Ty.IR != right.Ty.IR {
		return e.emitAnyLooseEquals(left, right, ex.Op == "!=")
	}

	// Unify types (promote right to left's type for now)
	// null or undefined against a present number or boolean: never equal,
	// loosely or strictly (`null == 0` is false). Comparing them would pit a
	// pointer against a scalar constant.
	if ex.Op == "==" || ex.Op == "===" || ex.Op == "!=" || ex.Op == "!==" {
		nullish := func(t Type) bool { return (t.IsNull || t.IsUndefined || t.IR == "void") && !isNullableScalar(t) }
		present := func(t Type) bool {
			return !t.IsDynamic && !t.Nullable && !t.IsNull && !t.IsUndefined && !t.IsBigInt &&
				(t.IR == "i1" || isNumberTy(t)) && t.IR != "ptr"
		}
		// Strictly, a `null` is never an `undefined` (both are a null pointer
		// at run time, so only their types tell them apart: TypeNull, and
		// TypeUndefined, which also carries IsNull).
		onlyNull := func(t Type) bool { return t.IsNull && !t.IsUndefined && !t.Nullable && !t.IsDynamic }
		onlyUndef := func(t Type) bool {
			return (t.IsUndefined && t.IR == "ptr" || t.IR == "void") && !t.Nullable && !t.IsDynamic
		}
		strictNullUndef := (ex.Op == "===" || ex.Op == "!==") &&
			(onlyNull(left.Ty) && onlyUndef(right.Ty) || onlyUndef(left.Ty) && onlyNull(right.Ty))
		// Only a nullish *binding* (`var n = null`, a switch discriminant): a
		// literal `null`/`undefined` keeps the comparison below, which also
		// reads a `number | null` held as a plain number (`w.desiredSize`).
		literal := func(x ast.Expression) bool {
			if _, ok := x.(*ast.NullLiteral); ok {
				return true
			}
			return isUndefinedLiteral(e, x)
		}
		lBind, rBind := !literal(ex.Left), !literal(ex.Right)
		if lBind && rBind && strictNullUndef ||
			lBind && nullish(left.Ty) && present(right.Ty) || rBind && nullish(right.Ty) && present(left.Ty) {
			if ex.Op == "!=" || ex.Op == "!==" {
				return Value{Ref: "true", Ty: TypeBool}, nil
			}
			return Value{Ref: "false", Ty: TypeBool}, nil
		}
	}

	// Bitwise and shift operators take each operand through ToInt32 on its
	// own (toInt32), so neither is converted to the other's type first: a
	// `Uint8Array` element `|` a shifted double must not truncate the double
	// to a byte.
	if isNumericOperand(left.Ty) && isNumericOperand(right.Ty) {
		switch ex.Op {
		case "&", "|", "^":
			reg := e.freshReg()
			l32, r32 := e.toInt32(left), e.toInt32(right)
			iop := map[string]string{"&": "and", "|": "or", "^": "xor"}[ex.Op]
			e.emitInstr(fmt.Sprintf("%s = %s i32 %s, %s", reg, iop, l32, r32))
			return e.int32ToNumber(reg), nil
		case "<<", ">>", ">>>":
			return e.emitBitShift(ex.Op, left, right)
		}
	}
	right = e.coerce(right, left.Ty)
	ty := left.Ty

	// String-specific operations: ptr that is not an object, array, closure, or null check.
	// Null/undefined comparisons fall through to icmp eq/ne below.
	isNullCheck := left.Ty.IsNull || right.Ty.IsNull
	// The right operand must also be a pointer: a mixed pair such as
	// `"-1" == -1` (string vs number) leaves `right` a bare `double` after the
	// no-op coerce above (there is no number→string conversion), and passing it
	// to emitStringBinary emitted an `icmp eq ptr %d, null` on that double —
	// invalid IR. Requiring `right.Ty.IR == "ptr"` diverts such a mixed pair to
	// the cross-type handling below (a clean strict reject — TS reports the same
	// no-overlap error — or the -compat=js Abstract-Equality box path).
	if ty.IR == "ptr" && right.Ty.IR == "ptr" && !ty.IsObject && !ty.IsArray && !ty.IsFunc && !isNullCheck &&
		!ty.IsFFIFunction && !right.Ty.IsFFIFunction { // a bound native function compares by identity (TDD-00229)
		return e.emitStringBinary(ex.Op, left, right, ex.GetPos())
	}

	// Equality between a heap object/array (ptr) and a bare scalar (non-ptr) that
	// survived unification — e.g. `obj === 5` (`===` never coerces) or a loose
	// `obj == 5` on an object with no numeric ToPrimitive method. The types are
	// disjoint (an object is never strict-equal to a number), so emitting the
	// icmp would compare a `ptr` against a scalar constant — invalid IR. Reject
	// cleanly, consistent with the disjoint-scalar rejection below and TS's own
	// no-overlap comparison error. Object-vs-object / object-vs-string stay ptr==
	// ptr comparisons; null/undefined checks are exempt.
	{
		lObjArr := ty.IsObject || ty.IsArray
		rObjArr := right.Ty.IsObject || right.Ty.IsArray
		if !isNullCheck && !ty.IsDynamic && !right.Ty.IsDynamic && !leftIsDate && !rightIsDate && (lObjArr || rObjArr) {
			switch ex.Op {
			case "==", "!=", "===", "!==":
				// Object vs a disjoint scalar: an object is never equal to a
				// number/boolean, so the icmp would compare a `ptr` against a
				// scalar constant (invalid IR). Both-objects / object-vs-string
				// stay ptr==ptr comparisons; only one-object-vs-non-ptr rejects.
				if lObjArr != rObjArr {
					other := right.Ty
					if rObjArr {
						other = ty
					}
					if other.IR != "ptr" {
						return Value{}, fmt.Errorf("%d:%d: operator '%s' between an object and a disjoint scalar type is not supported in strict mode — an object is never equal to a number/boolean, so this comparison is always %s (TypeScript reports the same no-overlap error); compile with -compat=js to evaluate it as untyped JS would", ex.GetPos().Line, ex.GetPos().Col, ex.Op, disjointEqConstResult(ex.Op))
					}
				}
			case "+", "-", "*", "/", "%", "**", "<", ">", "<=", ">=", "&", "|", "^", "<<", ">>", ">>>":
				// An object/array operand reaching here has no own numeric
				// ToPrimitive method (one with valueOf/toString was already
				// coerced by the pre-pass above), so it has no primitive value in
				// an arithmetic/relational/bitwise context. TypeScript reports the
				// same error; emitting the op would combine a `ptr` with a scalar
				// (invalid IR). Reject cleanly, naming the -compat=js hatch.
				return Value{}, fmt.Errorf("%d:%d: operator '%s' on an object without a primitive value (no valueOf/toString) is not supported in strict mode — TypeScript reports the same error; compile with -compat=js to coerce it as untyped JS would (valueOf/toString, else \"[object Object]\")", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
			}
		}
	}

	// After unification the operands still have incompatible storage types (e.g.
	// a number and a string, where coerce had no conversion): a cross-type
	// operation untyped JS permits but this typed subset — like TypeScript's own
	// checker for a cross-type `===`/arithmetic — does not. Reject cleanly rather
	// than emit an invalid mixed-type operand (`add i64 %n, <string const>`).
	// Null/undefined checks, dynamic (`any`) operands, and composite types
	// (handled by their own paths) are exempt.
	if !isNullCheck && right.Ty.IR != ty.IR &&
		!ty.IsObject && !ty.IsArray && !ty.IsFunc && !ty.IsDynamic && !ty.IsDate &&
		!right.Ty.IsObject && !right.Ty.IsArray && !right.Ty.IsFunc && !right.Ty.IsDynamic {
		// `-compat=js` (TDD-00076 A2): a mixed concrete pair (`n * "4"`,
		// `true + 1`) coerces exactly like JS — box both sides and dispatch.
		// strict keeps the typed-subset rejection.
		if e.compatJS() {
			switch ex.Op {
			case "+", "-", "*", "/", "%", "**", "<", ">", "<=", ">=":
				return e.emitAnyBinary(ex.Op, left, right, ex.GetPos())
			case "==":
				// Loose equality of a mixed concrete pair (`5 == "5"`) coerces
				// per JS Abstract Equality (TDD-00201 Stage 4).
				return e.emitAnyLooseEquals(left, right, false)
			case "!=":
				return e.emitAnyLooseEquals(left, right, true)
			}
		}
		return Value{}, fmt.Errorf("%d:%d: operator '%s' between incompatible types is not supported — this compiler is a typed subset (a value of one type cannot be combined with an incompatible one the way untyped JS allows)", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
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
			// frem lowers to a libcall to fmod — needs libm on Linux.
			e.requireLink("m")
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
			e.ensureJsPow()
			e.emitInstr(fmt.Sprintf("%s = call double @__kml_js_pow(double %s, double %s)", reg, f1.Ref, f2.Ref))
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

	// Bitwise (TDD-00123 Stage 2) — JS computes `& | ^` in the ToInt32 domain
	// (both operands truncated to a signed 32-bit int) but the *result is a
	// Number* (a double). Keeping it i64 would make `(a & b) / c` do integer
	// division; instead the 32-bit result is `sitofp`'d back to a double so it
	// participates in float arithmetic like every other `number`.
	case "&", "|", "^":
		l32 := e.toInt32(left)
		r32 := e.toInt32(right)
		iop := map[string]string{"&": "and", "|": "or", "^": "xor"}[ex.Op]
		e.emitInstr(fmt.Sprintf("%s = %s i32 %s, %s", reg, iop, l32, r32))
		return e.int32ToNumber(reg), nil
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
	// toInt32 carries the NaN/∞/out-of-i64-range handling (ADR-01057); the
	// shift count's low 5 bits are the same under ToUint32.
	l32 := e.toInt32(left)
	r32 := e.toInt32(right)
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

	// The i32 shift result is a Number (double) in JS (TDD-00123 Stage 2), so
	// convert it to a double: `<<`/`>>` produce a signed Int32 (`sitofp`),
	// while `>>>` produces an unsigned Uint32 (zero-extend to i64 first, then
	// `sitofp` the non-negative value — a bare `sitofp i32` would misread a
	// result ≥ 2^31 as negative, e.g. `-1 >>> 0` must be 4294967295).
	result := e.freshReg()
	if op == ">>>" {
		wide := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", wide, res32))
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", result, wide))
	} else {
		e.emitInstr(fmt.Sprintf("%s = sitofp i32 %s to double", result, res32))
	}
	return Value{Ref: result, Ty: TypeF64}, nil
}

// toInt32 applies JS's ToInt32 to a value: coerce to the integer domain and
// keep the low 32 bits (trunc gives the mod-2^32 wraparound regardless of
// sign). Returns an i32 SSA register. Shared by the bitwise operators and
// `emitBitShift`.
// bitwiseNonNumericToZero maps an operand whose ToInt32 is unconditionally 0
// to a literal i64 0, so the bitwise/shift `trunc` never receives a raw pointer
// or an empty (void) operand. ToInt32(undefined)=ToInt32(NaN)=0, ToInt32(null)=0,
// and ToNumber(object/function/symbol-without-a-numeric-valueOf) is NaN → 0 too.
// A method-bearing object was already run through the ToPrimitive ladder up in
// emitBinaryExpr, so by the time we reach a bitwise op a still-object/func/void
// operand genuinely coerces to 0 (compat=js; strict rejects these earlier).
func (e *Emitter) bitwiseNonNumericToZero(v Value) Value {
	// A Symbol is deliberately excluded: ToNumber(symbol) is a TypeError in JS
	// (not NaN→0), so a symbol operand must keep whatever throw/reject path it
	// already has, never silently coerce to 0.
	if v.Ty.IR == "void" || v.Ty.IsUndefined || v.Ty.IsNull ||
		((v.Ty.IsObject || v.Ty.IsFunc) && !v.Ty.IsSymbol) {
		return Value{Ref: "0", Ty: TypeI64}
	}
	return v
}

func (e *Emitter) toInt32(v Value) string {
	v = e.bitwiseNonNumericToZero(v)
	if v.Ty.Float {
		// Spec ToInt32 on a double: NaN and ±∞ are 0, everything else is the
		// truncated value modulo 2^32. A bare `fptosi` is poison for NaN/∞ and
		// for a magnitude past i64 (`~({})` came out -2, `1e20 | 0` garbage —
		// ADR-01057); `frem` by 2^32 keeps the value in i64 range, and a NaN
		// (also what ∞ leaves after the frem) selects to 0.
		e.ensureMathFuncs()
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @trunc(double %s)", t, v.Ref))
		m := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = frem double %s, 4294967296.0", m, t))
		isNaN := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp uno double %s, %s", isNaN, m, m))
		fin := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 0.0, double %s", fin, isNaN, m))
		v = Value{Ref: fin, Ty: TypeF64}
	}
	i := e.coerce(v, TypeI64)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", r, i.Ref))
	return r
}

// int32ToNumber converts a signed 32-bit bitwise result back to a `number`
// (double), the type JS bitwise operators produce (TDD-00123 Stage 2).
func (e *Emitter) int32ToNumber(i32 string) Value {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i32 %s to double", r, i32))
	return Value{Ref: r, Ty: TypeF64}
}

// countToNumber converts an integer-valued count/index result (`.length`,
// `indexOf`, `charCodeAt`, `codePointAt`, `search`, `localeCompare`, …) to a
// `number` (double) — the type JS produces for these (TDD-00123 Stage 3).
// Consumers that need an integer (array indices, slice/substring bounds, count
// args) already `coerce(..., TypeI64)` at their use site, `fptosi`-ing it back.
// A value that is already a float (or not an integer) passes through unchanged.
func (e *Emitter) countToNumber(v Value) Value {
	if v.Ty.Float || v.Ty.IR != "i64" {
		return v
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", r, v.Ref))
	return Value{Ref: r, Ty: TypeF64}
}

// typeofString maps a compiled type to its TypeScript typeof string. The
// object-flag alternation must come before the bare `IR == "ptr"` case — every
// heap-backed built-in (Promise, Map, generator instance, Date, …) is a ptr,
// and falling through mislabeled them all "string" (a 2026-08-21 conformance-
// sweep find: `typeof somePromise` reported "string").
func typeofString(ty Type) string {
	switch {
	case ty.IsUndefined, ty.IR == "":
		// The zero Type (an expression the checker can't resolve) reads as
		// JS's `typeof missingThing === "undefined"`, not a silent "number".
		return "undefined"
	case ty.IsFunc, ty.IsFFIFunction:
		return "function"
	case ty.IsBigInt:
		return "bigint"
	case ty.IsSymbol:
		return "symbol"
	case ty.IsNull, ty.IsObject, ty.IsArray, ty.IsTuple, ty.IsPromise, ty.IsMap,
		ty.IsSet, ty.IsGenerator, ty.IsDate, ty.IsResponse, ty.IsClass,
		ty.IsError, ty.IsRequest, ty.IsFetchRequest, ty.IsURL,
		ty.IsURLSearchParams, ty.IsHeaders, ty.IsEventEmitter, ty.IsRegExp,
		ty.IsArrayBuffer, ty.IsDataView, ty.IsTypedArray, ty.IsDynamicObject:
		return "object"
	case ty.IR == "i1":
		return "boolean"
	case ty.IR == "ptr":
		return "string"
	default:
		return "number"
	}
}

// emitUndefinedableTypeof answers `typeof v` at runtime for a `T | undefined`
// value: present → the base type's typeof string, absent → "undefined". The
// presence signal depends on the representation — a nullable-scalar's presence
// bit, a pointer's null, or a nested array's null data-ptr (TDD-00221). Returns
// ok=false for a shape it doesn't handle (letting the caller fall back to the
// static answer). The base typeof is computed from the type with its
// nullable/undefined flags cleared.
func (e *Emitter) emitUndefinedableTypeof(arg ast.Expression, ty Type) (Value, bool, error) {
	base := ty
	base.Nullable = false
	base.IsUndefined = false
	baseStr := e.internString(typeofString(base))
	// Absent: `undefined`, or null's "object" for a `T | null`.
	undefStr := e.internString("undefined")
	if !ty.IsUndefined {
		undefStr = e.internString("object")
	}

	// A nullable-scalar *local* auto-unwraps to its bare payload on an
	// identifier read (emitIdentifier), so its presence bit must be read from
	// storage, as the other null-aware operators do.
	if id, ok := arg.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && sym.isNullableScalarLocal() {
			present := e.loadNullableScalarPresent(sym.Ptr, sym.Ty)
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, present, baseStr, undefStr))
			return Value{Ref: sel, Ty: TypePtr}, true, nil
		}
	}

	val, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, false, err
	}
	var present string
	switch {
	case isNullableScalar(val.Ty):
		present, _ = e.nullableScalarAggParts(val)
	case val.Ty.IsArray:
		dataPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtr, val.Ref))
		present = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", present, dataPtr))
	case val.Ty.IR == "ptr":
		present = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", present, val.Ref))
	default:
		return Value{}, false, nil
	}
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, present, baseStr, undefStr))
	return Value{Ref: sel, Ty: TypePtr}, true, nil
}

// typeofBuiltinConstructors are global names that are callable constructors in
// JS (`typeof Promise === "function"`) and exist in this runtime, but aren't
// first-class values here — `typeof` answers for them statically.
var typeofBuiltinConstructors = map[string]bool{
	"Promise": true, "Map": true, "Set": true, "Date": true, "RegExp": true,
	"Error": true, "TypeError": true, "RangeError": true, "SyntaxError": true,
	"ReferenceError": true, "EvalError": true, "URIError": true, "AggregateError": true, "DOMException": true,
	"ArrayBuffer": true, "Int8Array": true, "Uint8Array": true, "Uint8ClampedArray": true,
	"Int16Array": true, "Uint16Array": true, "Int32Array": true, "Uint32Array": true,
	"Float32Array": true, "Float64Array": true, "BigInt64Array": true, "BigUint64Array": true,
	"Number": true, "String": true, "Boolean": true, "Symbol": true, "BigInt": true, "Buffer": true, "Blob": true,
	"Object": true, "Array": true, "URL": true, "URLSearchParams": true,
	"Headers": true, "Request": true, "Response": true, "EventEmitter": true,
	"TextEncoder": true, "TextDecoder": true, "XMLHttpRequest": true,
	"WebSocket": true, "EventSource": true, "AbortController": true,
	"AbortSignal": true, "Event": true, "EventTarget": true,
}

// typeofBuiltinFunctions are implemented global functions (`typeof fetch ===
// "function"`).
var typeofBuiltinFunctions = map[string]bool{
	"fetch": true, "setTimeout": true, "setInterval": true, "clearTimeout": true,
	"clearInterval": true, "queueMicrotask": true, "structuredClone": true,
	"parseInt": true, "parseFloat": true, "isNaN": true, "isFinite": true,
	"btoa": true, "atob": true, "encodeURI": true, "decodeURI": true,
	"encodeURIComponent": true, "decodeURIComponent": true,
}

// typeofPromiseStatics are the Promise combinators this runtime implements —
// `typeof Promise.all === "function"`; an unimplemented static (`Promise.try`)
// honestly reads "undefined" (it does not exist in this runtime).
var typeofPromiseStatics = map[string]bool{
	"all": true, "allSettled": true, "any": true, "race": true,
	"resolve": true, "reject": true,
}

// typeofStaticAnswer resolves `typeof <arg>` for arguments that aren't
// expressions at all in this compiler — references to built-in namespaces,
// constructors, classes, and unresolved identifiers — where inference has no
// type to give (previously they all fell into typeofString's "number" default,
// a silently wrong answer). Returns "" to let normal inference answer.
func (e *Emitter) typeofStaticAnswer(arg ast.Expression) string {
	switch a := arg.(type) {
	case *ast.Identifier:
		if _, found := e.lookup(a.Name); found {
			return "" // a real binding — infer normally (shadowing wins)
		}
		if _, ok := e.funcs[a.Name]; ok {
			return "" // named function — inference already answers "function"
		}
		if _, ok := e.classes[a.Name]; ok {
			return "function" // a class is a constructor function in JS
		}
		if impl, known := typeofBuiltinConstructors[a.Name]; known {
			if impl {
				return "function"
			}
			return "undefined"
		}
		if typeofBuiltinFunctions[a.Name] {
			return "function"
		}
		switch a.Name {
		case "Math", "JSON", "console", "globalThis":
			return "object"
		case "NaN", "Infinity":
			return "" // numeric globals — infer normally
		}
		// JS: `typeof undeclared` is "undefined", never an error.
		return "undefined"
	case *ast.MemberExpression:
		if obj, ok := a.Object.(*ast.Identifier); ok {
			if _, shadowed := e.lookup(obj.Name); !shadowed {
				switch obj.Name {
				case "Promise":
					if typeofPromiseStatics[a.Property] {
						return "function"
					}
					return "undefined"
				case "Math":
					if typeofMathMethods[a.Property] {
						return "function"
					}
					return "" // constants (Math.PI etc.) infer as number correctly
				case "JSON":
					if a.Property == "parse" || a.Property == "stringify" {
						return "function"
					}
					return "undefined"
				case "console":
					// Every console member this compiler exposes is a method — and
					// real console has no non-function own properties — so any member
					// reference is a function.
					return "function"
				case "Object", "Number", "String", "Array", "Date", "Symbol", "Reflect", "Boolean":
					if typeofNamespaceStatics[obj.Name][a.Property] {
						return "function"
					}
					// A non-method static (Number.MAX_VALUE, Symbol.iterator, …) or an
					// unknown member falls through to normal inference / "undefined".
					return ""
				}
			}
		}
		// A *value*-receiver method reference (`c.m`, `"hi".slice`, `[1].push`):
		// `typeof` is "function" (ADR-00607). Without this the member access
		// infers as the i64 default and wrongly answers "number". Only real
		// methods qualify — a plain field, a `length`, or an accessor property
		// still answers through normal inference.
		if e.isValueMethodRef(e.inferExprType(a.Object), a.Property) {
			return "function"
		}
	}
	return ""
}

// isValueMethodRef reports whether `objTy.prop` (a member reference on a value,
// not a namespace) names a callable method — a class instance method, or a known
// method of a built-in value type (string/array) — so `typeof` answers
// "function" (ADR-00607). Conservative: a name it doesn't recognize falls back
// to normal inference, so this only ever upgrades a wrong "number" to the right
// "function", never the reverse. A class accessor (getter/setter) is
// deliberately excluded — `typeof obj.accessor` is the accessed value's type.
func (e *Emitter) isValueMethodRef(objTy Type, prop string) bool {
	if objTy.IsClass {
		if info, ok := e.classes[objTy.ClassName]; ok {
			if _, isMethod := info.MethodSigs[prop]; isMethod {
				return true // a plain method (accessor keys are accessorMethodName-mangled, never a bare prop)
			}
		}
		return false
	}
	if objTy.IsArray {
		return typeofArrayMethods[prop]
	}
	if isForOfStringTy(objTy) {
		return typeofStringMethods[prop]
	}
	return false
}

// typeofStringMethods / typeofArrayMethods are the method names whose `typeof`
// on a string / array value is "function" (ADR-00607). Membership tests only —
// an omitted name simply falls back to normal inference, so the lists need not
// be exhaustive to stay sound.
var typeofStringMethods = map[string]bool{
	"slice": true, "substring": true, "substr": true, "charAt": true, "charCodeAt": true,
	"codePointAt": true, "indexOf": true, "lastIndexOf": true, "includes": true,
	"startsWith": true, "endsWith": true, "toUpperCase": true, "toLowerCase": true,
	"trim": true, "trimStart": true, "trimEnd": true, "split": true, "replace": true,
	"replaceAll": true, "repeat": true, "padStart": true, "padEnd": true, "concat": true,
	"at": true, "match": true, "matchAll": true, "search": true, "normalize": true,
	"localeCompare": true, "toString": true, "valueOf": true,
}

var typeofArrayMethods = map[string]bool{
	"push": true, "pop": true, "shift": true, "unshift": true, "slice": true, "splice": true,
	"concat": true, "join": true, "indexOf": true, "lastIndexOf": true, "includes": true,
	"find": true, "findIndex": true, "findLast": true, "findLastIndex": true, "filter": true,
	"map": true, "forEach": true, "reduce": true, "reduceRight": true, "some": true,
	"every": true, "sort": true, "reverse": true, "fill": true, "flat": true, "flatMap": true,
	"keys": true, "values": true, "entries": true, "at": true, "copyWithin": true,
	"toReversed": true, "toSorted": true, "toSpliced": true, "with": true, "toString": true,
}

// typeofNamespaceStatics maps a built-in namespace to its static *method* names,
// so `typeof Object.keys === "function"` etc. Non-method statics (numeric/symbol
// constants) are deliberately absent — they answer through normal inference.
// Enumeration-based (the ES static surface is stable), covering the common
// namespaces; an unlisted method reads "undefined" rather than a wrong "number".
var typeofNamespaceStatics = map[string]map[string]bool{
	"Object": {
		"keys": true, "values": true, "entries": true, "assign": true,
		"freeze": true, "isFrozen": true, "create": true, "fromEntries": true,
		"getOwnPropertyNames": true, "getPrototypeOf": true, "setPrototypeOf": true,
		"defineProperty": true, "defineProperties": true, "getOwnPropertyDescriptor": true,
		"hasOwn": true, "is": true, "seal": true, "isSealed": true, "preventExtensions": true,
	},
	"Number": {
		"isInteger": true, "isFinite": true, "isNaN": true, "isSafeInteger": true,
		"parseFloat": true, "parseInt": true,
	},
	"String":  {"fromCharCode": true, "fromCodePoint": true, "raw": true},
	"Array":   {"isArray": true, "from": true, "of": true},
	"Date":    {"now": true, "parse": true, "UTC": true},
	"Symbol":  {"for": true, "keyFor": true},
	"Boolean": {},
	"Reflect": {
		"get": true, "set": true, "has": true, "ownKeys": true,
		"getPrototypeOf": true, "defineProperty": true, "deleteProperty": true,
		"apply": true, "construct": true,
	},
}

// typeofMathMethods are the implemented Math functions (the emit_call_math.go
// dispatch set) — `typeof Math.floor === "function"`; Math's numeric constants
// stay on normal inference.
var typeofMathMethods = map[string]bool{
	"abs": true, "acos": true, "acosh": true, "asin": true, "asinh": true,
	"atan": true, "atan2": true, "atanh": true, "cbrt": true, "ceil": true,
	"clz32": true, "cos": true, "cosh": true, "exp": true, "expm1": true,
	"floor": true, "fround": true, "hypot": true, "imul": true, "log": true,
	"log10": true, "log1p": true, "log2": true, "max": true, "min": true,
	"pow": true, "random": true, "round": true, "sign": true, "sin": true,
	"sinh": true, "sqrt": true, "tan": true, "tanh": true, "trunc": true,
}

func (e *Emitter) emitUnary(ex *ast.UnaryExpression) (Value, error) {
	// `delete` (ADR-00487): env vars unset for real; a map-backed dict key
	// deletes through the map; everything else (fixed-shape fields, array
	// elements) is a clean rejection — the static layouts have nothing to
	// delete from.
	if ex.Op == "delete" {
		if mem, ok := ex.Arg.(*ast.MemberExpression); ok {
			if e.isProcessEnvExpr(mem.Object) {
				e.ensureUnsetenv()
				e.emitInstr(fmt.Sprintf("call i32 @unsetenv(ptr %s)", e.internString(mem.Property)))
				return Value{Ref: "1", Ty: TypeBool}, nil
			}
			if objTy := e.inferExprType(mem.Object); objTy.IsDynamicObject {
				objVal, err := e.emitExpr(mem.Object)
				if err != nil {
					return Value{}, err
				}
				res := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_delete(ptr %s, ptr %s)", res, objVal.Ref, e.internString(mem.Property)))
				return Value{Ref: res, Ty: TypeBool}, nil
			}
			// `delete anyVal.key` — D1 dynamic object (TDD-00155 Stage 1).
			if objTy := e.inferExprType(mem.Object); isUnconstrainedDynamic(objTy) {
				objVal, err := e.emitExpr(mem.Object)
				if err != nil {
					return Value{}, err
				}
				return e.emitDynAnyDelete(objVal, e.internString(mem.Property), ex.GetPos())
			}
		}
		if idx, ok := ex.Arg.(*ast.IndexExpression); ok {
			if objTy := e.inferExprType(idx.Object); objTy.IsDynamicObject {
				objVal, err := e.emitExpr(idx.Object)
				if err != nil {
					return Value{}, err
				}
				keyExpr, err := e.dynObjectKeyExpr(idx.Index, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				keyVal, err := e.emitExpr(keyExpr)
				if err != nil {
					return Value{}, err
				}
				res := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_delete(ptr %s, ptr %s)", res, objVal.Ref, keyVal.Ref))
				return Value{Ref: res, Ty: TypeBool}, nil
			}
			// `delete anyVal[key]` — D1 dynamic object (TDD-00155 Stage 1).
			if objTy := e.inferExprType(idx.Object); isUnconstrainedDynamic(objTy) {
				objVal, err := e.emitExpr(idx.Object)
				if err != nil {
					return Value{}, err
				}
				keyRef, err := e.dynAnyKeyRef(idx.Index, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				return e.emitDynAnyDelete(objVal, keyRef, ex.GetPos())
			}
		}
		return Value{}, fmt.Errorf("%d:%d: 'delete' supports process.env.KEY and index-signature/dynamic-object keys only — fixed-shape fields and array elements have static layouts", ex.GetPos().Line, ex.GetPos().Col)
	}

	// typeof is resolved purely from the inferred type — no code emitted for the
	// argument — EXCEPT for any/unknown, where the concrete type can change at
	// runtime, so it must become a genuine runtime tag dispatch instead.
	if ex.Op == "typeof" {
		if mem, ok := ex.Arg.(*ast.MemberExpression); ok && !mem.Optional {
			// An optional method: "function" where the instance's class
			// implements it, "undefined" where it does not.
			if ot := e.inferExprType(mem.Object); ot.IsClass && e.isOptionalMethod(ot.ClassName, mem.Property) {
				v, err := e.emitExpr(mem)
				if err != nil {
					return Value{}, err
				}
				absent := e.ptrIsNull(v.Ref)
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, absent, e.internString("undefined"), e.internString("function")))
				return Value{Ref: r, Ty: TypePtr}, nil
			}
		}
		if s := e.typeofStaticAnswer(ex.Arg); s != "" {
			return Value{Ref: e.internString(s), Ty: TypePtr}, nil
		}
		ty := e.inferExprType(ex.Arg)
		if ty.IsCaught {
			val, err := e.emitExpr(ex.Arg)
			if err != nil {
				return Value{}, err
			}
			return e.emitCaughtTypeof(val)
		}
		if ty.IsDynamic {
			val, err := e.emitExpr(ex.Arg)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynamicTypeof(val)
		}
		// A `T | undefined` value (TDD-00187/TDD-00221) needs a *runtime* answer:
		// `typeofString` would statically report "undefined" for every one, even a
		// present element. Test the presence signal (scalar presence bit, or a null
		// pointer / null array data-ptr) and pick the base type's typeof when
		// present, "undefined" when absent.
		if ty.IsUndefined && !ty.IsNull {
			if res, ok, err := e.emitUndefinedableTypeof(ex.Arg, ty); ok || err != nil {
				return res, err
			}
		}
		// A `T | null` scalar or string that holds null is "object".
		if ty.Nullable && !ty.IsUndefined && !ty.IsNull && typeofString(ty) != "object" && (isNullableScalar(ty) || isForOfStringTy(Type{IR: ty.IR})) {
			if res, ok, err := e.emitUndefinedableTypeof(ex.Arg, ty); ok || err != nil {
				return res, err
			}
		}
		ptr := e.internString(typeofString(ty))
		return Value{Ref: ptr, Ty: TypePtr}, nil
	}

	if ex.Op == "+" {
		arg, err := e.emitExprKeepNullable(ex.Arg)
		if err != nil {
			return Value{}, err
		}
		return e.emitUnaryPlus(arg, ex.GetPos())
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
		}
		// "!" falls through to the generic path below, whose toBool now handles
		// bigint truthiness (0n falsy, else truthy).
	}
	// Unary numeric coercion on a dynamic value (TDD-00076 A2): ToNumber,
	// negate, re-box.
	if arg.Ty.IsDynamic && ex.Op == "-" {
		d := e.emitAnyToNum(arg)
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fneg double %s", n, d))
		return Value{Ref: e.emitNbEncodeDouble(n), Ty: TypeAny}, nil
	}
	reg := e.freshReg()
	switch ex.Op {
	case "-":
		if arg.Ty.Float {
			e.emitInstr(fmt.Sprintf("%s = fneg %s %s", reg, arg.Ty.IR, arg.Ref))
		} else if arg.Ty.IsInteger() {
			e.emitInstr(fmt.Sprintf("%s = sub %s 0, %s", reg, arg.Ty.IR, arg.Ref))
		} else {
			// Unary `-` does ToNumber(operand); a non-numeric operand (a string
			// or object — IR "ptr", or an aggregate) has no numeric value, so
			// strict rejects it cleanly rather than emitting `sub ptr 0, …`
			// (invalid IR). BigInt/dynamic operands are handled above.
			return Value{}, fmt.Errorf("%d:%d: unary '-' requires a number or bigint operand", ex.GetPos().Line, ex.GetPos().Col)
		}
		return Value{Ref: reg, Ty: arg.Ty}, nil
	case "!":
		b := e.toBool(arg)
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", reg, b.Ref))
		return Value{Ref: reg, Ty: TypeBool}, nil
	case "~":
		// JS `~x` is `ToInt32(x) ^ -1`, yielding a Number (double) — see the
		// binary bitwise ops (TDD-00123 Stage 2). A non-numeric operand (a
		// string/object/array — IR "ptr" or an aggregate) has no direct integer
		// value: toInt32 would `trunc` the pointer as an i64 (invalid IR). Real
		// JS runs ToNumber(operand) first (`~{}` is -1: ToNumber({})→NaN→
		// ToInt32→0→~0). strict rejects it cleanly (consistent with unary '-'
		// and the binary object-operator rejection); -compat=js boxes and runs
		// the real ToNumber. BigInt/dynamic operands were handled above.
		if arg.Ty.IsDynamic || (!arg.Ty.Float && !arg.Ty.IsInteger() && arg.Ty.IR != "i1") {
			if !e.compatJS() && !arg.Ty.IsDynamic {
				return Value{}, fmt.Errorf("%d:%d: unary '~' requires a number or bigint operand", ex.GetPos().Line, ex.GetPos().Col)
			}
			// The same ToNumber unary `+` runs (ToPrimitive for an object, the
			// string form for an array — `~[5]` is -6), rather than a bare
			// box→ToNumber that read every heap reference as NaN.
			conv, err := e.emitUnaryPlus(arg, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			arg = e.coerce(conv, TypeF64)
		}
		x32 := e.toInt32(arg)
		e.emitInstr(fmt.Sprintf("%s = xor i32 %s, -1", reg, x32))
		return e.int32ToNumber(reg), nil
	}
	return Value{}, fmt.Errorf("unknown unary operator '%s'", ex.Op)
}

// emitUnaryPlus is JS unary `+`: ToNumber(operand) (ADR-01057). A number is
// itself; a boolean, null, undefined and a string convert as Number() does
// (the string through the runtime StringToNumber); a dynamic value runs
// ToPrimitive(number) then ToNumber; an object with a valueOf/toString/
// Symbol.toPrimitive takes the static ToPrimitive ladder, an array converts
// through its string form (`+[]` is 0, `+[5]` is 5), any other object is NaN;
// a BigInt or Symbol throws the spec TypeError. The result is a plain
// `number` (a double) except for a numeric operand, which keeps its type —
// inferExprType mirrors this.
func (e *Emitter) emitUnaryPlus(arg Value, pos ast.Pos) (Value, error) {
	nan := Value{Ref: "0x7FF8000000000000", Ty: TypeF64}
	t := arg.Ty
	switch {
	case t.IsBigInt:
		e.emitThrowTypeError("Cannot convert a BigInt value to a number")
		return nan, nil
	case t.IsSymbol:
		e.emitThrowTypeError("Cannot convert a Symbol value to a number")
		return nan, nil
	case isNullableScalar(t):
		present, payload := e.nullableScalarAggParts(arg)
		conv, err := e.emitUnaryPlus(payload, pos)
		if err != nil {
			return Value{}, err
		}
		conv = e.coerce(conv, TypeF64)
		absent := "0.0" // `T | null` reads null → 0
		if t.IsUndefined {
			absent = nan.Ref
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double %s", r, present, conv.Ref, absent))
		return Value{Ref: r, Ty: TypeF64}, nil
	case t.IsNull || t.IR == "void":
		if t.IsUndefined || t.IR == "void" {
			return nan, nil
		}
		return Value{Ref: "0.0", Ty: TypeF64}, nil
	case t.IR == "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = uitofp i1 %s to double", r, arg.Ref))
		return Value{Ref: r, Ty: TypeF64}, nil
	case t.Float || (t.IsInteger() && !t.IsDynamic):
		return arg, nil
	case t.IsDynamic:
		// A D1 dynamic object/array value is a raw pointer until boxed.
		boxed, err := e.emitBoxValue(arg)
		if err != nil {
			return Value{}, err
		}
		prim := e.emitAnyToPrimitive(boxed.Ref, false)
		return Value{Ref: e.emitAnyToNum(Value{Ref: prim, Ty: TypeAny}), Ty: TypeF64}, nil
	case isStringTy(t):
		e.ensureToNumber()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__kml_to_number(ptr %s)", r, e.nullSafeParseInput(arg).Ref))
		return Value{Ref: r, Ty: TypeF64}, nil
	case t.IsArray:
		s, err := e.emitValueToString(arg)
		if err != nil {
			return Value{}, err
		}
		e.ensureToNumber()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call double @__kml_to_number(ptr %s)", r, s.Ref))
		return Value{Ref: r, Ty: TypeF64}, nil
	case objectMayToPrimitive(t):
		if res, ok, err := e.emitObjectToPrimitive(arg, "number"); err == nil && ok {
			return e.emitUnaryPlus(res, pos)
		}
	}
	// Any other object/handle: ToPrimitive yields "[object …]" → NaN.
	return nan, nil
}

func (e *Emitter) emitUpdate(ex *ast.UpdateExpression) (Value, error) {
	ident, ok := ex.Arg.(*ast.Identifier)
	if !ok {
		// A member or index target (`obj.x++`, `this.x++`, `C.staticField++`,
		// `arr[i]++`) desugars to the equivalent compound assignment (`… += 1` /
		// `… -= 1`), reusing every member/index/static-field assignment path
		// emitAssign already implements (ADR-00376). Prefix returns the new
		// value; postfix returns the old value, read before the update.
		switch ex.Arg.(type) {
		case *ast.MemberExpression, *ast.IndexExpression:
			return e.emitTargetUpdate(ex)
		}
		return Value{}, fmt.Errorf("update expression requires an identifier, member, or index target")
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

	// A dynamic (`any`) operand: ToNumeric(old), step, store the number back
	// as a box; postfix yields the *numeric* old value (`x = "5"; x++` → 5,
	// then x is 6). The word is a NaN-box, so the raw `add i64 …, 1` below
	// would corrupt it — an untyped `for (j = 0; …; j++)` under `-compat=js`
	// never terminated (ADR-01061). A BigInt held in an `any` steps as a
	// number here — a remaining gap (the box has no BigInt kind).
	if sym.Ty.IsDynamic {
		prim := e.emitAnyToPrimitive(oldReg, false)
		oldNum := Value{Ref: e.emitAnyToNum(Value{Ref: prim, Ty: TypeAny}), Ty: TypeF64}
		newReg := e.freshReg()
		if ex.Op == "++" {
			e.emitInstr(fmt.Sprintf("%s = fadd double %s, 1.0", newReg, oldNum.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = fsub double %s, 1.0", newReg, oldNum.Ref))
		}
		boxed, err := e.emitBoxValue(Value{Ref: newReg, Ty: TypeF64})
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, sym.Ptr))
		if ex.Prefix {
			return Value{Ref: newReg, Ty: TypeF64}, nil
		}
		return oldNum, nil
	}

	// `++`/`--` do ToNumber(operand) then step; a non-numeric operand
	// (a string or object — IR "ptr", or an aggregate) has no numeric value,
	// so strict rejects it cleanly (matching tsc: "an arithmetic operand must
	// be of type number/bigint") rather than emitting `add ptr, 1` (invalid
	// IR). BigInt and nullable-scalar operands are handled above.
	if !sym.Ty.Float && !sym.Ty.IsInteger() {
		return Value{}, fmt.Errorf("%d:%d: operator '%s' requires a number or bigint operand", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
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

// emitTargetUpdate implements `++`/`--` on a member or index target by
// desugaring to the equivalent compound assignment (`target += 1` / `-= 1`),
// which reuses emitAssign's existing static-field / instance-field / index
// assignment machinery (ADR-00376). The step literal matches the target's type
// so a bigint field steps by `1n`. Prefix returns the post-update value (the
// compound assignment's result); postfix returns the value read before the
// update.
//
// A postfix update whose value is consumed reads the target twice (the pre-read
// and again inside the compound assignment), so a side-effecting *receiver*
// (`makeObj().x++`) is first hoisted into a temp binding (ADR-00606) — the
// receiver runs exactly once, both reads then go through the temp. A
// statement-position postfix discards the pre-read (dead-code-eliminated), and a
// simple receiver (identifier/`this`/static name) never had the problem.
func (e *Emitter) emitTargetUpdate(ex *ast.UpdateExpression) (Value, error) {
	op := "+="
	if ex.Op == "--" {
		op = "-="
	}
	pos := ex.GetPos()
	arg := ex.Arg
	// Only a *consumed* postfix reads twice; hoist the receiver so its side
	// effects run once. (Prefix reads once, and a discarded postfix is DCE'd —
	// hoisting there would be harmless but pointless.)
	if !ex.Prefix {
		hoisted, err := e.hoistUpdateReceiver(arg)
		if err != nil {
			return Value{}, err
		}
		arg = hoisted
	}
	var one ast.Expression
	if e.inferExprType(arg).IsBigInt {
		one = ast.NewBigIntLiteral("1", pos)
	} else {
		one = ast.NewNumberLiteral("1", pos)
	}
	assign := ast.NewAssignmentExpression(op, arg, one, pos)

	if ex.Prefix {
		return e.emitAssign(assign)
	}
	old, err := e.emitExpr(arg)
	if err != nil {
		return Value{}, err
	}
	if _, err := e.emitAssign(assign); err != nil {
		return Value{}, err
	}
	return old, nil
}

// hoistUpdateReceiver evaluates a postfix update target's side-effecting
// receiver exactly once and rewrites the target to read it back from a temp
// binding (ADR-00606), so the pre-read and the compound-assign no longer
// re-run those side effects. Only a member target with a non-trivial object
// (`makeObj().x`, not `obj.x`/`this.x`) needs it; every other shape is returned
// unchanged. The temp is a ptr (object receivers are always ptr-typed); a
// non-ptr receiver (none exist for member access today) falls back unchanged.
func (e *Emitter) hoistUpdateReceiver(arg ast.Expression) (ast.Expression, error) {
	switch t := arg.(type) {
	case *ast.MemberExpression:
		// `r!.x++` hoists r itself and asserts the temp: its type keeps
		// saying whether an absent r is null or undefined, for the guard.
		obj, asserted := t.Object, false
		for {
			nn, ok := obj.(*ast.NonNullExpression)
			if !ok {
				break
			}
			obj, asserted = nn.Arg, true
		}
		switch obj.(type) {
		case *ast.Identifier, *ast.ThisExpression:
			return arg, nil // idempotent receiver — no hoist needed
		}
		recv, err := e.emitExpr(obj)
		if err != nil {
			return nil, err
		}
		if recv.Ty.IR != "ptr" {
			return arg, nil
		}
		tmpName := fmt.Sprintf("__kml_upd_recv_%s", e.freshReg()[1:])
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", recv.Ref, slot))
		e.define(tmpName, Symbol{Ptr: slot, Ty: recv.Ty})
		var base ast.Expression = ast.NewIdentifier(tmpName, t.GetPos())
		if asserted {
			base = ast.NewNonNullExpression(base, t.GetPos())
		}
		return ast.NewMemberExpression(base, t.Property, t.GetPos()), nil
	case *ast.IndexExpression:
		// Hoist both a side-effecting array-producing *object* (`makeArr()[i]++`)
		// and a side-effecting *index* expression (`arr[side()]++`) so each runs
		// once (ADR-00606). Evaluation order is object-then-index, matching JS.
		obj := t.Object
		switch obj.(type) {
		case *ast.Identifier, *ast.ThisExpression:
			// idempotent receiver — leave as is
		default:
			recv, err := e.emitExpr(obj)
			if err != nil {
				return nil, err
			}
			if recv.Ty.IsArray {
				header := e.boxArrayValue(recv)
				tmpName := fmt.Sprintf("__kml_upd_arr_%s", e.freshReg()[1:])
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, slot))
				e.define(tmpName, Symbol{Ptr: slot, Ty: recv.Ty})
				obj = ast.NewIdentifier(tmpName, t.GetPos())
			}
			// (A non-array receiver falls through; other index-target kinds are
			// handled by their own assignment paths.)
		}
		idx := t.Index
		switch idx.(type) {
		case *ast.NumberLiteral, *ast.Identifier, *ast.ThisExpression:
			// idempotent index — leave as is
		default:
			idxVal, err := e.emitExpr(idx)
			if err != nil {
				return nil, err
			}
			idxVal = e.coerce(idxVal, TypeI64)
			tmpName := fmt.Sprintf("__kml_upd_idx_%s", e.freshReg()[1:])
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxVal.Ref, slot))
			e.define(tmpName, Symbol{Ptr: slot, Ty: TypeI64})
			idx = ast.NewIdentifier(tmpName, t.GetPos())
		}
		if obj == t.Object && idx == t.Index {
			return arg, nil
		}
		return ast.NewIndexExpression(obj, idx, t.GetPos()), nil
	}
	return arg, nil
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
	// `+=` where an OPERAND is a string but the target type is not (e.g. an
	// object target: `obj += ''` → "[object Object]", Test262 regress-533254)
	// is still string concatenation — the result is a string regardless of the
	// declared slot type. Keying only on isStringTy(ty) below missed this and
	// fell through to numeric `add ptr` (invalid IR).
	if op == "+" && !isStringTy(ty) && (isStringTy(left.Ty) || isStringTy(right.Ty)) {
		l, r := left, right
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
		return e.emitStringConcat(l, r)
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
			// frem lowers to a libcall to fmod — needs libm on Linux.
			e.requireLink("m")
			e.emitInstr(fmt.Sprintf("%s = frem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitDivZeroGuard(ty, left, right)
			if ty.Signed {
				e.emitInstr(fmt.Sprintf("%s = srem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			} else {
				e.emitInstr(fmt.Sprintf("%s = urem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
			}
		}
	case "&", "|", "^":
		// Lockstep with emitBinary's bitwise ops (TDD-00123 Stage 2): compute
		// in the ToInt32 domain, return a Number (double). The compound-assign
		// store coerces back to the target binding's type.
		l32 := e.toInt32(left)
		r32 := e.toInt32(right)
		iop := map[string]string{"&": "and", "|": "or", "^": "xor"}[op]
		e.emitInstr(fmt.Sprintf("%s = %s i32 %s, %s", reg, iop, l32, r32))
		return e.int32ToNumber(reg), nil
	case "**":
		// Backs `**=`; mirrors emitBinary's `**` — libm pow() for float, exact
		// i64 exponentiation-by-squaring otherwise. See emitBinary for the
		// integer-model rationale (negative exponent → 0, result stays i64).
		if ty.Float {
			f1 := e.coerce(left, TypeF64)
			f2 := e.coerce(right, TypeF64)
			e.ensureJsPow()
			e.emitInstr(fmt.Sprintf("%s = call double @__kml_js_pow(double %s, double %s)", reg, f1.Ref, f2.Ref))
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
	// `&&`/`||` are value-preserving — `a && b` yields `b` (or the falsy `a`),
	// `a || b` yields `a` (or `b`) — not a bool (TDD-00075), in either lane:
	// the shared type, or the union shortCircuitValueType gives. Operands it
	// has no single-slot type for fall through to the bool form below.
	if ty, ok := shortCircuitValueType(e.inferExprType(ex.Left), e.inferExprType(ex.Right)); ok {
		return e.emitShortCircuitValue(ex, ty)
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

// shortCircuitValueType is the -compat=js result type of a value-preserving
// `left && right` / `left || right`: the shared type when both operands have
// one (sameShortCircuitType), else the constrained union `Left | Right` when
// they are scalars of different kinds (`s && 0`, `"" || 3`, `a && b && !c` —
// nullCoalesceUnion, the same box `??` yields). ok is false for everything else
// (aggregates, nullable scalars, dynamics), which emitShortCircuit degrades to a
// bool. Shared by emission and inferExprType so the two cannot disagree — a
// nested `(a && b && !c) && d` once inferred `string` for a left operand the
// emitter had produced as an i1, storing that i1 through a `ptr` slot.
func shortCircuitValueType(lt, rt Type) (Type, bool) {
	if sameShortCircuitType(lt, rt) {
		return lt, true
	}
	// Two arrays (or an array and null): the array, its slot the chosen
	// operand's header, as a ternary's (ternaryArrayType).
	if aty, ok := ternaryArrayType(lt, rt); ok && !aty.IsTuple {
		return aty, true
	}
	if lt.IR == "" || lt.IR == "void" || rt.IR == "" || rt.IR == "void" {
		return Type{}, false
	}
	if lt.IsArray || rt.IsArray || isNullableScalar(lt) || isNullableScalar(rt) {
		return TypeAny, true // no shared slot: both boxed
	}
	if isUnconstrainedDynamic(lt) || isUnconstrainedDynamic(rt) {
		return TypeAny, true
	}
	// Join the two sides' scalar kinds: an operand that is already a union
	// (`(s && n) || b`, `a && b && !c && d` nesting) contributes its members;
	// `null`/`undefined` contributes nullability only.
	var members []Type
	nullable := lt.Nullable || rt.Nullable
	var add func(t Type) bool
	add = func(t Type) bool {
		if t.IsNull || t.IsUndefined {
			nullable = true
			return true
		}
		if t.IsDynamic {
			for _, m := range t.UnionMembers {
				if !add(m) {
					return false
				}
			}
			return true
		}
		bare := t.withoutNullable()
		if bare.IR == "ptr" && !isStringTy(bare) {
			return false
		}
		k := scalarTypeKind(bare)
		if k == "" {
			return false
		}
		for _, m := range members {
			if scalarTypeKind(m) == k {
				return true
			}
		}
		members = append(members, bare)
		return true
	}
	if !add(lt) || !add(rt) || len(members) == 0 {
		return TypeAny, true // objects of different shapes: both boxed
	}
	if len(members) == 1 && isStringTy(members[0]) {
		// `u && s`: a lone string member keeps its null-pointer absence.
		s := members[0]
		s.Nullable = nullable
		return s, true
	}
	return Type{IR: TypeAny.IR, IsDynamic: true, UnionMembers: members, Nullable: nullable}, true
}

// emitShortCircuitValue is emitShortCircuit's -compat=js value-preserving form:
// the result is the actual left or right operand value (type ty), not a bool,
// with the right operand still evaluated only on the non-short-circuit path.
// ty is a union when the operands' kinds differ (shortCircuitValueType); each
// operand is then boxed into it, and truthiness is taken from the operand
// itself, before boxing.
func (e *Emitter) emitShortCircuitValue(ex *ast.BinaryExpression, ty Type) (Value, error) {
	if ty.IsArray {
		return e.emitShortCircuitArray(ex, ty)
	}
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, ty.IR, ty.Align()))

	left, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}
	l := e.toBool(left)
	left, err = e.coerceShortCircuitOperand(left, ty, ex.Left.GetPos())
	if err != nil {
		return Value{}, err
	}
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
	right, err = e.coerceShortCircuitOperand(right, ty, ex.Right.GetPos())
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, right.Ref, resPtr, ty.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, ty.IR, resPtr, ty.Align()))
	return Value{Ref: result, Ty: ty}, nil
}

// emitShortCircuitArray is emitShortCircuitValue for an array result: the slot
// holds the chosen operand's header, so the result aliases that array.
func (e *Emitter) emitShortCircuitArray(ex *ast.BinaryExpression, aty Type) (Value, error) {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	store := func(v Value) {
		if v.Ty.IsNull {
			v = e.emitAbsentArrayValue(aty)
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.arrayReturnHeader(v), slot))
	}
	left, err := e.emitExprWithObjectHint(ex.Left, aty)
	if err != nil {
		return Value{}, err
	}
	l := e.toBool(left)
	store(left)
	rhsL := e.freshLabel("scv.rhs")
	mergeL := e.freshLabel("scv.merge")
	if ex.Op == "&&" {
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", l.Ref, rhsL, mergeL))
	} else {
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", l.Ref, mergeL, rhsL))
	}
	e.emitLabel(rhsL)
	right, err := e.emitExprWithObjectHint(ex.Right, aty)
	if err != nil {
		return Value{}, err
	}
	store(right)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(mergeL)
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, slot))
	nullable := aty
	nullable.Nullable = true // read through the null-safe loader either way
	out := e.arrayValueFromHeaderReg(h, nullable)
	out.Ty = aty
	return out, nil
}

// coerceShortCircuitOperand brings one `&&`/`||` operand into the result slot
// type. For a same-typed result this is a plain coerce. For a union result, a
// `string | undefined` operand — a run-time null pointer when absent — is boxed
// as `undefined` on that path and as its string otherwise (the static coerce
// would read the type's `undefined` flag and box a present string as absent),
// the same split emitNullCoalesceUnion makes for its right operand.
func (e *Emitter) coerceShortCircuitOperand(v Value, ty Type, pos ast.Pos) (Value, error) {
	if isUnconstrainedDynamic(ty) && !v.Ty.IsDynamic {
		// An operand of any shape into `any`: boxed with its layout, so it
		// still renders and reads as itself.
		return e.emitBoxValue(v)
	}
	if !ty.IsDynamic || v.Ty.IR != "ptr" || !v.Ty.Nullable || v.Ty.IsNull {
		return e.coerce(v, ty), nil
	}
	undef, err := e.emitExpr(ast.NewNullLiteral(true, pos))
	if err != nil {
		return Value{}, err
	}
	asUndef := e.coerce(undef, ty)
	asValue := e.coerce(Value{Ref: v.Ref, Ty: v.Ty.withoutNullable()}, ty)
	isNull := e.ptrIsNull(v.Ref)
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, %s %s, %s %s", sel, isNull, ty.IR, asUndef.Ref, ty.IR, asValue.Ref))
	return Value{Ref: sel, Ty: ty}, nil
}

// emitConditional emits a ternary expression cond ? consequent : alternate.
// Uses an alloca+store/load pattern so both branches can produce a single result.
// ternaryNullableScalarType reports the nullable-scalar result type of a
// ternary when exactly one branch is `null` and the other a non-pointer scalar
// (`b ? 3 : null` is `number | null`). Returns ok=false otherwise — a
// ptr-typed branch (string/object/array `| null`) keeps its existing
// null-pointer representation, and two non-null branches unify as before.
func ternaryNullableScalarType(a, b Type) (Type, bool) {
	nonPtrScalar := func(t Type) bool {
		return !t.IsNull && !t.IsDynamic && !t.IsArray && !t.IsObject &&
			t.IR != "ptr" && t.IR != "void" && !t.Nullable
	}
	if a.IsNull && nonPtrScalar(b) {
		nt := b
		nt.Nullable = true
		return nt, true
	}
	if b.IsNull && nonPtrScalar(a) {
		nt := a
		nt.Nullable = true
		return nt, true
	}
	// A branch that is already a `T | undefined` scalar (`c ? a[i] : 0`, `c ?
	// xs.pop() : null`) keeps its absence: the result is that nullable scalar,
	// with a float payload when either side is one.
	for _, p := range [][2]Type{{a, b}, {b, a}} {
		n, o := p[0], p[1]
		if !isNullableScalar(n) || !(o.IsNull || isNullableScalar(o) || nonPtrScalar(o)) {
			continue
		}
		if !o.IsNull && scalarTypeKind(o.withoutNullable()) != scalarTypeKind(n.withoutNullable()) {
			continue
		}
		nt := n
		if o.Float && !n.Float {
			nt = o
			nt.Nullable, nt.IsUndefined, nt.UncheckedIndex = true, n.IsUndefined, n.UncheckedIndex
		}
		if !(isNullableScalar(o) && o.UncheckedIndex) && !(nonPtrScalar(o)) {
			nt.UncheckedIndex = false
		}
		return nt, true
	}
	return Type{}, false
}

// ternaryTypeName is a short human label for a branch type, used only in the
// incompatible-ternary diagnostic.
func ternaryTypeName(t Type) string {
	switch {
	case isStringTy(t):
		return "string"
	case t.IsBigInt:
		return "bigint"
	case t.Float:
		return "number"
	case t.IR == "i1":
		return "boolean"
	case t.IsObject:
		return "object"
	case t.IR == "ptr":
		return "pointer"
	}
	return t.IR
}

func (e *Emitter) emitConditional(ex *ast.ConditionalExpression) (Value, error) {
	// A `cond ? scalar : null` (or `cond ? null : scalar`) is `T | null` — emit
	// the presence-flagged { i1, T } aggregate so the null survives downstream
	// (`??`, string concat, `=== null`, assignment to a `T | null` binding),
	// rather than coercing null to a payload zero (ADR-00538).
	if nty, ok := ternaryNullableScalarType(e.inferExprType(ex.Consequent), e.inferExprType(ex.Alternate)); ok {
		return e.emitConditionalNullableScalar(ex, nty)
	}
	ty := e.inferExprType(ex.Consequent)
	if aty, ok := ternaryArrayType(ty, e.inferExprType(ex.Alternate)); ok {
		return e.emitConditionalArray(ex, aty)
	}
	// A dynamic (`any`) branch makes the result `any`, whatever the other
	// (an array or Buffer included): both are boxed (TDD-00155/00156).
	if ty.IsDynamic || e.inferExprType(ex.Alternate).IsDynamic {
		return e.emitConditionalAny(ex, nil)
	}
	if ty.IsArray {
		return Value{}, fmt.Errorf("%d:%d: ternary branches have incompatible types (an array vs a non-array)", ex.GetPos().Line, ex.GetPos().Col)
	}
	// A ternary whose branches mix a string with a non-string (or a pointer with
	// a scalar) has a genuine union result type this typed-subset compiler can't
	// represent in the single result slot below — previously this coerced the
	// alternate to the consequent's type and emitted invalid IR (a double stored
	// through a ptr slot, a clang-stage failure). Reject cleanly instead; assign
	// each branch to its own binding, or convert both to a common type. (The
	// `cond ? scalar : null` nullable case is handled above.)
	altTy := e.inferExprType(ex.Alternate)
	// A ternary with a dynamic (`any`) branch has a representable result after
	// all: `any`. Box both branches into the NaN-boxed word (TDD-00155/00156).
	if ty.IsDynamic || altTy.IsDynamic {
		return e.emitConditionalAny(ex, nil)
	}
	// Scalar branches of different JS kinds (`c ? 1 : "a"`, `c ? 1 : true`) are
	// the union of the two — the same boxed type a `number | string` annotation
	// produces, and what `??` yields for the same operand shape.
	if uTy, ok := ternaryUnion(ty, altTy); ok {
		return e.emitConditionalAny(ex, &uTy)
	}
	// A pointer/reference branch (string/object/array) and a non-pointer scalar
	// branch (number/boolean/bigint) can't share the single result slot below:
	// coercing the pointer branch to the scalar's IR (or vice versa) stores a ptr
	// through a scalar slot — invalid IR at the clang stage. This subsumes the
	// former string-vs-non-string check (string is pointer-like). Reject cleanly;
	// the result is a genuine union (e.g. `number | Addr`) this typed-subset
	// compiler can't put in one slot — narrow first, or assign each branch to its
	// own binding. (The `cond ? scalar : null` nullable case is handled above, and
	// a dynamic branch became `any` above.)
	// `c ? null : obj` (or `c ? obj : null`) is the nullable pointer.
	if nty, ok := ternaryNullablePointerType(ty, altTy); ok {
		ty = nty
	}
	ptrLike := func(t Type) bool {
		return t.IR == "ptr" || t.IsObject || t.IsArray || isStringTy(t)
	}
	if ptrLike(ty) != ptrLike(altTy) {
		return Value{}, fmt.Errorf("%d:%d: ternary branches have incompatible types (%s vs %s) — a union-typed ternary (e.g. number | object) is not supported; narrow first, assign each branch separately, or convert both to one type", ex.GetPos().Line, ex.GetPos().Col, ternaryTypeName(ty), ternaryTypeName(altTy))
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

// ternaryArrayType is the result type of `c ? xs : ys` / `c ? xs : null`: the
// array type, nullable when a branch is `null`/`undefined` or itself nullable.
// Shared by emission and inferExprType.
func ternaryArrayType(a, b Type) (Type, bool) {
	switch {
	case a.IsArray && b.IsArray:
		if b.Nullable && !a.Nullable {
			a.Nullable, a.IsUndefined = true, b.IsUndefined
		}
		return a, true
	case a.IsArray && b.IsNull:
		a.Nullable, a.IsUndefined = true, b.IsUndefined
		return a, true
	case b.IsArray && a.IsNull:
		b.Nullable, b.IsUndefined = true, a.IsUndefined
		return b, true
	}
	return Type{}, false
}

// emitConditionalArray emits an array-valued ternary. Arrays are references, so
// the single result slot holds the chosen branch's *header* (a null header for
// a `null` branch) — the result aliases the array it picked, as in JS.
func (e *Emitter) emitConditionalArray(ex *ast.ConditionalExpression, aty Type) (Value, error) {
	thenL := e.freshLabel("ternary.then")
	elseL := e.freshLabel("ternary.else")
	mergeL := e.freshLabel("ternary.merge")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))

	cond, err := e.emitExpr(ex.Test)
	if err != nil {
		return Value{}, err
	}
	cond = e.toBool(cond)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, thenL, elseL))
	for _, br := range []struct {
		label string
		expr  ast.Expression
	}{{thenL, ex.Consequent}, {elseL, ex.Alternate}} {
		e.emitLabel(br.label)
		v, err := e.emitExprWithObjectHint(br.expr, aty)
		if err != nil {
			return Value{}, err
		}
		if v.Ty.IsNull {
			v = e.emitAbsentArrayValue(aty)
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.arrayReturnHeader(v), slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}
	e.emitLabel(mergeL)
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, slot))
	nullable := aty
	nullable.Nullable = true // read through the null-safe loader either way
	out := e.arrayValueFromHeaderReg(h, nullable)
	out.Ty = aty
	return out, nil
}

// emitConditionalAny emits a `cond ? a : b` where at least one branch is
// dynamic: each branch value is boxed into the NaN-boxed `any` word, so the
// result is a well-typed `any` regardless of how the branch types mix.
func (e *Emitter) emitConditionalAny(ex *ast.ConditionalExpression, uTy *Type) (Value, error) {
	thenL := e.freshLabel("ternary.then")
	elseL := e.freshLabel("ternary.else")
	mergeL := e.freshLabel("ternary.merge")

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))

	cond, err := e.emitExpr(ex.Test)
	if err != nil {
		return Value{}, err
	}
	cond = e.toBool(cond)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, thenL, elseL))

	emitBranch := func(label string, expr ast.Expression) error {
		e.emitLabel(label)
		// Built against the result, as an argument to an `any` parameter is:
		// an object literal branch is a dynamic object, not a static one boxed.
		hint := TypeAny
		if uTy != nil {
			hint = *uTy
		}
		v, err := e.emitExprWithObjectHint(expr, hint)
		if err != nil {
			return err
		}
		var boxed Value
		if uTy != nil {
			boxed = e.coerce(v, *uTy)
		} else if boxed, err = e.emitBoxValue(v); err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		return nil
	}
	if err := emitBranch(thenL, ex.Consequent); err != nil {
		return Value{}, err
	}
	if err := emitBranch(elseL, ex.Alternate); err != nil {
		return Value{}, err
	}

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resPtr))
	if uTy != nil {
		return Value{Ref: result, Ty: *uTy}, nil
	}
	return Value{Ref: result, Ty: TypeAny}, nil
}

// nullCoalesceBoxResult reports the result type of `left ?? right` when left is
// a *constrained* nullable box — the `T | undefined` a `(number | undefined)[]`
// element reads as — and right is a plain scalar. The nullish half is gone from
// the result: a right operand of the box's own single kind leaves the bare `T`
// (`xs[i] ?? 0` is a number, usable in arithmetic); any other scalar leaves the
// non-nullable union of the members and the right operand. ok is false for bare
// any/unknown and for a nullable/dynamic right operand, which keep the `any`
// result. Shared by emission and inferExprType so the two cannot disagree.
func nullCoalesceBoxResult(left, right Type) (Type, bool) {
	if !left.IsDynamic || len(left.UnionMembers) == 0 || !isSelfDescribingBox(left) {
		return Type{}, false
	}
	rk := scalarTypeKind(right)
	if rk == "" || right.Nullable || (right.IR == "ptr" && !isStringTy(right)) {
		return Type{}, false
	}
	members := left.UnionMembers
	covered := false
	for _, m := range members {
		if scalarTypeKind(m) == rk {
			covered = true
		}
	}
	if covered && len(members) == 1 {
		return members[0], true
	}
	if !covered {
		members = append(append([]Type{}, members...), right)
	}
	return Type{IR: TypeAny.IR, IsDynamic: true, UnionMembers: members}, true
}

// emitNullCoalesceBox emits `left ?? right` for nullCoalesceBoxResult's shape:
// a nullish box evaluates the right operand, a present one unboxes/re-boxes the
// left value into resTy.
func (e *Emitter) emitNullCoalesceBox(left Value, rightExpr ast.Expression, resTy Type) (Value, error) {
	resSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resSlot, resTy.IR, resTy.Align()))
	tag, _ := e.emitUnboxTagPayload(left)
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	isUndef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
	isNullish := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isNullish, isNull, isUndef))
	nullishL := e.freshLabel("nullc.box.nullish")
	presentL := e.freshLabel("nullc.box.present")
	mergeL := e.freshLabel("nullc.box.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish, nullishL, presentL))

	e.emitLabel(nullishL)
	right, err := e.emitExpr(rightExpr)
	if err != nil {
		return Value{}, err
	}
	right = e.coerce(right, resTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resTy.IR, right.Ref, resSlot, resTy.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(presentL)
	present := left
	present.Ty.Nullable, present.Ty.IsUndefined = false, false
	present = e.coerce(present, resTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resTy.IR, present.Ref, resSlot, resTy.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, resTy.IR, resSlot, resTy.Align()))
	return Value{Ref: result, Ty: resTy}, nil
}

// ternaryUnion reports the union result type of a ternary whose branches are
// scalars of different JS kinds (see nullCoalesceUnion, whose kind rule it
// shares). Shared by emission and inferExprType so the two cannot disagree.
func ternaryUnion(a, b Type) (Type, bool) {
	// An UncheckedIndex branch (`a[i]`, a spawnSync stdout) is plain `T` to
	// tsc; its run-time absence rides the branch value into the box.
	a, b = a.staticIndexType(), b.staticIndexType()
	if a.Nullable || b.Nullable || a.IsNull || b.IsNull || a.IsDynamic || b.IsDynamic {
		return Type{}, false
	}
	return nullCoalesceUnion(a, b)
}

// emitConditionalNullableScalar emits a `cond ? a : b` whose result type is a
// nullable scalar (nty, one branch null and the other a non-pointer scalar).
// Each branch is boxed into the { i1, T } aggregate via
// emitNullableScalarBoxedValue (a null literal → absent, a scalar → present),
// stored, and reloaded — the same store/load-through-an-alloca shape the plain
// ternary uses, over the presence-flagged storage type. See ADR-00538.
func (e *Emitter) emitConditionalNullableScalar(ex *ast.ConditionalExpression, nty Type) (Value, error) {
	thenL := e.freshLabel("ternary.then")
	elseL := e.freshLabel("ternary.else")
	mergeL := e.freshLabel("ternary.merge")

	storageIR := nullableScalarStorageIR(nty)
	align := storageAlign(nty)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, storageIR, align))

	cond, err := e.emitExpr(ex.Test)
	if err != nil {
		return Value{}, err
	}
	cond = e.toBool(cond)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, thenL, elseL))

	e.emitLabel(thenL)
	thenAgg, err := e.emitNullableScalarBoxedValue(ex.Consequent, nty)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", storageIR, thenAgg, resPtr, align))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(elseL)
	elseAgg, err := e.emitNullableScalarBoxedValue(ex.Alternate, nty)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", storageIR, elseAgg, resPtr, align))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, storageIR, resPtr, align))
	return Value{Ref: result, Ty: nty}, nil
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
			// Still typed as the operands' union when they differ in kind
			// (nullCoalesceUnion) — inference cannot see the narrowing.
			if uTy, ok := nullCoalesceUnion(payload.Ty, e.inferExprType(ex.Right)); ok {
				return e.coerce(payload, uTy), nil
			}
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
	// A dynamic (any/unknown) left operand: its runtime null/undefined lives in
	// the NaN-box tag, not a bare `ptr`. Test the tag and fall through to the
	// right operand only when the box actually holds null or undefined; the
	// result is itself an any-box (`let x: any = null; x ?? 7` is 7).
	if left.Ty.IsDynamic {
		return e.emitNullCoalesceDynamic(left, ex.Right)
	}
	// A `T[] | undefined` array left operand (a nested-array element absence,
	// TDD-00221): the {ptr,i64} aggregate is nullish exactly when its data-ptr is
	// null. Fall through to the right operand then, else keep the array.
	if left.Ty.IsArray && left.Ty.Nullable {
		return e.emitNullCoalesceArray(left, ex.Right)
	}
	// `null ?? x` / `undefined ?? x`: the left is statically nullish, so the
	// whole expression *is* the right operand, with the right's own type — not
	// the left's null (ptr) type. Without this the ptr-slot path below would
	// coerce a non-ptr right (a number/boolean) to ptr, and `coerce(number,
	// ptr)` silently returns the number, emitting an invalid `store ptr
	// <double>`. Matches JS (`null ?? 42 === 42`) and keeps the IR well-typed.
	// Keyed on IsNull (the literal `null`/`undefined`, both IsNull) — a runtime
	// `T | undefined` *pointer* union (a null-flagged ptr from wrapUndefinedable,
	// IsUndefined but not IsNull, TDD-00187) is NOT statically nullish and must
	// fall through to the runtime null test below.
	if left.Ty.IsNull {
		return e.emitExpr(ex.Right)
	}
	if left.Ty.IR != "ptr" {
		return left, nil
	}
	// A string left with a number/boolean right (`s ?? 42`): the result is the
	// union of the two (nullCoalesceUnion).
	if uTy, ok := nullCoalesceUnion(left.Ty.withoutNullable(), e.inferExprType(ex.Right)); ok {
		nonNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", nonNull, left.Ref))
		return e.emitNullCoalesceUnion(nonNull, Value{Ref: left.Ref, Ty: left.Ty.withoutNullable()}, ex.Right, uTy)
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
	// The result slot is a single `ptr`, so a non-ptr right operand (`str | null
	// ?? 42`) is a genuine `ptr | number` union this compiler can't represent —
	// reject cleanly rather than let `coerce(number, ptr)` fall through and emit
	// an invalid `store ptr <double>`.
	rightTy := right.Ty
	right, err = e.coerceChecked(right, TypePtr, ex.GetPos(), "?? operands")
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", right.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(noNullL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", left.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: nullCoalescePtrResult(left.Ty, rightTy)}, nil
}

// nullCoalescePtrResult is the type of `left ?? right` for a pointer left
// operand (string, object, class instance, Map…): the left's own type with its
// nullability dropped — `(row ?? dflt).name` is a field read off a Row — unless
// the right operand can itself be absent (`a ?? b` with `b?: string`, `a ??
// null`), in which case the result stays nullable, with the right's
// null-vs-undefined flavour. A left that carries no structure of its own (the
// literal-typed bare pointer) takes the right's. Shared by emission and
// inferExprType so the two cannot disagree.
func nullCoalescePtrResult(left, right Type) Type {
	res := left.withoutNullable()
	res.IsUndefined = false
	if right.IR == "ptr" && !right.IsNull && right.IsObject && !res.IsObject {
		res = right.withoutNullable()
		res.IsUndefined = false
	}
	if right.IsNull || right.Nullable {
		res.Nullable = true
		res.IsUndefined = right.IsUndefined
	}
	return res
}

// emitNullCoalesceArray implements `a ?? b` when the left operand is a
// `T[] | undefined` array value (TDD-00221): it is nullish exactly when the
// {ptr,i64} aggregate's data-ptr is null (an absent nested-array element). When
// present the whole expression is the left array; when absent it is the right
// operand, coerced to the (non-nullable) array type.
func (e *Emitter) emitNullCoalesceArray(left Value, rightExpr ast.Expression) (Value, error) {
	resTy := nullCoalesceArrayResult(left.Ty, e.inferExprType(rightExpr))
	if resTy.IsDynamic {
		return e.emitNullCoalesceArrayAny(left, rightExpr)
	}

	resSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca {ptr, i64}, align 8", resSlot))
	isAbsent := e.emitArrayIsAbsent(left)

	absentL := e.freshLabel("nullc.arr.absent")
	presentL := e.freshLabel("nullc.arr.present")
	mergeL := e.freshLabel("nullc.arr.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAbsent, absentL, presentL))

	e.emitLabel(absentL)
	right, err := e.emitExprWithObjectHint(rightExpr, resTy)
	if err != nil {
		return Value{}, err
	}
	right = e.coerce(right, resTy)
	e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", right.Ref, resSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(presentL)
	e.emitInstr(fmt.Sprintf("store {ptr, i64} %s, ptr %s, align 8", left.Ref, resSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", result, resSlot))
	return Value{Ref: result, Ty: resTy}, nil
}

// nullCoalesceArrayResult is the type of `xs ?? r` for a nullable array left
// operand: the array type (still nullable when `r` can itself be absent) for an
// array or nullish right operand, and `any` for anything else (`xs ?? "none"`
// is `T[] | string`). Shared by emission and inferExprType.
func nullCoalesceArrayResult(left, right Type) Type {
	if !right.IsArray && !right.IsNull {
		return TypeAny
	}
	res := left
	res.Nullable, res.IsUndefined = false, false
	if right.IsNull || right.Nullable {
		res.Nullable, res.IsUndefined = true, right.IsUndefined
	}
	return res
}

// emitNullCoalesceArrayAny is `xs ?? r` with a non-array `r`: each side is boxed
// into the `any` word.
func (e *Emitter) emitNullCoalesceArrayAny(left Value, rightExpr ast.Expression) (Value, error) {
	resSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", resSlot, TypeAny.IR))
	absentL := e.freshLabel("nullc.arr.absent")
	presentL := e.freshLabel("nullc.arr.present")
	mergeL := e.freshLabel("nullc.arr.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.emitArrayIsAbsent(left), absentL, presentL))

	e.emitLabel(absentL)
	right, err := e.emitExpr(rightExpr)
	if err != nil {
		return Value{}, err
	}
	rb, err := e.emitBoxValue(right)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", TypeAny.IR, rb.Ref, resSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(presentL)
	present := left
	present.Ty.Nullable, present.Ty.IsUndefined = false, false
	lb, err := e.emitBoxValue(present)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", TypeAny.IR, lb.Ref, resSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", result, TypeAny.IR, resSlot))
	return Value{Ref: result, Ty: TypeAny}, nil
}

// emitNullCoalesceDynamic implements `a ?? b` when the left operand is a dynamic
// (any/unknown) NaN-boxed value: it is nullish only when its tag is null or
// undefined. The result is an any-box holding the left value when present, else
// the right operand coerced to any.
func (e *Emitter) emitNullCoalesceDynamic(left Value, rightExpr ast.Expression) (Value, error) {
	if resTy, ok := nullCoalesceBoxResult(left.Ty, e.inferExprType(rightExpr)); ok {
		return e.emitNullCoalesceBox(left, rightExpr, resTy)
	}
	resSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resSlot))

	tag, _ := e.emitUnboxTagPayload(left)
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
	isUndef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
	isNullish := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isNullish, isNull, isUndef))

	nullishL := e.freshLabel("nullc.dyn.nullish")
	presentL := e.freshLabel("nullc.dyn.present")
	mergeL := e.freshLabel("nullc.dyn.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish, nullishL, presentL))

	e.emitLabel(nullishL)
	var right Value
	var err error
	if len(left.Ty.UnionMembers) > 0 {
		// A union's fallback is built as the member it is (`opts ?? {}` an
		// options object), then boxed.
		right, err = e.emitExprWithObjectHint(rightExpr, left.Ty)
	} else {
		right, err = e.emitExpr(rightExpr)
	}
	if err != nil {
		return Value{}, err
	}
	right = e.coerce(right, TypeAny)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", right.Ref, resSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(presentL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", left.Ref, resSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resSlot))
	return Value{Ref: result, Ty: TypeAny}, nil
}

// ternaryNullablePointerType is the result of a ternary with one null branch
// and one pointer branch (an object, a class instance, a string): the
// pointer type, nullable. ok is false for any other pair.
func ternaryNullablePointerType(a, b Type) (Type, bool) {
	ptr := func(t Type) bool {
		return !t.IsNull && !t.IsArray && !t.IsDynamic && (t.IsObject || isStringTy(t))
	}
	switch {
	case a.IsNull && ptr(b):
		b.Nullable = true
		return b, true
	case b.IsNull && ptr(a):
		a.Nullable = true
		return a, true
	}
	return Type{}, false
}

// isNumericOperand reports whether t is a plain number of any width (not a
// Date, bigint, box or nullable).
func isNumericOperand(t Type) bool {
	return (t.Float || t.IsInteger()) && !t.IsDate && !t.IsBigInt && !t.IsDynamic && !isNullableScalar(t) && !t.IsArray && !t.IsObject
}

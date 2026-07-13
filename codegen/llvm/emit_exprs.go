// emit_exprs.go — core expression emission, type inference helpers, coercion,
// var declarations, and the conditional ternary operator.
package llvm

import (
	"fmt"
	"strconv"
	"strings"
	"KlainMainLang/ast"
)

func (e *Emitter) emitExpr(expr ast.Expression) (Value, error) {
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		return e.emitNumberLit(ex)
	case *ast.StringLiteral:
		ptr := e.internString(ex.Value)
		return Value{Ref: ptr, Ty: TypePtr}, nil
	case *ast.BooleanLiteral:
		v := "0"
		if ex.Value {
			v = "1"
		}
		return Value{Ref: v, Ty: TypeBool}, nil
	case *ast.Identifier:
		return e.emitIdent(ex)
	case *ast.BinaryExpression:
		if ex.Op == "??" {
			return e.emitNullCoalesce(ex)
		}
		return e.emitBinary(ex)
	case *ast.UnaryExpression:
		return e.emitUnary(ex)
	case *ast.UpdateExpression:
		return e.emitUpdate(ex)
	case *ast.AssignmentExpression:
		return e.emitAssign(ex)
	case *ast.CallExpression:
		return e.emitCall(ex)
	case *ast.IndexExpression:
		return e.emitIndex(ex)
	case *ast.MemberExpression:
		return e.emitMember(ex)
	case *ast.SpreadElement:
		return Value{}, fmt.Errorf("%d:%d: spread element must be used inside an array literal", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.ArrayLiteral:
		return Value{}, fmt.Errorf("%d:%d: array literal must be used in a variable declaration", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.NewArrayExpression:
		return Value{}, fmt.Errorf("%d:%d: new Array() must be used in a variable declaration", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.NewMapExpression:
		return Value{}, fmt.Errorf("%d:%d: new Map() must be used in a variable declaration", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.NewSetExpression:
		return Value{}, fmt.Errorf("%d:%d: new Set() must be used in a variable declaration", ex.GetPos().Line, ex.GetPos().Col)
	case *ast.NewErrorExpression:
		return e.emitNewError(ex)
	case *ast.NewDateExpression:
		return e.emitNewDate(ex)
	case *ast.ObjectLiteral:
		return e.emitObjectLiteral(ex)
	case *ast.ArrowFunction:
		return e.emitArrowFunction(ex)
	case *ast.TemplateLiteral:
		return e.emitTemplateLiteral(ex)
	case *ast.ConditionalExpression:
		return e.emitConditional(ex)
	case *ast.NullLiteral:
		if ex.IsUndefined {
			return Value{Ref: "null", Ty: TypeUndefined}, nil
		}
		return Value{Ref: "null", Ty: TypeNull}, nil
	case *ast.AwaitExpression:
		return e.emitAwait(ex)
	}
	return Value{}, fmt.Errorf("unknown expression type %T", expr)
}

func (e *Emitter) emitNumberLit(n *ast.NumberLiteral) (Value, error) {
	v := n.Value
	if strings.ContainsRune(v, '.') {
		return Value{Ref: v, Ty: TypeF64}, nil
	}
	// Hex (0x), binary (0b), octal (0o) — convert to decimal for LLVM IR.
	if len(v) >= 2 && v[0] == '0' && (v[1]|32 == 'x' || v[1]|32 == 'b' || v[1]|32 == 'o') {
		n64, err := strconv.ParseInt(v, 0, 64)
		if err != nil {
			return Value{}, fmt.Errorf("invalid numeric literal %q: %v", v, err)
		}
		return Value{Ref: fmt.Sprintf("%d", n64), Ty: TypeI64}, nil
	}
	return Value{Ref: v, Ty: TypeI64}, nil
}

func (e *Emitter) emitIdent(id *ast.Identifier) (Value, error) {
	sym, ok := e.lookup(id.Name)
	if !ok {
		// Bare NaN/Infinity globals (real JS also has these outside the
		// Number.* namespace) — only after a local lookup miss, so a
		// user-declared variable of the same name still shadows them.
		switch id.Name {
		case "NaN":
			return Value{Ref: "0x7FF8000000000000", Ty: TypeF64}, nil
		case "Infinity":
			return Value{Ref: "0x7FF0000000000000", Ty: TypeF64}, nil
		}
		return Value{}, fmt.Errorf("%d:%d: undefined variable '%s'", id.GetPos().Line, id.GetPos().Col, id.Name)
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, sym.Ty.IR, sym.Ptr, sym.Ty.Align()))
	return Value{Ref: reg, Ty: sym.Ty}, nil
}

func (e *Emitter) emitBinary(ex *ast.BinaryExpression) (Value, error) {
	left, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}
	right, err := e.emitExpr(ex.Right)
	if err != nil {
		return Value{}, err
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
		} else if ty.Signed {
			e.emitInstr(fmt.Sprintf("%s = sdiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = udiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		}
		return Value{Ref: reg, Ty: ty}, nil
	case "%":
		if ty.Float {
			e.emitInstr(fmt.Sprintf("%s = frem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else if ty.Signed {
			e.emitInstr(fmt.Sprintf("%s = srem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = urem %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		}
		return Value{Ref: reg, Ty: ty}, nil

	case "<", ">", "<=", ">=", "==", "!=", "===", "!==":
		boolTy := TypeBool
		if ty.Float {
			fop := map[string]string{
				"<": "olt", ">": "ogt", "<=": "ole", ">=": "oge",
				"==": "oeq", "!=": "one", "===": "oeq", "!==": "one",
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

	case "&&":
		// Simplified: both operands must already be i1
		l := e.toBool(left)
		r := e.toBool(right)
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", reg, l.Ref, r.Ref))
		return Value{Ref: reg, Ty: TypeBool}, nil
	case "||":
		l := e.toBool(left)
		r := e.toBool(right)
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", reg, l.Ref, r.Ref))
		return Value{Ref: reg, Ty: TypeBool}, nil

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
	case "<<":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = shl i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case ">>":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = ashr i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case ">>>":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = lshr i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	}

	return Value{}, fmt.Errorf("unknown binary operator '%s'", ex.Op)
}

// typeofString maps a compiled type to its TypeScript typeof string.
func typeofString(ty Type) string {
	switch {
	case ty.IsFunc:
		return "function"
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

	oldReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", oldReg, sym.Ty.IR, sym.Ptr, sym.Ty.Align()))

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

func (e *Emitter) emitAssign(ex *ast.AssignmentExpression) (Value, error) {
	// Array element assignment: arr[i] = val  or  arr[i] += val
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		gepReg, elemTy, err := e.emitIndexPtr(idxEx)
		if err != nil {
			return Value{}, err
		}
		var rhs Value
		if ex.Op == "=" {
			rhs, err = e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
		} else {
			// Compound: load current element, apply op, store
			curReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curReg, elemTy.IR, gepReg, elemTy.Align()))
			cur := Value{Ref: curReg, Ty: elemTy}
			rhsVal, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			if err := dateCompoundAssignGuard(ex.Op, elemTy.IsDate, rhsVal.Ty.IsDate); err != nil {
				return Value{}, fmt.Errorf("%d:%d: %s", ex.GetPos().Line, ex.GetPos().Col, err)
			}
			rhsVal = e.coerce(rhsVal, elemTy)
			rhs, err = e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, elemTy)
			if err != nil {
				return Value{}, err
			}
		}
		rhs = e.coerce(rhs, elemTy)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, rhs.Ref, gepReg, elemTy.Align()))
		return rhs, nil
	}

	// Object field assignment: obj.field = val  or  pts[i].field = val  (or compound ops)
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		objVal, err := e.emitExpr(memEx.Object)
		if err != nil {
			return Value{}, err
		}
		if !objVal.Ty.IsObject {
			return Value{}, fmt.Errorf("field assignment on non-object")
		}
		idx, fieldTy, ok := objVal.Ty.FieldIndex(memEx.Property)
		if !ok {
			return Value{}, fmt.Errorf("no field '%s'", memEx.Property)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
		var rhs Value
		if ex.Op == "=" {
			rhs, err = e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
		} else {
			curReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curReg, fieldTy.IR, gepReg, fieldTy.Align()))
			cur := Value{Ref: curReg, Ty: fieldTy}
			rhsVal, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			if err := dateCompoundAssignGuard(ex.Op, fieldTy.IsDate, rhsVal.Ty.IsDate); err != nil {
				return Value{}, fmt.Errorf("%d:%d: %s", ex.GetPos().Line, ex.GetPos().Col, err)
			}
			rhsVal = e.coerce(rhsVal, fieldTy)
			rhs, err = e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, fieldTy)
			if err != nil {
				return Value{}, err
			}
		}
		rhs = e.coerce(rhs, fieldTy)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, rhs.Ref, gepReg, fieldTy.Align()))
		return rhs, nil
	}

	// Scalar variable assignment
	ident, ok := ex.Left.(*ast.Identifier)
	if !ok {
		return Value{}, fmt.Errorf("can only assign to identifiers or array elements")
	}
	sym, ok := e.lookup(ident.Name)
	if !ok {
		return Value{}, fmt.Errorf("undefined variable '%s'", ident.Name)
	}

	if sym.Ty.IsDynamic && ex.Op != "=" {
		return Value{}, fmt.Errorf("%d:%d: compound assignment ('%s') on any/unknown is not yet supported", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
	}

	var rhs Value
	if ex.Op == "=" {
		var err error
		rhs, err = e.emitExpr(ex.Right)
		if err != nil {
			return Value{}, err
		}
	} else {
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, sym.Ty.IR, sym.Ptr, sym.Ty.Align()))
		cur := Value{Ref: loadReg, Ty: sym.Ty}

		rhsVal, err := e.emitExpr(ex.Right)
		if err != nil {
			return Value{}, err
		}
		if err := dateCompoundAssignGuard(ex.Op, sym.Ty.IsDate, rhsVal.Ty.IsDate); err != nil {
			return Value{}, fmt.Errorf("%d:%d: %s", ex.GetPos().Line, ex.GetPos().Col, err)
		}
		rhsVal = e.coerce(rhsVal, sym.Ty)

		op := strings.TrimSuffix(ex.Op, "=")
		rhs, err = e.emitArith(op, cur, rhsVal, sym.Ty)
		if err != nil {
			return Value{}, err
		}
	}

	if sym.Ty.IsDynamic {
		var err error
		rhs, err = e.emitBoxValue(rhs)
		if err != nil {
			return Value{}, err
		}
	} else {
		rhs = e.coerce(rhs, sym.Ty)
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, rhs.Ref, sym.Ptr, sym.Ty.Align()))
	return rhs, nil
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

func (e *Emitter) emitArith(op string, left, right Value, ty Type) (Value, error) {
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
		} else if ty.Signed {
			e.emitInstr(fmt.Sprintf("%s = sdiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
		} else {
			e.emitInstr(fmt.Sprintf("%s = udiv %s %s, %s", reg, ty.IR, left.Ref, right.Ref))
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
	case "<<":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = shl i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case ">>":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = ashr i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	case ">>>":
		li := e.coerce(left, TypeI64)
		ri := e.coerce(right, TypeI64)
		e.emitInstr(fmt.Sprintf("%s = lshr i64 %s, %s", reg, li.Ref, ri.Ref))
		return Value{Ref: reg, Ty: TypeI64}, nil
	default:
		return Value{}, fmt.Errorf("unknown arithmetic operator '%s'", op)
	}
	return Value{Ref: reg, Ty: ty}, nil
}

// emitConditional emits a ternary expression cond ? consequent : alternate.
// Uses an alloca+store/load pattern so both branches can produce a single result.
func (e *Emitter) emitConditional(ex *ast.ConditionalExpression) (Value, error) {
	ty := e.inferExprType(ex.Consequent)
	if ty.IsArray {
		return Value{}, fmt.Errorf("%d:%d: ternary operator is not supported for array types", ex.GetPos().Line, ex.GetPos().Col)
	}

	thenL  := e.freshLabel("ternary.then")
	elseL  := e.freshLabel("ternary.else")
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
	left, err := e.emitExpr(ex.Left)
	if err != nil {
		return Value{}, err
	}
	if left.Ty.IR != "ptr" {
		return left, nil
	}

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, left.Ref))

	nullL   := e.freshLabel("nullc.null")
	noNullL := e.freshLabel("nullc.nn")
	mergeL  := e.freshLabel("nullc.merge")

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

	// Determine the result type before emitting branches.
	var resultTy Type
	if ex.Property == "length" && !objVal.Ty.IsObject {
		resultTy = TypeI64
	} else if objVal.Ty.IsObject {
		_, fieldTy, ok := objVal.Ty.FieldIndex(ex.Property)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: no field '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
		}
		resultTy = fieldTy
	} else {
		return Value{}, fmt.Errorf("%d:%d: optional chaining '?.' does not support property '%s' on type %s",
			ex.GetPos().Line, ex.GetPos().Col, ex.Property, objVal.Ty.IR)
	}

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", resPtr, resultTy.IR, resultTy.Align()))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, objVal.Ref))

	nullL   := e.freshLabel("optc.null")
	noNullL := e.freshLabel("optc.nn")
	mergeL  := e.freshLabel("optc.merge")

	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, noNullL))

	// null branch: store zero value
	e.emitLabel(nullL)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resultTy.IR, zeroRef(resultTy), resPtr, resultTy.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// non-null branch: perform the property access on objVal
	e.emitLabel(noNullL)
	var propVal Value
	if ex.Property == "length" {
		e.ensureStrlen()
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", r, objVal.Ref))
		propVal = Value{Ref: r, Ty: TypeI64}
	} else {
		idx, fieldTy, _ := objVal.Ty.FieldIndex(ex.Property)
		gepReg := e.freshReg()
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d",
			gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
			loadReg, fieldTy.IR, gepReg, fieldTy.Align()))
		propVal = Value{Ref: loadReg, Ty: fieldTy}
	}
	propVal = e.coerce(propVal, resultTy)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", resultTy.IR, propVal.Ref, resPtr, resultTy.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, resultTy.IR, resPtr, resultTy.Align()))
	return Value{Ref: result, Ty: resultTy}, nil
}

// emitIndexPtr computes and returns the GEP register pointing to arr[index].
// The array object may be a named variable (Symbol path) or any expression
// that returns a {ptr, i64} aggregate (extractvalue path).
func (e *Emitter) emitIndexPtr(ex *ast.IndexExpression) (gepReg string, elemTy Type, err error) {
	var dataPtrReg string

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
	}

	idxVal, err := e.emitExpr(ex.Index)
	if err != nil {
		return "", TypeVoid, err
	}
	idxVal = e.coerce(idxVal, TypeI64)

	gepReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, elemTy.IR, dataPtrReg, idxVal.Ref))
	return gepReg, elemTy, nil
}

func (e *Emitter) emitIndex(ex *ast.IndexExpression) (Value, error) {
	// process.env["KEY"]: dynamic-key environment variable lookup.
	if isProcessEnvExpr(ex.Object) {
		return e.emitProcessEnvGetDynamic(ex.Index)
	}
	// Group map access: grouped["key"] → sub-array.
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && sym.Ty.IsGroupMap {
			return e.emitGroupMapIndex(sym, ex.Index, ex.GetPos())
		}
	}
	// String indexing: s[i] returns a single-character string.
	if id, ok := ex.Object.(*ast.Identifier); ok {
		if sym, found := e.lookup(id.Name); found && isStringTy(sym.Ty) {
			strPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", strPtr, sym.Ptr))
			return e.emitStringCharAt(strPtr, ex.Index)
		}
	}
	// Array indexing.
	gepReg, elemTy, err := e.emitIndexPtr(ex)
	if err != nil {
		return Value{}, err
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, elemTy.IR, gepReg, elemTy.Align()))
	return Value{Ref: reg, Ty: elemTy}, nil
}

func (e *Emitter) emitMember(ex *ast.MemberExpression) (Value, error) {
	if ex.Optional {
		return e.emitOptionalMember(ex)
	}
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "Number" {
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
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "Math" {
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
	if id, ok := ex.Object.(*ast.Identifier); ok && id.Name == "process" {
		switch ex.Property {
		case "argv":
			return e.emitProcessArgv()
		case "pid":
			return e.emitProcessPid()
		case "platform":
			return Value{Ref: e.internString(nodePlatformName()), Ty: TypePtr}, nil
		}
	}
	if isProcessEnvExpr(ex.Object) {
		return e.emitProcessEnvGetStatic(ex.Property)
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
		}
	}
	if ex.Property == "length" {
		// Named array variable: load length from its LenPtr alloca.
		if id, ok := ex.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsArray {
				reg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", reg, sym.LenPtr))
				return Value{Ref: reg, Ty: TypeI64}, nil
			}
		}
		// Any other expression: evaluate it, then dispatch on the result type.
		objVal, err := e.emitExpr(ex.Object)
		if err != nil {
			return Value{}, err
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
	if !objVal.Ty.IsObject {
		return Value{}, fmt.Errorf("%d:%d: field access on non-object (no field '%s')", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
	}
	idx, fieldTy, ok := objVal.Ty.FieldIndex(ex.Property)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: no field '%s'", ex.GetPos().Line, ex.GetPos().Col, ex.Property)
	}
	gepReg := e.freshReg()
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, fieldTy.IR, gepReg, fieldTy.Align()))
	return Value{Ref: result, Ty: fieldTy}, nil
}

// emitTemplateLiteral builds the concatenated result of a template literal.
func (e *Emitter) emitTemplateLiteral(tl *ast.TemplateLiteral) (Value, error) {
	acc := Value{Ref: e.internString(tl.Quasis[0]), Ty: TypePtr}
	for i, expr := range tl.Exprs {
		val, err := e.emitExpr(expr)
		if err != nil {
			return Value{}, err
		}
		strVal, err := e.emitValueToString(val)
		if err != nil {
			return Value{}, fmt.Errorf("%d:%d: %w", tl.GetPos().Line, tl.GetPos().Col, err)
		}
		acc, err = e.emitStringConcat(acc, strVal)
		if err != nil {
			return Value{}, err
		}
		tail := Value{Ref: e.internString(tl.Quasis[i+1]), Ty: TypePtr}
		acc, err = e.emitStringConcat(acc, tail)
		if err != nil {
			return Value{}, err
		}
	}
	return acc, nil
}

// emitValueToString converts any value to a null-terminated string ptr.
// Strings pass through; numbers and bools are formatted via sprintf into a 32-byte scratch buffer.
func (e *Emitter) emitValueToString(v Value) (Value, error) {
	if v.Ty.IsDynamic {
		return e.emitDynamicToString(v)
	}
	if v.Ty.IsNull {
		label := "null"
		if v.Ty.IsUndefined {
			label = "undefined"
		}
		return Value{Ref: e.internString(label), Ty: TypePtr}, nil
	}
	if v.Ty.IR == "ptr" && !v.Ty.IsObject && !v.Ty.IsArray && !v.Ty.IsFunc {
		// Nullable string: at runtime select "null" string when ptr is null.
		if v.Ty.Nullable {
			isNull := e.freshReg()
			result := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s",
				result, isNull, e.internString("null"), v.Ref))
			return Value{Ref: result, Ty: TypePtr}, nil
		}
		return v, nil
	}
	e.ensureSprintf()
	e.ensureMalloc()
	scratch := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", scratch))
	switch {
	case v.Ty.IR == "i1":
		truePtr := e.internString("true")
		falsePtr := e.internString("false")
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, v.Ref, truePtr, falsePtr))
		return Value{Ref: r, Ty: TypePtr}, nil
	case v.Ty.Float:
		val := v
		if v.Ty.IR == "float" {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fpext float %s to double", r, v.Ref))
			val = Value{Ref: r, Ty: TypeF64}
		}
		fmtPtr := e.internString("%g")
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, double %s)", scratch, fmtPtr, val.Ref))
	case v.Ty.IsInteger():
		val := v
		if v.Ty.IR != "i64" {
			r := e.freshReg()
			ext := "sext"
			if !v.Ty.Signed {
				ext = "zext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to i64", r, ext, v.Ty.IR, v.Ref))
			val = Value{Ref: r, Ty: TypeI64}
		}
		fmtPtr := e.internString("%lld")
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", scratch, fmtPtr, val.Ref))
	default:
		return Value{}, fmt.Errorf("cannot convert type %s to string in template literal", v.Ty.IR)
	}
	return Value{Ref: scratch, Ty: TypePtr}, nil
}

// inferArrayType picks an element type by looking at the first element of a literal.
func (e *Emitter) inferArrayType(lit *ast.ArrayLiteral) Type {
	if len(lit.Elements) == 0 {
		return ArrayOf(TypeI64) // default: number[]
	}
	first := lit.Elements[0]
	if sp, ok := first.(*ast.SpreadElement); ok {
		// Spread of an array — infer from the spread source.
		if ty := e.inferExprType(sp.Arg); ty.IsArray {
			return ty
		}
		return ArrayOf(TypeI64)
	}
	return ArrayOf(e.inferExprType(first))
}

// inferObjectType determines field types by inspecting the literal's values.
// inferObjectType computes the merged field layout for an object literal.
// A field's position is fixed by its first occurrence (via a spread's source
// fields or an explicit property); a later property or spread with the same
// name overrides its type in place rather than moving it — matching JS's
// object spread semantics, where re-assigning an existing key doesn't change
// its enumeration order.
func (e *Emitter) inferObjectType(lit *ast.ObjectLiteral) Type {
	var fields []Field
	upsert := func(f Field) {
		for i, existing := range fields {
			if existing.Name == f.Name {
				fields[i] = f
				return
			}
		}
		fields = append(fields, f)
	}
	for _, prop := range lit.Properties {
		if spread, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" {
			srcTy := e.inferExprType(spread.Arg)
			for _, f := range srcTy.Fields {
				upsert(f)
			}
			continue
		}
		upsert(Field{Name: prop.Key, Ty: e.inferExprType(prop.Value)})
	}
	return ObjectType(fields)
}

func (e *Emitter) inferExprType(expr ast.Expression) Type {
	switch ex := expr.(type) {
	case *ast.NumberLiteral:
		if strings.ContainsRune(ex.Value, '.') {
			return TypeF64
		}
		return TypeI64
	case *ast.BooleanLiteral:
		return TypeBool
	case *ast.StringLiteral:
		return TypePtr
	case *ast.TemplateLiteral:
		return TypePtr
	case *ast.NullLiteral:
		if ex.IsUndefined {
			return TypeUndefined
		}
		return TypeNull
	case *ast.AwaitExpression:
		// Unwrap Promise<T> → T.
		argTy := e.inferExprType(ex.Argument)
		if argTy.IsPromise {
			if argTy.PromiseType != nil {
				return *argTy.PromiseType
			}
			return TypeVoid
		}
		return TypeI64
	case *ast.Identifier:
		if sym, ok := e.lookup(ex.Name); ok {
			return sym.Ty
		}
		switch ex.Name {
		case "NaN", "Infinity":
			return TypeF64
		}
		if _, ok := e.funcs[ex.Name]; ok {
			return Type{IR: "ptr", IsFunc: true}
		}
	case *ast.IndexExpression:
		if isProcessEnvExpr(ex.Object) {
			return TypePtr
		}
		objTy := e.inferExprType(ex.Object)
		if objTy.IsGroupMap {
			if objTy.ElemType != nil {
				return ArrayOf(*objTy.ElemType)
			}
			return ArrayOf(TypeI64)
		}
		if isStringTy(objTy) {
			return TypePtr
		}
		if objTy.IsArray && objTy.ElemType != nil {
			return *objTy.ElemType
		}
	case *ast.BinaryExpression:
		switch ex.Op {
		case "===", "!==", "==", "!=", "<", ">", "<=", ">=":
			return TypeBool
		case "+":
			lt := e.inferExprType(ex.Left)
			rt := e.inferExprType(ex.Right)
			if isStringTy(lt) || isStringTy(rt) {
				return TypePtr
			}
			// Date + number / number + Date: add a duration, stays a Date
			// (see emitBinary for the full Date-arithmetic rules).
			if lt.IsDate != rt.IsDate {
				return TypeDate
			}
			return TypeI64
		case "-":
			lt := e.inferExprType(ex.Left)
			rt := e.inferExprType(ex.Right)
			// Date - Date: a plain number (ms difference). Date - number:
			// subtract a duration, stays a Date. number - Date is rejected
			// by emitBinary, so its inferred type here is moot.
			if lt.IsDate && rt.IsDate {
				return TypeI64
			}
			if lt.IsDate && !rt.IsDate {
				return TypeDate
			}
			return TypeI64
		case "&&", "||":
			return e.inferExprType(ex.Left)
		case "??":
			lt := e.inferExprType(ex.Left)
			if lt.IR == "ptr" {
				return e.inferExprType(ex.Right)
			}
			return lt
		case "&", "|", "^", "<<", ">>", ">>>":
			return TypeI64
		}
	case *ast.MemberExpression:
		if id, ok := ex.Object.(*ast.Identifier); ok {
			if members, found := e.enums[id.Name]; found {
				if val, ok := members[ex.Property]; ok {
					return val.Ty
				}
			}
		}
		if ex.Property == "size" {
			if id, ok := ex.Object.(*ast.Identifier); ok {
				if sym, found := e.lookup(id.Name); found && (sym.Ty.IsMap || sym.Ty.IsSet) {
					return TypeI64
				}
			}
		}
		if id, ok := ex.Object.(*ast.Identifier); ok {
			switch id.Name {
			case "Math":
				switch ex.Property {
				case "PI", "E", "LN2", "LN10", "SQRT2", "LOG2E", "LOG10E":
					return TypeF64
				}
			case "Number":
				switch ex.Property {
				case "MAX_SAFE_INTEGER", "MIN_SAFE_INTEGER":
					return TypeI64
				case "EPSILON", "MAX_VALUE", "MIN_VALUE", "POSITIVE_INFINITY", "NEGATIVE_INFINITY", "NaN":
					return TypeF64
				}
			case "process":
				switch ex.Property {
				case "argv":
					return ArrayOf(TypePtr)
				case "pid":
					return TypeI64
				case "platform":
					return TypePtr
				}
			}
		}
		if isProcessEnvExpr(ex.Object) {
			return TypePtr
		}
		// General object field read: any expression whose type is an object,
		// not just a bare identifier — e.g. a field access chained off
		// another field access (ev.when.getFullYear() needs to know
		// ev.when's type before it can resolve getFullYear on it).
		if objTy := e.inferExprType(ex.Object); objTy.IsObject {
			if _, fieldTy, ok := objTy.FieldIndex(ex.Property); ok {
				return fieldTy
			}
		}
	case *ast.CallExpression:
		// If calling a named function, use its registered return type (handles async too).
		if id, ok := ex.Callee.(*ast.Identifier); ok {
			if sig, found := e.funcs[id.Name]; found {
				return sig.RetType
			}
			// Calling a closure-typed variable (e.g. a const-bound arrow
			// function) — same fallback resolveCallback (emit_func.go)
			// already uses, so a call's result is correctly typed regardless
			// of whether the callee is a named declaration or a value.
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsFunc && sym.Ty.FuncRetType != nil {
				return *sym.Ty.FuncRetType
			}
			switch id.Name {
			case "parseInt":
				return TypeI64
			case "parseFloat":
				return TypeF64
			case "isNaN", "isFinite":
				return TypeBool
			case "fetch":
				return PromiseOf(ResponseType())
			case "btoa", "atob", "encodeURIComponent", "decodeURIComponent", "encodeURI", "decodeURI":
				return TypePtr
			case "setTimeout", "setInterval":
				return TypeI64
			}
		}
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "console" {
				// Every console.* method returns void (emitConsolePrint and
				// everything that delegates to it, e.g. emitConsoleDir, all
				// return Value{Ty: TypeVoid}) — without this case, an
				// expression-bodied arrow whose only statement is e.g.
				// console.log(...) (a common HOF-callback shape, like
				// arr.forEach((n) => console.log(n))) fell through to this
				// function's blind TypeI64 fallback below, so the closure
				// got built expecting to return a number that emitExpr's
				// real (correctly void) evaluation never produces — a hard
				// clang-stage type mismatch. See docs/adr/ADR-00043.md.
				return TypeVoid
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "String" {
				switch mem.Property {
				case "fromCharCode", "fromCodePoint":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Number" {
				switch mem.Property {
				case "isInteger", "isFinite", "isNaN", "isSafeInteger":
					return TypeBool
				case "parseInt":
					return TypeI64
				case "parseFloat":
					return TypeF64
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Math" {
				switch mem.Property {
				case "random", "sqrt", "pow", "hypot", "log", "log2", "log10", "sin", "cos", "tan",
					"asin", "acos", "atan", "atan2", "sinh", "cosh", "tanh", "cbrt", "expm1", "log1p":
					return TypeF64
				case "floor", "ceil", "round", "trunc", "sign":
					return TypeI64
				case "abs":
					if len(ex.Args) == 1 {
						return e.inferExprType(ex.Args[0])
					}
				case "min", "max", "clamp":
					if len(ex.Args) > 0 {
						return e.inferExprType(ex.Args[0])
					}
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "JSON" {
				switch mem.Property {
				case "stringify":
					return TypePtr
				case "parse":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Date" && mem.Property == "now" {
				return TypeDate
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Date" && mem.Property == "parse" {
				return TypeI64
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "performance" && mem.Property == "now" {
				return TypeF64
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "fs" {
				switch mem.Property {
				case "readFileSync":
					return TypePtr
				case "existsSync":
					return TypeBool
				case "readdirSync":
					return ArrayOf(TypePtr)
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "process" {
				switch mem.Property {
				case "readLineSync", "execFileSync", "cwd":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "crypto" {
				switch mem.Property {
				case "getRandomValues":
					if len(ex.Args) == 1 {
						return e.inferExprType(ex.Args[0])
					}
				case "randomUUID":
					return TypePtr
				}
			}
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "Object" {
				switch mem.Property {
				case "groupBy":
					if len(ex.Args) >= 1 {
						arrTy := e.inferExprType(ex.Args[0])
						if arrTy.IsArray && arrTy.ElemType != nil {
							et := *arrTy.ElemType
							return Type{IR: "ptr", IsGroupMap: true, ElemType: &et}
						}
					}
					return Type{IR: "ptr", IsGroupMap: true}
				case "keys", "values":
					return ArrayOf(TypePtr)
				case "entries":
					entryTy := ObjectType([]Field{{Name: "key", Ty: TypePtr}, {Name: "value", Ty: TypePtr}})
					return ArrayOf(entryTy)
				}
			}
		}
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 {
				if sym, found := e.lookup(id.Name); found && sym.Ty.IsMap {
					switch mem.Property {
					case "get":
						if sym.Ty.MapVal != nil {
							return *sym.Ty.MapVal
						}
					case "has", "delete":
						return TypeBool
					case "keys":
						if sym.Ty.MapKey != nil {
							return ArrayOf(*sym.Ty.MapKey)
						}
					case "values":
						if sym.Ty.MapVal != nil {
							return ArrayOf(*sym.Ty.MapVal)
						}
					case "set":
						return sym.Ty
					}
				}
				if sym, found := e.lookup(id.Name); found && sym.Ty.IsSet {
					switch mem.Property {
					case "has", "delete":
						return TypeBool
					case "add":
						return sym.Ty
					case "values":
						if sym.Ty.MapKey != nil {
							return ArrayOf(*sym.Ty.MapKey)
						}
					}
				}
			}
		}
		if mem, ok := ex.Callee.(*ast.MemberExpression); ok {
			switch mem.Property {
			case "getTime", "valueOf", "getFullYear", "getMonth", "getDate", "getDay",
				"getHours", "getMinutes", "getSeconds", "getMilliseconds",
				"setFullYear", "setMonth", "setDate", "setHours", "setMinutes",
				"setSeconds", "setMilliseconds", "setTime":
				if e.inferExprType(mem.Object).IsDate {
					return TypeI64
				}
			case "toISOString", "toDateString", "toLocaleDateString":
				if e.inferExprType(mem.Object).IsDate {
					return TypePtr
				}
			case "text":
				if e.inferExprType(mem.Object).IsResponse {
					return TypePtr
				}
			case "json":
				if e.inferExprType(mem.Object).IsResponse {
					// No declaration context here to parse into (that's
					// handled separately, see emitResponseJSON) — TypePtr
					// matches bare JSON.parse's own default-context type.
					return TypePtr
				}
			case "split":
				return ArrayOf(TypePtr)
			case "substring", "trim", "toUpperCase", "toLowerCase", "replace":
				if isStringTy(e.inferExprType(mem.Object)) {
					return TypePtr
				}
			case "indexOf", "charCodeAt", "findIndex", "codePointAt", "search", "localeCompare":
				return TypeI64
			case "includes", "startsWith", "endsWith", "some", "every":
				return TypeBool
			case "join", "repeat", "padStart", "padEnd", "toFixed", "charAt":
				return TypePtr
			case "at":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray && objTy.ElemType != nil {
					return *objTy.ElemType
				}
				return TypePtr // string.at returns a char string
			case "concat", "reverse", "fill":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray {
					return objTy
				}
			case "slice":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray {
					return objTy
				}
				return TypePtr // string.slice
			case "map":
				if len(ex.Args) == 1 {
					if af, ok := ex.Args[0].(*ast.ArrowFunction); ok {
						var retTy Type
						if af.RetType != nil {
							retTy = e.resolveType(af.RetType)
						} else if af.Body != nil {
							retTy = e.inferExprType(af.Body)
						} else {
							retTy = TypeI64
						}
						return ArrayOf(retTy)
					}
				}
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray {
					return objTy
				}
			case "filter":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray {
					return objTy
				}
			case "find":
				objTy := e.inferExprType(mem.Object)
				if objTy.IsArray && objTy.ElemType != nil {
					return *objTy.ElemType
				}
			case "reduce":
				if len(ex.Args) == 2 {
					return e.inferExprType(ex.Args[1])
				}
			}
		}
	case *ast.UnaryExpression:
		if ex.Op == "typeof" {
			return TypePtr
		}
	case *ast.ConditionalExpression:
		return e.inferExprType(ex.Consequent)
	case *ast.NewErrorExpression:
		return errorObjType
	case *ast.NewDateExpression:
		return TypeDate
	case *ast.ObjectLiteral:
		return e.inferObjectType(ex)
	case *ast.ArrowFunction:
		params := make([]Type, len(ex.Params))
		for i, p := range ex.Params {
			if p.Type == nil {
				params[i] = TypeI64
				params[i].Inferred = true // no annotation — see docs/adr/ADR-00042.md
			} else {
				params[i] = e.resolveType(p.Type)
			}
		}
		var ret Type
		if ex.RetType != nil {
			ret = e.resolveType(ex.RetType)
		} else if ex.Body != nil {
			ret = e.inferExprType(ex.Body)
		} else if blockHasReturn(ex.Block) {
			// Same best-effort inference emitArrowFunctionWithHints uses when
			// actually emitting this closure — this duplicate exists because
			// inferExprType has to answer "what type is this arrow function"
			// before any closure value exists yet (e.g. right when a `const`
			// binding to it is being declared). The two computations used to
			// disagree (this one unconditionally defaulted to TypeI64
			// regardless of what was returned), which silently mistyped the
			// variable itself even though the actual closure body was
			// correctly built to return an object/array/Date — a real bug,
			// not just a missed optimization, since callers trust this type.
			paramNames := make([]string, len(ex.Params))
			for i, p := range ex.Params {
				paramNames[i] = p.Name
			}
			if inferred, ok := e.inferUnannotatedReturnType(ex.Block, paramNames, params); ok {
				ret = inferred
			} else {
				ret = TypeI64
			}
		} else {
			ret = TypeVoid
		}
		return FuncType(params, ret)
	}
	return TypeI64
}

// --- Helpers ---

// toBool converts a Value to i1 via icmp ne 0.
func (e *Emitter) toBool(v Value) Value {
	if v.Ty.IR == "i1" {
		return v
	}
	reg := e.freshReg()
	if v.Ty.Float {
		e.emitInstr(fmt.Sprintf("%s = fcmp one %s %s, 0.0", reg, v.Ty.IR, v.Ref))
	} else {
		e.emitInstr(fmt.Sprintf("%s = icmp ne %s %s, 0", reg, v.Ty.IR, v.Ref))
	}
	return Value{Ref: reg, Ty: TypeBool}
}

// coerce inserts a type conversion instruction if necessary.
func (e *Emitter) coerce(v Value, target Type) Value {
	// null/undefined assigned to a non-ptr type becomes the zero value.
	if v.Ty.IsNull && target.IR != "ptr" {
		return Value{Ref: zeroRef(target), Ty: target}
	}
	if v.Ty.IR == target.IR {
		return v
	}
	reg := e.freshReg()

	srcInt := v.Ty.IsInteger()
	dstInt := target.IsInteger()

	switch {
	// int → int (same size handled above, so either widen or narrow)
	case srcInt && dstInt:
		srcBits := typeBits(v.Ty.IR)
		dstBits := typeBits(target.IR)
		if dstBits > srcBits {
			ext := "sext"
			if !v.Ty.Signed {
				ext = "zext"
			}
			e.emitInstr(fmt.Sprintf("%s = %s %s %s to %s", reg, ext, v.Ty.IR, v.Ref, target.IR))
		} else {
			e.emitInstr(fmt.Sprintf("%s = trunc %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		}

	// int → float
	case srcInt && target.Float:
		op := "sitofp"
		if !v.Ty.Signed {
			op = "uitofp"
		}
		e.emitInstr(fmt.Sprintf("%s = %s %s %s to %s", reg, op, v.Ty.IR, v.Ref, target.IR))

	// float → int
	case v.Ty.Float && dstInt:
		op := "fptosi"
		if !target.Signed {
			op = "fptoui"
		}
		e.emitInstr(fmt.Sprintf("%s = %s %s %s to %s", reg, op, v.Ty.IR, v.Ref, target.IR))

	// float → float
	case v.Ty.Float && target.Float:
		srcBits := typeBits(v.Ty.IR)
		dstBits := typeBits(target.IR)
		if dstBits > srcBits {
			e.emitInstr(fmt.Sprintf("%s = fpext %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		} else {
			e.emitInstr(fmt.Sprintf("%s = fptrunc %s %s to %s", reg, v.Ty.IR, v.Ref, target.IR))
		}

	default:
		// No known coercion, return as-is
		return v
	}

	return Value{Ref: reg, Ty: target}
}

// emitToBool converts any scalar value to i1 for use in a branch.
func (e *Emitter) emitToBool(v Value) Value {
	if v.Ty.IR == "i1" {
		return v
	}
	r := e.freshReg()
	if v.Ty.IR == "ptr" {
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", r, v.Ref))
	} else if v.Ty.Float {
		e.emitInstr(fmt.Sprintf("%s = fcmp une %s %s, 0.0", r, v.Ty.IR, v.Ref))
	} else {
		e.emitInstr(fmt.Sprintf("%s = icmp ne %s %s, 0", r, v.Ty.IR, v.Ref))
	}
	return Value{Ref: r, Ty: TypeBool}
}

// emitVarDecl handles variable declarations (scalar, array, and object).
func (e *Emitter) emitVarDecl(v *ast.VarDeclaration) error {
	if init, ok := v.Init.(*ast.NewMapExpression); ok {
		return e.emitMapVarDecl(v, init)
	}
	if init, ok := v.Init.(*ast.NewSetExpression); ok {
		return e.emitSetVarDecl(v, init)
	}

	ty := e.resolveType(v.TypeAnnot)

	// Infer type from init when no annotation.
	if !ty.IsArray && !ty.IsObject && v.TypeAnnot == nil {
		switch init := v.Init.(type) {
		case *ast.NullLiteral:
			if init.IsUndefined {
				ty = TypeUndefined
			} else {
				ty = TypeNull
			}
		case *ast.StringLiteral:
			ty = TypePtr
		case *ast.TemplateLiteral:
			ty = TypePtr
		case *ast.Identifier:
			if sym, ok := e.lookup(init.Name); ok {
				ty = sym.Ty
			} else {
				switch init.Name {
				case "NaN", "Infinity":
					ty = TypeF64
				}
			}
		case *ast.IndexExpression:
			ty = e.inferExprType(init)
		case *ast.BinaryExpression:
			ty = e.inferExprType(init)
		case *ast.ArrayLiteral:
			ty = e.inferArrayType(init)
		case *ast.ObjectLiteral:
			ty = e.inferObjectType(init)
		case *ast.NewErrorExpression:
			ty = errorObjType
		case *ast.NewDateExpression:
			ty = TypeDate
		case *ast.AwaitExpression:
			ty = e.inferExprType(init)
		case *ast.ArrowFunction:
			ty = e.inferExprType(init)
		case *ast.MemberExpression:
			ty = e.inferExprType(init)
		case *ast.CallExpression:
			if callee, ok := init.Callee.(*ast.Identifier); ok {
				switch callee.Name {
				case "fetch":
					ty = PromiseOf(ResponseType())
				case "btoa", "atob", "encodeURIComponent", "decodeURIComponent", "encodeURI", "decodeURI":
					ty = TypePtr
				default:
					if sig, found := e.funcs[callee.Name]; found && (sig.RetType.IsArray || sig.RetType.IsObject || sig.RetType.IsFunc || sig.RetType.IsDate) {
						ty = sig.RetType
					} else if sym, found := e.lookup(callee.Name); found && sym.Ty.IsFunc && sym.Ty.FuncRetType != nil {
						// Calling a closure-typed variable (e.g. a const-bound
						// arrow function) rather than a named declaration —
						// same fallback as inferExprType's CallExpression case.
						retTy := *sym.Ty.FuncRetType
						if retTy.IsArray || retTy.IsObject || retTy.IsFunc || retTy.IsDate {
							ty = retTy
						}
					}
				}
			}
			// Built-in methods that return arrays with the same element type.
			if mem, ok := init.Callee.(*ast.MemberExpression); ok {
				switch mem.Property {
				case "splice":
					if arrId, ok := mem.Object.(*ast.Identifier); ok {
						if s, found := e.lookup(arrId.Name); found && s.Ty.IsArray {
							ty = s.Ty
						}
					}
				case "pop", "shift":
					if arrId, ok := mem.Object.(*ast.Identifier); ok {
						if s, found := e.lookup(arrId.Name); found && s.Ty.IsArray && s.Ty.ElemType != nil && s.Ty.ElemType.IsObject {
							ty = *s.Ty.ElemType
						}
					}
				default:
					inferred := e.inferExprType(init)
					if inferred.IR != TypeI64.IR || inferred.IsArray || inferred.IsObject {
						ty = inferred
					}
				}
			}
		case *ast.NewArrayExpression:
			if init.ElemType != nil {
				ty = ArrayOf(e.resolveType(init.ElemType))
			}
		}
	}
	if _, ok := v.Init.(*ast.NewArrayExpression); ok && !ty.IsArray {
		return fmt.Errorf("%d:%d: new Array() requires a type annotation or a type parameter, e.g. new Array<number>(n)", v.GetPos().Line, v.GetPos().Col)
	}

	if containsDynamicElement(ty) {
		return fmt.Errorf("%d:%d: any/unknown is not yet supported as an array element or object field type", v.GetPos().Line, v.GetPos().Col)
	}
	if ty.IsArray {
		return e.emitArrayVarDecl(v, ty)
	}
	if ty.IsObject {
		return e.emitObjectVarDecl(v, ty)
	}

	// If init is a float literal and no explicit type, use f64.
	if v.TypeAnnot == nil {
		if nl, ok := v.Init.(*ast.NumberLiteral); ok && strings.ContainsRune(nl.Value, '.') {
			ty = TypeF64
		}
	}

	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, ty.IR, ty.Align()))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty})

	if v.Init != nil {
		// JSON.parse needs the target type to choose number vs string deserialization.
		if ce, ok := v.Init.(*ast.CallExpression); ok {
			if mem, ok2 := ce.Callee.(*ast.MemberExpression); ok2 {
				if id, ok3 := mem.Object.(*ast.Identifier); ok3 && id.Name == "JSON" && mem.Property == "parse" {
					val, err := e.emitJSONParse(ce.Args, ty, ce.GetPos())
					if err != nil {
						return err
					}
					e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, val.Ref, ptrName, ty.Align()))
					return nil
				}
			}
			// response.json() needs the same target-type context as JSON.parse
			// itself, for exactly the same reason (emitResponseJSON delegates
			// to emitJSONParseValue once it has the buffered body string).
			if mem, ok2 := ce.Callee.(*ast.MemberExpression); ok2 {
				if mem.Property == "json" && e.inferExprType(mem.Object).IsResponse {
					val, err := e.emitResponseJSON(mem.Object, ty, ce.GetPos())
					if err != nil {
						return err
					}
					e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, val.Ref, ptrName, ty.Align()))
					return nil
				}
			}
		}
		val, err := e.emitExpr(v.Init)
		if err != nil {
			return err
		}
		if ty.IsDynamic {
			val, err = e.emitBoxValue(val)
			if err != nil {
				return err
			}
		} else {
			val = e.coerce(val, ty)
		}
		// Re-resolve the variable's current storage location rather than
		// trusting ptrName (captured above, before the initializer ran):
		// if evaluating the initializer itself created a closure that
		// captures this same variable — e.g. the self-cancelling-timer
		// idiom `const id = setInterval(() => { ...; clearInterval(id) },
		// ms)` — ADR-00001's capture-time promotion (boxing) already moved
		// v.Name from ptrName to a new shared heap cell via
		// updateSymbolInPlace. Storing into the now-stale ptrName in that
		// case would silently write the real value nowhere anyone (least
		// of all the closure itself) still reads from.
		finalPtr := ptrName
		if sym, ok := e.lookup(v.Name); ok {
			finalPtr = sym.Ptr
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, val.Ref, finalPtr, ty.Align()))
	} else if ty.IsDynamic {
		// No initializer: any/unknown default to undefined (matching JS `let x: any;`
		// -> x === undefined), rather than leaving the tag byte as uninitialized
		// garbage, which would drive real runtime branching in print/typeof/equality.
		undef, err := e.emitBoxValue(Value{Ty: TypeUndefined})
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, undef.Ref, ptrName, ty.Align()))
	}
	return nil
}

// typeBits returns the bit-width of the given LLVM IR type string.
func typeBits(ir string) int {
	switch ir {
	case "i1":
		return 1
	case "i8":
		return 8
	case "i16":
		return 16
	case "i32", "float":
		return 32
	case "i64", "double":
		return 64
	}
	return 64
}

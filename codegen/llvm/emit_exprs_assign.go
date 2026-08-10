package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// isLogicalAssignOp reports whether op is one of the three logical
// assignment operators (&&=, ||=, ??=) — genuinely short-circuiting, unlike
// every other compound-assignment operator, so they can't reuse emitArith's
// eager-evaluate-both-sides shape at all (see emitLogicalCompoundAssign).
func isLogicalAssignOp(op string) bool {
	return op == "&&=" || op == "||=" || op == "??="
}

// tryEmitAccessorAssign attempts `obj.prop = rhs` / `obj.prop OP= rhs`
// against a registered class accessor (TDD-00030). handled is false when
// objVal's class has no accessor at all for this property name — the
// caller should fall through to its own existing FieldIndex-based
// plain-field path unchanged, exactly as if this function had never been
// called.
func (e *Emitter) tryEmitAccessorAssign(objVal Value, prop, op string, rhsExpr ast.Expression, pos ast.Pos) (handled bool, result Value, err error) {
	getter, setter, ok := e.classAccessorSigs(objVal.Ty.ClassName, prop)
	if !ok {
		return false, Value{}, nil
	}
	if setter == nil {
		return true, Value{}, fmt.Errorf("%d:%d: property '%s' has no setter", pos.Line, pos.Col, prop)
	}
	// emitLogicalCompoundAssign is generic over "any assignable ptr+Type
	// lvalue" specifically because it can cheaply re-load the same memory
	// location across its short-circuit branches — an accessor has no such
	// location, and invoking a getter/setter more than once per
	// source-level use risks an observable double-invocation of user code
	// with side effects. Not attempted for V1 — see docs/tdd/TDD-00030.md.
	if isLogicalAssignOp(op) {
		return true, Value{}, fmt.Errorf("%d:%d: logical assignment ('%s') on a getter/setter property is not yet supported", pos.Line, pos.Col, op)
	}

	paramTy := setter.ParamTypes[0]
	var rhs Value
	if op == "=" {
		// Hint-aware (TDD-00028/TDD-00007): `obj.prop = [1,2,3]`/`obj.prop
		// = {...}` coerces against the setter's own declared parameter type.
		rhs, err = e.emitExprWithObjectHint(rhsExpr, paramTy)
		if err != nil {
			return true, Value{}, err
		}
	} else {
		if getter == nil {
			return true, Value{}, fmt.Errorf("%d:%d: property '%s' has no getter (required to read the current value for '%s')", pos.Line, pos.Col, prop, op)
		}
		cur, err := e.emitClassCall(objVal.Ty, objVal, accessorMethodName("get", prop), nil, pos, false)
		if err != nil {
			return true, Value{}, err
		}
		rhsVal, err := e.emitExpr(rhsExpr)
		if err != nil {
			return true, Value{}, err
		}
		if err := dateCompoundAssignGuard(op, paramTy.IsDate, rhsVal.Ty.IsDate); err != nil {
			return true, Value{}, fmt.Errorf("%d:%d: %s", pos.Line, pos.Col, err)
		}
		rhsVal = e.coerce(rhsVal, paramTy)
		rhs, err = e.emitArith(strings.TrimSuffix(op, "="), cur, rhsVal, paramTy, pos)
		if err != nil {
			return true, Value{}, err
		}
	}
	rhs = e.coerce(rhs, paramTy)
	if _, err := e.emitClassSetterCall(objVal.Ty, objVal, accessorMethodName("set", prop), rhs, pos); err != nil {
		return true, Value{}, err
	}
	return true, rhs, nil
}

// emitLogicalCompoundAssign implements &&=/||=/??= against an lvalue whose
// storage is already resolved to a single ptr+Type pair — the shape shared
// by every assignable form this compiler has (scalar variable, array
// element, object field, static field). The right side is only evaluated
// (and only then stored) down the branch where the operator's own
// short-circuit rule requires it; the other branch leaves the existing
// value at ptr untouched. Reloading from ptr at the merge point yields the
// correct final value either way, so no separate result temporary is
// needed — unlike emitNullCoalesce/emitConditional, which need one since
// their two branches produce a value that was never already sitting in a
// shared memory location. `??=` against a non-ptr-typed location can never
// trigger (mirrors emitNullCoalesce's own "left can never be null" fast
// path for non-ptr types) — the right side is never evaluated, exactly like
// bare `x ?? y`.
func (e *Emitter) emitLogicalCompoundAssign(op, ptr string, ty Type, rhsExpr ast.Expression) (Value, error) {
	curReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curReg, ty.IR, ptr, ty.Align()))
	cur := Value{Ref: curReg, Ty: ty}

	if op == "??=" && ty.IR != "ptr" {
		return cur, nil
	}

	var cond Value
	switch op {
	case "&&=":
		cond = e.toBool(cur)
	case "||=":
		notReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notReg, e.toBool(cur).Ref))
		cond = Value{Ref: notReg, Ty: TypeBool}
	case "??=":
		nullReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", nullReg, cur.Ref))
		cond = Value{Ref: nullReg, Ty: TypeBool}
	default:
		return Value{}, fmt.Errorf("unknown logical assignment operator '%s'", op)
	}

	storeL := e.freshLabel("logassign.store")
	mergeL := e.freshLabel("logassign.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, storeL, mergeL))

	e.emitLabel(storeL)
	rhs, err := e.emitExpr(rhsExpr)
	if err != nil {
		return Value{}, err
	}
	rhs = e.coerce(rhs, ty)
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, rhs.Ref, ptr, ty.Align()))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", result, ty.IR, ptr, ty.Align()))
	return Value{Ref: result, Ty: ty}, nil
}

func (e *Emitter) emitAssign(ex *ast.AssignmentExpression) (Value, error) {
	// Static field assignment: ClassName.staticField = val (or compound
	// ops) — TDD-00009 Stage 4. A bare class-name identifier is a
	// compile-time namespace, never a real runtime value, so this must be
	// checked before memEx.Object is evaluated generically below (same
	// reasoning the read-side check in emitMember follows).
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		if id, ok := memEx.Object.(*ast.Identifier); ok {
			if info, found := e.classes[id.Name]; found {
				return e.emitStaticFieldAssign(info, id.Name, memEx.Property, ex.Op, ex.Right, ex.GetPos())
			}
		}
	}
	// Dynamic object bracket assignment: obj[expr] = val (or compound ops) —
	// a computed-key object literal is a real Map<string,V>, so this must be
	// checked before emitIndexPtr, which only understands array storage.
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		if id, ok := idxEx.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsDynamicObject {
				mapPtr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, sym.Ptr))
				return e.emitDynamicObjectAssign(sym.Ty, mapPtr, idxEx.Index, ex.Op, ex.Right, ex.GetPos())
			}
		} else if objTy := e.inferExprType(idxEx.Object); objTy.IsDynamicObject {
			objVal, err := e.emitExpr(idxEx.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynamicObjectAssign(objVal.Ty, objVal.Ref, idxEx.Index, ex.Op, ex.Right, ex.GetPos())
		}
	}
	// Array element assignment: arr[i] = val  or  arr[i] += val
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		gepReg, elemTy, err := e.emitIndexPtr(idxEx)
		if err != nil {
			return Value{}, err
		}
		if isLogicalAssignOp(ex.Op) {
			return e.emitLogicalCompoundAssign(ex.Op, gepReg, elemTy, ex.Right)
		}
		var rhs Value
		if ex.Op == "=" {
			// Hint-aware (TDD-00028): `arr[i] = [1,2,3]` when elemTy is
			// itself array-typed builds/coerces against elemTy instead of
			// erroring — the same reasoning emitExprWithObjectHint already
			// established for object literals (TDD-00007).
			rhs, err = e.emitExprWithObjectHint(ex.Right, elemTy)
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
			rhs, err = e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, elemTy, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
		}
		rhs = e.coerce(rhs, elemTy)
		e.storeArrayElem(gepReg, elemTy, rhs)
		return rhs, nil
	}

	// Array variable reassignment: arr = val (the whole array, not
	// arr[i] = val). A separate branch from the generic scalar-variable
	// case below, because this compiler represents an array as two
	// allocas (Ptr/LenPtr — see the project's own Array value duality note), not
	// the single alloca every other assignable form uses; the generic
	// scalar path's single "store %s %s, ptr sym.Ptr" would try to store a
	// whole {ptr,i64} aggregate into sym.Ptr's plain "alloca ptr" slot, a
	// hard clang-stage type mismatch. Found while wiring TDD-00028's
	// array-literal-as-general-expression fix — a real, pre-existing,
	// unrelated bug (arr = otherArrayVar already failed identically before
	// any array-literal changes, confirmed directly).
	if ident, ok := ex.Left.(*ast.Identifier); ok {
		if sym, found := e.lookup(ident.Name); found && sym.Ty.IsArray {
			if ex.Op != "=" {
				return Value{}, fmt.Errorf("%d:%d: compound assignment ('%s') is not supported on an array variable", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
			}
			if sym.IsConst {
				return Value{}, fmt.Errorf("%d:%d: cannot assign to '%s' because it is a constant", ex.GetPos().Line, ex.GetPos().Col, ident.Name)
			}
			val, err := e.emitExprWithObjectHint(ex.Right, sym.Ty)
			if err != nil {
				return Value{}, err
			}
			if !val.Ty.IsArray {
				return Value{}, fmt.Errorf("%d:%d: cannot assign a non-array value to array variable '%s'", ex.GetPos().Line, ex.GetPos().Col, ident.Name)
			}
			if err := e.storeArrayAggregateInto(val, sym.Ptr, sym.LenPtr); err != nil {
				return Value{}, err
			}
			return val, nil
		}
	}

	// Object field assignment: obj.field = val  or  pts[i].field = val  (or compound ops)
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		objVal, err := e.emitExpr(memEx.Object)
		if err != nil {
			return Value{}, err
		}
		if objVal.Ty.IsDynamicObject {
			keyExpr := ast.NewStringLiteral(memEx.Property, memEx.GetPos())
			return e.emitDynamicObjectAssign(objVal.Ty, objVal.Ref, keyExpr, ex.Op, ex.Right, ex.GetPos())
		}
		if !objVal.Ty.IsObject {
			return Value{}, fmt.Errorf("field assignment on non-object")
		}
		// TDD-00030: a class accessor (getter/setter) is checked before the
		// plain-field FieldIndex path below — an accessor-only property
		// name is never a real Field, so FieldIndex would otherwise report
		// "no field" for it. Every non-accessor class, and every non-class
		// object, falls through unchanged.
		if objVal.Ty.IsClass {
			if handled, result, err := e.tryEmitAccessorAssign(objVal, memEx.Property, ex.Op, ex.Right, ex.GetPos()); err != nil {
				return Value{}, err
			} else if handled {
				return result, nil
			}
		}
		idx, fieldTy, ok := objVal.Ty.FieldIndex(memEx.Property)
		if !ok {
			return Value{}, fmt.Errorf("no field '%s'", memEx.Property)
		}
		if objVal.Ty.IsClass {
			if err := e.checkFieldVisibility(objVal.Ty.ClassName, memEx.Property, ex.GetPos()); err != nil {
				return Value{}, err
			}
		}
		e.emitFrozenCheck(objVal.Ref)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
		if isLogicalAssignOp(ex.Op) {
			return e.emitLogicalCompoundAssign(ex.Op, gepReg, fieldTy, ex.Right)
		}
		var rhs Value
		if ex.Op == "=" {
			// Hint-aware (TDD-00028/TDD-00007): `obj.field = [1,2,3]`/`obj.field
			// = {...}` coerces against the field's own declared type.
			rhs, err = e.emitExprWithObjectHint(ex.Right, fieldTy)
			if err != nil {
				return Value{}, err
			}
		} else {
			curReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curReg, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
			cur := Value{Ref: curReg, Ty: fieldTy}
			rhsVal, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			if err := dateCompoundAssignGuard(ex.Op, fieldTy.IsDate, rhsVal.Ty.IsDate); err != nil {
				return Value{}, fmt.Errorf("%d:%d: %s", ex.GetPos().Line, ex.GetPos().Col, err)
			}
			rhsVal = e.coerce(rhsVal, fieldTy)
			rhs, err = e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, fieldTy, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
		}
		rhs = e.coerce(rhs, fieldTy)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), rhs.Ref, gepReg, fieldTy.Align()))
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

	if sym.IsConst {
		return Value{}, fmt.Errorf("%d:%d: cannot assign to '%s' because it is a constant", ex.GetPos().Line, ex.GetPos().Col, ident.Name)
	}

	if sym.Ty.IsDynamic && ex.Op != "=" {
		return Value{}, fmt.Errorf("%d:%d: compound assignment ('%s') on any/unknown is not yet supported", ex.GetPos().Line, ex.GetPos().Col, ex.Op)
	}

	if isLogicalAssignOp(ex.Op) {
		return e.emitLogicalCompoundAssign(ex.Op, sym.Ptr, sym.Ty, ex.Right)
	}

	var rhs Value
	if ex.Op == "=" {
		var err error
		// Hint-aware (TDD-00028/TDD-00007): sym.Ty is never array-typed
		// here (that case is handled by its own branch above), so this
		// only matters for an object-literal-typed scalar variable, but
		// costs nothing to route through uniformly.
		rhs, err = e.emitExprWithObjectHint(ex.Right, sym.Ty)
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
		rhs, err = e.emitArith(op, cur, rhsVal, sym.Ty, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
	}

	if sym.Ty.IsDynamic {
		var err error
		if sym.Ty.UnionMembers != nil && !unionAllowsAssignmentFrom(sym.Ty, rhs.Ty) {
			return Value{}, fmt.Errorf("%d:%d: value's type is not a member of '%s's declared union type", ex.GetPos().Line, ex.GetPos().Col, ident.Name)
		}
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

package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

func (e *Emitter) emitAssign(ex *ast.AssignmentExpression) (Value, error) {
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
		if objVal.Ty.IsDynamicObject {
			keyExpr := ast.NewStringLiteral(memEx.Property, memEx.GetPos())
			return e.emitDynamicObjectAssign(objVal.Ty, objVal.Ref, keyExpr, ex.Op, ex.Right, ex.GetPos())
		}
		if !objVal.Ty.IsObject {
			return Value{}, fmt.Errorf("field assignment on non-object")
		}
		idx, fieldTy, ok := objVal.Ty.FieldIndex(memEx.Property)
		if !ok {
			return Value{}, fmt.Errorf("no field '%s'", memEx.Property)
		}
		e.emitFrozenCheck(objVal.Ref)
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
			rhs, err = e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, fieldTy)
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

// emit_stmts_forof_any.go — `for (const x of v)` where v is a bare any/unknown
// value (ADR-01077): a D1 dynamic array or a static array boxed into `any`.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitForOfAny drives the loop by the value's runtime `length` and per-index
// reads through the dynamic member-get (so both a dynarr and a boxed static
// array iterate, each by its own element-kind rules), binding the loop
// variable as `any`. A value with no numeric `length` — a number, a plain
// object, null/undefined — throws Node's "is not iterable" TypeError.
// Perf note: each element read goes through a numeric-string key; a direct
// per-tag element walk is the documented follow-up.
func (e *Emitter) emitForOfAny(s *ast.ForOfStatement, condL, bodyL, incL, endL string) error {
	iterVal, err := e.emitExpr(s.Iterable)
	if err != nil {
		return err
	}
	iterVal, err = e.emitBoxValue(iterVal)
	if err != nil {
		return err
	}
	e.ensureSprintf()
	e.ensureMalloc()

	// The iterable is evaluated once; keep it in a slot for the loop.
	iterSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iterSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", iterVal.Ref, iterSlot))

	// Only an array-shaped box is iterable here: a dynarr (tag 11) or a boxed
	// static array (tag 7). Everything else is Node's TypeError.
	tag, _ := e.emitUnboxTagPayload(iterVal)
	isDyn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynArray))
	isArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, isDyn, isArr))
	okL := e.freshLabel("forof.any.ok")
	badL := e.freshLabel("forof.any.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, badL))
	e.emitLabel(badL)
	e.emitThrowTypeError(forOfIterableName(s.Iterable) + " is not iterable")
	e.emitLabel(okL)

	lenVal, err := e.emitDynAnyMemberGetNamed(iterVal, e.internString("length"), "length", s.GetPos())
	if err != nil {
		return err
	}
	_, lenPay := e.emitUnboxTagPayload(lenVal)
	lenD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", lenD, lenPay))
	lenI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", lenI, lenD))
	lenSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenI, lenSlot))

	idxSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxSlot))

	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	varPtr := e.freshReg()
	if !isPattern {
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", varPtr))
		e.define(s.VarName, Symbol{Ptr: varPtr, Ty: TypeAny})
	}

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	e.emitSafepoint()
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idxSlot))
	// Reload the length each pass: a push inside the body extends the walk,
	// as it does in JS.
	curIter := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curIter, iterSlot))
	curLen, err := e.emitDynAnyMemberGetNamed(Value{Ref: curIter, Ty: TypeAny}, e.internString("length"), "length", s.GetPos())
	if err != nil {
		return err
	}
	_, curPay := e.emitUnboxTagPayload(curLen)
	curD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", curD, curPay))
	curI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", curI, curD))
	cond := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", cond, i, curI))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, bodyL, endL))

	e.emitLabel(bodyL)
	i2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i2, idxSlot))
	key := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", key))
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", key, e.internString("%lld"), i2))
	iter2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", iter2, iterSlot))
	elem, err := e.emitDynAnyMemberGet(Value{Ref: iter2, Ty: TypeAny}, key, s.GetPos())
	if err != nil {
		return err
	}
	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(elem.Ref, TypeAny, s.ObjectPattern, s.GetPos()); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if err := e.unpackDynArrayPatternInto(elem, s.ArrayPattern, s.GetPos()); err != nil {
			return err
		}
	default:
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", elem.Ref, varPtr))
	}
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	i3 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i3, idxSlot))
	i4 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i4, i3))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i4, idxSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// forOfIterableName renders the iterable expression for the "is not
// iterable" message the way Node does for the common shapes (`x`, `o.p`,
// `o[k]`); anything else is Node's generic "object".
func forOfIterableName(expr ast.Expression) string {
	switch x := expr.(type) {
	case *ast.Identifier:
		return x.Name
	case *ast.MemberExpression:
		return forOfIterableName(x.Object) + "." + x.Property
	case *ast.IndexExpression:
		return forOfIterableName(x.Object) + "[...]"
	}
	return "object"
}

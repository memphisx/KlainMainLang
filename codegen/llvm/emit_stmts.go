// emit_stmts.go — statement emission: emitStmt, emitReturn, emitFor, emitWhile,
// emitIf, emitForOf, emitBreak, emitContinue, emitSwitch.
package llvm

import (
	"fmt"
	"KlainMainLang/ast"
)

// namedLabel is one entry in Emitter.namedLabelStack: a label name and the
// break/continue target labels the loop it decorates registered for it.
// continueL is empty for label targets that don't support continue (there
// are none today, since only the five loop forms call pushPendingLabel, but
// emitContinue still guards against an empty continueL defensively).
type namedLabel struct {
	name      string
	breakL    string
	continueL string
}

// pushPendingLabel registers breakL/continueL under the label set by the
// enclosing LabeledStatement (if any) and returns a cleanup func to pop it —
// call sites should `defer e.pushPendingLabel(endL, continueL)()` right after
// computing their own labels, mirroring the existing breakStack/continueStack
// push+defer-pop pattern. A no-op (returns a no-op cleanup) when there's no
// pending label, so it's always safe to call unconditionally.
func (e *Emitter) pushPendingLabel(breakL, continueL string) func() {
	if e.pendingLabel == "" {
		return func() {}
	}
	name := e.pendingLabel
	e.pendingLabel = ""
	e.namedLabelStack = append(e.namedLabelStack, namedLabel{name: name, breakL: breakL, continueL: continueL})
	return func() { e.namedLabelStack = e.namedLabelStack[:len(e.namedLabelStack)-1] }
}

// lookupNamedLabel searches namedLabelStack innermost-first for name.
func (e *Emitter) lookupNamedLabel(name string) (namedLabel, bool) {
	for i := len(e.namedLabelStack) - 1; i >= 0; i-- {
		if e.namedLabelStack[i].name == name {
			return e.namedLabelStack[i], true
		}
	}
	return namedLabel{}, false
}

func (e *Emitter) emitStmt(stmt ast.Statement) error {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		return e.emitVarDecl(s)
	case *ast.FunctionDeclaration:
		return fmt.Errorf("%d:%d: nested function declarations are not supported", s.GetPos().Line, s.GetPos().Col)
	case *ast.ReturnStatement:
		return e.emitReturn(s)
	case *ast.ForStatement:
		return e.emitFor(s)
	case *ast.ForOfStatement:
		return e.emitForOf(s)
	case *ast.ForInStatement:
		return e.emitForIn(s)
	case *ast.WhileStatement:
		return e.emitWhile(s)
	case *ast.DoWhileStatement:
		return e.emitDoWhile(s)
	case *ast.LabeledStatement:
		e.pendingLabel = s.Label
		err := e.emitStmt(s.Body)
		e.pendingLabel = "" // clear even if Body never consumed it (non-loop label)
		return err
	case *ast.IfStatement:
		return e.emitIf(s)
	case *ast.SwitchStatement:
		return e.emitSwitch(s)
	case *ast.BreakStatement:
		return e.emitBreak(s)
	case *ast.ContinueStatement:
		return e.emitContinue(s)
	case *ast.ArrayDestructuring:
		return e.emitArrayDestructuring(s)
	case *ast.ObjectDestructuring:
		return e.emitObjectDestructuring(s)
	case *ast.BlockStatement:
		e.pushScope()
		for _, child := range s.Body {
			if err := e.emitStmt(child); err != nil {
				return err
			}
		}
		e.popScope()
		return nil
	case *ast.ExpressionStatement:
		_, err := e.emitExpr(s.Expr)
		return err
	case *ast.InterfaceDeclaration, *ast.TypeAliasDeclaration, *ast.EnumDeclaration:
		return nil // registered in pre-pass; no IR emitted
	case *ast.ThrowStatement:
		return e.emitThrow(s)
	case *ast.TryStatement:
		return e.emitTry(s)
	}
	return fmt.Errorf("unknown statement type %T", stmt)
}

func (e *Emitter) emitReturn(r *ast.ReturnStatement) error {
	// Async functions: store result directly in the malloc'd promise slot, branch to async-ret.
	if e.isAsync {
		if r.Value != nil && e.currentPromiseTy.IR != "void" && e.currentPromiseTy.IR != "" {
			val, err := e.emitExpr(r.Value)
			if err != nil {
				return err
			}
			val = e.coerce(val, e.currentPromiseTy)
			align := e.currentPromiseTy.Align()
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d",
				e.currentPromiseTy.IR, val.Ref, e.coroHdl, align))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", e.coroRetLabel))
		return nil
	}

	if r.Value == nil {
		if e.currentRetType.IR == "void" {
			e.emitTerminator("ret void")
		} else {
			e.emitTerminator(fmt.Sprintf("ret %s 0", e.currentRetType.IR))
		}
		return nil
	}

	if e.currentRetType.IsArray {
		// Return an array variable as the aggregate {ptr, i64}.
		id, ok := r.Value.(*ast.Identifier)
		if !ok {
			return fmt.Errorf("%d:%d: can only return a named array variable from a function", r.Value.GetPos().Line, r.Value.GetPos().Col)
		}
		sym, ok := e.lookup(id.Name)
		if !ok {
			return fmt.Errorf("%d:%d: undefined variable '%s'", r.Value.GetPos().Line, r.Value.GetPos().Col, id.Name)
		}
		if !sym.Ty.IsArray {
			return fmt.Errorf("%d:%d: '%s' is not an array", r.Value.GetPos().Line, r.Value.GetPos().Col, id.Name)
		}
		ptrReg := e.freshReg()
		lenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, sym.Ptr))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, sym.LenPtr))
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
		e.emitTerminator(fmt.Sprintf("ret {ptr, i64} %s", r1))
		return nil
	}

	val, err := e.emitExpr(r.Value)
	if err != nil {
		return err
	}
	if e.currentRetType.IR != "void" && e.currentRetType.IR != "" {
		val = e.coerce(val, e.currentRetType)
	}
	e.emitTerminator(fmt.Sprintf("ret %s %s", val.Ty.IR, val.Ref))
	return nil
}

func (e *Emitter) emitFor(s *ast.ForStatement) error {
	condL := e.freshLabel("for.cond")
	bodyL := e.freshLabel("for.body")
	incL  := e.freshLabel("for.inc")
	endL  := e.freshLabel("for.end")

	e.pushScope()
	defer e.popScope()
	e.breakStack = append(e.breakStack, endL)
	defer func() { e.breakStack = e.breakStack[:len(e.breakStack)-1] }()
	e.continueStack = append(e.continueStack, incL)
	defer func() { e.continueStack = e.continueStack[:len(e.continueStack)-1] }()
	defer e.pushPendingLabel(endL, incL)()

	if s.Init != nil {
		if err := e.emitStmt(s.Init); err != nil {
			return err
		}
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	if s.Test != nil {
		cond, err := e.emitExpr(s.Test)
		if err != nil {
			return err
		}
		cond = e.toBool(cond)
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, bodyL, endL))
	} else {
		e.emitTerminator(fmt.Sprintf("br label %%%s", bodyL))
	}

	e.emitLabel(bodyL)
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	if s.Update != nil {
		if _, err := e.emitExpr(s.Update); err != nil {
			return err
		}
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

func (e *Emitter) emitWhile(s *ast.WhileStatement) error {
	condL := e.freshLabel("while.cond")
	bodyL := e.freshLabel("while.body")
	endL  := e.freshLabel("while.end")

	e.breakStack = append(e.breakStack, endL)
	defer func() { e.breakStack = e.breakStack[:len(e.breakStack)-1] }()
	e.continueStack = append(e.continueStack, condL)
	defer func() { e.continueStack = e.continueStack[:len(e.continueStack)-1] }()
	defer e.pushPendingLabel(endL, condL)()

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	cond, err := e.emitExpr(s.Test)
	if err != nil {
		return err
	}
	cond = e.toBool(cond)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, bodyL, endL))

	e.emitLabel(bodyL)
	e.pushScope()
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.popScope()
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

func (e *Emitter) emitIf(s *ast.IfStatement) error {
	thenL := e.freshLabel("if.then")
	endL  := e.freshLabel("if.end")
	elseL := endL
	if s.Alternate != nil {
		elseL = e.freshLabel("if.else")
	}

	cond, err := e.emitExpr(s.Test)
	if err != nil {
		return err
	}
	cond = e.toBool(cond)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, thenL, elseL))

	e.emitLabel(thenL)
	e.pushScope()
	if err := e.emitStmt(s.Consequent); err != nil {
		return err
	}
	e.popScope()
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))

	if s.Alternate != nil {
		e.emitLabel(elseL)
		e.pushScope()
		if err := e.emitStmt(s.Alternate); err != nil {
			return err
		}
		e.popScope()
		e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
	}

	e.emitLabel(endL)
	return nil
}

// splitArrayAggregate stores a {ptr, i64} aggregate's fields into fresh
// allocas so a for...of loop body can keep reloading them each iteration —
// used for any iterable that isn't a plain named array variable (which
// already has its own allocas to reuse directly).
func (e *Emitter) splitArrayAggregate(arrVal Value) (dataPtrAlloca, lenAlloca string) {
	dataPtrAlloca = e.freshReg()
	lenAlloca = e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dataPtrAlloca))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))
	dataEx := e.freshReg()
	lenEx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataEx, arrVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenEx, arrVal.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataEx, dataPtrAlloca))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenEx, lenAlloca))
	return dataPtrAlloca, lenAlloca
}

// emitForOf emits a for-of loop over an array, Map, or Set.
// The iterable may be a named array/Map/Set variable or any expression that
// produces a {ptr, i64} array aggregate (e.g. Object.keys(obj), arr.slice(1),
// map.values()).
func (e *Emitter) emitForOf(s *ast.ForOfStatement) error {
	condL := e.freshLabel("forof.cond")
	bodyL := e.freshLabel("forof.body")
	incL  := e.freshLabel("forof.inc")
	endL  := e.freshLabel("forof.end")

	e.pushScope()
	defer e.popScope()
	e.breakStack = append(e.breakStack, endL)
	defer func() { e.breakStack = e.breakStack[:len(e.breakStack)-1] }()
	e.continueStack = append(e.continueStack, incL)
	defer func() { e.continueStack = e.continueStack[:len(e.continueStack)-1] }()
	defer e.pushPendingLabel(endL, incL)()

	// Resolve the iterable to a data-ptr alloca and a len alloca.
	// For named variables we reuse their existing allocas (no copy).
	// For any other expression we evaluate it, extract the aggregate fields,
	// and store them into fresh allocas so the loop body can keep reloading.
	var dataPtrAlloca, lenAlloca string
	var elemTy Type

	if id, ok := s.Iterable.(*ast.Identifier); ok {
		iterSym, found := e.lookup(id.Name)
		switch {
		case found && iterSym.Ty.IsArray:
			dataPtrAlloca = iterSym.Ptr
			lenAlloca = iterSym.LenPtr
			elemTy = *iterSym.Ty.ElemType
		case found && (iterSym.Ty.IsMap || iterSym.Ty.IsSet):
			// A Set iterates its elements; a Map iterates its values (not
			// [key,value] entries — see mapOrSetValuesArray).
			valsVal, err := e.mapOrSetValuesArray(iterSym)
			if err != nil {
				return err
			}
			elemTy = *valsVal.Ty.ElemType
			dataPtrAlloca, lenAlloca = e.splitArrayAggregate(valsVal)
		default:
			return fmt.Errorf("%d:%d: '%s' is not an array, Map, or Set", s.GetPos().Line, s.GetPos().Col, id.Name)
		}
	} else {
		arrVal, err := e.emitExpr(s.Iterable)
		if err != nil {
			return err
		}
		if !arrVal.Ty.IsArray || arrVal.Ty.ElemType == nil {
			return fmt.Errorf("%d:%d: for...of requires an array, Map, or Set value", s.GetPos().Line, s.GetPos().Col)
		}
		elemTy = *arrVal.Ty.ElemType
		dataPtrAlloca, lenAlloca = e.splitArrayAggregate(arrVal)
	}

	// Internal index counter (not user-visible).
	idxPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))

	// Loop variable.
	varPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, elemTy.IR, elemTy.Align()))
	e.define(s.VarName, Symbol{Ptr: varPtr, Ty: elemTy})

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal  := e.freshReg()
	lenVal  := e.freshReg()
	condReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenVal, lenAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", condReg, idxVal, lenVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, bodyL, endL))

	e.emitLabel(bodyL)
	dataPtr := e.freshReg()
	idxVal2 := e.freshReg()
	gepReg  := e.freshReg()
	elemVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtr, dataPtrAlloca))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal2, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, elemTy.IR, dataPtr, idxVal2))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elemVal, elemTy.IR, gepReg, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, elemVal, varPtr, elemTy.Align()))

	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxVal3 := e.freshReg()
	newIdx  := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal3, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newIdx, idxVal3))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newIdx, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// emitBreak jumps to the nearest enclosing loop/switch end label, or — when
// labeled — to the named loop's end label, however many levels out that is.
func (e *Emitter) emitBreak(s *ast.BreakStatement) error {
	if s.Label != "" {
		lbl, ok := e.lookupNamedLabel(s.Label)
		if !ok {
			return fmt.Errorf("%d:%d: undefined label '%s'", s.GetPos().Line, s.GetPos().Col, s.Label)
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", lbl.breakL))
		return nil
	}
	if len(e.breakStack) == 0 {
		return fmt.Errorf("break statement outside of loop or switch")
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", e.breakStack[len(e.breakStack)-1]))
	return nil
}

func (e *Emitter) emitContinue(s *ast.ContinueStatement) error {
	if s.Label != "" {
		lbl, ok := e.lookupNamedLabel(s.Label)
		if !ok {
			return fmt.Errorf("%d:%d: undefined label '%s'", s.GetPos().Line, s.GetPos().Col, s.Label)
		}
		if lbl.continueL == "" {
			return fmt.Errorf("%d:%d: label '%s' does not label a loop; continue does not apply", s.GetPos().Line, s.GetPos().Col, s.Label)
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", lbl.continueL))
		return nil
	}
	if len(e.continueStack) == 0 {
		return fmt.Errorf("continue statement outside of a loop")
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", e.continueStack[len(e.continueStack)-1]))
	return nil
}

// emitSwitch emits a switch statement using a chain of comparison blocks
// followed by case body blocks in source order (enabling fallthrough).
func (e *Emitter) emitSwitch(s *ast.SwitchStatement) error {
	endL := e.freshLabel("switch.end")

	e.breakStack = append(e.breakStack, endL)
	defer func() { e.breakStack = e.breakStack[:len(e.breakStack)-1] }()

	disc, err := e.emitExpr(s.Discriminant)
	if err != nil {
		return err
	}
	discIsStr := isStringTy(disc.Ty)

	// Assign a body label to every case in source order.
	bodyLabels := make([]string, len(s.Cases))
	for i, c := range s.Cases {
		if c.Test == nil {
			bodyLabels[i] = e.freshLabel("switch.default")
		} else {
			bodyLabels[i] = e.freshLabel(fmt.Sprintf("switch.case.%d", i))
		}
	}

	// Collect non-default cases and the default index.
	defaultIdx := -1
	var nonDefaultIdxs []int
	for i, c := range s.Cases {
		if c.Test == nil {
			defaultIdx = i
		} else {
			nonDefaultIdxs = append(nonDefaultIdxs, i)
		}
	}

	// Generate comparison labels.
	cmpLabels := make([]string, len(nonDefaultIdxs))
	for i := range nonDefaultIdxs {
		cmpLabels[i] = e.freshLabel(fmt.Sprintf("switch.cmp.%d", i))
	}

	// Branch from current block to first comparison (or default/end).
	if len(cmpLabels) > 0 {
		e.emitTerminator(fmt.Sprintf("br label %%%s", cmpLabels[0]))
	} else if defaultIdx >= 0 {
		e.emitTerminator(fmt.Sprintf("br label %%%s", bodyLabels[defaultIdx]))
	} else {
		e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
	}

	// Emit comparison chain.
	for ci, caseIdx := range nonDefaultIdxs {
		e.emitLabel(cmpLabels[ci])
		c := s.Cases[caseIdx]

		var failTarget string
		if ci+1 < len(cmpLabels) {
			failTarget = cmpLabels[ci+1]
		} else if defaultIdx >= 0 {
			failTarget = bodyLabels[defaultIdx]
		} else {
			failTarget = endL
		}

		caseVal, err := e.emitExpr(c.Test)
		if err != nil {
			return err
		}

		var eqReg string
		if discIsStr {
			e.ensureStrcmp()
			cmpRes := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", cmpRes, disc.Ref, caseVal.Ref))
			eqReg = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eqReg, cmpRes))
		} else {
			caseVal = e.coerce(caseVal, disc.Ty)
			eqReg = e.freshReg()
			if disc.Ty.Float {
				e.emitInstr(fmt.Sprintf("%s = fcmp oeq %s %s, %s", eqReg, disc.Ty.IR, disc.Ref, caseVal.Ref))
			} else {
				e.emitInstr(fmt.Sprintf("%s = icmp eq %s %s, %s", eqReg, disc.Ty.IR, disc.Ref, caseVal.Ref))
			}
		}
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eqReg, bodyLabels[caseIdx], failTarget))
	}

	// Emit case bodies in source order (preserves fallthrough semantics).
	for i, c := range s.Cases {
		e.emitLabel(bodyLabels[i])
		e.pushScope()
		for _, stmt := range c.Body {
			if err := e.emitStmt(stmt); err != nil {
				e.popScope()
				return err
			}
		}
		e.popScope()
		// Fallthrough: jump to next case body or end.
		if i+1 < len(s.Cases) {
			e.emitTerminator(fmt.Sprintf("br label %%%s", bodyLabels[i+1]))
		} else {
			e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
		}
	}

	e.emitLabel(endL)
	return nil
}

// emitDoWhile emits a do { body } while (cond) loop.
// The body always executes at least once; the condition is checked after.
func (e *Emitter) emitDoWhile(s *ast.DoWhileStatement) error {
	bodyL := e.freshLabel("dowhile.body")
	condL := e.freshLabel("dowhile.cond")
	endL  := e.freshLabel("dowhile.end")

	e.breakStack = append(e.breakStack, endL)
	defer func() { e.breakStack = e.breakStack[:len(e.breakStack)-1] }()
	e.continueStack = append(e.continueStack, condL)
	defer func() { e.continueStack = e.continueStack[:len(e.continueStack)-1] }()
	defer e.pushPendingLabel(endL, condL)()

	e.emitTerminator(fmt.Sprintf("br label %%%s", bodyL))

	e.emitLabel(bodyL)
	e.pushScope()
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.popScope()
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	cond, err := e.emitExpr(s.Test)
	if err != nil {
		return err
	}
	cond = e.toBool(cond)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, bodyL, endL))

	e.emitLabel(endL)
	return nil
}

// emitForIn emits a for (const key in obj) loop over object field names.
// Keys are the compile-time field names of obj's static type; the loop
// variable is bound as a string (ptr) in each iteration.
func (e *Emitter) emitForIn(s *ast.ForInStatement) error {
	condL := e.freshLabel("forin.cond")
	bodyL := e.freshLabel("forin.body")
	incL  := e.freshLabel("forin.inc")
	endL  := e.freshLabel("forin.end")

	e.pushScope()
	defer e.popScope()
	e.breakStack = append(e.breakStack, endL)
	defer func() { e.breakStack = e.breakStack[:len(e.breakStack)-1] }()
	e.continueStack = append(e.continueStack, incL)
	defer func() { e.continueStack = e.continueStack[:len(e.continueStack)-1] }()
	defer e.pushPendingLabel(endL, incL)()

	// Resolve the object being iterated.
	objId, ok := s.Object.(*ast.Identifier)
	if !ok {
		return fmt.Errorf("%d:%d: for...in requires a named object variable", s.GetPos().Line, s.GetPos().Col)
	}
	sym, found := e.lookup(objId.Name)
	if !found || !sym.Ty.IsObject || len(sym.Ty.Fields) == 0 {
		return fmt.Errorf("%d:%d: '%s' is not an object with known fields", s.GetPos().Line, s.GetPos().Col, objId.Name)
	}

	// Build a compile-time string[] of field names and materialise it at runtime.
	keysVal, err := e.emitObjectFieldNames(sym.Ty.Fields, s.GetPos())
	if err != nil {
		return err
	}

	// Cache the {ptr, i64} aggregate fields into allocas so the loop can read them.
	dataPtrAlloca := e.freshReg()
	lenAlloca     := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dataPtrAlloca))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))

	dataExtract := e.freshReg()
	lenExtract  := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataExtract, keysVal.Ref))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenExtract, keysVal.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataExtract, dataPtrAlloca))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenExtract, lenAlloca))

	// Index counter.
	idxPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))

	// Loop variable (key: string/ptr).
	varPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", varPtr))
	e.define(s.VarName, Symbol{Ptr: varPtr, Ty: TypePtr})

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal  := e.freshReg()
	lenVal  := e.freshReg()
	condReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenVal, lenAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", condReg, idxVal, lenVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, bodyL, endL))

	e.emitLabel(bodyL)
	dataPtr := e.freshReg()
	idxVal2 := e.freshReg()
	gepReg  := e.freshReg()
	elemVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtr, dataPtrAlloca))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal2, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", gepReg, dataPtr, idxVal2))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", elemVal, gepReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", elemVal, varPtr))

	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxVal3 := e.freshReg()
	newIdx  := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal3, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newIdx, idxVal3))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newIdx, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

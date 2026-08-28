// emit_nodetest.go — the node:test runner surface (TDD-00140): test/it,
// describe/suite, module-level hooks, and the TestContext. Tests run at
// registration (run-at-registration model, see the TDD) with each body inside
// a setjmp exception frame, so assertion throws mark the test failed and
// execution continues. Bookkeeping in runtime_nodetest.go.
package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// emitNodeTestRunnerCall dispatches the runner members of the `test` module.
func (e *Emitter) emitNodeTestRunnerCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureNodeTestRuntime()
	switch property {
	case "test", "it":
		return e.emitNodeTest(args, pos, false)
	case "describe", "suite":
		return e.emitNodeDescribe(args, pos)
	case "before":
		// Run-at-registration makes before() equivalent to running it now.
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: before takes one function", pos.Line, pos.Col)
		}
		cb, err := e.resolveCallback(args[0])
		if err != nil {
			return Value{}, err
		}
		if _, err := e.emitCBCall(cb, nil); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	case "after":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: after takes one function", pos.Line, pos.Col)
		}
		cb, err := e.resolveCallbackWithHints(args[0], nil)
		if err != nil {
			return Value{}, err
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: after's hook must be an arrow/function-expression literal", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_ntest_list_push(ptr @__kml_ntest_afters, ptr @__kml_ntest_afters_n, ptr @__kml_ntest_afters_cap, ptr %s)", cb.hdrPtr))
		return Value{Ty: TypeVoid}, nil
	case "beforeEach", "afterEach":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: %s takes one function", pos.Line, pos.Col, property)
		}
		cb, err := e.resolveCallbackWithHints(args[0], nil)
		if err != nil {
			return Value{}, err
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: %s's hook must be an arrow/function-expression literal", pos.Line, pos.Col, property)
		}
		slot := "@__kml_ntest_beforeEach"
		if property == "afterEach" {
			slot = "@__kml_ntest_afterEach"
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cb.hdrPtr, slot))
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: the test module has no runner member '%s'", pos.Line, pos.Col, property)
}

// nodeTestNameValue resolves the test's display name: the (optional) name
// expression joined onto the compile-time describe-prefix stack.
func (e *Emitter) nodeTestNameValue(nameExpr ast.Expression) (Value, error) {
	var nameVal Value
	if nameExpr != nil {
		v, err := e.emitExpr(nameExpr)
		if err != nil {
			return Value{}, err
		}
		nameVal = e.coerce(v, TypePtr)
	} else {
		nameVal = Value{Ref: e.internString("anonymous"), Ty: TypePtr}
	}
	if len(e.nodeTestPrefix) > 0 {
		prefix := strings.Join(e.nodeTestPrefix, " > ") + " > "
		joined, err := e.emitStringConcat(Value{Ref: e.internString(prefix), Ty: TypePtr}, nameVal)
		if err != nil {
			return Value{}, err
		}
		nameVal = joined
	}
	return nameVal, nil
}

// emitNodeTest implements test(name?, opts?, fn) / it / t.test — running the
// body now inside an exception frame. forceSkip marks a test.skip(...) form.
func (e *Emitter) emitNodeTest(args []ast.Expression, pos ast.Pos, forceSkip bool) (Value, error) {
	e.ensureNodeTestRuntime()
	if len(args) < 1 || len(args) > 3 {
		return Value{}, fmt.Errorf("%d:%d: test takes (name?, options?, fn)", pos.Line, pos.Col)
	}
	fnExpr := args[len(args)-1]
	var nameExpr ast.Expression
	var optsLit *ast.ObjectLiteral
	for _, a := range args[:len(args)-1] {
		if ol, ok := a.(*ast.ObjectLiteral); ok {
			optsLit = ol
		} else {
			nameExpr = a
		}
	}
	nameVal, err := e.nodeTestNameValue(nameExpr)
	if err != nil {
		return Value{}, err
	}

	skip, todo := forceSkip, false
	if optsLit != nil {
		for _, prop := range optsLit.Properties {
			switch prop.Key {
			case "skip":
				skip = true
			case "todo":
				todo = true
			case "concurrency", "timeout":
				// accepted no-ops: tests run serially at registration
			default:
				return Value{}, fmt.Errorf("%d:%d: test options support { skip, todo } (got '%s')", pos.Line, pos.Col, prop.Key)
			}
		}
	}
	if skip || todo {
		t := "false"
		if todo {
			t = "true"
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_ntest_skipped(ptr %s, i1 %s)", nameVal.Ref, t))
		return Value{Ty: TypeVoid}, nil
	}

	// TestContext: {aftersRoot@0, n@8, cap@16, skipped i8@24, todo i8@25}.
	e.ensureCalloc()
	ctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 32)", ctx))

	contextTypeArrowParams(fnExpr, "__kml_test_ctx")
	cb, err := e.resolveCallbackWithHints(fnExpr, []Type{TestContextType()})
	if err != nil {
		return Value{}, err
	}
	if cb.kind != cbClosure {
		return Value{}, fmt.Errorf("%d:%d: a test body must be an arrow/function-expression literal", pos.Line, pos.Col)
	}

	e.emitInstr("call void @__kml_ntest_call_slot(ptr @__kml_ntest_beforeEach)")

	e.ensureExceptionHelpers()
	tryL := e.freshLabel("ntest.body")
	catchL := e.freshLabel("ntest.catch")
	okL := e.freshLabel("ntest.ok")
	afterL := e.freshLabel("ntest.after")
	jmpbuf := e.freshReg()
	sjRet := e.freshReg()
	threw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", jmpbuf))
	e.emitInstr(fmt.Sprintf("%s = call i32 @setjmp(ptr %s)", sjRet, jmpbuf))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", threw, sjRet))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", threw, catchL, tryL))

	runCtxAfters := func() {
		rootp := e.freshReg()
		np := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 0", rootp, ctx))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", np, ctx))
		e.emitInstr(fmt.Sprintf("call void @__kml_ntest_list_run(ptr %s, ptr %s)", rootp, np))
		e.emitInstr("call void @__kml_ntest_call_slot(ptr @__kml_ntest_afterEach)")
	}

	e.emitLabel(tryL)
	var callArgs []Value
	if len(cb.paramTypes()) == 1 {
		callArgs = []Value{{Ref: ctx, Ty: TestContextType()}}
	}
	if _, err := e.emitCBCall(cb, callArgs); err != nil {
		return Value{}, err
	}
	e.emitInstr("call void @__kml_pop_jmpbuf()")
	e.emitTerminator(fmt.Sprintf("br label %%%s", okL))

	e.emitLabel(okL)
	runCtxAfters()
	// t.skip()/t.todo() set flags: report skipped/todo instead of pass.
	skf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 24", skf, ctx))
	skb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", skb, skf))
	skc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", skc, skb))
	skL := e.freshLabel("ntest.skipped")
	passL := e.freshLabel("ntest.pass")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", skc, skL, passL))
	e.emitLabel(skL)
	tdf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 25", tdf, ctx))
	tdb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", tdb, tdf))
	tdc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", tdc, tdb))
	e.emitInstr(fmt.Sprintf("call void @__kml_ntest_skipped(ptr %s, i1 %s)", nameVal.Ref, tdc))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(passL)
	e.emitInstr(fmt.Sprintf("call void @__kml_ntest_pass(ptr %s)", nameVal.Ref))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	e.emitLabel(catchL)
	thrown := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_get_thrown()", thrown))
	msgIdx, msgTy, _ := errorObjType.FieldIndex("message")
	mg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", mg, errorObjType.StructIR(), thrown, msgIdx))
	msg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align %d", msg, mg, msgTy.Align()))
	runCtxAfters()
	e.emitInstr(fmt.Sprintf("call void @__kml_ntest_fail(ptr %s, ptr %s)", nameVal.Ref, msg))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	e.emitLabel(afterL)
	return Value{Ty: TypeVoid}, nil
}

// emitNodeDescribe implements describe/suite: the group body runs now, its
// name joining the compile-time prefix stack; a throw in the body itself
// counts as one failure under the group's name.
func (e *Emitter) emitNodeDescribe(args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureNodeTestRuntime()
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: describe takes (name?, fn)", pos.Line, pos.Col)
	}
	fnExpr := args[len(args)-1]
	prefix := "group"
	if len(args) == 2 {
		if sl, ok := args[0].(*ast.StringLiteral); ok {
			prefix = sl.Value
		}
	}
	groupName := prefix
	if len(e.nodeTestPrefix) > 0 {
		groupName = strings.Join(e.nodeTestPrefix, " > ") + " > " + prefix
	}

	// The body's test() calls emit while the closure body is being resolved,
	// so the prefix must be pushed BEFORE resolution, not around the call.
	e.nodeTestPrefix = append(e.nodeTestPrefix, prefix)
	cb, err := e.resolveCallbackWithHints(fnExpr, nil)
	e.nodeTestPrefix = e.nodeTestPrefix[:len(e.nodeTestPrefix)-1]
	if err != nil {
		return Value{}, err
	}
	if cb.kind != cbClosure {
		return Value{}, fmt.Errorf("%d:%d: a describe body must be an arrow/function-expression literal", pos.Line, pos.Col)
	}

	e.ensureExceptionHelpers()
	tryL := e.freshLabel("ndesc.body")
	catchL := e.freshLabel("ndesc.catch")
	afterL := e.freshLabel("ndesc.after")
	jmpbuf := e.freshReg()
	sjRet := e.freshReg()
	threw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", jmpbuf))
	e.emitInstr(fmt.Sprintf("%s = call i32 @setjmp(ptr %s)", sjRet, jmpbuf))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", threw, sjRet))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", threw, catchL, tryL))

	e.emitLabel(tryL)
	if _, cerr := e.emitCBCall(cb, nil); cerr != nil {
		return Value{}, cerr
	}
	e.emitInstr("call void @__kml_pop_jmpbuf()")
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	e.emitLabel(catchL)
	thrown := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_get_thrown()", thrown))
	msgIdx, msgTy, _ := errorObjType.FieldIndex("message")
	mg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", mg, errorObjType.StructIR(), thrown, msgIdx))
	msg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align %d", msg, mg, msgTy.Align()))
	e.emitInstr(fmt.Sprintf("call void @__kml_ntest_fail(ptr %s, ptr %s)", e.internString(groupName), msg))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))

	e.emitLabel(afterL)
	return Value{Ty: TypeVoid}, nil
}

// emitTestContextMethod dispatches t.test/t.after/t.skip/t.todo/t.diagnostic.
func (e *Emitter) emitTestContextMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "test", "it":
		// A subtest: same machinery; the parent context is not threaded (the
		// subtest gets its own).
		return e.emitNodeTest(args, pos, false)
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "after":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: t.after takes one function", pos.Line, pos.Col)
		}
		cb, err := e.resolveCallbackWithHints(args[0], nil)
		if err != nil {
			return Value{}, err
		}
		if cb.kind != cbClosure {
			return Value{}, fmt.Errorf("%d:%d: t.after's hook must be an arrow/function-expression literal", pos.Line, pos.Col)
		}
		rootp := e.freshReg()
		np := e.freshReg()
		capp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 0", rootp, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", np, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", capp, objVal.Ref))
		e.emitInstr(fmt.Sprintf("call void @__kml_ntest_list_push(ptr %s, ptr %s, ptr %s, ptr %s)", rootp, np, capp, cb.hdrPtr))
		return Value{Ty: TypeVoid}, nil
	case "skip", "todo":
		off := 24
		if method == "todo" {
			off = 25
		}
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", g, objVal.Ref, off))
		e.emitInstr(fmt.Sprintf("store i8 1, ptr %s, align 1", g))
		if off == 25 {
			// todo implies the skipped-reporting path too
			g2 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 24", g2, objVal.Ref))
			e.emitInstr(fmt.Sprintf("store i8 1, ptr %s, align 1", g2))
		}
		return Value{Ty: TypeVoid}, nil
	case "diagnostic":
		if len(args) == 1 {
			return e.emitConsolePrint([]ast.Expression{args[0]}, 2, "# ")
		}
		return Value{Ty: TypeVoid}, nil
	case "beforeEach", "afterEach", "before", "plan":
		// accepted no-ops in V1 (module-level hooks cover the corpus's use)
		if len(args) >= 1 {
			if _, err := e.emitExpr(args[0]); err != nil {
				return Value{}, err
			}
		}
		return Value{Ty: TypeVoid}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a TestContext supports t.test, t.after, t.skip, t.todo, t.diagnostic (got '%s')", pos.Line, pos.Col, method)
}

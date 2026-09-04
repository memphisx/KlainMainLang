// emit_stmts.go — statement emission: emitStmt, emitReturn, emitFor, emitWhile,
// emitIf, emitForOf, emitBreak, emitContinue, emitSwitch.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
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
	// finallyDepth is len(pendingFinallys) at this labeled loop's entry, so a
	// labeled `break`/`continue` runs the finallys nested since here (same role
	// as breakFinallyDepth for unlabeled targets).
	finallyDepth int
	// freeScope is len(e.scopes) at this labeled loop's entry — the TDD-00173
	// analogue of finallyDepth for compiler-inserted frees.
	freeScope int
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
	e.namedLabelStack = append(e.namedLabelStack, namedLabel{name: name, breakL: breakL, continueL: continueL, finallyDepth: len(e.pendingFinallys), freeScope: len(e.scopes)})
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
		if err := e.emitVarDecl(s); err != nil {
			return err
		}
		if s.Kind == "var" {
			e.promoteVarToFuncScope(s.Name)
		}
		return e.maybeRegisterAutoFree(s)
	case *ast.VarDeclarationList:
		for _, d := range s.Decls {
			if err := e.emitVarDecl(d); err != nil {
				return err
			}
			if d.Kind == "var" {
				e.promoteVarToFuncScope(d.Name)
			}
			if err := e.maybeRegisterAutoFree(d); err != nil {
				return err
			}
		}
		return nil
	case *ast.FunctionDeclaration:
		// TDD-00057: only a declaration pushNestedFuncScope already
		// pre-registered (directly in the current enclosing body) is
		// supported — anything reaching here without having been
		// pre-scanned is a deeper, unsupported nesting (e.g. inside a
		// further if/for/while/switch block), rejected cleanly rather than
		// silently mishandled.
		if len(e.nestedFuncScopes) > 0 {
			cur := e.nestedFuncScopes[len(e.nestedFuncScopes)-1]
			// TDD-00129 Stage 1: a nested declaration that closes over an
			// enclosing local/parameter was classified capturing at pre-scan and
			// left out of byDecl — emit it as a closure value bound to its name.
			if cur.capturing[s] {
				return e.emitCapturingNestedFunc(s)
			}
			if entry, ok := cur.byDecl[s]; ok {
				// A nested generator (TDD-00094) emits its fiber body here, where
				// the enclosing scope is fully populated — so its captures are
				// resolved now (not at pre-scan time) and recorded on the shared
				// GeneratorInfo the g() call site reads to build the instance's env.
				if entry.Gen != nil {
					caps := e.gatherGeneratorCaptures(s)
					for _, c := range caps {
						if c.Ty.IsArray {
							return fmt.Errorf("%d:%d: a nested generator capturing an array variable ('%s') is not yet supported", s.GetPos().Line, s.GetPos().Col, c.Name)
						}
					}
					entry.Gen.Captures = caps
					return e.emitGeneratorFunctionDecl(s, entry.Gen)
				}
				return e.emitFunctionDeclAs(s, entry.Mangled, entry.Sig)
			}
		}
		return fmt.Errorf("%d:%d: nested function declarations are only supported directly in an enclosing function's own body (not inside a further if/for/while/switch block)", s.GetPos().Line, s.GetPos().Col)
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
		// TDD-00152: a lexical block is its own scope for nested function
		// declarations — pre-scan this block's own statements so a `function`
		// declared inside an `if`/`for`/`while`/bare block (one or more blocks
		// deeper than the enclosing function body) is registered and callable
		// within the block, then popped at block exit so it doesn't leak out
		// (strict-mode block scoping). Reuses the exact per-body mechanism the
		// enclosing function/arrow already uses (capture classification
		// included); `nil` params because a block introduces none of its own.
		if err := e.pushNestedFuncScope(nil, s.Body); err != nil {
			e.popScope()
			return err
		}
		for _, child := range s.Body {
			if err := e.emitStmt(child); err != nil {
				return err
			}
			e.emitOwnedFreesAfter(child)
		}
		// TDD-00173: this block's own @free/auto obligations fall due at its
		// fall-through end (exit paths — return/break/continue — already
		// emitted theirs inline).
		e.emitFreesAtScopeExit(len(e.scopes))
		e.popNestedFuncScope()
		e.popScope()
		return nil
	case *ast.ExpressionStatement:
		_, err := e.emitExpr(s.Expr)
		return err
	case *ast.InterfaceDeclaration, *ast.TypeAliasDeclaration, *ast.EnumDeclaration:
		return nil // registered in pre-pass; no IR emitted here
	case *ast.ClassDeclaration:
		// The class body/vtable/static-init are emitted in their own earlier
		// pass; nothing is emitted here EXCEPT observe-only decorator
		// applications (TDD-00161), which must run at the class's *source
		// position* in top-level execution order — a decorator that mutates
		// module state declared before the class must see it initialized.
		return e.emitClassDecoratorApplications(s)
	case *ast.ThrowStatement:
		return e.emitThrow(s)
	case *ast.TryStatement:
		return e.emitTry(s)
	}
	return fmt.Errorf("unknown statement type %T", stmt)
}

func (e *Emitter) emitReturn(r *ast.ReturnStatement) error {
	// Generator functions (TDD-00061/ADR-00172): a `return` inside a
	// generator body never emits an ordinary `ret` at all — it suspends via
	// the same fiber-swap `yield` uses, just with done=true so the
	// generator's own fiber is never resumed again. Checked first, before
	// the async case below, since a generator function itself can't be
	// async in V1 (async generators are separately deferred — see
	// TDD-00061) and the two are mutually exclusive at emission time
	// either way.
	if e.currentGenerator != nil {
		return e.emitGeneratorReturn(r)
	}
	// Async functions: store result directly in the malloc'd promise slot, branch to async-ret.
	if e.isAsync {
		if r.Value != nil && e.currentPromiseTy.IR != "void" && e.currentPromiseTy.IR != "" {
			val, err := e.emitExpr(r.Value)
			if err != nil {
				return err
			}
			// Async-return flattening (ADR-00265): `return <promise>` from an async
			// fn adopts that promise's state — await it, settle with its value (or
			// re-throw its rejection into this fn's own settle). Only when the fn's
			// own value type isn't itself a promise (no double-unwrap).
			if val.Ty.IsPromise && val.Ty.PromiseTask && !e.currentPromiseTy.IsPromise {
				val, err = e.emitAwaitTaskPromise(val.Ref, e.currentPromiseTy)
				if err != nil {
					return err
				}
			}
			val = e.coerce(val, e.currentPromiseTy)
			align := e.currentPromiseTy.Align()
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d",
				StructFieldIR(e.currentPromiseTy), val.Ref, e.coroHdl, align))
		}
		// Run any enclosing `finally` blocks before settling — the return value
		// is already stored, so the finally's side effects (which may `await`)
		// run in order, matching JS (ADR-00612). Previously skipped, so a
		// `finally` around an async `return` never ran.
		if err := e.emitReturnCleanups(); err != nil {
			return err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", e.coroRetLabel))
		return nil
	}

	if r.Value == nil {
		if err := e.emitReturnCleanups(); err != nil {
			return err
		}
		switch {
		case e.currentRetType.IR == "void":
			e.emitTerminator("ret void")
		case isNullableScalar(e.currentRetType):
			// `return;` in a `T | null` function yields undefined -> absent.
			e.emitTerminator(fmt.Sprintf("ret %s zeroinitializer", nullableScalarStorageIR(e.currentRetType)))
		default:
			// zeroRef gives the type-correct zero (0.0 for a float `number`,
			// null for a ptr, false for i1) — a bare `ret double 0` is invalid IR.
			e.emitTerminator(fmt.Sprintf("ret %s %s", e.currentRetType.IR, zeroRef(e.currentRetType)))
		}
		return nil
	}

	// A nullable-scalar return value (TDD-00064 Stage 3) is boxed into its
	// { i1, T } aggregate so the caller can tell a real value from null.
	if isNullableScalar(e.currentRetType) {
		agg, err := e.emitNullableScalarBoxedValue(r.Value, e.currentRetType)
		if err != nil {
			return err
		}
		if err := e.emitReturnCleanups(); err != nil {
			return err
		}
		e.emitTerminator(fmt.Sprintf("ret %s %s", nullableScalarStorageIR(e.currentRetType), agg))
		return nil
	}

	if e.currentRetType.IsArray {
		// Return an array as the aggregate {ptr, i64}. A named variable
		// reuses its existing Ptr/LenPtr allocas; any other expression
		// (arr.slice(1), an object's array-typed field, another function's
		// result) already evaluates to the same {ptr, i64} aggregate shape
		// this function needs to return, so it's just returned directly —
		// same "named variable vs. arbitrary expression" split
		// resolveArrayForHOF/resolveMapOrSetForCall already use elsewhere.
		if id, ok := r.Value.(*ast.Identifier); ok {
			sym, ok := e.lookup(id.Name)
			if !ok {
				return fmt.Errorf("%d:%d: undefined variable '%s'", r.Value.GetPos().Line, r.Value.GetPos().Col, id.Name)
			}
			if !sym.Ty.IsArray {
				return fmt.Errorf("%d:%d: '%s' is not an array", r.Value.GetPos().Line, r.Value.GetPos().Col, id.Name)
			}
			dataSlot, lenSlot := e.arrayDataLenSlots(sym)
			ptrReg := e.freshReg()
			lenReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, dataSlot))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
			r0 := e.freshReg()
			r1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, ptrReg))
			e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
			if err := e.emitReturnCleanups(); err != nil {
				return err
			}
			e.emitTerminator(fmt.Sprintf("ret {ptr, i64} %s", r1))
			return nil
		}
		arrVal, err := e.emitExpr(r.Value)
		if err != nil {
			return err
		}
		if !arrVal.Ty.IsArray {
			return fmt.Errorf("%d:%d: expression is not an array", r.Value.GetPos().Line, r.Value.GetPos().Col)
		}
		if err := e.emitReturnCleanups(); err != nil {
			return err
		}
		e.emitTerminator(fmt.Sprintf("ret {ptr, i64} %s", arrVal.Ref))
		return nil
	}

	val, err := e.emitExprWithObjectHint(r.Value, e.currentRetType)
	if err != nil {
		return err
	}
	if e.currentRetType.IsDynamic {
		// A constrained union return type (TDD-00043; bare any/unknown is
		// still rejected as a return type entirely, before the body is ever
		// emitted — see emitFunctionDecl's guard). coerce has no notion of
		// boxing (it only converts between concrete scalar IR types), so a
		// dynamic-typed return needs the same explicit emitBoxValue call
		// emitVarDecl/emitAssign/call-argument-passing already use for it.
		if e.currentRetType.UnionMembers != nil && !unionAllowsAssignmentFrom(e.currentRetType, val.Ty) {
			return fmt.Errorf("%d:%d: return value's type is not a member of the declared union return type", r.Value.GetPos().Line, r.Value.GetPos().Col)
		}
		val, err = e.emitBoxValue(val)
		if err != nil {
			return err
		}
	} else if e.currentRetType.IR != "void" && e.currentRetType.IR != "" {
		val = e.coerce(val, e.currentRetType)
	}
	if err := e.emitReturnCleanups(); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("ret %s %s", val.Ty.IR, val.Ref))
	return nil
}

func (e *Emitter) emitFor(s *ast.ForStatement) error {
	condL := e.freshLabel("for.cond")
	bodyL := e.freshLabel("for.body")
	incL := e.freshLabel("for.inc")
	endL := e.freshLabel("for.end")

	e.pushScope()
	defer e.popScope()
	defer e.pushBreakTarget(endL)()
	defer e.pushContinueTarget(incL)()
	defer e.pushPendingLabel(endL, incL)()

	if s.Init != nil {
		if err := e.emitStmt(s.Init); err != nil {
			return err
		}
	}
	// TDD-00152: mark this loop's own variable name(s) as active for the
	// duration of the body, so a block-nested `function` capturing one is
	// rejected cleanly rather than closing over the shared counter cell.
	loopVars := map[string]bool{}
	if s.Init != nil {
		collectCapturableNames([]ast.Statement{s.Init}, loopVars)
	}
	e.activeForLoopVars = append(e.activeForLoopVars, loopVars)
	defer func() { e.activeForLoopVars = e.activeForLoopVars[:len(e.activeForLoopVars)-1] }()
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	e.emitSafepoint() // loop back-edge preempt check (TDD-00143 Stage 2)
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
	for _, upd := range s.Update {
		if _, err := e.emitExpr(upd); err != nil {
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
	endL := e.freshLabel("while.end")

	defer e.pushBreakTarget(endL)()
	defer e.pushContinueTarget(condL)()
	defer e.pushPendingLabel(endL, condL)()

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	e.emitSafepoint() // loop back-edge preempt check (TDD-00143 Stage 2)
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
	endL := e.freshLabel("if.end")
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
	// Flow narrowing (TDD-00064 Stage 2): a nullable-scalar local proven
	// present by the guard is treated as definitely-T inside the branch.
	e.applyBranchNarrowing(s.Test, true)
	e.applyUnionBranchNarrowing(s.Test, true)
	if err := e.emitStmt(s.Consequent); err != nil {
		return err
	}
	thenExits := e.blockDone
	e.popScope()
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))

	if s.Alternate != nil {
		e.emitLabel(elseL)
		e.pushScope()
		e.applyBranchNarrowing(s.Test, false)
		e.applyUnionBranchNarrowing(s.Test, false)
		if err := e.emitStmt(s.Alternate); err != nil {
			return err
		}
		e.popScope()
		e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
	}

	e.emitLabel(endL)
	// Early-exit narrowing: `if (x === null) return/throw/break/continue;` (no
	// else) leaves x proven present for the remainder of the enclosing scope.
	if thenExits && s.Alternate == nil {
		if name, nonNullWhenTrue, ok := e.narrowingFromCondition(s.Test); ok && !nonNullWhenTrue {
			e.narrowNonNullInCurrentScope(name)
		}
		// Union early-exit narrowing (TDD-00114): when the guard's true branch
		// always exits, the code below runs only in its false branch, so narrow
		// the union to that branch's type in the current (enclosing) scope.
		e.applyUnionBranchNarrowing(s.Test, false)
	}
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
	incL := e.freshLabel("forof.inc")
	endL := e.freshLabel("forof.end")

	e.pushScope()
	defer e.popScope()

	// Sync generator (ADR-00613/ADR-00614): register an iterator-close `finally`
	// *before* the break/continue targets, so a `return`/`throw`/outer
	// break-continue out of the loop body closes the generator (running its own
	// `finally`) — interleaved with any body/consumer `finally` on the shared
	// pendingFinallys stack. A plain this-loop `break`/normal completion still
	// closes it once via endL (ADR-00613): those unwind only to the depth
	// recorded *after* this push, so they don't double-run the close. The
	// generator instance is evaluated exactly once here and bound to a synthetic
	// name the synthesized `.return()` references; emitForOfGenerator reuses it.
	if !s.Await {
		if objTy := e.inferExprType(s.Iterable); objTy.IsGenerator && !objTy.GeneratorIsAsync {
			genVal, err := e.emitExpr(s.Iterable)
			if err != nil {
				return err
			}
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", genVal.Ref, slot))
			gname := fmt.Sprintf("__kml_forof_gen_%s", e.freshReg()[1:])
			e.define(gname, Symbol{Ptr: slot, Ty: objTy})
			closeStmt := ast.NewExpressionStatement(
				ast.NewCallExpression(
					ast.NewMemberExpression(ast.NewIdentifier(gname, s.GetPos()), "return", s.GetPos()),
					nil, s.GetPos()),
				s.GetPos())
			e.pendingFinallys = append(e.pendingFinallys, []ast.Statement{closeStmt})
			defer func() { e.pendingFinallys = e.pendingFinallys[:len(e.pendingFinallys)-1] }()
			defer e.pushBreakTarget(endL)()
			defer e.pushContinueTarget(incL)()
			defer e.pushPendingLabel(endL, incL)()
			return e.emitForOfGenerator(s, objTy, genVal, condL, bodyL, incL, endL)
		}
	}

	defer e.pushBreakTarget(endL)()
	defer e.pushContinueTarget(incL)()
	defer e.pushPendingLabel(endL, incL)()

	// `for await...of` (TDD-00085): consumes an async-generator instance,
	// awaiting each `.next()` promise. A user class implementing the
	// async-iteration protocol by hand — a `[Symbol.asyncIterator]()` method
	// returning an iterator with `async next(): Promise<{value,done}>` — is the
	// second accepted shape (TDD-00089). Any other iterable is a clean rejection.
	if s.Await {
		objTy := e.inferExprType(s.Iterable)
		if objTy.IsGenerator {
			genVal, err := e.emitExpr(s.Iterable)
			if err != nil {
				return err
			}
			if objTy.GeneratorIsAsync {
				return e.emitForAwaitOfGenerator(s, objTy, genVal, condL, bodyL, incL, endL)
			}
			// A sync generator in `for await` (CreateAsyncFromSyncIterator):
			// drive it synchronously, awaiting each yielded value.
			return e.emitForAwaitOfSyncGenerator(s, objTy, genVal, condL, bodyL, incL, endL)
		}
		// A ReadableStream (or an already-obtained reader) in `for await`
		// (TDD-00097 Stage 1): read chunk-at-a-time, awaiting each read().
		if objTy.IsReadableStream || objTy.IsStreamReader {
			streamVal, err := e.emitExpr(s.Iterable)
			if err != nil {
				return err
			}
			return e.emitForAwaitOfStream(s, objTy, streamVal, condL, bodyL, incL, endL)
		}
		// A Node Readable (fs.createReadStream, Readable.from, …) — unwrap to its
		// inner WHATWG rstream (field 0) and reuse the stream for-await path
		// (TDD-00108). Its chunk type is StreamOut.
		if objTy.IsNodeReadable {
			nsVal, err := e.emitExpr(s.Iterable)
			if err != nil {
				return err
			}
			chunkTy := TypeI64
			if objTy.StreamOut != nil {
				chunkTy = *objTy.StreamOut
			}
			rsTy := ReadableStreamType(chunkTy)
			rsPtr := e.nodeStreamSide(nsVal.Ref, 0)
			return e.emitForAwaitOfStream(s, rsTy, Value{Ref: rsPtr, Ty: rsTy}, condL, bodyL, incL, endL)
		}
		if objTy.IsClass {
			if info, ok := e.classes[objTy.ClassName]; ok {
				if _, ok := info.MethodSigs[asyncIteratorMethodName]; ok {
					iterableVal, err := e.emitExpr(s.Iterable)
					if err != nil {
						return err
					}
					return e.emitForAwaitOfAsyncIterable(s, objTy, iterableVal, condL, bodyL, incL, endL)
				}
				// A sync [Symbol.iterator]() iterable in `for await`: drive it
				// synchronously, awaiting each value before binding.
				if _, ok := info.MethodSigs[syncIteratorMethodName]; ok {
					iterableVal, err := e.emitExpr(s.Iterable)
					if err != nil {
						return err
					}
					return e.emitForOfSymbolIterator(s, objTy, iterableVal, true, condL, bodyL, incL, endL)
				}
			}
		}
		// An object literal (a static struct) with a `[Symbol.asyncIterator]`
		// (or, failing that, `[Symbol.iterator]`) member — desugared by the
		// parser to a closure-typed `@@asyncIterator`/`@@iterator` field: call
		// the closure and iterate whatever it returns.
		if objTy.IsObject {
			if _, _, ok := objTy.FieldIndex(asyncIteratorMethodName); ok {
				return e.emitForOfObjectSymbolIterable(s, objTy, asyncIteratorMethodName, true, condL, bodyL, incL, endL)
			}
			if _, _, ok := objTy.FieldIndex(syncIteratorMethodName); ok {
				return e.emitForOfObjectSymbolIterable(s, objTy, syncIteratorMethodName, true, condL, bodyL, incL, endL)
			}
		}
		// `for await` over a sync array (TDD-00092): JS awaits each element, so an
		// array of promises is consumed sequentially and an array of plain values
		// awaits each as a harmless identity. Reuses the array for-of loop shape,
		// awaiting each element before binding.
		if objTy.IsArray {
			return e.emitForAwaitOfArray(s, objTy, condL, bodyL, incL, endL)
		}
		// A Map/Set in `for await`: materialize the values into an array (a Set
		// iterates its elements, a Map its values — same shape as the sync
		// for-of below) and reuse the array loop's per-element await.
		if objTy.IsMap || objTy.IsSet {
			var mapPtr string
			if id, ok := s.Iterable.(*ast.Identifier); ok {
				iterSym, found := e.lookup(id.Name)
				if !found {
					return fmt.Errorf("%d:%d: undefined variable '%s'", s.GetPos().Line, s.GetPos().Col, id.Name)
				}
				loaded := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", loaded, iterSym.Ptr))
				mapPtr = loaded
			} else {
				iterableVal, err := e.emitExpr(s.Iterable)
				if err != nil {
					return err
				}
				mapPtr = iterableVal.Ref
			}
			valsVal, err := e.mapOrSetValuesArray(objTy, mapPtr)
			if err != nil {
				return err
			}
			ptrR := e.freshReg()
			lenR := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrR, valsVal.Ref))
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenR, valsVal.Ref))
			return e.emitForAwaitOfArrayCore(s, *valsVal.Ty.ElemType, ptrR, lenR, condL, bodyL, incL, endL)
		}
		return fmt.Errorf("%d:%d: 'for await...of' requires an async generator, a sync generator, a class with a [Symbol.asyncIterator]() method, an array, a Map, or a Set (TDD-00089/TDD-00092)", s.GetPos().Line, s.GetPos().Col)
	}

	// A klain:sync Channel ranges until close (TDD-00143 Stage 3): each
	// iteration receives one value; the loop ends when the channel is closed
	// and drained — the `for (const v of ch) {}` equivalent of Go's channel
	// range. The channel is evaluated once.
	if objTy := e.inferExprType(s.Iterable); objTy.IsChannel {
		return e.emitForOfChannel(s, objTy, condL, bodyL, incL, endL)
	}

	// Stage 1a (TDD-00009): a class instance whose class declares a zero-arg
	// `next(): T | null` method iterates via repeated next() calls rather
	// than a pre-materialized array — a genuinely different loop shape
	// (unknown length, one call per iteration) from the array/Map/Set path
	// below, so it's dispatched first and returns early via its own,
	// independent loop-emission code.
	// An object literal (a static struct) with a `[Symbol.iterator]` member —
	// same desugar and dispatch as the for-await object case, sync flavor.
	if objTy := e.inferExprType(s.Iterable); objTy.IsObject {
		if _, _, ok := objTy.FieldIndex(syncIteratorMethodName); ok {
			return e.emitForOfObjectSymbolIterable(s, objTy, syncIteratorMethodName, false, condL, bodyL, incL, endL)
		}
	}

	if objTy := e.inferExprType(s.Iterable); objTy.IsClass {
		if info, ok := e.classes[objTy.ClassName]; ok {
			// A `[Symbol.iterator]()` method (desugared to `@@iterator`) wins
			// over the structural `next(): T | null` shape — the spec's real
			// `{value, done}` protocol, dispatched like `[Symbol.asyncIterator]`
			// is in `for await`.
			if _, ok := info.MethodSigs[syncIteratorMethodName]; ok {
				iterableVal, err := e.emitExpr(s.Iterable)
				if err != nil {
					return err
				}
				return e.emitForOfSymbolIterator(s, objTy, iterableVal, false, condL, bodyL, incL, endL)
			}
			if sig, ok := info.MethodSigs["next"]; ok &&
				len(sig.ParamTypes) == 0 && sig.RetType.Nullable &&
				!sig.RetType.IsArray && !sig.RetType.IsMap && !sig.RetType.IsSet {
				recvVal, err := e.emitExpr(s.Iterable)
				if err != nil {
					return err
				}
				return e.emitForOfClassIterator(s, objTy, sig, recvVal, condL, bodyL, incL, endL)
			}
		}
	}

	// TDD-00061/ADR-00172: a generator instance (`for (const x of gen())
	// {...}`) — a third genuinely different loop shape, its own {value,
	// done} result rather than either a pre-materialized array or Stage
	// 1a's T | null sentinel (a legitimately falsy/zero yielded value must
	// not be misread as "done", the reason this needed its own case rather
	// than reusing the sentinel path above). The iterable is evaluated
	// once here (constructing exactly one generator instance — s.Iterable
	// may itself be a `gen(...)` call, which must not run again per
	// iteration) and handed to emitForOfGenerator, which drives it via
	// repeated .next() calls against that same instance.
	if objTy := e.inferExprType(s.Iterable); objTy.IsGenerator {
		genVal, err := e.emitExpr(s.Iterable)
		if err != nil {
			return err
		}
		return e.emitForOfGenerator(s, objTy, genVal, condL, bodyL, incL, endL)
	}

	// `for (const [k, v] of map)` decomposes ENTRIES (ADR-00481, clearing
	// the ADR-00011 caveat): a two-plain-name array pattern over a Map
	// iterates the keys and values arrays in parallel (the runtime emits
	// both in one insertion order). Anything else keeps the values-only
	// iteration below.
	if len(s.ArrayPattern) == 2 && s.ObjectPattern == nil {
		plain := s.ArrayPattern[0].Name != "" && s.ArrayPattern[1].Name != "" &&
			s.ArrayPattern[0].SubArray == nil && s.ArrayPattern[0].SubObject == nil &&
			s.ArrayPattern[1].SubArray == nil && s.ArrayPattern[1].SubObject == nil &&
			!s.ArrayPattern[0].Rest && !s.ArrayPattern[1].Rest
		if iterTy := e.inferExprType(s.Iterable); plain && iterTy.IsMap && !iterTy.IsSet && !iterTy.IsDynamicObject {
			mapVal, err := e.emitExpr(s.Iterable)
			if err != nil {
				return err
			}
			keysVal, err := e.emitMapCall(iterTy, mapVal.Ref, "keys", nil, s.GetPos())
			if err != nil {
				return err
			}
			valsVal, err := e.mapOrSetValuesArray(iterTy, mapVal.Ref)
			if err != nil {
				return err
			}
			keyTy, valTy := *keysVal.Ty.ElemType, *valsVal.Ty.ElemType
			kData, kLen := e.splitArrayAggregate(keysVal)
			vData, _ := e.splitArrayAggregate(valsVal)

			idxPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
			e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))
			kPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", kPtr, keyTy.IR, keyTy.Align()))
			vPtr := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", vPtr, valTy.IR, valTy.Align()))
			e.define(s.ArrayPattern[0].Name, Symbol{Ptr: kPtr, Ty: keyTy})
			e.define(s.ArrayPattern[1].Name, Symbol{Ptr: vPtr, Ty: valTy})

			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(condL)
			e.emitSafepoint() // loop back-edge preempt check (TDD-00143 Stage 2)
			idxV, lenV, cond := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxV, idxPtr))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenV, kLen))
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", cond, idxV, lenV))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond, bodyL, endL))

			e.emitLabel(bodyL)
			idx2 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx2, idxPtr))
			kd, kg := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", kd, kData))
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", kg, keyTy.IR, kd, idx2))
			kv := e.loadArrayElem(kg, keyTy)
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", keyTy.IR, kv.Ref, kPtr, keyTy.Align()))
			vd, vg := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", vd, vData))
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", vg, valTy.IR, vd, idx2))
			vv := e.loadArrayElem(vg, valTy)
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", valTy.IR, vv.Ref, vPtr, valTy.Align()))
			for _, st := range s.Body.Body {
				if err := e.emitStmt(st); err != nil {
					return err
				}
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", incL))
			e.emitLabel(incL)
			idx3, idx4 := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idx3, idxPtr))
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idx4, idx3))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idx4, idxPtr))
			e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
			e.emitLabel(endL)
			return nil
		}
	}

	// Resolve the iterable to a data-ptr alloca and a len alloca.
	// For named variables we reuse their existing allocas (no copy).
	// For any other expression we evaluate it, extract the aggregate fields,
	// and store them into fresh allocas so the loop body can keep reloading.
	var dataPtrAlloca, lenAlloca string
	var elemTy Type
	// TDD-00101: iterating a BigInt64Array/BigUint64Array loads the raw i64
	// but binds the loop variable as a bigint handle (wrapped per element).
	var iterTaTy Type

	if strTy := e.inferExprType(s.Iterable); isForOfStringTy(strTy) {
		// A string iterates its characters — one 1-byte character string per
		// element, this compiler's byte-string model (ADR-00535). Materialize a
		// char array and drive the shared array-iteration loop below. Faithful
		// for ASCII/Latin-1; real JS iterates by code point, the same documented
		// Unicode narrowing the rest of the string layer carries.
		sv, err := e.emitExpr(s.Iterable)
		if err != nil {
			return err
		}
		charArr := e.emitStringToCharArray(sv)
		elemTy = TypePtr
		dataPtrAlloca, lenAlloca = e.splitArrayAggregate(charArr)
	} else if id, ok := s.Iterable.(*ast.Identifier); ok {
		iterSym, found := e.lookup(id.Name)
		switch {
		case found && iterSym.Ty.IsArray:
			iterTaTy = iterSym.Ty
			// Object-reference model (TDD-00127): the data/len field addresses of
			// the array's current header. Caching them here (rather than the
			// header pointer) is deliberate — an in-place mutation mid-loop
			// (push growing/moving the buffer) writes the new data ptr/len back
			// into these same header fields, so each iteration's reload observes
			// it, exactly as the old two-alloca form did.
			dataPtrAlloca, lenAlloca = e.arrayDataLenSlots(iterSym)
			elemTy = *iterSym.Ty.ElemType
		case found && (iterSym.Ty.IsMap || iterSym.Ty.IsSet):
			// A Set iterates its elements; a Map iterates its values (not
			// [key,value] entries — see mapOrSetValuesArray).
			mapPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, iterSym.Ptr))
			valsVal, err := e.mapOrSetValuesArray(iterSym.Ty, mapPtr)
			if err != nil {
				return err
			}
			elemTy = *valsVal.Ty.ElemType
			dataPtrAlloca, lenAlloca = e.splitArrayAggregate(valsVal)
		default:
			return fmt.Errorf("%d:%d: '%s' is not an array, Map, Set, generator, or a class with a next(): T | null method", s.GetPos().Line, s.GetPos().Col, id.Name)
		}
	} else {
		arrVal, err := e.emitExpr(s.Iterable)
		if err != nil {
			return err
		}
		switch {
		case arrVal.Ty.IsArray && arrVal.Ty.ElemType != nil:
			iterTaTy = arrVal.Ty
			elemTy = *arrVal.Ty.ElemType
			dataPtrAlloca, lenAlloca = e.splitArrayAggregate(arrVal)
		case arrVal.Ty.IsMap || arrVal.Ty.IsSet:
			// Same non-named-variable case emitMapCall/emitSetCall/.size
			// already handle — a Map/Set-typed field access, array index,
			// or call result (e.g. `for (const t of c.tags)`).
			valsVal, err := e.mapOrSetValuesArray(arrVal.Ty, arrVal.Ref)
			if err != nil {
				return err
			}
			elemTy = *valsVal.Ty.ElemType
			dataPtrAlloca, lenAlloca = e.splitArrayAggregate(valsVal)
		default:
			return fmt.Errorf("%d:%d: for...of requires an array, Map, Set, generator, or class-with-next() value", s.GetPos().Line, s.GetPos().Col)
		}
	}

	// Internal index counter (not user-visible).
	idxPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))

	// Loop variable. An array-typed element (nested array, TDD-00029) needs
	// the two-alloca "Named Symbol" representation (Ptr+LenPtr) instead of
	// the single scalar alloca every other element type uses, so .length/
	// indexing/etc. work on it inside the loop body.
	//
	// A destructuring loop variable (`for (const [a, b] of …)` /
	// `for (const { x, y } of …)`, TDD-00065 Stage 1) defines no single
	// binding here at all — the per-iteration element is unpacked in the
	// body below, through the same unpack*PatternInto core every other
	// destructuring position shares.
	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	bindTy := elemTy
	if iterTaTy.BigIntElem {
		bindTy = BigIntType()
	}
	varPtr := e.freshReg()
	if isPattern {
		// no pre-loop binding
	} else if elemTy.IsArray {
		// Object-reference model (TDD-00127): a stable slot holding a pointer to
		// the current element's {data, len} header, rebuilt each iteration.
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", varPtr))
		e.define(s.VarName, Symbol{Ptr: varPtr, Ty: elemTy})
	} else {
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, bindTy.IR, bindTy.Align()))
		e.define(s.VarName, Symbol{Ptr: varPtr, Ty: bindTy})
	}

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	e.emitSafepoint() // loop back-edge preempt check (TDD-00143 Stage 2)
	idxVal := e.freshReg()
	lenVal := e.freshReg()
	condReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenVal, lenAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", condReg, idxVal, lenVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, bodyL, endL))

	e.emitLabel(bodyL)
	dataPtr := e.freshReg()
	idxVal2 := e.freshReg()
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtr, dataPtrAlloca))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal2, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, elemTy.IR, dataPtr, idxVal2))
	elemVal := e.loadArrayElem(gepReg, elemTy)
	switch {
	case s.ObjectPattern != nil:
		// The element is unpacked as an object — its fields become the
		// loop-body bindings. Requires an object/class element type; a
		// non-object element type is a clean rejection inside
		// unpackObjectPatternInto's own FieldIndex lookup.
		if err := e.unpackObjectPatternInto(elemVal.Ref, elemTy, s.ObjectPattern, s.GetPos()); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		// A tuple element (`for (const [k, v] of pairs)` over `[K, V][]`,
		// TDD-00066) binds positionally to the tuple's fields.
		if elemTy.IsTuple {
			if err := e.unpackTuplePatternInto(elemVal.Ref, elemTy, s.ArrayPattern, s.GetPos()); err != nil {
				return err
			}
			break
		}
		// Otherwise the element must itself be an array to array-destructure it.
		if !elemTy.IsArray || elemTy.ElemType == nil {
			return fmt.Errorf("%d:%d: cannot array-destructure a for-of element of non-array type", s.GetPos().Line, s.GetPos().Col)
		}
		innerPtr := e.freshReg()
		innerLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", innerPtr, elemVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", innerLen, elemVal.Ref))
		if err := e.unpackArrayPatternInto(innerPtr, innerLen, *elemTy.ElemType, s.ArrayPattern); err != nil {
			return err
		}
	case elemTy.IsArray:
		// Point the loop variable's slot at a fresh header wrapping this
		// element's aggregate (object-reference model, TDD-00127).
		elemHeader := e.boxArrayValue(elemVal)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", elemHeader, varPtr))
	default:
		if iterTaTy.BigIntElem {
			elemVal = e.wrapTypedArrayLoad(elemVal, iterTaTy)
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", bindTy.IR, elemVal.Ref, varPtr, bindTy.Align()))
	}

	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxVal3 := e.freshReg()
	newIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal3, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newIdx, idxVal3))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newIdx, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// emitBreak jumps to the nearest enclosing loop/switch end label, or — when
// labeled — to the named loop's end label, however many levels out that is.
// pushBreakTarget registers a break label together with the finally depth at
// this loop/switch entry, so a `break` to it runs exactly the finallys nested
// since here. The returned func pops both stacks (use with `defer ...()`).
func (e *Emitter) pushBreakTarget(label string) func() {
	e.breakStack = append(e.breakStack, label)
	e.breakFinallyDepth = append(e.breakFinallyDepth, len(e.pendingFinallys))
	e.breakFreeScope = append(e.breakFreeScope, len(e.scopes))
	return func() {
		e.breakStack = e.breakStack[:len(e.breakStack)-1]
		e.breakFinallyDepth = e.breakFinallyDepth[:len(e.breakFinallyDepth)-1]
		e.breakFreeScope = e.breakFreeScope[:len(e.breakFreeScope)-1]
	}
}

// pushContinueTarget is pushBreakTarget's counterpart for the continue label.
func (e *Emitter) pushContinueTarget(label string) func() {
	e.continueStack = append(e.continueStack, label)
	e.continueFinallyDepth = append(e.continueFinallyDepth, len(e.pendingFinallys))
	e.continueFreeScope = append(e.continueFreeScope, len(e.scopes))
	return func() {
		e.continueStack = e.continueStack[:len(e.continueStack)-1]
		e.continueFinallyDepth = e.continueFinallyDepth[:len(e.continueFinallyDepth)-1]
		e.continueFreeScope = e.continueFreeScope[:len(e.continueFreeScope)-1]
	}
}

// emitFinallysToDepth runs the pending finallys nested since a target depth —
// used by break/continue, which unwind only to their loop, unlike return's
// emitPendingFinallys (which runs all the way to the function boundary).
func (e *Emitter) emitFinallysToDepth(depth int) error {
	saved := e.pendingFinallys
	defer func() { e.pendingFinallys = saved }()
	for i := len(saved) - 1; i >= depth; i-- {
		if e.blockDone {
			break
		}
		e.pendingFinallys = saved[:i]
		e.pushScope()
		for _, stmt := range saved[i] {
			if err := e.emitStmt(stmt); err != nil {
				e.popScope()
				return err
			}
		}
		e.popScope()
	}
	return nil
}

func (e *Emitter) emitBreak(s *ast.BreakStatement) error {
	if s.Label != "" {
		lbl, ok := e.lookupNamedLabel(s.Label)
		if !ok {
			return fmt.Errorf("%d:%d: undefined label '%s'", s.GetPos().Line, s.GetPos().Col, s.Label)
		}
		if err := e.emitFinallysToDepth(lbl.finallyDepth); err != nil {
			return err
		}
		e.emitFreesAbove(lbl.freeScope)
		e.emitTerminator(fmt.Sprintf("br label %%%s", lbl.breakL))
		return nil
	}
	if len(e.breakStack) == 0 {
		return fmt.Errorf("break statement outside of loop or switch")
	}
	if err := e.emitFinallysToDepth(e.breakFinallyDepth[len(e.breakFinallyDepth)-1]); err != nil {
		return err
	}
	e.emitFreesAbove(e.breakFreeScope[len(e.breakFreeScope)-1])
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
		if err := e.emitFinallysToDepth(lbl.finallyDepth); err != nil {
			return err
		}
		e.emitFreesAbove(lbl.freeScope)
		e.emitTerminator(fmt.Sprintf("br label %%%s", lbl.continueL))
		return nil
	}
	if len(e.continueStack) == 0 {
		return fmt.Errorf("continue statement outside of a loop")
	}
	if err := e.emitFinallysToDepth(e.continueFinallyDepth[len(e.continueFinallyDepth)-1]); err != nil {
		return err
	}
	e.emitFreesAbove(e.continueFreeScope[len(e.continueFreeScope)-1])
	e.emitTerminator(fmt.Sprintf("br label %%%s", e.continueStack[len(e.continueStack)-1]))
	return nil
}

// emitSwitch emits a switch statement using a chain of comparison blocks
// followed by case body blocks in source order (enabling fallthrough).
func (e *Emitter) emitSwitch(s *ast.SwitchStatement) error {
	endL := e.freshLabel("switch.end")

	defer e.pushBreakTarget(endL)()

	disc, err := e.emitExpr(s.Discriminant)
	if err != nil {
		return err
	}
	discIsStr := isStringTy(disc.Ty)

	// TDD-00152: a `switch` body is a single lexical block in JS — a function
	// declared in any case is hoisted to the switch block and visible across
	// cases (fallthrough). Pre-scan every case's statements as one block so
	// block-nested declarations are registered, popped once the switch ends.
	var switchStmts []ast.Statement
	for _, c := range s.Cases {
		switchStmts = append(switchStmts, c.Body...)
	}
	if err := e.pushNestedFuncScope(nil, switchStmts); err != nil {
		return err
	}
	defer e.popNestedFuncScope()

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
			// Binary-safe switch: __kml_str_cmp compares over header lengths, so a
			// discriminant or case label with an embedded NUL matches correctly.
			e.ensureStrHeaderRuntime()
			cmpRes := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_str_cmp(ptr %s, ptr %s)", cmpRes, disc.Ref, caseVal.Ref))
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
	endL := e.freshLabel("dowhile.end")

	defer e.pushBreakTarget(endL)()
	defer e.pushContinueTarget(condL)()
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
	e.emitSafepoint() // loop back-edge preempt check (TDD-00143 Stage 2)
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
	incL := e.freshLabel("forin.inc")
	endL := e.freshLabel("forin.end")

	e.pushScope()
	defer e.popScope()
	defer e.pushBreakTarget(endL)()
	defer e.pushContinueTarget(incL)()
	defer e.pushPendingLabel(endL, incL)()

	// Resolve the object being iterated. for...in only ever needs the
	// object's static field-name list (a compile-time constant), never its
	// runtime value/pointer, so this works for any object-typed expression,
	// not just a named variable — e.g. `for (const k in c.point)`.
	objTy := e.inferExprType(s.Object)
	var keysVal Value
	if isUnconstrainedDynamic(objTy) {
		// A bare any/unknown object iterates its D1 dynamic-object keys at
		// runtime (TDD-00155 Stage 1), insertion order.
		objVal, err := e.emitExpr(s.Object)
		if err != nil {
			return err
		}
		keysVal, err = e.emitDynAnyKeys(objVal, s.GetPos())
		if err != nil {
			return err
		}
	} else {
		fields := objTy.VisibleFields()
		if !objTy.IsObject || (!objTy.IsClass && len(fields) == 0) {
			return fmt.Errorf("%d:%d: for...in requires an object with known fields", s.GetPos().Line, s.GetPos().Col)
		}
		// Build a compile-time string[] of field names and materialise it at runtime.
		var err error
		keysVal, err = e.emitObjectFieldNames(fields, s.GetPos())
		if err != nil {
			return err
		}
	}

	// Cache the {ptr, i64} aggregate fields into allocas so the loop can read them.
	dataPtrAlloca := e.freshReg()
	lenAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dataPtrAlloca))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))

	dataExtract := e.freshReg()
	lenExtract := e.freshReg()
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
	e.emitSafepoint() // loop back-edge preempt check (TDD-00143 Stage 2)
	idxVal := e.freshReg()
	lenVal := e.freshReg()
	condReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenVal, lenAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", condReg, idxVal, lenVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, bodyL, endL))

	e.emitLabel(bodyL)
	dataPtr := e.freshReg()
	idxVal2 := e.freshReg()
	gepReg := e.freshReg()
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
	newIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal3, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newIdx, idxVal3))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newIdx, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

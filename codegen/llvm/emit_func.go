// emit_func.go — function and closure emission: declarations, free-variable
// scanning, closure construction, and closure call paths.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emitFunctionDecl emits one user-defined, non-generic function into
// e.functions under its own source name, using its own registered signature.
func (e *Emitter) emitFunctionDecl(decl *ast.FunctionDeclaration) error {
	return e.emitFunctionDeclAs(decl, decl.Name, e.funcs[decl.Name])
}

// emitFunctionDeclAs emits decl's body into e.functions under llvmName,
// using sig for parameter/return types instead of decl's own (possibly
// generic, T-named) annotations — the shared core both emitFunctionDecl
// (llvmName == decl.Name, sig == e.funcs[decl.Name]) and TDD-00010 V1's
// on-demand generic-function instantiation (a mangled llvmName, a
// per-instantiation substituted sig — see emit_generics.go) go through.
func (e *Emitter) emitFunctionDeclAs(decl *ast.FunctionDeclaration, llvmName string, sig FuncSig) error {
	// TDD-00061/ADR-00172: a top-level generator function is diverted to
	// emitGeneratorFunctionDecl straight from EmitProgram's own Pass 2 loop
	// and never reaches this function at all — so IsGenerator reaching here
	// only ever means a *nested* generator declaration (routed through
	// here via emit_stmts.go's ordinary nested-function-declaration
	// dispatch) or an on-demand generic instantiation (emit_generics.go) —
	// neither supported yet (V1 scope: top-level declarations only).
	// Without this check, a generator with no `yield` in its body (legal,
	// if unusual, real JS) would silently compile and run as an ordinary
	// function instead of being rejected — exactly the "parses now,
	// silently mishandled" failure mode this project's own conventions
	// treat as worse than a clean rejection.
	if decl.IsGenerator {
		return fmt.Errorf("%d:%d: a nested generator function declaration is not yet supported", decl.GetPos().Line, decl.GetPos().Col)
	}
	// Save current function context.
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedIsAsync := e.isAsync
	savedCoroHdl := e.coroHdl
	savedAsyncPromiseReg := e.asyncPromiseReg
	savedAsyncCatchLabel := e.asyncCatchLabel
	savedPromiseTy := e.currentPromiseTy
	savedCoroRetLabel := e.coroRetLabel

	// Reset for this function. break/continue/named-label stacks reset too
	// (not just scopes) — a bare `break`/`continue` or a `break LABEL`/
	// `continue LABEL` targeting an enclosing loop is only ever valid
	// within that loop's own function; without this reset, a function
	// declaration nested inside a live loop body would silently inherit
	// the outer loop's break/continue targets and emit a `br label` into a
	// different LLVM function's own label space — invalid IR clang itself
	// would reject, not a clean compile-time error from this compiler.
	// Restored via defer (not a plain assignment at the bottom of this
	// function) because emitDoWhile/emitFor/etc. manage these same three
	// fields with their own push-then-defer-pop pattern — if this
	// function's body emission returns an error partway through (e.g.
	// exactly the "undefined label" error this reset is meant to produce),
	// a plain end-of-function restore is skipped, leaving the *enclosing*
	// loop's own deferred pop to run against a stack it no longer
	// recognizes (observed directly: a slice-bounds panic in emitDoWhile's
	// own deferred cleanup, from restoring this via plain assignment
	// instead of defer on a first attempt at this same fix).
	savedBreakStack := e.breakStack
	savedContinueStack := e.continueStack
	savedNamedLabelStack := e.namedLabelStack
	e.breakStack = nil
	e.continueStack = nil
	e.namedLabelStack = nil
	// currentGenerator resets the same way, same reasoning, same
	// defer-not-plain-assignment fix: a `return`/`yield` inside THIS
	// function's own body must never be misrouted into an *enclosing*
	// generator's own suspend-and-swap machinery — confirmed directly as a
	// real bug, not a theoretical one: an arrow function's own plain
	// `return n;`, declared inside a generator's body, silently emitted a
	// GEP into the generator's own instance struct *inside the arrow
	// function's own separately-compiled LLVM function*, corrupting
	// unrelated memory (a different function's `%env` parameter
	// reinterpreted as if it were a generator instance pointer) instead of
	// a normal `ret`.
	savedCurrentGenerator := e.currentGenerator
	e.currentGenerator = nil
	defer func() {
		e.breakStack = savedBreakStack
		e.continueStack = savedContinueStack
		e.namedLabelStack = savedNamedLabelStack
		e.currentGenerator = savedCurrentGenerator
	}()
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.isAsync = decl.IsAsync
	e.coroHdl = ""
	e.asyncPromiseReg = ""
	e.asyncCatchLabel = ""
	e.currentPromiseTy = TypeVoid
	e.coroRetLabel = ""
	e.pushScope()
	if decl.Body != nil {
		if err := e.pushNestedFuncScope(decl.Body.Body); err != nil {
			return err
		}
		defer e.popNestedFuncScope()
	}

	// registerFunctions already resolved the signature (explicit annotations,
	// or best-effort inference for an unannotated return type — see
	// inferUnannotatedReturnType) before any function body was emitted (or,
	// for a generic instantiation, the caller built and registered one
	// on demand — see emit_generics.go); reuse it rather than recomputing
	// param/return types independently here, so this function's own emitted
	// signature always matches what every caller already expects it to be.
	retType := sig.RetType
	// TDD-00062 (Staged V2): a bare `any`/`unknown` return type is now
	// allowed — the { i8, i64 } box round-trips through a return position
	// exactly as TDD-00010 V2's `@erased` bare-T return already did. Only a
	// *nested* dynamic shape (T[], an object field typed T, etc.) is still
	// rejected: containsDynamicElement deliberately skips the top level, so
	// this catches those without catching bare any/unknown. (Rejecting
	// nested dynamics also subsumes the old erased-only carve-out — an
	// erased T[] is IsArray, not top-level IsDynamic, so it still fails.)
	if containsDynamicElement(retType) {
		return fmt.Errorf("%d:%d: any/unknown is not yet supported nested inside an array or object return type", decl.GetPos().Line, decl.GetPos().Col)
	}
	if err := validateCompositeType(retType, decl.GetPos().Line, decl.GetPos().Col); err != nil {
		return err
	}

	// A may-suspend async function (TDD-00083 Stage 2) is compiled as a coroutine
	// task: signature `void @f(ptr %__taskargs)`, params read from an args bundle,
	// and a return that resolves the running task's promise. It still uses the
	// coroHdl return-value slot, so the body/return machinery is unchanged.
	taskBody := decl.IsAsync && sig.MaySuspend

	// For async functions, the IR return type is always ptr (the coro handle).
	// The logical return type (Promise<T>) is stored; T is tracked in currentPromiseTy.
	if decl.IsAsync {
		if retType.IsPromise && retType.PromiseType != nil {
			e.currentPromiseTy = *retType.PromiseType
		}
		// IR return type is ptr (the coroutine handle); a task body returns void.
		e.currentRetType = TypePtr
		e.coroRetLabel = e.freshLabel("coro.ret")
		if taskBody {
			// A may-suspend body: coroHdl slot only; emitTaskEpilogue marshals it
			// into the running task's promise, the trampoline settles/rejects it.
			e.ensureTaskRuntime()
			e.emitAsyncPrologue()
		} else {
			// A non-suspending body: returns a settled task promise via the inline
			// catch-and-settle wrapper (TDD-00084 Part A).
			e.emitInlineAsyncPrologue()
		}
	} else {
		e.currentRetType = retType
	}

	// Build LLVM parameter list and alloca each parameter. Param types come
	// from the already-registered signature (registerFunctions), not
	// recomputed here — same reasoning as retType above: one authoritative
	// computation, reused everywhere, rather than a second copy that could
	// silently drift from it.
	// Array parameters expand to two LLVM params: (ptr, i64 length).
	// Object and scalar parameters are each one ptr/scalar LLVM param.
	var llvmParams []string
	if taskBody {
		// Params come from the args bundle, not LLVM parameters; the only LLVM
		// parameter is the bundle pointer.
		llvmParams = []string{"ptr %__taskargs"}
		if err := e.bindTaskParamsFromBundle(decl, sig); err != nil {
			return err
		}
	}
	for i, p := range decl.Params {
		if taskBody {
			break
		}
		pty := sig.ParamTypes[i]
		// TDD-00062 (Staged V2): a bare `any`/`unknown` parameter is now
		// allowed (same box round-trip as the return type above). Only a
		// nested dynamic shape stays rejected — containsDynamicElement skips
		// the top level, so bare any/unknown passes here.
		if containsDynamicElement(pty) {
			return fmt.Errorf("%d:%d: any/unknown is not yet supported nested inside an array or object parameter type", decl.GetPos().Line, decl.GetPos().Col)
		}
		if err := validateCompositeType(pty, decl.GetPos().Line, decl.GetPos().Col); err != nil {
			return err
		}
		if pty.IsArray {
			llvmParams = append(llvmParams,
				fmt.Sprintf("ptr %%p_%s_ptr", p.Name),
				fmt.Sprintf("i64 %%p_%s_len", p.Name),
			)
			ptrAlloca := "%v_" + p.Name + "_ptr"
			lenAlloca := "%v_" + p.Name + "_len"
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrAlloca))
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))
			e.emitInstr(fmt.Sprintf("store ptr %%p_%s_ptr, ptr %s, align 8", p.Name, ptrAlloca))
			e.emitInstr(fmt.Sprintf("store i64 %%p_%s_len, ptr %s, align 8", p.Name, lenAlloca))
			// A destructured array parameter (`[a, b]: number[]`) unpacks
			// straight from the raw incoming (ptr, len) pair — no need to
			// bind the whole array under its own synthetic name first, since
			// nothing else in the function body can reference it by that
			// name anyway (it was never real source syntax). See
			// docs/tdd/TDD-00029.md's own two-alloca array Symbol shape,
			// reused unchanged by unpackArrayPatternInto for a nested-array
			// destructured element.
			if p.ArrayPattern != nil {
				dataPtrReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtrReg, ptrAlloca))
				if err := e.unpackArrayPatternInto(dataPtrReg, "%p_"+p.Name+"_len", *pty.ElemType, p.ArrayPattern); err != nil {
					return err
				}
				continue
			}
			e.define(p.Name, Symbol{Ptr: ptrAlloca, LenPtr: lenAlloca, Ty: pty})
		} else if isNullableScalar(pty) {
			// A nullable-scalar parameter is passed and stored as its
			// presence-flagged { i1, T } aggregate (TDD-00064 Stage 3).
			llvmParams = append(llvmParams, nullableScalarParamDecl(p.Name, pty))
			e.defineNullableScalarParam(p.Name, "%v_"+p.Name, pty)
		} else {
			llvmParams = append(llvmParams, fmt.Sprintf("%s %%p_%s", pty.IR, p.Name))
			ptrName := "%v_" + p.Name
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, pty.IR, pty.Align()))
			e.emitInstr(fmt.Sprintf("store %s %%p_%s, ptr %s, align %d", pty.IR, p.Name, ptrName, pty.Align()))
			// A destructured object parameter (`{x, y}: T`) unpacks straight
			// from the raw incoming object pointer — same reasoning as the
			// array-pattern branch above.
			if p.ObjectPattern != nil {
				objPtrReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objPtrReg, ptrName))
				if err := e.unpackObjectPatternInto(objPtrReg, pty, p.ObjectPattern, decl.GetPos()); err != nil {
					return err
				}
				continue
			}
			e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
		}
	}

	// Emit body statements.
	for _, stmt := range decl.Body.Body {
		if err := e.emitStmt(stmt); err != nil {
			return err
		}
	}

	// Add implicit terminator and assemble the function IR.
	if taskBody {
		// May-suspend task body: coro.ret resolves the task promise; signature
		// is `void @f(ptr %__taskargs)`.
		e.emitTaskEpilogue()
		e.functions.WriteString(fmt.Sprintf("\ndefine void @%s(%s) {\nentry:\n",
			llvmName, strings.Join(llvmParams, ", ")))
	} else if decl.IsAsync {
		// Non-suspending async: coro.ret fulfills the settled promise, plus a
		// catch block that rejects it on a thrown error (TDD-00084 Part A).
		e.emitInlineAsyncEpilogue()
		e.functions.WriteString(fmt.Sprintf("\ndefine ptr @%s(%s) {\nentry:\n",
			llvmName, strings.Join(llvmParams, ", ")))
	} else {
		// Non-async: void → ret void; non-void → unreachable fallthrough.
		if retType.IR == "void" {
			e.emitTerminator("ret void")
		} else {
			e.emitTerminator("unreachable")
		}
		e.functions.WriteString(fmt.Sprintf("\ndefine %s @%s(%s) {\nentry:\n",
			retType.LLVMRetType(), llvmName, strings.Join(llvmParams, ", ")))
	}
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	// Restore saved context.
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
	e.isAsync = savedIsAsync
	e.coroHdl = savedCoroHdl
	e.asyncPromiseReg = savedAsyncPromiseReg
	e.asyncCatchLabel = savedAsyncCatchLabel
	e.currentPromiseTy = savedPromiseTy
	e.coroRetLabel = savedCoroRetLabel
	e.blockDone = false // main body starts unterminated

	return nil
}

// =============================================================================
// Nested function declarations (TDD-00057)
// =============================================================================

// nestedFuncEntry is one pre-registered nested function declaration's
// mangled LLVM name and signature.
type nestedFuncEntry struct {
	Mangled string
	Sig     FuncSig
}

// nestedFuncScope is the set of function declarations found directly (not
// recursively into a further sub-block) in one enclosing body, pre-scanned
// by pushNestedFuncScope before that body's statements are emitted so a
// forward reference or self-recursive call resolves — the same two-pass
// principle registerFunctions already applies at Program top level, scoped
// down to a single function/closure body. byDecl (keyed by AST identity,
// not name) is what lets emitStmt tell "this exact declaration was
// pre-registered directly in the current body" apart from a deeper,
// unsupported nesting that reaches emitStmt's *ast.FunctionDeclaration case
// via some other block without ever being pre-scanned.
type nestedFuncScope struct {
	byName map[string]nestedFuncEntry
	byDecl map[*ast.FunctionDeclaration]nestedFuncEntry
}

// pushNestedFuncScope pre-scans body (direct statements only) for nested
// function declarations, registers each under a fresh mangled name, and
// pushes the resulting scope onto e.nestedFuncScopes. Every push must be
// matched by a popNestedFuncScope once the body's statements have been
// emitted (skipped on an error return, same as every other per-function
// emitter-state restore in this file — an error aborts the whole
// compilation, so nothing downstream ever observes the unpopped frame).
func (e *Emitter) pushNestedFuncScope(body []ast.Statement) error {
	// Pushed before the scan below runs (and popped on an early-error
	// return, to leave e.nestedFuncScopes exactly as this function found it
	// — every caller relies on that on error) rather than only once fully
	// built, so an earlier-in-this-same-body sibling is already resolvable
	// via resolveFuncRef while a later sibling's own signature is still
	// being computed — the same immediate-write-as-you-go shape
	// registerFunctions (emitter.go) uses for the identical reason. scope's
	// maps are reference types, so mutating the local variable below and
	// reading back through e.nestedFuncScopes see the same underlying data.
	scope := nestedFuncScope{byName: map[string]nestedFuncEntry{}, byDecl: map[*ast.FunctionDeclaration]nestedFuncEntry{}}
	e.nestedFuncScopes = append(e.nestedFuncScopes, scope)

	var unannotated []*ast.FunctionDeclaration
	for _, stmt := range body {
		fd, ok := stmt.(*ast.FunctionDeclaration)
		if !ok {
			continue
		}
		if len(fd.TypeParams) > 0 {
			e.nestedFuncScopes = e.nestedFuncScopes[:len(e.nestedFuncScopes)-1]
			return fmt.Errorf("%d:%d: a generic nested function declaration is not supported", fd.GetPos().Line, fd.GetPos().Col)
		}
		if _, dup := scope.byName[fd.Name]; dup {
			e.nestedFuncScopes = e.nestedFuncScopes[:len(e.nestedFuncScopes)-1]
			return fmt.Errorf("%d:%d: '%s' is already declared in this scope", fd.GetPos().Line, fd.GetPos().Col, fd.Name)
		}
		e.nestedFuncCtr++
		entry := nestedFuncEntry{
			Mangled: fmt.Sprintf("%s__nested%d", fd.Name, e.nestedFuncCtr),
			Sig:     e.buildFunctionSig(fd),
		}
		scope.byName[fd.Name] = entry
		scope.byDecl[fd] = entry
		if fd.ReturnType == nil {
			unannotated = append(unannotated, fd)
		}
	}
	// TDD-00058: same fixed-point re-inference registerFunctions now does,
	// scoped to this body's own directly-declared nested siblings — closes
	// the identical forward-reference boundary ADR-00041 originally found
	// at top level, here for a nested unannotated function calling a
	// same-body unannotated sibling declared later.
	e.reinferUntilFixedPoint(unannotated, func(fd *ast.FunctionDeclaration) FuncSig {
		return e.buildFunctionSig(fd)
	}, func(fd *ast.FunctionDeclaration, sig FuncSig) {
		entry := nestedFuncEntry{Mangled: scope.byName[fd.Name].Mangled, Sig: sig}
		scope.byName[fd.Name] = entry
		scope.byDecl[fd] = entry
	}, func(fd *ast.FunctionDeclaration) Type {
		return scope.byName[fd.Name].Sig.RetType
	})
	return nil
}

// popNestedFuncScope removes the most recently pushed nestedFuncScope frame.
func (e *Emitter) popNestedFuncScope() {
	e.nestedFuncScopes = e.nestedFuncScopes[:len(e.nestedFuncScopes)-1]
}

// resolveFuncRef resolves a bare identifier used as a callee/callback name:
// e.nestedFuncScopes innermost-first, then the flat top-level e.funcs map.
// Every call-site that used to index e.funcs directly by a source-written
// identifier name goes through this instead, so a nested function's mangled
// LLVM name is used wherever its source name would have been.
func (e *Emitter) resolveFuncRef(name string) (string, FuncSig, bool) {
	for i := len(e.nestedFuncScopes) - 1; i >= 0; i-- {
		if entry, ok := e.nestedFuncScopes[i].byName[name]; ok {
			return entry.Mangled, entry.Sig, true
		}
	}
	if sig, ok := e.funcs[name]; ok {
		return name, sig, true
	}
	return "", FuncSig{}, false
}

// =============================================================================
// Closure / arrow-function support
// =============================================================================

// CapturedVar describes one variable captured from an enclosing scope.
type CapturedVar struct {
	Name string
	Ty   Type
	Sym  Symbol // the symbol as it exists in the enclosing scope
	// IsSelf marks the synthetic self-reference capture of a named function
	// expression (`var f = function fact(n) { ... fact(n-1) ... }`): its cell
	// holds the closure's own header pointer rather than a value copied from an
	// enclosing symbol, so its env-build path is special (see
	// emitFunctionExpression). Sym is unused for it.
	IsSelf bool
}

// envStructIR returns the LLVM struct type string for the closure environment.
// Every slot holds a pointer to a shared heap cell (see emitArrowFunctionWithHints),
// regardless of the captured variable's own type.
func envStructIR(caps []CapturedVar) string {
	parts := make([]string, len(caps))
	for i := range caps {
		parts[i] = "ptr"
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// envStructSize returns the byte size of the closure environment: one pointer per capture.
func envStructSize(caps []CapturedVar) int64 {
	return int64(len(caps)) * 8
}

// --- free-variable scanning ---

// addParamBoundNames adds the names params actually bind in their own body
// scope. A destructured param's real bound names are its pattern's field/
// element names, not the param's own Name (a synthetic internal name, e.g.
// "__param0", never referenced by the body) — see gatherCaptures' own
// comment for the wrong-answer bug this avoids: a pattern field sharing a
// name with an outer-scope variable must shadow it, not be free-variable-
// scanned as a capture of the outer binding.
func addParamBoundNames(bound map[string]bool, params []ast.Param) {
	for _, p := range params {
		bound[p.Name] = true
		// Recurse into nested sub-patterns (TDD-00065 Stage 2) so a
		// nested-bound name shadows an outer variable rather than being
		// scanned as a capture of it.
		collectArrayPatternNames(p.ArrayPattern, bound)
		collectObjectPatternNames(p.ObjectPattern, bound)
	}
}

func scanExprFV(expr ast.Expression, bound map[string]bool, result map[string]bool) {
	if expr == nil {
		return
	}
	switch x := expr.(type) {
	case *ast.Identifier:
		if !bound[x.Name] {
			result[x.Name] = true
		}
	case *ast.BinaryExpression:
		scanExprFV(x.Left, bound, result)
		scanExprFV(x.Right, bound, result)
	case *ast.UnaryExpression:
		scanExprFV(x.Arg, bound, result)
	case *ast.UpdateExpression:
		scanExprFV(x.Arg, bound, result)
	case *ast.AssignmentExpression:
		scanExprFV(x.Left, bound, result)
		scanExprFV(x.Right, bound, result)
	case *ast.CallExpression:
		scanExprFV(x.Callee, bound, result)
		for _, a := range x.Args {
			scanExprFV(a, bound, result)
		}
	case *ast.MemberExpression:
		scanExprFV(x.Object, bound, result) // Property is a string, not a var ref
	case *ast.IndexExpression:
		scanExprFV(x.Object, bound, result)
		scanExprFV(x.Index, bound, result)
	case *ast.ArrayLiteral:
		for _, e := range x.Elements {
			scanExprFV(e, bound, result)
		}
	case *ast.ObjectLiteral:
		for _, p := range x.Properties {
			scanExprFV(p.Value, bound, result)
		}
	case *ast.SpreadElement:
		scanExprFV(x.Arg, bound, result)
	case *ast.NewArrayExpression:
		scanExprFV(x.Size, bound, result)
	case *ast.NewSetExpression:
		scanExprFV(x.Init, bound, result)
	case *ast.TemplateLiteral:
		for _, ex := range x.Exprs {
			scanExprFV(ex, bound, result)
		}
	case *ast.TaggedTemplateExpression:
		scanExprFV(x.Tag, bound, result)
		for _, ex := range x.Exprs {
			scanExprFV(ex, bound, result)
		}
	case *ast.ConditionalExpression:
		scanExprFV(x.Test, bound, result)
		scanExprFV(x.Consequent, bound, result)
		scanExprFV(x.Alternate, bound, result)
	case *ast.SequenceExpression:
		for _, sub := range x.Exprs {
			scanExprFV(sub, bound, result)
		}
	case *ast.ArrowFunction:
		// Nested arrow function: its params are bound within its own body.
		innerBound := make(map[string]bool, len(bound)+len(x.Params))
		for k, v := range bound {
			innerBound[k] = v
		}
		addParamBoundNames(innerBound, x.Params)
		if x.Body != nil {
			scanExprFV(x.Body, innerBound, result)
		}
		if x.Block != nil {
			scanStmtsFV(x.Block.Body, innerBound, result)
		}
	case *ast.FunctionExpression:
		// Function expression: its own params are bound within its body;
		// no outer captures leak automatically but the body's free vars
		// against the outer bound set ARE actual captures.
		innerBound := make(map[string]bool, len(bound)+len(x.Params))
		for k, v := range bound {
			innerBound[k] = v
		}
		addParamBoundNames(innerBound, x.Params)
		scanStmtsFV(x.Body.Body, innerBound, result)
		// NumberLiteral, StringLiteral, BooleanLiteral: no identifiers
	}
}

// collectArrayPatternNames records every leaf binding name introduced by an
// array destructuring pattern, recursing into nested sub-patterns
// (TDD-00065 Stage 2) so the free-variable scan doesn't misclassify a
// nested-bound name as an outer capture.
func collectArrayPatternNames(elems []ast.ArrayPatternElem, out map[string]bool) {
	for _, el := range elems {
		switch {
		case el.SubArray != nil:
			collectArrayPatternNames(el.SubArray, out)
		case el.SubObject != nil:
			collectObjectPatternNames(el.SubObject, out)
		case el.Name != "":
			out[el.Name] = true
		}
	}
}

// collectObjectPatternNames is collectArrayPatternNames' object-pattern
// counterpart.
func collectObjectPatternNames(props []ast.DestructProp, out map[string]bool) {
	for _, pr := range props {
		switch {
		case pr.SubArray != nil:
			collectArrayPatternNames(pr.SubArray, out)
		case pr.SubObject != nil:
			collectObjectPatternNames(pr.SubObject, out)
		default:
			out[pr.Local] = true
		}
	}
}

func scanStmtsFV(stmts []ast.Statement, bound map[string]bool, result map[string]bool) {
	// Copy bound so local declarations don't bleed back to the caller.
	local := make(map[string]bool, len(bound))
	for k, v := range bound {
		local[k] = v
	}
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VarDeclaration:
			if s.Init != nil {
				scanExprFV(s.Init, local, result)
			}
			local[s.Name] = true
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				if d.Init != nil {
					scanExprFV(d.Init, local, result)
				}
				local[d.Name] = true
			}
		case *ast.ExpressionStatement:
			scanExprFV(s.Expr, local, result)
		case *ast.ReturnStatement:
			if s.Value != nil {
				scanExprFV(s.Value, local, result)
			}
		case *ast.IfStatement:
			scanExprFV(s.Test, local, result)
			if s.Consequent != nil {
				scanStmtsFV(s.Consequent.Body, local, result)
			}
			if s.Alternate != nil {
				scanStmtsFV([]ast.Statement{s.Alternate}, local, result)
			}
		case *ast.ForStatement:
			inner := make(map[string]bool, len(local))
			for k, v := range local {
				inner[k] = v
			}
			if s.Init != nil {
				if vd, ok := s.Init.(*ast.VarDeclaration); ok {
					if vd.Init != nil {
						scanExprFV(vd.Init, inner, result)
					}
					inner[vd.Name] = true
				} else if vdl, ok := s.Init.(*ast.VarDeclarationList); ok {
					for _, d := range vdl.Decls {
						if d.Init != nil {
							scanExprFV(d.Init, inner, result)
						}
						inner[d.Name] = true
					}
				} else if es, ok := s.Init.(*ast.ExpressionStatement); ok {
					scanExprFV(es.Expr, inner, result)
				}
			}
			if s.Test != nil {
				scanExprFV(s.Test, inner, result)
			}
			for _, upd := range s.Update {
				scanExprFV(upd, inner, result)
			}
			if s.Body != nil {
				scanStmtsFV(s.Body.Body, inner, result)
			}
		case *ast.WhileStatement:
			scanExprFV(s.Test, local, result)
			if s.Body != nil {
				scanStmtsFV(s.Body.Body, local, result)
			}
		case *ast.BlockStatement:
			scanStmtsFV(s.Body, local, result)
		case *ast.ForOfStatement:
			scanExprFV(s.Iterable, local, result)
			inner := make(map[string]bool, len(local)+1)
			for k, v := range local {
				inner[k] = v
			}
			// A destructuring loop variable (TDD-00065) binds each of its
			// pattern's (possibly nested) leaf names, not a single VarName.
			if s.ArrayPattern != nil {
				collectArrayPatternNames(s.ArrayPattern, inner)
			} else if s.ObjectPattern != nil {
				collectObjectPatternNames(s.ObjectPattern, inner)
			} else {
				inner[s.VarName] = true
			}
			if s.Body != nil {
				scanStmtsFV(s.Body.Body, inner, result)
			}
		case *ast.SwitchStatement:
			scanExprFV(s.Discriminant, local, result)
			for _, c := range s.Cases {
				if c.Test != nil {
					scanExprFV(c.Test, local, result)
				}
				scanStmtsFV(c.Body, local, result)
			}
		case *ast.BreakStatement:
			// no identifier references
		case *ast.ContinueStatement:
			// no identifier references
		case *ast.ArrayDestructuring:
			scanExprFV(s.Init, local, result)
			for _, elem := range s.Elems {
				if elem.Default != nil {
					scanExprFV(elem.Default, local, result)
				}
				if elem.Name != "" {
					local[elem.Name] = true
				}
			}
		case *ast.ObjectDestructuring:
			scanExprFV(s.Init, local, result)
			for _, prop := range s.Props {
				if prop.Default != nil {
					scanExprFV(prop.Default, local, result)
				}
				local[prop.Local] = true
			}
		}
	}
}

// gatherCaptures returns the variables from the enclosing scope that the arrow
// function's body references (sorted for deterministic output). Array variables
// cannot be captured yet (would require two env slots).
func (e *Emitter) gatherCaptures(af *ast.ArrowFunction) ([]CapturedVar, error) {
	// Build the initial bound set from the arrow function's own params —
	// see addParamBoundNames' own comment for why a destructured param's
	// pattern names, not its synthetic p.Name, are what actually matters.
	bound := make(map[string]bool, len(af.Params))
	addParamBoundNames(bound, af.Params)
	// Collect all identifier names referenced in the body.
	refs := make(map[string]bool)
	if af.Body != nil {
		scanExprFV(af.Body, bound, refs)
	}
	if af.Block != nil {
		scanStmtsFV(af.Block.Body, bound, refs)
	}

	var caps []CapturedVar
	for name := range refs {
		sym, found := e.lookup(name)
		if !found {
			continue // built-in, function name, etc.
		}
		if sym.Ty.IsArray {
			return nil, fmt.Errorf("capturing array variable '%s' in a closure is not yet supported", name)
		}
		caps = append(caps, CapturedVar{Name: name, Ty: sym.Ty, Sym: sym})
	}
	// Sort for deterministic LLVM output.
	for i := 0; i < len(caps); i++ {
		for j := i + 1; j < len(caps); j++ {
			if caps[i].Name > caps[j].Name {
				caps[i], caps[j] = caps[j], caps[i]
			}
		}
	}
	return caps, nil
}

// --- closure function emission ---

// emitClosureFunc emits the named LLVM function for an arrow function into
// e.functions. The function takes ptr %env as its first parameter, followed by
// the arrow function's regular parameters. Captured variables are accessed via
// GEP into %env.
func (e *Emitter) emitClosureFunc(af *ast.ArrowFunction, caps []CapturedVar, retTy Type, paramTypes []Type, closureName string) error {
	// Save emitter state.
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone
	savedIsAsync := e.isAsync
	savedCoroHdl := e.coroHdl
	savedAsyncPromiseReg := e.asyncPromiseReg
	savedAsyncCatchLabel := e.asyncCatchLabel
	savedPromiseTy := e.currentPromiseTy
	savedCoroRetLabel := e.coroRetLabel
	// A closure's own break/continue/named-label context starts empty, not
	// inherited from whatever loop happens to enclose it lexically — a
	// `break`/`continue`/`break LABEL` inside this closure's body can only
	// ever target a loop declared *within* this same closure, never one in
	// the enclosing function (that would need a `br label` crossing into a
	// different LLVM function's own label space, which is invalid IR).
	// Restored via defer, not a plain assignment below — see
	// emitFunctionDeclAs' identical reset for why: an error return
	// partway through this closure's own body emission must not skip the
	// restore, or an *enclosing* loop's own deferred break/continue/label
	// stack pop panics on a stack it no longer recognizes.
	savedBreakStack := e.breakStack
	savedContinueStack := e.continueStack
	savedNamedLabelStack := e.namedLabelStack
	e.breakStack = nil
	e.continueStack = nil
	e.namedLabelStack = nil
	// currentGenerator resets the same way, same reasoning — see
	// emitFunctionDeclAs's identical reset for the real, confirmed bug this
	// avoids (a `return`/`yield` inside this function's own body must
	// never be misrouted into an *enclosing* generator's own suspend
	// machinery).
	savedCurrentGenerator := e.currentGenerator
	e.currentGenerator = nil
	defer func() {
		e.breakStack = savedBreakStack
		e.continueStack = savedContinueStack
		e.namedLabelStack = savedNamedLabelStack
		e.currentGenerator = savedCurrentGenerator
	}()

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = retTy
	e.isAsync = af.IsAsync
	e.coroHdl = ""
	e.asyncPromiseReg = ""
	e.asyncCatchLabel = ""
	e.currentPromiseTy = TypeVoid
	e.coroRetLabel = ""
	e.pushScope()
	if af.Block != nil {
		if err := e.pushNestedFuncScope(af.Block.Body); err != nil {
			return err
		}
		defer e.popNestedFuncScope()
	}

	// Async arrow function: same treatment emitFunctionDecl already gives a
	// named async function — an `await` inside a closure body was already
	// mechanically handled by emitAwait regardless of this flag, but
	// `return`/the implicit expression-body result was silently taking the
	// plain (non-async) path with nothing ever setting e.isAsync for a
	// closure, so it returned the raw value directly instead of wrapping it
	// in the malloc'd Promise slot every caller (including
	// emit_http.go's async-handler unwrap) expects. Found while wiring
	// http.listen's own async-handler support (ADR-00050) — a real,
	// pre-existing gap, not new: async arrow functions containing `await`
	// were unreachable in any previously-tested shape (named top-level
	// functions can't be passed by reference either — a separate, already-
	// tracked limitation — so an async arrow function used to be the only
	// way to get an async *callback*, and nothing exercised it before now).
	if af.IsAsync {
		if retTy.IsPromise && retTy.PromiseType != nil {
			e.currentPromiseTy = *retTy.PromiseType
		}
		e.coroRetLabel = e.freshLabel("coro.ret")
		// An async arrow is never may-suspend-classified — inline settled path.
		e.emitInlineAsyncPrologue()
	}

	// Build the LLVM parameter list string and alloca+store each regular param.
	// Array parameters expand to two LLVM params: (ptr, i64 length) —
	// mirrors emitFunctionDeclAs's own identical shape exactly. Originally
	// rejected entirely (see ADR-00105's Investigation for where the gap
	// was first found, and ADR-00151/TDD-00059 for confirming a plain
	// array param and a rest param independently produced invalid LLVM IR,
	// not just the destructured case) — fixed here rather than left
	// documented, since every call site that invokes a closure
	// (emitClosureCallByPtr, emitCBCall) needed the matching decomposition
	// too and now has it, closing this out as a real fix rather than a
	// narrower guard. Capturing an array *variable* from the enclosing
	// scope (a separate mechanism — env-struct slots, not parameters) is
	// untouched and still rejected by gatherCaptures; that's a genuinely
	// different problem (an env slot holds one heap-cell pointer, not a
	// (ptr, i64) pair) not attempted here.
	paramStr := "ptr %env"
	for i, p := range af.Params {
		pty := paramTypes[i]
		if pty.IsArray {
			paramStr += fmt.Sprintf(", ptr %%p_%s_ptr, i64 %%p_%s_len", p.Name, p.Name)
			ptrAlloca := "%v_" + p.Name + "_ptr"
			lenAlloca := "%v_" + p.Name + "_len"
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrAlloca))
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))
			e.emitInstr(fmt.Sprintf("store ptr %%p_%s_ptr, ptr %s, align 8", p.Name, ptrAlloca))
			e.emitInstr(fmt.Sprintf("store i64 %%p_%s_len, ptr %s, align 8", p.Name, lenAlloca))
			if p.ArrayPattern != nil {
				dataPtrReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtrReg, ptrAlloca))
				if err := e.unpackArrayPatternInto(dataPtrReg, "%p_"+p.Name+"_len", *pty.ElemType, p.ArrayPattern); err != nil {
					return err
				}
				continue
			}
			e.define(p.Name, Symbol{Ptr: ptrAlloca, LenPtr: lenAlloca, Ty: pty})
			continue
		}
		if isNullableScalar(pty) {
			// Nullable-scalar closure parameter (TDD-00064 Stage 3).
			paramStr += ", " + nullableScalarParamDecl(p.Name, pty)
			e.defineNullableScalarParam(p.Name, "%v_"+p.Name, pty)
			continue
		}
		paramStr += fmt.Sprintf(", %s %%p_%s", pty.IR, p.Name)
		ptrName := "%v_" + p.Name
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, pty.IR, pty.Align()))
		e.emitInstr(fmt.Sprintf("store %s %%p_%s, ptr %s, align %d", pty.IR, p.Name, ptrName, pty.Align()))
		if p.ObjectPattern != nil {
			objPtrReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objPtrReg, ptrName))
			if err := e.unpackObjectPatternInto(objPtrReg, pty, p.ObjectPattern, af.GetPos()); err != nil {
				return err
			}
			continue
		}
		e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
	}

	// Set up captured-variable access: each env slot holds a pointer to a heap
	// cell shared with the enclosing scope (and any other closure capturing the
	// same variable), so load that pointer once and use it directly as storage.
	// These go in the body builder (not allocas) but come before body statements.
	if len(caps) > 0 {
		ir := envStructIR(caps)
		for i, cap := range caps {
			slotGep := fmt.Sprintf("%%vcapslot_%s", cap.Name)
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%env, i32 0, i32 %d", slotGep, ir, i))
			cellPtr := fmt.Sprintf("%%vcap_%s", cap.Name)
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cellPtr, slotGep))
			e.define(cap.Name, Symbol{Ptr: cellPtr, Ty: cap.Ty, Boxed: true, IsConst: cap.Sym.IsConst})
		}
	}

	// Emit the body.
	if af.Block != nil {
		for _, stmt := range af.Block.Body {
			if err := e.emitStmt(stmt); err != nil {
				return err
			}
		}
		if af.IsAsync {
			// Fallthrough (no explicit `return`) resolves to the malloc'd
			// slot's zero-initialized-by-malloc contents — matching a plain
			// async function falling off the end. Every explicit `return`
			// inside the block already branched to coroRetLabel itself
			// (emitReturn's async-aware path).
			e.emitInlineAsyncEpilogue()
		} else if retTy.IR == "void" {
			e.emitTerminator("ret void")
		} else {
			e.emitTerminator("unreachable")
		}
	} else if af.Body != nil {
		if af.IsAsync {
			val, err := e.emitExpr(af.Body)
			if err != nil {
				return err
			}
			if e.currentPromiseTy.IR != "void" && e.currentPromiseTy.IR != "" {
				val = e.coerce(val, e.currentPromiseTy)
				align := e.currentPromiseTy.Align()
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d",
					StructFieldIR(e.currentPromiseTy), val.Ref, e.coroHdl, align))
			}
			e.emitInlineAsyncEpilogue()
		} else if retTy.IR == "void" {
			if _, err := e.emitExpr(af.Body); err != nil {
				return err
			}
			e.emitTerminator("ret void")
		} else if retTy.IsArray {
			// Mirrors emitReturn's own IsArray branch (emit_stmts.go) —
			// same named-array-identifier vs. arbitrary-expression split,
			// and the same hint-aware evaluation (emitExprWithObjectHint)
			// so an array-literal body's element type coerces against the
			// declared/inferred return type instead of self-inferring.
			// Found missing while wiring .flatMap(): an arrow-function body
			// returning an array (`(x) => [x, x*10]`) was the first thing
			// to actually exercise an expression-bodied arrow function
			// returning an array — its `ret` instruction used the array's
			// scalar `ptr` IR instead of the aggregate `{ptr, i64}`
			// LLVMRetType, a hard clang-stage type mismatch, not survivable
			// at all (a block-bodied arrow/named function already went
			// through emitReturn's own correct array-aware path via
			// emitStmt — only the expression-body shortcut here missed it).
			if id, ok := af.Body.(*ast.Identifier); ok {
				sym, ok := e.lookup(id.Name)
				if !ok {
					return fmt.Errorf("%d:%d: undefined variable '%s'", af.Body.GetPos().Line, af.Body.GetPos().Col, id.Name)
				}
				if !sym.Ty.IsArray {
					return fmt.Errorf("%d:%d: '%s' is not an array", af.Body.GetPos().Line, af.Body.GetPos().Col, id.Name)
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
			} else {
				arrVal, err := e.emitExprWithObjectHint(af.Body, retTy)
				if err != nil {
					return err
				}
				if !arrVal.Ty.IsArray {
					return fmt.Errorf("%d:%d: expression is not an array", af.Body.GetPos().Line, af.Body.GetPos().Col)
				}
				e.emitTerminator(fmt.Sprintf("ret {ptr, i64} %s", arrVal.Ref))
			}
		} else {
			val, err := e.emitExprWithObjectHint(af.Body, retTy)
			if err != nil {
				return err
			}
			if retTy.IsDynamic {
				// Constrained union return type (TDD-00043) — same reasoning
				// as emitReturn's own IsDynamic branch (emit_stmts.go): coerce
				// doesn't box, so a dynamic-typed return needs emitBoxValue
				// explicitly. Bare any/unknown is still rejected as a return
				// type before this function is ever called.
				if retTy.UnionMembers != nil && !unionAllowsAssignmentFrom(retTy, val.Ty) {
					return fmt.Errorf("%d:%d: return value's type is not a member of the declared union return type", af.Body.GetPos().Line, af.Body.GetPos().Col)
				}
				val, err = e.emitBoxValue(val)
				if err != nil {
					return err
				}
			} else {
				val = e.coerce(val, retTy)
			}
			e.emitTerminator(fmt.Sprintf("ret %s %s", retTy.LLVMRetType(), val.Ref))
		}
	}

	// Write the function into e.functions.
	e.functions.WriteString(fmt.Sprintf("\ndefine %s %s(%s) {\nentry:\n",
		retTy.LLVMRetType(), closureName, paramStr))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	// Restore state.
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
	e.blockDone = savedBlockDone
	e.isAsync = savedIsAsync
	e.coroHdl = savedCoroHdl
	e.asyncPromiseReg = savedAsyncPromiseReg
	e.asyncCatchLabel = savedAsyncCatchLabel
	e.currentPromiseTy = savedPromiseTy
	e.coroRetLabel = savedCoroRetLabel
	return nil
}

// emitArrowFunction creates the closure struct {funcPtr, envPtr} on the heap
// and returns a Value pointing to it.
func (e *Emitter) emitArrowFunction(af *ast.ArrowFunction) (Value, error) {
	return e.emitArrowFunctionWithHints(af, nil)
}

// blockHasReturn reports whether a return statement is reachable anywhere in
// the block, recursing into nested control-flow bodies but not into nested
// function/arrow literals (which have their own, independently-inferred return type).
func blockHasReturn(block *ast.BlockStatement) bool {
	if block == nil {
		return false
	}
	for _, stmt := range block.Body {
		if stmtHasReturn(stmt) {
			return true
		}
	}
	return false
}

func stmtHasReturn(stmt ast.Statement) bool {
	switch s := stmt.(type) {
	case *ast.ReturnStatement:
		return true
	case *ast.BlockStatement:
		return blockHasReturn(s)
	case *ast.IfStatement:
		if blockHasReturn(s.Consequent) {
			return true
		}
		return s.Alternate != nil && stmtHasReturn(s.Alternate)
	case *ast.ForStatement:
		return blockHasReturn(s.Body)
	case *ast.ForOfStatement:
		return blockHasReturn(s.Body)
	case *ast.ForInStatement:
		return blockHasReturn(s.Body)
	case *ast.WhileStatement:
		return blockHasReturn(s.Body)
	case *ast.DoWhileStatement:
		return blockHasReturn(s.Body)
	case *ast.SwitchStatement:
		for _, c := range s.Cases {
			for _, cs := range c.Body {
				if stmtHasReturn(cs) {
					return true
				}
			}
		}
		return false
	case *ast.TryStatement:
		if blockHasReturn(s.Body) {
			return true
		}
		if s.Catch != nil && blockHasReturn(s.Catch.Body) {
			return true
		}
		return s.Finally != nil && blockHasReturn(s.Finally)
	default:
		return false
	}
}

// firstReturnExprInBlock finds the first reachable return statement's value
// expression in the block (same recursion shape as blockHasReturn/
// stmtHasReturn — nested control-flow bodies, not nested function/arrow
// literals), skipping bare `return;` statements (nothing to infer from) in
// favor of a later one that has a value. Used to give an unannotated
// function/arrow function a real return type instead of defaulting to
// void/i64 regardless of what it actually returns.
func firstReturnExprInBlock(block *ast.BlockStatement) ast.Expression {
	if block == nil {
		return nil
	}
	for _, stmt := range block.Body {
		if e := firstReturnExprInStmt(stmt); e != nil {
			return e
		}
	}
	return nil
}

func firstReturnExprInStmt(stmt ast.Statement) ast.Expression {
	switch s := stmt.(type) {
	case *ast.ReturnStatement:
		return s.Value
	case *ast.BlockStatement:
		return firstReturnExprInBlock(s)
	case *ast.IfStatement:
		if e := firstReturnExprInBlock(s.Consequent); e != nil {
			return e
		}
		if s.Alternate != nil {
			return firstReturnExprInStmt(s.Alternate)
		}
	case *ast.ForStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.ForOfStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.ForInStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.WhileStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.DoWhileStatement:
		return firstReturnExprInBlock(s.Body)
	case *ast.SwitchStatement:
		for _, c := range s.Cases {
			for _, cs := range c.Body {
				if e := firstReturnExprInStmt(cs); e != nil {
					return e
				}
			}
		}
	case *ast.TryStatement:
		if e := firstReturnExprInBlock(s.Body); e != nil {
			return e
		}
		if s.Catch != nil {
			if e := firstReturnExprInBlock(s.Catch.Body); e != nil {
				return e
			}
		}
		if s.Finally != nil {
			return firstReturnExprInBlock(s.Finally)
		}
	}
	return nil
}

// inferUnannotatedReturnType is the shared best-effort inference used by both
// registerFunctions (top-level function declarations) and
// emitArrowFunctionWithHints/inferExprType's *ast.ArrowFunction case
// (block-bodied arrow functions) when no explicit return-type annotation is
// present: push the function's own parameters into a temporary scope
// (inferExprType and its helpers never emit IR or mint registers, so this is
// safe to call before the real function body exists), then infer the first
// reachable return statement's expression type and use it as-is — including
// plain scalars, not just object/array/closure/Date. Returning ok=false (no
// reachable return has a value at all) leaves the caller's own default
// (void, or a scalar placeholder) untouched. A function with multiple
// returns of different shapes still only considers the first one; not
// attempted here — this compiler has no general union-type support beyond
// `T | null` (see the project's own instructions), so a function that legitimately returns
// different types on different paths was never a designed-for case.
func (e *Emitter) inferUnannotatedReturnType(block *ast.BlockStatement, paramNames []string, paramTypes []Type) (Type, bool) {
	retExpr := firstReturnExprInBlock(block)
	if retExpr == nil {
		return Type{}, false
	}
	e.pushScope()
	for i, name := range paramNames {
		e.define(name, Symbol{Ty: paramTypes[i]})
	}
	inferred := e.inferExprType(retExpr)
	e.popScope()
	return inferred, true
}

// emitArrowFunctionWithHints is like emitArrowFunction but fills in types for
// parameters that have no annotation, using hints[i] when available. This lets
// HOF callers propagate the element type into untyped lambda parameters.
func (e *Emitter) emitArrowFunctionWithHints(af *ast.ArrowFunction, hints []Type) (Value, error) {
	caps, err := e.gatherCaptures(af)
	if err != nil {
		return Value{}, err
	}

	// Resolve param types: use hint when no annotation is present.
	paramTypes := make([]Type, len(af.Params))
	for i, p := range af.Params {
		if p.Rest && p.Type == nil {
			// Same default rest-element type buildFunctionSig (emitter.go)
			// already gives an unannotated named-function rest param — kept
			// consistent rather than falling into the plain-scalar
			// unannotated-param default just below, which would be wrong
			// for a rest param specifically (it always collects into an
			// array, never a bare scalar).
			paramTypes[i] = ArrayOf(TypeI64)
		} else if p.Type == nil && i < len(hints) {
			paramTypes[i] = hints[i]
		} else if p.Type == nil {
			paramTypes[i] = TypeI64
			paramTypes[i].Inferred = true // no annotation, no hint — see docs/adr/ADR-00042.md
		} else {
			paramTypes[i] = e.resolveType(p.Type)
		}
		// TDD-00062 (Staged V2): a bare `any`/`unknown` arrow-function
		// parameter is allowed — the closure-call path (emitClosureCallByPtr)
		// already boxes a dynamic-typed argument. Only a nested dynamic shape
		// stays rejected.
		if containsDynamicElement(paramTypes[i]) {
			return Value{}, fmt.Errorf("%d:%d: any/unknown is not yet supported nested inside an array or object parameter type", af.GetPos().Line, af.GetPos().Col)
		}
		if err := validateCompositeType(paramTypes[i], af.GetPos().Line, af.GetPos().Col); err != nil {
			return Value{}, err
		}
	}
	var retTy Type
	if af.RetType != nil {
		retTy = e.resolveType(af.RetType)
		// TDD-00062 (Staged V2): bare `any`/`unknown` arrow-function return
		// type is allowed; only a nested dynamic shape stays rejected.
		if containsDynamicElement(retTy) {
			return Value{}, fmt.Errorf("%d:%d: any/unknown is not yet supported nested inside an array or object return type", af.GetPos().Line, af.GetPos().Col)
		}
		if err := validateCompositeType(retTy, af.GetPos().Line, af.GetPos().Col); err != nil {
			return Value{}, err
		}
	} else if af.Body != nil {
		// Temporarily push params into scope so inferExprType can resolve them.
		e.pushScope()
		for i, p := range af.Params {
			e.define(p.Name, Symbol{Ptr: fmt.Sprintf("%%__hint_%d", i), Ty: paramTypes[i]})
		}
		retTy = e.inferExprType(af.Body)
		e.popScope()
	} else if blockHasReturn(af.Block) {
		paramNames := make([]string, len(af.Params))
		for i, p := range af.Params {
			paramNames[i] = p.Name
		}
		if inferred, ok := e.inferUnannotatedReturnType(af.Block, paramNames, paramTypes); ok {
			retTy = inferred
		} else {
			retTy = TypeI64 // block body: scalar default, caller may override via annotation
		}
	} else {
		retTy = TypeVoid // block body with no reachable return (e.g. forEach callback)
	}

	// Emit the LLVM function for this closure.
	closureName := fmt.Sprintf("@__closure_%d", e.closureCtr)
	e.closureCtr++
	if err := e.emitClosureFunc(af, caps, retTy, paramTypes, closureName); err != nil {
		return Value{}, err
	}

	// Allocate the 16-byte closure header {ptr funcPtr, ptr envPtr}.
	e.ensureMalloc()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))

	// Store function pointer into header[0].
	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", closureName, fpSlot))

	// Allocate and populate the environment (if there are captures).
	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, hdr))
	if len(caps) > 0 {
		envSize := envStructSize(caps)
		envIR := envStructIR(caps)
		env := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", env, envSize))
		for i, cap := range caps {
			cellPtr := cap.Sym.Ptr
			if !cap.Sym.Boxed {
				// First closure to capture this variable: promote it to a heap
				// cell shared by pointer with the enclosing scope and every
				// closure that captures it (instead of copying its value).
				newCell := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", newCell, cap.Ty.Align()))
				curVal := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
					curVal, cap.Ty.IR, cap.Sym.Ptr, cap.Ty.Align()))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d",
					cap.Ty.IR, curVal, newCell, cap.Ty.Align()))
				e.updateSymbolInPlace(cap.Name, Symbol{Ptr: newCell, Ty: cap.Ty, Boxed: true, IsConst: cap.Sym.IsConst})
				cellPtr = newCell
			}
			slotReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d",
				slotReg, envIR, env, i))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cellPtr, slotReg))
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, epSlot))
	} else {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", epSlot))
	}

	closureTy := FuncType(paramTypes, retTy)
	if len(af.Params) > 0 && af.Params[len(af.Params)-1].Rest {
		closureTy.FuncHasRest = true
	}
	return Value{Ref: hdr, Ty: closureTy}, nil
}

// emitFunctionExpression handles an anonymous function expression
// (`var f = function(x): T { return x; }`) — the same closure-value
// construction emitArrowFunctionWithHints already uses for arrows, adapted
// for a FunctionExpression's block-only body (function expressions never
// have expression bodies) and with the same capture / LLVM-function-emission
// / {funcPtr,envPtr} heap-allocation pipeline.
func (e *Emitter) emitFunctionExpression(fe *ast.FunctionExpression, hints []Type) (Value, error) {
	// Gather captured variables BEFORE resetting emitter state — the
	// free-variable scan needs the enclosing scope's context (same
	// ordering gatherCaptures uses for arrow functions).
	refs := make(map[string]bool)
	bound := make(map[string]bool, len(fe.Params))
	addParamBoundNames(bound, fe.Params)
	scanStmtsFV(fe.Body.Body, bound, refs)

	var caps []CapturedVar
	for name := range refs {
		// A named function expression's own name resolves to the function
		// itself inside its body (a self-reference binding added below), and
		// shadows any enclosing variable of the same name — so it is never
		// captured from the enclosing scope here.
		if name == fe.Name {
			continue
		}
		sym, found := e.lookup(name)
		if !found {
			continue
		}
		if sym.Ty.IsArray {
			return Value{}, fmt.Errorf("capturing array variable '%s' in a closure is not yet supported", name)
		}
		caps = append(caps, CapturedVar{Name: name, Ty: sym.Ty, Sym: sym})
	}
	// Sort for deterministic LLVM output.
	for i := 0; i < len(caps); i++ {
		for j := i + 1; j < len(caps); j++ {
			if caps[i].Name > caps[j].Name {
				caps[i], caps[j] = caps[j], caps[i]
			}
		}
	}

	// Resolve param types.
	paramTypes := make([]Type, len(fe.Params))
	for i, p := range fe.Params {
		if p.Rest && p.Type == nil {
			paramTypes[i] = ArrayOf(TypeI64)
		} else if p.Type == nil && i < len(hints) {
			paramTypes[i] = hints[i]
		} else if p.Type == nil {
			paramTypes[i] = TypeI64
			paramTypes[i].Inferred = true
		} else {
			paramTypes[i] = e.resolveType(p.Type)
		}
		// TDD-00062 (Staged V2): a bare `any`/`unknown` function-expression
		// parameter is allowed (same closure-call boxing as arrow functions).
		// Only a nested dynamic shape stays rejected.
		if containsDynamicElement(paramTypes[i]) {
			return Value{}, fmt.Errorf("%d:%d: any/unknown is not yet supported nested inside an array or object parameter type", fe.GetPos().Line, fe.GetPos().Col)
		}
		if err := validateCompositeType(paramTypes[i], fe.GetPos().Line, fe.GetPos().Col); err != nil {
			return Value{}, err
		}
	}

	// Resolve return type.
	var retTy Type
	if fe.RetType != nil {
		retTy = e.resolveType(fe.RetType)
		// TDD-00062 (Staged V2): bare `any`/`unknown` function-expression
		// return type is allowed; only a nested dynamic shape stays rejected.
		if containsDynamicElement(retTy) {
			return Value{}, fmt.Errorf("%d:%d: any/unknown is not yet supported nested inside an array or object return type", fe.GetPos().Line, fe.GetPos().Col)
		}
		if err := validateCompositeType(retTy, fe.GetPos().Line, fe.GetPos().Col); err != nil {
			return Value{}, err
		}
	} else if blockHasReturn(fe.Body) {
		paramNames := make([]string, len(fe.Params))
		for i, p := range fe.Params {
			paramNames[i] = p.Name
		}
		if inferred, ok := e.inferUnannotatedReturnType(fe.Body, paramNames, paramTypes); ok {
			retTy = inferred
		} else {
			retTy = TypeI64
		}
	} else {
		retTy = TypeVoid
	}

	// A named function expression binds its own name inside its body for
	// self-reference/recursion (TDD-00060). Model it as a synthetic capture
	// whose env cell holds this closure's own header pointer — filled after the
	// header is allocated (below). Appended last so its env-slot index is
	// deterministic and stable across the body-setup and env-build loops. Only
	// added when the body actually references the name (refs), so a merely
	// decorative name costs nothing.
	selfTy := FuncType(paramTypes, retTy)
	if len(fe.Params) > 0 && fe.Params[len(fe.Params)-1].Rest {
		selfTy.FuncHasRest = true
	}
	if fe.Name != "" {
		// A named function expression whose name shadows a top-level function of
		// the same name is not yet supported: inside the expression body, JS
		// scoping makes the name refer to the expression itself, but this
		// compiler resolves top-level function references *before* codegen sees
		// this self-scope, so the self-binding below can't reclaim them and the
		// recursion would silently call the outer function instead. Reject the
		// collision cleanly rather than miscompile it — the plain
		// (non-shadowing) recursive case is unaffected.
		//
		// Detected two ways because the full pipeline runs the resolver's rename
		// pass (TDD-00041) but the direct parse→emit test path does not:
		//   (1) resolver present: the body's `N` was rewritten to the outer
		//       function's mangled `N__kml_mod<N>`, and the outer decl renamed,
		//       so resolveFuncRef(fe.Name) misses — catch the mangled reference.
		//   (2) resolver absent: the name is still `N` and the outer function is
		//       still registered under it — catch via resolveFuncRef.
		mangledSelfPrefix := fe.Name + "__kml_mod"
		collides := false
		for ref := range refs {
			if strings.HasPrefix(ref, mangledSelfPrefix) {
				collides = true
				break
			}
		}
		if refs[fe.Name] {
			if _, _, topLevelExists := e.resolveFuncRef(fe.Name); topLevelExists {
				collides = true
			}
		}
		if collides {
			return Value{}, fmt.Errorf("%d:%d: a named function expression whose name '%s' shadows a top-level function of the same name is not yet supported — rename one of them", fe.GetPos().Line, fe.GetPos().Col, fe.Name)
		}
		if refs[fe.Name] {
			caps = append(caps, CapturedVar{Name: fe.Name, Ty: selfTy, IsSelf: true})
		}
	}

	// Emit the LLVM function for this closure. Reuse emitClosureFunc by
	// adapting to its ArrowFunction-typed interface — the only ArrowFunction
	// fields emitClosureFunc actually uses are Params, Block, Body, IsAsync,
	// and GetPos. Block and Body are mutually exclusive; function expressions
	// only ever have a Body (block), never an expression body.
	closureName := fmt.Sprintf("@__closure_%d", e.closureCtr)
	e.closureCtr++

	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone
	savedIsAsync := e.isAsync
	savedCoroHdl := e.coroHdl
	savedAsyncPromiseReg := e.asyncPromiseReg
	savedAsyncCatchLabel := e.asyncCatchLabel
	savedPromiseTy := e.currentPromiseTy
	savedCoroRetLabel := e.coroRetLabel
	// Same reset emitClosureFunc gives an arrow function's own break/
	// continue/named-label context, restored via defer for the same
	// reason (see its comment) — an error return partway through this
	// function expression's own body must not skip the restore, or an
	// enclosing loop's own deferred stack pop panics.
	savedBreakStack := e.breakStack
	savedContinueStack := e.continueStack
	savedNamedLabelStack := e.namedLabelStack
	e.breakStack = nil
	e.continueStack = nil
	e.namedLabelStack = nil
	// currentGenerator resets the same way, same reasoning — see
	// emitFunctionDeclAs's identical reset for the real, confirmed bug this
	// avoids (a `return`/`yield` inside this function's own body must
	// never be misrouted into an *enclosing* generator's own suspend
	// machinery).
	savedCurrentGenerator := e.currentGenerator
	e.currentGenerator = nil
	defer func() {
		e.breakStack = savedBreakStack
		e.continueStack = savedContinueStack
		e.namedLabelStack = savedNamedLabelStack
		e.currentGenerator = savedCurrentGenerator
	}()

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = retTy
	e.isAsync = fe.IsAsync
	e.coroHdl = ""
	e.asyncPromiseReg = ""
	e.asyncCatchLabel = ""
	e.currentPromiseTy = TypeVoid
	e.coroRetLabel = ""
	e.pushScope()
	if err := e.pushNestedFuncScope(fe.Body.Body); err != nil {
		return Value{}, err
	}
	defer e.popNestedFuncScope()

	if fe.IsAsync {
		if retTy.IsPromise && retTy.PromiseType != nil {
			e.currentPromiseTy = *retTy.PromiseType
		}
		e.coroRetLabel = e.freshLabel("coro.ret")
		// A function expression is never may-suspend-classified — inline path.
		e.emitInlineAsyncPrologue()
	}

	// Build the LLVM parameter list string and alloca+store each regular param.
	// Mirrors emitClosureFunc's own identical shape exactly.
	paramStr := "ptr %env"
	closureParamTypes := paramTypes // save a copy before the loop consumes paramTypes
	for _, p := range fe.Params {
		pty := paramTypes[0]
		paramTypes = paramTypes[1:] // consume
		if pty.IsArray {
			paramStr += fmt.Sprintf(", ptr %%p_%s_ptr, i64 %%p_%s_len", p.Name, p.Name)
			ptrAlloca := "%v_" + p.Name + "_ptr"
			lenAlloca := "%v_" + p.Name + "_len"
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrAlloca))
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenAlloca))
			e.emitInstr(fmt.Sprintf("store ptr %%p_%s_ptr, ptr %s, align 8", p.Name, ptrAlloca))
			e.emitInstr(fmt.Sprintf("store i64 %%p_%s_len, ptr %s, align 8", p.Name, lenAlloca))
			if p.ArrayPattern != nil {
				dataPtrReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataPtrReg, ptrAlloca))
				elemTy := TypeI64
				if pty.ElemType != nil {
					elemTy = *pty.ElemType
				}
				if err := e.unpackArrayPatternInto(dataPtrReg, "%p_"+p.Name+"_len", elemTy, p.ArrayPattern); err != nil {
					return Value{}, err
				}
				continue
			}
			e.define(p.Name, Symbol{Ptr: ptrAlloca, LenPtr: lenAlloca, Ty: pty})
			continue
		}
		if isNullableScalar(pty) {
			// Nullable-scalar function-expression parameter (TDD-00064 Stage 3).
			paramStr += ", " + nullableScalarParamDecl(p.Name, pty)
			e.defineNullableScalarParam(p.Name, "%v_"+p.Name, pty)
			continue
		}
		paramStr += fmt.Sprintf(", %s %%p_%s", pty.IR, p.Name)
		ptrName := "%v_" + p.Name
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, pty.IR, pty.Align()))
		e.emitInstr(fmt.Sprintf("store %s %%p_%s, ptr %s, align %d", pty.IR, p.Name, ptrName, pty.Align()))
		if p.ObjectPattern != nil {
			objPtrReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objPtrReg, ptrName))
			if err := e.unpackObjectPatternInto(objPtrReg, pty, p.ObjectPattern, fe.GetPos()); err != nil {
				return Value{}, err
			}
			continue
		}
		e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
	}

	// Set up captured-variable access — same pattern emitClosureFunc uses.
	if len(caps) > 0 {
		ir := envStructIR(caps)
		for i, cap := range caps {
			slotGep := fmt.Sprintf("%%vcapslot_%s", cap.Name)
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %%env, i32 0, i32 %d", slotGep, ir, i))
			cellPtr := fmt.Sprintf("%%vcap_%s", cap.Name)
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cellPtr, slotGep))
			e.define(cap.Name, Symbol{Ptr: cellPtr, Ty: cap.Ty, Boxed: true, IsConst: cap.Sym.IsConst})
		}
	}

	// Emit the body.
	for _, stmt := range fe.Body.Body {
		if err := e.emitStmt(stmt); err != nil {
			return Value{}, err
		}
	}
	if fe.IsAsync {
		e.emitInlineAsyncEpilogue()
	} else if retTy.IR == "void" {
		e.emitTerminator("ret void")
	} else {
		e.emitTerminator("unreachable")
	}

	// Write the function into e.functions — exactly the same
	// pattern emitClosureFunc uses: define + allocas + body + }.
	e.functions.WriteString(fmt.Sprintf("\ndefine %s %s(%s) {\nentry:\n",
		retTy.LLVMRetType(), closureName, paramStr))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	// Restore state.
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.labelCtr = savedLabelCtr
	e.scopes = savedScopes
	e.currentRetType = savedRetType
	e.blockDone = savedBlockDone
	e.isAsync = savedIsAsync
	e.coroHdl = savedCoroHdl
	e.asyncPromiseReg = savedAsyncPromiseReg
	e.asyncCatchLabel = savedAsyncCatchLabel
	e.currentPromiseTy = savedPromiseTy
	e.coroRetLabel = savedCoroRetLabel

	// Allocate the {funcPtr, envPtr} closure header.
	e.ensureMalloc()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))

	fpSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", closureName, fpSlot))

	epSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, hdr))
	if len(caps) > 0 {
		envSize := envStructSize(caps)
		envIR := envStructIR(caps)
		env := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", env, envSize))
		for i, cap := range caps {
			// Self-reference cell (named function expression): holds this
			// closure's own header pointer. Circular by construction — the
			// header's env contains this cell, and the cell points back at the
			// header — which is exactly what lets the body call itself.
			if cap.IsSelf {
				selfCell := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", selfCell))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, selfCell))
				slotReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d",
					slotReg, envIR, env, i))
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", selfCell, slotReg))
				continue
			}
			cellPtr := cap.Sym.Ptr
			if !cap.Sym.Boxed {
				newCell := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", newCell, cap.Ty.Align()))
				curVal := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d",
					curVal, cap.Ty.IR, cap.Sym.Ptr, cap.Ty.Align()))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d",
					cap.Ty.IR, curVal, newCell, cap.Ty.Align()))
				e.updateSymbolInPlace(cap.Name, Symbol{Ptr: newCell, Ty: cap.Ty, Boxed: true, IsConst: cap.Sym.IsConst})
				cellPtr = newCell
			}
			slotReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d",
				slotReg, envIR, env, i))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cellPtr, slotReg))
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, epSlot))
	} else {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", epSlot))
	}

	closureTy := FuncType(closureParamTypes, retTy)
	if len(fe.Params) > 0 && fe.Params[len(fe.Params)-1].Rest {
		closureTy.FuncHasRest = true
	}
	return Value{Ref: hdr, Ty: closureTy}, nil
}

// --- closure call paths ---

// emitClosureCall calls a closure whose header pointer is stored in sym.Ptr.
func (e *Emitter) emitClosureCall(sym Symbol, args []ast.Expression, pos ast.Pos) (Value, error) {
	closureReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", closureReg, sym.Ptr))
	return e.emitClosureCallByPtr(closureReg, sym.Ty, args, pos)
}

// emitClosureCallByPtr calls a closure given the direct header pointer and its type.
func (e *Emitter) emitClosureCallByPtr(closurePtr string, ty Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Load function pointer from header[0].
	fpSlot := e.freshReg()
	fpVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, closurePtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fpVal, fpSlot))

	// Load env pointer from header[1].
	epSlot := e.freshReg()
	epVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, closurePtr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", epVal, epSlot))

	// regularCount excludes the rest slot itself (FuncHasRest, added
	// alongside this fix — TDD-00059/ADR-00151 — since a closure *value*'s
	// own Type previously had no way to tell "one array-typed parameter"
	// apart from "a rest parameter collecting N individual trailing call
	// arguments," the same distinction FuncSig.HasRest already gives a
	// named function's call sites).
	regularCount := len(ty.FuncParams)
	if ty.FuncHasRest {
		regularCount--
	}

	// Build arg list: env first, then actual args.
	argParts := []string{"ptr " + epVal}
	for i := 0; i < regularCount && i < len(args); i++ {
		arg := args[i]
		paramTy := ty.FuncParams[i]
		// A nullable-scalar closure parameter takes its boxed { i1, T }
		// aggregate (TDD-00064 Stage 3) — handled before the generic path so a
		// null literal boxes as absent rather than round-tripping through coerce.
		if isNullableScalar(paramTy) {
			argStr, err := e.emitNullableScalarArg(arg, paramTy)
			if err != nil {
				return Value{}, err
			}
			argParts = append(argParts, argStr)
			continue
		}
		val, err := e.emitExprWithObjectHint(arg, paramTy)
		if err != nil {
			return Value{}, err
		}
		if paramTy.Inferred && !isSafeNumericArg(val.Ty) {
			return Value{}, fmt.Errorf("%d:%d: parameter %d has no type annotation (defaults to number) but was called with a non-numeric argument here — add an explicit type annotation", arg.GetPos().Line, arg.GetPos().Col, i+1)
		}
		if paramTy.IsDynamic {
			// A constrained-union-typed closure parameter (TDD-00043) —
			// coerce has no notion of boxing, so this needs the same
			// explicit emitBoxValue call every other dynamic-typed target
			// already uses. Bare any/unknown can't reach here at all: a
			// closure's own declared param types are validated the same
			// way a named function's are, before this call site ever runs.
			if paramTy.UnionMembers != nil && !unionAllowsAssignmentFrom(paramTy, val.Ty) {
				return Value{}, fmt.Errorf("%d:%d: argument's type is not a member of parameter %d's declared union type", arg.GetPos().Line, arg.GetPos().Col, i+1)
			}
			var err error
			val, err = e.emitBoxValue(val)
			if err != nil {
				return Value{}, err
			}
		} else {
			val = e.coerce(val, paramTy)
		}
		// An array-typed parameter decomposes into two LLVM args (ptr, i64
		// len) at the call site — matches the (ptr, i64) callee ABI
		// emitClosureFunc's own parameter loop now expands an array
		// parameter into (ADR-00151/TDD-00059). val is already the real
		// {ptr,i64} aggregate (emitExprWithObjectHint/emitExpr's own
		// IsArray handling), so this only needs to split it, not build it.
		if paramTy.IsArray {
			ptrReg := e.freshReg()
			lenReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, val.Ref))
			e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, val.Ref))
			argParts = append(argParts, "ptr "+ptrReg, "i64 "+lenReg)
			continue
		}
		argParts = append(argParts, fmt.Sprintf("%s %s", val.Ty.IR, val.Ref))
	}
	// Pack rest args into a temporary heap array — identical shape to
	// emitCallToFuncSig's own rest-packing (emit_call.go) and
	// emitClassCall's (emit_classes.go).
	if ty.FuncHasRest {
		restArgs := args[regularCount:]
		restTy := ty.FuncParams[len(ty.FuncParams)-1]
		elemTy := TypeI64
		if restTy.ElemType != nil {
			elemTy = *restTy.ElemType
		}
		if len(restArgs) == 0 {
			argParts = append(argParts, "ptr null", "i64 0")
		} else {
			n := int64(len(restArgs))
			e.ensureMalloc()
			dataReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*int64(elemTy.Align())))
			for i, arg := range restArgs {
				val, err := e.emitExprWithObjectHint(arg, elemTy)
				if err != nil {
					return Value{}, err
				}
				val = e.coerce(val, elemTy)
				gepReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %d", gepReg, elemTy.IR, dataReg, i))
				e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val.Ref, gepReg, elemTy.Align()))
			}
			argParts = append(argParts, fmt.Sprintf("ptr %s", dataReg), fmt.Sprintf("i64 %d", n))
		}
	}

	// Build the LLVM function type string for the indirect call.
	// Format: retTy (ptr, argTy1, argTy2, ...)
	paramTyStrs := []string{"ptr"}
	for _, p := range ty.FuncParams {
		if p.IsArray {
			paramTyStrs = append(paramTyStrs, "ptr", "i64")
			continue
		}
		// A nullable-scalar parameter is passed as its { i1, T } aggregate
		// (TDD-00064 Stage 3), so the indirect-call function type must name that
		// storage shape, not the bare scalar.
		paramTyStrs = append(paramTyStrs, storageIR(p))
	}
	fnTypePart := "(" + strings.Join(paramTyStrs, ", ") + ")"

	retTy := ty.FuncRetType
	if retTy == nil || retTy.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnTypePart, fpVal, strings.Join(argParts, ", ")))
		return Value{Ty: TypeVoid}, nil
	}

	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", result, retTy.LLVMRetType(), fnTypePart, fpVal, strings.Join(argParts, ", ")))
	return Value{Ref: result, Ty: *retTy}, nil
}

// =============================================================================
// Callback helpers (used by HOF emission in emit_arrays.go)
// =============================================================================

// cbKind discriminates how to call a callback value.
type cbKind int

const (
	cbClosure cbKind = iota // closure header {funcPtr, envPtr} on heap
	cbNamed                 // top-level named function, called directly
)

// Callback holds everything needed to emit a callback invocation.
type Callback struct {
	kind   cbKind
	hdrPtr string  // register holding {ptr,ptr} closure header (cbClosure)
	ty     Type    // FuncType (cbClosure)
	name   string  // bare function name without @ (cbNamed)
	sig    FuncSig // (cbNamed)
}

func (cb Callback) paramTypes() []Type {
	if cb.kind == cbClosure {
		return cb.ty.FuncParams
	}
	return cb.sig.ParamTypes
}

func (cb Callback) retType() Type {
	if cb.kind == cbClosure {
		if cb.ty.FuncRetType != nil {
			return *cb.ty.FuncRetType
		}
		return TypeVoid
	}
	return cb.sig.RetType
}

func (cb Callback) arity() int { return len(cb.paramTypes()) }

// resolveCallback evaluates a callback argument (arrow function, closure var, or
// named function identifier) and returns a Callback descriptor.
func (e *Emitter) resolveCallback(arg ast.Expression) (Callback, error) {
	switch cb := arg.(type) {
	case *ast.ArrowFunction:
		v, err := e.emitArrowFunction(cb)
		if err != nil {
			return Callback{}, err
		}
		return Callback{kind: cbClosure, hdrPtr: v.Ref, ty: v.Ty}, nil
	case *ast.FunctionExpression:
		v, err := e.emitFunctionExpression(cb, nil)
		if err != nil {
			return Callback{}, err
		}
		return Callback{kind: cbClosure, hdrPtr: v.Ref, ty: v.Ty}, nil
	case *ast.Identifier:
		if mangled, sig, found := e.resolveFuncRef(cb.Name); found {
			return Callback{kind: cbNamed, name: mangled, sig: sig}, nil
		}
		if sym, found := e.lookup(cb.Name); found && sym.Ty.IsFunc {
			hdr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, sym.Ptr))
			return Callback{kind: cbClosure, hdrPtr: hdr, ty: sym.Ty}, nil
		}
		return Callback{}, fmt.Errorf("'%s' is not a callable", cb.Name)
	}
	return Callback{}, fmt.Errorf("callback must be an arrow function or function identifier")
}

// resolveCallbackWithHints is like resolveCallback but propagates element-type
// hints to untyped arrow function parameters.
func (e *Emitter) resolveCallbackWithHints(arg ast.Expression, hints []Type) (Callback, error) {
	if af, ok := arg.(*ast.ArrowFunction); ok {
		v, err := e.emitArrowFunctionWithHints(af, hints)
		if err != nil {
			return Callback{}, err
		}
		return Callback{kind: cbClosure, hdrPtr: v.Ref, ty: v.Ty}, nil
	}
	if fe, ok := arg.(*ast.FunctionExpression); ok {
		v, err := e.emitFunctionExpression(fe, hints)
		if err != nil {
			return Callback{}, err
		}
		return Callback{kind: cbClosure, hdrPtr: v.Ref, ty: v.Ty}, nil
	}
	return e.resolveCallback(arg)
}

// emitCBCall invokes callback cb with the given pre-evaluated arguments.
// Values in args are coerced to the callback's declared param types.
func (e *Emitter) emitCBCall(cb Callback, args []Value) (Value, error) {
	params := cb.paramTypes()
	retTy := cb.retType()

	// Coerce args to declared param types.
	coerced := make([]Value, len(args))
	for i, a := range args {
		if i < len(params) {
			coerced[i] = e.coerce(a, params[i])
		} else {
			coerced[i] = a
		}
	}

	switch cb.kind {
	case cbClosure:
		fpSlot := e.freshReg()
		fpVal := e.freshReg()
		epSlot := e.freshReg()
		epVal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 0", fpSlot, cb.hdrPtr))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fpVal, fpSlot))
		e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, ptr}, ptr %s, i32 0, i32 1", epSlot, cb.hdrPtr))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", epVal, epSlot))

		tyParts := []string{"ptr"}
		for _, p := range params {
			if p.IsArray {
				tyParts = append(tyParts, "ptr", "i64")
				continue
			}
			tyParts = append(tyParts, p.IR)
		}
		fnType := "(" + strings.Join(tyParts, ", ") + ")"

		argParts := []string{"ptr " + epVal}
		for i, v := range coerced {
			// An array-typed callback param decomposes into two call args
			// (ptr, i64 len) — v is already the real {ptr,i64} aggregate
			// (whatever built `args` upstream, e.g. a HOF's own element
			// value, already produced it in that shape); this only splits
			// it, matching emitClosureFunc's (ptr, i64) callee ABI for an
			// array parameter. Previously this branch used v.Ty.IR ("ptr",
			// the array-type marker) directly as if the aggregate were a
			// plain scalar pointer — invalid IR the moment a callback
			// actually took an array parameter (the array-methods "no
			// nested-array element as the callback's own parameter"
			// caveat). See ADR-00151/TDD-00059.
			if i < len(params) && params[i].IsArray {
				ptrReg := e.freshReg()
				lenReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, v.Ref))
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, v.Ref))
				argParts = append(argParts, "ptr "+ptrReg, "i64 "+lenReg)
				continue
			}
			ty := v.Ty.IR
			if i < len(params) {
				ty = params[i].IR
			}
			argParts = append(argParts, ty+" "+v.Ref)
		}
		argStr := strings.Join(argParts, ", ")

		if retTy.IR == "void" || retTy.IR == "" {
			e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fnType, fpVal, argStr))
			return Value{Ty: TypeVoid}, nil
		}
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", result, retTy.LLVMRetType(), fnType, fpVal, argStr))
		return Value{Ref: result, Ty: retTy}, nil

	case cbNamed:
		var argParts []string
		for i, v := range coerced {
			// Same array decomposition as the cbClosure branch above — a
			// plain top-level function called as a callback (e.g. passed
			// by name to .map()) needs it too, independent of the closure
			// ABI change: this branch also used to pass an array's raw
			// {ptr,i64} aggregate as if it were a single scalar pointer.
			if i < len(params) && params[i].IsArray {
				ptrReg := e.freshReg()
				lenReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", ptrReg, v.Ref))
				e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, v.Ref))
				argParts = append(argParts, "ptr "+ptrReg, "i64 "+lenReg)
				continue
			}
			ty := v.Ty.IR
			if i < len(params) {
				ty = params[i].IR
			}
			argParts = append(argParts, ty+" "+v.Ref)
		}
		argStr := strings.Join(argParts, ", ")
		if retTy.IR == "void" {
			e.emitInstr(fmt.Sprintf("call void @%s(%s)", cb.name, argStr))
			return Value{Ty: TypeVoid}, nil
		}
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", result, retTy.LLVMRetType(), cb.name, argStr))
		return Value{Ref: result, Ty: retTy}, nil
	}
	return Value{}, fmt.Errorf("unknown callback kind")
}

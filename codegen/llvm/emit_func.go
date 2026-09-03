// emit_func.go — function and closure emission: declarations, free-variable
// scanning, closure construction, and closure call paths.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"reflect"
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
	// Namespace-member context for the bare-sibling-reference fallback
	// (TDD-00148 Stage 4): a `X__kmlns_f` name marks this function as
	// namespace X's member. Restored by defer so nested function emission
	// (which passes back through here with a non-namespace name and clears
	// it) unwinds correctly even on error paths.
	savedCurNamespace := e.curNamespace
	e.curNamespace = ""
	if i := strings.LastIndex(llvmName, "__kmlns_"); i > 0 {
		// A nested namespace's flattened name restores its dots
		// (`A__kmlns_B__kmlns_f` → namespace "A.B" — TDD-00148 V3).
		e.curNamespace = strings.ReplaceAll(llvmName[:i], "__kmlns_", ".")
	}
	defer func() { e.curNamespace = savedCurNamespace }()

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
	// A nested function/closure body is not the constructor, even when lexically
	// inside one — a `readonly` write here is an error (TDD-00154), so clear the
	// ctor context for the duration of this body's emission.
	savedCurrentCtorClass := e.currentCtorClass
	e.currentCtorClass = ""
	// Eager-boxing capture set for this body (see hoistedCaptures): its own
	// locals/params captured by some nested closure, boxed at declaration.
	savedHoistedCaptures := e.hoistedCaptures
	savedWidened := e.widenedBindings
	if decl.Body != nil {
		paramNames := make([]string, len(decl.Params))
		for i, p := range decl.Params {
			paramNames[i] = p.Name
		}
		e.hoistedCaptures = capturedLocalNames(decl.Body.Body, paramNames)
		e.widenedBindings = e.crossTypeWidenedBindings(decl.Body.Body)
	} else {
		e.hoistedCaptures = nil
		e.widenedBindings = nil
	}
	defer func() {
		e.breakStack = savedBreakStack
		e.continueStack = savedContinueStack
		e.namedLabelStack = savedNamedLabelStack
		e.currentGenerator = savedCurrentGenerator
		e.currentCtorClass = savedCurrentCtorClass
		e.hoistedCaptures = savedHoistedCaptures
		e.widenedBindings = savedWidened
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
		if err := e.pushNestedFuncScope(decl.Params, decl.Body.Body); err != nil {
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
	// A recognized prototype constructor (TDD-00155 Stage 4, `-compat=js`)
	// takes a hidden boxed `this` first parameter — its body's `this.x = v`
	// assignments are dynamic member writes on the receiver bag.
	if e.compatJS() && e.jsProtoCtor[llvmName] {
		llvmParams = append(llvmParams, "i64 %p_this")
		thisPtr := "%v_this"
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", thisPtr))
		e.emitInstr(fmt.Sprintf("store i64 %%p_this, ptr %s, align 8", thisPtr))
		e.define("this", Symbol{Ptr: thisPtr, Ty: TypeAny})
	}
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
			// Object-reference model (TDD-00127): the incoming `ptr` argument is
			// the caller's {data, len} header pointer; the `i64 len` argument is
			// redundant (length lives in the header) but kept for ABI stability.
			// A destructured array parameter (`[a, b]: number[]`) unpacks
			// straight from the header's data field + the passed length — no
			// named binding, since nothing in the body can reference it.
			if p.ArrayPattern != nil {
				dataPtrReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%p_%s_ptr, align 8", dataPtrReg, p.Name))
				if err := e.unpackArrayPatternInto(dataPtrReg, "%p_"+p.Name+"_len", *pty.ElemType, p.ArrayPattern); err != nil {
					return err
				}
				continue
			}
			e.bindArrayParam(p.Name, pty)
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
			if e.hoistedCaptures[p.Name] {
				// Captured by a nested closure: box eagerly at entry (which
				// dominates the whole body) instead of at the capture site.
				e.boxHoistedCapture(p.Name, pty, "%p_"+p.Name, false, true)
			} else {
				e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
			}
		}
	}

	// A body referencing `arguments` gets a synthesized array of the declared
	// parameters bound under that name (ADR-00387). Skipped for a may-suspend
	// task body, whose params live in %__taskargs rather than plain allocas.
	if !taskBody {
		if err := e.synthesizeArgumentsObject(decl, sig); err != nil {
			return err
		}
	}

	// Emit body statements.
	e.emitSafepoint() // function-entry preempt check (TDD-00143 Stage 2)
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
// synthesizeArgumentsObject binds a local `arguments` array, built from the
// declared parameters, when the function body references `arguments` (ADR-00387).
//
// V1 scope: a regular (non-task) named function body whose parameters are all
// the same simple (scalar/string) type, with no rest or destructured parameter.
// Since this compiler has static arity, the synthesized `arguments` reflects
// exactly the declared parameters — matching real JS for a call that passes
// every declared argument. It does not grow with extra untyped arguments (there
// is no variadic beyond an explicit `...rest`, which callers use directly), and
// it is unavailable in an arrow function (real JS arrows have no own
// `arguments`; here the reference simply stays an undefined-variable error).
func (e *Emitter) synthesizeArgumentsObject(decl *ast.FunctionDeclaration, sig FuncSig) error {
	if decl.Body == nil {
		return nil
	}
	// A parameter named `arguments` shadows the object — the user binding wins.
	for _, p := range decl.Params {
		if p.Name == "arguments" {
			return nil
		}
	}
	// Only synthesize when the body actually references `arguments`, so an
	// ordinary function pays nothing.
	bound := map[string]bool{}
	addParamBoundNames(bound, decl.Params)
	refs := map[string]bool{}
	scanStmtsFV(decl.Body.Body, bound, refs)
	if !refs["arguments"] {
		return nil
	}
	elemTy := TypeI64 // a zero-parameter `arguments` is an empty number[]
	if len(decl.Params) > 0 {
		for _, p := range decl.Params {
			if p.Rest {
				return fmt.Errorf("%d:%d: `arguments` is not supported in a function with a rest parameter — iterate the `...%s` parameter directly", decl.GetPos().Line, decl.GetPos().Col, p.Name)
			}
			if p.ArrayPattern != nil || p.ObjectPattern != nil {
				return fmt.Errorf("%d:%d: `arguments` is not supported in a function with a destructured parameter", decl.GetPos().Line, decl.GetPos().Col)
			}
		}
		elemTy = sig.ParamTypes[0]
		for i := 1; i < len(sig.ParamTypes); i++ {
			if sig.ParamTypes[i].IR != elemTy.IR || sig.ParamTypes[i].IsArray != elemTy.IsArray {
				return fmt.Errorf("%d:%d: `arguments` is only supported when every parameter shares one type (this compiler's arrays are homogeneous) — use a `...rest` parameter for mixed argument types", decl.GetPos().Line, decl.GetPos().Col)
			}
		}
		if elemTy.IsArray || isNullableScalar(elemTy) || elemTy.IsDynamic {
			return fmt.Errorf("%d:%d: `arguments` is not yet supported for array, nullable, or any/unknown parameter types", decl.GetPos().Line, decl.GetPos().Col)
		}
	}
	// Build the array from the parameter values, reusing the array-literal data
	// path (each element loads its parameter, already in scope), then bind the
	// two-alloca array Symbol under `arguments`.
	elems := make([]ast.Expression, len(decl.Params))
	for i, p := range decl.Params {
		elems[i] = ast.NewIdentifier(p.Name, decl.GetPos())
	}
	dataReg, n, err := e.emitArrayLiteralData(ast.NewArrayLiteral(elems, decl.GetPos()), elemTy)
	if err != nil {
		return err
	}
	slot := e.newArrayHeaderSlot(dataReg, fmt.Sprintf("%d", n))
	e.define("arguments", Symbol{Ptr: slot, Ty: ArrayOf(elemTy)})
	return nil
}

// Nested function declarations (TDD-00057)
// =============================================================================

// nestedFuncEntry is one pre-registered nested function declaration's
// mangled LLVM name and signature. Gen is non-nil for a nested *generator*
// declaration (TDD-00094): its GeneratorInfo replaces the ordinary Sig, and the
// call site constructs a generator instance rather than making a plain call.
type nestedFuncEntry struct {
	Mangled string
	Sig     FuncSig
	Gen     *GeneratorInfo
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
	// capturing (TDD-00129 Stage 1) holds the nested declarations in this body
	// that reference an enclosing function's local/parameter and so must be
	// emitted as a closure *value* bound to their own name, not a mangled
	// direct-call symbol. They are deliberately absent from byName/byDecl, so
	// resolveFuncRef misses them and a call falls through to the closure-value
	// lookup — and a call *before* the declaration cleanly errors as an
	// undefined function/closure (Stage 1 supports use at or after the
	// declaration point only).
	capturing map[*ast.FunctionDeclaration]bool
}

// pushNestedFuncScope pre-scans body (direct statements only) for nested
// function declarations, registers each under a fresh mangled name, and
// pushes the resulting scope onto e.nestedFuncScopes. Every push must be
// matched by a popNestedFuncScope once the body's statements have been
// emitted (skipped on an error return, same as every other per-function
// emitter-state restore in this file — an error aborts the whole
// compilation, so nothing downstream ever observes the unpopped frame).
func (e *Emitter) pushNestedFuncScope(params []ast.Param, body []ast.Statement) error {
	// TDD-00129 Stage 1: build this body's capturable-binding frame (params +
	// var/let/const/destructured locals, never function/class names) and push
	// it before classifying, so a nested declaration referencing one of this
	// body's own locals — not only an outer body's — is seen as capturing.
	frame := map[string]bool{}
	for _, p := range params {
		frame[p.Name] = true
	}
	collectCapturableNames(body, frame)
	e.enclosingCapturables = append(e.enclosingCapturables, frame)

	// Pushed before the scan below runs (and popped on an early-error
	// return, to leave e.nestedFuncScopes exactly as this function found it
	// — every caller relies on that on error) rather than only once fully
	// built, so an earlier-in-this-same-body sibling is already resolvable
	// via resolveFuncRef while a later sibling's own signature is still
	// being computed — the same immediate-write-as-you-go shape
	// registerFunctions (emitter.go) uses for the identical reason. scope's
	// maps are reference types, so mutating the local variable below and
	// reading back through e.nestedFuncScopes see the same underlying data.
	scope := nestedFuncScope{
		byName:    map[string]nestedFuncEntry{},
		byDecl:    map[*ast.FunctionDeclaration]nestedFuncEntry{},
		capturing: map[*ast.FunctionDeclaration]bool{},
	}
	e.nestedFuncScopes = append(e.nestedFuncScopes, scope)

	// popFrames undoes both parallel pushes on an early-error return, keeping
	// enclosingCapturables and nestedFuncScopes balanced.
	popFrames := func() {
		e.nestedFuncScopes = e.nestedFuncScopes[:len(e.nestedFuncScopes)-1]
		e.enclosingCapturables = e.enclosingCapturables[:len(e.enclosingCapturables)-1]
	}

	var unannotated []*ast.FunctionDeclaration
	for _, stmt := range body {
		fd, ok := stmt.(*ast.FunctionDeclaration)
		if !ok {
			continue
		}
		if len(fd.TypeParams) > 0 {
			popFrames()
			return fmt.Errorf("%d:%d: a generic nested function declaration is not supported", fd.GetPos().Line, fd.GetPos().Col)
		}
		if _, dup := scope.byName[fd.Name]; dup {
			popFrames()
			return fmt.Errorf("%d:%d: '%s' is already declared in this scope", fd.GetPos().Line, fd.GetPos().Col, fd.Name)
		}
		// A nested generator (TDD-00094) registers its GeneratorInfo instead of an
		// ordinary Sig; the `g()` call site (lookupGenerator) then constructs an
		// instance. A capturing nested generator still needs its own __env work
		// (out of TDD-00129 Stage 1's scope — see its Stage 3 note), so generators
		// stay on the existing direct-registration path here regardless of capture.
		if fd.IsGenerator {
			e.nestedFuncCtr++
			info, err := e.buildGeneratorSig(fd)
			if err != nil {
				popFrames()
				return err
			}
			entry := nestedFuncEntry{
				Mangled: fmt.Sprintf("%s__nested%d", fd.Name, e.nestedFuncCtr),
				Gen:     info,
			}
			scope.byName[fd.Name] = entry
			scope.byDecl[fd] = entry
			continue
		}
		// TDD-00129 Stage 1: a nested declaration that closes over an enclosing
		// function's local/parameter is emitted as a closure value (at its
		// declaration point, by emitStmt) rather than a mangled direct-call
		// symbol — so it is recorded in `capturing` and deliberately left out of
		// byName/byDecl. Keeping it out of resolveFuncRef is what makes a call
		// fall through to the closure-value lookup, and makes a call before the
		// declaration a clean "undefined function or closure" error.
		// A for-loop variable capture is rejected before the ordinary
		// capturing/non-capturing split — the loop variable isn't an
		// enclosingCapturables binding (it's declared in the for-init, not a
		// body statement), so nestedDeclCaptures would misclassify it as
		// non-capturing and emit a broken direct call.
		if v, ok := e.nestedDeclCapturesForLoopVar(fd); ok {
			popFrames()
			return fmt.Errorf("%d:%d: a nested function declaration capturing a for-loop variable ('%s') is not supported — the loop variable is a single per-loop cell, so a closure over it would corrupt the loop; copy it to a `const` inside the loop body and capture that instead", fd.GetPos().Line, fd.GetPos().Col, v)
		}
		if e.nestedDeclCaptures(fd) {
			scope.capturing[fd] = true
			continue
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

// emitCapturingNestedFunc emits a capturing nested function declaration
// (TDD-00129 Stage 1) as a closure *value* bound to its own name in the current
// scope, by treating it as the equivalent named function expression. The
// function-expression path already provides everything the capture needs:
// gatherCaptures/env-struct (with by-reference mutation via boxed cells), the
// ADR-00178 self-capture for recursion, and the closure-value representation so
// the function can be returned, stored, or passed as a callback. The name is
// defined *after* emission (use at or after the declaration point — Stage 1);
// recursion inside the body resolves through the self-capture, not this slot.
func (e *Emitter) emitCapturingNestedFunc(fd *ast.FunctionDeclaration) error {
	fe := ast.NewFunctionExpression(fd.Name, fd.Params, fd.ReturnType, fd.Body, fd.IsAsync, fd.GetPos())
	val, err := e.emitFunctionExpression(fe, nil)
	if err != nil {
		return err
	}
	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
	e.define(fd.Name, Symbol{Ptr: ptrName, Ty: val.Ty})
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
	return nil
}

// popNestedFuncScope removes the most recently pushed nestedFuncScope frame,
// and the parallel enclosingCapturables frame (TDD-00129 Stage 1).
func (e *Emitter) popNestedFuncScope() {
	e.nestedFuncScopes = e.nestedFuncScopes[:len(e.nestedFuncScopes)-1]
	e.enclosingCapturables = e.enclosingCapturables[:len(e.enclosingCapturables)-1]
}

// collectCapturableNames adds every capturable binding a body directly declares
// — var/let/const and their destructured leaf names — to out. Function and
// class declarations are intentionally excluded: they are resolved by name
// (nested-func scopes / e.funcs / class table), never closed over as variables,
// so treating them as captures would wrongly route a helper that merely calls a
// sibling function through the closure path. Only the immediate statement list
// is walked; a binding inside a further block isn't in scope for a nested
// function declared directly in this body anyway (TDD-00129 Stage 1).
func collectCapturableNames(body []ast.Statement, out map[string]bool) {
	for _, stmt := range body {
		switch s := stmt.(type) {
		case *ast.VarDeclaration:
			out[s.Name] = true
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				out[d.Name] = true
			}
		case *ast.ArrayDestructuring:
			collectArrayPatternNames(s.Elems, out)
		case *ast.ObjectDestructuring:
			collectObjectPatternNames(s.Props, out)
		}
	}
}

// nestedDeclCaptures reports whether nested function declaration fd references a
// binding of some enclosing function scope — i.e. a name present in any
// enclosingCapturables frame — after excluding fd's own parameters and its own
// name (self-recursion is not a capture). Purely syntactic, run at pre-scan
// before the enclosing scope's params/locals are define()d. Over-approximation
// is safe: a declaration wrongly flagged capturing is emitted as a closure whose
// precise capture scan (emitFunctionExpression, via e.lookup) simply finds no
// enclosing-scope symbol and produces a zero-capture closure — correct, only
// slightly less efficient than a direct call. Under-approximation cannot happen
// for an in-scope local, since every such binding is recorded in a frame.
func (e *Emitter) nestedDeclCaptures(fd *ast.FunctionDeclaration) bool {
	if fd.Body == nil {
		return false
	}
	bound := map[string]bool{fd.Name: true}
	addParamBoundNames(bound, fd.Params)
	refs := map[string]bool{}
	scanStmtsFV(fd.Body.Body, bound, refs)
	for name := range refs {
		for _, frame := range e.enclosingCapturables {
			if frame[name] {
				return true
			}
		}
	}
	return false
}

// nestedDeclCapturesForLoopVar reports whether nested declaration fd closes over
// a C-style for-loop variable whose body is currently being emitted (TDD-00152).
// Such a capture is rejected: the loop variable lives in one alloca reused every
// iteration, so a closure over it would share the counter cell and corrupt the
// loop (the same per-iteration-binding limitation arrows hit).
func (e *Emitter) nestedDeclCapturesForLoopVar(fd *ast.FunctionDeclaration) (string, bool) {
	if fd.Body == nil || len(e.activeForLoopVars) == 0 {
		return "", false
	}
	bound := map[string]bool{fd.Name: true}
	addParamBoundNames(bound, fd.Params)
	refs := map[string]bool{}
	scanStmtsFV(fd.Body.Body, bound, refs)
	for name := range refs {
		for _, frame := range e.activeForLoopVars {
			if frame[name] {
				return name, true
			}
		}
	}
	return "", false
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
	// Bare sibling reference inside a namespace member function (TDD-00148
	// Stage 4): retry under the mangled sibling name. Last, so every local/
	// nested/top-level binding of the bare name shadows the sibling.
	if m := e.nsSibling(name); m != "" && m != name {
		if sig, ok := e.funcs[m]; ok {
			return m, sig, true
		}
	}
	return "", FuncSig{}, false
}

// lookupGenerator resolves a generator by name, checking nested-generator scopes
// innermost-first (a nested `function*` — TDD-00094) before the flat top-level
// e.generators map, mirroring resolveFuncRef for ordinary functions.
func (e *Emitter) lookupGenerator(name string) (*GeneratorInfo, bool) {
	for i := len(e.nestedFuncScopes) - 1; i >= 0; i-- {
		if entry, ok := e.nestedFuncScopes[i].byName[name]; ok {
			if entry.Gen != nil {
				return entry.Gen, true
			}
			return nil, false // shadowed by a nested non-generator function
		}
	}
	if info, ok := e.generators[name]; ok {
		return info, true
	}
	return nil, false
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

// promoteCaptureToCell allocates a fresh heap cell for a variable being
// captured by a closure for the first time, seeds it, retargets the enclosing
// scope's symbol at the cell (so the outer scope and every capturing closure
// share one mutable cell by pointer), and returns the cell pointer.
//
// Seeding normally copies the variable's current value out of its slot. But
// when the variable is still being initialized — a closure inside the
// variable's own initializer captured it, the self-reference idiom
// `const s = f(() => use(s))` — its slot holds no value yet, so loading it
// would read uninitialized memory (undefined behavior the optimizer exploits at
// -O2, e.g. treating a later non-null value as null). In that case the cell is
// seeded with the type's deterministic default instead; the real value is
// written into this same shared cell afterward by the variable declaration's
// own store, which re-resolves to the cell (emit_exprs_vardecl.go). A closure
// invoked only after the declaration completes therefore observes the assigned
// value; one invoked synchronously during the initializer (genuine TDZ misuse)
// observes the default rather than garbage.
func (e *Emitter) promoteCaptureToCell(name string, ty Type, srcPtr string, isConst bool) string {
	newCell := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", newCell, ty.Align()))
	if e.varsBeingInitialized[name] {
		// Seed with the type's deterministic default (a body-block store, since
		// newCell is a body register — emitVarSlotDefault targets the entry block).
		if ty.IsDynamic {
			e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, newCell))
		} else {
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, ty.zeroLiteral(), newCell, ty.Align()))
		}
	} else {
		curVal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curVal, ty.IR, srcPtr, ty.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, curVal, newCell, ty.Align()))
	}
	e.updateSymbolInPlace(name, Symbol{Ptr: newCell, Ty: ty, Boxed: true, IsConst: isConst})
	return newCell
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
	case *ast.YieldExpression:
		// `yield e` / `yield* e` — the operand is a free-variable position (a bare
		// `yield` has a nil argument). Needed so a nested generator's captures are
		// detected (TDD-00094).
		scanExprFV(x.Argument, bound, result)
	case *ast.AwaitExpression:
		scanExprFV(x.Argument, bound, result)
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
	case *ast.NewMapExpression:
		scanExprFV(x.Init, bound, result)
	case *ast.NewWeakRefExpression:
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
	case *ast.NewTypedArrayExpression:
		// `new Int32Array(buf)` inside a closure body: buf is a real free
		// variable (was silently unscanned, later failing as "undefined
		// variable" at emission — found via a SharedArrayBuffer view in a
		// worker handler, TDD-00099).
		scanExprFV(x.Arg, bound, result)
		if x.ByteOffset != nil {
			scanExprFV(x.ByteOffset, bound, result)
		}
		if x.Length != nil {
			scanExprFV(x.Length, bound, result)
		}
	case *ast.NewArrayBufferExpression:
		scanExprFV(x.ByteLength, bound, result)
	case *ast.NewBlobExpression:
		if x.Parts != nil {
			scanExprFV(x.Parts, bound, result)
		}
		if x.Options != nil {
			scanExprFV(x.Options, bound, result)
		}
	case *ast.NewDataViewExpression:
		scanExprFV(x.Buffer, bound, result)
		scanExprFV(x.ByteOffset, bound, result)
		scanExprFV(x.ByteLength, bound, result)
	case *ast.NewNodeStreamExpression:
		if x.Options != nil {
			scanExprFV(x.Options, bound, result)
		}
	case *ast.NewCompressionStreamExpression:
		if x.Format != nil {
			scanExprFV(x.Format, bound, result)
		}
	case *ast.NewTransformStreamExpression:
		if x.Transformer != nil {
			scanExprFV(x.Transformer, bound, result)
		}
		if x.WritableStrategy != nil {
			scanExprFV(x.WritableStrategy, bound, result)
		}
		if x.ReadableStrategy != nil {
			scanExprFV(x.ReadableStrategy, bound, result)
		}
	case *ast.NewWritableStreamExpression:
		if x.Sink != nil {
			scanExprFV(x.Sink, bound, result)
		}
		if x.Strategy != nil {
			scanExprFV(x.Strategy, bound, result)
		}
	case *ast.NewReadableStreamExpression:
		// The underlying source / strategy object literals hold callbacks
		// whose free variables are real captures (TDD-00097 Stage 1).
		if x.Source != nil {
			scanExprFV(x.Source, bound, result)
		}
		if x.Strategy != nil {
			scanExprFV(x.Strategy, bound, result)
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
		case *ast.ForInStatement:
			scanExprFV(s.Object, local, result)
			inner := make(map[string]bool, len(local)+1)
			for k, v := range local {
				inner[k] = v
			}
			inner[s.VarName] = true
			if s.Body != nil {
				scanStmtsFV(s.Body.Body, inner, result)
			}
		case *ast.DoWhileStatement:
			if s.Body != nil {
				scanStmtsFV(s.Body.Body, local, result)
			}
			scanExprFV(s.Test, local, result)
		case *ast.LabeledStatement:
			if s.Body != nil {
				scanStmtsFV([]ast.Statement{s.Body}, local, result)
			}
		case *ast.ThrowStatement:
			scanExprFV(s.Argument, local, result)
		case *ast.TryStatement:
			if s.Body != nil {
				scanStmtsFV(s.Body.Body, local, result)
			}
			if s.Catch != nil {
				inner := make(map[string]bool, len(local)+1)
				for k, v := range local {
					inner[k] = v
				}
				if s.Catch.Param != "" {
					inner[s.Catch.Param] = true
				}
				for _, p := range s.Catch.ObjectPattern {
					inner[p.Local] = true
				}
				if s.Catch.Body != nil {
					scanStmtsFV(s.Catch.Body.Body, inner, result)
				}
			}
			if s.Finally != nil {
				scanStmtsFV(s.Finally.Body, local, result)
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

// --- captured-local pre-scan (eager boxing) ---
//
// capScanExpr / capScanStmts mirror scanExprFV / scanStmtsFV EXACTLY, with one
// deliberate difference: at each closure boundary (an ArrowFunction or
// FunctionExpression) the bound set is RESET to that closure's own params only,
// instead of being inherited from the enclosing bound set. With the reset, a
// closure's reference to an enclosing-function local surfaces in `result` as a
// free variable — precisely the set of variables some nested closure captures.
// Non-closure code still binds locals as it walks (so a plain, non-closure use
// of a local is NOT collected). Used by capturedLocalNames to decide which of a
// function's own locals/params to heap-box eagerly at their declaration.
//
// The original scanExprFV/scanStmtsFV must keep their accumulate-bound behavior
// (gatherCaptures depends on it), so these are separate copies rather than a
// shared, parameterized traversal.
func capScanExpr(expr ast.Expression, bound map[string]bool, result map[string]bool) {
	if expr == nil {
		return
	}
	switch x := expr.(type) {
	case *ast.Identifier:
		if !bound[x.Name] {
			result[x.Name] = true
		}
	case *ast.BinaryExpression:
		capScanExpr(x.Left, bound, result)
		capScanExpr(x.Right, bound, result)
	case *ast.UnaryExpression:
		capScanExpr(x.Arg, bound, result)
	case *ast.YieldExpression:
		capScanExpr(x.Argument, bound, result)
	case *ast.AwaitExpression:
		capScanExpr(x.Argument, bound, result)
	case *ast.UpdateExpression:
		capScanExpr(x.Arg, bound, result)
	case *ast.AssignmentExpression:
		capScanExpr(x.Left, bound, result)
		capScanExpr(x.Right, bound, result)
	case *ast.CallExpression:
		capScanExpr(x.Callee, bound, result)
		for _, a := range x.Args {
			capScanExpr(a, bound, result)
		}
	case *ast.MemberExpression:
		capScanExpr(x.Object, bound, result)
	case *ast.IndexExpression:
		capScanExpr(x.Object, bound, result)
		capScanExpr(x.Index, bound, result)
	case *ast.ArrayLiteral:
		for _, el := range x.Elements {
			capScanExpr(el, bound, result)
		}
	case *ast.ObjectLiteral:
		for _, p := range x.Properties {
			capScanExpr(p.Value, bound, result)
		}
	case *ast.SpreadElement:
		capScanExpr(x.Arg, bound, result)
	case *ast.NewArrayExpression:
		capScanExpr(x.Size, bound, result)
	case *ast.NewSetExpression:
		capScanExpr(x.Init, bound, result)
	case *ast.NewMapExpression:
		capScanExpr(x.Init, bound, result)
	case *ast.NewWeakRefExpression:
		capScanExpr(x.Init, bound, result)
	case *ast.TemplateLiteral:
		for _, ex := range x.Exprs {
			capScanExpr(ex, bound, result)
		}
	case *ast.TaggedTemplateExpression:
		capScanExpr(x.Tag, bound, result)
		for _, ex := range x.Exprs {
			capScanExpr(ex, bound, result)
		}
	case *ast.ConditionalExpression:
		capScanExpr(x.Test, bound, result)
		capScanExpr(x.Consequent, bound, result)
		capScanExpr(x.Alternate, bound, result)
	case *ast.SequenceExpression:
		for _, sub := range x.Exprs {
			capScanExpr(sub, bound, result)
		}
	case *ast.NewTypedArrayExpression:
		capScanExpr(x.Arg, bound, result)
		if x.ByteOffset != nil {
			capScanExpr(x.ByteOffset, bound, result)
		}
		if x.Length != nil {
			capScanExpr(x.Length, bound, result)
		}
	case *ast.NewArrayBufferExpression:
		capScanExpr(x.ByteLength, bound, result)
	case *ast.NewBlobExpression:
		if x.Parts != nil {
			capScanExpr(x.Parts, bound, result)
		}
		if x.Options != nil {
			capScanExpr(x.Options, bound, result)
		}
	case *ast.NewDataViewExpression:
		capScanExpr(x.Buffer, bound, result)
		capScanExpr(x.ByteOffset, bound, result)
		capScanExpr(x.ByteLength, bound, result)
	case *ast.NewNodeStreamExpression:
		if x.Options != nil {
			capScanExpr(x.Options, bound, result)
		}
	case *ast.NewCompressionStreamExpression:
		if x.Format != nil {
			capScanExpr(x.Format, bound, result)
		}
	case *ast.NewTransformStreamExpression:
		if x.Transformer != nil {
			capScanExpr(x.Transformer, bound, result)
		}
		if x.WritableStrategy != nil {
			capScanExpr(x.WritableStrategy, bound, result)
		}
		if x.ReadableStrategy != nil {
			capScanExpr(x.ReadableStrategy, bound, result)
		}
	case *ast.NewWritableStreamExpression:
		if x.Sink != nil {
			capScanExpr(x.Sink, bound, result)
		}
		if x.Strategy != nil {
			capScanExpr(x.Strategy, bound, result)
		}
	case *ast.NewReadableStreamExpression:
		if x.Source != nil {
			capScanExpr(x.Source, bound, result)
		}
		if x.Strategy != nil {
			capScanExpr(x.Strategy, bound, result)
		}
	case *ast.ArrowFunction:
		// Closure boundary: RESET bound to this closure's own params only.
		innerBound := make(map[string]bool, len(x.Params))
		addParamBoundNames(innerBound, x.Params)
		if x.Body != nil {
			capScanExpr(x.Body, innerBound, result)
		}
		if x.Block != nil {
			capScanStmts(x.Block.Body, innerBound, result)
		}
	case *ast.FunctionExpression:
		// Closure boundary: RESET bound to this closure's own params only.
		innerBound := make(map[string]bool, len(x.Params))
		addParamBoundNames(innerBound, x.Params)
		capScanStmts(x.Body.Body, innerBound, result)
	}
}

func capScanStmts(stmts []ast.Statement, bound map[string]bool, result map[string]bool) {
	local := make(map[string]bool, len(bound))
	for k, v := range bound {
		local[k] = v
	}
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.VarDeclaration:
			if s.Init != nil {
				capScanExpr(s.Init, local, result)
			}
			local[s.Name] = true
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				if d.Init != nil {
					capScanExpr(d.Init, local, result)
				}
				local[d.Name] = true
			}
		case *ast.ExpressionStatement:
			capScanExpr(s.Expr, local, result)
		case *ast.ReturnStatement:
			if s.Value != nil {
				capScanExpr(s.Value, local, result)
			}
		case *ast.IfStatement:
			capScanExpr(s.Test, local, result)
			if s.Consequent != nil {
				capScanStmts(s.Consequent.Body, local, result)
			}
			if s.Alternate != nil {
				capScanStmts([]ast.Statement{s.Alternate}, local, result)
			}
		case *ast.ForStatement:
			inner := make(map[string]bool, len(local))
			for k, v := range local {
				inner[k] = v
			}
			if s.Init != nil {
				if vd, ok := s.Init.(*ast.VarDeclaration); ok {
					if vd.Init != nil {
						capScanExpr(vd.Init, inner, result)
					}
					inner[vd.Name] = true
				} else if vdl, ok := s.Init.(*ast.VarDeclarationList); ok {
					for _, d := range vdl.Decls {
						if d.Init != nil {
							capScanExpr(d.Init, inner, result)
						}
						inner[d.Name] = true
					}
				} else if es, ok := s.Init.(*ast.ExpressionStatement); ok {
					capScanExpr(es.Expr, inner, result)
				}
			}
			if s.Test != nil {
				capScanExpr(s.Test, inner, result)
			}
			for _, upd := range s.Update {
				capScanExpr(upd, inner, result)
			}
			if s.Body != nil {
				capScanStmts(s.Body.Body, inner, result)
			}
		case *ast.WhileStatement:
			capScanExpr(s.Test, local, result)
			if s.Body != nil {
				capScanStmts(s.Body.Body, local, result)
			}
		case *ast.BlockStatement:
			capScanStmts(s.Body, local, result)
		case *ast.ForOfStatement:
			capScanExpr(s.Iterable, local, result)
			inner := make(map[string]bool, len(local)+1)
			for k, v := range local {
				inner[k] = v
			}
			if s.ArrayPattern != nil {
				collectArrayPatternNames(s.ArrayPattern, inner)
			} else if s.ObjectPattern != nil {
				collectObjectPatternNames(s.ObjectPattern, inner)
			} else {
				inner[s.VarName] = true
			}
			if s.Body != nil {
				capScanStmts(s.Body.Body, inner, result)
			}
		case *ast.ForInStatement:
			capScanExpr(s.Object, local, result)
			inner := make(map[string]bool, len(local)+1)
			for k, v := range local {
				inner[k] = v
			}
			inner[s.VarName] = true
			if s.Body != nil {
				capScanStmts(s.Body.Body, inner, result)
			}
		case *ast.DoWhileStatement:
			if s.Body != nil {
				capScanStmts(s.Body.Body, local, result)
			}
			capScanExpr(s.Test, local, result)
		case *ast.LabeledStatement:
			if s.Body != nil {
				capScanStmts([]ast.Statement{s.Body}, local, result)
			}
		case *ast.ThrowStatement:
			capScanExpr(s.Argument, local, result)
		case *ast.TryStatement:
			if s.Body != nil {
				capScanStmts(s.Body.Body, local, result)
			}
			if s.Catch != nil {
				inner := make(map[string]bool, len(local)+1)
				for k, v := range local {
					inner[k] = v
				}
				if s.Catch.Param != "" {
					inner[s.Catch.Param] = true
				}
				for _, p := range s.Catch.ObjectPattern {
					inner[p.Local] = true
				}
				if s.Catch.Body != nil {
					capScanStmts(s.Catch.Body.Body, inner, result)
				}
			}
			if s.Finally != nil {
				capScanStmts(s.Finally.Body, local, result)
			}
		case *ast.SwitchStatement:
			capScanExpr(s.Discriminant, local, result)
			for _, c := range s.Cases {
				if c.Test != nil {
					capScanExpr(c.Test, local, result)
				}
				capScanStmts(c.Body, local, result)
			}
		case *ast.ArrayDestructuring:
			capScanExpr(s.Init, local, result)
			for _, elem := range s.Elems {
				if elem.Default != nil {
					capScanExpr(elem.Default, local, result)
				}
				if elem.Name != "" {
					local[elem.Name] = true
				}
			}
		case *ast.ObjectDestructuring:
			capScanExpr(s.Init, local, result)
			for _, prop := range s.Props {
				if prop.Default != nil {
					capScanExpr(prop.Default, local, result)
				}
				local[prop.Local] = true
			}
		}
	}
}

// capturedLocalNames returns every name that some nested closure inside body
// captures free from the enclosing (this) function's scope — the set of this
// function's own locals/params that must be heap-boxed eagerly at their
// declaration point (see hoistedCaptures / boxHoistedCapture). paramNames seed
// the bound set so a non-closure reference to a param is not itself collected;
// a name is intersected with what's actually declared here implicitly, at the
// declaration/param site (only a name being declared here is boxed).
func capturedLocalNames(body []ast.Statement, paramNames []string) map[string]bool {
	bound := make(map[string]bool, len(paramNames))
	for _, p := range paramNames {
		bound[p] = true
	}
	result := make(map[string]bool)
	capScanStmts(body, bound, result)
	return result
}

// crossTypeWidenedBindings (compat=js only) returns the untyped scalar bindings
// in one scope's body that are assigned two or more distinct scalar kinds
// (number / string / boolean) across their lifetime — a "dynamic variable" in
// the JS sense (`let x = 5; x = 'hi'`). Under -compat=js such a binding must be
// backed by the any-box (NaN-boxed { i8, i64 }) from its declaration, since a
// fixed scalar slot can't hold a later different-kinded value without emitting
// invalid IR. Strict rejects this instead (ADR-00651); this widening is the
// -compat=js counterpart (TDD-00162). Only *untyped* var/let are eligible — an
// explicit annotation is a contract kept even under -compat=js — and nested
// function/arrow bodies are separate scopes scanned by their own pass, so this
// walker does not descend into them (a rare closure-reassignment to a different
// kind stays a clean rejection, a documented V1 edge).
func (e *Emitter) crossTypeWidenedBindings(body []ast.Statement) map[string]bool {
	if !e.compatJS() {
		return nil
	}
	untyped := map[string]bool{}
	kinds := map[string]map[string]bool{}
	note := func(name string, t Type) {
		if !untyped[name] {
			return
		}
		if k := scalarTypeKind(t); k != "" {
			if kinds[name] == nil {
				kinds[name] = map[string]bool{}
			}
			kinds[name][k] = true
		}
	}
	declare := func(v *ast.VarDeclaration) {
		if v.Kind == "const" || v.TypeAnnot != nil {
			return
		}
		untyped[v.Name] = true
		if v.Init != nil {
			note(v.Name, e.inferExprType(v.Init))
		}
	}
	var walkExpr func(ast.Expression)
	walkExpr = func(expr ast.Expression) {
		as, ok := expr.(*ast.AssignmentExpression)
		if !ok {
			return
		}
		if id, ok := as.Left.(*ast.Identifier); ok && as.Op == "=" {
			note(id.Name, e.inferExprType(as.Right))
		}
		walkExpr(as.Right)
	}
	var walkStmts func([]ast.Statement)
	walkStmts = func(stmts []ast.Statement) {
		for _, stmt := range stmts {
			switch s := stmt.(type) {
			case *ast.VarDeclaration:
				declare(s)
			case *ast.VarDeclarationList:
				for _, d := range s.Decls {
					declare(d)
				}
			case *ast.ExpressionStatement:
				walkExpr(s.Expr)
			case *ast.IfStatement:
				if s.Consequent != nil {
					walkStmts(s.Consequent.Body)
				}
				if s.Alternate != nil {
					walkStmts([]ast.Statement{s.Alternate})
				}
			case *ast.ForStatement:
				if es, ok := s.Init.(*ast.ExpressionStatement); ok {
					walkExpr(es.Expr)
				}
				for _, upd := range s.Update {
					walkExpr(upd)
				}
				if s.Body != nil {
					walkStmts(s.Body.Body)
				}
			case *ast.WhileStatement:
				if s.Body != nil {
					walkStmts(s.Body.Body)
				}
			case *ast.BlockStatement:
				walkStmts(s.Body)
			case *ast.ForOfStatement:
				if s.Body != nil {
					walkStmts(s.Body.Body)
				}
			case *ast.SwitchStatement:
				for _, c := range s.Cases {
					walkStmts(c.Body)
				}
			case *ast.TryStatement:
				if s.Body != nil {
					walkStmts(s.Body.Body)
				}
				if s.Catch != nil && s.Catch.Body != nil {
					walkStmts(s.Catch.Body.Body)
				}
				if s.Finally != nil {
					walkStmts(s.Finally.Body)
				}
			}
		}
	}
	walkStmts(body)
	result := map[string]bool{}
	for name := range untyped {
		if len(kinds[name]) > 1 {
			result[name] = true
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// boxHoistedCapture allocates a heap cell for a captured local/param at its
// dominating declaration point, seeds it (with the given already-materialized
// initReg IR value of type ty, or the type's deterministic default when initReg
// is ""), registers name as a boxed Symbol sharing that cell by pointer, and
// returns the cell pointer. This is promoteCaptureToCell's work done eagerly at
// declaration rather than lazily at the capturing closure's construction site,
// so the cell dominates every later read regardless of which conditional block
// the closure literal is emitted into.
// atEntry selects the entry (allocas) block for the cell allocation and seed,
// rather than the current body block. Use it for a function-scoped `var`
// (promoteVarToFuncScope) — which can be declared inside a conditional yet read
// after it, so its cell must dominate unconditionally, exactly why its slot has
// always lived in the entry block (emitVarSlotDefault) — and for a captured
// param (bound once, at entry). A `let`/`const` instead boxes at its declaration
// point in the body: that dominates its whole (block-scoped) legal reach, and,
// crucially, a per-iteration declaration inside a loop re-runs the malloc each
// iteration, giving each iteration's closure its own fresh cell (matching JS
// per-iteration `let` binding semantics), which a single entry-block cell would
// not.
func (e *Emitter) boxHoistedCapture(name string, ty Type, initReg string, isConst, atEntry bool) string {
	e.ensureMalloc()
	box := e.freshReg()
	emit := e.emitInstr
	if atEntry {
		emit = e.emitAlloca
	}
	emit(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", box, ty.Align()))
	if initReg == "" {
		if ty.IsDynamic {
			emit(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, box))
		} else {
			emit(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, ty.zeroLiteral(), box, ty.Align()))
		}
	} else {
		emit(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, initReg, box, ty.Align()))
	}
	e.define(name, Symbol{Ptr: box, Ty: ty, Boxed: true, IsConst: isConst})
	return box
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
		// A module global (TDD-00093) is accessible from inside any function —
		// including a closure body — directly via e.moduleGlobals, exactly like a
		// top-level function name. Capturing it would box a *second* copy, so a
		// mutation made through the global elsewhere wouldn't be seen; skip it and
		// let the body resolve the name to the one global.
		if _, isGlobal := e.moduleGlobals[name]; isGlobal {
			continue
		}
		sym, found := e.lookup(name)
		if !found {
			continue // built-in, function name, etc.
		}
		if sym.Ty.IsArray {
			return nil, fmt.Errorf("capturing array variable '%s' in a closure is not yet supported", name)
		}
		caps = append(caps, CapturedVar{Name: name, Ty: sym.Ty, Sym: sym})
	}
	// An arrow shares the enclosing method's lexical `this` (ADR-00460):
	// when the body uses `this` (outside any nested `function(){}`, which
	// keeps its own dynamic this) and a receiver is in scope, capture it
	// like any other variable — emitThisExpression's `lookup("this")` then
	// resolves to the captured cell inside the closure body.
	if lexicalThisIn(af) {
		if sym, found := e.lookup("this"); found {
			caps = append(caps, CapturedVar{Name: "this", Ty: sym.Ty, Sym: sym})
		}
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

// gatherGeneratorCaptures returns the enclosing-scope variables a nested generator
// declaration's body references free (TDD-00094) — its free identifiers minus its
// own params, minus module globals and top-level functions (both resolvable from
// anywhere). Deterministically sorted. Used to decide capture handling; a
// non-empty result means the generator closes over enclosing state.
func (e *Emitter) gatherGeneratorCaptures(fd *ast.FunctionDeclaration) []CapturedVar {
	bound := make(map[string]bool, len(fd.Params))
	addParamBoundNames(bound, fd.Params)
	refs := make(map[string]bool)
	if fd.Body != nil {
		scanStmtsFV(fd.Body.Body, bound, refs)
	}
	var caps []CapturedVar
	for name := range refs {
		if _, isGlobal := e.moduleGlobals[name]; isGlobal {
			continue
		}
		if sym, found := e.lookup(name); found {
			caps = append(caps, CapturedVar{Name: name, Ty: sym.Ty, Sym: sym})
		}
	}
	for i := 0; i < len(caps); i++ {
		for j := i + 1; j < len(caps); j++ {
			if caps[i].Name > caps[j].Name {
				caps[i], caps[j] = caps[j], caps[i]
			}
		}
	}
	return caps
}

// --- closure function emission ---

// emitClosureFunc emits the named LLVM function for an arrow function into
// e.functions. The function takes ptr %env as its first parameter, followed by
// the arrow function's regular parameters. Captured variables are accessed via
// GEP into %env.
func (e *Emitter) emitClosureFunc(af *ast.ArrowFunction, caps []CapturedVar, retTy Type, paramTypes []Type, closureName string) error {
	// Only the http.listen handler arrow itself keeps the bare-slot async
	// model — a nested async closure gets the real settled-promise path.
	if e.emittingHTTPHandler && ast.Node(af) != e.httpHandlerNode {
		e.emittingHTTPHandler = false
		defer func() { e.emittingHTTPHandler = true }()
	}
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
	// A nested function/closure body is not the constructor, even when lexically
	// inside one — a `readonly` write here is an error (TDD-00154), so clear the
	// ctor context for the duration of this body's emission.
	savedCurrentCtorClass := e.currentCtorClass
	e.currentCtorClass = ""
	// Eager-boxing capture set for this closure body (see hoistedCaptures).
	savedHoistedCaptures := e.hoistedCaptures
	savedWidened := e.widenedBindings
	{
		paramNames := make([]string, len(af.Params))
		for i, p := range af.Params {
			paramNames[i] = p.Name
		}
		if af.Block != nil {
			e.hoistedCaptures = capturedLocalNames(af.Block.Body, paramNames)
			e.widenedBindings = e.crossTypeWidenedBindings(af.Block.Body)
		} else if af.Body != nil {
			e.hoistedCaptures = capturedLocalNames([]ast.Statement{&ast.ExpressionStatement{Expr: af.Body}}, paramNames)
			e.widenedBindings = e.crossTypeWidenedBindings([]ast.Statement{&ast.ExpressionStatement{Expr: af.Body}})
		} else {
			e.hoistedCaptures = nil
			e.widenedBindings = nil
		}
	}
	defer func() {
		e.breakStack = savedBreakStack
		e.continueStack = savedContinueStack
		e.namedLabelStack = savedNamedLabelStack
		e.currentGenerator = savedCurrentGenerator
		e.currentCtorClass = savedCurrentCtorClass
		e.hoistedCaptures = savedHoistedCaptures
		e.widenedBindings = savedWidened
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
		if err := e.pushNestedFuncScope(af.Params, af.Block.Body); err != nil {
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
			// Object-reference array param (TDD-00127): see bindArrayParam.
			if p.ArrayPattern != nil {
				dataPtrReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%p_%s_ptr, align 8", dataPtrReg, p.Name))
				if err := e.unpackArrayPatternInto(dataPtrReg, "%p_"+p.Name+"_len", *pty.ElemType, p.ArrayPattern); err != nil {
					return err
				}
				continue
			}
			e.bindArrayParam(p.Name, pty)
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
		if e.hoistedCaptures[p.Name] {
			e.boxHoistedCapture(p.Name, pty, "%p_"+p.Name, false, true)
		} else {
			e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
		}
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
	e.emitSafepoint() // function-entry preempt check (TDD-00143 Stage 2)
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
				dataSlot, lenSlot := e.arrayDataLenSlots(sym)
				ptrReg := e.freshReg()
				lenReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, dataSlot))
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, lenSlot))
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
		} else if isNullableScalar(retTy) {
			// Mirrors emitReturn's own nullable-scalar branch (emit_stmts.go):
			// the presence-aware boxing must see the AST (a null literal or a
			// nullable identifier keeps its absent bit) — an ordinary
			// emitExpr read auto-unwraps to the bare payload and would wrap
			// everything present (ADR-00478; previously this shortcut emitted
			// the bare payload where the { i1, T } aggregate was expected — a
			// hard clang error).
			agg, err := e.emitNullableScalarBoxedValue(af.Body, retTy)
			if err != nil {
				return err
			}
			e.emitTerminator(fmt.Sprintf("ret %s %s", nullableScalarStorageIR(retTy), agg))
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
	// Best-effort visibility for the block's own top-level locals, so a
	// `return { body: localStream }` infers the local's real type instead of
	// the bare-scalar default (found wiring TDD-00097 Stage 5's streaming
	// http bodies; helps any handler returning a local).
	defineDecl := func(vd *ast.VarDeclaration) {
		if vd.TypeAnnot != nil {
			e.define(vd.Name, Symbol{Ty: e.resolveType(vd.TypeAnnot)})
		} else if vd.Init != nil {
			e.define(vd.Name, Symbol{Ty: e.inferExprType(vd.Init)})
		}
	}
	for _, st := range block.Body {
		switch vd := st.(type) {
		case *ast.VarDeclaration:
			defineDecl(vd)
		case *ast.VarDeclarationList:
			for _, d := range vd.Decls {
				defineDecl(d)
			}
		}
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
			paramTypes[i] = ArrayOf(TypeF64)
		} else if p.Type == nil && i < len(hints) {
			paramTypes[i] = hints[i]
		} else if p.Type == nil {
			paramTypes[i] = TypeF64
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
		} else if firstReturnExprInBlock(af.Block) == nil {
			// Every return in the block is a bare `return;` — a void closure.
			// The scalar default below used to win here, emitting `ret i64 0`
			// for the bare return and a runtime-reachable `unreachable` at the
			// fall-through end (a real pre-existing crash, found by TDD-00097
			// Stage 6's pull callbacks using early-return).
			retTy = TypeVoid
		} else {
			retTy = TypeF64 // block body: scalar default, caller may override via annotation
		}
	} else {
		retTy = TypeVoid // block body with no reachable return (e.g. forEach callback)
	}

	// An async arrow always returns a promise slot (the inline async
	// prologue/epilogue below) — wrap a non-promise inferred/void return type
	// so the emitted define's return IR (ptr) matches what the epilogue
	// actually returns, and so callers see a Promise-typed callback. Found
	// wiring ReadableStream's async pull (TDD-00097): an async block-bodied
	// arrow with no `return` inferred `void` and emitted `ret ptr` inside a
	// `define void`, which clang rejects.
	if af.IsAsync && !retTy.IsPromise {
		retTy = PromiseOf(retTy)
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
				cellPtr = e.promoteCaptureToCell(cap.Name, cap.Ty, cap.Sym.Ptr, cap.Sym.IsConst)
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
	// A generator expression that reaches here wasn't a top-level `const G =
	// function* ...` binding (those were rewritten into named declarations,
	// TDD-00096) — an argument/nested/IIFE use has no first-class
	// generator-value model yet, so reject it cleanly rather than emitting
	// a plain closure whose `yield`s would then fail confusingly.
	if fe.IsGenerator {
		return Value{}, fmt.Errorf("%d:%d: a generator expression is only supported as a top-level `const/let/var G = function* ...` binding (V1) — using it as a value (an argument, a nested binding, or an IIFE) is not yet supported", fe.GetPos().Line, fe.GetPos().Col)
	}
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
		// A module global (TDD-00093) is referenced directly, never captured —
		// otherwise a mutation the body makes (`count++`) would hit a boxed copy
		// instead of the one global (same reasoning as gatherCaptures).
		if _, isGlobal := e.moduleGlobals[name]; isGlobal {
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
			paramTypes[i] = ArrayOf(TypeF64)
		} else if p.Type == nil && i < len(hints) {
			paramTypes[i] = hints[i]
		} else if p.Type == nil {
			paramTypes[i] = TypeF64
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
		} else if firstReturnExprInBlock(fe.Body) == nil {
			retTy = TypeVoid // every return is bare — see the arrow variant above
		} else {
			retTy = TypeF64
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
		// the same name: inside the expression body, JS scoping makes the name
		// refer to the expression itself. The resolver's rename pass (TDD-00041)
		// binds fe.Name in the expression's own scope *before* rewriting the
		// body, so a self-reference stays unmangled (`N`, not the outer
		// function's `N__kml_mod<N>`) and the self-capture below reclaims it —
		// the recursion calls the expression, not the shadowed outer function.
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
	// A nested function/closure body is not the constructor, even when lexically
	// inside one — a `readonly` write here is an error (TDD-00154), so clear the
	// ctor context for the duration of this body's emission.
	savedCurrentCtorClass := e.currentCtorClass
	e.currentCtorClass = ""
	// Eager-boxing capture set for this function-expression body.
	savedHoistedCaptures := e.hoistedCaptures
	savedWidened := e.widenedBindings
	{
		paramNames := make([]string, len(fe.Params))
		for i, p := range fe.Params {
			paramNames[i] = p.Name
		}
		if fe.Body != nil {
			e.hoistedCaptures = capturedLocalNames(fe.Body.Body, paramNames)
			e.widenedBindings = e.crossTypeWidenedBindings(fe.Body.Body)
		} else {
			e.hoistedCaptures = nil
			e.widenedBindings = nil
		}
	}
	defer func() {
		e.breakStack = savedBreakStack
		e.continueStack = savedContinueStack
		e.namedLabelStack = savedNamedLabelStack
		e.currentGenerator = savedCurrentGenerator
		e.currentCtorClass = savedCurrentCtorClass
		e.hoistedCaptures = savedHoistedCaptures
		e.widenedBindings = savedWidened
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
	if err := e.pushNestedFuncScope(fe.Params, fe.Body.Body); err != nil {
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
			// Object-reference array param (TDD-00127): see bindArrayParam.
			if p.ArrayPattern != nil {
				dataPtrReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%p_%s_ptr, align 8", dataPtrReg, p.Name))
				elemTy := TypeF64
				if pty.ElemType != nil {
					elemTy = *pty.ElemType
				}
				if err := e.unpackArrayPatternInto(dataPtrReg, "%p_"+p.Name+"_len", elemTy, p.ArrayPattern); err != nil {
					return Value{}, err
				}
				continue
			}
			e.bindArrayParam(p.Name, pty)
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
		if e.hoistedCaptures[p.Name] {
			e.boxHoistedCapture(p.Name, pty, "%p_"+p.Name, false, true)
		} else {
			e.define(p.Name, Symbol{Ptr: ptrName, Ty: pty})
		}
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
	e.emitSafepoint() // function-entry preempt check (TDD-00143 Stage 2)
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
				cellPtr = e.promoteCaptureToCell(cap.Name, cap.Ty, cap.Sym.Ptr, cap.Sym.IsConst)
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

// emitFunctionCallApply implements Function.prototype.call / .apply on a
// first-class function value (TDD-00137 Stage A). `fnExpr` is the function-typed
// receiver; `method` is "call" or "apply". The trailing arguments are forwarded
// to a direct closure call — `thisArg` (the first argument) is evaluated for its
// side effects then discarded, since this compiler's closures have no rebindable
// `this`. For `apply`, the second argument must be a literal array whose
// elements become the call arguments; a runtime args array is a clean error
// (Stage B).
func (e *Emitter) emitFunctionCallApply(fnExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	fnVal, err := e.emitExpr(fnExpr)
	if err != nil {
		return Value{}, err
	}
	if !fnVal.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: .%s requires a function value", pos.Line, pos.Col, method)
	}
	// thisArg: evaluate (for side effects) then ignore — no rebindable `this`.
	if len(args) >= 1 {
		if _, err := e.emitExpr(args[0]); err != nil {
			return Value{}, err
		}
	}

	var callArgs []ast.Expression
	if method == "call" {
		callArgs = args[1:]
	} else { // apply
		if len(args) < 2 {
			// apply() / apply(thisArg) with no args array — an empty argument list.
			callArgs = nil
		} else if lit, ok := args[1].(*ast.ArrayLiteral); ok {
			callArgs = lit.Elements
		} else {
			// Stage B: fn.apply(thisArg, runtimeArray) — spread the runtime
			// array into fn's rest parameter, exactly like fn(...arr). The
			// closure-call path validates that fn actually has a rest slot and
			// the element types match (a non-rest fn is a clean error there).
			callArgs = []ast.Expression{ast.NewSpreadElement(args[1], args[1].GetPos())}
		}
	}
	return e.emitClosureCallByPtr(fnVal.Ref, fnVal.Ty, callArgs, pos)
}

// emitFunctionBind implements Function.prototype.bind (TDD-00137 Stage C):
// `fn.bind(thisArg, ...bound)` returns a new function value that, when called
// with the remaining arguments, invokes `fn(bound…, remaining…)`. `thisArg` is
// evaluated then discarded (no rebindable `this`). V1 is bounded to functions
// with plain scalar/string/pointer parameters and no rest slot — an array,
// nullable-scalar, dynamic, or rest parameter is a clean compile error.
func (e *Emitter) emitFunctionBind(fnExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	fnVal, err := e.emitExpr(fnExpr)
	if err != nil {
		return Value{}, err
	}
	if !fnVal.Ty.IsFunc {
		return Value{}, fmt.Errorf("%d:%d: .bind requires a function value", pos.Line, pos.Col)
	}
	if fnVal.Ty.FuncHasRest {
		return Value{}, fmt.Errorf("%d:%d: .bind on a rest-parameter function is not yet supported", pos.Line, pos.Col)
	}
	for _, p := range fnVal.Ty.FuncParams {
		if p.IsArray || isNullableScalar(p) || p.IsDynamic {
			return Value{}, fmt.Errorf("%d:%d: .bind is supported only on functions whose parameters are plain scalar/string/pointer types (V1)", pos.Line, pos.Col)
		}
	}
	boundCount := len(args) - 1
	if boundCount < 0 {
		boundCount = 0
	}
	if boundCount > len(fnVal.Ty.FuncParams) {
		return Value{}, fmt.Errorf("%d:%d: .bind supplies %d bound argument(s) but the function takes only %d", pos.Line, pos.Col, boundCount, len(fnVal.Ty.FuncParams))
	}
	// thisArg (args[0]) — evaluate for side effects, then ignore.
	if len(args) >= 1 {
		if _, err := e.emitExpr(args[0]); err != nil {
			return Value{}, err
		}
	}

	// env layout: slot 0 = the original fn closure header; slots 1..K = the
	// bound argument values (one 8-byte slot each; every allowed param type
	// fits in 8 bytes).
	e.ensureMalloc()
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", env, 8*(1+boundCount)))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fnVal.Ref, env))
	for i := 0; i < boundCount; i++ {
		paramTy := fnVal.Ty.FuncParams[i]
		bv, err := e.emitExprWithObjectHint(args[i+1], paramTy)
		if err != nil {
			return Value{}, err
		}
		bv = e.coerce(bv, paramTy)
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", slot, env, 8*(1+i)))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(paramTy), bv.Ref, slot))
	}

	tramp := e.emitBindTrampoline(fnVal.Ty, boundCount)
	hdr := e.buildBuiltinClosure(tramp, env)
	retTy := TypeVoid
	if fnVal.Ty.FuncRetType != nil {
		retTy = *fnVal.Ty.FuncRetType
	}
	return Value{Ref: hdr, Ty: FuncType(fnVal.Ty.FuncParams[boundCount:], retTy)}, nil
}

// emitBindTrampoline emits the forwarding function for a .bind result: it loads
// the original closure header + the bound args from its env and tail-calls the
// original with `(bound…, remaining…)`. Signature is
// `<ret> (ptr %env, <remaining param IRs>)` — matching how the reduced closure
// value is later invoked (emitClosureCallByPtr).
func (e *Emitter) emitBindTrampoline(fnTy Type, boundCount int) string {
	e.streamSiteCtr++
	fn := fmt.Sprintf("@__kml_bind_%d", e.streamSiteCtr)
	remaining := fnTy.FuncParams[boundCount:]
	retTy := TypeVoid
	if fnTy.FuncRetType != nil {
		retTy = *fnTy.FuncRetType
	}

	// Trampoline parameter declaration.
	paramDecls := []string{"ptr %env"}
	for i, p := range remaining {
		paramDecls = append(paramDecls, fmt.Sprintf("%s %%r%d", storageIR(p), i))
	}
	// The original closure's function-pointer type: (ptr env, all params…).
	allIRs := []string{"ptr"}
	for _, p := range fnTy.FuncParams {
		allIRs = append(allIRs, storageIR(p))
	}
	fpTypePart := "(" + strings.Join(allIRs, ", ") + ")"

	restore := e.beginThunkEmit()
	fnhdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %%env, align 8", fnhdr))
	fp := e.freshReg()
	fpp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", fpp, fnhdr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, fpp))
	ep := e.freshReg()
	epp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", epp, fnhdr))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ep, epp))

	callArgs := []string{"ptr " + ep}
	for i := 0; i < boundCount; i++ {
		p := fnTy.FuncParams[i]
		slot := e.freshReg()
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%env, i64 %d", slot, 8*(1+i)))
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", v, storageIR(p), slot))
		callArgs = append(callArgs, fmt.Sprintf("%s %s", storageIR(p), v))
	}
	for i, p := range remaining {
		callArgs = append(callArgs, fmt.Sprintf("%s %%r%d", storageIR(p), i))
	}

	if retTy.IR == "void" {
		e.emitInstr(fmt.Sprintf("call void %s %s(%s)", fpTypePart, fp, strings.Join(callArgs, ", ")))
		e.emitInstr("ret void")
	} else {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s %s %s(%s)", r, retTy.LLVMRetType(), fpTypePart, fp, strings.Join(callArgs, ", ")))
		e.emitInstr(fmt.Sprintf("ret %s %s", retTy.LLVMRetType(), r))
	}
	body := e.allocas.String() + e.body.String()
	restore()

	e.functions.WriteString(fmt.Sprintf("\ndefine %s %s(%s) {\nentry:\n%s}\n", retTy.LLVMRetType(), fn, strings.Join(paramDecls, ", "), body))
	return fn
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

	// Thenable adoption (TDD-00091): `resolve(aPromise)` on a `new Promise`
	// resolve closure doesn't coerce the promise to the value type — it settles
	// the target (the resolve closure's env) when the argument promise settles.
	// The resolve closure carries IsPromiseResolver; adoption fires only when the
	// single argument is actually a Promise (a plain value falls through to the
	// normal settle path below).
	if ty.IsPromiseResolver && len(args) == 1 && e.inferExprType(args[0]).IsPromise {
		argVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		e.emitPromiseAdopt(epVal, argVal.Ref)
		return Value{Ty: TypeVoid}, nil
	}

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

	// Spread argument (TDD-00106): same V1 rule as a named-function call.
	if err := e.checkSpreadArgs(args, ty.FuncHasRest, regularCount, pos); err != nil {
		return Value{}, err
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
			if !coerciblePure(val.Ty, paramTy) {
				return Value{}, fmt.Errorf("%d:%d: argument %d has a type incompatible with the parameter's declared type — this compiler is a typed subset", arg.GetPos().Line, arg.GetPos().Col, i+1)
			}
			val = e.coerce(val, paramTy)
		}
		// An array-typed parameter decomposes into two LLVM args (ptr, i64
		// len) at the call site — matches the (ptr, i64) callee ABI
		// emitClosureFunc's own parameter loop now expands an array
		// parameter into (ADR-00151/TDD-00059). val is already the real
		// {ptr,i64} aggregate (emitExprWithObjectHint/emitExpr's own
		// IsArray handling), so this only needs to split it, not build it.
		if paramTy.IsArray {
			header, lenReg := e.packArrayArg(arg, val)
			argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
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
		elemTy := TypeF64
		if restTy.ElemType != nil {
			elemTy = *restTy.ElemType
		}
		if spread, ok := singleSpread(restArgs); ok {
			// f(...arr) into a closure's rest slot — forward the array's (ptr,len)
			// (TDD-00106), the same as the named-function path.
			ptrReg, lenReg, srcElemTy, err := e.resolveArrayForHOF(spread.Arg, spread.Arg.GetPos())
			if err != nil {
				return Value{}, err
			}
			if srcElemTy.IR != elemTy.IR || srcElemTy.IsArray != elemTy.IsArray || srcElemTy.IsObject != elemTy.IsObject {
				return Value{}, fmt.Errorf("%d:%d: spread array's element type does not match the rest parameter's element type", spread.Arg.GetPos().Line, spread.Arg.GetPos().Col)
			}
			restHdr := e.newArrayHeader(ptrReg, lenReg)
			argParts = append(argParts, "ptr "+restHdr, "i64 "+lenReg)
		} else if len(restArgs) == 0 {
			argParts = append(argParts, "ptr "+e.emptyArrayArgHeader(), "i64 0")
		} else if anySpread(restArgs) {
			// A runtime-length mix of spreads and positional args feeding the
			// closure's rest slot (TDD-00106 V2) — same concat as the named path.
			dataReg, lenReg, err := e.emitRestArgBuffer(restArgs, elemTy)
			if err != nil {
				return Value{}, err
			}
			restHdr := e.newArrayHeader(dataReg, lenReg)
			argParts = append(argParts, "ptr "+restHdr, "i64 "+lenReg)
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
			restHdr := e.newArrayHeader(dataReg, fmt.Sprintf("%d", n))
			argParts = append(argParts, "ptr "+restHdr, fmt.Sprintf("i64 %d", n))
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
	// Any other expression whose static type is a function value — a bound
	// function (`fn.bind(null, x)`), a wrapper-returning call (`mustCall(fn)`),
	// a function-typed object field or array element (`obj.handler`,
	// `handlers[i]`), a ternary (`cond ? f : g`), etc. Evaluate it once to its
	// closure header and dispatch as a closure. Checked after the specific
	// arrow/expression/identifier cases above so their tailored handling wins.
	if ty := e.inferExprType(arg); ty.IsFunc {
		v, err := e.emitExpr(arg)
		if err != nil {
			return Callback{}, err
		}
		return Callback{kind: cbClosure, hdrPtr: v.Ref, ty: v.Ty}, nil
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

	// Reconcile the argument count to the callback's declared arity so the
	// emitted call always has exactly as many operands as the callee /
	// function-pointer type declares. A higher-order method passes a fixed
	// argument set (element, index, array), but a callback may declare fewer
	// parameters — JS silently ignores the extras — or more, which JS binds to
	// undefined. Passing the raw HOF arguments against the narrower or wider
	// declared signature is invalid LLVM IR ("too many/too few arguments
	// specified"); truncate the extras and pad any missing parameter with a
	// zero value of its type so the call is always well-typed. Fixes the
	// long-standing HOF-arity invalid-IR cluster (e.g. a zero-parameter
	// predicate `[].findIndex(function () {})`, or a default-param skip).
	coerced := make([]Value, len(params))
	for i := range params {
		switch {
		case i < len(args):
			coerced[i] = e.coerce(args[i], params[i])
		case params[i].IsArray:
			// A missing array parameter: an empty Ref marks it for the
			// decomposition branches below, which emit an empty `ptr null,
			// i64 0` header rather than materializing a real aggregate.
			coerced[i] = Value{Ty: params[i]}
		default:
			coerced[i] = Value{Ref: zeroRef(params[i]), Ty: params[i]}
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
			if params[i].IsArray {
				if v.Ref == "" {
					// Padded missing array parameter (see the reconciliation
					// above) — an empty header.
					argParts = append(argParts, "ptr null", "i64 0")
					continue
				}
				// The callback's array argument is a transient element value —
				// materialize a fresh header for it (TDD-00127).
				header, lenReg := e.arrayArgFromAggregate(v)
				argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
				continue
			}
			argParts = append(argParts, params[i].IR+" "+v.Ref)
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
			if params[i].IsArray {
				if v.Ref == "" {
					argParts = append(argParts, "ptr null", "i64 0")
					continue
				}
				// The callback's array argument is a transient element value —
				// materialize a fresh header for it (TDD-00127).
				header, lenReg := e.arrayArgFromAggregate(v)
				argParts = append(argParts, "ptr "+header, "i64 "+lenReg)
				continue
			}
			argParts = append(argParts, params[i].IR+" "+v.Ref)
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


// lexicalThisIn reports whether n's subtree contains a `this` expression
// that would bind lexically — the walk recurses through nested arrows (they
// share the enclosing `this`) but stops at function expressions and
// function declarations, whose bodies have their own dynamic `this`
// (ADR-00460). Implemented as a small reflective walk so every present and
// future AST node shape is covered without a hand-maintained visitor.
func lexicalThisIn(n any) bool {
	switch n.(type) {
	case nil:
		return false
	case *ast.ThisExpression:
		return true
	case *ast.FunctionExpression, *ast.FunctionDeclaration:
		return false
	}
	rv := reflect.ValueOf(n)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < rv.NumField(); i++ {
		f := rv.Field(i)
		if !f.CanInterface() {
			continue
		}
		if lexicalThisInValue(f) {
			return true
		}
	}
	return false
}

func lexicalThisInValue(f reflect.Value) bool {
	switch f.Kind() {
	case reflect.Interface, reflect.Ptr:
		if f.IsNil() {
			return false
		}
		return lexicalThisIn(f.Interface())
	case reflect.Slice:
		for i := 0; i < f.Len(); i++ {
			if lexicalThisInValue(f.Index(i)) {
				return true
			}
		}
	case reflect.Struct:
		return lexicalThisIn(f.Interface())
	}
	return false
}

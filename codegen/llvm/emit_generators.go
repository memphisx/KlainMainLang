// emit_generators.go — generator function construction, `yield`, and
// `.next()` (TDD-00061/ADR-00172). A generator instance is its own fiber
// (a private ucontext_t + stack), reusing this compiler's existing
// http.listen/fetch() fiber primitive (TDD-00006 Part 2, runtime_http.go)
// generalized to a per-instance context/stack pair instead of the HTTP
// case's one global connection array — a generator's own "resume target"
// changes on every .next() call (whoever calls it), unlike a connection
// fiber's fixed scheduler swap-back target.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// GeneratorInfo is one top-level `function*` declaration's registered
// shape — see buildGeneratorSig.
type GeneratorInfo struct {
	ParamTypes   []Type
	ParamNames   []string
	ElemTy       Type   // T in `function* f(): T` — the yielded/returned value type
	GenTy        Type   // GeneratorType(ElemTy, ParamTypes) — the constructed instance's own type
	BodyFuncName string // mangled LLVM function name for the compiled body, e.g. "@__generator_body_gen_1"
	// ThisTy is the receiver type for a generator *method* (TDD-00063 Stage
	// 2b) — nil for a free-function generator. When set, GenTy carries a
	// __this slot and the body binds `this` from it at entry.
	ThisTy *Type
}

// generatorEmitCtx tracks state while emitting one generator function's own
// body — set/cleared by emitGeneratorFunctionDecl the same way
// currentRetType/isAsync track an ordinary function's state, read by
// emitYieldExpression and emitReturn to route yield/return through the
// suspend-and-swap path instead of an ordinary value/`ret`.
type generatorEmitCtx struct {
	genObjReg string // %genObj — this invocation's own struct pointer
	genTy     Type
	elemTy    Type
}

// buildGeneratorSig computes a top-level `function*` declaration's
// GeneratorInfo — mirrors buildFunctionSig's basic param-type resolution,
// but deliberately narrower for V1: a plain (non-destructured, non-rest,
// non-default, non-optional) parameter list, a non-array element type (no
// aggregate-typed zero-value/slot machinery built yet — see
// emitScalarZero's own scope), and an explicit return-type annotation (the
// element type yield/return produce) — no yield-based inference exists yet,
// unlike an ordinary unannotated function's own best-effort inference.
func (e *Emitter) buildGeneratorSig(fd *ast.FunctionDeclaration) (*GeneratorInfo, error) {
	if fd.ReturnType == nil {
		return nil, fmt.Errorf("%d:%d: generator function '%s' requires an explicit return type annotation (the element type yield/return produce) — inferring it from the body's own yield expressions is not yet supported", fd.GetPos().Line, fd.GetPos().Col, fd.Name)
	}
	elemTy := e.resolveType(fd.ReturnType)
	if elemTy.IsArray {
		return nil, fmt.Errorf("%d:%d: an array element type is not yet supported on a generator function", fd.GetPos().Line, fd.GetPos().Col)
	}
	var paramTypes []Type
	var paramNames []string
	for _, p := range fd.Params {
		if p.ArrayPattern != nil || p.ObjectPattern != nil {
			return nil, fmt.Errorf("%d:%d: a destructured parameter is not yet supported on a generator function", fd.GetPos().Line, fd.GetPos().Col)
		}
		if p.Rest {
			return nil, fmt.Errorf("%d:%d: a rest parameter is not yet supported on a generator function", fd.GetPos().Line, fd.GetPos().Col)
		}
		if p.Default != nil || p.Optional {
			return nil, fmt.Errorf("%d:%d: a default or optional parameter is not yet supported on a generator function", fd.GetPos().Line, fd.GetPos().Col)
		}
		var pty Type
		if p.Type != nil {
			pty = e.resolveType(p.Type)
		} else {
			pty = TypeI64
			pty.Inferred = true
		}
		if pty.IsArray {
			return nil, fmt.Errorf("%d:%d: an array-typed parameter is not yet supported on a generator function", fd.GetPos().Line, fd.GetPos().Col)
		}
		paramTypes = append(paramTypes, pty)
		paramNames = append(paramNames, p.Name)
	}
	e.generatorBodyCtr++
	return &GeneratorInfo{
		ParamTypes:   paramTypes,
		ParamNames:   paramNames,
		ElemTy:       elemTy,
		GenTy:        GeneratorType(elemTy, paramTypes, nil),
		BodyFuncName: fmt.Sprintf("@__generator_body_%s_%d", fd.Name, e.generatorBodyCtr),
	}, nil
}

// buildGeneratorMethodInfo is buildGeneratorSig's sibling for a generator
// *method* (TDD-00063 Stage 2b): same param/element-type rules, but the
// resulting GenTy carries a __this slot (the receiver), and the body func
// name is namespaced by class+method rather than a bare function name.
// classTy is the receiver type `this` binds to inside the body.
func (e *Emitter) buildGeneratorMethodInfo(fd *ast.FunctionDeclaration, className string, classTy Type) (*GeneratorInfo, error) {
	base, err := e.buildGeneratorSig(fd)
	if err != nil {
		return nil, err
	}
	base.ThisTy = &classTy
	base.GenTy = GeneratorType(base.ElemTy, base.ParamTypes, &classTy)
	base.BodyFuncName = fmt.Sprintf("@__generator_method_%s_%s_%d", llvmSafeSymbol(className), llvmSafeSymbol(fd.Name), e.generatorBodyCtr)
	return base, nil
}

// genNextResultType returns `.next()`'s own result shape ({value: T, done:
// bool}) — a plain object type (not IsGenerator), so `.value`/`.done` are
// read via the ordinary generic object-field machinery with no dispatch
// code of their own needed anywhere.
func genNextResultType(elem Type) Type {
	return ObjectType([]Field{
		{Name: "value", Ty: elem},
		{Name: "done", Ty: TypeBool},
	})
}

// ensureGeneratorRuntime declares the fiber-context-switching primitive
// (shared with http.listen/fetch() via ensureFiberRuntime — same
// getcontext/makecontext/swapcontext declares, same ucontextLayout()
// constants) plus one generator-specific global: a "pending generator
// object" handoff slot. makecontext's target function (the generator's own
// compiled body, see emitGeneratorFunctionDecl) takes zero arguments —
// mirroring http.listen's own `@__kml_listen_dispatch` entry point, which
// also takes none and instead reads a global (`@__kml_current_conn_idx`)
// to find its own context on first entry. A generator's own entry needs
// its genObj pointer exactly once, at first-call time only: every
// subsequent resume picks up mid-function, wherever its last yield's own
// swapcontext call left off, with %genObj still live as an ordinary local
// on the generator's own never-torn-down fiber stack — so a single global,
// written right before the first swapcontext into a freshly makecontext'd
// generator and read exactly once at that generator's own entry, is
// sufficient (unlike the connection-array case, this doesn't need to
// persist across multiple generators' first-entries interleaving, since
// each generator's own first entry happens synchronously inside the one
// `gen(args)` construction call that set the global — no other code runs
// in between to overwrite it first).
func (e *Emitter) ensureGeneratorRuntime() {
	if e.usedGeneratorRuntime {
		return
	}
	e.usedGeneratorRuntime = true
	e.ensureFiberRuntime()
	e.ensureMalloc()
	e.emitGlobal("@__kml_gen_pending_obj = internal global ptr null, align 8")
}

// storeGeneratorField GEPs+stores one field of a generator instance —
// shared by construction (emitGeneratorConstruction) and the body's own
// prologue/yield/return paths (emitGeneratorFunctionDecl/
// emitYieldExpression), all of which repeat the identical
// FieldIndex-then-GEP-then-store shape enough times to warrant one helper.
func (e *Emitter) storeGeneratorField(genObjReg string, genTy Type, field, ir, val string) {
	idx, fieldTy, _ := genTy.FieldIndex(field)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, genTy.StructIR(), genObjReg, idx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, val, gep, fieldTy.Align()))
}

// loadGeneratorField GEPs+loads one field of a generator instance — see
// storeGeneratorField's own doc comment.
func (e *Emitter) loadGeneratorField(genObjReg string, genTy Type, field string) Value {
	idx, fieldTy, _ := genTy.FieldIndex(field)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, genTy.StructIR(), genObjReg, idx))
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, fieldTy.IR, gep, fieldTy.Align()))
	return Value{Ref: reg, Ty: fieldTy}
}

// emitGeneratorConstruction implements `gen(args)` — calling a generator
// function does not run its body at all (unlike an ordinary function call):
// it mallocs the instance struct plus its own private ucontext_t/stack,
// getcontext+makecontext (pointed at the generator's own compiled body
// function, GeneratorInfo.BodyFuncName), stores the call's own arguments
// into the struct's own __paramN fields, and returns the struct pointer as
// the generator's own opaque instance value — the body only actually starts
// running on the first .next() call (emitGeneratorNext).
func (e *Emitter) emitGeneratorConstruction(info *GeneratorInfo, args []ast.Expression, pos ast.Pos) (Value, error) {
	return e.emitGeneratorConstructionWithThis(info, "", args, pos)
}

// emitGeneratorConstructionWithThis is emitGeneratorConstruction's shared
// core, additionally storing a receiver into the __this slot when thisRef is
// non-empty (a generator method, TDD-00063 Stage 2b) — a free-function
// generator passes "". Everything else (fiber ctx/stack setup, __paramN
// stores, the returned opaque instance value) is identical either way.
func (e *Emitter) emitGeneratorConstructionWithThis(info *GeneratorInfo, thisRef string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != len(info.ParamTypes) {
		return Value{}, fmt.Errorf("%d:%d: generator expects %d argument(s), got %d", pos.Line, pos.Col, len(info.ParamTypes), len(args))
	}
	e.ensureGeneratorRuntime()

	genTy := info.GenTy
	genObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", genObj, genTy.StructSize()))

	ctxSize, ssSpOff, ssSizeOff, ucLinkOff := ucontextLayout()
	ctxReg := e.freshReg()
	stackReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", ctxReg, ctxSize))
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", stackReg, fiberStackBytes))
	e.emitInstr(fmt.Sprintf("call void @getcontext(ptr %s)", ctxReg))
	spGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", spGep, ctxReg, ssSpOff))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", stackReg, spGep))
	szGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", szGep, ctxReg, ssSizeOff))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", fiberStackBytes, szGep))
	// uc_link: null, not a fallback scheduler ctx (http.listen's own
	// @__kml_main_ctx precedent) — this compiler's own generator-body
	// codegen guarantees every exit path (yield, return, fall-off-the-end)
	// swaps back explicitly rather than ever letting the body function
	// actually `ret`, so uc_link is never meant to be exercised; null makes
	// an undiscovered codegen bug that broke that guarantee a clean
	// process exit (ucontext.h's own documented null-uc_link behavior),
	// not a resume into a stale/unpopulated fallback context.
	linkGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", linkGep, ctxReg, ucLinkOff))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", linkGep))
	e.emitInstr(fmt.Sprintf("call void (ptr, ptr, i32, ...) @makecontext(ptr %s, ptr %s, i32 0)", ctxReg, info.BodyFuncName))

	e.storeGeneratorField(genObj, genTy, GeneratorCtxField, "ptr", ctxReg)
	e.storeGeneratorField(genObj, genTy, GeneratorStackField, "ptr", stackReg)
	e.storeGeneratorField(genObj, genTy, GeneratorCallerCtxField, "ptr", "null")
	e.storeGeneratorField(genObj, genTy, GeneratorStartedField, "i1", "0")
	e.storeGeneratorField(genObj, genTy, GeneratorDoneField, "i1", "0")
	zeroElem := e.emitScalarZero(info.ElemTy)
	e.storeGeneratorField(genObj, genTy, GeneratorYieldedField, zeroElem.Ty.IR, zeroElem.Ref)
	zeroElem2 := e.emitScalarZero(info.ElemTy)
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, zeroElem2.Ty.IR, zeroElem2.Ref)

	for i, argExpr := range args {
		val, err := e.emitExpr(argExpr)
		if err != nil {
			return Value{}, err
		}
		val = e.coerce(val, info.ParamTypes[i])
		e.storeGeneratorField(genObj, genTy, fmt.Sprintf("__param%d", i), val.Ty.IR, val.Ref)
	}
	if thisRef != "" {
		e.storeGeneratorField(genObj, genTy, GeneratorThisField, "ptr", thisRef)
	}

	return Value{Ref: genObj, Ty: genTy}, nil
}

// emitGeneratorSwapToCaller stores val into the generator's own __yielded
// field, done into __done, then swapcontext(genObj.ctx [save here],
// genObj.callerCtx [resume this]) — the shared core both a `yield expr`
// (done=false, control resumes right after this call once a later .next()
// swaps back in) and a `return expr`/fall-off-the-end exit (done=true,
// this generator's own fiber is never resumed again since emitGeneratorNext
// checks __done before ever swapping into it) use identically. No
// GC_stackbottom management here — see ensureGeneratorRuntime and
// emitGeneratorNext's own comments for why that's entirely the *resumer's*
// (the .next() call site's) responsibility, mirroring emit_http.go's
// identical existing fiber-yield precedent.
func (e *Emitter) emitGeneratorSwapToCaller(gctx *generatorEmitCtx, val Value, done bool) {
	doneLit := "0"
	if done {
		doneLit = "1"
	}
	e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorYieldedField, val.Ty.IR, val.Ref)
	e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorDoneField, "i1", doneLit)
	callerCtx := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorCallerCtxField)
	ownCtx := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorCtxField)
	swaprc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @swapcontext(ptr %s, ptr %s)", swaprc, ownCtx.Ref, callerCtx.Ref))
}

// emitYieldExpression implements `yield expr` / a bare `yield` inside a
// generator function's own body (gated on e.currentGenerator != nil by
// emitExpr's own dispatch — see its case for the "not inside a generator"
// fallback). Suspends via emitGeneratorSwapToCaller (done=false); once
// resumed by a later .next(value) call, __sent holds that value — which
// becomes this yield expression's own result, matching real JS semantics
// (the value yield "returns" is whatever the *next* .next() call sends in,
// not anything related to what was yielded out).
func (e *Emitter) emitYieldExpression(ex *ast.YieldExpression) (Value, error) {
	gctx := e.currentGenerator
	if ex.Delegate {
		return Value{}, fmt.Errorf("%d:%d: yield* is not yet supported", ex.GetPos().Line, ex.GetPos().Col)
	}
	var val Value
	if ex.Argument != nil {
		v, err := e.emitExpr(ex.Argument)
		if err != nil {
			return Value{}, err
		}
		val = e.coerce(v, gctx.elemTy)
	} else {
		val = e.emitScalarZero(gctx.elemTy)
	}
	e.emitGeneratorSwapToCaller(gctx, val, false)
	return e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorSentField), nil
}

// emitGeneratorReturn implements a `return expr;`/bare `return;` inside a
// generator function's own body (gated on e.currentGenerator != nil by
// emitReturn's own dispatch, checked before its ordinary async/void/typed
// handling). Suspends via emitGeneratorSwapToCaller (done=true) and never
// resumes — the trailing `ret void` emitGeneratorFunctionDecl's own
// epilogue always appends after this function's body finishes is
// unreachable in practice (emitGeneratorNext never swaps into a __done
// generator again), just a required well-formedness terminator.
func (e *Emitter) emitGeneratorReturn(r *ast.ReturnStatement) error {
	gctx := e.currentGenerator
	var val Value
	if r.Value != nil {
		v, err := e.emitExpr(r.Value)
		if err != nil {
			return err
		}
		val = e.coerce(v, gctx.elemTy)
	} else {
		val = e.emitScalarZero(gctx.elemTy)
	}
	e.emitGeneratorSwapToCaller(gctx, val, true)
	e.emitTerminator("ret void")
	return nil
}

// emitGeneratorFunctionDecl compiles a top-level `function*` declaration's
// own body into its dedicated LLVM function (GeneratorInfo.BodyFuncName) —
// a deliberately separate emission path from emitFunctionDeclAs/
// emitClosureFunc, not a refactor of either, matching this project's own
// precedent (ADR-00063's emitClassMember, a similarly separate calling
// convention) for why: this function's calling convention (zero LLVM
// parameters, its own genObj read once from a global handoff slot at
// entry, no ordinary `ret <value>` at all) is different enough from an
// ordinary function/closure that sharing the save/reset/restore dance
// would cost more in accumulated special-casing than a clean duplicate.
// No capture scanning: a generator gets its own clean scope, exactly like
// a plain top-level or TDD-00057 nested function declaration does — not an
// arrow function's closure — so referencing an outer local from inside a
// generator body is a clean "undefined variable" error, not a capture.
func (e *Emitter) emitGeneratorFunctionDecl(decl *ast.FunctionDeclaration, info *GeneratorInfo) error {
	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone
	savedCurrentGenerator := e.currentGenerator
	// break/continue/named-label stacks reset too, defensively, same
	// reasoning as every other per-function emitter-state reset in
	// emit_func.go — a generator declaration is only ever entered from
	// top-level code in V1 (never itself nested inside a live loop's own
	// break/continue context), so this isn't yet exploitable in practice,
	// but leaving it unreset would be a landmine for whenever nested
	// generator support is picked up. Restored via defer, not a plain
	// assignment — see emitFunctionDeclAs's identical fix for why a plain
	// assignment isn't safe against an error return partway through body
	// emission.
	savedBreakStack := e.breakStack
	savedContinueStack := e.continueStack
	savedNamedLabelStack := e.namedLabelStack
	e.breakStack = nil
	e.continueStack = nil
	e.namedLabelStack = nil
	defer func() {
		e.allocas = savedAllocas
		e.body = savedBody
		e.regCtr = savedRegCtr
		e.labelCtr = savedLabelCtr
		e.scopes = savedScopes
		e.currentRetType = savedRetType
		e.blockDone = savedBlockDone
		e.currentGenerator = savedCurrentGenerator
		e.breakStack = savedBreakStack
		e.continueStack = savedContinueStack
		e.namedLabelStack = savedNamedLabelStack
	}()

	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = info.ElemTy
	e.pushScope()

	// Read the pending-object handoff global exactly once, at entry — see
	// ensureGeneratorRuntime's own doc comment for why this is safe (only
	// ever meaningful on this generator's own first entry; every later
	// resume picks up mid-function with genObjReg still live as an
	// ordinary SSA value on this generator's own never-torn-down stack,
	// never re-reading the global).
	genObjReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_gen_pending_obj, align 8", genObjReg))

	gctx := &generatorEmitCtx{genObjReg: genObjReg, genTy: info.GenTy, elemTy: info.ElemTy}
	e.currentGenerator = gctx

	// A generator *method* (TDD-00063 Stage 2b) binds `this` (and `super`/the
	// lexical enclosing-class identity for visibility checks) from the __this
	// slot the construction site stored — the same load-once-at-entry story
	// the __paramN slots use. A free-function generator (ThisTy == nil) skips
	// this entirely.
	if info.ThisTy != nil {
		classTy := *info.ThisTy
		thisVal := e.loadGeneratorField(genObjReg, info.GenTy, GeneratorThisField)
		thisPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", thisPtr))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", thisVal.Ref, thisPtr))
		e.define("this", Symbol{Ptr: thisPtr, Ty: classTy})
		e.define("__kml_enclosing_class", Symbol{Ty: classTy})
		if classInfo, ok := e.classes[classTy.ClassName]; ok && classInfo.BaseClass != "" {
			baseTy := e.classes[classInfo.BaseClass].Ty
			e.define("super", Symbol{Ptr: thisPtr, Ty: baseTy})
		}
	}

	for i, p := range decl.Params {
		pty := info.ParamTypes[i]
		val := e.loadGeneratorField(genObjReg, info.GenTy, fmt.Sprintf("__param%d", i))
		localPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", localPtr, pty.IR, pty.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", pty.IR, val.Ref, localPtr, pty.Align()))
		e.define(p.Name, Symbol{Ptr: localPtr, Ty: pty})
	}

	if err := e.pushNestedFuncScope(decl.Body.Body); err != nil {
		return err
	}
	for _, stmt := range decl.Body.Body {
		if err := e.emitStmt(stmt); err != nil {
			e.popNestedFuncScope()
			return err
		}
	}
	e.popNestedFuncScope()

	// Implicit epilogue: the body fell off the end without an explicit
	// `return` (legal — a generator that just stops yielding). Same
	// done=true suspend-and-swap emitGeneratorReturn uses, only reached
	// when the body's last statement didn't already terminate the current
	// block (an explicit return already emitted its own terminator, in
	// which case blockDone is already true and this is skipped, same
	// "instructions after a terminator are dropped" convention every other
	// implicit-epilogue call site in this codebase already follows).
	if !e.blockDone {
		e.emitGeneratorSwapToCaller(gctx, e.emitScalarZero(info.ElemTy), true)
		e.emitTerminator("ret void")
	}

	e.functions.WriteString(fmt.Sprintf("\ndefine void %s() {\nentry:\n", info.BodyFuncName))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	return nil
}

// emitGeneratorNext implements `gen.next(value)`. Reads __done first: a
// generator that's already finished returns {value: T's zero, done: true}
// immediately with no swap at all — calling .next() again on a finished
// generator is a harmless no-op in real JS, not an error. Otherwise:
// allocas a fresh local ucontext_t (this call's own save point — valid only
// for this call's duration, unlike the generator's own long-lived __ctx),
// stores it into __callerCtx so the generator's own yield/return knows
// where to swap back to, repoints GC_stackbottom at the generator's own
// stack for the duration of the swap (gc mode only — mirrors
// runtime_http.go's identical connection-fiber launch/resume pattern
// exactly, generalized from its fixed global connection array to this
// call's own local save buffer), then swaps in.
func (e *Emitter) emitGeneratorNext(receiver ast.Expression, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	genVal, err := e.emitExpr(receiver)
	if err != nil {
		return Value{}, err
	}
	return e.emitGeneratorNextByValue(genVal.Ref, genTy, args, pos)
}

// emitGeneratorNextByValue is emitGeneratorNext's real implementation,
// taking an already-evaluated generator instance pointer instead of a
// receiver expression to evaluate — shared with emitForOfGenerator, which
// must evaluate `for (const x of gen()) {...}`'s own `gen()` call exactly
// once (constructing one generator instance) and then call .next()
// repeatedly against that same instance, not re-evaluate the iterable
// expression (and so construct a fresh, independent generator) on every
// iteration the way emitGeneratorNext's own receiver-expression form would.
func (e *Emitter) emitGeneratorNextByValue(genObj string, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: .next() takes at most 1 argument", pos.Line, pos.Col)
	}
	e.ensureGeneratorRuntime()
	elemTy := *genTy.GeneratorElemType
	resultTy := genNextResultType(elemTy)

	var sentVal Value
	if len(args) == 1 {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		sentVal = e.coerce(v, elemTy)
	} else {
		sentVal = e.emitScalarZero(elemTy)
	}
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, sentVal.Ty.IR, sentVal.Ref)

	alreadyDone := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	swapL := e.freshLabel("gen.swap")
	skipL := e.freshLabel("gen.skip")
	mergeL := e.freshLabel("gen.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", alreadyDone.Ref, skipL, swapL))

	e.emitLabel(swapL)
	ctxSize, _, _, _ := ucontextLayout()
	saveCtx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i8], align 16", saveCtx, ctxSize))
	e.storeGeneratorField(genObj, genTy, GeneratorCallerCtxField, "ptr", saveCtx)
	e.storeGeneratorField(genObj, genTy, GeneratorStartedField, "i1", "1")
	// The pending-object handoff global (ensureGeneratorRuntime's own doc
	// comment) — written unconditionally before every swap-in, not just
	// the first: only this generator's own first-ever entry actually reads
	// it (every later resume picks up mid-function with its own genObj
	// already live as an ordinary local), so writing it every time is
	// harmless and avoids needing to track "is this the first call"
	// separately from what __started already exists to record.
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_gen_pending_obj, align 8", genObj))

	gcSet, gcRestore := e.generatorGCStackbottomOps(genObj, genTy)
	if gcSet != "" {
		e.body.WriteString(gcSet)
	}
	ownCtx := e.loadGeneratorField(genObj, genTy, GeneratorCtxField)
	swaprc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @swapcontext(ptr %s, ptr %s)", swaprc, saveCtx, ownCtx.Ref))
	if gcRestore != "" {
		e.body.WriteString(gcRestore)
	}
	swapYielded := e.loadGeneratorField(genObj, genTy, GeneratorYieldedField)
	swapEndL := swapL
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// Calling .next() again on an already-finished generator is a no-op in
	// real JS returning {value: undefined, done: true} — this compiler has
	// no general "undefined" sentinel for a concrete scalar type (the same
	// established zero-value stand-in ADR-00164's optional params/
	// ADR-00157's calloc fix/ADR-00167's pop-on-empty-array all already
	// use), so a zero value here, not whatever this generator's own
	// __yielded field happens to still hold from its very last real
	// yield/return — that field is never reset once done, so reading it
	// again on a repeat call would silently return a stale, misleading
	// value instead of the "nothing more to give" zero this generator's
	// completion already established.
	e.emitLabel(skipL)
	skipZero := e.emitScalarZero(elemTy)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	yieldedReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", yieldedReg, elemTy.IR, swapYielded.Ref, swapEndL, skipZero.Ref, skipL))
	yielded := Value{Ref: yieldedReg, Ty: elemTy}
	done := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)

	resultReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", resultReg, resultTy.StructSize()))
	vIdx, _, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), resultReg, vIdx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, yielded.Ref, vGep, elemTy.Align()))
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), resultReg, dIdx))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", done.Ref, dGep))

	return Value{Ref: resultReg, Ty: resultTy}, nil
}

// generatorGCStackbottomOps returns the gc-mode-only GC_stackbottom
// repoint/restore IR text around a .next() call's own swapcontext — empty
// strings in manual mode. Mirrors runtime_http.go's identical
// connection-fiber launch/resume pattern exactly (repoint to the target
// fiber's own stack's high end before swapping in, restore to the real
// process stack right after the swap returns, unconditionally — covers
// both a mid-body yield and the generator running to completion in one
// shot, same reasoning as the HTTP case's own comment).
func (e *Emitter) generatorGCStackbottomOps(genObj string, genTy Type) (setOp, restoreOp string) {
	if !e.isGCMode() {
		return "", ""
	}
	stack := e.loadGeneratorField(genObj, genTy, GeneratorStackField)
	high := e.freshReg()
	setOp = fmt.Sprintf("  %s = getelementptr i8, ptr %s, i64 %d\n  store ptr %s, ptr @GC_stackbottom, align 8\n",
		high, stack.Ref, fiberStackBytes, high)
	origBottom := e.freshReg()
	restoreOp = fmt.Sprintf("  %s = load ptr, ptr @__kml_gc_orig_stackbottom, align 8\n  store ptr %s, ptr @GC_stackbottom, align 8\n",
		origBottom, origBottom)
	return setOp, restoreOp
}

// emitForOfGenerator implements `for (const x of gen()) {...}` — a fourth,
// independent for...of loop shape (distinct from the array/Map/Set path and
// from Stage 1a's class-`next(): T | null` sentinel case, ADR-00063):
// repeated .next() calls against one generator instance (genVal, evaluated
// once by the caller — s.Iterable may be an arbitrary expression like
// `gen()` itself, which must only ever construct one instance, not one per
// iteration) until its own `{value, done}` result says done, binding the
// loop variable to `.value` each time it doesn't. Mirrors
// emitForOfClassIterator's own alloca-then-reload shape for the per-
// iteration result (defensive: the result is computed inside cond, used
// again in body, across the several internal sub-labels
// emitGeneratorNextByValue's own suspend/resume swap emits in between) —
// same reasoning, applied to a ptr-typed result here instead of a scalar
// one.
func (e *Emitter) emitForOfGenerator(s *ast.ForOfStatement, genTy Type, genVal Value, condL, bodyL, incL, endL string) error {
	elemTy := *genTy.GeneratorElemType

	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultAlloca))

	varPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, elemTy.IR, elemTy.Align()))
	e.define(s.VarName, Symbol{Ptr: varPtr, Ty: elemTy})

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	nextVal, err := e.emitGeneratorNextByValue(genVal.Ref, genTy, nil, s.GetPos())
	if err != nil {
		return err
	}
	resultTy := nextVal.Ty
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nextVal.Ref, resultAlloca))
	resultReloaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", resultReloaded, resultAlloca))
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), resultReloaded, dIdx))
	doneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", doneReg, dGep))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doneReg, endL, bodyL))

	e.emitLabel(bodyL)
	resultForBody := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", resultForBody, resultAlloca))
	vIdx, _, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), resultForBody, vIdx))
	loaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, elemTy.IR, vGep, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, loaded, varPtr, elemTy.Align()))
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

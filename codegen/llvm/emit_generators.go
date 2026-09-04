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
	IsAsync      bool   // an `async function*` (TDD-00085) — .next() returns Promise<{value,done}>, the body may await
	GenTy        Type   // GeneratorType(ElemTy, ParamTypes) — the constructed instance's own type
	BodyFuncName string // mangled LLVM function name for the compiled body, e.g. "@__generator_body_gen_1"
	// ThisTy is the receiver type for a generator *method* (TDD-00063 Stage
	// 2b) — nil for a free-function generator. When set, GenTy carries a
	// __this slot and the body binds `this` from it at entry.
	ThisTy *Type
	// Captures are the enclosing-scope variables a *nested* generator closes over
	// (TDD-00094), gathered at the declaration's emission point. Empty for a
	// top-level generator. At construction the captured cells are boxed into the
	// instance's __env; the body binds each name to its cell through __env, so a
	// mutation of an enclosing `let` is seen by a later `.next()` (by reference).
	Captures []CapturedVar
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
	var elemTy Type
	if fd.ReturnType != nil {
		elemTy = e.resolveType(fd.ReturnType)
	} else {
		// No annotation: infer the element type from the body's own yield
		// expressions (TDD-00096 Part 2) — real-JS sources never carry the
		// annotation. Only the genuinely ambiguous case still rejects.
		inferred, ok := e.inferGeneratorElemType(fd, paramNames, paramTypes)
		if !ok {
			return nil, fmt.Errorf("%d:%d: generator function '%s' requires an explicit return type annotation — its yield expressions produce conflicting element types that don't join", fd.GetPos().Line, fd.GetPos().Col, fd.Name)
		}
		elemTy = inferred
	}
	e.generatorBodyCtr++
	return &GeneratorInfo{
		ParamTypes:   paramTypes,
		ParamNames:   paramNames,
		ElemTy:       elemTy,
		IsAsync:      fd.IsAsync,
		GenTy:        GeneratorType(elemTy, paramTypes, nil, fd.IsAsync),
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
	base.GenTy = GeneratorType(base.ElemTy, base.ParamTypes, &classTy, base.IsAsync)
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
	e.emitGlobal("@__kml_gen_pending_obj = internal thread_local global ptr null, align 8")
}

// storeGeneratorField GEPs+stores one field of a generator instance —
// shared by construction (emitGeneratorConstruction) and the body's own
// prologue/yield/return paths (emitGeneratorFunctionDecl/
// emitYieldExpression), all of which repeat the identical
// FieldIndex-then-GEP-then-store shape enough times to warrant one helper.
//
// An array-typed slot (the __yielded/__sent/__paramN fields of a generator
// yielding an array element type, ADR-00676) is laid out and stored as the
// inline { ptr, i64 } aggregate StructFieldIR reserves for it — the caller
// passes the array's aggregate Value.Ref (its established {data,len} shape),
// and the passed `ir` string ("ptr", the array *type*'s IR) is overridden
// with the field's storage IR so the 16-byte slot and the store agree. Every
// non-array field is unaffected (StructFieldIR == Type.IR there).
func (e *Emitter) storeGeneratorField(genObjReg string, genTy Type, field, ir, val string) {
	idx, fieldTy, _ := genTy.FieldIndex(field)
	if fieldTy.IsArray {
		ir = StructFieldIR(fieldTy)
	}
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, genTy.StructIR(), genObjReg, idx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, val, gep, fieldTy.Align()))
}

// loadGeneratorField GEPs+loads one field of a generator instance — see
// storeGeneratorField's own doc comment. An array-typed slot is loaded as the
// inline { ptr, i64 } aggregate (StructFieldIR), returned as an ordinary array
// Value whose Ref is that aggregate register (ADR-00676).
func (e *Emitter) loadGeneratorField(genObjReg string, genTy Type, field string) Value {
	idx, fieldTy, _ := genTy.FieldIndex(field)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, genTy.StructIR(), genObjReg, idx))
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, StructFieldIR(fieldTy), gep, fieldTy.Align()))
	return Value{Ref: reg, Ty: fieldTy}
}

// genZeroElem produces a zero value of a generator's element type for the
// "nothing more to give" / uninitialised-slot positions (ADR-00676). For an
// array element type this is the empty { null, 0 } aggregate (emitScalarZero
// only builds scalar/pointer zeros, and would produce a bare null ptr where
// the inline {ptr,i64} aggregate is required); every other type defers to
// emitScalarZero unchanged.
func (e *Emitter) genZeroElem(t Type) Value {
	if t.IsArray {
		agg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} { ptr null, i64 0 }, ptr null, 0", agg))
		return Value{Ref: agg, Ty: t}
	}
	return e.emitScalarZero(t)
}

// genUnpackArrayElemPattern destructures a `for (const [a, b] of gen())` /
// `for await` loop pattern whose element type is itself an array (ADR-00676) —
// `const [a, b]` over a yielded `number[]` binds a=arr[0], b=arr[1]. The
// element arrives as the inline {ptr,i64} aggregate; its data/len are extracted
// and fed to the shared array-pattern unpack core (the same one plain array
// destructuring uses). elemTy is the array element type (elemTy.ElemType is the
// per-slot type the bindings receive).
func (e *Emitter) genUnpackArrayElemPattern(agg string, elemTy Type, elems []ast.ArrayPatternElem) error {
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataReg, agg))
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, agg))
	return e.unpackArrayPatternInto(dataReg, lenReg, *elemTy.ElemType, elems)
}

// genLoadElemAt loads a generator element value (the {value} slot of a
// `{value,done}` result object, or an async request-queue sent-value slot)
// from an already-GEP'd address, as the inline {ptr,i64} aggregate for an
// array element type or a plain scalar/pointer load otherwise (ADR-00676).
func (e *Emitter) genLoadElemAt(gepReg string, elemTy Type) string {
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, StructFieldIR(elemTy), gepReg, elemTy.Align()))
	return reg
}

// genDefineLoopVar binds a `for (const x of gen())` / `for await` loop
// variable slot once, before the loop (ADR-00676). An array element type
// binds a stable header slot (alloca ptr, object-reference array model,
// TDD-00127) that each iteration re-points via genStoreLoopVar; every other
// type binds a plain alloca of its own IR. Returns the slot register.
func (e *Emitter) genDefineLoopVar(name string, elemTy Type) string {
	varPtr := e.freshReg()
	if elemTy.IsArray {
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", varPtr))
	} else {
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, elemTy.IR, elemTy.Align()))
	}
	e.define(name, Symbol{Ptr: varPtr, Ty: elemTy})
	return varPtr
}

// genStoreLoopVar writes one iteration's element aggregate into a loop-var
// slot made by genDefineLoopVar. An array element is boxed into a fresh
// {data,len} header whose pointer is stored into the stable ptr slot (a
// per-iteration reassignment, object-reference model); every other type
// stores its scalar/pointer value directly.
func (e *Emitter) genStoreLoopVar(varPtr string, elemTy Type, val string) {
	if elemTy.IsArray {
		header := e.boxArrayValue(Value{Ref: val, Ty: elemTy})
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, varPtr))
		return
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", elemTy.IR, val, varPtr, elemTy.Align()))
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
	zeroElem := e.genZeroElem(info.ElemTy)
	e.storeGeneratorField(genObj, genTy, GeneratorYieldedField, zeroElem.Ty.IR, zeroElem.Ref)
	zeroElem2 := e.genZeroElem(info.ElemTy)
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, zeroElem2.Ty.IR, zeroElem2.Ref)
	e.storeGeneratorField(genObj, genTy, GeneratorResumeModeField, "i64", "0")
	e.storeGeneratorField(genObj, genTy, GeneratorThrownField, "ptr", "null")
	// This generator's own isolated jmpbuf stack (16 frames * 512 bytes, matching
	// a coroutine task's), so its body's try-frames never interleave on the shared
	// global stack with the caller's own trys while the two fibers alternate
	// (TDD-00086). __jmpTop tracks its top across suspension; __genError carries an
	// uncaught body throw back to the caller.
	jmpStk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", jmpStk, 16*512))
	e.storeGeneratorField(genObj, genTy, GeneratorJmpStkField, "ptr", jmpStk)
	e.storeGeneratorField(genObj, genTy, GeneratorJmpTopField, "i64", "0")
	e.storeGeneratorField(genObj, genTy, GeneratorGenErrorField, "ptr", "null")
	e.storeGeneratorField(genObj, genTy, GeneratorPendingQField, "ptr", "null")
	e.storeGeneratorField(genObj, genTy, GeneratorParkedField, "i64", "0")
	e.storeGeneratorField(genObj, genTy, GeneratorReqHeadField, "ptr", "null")
	e.storeGeneratorField(genObj, genTy, GeneratorReqTailField, "ptr", "null")

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

	// A nested generator's closure env (TDD-00094): box each captured cell (shared
	// by reference with the enclosing scope, mirroring an arrow's capture) and
	// store the env pointer into __env. Each capture's *current* symbol is
	// re-resolved here (construction is at the `g()` call site, after the
	// declaration — the var may have been boxed by an intervening closure); the
	// type is fixed from the declaration. A top-level/non-capturing generator
	// stores a null __env.
	if len(info.Captures) > 0 {
		env := e.freshReg()
		envIR := envStructIR(info.Captures)
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", env, envStructSize(info.Captures)))
		for i, cap := range info.Captures {
			sym, ok := e.lookup(cap.Name)
			if !ok {
				return Value{}, fmt.Errorf("%d:%d: generator captures '%s' but it is not in scope at construction", pos.Line, pos.Col, cap.Name)
			}
			cellPtr := sym.Ptr
			if !sym.Boxed {
				cellPtr = e.promoteCaptureToCell(cap.Name, cap.Ty, sym.Ptr, sym.IsConst)
			}
			slotReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slotReg, envIR, env, i))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cellPtr, slotReg))
		}
		e.storeGeneratorField(genObj, genTy, GeneratorEnvField, "ptr", env)
	} else {
		e.storeGeneratorField(genObj, genTy, GeneratorEnvField, "ptr", "null")
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
		return e.emitYieldStar(ex)
	}
	var val Value
	if ex.Argument != nil {
		// Hint the element type so a `yield [a, b]` / `yield { … }` whose slot is
		// a tuple/object builds that shape directly rather than a bare array
		// aggregate the tuple slot can't hold (ADR-00257).
		v, err := e.emitExprWithObjectHint(ex.Argument, gctx.elemTy)
		if err != nil {
			return Value{}, err
		}
		val = e.coerce(v, gctx.elemTy)
	} else {
		val = e.genZeroElem(gctx.elemTy)
	}
	e.emitGeneratorSwapToCaller(gctx, val, false)
	return e.emitYieldResumeDispatch(gctx)
}

// emitYieldStar implements `yield* inner` inside a generator body (TDD-00086):
// it delegates to an inner generator, re-yielding each value the inner produces,
// forwarding each `.next(v)` sent value / `.throw()` / `.return()` into the inner,
// and evaluating to the inner generator's own return value. The inner must be a
// generator of the same element type — a general iterable needs `Symbol.iterator`
// (TDD-00044). An **async** inner is supported inside an **async** outer: each
// inner step returns a `Promise<{value,done}>` this awaits (ADR-00260's follow-on).
func (e *Emitter) emitYieldStar(ex *ast.YieldExpression) (Value, error) {
	gctx := e.currentGenerator
	if ex.Argument == nil {
		return Value{}, fmt.Errorf("%d:%d: yield* requires an operand", ex.GetPos().Line, ex.GetPos().Col)
	}
	innerTy := e.inferExprType(ex.Argument)
	if !innerTy.IsGenerator {
		// yield* over a user `Symbol.asyncIterator` iterable (TDD-00094) — only
		// inside an async generator, since awaiting each step needs an async
		// context. Delegates by re-yielding each awaited element.
		if innerTy.IsClass && gctx != nil && gctx.genTy.GeneratorIsAsync {
			if info, ok := e.classes[innerTy.ClassName]; ok {
				if _, ok := info.MethodSigs[asyncIteratorMethodName]; ok {
					return e.emitYieldStarAsyncIterable(gctx, ex, innerTy)
				}
			}
		}
		return Value{}, fmt.Errorf("%d:%d: yield* requires a generator operand, or a class with [Symbol.asyncIterator]() inside an async generator (TDD-00094); a general sync `Symbol.iterator` iterable is not yet supported", ex.GetPos().Line, ex.GetPos().Col)
	}
	isAsyncInner := innerTy.GeneratorIsAsync
	if isAsyncInner && !gctx.genTy.GeneratorIsAsync {
		return Value{}, fmt.Errorf("%d:%d: yield* over an async generator is only allowed inside an async generator (awaiting each step needs an async context)", ex.GetPos().Line, ex.GetPos().Col)
	}
	innerElem := *innerTy.GeneratorElemType
	if innerElem.IR != gctx.elemTy.IR {
		return Value{}, fmt.Errorf("%d:%d: yield* inner generator element type must match the outer generator's", ex.GetPos().Line, ex.GetPos().Col)
	}
	resultTy := genNextResultType(innerElem)

	// One inner step (.next/.throw/.return), returning the `{value,done}` result
	// object pointer — for an async inner it awaits the returned promise.
	stepNext := func(innerReg string, sent Value) string {
		if isAsyncInner {
			prom := e.emitAsyncGeneratorNextCore(innerReg, innerTy, sent)
			return e.emitAwaitSettledResult(prom.Ref, resultTy).Ref
		}
		return e.emitSyncGeneratorNextCore(innerReg, innerTy, sent).Ref
	}
	stepThrow := func(innerReg, errPtr string) (string, error) {
		if isAsyncInner {
			prom, err := e.emitAsyncGeneratorThrowByValue(innerReg, innerTy, errPtr, ex.GetPos())
			if err != nil {
				return "", err
			}
			return e.emitAwaitSettledResult(prom.Ref, resultTy).Ref, nil
		}
		res, err := e.emitGeneratorThrowByValue(innerReg, innerTy, errPtr, ex.GetPos())
		if err != nil {
			return "", err
		}
		return res.Ref, nil
	}
	stepReturn := func(innerReg string, rv Value) (string, error) {
		if isAsyncInner {
			prom, err := e.emitAsyncGeneratorReturnByValue(innerReg, innerTy, rv, ex.GetPos())
			if err != nil {
				return "", err
			}
			return e.emitAwaitSettledResult(prom.Ref, resultTy).Ref, nil
		}
		res, err := e.emitGeneratorReturnByValue(innerReg, innerTy, rv, ex.GetPos())
		if err != nil {
			return "", err
		}
		return res.Ref, nil
	}

	innerVal, err := e.emitExpr(ex.Argument)
	if err != nil {
		return Value{}, err
	}
	// Persist the inner instance + latest result across the outer's suspensions.
	innerPtrA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", innerPtrA))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", innerVal.Ref, innerPtrA))
	resultA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultA))

	loadResultField := func(field string, fieldTy Type) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, resultA))
		idx, _, _ := resultTy.FieldIndex(field)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, resultTy.StructIR(), r, idx))
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", out, StructFieldIR(fieldTy), gep, fieldTy.Align()))
		return out
	}

	// r = inner.next(undefined)
	inner0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", inner0, innerPtrA))
	r0 := stepNext(inner0, e.genZeroElem(innerElem))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", r0, resultA))

	condL := e.freshLabel("ystar.cond")
	bodyL := e.freshLabel("ystar.body")
	endL := e.freshLabel("ystar.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	doneReg := loadResultField("done", TypeBool)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doneReg, endL, bodyL))

	e.emitLabel(bodyL)
	valReg := loadResultField("value", innerElem)
	// Re-yield the inner value out of the outer generator. On resume, forward the
	// caller's action into the inner iterator: `.next(v)` sends v, `.throw(e)`
	// throws into the inner, `.return(v)` returns into the inner and then completes
	// the outer too. Each feeds a fresh `{value,done}` back into the loop (or, for a
	// completing return, ends the outer generator).
	e.emitGeneratorSwapToCaller(gctx, Value{Ref: valReg, Ty: gctx.elemTy}, false)
	innerN := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", innerN, innerPtrA))
	mode := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorResumeModeField)
	fwThrowL := e.freshLabel("ystar.fwthrow")
	fwReturnL := e.freshLabel("ystar.fwreturn")
	fwNextL := e.freshLabel("ystar.fwnext")
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [ i64 1, label %%%s i64 2, label %%%s ]", mode.Ref, fwNextL, fwThrowL, fwReturnL))

	// .throw(e): forward into inner.throw(e). If the inner catches and yields, its
	// result feeds the loop; if not, the inner throw re-throws into the outer (its
	// own try/catch-all handles it).
	e.emitLabel(fwThrowL)
	thrown := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorThrownField)
	rt, err := stepThrow(innerN, thrown.Ref)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rt, resultA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	// .return(v): forward into inner.return(v), then complete the outer with the
	// inner's (possibly finally-adjusted) return value.
	e.emitLabel(fwReturnL)
	retSent := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorSentField)
	rr, err := stepReturn(innerN, retSent)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rr, resultA))
	finalRet := loadResultField("value", innerElem)
	if err := e.emitPendingFinallys(); err != nil {
		return Value{}, err
	}
	e.emitGeneratorSwapToCaller(gctx, Value{Ref: finalRet, Ty: gctx.elemTy}, true)
	e.emitTerminator("ret void")

	// .next(v): send v into the inner's next step.
	e.emitLabel(fwNextL)
	sent := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorSentField)
	rn := stepNext(innerN, sent)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rn, resultA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	// The yield* expression's value is the inner generator's own return value.
	e.emitLabel(endL)
	finalReg := loadResultField("value", innerElem)
	return Value{Ref: finalReg, Ty: innerElem}, nil
}

// emitYieldStarAsyncIterable delegates `yield* obj` where obj is a user
// `Symbol.asyncIterator` iterable (TDD-00094), inside an async generator: get the
// iterator, then loop awaiting `it.next()` and re-yielding each `{value}` out of
// the outer generator until `done`. V1 forwards values only: a `.throw(e)` /
// `.return(v)` on the outer while suspended here runs the outer's *own* resume path
// (propagate the throw into the outer body / complete the outer with the returned
// value) rather than delegating into the inner async iterator's optional
// `.throw`/`.return` — a bounded divergence from JS's forwarding, but not a
// swallow. The reused iterator/await core is emitForAwaitOfAsyncIterable's.
func (e *Emitter) emitYieldStarAsyncIterable(gctx *generatorEmitCtx, ex *ast.YieldExpression, iterableTy Type) (Value, error) {
	pos := ex.GetPos()
	iterableVal, err := e.emitExpr(ex.Argument)
	if err != nil {
		return Value{}, err
	}
	iterVal, err := e.emitClassCall(iterableTy, iterableVal, asyncIteratorMethodName, nil, pos, false)
	if err != nil {
		return Value{}, err
	}
	if !iterVal.Ty.IsClass {
		return Value{}, fmt.Errorf("%d:%d: [Symbol.asyncIterator]() must return a class instance with a next() method (TDD-00094)", pos.Line, pos.Col)
	}
	iterTy := iterVal.Ty
	iterInfo, ok := e.classes[iterTy.ClassName]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: unknown iterator class '%s'", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	nextSig, ok := iterInfo.MethodSigs["next"]
	if !ok || !nextSig.RetType.IsPromise || nextSig.RetType.PromiseType == nil {
		return Value{}, fmt.Errorf("%d:%d: async iterator '%s'.next() must return a Promise<{value,done}> (TDD-00094)", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	resultTy := *nextSig.RetType.PromiseType
	_, elemTy, ok := resultTy.FieldIndex("value")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: async iterator '%s'.next() result has no 'value' field (TDD-00094)", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	if _, _, ok := resultTy.FieldIndex("done"); !ok {
		return Value{}, fmt.Errorf("%d:%d: async iterator '%s'.next() result has no 'done' field (TDD-00094)", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	if elemTy.IR != gctx.elemTy.IR {
		return Value{}, fmt.Errorf("%d:%d: yield* async-iterable element type must match the outer generator's", pos.Line, pos.Col)
	}

	// The iterator instance and latest result persist across the outer's suspensions.
	iterAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", iterAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", iterVal.Ref, iterAlloca))
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultAlloca))

	dIdx, _, _ := resultTy.FieldIndex("done")
	vIdx, _, _ := resultTy.FieldIndex("value")

	// A forwarded delegation step (`it.throw(e)` / `it.return(v)`) — call the inner
	// iterator's optional method with a runtime value, await the returned
	// `Promise<{value,done}>`, and stash the result. The method takes an
	// *expression* argument, so bind the runtime value to a fresh synthetic local
	// and hand emitClassCall an identifier for it (reusing its dispatch/coercion).
	e.asyncGenStepCtr++
	forwardCall := func(method string, arg Value) error {
		sig := iterInfo.MethodSigs[method]
		paramTy := arg.Ty
		if len(sig.ParamTypes) > 0 {
			paramTy = sig.ParamTypes[0]
		}
		coerced := e.coerce(arg, paramTy)
		tmp := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", tmp, paramTy.IR, paramTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", paramTy.IR, coerced.Ref, tmp, paramTy.Align()))
		argName := fmt.Sprintf("__ystar_ai_%s_arg_%d", method, e.asyncGenStepCtr)
		e.define(argName, Symbol{Ptr: tmp, Ty: paramTy})
		itReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", itReg, iterAlloca))
		p, err := e.emitClassCall(iterTy, Value{Ref: itReg, Ty: iterTy}, method, []ast.Expression{&ast.Identifier{Name: argName}}, pos, false)
		if err != nil {
			return err
		}
		res, err := e.emitAwaitTaskPromise(p.Ref, resultTy)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", res.Ref, resultAlloca))
		return nil
	}
	// canForward reports whether the inner iterator has a delegatable `method`
	// (present and returning a Promise<{value,done}>-shaped result).
	canForward := func(method string) bool {
		sig, ok := iterInfo.MethodSigs[method]
		return ok && sig.RetType.IsPromise && sig.RetType.PromiseType != nil
	}

	condL := e.freshLabel("ystar.ai.cond")
	procL := e.freshLabel("ystar.ai.proc")
	bodyL := e.freshLabel("ystar.ai.body")
	endL := e.freshLabel("ystar.ai.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	// condL: it.next() — await a fresh step, then process it.
	e.emitLabel(condL)
	iterReloaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", iterReloaded, iterAlloca))
	prom, err := e.emitClassCall(iterTy, Value{Ref: iterReloaded, Ty: iterTy}, "next", nil, pos, false)
	if err != nil {
		return Value{}, err
	}
	resultObj, err := e.emitAwaitTaskPromise(prom.Ref, resultTy)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", resultObj.Ref, resultAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", procL))

	// procL: a fresh {value,done} sits in resultAlloca (from next/throw/return) —
	// finish the delegation if done, else re-yield its value.
	e.emitLabel(procL)
	rReload := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rReload, resultAlloca))
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), rReload, dIdx))
	doneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", doneReg, dGep))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", doneReg, endL, bodyL))

	e.emitLabel(bodyL)
	rForVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rForVal, resultAlloca))
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), rForVal, vIdx))
	valReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valReg, StructFieldIR(elemTy), vGep, elemTy.Align()))
	e.emitGeneratorSwapToCaller(gctx, Value{Ref: valReg, Ty: gctx.elemTy}, false)
	// On resume, dispatch on how the outer was re-entered (the resumer set
	// __resumeMode before swapping back in). Mode 0 loops for the next element;
	// mode 1/2 delegate `.throw`/`.return` into the inner iterator's own method
	// when it has one (feeding the fresh result back into procL — so the inner may
	// catch a throw and keep yielding, or run its own cleanup), and otherwise run
	// the outer's own path (re-throw at this point / complete the outer with __sent).
	mode := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorResumeModeField)
	aiThrowL := e.freshLabel("ystar.ai.throw")
	aiReturnL := e.freshLabel("ystar.ai.return")
	aiNextL := e.freshLabel("ystar.ai.next")
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [ i64 1, label %%%s i64 2, label %%%s ]", mode.Ref, aiNextL, aiThrowL, aiReturnL))

	e.emitLabel(aiThrowL)
	thrown := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorThrownField)
	if canForward("throw") {
		if err := forwardCall("throw", thrown); err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", procL))
	} else {
		e.ensureExceptionHelpers()
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", thrown.Ref))
		e.emitTerminator("unreachable")
	}

	e.emitLabel(aiReturnL)
	rv := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorSentField)
	if canForward("return") {
		// Forward into the inner's return; if it honors it (done), complete the
		// outer with the inner's value, else re-yield and keep going (procL).
		if err := forwardCall("return", Value{Ref: rv.Ref, Ty: elemTy}); err != nil {
			return Value{}, err
		}
		rr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rr, resultAlloca))
		rdGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", rdGep, resultTy.StructIR(), rr, dIdx))
		rdone := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", rdone, rdGep))
		retDoneL := e.freshLabel("ystar.ai.retdone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", rdone, retDoneL, procL))
		e.emitLabel(retDoneL)
		rvGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", rvGep, resultTy.StructIR(), rr, vIdx))
		rvVal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", rvVal, StructFieldIR(elemTy), rvGep, elemTy.Align()))
		if err := e.emitPendingFinallys(); err != nil {
			return Value{}, err
		}
		e.emitGeneratorSwapToCaller(gctx, Value{Ref: rvVal, Ty: gctx.elemTy}, true)
		e.emitTerminator("ret void")
	} else {
		if err := e.emitPendingFinallys(); err != nil {
			return Value{}, err
		}
		e.emitGeneratorSwapToCaller(gctx, rv, true)
		e.emitTerminator("ret void")
	}

	e.emitLabel(aiNextL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return e.genZeroElem(gctx.elemTy), nil
}

// emitYieldResumeDispatch emits the post-swap resume handling shared by every
// `yield` (TDD-00086): the resumer set __resumeMode before swapping back in, and
// this dispatches on it — mode 0 (`.next(v)`) returns __sent as the yield
// expression's value; mode 1 (`.throw(e)`) throws __thrown at the suspension
// point (a body try/catch handles it, else it propagates to the .throw() caller);
// mode 2 (`.return(v)`) runs enclosing finally blocks and completes the generator
// with __sent. Modes 1/2 terminate the current block; execution continues only on
// the mode-0 path, whose __sent value this returns.
func (e *Emitter) emitYieldResumeDispatch(gctx *generatorEmitCtx) (Value, error) {
	mode := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorResumeModeField)
	throwL := e.freshLabel("yield.throw")
	returnL := e.freshLabel("yield.return")
	nextL := e.freshLabel("yield.next")
	e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [ i64 1, label %%%s i64 2, label %%%s ]", mode.Ref, nextL, throwL, returnL))

	// mode 1: throw the injected error at the suspension point.
	e.emitLabel(throwL)
	e.ensureExceptionHelpers()
	thrown := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorThrownField)
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", thrown.Ref))
	e.emitTerminator("unreachable")

	// mode 2: run enclosing finallys, then complete the generator with __sent.
	e.emitLabel(returnL)
	rv := e.loadGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorSentField)
	if err := e.emitPendingFinallys(); err != nil {
		return Value{}, err
	}
	e.emitGeneratorSwapToCaller(gctx, rv, true)
	e.emitTerminator("ret void")

	// mode 0: the ordinary resume — __sent is this yield expression's value.
	e.emitLabel(nextL)
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
		v, err := e.emitExprWithObjectHint(r.Value, gctx.elemTy)
		if err != nil {
			return err
		}
		val = e.coerce(v, gctx.elemTy)
	} else {
		val = e.genZeroElem(gctx.elemTy)
	}
	// A `return` inside a `try/finally` must run the enclosing finally blocks
	// before it completes the generator — the same emitPendingFinallys an
	// ordinary function return makes (TDD-00086; previously skipped here, so a
	// generator's finally never ran on an explicit return).
	if err := e.emitPendingFinallys(); err != nil {
		return err
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
	savedPendingFrees, savedBreakFreeScope, savedContinueFreeScope := e.pendingFrees, e.breakFreeScope, e.continueFreeScope
	e.pendingFrees, e.breakFreeScope, e.continueFreeScope = nil, nil, nil
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
		e.pendingFrees, e.breakFreeScope, e.continueFreeScope = savedPendingFrees, savedBreakFreeScope, savedContinueFreeScope
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

	// An async generator's body may `await`: pull in the task runtime and mark the
	// program may-suspend so those awaits emit the scheduler-driven path (a fetch
	// await busy-spins; a task-promise await drives the scheduler) — TDD-00085.
	if info.IsAsync {
		e.hasMaySuspend = true
		e.ensureTaskRuntime()
	}

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

	// Nested-generator captures (TDD-00094): read the closure env from __env and
	// bind each captured name to its shared heap cell as a Boxed symbol, so the
	// body reads/writes the enclosing variable by reference — a mutation is seen
	// across `.next()` and by the enclosing scope, matching JS.
	if len(info.Captures) > 0 {
		envVal := e.loadGeneratorField(genObjReg, info.GenTy, GeneratorEnvField)
		envIR := envStructIR(info.Captures)
		for i, cap := range info.Captures {
			slotReg := e.freshReg()
			cellReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slotReg, envIR, envVal.Ref, i))
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cellReg, slotReg))
			e.define(cap.Name, Symbol{Ptr: cellReg, Ty: cap.Ty, Boxed: true, IsConst: cap.Sym.IsConst})
		}
	}

	// Outer catch-all (TDD-00086): a body throw that escapes every enclosing
	// `try` — or the injected throw of a `.throw()` with no handler, or a throwing
	// `finally` under `.return()` — longjmps here (this jmpbuf is the bottom-most
	// frame of the generator's own isolated stack). It records the error in
	// __genError and swaps back done=true; the resumer re-throws it on the caller
	// side (a sync generator) or rejects the `.next()`/`.throw()`/`.return()`
	// promise (an async generator), where the caller's own jmpbuf stack — not this
	// fiber's — is active. Emitted for async generators too now, replacing their
	// former per-`.next()` setjmp guard.
	{
		e.ensureExceptionHelpers()
		outerJb := e.freshReg()
		sj := e.freshReg()
		threw := e.freshReg()
		bodyStartL := e.freshLabel("gen.body")
		catchAllL := e.freshLabel("gen.catchall")
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", outerJb))
		e.emitInstr(fmt.Sprintf("%s = call i32 @setjmp(ptr %s)", sj, outerJb))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", threw, sj))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", threw, catchAllL, bodyStartL))

		e.emitLabel(catchAllL)
		// Reload genObj from the handoff global — a longjmp may have clobbered the
		// entry-block SSA register, and no other generator ran on this fiber since
		// the swap-in that wrote it (ensureGeneratorRuntime's own reasoning).
		reGen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_gen_pending_obj, align 8", reGen))
		errReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_get_thrown()", errReg))
		e.storeGeneratorField(reGen, info.GenTy, GeneratorGenErrorField, "ptr", errReg)
		zeroY := e.genZeroElem(info.ElemTy)
		e.storeGeneratorField(reGen, info.GenTy, GeneratorYieldedField, zeroY.Ty.IR, zeroY.Ref)
		e.storeGeneratorField(reGen, info.GenTy, GeneratorDoneField, "i1", "1")
		callerCtx := e.loadGeneratorField(reGen, info.GenTy, GeneratorCallerCtxField)
		ownCtx := e.loadGeneratorField(reGen, info.GenTy, GeneratorCtxField)
		swaprc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @swapcontext(ptr %s, ptr %s)", swaprc, ownCtx.Ref, callerCtx.Ref))
		e.emitTerminator("ret void")

		e.emitLabel(bodyStartL)
	}

	if err := e.pushNestedFuncScope(decl.Params, decl.Body.Body); err != nil {
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
		e.emitGeneratorSwapToCaller(gctx, e.genZeroElem(info.ElemTy), true)
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
// emitGeneratorSwapIntoBody emits the caller-side swap into a suspended generator
// shared by `.next()`/`.throw()`/`.return()` (TDD-00086): it allocates this call's
// save buffer, hands off genObj, activates the generator's own isolated jmpbuf
// stack for the duration of the swap (so the body's try-frames never interleave
// with the caller's on the shared global stack), restores the caller's jmpbuf
// stack afterward, and re-throws on the caller side any error the body's outer
// catch-all recorded in __genError. Leaves the emitter positioned in a fresh live
// block whose label it returns (for a subsequent phi).
// emitGeneratorSwapCore swaps into the generator's fiber with its own isolated
// jmpbuf stack active (TDD-00086), leaving the emitter in the block right after
// the swap returns. It does NOT handle __genError — the sync path re-throws it,
// the async path rejects its promise (TDD-00086 async extension). When
// resetCurrentTask is set (an async generator), __kml_current_task is saved to a
// stack slot and nulled for the body's run (so its awaits busy-drive rather than
// mis-park the consumer) and restored afterward.
func (e *Emitter) emitGeneratorSwapCore(genObj string, genTy Type, resetCurrentTask bool) {
	e.ensureExceptionHelpers()
	ctxSize, _, _, _ := ucontextLayout()
	saveCtx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i8], align 16", saveCtx, ctxSize))
	e.storeGeneratorField(genObj, genTy, GeneratorCallerCtxField, "ptr", saveCtx)
	e.storeGeneratorField(genObj, genTy, GeneratorStartedField, "i1", "1")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_gen_pending_obj, align 8", genObj))

	// Save current_task (survives a longjmp) for an async generator.
	ctSlot := ""
	if resetCurrentTask {
		ctSlot = e.freshReg()
		ct0 := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ctSlot))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_current_task, align 8", ct0))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ct0, ctSlot))
	}

	// Activate the generator's own jmpbuf stack (save the caller's first).
	callerStk := e.freshReg()
	callerTop := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_cur_jmp_stk, align 8", callerStk))
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr @__kml_jmp_top, align 4", callerTop))
	genStk := e.loadGeneratorField(genObj, genTy, GeneratorJmpStkField)
	genTop64 := e.loadGeneratorField(genObj, genTy, GeneratorJmpTopField)
	genTop32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i32", genTop32, genTop64.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_cur_jmp_stk, align 8", genStk.Ref))
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr @__kml_jmp_top, align 4", genTop32))

	if resetCurrentTask {
		e.emitInstr("store ptr null, ptr @__kml_current_task, align 8")
	}

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

	if resetCurrentTask {
		ctR := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ctR, ctSlot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_current_task, align 8", ctR))
	}

	// Save the generator's jmpbuf top across its suspension, restore the caller's.
	newTop := e.freshReg()
	newTop64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr @__kml_jmp_top, align 4", newTop))
	e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", newTop64, newTop))
	e.storeGeneratorField(genObj, genTy, GeneratorJmpTopField, "i64", newTop64)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr @__kml_cur_jmp_stk, align 8", callerStk))
	e.emitInstr(fmt.Sprintf("store i32 %s, ptr @__kml_jmp_top, align 4", callerTop))
}

func (e *Emitter) emitGeneratorSwapIntoBody(genObj string, genTy Type) string {
	e.emitGeneratorSwapCore(genObj, genTy, false)

	// Re-throw an uncaught body error on the caller side (its own jmpbuf stack is
	// active again now).
	genErr := e.loadGeneratorField(genObj, genTy, GeneratorGenErrorField)
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isErr, genErr.Ref))
	rethrowL := e.freshLabel("gen.rethrow")
	contL := e.freshLabel("gen.cont")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, rethrowL, contL))
	e.emitLabel(rethrowL)
	e.storeGeneratorField(genObj, genTy, GeneratorGenErrorField, "ptr", "null")
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", genErr.Ref))
	e.emitTerminator("unreachable")
	e.emitLabel(contL)
	return contL
}

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
	if genTy.GeneratorIsAsync {
		return e.emitAsyncGeneratorNextByValue(genObj, genTy, args, pos)
	}
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: .next() takes at most 1 argument", pos.Line, pos.Col)
	}
	e.ensureGeneratorRuntime()
	elemTy := *genTy.GeneratorElemType

	var sentVal Value
	if len(args) == 1 {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		sentVal = e.coerce(v, elemTy)
	} else {
		sentVal = e.genZeroElem(elemTy)
	}
	return e.emitSyncGeneratorNextCore(genObj, genTy, sentVal), nil
}

// emitSyncGeneratorNextCore drives a suspended sync generator one step forward
// with an already-evaluated sent value and returns its `{value, done}` result —
// the shared core of `.next()` and of `yield*`'s delegation loop (TDD-00086),
// which feeds each sent value straight through as a Value rather than an
// expression.
func (e *Emitter) emitSyncGeneratorNextCore(genObj string, genTy Type, sentVal Value) Value {
	elemTy := *genTy.GeneratorElemType
	resultTy := genNextResultType(elemTy)
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, sentVal.Ty.IR, sentVal.Ref)
	// Ordinary resume: the yield returns the sent value (TDD-00086 mode 0).
	e.storeGeneratorField(genObj, genTy, GeneratorResumeModeField, "i64", "0")

	alreadyDone := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	swapL := e.freshLabel("gen.swap")
	skipL := e.freshLabel("gen.skip")
	mergeL := e.freshLabel("gen.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", alreadyDone.Ref, skipL, swapL))

	e.emitLabel(swapL)
	// The swap into the body (isolated jmpbuf stack + uncaught-error re-throw)
	// ends in a fresh block; the phi below reads __yielded from there.
	swapEndL := e.emitGeneratorSwapIntoBody(genObj, genTy)
	swapYielded := e.loadGeneratorField(genObj, genTy, GeneratorYieldedField)
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
	skipZero := e.genZeroElem(elemTy)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	yieldedReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", yieldedReg, StructFieldIR(elemTy), swapYielded.Ref, swapEndL, skipZero.Ref, skipL))
	yielded := Value{Ref: yieldedReg, Ty: elemTy}
	done := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	return e.buildGenNextResult(resultTy, elemTy, yielded, done)
}

// buildGenNextResult mallocs and populates a `{value, done}` result object from an
// already-loaded yielded value and done flag — shared by `.next()` and `.throw()`
// (TDD-00086).
func (e *Emitter) buildGenNextResult(resultTy, elemTy Type, yielded, done Value) Value {
	resultReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", resultReg, resultTy.StructSize()))
	vIdx, _, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), resultReg, vIdx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(elemTy), yielded.Ref, vGep, elemTy.Align()))
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), resultReg, dIdx))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", done.Ref, dGep))
	return Value{Ref: resultReg, Ty: resultTy}
}

// emitGeneratorThrow implements `gen.throw(e)` (TDD-00086 Stage 1): it injects the
// error at the generator's current suspension point — a body `try/catch` around
// the `yield` handles it (the generator resumes there and may yield/return again),
// and an uncaught throw propagates to the `.throw()` caller and finishes the
// generator. Evaluates the receiver + error, then defers to the by-value form
// (shared with `yield*` forwarding, Stage 4).
func (e *Emitter) emitGeneratorThrow(receiver ast.Expression, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	genVal, err := e.emitExpr(receiver)
	if err != nil {
		return Value{}, err
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: .throw() expects 1 argument", pos.Line, pos.Col)
	}
	ev, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	errPtr, err := e.errorPtrFromValue(ev)
	if err != nil {
		return Value{}, err
	}
	return e.emitGeneratorThrowByValue(genVal.Ref, genTy, errPtr, pos)
}

// emitGeneratorThrowByValue is emitGeneratorThrow's core over an already-evaluated
// instance pointer and error pointer. Mirrors emitAsyncGeneratorNextByValue's
// setjmp-bracketed swap: a throw uncaught in the body longjmps back here (the
// jmpbuf this pushes is the innermost one when the body has no enclosing `try`),
// where the generator is marked done and the error re-thrown to the caller.
func (e *Emitter) emitGeneratorThrowByValue(genObj string, genTy Type, errPtr string, pos ast.Pos) (Value, error) {
	if genTy.GeneratorIsAsync {
		return e.emitAsyncGeneratorThrowByValue(genObj, genTy, errPtr, pos)
	}
	e.ensureGeneratorRuntime()
	e.ensureExceptionHelpers()
	elemTy := *genTy.GeneratorElemType
	resultTy := genNextResultType(elemTy)

	e.storeGeneratorField(genObj, genTy, GeneratorThrownField, "ptr", errPtr)
	e.storeGeneratorField(genObj, genTy, GeneratorResumeModeField, "i64", "1")

	// suspended = started && !done. A not-started or already-done generator does
	// not run — the error goes straight to the caller.
	started := e.loadGeneratorField(genObj, genTy, GeneratorStartedField)
	done := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	notDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notDone, done.Ref))
	suspended := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", suspended, started.Ref, notDone))
	swapL := e.freshLabel("throw.swap")
	propL := e.freshLabel("throw.prop")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", suspended, swapL, propL))

	// not started / already done: throw straight to the caller (the generator
	// never handled it), completing it.
	e.emitLabel(propL)
	e.storeGeneratorField(genObj, genTy, GeneratorDoneField, "i1", "1")
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errPtr))
	e.emitTerminator("unreachable")

	// suspended: resume in mode 1 — the yield-point handler throws __thrown, which
	// the body's own try/catch handles (it may yield/return again) or the outer
	// catch-all captures into __genError for the swap helper to re-throw here.
	e.emitLabel(swapL)
	e.emitGeneratorSwapIntoBody(genObj, genTy)
	yielded := e.loadGeneratorField(genObj, genTy, GeneratorYieldedField)
	doneAfter := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	return e.buildGenNextResult(resultTy, elemTy, yielded, doneAfter), nil
}

// emitGeneratorReturnMethod implements `gen.return(v)` (TDD-00086 Stage 2): it
// completes the generator as if `return v` ran at the suspension point, running
// any enclosing `finally` blocks, and yields `{value: v, done: true}`. Evaluates
// the receiver + value, then defers to the by-value form (Stage 4 forwarding).
func (e *Emitter) emitGeneratorReturnMethod(receiver ast.Expression, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	genVal, err := e.emitExpr(receiver)
	if err != nil {
		return Value{}, err
	}
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: .return() takes at most 1 argument", pos.Line, pos.Col)
	}
	elemTy := *genTy.GeneratorElemType
	var rv Value
	if len(args) == 1 {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		rv = e.coerce(v, elemTy)
	} else {
		rv = e.genZeroElem(elemTy)
	}
	return e.emitGeneratorReturnByValue(genVal.Ref, genTy, rv, pos)
}

// emitGeneratorReturnByValue is emitGeneratorReturnMethod's core over an evaluated
// instance pointer + return value. A not-started or done generator completes
// immediately with `{value: v, done: true}` and never runs the body. A suspended
// generator resumes in mode 2, whose yield-point handler runs enclosing finallys
// then completes with v (emitYieldResumeDispatch); a `finally` that itself throws
// is captured by the body's outer catch-all and re-thrown to the caller by the
// swap helper.
func (e *Emitter) emitGeneratorReturnByValue(genObj string, genTy Type, rv Value, pos ast.Pos) (Value, error) {
	if genTy.GeneratorIsAsync {
		return e.emitAsyncGeneratorReturnByValue(genObj, genTy, rv, pos)
	}
	e.ensureGeneratorRuntime()
	e.ensureExceptionHelpers()
	elemTy := *genTy.GeneratorElemType
	resultTy := genNextResultType(elemTy)
	trueVal := Value{Ref: "true", Ty: TypeBool}

	e.storeGeneratorField(genObj, genTy, GeneratorSentField, rv.Ty.IR, rv.Ref)
	e.storeGeneratorField(genObj, genTy, GeneratorResumeModeField, "i64", "2")

	started := e.loadGeneratorField(genObj, genTy, GeneratorStartedField)
	done := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	notDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notDone, done.Ref))
	suspended := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", suspended, started.Ref, notDone))
	swapL := e.freshLabel("ret.swap")
	propL := e.freshLabel("ret.prop")
	mergeL := e.freshLabel("ret.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", suspended, swapL, propL))

	// not started / done: complete now with {value: v, done: true}, no body run.
	e.emitLabel(propL)
	e.storeGeneratorField(genObj, genTy, GeneratorDoneField, "i1", "1")
	resProp := e.buildGenNextResult(resultTy, elemTy, rv, trueVal)
	propEndL := e.freshLabel("ret.propend")
	e.emitTerminator(fmt.Sprintf("br label %%%s", propEndL))
	e.emitLabel(propEndL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// suspended: resume in mode 2 — the yield-point handler runs enclosing
	// finallys then completes with __sent (emitYieldResumeDispatch).
	e.emitLabel(swapL)
	e.emitGeneratorSwapIntoBody(genObj, genTy)
	yielded := e.loadGeneratorField(genObj, genTy, GeneratorYieldedField)
	doneAfter := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	resSwap := e.buildGenNextResult(resultTy, elemTy, yielded, doneAfter)
	doswapEndL := e.freshLabel("ret.doswapend")
	e.emitTerminator(fmt.Sprintf("br label %%%s", doswapEndL))
	e.emitLabel(doswapEndL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	resReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", resReg, resProp.Ref, propEndL, resSwap.Ref, doswapEndL))
	return Value{Ref: resReg, Ty: resultTy}, nil
}

// emitAsyncGeneratorNextByValue emits `.next(v)` on an async generator (TDD-00085
// Stage 2): it runs the generator to its next `yield`/`return`, driving the
// body's `await`s synchronously (current_task is reset to null across the swap so
// they busy-drive rather than mis-park the consumer), and returns an
// already-settled `Promise<{value,done}>` — fulfilled with the yielded/returned
// result, or rejected if the body threw. A `for await...of` consumer awaits it.
// emitAsyncGenRuntimeSetup pulls in the runtime an async generator resume needs.
func (e *Emitter) emitAsyncGenRuntimeSetup() {
	e.ensureGeneratorRuntime()
	e.ensureTaskRuntime()
	e.ensureExceptionHelpers()
	e.hasMaySuspend = true
}

// emitAsyncGenRejectPromise stores errPtr as promise q's rejection reason.
func (e *Emitter) emitAsyncGenRejectPromise(q, errPtr string) {
	errBits := e.freshReg()
	v0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", errBits, errPtr))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0, promiseStructIR, q))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", errBits, v0))
	// Reject through __kml_promise_settle so a parked awaiter (deferred .next())
	// is woken; a no-op drain in the synchronous no-waiter case.
	e.ensurePromiseSettle()
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 2)", q))
}

// emitAsyncGenSwapAndSettle runs a suspended async generator one resume step
// (isolated jmpbuf stack + current_task juggle) and settles the promise q:
// fulfilled with the `{value,done}` result, or — when the body's outer catch-all
// captured an uncaught throw into __genError — rejected with that error
// (TDD-00086 async extension). Leaves the emitter in a live block.
// emitAsyncGenSettleFromFields settles q from the generator's post-swap state:
// a captured body error rejects q, else q fulfils with {__yielded, __done} —
// always through __kml_promise_settle (the ADR-00275 rule: reactions/parked
// awaiters must be woken).
func (e *Emitter) emitAsyncGenSettleFromFields(genObj string, genTy Type, elemTy, resultTy Type, q string) {
	genErr := e.loadGeneratorField(genObj, genTy, GeneratorGenErrorField)
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isErr, genErr.Ref))
	rejL := e.freshLabel("agen.rej")
	fulL := e.freshLabel("agen.ful")
	mrgL := e.freshLabel("agen.settled")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, rejL, fulL))
	e.emitLabel(rejL)
	e.storeGeneratorField(genObj, genTy, GeneratorGenErrorField, "ptr", "null")
	e.emitAsyncGenRejectPromise(q, genErr.Ref)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mrgL))
	e.emitLabel(fulL)
	yielded := e.loadGeneratorField(genObj, genTy, GeneratorYieldedField)
	e.settleAsyncGenResult(q, genObj, genTy, resultTy, elemTy, yielded)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mrgL))
	e.emitLabel(mrgL)
}

// emitAsyncGenAwaitParkUntilSettled is emitted INSIDE an async generator's body
// (the fiber): it parks the current step on promise p — attach a {stepFn,
// genObj} resume closure to p's reaction list (or enqueue it onto the microtask
// FIFO now when p is already settled: awaiting a settled promise still defers,
// as in JS), set __parked, and swap back to the step's caller, leaving the
// step's q pending. When p settles, its reaction drain enqueues the resume,
// which swaps back into the fiber right here; __parked clears and p is settled.
// No __yielded/__done stores (unlike a yield's swap-out) and no jmpbuf/GC
// bookkeeping (the resumer's swapCore owns that, same as for yields).
func (e *Emitter) emitAsyncGenAwaitParkUntilSettled(pReg string) {
	gctx := e.currentGenerator
	genObj := gctx.genObjReg
	genTy := gctx.genTy
	e.ensureMicrotasks()
	e.ensureMalloc()
	stepFn := e.ensureAsyncGenStepFn(genTy)

	clo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	cf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", cf, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", stepFn, cf))
	ce := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", ce, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", genObj, ce))

	// Settled → enqueue the resume now; pending → attach a reaction node.
	sP := e.freshReg()
	sV := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", sP, promiseStructIR, pReg))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sV, sP))
	settled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", settled, sV))
	nowL := e.freshLabel("agpark.now")
	attachL := e.freshLabel("agpark.attach")
	parkL := e.freshLabel("agpark.park")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", settled, nowL, attachL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void @__kml_microtask_enqueue(ptr %s)", clo))
	e.emitTerminator(fmt.Sprintf("br label %%%s", parkL))
	e.emitLabel(attachL)
	node := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", node))
	rxP := e.freshReg()
	oldHead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", rxP, promiseStructIR, pReg))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", oldHead, rxP))
	nClo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", nClo, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", clo, nClo))
	nNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", nNext, node))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", oldHead, nNext))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", node, rxP))
	e.emitTerminator(fmt.Sprintf("br label %%%s", parkL))

	e.emitLabel(parkL)
	e.storeGeneratorField(genObj, genTy, GeneratorParkedField, "i64", "1")
	callerCtx := e.loadGeneratorField(genObj, genTy, GeneratorCallerCtxField)
	ownCtx := e.loadGeneratorField(genObj, genTy, GeneratorCtxField)
	swaprc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @swapcontext(ptr %s, ptr %s)", swaprc, ownCtx.Ref, callerCtx.Ref))
	// Resumed by the step fn: the awaited promise is settled now.
	e.storeGeneratorField(genObj, genTy, GeneratorParkedField, "i64", "0")
}

// asyncGenReqNodeIR is the queued-request node layout: {i64 resumeMode,
// ptr sentSlot (malloc'd elem-typed spill, freed at pop; null for .throw),
// ptr thrown, ptr q, ptr next}.
const asyncGenReqNodeIR = "{ i64, ptr, ptr, ptr, ptr }"

// emitAsyncGenSubmitRequest appends a {mode, sent, thrown, q} request to the
// generator's FIFO — called by .next/.throw/.return when a step is in flight
// (the spec's AsyncGeneratorEnqueue). sentVal may be the zero value for .throw.
func (e *Emitter) emitAsyncGenSubmitRequest(genObj string, genTy Type, elemTy Type, mode string, sentVal Value, thrown string, q string) {
	e.ensureMalloc()
	node := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 40)", node))
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", slot, StructFieldSize(elemTy)))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(elemTy), sentVal.Ref, slot, elemTy.Align()))
	storeAt := func(idx int, ir, val string) {
		gp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gp, asyncGenReqNodeIR, node, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, val, gp))
	}
	storeAt(0, "i64", mode)
	storeAt(1, "ptr", slot)
	storeAt(2, "ptr", thrown)
	storeAt(3, "ptr", q)
	storeAt(4, "ptr", "null")
	// Append: empty queue → head = tail = node; else tail.next = node, tail = node.
	head := e.loadGeneratorField(genObj, genTy, GeneratorReqHeadField)
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isEmpty, head.Ref))
	emptyL := e.freshLabel("agenq.empty")
	tailL := e.freshLabel("agenq.tail")
	doneL := e.freshLabel("agenq.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEmpty, emptyL, tailL))
	e.emitLabel(emptyL)
	e.storeGeneratorField(genObj, genTy, GeneratorReqHeadField, "ptr", node)
	e.storeGeneratorField(genObj, genTy, GeneratorReqTailField, "ptr", node)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(tailL)
	tail := e.loadGeneratorField(genObj, genTy, GeneratorReqTailField)
	tn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", tn, asyncGenReqNodeIR, tail.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", node, tn))
	e.storeGeneratorField(genObj, genTy, GeneratorReqTailField, "ptr", node)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// emitAsyncGeneratorThrowByValue implements `.throw(e)` on an async generator
// (TDD-00086): it injects the error at the suspension point and returns a settled
// `Promise<{value,done}>` — fulfilled if the body caught the throw and yielded/
// returned, rejected otherwise. A not-started or done generator rejects with e.
func (e *Emitter) emitAsyncGeneratorThrowByValue(genObj string, genTy Type, errPtr string, pos ast.Pos) (Value, error) {
	e.emitAsyncGenRuntimeSetup()
	elemTy := *genTy.GeneratorElemType
	resultTy := genNextResultType(elemTy)

	// A not-started, idle generator rejects immediately without running the
	// body (and is marked done). Otherwise the request goes through the shared
	// submit-or-start: a busy generator queues it (serviced when the in-flight
	// step settles — the spec never injects into a step mid-await); an idle
	// suspended one swaps in with resume mode 1; an idle done one rejects via
	// the step fn's drained-queue dispatch.
	q := e.freshReg()
	pending := e.loadGeneratorField(genObj, genTy, GeneratorPendingQField)
	idle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", idle, pending.Ref))
	started := e.loadGeneratorField(genObj, genTy, GeneratorStartedField)
	notStarted := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notStarted, started.Ref))
	freshRej := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", freshRej, idle, notStarted))
	rejL := e.freshLabel("athrow.rej")
	submitL := e.freshLabel("athrow.submit")
	doneL := e.freshLabel("athrow.done")
	qSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", qSlot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", freshRej, rejL, submitL))

	e.emitLabel(rejL)
	qr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", qr))
	e.storeGeneratorField(genObj, genTy, GeneratorDoneField, "i1", "1")
	e.emitAsyncGenRejectPromise(qr, errPtr)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", qr, qSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(submitL)
	zeroSent := e.genZeroElem(elemTy)
	qs := e.emitAsyncGenSubmitOrStart(genObj, genTy, elemTy, "1", zeroSent, errPtr)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", qs, qSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", q, qSlot))
	qt := PromiseOf(resultTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

// emitAsyncGeneratorReturnByValue implements `.return(v)` on an async generator
// (TDD-00086): it completes the generator running enclosing finallys and returns
// a settled `Promise<{value: v, done: true}>`. A not-started or done generator
// settles immediately without running the body.
func (e *Emitter) emitAsyncGeneratorReturnByValue(genObj string, genTy Type, rv Value, pos ast.Pos) (Value, error) {
	e.emitAsyncGenRuntimeSetup()
	elemTy := *genTy.GeneratorElemType
	resultTy := genNextResultType(elemTy)
	trueVal := Value{Ref: "true", Ty: TypeBool}

	// A not-started, idle generator settles {v, done:true} immediately without
	// running the body. Otherwise the shared submit-or-start: busy queues the
	// request; idle-suspended swaps in with resume mode 2 (running enclosing
	// finallys — which may themselves await and park); idle-done settles via
	// the step fn's drained-queue dispatch.
	q := e.freshReg()
	pending := e.loadGeneratorField(genObj, genTy, GeneratorPendingQField)
	idle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", idle, pending.Ref))
	started := e.loadGeneratorField(genObj, genTy, GeneratorStartedField)
	notStarted := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notStarted, started.Ref))
	freshProp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", freshProp, idle, notStarted))
	propL := e.freshLabel("aret.prop")
	submitL := e.freshLabel("aret.submit")
	doneL := e.freshLabel("aret.done")
	qSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", qSlot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", freshProp, propL, submitL))

	e.emitLabel(propL)
	qp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", qp))
	e.storeGeneratorField(genObj, genTy, GeneratorDoneField, "i1", "1")
	resProp := e.buildGenNextResult(resultTy, elemTy, rv, trueVal)
	e.storePromiseValue(qp, resProp)
	e.ensurePromiseSettle()
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 1)", qp))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", qp, qSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(submitL)
	qs := e.emitAsyncGenSubmitOrStart(genObj, genTy, elemTy, "2", rv, "null")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", qs, qSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", q, qSlot))
	qt := PromiseOf(resultTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

func (e *Emitter) emitAsyncGeneratorNextByValue(genObj string, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: .next() takes at most 1 argument", pos.Line, pos.Col)
	}
	elemTy := *genTy.GeneratorElemType

	var sentVal Value
	if len(args) == 1 {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		sentVal = e.coerce(v, elemTy)
	} else {
		sentVal = e.genZeroElem(elemTy)
	}
	return e.emitAsyncGeneratorNextCore(genObj, genTy, sentVal), nil
}

// emitAsyncGeneratorNextCore submits a `.next(sent)` request: if no step is in
// flight it starts one **synchronously** — the body runs on its fiber right now,
// up to the first `await`-park, `yield`, or completion, matching V8/spec (the
// spec's AsyncGeneratorResume; node prints `before body after` for
// `log("before"); g().next(); log("after")`) — else it queues the request
// (AsyncGeneratorEnqueue), serviced when the in-flight step settles. Returns the
// step's `Promise<{value,done}>`, pending while the fiber is parked at an await.
func (e *Emitter) emitAsyncGeneratorNextCore(genObj string, genTy Type, sentVal Value) Value {
	e.emitAsyncGenRuntimeSetup()
	elemTy := *genTy.GeneratorElemType
	resultTy := genNextResultType(elemTy)
	q := e.emitAsyncGenSubmitOrStart(genObj, genTy, elemTy, "0", sentVal, "null")
	qt := PromiseOf(resultTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}
}

// emitAsyncGenSubmitOrStart is the shared `.next`/`.throw`/`.return` entry: it
// allocates the request's result promise q, then either starts the step now
// (idle: store the resume fields + __pendingQ and call the step function
// synchronously) or appends the request to the generator's FIFO (busy: a step
// is running or parked at an await). Returns q's register.
func (e *Emitter) emitAsyncGenSubmitOrStart(genObj string, genTy Type, elemTy Type, mode string, sentVal Value, thrown string) string {
	stepFn := e.ensureAsyncGenStepFn(genTy)
	q := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_task_alloc_promise()", q))
	pending := e.loadGeneratorField(genObj, genTy, GeneratorPendingQField)
	busy := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", busy, pending.Ref))
	busyL := e.freshLabel("agen.submit.q")
	startL := e.freshLabel("agen.submit.start")
	doneL := e.freshLabel("agen.submit.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", busy, busyL, startL))

	e.emitLabel(busyL)
	e.emitAsyncGenSubmitRequest(genObj, genTy, elemTy, mode, sentVal, thrown, q)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(startL)
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, sentVal.Ty.IR, sentVal.Ref)
	e.storeGeneratorField(genObj, genTy, GeneratorResumeModeField, "i64", mode)
	e.storeGeneratorField(genObj, genTy, GeneratorThrownField, "ptr", thrown)
	e.storeGeneratorField(genObj, genTy, GeneratorPendingQField, "ptr", q)
	e.emitInstr(fmt.Sprintf("call void %s(ptr %s)", stepFn, genObj))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	return q
}

// ensureAsyncGenStepFn emits (once per generator type) the step function
// `void @__kml_agen_step_N(ptr %env)`, env = the generator object itself. It is
// both the synchronous step entry (called directly by submit-or-start) and the
// await-park resume target (enqueued as a {stepFn, genObj} microtask closure by
// the awaited promise's settle). The loop:
//
//	steploop: q = __pendingQ
//	  done?  → settle q per __resumeMode (0: {zero,done}, 1: reject __thrown,
//	           2: {__sent,done}) — the spec's drained-queue completions
//	  else   → swap into the fiber; on return:
//	             __parked? → ret (q stays pending; a settle re-enqueues us)
//	             else      → settle q from __genError/__yielded (promise_settle)
//	  then pop the next queued request into the resume fields and loop,
//	  or (empty queue) clear __pendingQ and ret.
func (e *Emitter) ensureAsyncGenStepFn(genTy Type) string {
	key := genTy.StructIR()
	if name, ok := e.asyncGenStepFns[key]; ok {
		return name
	}
	e.asyncGenStepCtr++
	name := fmt.Sprintf("@__kml_agen_step_%d", e.asyncGenStepCtr)
	e.asyncGenStepFns[key] = name
	elemTy := *genTy.GeneratorElemType
	resultTy := genNextResultType(elemTy)
	restore := e.beginThunkEmit()
	defer restore()
	e.ensureFree()
	genObj := "%env"

	loopL := e.freshLabel("agstep.loop")
	doneDispL := e.freshLabel("agstep.donedisp")
	dRejL := e.freshLabel("agstep.donerej")
	dChk2L := e.freshLabel("agstep.donechk2")
	dRetL := e.freshLabel("agstep.doneret")
	dZeroL := e.freshLabel("agstep.donezero")
	swapL := e.freshLabel("agstep.swap")
	settleL := e.freshLabel("agstep.settle")
	drainL := e.freshLabel("agstep.drainq")
	idleL := e.freshLabel("agstep.idle")
	popL := e.freshLabel("agstep.pop")
	retL := e.freshLabel("agstep.ret")

	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(loopL)
	q := e.freshReg()
	qv := e.loadGeneratorField(genObj, genTy, GeneratorPendingQField)
	e.emitInstr(fmt.Sprintf("%s = bitcast ptr %s to ptr", q, qv.Ref))
	done := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done.Ref, doneDispL, swapL))

	// A request popped onto (or submitted to) an already-finished generator:
	// complete it per the spec's drained-queue rules.
	e.emitLabel(doneDispL)
	mode := e.loadGeneratorField(genObj, genTy, GeneratorResumeModeField)
	is1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", is1, mode.Ref))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is1, dRejL, dChk2L))
	e.emitLabel(dRejL)
	thrown := e.loadGeneratorField(genObj, genTy, GeneratorThrownField)
	e.emitAsyncGenRejectPromise(q, thrown.Ref)
	e.emitTerminator(fmt.Sprintf("br label %%%s", drainL))
	e.emitLabel(dChk2L)
	is2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", is2, mode.Ref))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is2, dRetL, dZeroL))
	e.emitLabel(dRetL)
	sentBack := e.loadGeneratorField(genObj, genTy, GeneratorSentField)
	e.settleAsyncGenResult(q, genObj, genTy, resultTy, elemTy, sentBack)
	e.emitTerminator(fmt.Sprintf("br label %%%s", drainL))
	e.emitLabel(dZeroL)
	zeroV := e.genZeroElem(elemTy)
	e.settleAsyncGenResult(q, genObj, genTy, resultTy, elemTy, zeroV)
	e.emitTerminator(fmt.Sprintf("br label %%%s", drainL))

	// Live generator: swap into the fiber; the resume fields (__sent /
	// __resumeMode / __thrown) are already staged. A park leaves q pending.
	e.emitLabel(swapL)
	e.emitGeneratorSwapCore(genObj, genTy, true)
	parked := e.loadGeneratorField(genObj, genTy, GeneratorParkedField)
	isParked := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isParked, parked.Ref))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isParked, retL, settleL))

	e.emitLabel(settleL)
	e.emitAsyncGenSettleFromFields(genObj, genTy, elemTy, resultTy, q)
	e.emitTerminator(fmt.Sprintf("br label %%%s", drainL))

	// Service the next queued request, if any.
	e.emitLabel(drainL)
	head := e.loadGeneratorField(genObj, genTy, GeneratorReqHeadField)
	hnull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", hnull, head.Ref))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hnull, idleL, popL))

	e.emitLabel(idleL)
	e.storeGeneratorField(genObj, genTy, GeneratorPendingQField, "ptr", "null")
	e.emitTerminator(fmt.Sprintf("br label %%%s", retL))

	e.emitLabel(popL)
	loadAt := func(idx int) string {
		gp := e.freshReg()
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gp, asyncGenReqNodeIR, head.Ref, idx))
		ir := "ptr"
		if idx == 0 {
			ir = "i64"
		}
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", v, ir, gp))
		return v
	}
	pmode := loadAt(0)
	pslot := loadAt(1)
	pthrown := loadAt(2)
	pq := loadAt(3)
	pnext := loadAt(4)
	e.storeGeneratorField(genObj, genTy, GeneratorReqHeadField, "ptr", pnext)
	psent := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", psent, StructFieldIR(elemTy), pslot, elemTy.Align()))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", pslot))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", head.Ref))
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, elemTy.IR, psent)
	e.storeGeneratorField(genObj, genTy, GeneratorResumeModeField, "i64", pmode)
	e.storeGeneratorField(genObj, genTy, GeneratorThrownField, "ptr", pthrown)
	e.storeGeneratorField(genObj, genTy, GeneratorPendingQField, "ptr", pq)
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(retL)
	e.emitInstr("ret void")

	e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%env) {\nentry:\n", name))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	return name
}

// settleAsyncGenResult builds the `{value, done}` result object from the
// generator's yielded value + __done flag and settles the promise q fulfilled
// with it (TDD-00085).
func (e *Emitter) settleAsyncGenResult(q, genObj string, genTy, resultTy, elemTy Type, yielded Value) {
	done := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	resultReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", resultReg, resultTy.StructSize()))
	vIdx, _, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), resultReg, vIdx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(elemTy), yielded.Ref, vGep, elemTy.Align()))
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), resultReg, dIdx))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", done.Ref, dGep))
	e.storePromiseValue(q, Value{Ref: resultReg, Ty: resultTy})
	// Settle through __kml_promise_settle (not a bare state store) so any
	// awaiter parked on q — the deferred-.next() microtask step settles q after
	// its awaiter has already attached a resume reaction — is actually woken.
	// A no-op drain when there is no waiter, so the synchronous path is
	// unaffected.
	e.ensurePromiseSettle()
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 1)", q))
}

// emitAwaitSettledResult awaits a settled task promise holding a `{value,done}`
// result object (an inner async generator's `.next()`/`.throw()`/`.return()`
// promise) and returns that object — re-throwing on the caller side if the
// promise rejected. Used by async `yield*` (TDD-00086); mirrors emitAwait's
// task-promise branch for the already-settled case an async generator produces.
func (e *Emitter) emitAwaitSettledResult(promReg string, resultTy Type) Value {
	e.ensureFree()
	e.ensureExceptionHelpers()
	if e.currentGenerator != nil && e.currentGenerator.genTy.GeneratorIsAsync {
		// `yield*` awaits an inner step from inside the outer's fiber — park the
		// outer on it (nested parks compose: inner settles → outer resumes).
		e.emitAsyncGenAwaitParkUntilSettled(promReg)
	} else {
		e.ensureTaskRuntime()
		e.emitInstr(fmt.Sprintf("call void @__kml_task_await_ready(ptr %s)", promReg))
	}
	resReg := e.freshReg()
	resP := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", resP, promiseStructIR, promReg))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", resReg, resP))
	rej := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", rej, resReg))
	rejL := e.freshLabel("ystar.await.reject")
	okL := e.freshLabel("ystar.await.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", rej, rejL, okL))
	e.emitLabel(rejL)
	v0P := e.freshReg()
	v0 := e.freshReg()
	errReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", v0P, promiseStructIR, promReg))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", v0, v0P))
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", errReg, v0))
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", promReg))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
	val := e.loadPromiseValue(promReg, resultTy)
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", promReg))
	return val
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
	setOp = fmt.Sprintf("  %s = getelementptr i8, ptr %s, i64 %d\n  %s\n",
		high, stack.Ref, fiberStackBytes, e.gcSBStore(high))
	origBottom := e.freshReg()
	restoreOp = fmt.Sprintf("  %s = load ptr, ptr @__kml_gc_orig_stackbottom, align 8\n  %s\n",
		origBottom, e.gcSBStore(origBottom))
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
// emitForAwaitOfGenerator implements `for await (const x of asyncGen()) {...}`
// (TDD-00085 Stage 3): the async-generator analogue of emitForOfGenerator, but
// each `.next()` returns a `Promise<{value,done}>` that the loop awaits (driving
// the scheduler / re-throwing a rejected step) before reading `.done`/`.value`.
func (e *Emitter) emitForAwaitOfGenerator(s *ast.ForOfStatement, genTy Type, genVal Value, condL, bodyL, incL, endL string) error {
	elemTy := *genTy.GeneratorElemType
	resultTy := genNextResultType(elemTy)
	e.ensureTaskRuntime()
	e.ensureFree()
	e.ensureExceptionHelpers()

	// A destructuring loop variable (`for await (const { x, y } of …)` /
	// `for await (const [a, b] of …)`) binds no single loop variable — the
	// per-iteration element is unpacked in the body through the same
	// unpack*PatternInto core the sync for-of and every other destructuring
	// position share (ADR-00257). An async generator's element type is never an
	// array (generators reject array element types), so the element is an
	// object or a tuple here.
	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultAlloca))
	varPtr := e.freshReg()
	if !isPattern {
		varPtr = e.genDefineLoopVar(s.VarName, elemTy)
	}

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	// prom = it.next() (Promise<{value,done}>); await it, re-throwing a rejection.
	prom, err := e.emitGeneratorNextByValue(genVal.Ref, genTy, nil, s.GetPos())
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_task_await_ready(ptr %s)", prom.Ref))
	e.emitTaskRethrowIfRejected(prom.Ref)
	resultObj := e.loadPromiseValue(prom.Ref, resultTy)
	e.emitInstr(fmt.Sprintf("call void @free(ptr %s)", prom.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", resultObj.Ref, resultAlloca))
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
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, StructFieldIR(elemTy), vGep, elemTy.Align()))
	switch {
	case s.ObjectPattern != nil:
		// The yielded object's fields become the loop-body bindings; a
		// non-object element type is a clean rejection inside
		// unpackObjectPatternInto's own FieldIndex lookup.
		if err := e.unpackObjectPatternInto(loaded, elemTy, s.ObjectPattern, s.GetPos()); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		// A tuple element binds positionally to the tuple's fields (an async
		// generator's element type is never an array, so a tuple is the only
		// array-destructurable element shape here).
		if elemTy.IsArray {
			if err := e.genUnpackArrayElemPattern(loaded, elemTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if elemTy.IsTuple {
			if err := e.unpackTuplePatternInto(loaded, elemTy, s.ArrayPattern, s.GetPos()); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("%d:%d: cannot array-destructure a for-await element of non-tuple type", s.GetPos().Line, s.GetPos().Col)
		}
	default:
		e.genStoreLoopVar(varPtr, elemTy, loaded)
	}
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// emitForAwaitOfSyncGenerator consumes `for await (const x of syncGen())` —
// JS treats a sync iterable in `for await` as CreateAsyncFromSyncIterator: each
// yielded value is awaited before binding (a Promise element awaits to its T, a
// plain value is an identity await). The loop shape is emitForOfGenerator's
// (repeated synchronous .next() against one instance); only the per-element
// await before binding is new.
func (e *Emitter) emitForAwaitOfSyncGenerator(s *ast.ForOfStatement, genTy Type, genVal Value, condL, bodyL, incL, endL string) error {
	pos := s.GetPos()
	elemTy := *genTy.GeneratorElemType

	// The bound value's type is the yielded element awaited.
	awaitedTy := elemTy
	if elemTy.IsPromise {
		if elemTy.PromiseType != nil {
			awaitedTy = *elemTy.PromiseType
		} else {
			awaitedTy = TypeVoid
		}
	}

	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultAlloca))

	varPtr := e.freshReg()
	if !isPattern {
		varPtr = e.genDefineLoopVar(s.VarName, awaitedTy)
	}

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	nextVal, err := e.emitGeneratorNextByValue(genVal.Ref, genTy, nil, pos)
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
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, StructFieldIR(elemTy), vGep, elemTy.Align()))

	// Await the yielded value before binding (identity for a plain value).
	boundVal := Value{Ref: loaded, Ty: elemTy}
	boundTy := elemTy
	if elemTy.IsPromise {
		aw, err := e.emitAwaitPromiseElem(loaded, elemTy)
		if err != nil {
			return err
		}
		boundVal = aw
		boundTy = awaitedTy
		if aw.Ty.IR != "" {
			boundTy = aw.Ty
		}
	}

	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(boundVal.Ref, boundTy, s.ObjectPattern, pos); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if boundTy.IsArray {
			if err := e.genUnpackArrayElemPattern(boundVal.Ref, boundTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if boundTy.IsTuple {
			if err := e.unpackTuplePatternInto(boundVal.Ref, boundTy, s.ArrayPattern, pos); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("%d:%d: cannot array-destructure a for-await element of non-tuple type", pos.Line, pos.Col)
		}
	default:
		if awaitedTy.IR != "void" && awaitedTy.IR != "" {
			e.genStoreLoopVar(varPtr, boundTy, boundVal.Ref)
		}
	}
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// asyncIteratorMethodName is the reserved `MethodSigs` key a `[Symbol.asyncIterator]()`
// method desugars to (TDD-00089). The `@@` prefix is not a lexable identifier, so it can
// never collide with a user-declared method — the parser produces it, this file consumes it.
const asyncIteratorMethodName = "@@asyncIterator"

// emitForAwaitOfAsyncIterable consumes a user class that implements the async-iteration
// protocol by hand (TDD-00089): `it = iterable[Symbol.asyncIterator]()` yields an iterator
// object whose `async next(): Promise<{value,done}>` is awaited each turn. It reuses class
// method dispatch (emitClassCall), the shared task-promise await (emitAwaitTaskPromise —
// which parks/drives and re-throws a rejection), and the same structural `{value,done}`
// reads as emitForAwaitOfGenerator; only the get-iterator + call-next wiring is new.
func (e *Emitter) emitForAwaitOfAsyncIterable(s *ast.ForOfStatement, iterableTy Type, iterableVal Value, condL, bodyL, incL, endL string) error {
	pos := s.GetPos()

	// it = iterable[Symbol.asyncIterator]() — an ordinary method call on the reserved name.
	iterVal, err := e.emitClassCall(iterableTy, iterableVal, asyncIteratorMethodName, nil, pos, false)
	if err != nil {
		return err
	}
	if !iterVal.Ty.IsClass {
		return fmt.Errorf("%d:%d: [Symbol.asyncIterator]() must return a class instance with a next() method (TDD-00089)", pos.Line, pos.Col)
	}
	return e.emitForAwaitOfAsyncIteratorInstance(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
}

// emitForAwaitOfAsyncIteratorInstance drives an already-obtained iterator class
// instance (an `async next(): Promise<{value,done}>` object) — the loop half of
// emitForAwaitOfAsyncIterable, also reachable directly when an object literal's
// `[Symbol.asyncIterator]` closure returns the iterator instance itself.
func (e *Emitter) emitForAwaitOfAsyncIteratorInstance(s *ast.ForOfStatement, iterTy Type, iterVal Value, condL, bodyL, incL, endL string) error {
	pos := s.GetPos()
	iterInfo, ok := e.classes[iterTy.ClassName]
	if !ok {
		return fmt.Errorf("%d:%d: unknown iterator class '%s'", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	nextSig, ok := iterInfo.MethodSigs["next"]
	if !ok {
		return fmt.Errorf("%d:%d: async iterator '%s' has no next() method (TDD-00089)", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	if !nextSig.RetType.IsPromise || nextSig.RetType.PromiseType == nil {
		return fmt.Errorf("%d:%d: async iterator '%s'.next() must return a Promise<{value,done}> (TDD-00089)", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	resultTy := *nextSig.RetType.PromiseType
	_, elemTy, ok := resultTy.FieldIndex("value")
	if !ok {
		return fmt.Errorf("%d:%d: async iterator '%s'.next() result has no 'value' field (TDD-00089)", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	if _, _, ok := resultTy.FieldIndex("done"); !ok {
		return fmt.Errorf("%d:%d: async iterator '%s'.next() result has no 'done' field (TDD-00089)", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}

	// The iterator instance persists across the loop; hold it in an alloca (it may be
	// `return this` or a fresh object — either way a heap ptr to reload each turn).
	iterAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", iterAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", iterVal.Ref, iterAlloca))

	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultAlloca))
	varPtr := e.freshReg()
	if !isPattern {
		varPtr = e.genDefineLoopVar(s.VarName, elemTy)
	}

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	// prom = it.next() (a task-shaped Promise<{value,done}>); await it, re-throwing a rejection.
	iterReloaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", iterReloaded, iterAlloca))
	prom, err := e.emitClassCall(iterTy, Value{Ref: iterReloaded, Ty: iterTy}, "next", nil, pos, false)
	if err != nil {
		return err
	}
	resultObj, err := e.emitAwaitTaskPromise(prom.Ref, resultTy)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", resultObj.Ref, resultAlloca))
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
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, StructFieldIR(elemTy), vGep, elemTy.Align()))
	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(loaded, elemTy, s.ObjectPattern, pos); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if elemTy.IsArray {
			if err := e.genUnpackArrayElemPattern(loaded, elemTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if elemTy.IsTuple {
			if err := e.unpackTuplePatternInto(loaded, elemTy, s.ArrayPattern, pos); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("%d:%d: cannot array-destructure a for-await element of non-tuple type", pos.Line, pos.Col)
		}
	default:
		e.genStoreLoopVar(varPtr, elemTy, loaded)
	}
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// syncIteratorMethodName is the reserved `MethodSigs` key a `[Symbol.iterator]()`
// method desugars to — the sync analogue of asyncIteratorMethodName.
const syncIteratorMethodName = "@@iterator"

// emitForOfSymbolIterator consumes a user class implementing the *sync*
// iteration protocol by hand: `it = iterable[Symbol.iterator]()` yields an
// iterator whose `next(): {value, done}` is called each turn (`return this`
// self-iterators and separate iterator objects both work) — the spec's real
// `{value, done}` protocol, alongside the structural `next(): T | null` shape
// this compiler's for...of has always dispatched. With isAwait (`for await`),
// each value is additionally awaited before binding (identity for a plain
// value, a real await for a Promise value). Mirrors
// emitForAwaitOfAsyncIterable; only the non-promise next() and the optional
// per-value await differ.
func (e *Emitter) emitForOfSymbolIterator(s *ast.ForOfStatement, iterableTy Type, iterableVal Value, isAwait bool, condL, bodyL, incL, endL string) error {
	pos := s.GetPos()

	iterVal, err := e.emitClassCall(iterableTy, iterableVal, syncIteratorMethodName, nil, pos, false)
	if err != nil {
		return err
	}
	if !iterVal.Ty.IsClass {
		return fmt.Errorf("%d:%d: [Symbol.iterator]() must return a class instance with a next() method", pos.Line, pos.Col)
	}
	iterTy := iterVal.Ty
	iterInfo, ok := e.classes[iterTy.ClassName]
	if !ok {
		return fmt.Errorf("%d:%d: unknown iterator class '%s'", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	nextSig, ok := iterInfo.MethodSigs["next"]
	if !ok {
		return fmt.Errorf("%d:%d: iterator '%s' has no next() method", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	resultTy := nextSig.RetType
	if resultTy.IsPromise {
		return fmt.Errorf("%d:%d: '%s'.next() returns a Promise — a [Symbol.iterator]() iterator must be synchronous (use [Symbol.asyncIterator] with for await)", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	_, elemTy, ok := resultTy.FieldIndex("value")
	if !ok {
		return fmt.Errorf("%d:%d: iterator '%s'.next() result has no 'value' field", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	if _, _, ok := resultTy.FieldIndex("done"); !ok {
		return fmt.Errorf("%d:%d: iterator '%s'.next() result has no 'done' field", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}

	// The bound value's type: with `for await`, a Promise value awaits to its T.
	boundDeclTy := elemTy
	if isAwait && elemTy.IsPromise {
		if elemTy.PromiseType != nil {
			boundDeclTy = *elemTy.PromiseType
		} else {
			boundDeclTy = TypeVoid
		}
	}

	iterAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", iterAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", iterVal.Ref, iterAlloca))

	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultAlloca))
	varPtr := e.freshReg()
	if !isPattern {
		varPtr = e.genDefineLoopVar(s.VarName, boundDeclTy)
	}

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	iterReloaded := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", iterReloaded, iterAlloca))
	resultObj, err := e.emitClassCall(iterTy, Value{Ref: iterReloaded, Ty: iterTy}, "next", nil, pos, false)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", resultObj.Ref, resultAlloca))
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
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, StructFieldIR(elemTy), vGep, elemTy.Align()))

	boundVal := Value{Ref: loaded, Ty: elemTy}
	boundTy := elemTy
	if isAwait && elemTy.IsPromise {
		aw, err := e.emitAwaitPromiseElem(loaded, elemTy)
		if err != nil {
			return err
		}
		boundVal = aw
		boundTy = boundDeclTy
		if aw.Ty.IR != "" {
			boundTy = aw.Ty
		}
	}

	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(boundVal.Ref, boundTy, s.ObjectPattern, pos); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if boundTy.IsArray {
			if err := e.genUnpackArrayElemPattern(boundVal.Ref, boundTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if boundTy.IsTuple {
			if err := e.unpackTuplePatternInto(boundVal.Ref, boundTy, s.ArrayPattern, pos); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("%d:%d: cannot array-destructure a for-of element of non-tuple type", pos.Line, pos.Col)
		}
	default:
		if boundDeclTy.IR != "void" && boundDeclTy.IR != "" {
			e.genStoreLoopVar(varPtr, boundTy, boundVal.Ref)
		}
	}
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

// emitForOfObjectSymbolIterable consumes an object literal (a static struct)
// carrying a closure-typed `@@asyncIterator`/`@@iterator` field (the parser's
// desugar of `{ [Symbol.asyncIterator]: … }` / `{ [Symbol.iterator]() {…} }`):
// load the field's closure, call it with no arguments, and iterate the result
// by its type — an async generator (for await only), a sync generator, or a
// class instance (its own `[Symbol.asyncIterator]`/`[Symbol.iterator]` when
// present, else — for the async protocol — directly an `async next()`
// iterator). isAwait selects the awaiting loop shapes.
func (e *Emitter) emitForOfObjectSymbolIterable(s *ast.ForOfStatement, objTy Type, key string, isAwait bool, condL, bodyL, incL, endL string) error {
	pos := s.GetPos()
	fIdx, fTy, _ := objTy.FieldIndex(key)
	if !fTy.IsFunc {
		return fmt.Errorf("%d:%d: a [Symbol.iterator]/[Symbol.asyncIterator] member must be function-valued", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(s.Iterable)
	if err != nil {
		return err
	}
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objTy.StructIR(), objVal.Ref, fIdx))
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, gep))
	iterVal, err := e.emitClosureCallByPtr(hdr, fTy, nil, pos)
	if err != nil {
		return err
	}
	switch {
	case iterVal.Ty.IsGenerator && iterVal.Ty.GeneratorIsAsync:
		if !isAwait {
			return fmt.Errorf("%d:%d: a [Symbol.iterator]() returning an async generator requires 'for await...of'", pos.Line, pos.Col)
		}
		return e.emitForAwaitOfGenerator(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
	case iterVal.Ty.IsGenerator:
		if isAwait {
			return e.emitForAwaitOfSyncGenerator(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
		}
		return e.emitForOfGenerator(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
	case iterVal.Ty.IsClass:
		if info, ok := e.classes[iterVal.Ty.ClassName]; ok {
			if _, has := info.MethodSigs[asyncIteratorMethodName]; has && isAwait {
				return e.emitForAwaitOfAsyncIterable(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
			}
			if _, has := info.MethodSigs[syncIteratorMethodName]; has {
				return e.emitForOfSymbolIterator(s, iterVal.Ty, iterVal, isAwait, condL, bodyL, incL, endL)
			}
		}
		if key == asyncIteratorMethodName && isAwait {
			// The returned instance is directly the iterator (async next()).
			return e.emitForAwaitOfAsyncIteratorInstance(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
		}
		return fmt.Errorf("%d:%d: the class instance returned by [Symbol.iterator]() must itself declare [Symbol.iterator]()", pos.Line, pos.Col)
	}
	return fmt.Errorf("%d:%d: a [Symbol.iterator]/[Symbol.asyncIterator] member must return a generator or a class instance", pos.Line, pos.Col)
}

// emitForAwaitOfArray consumes `for await (const x of arr)` over a sync array
// (TDD-00092): JS awaits each element, so an array of promises is awaited
// sequentially (element N's promise settles before N+1 is bound) and an array of
// plain values awaits each as a harmless identity. Reuses the array for-of loop
// shape; the only difference is the per-element await before binding.
func (e *Emitter) emitForAwaitOfArray(s *ast.ForOfStatement, arrTy Type, condL, bodyL, incL, endL string) error {
	pos := s.GetPos()
	ptrReg, lenReg, _, err := e.resolveArrayForHOF(s.Iterable, pos)
	if err != nil {
		return err
	}
	return e.emitForAwaitOfArrayCore(s, *arrTy.ElemType, ptrReg, lenReg, condL, bodyL, incL, endL)
}

// emitForAwaitOfArrayCore is the loop body shared by the array iterable and the
// Map/Set iterable (whose values are first materialized into an array): index
// over ptrReg/lenReg, awaiting each element before binding.
func (e *Emitter) emitForAwaitOfArrayCore(s *ast.ForOfStatement, elemTy Type, ptrReg, lenReg, condL, bodyL, incL, endL string) error {
	pos := s.GetPos()

	// The bound value's type is the element type awaited: T for a Promise<T>
	// element (Response for a raw fetch element), or the element type itself
	// for a plain value.
	awaitedTy := elemTy
	isPromiseElem := elemTy.IsPromise
	if isPromiseElem {
		if elemTy.PromiseType != nil {
			awaitedTy = *elemTy.PromiseType
		} else {
			awaitedTy = TypeVoid
		}
	}

	idxPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxPtr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxPtr))

	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	varPtr := e.freshReg()
	if isPattern {
		// no pre-loop binding — the element is unpacked in the body
	} else if awaitedTy.IsArray {
		// Object-reference model (TDD-00127): a stable slot holding a pointer to
		// the current element's header, rebuilt each iteration.
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", varPtr))
		e.define(s.VarName, Symbol{Ptr: varPtr, Ty: awaitedTy})
	} else {
		varPtr = e.genDefineLoopVar(s.VarName, awaitedTy)
	}

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	condReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", condReg, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, bodyL, endL))

	e.emitLabel(bodyL)
	idxVal2 := e.freshReg()
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal2, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", gepReg, elemTy.IR, ptrReg, idxVal2))
	elemVal := e.loadArrayElem(gepReg, elemTy)

	// Await the element: a promise element drives to settled (re-throwing a
	// rejection; a raw fetch element drives the fetch and builds the Response);
	// a plain value is an identity await.
	boundVal := elemVal
	boundTy := elemTy
	if isPromiseElem {
		aw, err := e.emitAwaitPromiseElem(elemVal.Ref, elemTy)
		if err != nil {
			return err
		}
		boundVal = aw
		boundTy = awaitedTy
		// emitAwaitPromiseElem returns the Response type itself for a raw fetch
		// element; keep the binding type in sync with the returned value.
		if aw.Ty.IR != "" {
			boundTy = aw.Ty
		}
	}

	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(boundVal.Ref, boundTy, s.ObjectPattern, pos); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if boundTy.IsArray {
			if err := e.genUnpackArrayElemPattern(boundVal.Ref, boundTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if boundTy.IsTuple {
			if err := e.unpackTuplePatternInto(boundVal.Ref, boundTy, s.ArrayPattern, pos); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("%d:%d: cannot array-destructure a for-await element of non-tuple type", pos.Line, pos.Col)
		}
	default:
		if awaitedTy.IsArray {
			// Point the loop variable's slot at a fresh header for this element.
			elemHeader := e.boxArrayValue(boundVal)
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", elemHeader, varPtr))
		} else if awaitedTy.IR != "void" && awaitedTy.IR != "" {
			e.genStoreLoopVar(varPtr, boundTy, boundVal.Ref)
		}
	}

	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, idxPtr))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, cur))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	return nil
}

func (e *Emitter) emitForOfGenerator(s *ast.ForOfStatement, genTy Type, genVal Value, condL, bodyL, incL, endL string) error {
	elemTy := *genTy.GeneratorElemType

	// A destructuring loop variable over a generator (`for (const [a, b] of gen())`
	// / `for (const { x, y } of gen())`) unpacks the per-iteration element in the
	// body, defining no single binding here — the same shape the array/for-await
	// paths use (ADR-00257).
	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultAlloca))

	varPtr := e.freshReg()
	if !isPattern {
		varPtr = e.genDefineLoopVar(s.VarName, elemTy)
	}

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
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loaded, StructFieldIR(elemTy), vGep, elemTy.Align()))
	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(loaded, elemTy, s.ObjectPattern, s.GetPos()); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if elemTy.IsArray {
			if err := e.genUnpackArrayElemPattern(loaded, elemTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if elemTy.IsTuple {
			if err := e.unpackTuplePatternInto(loaded, elemTy, s.ArrayPattern, s.GetPos()); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("%d:%d: cannot array-destructure a for-of generator element of non-tuple type", s.GetPos().Line, s.GetPos().Col)
		}
	default:
		e.genStoreLoopVar(varPtr, elemTy, loaded)
	}
	if err := e.emitStmt(s.Body); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	// TDD-00061 follow-up (ADR-00613): a `break` out of the loop leaves the
	// generator *suspended*, so — matching JS's iterator-close protocol — drive
	// its `.return()` here to run any enclosing `finally` in the generator body.
	// On normal completion the generator is already done, where
	// emitGeneratorReturnByValue is a no-op (it neither resumes the body nor
	// re-runs a finally), so this is safe to emit unconditionally at the single
	// shared loop-exit label. Async generators are consumed by the for-await
	// path, not here.
	if !genTy.GeneratorIsAsync {
		if _, err := e.emitGeneratorReturnByValue(genVal.Ref, genTy, e.genZeroElem(elemTy), s.GetPos()); err != nil {
			return err
		}
	}
	return nil
}

// collectYieldExprs walks a statement list gathering every YieldExpression —
// TDD-00096 Part 2's inference input. Same pragmatic statement/expression
// coverage as the async classifier (emit_task_classify.go): common container
// shapes, not an exhaustive visitor; a yield hiding in an unvisited corner
// simply doesn't contribute to inference.
func collectYieldExprs(stmts []ast.Statement, out *[]*ast.YieldExpression) {
	for _, s := range stmts {
		collectYieldStmt(s, out)
	}
}

func collectYieldStmt(s ast.Statement, out *[]*ast.YieldExpression) {
	switch st := s.(type) {
	case *ast.BlockStatement:
		collectYieldExprs(st.Body, out)
	case *ast.VarDeclaration:
		collectYieldExpr(st.Init, out)
	case *ast.ExpressionStatement:
		collectYieldExpr(st.Expr, out)
	case *ast.ReturnStatement:
		collectYieldExpr(st.Value, out)
	case *ast.IfStatement:
		collectYieldExpr(st.Test, out)
		if st.Consequent != nil {
			collectYieldExprs(st.Consequent.Body, out)
		}
		if st.Alternate != nil {
			collectYieldStmt(st.Alternate, out)
		}
	case *ast.ForStatement:
		if st.Init != nil {
			collectYieldStmt(st.Init, out)
		}
		collectYieldExpr(st.Test, out)
		for _, u := range st.Update {
			collectYieldExpr(u, out)
		}
		if st.Body != nil {
			collectYieldExprs(st.Body.Body, out)
		}
	case *ast.WhileStatement:
		collectYieldExpr(st.Test, out)
		if st.Body != nil {
			collectYieldExprs(st.Body.Body, out)
		}
	case *ast.DoWhileStatement:
		collectYieldExpr(st.Test, out)
		if st.Body != nil {
			collectYieldExprs(st.Body.Body, out)
		}
	case *ast.ForOfStatement:
		collectYieldExpr(st.Iterable, out)
		if st.Body != nil {
			collectYieldExprs(st.Body.Body, out)
		}
	case *ast.ForInStatement:
		collectYieldExpr(st.Object, out)
		if st.Body != nil {
			collectYieldExprs(st.Body.Body, out)
		}
	case *ast.TryStatement:
		if st.Body != nil {
			collectYieldExprs(st.Body.Body, out)
		}
		if st.Catch != nil && st.Catch.Body != nil {
			collectYieldExprs(st.Catch.Body.Body, out)
		}
		if st.Finally != nil {
			collectYieldExprs(st.Finally.Body, out)
		}
	case *ast.SwitchStatement:
		collectYieldExpr(st.Discriminant, out)
		for _, c := range st.Cases {
			collectYieldExpr(c.Test, out)
			collectYieldExprs(c.Body, out)
		}
	case *ast.LabeledStatement:
		collectYieldStmt(st.Body, out)
	}
}

func collectYieldExpr(ex ast.Expression, out *[]*ast.YieldExpression) {
	switch x := ex.(type) {
	case nil:
	case *ast.YieldExpression:
		*out = append(*out, x)
		collectYieldExpr(x.Argument, out)
	case *ast.CallExpression:
		collectYieldExpr(x.Callee, out)
		for _, a := range x.Args {
			collectYieldExpr(a, out)
		}
	case *ast.BinaryExpression:
		collectYieldExpr(x.Left, out)
		collectYieldExpr(x.Right, out)
	case *ast.ConditionalExpression:
		collectYieldExpr(x.Test, out)
		collectYieldExpr(x.Consequent, out)
		collectYieldExpr(x.Alternate, out)
	case *ast.AssignmentExpression:
		collectYieldExpr(x.Left, out)
		collectYieldExpr(x.Right, out)
	case *ast.AwaitExpression:
		collectYieldExpr(x.Argument, out)
	}
}

// inferGeneratorElemType infers an un-annotated generator's element type
// from its yields (TDD-00096 Part 2): each plain `yield <expr>`'s inferred
// type joins under the usual numeric rule (any float makes the join f64);
// `yield*` over a call to a known generator contributes that generator's
// element type. Zero contributing yields fall back to the first `return
// <expr>`'s type, then i64. Parameters are temporarily bound so a
// `yield param` infers from the declared parameter type. Reports !ok only
// for a genuinely non-joinable mix.
func (e *Emitter) inferGeneratorElemType(fd *ast.FunctionDeclaration, paramNames []string, paramTypes []Type) (Type, bool) {
	e.pushScope()
	for i, n := range paramNames {
		e.define(n, Symbol{Ty: paramTypes[i]})
	}
	defer e.popScope()

	// Shallow-bind the body's own let/var/const declarations (recursively
	// through blocks and for-inits) so a `yield i * 1.5` over a loop-local
	// infers from the local's actual type rather than the unknown-identifier
	// i64 fallback. An approximation — shadowing collapses to last-writer —
	// but inference-only: the real emission scopes normally.
	var bindLocals func(stmts []ast.Statement)
	bindLocal := func(vd *ast.VarDeclaration) {
		if vd == nil {
			return
		}
		var ty Type
		if vd.TypeAnnot != nil {
			ty = e.resolveType(vd.TypeAnnot)
		} else if vd.Init != nil {
			ty = e.inferExprType(vd.Init)
		} else {
			ty = TypeI64
		}
		e.define(vd.Name, Symbol{Ty: ty})
	}
	bindLocals = func(stmts []ast.Statement) {
		for _, s := range stmts {
			switch st := s.(type) {
			case *ast.VarDeclaration:
				bindLocal(st)
			case *ast.VarDeclarationList:
				for _, d := range st.Decls {
					bindLocal(d)
				}
			case *ast.BlockStatement:
				bindLocals(st.Body)
			case *ast.IfStatement:
				if st.Consequent != nil {
					bindLocals(st.Consequent.Body)
				}
				if st.Alternate != nil {
					bindLocals([]ast.Statement{st.Alternate})
				}
			case *ast.ForStatement:
				if st.Init != nil {
					bindLocals([]ast.Statement{st.Init})
				}
				if st.Body != nil {
					bindLocals(st.Body.Body)
				}
			case *ast.WhileStatement:
				if st.Body != nil {
					bindLocals(st.Body.Body)
				}
			case *ast.DoWhileStatement:
				if st.Body != nil {
					bindLocals(st.Body.Body)
				}
			case *ast.ForOfStatement:
				if st.Body != nil {
					bindLocals(st.Body.Body)
				}
			case *ast.TryStatement:
				if st.Body != nil {
					bindLocals(st.Body.Body)
				}
				if st.Catch != nil && st.Catch.Body != nil {
					bindLocals(st.Catch.Body.Body)
				}
				if st.Finally != nil {
					bindLocals(st.Finally.Body)
				}
			}
		}
	}
	if fd.Body != nil {
		bindLocals(fd.Body.Body)
	}

	var yields []*ast.YieldExpression
	if fd.Body != nil {
		collectYieldExprs(fd.Body.Body, &yields)
	}
	var joined Type
	have := false
	join := func(t Type) bool {
		if t.IR == "void" || t.IR == "" {
			return true
		}
		if !have {
			joined = t
			have = true
			return true
		}
		if joined.IR == t.IR {
			return true
		}
		num := func(x Type) bool { return x.Float || x.IsInteger() }
		if num(joined) && num(t) {
			if t.Float {
				joined = t
			}
			return true
		}
		return false
	}
	for _, y := range yields {
		if y.Argument == nil {
			continue
		}
		if y.Delegate {
			if call, ok := y.Argument.(*ast.CallExpression); ok {
				if id, ok := call.Callee.(*ast.Identifier); ok {
					if info, found := e.lookupGenerator(id.Name); found {
						if !join(info.ElemTy) {
							return Type{}, false
						}
					}
				}
			}
			continue
		}
		if !join(e.inferExprType(y.Argument)) {
			return Type{}, false
		}
	}
	if !have {
		var rets []*ast.YieldExpression
		_ = rets
		// No yields at all: fall back to the first return-with-value's type,
		// else i64 (a generator that only completes).
		if fd.Body != nil {
			for _, s := range fd.Body.Body {
				if r, ok := s.(*ast.ReturnStatement); ok && r.Value != nil {
					if join(e.inferExprType(r.Value)) {
						break
					}
				}
			}
		}
		if !have {
			joined = TypeI64
		}
	}
	return joined, true
}

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
	"sort"
	"strings"
)

// GeneratorInfo is one top-level `function*` declaration's registered
// shape — see buildGeneratorSig.
type GeneratorInfo struct {
	// ctorFn is the function its value calls, once emitted; valueGlobal
	// holds a capture-free generator's one value.
	ctorFn, valueGlobal string
	// displayName is its declared name, its function value's name when the
	// value is first made for an instance's prototype (genproto.go).
	displayName  string
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
	// Src is the declaration's source text (Function.prototype.toString).
	Src ast.SourceRange
	// Captures are the enclosing-scope variables a *nested* generator closes over
	// (TDD-00094), gathered at the declaration's emission point. Empty for a
	// top-level generator. At construction the captured cells are boxed into the
	// instance's __env; the body binds each name to its cell through __env, so a
	// mutation of an enclosing `let` is seen by a later `.next()` (by reference).
	Captures []CapturedVar
	// HasRest marks the last parameter as a rest parameter: a call packs its
	// trailing arguments into that array.
	HasRest bool
	// MinArgs is the count of leading required parameters (Function.length).
	MinArgs int
	// Optional marks each non-rest parameter that may be omitted.
	Optional []bool
	// EagerStart: the body runs its parameter defaults when constructed
	// (FunctionDeclarationInstantiation at the call), then suspends.
	EagerStart bool
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
// generatorWrapperNames are the standard-library generic types a TypeScript
// author writes as a generator/iterator return annotation. This codebase's own
// convention writes the *element* type directly (`function* f(): number`), but
// idiomatic TS wraps it (`Generator<number>`, `IterableIterator<string>`), and
// the wrapper's first type argument is that element type (`Generator<T, TReturn,
// TNext>` — TReturn/TNext are ignored in V1). Without this, a wrapped annotation
// fell through resolveType's generic-name path and was silently misread as an
// array (`Generator<number>` → `number[]`), so a scalar `yield` produced invalid
// IR (a double stored into an array `{ptr,i64}` slot).
var generatorWrapperNames = map[string]bool{
	"Generator": true, "AsyncGenerator": true,
	"Iterator": true, "AsyncIterator": true,
	"IterableIterator": true, "AsyncIterableIterator": true,
	"Iterable": true, "AsyncIterable": true,
}

// generatorElemAnnotation returns the type annotation to resolve as a
// generator's element type, or nil to infer it from the body. For a wrapper
// return type (see generatorWrapperNames) it unwraps to the first type argument
// (nil when the wrapper carries none, e.g. a bare `Generator`); otherwise the
// annotation is the element type written directly.
func generatorElemAnnotation(rt *ast.TypeAnnotation) *ast.TypeAnnotation {
	if rt == nil {
		return nil
	}
	if generatorWrapperNames[rt.Name] {
		if len(rt.TypeArgs) > 0 {
			return rt.TypeArgs[0]
		}
		return rt.ElemType // single type arg arrives here (as Promise<T>/Array<T> do); nil ⇒ infer
	}
	return rt
}

func (e *Emitter) buildGeneratorSig(fd *ast.FunctionDeclaration) (*GeneratorInfo, error) {
	return e.buildGeneratorSigThis(fd, nil)
}

// buildGeneratorSigThis is buildGeneratorSig with a method's receiver type,
// which `this` binds to while the element type is inferred.
func (e *Emitter) buildGeneratorSigThis(fd *ast.FunctionDeclaration, thisTy *Type) (*GeneratorInfo, error) {
	// A body reading `arguments` collects the overflow in a hidden rest
	// parameter, as a function's does (TDD-00210).
	maybeAddArgumentsRestParam(fd)
	e.desugarGeneratorPatternParams(fd)
	var paramTypes []Type
	var paramNames []string
	hasRest := false
	minArgs, seenOpt := 0, false
	var optional []bool
	for _, p := range fd.Params {
		isOpt := p.Rest || p.Optional || p.Default != nil || (seenOpt && e.compatJS())
		seenOpt = seenOpt || isOpt
		if !seenOpt {
			minArgs++
		}
		if !p.Rest {
			optional = append(optional, isOpt)
		}
		if p.Rest {
			// The trailing arguments, collected as a call to an ordinary
			// function collects them.
			pty := ArrayOf(TypeF64)
			if p.Type != nil {
				pty = e.resolveType(p.Type)
			} else if e.compatJS() || p.Name == argumentsRestParam {
				pty = ArrayOf(TypeAny) // `arguments`' overflow: boxed
			}
			paramTypes = append(paramTypes, pty)
			paramNames = append(paramNames, p.Name)
			hasRest = true
			continue
		}
		var pty Type
		if p.Type != nil {
			pty = e.resolveType(p.Type)
		} else if p.Default != nil || p.Optional || (seenOpt && e.compatJS()) {
			// An unannotated optional parameter: its default's type, or any.
			pty = TypeAny
			if p.Default != nil && !e.compatJS() {
				if dt := e.inferExprType(p.Default); dt.IR != "" && dt.IR != "void" {
					pty = dt
				}
			}
		} else if e.compatJS() {
			pty = TypeAny // any argument may be omitted: undefined
		} else {
			pty = TypeI64
			pty.Inferred = true
		}
		// An optional or defaulted parameter arrives as `T | undefined`; the
		// body's prologue fills the default (emitParamDefaultPrologue).
		pty = optionalParamType(p, pty)
		paramTypes = append(paramTypes, pty)
		paramNames = append(paramNames, p.Name)
	}
	var elemTy Type
	if elemAnn := generatorElemAnnotation(fd.ReturnType); elemAnn != nil {
		// The element type: written directly (this codebase's convention,
		// `function* f(): number` / `: number[]`), or unwrapped from an
		// idiomatic-TS wrapper (`Generator<number>` → number). See
		// generatorElemAnnotation.
		elemTy = e.resolveType(elemAnn)
		// The one value slot holds what it yields and what it returns:
		// `Generator<undefined, number>` returns a number through it, so a
		// TReturn of another representation boxes both.
		if rt := fd.ReturnType; generatorWrapperNames[rt.Name] && len(rt.TypeArgs) > 1 && rt.TypeArgs[1] != nil {
			if ret := e.resolveType(rt.TypeArgs[1]); ret.IR != "" && ret.IR != "void" && !(ret.IsUndefined() && !ret.Nullable()) && reprKey(ret) != reprKey(elemTy) {
				elemTy = TypeAny
			}
		}
	} else {
		// No usable annotation (none at all, or a bare `Generator` with no type
		// argument): infer the element type from the body's own yield expressions
		// (TDD-00096 Part 2) — real-JS sources never carry the annotation.
		// Yields of different representations are a union (TypeScript's
		// `Generator<number | number[]>`), held boxed.
		inferred, ok := e.inferGeneratorElemType(fd, paramNames, paramTypes, thisTy)
		if !ok {
			inferred = TypeAny
		}
		elemTy = inferred
	}
	e.generatorBodyCtr++
	body := fmt.Sprintf("@__generator_body_%s_%d", llvmSafeSymbol(fd.Name), e.generatorBodyCtr)
	if e.inLib != "" {
		body = e.literalSymbol("@__generator_body", fd.GetPos())
	}
	return &GeneratorInfo{
		displayName:  fd.Name,
		ParamTypes:   paramTypes,
		ParamNames:   paramNames,
		ElemTy:       elemTy,
		IsAsync:      fd.IsAsync,
		Src:          fd.Src,
		GenTy:        GeneratorType(elemTy, paramTypes, nil, fd.IsAsync),
		BodyFuncName: body,
		HasRest:      hasRest,
		MinArgs:      minArgs,
		EagerStart:   hasBodyDefault(fd.Params) || len(e.genPatternInits[fd]) > 0,
		Optional:     optional,
	}, nil
}

// buildGeneratorMethodInfo is buildGeneratorSig's sibling for a generator
// *method* (TDD-00063 Stage 2b): same param/element-type rules, but the
// resulting GenTy carries a __this slot (the receiver), and the body func
// name is namespaced by class+method rather than a bare function name.
// classTy is the receiver type `this` binds to inside the body.
func (e *Emitter) buildGeneratorMethodInfo(fd *ast.FunctionDeclaration, className string, classTy Type) (*GeneratorInfo, error) {
	base, err := e.buildGeneratorSigThis(fd, &classTy)
	if err != nil {
		return nil, err
	}
	base.ThisTy = &classTy
	base.GenTy = GeneratorType(base.ElemTy, base.ParamTypes, &classTy, base.IsAsync)
	base.BodyFuncName = fmt.Sprintf("@__generator_method_%s_%s_%d", llvmSafeSymbol(className), llvmSafeSymbol(fd.Name), e.generatorBodyCtr)
	if e.inLib != "" {
		base.BodyFuncName = e.literalSymbol("@__generator_method", fd.GetPos())
	}
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
// (shared with the tasks via ensureFiberRuntime — same
// getcontext/makecontext/swapcontext declares, same ucontextLayout()
// constants) plus one generator-specific global: a "pending generator
// object" handoff slot. makecontext's target function (the generator's own
// compiled body, see emitGeneratorFunctionDecl) takes zero arguments and
// reads a global to find its own object on first entry. A generator's own entry needs
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
// yielding an array element type, ADR-00676) holds the array's header
// pointer — the caller passes the array's Value.Ref. Every
// non-array field is unaffected (StructFieldIR == Type.IR there).
func (e *Emitter) storeGeneratorField(genObjReg string, genTy Type, field, ir, val string) {
	idx, fieldTy, _ := genTy.FieldIndex(field)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, genTy.StructIR(), genObjReg, idx))
	if fieldTy.IsArray() {
		// The slot holds a pointer to the array's {data,len} header (TDD-00213
		// Stage 2); store the passed array's header.
		e.storeArrayFieldHeader(gep, Value{Ref: val, Ty: fieldTy})
		return
	}
	e.heapStore(ir, val, gep, fieldTy.Align())
}

// loadGeneratorField GEPs+loads one field of a generator instance — see
// storeGeneratorField's own doc comment. An array-typed slot is loaded as its
// header pointer, returned as an ordinary array Value (ADR-00676).
func (e *Emitter) loadGeneratorField(genObjReg string, genTy Type, field string) Value {
	idx, fieldTy, _ := genTy.FieldIndex(field)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, genTy.StructIR(), genObjReg, idx))
	if fieldTy.IsArray() {
		// The slot holds a header pointer (TDD-00213 Stage 2). A generator
		// field is always written before it is read, so the branchless load is safe.
		return e.loadArraySlotAggregate(gep, fieldTy)
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, StructFieldIR(fieldTy), gep, fieldTy.Align()))
	return Value{Ref: reg, Ty: fieldTy}
}

// genZeroElem produces a zero value of a generator's element type for the
// "nothing more to give" / uninitialised-slot positions (ADR-00676). For an
// array element type this is the empty array (emitScalarZero
// only builds scalar/pointer zeros); every other type defers to
// emitScalarZero unchanged.
// genElemValIR is the LLVM type of a generator element VALUE as it lives in an
// SSA register or a phi (an array's header pointer).
func genElemValIR(elemTy Type) string {
	return StructFieldIR(elemTy)
}

func (e *Emitter) genZeroElem(t Type) Value {
	if t.IsArray() {
		return Value{Ref: "null", Ty: t}
	}
	if t.IsDynamic() {
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: t}
	}
	if isNullableScalar(t) {
		// An absent `T | null`: the presence bit clear.
		return Value{Ref: "zeroinitializer", Ty: t}
	}
	return e.emitScalarZero(t)
}

// genUnpackArrayElemPattern destructures a `for (const [a, b] of gen())` /
// `for await` loop pattern whose element type is itself an array (ADR-00676) —
// `const [a, b]` over a yielded `number[]` binds a=arr[0], b=arr[1]. The
// element arrives as an array value; its data/len are loaded
// and fed to the shared array-pattern unpack core (the same one plain array
// destructuring uses). elemTy is the array element type (elemTy.ElemType is the
// per-slot type the bindings receive).
func (e *Emitter) genUnpackArrayElemPattern(agg string, elemTy Type, elems []ast.ArrayPatternElem) error {
	dataReg, lenReg := e.arrayParts(Value{Ref: agg, Ty: elemTy})
	return e.unpackArrayPatternInto(dataReg, lenReg, *elemTy.ElemType, elems)
}

// genLoadElemAt loads a generator element value (the {value} slot of a
// `{value,done}` result object, or an async request-queue sent-value slot)
// from an already-GEP'd address, as the header pointer for an
// array element type or a plain scalar/pointer load otherwise (ADR-00676).
func (e *Emitter) genLoadElemAt(gepReg string, elemTy Type) string {
	if elemTy.IsArray() {
		// The slot holds a header pointer (TDD-00213 Stage 2), which the generator
		// machinery carries in registers. The
		// slot is always written before read, so no null guard is needed.
		return e.loadArraySlotAggregate(gepReg, elemTy).Ref
	}
	reg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", reg, StructFieldIR(elemTy), gepReg, elemTy.Align()))
	return reg
}

// genStoreElemAt stores a generator element VALUE register into an already-GEP'd
// state slot — the header-pointer store for an array element (TDD-00213
// Stage 2) or a plain store otherwise.
// valRef is the element value register (the header pointer for an array).
func (e *Emitter) genStoreElemAt(gepReg string, elemTy Type, valRef string) {
	if elemTy.IsArray() {
		e.storeArrayFieldHeader(gepReg, Value{Ref: valRef, Ty: elemTy})
		return
	}
	e.heapStore(StructFieldIR(elemTy), valRef, gepReg, elemTy.Align())
}

// genDefineLoopVar binds a `for (const x of gen())` / `for await` loop
// variable slot once, before the loop (ADR-00676). An array element type
// binds a stable header slot (alloca ptr, object-reference array model,
// TDD-00127) that each iteration re-points via genStoreLoopVar; every other
// type binds a plain alloca of its own IR. Returns the slot register.
func (e *Emitter) genDefineLoopVar(name string, elemTy Type) string {
	varPtr := e.freshReg()
	if elemTy.IsArray() {
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", varPtr))
	} else {
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", varPtr, StructFieldIR(elemTy), elemTy.Align()))
	}
	e.define(name, Symbol{Ptr: varPtr, Ty: elemTy})
	return varPtr
}

// genStoreLoopVar writes one iteration's element value into a loop-var
// slot made by genDefineLoopVar. An array element's header pointer is
// stored into the stable ptr slot (a
// per-iteration reassignment, object-reference model); every other type
// stores its scalar/pointer value directly.
func (e *Emitter) genStoreLoopVar(varPtr string, elemTy Type, val string) {
	if elemTy.IsArray() {
		header := e.boxArrayValue(Value{Ref: val, Ty: elemTy})
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", header, varPtr))
		return
	}
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(elemTy), val, varPtr, elemTy.Align()))
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
	// The declared parameters the call passes (a spread among them: not
	// known), for the body's `arguments`.
	passed := len(args)
	for _, a := range args {
		if _, ok := a.(*ast.SpreadElement); ok {
			passed = -1
			break
		}
	}
	regular := len(info.ParamTypes)
	if info.HasRest {
		regular--
	}
	if passed > regular {
		passed = regular
	}
	// Untyped JavaScript may omit any argument (undefined) or pass extra
	// ones, evaluated in order after the declared ones and dropped.
	var extraArgs []ast.Expression
	if e.compatJS() {
		regular := len(info.ParamTypes)
		if info.HasRest {
			regular--
		}
		if !info.HasRest && len(args) > regular {
			extraArgs = args[regular:]
			args = args[:regular:regular]
		}
		if len(args) < regular {
			args = append([]ast.Expression(nil), args...)
			for len(args) < regular {
				args = append(args, ast.NewNullLiteral(true, pos))
			}
		}
	}
	if info.HasRest {
		// The trailing arguments become the rest parameter's array.
		regular := len(info.ParamTypes) - 1
		if len(args) < regular {
			return Value{}, fmt.Errorf("%d:%d: generator expects at least %d argument(s), got %d", pos.Line, pos.Col, regular, len(args))
		}
		packed := append(append([]ast.Expression(nil), args[:regular]...), ast.NewArrayLiteral(args[regular:], pos))
		args = packed
	}
	if !info.HasRest && len(args) < len(info.ParamTypes) && len(args) >= info.MinArgs {
		// Omitted optional parameters take undefined (their defaults fill in).
		args = append([]ast.Expression(nil), args...)
		for len(args) < len(info.ParamTypes) {
			args = append(args, ast.NewNullLiteral(true, pos))
		}
	}
	if len(args) != len(info.ParamTypes) {
		return Value{}, fmt.Errorf("%d:%d: generator expects %d argument(s), got %d", pos.Line, pos.Col, len(info.ParamTypes), len(args))
	}
	paramVals := make([]Value, len(args))
	for i, argExpr := range args {
		if pty := info.ParamTypes[i]; pty.IsArray() {
			val, err := e.emitExprWithObjectHint(argExpr, pty)
			if err != nil {
				return Value{}, err
			}
			paramVals[i] = e.convert(val, pty)
			continue
		}
		if pty := info.ParamTypes[i]; isNullableScalar(pty) {
			// A nullable-scalar parameter keeps its presence bit: its
			// __paramN slot is the { i1, T } aggregate (TDD-00064 Stage 3).
			agg, err := e.emitNullableScalarBoxedValue(argExpr, pty)
			if err != nil {
				return Value{}, err
			}
			paramVals[i] = Value{Ref: agg, Ty: pty}
			continue
		}
		val, err := e.emitExpr(argExpr)
		if err != nil {
			return Value{}, err
		}
		paramVals[i] = e.convert(val, info.ParamTypes[i])
	}
	for _, x := range extraArgs {
		if _, err := e.emitExpr(x); err != nil {
			return Value{}, err
		}
	}
	env, err := e.packGeneratorEnv(info, pos)
	if err != nil {
		return Value{}, err
	}
	e.genArgc = fmt.Sprint(passed + 1)
	return e.emitGeneratorConstructValues(info, thisRef, paramVals, env), nil
}

// emitGeneratorConstructValues is the construction core over already-evaluated
// parameter Values and an already-packed env pointer register ("null" when
// capture-free): fiber ctx/stack setup, field init, __paramN/__this/__env
// stores. Shared by the expression-level construction above and the
// first-class generator-value thunk (emitGeneratorCtorClosure), which packs
// the env at the escape point and constructs later.
func (e *Emitter) emitGeneratorConstructValues(info *GeneratorInfo, thisRef string, paramVals []Value, envReg string) Value {
	// Taken first: emitting the instance's prototype may emit the
	// generator's thunk, itself a construction.
	argc := e.genArgc
	if argc == "" {
		argc = "0"
	}
	e.genArgc = ""
	e.ensureGeneratorRuntime()

	genTy := info.GenTy
	genObj := e.freshReg()
	e.emitObjMallocInto(genObj, genTy)

	ctxSize, ssSpOff, ssSizeOff, ucLinkOff := e.ucontextLayout()
	ctxReg := e.freshReg()
	stackReg := e.freshReg()
	e.heapMallocInto(ctxReg, fmt.Sprint(ctxSize))
	stackField := stackReg
	if e.genStackOwned() {
		// Under -mm=gc the field holds the stack's owner cell, whose mark
		// procedure scans the guarded stack (gcshim.c); its first word is
		// the stack.
		e.declareFn("__kml_gen_stack_alloc", "declare ptr @__kml_gen_stack_alloc(i64)")
		stackField = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_gen_stack_alloc(i64 %d)", stackField, fiberStackBytes))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", stackReg, stackField))
	} else if !e.isGCMode() && e.opts.Target.OS() != "windows" {
		// A guard page under the stack, so an overflow throws (TDD-00249).
		e.declareFn("__kml_fiber_stack_alloc", "declare ptr @__kml_fiber_stack_alloc(i64)")
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fiber_stack_alloc(i64 %d)", stackReg, fiberStackBytes))
	} else {
		e.heapMallocInto(stackReg, fmt.Sprint(fiberStackBytes))
	}
	e.emitInstr(fmt.Sprintf("call void @getcontext(ptr %s)", ctxReg))
	spGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", spGep, ctxReg, ssSpOff))
	e.heapStore("ptr", stackReg, spGep, 8)
	szGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", szGep, ctxReg, ssSizeOff))
	e.heapStore("i64", fmt.Sprint(fiberStackBytes), szGep, 8)
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
	e.heapStore("ptr", "null", linkGep, 8)
	e.emitInstr(fmt.Sprintf("call void (ptr, ptr, i32, ...) @makecontext(ptr %s, ptr %s, i32 0)", ctxReg, info.BodyFuncName))

	e.storeGeneratorField(genObj, genTy, GeneratorProtoField, "i64", e.emitGenInstanceProto(info))
	e.storeGeneratorField(genObj, genTy, GeneratorArgcField, "i64", argc)
	e.storeGeneratorField(genObj, genTy, GeneratorCtxField, "ptr", ctxReg)
	e.storeGeneratorField(genObj, genTy, GeneratorStackField, "ptr", stackField)
	e.storeGeneratorField(genObj, genTy, GeneratorCallerCtxField, "ptr", "null")
	e.storeGeneratorField(genObj, genTy, GeneratorStartedField, "i1", "0")
	e.storeGeneratorField(genObj, genTy, GeneratorHasRetField, "i1", "0")
	e.storeGeneratorField(genObj, genTy, GeneratorDoneField, "i1", "0")
	zeroElem := e.genZeroElem(info.ElemTy)
	e.storeGeneratorField(genObj, genTy, GeneratorYieldedField, StructFieldIR(zeroElem.Ty), zeroElem.Ref)
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, "i64", fmt.Sprint(nbUndefined))
	e.storeGeneratorField(genObj, genTy, GeneratorResumeModeField, "i64", "0")
	e.storeGeneratorField(genObj, genTy, GeneratorThrownField, "ptr", "null")
	// This generator's own isolated jmpbuf stack (jmpstack.c, as a coroutine
	// task's), so its body's try-frames never interleave on the shared
	// global stack with the caller's own trys while the two fibers alternate
	// (TDD-00086). __jmpTop tracks its top across suspension; __genError carries an
	// uncaught body throw back to the caller.
	jmpStk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_jmp_stack_new()", jmpStk))
	e.storeGeneratorField(genObj, genTy, GeneratorJmpStkField, "ptr", jmpStk)
	if e.genStackOwned() {
		// Freed with the stack, once the generator is unreachable.
		e.declareFn("__kml_gen_stack_own", "declare void @__kml_gen_stack_own(ptr, ptr, ptr)")
		e.emitInstr(fmt.Sprintf("call void @__kml_gen_stack_own(ptr %s, ptr %s, ptr @__kml_jmp_stack_free)", stackField, jmpStk))
	}
	e.storeGeneratorField(genObj, genTy, GeneratorJmpTopField, "i64", "0")
	e.storeGeneratorField(genObj, genTy, GeneratorGenErrorField, "ptr", "null")
	e.storeGeneratorField(genObj, genTy, GeneratorPendingQField, "ptr", "null")
	e.storeGeneratorField(genObj, genTy, GeneratorParkedField, "i64", "0")
	e.storeGeneratorField(genObj, genTy, GeneratorReqHeadField, "ptr", "null")
	e.storeGeneratorField(genObj, genTy, GeneratorReqTailField, "ptr", "null")

	for i, val := range paramVals {
		if val.Ty.IsArray() {
			// The caller's array itself (its live header), not a copy.
			idx, _, _ := genTy.FieldIndex(fmt.Sprintf("__param%d", i))
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, genTy.StructIR(), genObj, idx))
			e.heapStore("ptr", val.Ref, gep, 8)
			continue
		}
		e.storeGeneratorField(genObj, genTy, fmt.Sprintf("__param%d", i), StructFieldIR(val.Ty), val.Ref)
	}
	if thisRef != "" {
		e.storeGeneratorField(genObj, genTy, GeneratorThisField, "ptr", thisRef)
	}
	// A nested generator's closure env (TDD-00094): the captured cells, packed
	// by packGeneratorEnv (shared by reference with the enclosing scope,
	// mirroring an arrow's capture); "null" for a top-level/capture-free one.
	e.storeGeneratorField(genObj, genTy, GeneratorEnvField, "ptr", envReg)

	if info.EagerStart {
		// Run the parameter defaults now, as the call does; a throwing one
		// throws here.
		e.emitGeneratorSwapCore(genObj, genTy, info.IsAsync)
		e.storeGeneratorField(genObj, genTy, GeneratorCallerCtxField, "ptr", "null")
		genErr := e.loadGeneratorField(genObj, genTy, GeneratorGenErrorField)
		isErr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isErr, genErr.Ref))
		rethrowL, contL := e.freshLabel("gen.dflt.throw"), e.freshLabel("gen.dflt.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, rethrowL, contL))
		e.emitLabel(rethrowL)
		tag, pay := e.loadThrownCell(genErr.Ref)
		e.excThrowAny(tag, pay)
		e.emitTerminator("unreachable")
		e.emitLabel(contL)
	}
	return Value{Ref: genObj, Ty: genTy}
}

// hasBodyDefault reports a parameter list with a default its body fills.
func hasBodyDefault(params []ast.Param) bool {
	for _, p := range params {
		if bodyDefaulted(p) {
			return true
		}
	}
	return false
}

// emitGeneratorCtorClosure realizes a generator referenced in VALUE position
// (`return g`, `const G = g`, passing `g` as an argument) as a first-class
// closure whose call constructs a fresh instance — matching JS, where a
// generator function is an ordinary function value. The captured env is
// packed HERE, at the escape point, while the enclosing scope's cells are
// still resolvable; the thunk carries it as its closure env and threads it
// into every instance it constructs (cells are shared heap boxes, so
// enclosing-scope mutations stay visible by reference, and instances made
// after the enclosing frame returned keep working).
func (e *Emitter) emitGeneratorCtorClosure(info *GeneratorInfo, displayName string, pos ast.Pos) (Value, error) {
	env, err := e.packGeneratorEnv(info, pos)
	if err != nil {
		return Value{}, err
	}

	if info.ctorFn == "" {
		info.ctorFn = e.emitGeneratorCtorThunk(info, displayName)
	}
	if len(info.Captures) > 0 {
		hdr := e.buildBuiltinClosure(info.ctorFn, env)
		return Value{Ref: hdr, Ty: generatorValueType(info)}, nil
	}
	// A generator capturing nothing is one function value: every reference
	// reads the same closure (`G === G`), made on first use.
	if info.valueGlobal == "" {
		info.valueGlobal = genSymbol(info, "@__kml_genval")
		e.emitGlobal(info.valueGlobal + " = internal global ptr null, align 8")
	}
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cur, info.valueGlobal))
	isNull := e.ptrIsNull(cur)
	mk, done := e.freshLabel("genval.mk"), e.freshLabel("genval.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, mk, done))
	e.emitLabel(mk)
	hdr := e.buildBuiltinClosure(info.ctorFn, "null")
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, info.valueGlobal))
	e.emitTerminator(fmt.Sprintf("br label %%%s", done))
	e.emitLabel(done)
	v := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, info.valueGlobal))
	return Value{Ref: v, Ty: generatorValueType(info)}, nil
}

// genSymbol names one of info's helpers after its body: by source position
// in the builtin library, so library code spells it the same in every
// program.
func genSymbol(info *GeneratorInfo, prefix string) string {
	b := strings.TrimPrefix(info.BodyFuncName, "@")
	quoted := strings.HasPrefix(b, `"`)
	name := strings.TrimPrefix(prefix, "@") + strings.TrimPrefix(strings.Trim(b, `"`), "__generator_body")
	if quoted {
		return `@"` + name + `"`
	}
	return "@" + name
}

// emitGeneratorCtorThunk emits the function a generator's value calls: it
// constructs an instance from its arguments and closure env.
func (e *Emitter) emitGeneratorCtorThunk(info *GeneratorInfo, displayName string) string {
	fn := genSymbol(info, "@__kml_genctor")
	info.ctorFn = fn // named before its body, which may read its value (genproto.go)
	// Function.length: the parameters before the first optional or rest one.
	arity := info.MinArgs
	e.registerFnMeta(fn, unmangleTopLevelName(displayName), arity, fnKindOf(info.IsAsync, true))
	e.fnMetaSource(fn, info.Src)
	restore := e.beginThunkEmit()
	paramDecl := "ptr %env"
	paramVals := make([]Value, len(info.ParamTypes))
	for i, pt := range info.ParamTypes {
		if pt.IsArray() {
			// The closure ABI passes an array as (header ptr, i64 len) —
			// the header is the array value for the store.
			paramDecl += fmt.Sprintf(", ptr %%p%d_ptr, i64 %%p%d_len", i, i)
			paramVals[i] = e.arrayValueFromHeaderReg(fmt.Sprintf("%%p%d_ptr", i), pt)
			continue
		}
		if isNullableScalar(pt) {
			// Passed as its { i1, T } aggregate, as any closure's is.
			paramDecl += fmt.Sprintf(", %s %%p%d", nullableScalarStorageIR(pt), i)
		} else {
			paramDecl += fmt.Sprintf(", %s %%p%d", pt.IR, i)
		}
		paramVals[i] = Value{Ref: fmt.Sprintf("%%p%d", i), Ty: pt}
	}
	if countsArgs(info.HasRest, info.ParamNames) {
		// The count the caller stored (TDD-00210), for the body's
		// `arguments`.
		e.ensureArgc()
		c := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr @__kml_argc, align 8", c))
		e.emitInstr("store i64 -1, ptr @__kml_argc, align 8")
		inc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", inc, c))
		e.genArgc = inc // -1 (not known) becomes 0
	}
	inst := e.emitGeneratorConstructValues(info, "", paramVals, "%env")
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine ptr %s(%s) {\nentry:\n%sret ptr %s\n}\n", fn, paramDecl, body, inst.Ref))
	return fn
}

// generatorValueType is a generator function's type as a value: a function
// of its parameters returning the instance.
func generatorValueType(info *GeneratorInfo) Type {
	ft := FuncType(info.ParamTypes, info.GenTy)
	ft.FuncHasRest = info.HasRest
	ft.FuncCountsArgs = countsArgs(info.HasRest, info.ParamNames)
	for _, o := range info.Optional {
		if o {
			ft.FuncParamOptional = append([]bool(nil), info.Optional...)
			break
		}
	}
	return ft
}

// packGeneratorEnv boxes a nested generator's captured cells into a fresh env
// struct in the CURRENT scope, returning its pointer register ("null" for a
// capture-free generator). Split out of construction so a generator escaping
// as a first-class value (emitGeneratorCtorClosure) can pack the env at the
// escape point — where the enclosing scope is still live — and hand it to the
// constructor thunk that runs later.
func (e *Emitter) packGeneratorEnv(info *GeneratorInfo, pos ast.Pos) (string, error) {
	if len(info.Captures) == 0 {
		return "null", nil
	}
	env := e.freshReg()
	envIR := envStructIR(info.Captures)
	e.heapMallocInto(env, fmt.Sprint(envStructSize(info.Captures)))
	for i, cap := range info.Captures {
		sym, ok := e.lookup(cap.Name)
		if !ok {
			return "", fmt.Errorf("%d:%d: generator captures '%s' but it is not in scope at construction", pos.Line, pos.Col, cap.Name)
		}
		cellPtr := sym.Ptr
		if !sym.Boxed {
			cellPtr = e.promoteCaptureToCell(cap.Name, cap.Ty, sym.Ptr, sym.IsConst)
		}
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", slotReg, envIR, env, i))
		e.heapStore("ptr", cellPtr, slotReg, 8)
	}
	return env, nil
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
	if val.Ty.IsArray() {
		// Keep the value's live header, so the caller reads the yielded array.
		idx, _, _ := gctx.genTy.FieldIndex(GeneratorYieldedField)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, gctx.genTy.StructIR(), gctx.genObjReg, idx))
		e.storeArrayFieldHeader(gep, val)
	} else {
		e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorYieldedField, StructFieldIR(val.Ty), val.Ref)
	}
	e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorDoneField, "i1", doneLit)
	e.genSwapOut(gctx.genObjReg, gctx.genTy)
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
	switch {
	case ex.Argument != nil && gctx.genTy.GeneratorIsAsync:
		v, err := e.emitAsyncGenOperand(ex.Argument, gctx.elemTy)
		if err != nil {
			return Value{}, err
		}
		val = e.convert(v, gctx.elemTy)
	case ex.Argument != nil:
		// Hint the element type so a `yield [a, b]` / `yield { … }` whose slot is
		// a tuple/object builds that shape directly rather than a bare array
		// aggregate the tuple slot can't hold (ADR-00257).
		v, err := e.emitExprWithObjectHint(ex.Argument, gctx.elemTy)
		if err != nil {
			return Value{}, err
		}
		val = e.convert(v, gctx.elemTy)
	default:
		val = e.genZeroElem(gctx.elemTy)
		if err := e.asyncGenAwaitTick(gctx, ex.GetPos()); err != nil {
			return Value{}, err
		}
	}
	e.emitGeneratorSwapToCaller(gctx, val, false)
	return e.emitYieldResumeDispatch(gctx)
}

// asyncGenAwaitTick is the Await an async generator's `yield v` and
// `return v` make on v before they complete the request: one microtask
// turn, so the request's promise settles a tick after an async function's
// would.
func (e *Emitter) asyncGenAwaitTick(gctx *generatorEmitCtx, pos ast.Pos) error {
	if gctx == nil || !gctx.genTy.GeneratorIsAsync {
		return nil
	}
	_, err := e.emitAwait(ast.NewAwaitExpression(ast.NewNullLiteral(true, pos), pos))
	return err
}

// awaitedIn is the type an async generator yields or returns for a value of
// type t (a promise's settled type: `yield p` awaits p); t elsewhere.
func awaitedIn(async bool, t Type) Type {
	if async && t.IsPromise() && !t.IsDynamic() {
		if t.PromiseType == nil {
			return TypeUndefined
		}
		return *t.PromiseType
	}
	return t
}

// emitAsyncGenOperand evaluates a `yield`/`return` operand in an async
// generator and awaits it, as the spec's AsyncGeneratorYield and return do:
// a promise yields its settled value, any other value takes one microtask
// turn.
func (e *Emitter) emitAsyncGenOperand(arg ast.Expression, elemTy Type) (Value, error) {
	v, err := e.emitExprWithObjectHint(arg, elemTy)
	if err != nil {
		return Value{}, err
	}
	if v.Ty.IsPromise() || v.Ty.IsDynamic() {
		return e.emitAwait(ast.NewAwaitExpression(e.bindValue(v, arg.GetPos()), arg.GetPos()))
	}
	if _, err := e.emitAwait(ast.NewAwaitExpression(ast.NewNullLiteral(true, arg.GetPos()), arg.GetPos())); err != nil {
		return Value{}, err
	}
	return v, nil
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
	if !innerTy.IsGenerator() && !innerTy.Nullable() && (innerTy.IsArray() || innerTy.IsSet() || innerTy.IsMap() || isForOfStringTy(innerTy)) && !gctx.genTy.GeneratorIsAsync {
		// A builtin iterable: each element is yielded, and the delegation
		// evaluates to undefined (an array iterator's return value).
		pos := ex.GetPos()
		v := "__kml_ystar_" + e.freshReg()[1:]
		loop := ast.NewForOfStatement("const", v, ex.Argument, ast.NewBlockStatement([]ast.Statement{
			ast.NewExpressionStatement(ast.NewYieldExpression(ast.NewIdentifier(v, pos), false, pos), pos),
		}, pos), pos)
		if err := e.emitStmt(loop); err != nil {
			return Value{}, err
		}
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	if !innerTy.IsGenerator() {
		// yield* over a user `Symbol.asyncIterator` iterable (TDD-00094) — only
		// inside an async generator, since awaiting each step needs an async
		// context. Delegates by re-yielding each awaited element.
		if innerTy.IsClass() && gctx != nil && gctx.genTy.GeneratorIsAsync {
			if info, ok := e.classes[innerTy.ClassName]; ok {
				if _, ok := info.MethodSigs[asyncIteratorMethodName]; ok {
					return e.emitYieldStarAsyncIterable(gctx, ex, innerTy)
				}
			}
		}
		return e.emitYieldStarAny(gctx, ex)
	}
	isAsyncInner := innerTy.GeneratorIsAsync
	if isAsyncInner && !gctx.genTy.GeneratorIsAsync {
		return Value{}, fmt.Errorf("%d:%d: yield* over an async generator is only allowed inside an async generator (awaiting each step needs an async context)", ex.GetPos().Line, ex.GetPos().Col)
	}
	innerElem := *innerTy.GeneratorElemType
	if innerElem.IR != gctx.elemTy.IR && gctx.elemTy.IsDynamic() && !isAsyncInner {
		// An outer generator yielding a union (boxed elements) delegates
		// through the dynamic protocol, which boxes each inner value.
		return e.emitYieldStarAny(gctx, ex)
	}
	if innerElem.IR != gctx.elemTy.IR {
		return Value{}, fmt.Errorf("%d:%d: yield* inner generator element type must match the outer generator's", ex.GetPos().Line, ex.GetPos().Col)
	}
	resultTy := genNextResultType(innerElem)
	if isAsyncInner {
		resultTy = asyncGenResultType(innerElem)
	}

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
		// An array-typed value field holds a header pointer (TDD-00213 Stage 2),
		// the array value the caller re-yields/returns.
		if field == "value" {
			return e.loadGenResultValue(gep, resultTy, fieldTy)
		}
		return e.genLoadElemAt(gep, fieldTy)
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
	retSent := e.genSentAs(gctx.genObjReg, gctx.genTy, innerElem)
	rr, err := stepReturn(innerN, retSent)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rr, resultA))
	finalRet := loadResultField("value", innerElem)
	if err := e.emitPendingFinallys(); err != nil {
		return Value{}, err
	}
	e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorHasRetField, "i1", "1") // completes with a value
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
	if !iterVal.Ty.IsClass() {
		return Value{}, fmt.Errorf("%d:%d: [Symbol.asyncIterator]() must return a class instance with a next() method (TDD-00094)", pos.Line, pos.Col)
	}
	iterTy := iterVal.Ty
	iterInfo, ok := e.classes[iterTy.ClassName]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: unknown iterator class '%s'", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	nextSig, ok := iterInfo.MethodSigs["next"]
	if !ok || !nextSig.RetType.IsPromise() || nextSig.RetType.PromiseType == nil {
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
		coerced := e.convert(arg, paramTy)
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
		return ok && sig.RetType.IsPromise() && sig.RetType.PromiseType != nil
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
	valReg := e.genLoadElemAt(vGep, elemTy)
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
		if err := forwardCall("throw", e.thrownCellAny(thrown.Ref)); err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", procL))
	} else {
		e.ensureExceptionHelpers()
		tag, pay := e.loadThrownCell(thrown.Ref)
		e.excThrowAny(tag, pay)
		e.emitTerminator("unreachable")
	}

	e.emitLabel(aiReturnL)
	rv := e.genSentAs(gctx.genObjReg, gctx.genTy, elemTy)
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
		rvVal := e.genLoadElemAt(rvGep, elemTy)
		if err := e.emitPendingFinallys(); err != nil {
			return Value{}, err
		}
		e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorHasRetField, "i1", "1") // completes with a value
		e.emitGeneratorSwapToCaller(gctx, Value{Ref: rvVal, Ty: gctx.elemTy}, true)
		e.emitTerminator("ret void")
	} else {
		if err := e.emitPendingFinallys(); err != nil {
			return Value{}, err
		}
		e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorHasRetField, "i1", "1") // completes with a value
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
	tag, pay := e.loadThrownCell(thrown.Ref)
	e.excThrowAny(tag, pay)
	e.emitTerminator("unreachable")

	// mode 2: run enclosing finallys, then complete the generator with __sent.
	e.emitLabel(returnL)
	rv := e.genSentAs(gctx.genObjReg, gctx.genTy, gctx.elemTy)
	e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorHasRetField, "i1", "1")
	// An async generator resumed by return(v) awaits v before completing
	// (AsyncGeneratorUnwrapYieldResumption).
	if gctx.genTy.GeneratorIsAsync {
		aw, err := e.emitAwait(ast.NewAwaitExpression(e.bindValue(rv, ast.Pos{}), ast.Pos{}))
		if err != nil {
			return Value{}, err
		}
		rv = e.convert(aw, gctx.elemTy)
	}
	if err := e.emitPendingFinallys(); err != nil {
		return Value{}, err
	}
	e.emitGeneratorSwapToCaller(gctx, rv, true)
	e.emitTerminator("ret void")

	// mode 0: the ordinary resume — __sent is this yield expression's value.
	// The stored slot is typed as the generator's element type (this project
	// collapses TNext onto TYield — `.next(v)` coerce-checks v into elemTy), but
	// a `yield` *expression* is TypeScript's `TNext`, which defaults to `any`. A
	// resume value consumed as an rvalue (`obj.foo = yield`, `x = yield`) must
	// therefore present as a dynamic box so its downstream store/coerce converts
	// to whatever concrete target receives it — without this, an element type
	// that differs from the assignment target (e.g. a bare `yield` resumed into
	// a string field) emitted a raw store of the element word into the target's
	// slot (invalid IR: `store ptr <i64>`).
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
		var v Value
		var err error
		if gctx.genTy.GeneratorIsAsync {
			v, err = e.emitAsyncGenOperand(r.Value, gctx.elemTy)
		} else {
			v, err = e.emitExprWithObjectHint(r.Value, gctx.elemTy)
		}
		if err != nil {
			return err
		}
		val = e.convert(v, gctx.elemTy)
		e.storeGeneratorField(gctx.genObjReg, gctx.genTy, GeneratorHasRetField, "i1", "1")
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
	restoreBuilder := e.beginThunkEmit()
	// A fresh context, as every function emitter's (emit_func.go): none of
	// the enclosing function's loop, cleanup, async or constructor state.
	// Restored via defer, not a plain assignment — see emitFunctionDeclAs
	// for why a plain assignment isn't safe against an error return partway
	// through body emission.
	savedCtx := e.funcContext
	e.funcContext = &funcContext{
		inModuleTask: savedCtx.inModuleTask,
	}
	// Eager-boxing capture set (see hoistedCaptures): a local or parameter a
	// nested closure captures is boxed where it is declared, not where the
	// closure is made — a closure made in one branch would otherwise box it
	// in a block that does not dominate its later uses.
	savedHoistedCaptures := e.hoistedCaptures
	paramNames := make([]string, len(decl.Params))
	for i, p := range decl.Params {
		paramNames[i] = p.Name
	}
	e.hoistedCaptures = nil
	if decl.Body != nil {
		e.hoistedCaptures = capturedLocalNames(decl.Body.Body, paramNames)
	}
	defer func() {
		e.hoistedCaptures = savedHoistedCaptures
		restoreBuilder()
		e.funcContext = savedCtx
	}()
	// The body runs on its own fiber: its base frame, then its own. A body
	// with no frame of its own (one the compiler synthesized, as a static
	// generator method's) still cuts its callees' frames from its creator's,
	// which may be gone when it resumes.
	if e.fileOfFunc(decl) != "" || e.programHasFrames() {
		e.enterFiberBase()
	}
	e.enterFrame(decl, unmangleTopLevelName(decl.Name))

	e.scopes = nil
	e.currentRetType = info.ElemTy
	e.pushScope()

	// Read the pending-object handoff global exactly once, at entry — see
	// ensureGeneratorRuntime's own doc comment for why this is safe (only
	// ever meaningful on this generator's own first entry; every later
	// resume picks up mid-function with genObjReg still live as an
	// ordinary SSA value on this generator's own never-torn-down stack,
	// never re-reading the global).
	// Ensure the runtime global is declared here, not only on the construction
	// path: a generator that is declared but never constructed (`function* g(){};
	// typeof g.prototype`) still emits this body and loads the global, so without
	// this the global is referenced-but-undefined (invalid IR). Idempotent.
	e.ensureGeneratorRuntime()
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
		if classInfo, ok := e.classes[classTy.ClassName]; ok && classInfo.BaseClass != "" {
			baseTy := e.classes[classInfo.BaseClass].Ty
			e.define("super", Symbol{Ptr: thisPtr, Ty: baseTy})
		}
	}

	for i, p := range decl.Params {
		pty := info.ParamTypes[i]
		if pty.IsArray() && !e.hoistedCaptures[p.Name] {
			// An array parameter: the named array's header slot, holding the
			// header the instance's field points to (the caller's array).
			idx, _, _ := info.GenTy.FieldIndex(fmt.Sprintf("__param%d", i))
			gep, hdr, slot := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, info.GenTy.StructIR(), genObjReg, idx))
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, gep))
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hdr, slot))
			e.define(p.Name, Symbol{Ptr: slot, Ty: pty})
			continue
		}
		val := e.loadGeneratorField(genObjReg, info.GenTy, fmt.Sprintf("__param%d", i))
		if isNullableScalar(pty) {
			localPtr := e.freshReg()
			agg := nullableScalarStorageIR(pty)
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", localPtr, agg, storageAlign(pty)))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", agg, val.Ref, localPtr, storageAlign(pty)))
			e.define(p.Name, Symbol{Ptr: localPtr, Ty: pty})
			continue
		}
		if e.hoistedCaptures[p.Name] {
			e.boxHoistedCapture(p.Name, pty, val.Ref, false, false)
			continue
		}
		localPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", localPtr, pty.IR, pty.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", pty.IR, val.Ref, localPtr, pty.Align()))
		e.define(p.Name, Symbol{Ptr: localPtr, Ty: pty})
	}
	if err := e.synthesizeArgumentsObject(decl, FuncSig{}); err != nil {
		return err
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
			e.define(cap.Name, Symbol{Ptr: cellReg, Ty: cap.Ty, Boxed: true, IsConst: cap.Sym.IsConst, ForwardName: cap.Sym.ForwardName})
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
		bodyStartL := e.freshLabel("gen.body")
		catchAllL := e.freshLabel("gen.catchall")
		e.excEnter(catchAllL, bodyStartL)

		e.emitLabel(catchAllL)
		// Reload genObj from the handoff global — a longjmp may have clobbered the
		// entry-block SSA register, and no other generator ran on this fiber since
		// the swap-in that wrote it (ensureGeneratorRuntime's own reasoning).
		reGen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_gen_pending_obj, align 8", reGen))
		// The thrown value as it was thrown (any value, not only an Error):
		// a {tag, payload} cell the caller side re-throws or rejects with.
		errReg := e.emitThrownCell()
		e.storeGeneratorField(reGen, info.GenTy, GeneratorGenErrorField, "ptr", errReg)
		zeroY := e.genZeroElem(info.ElemTy)
		e.storeGeneratorField(reGen, info.GenTy, GeneratorYieldedField, StructFieldIR(zeroY.Ty), zeroY.Ref)
		e.storeGeneratorField(reGen, info.GenTy, GeneratorDoneField, "i1", "1")
		e.genSwapOut(reGen, info.GenTy)
		e.emitTerminator("ret void")

		e.emitLabel(bodyStartL)
	}

	// Parameter defaults, with captures in scope and a throw caught above.
	genParamTypes := make([]Type, len(decl.Params))
	for i, p := range decl.Params {
		if sym, ok := e.lookup(p.Name); ok {
			genParamTypes[i] = sym.Ty
		}
	}
	if err := e.emitParamDefaultPrologue(decl.Params, genParamTypes, decl.Body, decl.GetPos()); err != nil {
		return err
	}
	// Destructured parameters, unpacked from their hidden plain ones.
	for _, st := range e.genPatternInits[decl] {
		if err := e.emitStmt(st); err != nil {
			return err
		}
	}
	if info.EagerStart {
		// Suspended at the start, as a fresh generator is; the construction
		// swapped in only to run the defaults.
		e.emitGeneratorSwapToCaller(gctx, e.genZeroElem(info.ElemTy), false)
		if _, err := e.emitYieldResumeDispatch(gctx); err != nil {
			return err
		}
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

	e.writeFunc(fmt.Sprintf("void %s()", info.BodyFuncName), e.allocas.String()+e.body.String())

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
	ctxSize, _, _, _ := e.ucontextLayout()
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
	e.fiberSwitch(saveCtx, ownCtx.Ref)
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
	// A caller context is set only while the body runs: resuming a running
	// generator (its own body calling next()) is a TypeError.
	running := e.loadGeneratorField(genObj, genTy, GeneratorCallerCtxField)
	isRun := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isRun, running.Ref))
	runL, okL := e.freshLabel("gen.running"), e.freshLabel("gen.resume")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRun, runL, okL))
	e.emitLabel(runL)
	e.emitThrowTypeError("Generator is already running")
	e.emitLabel(okL)
	e.emitGeneratorSwapCore(genObj, genTy, false)
	e.storeGeneratorField(genObj, genTy, GeneratorCallerCtxField, "ptr", "null")
	e.emitGenReleaseIfDone(genObj, genTy)

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
	tag, pay := e.loadThrownCell(genErr.Ref)
	e.excThrowAny(tag, pay)
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

	var sentVal Value
	if len(args) == 1 {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		// The sent value is the `yield` expression's value: TNext, its own
		// type (`any` unless declared), boxed into the sent slot.
		sentVal, err = e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
	} else {
		sentVal = Value{Ty: TypeUndefined}
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
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, "i64", e.genBoxSent(sentVal))
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
	e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", yieldedReg, genElemValIR(elemTy), swapYielded.Ref, swapEndL, skipZero.Ref, skipL))
	yielded := Value{Ref: yieldedReg, Ty: elemTy}
	done := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	return e.buildGenNextResult(resultTy, elemTy, yielded, done)
}

// buildGenNextResult mallocs and populates a `{value, done}` result object from an
// already-loaded yielded value and done flag — shared by `.next()` and `.throw()`
// (TDD-00086).
func (e *Emitter) buildGenNextResult(resultTy, elemTy Type, yielded, done Value) Value {
	resultReg := e.freshReg()
	e.emitObjMallocInto(resultReg, resultTy)
	vIdx, _, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), resultReg, vIdx))
	if elemTy.IsArray() {
		e.storeArrayFieldHeader(vGep, yielded) // {value} slot holds a header pointer (TDD-00213 S2)
	} else {
		e.genStoreElemAt(vGep, elemTy, yielded.Ref)
	}
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), resultReg, dIdx))
	e.heapStore("i1", done.Ref, dGep, 1)
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
	errPtr, err := e.thrownCellOf(ev)
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
	tag, pay := e.loadThrownCell(errPtr)
	e.excThrowAny(tag, pay)
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
		// A mismatched `.return(x)` argument is a clean COMPILE_ERROR, not a raw
		// store of the wrong shape (invalid IR) — see the `.next()` path.
		rv, err = e.convertChecked(v, elemTy, args[0].GetPos(), "generator .return() value")
		if err != nil {
			return Value{}, err
		}
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

	e.storeGeneratorField(genObj, genTy, GeneratorSentField, "i64", e.genBoxSent(rv))
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
func (e *Emitter) emitAsyncGenRejectPromise(q, cell string) {
	// cell is a thrown cell (emitThrownCell/thrownCellOf): the value as
	// thrown, an Error keeping its tag so a reject handler reads its
	// message/name/instanceof (TDD-00207). Settling wakes a parked awaiter (a
	// deferred .next()); a no-op drain in the synchronous no-waiter case.
	tag, pay := e.loadThrownCell(cell)
	e.emitRejectCaught(q, tag, pay)
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
	tag, pay := e.loadThrownCell(genErr.Ref)
	e.emitRejectCaught(q, tag, pay)
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
	e.heapMallocInto(clo, "16")
	cf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", cf, clo))
	e.heapStore("ptr", stepFn, cf, 8)
	ce := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", ce, clo))
	e.heapStore("ptr", genObj, ce, 8)

	// Settled → enqueue the resume now; pending → attach a reaction node.
	e.emitMarkPromiseHandled(pReg)
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
	e.heapMallocInto(node, "16")
	rxP := e.freshReg()
	oldHead := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 4", rxP, promiseStructIR, pReg))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", oldHead, rxP))
	nClo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", nClo, node))
	e.heapStore("ptr", clo, nClo, 8)
	nNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", nNext, node))
	e.heapStore("ptr", oldHead, nNext, 8)
	e.heapStore("ptr", node, rxP, 8)
	e.emitTerminator(fmt.Sprintf("br label %%%s", parkL))

	e.emitLabel(parkL)
	e.storeGeneratorField(genObj, genTy, GeneratorParkedField, "i64", "1")
	e.genSwapOut(genObj, genTy)
	// Resumed by the step fn: the awaited promise is settled now.
	e.storeGeneratorField(genObj, genTy, GeneratorParkedField, "i64", "0")
}

// asyncGenReqNodeIR is the queued-request node layout: {i64 resumeMode,
// ptr sentSlot (malloc'd boxed spill, freed at pop),
// ptr thrown, ptr q, ptr next}.
const asyncGenReqNodeIR = "{ i64, ptr, ptr, ptr, ptr }"

// emitAsyncGenSubmitRequest appends a {mode, sent, thrown, q} request to the
// generator's FIFO — called by .next/.throw/.return when a step is in flight
// (the spec's AsyncGeneratorEnqueue). sentVal may be the zero value for .throw.
func (e *Emitter) emitAsyncGenSubmitRequest(genObj string, genTy Type, elemTy Type, mode string, sentVal Value, thrown string, q string) {
	e.ensureMalloc()
	node := e.freshReg()
	e.heapMallocInto(node, "40")
	slot := e.freshReg()
	e.heapMallocInto(slot, "8")
	e.heapStore("i64", e.genBoxSent(sentVal), slot, 8)
	storeAt := func(idx int, ir, val string) {
		gp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gp, asyncGenReqNodeIR, node, idx))
		e.heapStore(ir, val, gp, 8)
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
	e.heapStore("ptr", node, tn, 8)
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
	resultTy := asyncGenResultType(elemTy)

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
	resultTy := asyncGenResultType(elemTy)
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
	resProp := e.buildAsyncGenResult(resultTy, elemTy, rv, trueVal, "true")
	e.storePromiseValue(qp, resProp)
	// return(v) awaits v first (AsyncGeneratorAwaitReturn): one turn later.
	e.ensurePromiseSettle()
	e.ensureMicrotasks()
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle_later(ptr %s)", qp))
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

	var sentVal Value
	if len(args) == 1 {
		v, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		// The `yield` expression's value (TNext) — see the sync path.
		sentVal, err = e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
	} else {
		sentVal = Value{Ty: TypeUndefined}
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
	resultTy := asyncGenResultType(elemTy)
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
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, "i64", e.genBoxSent(sentVal))
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
	// Named by its key (the generator's layout, which decides the body): it
	// re-enqueues itself, so its body names it.
	name := contentSymbol("@__kml_agen_step.", key)
	e.asyncGenStepFns[key] = name
	elemTy := *genTy.GeneratorElemType
	resultTy := asyncGenResultType(elemTy)
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

	laterSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", laterSlot))
	e.emitInstr(fmt.Sprintf("store i1 0, ptr %s, align 1", laterSlot))
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
	sentBack := e.genSentAs(genObj, genTy, elemTy)
	doneNow := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	e.storePromiseValue(q, e.buildAsyncGenResult(resultTy, elemTy, sentBack, doneNow, "true"))
	// return(v) on a finished generator awaits v first: one turn later, and
	// the requests queued behind it complete after it (laterSlot).
	e.ensurePromiseSettle()
	e.ensureMicrotasks()
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle_later(ptr %s)", q))
	e.emitInstr(fmt.Sprintf("store i1 1, ptr %s, align 1", laterSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", drainL))
	e.emitLabel(dZeroL)
	zeroV := e.genZeroElem(elemTy)
	doneZ := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	e.storePromiseValue(q, e.buildAsyncGenResult(resultTy, elemTy, zeroV, doneZ, "false"))
	e.ensurePromiseSettle()
	e.ensureMicrotasks()
	later := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", later, laterSlot))
	nowL, laterL := e.freshLabel("agstep.zeronow"), e.freshLabel("agstep.zerolater")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", later, laterL, nowL))
	e.emitLabel(laterL)
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle_later(ptr %s)", q))
	e.emitTerminator(fmt.Sprintf("br label %%%s", drainL))
	e.emitLabel(nowL)
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 1)", q))
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
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", psent, pslot))
	e.heapFree(pslot)
	e.heapFree(head.Ref)
	e.storeGeneratorField(genObj, genTy, GeneratorSentField, "i64", psent)
	e.storeGeneratorField(genObj, genTy, GeneratorResumeModeField, "i64", pmode)
	e.storeGeneratorField(genObj, genTy, GeneratorThrownField, "ptr", pthrown)
	e.storeGeneratorField(genObj, genTy, GeneratorPendingQField, "ptr", pq)
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))

	e.emitLabel(retL)
	e.emitInstr("ret void")

	e.writeFunc(fmt.Sprintf("linkonce_odr hidden void %s(ptr %%env)", name), e.allocas.String()+e.body.String())
	return name
}

// settleAsyncGenResult builds the `{value, done}` result object from the
// generator's yielded value + __done flag and settles the promise q fulfilled
// with it (TDD-00085).
func (e *Emitter) settleAsyncGenResult(q, genObj string, genTy, resultTy, elemTy Type, yielded Value) {
	done := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	// The value is the step's while not done, the return value on the step
	// that completed with one, and undefined otherwise (emitUserGenResult).
	hasRet := e.loadGeneratorField(genObj, genTy, GeneratorHasRetField)
	e.storeGeneratorField(genObj, genTy, GeneratorHasRetField, "i1", "0")
	present := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", present, e.boolNot(done.Ref), hasRet.Ref))
	e.settleAsyncGenWith(q, e.buildAsyncGenResult(resultTy, elemTy, yielded, done, present))
}

// settleAsyncGenWith fulfils q with the result object res.
func (e *Emitter) settleAsyncGenWith(q string, res Value) {
	e.storePromiseValue(q, res)
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
	e.emitFreePromise(promReg)
	e.excThrow(errReg)
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
	val := e.loadPromiseValue(promReg, resultTy)
	e.emitFreePromise(promReg)
	return val
}

// generatorGCStackbottomOps returns the gc-mode-only GC_stackbottom
// repoint/restore IR text around a .next() call's own swapcontext — empty
// strings in manual mode. Repoint to the generator's own stack's high end
// before swapping in; right after the swap returns (a mid-body yield or the
// body running to completion alike), restore the stack bottom the *caller*
// had. That is not always the process stack: the caller may itself run on a
// coroutine stack — the module task of a top-level-await entry (ADR-01050),
// an async task, another generator. Restoring the process stack there left
// the collector scanning from a coroutine SP to the process stack base,
// across unmapped memory (a Linux -mm=gc segfault).
func (e *Emitter) generatorGCStackbottomOps(genObj string, genTy Type) (setOp, restoreOp string) {
	if !e.isGCMode() {
		return "", ""
	}
	saved := e.freshReg()
	stack := e.loadGeneratorField(genObj, genTy, GeneratorStackField).Ref
	lo := ""
	if e.genStackOwned() {
		lo, stack = fmt.Sprintf("  %s = load ptr, ptr %s, align 8\n", e.freshReg(), stack), ""
		stack = strings.Fields(lo)[0]
	}
	high := e.freshReg()
	setOp = fmt.Sprintf("%s  %s\n  %s = getelementptr i8, ptr %s, i64 %d\n  %s\n",
		lo, e.gcSBLoad(saved), high, stack, fiberStackBytes, e.gcSBStore(high))
	restoreOp = fmt.Sprintf("  %s\n", e.gcSBStore(saved))
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
	resultTy := asyncGenResultType(elemTy)
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

	// Leaving the loop early (a `break`, `return` or `throw` out of the body)
	// closes the generator — `await it.return()`, running its `finally` —
	// as JavaScript's AsyncIteratorClose does. The generator is bound to a
	// synthetic name the close references.
	genSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", genSlot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", genVal.Ref, genSlot))
	gname := fmt.Sprintf("__kml_forawait_gen_%s", e.freshReg()[1:])
	e.define(gname, Symbol{Ptr: genSlot, Ty: genTy})
	closeStmt := ast.NewExpressionStatement(ast.NewAwaitExpression(
		ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(gname, s.GetPos()), "return", s.GetPos()), nil, s.GetPos()),
		s.GetPos()), s.GetPos())
	e.pendingFinallys = append(e.pendingFinallys, pendingExit{body: []ast.Statement{closeStmt}})
	popClose := func() { e.pendingFinallys = e.pendingFinallys[:len(e.pendingFinallys)-1] }

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	// prom = it.next() (Promise<{value,done}>); await it, re-throwing a rejection.
	prom, err := e.emitGeneratorNextByValue(genVal.Ref, genTy, nil, s.GetPos())
	if err != nil {
		return err
	}
	if e.currentGenerator != nil && e.currentGenerator.genTy.GeneratorIsAsync {
		// inside an async generator's fiber, which is no task: park its step
		e.emitAsyncGenAwaitParkUntilSettled(prom.Ref)
	} else {
		e.emitInstr(fmt.Sprintf("call void @__kml_task_await_ready(ptr %s)", prom.Ref))
	}
	e.emitTaskRethrowIfRejected(prom.Ref)
	resultObj := e.loadPromiseValue(prom.Ref, resultTy)
	e.emitFreePromise(prom.Ref)
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
	loaded := e.loadGenResultValue(vGep, resultTy, elemTy)
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
		if elemTy.IsArray() {
			if err := e.genUnpackArrayElemPattern(loaded, elemTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if elemTy.IsTuple() {
			if err := e.unpackTuplePatternInto(loaded, elemTy, s.ArrayPattern, s.GetPos()); err != nil {
				return err
			}
		} else if err := e.genUnpackIterableElem(Value{Ref: loaded, Ty: elemTy}, s.ArrayPattern, s.GetPos()); err != nil {
			return err
		}
	default:
		e.genStoreLoopVar(varPtr, elemTy, loaded)
	}
	if err := e.emitForOfBody(s); err != nil {
		popClose()
		return err
	}
	popClose()
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(endL)
	// A `break` out of the loop leaves the generator suspended: close it
	// (a no-op on one that already completed).
	return e.emitStmt(closeStmt)
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
	if elemTy.IsPromise() {
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
	loaded := e.genLoadElemAt(vGep, elemTy)

	// Await the yielded value before binding (identity for a plain value).
	boundVal := Value{Ref: loaded, Ty: elemTy}
	boundTy := elemTy
	if elemTy.IsPromise() {
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
		if boundTy.IsArray() {
			if err := e.genUnpackArrayElemPattern(boundVal.Ref, boundTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if boundTy.IsTuple() {
			if err := e.unpackTuplePatternInto(boundVal.Ref, boundTy, s.ArrayPattern, pos); err != nil {
				return err
			}
		} else if err := e.genUnpackIterableElem(boundVal, s.ArrayPattern, pos); err != nil {
			return err
		}
	default:
		if awaitedTy.IR != "void" && awaitedTy.IR != "" {
			e.genStoreLoopVar(varPtr, boundTy, boundVal.Ref)
		}
	}
	if err := e.emitForOfBody(s); err != nil {
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
	switch {
	case iterVal.Ty.IsGenerator() && iterVal.Ty.GeneratorIsAsync:
		// `async *[Symbol.asyncIterator]() { yield … }`: the method's own
		// async generator is the iterator.
		return e.emitForAwaitOfGenerator(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
	case iterVal.Ty.IsGenerator():
		return e.emitForAwaitOfSyncGenerator(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
	case iterVal.Ty.IsDynamic():
		// An iterator typed `any` (a library's `on()` iterator): driven
		// through the dynamic protocol, as an async iterable itself.
		return e.emitForOfAnyValue(s, iterVal, condL, bodyL, incL, endL)
	case !iterVal.Ty.IsClass():
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
	if !nextSig.RetType.IsPromise() || nextSig.RetType.PromiseType == nil {
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
	loaded := e.genLoadElemAt(vGep, elemTy)
	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(loaded, elemTy, s.ObjectPattern, pos); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if elemTy.IsArray() {
			if err := e.genUnpackArrayElemPattern(loaded, elemTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if elemTy.IsTuple() {
			if err := e.unpackTuplePatternInto(loaded, elemTy, s.ArrayPattern, pos); err != nil {
				return err
			}
		} else if err := e.genUnpackIterableElem(Value{Ref: loaded, Ty: elemTy}, s.ArrayPattern, pos); err != nil {
			return err
		}
	default:
		e.genStoreLoopVar(varPtr, elemTy, loaded)
	}
	if err := e.emitForOfBody(s); err != nil {
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
	if !iterVal.Ty.IsClass() {
		return e.emitForOfIteratorObject(s, iterVal, isAwait, condL, bodyL, incL, endL)
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
	if resultTy.IsPromise() {
		return fmt.Errorf("%d:%d: '%s'.next() returns a Promise — a [Symbol.iterator]() iterator must be synchronous (use [Symbol.asyncIterator] with for await)", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	_, elemTy, ok := resultTy.FieldIndex("value")
	if !ok && (resultTy.IsDynamic() || !resultTy.IsObject()) {
		// next() returns a result whose shape is known only at run time (an
		// `any`, a null-prototype object): the iteration protocol read
		// dynamically, the loop variable an `any`.
		return e.emitForOfAnyValue(s, iterableVal, condL, bodyL, incL, endL)
	}
	if !ok {
		return fmt.Errorf("%d:%d: iterator '%s'.next() result has no 'value' field", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}
	if _, _, ok := resultTy.FieldIndex("done"); !ok {
		return fmt.Errorf("%d:%d: iterator '%s'.next() result has no 'done' field", pos.Line, pos.Col, inspectClassName(iterTy.ClassName))
	}

	// The bound value's type: with `for await`, a Promise value awaits to its T.
	boundDeclTy := elemTy
	if isAwait && elemTy.IsPromise() {
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
	loaded := e.genLoadElemAt(vGep, elemTy)

	boundVal := Value{Ref: loaded, Ty: elemTy}
	boundTy := elemTy
	if isAwait && elemTy.IsPromise() {
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
		if boundTy.IsArray() {
			if err := e.genUnpackArrayElemPattern(boundVal.Ref, boundTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if boundTy.IsTuple() {
			if err := e.unpackTuplePatternInto(boundVal.Ref, boundTy, s.ArrayPattern, pos); err != nil {
				return err
			}
		} else if err := e.genUnpackIterableElem(boundVal, s.ArrayPattern, pos); err != nil {
			return err
		}
	default:
		if boundDeclTy.IR != "void" && boundDeclTy.IR != "" {
			e.genStoreLoopVar(varPtr, boundTy, boundVal.Ref)
		}
	}
	if err := e.emitForOfBody(s); err != nil {
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
	if fTy.IsDynamic() {
		// A member held as `any` (a generator method, `*[Symbol.iterator]()`)
		// runs the iteration protocol at run time, on the boxed object.
		objVal, err := e.emitExpr(s.Iterable)
		if err != nil {
			return err
		}
		return e.emitForOfAnyValue(s, objVal, condL, bodyL, incL, endL)
	}
	if !fTy.IsFunc() {
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
	var recv []ast.Expression
	if fTy.FuncThis {
		// A method reading its own `this` (`[Symbol.iterator]() { return
		// this; }`) is called on the object, held under a hidden name so
		// the iterable is evaluated once.
		name := "__kml_iterobj" + strings.TrimPrefix(e.freshReg(), "%")
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, objVal.Ty.IR, objVal.Ty.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", objVal.Ty.IR, objVal.Ref, slot, objVal.Ty.Align()))
		e.define(name, Symbol{Ptr: slot, Ty: objVal.Ty})
		recv = []ast.Expression{ast.NewIdentifier(name, pos)}
	}
	iterVal, err := e.emitClosureCallByPtr(hdr, fTy, recv, pos)
	if err != nil {
		return err
	}
	switch {
	case iterVal.Ty.IsGenerator() && iterVal.Ty.GeneratorIsAsync:
		if !isAwait {
			return fmt.Errorf("%d:%d: a [Symbol.iterator]() returning an async generator requires 'for await...of'", pos.Line, pos.Col)
		}
		return e.emitForAwaitOfGenerator(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
	case iterVal.Ty.IsGenerator():
		if isAwait {
			return e.emitForAwaitOfSyncGenerator(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
		}
		return e.emitForOfGenerator(s, iterVal.Ty, iterVal, condL, bodyL, incL, endL)
	case iterVal.Ty.IsClass():
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
	if key == syncIteratorMethodName {
		return e.emitForOfIteratorObject(s, iterVal, isAwait, condL, bodyL, incL, endL)
	}
	return fmt.Errorf("%d:%d: a [Symbol.asyncIterator] member must return an async generator, a generator or a class instance", pos.Line, pos.Col)
}

// emitForOfIteratorObject iterates iterVal, the iterator a
// `[Symbol.iterator]()` returned, by the protocol whatever its
// representation (an object literal's `next`, a value held as any): each
// turn evaluates `it.next()`, then the result's `done` and `value`, as the
// ordinary expressions they are, over hidden locals holding the iterator and
// the result. With isAwait each value is awaited before it is bound.
func (e *Emitter) emitForOfIteratorObject(s *ast.ForOfStatement, iterVal Value, isAwait bool, condL, bodyL, incL, endL string) error {
	pos := s.GetPos()
	hidden := func(v Value) *ast.Identifier {
		name := "__kml_iter" + strings.TrimPrefix(e.freshReg(), "%")
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, v.Ty.IR, v.Ty.Align()))
		e.define(name, Symbol{Ptr: slot, Ty: v.Ty})
		return ast.NewIdentifier(name, pos)
	}
	store := func(id *ast.Identifier, v Value) {
		sym, _ := e.lookup(id.Name)
		v = e.convert(v, sym.Ty)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", sym.Ty.IR, v.Ref, sym.Ptr, sym.Ty.Align()))
	}
	it := hidden(iterVal)
	store(it, iterVal)
	next := ast.NewCallExpression(ast.NewMemberExpression(it, "next", pos), nil, pos)
	resTy := e.inferExprType(next)
	if resTy.IR == "" || resTy.IR == "void" {
		return fmt.Errorf("%d:%d: the iterator [Symbol.iterator]() returned has no next() method", pos.Line, pos.Col)
	}
	res := hidden(Value{Ty: resTy})
	valueExpr := ast.NewMemberExpression(res, "value", pos)
	elemTy := e.inferExprType(valueExpr)
	boundDeclTy := elemTy
	if _, yt, isGen := iterVal.Ty.FieldIndex(GeneratorYieldedField); isGen && iterVal.Ty.IsGenerator() && isNullableScalar(elemTy) && !isNullableScalar(yt) {
		// A generator's value is read only while done is false: what it
		// yields, as tsc types the binding, not the result's `T | undefined`.
		boundDeclTy = elemTy.withoutNullable()
	}
	if isAwait && elemTy.IsPromise() {
		boundDeclTy = TypeVoid
		if elemTy.PromiseType != nil {
			boundDeclTy = *elemTy.PromiseType
		}
	}
	isPattern := s.ArrayPattern != nil || s.ObjectPattern != nil
	varPtr := ""
	if !isPattern {
		varPtr = e.genDefineLoopVar(s.VarName, boundDeclTy)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	r, err := e.emitExpr(next)
	if err != nil {
		return err
	}
	store(res, r)
	done, err := e.emitExpr(ast.NewMemberExpression(res, "done", pos))
	if err != nil {
		return err
	}
	d := e.toBool(done)
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", d.Ref, endL, bodyL))

	e.emitLabel(bodyL)
	v, err := e.emitExpr(valueExpr)
	if err != nil {
		return err
	}
	if isAwait && v.Ty.IsPromise() {
		if v, err = e.emitAwaitPromiseElem(v.Ref, v.Ty); err != nil {
			return err
		}
	}
	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(v.Ref, v.Ty, s.ObjectPattern, pos); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		switch {
		case v.Ty.IsArray():
			err = e.genUnpackArrayElemPattern(v.Ref, v.Ty, s.ArrayPattern)
		case v.Ty.IsTuple():
			err = e.unpackTuplePatternInto(v.Ref, v.Ty, s.ArrayPattern, pos)
		default:
			err = e.genUnpackIterableElem(v, s.ArrayPattern, pos)
		}
		if err != nil {
			return err
		}
	default:
		if boundDeclTy.IR != "void" && boundDeclTy.IR != "" {
			e.genStoreLoopVar(varPtr, boundDeclTy, e.convert(v, boundDeclTy).Ref)
		}
	}
	if err := e.emitForOfBody(s); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))
	e.emitLabel(incL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	return nil
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
	isPromiseElem := elemTy.IsPromise()
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
	} else if awaitedTy.IsArray() {
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
	} else if elemTy.IsDynamic() {
		// A boxed element may hold a promise (`[p, 2]`): its value, else
		// the value itself, after the await's tick.
		aw, err := e.emitAwaitAny(elemVal)
		if err != nil {
			return err
		}
		boundVal = e.convert(aw, elemTy)
	} else if _, err := e.emitAwaitAny(Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}); err != nil {
		return err // a plain value takes the await's tick
	}
	// An array's async-from-sync iterator settles each step's promise from
	// a reaction on the value's, one tick before the loop's await resumes.
	if _, err := e.emitAwaitAny(Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}); err != nil {
		return err
	}

	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(boundVal.Ref, boundTy, s.ObjectPattern, pos); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if boundTy.IsArray() {
			if err := e.genUnpackArrayElemPattern(boundVal.Ref, boundTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if boundTy.IsTuple() {
			if err := e.unpackTuplePatternInto(boundVal.Ref, boundTy, s.ArrayPattern, pos); err != nil {
				return err
			}
		} else if err := e.genUnpackIterableElem(boundVal, s.ArrayPattern, pos); err != nil {
			return err
		}
	default:
		if awaitedTy.IsArray() {
			// Point the loop variable's slot at this element's header.
			elemHeader := e.boxArrayValue(boundVal)
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", elemHeader, varPtr))
		} else if awaitedTy.IR != "void" && awaitedTy.IR != "" {
			e.genStoreLoopVar(varPtr, boundTy, boundVal.Ref)
		}
	}

	if err := e.emitForOfBody(s); err != nil {
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
	loaded := e.genLoadElemAt(vGep, elemTy)
	switch {
	case s.ObjectPattern != nil:
		if err := e.unpackObjectPatternInto(loaded, elemTy, s.ObjectPattern, s.GetPos()); err != nil {
			return err
		}
	case s.ArrayPattern != nil:
		if elemTy.IsArray() {
			if err := e.genUnpackArrayElemPattern(loaded, elemTy, s.ArrayPattern); err != nil {
				return err
			}
		} else if elemTy.IsTuple() {
			if err := e.unpackTuplePatternInto(loaded, elemTy, s.ArrayPattern, s.GetPos()); err != nil {
				return err
			}
		} else if err := e.genUnpackIterableElem(Value{Ref: loaded, Ty: elemTy}, s.ArrayPattern, s.GetPos()); err != nil {
			return err
		}
	default:
		e.genStoreLoopVar(varPtr, elemTy, loaded)
	}
	if err := e.emitForOfBody(s); err != nil {
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

// collectYields returns every yield expression of a generator body, in source
// order, through the generated AST traversal — a yield in any expression or
// statement position is found — without entering a nested function or class,
// whose yields are its own.
func collectYields(body *ast.BlockStatement) []*ast.YieldExpression {
	var out []*ast.YieldExpression
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.YieldExpression:
			out = append(out, n)
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction,
			*ast.ClassDeclaration, *ast.ClassExpression:
			return false
		}
		return true
	})
	return out
}

// inferGeneratorElemType infers an un-annotated generator's element type
// from its yields (TDD-00096 Part 2): each plain `yield <expr>`'s inferred
// type joins under the usual numeric rule (any float makes the join f64);
// `yield*` over a call to a known generator contributes that generator's
// element type. Zero contributing yields fall back to the first `return
// <expr>`'s type, then i64. Parameters are temporarily bound so a
// `yield param` infers from the declared parameter type. Reports !ok only
// for a genuinely non-joinable mix.
func (e *Emitter) inferGeneratorElemType(fd *ast.FunctionDeclaration, paramNames []string, paramTypes []Type, thisTy *Type) (Type, bool) {
	e.pushScope()
	// Its own name, read inside it before it is registered (a nested
	// generator's signature is built first): a function value, boxed.
	if fd.Name != "" {
		if _, found := e.lookupGenerator(fd.Name); !found {
			if _, found := e.lookup(fd.Name); !found {
				e.define(fd.Name, Symbol{Ty: TypeAny})
			}
		}
	}
	if thisTy != nil {
		e.define("this", Symbol{Ty: *thisTy})
	}
	for i, n := range paramNames {
		e.define(n, Symbol{Ty: paramTypes[i]})
	}
	// A destructured parameter's names (desugarGeneratorPatternParams).
	for _, st := range e.genPatternInits[fd] {
		names := map[string]bool{}
		switch d := st.(type) {
		case *ast.ArrayDestructuring:
			collectArrayPatternNames(d.Elems, names)
		case *ast.ObjectDestructuring:
			collectObjectPatternNames(d.Props, names)
		}
		for _, n := range sortedSet(names) {
			e.define(n, Symbol{Ty: TypeAny})
		}
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
				if st.VarName != "" && st.ArrayPattern == nil && st.ObjectPattern == nil {
					e.define(st.VarName, Symbol{Ty: forOfBindingType(e.inferExprType(st.Iterable), st.Await)})
				}
				if st.ArrayPattern != nil {
					elemTy := forOfBindingType(e.inferExprType(st.Iterable), st.Await)
					for i, el := range st.ArrayPattern {
						if el.Name != "" && el.SubArray == nil {
							e.define(el.Name, Symbol{Ty: patternElemType(elemTy, i, el.Rest)})
						}
					}
				}
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

	yields := collectYields(fd.Body)
	var joined Type
	have, sawNull := false, false
	join := func(t Type) bool {
		if t.IR == "void" || t.IR == "" {
			return true
		}
		if t.IsNull() && !t.Nullable() {
			sawNull = true // `yield null`: the join is `T | null`
			return true
		}
		if !have {
			joined = t
			have = true
			return true
		}
		if joined.IsDynamic() != t.IsDynamic() {
			return false // an any beside a typed value: the element is any
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
			if !y.Delegate {
				// A bare `yield` yields undefined, which only the
				// element any carries.
				return Type{}, false
			}
			continue
		}
		if y.Delegate {
			if call, ok := y.Argument.(*ast.CallExpression); ok {
				if id, ok := call.Callee.(*ast.Identifier); ok {
					if info, found := e.lookupGenerator(id.Name); found {
						if !join(info.ElemTy) {
							return Type{}, false
						}
						continue
					}
				}
			}
			// An operand the dynamic delegation drives (emitYieldStarAny):
			// its values are not known statically.
			if t := e.inferExprType(y.Argument); e.yieldStarIsDynamic(t, fd.IsAsync) {
				return Type{}, false
			}
			continue
		}
		if !join(awaitedIn(fd.IsAsync, e.inferExprType(y.Argument))) {
			return Type{}, false
		}
	}
	// The returned values are read through the same value slot: a return
	// of another representation makes the element any.
	if fd.Body != nil {
		for _, r := range returnExprs(fd.Body, nil) {
			if !join(awaitedIn(fd.IsAsync, e.inferExprType(r))) {
				return Type{}, false
			}
		}
	}
	if !have {
		joined = TypeI64 // a generator that only completes
	}
	if sawNull {
		if !have {
			return TypeNull, true
		}
		joined.setNullable(true)
	}
	return joined, true
}

// forOfBindingType is the type a `for (const x of it)` binding has for an
// iterable of type it (under `for await` a promise element is its value).
// An iterable whose element type is not known statically binds `any`.
func forOfBindingType(it Type, await bool) Type {
	var el Type
	switch {
	case it.IsArray() && it.ElemType != nil:
		el = *it.ElemType
	case it.IsGenerator() && it.GeneratorElemType != nil:
		el = *it.GeneratorElemType
	case it.IsSet() && it.MapKey != nil:
		el = *it.MapKey
	case isStringTy(it):
		return TypePtr
	default:
		return TypeAny
	}
	if await && el.IsPromise() {
		if el.PromiseType != nil {
			return *el.PromiseType
		}
		return TypeAny
	}
	return el
}

// genBoxSent is v as the boxed word the sent slot holds.
func (e *Emitter) genBoxSent(v Value) string {
	if v.Ty.IsDynamic() && v.Ref != "" {
		return v.Ref
	}
	b, err := e.emitBoxValue(v)
	if err != nil {
		return fmt.Sprint(nbUndefined)
	}
	return b.Ref
}

// genSentAs is the sent slot's value as ty — a `.return(v)` value becoming
// the generator's result.
func (e *Emitter) genSentAs(genObj string, genTy Type, ty Type) Value {
	w := e.loadGeneratorField(genObj, genTy, GeneratorSentField)
	if ty.IsDynamic() {
		return Value{Ref: w.Ref, Ty: ty}
	}
	return e.emitUnboxBoxToType(w.Ref, ty)
}

// genUserResultType is the result a program's own gen.next() / .throw() /
// .return() call reads: {value, done}, value `T | undefined` since it is
// undefined once the generator is done without a return value.
func genUserResultType(elem Type) Type {
	v := elem
	if !v.IsDynamic() && !v.Nullable() {
		v = undefinedableElem(v)
	}
	return genNextResultType(v)
}

// emitUserGenResult converts a sync generator step's internal {value, done}
// into the program's result: value is the step's while not done, the return
// value on the step that completed with one, and undefined otherwise.
func (e *Emitter) emitUserGenResult(genObj string, genTy Type, res Value) Value {
	elemTy := *genTy.GeneratorElemType
	userTy := genUserResultType(elemTy)
	_, vTy, _ := userTy.FieldIndex("value")
	vIdx, _, _ := res.Ty.FieldIndex("value")
	dIdx, _, _ := res.Ty.FieldIndex("done")
	vGep, dGep, done := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, res.Ty.StructIR(), res.Ref, vIdx))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, res.Ty.StructIR(), res.Ref, dIdx))
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", done, dGep))
	hasRet := e.loadGeneratorField(genObj, genTy, GeneratorHasRetField)
	// The return value is handed out once: a later next() reads undefined.
	e.storeGeneratorField(genObj, genTy, GeneratorHasRetField, "i1", "0")
	present := e.freshReg()
	notDone := e.boolNot(done)
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", present, notDone, hasRet.Ref))

	out := e.freshReg()
	e.emitObjMallocInto(out, userTy)
	uvIdx, _, _ := userTy.FieldIndex("value")
	udIdx, _, _ := userTy.FieldIndex("done")
	uvGep, udGep := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", uvGep, userTy.StructIR(), out, uvIdx))
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", udGep, userTy.StructIR(), out, udIdx))
	e.heapStore("i1", done, udGep, 1)
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", raw, StructFieldIR(elemTy), vGep))
	switch {
	case isNullableScalar(vTy) && !isNullableScalar(elemTy):
		e.heapStore(StructFieldIR(vTy), e.makeNullableScalarAgg(vTy, present, raw), uvGep, 8)
	case StructFieldIR(elemTy) == "ptr":
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", sel, present, raw))
		e.heapStore("ptr", sel, uvGep, 8)
	default:
		e.heapStore(StructFieldIR(elemTy), raw, uvGep, 8)
	}
	return Value{Ref: out, Ty: userTy}
}

// emitGeneratorUserCall is a program's own gen.next() / .throw() / .return()
// (TDD-00061, TDD-00086): the step, its result read as the program sees it
// (emitUserGenResult) for a sync generator.
func (e *Emitter) emitGeneratorUserCall(mem *ast.MemberExpression, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
	m := generatorMethods[mem.Property]
	if genTy.GeneratorIsAsync {
		return m.async(e, mem.Object, genTy, args, pos)
	}
	genVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	// The step runs on the evaluated instance, bound to a name so the
	// by-receiver forms evaluate it once.
	name := fmt.Sprintf("__kml_genrecv_%d", e.optRecvCtr)
	e.optRecvCtr++
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", genVal.Ref, slot))
	e.define(name, Symbol{Ptr: slot, Ty: genTy})
	recv := ast.NewIdentifier(name, mem.GetPos())
	res, err := m.sync(e, genVal.Ref, recv, genTy, args, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitUserGenResult(genVal.Ref, genTy, res), nil
}

// generatorMethod is one of a generator's own methods: its async form on
// the receiver expression, and its sync step on the evaluated instance
// (gen) and the name bound to it (recv).
type generatorMethod struct {
	async func(e *Emitter, obj ast.Expression, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error)
	sync  func(e *Emitter, gen string, recv ast.Expression, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error)
}

// generatorMethods are a generator's next, throw and return (TDD-00061).
var generatorMethods map[string]generatorMethod

func init() {
	generatorMethods = map[string]generatorMethod{
		"next": {
			async: (*Emitter).emitGeneratorNext,
			sync: func(e *Emitter, gen string, _ ast.Expression, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
				return e.emitGeneratorNextByValue(gen, genTy, args, pos)
			},
		},
		"throw": {
			async: (*Emitter).emitGeneratorThrow,
			sync: func(e *Emitter, _ string, recv ast.Expression, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
				return e.emitGeneratorThrow(recv, genTy, args, pos)
			},
		},
		"return": {
			async: (*Emitter).emitGeneratorReturnMethod,
			sync: func(e *Emitter, gen string, recv ast.Expression, genTy Type, args []ast.Expression, pos ast.Pos) (Value, error) {
				if len(args) > 0 && !isUndefinedLiteral(e, args[0]) {
					// `.return(v)` completes with v; `.return(undefined)` is
					// `.return()`.
					e.storeGeneratorField(gen, genTy, GeneratorHasRetField, "i1", "1")
				}
				return e.emitGeneratorReturnMethod(recv, genTy, args, pos)
			},
		},
	}
}

// genStackOwned reports whether a generator's stack field holds the owner
// cell of a guarded stack (-mm=gc on POSIX, gcshim.c) rather than the stack.
func (e *Emitter) genStackOwned() bool {
	return e.isGCMode() && e.opts.Target.OS() != "windows"
}

// genSwapOut suspends the running generator body back to its caller. With
// an owned stack the owner records where the parked frames end, so the
// collector scans only them, until the body runs again.
func (e *Emitter) genSwapOut(genObj string, genTy Type) {
	callerCtx := e.loadGeneratorField(genObj, genTy, GeneratorCallerCtxField)
	ownCtx := e.loadGeneratorField(genObj, genTy, GeneratorCtxField)
	if !e.genStackOwned() {
		e.fiberSwitch(ownCtx.Ref, callerCtx.Ref)
		return
	}
	owner := e.loadGeneratorField(genObj, genTy, GeneratorStackField)
	e.declareFn("__kml_gen_stack_park", "declare void @__kml_gen_stack_park(ptr)")
	e.declareFn("__kml_gen_stack_unpark", "declare void @__kml_gen_stack_unpark(ptr)")
	e.emitInstr(fmt.Sprintf("call void @__kml_gen_stack_park(ptr %s)", owner.Ref))
	e.fiberSwitch(ownCtx.Ref, callerCtx.Ref)
	e.emitInstr(fmt.Sprintf("call void @__kml_gen_stack_unpark(ptr %s)", owner.Ref))
}

// emitGenReleaseIfDone releases a sync generator's stack and jmpbuf stack
// once its body has completed: it never runs again, and a program making
// many short generators must not keep a stack per instance.
func (e *Emitter) emitGenReleaseIfDone(genObj string, genTy Type) {
	if e.opts.Target.OS() == "windows" {
		return
	}
	done := e.loadGeneratorField(genObj, genTy, GeneratorDoneField)
	stack := e.loadGeneratorField(genObj, genTy, GeneratorStackField)
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, stack.Ref))
	both := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, done.Ref, has))
	relL, contL := e.freshLabel("gen.release"), e.freshLabel("gen.kept")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", both, relL, contL))
	e.emitLabel(relL)
	if e.genStackOwned() {
		e.declareFn("__kml_gen_stack_release", "declare void @__kml_gen_stack_release(ptr)")
		e.emitInstr(fmt.Sprintf("call void @__kml_gen_stack_release(ptr %s)", stack.Ref))
	} else if !e.isGCMode() {
		e.declareFn("__kml_fiber_stack_free", "declare void @__kml_fiber_stack_free(ptr, i64)")
		e.emitInstr(fmt.Sprintf("call void @__kml_fiber_stack_free(ptr %s, i64 %d)", stack.Ref, fiberStackBytes))
		jmp := e.loadGeneratorField(genObj, genTy, GeneratorJmpStkField)
		e.emitInstr(fmt.Sprintf("call void @__kml_jmp_stack_free(ptr %s)", jmp.Ref))
	}
	e.storeGeneratorField(genObj, genTy, GeneratorStackField, "ptr", "null")
	e.storeGeneratorField(genObj, genTy, GeneratorJmpStkField, "ptr", "null")
	e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
	e.emitLabel(contL)
}

// genUnpackIterableElem array-destructures a loop element that is not an
// array or a tuple (a Set, a value held in `any`, an object with
// [Symbol.iterator]) through its elements as an array (`[...elem]`), as
// `let [a] = iterable` does.
func (e *Emitter) genUnpackIterableElem(v Value, pat []ast.ArrayPatternElem, pos ast.Pos) error {
	if v.Ty.IR == "" || v.Ty.IR == "void" {
		return fmt.Errorf("%d:%d: cannot array-destructure a loop element of no value", pos.Line, pos.Col)
	}
	if v.Ty.IsGenerator() || v.Ty.IsDynamic() {
		// An iterator is stepped only as far as the pattern reads (`[]`
		// reads nothing), and closed when left unfinished.
		return e.unpackDynArrayPatternInto(v, pat, pos)
	}
	arr, err := e.emitExpr(ast.NewArrayLiteral([]ast.Expression{&ast.SpreadElement{Arg: e.bindValue(v, pos)}}, pos))
	if err != nil {
		return err
	}
	if !arr.Ty.IsArray() {
		return fmt.Errorf("%d:%d: cannot array-destructure a loop element that is not iterable", pos.Line, pos.Col)
	}
	return e.genUnpackArrayElemPattern(arr.Ref, arr.Ty, pat)
}

// patternElemType is the type an array pattern's i-th name binds over a
// value of type t: an array's element (an array for a rest), a tuple's
// field, a Set's element, a Map entry's key or value; any otherwise.
func patternElemType(t Type, i int, rest bool) Type {
	switch {
	case t.IsDynamic():
		return TypeAny
	case t.IsTuple() && !t.IsArray():
		if rest {
			return ArrayOf(tupleCommonElem(t, i))
		}
		if i < len(t.Fields) {
			return t.Fields[i].Ty
		}
		return TypeAny
	case t.IsArray() && t.ElemType != nil:
		if rest {
			return t
		}
		return *t.ElemType
	case t.IsSet() && t.MapKey != nil && !rest:
		return *t.MapKey
	}
	return TypeAny
}

// emitThrownCell copies the value just caught (its tag and payload) into a
// fresh 16-byte cell: a generator's catch-all keeps it there until the
// caller side re-throws or rejects with it (loadThrownCell).
func (e *Emitter) emitThrownCell() string {
	e.ensureMalloc()
	tag, pay := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_get_thrown_tag()", tag))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_get_thrown_pay()", pay))
	return e.thrownCellFrom(tag, pay)
}

// thrownCellFrom stores a thrown value's tag and payload into a fresh cell.
func (e *Emitter) thrownCellFrom(tag, pay string) string {
	e.ensureMalloc()
	cell := e.freshReg()
	e.heapMallocInto(cell, "16")
	e.heapStore("i8", tag, cell, 8)
	pp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", pp, cell))
	e.heapStore("i64", pay, pp, 8)
	return cell
}

// loadThrownCell reads an emitThrownCell cell's tag and payload.
func (e *Emitter) loadThrownCell(cell string) (tag, pay string) {
	tag, pay = e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 8", tag, cell))
	pp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", pp, cell))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", pay, pp))
	return tag, pay
}

// thrownCellOf is a thrown cell holding v as `throw v` throws it (an Error
// keeps its Error tag, any other value its own), for .throw(v).
func (e *Emitter) thrownCellOf(v Value) (string, error) {
	var tag, pay string
	isErrSubclass := false
	if v.Ty.ClassName != "" {
		if info, ok := e.classes[v.Ty.ClassName]; ok && info.IsErrorSubclass {
			isErrSubclass = true
		}
	}
	switch {
	case v.Ty.IsCaught():
		tag, pay = e.caughtParts(v)
	case v.Ty.IsError() || isErrSubclass:
		p := e.convert(v, TypePtr)
		tag, pay = e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i8 0, %d", tag, kmlTagError))
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", pay, p.Ref))
	default:
		b, err := e.emitBoxValue(v)
		if err != nil {
			return "", err
		}
		tag, pay = e.emitUnboxTagPayload(b)
	}
	return e.thrownCellFrom(tag, pay), nil
}

// thrownCellAny is a thrown cell's value as `any`.
func (e *Emitter) thrownCellAny(cell string) Value {
	tag, pay := e.loadThrownCell(cell)
	return e.emitCaughtToAny(e.emitCaughtAggregate(tag, pay))
}

// asyncGenResultType is the {value, done} object an async generator's
// next/throw/return promise settles with: value may be undefined (a
// generator that completes without a return value), as the program reads it.
func asyncGenResultType(elem Type) Type {
	return genUserResultType(elem)
}

// buildAsyncGenResult builds an asyncGenResultType object holding yielded
// (of type elemTy) when present is true, undefined otherwise.
func (e *Emitter) buildAsyncGenResult(resultTy, elemTy Type, yielded, done Value, present string) Value {
	resultReg := e.freshReg()
	e.emitObjMallocInto(resultReg, resultTy)
	vIdx, vTy, _ := resultTy.FieldIndex("value")
	vGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", vGep, resultTy.StructIR(), resultReg, vIdx))
	switch {
	case elemTy.IsArray():
		e.storeArrayFieldHeader(vGep, yielded) // {value} slot holds a header pointer (TDD-00213 S2)
	case isNullableScalar(vTy) && !isNullableScalar(elemTy):
		e.heapStore(StructFieldIR(vTy), e.makeNullableScalarAgg(vTy, present, yielded.Ref), vGep, 8)
	case StructFieldIR(elemTy) == "ptr" && present != "true":
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", sel, present, yielded.Ref))
		e.heapStore("ptr", sel, vGep, 8)
	default:
		e.genStoreElemAt(vGep, elemTy, yielded.Ref)
	}
	dIdx, _, _ := resultTy.FieldIndex("done")
	dGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, resultTy.StructIR(), resultReg, dIdx))
	e.heapStore("i1", done.Ref, dGep, 1)
	return Value{Ref: resultReg, Ty: resultTy}
}

// loadGenResultValue reads a result object's value field (at gep, of the
// layout resultTy) as elemTy: an undefined-capable scalar slot gives its
// payload (read only while the result is not done).
func (e *Emitter) loadGenResultValue(gep string, resultTy, elemTy Type) string {
	_, vTy, _ := resultTy.FieldIndex("value")
	if isNullableScalar(vTy) && !isNullableScalar(elemTy) {
		return e.loadNullableScalarPayload(gep, vTy)
	}
	return e.genLoadElemAt(gep, elemTy)
}

// desugarGeneratorPatternParams turns each destructured parameter of a
// generator into a hidden plain one (keeping its type and default) and
// records the destructuring the body's start runs over it, before the
// generator first suspends — so it runs at the call. Idempotent: the
// recorded statements are kept per declaration.
func (e *Emitter) desugarGeneratorPatternParams(fd *ast.FunctionDeclaration) {
	if c := e.front(); c != nil && hasPatternParam(fd) {
		c.WarmDecl(fd) // the checker types the parameters as written
	}
	for i := range fd.Params {
		p := &fd.Params[i]
		if p.ArrayPattern == nil && p.ObjectPattern == nil {
			continue
		}
		hidden := fmt.Sprintf("__kml_gdp%d", i)
		arg := ast.NewIdentifier(hidden, p.Pos)
		var st ast.Statement
		if p.ArrayPattern != nil {
			st = ast.NewArrayDestructuring("let", p.ArrayPattern, arg, p.Pos)
		} else {
			st = ast.NewObjectDestructuring("let", p.ObjectPattern, arg, p.Pos)
		}
		if e.genPatternInits == nil {
			e.genPatternInits = map[*ast.FunctionDeclaration][]ast.Statement{}
		}
		e.genPatternInits[fd] = append(e.genPatternInits[fd], st)
		p.Name, p.ArrayPattern, p.ObjectPattern = hidden, nil, nil
		if p.Type == nil && p.Rest {
			// A rest pattern (`...[a, b]`) destructures the collected array.
			p.Type = &ast.TypeAnnotation{Name: "any[]", Source: "ts"}
		} else if p.Type == nil {
			p.Type = &ast.TypeAnnotation{Name: "any", Source: "ts"}
		}
	}
}

// genDesugaredBody reports whether the generator with body body had its
// destructured parameters rewritten (desugarGeneratorPatternParams).
func (e *Emitter) genDesugaredBody(body *ast.BlockStatement) bool {
	for fd := range e.genPatternInits {
		if fd.Body == body {
			return true
		}
	}
	return false
}

// hasPatternParam reports whether fd has a destructured parameter.
func hasPatternParam(fd *ast.FunctionDeclaration) bool {
	for _, p := range fd.Params {
		if p.ArrayPattern != nil || p.ObjectPattern != nil {
			return true
		}
	}
	return false
}

// readsOwnValue reports whether fd's body reads fd's name other than as a
// call's callee (`return g`, `yield g`).
func readsOwnValue(fd *ast.FunctionDeclaration) bool {
	if fd.Body == nil || fd.Name == "" {
		return false
	}
	refs, calls := 0, 0
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Identifier:
			if x.Name == fd.Name {
				refs++
			}
		case *ast.CallExpression:
			if id, ok := x.Callee.(*ast.Identifier); ok && id.Name == fd.Name {
				calls++
			}
		}
		return true
	})
	return refs > calls
}

// emitSelfCapturingNestedGenerator emits a capturing nested generator whose
// body reads its own value. Its function object is made once per run of
// the enclosing function, into a cell under its name that its body
// captures, so every reference is the same value (`g().next().value === g`).
func (e *Emitter) emitSelfCapturingNestedGenerator(fd *ast.FunctionDeclaration, info *GeneratorInfo, caps []CapturedVar) error {
	shown := fd.Name
	if n, ok := e.genShownNames[fd.Name]; ok {
		shown = n
	}
	_, err := e.emitSelfReferencingGenerator(fd, info, caps, shown)
	return err
}

// emitSelfReferencingGenerator emits generator fd, whose body reads its own
// name as a value, and returns its function object: made once, into a cell
// under fd's name that the body captures.
func (e *Emitter) emitSelfReferencingGenerator(fd *ast.FunctionDeclaration, info *GeneratorInfo, caps []CapturedVar, shown string) (Value, error) {
	cell := e.freshReg()
	e.heapMallocInto(cell, "8")
	e.heapStore("ptr", "null", cell, 8)
	sym := Symbol{Ptr: cell, Ty: generatorValueType(info), Boxed: true}
	caps = append(caps, CapturedVar{Name: fd.Name, Ty: sym.Ty, Sym: sym})
	sort.Slice(caps, func(i, j int) bool { return caps[i].Name < caps[j].Name })
	info.Captures = caps
	if err := e.emitGeneratorFunctionDecl(fd, info); err != nil {
		return Value{}, err
	}
	e.define(fd.Name, sym) // the env packs it by name
	v, err := e.emitGeneratorCtorClosure(info, shown, fd.GetPos())
	if err != nil {
		return Value{}, err
	}
	e.heapStore("ptr", v.Ref, cell, 8)
	return v, nil
}

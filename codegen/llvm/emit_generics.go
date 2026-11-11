// emit_generics.go — TDD-00010 V1: user-defined generics (monomorphization)
// for functions, interfaces, and classes, extended to N type parameters
// (`<K, V>`) by TDD-00037. On-demand (lazy) specialization during normal
// codegen emission, not a separate up-front collection pass — see the TDD's
// Design section for why this is a simpler, equally-correct realization of
// the same idea, made possible by whole-program compilation
// (resolver.ResolveProgram) and LLVM IR text's tolerance for out-of-order
// function definitions.
//
// Type parameters remain unconstrained throughout. Accepted concrete types
// for a *function* instantiation are number/string/boolean and arrays of
// these (see mangleTypeArg) — a generic interface's field types and a
// generic class's field/param/return types can substitute any of those the
// same way. Every substitution site below takes a `subs map[string]Type`
// (type-parameter name → concrete type) rather than a single pair, built
// once per instantiation/usage site and threaded through.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"hash/crc32"
	"hash/fnv"
	"strings"
)

// mangleTypeArg returns a compact, LLVM-identifier-safe suffix identifying a
// concrete generic instantiation argument, or an error naming why a type
// isn't accepted in V1 — object/class/Map/Set/Promise/closure type
// arguments are all deliberately out of scope (see this file's doc comment).
func mangleTypeArg(t Type) (string, error) {
	if t.IsTuple() && !t.IsDynamic() {
		// A tuple by its element types: two tuples sharing a storage
		// array (`[number, string]`, `[string, number]`) are different
		// instantiations.
		s, err := mangleObjectStructural(t)
		if err != nil {
			return "", err
		}
		return "tup" + s, nil
	}
	if t.IsArray() {
		inner, err := mangleTypeArg(*t.ElemType)
		if err != nil {
			return "", err
		}
		return inner + "arr", nil
	}
	if t.IR == "i1" {
		return "bool", nil
	}
	// A class type argument (TDD-00069): mangle by ClassName — the concrete
	// class identity the per-instantiation body needs for method dispatch, made
	// unique per file by the resolver ([TDD-00041](../../tdd/TDD-00041.md)).
	if t.IsClass() && !t.IsDynamic() {
		if t.ClassName == "" {
			return "", fmt.Errorf("an anonymous class type argument is not supported (give it a name)")
		}
		return "cls" + llvmSafeSymbol(t.ClassName), nil
	}
	// An object/interface type argument (TDD-00069): a resolved named interface
	// is a structural ObjectType carrying no name (Type has no Name field), so
	// mangle *structurally* — a deterministic encoding of the (ordered) field
	// names and types. This is stronger than the TDD's nominal-only V1 plan: it
	// also handles an anonymous inline object type and a bare object-literal
	// argument (`f({ x: 1 })`), since structurally-identical shapes correctly
	// share one monomorphization. Recurses through mangleTypeArg for field types.
	if t.IsObject() && !t.IsDynamicObject() && !t.IsMap() && !t.IsSet() && !t.IsDynamic() {
		return mangleObjectStructural(t)
	}
	// `any` (and `unknown`): the instantiation's T slots are NaN-boxed values,
	// as any `any`-typed code (`class C<T = any>` constructed without type
	// arguments takes this).
	if isUnconstrainedDynamic(t) {
		return "any", nil
	}
	if t.IsMap() || t.IsSet() || t.IsPromise() || t.IsFunc() || t.IsDynamicObject() || t.IsDynamic() {
		return hashedTypeArg(t), nil
	}
	if isNumberTy(t) {
		return "num", nil
	}
	if isStringTy(t) {
		return "str", nil
	}
	return hashedTypeArg(t), nil
}

// hashedTypeArg names any other type argument by a hash of its full shape.
func hashedTypeArg(t Type) string {
	h := fnv.New64a()
	h.Write([]byte(reprKey(t)))
	return fmt.Sprintf("t%x", h.Sum64())
}

// mangleObjectStructural produces a deterministic, LLVM-safe suffix for an
// object/interface type argument from its field list — field count, then each
// field's name and (recursively mangled) type, in declaration order. Two
// structurally-identical shapes (whatever their source names) mangle the same,
// which is exactly right for monomorphization: they produce identical code and
// should share one instantiation. A field whose own type isn't a supported
// argument type propagates that rejection.
func mangleObjectStructural(t Type) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "obj%d", len(t.Fields))
	for _, f := range t.Fields {
		fieldMangle, err := mangleTypeArg(f.Ty)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "_%s%s", llvmSafeSymbol(f.Name), fieldMangle)
	}
	return b.String(), nil
}

// mangleTypeArgs joins one mangleTypeArg suffix per entry of typeParams, in
// that declared order, so the mangled name is deterministic regardless of
// subs' own map iteration order (e.g. Box<number,string> → Box__num_str).
// Errors if subs is missing a concrete type for any name in typeParams.
func mangleTypeArgs(typeParams []string, subs map[string]Type) (string, error) {
	suffixes := make([]string, 0, len(typeParams))
	for _, tp := range typeParams {
		concrete, ok := subs[tp]
		if !ok {
			return "", fmt.Errorf("no concrete type supplied for type parameter '%s'", tp)
		}
		suffix, err := mangleTypeArg(concrete)
		if err != nil {
			return "", err
		}
		suffixes = append(suffixes, suffix)
	}
	return strings.Join(suffixes, "_"), nil
}

// buildTypeArgSubs zips typeParams (a declaration's own TypeParams, in
// order) positionally against typeArgs (a usage site's parsed type
// arguments), resolving each via annotationType. Arity mismatches are each
// caller's own responsibility to reject before calling this — the parser
// has no symbol table (see parseTypeParamList's own doc comment), so arity
// checking happens here, at the codegen call sites that already know both
// lengths. Only the shorter of the two lengths is zipped; a caller that
// hasn't already validated matching lengths gets a partially-built map, not
// a panic.
func (e *Emitter) buildTypeArgSubs(typeParams []string, typeArgs []ast.TypeNode) map[string]Type {
	subs := make(map[string]Type, len(typeParams))
	for i, tp := range typeParams {
		if i >= len(typeArgs) {
			break
		}
		subs[tp] = e.annotationType(typeArgs[i])
	}
	return subs
}

// substituteGenericType resolves a declaration-site type annotation that may
// reference one of subs' type-parameter names (a bare "T", or "T[]" — V1
// doesn't support nesting a type parameter any deeper than one array level)
// into its concrete Type, substituting concrete for every occurrence;
// anything else resolves normally via annotationType, exactly as it would for a
// non-generic declaration.
func (e *Emitter) substituteGenericType(ta ast.TypeNode, subs map[string]Type) Type {
	if ta == nil {
		return TypeVoid
	}
	// Anything deeper (`Promise<T>`, `Map<string, T>`, `T[][]`, a closure type
	// mentioning T) resolves structurally with the type parameters in scope.
	var t Type
	e.withTypeParamScope(subs, func() { t = e.annotationType(ta) })
	return t
}

// withTypeParamScope runs fn with subs layered over the active type-parameter
// scope (an inner generic instantiation shadows an outer one's names), so
// every annotationType inside fn — a body's `new Promise<T>`, a local's `T[]`
// annotation, the return-type inference — substitutes the concrete types.
// Restored on return, including on the error paths fn unwinds through.
func (e *Emitter) withTypeParamScope(subs map[string]Type, fn func()) {
	saved := e.typeParamScope
	merged := make(map[string]Type, len(saved)+len(subs))
	for k, v := range saved {
		merged[k] = v
	}
	for k, v := range subs {
		merged[k] = v
	}
	e.typeParamScope = merged
	defer func() { e.typeParamScope = saved }()
	fn()
}

// buildGenericParamSig is buildParamSig's generic-aware sibling: the same
// per-parameter rules (explicit annotation via annotationType, unannotated rest
// defaults to number[], unannotated scalar defaults to inferred TypeI64),
// except a parameter whose annotation names one of subs' type parameters
// substitutes its concrete type instead.
func (e *Emitter) buildGenericParamSig(params []ast.Param, subs map[string]Type) FuncSig {
	var sig FuncSig
	for _, p := range params {
		var pty Type
		if p.Type != nil {
			pty = e.substituteGenericType(p.Type, subs)
		} else if p.Rest {
			pty = ArrayOf(TypeI64)
		} else {
			pty = TypeI64
			pty.Inferred = true
		}
		sig.ParamTypes = append(sig.ParamTypes, optionalParamType(p, pty))
		sig.ParamNames = append(sig.ParamNames, p.Name)
		sig.Defaults = append(sig.Defaults, callSiteDefault(p)) // a destructured parameter's
		sig.HasDefault = append(sig.HasDefault, p.Default != nil)
		sig.Optional = append(sig.Optional, p.Optional || bodyDefaulted(p))
	}
	if len(params) > 0 && params[len(params)-1].Rest {
		sig.HasRest = true
	}
	return sig
}

// genericParamPos is the parameter position a call-site argument's type
// infers a given type parameter from — either bare ("T") or a one-level
// array of it ("T[]", IsArray=true).
type genericParamPos struct {
	Idx     int
	IsArray bool
	// Ctor marks a constructor-typed parameter (`new () => T`): T is the
	// instance type of the class the argument names.
	Ctor bool
	// FnRet marks a function-typed parameter returning T (`fn: () => T`):
	// T is what the callback argument returns.
	FnRet bool
	// Chunk marks a `ReadableStream<T>` parameter: T is the argument
	// stream's chunk type.
	Chunk bool
}

// streamOfParam reports whether n is `ReadableStream<typeParam>`.
func streamOfParam(n ast.TypeNode, typeParam string) bool {
	r, ok := ast.Unparen(n).(*ast.TypeReference)
	return ok && r.Name == "ReadableStream" && len(r.Qualifier) == 0 && len(r.TypeArgs) == 1 && ast.RefName(r.TypeArgs[0]) == typeParam
}

// genericFuncTypeParamIndex finds, independently for each of decl's type
// parameters, every parameter position whose declared type is that type
// parameter (bare or one-level array), in order — the positions a call
// site's argument types are inferred from. A type parameter absent from the
// result map has no inferable position (TDD-00010 V1, TDD-00037). No two type
// parameters can ever compete for the same position, since a parameter's type
// annotation names exactly one type-parameter name.
func genericFuncTypeParamIndex(decl *ast.FunctionDeclaration) map[string][]genericParamPos {
	positions := make(map[string][]genericParamPos, len(decl.TypeParams))
	for _, typeParam := range decl.TypeParams {
		for i, p := range decl.Params {
			if p.Type == nil {
				continue
			}
			n := ast.NonNullish(p.Type)
			ret, ctor, isSig := ast.SignatureReturn(n)
			switch {
			case ast.RefName(n) == typeParam:
				positions[typeParam] = append(positions[typeParam], genericParamPos{Idx: i})
			case ast.ArrayElemNode(n) != nil && ast.RefName(ast.ArrayElemNode(n)) == typeParam:
				positions[typeParam] = append(positions[typeParam], genericParamPos{Idx: i, IsArray: true})
			case isSig && ast.RefName(ret) == typeParam:
				positions[typeParam] = append(positions[typeParam], genericParamPos{Idx: i, Ctor: ctor, FnRet: !ctor})
			case streamOfParam(n, typeParam):
				positions[typeParam] = append(positions[typeParam], genericParamPos{Idx: i, Chunk: true})
			}
		}
	}
	return positions
}

// inferGenericCallConcreteTypes is the pure (no IR emission) core shared by
// emitGenericFuncCall and typeOfExpr's own generic-call case: finds, for
// every one of decl's type parameters independently, its own type-parameter-
// typed argument at the call site and infers its concrete type. Returns
// ok=false and the specific type-parameter name nothing could be inferred
// for the moment any one of them fails — safe to call speculatively/
// repeatedly, since typeOfExpr (unlike emitExpr) must never trigger real
// emission as a side effect of merely asking "what type is this call."
func (e *Emitter) inferGenericCallConcreteTypes(decl *ast.FunctionDeclaration, args []ast.Expression, call *ast.CallExpression) (subs map[string]Type, missing string, ok bool) {
	// The checker's inference, every type parameter's argument mapped by
	// reprOf.
	if subs, ok := e.checkerTypeArgs(decl, call); ok {
		return subs, "", true
	}
	positions := genericFuncTypeParamIndex(decl)
	subs = make(map[string]Type, len(decl.TypeParams))
	for i, typeParam := range decl.TypeParams {
		var present []genericParamPos
		for _, pos := range positions[typeParam] {
			if pos.Idx < len(args) {
				present = append(present, pos)
			}
		}
		if len(present) == 0 {
			// Nothing at the call site infers it: the call's contextual
			// type through the result (`const a: number[] = mk()`), else
			// its default, as in tsc (`function f<T = void>(v?: T)` called
			// as `f()`).
			if t, ok := e.contextTypeArg(call, typeParam); ok {
				subs[typeParam] = t
				continue
			}
			if i < len(decl.TypeParamDefaults) && decl.TypeParamDefaults[i] != nil {
				subs[typeParam] = e.annotationType(decl.TypeParamDefaults[i])
				continue
			}
			// Otherwise its constraint, else unknown, as tsc infers it.
			subs[typeParam] = e.uninferredTypeArg(decl, i)
			continue
		}
		// The first position whose argument answers the type parameter: an
		// `any` argument, or one that is not an array where `T[]` is
		// declared, leaves it to the next (`eq(x as any, [1, 2])` is T =
		// number). When none answers, the first one's `any` stands.
		inferred, found := Type{}, false
		for _, pos := range present {
			t, ok := e.inferFromGenericArg(args[pos.Idx], pos)
			if !ok || found && t.IsDynamic() {
				continue
			}
			inferred, found = t, true
			if !t.IsDynamic() {
				break
			}
		}
		if !found {
			inferred = e.uninferredTypeArg(decl, i)
		}
		subs[typeParam] = inferred
	}
	return subs, "", true
}

// uninferredTypeArg is the type argument of decl's i-th type parameter when
// nothing infers it: its constraint when a plain one, else unknown.
func (e *Emitter) uninferredTypeArg(decl *ast.FunctionDeclaration, i int) Type {
	if i < len(decl.TypeParamConstraints) && decl.TypeParamConstraints[i] != nil {
		if c := e.annotationType(decl.TypeParamConstraints[i]); c.IR != "" && c.IR != "void" {
			return c
		}
	}
	return TypeAny
}

// inferFromGenericArg is the type argument the argument arg, at the type
// parameter's position pos, infers; ok is false when it infers none.
func (e *Emitter) inferFromGenericArg(arg ast.Expression, pos genericParamPos) (Type, bool) {
	if pos.Ctor {
		return e.constructedType(arg)
	}
	if pos.Chunk {
		if t := e.typeOfExpr(arg); t.IsReadableStream() && t.StreamChunk != nil {
			return *t.StreamChunk, true
		}
		return Type{}, false
	}
	if pos.FnRet {
		// The callback's own return type (its body inferred, or annotated).
		if t := e.typeOfExpr(arg); t.IsFunc() && t.FuncRetType != nil && t.FuncRetType.IR != "" {
			return *t.FuncRetType, true
		}
		return Type{}, false
	}
	// typeOfExpr has no *ast.ArrayLiteral case of its own (see
	// inferArrayType's separate existing callers, e.g.
	// emit_exprs_vardecl.go) — a literal array argument needs that instead.
	var concrete Type
	if lit, ok := arg.(*ast.ArrayLiteral); ok {
		concrete = e.inferArrayType(lit)
	} else {
		concrete = e.typeOfExpr(arg)
	}
	if !pos.IsArray {
		return concrete, true
	}
	if concrete.IsDynamic() {
		return concrete, true // an `any` argument: T is any
	}
	if !concrete.IsArray() || concrete.ElemType == nil {
		return Type{}, false
	}
	return *concrete.ElemType, true
}

// emitGenericFuncCall is emit_call.go's dispatch target for a call to a
// registered generic function name: infer every type argument via
// inferGenericCallConcreteTypes, instantiate (or reuse a memoized prior
// instantiation), and dispatch exactly like a call to a concrete named
// function.
func (e *Emitter) emitGenericFuncCall(decl *ast.FunctionDeclaration, args []ast.Expression, typeArgs []ast.TypeNode, pos ast.Pos, call *ast.CallExpression) (Value, error) {
	// Polymorphic recursion (`f<T>(x: T) { f([x]) }`) instantiates ever
	// deeper types; past the cap every type parameter is any, one
	// instantiation the deeper calls share.
	if e.genericDepth > 8 {
		subs := map[string]Type{}
		for _, tp := range decl.TypeParams {
			subs[tp] = TypeAny
		}
		mangled, sig, err := e.instantiateGenericFunc(decl, subs)
		if err != nil {
			return Value{}, err
		}
		return e.emitCallToFuncSig(mangled, sig, args, pos)
	}
	e.genericDepth++
	defer func() { e.genericDepth-- }()
	subs, ok := e.explicitGenericSubs(decl, typeArgs)
	if !ok {
		var missing string
		subs, missing, ok = e.inferGenericCallConcreteTypes(decl, args, call)
		if !ok {
			return Value{}, internalAt(pos, "type argument %s of %s not inferred (inference falls back to the constraint or unknown)", missing, ast.Unmangle(decl.Name))
		}
	}
	mangled, sig, err := e.instantiateGenericFunc(decl, subs)
	if err != nil {
		return Value{}, err
	}
	return e.emitCallToFuncSig(mangled, sig, args, pos)
}

// genericCallReturnType is typeOfExpr's pure helper for a call to a
// registered generic function: infers every concrete type the same way
// emitGenericFuncCall would, then substitutes them into decl's return-type
// annotation — or, for an unannotated return type, best-effort-infers it
// from the body against the substituted parameter types (the same
// funcBodyReturnType path instantiateGenericFunc itself uses, which
// is already documented as safe to call without a real function existing
// yet). Returns ok=false wherever emitGenericFuncCall would itself error
// (nothing to infer from, or an unsupported concrete type) — typeOfExpr
// has no error return, so callers just fall through to its own final
// default the same way every other unresolvable case here already does.
func (e *Emitter) genericCallReturnType(decl *ast.FunctionDeclaration, args []ast.Expression, typeArgs []ast.TypeNode, call *ast.CallExpression) (Type, bool) {
	// Same depth cap the generic-interface/alias instantiation uses
	// (ADR-00452/ADR-00473): a self-referential instantiation
	// (`foo<typeof y>()` whose y depends on the call) recurses through
	// inference forever — observed as a real oracle stack overflow.
	if e.genericDepth > 8 {
		return Type{}, false
	}
	e.genericDepth++
	defer func() { e.genericDepth-- }()
	subs, ok := e.explicitGenericSubs(decl, typeArgs)
	if !ok {
		subs, _, ok = e.inferGenericCallConcreteTypes(decl, args, call)
		if !ok {
			return Type{}, false
		}
	}
	if _, err := mangleTypeArgs(decl.TypeParams, subs); err != nil {
		return Type{}, false
	}
	if decl.ReturnType != nil {
		return e.substituteGenericType(decl.ReturnType, subs), true
	}
	var t Type
	var ok2 bool
	e.withTypeParamScope(subs, func() {
		sig := e.buildGenericParamSig(decl.Params, subs)
		paramNames := make([]string, len(decl.Params))
		for i, p := range decl.Params {
			paramNames[i] = p.Name
		}
		t, ok2 = e.funcBodyReturnType(decl.Body, paramNames, e.bodyParamTypes(decl.Params, sig.ParamTypes))
	})
	return t, ok2
}

// instantiateGenericFunc returns the mangled LLVM name and signature for
// decl specialized at subs, building and emitting it on first use. Memoized
// via e.funcs: a repeated instantiation (e.g. the same generic function
// called twice with the same inferred types) is emitted once.
// genericFuncSigIn is the signature of decl instantiated with subs, its
// type parameters already in scope. No code is emitted.
func (e *Emitter) genericFuncSigIn(decl *ast.FunctionDeclaration, subs map[string]Type) FuncSig {
	sig := e.buildGenericParamSig(decl.Params, subs)
	if decl.ReturnType != nil {
		sig.RetType = e.substituteGenericType(decl.ReturnType, subs)
		return sig
	}
	// Best-effort inference, same as registerFunctions.
	paramNames := make([]string, len(decl.Params))
	for i, p := range decl.Params {
		paramNames[i] = p.Name
	}
	if inferred, ok := e.funcBodyReturnType(decl.Body, paramNames, e.bodyParamTypes(decl.Params, sig.ParamTypes)); ok {
		sig.RetType = inferred
	} else {
		sig.RetType = TypeVoid
	}
	return sig
}

// erasedGenericValueType is the type of generic function decl read as a
// value: its erased instantiation's (erasedGenericFunc), computed without
// emitting it.
func (e *Emitter) erasedGenericValueType(decl *ast.FunctionDeclaration) Type {
	subs := make(map[string]Type, len(decl.TypeParams))
	for _, tp := range decl.TypeParams {
		subs[tp] = TypeAny
	}
	var sig FuncSig
	e.withTypeParamScope(subs, func() { sig = e.genericFuncSigIn(decl, subs) })
	return funcValueType(sig)
}

// erasedGenericFunc is decl instantiated with every type parameter `any`:
// the function a generic function is as a value.
func (e *Emitter) erasedGenericFunc(decl *ast.FunctionDeclaration) (string, FuncSig, error) {
	subs := make(map[string]Type, len(decl.TypeParams))
	for _, tp := range decl.TypeParams {
		subs[tp] = TypeAny
	}
	return e.instantiateGenericFunc(decl, subs)
}

func (e *Emitter) instantiateGenericFunc(decl *ast.FunctionDeclaration, subs map[string]Type) (string, FuncSig, error) {
	suffix, err := mangleTypeArgs(decl.TypeParams, subs)
	if err != nil {
		return "", FuncSig{}, internalAt(decl.GetPos(), "generic function %s: %s", decl.Name, err)
	}
	mangled := decl.Name + "__" + suffix

	var sig FuncSig
	var emitErr error
	// The instantiation is the declaration's code: only its own type
	// arguments are in scope, not the instantiating site's, so one
	// instantiation is the same wherever it is first made.
	savedScope := e.typeParamScope
	e.typeParamScope = nil
	defer func() { e.typeParamScope = savedScope }()
	cached, found := false, FuncSig{}
	e.withTypeParamScope(subs, func() {
		sig = e.genericFuncSigIn(decl, subs)
		// Type arguments of one layout may still give different
		// signatures (`f<"a">` and `f<"b">` of a parameter typed `T
		// extends "a" ? 1 : "u"`): each signature its own instance.
		if prev, ok := e.funcs[mangled]; ok && funcSigReprKey(prev) != funcSigReprKey(sig) {
			mangled += fmt.Sprintf("_s%08x", crc32.ChecksumIEEE([]byte(funcSigReprKey(sig))))
		}
		if prev, ok := e.funcs[mangled]; ok {
			cached, found = true, prev
			return
		}

		// Register before emitting the body — guards direct/mutual recursion the
		// same way top-level forward references already rely on signatures
		// being registered ahead of bodies (registerFunctions vs. emitFunctionDecl).
		// The body is emitted with the type parameters in scope, so a
		// `new Promise<T>` / `const xs: T[]` inside it substitutes too.
		e.funcs[mangled] = sig
		// Emitted as its declaration's code, wherever the instantiating
		// call is: a builtin module's names its literals by its module.
		restore := e.libScope(decl)
		savedInst := e.genericInst
		e.genericInst = suffix
		emitErr = e.emitFunctionDeclAs(decl, mangled, sig)
		e.genericInst = savedInst
		restore()
	})
	if cached {
		return mangled, found, nil
	}
	if emitErr != nil {
		delete(e.funcs, mangled)
		return "", FuncSig{}, emitErr
	}
	return mangled, sig, nil
}

// funcSigReprKey is the layout of sig's parameters and result.
func funcSigReprKey(sig FuncSig) string {
	var b strings.Builder
	for _, p := range sig.ParamTypes {
		b.WriteString(reprKey(p))
		b.WriteByte(',')
	}
	b.WriteString("->")
	b.WriteString(reprKey(sig.RetType))
	return b.String()
}

// withMethodFields appends an interface's method signatures as
// function-typed members (TDD-00233), as TypeScript types them; a name a
// property already has, or an overload's later signature, adds nothing.
func (e *Emitter) withMethodFields(fields []Field, methods []ast.InterfaceMethodSig, resolve func(ast.TypeNode) Type) []Field {
	seen := map[string]bool{}
	for _, f := range fields {
		seen[f.Name] = true
	}
	overloaded := overloadedMethods(methods)
	for _, m := range methods {
		if seen[m.Name] || m.Type == nil {
			continue
		}
		seen[m.Name] = true
		fty := resolve(m.Type)
		if overloaded[m.Name] {
			// Several signatures: a dynamic function value, called with
			// its arguments as written, whichever signature they select.
			fty = TypeAny
		}
		if m.Optional {
			fty = undefinedableElem(fty)
		}
		fields = append(fields, Field{Name: m.Name, Ty: fty, Optional: m.Optional})
	}
	return fields
}

// overloadedMethods are the method names methods declares more than once.
func overloadedMethods(methods []ast.InterfaceMethodSig) map[string]bool {
	n := map[string]int{}
	for _, m := range methods {
		n[m.Name]++
	}
	out := map[string]bool{}
	for k, c := range n {
		if c > 1 {
			out[k] = true
		}
	}
	return out
}

// genericClassMangledFields is the pure (no IR emission, no e.classes/
// e.interfaces registration) core shared by instantiateGenericClass and
// genericClassInstanceType: the mangled name and field substitution are
// exactly the same work either way, only what's done with the result
// differs. Safe to call speculatively/repeatedly.
func (e *Emitter) genericClassMangledFields(decl *ast.ClassDeclaration, subs map[string]Type) (string, []Field, error) {
	suffix, err := mangleTypeArgs(decl.TypeParams, subs)
	if err != nil {
		return "", nil, internalAt(decl.GetPos(), "generic class %s: %s", decl.Name, err)
	}
	mangled := decl.Name + "__" + suffix
	var ownFields []Field
	for _, f := range decl.Fields {
		if f.Static {
			continue // the class's, not an instance's
		}
		if f.Name == ClassTagField || f.Name == ClassVTableField {
			return "", nil, internalAt(decl.GetPos(), "class %s declares the reserved field %s", decl.Name, f.Name)
		}
		// An unannotated field (`x = expr`, TDD-00063 Stage 1) has no type to
		// substitute T into — its type comes from its initializer instead,
		// the same compile-time inference the non-generic path uses.
		var fty Type
		if f.Type != nil {
			fty = e.substituteGenericType(f.Type, subs)
			// `tag?: T` widens to `T | undefined` (TDD-00187 Stage 2).
			if f.Optional {
				fty = optionalFieldType(fty)
			}
		} else {
			fty = e.typeOfExpr(f.Initializer)
		}
		ownFields = append(ownFields, Field{Name: f.Name, Ty: fty})
	}
	return mangled, ownFields, nil
}

// genericClassInstanceType is the pure sibling instantiateGenericClass's own
// real (memoized, body-emitting) path delegates to: it returns the Type a
// `new ClassName<...>(...)` expression evaluates to, without registering
// anything into e.classes or emitting any IR — safe to call from
// typeOfExpr and emitVarDecl's own pre-inference type lookup, both of
// which must never trigger real emission as a side effect of merely asking
// "what type is this expression."  The real emission still only ever
// happens once, from emitNewExpression's own call to
// instantiateGenericClass at the actual construction site.
//
// Memoized per instantiation. A field whose type is the instantiation itself
// (`next: List<T>`) gets the class by name while its own fields are still
// being built: rebuilding it there recursed once per such field and level,
// exponentially with two of them.
func (e *Emitter) genericClassInstanceType(decl *ast.ClassDeclaration, subs map[string]Type) (Type, error) {
	suffix, err := mangleTypeArgs(decl.TypeParams, subs)
	if err == nil {
		key := decl.Name + "__" + suffix
		if t, ok := e.genericInstTypes[key]; ok {
			return t, nil
		}
		if e.genericInstBuilding[key] {
			return ClassType(key, nil, nil, false), nil
		}
		if e.genericInstBuilding == nil {
			e.genericInstBuilding, e.genericInstTypes = map[string]bool{}, map[string]Type{}
		}
		e.genericInstBuilding[key] = true
		defer delete(e.genericInstBuilding, key)
	}
	mangled, ownFields, err := e.genericClassMangledFields(decl, subs)
	if err != nil {
		return Type{}, err
	}
	t := ClassType(mangled, nil, ownFields, false)
	e.genericInstTypes[mangled] = t
	if e.genericInstSrc == nil {
		e.genericInstSrc = map[string]genericInstSource{}
	}
	e.genericInstSrc[mangled] = genericInstSource{decl: decl, subs: subs}
	return t, nil
}

type genericInstSource struct {
	decl *ast.ClassDeclaration
	subs map[string]Type
}

// ensureGenericInstance instantiates the generic class instantiation named
// name when a type named it before any construction did, reporting whether
// the class is registered.
func (e *Emitter) ensureGenericInstance(name string) (bool, error) {
	if _, ok := e.classes[name]; ok {
		return true, nil
	}
	src, ok := e.genericInstSrc[name]
	if !ok {
		return false, nil
	}
	if _, err := e.instantiateGenericClass(src.decl, src.subs); err != nil {
		return false, err
	}
	_, ok = e.classes[name]
	return ok, nil
}

// instantiateGenericClass builds and emits (on first use) a full,
// independent ClassInfo for decl specialized at subs, and returns its
// mangled name. Memoized via e.classes. Scoped-down relative to a plain
// class (registerClasses/emitClassDecl): no inheritance, vtable or static
// members — registerClasses already rejects those on
// any generic class declaration before it ever reaches here (see its own
// validation), so this only ever needs to handle fields + constructor +
// instance methods.
func (e *Emitter) instantiateGenericClass(decl *ast.ClassDeclaration, subs map[string]Type) (string, error) {
	mangled, ownFields, err := e.genericClassMangledFields(decl, subs)
	if err != nil {
		return "", err
	}
	if _, ok := e.classes[mangled]; ok {
		return mangled, nil
	}
	// An instantiation shows its generic's name (`Box`, not `Box__num`).
	if e.classShownNames == nil {
		e.classShownNames = map[string]string{}
	}
	e.classShownNames[mangled] = e.classDisplayName(decl.Name)
	ty := ClassType(mangled, nil, ownFields, false)
	e.interfaces[mangled] = ty

	info := ClassInfo{
		TypeSubs:                subs,
		GenericOf:               decl.Name,
		Ty:                      ty,
		InheritedFields:         nil,
		OwnFields:               ownFields,
		FlatFields:              ownFields,
		Methods:                 make(map[string]*ast.FunctionDeclaration),
		MethodSigs:              make(map[string]FuncSig),
		MethodImplementor:       make(map[string]string),
		MethodDispatchSlot:      make(map[string]*MethodSlot),
		TagID:                   e.classTypeID(mangled),
		RootClass:               mangled,
		FieldOrigin:             make(map[string]string),
		StaticFieldTypes:        make(map[string]Type),
		OwnStaticFieldTypes:     make(map[string]Type),
		StaticFieldOwner:        make(map[string]string),
		StaticMethodSigs:        make(map[string]FuncSig),
		StaticMethodImplementor: make(map[string]string),
		GenMethodInfo:           make(map[string]*GeneratorInfo),
	}
	for _, f := range decl.Fields {
		info.FieldOrigin[f.Name] = mangled
	}

	// TDD-00063 Stage 1: run own field initializers at the top of the
	// constructor. A generic class can never `extends` (registerClasses
	// rejects it), so there is never a super() to sequence after — the
	// initializers always go first. This path runs once per distinct
	// type-arg set, so it builds a fresh constructor/body rather than
	// splicing into the shared template AST (which the non-generic
	// registerClasses path, running exactly once per class, does instead).
	switch {
	case decl.Constructor != nil:
		sig := e.buildGenericParamSig(decl.Constructor.Params, subs)
		sig.RetType = TypeVoid
		ctor := decl.Constructor
		if inits := classFieldInitStmts(decl); len(inits) > 0 {
			body := ast.NewBlockStatement(append(append([]ast.Statement{}, inits...), decl.Constructor.Body.Body...), decl.Constructor.GetPos())
			ctor = &ast.FunctionDeclaration{Name: decl.Constructor.Name, Params: decl.Constructor.Params, ReturnType: decl.Constructor.ReturnType, Body: body}
		}
		info.Constructor = ctor
		info.CtorSig = sig
	case len(ownFields) > 0:
		// ADR-00373: no explicit constructor and at least one own field —
		// synthesize a constructor running whatever field initializers exist
		// (`classFieldInitStmts` skips uninitialized fields); the rest stay at
		// their calloc'd deterministic-zero value (ADR-00157). A generic class
		// can't `extends`, so there is never a super() to sequence first.
		body := ast.NewBlockStatement(classFieldInitStmts(decl), decl.GetPos())
		sig := e.buildGenericParamSig(nil, subs)
		sig.RetType = TypeVoid
		info.Constructor = &ast.FunctionDeclaration{Name: "constructor", Body: body}
		info.CtorSig = sig
	}

	// Registered while its methods' signatures are built, so an inferred
	// return that calls a sibling (`return this.#run()`) sees the
	// sibling's; a second pass settles one that calls a later sibling.
	e.classes[mangled] = info
	inferRet := func(m *ast.FunctionDeclaration, sig FuncSig) Type {
		ret := TypeVoid
		e.pushScope()
		e.define("this", Symbol{Ty: ty})
		if inferred, ok := e.funcBodyReturnType(m.Body, sig.ParamNames, e.bodyParamTypes(m.Params, sig.ParamTypes)); ok {
			ret = inferred
		}
		e.popScope()
		// An unannotated async method returns the promise of its result
		// (methodSig's rule).
		if m.IsAsync && !ret.IsPromise() {
			ret = PromiseOf(ret)
		}
		return ret
	}
	for _, m := range decl.Methods {
		key := genericMethodKey(m)
		if _, dup := info.MethodSigs[key]; dup {
			return "", internalAt(m.GetPos(), "class %s keeps two methods named %s (a later one replaces an earlier before code generation)", decl.Name, m.Name)
		}
		// A generator method: a call constructs a generator instance over
		// its body (as on a non-generic class), with no dispatch slot.
		if m.IsGenerator && m.Body != nil {
			var genInfo *GeneratorInfo
			var gerr error
			e.withTypeParamScope(subs, func() { genInfo, gerr = e.buildGeneratorMethodInfo(m, mangled, ty) })
			if gerr != nil {
				return "", gerr
			}
			sig := e.buildGenericParamSig(m.Params, subs)
			sig.RetType = genInfo.GenTy
			info.GenMethodInfo[key] = genInfo
			info.MethodImplementor[key] = mangled
			info.MethodSigs[key] = sig
			info.Methods[key] = m
			continue
		}
		sig := e.buildGenericParamSig(m.Params, subs)
		if m.AccessorKind == "set" {
			sig.RetType = TypeVoid // a setter's result is discarded
		} else if annotIs(m.ReturnType, "this") {
			sig.RetType = ty // `this`: the instantiation's own instance
			sig.RetThis = true
		} else if m.ReturnType != nil {
			sig.RetType = e.substituteGenericType(m.ReturnType, subs)
		} else {
			sig.RetType = inferRet(m, sig)
		}
		sig.IsAsync = m.IsAsync
		info.MethodImplementor[key] = mangled
		info.MethodSigs[key] = sig
		info.Methods[key] = m
		info.MethodOrder = append(info.MethodOrder, key)
	}
	for _, m := range decl.Methods {
		key := genericMethodKey(m)
		if m.IsGenerator || m.AccessorKind == "set" || m.ReturnType != nil || m.Body == nil {
			continue
		}
		sig := info.MethodSigs[key]
		sig.RetType = inferRet(m, sig)
		info.MethodSigs[key] = sig
	}

	// Register before emitting bodies — guards a method/constructor that
	// constructs another instance of the same instantiation recursively,
	// the same way instantiateGenericFunc registers its signature first.
	e.classes[mangled] = info
	e.classSrc[mangled] = decl.Src
	// The members' bodies see the type arguments (`x as T`, a local's `T[]`),
	// as a generic function's body does.
	var emitErr error
	restore := e.libScope(decl)
	savedInst := e.genericInst
	e.genericInst = mangled
	e.withTypeParamScope(subs, func() { emitErr = e.emitClassDeclAs(decl, mangled, info) })
	e.genericInst = savedInst
	restore()
	if emitErr != nil {
		delete(e.classes, mangled)
		return "", emitErr
	}
	return mangled, nil
}

// emitClassDeclAs is emitClassDecl's generic-instantiation sibling: the same
// "@llvmName__kml_ctor" / "@llvmName_methodName" emission shape
// (emitClassMember does the actual work, already fully parameterized by
// name/type/sig — no changes needed there), but reading info directly
// instead of e.classes[decl.Name] and naming every emitted symbol off
// llvmName (the mangled instantiation name) instead of decl.Name. No static
// members here — registerClasses already rejects those on any generic class
// declaration before it's ever registered into e.genericClasses.
func (e *Emitter) emitClassDeclAs(decl *ast.ClassDeclaration, llvmName string, info ClassInfo) error {
	if info.Constructor != nil {
		ctorName := llvmName + ctorSymbolSuffix
		e.currentCtorClass = llvmName
		err := e.emitClassMember(ctorName, info.Ty, info.Constructor.Params, info.CtorSig, info.Constructor.Body, TypeVoid, info.Constructor.GetPos(), false, false)
		e.currentCtorClass = ""
		if err != nil {
			return err
		}
	}
	for _, m := range decl.Methods {
		key := genericMethodKey(m)
		if genInfo := info.GenMethodInfo[key]; genInfo != nil {
			// A generator method's body, bound to the receiver it was
			// constructed with.
			genInfo.ThisTy = &info.Ty
			if err := e.emitGeneratorFunctionDecl(m, genInfo); err != nil {
				return err
			}
			continue
		}
		sig := info.MethodSigs[key]
		memberName := llvmSafeSymbol(llvmName + "_" + key)
		if err := e.emitClassMember(memberName, info.Ty, m.Params, sig, m.Body, sig.RetType, m.GetPos(), false, m.IsAsync); err != nil {
			return err
		}
	}
	return nil
}

// explicitGenericSubs builds the type-parameter substitution map from
// explicit call-site type arguments (`id<string>(x)` — ADR-00473),
// positionally against decl's parameter list. ok is false when no explicit
// arguments were given or the arity doesn't cover the parameters (callers
// then fall back to inference).
func (e *Emitter) explicitGenericSubs(decl *ast.FunctionDeclaration, typeArgs []ast.TypeNode) (map[string]Type, bool) {
	if len(typeArgs) == 0 || len(typeArgs) < len(decl.TypeParams) {
		return nil, false
	}
	subs := make(map[string]Type, len(decl.TypeParams))
	for i, tp := range decl.TypeParams {
		subs[tp] = e.annotationType(typeArgs[i])
	}
	return subs, true
}

// classTypeArgs is the type argument list a `new C<…>(…)` of the generic
// class decl instantiates it with: the explicit ones, then each omitted
// parameter's default (`class C<T = any>`). ok is false when an omitted
// parameter has no default.
func classTypeArgs(decl *ast.ClassDeclaration, explicit []ast.TypeNode) (args []ast.TypeNode, ok bool) {
	args = explicit
	for i := len(args); i < len(decl.TypeParams); i++ {
		if i >= len(decl.TypeParamDefaults) || decl.TypeParamDefaults[i] == nil {
			return nil, false
		}
		args = append(args[:i:i], decl.TypeParamDefaults[i])
	}
	return args, len(args) == len(decl.TypeParams)
}

// genericArgsOK reports whether classTypeArgs completes explicit.
func genericArgsOK(decl *ast.ClassDeclaration, explicit []ast.TypeNode) bool {
	_, ok := classTypeArgs(decl, explicit)
	return ok
}

// genericMethodKey is a generic class member's dispatch key, as
// registerClasses keys a class's: an accessor under accessorMethodName.
func genericMethodKey(m *ast.FunctionDeclaration) string {
	if m.AccessorKind != "" {
		return accessorMethodName(m.AccessorKind, m.Name)
	}
	return m.Name
}

// genericInstMethodRet is the declared result of method name on the
// instantiation className when it is not instantiated yet, without
// instantiating it (type inference never emits).
func (e *Emitter) genericInstMethodRet(className, name string) (Type, bool) {
	if _, ok := e.classes[className]; ok {
		return Type{}, false
	}
	src, ok := e.genericInstSrc[className]
	if !ok {
		return Type{}, false
	}
	for _, m := range src.decl.Methods {
		if m.Name != name || m.AccessorKind != "" {
			continue
		}
		if m.ReturnType == nil {
			// Unannotated: inferred from its body, as the instantiation
			// will infer it (an async one's promise).
			self, ok := e.genericInstTypes[className]
			if !ok || m.Body == nil || m.IsGenerator || e.genericInstInferring[m] {
				return Type{}, false
			}
			if e.genericInstInferring == nil {
				e.genericInstInferring = map[*ast.FunctionDeclaration]bool{}
			}
			e.genericInstInferring[m] = true
			defer delete(e.genericInstInferring, m)
			ret := TypeVoid
			e.withTypeParamScope(src.subs, func() {
				sig := e.buildGenericParamSig(m.Params, src.subs)
				e.pushScope()
				e.define("this", Symbol{Ty: self})
				if t, ok := e.funcBodyReturnType(m.Body, sig.ParamNames, e.bodyParamTypes(m.Params, sig.ParamTypes)); ok {
					ret = t
				}
				e.popScope()
			})
			if m.IsAsync && !ret.IsPromise() {
				ret = PromiseOf(ret)
			}
			return ret, true
		}
		if annotIs(m.ReturnType, "this") {
			if t, ok := e.genericInstTypes[className]; ok {
				return t, true // the instantiation's own instance
			}
		}
		var t Type
		e.withTypeParamScope(src.subs, func() { t = e.substituteGenericType(m.ReturnType, src.subs) })
		return t, true
	}
	return Type{}, false
}

// checkerTypeArgs is each of decl's type parameters' argument the checker
// infers for call, mapped by reprOf; ok is false unless it answers all.
func (e *Emitter) checkerTypeArgs(decl *ast.FunctionDeclaration, call *ast.CallExpression) (map[string]Type, bool) {
	c := e.front()
	if call == nil || c == nil {
		return nil, false
	}
	if orig, ok := e.callOrigins[call]; ok {
		call = orig
	}
	targs := c.CallTypeArgs(call)
	subs := make(map[string]Type, len(decl.TypeParams))
	for _, tp := range decl.TypeParams {
		t := targs[tp]
		if t == nil {
			return nil, false
		}
		r, ok := reprOf(t)
		if !ok && e.reprOracle() != nil {
			// An object type argument (`A = { BLAH: number }`): its layout,
			// as an annotation of it has.
			r, ok = e.repr().of(t)
		}
		if i := typeParamIndex(decl, tp); i >= 0 && (i < len(decl.TypeParamConstraints) && tupleAnnotation(decl.TypeParamConstraints[i]) || spreadInTuple(decl, tp)) {
			// A tuple constraint (`T extends [A, B]`) types the argument as
			// the tuple an annotation declares, as an explicit type
			// argument does.
			rc := reprCtx{tuples: true}
			if e.reprOracle() != nil {
				rc = e.repr()
				rc.tuples = true
			}
			r, ok = rc.of(t)
		}
		if !ok {
			return nil, false
		}
		subs[tp] = r
	}
	return subs, true
}

// typeParamIndex is the position of type parameter name in decl, or -1.
func typeParamIndex(decl *ast.FunctionDeclaration, name string) int {
	for i, tp := range decl.TypeParams {
		if tp == name {
			return i
		}
	}
	return -1
}

// tupleAnnotation reports a type written as a tuple (`[A, B]`).
func tupleAnnotation(n ast.TypeNode) bool {
	_, ok := n.(*ast.TupleType)
	return ok
}

// spreadInTuple reports a type parameter a parameter of decl spreads in a
// tuple type (`x: [...A]`, `...args: [...A, L]`): what it is inferred to is
// a tuple, laid out as one.
func spreadInTuple(decl *ast.FunctionDeclaration, tp string) bool {
	found := false
	var walk func(n ast.TypeNode, inTuple bool)
	walk = func(n ast.TypeNode, inTuple bool) {
		switch x := n.(type) {
		case *ast.TupleType:
			for _, el := range x.Elements {
				walk(el, true)
			}
		case *ast.RestType:
			if r, ok := x.Type.(*ast.TypeReference); ok && inTuple && r.Name == tp && len(r.TypeArgs) == 0 && len(r.Qualifier) == 0 {
				found = true
			}
		case *ast.NamedTupleMember:
			if r, ok := x.Type.(*ast.TypeReference); ok && x.Rest && inTuple && r.Name == tp && len(r.TypeArgs) == 0 && len(r.Qualifier) == 0 {
				found = true
			}
		case *ast.ParenthesizedType:
			walk(x.Type, inTuple)
		}
	}
	for _, p := range decl.Params {
		if p.Type != nil {
			walk(p.Type, false)
		}
	}
	return found
}

// contextTypeArg is the type argument the checker infers for the generic
// call's type parameter name from the call's contextual type; a call
// synthesized for a builtin's lowering asks about the program's call, whose
// declaration names its type parameters as the lowering does.
func (e *Emitter) contextTypeArg(call *ast.CallExpression, name string) (Type, bool) {
	c := e.front()
	if call == nil || c == nil {
		return Type{}, false
	}
	if orig, ok := e.callOrigins[call]; ok {
		call = orig
	}
	t := c.CallTypeArgs(call)[name]
	if t == nil {
		return Type{}, false
	}
	return reprOf(t)
}

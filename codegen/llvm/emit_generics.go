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
	"hash/fnv"
	"strings"
)

// mangleTypeArg returns a compact, LLVM-identifier-safe suffix identifying a
// concrete generic instantiation argument, or an error naming why a type
// isn't accepted in V1 — object/class/Map/Set/Promise/closure type
// arguments are all deliberately out of scope (see this file's doc comment).
func mangleTypeArg(t Type) (string, error) {
	if t.IsArray {
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
	if t.IsClass && !t.IsDynamic {
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
	if t.IsObject && !t.IsDynamicObject && !t.IsMap && !t.IsSet && !t.IsDynamic {
		return mangleObjectStructural(t)
	}
	// `any` (and `unknown`): the instantiation's T slots are NaN-boxed values,
	// as any `any`-typed code (`class C<T = any>` constructed without type
	// arguments takes this).
	if isUnconstrainedDynamic(t) {
		return "any", nil
	}
	if t.IsMap || t.IsSet || t.IsPromise || t.IsFunc || t.IsDynamicObject || t.IsDynamic {
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
// arguments), resolving each via resolveType. Arity mismatches are each
// caller's own responsibility to reject before calling this — the parser
// has no symbol table (see parseTypeParamList's own doc comment), so arity
// checking happens here, at the codegen call sites that already know both
// lengths. Only the shorter of the two lengths is zipped; a caller that
// hasn't already validated matching lengths gets a partially-built map, not
// a panic.
func (e *Emitter) buildTypeArgSubs(typeParams []string, typeArgs []*ast.TypeAnnotation) map[string]Type {
	subs := make(map[string]Type, len(typeParams))
	for i, tp := range typeParams {
		if i >= len(typeArgs) {
			break
		}
		subs[tp] = e.resolveType(typeArgs[i])
	}
	return subs
}

// substituteGenericType resolves a declaration-site type annotation that may
// reference one of subs' type-parameter names (a bare "T", or "T[]" — V1
// doesn't support nesting a type parameter any deeper than one array level)
// into its concrete Type, substituting concrete for every occurrence;
// anything else resolves normally via resolveType, exactly as it would for a
// non-generic declaration.
func (e *Emitter) substituteGenericType(ta *ast.TypeAnnotation, subs map[string]Type) Type {
	if ta == nil {
		return TypeVoid
	}
	if concrete, ok := subs[ta.Name]; ok {
		// `T | undefined`: the reference's own absence carries over.
		// (as resolveType's type-parameter scope does).
		if ta.Nullable {
			concrete.Nullable = true
			concrete.IsUndefined = ta.Undefined
		}
		return concrete
	}
	for typeParam, concrete := range subs {
		if ta.Name == typeParam+"[]" {
			t := ArrayOf(concrete)
			if ta.Nullable {
				t.Nullable, t.IsUndefined = true, ta.Undefined
			}
			return t
		}
	}
	// Anything deeper (`Promise<T>`, `Map<string, T>`, `T[][]`, a closure type
	// mentioning T) resolves structurally with the type parameters in scope.
	var t Type
	e.withTypeParamScope(subs, func() { t = e.resolveType(ta) })
	return t
}

// withTypeParamScope runs fn with subs layered over the active type-parameter
// scope (an inner generic instantiation shadows an outer one's names), so
// every resolveType inside fn — a body's `new Promise<T>`, a local's `T[]`
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
// per-parameter rules (explicit annotation via resolveType, unannotated rest
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
		sig.Defaults = append(sig.Defaults, p.Default)
		sig.Optional = append(sig.Optional, p.Optional)
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
			switch {
			case p.Type.Name == typeParam:
				positions[typeParam] = append(positions[typeParam], genericParamPos{Idx: i})
			case p.Type.Name == typeParam+"[]":
				positions[typeParam] = append(positions[typeParam], genericParamPos{Idx: i, IsArray: true})
			case p.Type.IsCtorType && p.Type.FuncRetType != nil && p.Type.FuncRetType.Name == typeParam:
				positions[typeParam] = append(positions[typeParam], genericParamPos{Idx: i, Ctor: true})
			case p.Type.IsFuncType && !p.Type.IsCtorType && p.Type.FuncRetType != nil && p.Type.FuncRetType.Name == typeParam:
				positions[typeParam] = append(positions[typeParam], genericParamPos{Idx: i, FnRet: true})
			}
		}
	}
	return positions
}

// inferGenericCallConcreteTypes is the pure (no IR emission) core shared by
// emitGenericFuncCall and inferExprType's own generic-call case: finds, for
// every one of decl's type parameters independently, its own type-parameter-
// typed argument at the call site and infers its concrete type. Returns
// ok=false and the specific type-parameter name nothing could be inferred
// for the moment any one of them fails — safe to call speculatively/
// repeatedly, since inferExprType (unlike emitExpr) must never trigger real
// emission as a side effect of merely asking "what type is this call."
func (e *Emitter) inferGenericCallConcreteTypes(decl *ast.FunctionDeclaration, args []ast.Expression) (subs map[string]Type, missing string, ok bool) {
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
			// Nothing at the call site infers it: its default, as in tsc
			// (`function f<T = void>(v?: T)` called as `f()`).
			if i < len(decl.TypeParamDefaults) && decl.TypeParamDefaults[i] != nil {
				subs[typeParam] = e.resolveType(decl.TypeParamDefaults[i])
				continue
			}
			return nil, typeParam, false
		}
		// The first position whose argument answers the type parameter: an
		// `any` argument, or one that is not an array where `T[]` is
		// declared, leaves it to the next (`eq(x as any, [1, 2])` is T =
		// number). When none answers, the first one's `any` stands.
		inferred, found := Type{}, false
		for _, pos := range present {
			t, ok := e.inferFromGenericArg(args[pos.Idx], pos)
			if !ok || found && t.IsDynamic {
				continue
			}
			inferred, found = t, true
			if !t.IsDynamic {
				break
			}
		}
		if !found {
			return nil, typeParam, false
		}
		subs[typeParam] = inferred
	}
	return subs, "", true
}

// inferFromGenericArg is the type argument the argument arg, at the type
// parameter's position pos, infers; ok is false when it infers none.
func (e *Emitter) inferFromGenericArg(arg ast.Expression, pos genericParamPos) (Type, bool) {
	if pos.Ctor {
		return e.constructedType(arg)
	}
	if pos.FnRet {
		// The callback's own return type (its body inferred, or annotated).
		if t := e.inferExprType(arg); t.IsFunc && t.FuncRetType != nil && t.FuncRetType.IR != "" {
			return *t.FuncRetType, true
		}
		return Type{}, false
	}
	// inferExprType has no *ast.ArrayLiteral case of its own (see
	// inferArrayType's separate existing callers, e.g.
	// emit_exprs_vardecl.go) — a literal array argument needs that instead.
	var concrete Type
	if lit, ok := arg.(*ast.ArrayLiteral); ok {
		concrete = e.inferArrayType(lit)
	} else {
		concrete = e.inferExprType(arg)
	}
	if !pos.IsArray {
		return concrete, true
	}
	if concrete.IsDynamic {
		return concrete, true // an `any` argument: T is any
	}
	if !concrete.IsArray || concrete.ElemType == nil {
		return Type{}, false
	}
	return *concrete.ElemType, true
}

// emitGenericFuncCall is emit_call.go's dispatch target for a call to a
// registered generic function name: infer every type argument via
// inferGenericCallConcreteTypes, instantiate (or reuse a memoized prior
// instantiation), and dispatch exactly like a call to a concrete named
// function.
// checkTypeParamConstraints enforces each `<T extends X>` bound (TDD-00113): the
// concrete type inferred/supplied for a constrained type parameter must
// structurally satisfy its constraint annotation (reusing matchExtends, the same
// structural-subtype test conditional types use — width subtyping, so an object
// with extra fields still satisfies a smaller shape). An unconstrained parameter
// (nil entry) is always fine. constraints is positionally aligned with
// typeParams; a nil/short slice means no constraints. declKind/declName/pos are
// for the error message.
func (e *Emitter) checkTypeParamConstraints(typeParams []string, constraints []*ast.TypeAnnotation, subs map[string]Type, declKind, declName string, pos ast.Pos) error {
	for i, name := range typeParams {
		if i >= len(constraints) || constraints[i] == nil {
			continue
		}
		concrete, ok := subs[name]
		if !ok {
			continue
		}
		if !e.matchExtends(concrete, constraints[i], map[string]Type{}) {
			return fmt.Errorf("%d:%d: type argument for '%s' does not satisfy the constraint '%s extends %s' on %s '%s'",
				pos.Line, pos.Col, name, name, inspectClassName(constraints[i].Name), declKind, inspectClassName(declName))
		}
	}
	return nil
}

func (e *Emitter) emitGenericFuncCall(decl *ast.FunctionDeclaration, args []ast.Expression, typeArgs []*ast.TypeAnnotation, pos ast.Pos) (Value, error) {
	if e.genericDepth > 8 {
		return Value{}, fmt.Errorf("%d:%d: type instantiation for generic function '%s' is excessively deep (self-referential type arguments)", pos.Line, pos.Col, decl.Name)
	}
	e.genericDepth++
	defer func() { e.genericDepth-- }()
	subs, ok := e.explicitGenericSubs(decl, typeArgs)
	if !ok {
		var missing string
		subs, missing, ok = e.inferGenericCallConcreteTypes(decl, args)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: cannot infer type argument '%s' for generic function '%s' — declare a parameter typed '%s' or '%s[]' to infer from, or pass explicit call-site type arguments (`%s<T>(…)`)", pos.Line, pos.Col, missing, ast.Unmangle(decl.Name), missing, missing, ast.Unmangle(decl.Name))
		}
	}
	if err := e.checkTypeParamConstraints(decl.TypeParams, decl.TypeParamConstraints, subs, "function", decl.Name, pos); err != nil {
		return Value{}, err
	}
	mangled, sig, err := e.instantiateGenericFunc(decl, subs)
	if err != nil {
		return Value{}, err
	}
	return e.emitCallToFuncSig(mangled, sig, args, pos)
}

// genericCallReturnType is inferExprType's pure helper for a call to a
// registered generic function: infers every concrete type the same way
// emitGenericFuncCall would, then substitutes them into decl's return-type
// annotation — or, for an unannotated return type, best-effort-infers it
// from the body against the substituted parameter types (the same
// inferUnannotatedReturnType path instantiateGenericFunc itself uses, which
// is already documented as safe to call without a real function existing
// yet). Returns ok=false wherever emitGenericFuncCall would itself error
// (nothing to infer from, or an unsupported concrete type) — inferExprType
// has no error return, so callers just fall through to its own final
// default the same way every other unresolvable case here already does.
func (e *Emitter) genericCallReturnType(decl *ast.FunctionDeclaration, args []ast.Expression, typeArgs []*ast.TypeAnnotation) (Type, bool) {
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
		subs, _, ok = e.inferGenericCallConcreteTypes(decl, args)
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
		t, ok2 = e.inferUnannotatedReturnType(decl.Body, paramNames, sig.ParamTypes)
	})
	return t, ok2
}

// instantiateGenericFunc returns the mangled LLVM name and signature for
// decl specialized at subs, building and emitting it on first use. Memoized
// via e.funcs: a repeated instantiation (e.g. the same generic function
// called twice with the same inferred types) is emitted once.
func (e *Emitter) instantiateGenericFunc(decl *ast.FunctionDeclaration, subs map[string]Type) (string, FuncSig, error) {
	suffix, err := mangleTypeArgs(decl.TypeParams, subs)
	if err != nil {
		return "", FuncSig{}, fmt.Errorf("%d:%d: generic function '%s': %s", decl.GetPos().Line, decl.GetPos().Col, decl.Name, err)
	}
	mangled := decl.Name + "__" + suffix
	if sig, ok := e.funcs[mangled]; ok {
		return mangled, sig, nil
	}

	var sig FuncSig
	var emitErr error
	e.withTypeParamScope(subs, func() {
		sig = e.buildGenericParamSig(decl.Params, subs)
		if decl.ReturnType != nil {
			sig.RetType = e.substituteGenericType(decl.ReturnType, subs)
		} else {
			// Best-effort inference, same as registerFunctions.
			paramNames := make([]string, len(decl.Params))
			for i, p := range decl.Params {
				paramNames[i] = p.Name
			}
			if inferred, ok := e.inferUnannotatedReturnType(decl.Body, paramNames, sig.ParamTypes); ok {
				sig.RetType = inferred
			} else {
				sig.RetType = TypeVoid
			}
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
	if emitErr != nil {
		delete(e.funcs, mangled)
		return "", FuncSig{}, emitErr
	}
	return mangled, sig, nil
}

// instantiateGenericInterface builds the concrete ObjectType for decl's
// fields with subs substituted for decl's own type parameters — called from
// resolveType whenever a `Box<number, string>`-shaped type annotation names
// a registered generic interface. Not memoized, matching this codebase's
// existing convention for ArrayOf/MapType/SetType/PromiseOf (types.go) —
// each call builds a fresh, structurally-equal Type value.
func (e *Emitter) instantiateGenericInterface(decl *ast.InterfaceDeclaration, subs map[string]Type) Type {
	// An index signature (`interface Dict<T> { [k: string]: T }`) makes it a
	// dictionary of the substituted value type.
	if decl.IndexSig != nil {
		keyTy := TypePtr
		valTy := e.substituteGenericType(decl.IndexSig, subs)
		dict := Type{IR: "ptr", IsMap: true, IsDynamicObject: true, MapKey: &keyTy, MapVal: &valTy}
		return e.withDictFields(dict, decl.Fields)
	}
	fields := make([]Field, len(decl.Fields))
	for i, f := range decl.Fields {
		fty := e.substituteGenericType(f.Type, subs)
		// `name?: T` widens to `T | undefined` (TDD-00187 Stage 2).
		if f.Optional {
			fty = optionalFieldType(fty)
		}
		fields[i] = Field{Name: f.Name, Ty: fty, Optional: f.Optional}
	}
	fields = e.withMethodFields(fields, decl.Methods, func(t *ast.TypeAnnotation) Type { return e.substituteGenericType(t, subs) })
	return ObjectType(fields)
}

// withMethodFields appends an interface's method signatures as
// function-typed members (TDD-00233), as TypeScript types them; a name a
// property already has, or an overload's later signature, adds nothing.
func (e *Emitter) withMethodFields(fields []Field, methods []ast.InterfaceMethodSig, resolve func(*ast.TypeAnnotation) Type) []Field {
	seen := map[string]bool{}
	for _, f := range fields {
		seen[f.Name] = true
	}
	for _, m := range methods {
		if seen[m.Name] || m.Type == nil {
			continue
		}
		seen[m.Name] = true
		fty := resolve(m.Type)
		if m.Optional {
			fty = undefinedableElem(fty)
		}
		fields = append(fields, Field{Name: m.Name, Ty: fty, Optional: m.Optional})
	}
	return fields
}

// genericClassMangledFields is the pure (no IR emission, no e.classes/
// e.interfaces registration) core shared by instantiateGenericClass and
// genericClassInstanceType: the mangled name and field substitution are
// exactly the same work either way, only what's done with the result
// differs. Safe to call speculatively/repeatedly.
func (e *Emitter) genericClassMangledFields(decl *ast.ClassDeclaration, subs map[string]Type) (string, []Field, error) {
	suffix, err := mangleTypeArgs(decl.TypeParams, subs)
	if err != nil {
		return "", nil, fmt.Errorf("%d:%d: generic class '%s': %s", decl.GetPos().Line, decl.GetPos().Col, decl.Name, err)
	}
	mangled := decl.Name + "__" + suffix
	var ownFields []Field
	for _, f := range decl.Fields {
		if f.Name == ClassTagField || f.Name == ClassVTableField {
			return "", nil, fmt.Errorf("%d:%d: class '%s' cannot declare a field named '%s' — reserved for the compiler's internal runtime state", decl.GetPos().Line, decl.GetPos().Col, decl.Name, f.Name)
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
			fty = e.inferExprType(f.Initializer)
		}
		ownFields = append(ownFields, Field{Name: f.Name, Ty: fty})
	}
	return mangled, ownFields, nil
}

// genericClassInstanceType is the pure sibling instantiateGenericClass's own
// real (memoized, body-emitting) path delegates to: it returns the Type a
// `new ClassName<...>(...)` expression evaluates to, without registering
// anything into e.classes or emitting any IR — safe to call from
// inferExprType and emitVarDecl's own pre-inference type lookup, both of
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
	return t, nil
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

	for _, m := range decl.Methods {
		key := genericMethodKey(m)
		if _, dup := info.MethodSigs[key]; dup {
			return "", fmt.Errorf("%d:%d: class '%s' declares more than one method named '%s'", m.GetPos().Line, m.GetPos().Col, decl.Name, m.Name)
		}
		sig := e.buildGenericParamSig(m.Params, subs)
		if m.AccessorKind == "set" {
			sig.RetType = TypeVoid // a setter's result is discarded
		} else if m.ReturnType != nil {
			sig.RetType = e.substituteGenericType(m.ReturnType, subs)
		} else {
			sig.RetType = TypeVoid
			e.pushScope()
			e.define("this", Symbol{Ty: ty})
			if inferred, ok := e.inferUnannotatedReturnType(m.Body, sig.ParamNames, sig.ParamTypes); ok {
				sig.RetType = inferred
			}
			e.popScope()
		}
		info.MethodImplementor[key] = mangled
		info.MethodSigs[key] = sig
		info.Methods[key] = m
		info.MethodOrder = append(info.MethodOrder, key)
	}

	// Register before emitting bodies — guards a method/constructor that
	// constructs another instance of the same instantiation recursively,
	// the same way instantiateGenericFunc registers its signature first.
	e.classes[mangled] = info
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
		// TDD-00063 Stage 2: generator methods aren't wired through codegen
		// yet — clean rejection rather than a silently-wrong plain method.
		if m.IsGenerator {
			return fmt.Errorf("%d:%d: generator method '%s' on class '%s' is not yet supported", m.GetPos().Line, m.GetPos().Col, m.Name, decl.Name)
		}
		key := genericMethodKey(m)
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
func (e *Emitter) explicitGenericSubs(decl *ast.FunctionDeclaration, typeArgs []*ast.TypeAnnotation) (map[string]Type, bool) {
	if len(typeArgs) == 0 || len(typeArgs) < len(decl.TypeParams) {
		return nil, false
	}
	subs := make(map[string]Type, len(decl.TypeParams))
	for i, tp := range decl.TypeParams {
		subs[tp] = e.resolveType(typeArgs[i])
	}
	return subs, true
}

// classTypeArgs is the type argument list a `new C<…>(…)` of the generic
// class decl instantiates it with: the explicit ones, then each omitted
// parameter's default (`class C<T = any>`). ok is false when an omitted
// parameter has no default.
func classTypeArgs(decl *ast.ClassDeclaration, explicit []*ast.TypeAnnotation) (args []*ast.TypeAnnotation, ok bool) {
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
func genericArgsOK(decl *ast.ClassDeclaration, explicit []*ast.TypeAnnotation) bool {
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

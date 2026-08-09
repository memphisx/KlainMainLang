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
	if t.IsObject || t.IsMap || t.IsSet || t.IsPromise || t.IsFunc || t.IsDynamicObject || t.IsGroupMap || t.IsClass || t.IsDynamic {
		return "", fmt.Errorf("type argument is not supported in V1 (only number, string, boolean, and arrays of these — see docs/tdd/TDD-00010.md)")
	}
	if isNumberTy(t) {
		return "num", nil
	}
	if isStringTy(t) {
		return "str", nil
	}
	return "", fmt.Errorf("type argument is not supported in V1 (only number, string, boolean, and arrays of these — see docs/tdd/TDD-00010.md)")
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
		return concrete
	}
	for typeParam, concrete := range subs {
		if ta.Name == typeParam+"[]" {
			return ArrayOf(concrete)
		}
	}
	return e.resolveType(ta)
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
		sig.ParamTypes = append(sig.ParamTypes, pty)
		sig.ParamNames = append(sig.ParamNames, p.Name)
		sig.Defaults = append(sig.Defaults, p.Default)
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
}

// genericFuncTypeParamIndex finds, independently for each of decl's type
// parameters, the first parameter position whose declared type is that type
// parameter (bare or one-level array) — the position a call site's argument
// type is inferred from. A type parameter absent from the result map has no
// inferable position, which TDD-00010 V1 (and TDD-00037's N-ary extension)
// treats as uninstantiable: with no call-site type-argument syntax (see the
// TDD's Design section on the `a<b>(c)` grammar ambiguity), a generic
// function that never mentions a type parameter in a parameter position has
// nothing to infer it from. No two type parameters can ever compete for the
// same position, since a parameter's type annotation names exactly one
// type-parameter name.
func genericFuncTypeParamIndex(decl *ast.FunctionDeclaration) map[string]genericParamPos {
	positions := make(map[string]genericParamPos, len(decl.TypeParams))
	for _, typeParam := range decl.TypeParams {
		for i, p := range decl.Params {
			if p.Type == nil {
				continue
			}
			if p.Type.Name == typeParam {
				positions[typeParam] = genericParamPos{Idx: i, IsArray: false}
				break
			}
			if p.Type.Name == typeParam+"[]" {
				positions[typeParam] = genericParamPos{Idx: i, IsArray: true}
				break
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
	for _, typeParam := range decl.TypeParams {
		pos, found := positions[typeParam]
		if !found || pos.Idx >= len(args) {
			return nil, typeParam, false
		}
		// inferExprType has no *ast.ArrayLiteral case of its own (see
		// inferArrayType's separate existing callers, e.g.
		// emit_exprs_vardecl.go) — a literal array argument needs that
		// instead, the same per-case dispatch used everywhere else an array
		// literal's type is needed ahead of emission.
		var concrete Type
		if lit, ok := args[pos.Idx].(*ast.ArrayLiteral); ok {
			concrete = e.inferArrayType(lit)
		} else {
			concrete = e.inferExprType(args[pos.Idx])
		}
		if pos.IsArray {
			if !concrete.IsArray || concrete.ElemType == nil {
				return nil, typeParam, false
			}
			concrete = *concrete.ElemType
		}
		subs[typeParam] = concrete
	}
	return subs, "", true
}

// emitGenericFuncCall is emit_call.go's dispatch target for a call to a
// registered generic function name: infer every type argument via
// inferGenericCallConcreteTypes, instantiate (or reuse a memoized prior
// instantiation), and dispatch exactly like a call to a concrete named
// function.
func (e *Emitter) emitGenericFuncCall(decl *ast.FunctionDeclaration, args []ast.Expression, pos ast.Pos) (Value, error) {
	subs, missing, ok := e.inferGenericCallConcreteTypes(decl, args)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: cannot infer type argument '%s' for generic function '%s' — declare a parameter typed '%s' or '%s[]' to infer from (explicit call-site type arguments aren't supported yet, see docs/tdd/TDD-00010.md)", pos.Line, pos.Col, missing, decl.Name, missing, missing)
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
func (e *Emitter) genericCallReturnType(decl *ast.FunctionDeclaration, args []ast.Expression) (Type, bool) {
	subs, _, ok := e.inferGenericCallConcreteTypes(decl, args)
	if !ok {
		return Type{}, false
	}
	if _, err := mangleTypeArgs(decl.TypeParams, subs); err != nil {
		return Type{}, false
	}
	if decl.ReturnType != nil {
		return e.substituteGenericType(decl.ReturnType, subs), true
	}
	sig := e.buildGenericParamSig(decl.Params, subs)
	paramNames := make([]string, len(decl.Params))
	for i, p := range decl.Params {
		paramNames[i] = p.Name
	}
	return e.inferUnannotatedReturnType(decl.Body, paramNames, sig.ParamTypes)
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

	sig := e.buildGenericParamSig(decl.Params, subs)
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
	e.funcs[mangled] = sig
	if err := e.emitFunctionDeclAs(decl, mangled, sig); err != nil {
		delete(e.funcs, mangled)
		return "", FuncSig{}, err
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
	fields := make([]Field, len(decl.Fields))
	for i, f := range decl.Fields {
		fields[i] = Field{Name: f.Name, Ty: e.substituteGenericType(f.Type, subs)}
	}
	return ObjectType(fields)
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
		if f.Name == ClassTagField || f.Name == ClassVTableField || f.Name == ClassEventEmitterField {
			return "", nil, fmt.Errorf("%d:%d: class '%s' cannot declare a field named '%s' — reserved for the compiler's internal runtime state", decl.GetPos().Line, decl.GetPos().Col, decl.Name, f.Name)
		}
		ownFields = append(ownFields, Field{Name: f.Name, Ty: e.substituteGenericType(f.Type, subs)})
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
func (e *Emitter) genericClassInstanceType(decl *ast.ClassDeclaration, subs map[string]Type) (Type, error) {
	mangled, ownFields, err := e.genericClassMangledFields(decl, subs)
	if err != nil {
		return Type{}, err
	}
	return ClassType(mangled, nil, ownFields, false, false), nil
}

// instantiateGenericClass builds and emits (on first use) a full,
// independent ClassInfo for decl specialized at subs, and returns its
// mangled name. Memoized via e.classes. Scoped-down relative to a plain
// class (registerClasses/emitClassDecl): no inheritance, vtable, static
// members, or EventEmitter mixin — registerClasses already rejects those on
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
	ty := ClassType(mangled, nil, ownFields, false, false)
	e.interfaces[mangled] = ty

	info := ClassInfo{
		Ty:                        ty,
		InheritedFields:           nil,
		OwnFields:                 ownFields,
		FlatFields:                ownFields,
		Methods:                   make(map[string]*ast.FunctionDeclaration),
		MethodSigs:                make(map[string]FuncSig),
		MethodImplementor:         make(map[string]string),
		MethodDispatchSlot:        make(map[string]*MethodSlot),
		TagID:                     e.nextClassTagID,
		RootClass:                 mangled,
		FieldOrigin:               make(map[string]string),
		OwnFieldVisibility:        make(map[string]string),
		OwnMethodVisibility:       make(map[string]string),
		StaticFieldTypes:          make(map[string]Type),
		OwnStaticFieldTypes:       make(map[string]Type),
		StaticFieldOwner:          make(map[string]string),
		OwnStaticFieldVisibility:  make(map[string]string),
		StaticMethodSigs:          make(map[string]FuncSig),
		StaticMethodImplementor:   make(map[string]string),
		OwnStaticMethodVisibility: make(map[string]string),
	}
	e.nextClassTagID++
	for _, f := range decl.Fields {
		info.FieldOrigin[f.Name] = mangled
		info.OwnFieldVisibility[f.Name] = f.Visibility
	}

	if decl.Constructor != nil {
		sig := e.buildGenericParamSig(decl.Constructor.Params, subs)
		sig.RetType = TypeVoid
		info.Constructor = decl.Constructor
		info.CtorSig = sig
	} else if len(ownFields) > 0 {
		return "", fmt.Errorf("%d:%d: class '%s' has fields but no constructor to initialize them", decl.GetPos().Line, decl.GetPos().Col, decl.Name)
	}

	for _, m := range decl.Methods {
		if _, dup := info.MethodSigs[m.Name]; dup {
			return "", fmt.Errorf("%d:%d: class '%s' declares more than one method named '%s'", m.GetPos().Line, m.GetPos().Col, decl.Name, m.Name)
		}
		sig := e.buildGenericParamSig(m.Params, subs)
		if m.ReturnType != nil {
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
		info.MethodImplementor[m.Name] = mangled
		info.MethodSigs[m.Name] = sig
		info.Methods[m.Name] = m
		info.MethodOrder = append(info.MethodOrder, m.Name)
		info.OwnMethodVisibility[m.Name] = m.Visibility
	}

	// Register before emitting bodies — guards a method/constructor that
	// constructs another instance of the same instantiation recursively,
	// the same way instantiateGenericFunc registers its signature first.
	e.classes[mangled] = info
	if err := e.emitClassDeclAs(decl, mangled, info); err != nil {
		delete(e.classes, mangled)
		return "", err
	}
	return mangled, nil
}

// emitClassDeclAs is emitClassDecl's generic-instantiation sibling: the same
// "@llvmName_constructor" / "@llvmName_methodName" emission shape
// (emitClassMember does the actual work, already fully parameterized by
// name/type/sig — no changes needed there), but reading info directly
// instead of e.classes[decl.Name] and naming every emitted symbol off
// llvmName (the mangled instantiation name) instead of decl.Name. No static
// members here — registerClasses already rejects those on any generic class
// declaration before it's ever registered into e.genericClasses.
func (e *Emitter) emitClassDeclAs(decl *ast.ClassDeclaration, llvmName string, info ClassInfo) error {
	if info.Constructor != nil {
		ctorName := llvmName + "_constructor"
		if err := e.emitClassMember(ctorName, info.Ty, info.Constructor.Params, info.CtorSig, info.Constructor.Body, TypeVoid, info.Constructor.GetPos(), false); err != nil {
			return err
		}
	}
	for _, m := range decl.Methods {
		sig := info.MethodSigs[m.Name]
		memberName := llvmName + "_" + m.Name
		if err := e.emitClassMember(memberName, info.Ty, m.Params, sig, m.Body, sig.RetType, m.GetPos(), false); err != nil {
			return err
		}
	}
	return nil
}

// emit_generics.go — TDD-00010 V1: user-defined generics (monomorphization)
// for functions, interfaces, and classes. On-demand (lazy) specialization
// during normal codegen emission, not a separate up-front collection pass —
// see the TDD's Design section for why this is a simpler, equally-correct
// realization of the same idea, made possible by whole-program compilation
// (resolver.ResolveProgram) and LLVM IR text's tolerance for out-of-order
// function definitions.
//
// V1 scope, uniformly across functions/interfaces/classes: exactly one,
// unconstrained type parameter. Accepted concrete types for a *function*
// instantiation are number/string/boolean and arrays of these (see
// mangleTypeArg) — a generic interface's field types and a generic class's
// field/param/return types can substitute any of those the same way.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
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

// substituteGenericType resolves a declaration-site type annotation that may
// reference typeParam (a bare "T", or "T[]" — V1 doesn't support nesting a
// type parameter any deeper than one array level) into a concrete Type,
// substituting concrete for every occurrence; anything else resolves
// normally via resolveType, exactly as it would for a non-generic
// declaration.
func (e *Emitter) substituteGenericType(ta *ast.TypeAnnotation, typeParam string, concrete Type) Type {
	if ta == nil {
		return TypeVoid
	}
	if ta.Name == typeParam {
		return concrete
	}
	if ta.Name == typeParam+"[]" {
		return ArrayOf(concrete)
	}
	return e.resolveType(ta)
}

// buildGenericParamSig is buildParamSig's generic-aware sibling: the same
// per-parameter rules (explicit annotation via resolveType, unannotated rest
// defaults to number[], unannotated scalar defaults to inferred TypeI64),
// except a parameter whose annotation names typeParam substitutes concrete
// instead.
func (e *Emitter) buildGenericParamSig(params []ast.Param, typeParam string, concrete Type) FuncSig {
	var sig FuncSig
	for _, p := range params {
		var pty Type
		if p.Type != nil {
			pty = e.substituteGenericType(p.Type, typeParam, concrete)
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

// genericFuncTypeParamIndex finds the parameter position whose declared type
// is decl's own type parameter — either bare ("T") or a one-level array of
// it ("T[]", isArray=true) — the position a call site's argument type is
// inferred from. Returns idx=-1 if there is none, which TDD-00010 V1 treats
// as uninstantiable: with no call-site type-argument syntax (see the TDD's
// Design section on the `a<b>(c)` grammar ambiguity), a generic function
// that never mentions its own type parameter in a parameter position has
// nothing to infer T from.
func genericFuncTypeParamIndex(decl *ast.FunctionDeclaration) (idx int, isArray bool) {
	typeParam := decl.TypeParams[0]
	for i, p := range decl.Params {
		if p.Type == nil {
			continue
		}
		if p.Type.Name == typeParam {
			return i, false
		}
		if p.Type.Name == typeParam+"[]" {
			return i, true
		}
	}
	return -1, false
}

// inferGenericCallConcreteType is the pure (no IR emission) core shared by
// emitGenericFuncCall and inferExprType's own generic-call case: finds
// decl's type-parameter-typed argument at the call site and infers its
// concrete type, or ok=false if there's nothing to infer from. Safe to call
// speculatively/repeatedly — inferExprType (unlike emitExpr) must never
// trigger real emission as a side effect of merely asking "what type is
// this call."
func (e *Emitter) inferGenericCallConcreteType(decl *ast.FunctionDeclaration, args []ast.Expression) (Type, bool) {
	idx, isArray := genericFuncTypeParamIndex(decl)
	if idx == -1 || idx >= len(args) {
		return Type{}, false
	}
	// inferExprType has no *ast.ArrayLiteral case of its own (see
	// inferArrayType's separate existing callers, e.g.
	// emit_exprs_vardecl.go) — a literal array argument needs that instead,
	// the same per-case dispatch used everywhere else an array literal's
	// type is needed ahead of emission.
	var concrete Type
	if lit, ok := args[idx].(*ast.ArrayLiteral); ok {
		concrete = e.inferArrayType(lit)
	} else {
		concrete = e.inferExprType(args[idx])
	}
	if isArray {
		if !concrete.IsArray || concrete.ElemType == nil {
			return Type{}, false
		}
		concrete = *concrete.ElemType
	}
	return concrete, true
}

// emitGenericFuncCall is emit_call.go's dispatch target for a call to a
// registered generic function name: infer the type argument via
// inferGenericCallConcreteType, instantiate (or reuse a memoized prior
// instantiation), and dispatch exactly like a call to a concrete named
// function.
func (e *Emitter) emitGenericFuncCall(decl *ast.FunctionDeclaration, args []ast.Expression, pos ast.Pos) (Value, error) {
	concrete, ok := e.inferGenericCallConcreteType(decl, args)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: cannot infer a type argument for generic function '%s' — declare at least one parameter typed '%s' or '%s[]' to infer from (explicit call-site type arguments aren't supported yet, see docs/tdd/TDD-00010.md)", pos.Line, pos.Col, decl.Name, decl.TypeParams[0], decl.TypeParams[0])
	}
	mangled, sig, err := e.instantiateGenericFunc(decl, concrete)
	if err != nil {
		return Value{}, err
	}
	return e.emitCallToFuncSig(mangled, sig, args, pos)
}

// genericCallReturnType is inferExprType's pure helper for a call to a
// registered generic function: infers the concrete type the same way
// emitGenericFuncCall would, then substitutes it into decl's return-type
// annotation — or, for an unannotated return type, best-effort-infers it
// from the body against the substituted parameter types (the same
// inferUnannotatedReturnType path instantiateGenericFunc itself uses, which
// is already documented as safe to call without a real function existing
// yet). Returns ok=false wherever emitGenericFuncCall would itself error
// (nothing to infer from, or an unsupported concrete type) — inferExprType
// has no error return, so callers just fall through to its own final
// default the same way every other unresolvable case here already does.
func (e *Emitter) genericCallReturnType(decl *ast.FunctionDeclaration, args []ast.Expression) (Type, bool) {
	concrete, ok := e.inferGenericCallConcreteType(decl, args)
	if !ok {
		return Type{}, false
	}
	if _, err := mangleTypeArg(concrete); err != nil {
		return Type{}, false
	}
	typeParam := decl.TypeParams[0]
	if decl.ReturnType != nil {
		return e.substituteGenericType(decl.ReturnType, typeParam, concrete), true
	}
	sig := e.buildGenericParamSig(decl.Params, typeParam, concrete)
	paramNames := make([]string, len(decl.Params))
	for i, p := range decl.Params {
		paramNames[i] = p.Name
	}
	return e.inferUnannotatedReturnType(decl.Body, paramNames, sig.ParamTypes)
}

// instantiateGenericFunc returns the mangled LLVM name and signature for
// decl specialized at concrete, building and emitting it on first use.
// Memoized via e.funcs: a repeated instantiation (e.g. the same generic
// function called twice with the same inferred type) is emitted once.
func (e *Emitter) instantiateGenericFunc(decl *ast.FunctionDeclaration, concrete Type) (string, FuncSig, error) {
	suffix, err := mangleTypeArg(concrete)
	if err != nil {
		return "", FuncSig{}, fmt.Errorf("%d:%d: generic function '%s': %s", decl.GetPos().Line, decl.GetPos().Col, decl.Name, err)
	}
	mangled := decl.Name + "__" + suffix
	if sig, ok := e.funcs[mangled]; ok {
		return mangled, sig, nil
	}

	typeParam := decl.TypeParams[0]
	sig := e.buildGenericParamSig(decl.Params, typeParam, concrete)
	if decl.ReturnType != nil {
		sig.RetType = e.substituteGenericType(decl.ReturnType, typeParam, concrete)
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
// fields with concrete substituted for decl's own type parameter — called
// from resolveType whenever a `Box<number>`-shaped type annotation names a
// registered generic interface. Not memoized, matching this codebase's
// existing convention for ArrayOf/MapType/SetType/PromiseOf (types.go) —
// each call builds a fresh, structurally-equal Type value.
func (e *Emitter) instantiateGenericInterface(decl *ast.InterfaceDeclaration, concrete Type) Type {
	typeParam := decl.TypeParams[0]
	fields := make([]Field, len(decl.Fields))
	for i, f := range decl.Fields {
		fields[i] = Field{Name: f.Name, Ty: e.substituteGenericType(f.Type, typeParam, concrete)}
	}
	return ObjectType(fields)
}

// genericClassMangledFields is the pure (no IR emission, no e.classes/
// e.interfaces registration) core shared by instantiateGenericClass and
// genericClassInstanceType: the mangled name and field substitution are
// exactly the same work either way, only what's done with the result
// differs. Safe to call speculatively/repeatedly.
func (e *Emitter) genericClassMangledFields(decl *ast.ClassDeclaration, concrete Type) (string, []Field, error) {
	suffix, err := mangleTypeArg(concrete)
	if err != nil {
		return "", nil, fmt.Errorf("%d:%d: generic class '%s': %s", decl.GetPos().Line, decl.GetPos().Col, decl.Name, err)
	}
	mangled := decl.Name + "__" + suffix
	typeParam := decl.TypeParams[0]
	var ownFields []Field
	for _, f := range decl.Fields {
		if f.Name == ClassTagField || f.Name == ClassVTableField || f.Name == ClassEventEmitterField {
			return "", nil, fmt.Errorf("%d:%d: class '%s' cannot declare a field named '%s' — reserved for the compiler's internal runtime state", decl.GetPos().Line, decl.GetPos().Col, decl.Name, f.Name)
		}
		ownFields = append(ownFields, Field{Name: f.Name, Ty: e.substituteGenericType(f.Type, typeParam, concrete)})
	}
	return mangled, ownFields, nil
}

// genericClassInstanceType is the pure sibling instantiateGenericClass's own
// real (memoized, body-emitting) path delegates to: it returns the Type a
// `new ClassName<T>(...)` expression evaluates to, without registering
// anything into e.classes or emitting any IR — safe to call from
// inferExprType and emitVarDecl's own pre-inference type lookup, both of
// which must never trigger real emission as a side effect of merely asking
// "what type is this expression."  The real emission still only ever
// happens once, from emitNewExpression's own call to
// instantiateGenericClass at the actual construction site.
func (e *Emitter) genericClassInstanceType(decl *ast.ClassDeclaration, concrete Type) (Type, error) {
	mangled, ownFields, err := e.genericClassMangledFields(decl, concrete)
	if err != nil {
		return Type{}, err
	}
	return ClassType(mangled, nil, ownFields, false, false), nil
}

// instantiateGenericClass builds and emits (on first use) a full,
// independent ClassInfo for decl specialized at concrete, and returns its
// mangled name. Memoized via e.classes. Scoped-down relative to a plain
// class (registerClasses/emitClassDecl): no inheritance, vtable, static
// members, or EventEmitter mixin — registerClasses already rejects those on
// any generic class declaration before it ever reaches here (see its own
// validation), so this only ever needs to handle fields + constructor +
// instance methods.
func (e *Emitter) instantiateGenericClass(decl *ast.ClassDeclaration, concrete Type) (string, error) {
	mangled, ownFields, err := e.genericClassMangledFields(decl, concrete)
	if err != nil {
		return "", err
	}
	if _, ok := e.classes[mangled]; ok {
		return mangled, nil
	}
	typeParam := decl.TypeParams[0]
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
		sig := e.buildGenericParamSig(decl.Constructor.Params, typeParam, concrete)
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
		sig := e.buildGenericParamSig(m.Params, typeParam, concrete)
		if m.ReturnType != nil {
			sig.RetType = e.substituteGenericType(m.ReturnType, typeParam, concrete)
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

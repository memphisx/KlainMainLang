package llvm

import (
	"reflect"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
)

// ambientInterfaceType is the type an interface or type alias the builtin
// declarations (lib/*.d.ts) declare at top level, registered on first use
// as a program interface is: a parameter typed `RequestInit` holds the
// object passed, read through the checked view (TDD-00233).
func (e *Emitter) ambientInterfaceType(name string) (Type, bool) {
	if ty, ok := e.interfaces[name]; ok {
		return ty, true
	}
	if e.ambientMiss[name] {
		return Type{}, false
	}
	if alias := ambientAliasDecl(name); alias != nil && len(alias.TypeParams) == 0 && alias.Type != nil {
		// A type alias (`type BodyInit = …`): what it names.
		ph := ObjectType(nil)
		ph.RefName = name
		e.interfaces[name] = ph
		ty := e.resolveType(alias.Type)
		// An alias this compiler has no representation for (BodyInit's
		// union with `AsyncIterable<Uint8Array>`) resolves to the unknown-
		// name default; it is a boxed value, read by its run-time kind.
		if reflect.DeepEqual(ty, TypeI64) ||
			(len(alias.Type.UnionMembers) > 0 && e.unionHasUnknownMember(alias.Type)) {
			ty = TypeAny
		}
		e.interfaces[name] = ty
		return ty, true
	}
	decl := ambientInterfaceDecl(name)
	if decl == nil || decl.IndexSig != nil || decl.CallSig != nil || len(decl.TypeParams) > 0 {
		if e.ambientMiss == nil {
			e.ambientMiss = map[string]bool{}
		}
		e.ambientMiss[name] = true
		return Type{}, false
	}
	// A placeholder breaks a self-reference while the members resolve.
	ph := ObjectType(nil)
	ph.RefName = name
	e.interfaces[name] = ph
	fields := make([]Field, 0, len(decl.Fields))
	for _, f := range decl.Fields {
		fty := e.resolveType(f.Type)
		if f.Optional {
			fty = optionalFieldType(fty)
		}
		fields = append(fields, Field{Name: f.Name, Ty: fty, Optional: f.Optional})
	}
	fields = e.withMethodFields(fields, decl.Methods, e.resolveType)
	ty := ObjectType(fields)
	ty.RefName = name
	e.interfaces[name] = ty
	return ty, true
}

// ambientInterfaceDecl finds a top-level interface named name in the
// builtin declarations (the first, when several merge).
func ambientInterfaceDecl(name string) *ast.InterfaceDeclaration {
	progs, err := lib.Programs()
	if err != nil {
		return nil
	}
	for _, p := range progs {
		for _, st := range p.Body {
			if d, ok := st.(*ast.InterfaceDeclaration); ok && d.Name == name {
				return d
			}
		}
	}
	return nil
}

// ambientAliasDecl finds a top-level type alias named name in the builtin
// declarations.
func ambientAliasDecl(name string) *ast.TypeAliasDeclaration {
	progs, err := lib.Programs()
	if err != nil {
		return nil
	}
	for _, p := range progs {
		for _, st := range p.Body {
			if d, ok := st.(*ast.TypeAliasDeclaration); ok && d.Name == name {
				return d
			}
		}
	}
	return nil
}

// unionHasUnknownMember reports a union annotation with a member naming a
// type that resolves to nothing (the unknown-name `number` default).
func (e *Emitter) unionHasUnknownMember(ta *ast.TypeAnnotation) bool {
	for _, m := range ta.UnionMembers {
		if m == nil || m.Name == "" || m.IsStringLiteral || m.ElemType != nil || m.IsFuncType {
			continue
		}
		switch m.Name {
		case "number", "bigint", "null", "undefined", "string", "boolean", "any", "unknown", "never", "void", "object", "symbol":
			continue
		}
		if len(m.TypeArgs) > 0 || len(m.Qualifier) > 0 {
			return true
		}
		if t := e.resolveType(m); reflect.DeepEqual(t, TypeI64) {
			return true
		}
	}
	return false
}

// registerAmbientGeneric makes a generic type alias the builtin declarations
// declare (`IteratorResult<T, TReturn>`), or a generic interface of theirs
// that is only a data shape (fields, no methods, index or call signatures:
// `IteratorYieldResult<T>`), instantiable the way a program's own generic is.
// A program's own declaration of the name wins.
func (e *Emitter) registerAmbientGeneric(name string) {
	if name == "" || e.userTypeName(name) || utilityTypeNames[name] {
		return // a utility type codegen evaluates itself (resolveUtilityType)
	}
	if _, ok := e.genericTypeAliases[name]; ok {
		return
	}
	if _, ok := e.genericInterfaces[name]; ok {
		return
	}
	if e.ambientGenericMiss[name] {
		return
	}
	if alias := ambientAliasDecl(name); alias != nil && len(alias.TypeParams) > 0 && alias.Type != nil {
		if n := alias.Type.TypeNode(); n != nil && len(alias.Type.UnionMembers) == 0 && alias.Type.Name == "" {
			if conv, err := ast.TypeAnnotationOf(n, "ts"); err == nil {
				cp := *alias
				cp.Type = conv
				alias = &cp
			}
		}
		e.genericTypeAliases[name] = alias
		// Its union's members are looked up by name before they resolve.
		for _, m := range alias.Type.UnionMembers {
			if m != nil && len(m.TypeArgs) > 0 {
				e.registerAmbientGeneric(m.Name)
			}
		}
		return
	}
	if decl := ambientInterfaceDecl(name); decl != nil && len(decl.TypeParams) > 0 &&
		decl.IndexSig == nil && decl.CallSig == nil && len(decl.Methods) == 0 && len(decl.Extends) == 0 && len(decl.Fields) > 0 {
		e.genericInterfaces[name] = decl
		return
	}
	if e.ambientGenericMiss == nil {
		e.ambientGenericMiss = map[string]bool{}
	}
	e.ambientGenericMiss[name] = true
}

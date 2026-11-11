package llvm

import (
	"reflect"

	"KlainMainLang/ast"
)

// eraseTypeOnlyGenerics drops the type parameters of every class that uses
// none of them in its own members (`class EventEmitter<T = any>` in
// lib/node/events.ts, whose T only types what a checker sees): codegen
// compiles it as an ordinary class, so it can be extended and constructed
// like one, its type arguments at a use site (`extends EventEmitter<Events>`,
// `new EventEmitter<Events>()`) erased as TypeScript erases them.
func (e *Emitter) eraseTypeOnlyGenerics(prog *ast.Program) {
	for _, stmt := range prog.Body {
		cd, ok := stmt.(*ast.ClassDeclaration)
		if !ok || len(cd.TypeParams) == 0 {
			continue
		}
		params := map[string]bool{}
		for _, p := range cd.TypeParams {
			params[p] = true
		}
		used := false
		var walk func(v reflect.Value)
		walk = func(v reflect.Value) {
			if used || !v.IsValid() {
				return
			}
			switch v.Kind() {
			case reflect.Ptr, reflect.Interface:
				if v.IsNil() {
					return
				}
				if ta, ok := v.Interface().(ast.TypeNode); ok && ta != nil {
					used = namesTypeParam(ta, params)
					return
				}
				if fd, ok := v.Interface().(*ast.FunctionDeclaration); ok && fd != nil && fd.IsOverloadSig {
					return // a signature only the checker reads
				}
				walk(v.Elem())
			case reflect.Struct:
				for i := 0; i < v.NumField(); i++ {
					if v.Type().Field(i).IsExported() {
						walk(v.Field(i))
					}
				}
			case reflect.Slice, reflect.Array:
				for i := 0; i < v.Len(); i++ {
					walk(v.Index(i))
				}
			}
		}
		// The class's own type parameter list is not a use.
		saved, savedC, savedD := cd.TypeParams, cd.TypeParamConstraints, cd.TypeParamDefaults
		cd.TypeParams, cd.TypeParamConstraints, cd.TypeParamDefaults = nil, nil, nil
		walk(reflect.ValueOf(cd))
		if used {
			cd.TypeParams, cd.TypeParamConstraints, cd.TypeParamDefaults = saved, savedC, savedD
			continue
		}
		if e.typeOnlyGenerics == nil {
			e.typeOnlyGenerics = map[string]bool{}
		}
		e.typeOnlyGenerics[cd.Name] = true
		cd.Declared = &ast.ClassTypeParams{Names: saved, Constraints: savedC, Defaults: savedD}
	}
}

// eraseInheritingGenerics compiles a generic class that takes part in
// inheritance (it extends a class, another class extends it, or it is
// abstract) erased: each of its type parameters reads as any in its members
// (`T` and `T[]` become `any` and `any[]`), and it is registered as an
// ordinary class, its type arguments at use sites ignored as for a type-only
// generic. Instantiation per type-argument set does not compose with one
// class object shared by base and derived classes; boxing the type
// parameter's values keeps JavaScript's behaviour at a speed cost.
func (e *Emitter) eraseInheritingGenerics(prog *ast.Program) {
	extended := map[string]bool{}
	for _, stmt := range prog.Body {
		if cd, ok := stmt.(*ast.ClassDeclaration); ok && cd.BaseClass != "" {
			extended[cd.BaseClass] = true
		}
	}
	for _, stmt := range prog.Body {
		cd, ok := stmt.(*ast.ClassDeclaration)
		if !ok || len(cd.TypeParams) == 0 || e.typeOnlyGenerics[cd.Name] {
			continue
		}
		if cd.BaseClass == "" && !cd.IsAbstract && !extended[cd.Name] {
			continue
		}
		params := map[string]bool{}
		for _, p := range cd.TypeParams {
			params[p] = true
		}
		declared := cd.DeclaredTypeParams()
		cd.Declared = &declared
		cd.TypeParams, cd.TypeParamConstraints, cd.TypeParamDefaults = nil, nil, nil
		if e.typeOnlyGenerics == nil {
			e.typeOnlyGenerics = map[string]bool{}
		}
		e.typeOnlyGenerics[cd.Name] = true
	}
}

// namesTypeParam reports whether the type node n names one of params.
func namesTypeParam(n ast.TypeNode, params map[string]bool) bool {
	found := false
	if n != nil {
		ast.Inspect(n, func(x ast.Node) bool {
			if r, ok := x.(*ast.TypeReference); ok && len(r.Qualifier) == 0 && params[r.Name] {
				found = true
			}
			return !found
		})
	}
	return found
}

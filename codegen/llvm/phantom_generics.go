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
				if ta, ok := v.Interface().(*ast.TypeAnnotation); ok && ta != nil && len(ta.Qualifier) == 0 && params[ta.Name] {
					used = true
					return
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
	}
}

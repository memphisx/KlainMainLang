package llvm

import (
	"reflect"
	"strconv"

	"KlainMainLang/ast"
)

// local_types.go — interfaces and type aliases declared inside a function
// or block. Codegen resolves a type annotation by name against the
// program's top-level types, so each local one is renamed to a name of its
// own (`Local__kml_lt3`), every annotation in its block that names it is
// rewritten, and the declaration moves to the top level, where it registers
// as any other. Inner blocks are handled first, so a name an inner block
// redeclares keeps the inner declaration. Run after checking: the checker
// read the original names.

// hoistLocalTypes lifts prog's local type declarations to its top level.
func hoistLocalTypes(prog *ast.Program) {
	n := 0
	var hoisted []ast.Statement
	var visit func(v reflect.Value, seen map[uintptr]bool)
	visit = func(v reflect.Value, seen map[uintptr]bool) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if b, ok := v.Interface().(*ast.BlockStatement); ok {
				for _, st := range b.Body {
					visit(reflect.ValueOf(st), seen)
				}
				hoistBlockTypes(b, &n, &hoisted)
				return
			}
			visit(v.Elem(), seen)
		case reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem(), seen)
			}
		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					visit(v.Field(i), seen)
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i), seen)
			}
		}
	}
	seen := map[uintptr]bool{}
	for _, st := range prog.Body {
		visit(reflect.ValueOf(st), seen)
	}
	if len(hoisted) > 0 {
		prog.Body = append(hoisted, prog.Body...)
	}
}

// hoistBlockTypes renames and removes b's own type declarations (the
// checker resolves the annotations naming them by scope).
func hoistBlockTypes(b *ast.BlockStatement, n *int, hoisted *[]ast.Statement) {
	renames := map[string]string{}
	kept := b.Body[:0:0]
	for _, st := range b.Body {
		switch d := st.(type) {
		case *ast.InterfaceDeclaration:
			*n++
			nu := d.Name + "__kml_lt" + strconv.Itoa(*n)
			if prev, ok := renames[d.Name]; ok {
				nu = prev // merged declarations share one name
			}
			renames[d.Name] = nu
			d.Name = nu
			*hoisted = append(*hoisted, d)
			continue
		case *ast.TypeAliasDeclaration:
			*n++
			nu := d.Name + "__kml_lt" + strconv.Itoa(*n)
			renames[d.Name] = nu
			d.Name = nu
			*hoisted = append(*hoisted, d)
			continue
		}
		kept = append(kept, st)
	}
	if len(renames) > 0 {
		b.Body = kept
	}
}

// normalizeRestParams gives every rest parameter whose declared type is
// not an array (a tuple `...a: [number, string]`, `never`, a type
// parameter, a conditional alias like `Args<K, T>`) an `any[]` slot: its
// arguments are collected boxed, as JavaScript holds them, and read
// dynamically. The checker still reads the declared type.
func (e *Emitter) normalizeRestParams(prog *ast.Program) {
	var visit func(v reflect.Value, seen map[uintptr]bool)
	visit = func(v reflect.Value, seen map[uintptr]bool) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			visit(v.Elem(), seen)
		case reflect.Interface:
			if !v.IsNil() {
				visit(v.Elem(), seen)
			}
		case reflect.Struct:
			if p, ok := v.Addr().Interface().(*ast.Param); ok && v.CanAddr() {
				if p.Rest && p.Type != nil && !e.restAnnotationIsArray(p.Type) {
					if e.anyArrayStorage == nil {
						e.anyArrayStorage = map[ast.TypeNode]bool{}
					}
					e.anyArrayStorage[p.Type] = true
				}
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					visit(v.Field(i), seen)
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				visit(v.Index(i), seen)
			}
		}
	}
	visit(reflect.ValueOf(prog), map[uintptr]bool{})
}

// restAnnotationIsArray reports whether a rest parameter's annotation is an
// array type, spelled (`T[]`, `Array<T>`) or through an alias.
func (e *Emitter) restAnnotationIsArray(ta ast.TypeNode) bool {
	n := ta
	if n == nil {
		return false
	}
	switch x := ast.Unparen(n).(type) {
	case *ast.ArrayType:
		return true
	case *ast.TupleType:
		return false
	case *ast.KeywordType:
		return false
	case *ast.TypeReference:
		if x.Name == "Array" && len(x.Qualifier) == 0 {
			return true
		}
	}
	return e.annotationType(ta).IsArray()
}

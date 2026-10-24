package llvm

import (
	"reflect"
	"strconv"
	"strings"

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
				inner := len(hoisted)
				for _, st := range b.Body {
					visit(reflect.ValueOf(st), seen)
				}
				hoistBlockTypes(b, &n, &hoisted, inner)
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

// hoistBlockTypes renames and removes b's own type declarations, rewriting
// the annotations in b, and in the declarations hoisted from inside it
// (those from index inner on), that name them.
func hoistBlockTypes(b *ast.BlockStatement, n *int, hoisted *[]ast.Statement, inner int) {
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
	if len(renames) == 0 {
		return
	}
	b.Body = kept
	rename := func(ta *ast.TypeAnnotation) {
		if len(ta.Qualifier) > 0 {
			return
		}
		base := strings.TrimRight(ta.Name, "[]")
		if nu, ok := renames[base]; ok {
			ta.Name = nu + ta.Name[len(base):]
		}
	}
	seen := map[uintptr]bool{}
	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		switch v.Kind() {
		case reflect.Pointer:
			if v.IsNil() || seen[v.Pointer()] {
				return
			}
			seen[v.Pointer()] = true
			if ta, ok := v.Interface().(*ast.TypeAnnotation); ok {
				rename(ta)
			}
			walk(v.Elem())
		case reflect.Interface:
			if !v.IsNil() {
				walk(v.Elem())
			}
		case reflect.Struct:
			if v.CanAddr() {
				if ta, ok := v.Addr().Interface().(*ast.TypeAnnotation); ok && !seen[v.Addr().Pointer()] {
					seen[v.Addr().Pointer()] = true
					rename(ta)
				}
			}
			for i := 0; i < v.NumField(); i++ {
				if v.Type().Field(i).IsExported() {
					walk(v.Field(i))
				}
			}
		case reflect.Slice:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}
		}
	}
	for _, st := range b.Body {
		walk(reflect.ValueOf(st))
	}
	for _, st := range (*hoisted)[inner:] {
		walk(reflect.ValueOf(st))
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
					p.Type = ast.WithAnyArrayStorage(p.Type)
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
func (e *Emitter) restAnnotationIsArray(ta *ast.TypeAnnotation) bool {
	if ta.ElemType != nil || strings.HasSuffix(ta.Name, "[]") {
		return true
	}
	if ta.TupleElems != nil || ta.EmptyTuple || ta.Name == "" || ta.Name == "never" {
		return false
	}
	_, named := e.interfaces[ta.Name]
	_, generic := e.genericTypeAliases[ta.Name]
	if named || generic {
		return e.resolveType(ta).IsArray()
	}
	return false
}

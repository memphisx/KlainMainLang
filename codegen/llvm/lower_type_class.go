package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/checker"
)

// A class declared `/** @lowerType <layout> */` (TDD-00255 Stage 3) is one
// whose instances C code builds: it allocates the instance zeroed (calloc),
// stores the header word, runs the field initializers and fills the fields
// it owns, reading the layout from symbols this file exports under the
// layout's name:
//
//	__kml_lt_<layout>_size     i64  the instance's byte size
//	__kml_lt_<layout>_tag      i64  the header word (field 0)
//	__kml_lt_<layout>_vtable   ptr  the method table, or null
//	__kml_lt_<layout>_vtable_at i64 its offset (0 when there is none)
//	__kml_lt_<layout>_<field>  i64  each field's byte offset (`#x` → `priv_x`)
//	__kml_lt_<layout>_init     ptr  the field initializers, a void(ptr) function
//
// A field C reads is an extern of its own, so renaming it breaks the link
// rather than the memory.
const lowerTypeInitMethod = "__kml_lt_init"

// addLowerTypeInit gives a @lowerType class a method running its own field
// initializers, compiled like any other method and exported as the layout's
// init function.
func addLowerTypeInit(cd *ast.ClassDeclaration) error {
	if cd.LowerType == "" {
		return nil
	}
	pos := cd.GetPos()
	if cd.BaseClass != "" || len(cd.TypeParams) > 0 {
		return fmt.Errorf("%d:%d: a @lowerType class cannot extend another or be generic", pos.Line, pos.Col)
	}
	for _, m := range cd.Methods {
		if m.Name == lowerTypeInitMethod {
			return nil
		}
	}
	cd.Methods = append(cd.Methods, &ast.FunctionDeclaration{
		Name:       lowerTypeInitMethod,
		Visibility: "private",
		Body:       ast.NewBlockStatement(classFieldInitStmts(cd), pos),
	})
	return nil
}

// emitLowerTypeLayout exports className's layout under layout's name.
func (e *Emitter) emitLowerTypeLayout(cd *ast.ClassDeclaration, className string, info ClassInfo) {
	layout := cd.LowerType
	if e.lowerTypeClasses == nil {
		e.lowerTypeClasses = map[string]*ast.ClassDeclaration{}
	}
	e.lowerTypeClasses[className] = cd
	p := "@__kml_lt_" + layout + "_"
	e.emitGlobal(fmt.Sprintf("%ssize = weak_odr constant i64 %d, align 8", p, info.Ty.StructSize()))
	e.emitGlobal(fmt.Sprintf("%stag = weak_odr constant i64 %d, align 8", p, info.TagID))
	// The method table, for a class whose tree overrides a method: its
	// pointer and where it goes (0 for none, field 0 being the header).
	if info.Ty.HasVTable {
		e.emitGlobal(fmt.Sprintf("%svtable = weak_odr constant ptr @%s_vtable, align 8", p, className))
		e.emitGlobal(fmt.Sprintf("%svtable_at = weak_odr constant i64 %d, align 8", p, info.Ty.FieldOffset(info.Ty.VTableIndex())))
	} else {
		e.emitGlobal(fmt.Sprintf("%svtable = weak_odr constant ptr null, align 8", p))
		e.emitGlobal(fmt.Sprintf("%svtable_at = weak_odr constant i64 0, align 8", p))
	}
	for i, f := range info.Ty.Fields {
		if i == 0 || f.Name == ClassVTableField || strings.HasPrefix(f.Name, "__kml_") {
			continue
		}
		name := f.Name
		if strings.HasPrefix(name, "#") {
			name = "priv_" + name[1:]
		}
		e.emitGlobal(fmt.Sprintf("%s%s = weak_odr constant i64 %d, align 8", p, name, info.Ty.FieldOffset(i)))
	}
	e.emitGlobal(fmt.Sprintf("%sinit = weak_odr constant ptr @%s_%s, align 8", p, info.MethodImplementor[lowerTypeInitMethod], lowerTypeInitMethod))
}

// lowerTypeClassRepr is a @lowerType class's instance type, for an entry
// point returning one. A library class is typed through its ambient
// declaration (node.d.ts's `interface HttpRequest`), so the class is found by
// that name.
func (e *Emitter) lowerTypeClassRepr(t *checker.Type) (Type, bool) {
	if t == nil || t.Symbol == nil {
		return Type{}, false
	}
	for name, cd := range e.lowerTypeClasses {
		if ast.Unmangle(name) == t.Symbol.Name || cd.Name == t.Symbol.Name {
			return e.classes[name].Ty, true
		}
	}
	return Type{}, false
}

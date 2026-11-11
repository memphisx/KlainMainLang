package resolver

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
)

// reservedMembers are the per-instance slots codegen gives every class
// object (codegen/llvm's ClassTagField and ClassVTableField).
var reservedMembers = map[string]bool{"__kml_tag": true, "__kml_vtable": true}

// reservedMemberDiags reports a class declaring, or assigning `this.x` of,
// a field codegen reserves for its own slots, in either lane.
func reservedMemberDiags(prog *ast.Program, offset func(line, col int) int) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	at := func(p ast.Pos, name string) {
		start := offset(p.Line, p.Col)
		out = append(out, diag.New(diag.ReservedMember, diag.Span{Pos: diag.Pos{Line: p.Line, Col: p.Col}, Start: start, End: start}, name))
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		cd, ok := n.(*ast.ClassDeclaration)
		if !ok {
			return true
		}
		for _, f := range cd.Fields {
			if reservedMembers[f.Name] && !f.Static {
				p := f.Pos
				if p.Line == 0 {
					p = cd.GetPos()
				}
				at(p, f.Name)
			}
		}
		var bodies []ast.Node
		if cd.Constructor != nil {
			bodies = append(bodies, cd.Constructor)
		}
		for _, m := range cd.Methods {
			bodies = append(bodies, m)
		}
		for _, b := range bodies {
			ast.Inspect(b, func(n ast.Node) bool {
				if as, ok := n.(*ast.AssignmentExpression); ok {
					if m, ok := as.Left.(*ast.MemberExpression); ok && reservedMembers[m.Property] {
						if _, this := m.Object.(*ast.ThisExpression); this {
							at(m.GetPos(), m.Property)
						}
					}
				}
				return true
			})
		}
		return true
	})
	return out
}

// paramDecoratorDiags reports each parameter decorator under the standard
// (TC39) dialect, which has none: TS1206 at its `@`, as tsc reports it.
func paramDecoratorDiags(prog *ast.Program, offset func(line, col int) int) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	check := func(params []ast.Param) {
		for _, p := range params {
			for _, d := range p.Decorators {
				at := d.GetPos()
				if at.Col > 1 {
					at.Col-- // the `@`
				}
				start := offset(at.Line, at.Col)
				out = append(out, diag.New(diag.DecoratorsNotValid, diag.Span{Pos: diag.Pos{Line: at.Line, Col: at.Col}, Start: start, End: start}))
			}
		}
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			if cd.Constructor != nil {
				check(cd.Constructor.Params)
			}
			for _, m := range cd.Methods {
				check(m.Params)
			}
		}
		return true
	})
	return out
}

// decoratedMemberDiags reports the decorated class members this compiler
// does not implement yet under the chosen dialect: with experimental
// decorators, a static, accessor or generator method's, and an `accessor`
// auto-field's (a standard decorator); with standard ones, a static
// accessor's and a generator method's. (A generic class's decorated members
// are refused once it is known to stay generic.)
func decoratedMemberDiags(prog *ast.Program, offset func(line, col int) int, standard bool) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	at := func(p ast.Pos, m *diag.Message, args ...any) {
		start := offset(p.Line, p.Col)
		out = append(out, diag.New(m, diag.Span{Pos: diag.Pos{Line: p.Line, Col: p.Col}, Start: start, End: start}, args...))
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		cd, ok := n.(*ast.ClassDeclaration)
		if !ok {
			return true
		}
		if len(cd.TypeParams) > 0 {
			// A generic class: no decorator of any kind yet.
			if p, ok := firstDecorator(cd); ok {
				at(p, diag.DecoratedMemberUnsupported, "a generic class ('"+cd.Name+"')")
			}
			return true
		}
		for _, m := range cd.Methods {
			if len(m.Decorators) == 0 {
				continue
			}
			what := ""
			switch {
			case m.IsGenerator:
				what = "a generator method"
			case standard && m.AccessorKind != "" && m.IsStatic:
				what = "a static accessor"
			case !standard && m.AccessorKind != "":
				what = "an accessor (get/set) under -decorators=experimental"
			case !standard && m.IsStatic:
				what = "a static method under -decorators=experimental"
			}
			if what != "" {
				at(m.GetPos(), diag.DecoratedMemberUnsupported, what+" ('"+m.Name+"')")
			}
		}
		if len(cd.AutoAccessors) > 0 && !standard {
			at(cd.GetPos(), diag.AutoAccessorNeedsStandard)
		}
		return true
	})
	return out
}

// firstDecorator is the position of cd's first decorator: on the class, a
// member, a field, an auto-accessor or a parameter.
func firstDecorator(cd *ast.ClassDeclaration) (ast.Pos, bool) {
	if len(cd.Decorators) > 0 {
		return cd.Decorators[0].GetPos(), true
	}
	fns := append([]*ast.FunctionDeclaration{}, cd.Methods...)
	if cd.Constructor != nil {
		fns = append(fns, cd.Constructor)
	}
	for _, m := range fns {
		if len(m.Decorators) > 0 {
			return m.Decorators[0].GetPos(), true
		}
		for _, p := range m.Params {
			if len(p.Decorators) > 0 {
				return p.Decorators[0].GetPos(), true
			}
		}
	}
	for _, f := range cd.Fields {
		if len(f.Decorators) > 0 {
			return f.Decorators[0].GetPos(), true
		}
	}
	if len(cd.AutoAccessors) > 0 {
		return cd.GetPos(), true
	}
	return ast.Pos{}, false
}

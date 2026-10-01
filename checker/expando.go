package checker

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// Expando declarations (TypeScript 3.1): `f.p = v` in the scope that
// declares the function f — a function declaration, or a const bound to a
// function expression or arrow — declares p on f's type, typed as the
// widened union of every value so assigned.

// expandoDecl is the declaration node of the function sym declares, when an
// expando assignment can add to it; nil otherwise.
func expandoDecl(sym *binder.Symbol) ast.Node {
	if sym == nil || len(sym.Declarations) == 0 {
		return nil
	}
	if len(sym.Declarations) > 1 {
		// An overloaded function: its implementation, when every
		// declaration is one of its signatures.
		var impl *ast.FunctionDeclaration
		for _, d := range sym.Declarations {
			fd, ok := d.Node.(*ast.FunctionDeclaration)
			if !ok {
				return nil
			}
			if fd.Body != nil && !fd.Ambient {
				impl = fd
			}
		}
		if impl == nil {
			return nil
		}
		return impl
	}
	switch d := sym.Declarations[0].Node.(type) {
	case *ast.FunctionDeclaration:
		return d
	case *ast.VarDeclaration:
		if d.Kind != "const" || d.TypeAnnot != nil {
			return nil
		}
		switch d.Init.(type) {
		case *ast.FunctionExpression, *ast.ArrowFunction:
			return d
		}
	}
	return nil
}

// expandos are the property assignments the scope declaring sym makes to
// it, by property name (cached).
func (c *Checker) expandos(sym *binder.Symbol) map[string][]ast.Expression {
	if got, ok := c.expandoCache[sym]; ok {
		return got
	}
	if c.expandoCache == nil {
		c.expandoCache = map[*binder.Symbol]map[string][]ast.Expression{}
	}
	c.expandoCache[sym] = nil // a cycle through an assigned value reads none
	decl := expandoDecl(sym)
	if decl == nil {
		return nil
	}
	container := c.parentOf(decl)
	for {
		switch container.(type) {
		case *ast.ExportDeclaration, *ast.VarDeclarationList:
			container = c.parentOf(container)
			continue
		}
		break
	}
	if container == nil {
		return nil
	}
	out := map[string][]ast.Expression{}
	var walk func(n ast.Node)
	walk = func(n ast.Node) {
		ast.ForEachChild(n, func(ch ast.Node) bool {
			switch x := ch.(type) {
			case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction,
				*ast.ClassDeclaration, *ast.ClassExpression:
				return true // another scope's statements
			case *ast.ExpressionStatement:
				if a, ok := x.Expr.(*ast.AssignmentExpression); ok && a.Op == "=" {
					if m, ok := a.Left.(*ast.MemberExpression); ok && !m.Optional {
						if id, ok := m.Object.(*ast.Identifier); ok {
							if s, _ := c.b.Resolve(id); s == sym {
								out[m.Property] = append(out[m.Property], a.Right)
							}
						}
					}
				}
			}
			walk(ch)
			return true
		})
	}
	walk(container)
	c.expandoCache[sym] = out
	return out
}

// expandoType is the type of the expando property prop of the function
// object names, or nil when object names no such function or the scope
// declares no such property.
func (c *Checker) expandoType(object ast.Expression, prop string) *Type {
	id, ok := object.(*ast.Identifier)
	if !ok {
		return nil
	}
	sym, _ := c.b.Resolve(id)
	values := c.expandos(sym)[prop]
	if len(values) == 0 {
		return nil
	}
	ts := make([]*Type, 0, len(values))
	for _, v := range values {
		ts = append(ts, c.widenFrom(v, c.TypeOf(v)))
	}
	return c.in.union(ts...)
}

// expandoFunction reports whether object names a function an expando
// assignment can add to (whose other properties are then TS2339).
func (c *Checker) expandoFunction(object ast.Expression) bool {
	id, ok := object.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := c.b.Resolve(id)
	if expandoDecl(sym) == nil {
		return false
	}
	// A namespace merged into the function (`function f() {}` and
	// `namespace f {}`, renamed apart by the resolver) declares members too.
	merged := false
	if sym.Scope != nil {
		name := sourceName(sym.Name)
		sym.Scope.Symbols.Each(func(o *binder.Symbol) {
			if o != sym && sourceName(o.Name) == name && c.b.NamespaceScope(o) != nil {
				merged = true
			}
		})
	}
	return !merged
}

// functionMember is the type of the Function interface's member prop (a
// function value's apparent type), or nil.
func (c *Checker) functionMember(prop string) *Type {
	if c.b.Globals == nil {
		return nil
	}
	sym := c.b.Globals.Symbols.Get("Function")
	if sym == nil || sym.Flags&binder.Interface == 0 {
		return nil
	}
	ft := c.interfaceOf(sym, nil)
	if c.Unanswered(ft) {
		return nil
	}
	if p := ft.Prop(prop); p != nil {
		return p.Type
	}
	return nil
}

// ExpandoMember reports whether e reads an expando property of the function
// its object names (`f.p` after `f.p = v`).
func (c *Checker) ExpandoMember(e *ast.MemberExpression) bool {
	return c.expandoFunction(e.Object) && c.expandoType(e.Object, e.Property) != nil
}

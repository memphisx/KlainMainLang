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
	decls := sym.Declarations
	if len(decls) > 1 {
		// A namespace merged into the function adds a declaration with
		// no node of its own.
		kept := decls[:0:0]
		for _, d := range decls {
			if d.Node != nil {
				kept = append(kept, d)
			}
		}
		decls = kept
	}
	if len(decls) == 0 {
		return nil
	}
	if len(decls) > 1 {
		// An overloaded function: its implementation, when every
		// declaration is one of its signatures.
		var impl *ast.FunctionDeclaration
		for _, d := range decls {
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
	switch d := decls[0].Node.(type) {
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

// expandoTarget is the function symbol sym names: sym itself, or what a
// const alias of it (`export default strict`, a `const` the resolver binds
// to the exported function) names.
func (c *Checker) expandoTarget(sym *binder.Symbol) *binder.Symbol {
	for i := 0; i < 8 && sym != nil && len(sym.Declarations) == 1; i++ {
		v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
		if !ok || v.Kind != "const" || v.TypeAnnot != nil {
			break
		}
		id, ok := v.Init.(*ast.Identifier)
		if !ok {
			break
		}
		next, _ := c.b.Resolve(id)
		if next == nil || next == sym {
			break
		}
		sym = next
	}
	return sym
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
					// `foo[k] = v` by a constant key (a const string, number
					// or unique symbol) declares the member that key names.
					if ix, ok := a.Left.(*ast.IndexExpression); ok && !ix.Optional {
						if id, ok := ix.Object.(*ast.Identifier); ok {
							if s, _ := c.b.Resolve(id); s == sym {
								if key, lit := c.accessKey(ix.Index); lit {
									out[key] = append(out[key], a.Right)
								}
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
	values := c.expandos(c.expandoTarget(sym))[prop]
	if len(values) == 0 {
		return nil
	}
	ts := make([]*Type, 0, len(values))
	for _, v := range values {
		ts = append(ts, c.widenFrom(v, c.TypeOf(v)))
	}
	return c.in.union(ts...)
}

// expandoMember reports whether m is an expando property: one the scope
// declares by assigning it on a function.
func (c *Checker) expandoMember(m *ast.MemberExpression) bool {
	id, ok := m.Object.(*ast.Identifier)
	if !ok {
		return false
	}
	sym, _ := c.b.Resolve(id)
	return sym != nil && len(c.expandos(c.expandoTarget(sym))[m.Property]) > 0
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

// untypedCallee reports a callee tsc calls untyped (isUntypedFunctionCall):
// not a union, assignable to Function, with no call or construct
// signature of its own. Its call and its construction are any.
func (c *Checker) untypedCallee(t *Type) bool {
	if c.Unanswered(t) || t.Flags&Union != 0 || t.Flags&Object == 0 || t.Kind == Function || len(t.Calls) > 0 || len(t.Constructs) > 0 {
		return false
	}
	if c.b.Globals == nil {
		return false
	}
	sym := c.b.Globals.Symbols.Get("Function")
	if sym == nil || sym.Flags&binder.Interface == 0 {
		return false
	}
	fn := c.interfaceOf(sym, nil)
	if c.Unanswered(fn) {
		return false
	}
	return t == fn || c.assignableTo(t, fn) == yes
}

// untypedUnion reports a union with a member called untyped
// (untypedCallee) whose other members are callable or constructable: tsc
// calls it untyped too.
func (c *Checker) untypedUnion(t *Type) bool {
	if c.Unanswered(t) || t.Flags&Union == 0 {
		return false
	}
	untyped := false
	for _, m := range members(t) {
		switch {
		case c.untypedCallee(m):
			untyped = true
		case m.Flags&Object != 0 && (m.Kind == Function || len(m.Calls) > 0 || len(m.Constructs) > 0):
		default:
			return false
		}
	}
	return untyped
}

// optionalOnNullish reports the receiver of an optional access that is only
// ever null or undefined: the access always short-circuits, and its member
// is one of never.
func (c *Checker) optionalOnNullish(obj *Type) bool {
	if c.Unanswered(obj) || obj.Flags&(Any|Unknown) != 0 {
		return false
	}
	for _, m := range members(obj) {
		if m.Flags&(Null|Undefined|Void) == 0 {
			return false
		}
	}
	return true
}

// invalidIndexKey reports a key type no property key can be: an object,
// array or function (TS2538). JavaScript converts it with ToPropertyKey.
func (c *Checker) invalidIndexKey(kt *Type) bool {
	return !c.Unanswered(kt) && kt.Flags&Union == 0 && kt.Flags&(Object|NonPrimitive|Void) != 0
}

// symbolKey reports a key typed `symbol` (not one unique symbol, which names
// a property).
func symbolKey(kt *Type) bool {
	return kt.Flags&ESSymbol != 0 && kt.Flags&Union == 0 && kt.Symbol == nil && kt.Value == ""
}

// wellKnownSymbolRead reports a key read off a SymbolConstructor
// (`Symbol.iterator`, through any binding of that type): a unique symbol the
// checker types as `symbol`, which names a property rather than indexing.
func (c *Checker) wellKnownSymbolRead(key ast.Expression) bool {
	m, ok := key.(*ast.MemberExpression)
	if !ok {
		return false
	}
	ot := c.TypeOf(m.Object)
	return !c.Unanswered(ot) && ot.Symbol != nil && sourceName(ot.Symbol.Name) == "SymbolConstructor"
}

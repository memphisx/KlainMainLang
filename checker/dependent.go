package checker

import (
	"strconv"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// Dependent destructuring (TypeScript 4.6): the leaves of an object or tuple pattern
// read from a discriminated union narrow together. A test of one leaf, the
// discriminant, narrows the union the pattern read, and with it every other
// leaf: `const { done, value } = r; if (!done) value` reads value off the
// members whose done can be falsy (tsc's getCandidateDiscriminantPropertyAccess).

// depInfo is a dependent leaf's pattern: the property the leaf takes, and
// each simple leaf of the pattern (the leaf's own included) by its key.
type depInfo struct {
	key  string
	sibs map[*binder.Symbol]string
}

type depEntry struct {
	info *depInfo
	src  *Type
}

// dependentLeaf is sym's pattern and the union it reads, when sym is a leaf
// of a `const` or a parameter's object pattern (no default, no rest, no
// leaf reassigned) over a union some leaf discriminates.
func (c *Checker) dependentLeaf(sym *binder.Symbol) (*depInfo, *Type, bool) {
	if c.depLeaves == nil {
		c.depLeaves = map[*binder.Symbol]depEntry{}
	}
	if e, ok := c.depLeaves[sym]; ok {
		return e.info, e.src, e.info != nil
	}
	c.depLeaves[sym] = depEntry{} // none while its source is typed
	info, src := c.findDependentLeaf(sym)
	c.depLeaves[sym] = depEntry{info, src}
	return info, src, info != nil
}

func (c *Checker) findDependentLeaf(sym *binder.Symbol) (*depInfo, *Type) {
	if len(sym.Declarations) != 1 || sym.Scope == nil {
		return nil, nil
	}
	var props []ast.DestructProp
	var src *Type
	var fn ast.Node
	switch n := sym.Declarations[0].Node.(type) {
	case *ast.ArrayDestructuring:
		// A tuple pattern's leaves take its elements by index.
		if n.Kind != "const" {
			return nil, nil
		}
		for i, el := range n.Elems {
			if el.Name == "" {
				continue
			}
			p := ast.DestructProp{Key: strconv.Itoa(i), Local: el.Name, Default: el.Default, Rest: el.Rest}
			props = append(props, p)
		}
		if st := c.patternAnnotation(n.TypeAnnot); st != nil {
			src = st
		} else {
			src = c.TypeOf(n.Init)
		}
	case *ast.ObjectDestructuring:
		if n.Kind != "const" {
			return nil, nil
		}
		props = n.Props
		if st := c.patternAnnotation(n.TypeAnnot); st != nil {
			src = st
		} else {
			src = c.TypeOf(n.Init)
		}
	case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction:
		params, hasBody := funcParams(n)
		if !hasBody {
			return nil, nil
		}
		for i, p := range params {
			if p.ObjectPattern == nil || !patternBinds(p, sym.Name) {
				continue
			}
			if p.Rest || p.Default != nil {
				return nil, nil
			}
			props, fn = p.ObjectPattern, n
			if p.Type != nil {
				src = c.typeFromNode(p.Type, sym.Scope)
			} else {
				src = c.contextualParam(n, i, false)
			}
		}
	}
	if src == nil || c.Unanswered(src) || src.Flags&Union == 0 {
		return nil, nil
	}
	info := &depInfo{sibs: map[*binder.Symbol]string{}}
	discriminated := false
	for _, p := range props {
		if p.Rest || p.KeyExpr != nil || p.SubArray != nil || p.SubObject != nil {
			if p.Local == sym.Name {
				return nil, nil
			}
			continue
		}
		s := sym.Scope.Symbols.Get(p.Local)
		if s == nil {
			continue
		}
		if p.Local == sym.Name {
			if p.Default != nil {
				return nil, nil
			}
			info.key = p.Key
		}
		if p.Default == nil {
			info.sibs[s] = p.Key
			discriminated = discriminated || isDiscriminant(src, p.Key)
		}
	}
	if info.key == "" || !discriminated {
		return nil, nil
	}
	if fn != nil && c.assignsAny(fn, info.sibs) {
		return nil, nil // a reassigned leaf no longer says which member it came from
	}
	return info, src
}

// assignsAny reports whether fn's body stores to one of syms.
func (c *Checker) assignsAny(fn ast.Node, syms map[*binder.Symbol]string) bool {
	found := false
	target := func(e ast.Expression) {
		ast.Inspect(e, func(n ast.Node) bool {
			if id, ok := n.(*ast.Identifier); ok {
				if s, ok := c.b.Resolve(id); ok {
					if _, in := syms[s]; in {
						found = true
					}
				}
			}
			return !found
		})
	}
	ast.Inspect(fn, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			target(x.Left)
		case *ast.UpdateExpression:
			target(x.Arg)
		}
		return !found
	})
	return found
}

// dependentType is the dependent leaf id's type: its property of the union
// its pattern read, narrowed by the tests of its pattern's leaves before id.
func (c *Checker) dependentType(id *ast.Identifier, sym *binder.Symbol, info *depInfo, src *Type) (*Type, bool) {
	f, local := c.b.FlowAt(id)
	r := ref{sym: sym, dep: info}
	if !local {
		if f == nil {
			return nil, false
		}
		r.outer = c.b.ClosureNarrows(id, sym)
	}
	t, _ := c.flowType(f, r, src, newFlowWalk())
	if c.Unanswered(t) {
		return nil, false
	}
	return c.unionProp(t, info.key)
}

// unionProp is property key of each member of t, a union of object types.
func (c *Checker) unionProp(t *Type, key string) (*Type, bool) {
	var ts []*Type
	for _, m := range members(t) {
		if am := c.apparentType(m); am != nil && m.Flags&Object == 0 {
			m = am
		}
		if m.Flags&Object == 0 || (m.Kind != Anonymous && m.Kind != Instance && m.Kind != Interface && m.Kind != Tuple) {
			return nil, false
		}
		p := discProp(m, key)
		if p == nil {
			return nil, false
		}
		ts = append(ts, p.Type)
	}
	if len(ts) == 0 {
		return c.neverT, true
	}
	return c.in.union(ts...), true
}

// discAccess is e as a property read of r: `x.p` with x denoting r, or, for
// a dependent pattern's union, a leaf of the pattern (the property it took).
func (c *Checker) discAccess(e ast.Expression, r ref) (access, bool) {
	if r.dep != nil {
		if id, ok := e.(*ast.Identifier); ok {
			if s, ok := c.b.Resolve(id); ok {
				if key, in := r.dep.sibs[s]; in {
					return access{e, key, false}, true
				}
			}
		}
		return access{}, false
	}
	m, ok := c.accessOf(e)
	if !ok || !c.refersTo(m.Object, r) {
		return access{}, false
	}
	return m, true
}

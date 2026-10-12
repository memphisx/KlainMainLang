package checker

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/diag"
)

// arrayStorageType is the type of a binding declared `[]` with no type
// (tsc's evolving array): an array of every element type stored to it
// anywhere, by push/unshift, an element store or an assigned array; any[]
// with none. Each read narrows it along the flow graph (evolvingArrayRead).
func (c *Checker) arrayStorageType(sym *binder.Symbol) *Type {
	c.evolvingArray[sym] = true
	var elems []*Type
	for _, n := range c.b.StoresTo(sym) {
		switch n := n.(type) {
		case *ast.VarDeclaration:
			continue // the `[]` itself
		case *ast.AssignmentExpression:
			if n.Op != "=" {
				return c.unanswered
			}
			t := c.TypeOf(n.Right)
			if c.Unanswered(t) || t.Flags&Object == 0 || t.Kind != Array {
				return c.unanswered
			}
			if t.Elem.Flags&Never == 0 {
				elems = append(elems, widen(c, t.Elem))
			}
		default:
			return c.unanswered
		}
	}
	for _, m := range c.arrayMutationsOf(sym) {
		ts, ok := c.mutationElems(m)
		if !ok {
			return c.unanswered
		}
		elems = append(elems, ts...)
	}
	if len(elems) == 0 {
		return c.in.array(c.anyT)
	}
	return c.in.array(c.in.union(elems...))
}

// arrayMutationsOf lists the push/unshift calls and element stores on the
// binding sym, by name.
func (c *Checker) arrayMutationsOf(sym *binder.Symbol) []ast.Node {
	if c.arrayMuts == nil {
		c.arrayMuts = map[*binder.Symbol][]ast.Node{}
		if c.b.Module != nil && c.b.Module.Node != nil {
			ast.Inspect(c.b.Module.Node, func(n ast.Node) bool {
				var obj ast.Expression
				switch n := n.(type) {
				case *ast.CallExpression:
					if m, ok := n.Callee.(*ast.MemberExpression); ok && !m.Optional && !n.Optional && (m.Property == "push" || m.Property == "unshift") {
						obj = m.Object
					}
				case *ast.AssignmentExpression:
					if ix, ok := n.Left.(*ast.IndexExpression); ok && n.Op == "=" {
						obj = ix.Object
					}
				}
				if id, ok := obj.(*ast.Identifier); ok {
					if s, _ := c.b.Resolve(id); s != nil {
						c.arrayMuts[s] = append(c.arrayMuts[s], n)
					}
				}
				return true
			})
		}
	}
	return c.arrayMuts[sym]
}

// isArrayMutation reports whether n (a push/unshift call or an element
// store) mutates the evolving array r.
func (c *Checker) isArrayMutation(n ast.Node, r ref) bool {
	switch n := n.(type) {
	case *ast.CallExpression:
		m, ok := n.Callee.(*ast.MemberExpression)
		return ok && !m.Optional && !n.Optional && (m.Property == "push" || m.Property == "unshift") && c.refersTo(m.Object, r)
	case *ast.AssignmentExpression:
		ix, ok := n.Left.(*ast.IndexExpression)
		return ok && n.Op == "=" && c.refersTo(ix.Object, r)
	}
	return false
}

// mutationElems is the element types a mutation adds: each pushed value's
// widened type (a spread one's elements); an element store's value, at a
// numeric index (tsc's addEvolvingArrayElementType).
func (c *Checker) mutationElems(n ast.Node) ([]*Type, bool) {
	var vals []ast.Expression
	switch n := n.(type) {
	case *ast.CallExpression:
		vals = n.Args
	case *ast.AssignmentExpression:
		ix := n.Left.(*ast.IndexExpression)
		if it := c.TypeOf(ix.Index); c.Unanswered(it) || it.Flags&(Number|NumberLiteral|Int|Float32|Any) == 0 {
			return nil, true
		}
		vals = []ast.Expression{n.Right}
	}
	var ts []*Type
	for _, v := range vals {
		if sp, ok := v.(*ast.SpreadElement); ok {
			t := c.TypeOf(sp.Arg)
			if c.Unanswered(t) || t.Flags&Object == 0 || t.Kind != Array {
				return nil, false
			}
			ts = append(ts, widen(c, t.Elem))
			continue
		}
		t := c.TypeOf(v)
		if c.Unanswered(t) {
			return nil, false
		}
		ts = append(ts, c.widenFrom(v, t))
	}
	return ts, true
}

// addArrayElems is the evolving array t after the mutation n.
func (c *Checker) addArrayElems(t *Type, n ast.Node) *Type {
	if c.Unanswered(t) {
		return t
	}
	base, ok := c.evolvingElems(t)
	if !ok {
		return t
	}
	add, ok := c.mutationElems(n)
	if !ok {
		return c.unanswered
	}
	return c.evolvedArray(append(base, add...))
}

// evolvedArray is the array of elems, never[] with none.
func (c *Checker) evolvedArray(elems []*Type) *Type {
	if len(elems) == 0 {
		return c.in.array(c.neverT)
	}
	return c.in.array(c.in.union(elems...))
}

// evolvingElems is the element types of an evolving array's flow type: an
// array, or a union of them where paths joined (never[], the empty one,
// adds none).
func (c *Checker) evolvingElems(t *Type) ([]*Type, bool) {
	var out []*Type
	for _, m := range members(t) {
		if m.Flags&Never != 0 {
			continue // a path that does not continue
		}
		if m.Flags&Object == 0 || m.Kind != Array {
			return nil, false
		}
		if m.Elem.Flags&Never == 0 {
			out = append(out, m.Elem)
		}
	}
	return out, true
}

// evolvingArrayRead is the type of a read of the evolving array sym at id:
// its flow type finalized (tsc's finalizeEvolvingArrayType), or any[] where
// no element type reached it (TS7034, TS7005) or the read is in another
// function. A mutation's own target, and `.length`, read it as declared.
func (c *Checker) evolvingArrayRead(id *ast.Identifier, sym *binder.Symbol, declared *Type) *Type {
	if c.arrayOperationTarget(id) {
		return declared
	}
	f, local := c.b.FlowAt(id)
	if !local || f == nil {
		c.reportEvolvingArray(id, sym)
		return c.in.array(c.anyT)
	}
	t, _ := c.flowType(f, ref{sym: sym}, declared, newFlowWalk())
	if c.Unanswered(t) {
		return t
	}
	elems, ok := c.evolvingElems(t)
	if !ok {
		return t // assigned something else
	}
	if len(elems) == 0 {
		c.reportEvolvingArray(id, sym)
		return c.in.array(c.anyT)
	}
	return c.in.array(c.in.union(elems...))
}

// arrayOperationTarget reports tsc's isEvolvingArrayOperationTarget: id is
// the array of a push/unshift call, an element store or a `.length` read.
func (c *Checker) arrayOperationTarget(id *ast.Identifier) bool {
	switch p := c.parentOf(id).(type) {
	case *ast.MemberExpression:
		if p.Object != ast.Expression(id) {
			return false
		}
		if p.Property == "length" {
			return true
		}
		call, ok := c.parentOf(p).(*ast.CallExpression)
		return ok && call.Callee == ast.Expression(p) && (p.Property == "push" || p.Property == "unshift")
	case *ast.IndexExpression:
		a, ok := c.parentOf(p).(*ast.AssignmentExpression)
		return ok && p.Object == ast.Expression(id) && a.Left == ast.Expression(p) && a.Op == "="
	}
	return false
}

// reportEvolvingArray reports, once each, an evolving array read where no
// element type reached it: TS7034 at its declaration, TS7005 at the read.
func (c *Checker) reportEvolvingArray(id *ast.Identifier, sym *binder.Symbol) {
	if c.opts.CompatJS() || c.implicitAnyReported[id] {
		return
	}
	c.implicitAnyReported[id] = true
	if len(sym.Declarations) > 0 {
		if d := sym.Declarations[0].Node; !c.implicitAnyReported[d] {
			c.implicitAnyReported[d] = true
			pos := d.GetPos()
			if v, ok := d.(*ast.VarDeclaration); ok && v.NamePos.Line > 0 {
				pos = v.NamePos
			}
			c.report(diag.VarImplicitAnyArraySome, pos, sourceName(id.Name))
		}
	}
	c.report(diag.VarImplicitAnyArray, id.GetPos(), sourceName(id.Name))
}

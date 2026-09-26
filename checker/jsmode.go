package checker

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// The -compat=js mode's inference (TDD-00022), moved from codegen's jsinfer
// pass: plain JavaScript declares no parameter or field types, so a
// parameter takes what its call sites pass and a class's fields are what its
// constructor assigns.

type jsTables struct {
	calls map[*binder.Symbol][][]ast.Expression // a function's call sites' arguments
	news  map[*binder.Symbol][][]ast.Expression // a class's `new` sites' arguments
	param map[jsParamKey]*Type
	busy  map[jsParamKey]bool
}

type jsParamKey struct {
	fn ast.Node
	i  int
}

// tables indexes every call through a name and every `new`, once.
func (c *Checker) tables() *jsTables {
	if c.js != nil {
		return c.js
	}
	c.js = &jsTables{calls: map[*binder.Symbol][][]ast.Expression{}, news: map[*binder.Symbol][][]ast.Expression{},
		param: map[jsParamKey]*Type{}, busy: map[jsParamKey]bool{}}
	if c.b.Module == nil || c.b.Module.Node == nil {
		return c.js
	}
	ast.Inspect(c.b.Module.Node, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpression:
			if id, ok := x.Callee.(*ast.Identifier); ok && !x.Optional {
				if sym, ok := c.b.Resolve(id); ok && sym != nil {
					c.js.calls[sym] = append(c.js.calls[sym], x.Args)
				}
			}
		case *ast.NewExpression:
			if sym := c.b.NewTarget(x); sym != nil {
				c.js.news[sym] = append(c.js.news[sym], x.Args)
			}
		}
		return true
	})
	return c.js
}

// callSiteParam is the type of parameter i of fn from its call sites: the
// first site whose argument the checker types gives it (widened); a site
// that disagrees leaves it unanswered (codegen rejects the program, asking
// for an annotation); no site gives any, as TypeScript's checkJs does. A
// function whose callers are out of sight stays unanswered.
func (c *Checker) callSiteParam(fn ast.Node, i int) *Type {
	t := c.tables()
	key := jsParamKey{fn, i}
	if r, ok := t.param[key]; ok {
		return r
	}
	if t.busy[key] {
		return c.unanswered // the argument depends on the parameter itself
	}
	t.busy[key] = true
	sites, known := c.sitesOf(fn)
	r := c.unanswered
	if known {
		r = c.inferFromSites(sites, i)
	}
	delete(t.busy, key)
	t.param[key] = r
	return r
}

func (c *Checker) inferFromSites(sites [][]ast.Expression, i int) *Type {
	var first *Type
	for _, args := range sites {
		if i >= len(args) || contextSensitive(args[i]) {
			continue
		}
		at := c.TypeOf(args[i])
		if c.Unanswered(at) || at.Flags&Any != 0 {
			continue
		}
		at = widen(c, at)
		switch {
		case first == nil:
			first = at
		case first != at:
			return c.unanswered
		}
	}
	if first == nil {
		return c.anyT
	}
	return first
}

// sitesOf lists the argument lists fn is called with: a function
// declaration's calls by name, a function or arrow's through the variable it
// initializes, a constructor's through `new`. known is false where the
// callers are not in sight: a method (its receiver decides the call) or a
// function passed as an argument (the callee calls it, and the builtin
// library is not modelled yet).
func (c *Checker) sitesOf(fn ast.Node) (sites [][]ast.Expression, known bool) {
	t := c.tables()
	switch f := fn.(type) {
	case *ast.FunctionDeclaration:
		if cls, ok := c.parentOf(f).(*ast.ClassDeclaration); ok {
			if cls.Constructor == f {
				if sym := c.symbolOf(cls); sym != nil {
					return t.news[sym], true
				}
			}
			return nil, false
		}
		if sym := c.symbolOf(f); sym != nil {
			return t.calls[sym], true
		}
	case *ast.FunctionExpression, *ast.ArrowFunction:
		if vd, ok := c.parentOf(fn).(*ast.VarDeclaration); ok && ast.Node(vd.Init) == fn {
			if sym := c.symbolOf(vd); sym != nil {
				return t.calls[sym], true
			}
		}
	}
	return nil, false
}

func hasInstanceFields(decl *ast.ClassDeclaration) bool {
	for _, f := range decl.Fields {
		if !f.Static {
			return true
		}
	}
	return false
}

// constructorFields are the fields a JavaScript constructor creates with
// `this.name = value`, anywhere in its body but not in nested functions:
// the first store types the field (widened); a later store of another type
// leaves it unanswered.
func (c *Checker) constructorFields(decl *ast.ClassDeclaration) []*Property {
	var order []string
	types := map[string]*Type{}
	ast.Inspect(decl.Constructor.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.ArrowFunction, *ast.FunctionDeclaration, *ast.ClassDeclaration:
			return false
		case *ast.AssignmentExpression:
			m, ok := x.Left.(*ast.MemberExpression)
			if !ok || x.Op != "=" {
				return true
			}
			if _, isThis := m.Object.(*ast.ThisExpression); !isThis {
				return true
			}
			vt := widen(c, c.TypeOf(x.Right))
			prev, seen := types[m.Property]
			switch {
			case !seen:
				order = append(order, m.Property)
				types[m.Property] = vt
			case prev != vt:
				types[m.Property] = c.unanswered
			}
		}
		return true
	})
	props := make([]*Property, len(order))
	for i, name := range order {
		props[i] = &Property{Name: name, Type: types[name]}
	}
	return props
}

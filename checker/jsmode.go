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
// first site whose argument the checker types gives it (widened); sites
// that disagree, or no site, give any, as TypeScript's checkJs types an
// unannotated parameter. A function whose callers are out of sight stays
// unanswered.
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
	} else if c.isMethod(fn) || c.isReturnedFunction(fn) {
		// Its receiver, or whoever takes the returned function, decides
		// the call: any, as checkJs gives.
		r = c.anyT
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
		if c.Unanswered(at) {
			continue
		}
		if at.Flags&Any != 0 && at.Flags&Union == 0 {
			// A value of any type reaches the parameter: it is any.
			return c.anyT
		}
		// A null or undefined argument widens to any in a JS file: it
		// says nothing of the parameter's type.
		if at.Flags&Union == 0 && at.Flags&(Null|Undefined|Void) != 0 {
			continue
		}
		at = widen(c, at)
		switch {
		case first == nil:
			first = at
		case first != at:
			return c.anyT // a polymorphic parameter
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
		if call, ok := c.parentOf(fn).(*ast.CallExpression); ok && ast.Node(call.Callee) == fn {
			return [][]ast.Expression{call.Args}, true // an IIFE: its one call
		}
	}
	return nil, false
}

// isReturnedFunction reports whether fn is a function literal its
// enclosing function returns (`return (a) => a`, `() => (a) => a`).
func (c *Checker) isReturnedFunction(fn ast.Node) bool {
	switch fn.(type) {
	case *ast.FunctionExpression, *ast.ArrowFunction:
	default:
		return false
	}
	switch p := c.parentOf(fn).(type) {
	case *ast.ReturnStatement:
		return true
	case *ast.ArrowFunction:
		return ast.Node(p.Body) == fn
	}
	return false
}

// isMethod reports whether fn is a class's method, accessor or static
// method (not its constructor).
func (c *Checker) isMethod(fn ast.Node) bool {
	f, ok := fn.(*ast.FunctionDeclaration)
	if !ok {
		return false
	}
	cls, ok := c.parentOf(f).(*ast.ClassDeclaration)
	return ok && cls.Constructor != f
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

// JSCallSiteParam is parameter i of fn as its call sites type it under
// -compat=js, or nil where the callers are not all in sight (a method, a
// function passed as an argument) or the parameter is annotated.
func (c *Checker) JSCallSiteParam(fn ast.Node, i int) *Type {
	if !c.opts.CompatJS() {
		return nil
	}
	if _, known := c.sitesOf(fn); !known {
		return nil
	}
	t := c.callSiteParam(fn, i)
	if c.Unanswered(t) {
		return nil
	}
	return t
}

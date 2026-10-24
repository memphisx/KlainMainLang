package checker

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"fmt"
)

// The -compat=js mode's inference (TDD-00022), moved from codegen's jsinfer
// pass: plain JavaScript declares no parameter or field types, so a
// parameter takes what its call sites pass and a class's fields are what its
// constructor assigns.

type jsTables struct {
	calls map[*binder.Symbol][][]ast.Expression // a function's call sites' arguments
	news  map[*binder.Symbol][][]ast.Expression // a class's `new` sites' arguments
	tags  map[*binder.Symbol]bool               // a function tagging a template
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
		tags: map[*binder.Symbol]bool{}, param: map[jsParamKey]*Type{}, busy: map[jsParamKey]bool{}}
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
		case *ast.TaggedTemplateExpression:
			if id, ok := x.Tag.(*ast.Identifier); ok {
				if sym, ok := c.b.Resolve(id); ok && sym != nil {
					c.js.tags[sym] = true
				}
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
	if known && c.taggedFunction(fn) {
		// A tag's call passes the template's strings and values: any, as
		// codegen compiles a function some caller tags with.
		r = c.anyT
	} else if known {
		r = c.inferFromSites(sites, i)
	} else if c.isMethod(fn) || c.isReturnedFunction(fn) || c.isFieldInit(fn) || c.isAssignedValue(fn) || c.isMemberReceiver(fn) || c.isArrayElement(fn) || c.isTemplateTag(fn) {
		// Its receiver, whoever takes the returned function, whoever reads
		// the field, the member read off it (`.call(…)`, `.apply(…)`), or
		// whoever reads the array it is an element of decides the call:
		// any, as checkJs gives.
		r = c.anyT
	}
	delete(t.busy, key)
	t.param[key] = r
	return r
}

func (c *Checker) inferFromSites(sites [][]ast.Expression, i int) *Type {
	var first *Type
	for _, args := range sites {
		if i >= len(args) {
			spread := false
			for _, a := range args {
				_, isSpread := a.(*ast.SpreadElement)
				spread = spread || isSpread
			}
			if !spread {
				// The call omits it: the parameter is undefined there.
				return c.anyT
			}
			continue
		}
		if contextSensitive(args[i]) {
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
		// A null or undefined argument widens to any in a JS file, and
		// the parameter holds that value at run time: it is any.
		if at.Flags&Union == 0 && at.Flags&(Null|Undefined|Void) != 0 {
			return c.anyT
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
			// A plain function is also a constructor (`new F(…)`).
			return append(append([][]ast.Expression{}, t.calls[sym]...), t.news[sym]...), true
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

// taggedFunction reports a function (declared, or a variable's literal)
// some template is tagged with by name (`f`x${n}“).
func (c *Checker) taggedFunction(fn ast.Node) bool {
	t := c.tables()
	if vd, ok := c.parentOf(fn).(*ast.VarDeclaration); ok && ast.Node(vd.Init) == fn {
		fn = vd
	}
	sym := c.symbolOf(fn)
	return sym != nil && t.tags[sym]
}

// isTemplateTag reports a function literal tagging a template
// (`(function (s) {})`x“).
func (c *Checker) isTemplateTag(fn ast.Node) bool {
	tt, ok := c.parentOf(fn).(*ast.TaggedTemplateExpression)
	return ok && ast.Node(tt.Tag) == fn
}

// isMemberReceiver reports a function literal a member is read off
// (`function (a) {}.call(null, 1)`).
func (c *Checker) isMemberReceiver(fn ast.Node) bool {
	switch fn.(type) {
	case *ast.FunctionExpression, *ast.ArrowFunction:
	default:
		return false
	}
	m, ok := c.parentOf(fn).(*ast.MemberExpression)
	return ok && ast.Node(m.Object) == fn
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

// isFieldInit reports whether fn is a function literal initializing a
// class field (`#f = (a) => a`).
// isAssignedValue reports a function or arrow assigned to a name (`f = (a)
// => …`): whatever the name holds at a call decides it, so its callers are
// not its own.
func (c *Checker) isAssignedValue(fn ast.Node) bool {
	switch fn.(type) {
	case *ast.FunctionExpression, *ast.ArrowFunction:
	default:
		return false
	}
	a, ok := c.parentOf(fn).(*ast.AssignmentExpression)
	return ok && a.Op == "=" && ast.Node(a.Right) == fn
}

// isArrayElement reports a function literal written as an array literal's
// element (`[function (x) {}]`): whoever reads it off the array calls it.
func (c *Checker) isArrayElement(fn ast.Node) bool {
	switch fn.(type) {
	case *ast.FunctionExpression, *ast.ArrowFunction:
	default:
		return false
	}
	_, ok := c.parentOf(fn).(*ast.ArrayLiteral)
	return ok
}

func (c *Checker) isFieldInit(fn ast.Node) bool {
	switch fn.(type) {
	case *ast.FunctionExpression, *ast.ArrowFunction:
	default:
		return false
	}
	cls, ok := c.parentOf(fn).(*ast.ClassDeclaration)
	if !ok {
		return false
	}
	for _, f := range cls.Fields {
		if ast.Node(f.Initializer) == fn {
			return true
		}
	}
	return false
}

// isMethod reports whether fn is a class's method, accessor or static
// method (not its constructor).
func (c *Checker) isMethod(fn ast.Node) bool {
	switch fn.(type) {
	case *ast.FunctionExpression, *ast.ArrowFunction:
		// An object literal's method (`{ f: function (a) {} }`, `{ f(a) {} }`).
		if obj, ok := c.parentOf(fn).(*ast.ObjectLiteral); ok {
			for _, p := range obj.Properties {
				if ast.Node(p.Value) == fn {
					return true
				}
			}
		}
		return false
	}
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
	if fd, isFD := fn.(*ast.FunctionDeclaration); isFD {
		// Its parameters are inferred inside its own symbol's resolution,
		// as a read of the function infers them: a site's argument whose
		// contextual type reads the function back then meets the
		// resolution cycle, not a half-built signature cached as the
		// function's type.
		if sym := c.symbolOf(fd); sym != nil {
			c.typeOfSymbol(sym)
		}
	}
	t := c.callSiteParam(fn, i)
	if c.Unanswered(t) {
		return nil
	}
	return t
}

// JSConstructorFields are the fields a JavaScript class's constructor
// creates (`this.name = value`), with their types; an unanswered type is a
// field stored with values of different types.
func (c *Checker) JSConstructorFields(decl *ast.ClassDeclaration) []*Property {
	if !c.opts.CompatJS() || decl.Constructor == nil || hasInstanceFields(decl) {
		return nil
	}
	return c.constructorFields(decl)
}

// jsUndeclaredMember reports, in the js lane, a member a program class's
// instance does not have and the class never assigns (`this.x = …`):
// tsc's error type, any, unreported in JavaScript
// (`this.__lookupGetter__`).
func (c *Checker) jsUndeclaredMember(m *Type, name string) bool {
	if !c.opts.CompatJS() || m.Kind != Instance || m.Symbol == nil || c.inLibrary(m.Symbol.Scope) {
		return false
	}
	for b := m; b != nil; b = b.Base {
		if b.Symbol == nil {
			return false
		}
		decl := classDecl(b.Symbol)
		if decl == nil || assignsThisMember(decl, name) {
			return false
		}
		// A class still being built (its own field initializer reading
		// `this.g`) answers from its declaration: a member it declares is
		// still to come.
		if (b.partial || c.building[b]) && (declaresMember(decl, name) || decl.BaseClass != "" && b.Base == nil) {
			return false
		}
	}
	return true
}

// declaresMember reports whether a class declares an instance member name.
func declaresMember(decl *ast.ClassDeclaration, name string) bool {
	for _, f := range decl.Fields {
		if !f.Static && f.Name == name {
			return true
		}
	}
	for _, m := range decl.Methods {
		if !m.IsStatic && m.Name == name {
			return true
		}
	}
	return false
}

// assignsThisMember reports whether a class's body assigns `this.name`.
func assignsThisMember(decl *ast.ClassDeclaration, name string) bool {
	found := false
	ast.Inspect(decl, func(n ast.Node) bool {
		if a, ok := n.(*ast.AssignmentExpression); ok {
			if m, ok := a.Left.(*ast.MemberExpression); ok && m.Property == name {
				if _, isThis := m.Object.(*ast.ThisExpression); isThis {
					found = true
				}
			}
		}
		return !found
	})
	return found
}

// jsMethodAssignedMember is, in the js lane, the type of an instance member
// a program class declares only by assigning it in a method or accessor
// (`set m(v) { this._v = v }`): the union of the assigned values' widened
// types, as tsc declares it in a JavaScript file. nil when none assigns it.
func (c *Checker) jsMethodAssignedMember(m *Type, name string) *Type {
	if !c.opts.CompatJS() || m.Kind != Instance {
		return nil
	}
	for b := m; b != nil; b = b.Base {
		if b.Symbol == nil || c.inLibrary(b.Symbol.Scope) {
			return nil
		}
		decl := classDecl(b.Symbol)
		if decl == nil {
			return nil
		}
		key := fmt.Sprintf("%p.%s", decl, name)
		if c.methodAssigning[key] {
			return c.anyT
		}
		var rhs []ast.Expression
		for _, md := range decl.Methods {
			if md.IsStatic || md.Body == nil {
				continue
			}
			ast.Inspect(md.Body, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
					return false
				case *ast.AssignmentExpression:
					if mm, ok := x.Left.(*ast.MemberExpression); ok && x.Op == "=" && mm.Property == name {
						if _, isThis := mm.Object.(*ast.ThisExpression); isThis {
							rhs = append(rhs, x.Right)
						}
					}
				}
				return true
			})
		}
		if len(rhs) == 0 {
			continue
		}
		if c.methodAssigning == nil {
			c.methodAssigning = map[string]bool{}
		}
		c.methodAssigning[key] = true
		ts := make([]*Type, 0, len(rhs))
		for _, r := range rhs {
			ts = append(ts, widen(c, c.TypeOf(r)))
		}
		delete(c.methodAssigning, key)
		for _, t := range ts {
			if c.Unanswered(t) {
				return t
			}
			if t.Flags&Any != 0 {
				return c.anyT
			}
		}
		return c.in.union(ts...)
	}
	return nil
}

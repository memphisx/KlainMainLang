package checker

import (
	"fmt"
	"strconv"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/diag"
)

// typeOfSymbol returns the type a value symbol's declaration gives it: its
// annotation, or its initializer's type (widened for a mutable binding).
func (c *Checker) typeOfSymbol(sym *binder.Symbol) *Type {
	if t, ok := c.symTypes[sym]; ok {
		return t
	}
	if c.resolving[sym] {
		if k := c.symCut[sym]; k != nil {
			c.cuts = append(c.cuts, k)
			c.cutCount++
		}
		// A function read while its own return type is inferred: tsc gives
		// that return type any (TS7023). A declaration is still itself, a
		// function with its parameters and an any result.
		if len(sym.Declarations) == 1 && isUnannotatedFunction(sym.Declarations[0].Node) {
			if fe, ok := sym.Declarations[0].Node.(*ast.FunctionExpression); ok {
				if c.feCycled == nil {
					c.feCycled = map[*ast.FunctionExpression]bool{}
				}
				c.feCycled[fe] = true
			}
			c.reportReturnCycle(sym)
			c.markReturnCycle(sym)
			if fd, ok := sym.Declarations[0].Node.(*ast.FunctionDeclaration); ok && !fd.IsAsync && !fd.IsGenerator && len(fd.Overloads) == 0 && !c.selfRef[sym] {
				c.selfRef[sym] = true
				t := c.signatureType(fd, fd.Params, nil, nil, nil, sym.Scope)
				delete(c.selfRef, sym)
				if !c.Unanswered(t) && t.Flags&Object != 0 && t.Kind == Function {
					return c.in.withResult(t, c.anyT)
				}
			}
			return c.anyT
		}
		// `var f = function (n) { return f(n - 1) }`: the function read
		// while its result is inferred is itself with an any result
		// (TS7023), as a declaration is.
		if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok && len(sym.Declarations) == 1 && v.TypeAnnot == nil {
			if fe, ok := v.Init.(*ast.FunctionExpression); ok && fe.RetType == nil && !fe.IsAsync && !fe.IsGenerator && !c.selfRef[sym] {
				c.reportReturnCycleAt(sym, fe)
				c.selfRef[sym] = true
				t := c.signatureType(fe, fe.Params, nil, nil, nil, sym.Scope)
				delete(c.selfRef, sym)
				if !c.Unanswered(t) && t.Flags&Object != 0 && t.Kind == Function {
					return c.in.withResult(t, c.anyT)
				}
			}
		}
		return c.unanswered // a cycle (a binding typed by its own initializer)
	}
	c.resolving[sym] = true
	if c.symCut == nil {
		c.symCut = map[*binder.Symbol]ast.Expression{}
	}
	key := ast.NewIdentifier(sym.Name, ast.Pos{})
	c.symCut[sym] = key
	c.assigning[key] = true
	c.resolveStack = append(c.resolveStack, sym)
	t := c.computeSymbolType(sym)
	c.resolveStack = c.resolveStack[:len(c.resolveStack)-1]
	if len(sym.Declarations) == 1 {
		if fd, ok := sym.Declarations[0].Node.(*ast.FunctionDeclaration); ok && c.fdCycled[fd] && !c.Unanswered(t) && t.Flags&Object != 0 && t.Kind == Function {
			t = c.in.withResult(t, c.anyT) // tsc's result for a return cycle
		}
	}
	delete(c.resolving, sym)
	delete(c.symCut, sym)
	delete(c.assigning, key)
	if len(c.assigning) == 0 {
		c.cuts = c.cuts[:0]
	}
	c.symTypes[sym] = t
	return t
}

func (c *Checker) computeSymbolType(sym *binder.Symbol) *Type {
	if sym.Implicit && sym.Name == "arguments" && c.b.Globals != nil {
		// A function's own `arguments`: the lib's IArguments.
		if ia := c.b.Globals.Symbols.Get("IArguments"); ia != nil && ia.Flags&binder.Interface != 0 {
			return c.interfaceOf(ia, nil)
		}
	}
	if sym.Implicit || len(sym.Declarations) == 0 {
		return c.unanswered
	}
	// The value's declaration (tsc's valueDeclaration): an interface or
	// type alias merged with a value (`interface Math` + `declare var
	// Math: Math`) does not give the value its type.
	d := sym.Declarations[0]
	for _, x := range sym.Declarations {
		switch x.Node.(type) {
		case *ast.InterfaceDeclaration, *ast.TypeAliasDeclaration:
			continue
		}
		d = x
		break
	}
	// A `var` sharing its name with a function declaration (TS2300 in
	// JavaScript too) is typed by the function, as tsc types it.
	if _, isVar := d.Node.(*ast.VarDeclaration); isVar {
		for _, x := range sym.Declarations {
			if fd, ok := x.Node.(*ast.FunctionDeclaration); ok && fd.Body != nil {
				d = x
				break
			}
		}
	}
	return c.declType(sym, d)
}

// declType is the type declaration d gives symbol sym: its annotation, its
// initializer's (widened unless const), a loop's element, and so on.
func (c *Checker) declType(sym *binder.Symbol, d binder.Declaration) *Type {
	switch n := d.Node.(type) {
	case *ast.VarDeclaration:
		if n.TypeAnnot != nil {
			if tn := n.TypeAnnot.TypeNode(); tn != nil {
				return c.typeFromNode(tn, sym.Scope)
			}
			return c.unanswered
		}
		if n.Init == nil {
			// An ambient, exported or namespace-member variable without a
			// type is any: only a local one evolves (tsc's autoType).
			if c.ImplicitAnyVariables || n.Ambient || c.b.Exported(n) || sym.Scope.Kind == binder.NamespaceScope {
				return c.anyT
			}
			return c.storageType(sym)
		}
		if a, ok := n.Init.(*ast.ArrayLiteral); ok && len(a.Elements) == 0 && !c.ImplicitAnyVariables && !n.Ambient && !c.b.Exported(n) && sym.Scope.Kind != binder.NamespaceScope {
			return c.arrayStorageType(sym) // `[]`: an evolving array
		}
		if _, ok := n.Init.(*ast.NullLiteral); ok && n.Kind != "const" {
			// `let x = null` / `let x = undefined` evolve like `let x;`:
			// TypeScript widens their declared type to an evolving any.
			if c.ImplicitAnyVariables {
				return c.anyT
			}
			return c.storageType(sym)
		}
		t := c.TypeOf(n.Init)
		if n.Kind == "const" || c.constAsserted(n.Init) {
			return t
		}
		return c.widenFrom(n.Init, t)
	case *ast.FunctionDeclaration:
		if sym.Flags&binder.Function != 0 && d.Flags&binder.Function != 0 {
			if sigs := ambientOverloads(sym); len(sigs) > 1 {
				// `declare function f(…)` more than once: every one is a
				// signature, with no implementation.
				return c.overloadsType(strconv.Itoa(sym.ID), sym.Scope, sigs)
			}
			if len(n.Overloads) > 0 {
				return c.overloadsType(strconv.Itoa(sym.ID), sym.Scope, n.Overloads)
			}
			return c.signatureType(n, n.Params, n.ReturnType, n.Body, nil, sym.Scope)
		}
		return c.paramType(n, sym, n.Params, sym.Scope)
	case *ast.FunctionExpression:
		if d.Flags&binder.Function != 0 {
			return c.TypeOf(n) // the expression's own name: the function
		}
		return c.contextualOr(n, sym, n.Params)
	case *ast.ArrowFunction:
		return c.contextualOr(n, sym, n.Params)
	case *ast.ClassDeclaration:
		return c.classObject(sym, n)
	case *ast.EnumDeclaration:
		if sym.Flags&binder.EnumMember != 0 {
			// A member read unqualified in an initializer: its literal type.
			if sym.Scope.Parent == nil {
				return c.unanswered
			}
			enum := sym.Scope.Parent.Symbols.Get(n.Name)
			if enum == nil {
				return c.unanswered
			}
			for _, m := range c.enumMembers(enum) {
				if m.Member == sym.Name {
					return m
				}
			}
			return c.unanswered
		}
		return c.enumObject(sym)
	case *ast.ArrayDestructuring:
		if st := c.patternAnnotation(n.TypeAnnot); st != nil {
			return c.bindingType(sym.Name, n.Kind, n.Elems, nil, st, nil)
		}
		return c.bindingType(sym.Name, n.Kind, n.Elems, nil, c.TypeOf(n.Init), c.pathOf(n.Init))
	case *ast.ObjectDestructuring:
		if st := c.patternAnnotation(n.TypeAnnot); st != nil {
			return c.bindingType(sym.Name, n.Kind, nil, n.Props, st, nil)
		}
		return c.bindingType(sym.Name, n.Kind, nil, n.Props, c.TypeOf(n.Init), c.pathOf(n.Init))
	case *ast.TryStatement:
		if n.Catch != nil && n.Catch.ObjectPattern == nil && n.Catch.Param == sym.Name {
			if ta := n.Catch.ParamType; ta != nil && ta.TypeNode() != nil {
				return c.typeFromNode(ta.TypeNode(), c.b.Module) // `catch (e: any)`
			}
			if c.CatchVariablesAny || c.opts.CompatJS() {
				return c.anyT
			}
			return c.unknownT // --strict's useUnknownInCatchVariables
		}
	case *ast.ForInStatement:
		if n.VarName == sym.Name {
			return c.strT // a property key
		}
	case *ast.ForOfStatement:
		if n.ArrayPattern != nil || n.ObjectPattern != nil {
			// A destructured loop variable: its leaf of the element.
			if n.Await {
				return c.unanswered
			}
			el := c.iteratedType(c.TypeOf(n.Iterable))
			return c.bindingType(sym.Name, n.Kind, n.ArrayPattern, n.ObjectPattern, el, nil)
		}
		if n.VarName != sym.Name {
			return c.unanswered
		}
		it := c.TypeOf(n.Iterable)
		switch {
		case it.Flags&Object != 0 && it.Kind == Array && n.Await:
			return c.awaited(it.Elem, 0) // `for await` awaits each element
		case n.Await:
			if isStringLike(it) {
				return c.strT
			}
			// An async iterable's element, or a sync iterable's awaited
			// (tsc's getIteratedTypeOrElementType with an async use).
			if et := c.asyncIteratedType(it); !c.Unanswered(et) {
				return c.awaited(et, 0)
			}
		default:
			return c.iteratedType(it) // an any's element is any, a Map's an entry
		}
	}
	return c.unanswered
}

// paramType is the declared type of the parameter sym of a function with
// params.
func (c *Checker) paramType(fn ast.Node, sym *binder.Symbol, params []ast.Param, scope *binder.Scope) *Type {
	for i, p := range params {
		if (p.ArrayPattern != nil || p.ObjectPattern != nil) && patternBinds(p, sym.Name) {
			return c.patternParamLeafType(fn, i, p, sym.Name, scope)
		}
	}
	for i, p := range params {
		if p.Name != sym.Name || p.ArrayPattern != nil || p.ObjectPattern != nil {
			continue
		}
		var t *Type
		switch {
		case p.Type != nil && p.Type.TypeNode() != nil:
			t = c.typeFromNode(p.Type.TypeNode(), scope)
		case p.Default != nil && defaultRefersTo(p.Default, p.Name):
			return c.anyT // `x = x`: TS2372, the parameter is any
		case p.Default != nil:
			t = c.widenFrom(p.Default, c.TypeOf(p.Default))
		case c.opts.CompatJS() && fn != nil && !p.Rest:
			if t := c.callSiteParam(fn, i); !c.Unanswered(t) {
				return t
			}
			return c.anyT
		case p.Rest:
			return c.in.array(c.anyT) // an implicit any[] (TS7019)
		default:
			return c.anyT // an implicit any (TS7006)
		}
		if (p.Optional || p.Default != nil) && !c.Unanswered(t) && p.Default == nil {
			t = c.in.union(t, c.undefinedT)
		}
		return t
	}
	return c.unanswered
}

// patternBinds reports whether a destructuring parameter binds name.
func patternBinds(p ast.Param, name string) bool {
	for _, n := range arrayPatternLeafNames(p.ArrayPattern) {
		if n == name {
			return true
		}
	}
	for _, n := range objectPatternLeafNames(p.ObjectPattern) {
		if n == name {
			return true
		}
	}
	return false
}

func arrayPatternLeafNames(elems []ast.ArrayPatternElem) []string {
	var out []string
	for _, e := range elems {
		if e.Name != "" {
			out = append(out, e.Name)
		}
		out = append(out, arrayPatternLeafNames(e.SubArray)...)
		out = append(out, objectPatternLeafNames(e.SubObject)...)
	}
	return out
}

func objectPatternLeafNames(props []ast.DestructProp) []string {
	var out []string
	for _, p := range props {
		if p.Local != "" && p.SubArray == nil && p.SubObject == nil {
			out = append(out, p.Local)
		}
		out = append(out, arrayPatternLeafNames(p.SubArray)...)
		out = append(out, objectPatternLeafNames(p.SubObject)...)
	}
	return out
}

// patternParamLeafType is the type of name, bound by the destructuring
// parameter p (the i-th of fn): the element or property it takes from the
// parameter's annotated type, else its contextual type, else its default's.
// A parameter with none of these is an implicit any, and so is each leaf.
func (c *Checker) patternParamLeafType(fn ast.Node, i int, p ast.Param, name string, scope *binder.Scope) *Type {
	var src *Type
	switch {
	case p.Type != nil && p.Type.TypeNode() != nil:
		src = c.typeFromNode(p.Type.TypeNode(), scope)
	default:
		if ct := c.contextualParam(fn, i, p.Rest); ct != nil {
			src = ct
		} else if p.Default != nil {
			src = c.patternDefaultType(p) // a tuple for `[a, b] = [1, "x"]`
		} else if p.ArrayPattern != nil || p.ObjectPattern != nil {
			// The pattern's implied type: a leaf with a default is the
			// default's, any other (a computed key's, a rest's included) an
			// implicit any.
			implied := c.impliedPatternType(p.ArrayPattern, p.ObjectPattern)
			if t := c.bindingType(name, "let", p.ArrayPattern, p.ObjectPattern, implied, nil); !c.Unanswered(t) {
				return t
			}
			return c.anyT
		} else {
			return c.anyT
		}
	}
	if c.Unanswered(src) {
		return src
	}
	if src.Flags&Any != 0 {
		return c.anyT
	}
	return c.bindingType(name, "let", p.ArrayPattern, p.ObjectPattern, src, nil)
}

// signatureType is the function type of a declaration with params: its
// parameters' annotated types, and its annotated return type or, without one,
// the type its body returns (see returnType).
func (c *Checker) signatureType(fn ast.Node, params []ast.Param, ret *ast.TypeAnnotation, body *ast.BlockStatement, exprBody ast.Expression, scope *binder.Scope) *Type {
	// Annotations resolve in the function's own scope: its type parameters
	// are declared there.
	if s := c.b.ScopeOf(fn); s != nil {
		scope = s
	}
	// A leading `this` is the this-parameter (thisParamType), never an
	// argument: code generation puts it at the head of the list it emits.
	if len(params) > 0 && params[0].Name == "this" {
		params = params[1:]
	}
	var tps []*Type
	if names := ast.FunctionTypeParams(fn); len(names) > 0 {
		for _, name := range names {
			sym := scope.Symbols.Get(name)
			if sym == nil || sym.Flags&binder.TypeParameter == 0 {
				// An overload signature: the binder never saw it, and its
				// type parameters are named through the environment.
				if t := c.lookupEnv(name); t != nil && t.Flags&TypeParam != 0 {
					tps = append(tps, t)
					continue
				}
				return c.unanswered
			}
			tps = append(tps, c.typeParamType(sym))
		}
	}
	var ps []*Type
	var opts []bool
	rest := false
	for i, p := range params {
		var t *Type
		switch {
		case p.ArrayPattern != nil || p.ObjectPattern != nil:
			if (p.Type == nil || p.Type.TypeNode() == nil) && p.Default != nil && !p.Rest && !c.opts.CompatJS() {
				// Typed from its default, contextually typed by the pattern:
				// an array pattern makes an array literal a tuple (tsc's
				// getWidenedTypeForVariableLikeDeclaration).
				t = c.patternDefaultType(p)
				break
			}
			if (p.Type == nil || p.Type.TypeNode() == nil) && !p.Rest && !c.opts.CompatJS() {
				// Its contextual type, else the type the pattern implies
				// (tsc's getTypeFromBindingPattern).
				if ct := c.contextualParam(fn, i, false); ct != nil {
					t = ct
				} else {
					t = c.impliedPatternType(p.ArrayPattern, p.ObjectPattern)
				}
				break
			}
			if p.Type == nil || p.Type.TypeNode() == nil {
				if !c.opts.CompatJS() {
					return c.unanswered
				}
				t = c.anyT // a JS pattern parameter accepts any value
				if p.Rest {
					t = c.in.array(c.anyT) // a rest pattern destructures the any[] it collects
				}
				break
			}
			t = c.typeFromNode(p.Type.TypeNode(), scope)
		case p.Type != nil && p.Type.TypeNode() != nil:
			t = c.typeFromNode(p.Type.TypeNode(), scope)
		default:
			// An unannotated parameter: its contextual type, else its
			// default's.
			t = c.unanswered
			if ct := c.contextualParam(fn, i, p.Rest); ct != nil {
				t = ct
			} else if p.Default != nil && defaultRefersTo(p.Default, p.Name) {
				t = c.anyT // `x = x`: TS2372, the parameter is any
			} else if p.Default != nil {
				t = c.widenFrom(p.Default, c.TypeOf(p.Default))
			} else if at := c.iifeParam(fn, i, p.Rest); at != nil {
				t = at
			} else if !p.Rest && c.iifeArgContextFree(fn) {
				t = c.anyT // no context reaches it (TS7006 in strict)
			} else if ne, isNew := c.parentOf(fn).(*ast.NewExpression); isNew && c.opts.CompatJS() && fn != nil && ast.Node(ne.Callee) == fn && !p.Rest {
				t = c.anyT // `new function (a) {…}(5)`: no IIFE context in tsc
			} else if c.opts.CompatJS() && fn != nil && !p.Rest {
				t = c.callSiteParam(fn, i)
			} else if p.Rest {
				t = c.in.array(c.anyT) // an unannotated rest: any[] (TS7019 in strict)
			}
		}
		if c.Unanswered(t) {
			return t
		}
		// tsc's isOptionalParameter: an immediately invoked function's
		// parameter past its last argument is optional.
		opt := p.Optional || p.Default != nil || c.iifeOmitted(fn, i, p.Rest)
		if opt {
			t = c.in.union(t, c.undefinedT) // seen from a caller, it may be omitted
		}
		ps = append(ps, t)
		opts = append(opts, opt)
		rest = rest || p.Rest
	}
	var r *Type
	var pred *Predicate
	switch {
	case ret != nil && ret.TypeNode() != nil:
		if tp, ok := ret.TypeNode().(*ast.TypePredicate); ok {
			pred = c.predicate(tp, params, scope)
			if pred == nil {
				return c.unanswered
			}
			r = c.boolT
			if pred.Asserts {
				r = c.voidT
			}
			break
		}
		r = c.typeFromNode(ret.TypeNode(), scope)
		if (isGeneratorFn(fn) || isAsyncGeneratorFn(fn)) && !c.Unanswered(r) && !c.iteratorLike(r) {
			// This compiler's generator annotation names what it yields
			// (`function* g(): number`).
			if gr := c.generatorOf(r, isAsyncGeneratorFn(fn)); gr != nil {
				r = gr
			}
		}
	case ret != nil:
		return c.unanswered
	case body == nil && exprBody == nil:
		r = c.voidT // a signature without a body (a constructor's, typed for `new`)
	case isGeneratorFn(fn):
		r = c.generatorReturn(body, false)
	case isAsyncGeneratorFn(fn):
		r = c.generatorReturn(body, true)
	case isAsyncFn(fn):
		// Promise of what its returns resolve to (tsc's
		// createPromiseReturnType); Promise<void> when none returns a value.
		r = c.returnType(body, exprBody)
		if c.Unanswered(r) {
			return r
		}
		if _, decl := fn.(*ast.FunctionDeclaration); !decl && body != nil && !hasReturn(body) && !c.endReachable(body) {
			// A function expression or arrow that never completes:
			// Promise<never> (tsc's inference for a non-declaration).
			r = c.neverT
		}
		if r.Flags&Void == 0 {
			r = c.awaited(r, 0)
		}
		if r = c.promiseOf(r); r == nil {
			return c.unanswered
		}
	default:
		r = c.returnType(body, exprBody)
		if _, decl := fn.(*ast.FunctionDeclaration); !decl && fn != nil {
			r = c.contextualReturn(fn, body, exprBody, r)
		}
	}
	if c.Unanswered(r) {
		return r
	}
	if pred == nil && ret == nil && fn != nil {
		pred = c.inferredPredicate(fn, params, ps, r, body, exprBody)
	}
	names := make([]string, len(params))
	for i, p := range params {
		names[i] = p.Name
	}
	return c.in.withThis(c.in.withParamNames(c.in.function(ps, opts, rest, r, pred, tps), names), c.thisParamType(fn, scope))
}

// thisParamType is the type of fn's `this: T` parameter, nil when it has
// none (or it is `this: void`, which any call satisfies).
func (c *Checker) thisParamType(fn ast.Node, scope *binder.Scope) *Type {
	prog, ok := c.b.Module.Node.(*ast.Program)
	if !ok || fn == nil || prog.ThisParams[fn] == nil {
		return nil
	}
	tn := prog.ThisParams[fn].TypeNode()
	if tn == nil {
		return nil
	}
	t := c.typeFromNode(tn, scope)
	if c.Unanswered(t) || t.Flags&Void != 0 {
		return nil
	}
	return t
}

// inferredPredicate is TypeScript's inferred type predicate (5.5): a
// function with no return annotation, a boolean result and one return
// expression (or an expression body) is a guard `x is T` for the first
// parameter, never assigned, that the expression narrows to T when true
// while narrowing T itself by the false branch leaves never
// (checkIfExpressionRefinesParameter).
func (c *Checker) inferredPredicate(fn ast.Node, params []ast.Param, ps []*Type, r *Type, body *ast.BlockStatement, exprBody ast.Expression) *Predicate {
	if r.Flags&Boolean == 0 {
		return nil
	}
	switch f := fn.(type) {
	case *ast.FunctionDeclaration:
		if f.IsAsync || f.IsGenerator {
			return nil
		}
	case *ast.FunctionExpression:
		if f.IsAsync || f.IsGenerator {
			return nil
		}
	case *ast.ArrowFunction:
		if f.IsAsync {
			return nil
		}
	default:
		return nil
	}
	expr := exprBody
	if expr == nil {
		if body == nil || c.endReachable(body) {
			return nil
		}
		n := 0
		ast.Inspect(body, func(x ast.Node) bool {
			switch x := x.(type) {
			case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
				return false
			case *ast.ReturnStatement:
				n++
				expr = x.Value
			}
			return true
		})
		if n != 1 || expr == nil {
			return nil
		}
	}
	scope := c.b.ScopeOf(fn)
	if scope == nil {
		return nil
	}
	for i, p := range params {
		if i >= len(ps) || p.Rest || p.Name == "" || p.Name == "this" || p.ArrayPattern != nil || p.ObjectPattern != nil {
			continue
		}
		sym := scope.Symbols.Get(p.Name)
		if sym == nil || c.assignedIn(sym, fn) {
			continue
		}
		declared := ps[i]
		rf := ref{sym: sym}
		// The parameter's type where the expression is evaluated: at its
		// first read of the parameter.
		base := declared
		var first *ast.Identifier
		ast.Inspect(expr, func(x ast.Node) bool {
			if id, ok := x.(*ast.Identifier); ok && first == nil {
				if s, _ := c.b.Resolve(id); s == sym {
					first = id
				}
			}
			return first == nil
		})
		if first == nil {
			continue
		}
		if f, local := c.b.FlowAt(first); f != nil && local {
			base, _ = c.flowType(f, rf, declared, newFlowWalk())
		}
		trueType := c.narrowBy(base, rf, expr, true)
		if c.Unanswered(trueType) || trueType == declared {
			continue
		}
		if falseSub := c.narrowBy(trueType, rf, expr, false); c.Unanswered(falseSub) || falseSub.Flags&Never == 0 {
			continue
		}
		return &Predicate{Index: i, Type: trueType}
	}
	return nil
}

// assignedIn reports whether sym is assigned anywhere inside fn's body.
func (c *Checker) assignedIn(sym *binder.Symbol, fn ast.Node) bool {
	found := false
	ast.Inspect(fn, func(x ast.Node) bool {
		var target ast.Expression
		switch x := x.(type) {
		case *ast.AssignmentExpression:
			target = x.Left
		case *ast.UpdateExpression:
			target = x.Arg
		}
		if id, ok := target.(*ast.Identifier); ok {
			if s, _ := c.b.Resolve(id); s == sym {
				found = true
			}
		}
		return !found
	})
	return found
}

// predicate is a `p is T`, `asserts p is T` or `asserts p` result's
// predicate; nil for `this is T`, not modelled yet.
func (c *Checker) predicate(tp *ast.TypePredicate, params []ast.Param, scope *binder.Scope) *Predicate {
	if tp.Type == nil && !tp.Asserts {
		return nil
	}
	if tp.This {
		// `this is T` / `asserts this is T`: about the call's receiver.
		var t *Type
		if tp.Type != nil {
			if t = c.typeFromNode(tp.Type, scope); c.Unanswered(t) {
				return nil
			}
		}
		return &Predicate{Index: -1, Type: t, Asserts: tp.Asserts}
	}
	for i, p := range params {
		if p.Name == tp.ParameterName {
			var t *Type
			if tp.Type != nil {
				if t = c.typeFromNode(tp.Type, scope); c.Unanswered(t) {
					return nil
				}
			}
			return &Predicate{Index: i, Type: t, Asserts: tp.Asserts}
		}
	}
	return nil
}

// BodyReturnType is the type a function body returns without an
// annotation (returnType), for codegen's return-type decision.
func (c *Checker) BodyReturnType(body *ast.BlockStatement) *Type {
	// A named function expression returning through its own name: any, as
	// its type's result (checkExpr's feCycled).
	if fe, ok := c.parentOf(body).(*ast.FunctionExpression); ok && fe.Name != "" && !fe.IsGenerator && fe.RetType == nil {
		c.TypeOf(fe)
		if c.feCycled[fe] {
			return c.anyT
		}
	}
	// A function declaration whose result cycled through a call of itself.
	if fd, ok := c.parentOf(body).(*ast.FunctionDeclaration); ok && fd.ReturnType == nil {
		if sym := c.symbolOf(fd); sym != nil {
			c.typeOfSymbol(sym)
		}
		if c.fdCycled[fd] {
			return c.anyT
		}
	}
	return c.returnType(body, nil)
}

// ModuleVarType is the declared type of a module-level variable declaration
// (an evolving let's storage type), for codegen's module-global decision.
func (c *Checker) ModuleVarType(v *ast.VarDeclaration) *Type {
	sym := c.b.Module.Symbols.Get(v.Name)
	if sym == nil {
		return c.unanswered
	}
	for _, d := range sym.Declarations {
		if d.Node == ast.Node(v) {
			return c.typeOfSymbol(sym)
		}
	}
	return c.unanswered
}

// returnType is the type a function body returns, as TypeScript infers it
// without an annotation: the union of its return expressions' types with
// literals widened, void when no return has a value, or an expression body's
// type.
func (c *Checker) returnType(body *ast.BlockStatement, exprBody ast.Expression) *Type {
	if exprBody != nil {
		return c.widenUnit(c.TypeOf(exprBody))
	}
	if body == nil {
		return c.unanswered
	}
	var results []*Type
	valueless, polymorphicThis, selfCall := false, false, false
	fn := c.parentOf(body)
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return false // a nested function's returns are its own
		case *ast.ReturnStatement:
			if n.Value == nil {
				valueless = true
			} else if th, this := n.Value.(*ast.ThisExpression); this && c.thisType(th) != c.anyT {
				// TypeScript's polymorphic `this` type: a read through a
				// receiver instantiates it with the receiver (withThis).
				if tt := c.thisType(th); c.Unanswered(tt) {
					polymorphicThis = true
				} else {
					results = append(results, c.thisT)
				}
			} else if c.callsItself(fn, n.Value) {
				// `return f(…)` of the function itself returns what its
				// other returns do (tsc's checkAndAggregateReturnExpression-
				// Types: never).
				selfCall = true
			} else {
				results = append(results, c.TypeOf(n.Value))
			}
		}
		return true
	})
	if polymorphicThis {
		return c.unanswered
	}
	if len(results) == 0 && selfCall && !valueless && !c.endReachable(body) {
		return c.neverT
	}
	if len(results) == 0 {
		return c.voidT
	}
	if valueless || c.endReachable(body) {
		results = append(results, c.undefinedT)
	}
	return c.widenUnit(c.join(results...))
}

// contextualReturn adjusts a function expression's or arrow's inferred
// return type r as TypeScript does: a body with no return statement whose
// end is unreachable returns never; one returning nothing where the context
// expects undefined returns undefined; and a literal is kept when the
// contextual return type has literal members (`() => "bar"` for
// `() => "foo" | "bar"`).
func (c *Checker) contextualReturn(fn ast.Node, body *ast.BlockStatement, exprBody ast.Expression, r *Type) *Type {
	if body != nil && !hasReturn(body) && !c.endReachable(body) && !c.literalAccessor(fn) {
		return c.neverT // tsc's mayReturnNever: not an accessor's (void)
	}
	if c.contextualizing[fn] {
		return r // the context's inference is typing this function itself
	}
	c.contextualizing[fn] = true
	ct := c.contextualType(fn)
	delete(c.contextualizing, fn)
	if ct != nil && ct.Flags&Union != 0 {
		ct = c.unionSignatureContext(ct) // a union of signatures: their results' union
	}
	if ct == nil || ct.Flags&Object == 0 || ct.Kind != Function || ct.Result == nil {
		return r
	}
	if r == c.voidT && ct.Result.Flags&Void == 0 && someMember(ct.Result, Undefined) && body != nil && !hasValueReturn(body) {
		return c.undefinedT
	}
	if someMember(ct.Result, Literal) && exprBody != nil {
		return c.TypeOf(exprBody)
	}
	if someMember(ct.Result, Literal) && body != nil {
		// The returned literals the contextual result keeps stay literal.
		var ts []*Type
		ok := true
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
				return false
			case *ast.ReturnStatement:
				if n.Value == nil {
					ok = false
					return false
				}
				t := c.TypeOf(n.Value)
				if c.Unanswered(t) {
					ok = false
					return false
				}
				for _, m := range members(t) {
					if m.Flags&Literal != 0 && !c.literalOfContext(m, ct.Result) {
						ok = false
					}
				}
				ts = append(ts, t)
			}
			return ok
		})
		if ok && len(ts) > 0 {
			return c.in.union(ts...)
		}
	}
	return r
}

func someMember(t *Type, f TypeFlags) bool {
	for _, m := range members(t) {
		if m.Flags&f != 0 {
			return true
		}
	}
	return false
}

func everyMember(t *Type, f TypeFlags) bool {
	for _, m := range members(t) {
		if m.Flags&f == 0 {
			return false
		}
	}
	return true
}

// hasReturn and hasValueReturn report a body's own return statements (not
// a nested function's).
func hasReturn(body *ast.BlockStatement) bool      { return findReturn(body, false) }
func hasValueReturn(body *ast.BlockStatement) bool { return findReturn(body, true) }

func findReturn(body *ast.BlockStatement, withValue bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.ReturnStatement:
			if !withValue || n.Value != nil {
				found = true
			}
		}
		return !found
	})
	return found
}

// widenUnit widens an inferred return type that is a single literal
// (`return 2` is number) and keeps a union of them (`1 | "x"`), as
// TypeScript does without a contextual return type.
func (c *Checker) widenUnit(t *Type) *Type {
	if t.Flags&Literal != 0 {
		return widen(c, t)
	}
	return t
}

// instanceType is the type of an instance of the class sym declares: its
// fields and methods, as declared.
// FieldType is the type of class cd's instance field name, or nil.
func (c *Checker) FieldType(cd *ast.ClassDeclaration, name string) *Type {
	sym := c.symbolOf(cd)
	if sym == nil {
		return nil
	}
	inst := c.instanceType(sym)
	if inst == nil || c.Unanswered(inst) {
		return nil
	}
	if p := inst.Prop(name); p != nil {
		return p.Type
	}
	return nil
}

// untypedFieldType is the type of a field declared with neither a type nor
// an initializer: what the constructor assigns to it, else any.
func (c *Checker) untypedFieldType(decl *ast.ClassDeclaration, name string) *Type {
	if decl.Constructor != nil && decl.Constructor.Body != nil {
		for _, p := range c.constructorFields(decl) {
			if p.Name == name {
				return p.Type
			}
		}
	}
	return c.anyT
}

func (c *Checker) instanceType(sym *binder.Symbol) *Type { return c.instanceOf(sym, nil) }

// classDecl returns the class declaration of sym, or nil.
func classDecl(sym *binder.Symbol) *ast.ClassDeclaration {
	var decl *ast.ClassDeclaration
	for _, d := range sym.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
			decl = cd
		}
	}
	return decl
}

// instanceOf is the instance type of class sym with type arguments args (nil
// for a non-generic class). The class's type parameters name args while its
// members, and its base's type arguments (`extends Base<T[]>`), are typed.
func (c *Checker) instanceOf(sym *binder.Symbol, args []*Type) *Type {
	decl := classDecl(sym)
	if decl == nil {
		return nil
	}
	if n := len(decl.DeclaredTypeParams().Names); len(args) < n && len(decl.DeclaredTypeParams().Defaults) == n {
		// Omitted arguments take their parameters' defaults (`EventEmitter`
		// is `EventEmitter<any>`), each typed with the ones before it.
		full := append([]*Type(nil), args...)
		for i := len(args); i < n; i++ {
			d := decl.DeclaredTypeParams().Defaults[i]
			if d == nil || d.TypeNode() == nil {
				return nil
			}
			pop := c.declEnv(decl.DeclaredTypeParams().Names[:i], full)
			dt := c.typeFromNode(d.TypeNode(), sym.Scope)
			pop()
			if c.Unanswered(dt) {
				return nil
			}
			full = append(full, dt)
		}
		args = full
	}
	if len(decl.DeclaredTypeParams().Names) != len(args) {
		return nil
	}
	generic := len(args) > 0
	if c.tooDeep(sym, args) {
		return nil
	}
	// Interned before its base is typed: a base member naming this class
	// (`child?: Derived`) gets this type, filled in below.
	t, fresh := c.in.nominal(Instance, sym, args)
	if !fresh {
		return t
	}
	c.building[t] = true
	defer delete(c.building, t)
	defer c.nest(sym, args)()
	defer c.declEnv(append(append([]string(nil), decl.DeclaredTypeParams().Names...), "this"), append(append([]*Type(nil), args...), c.thisT))()
	var base *Type
	if decl.BaseClass != "" {
		if base = c.baseInstance(decl, sym); base == nil {
			delete(c.in.byKey, nominalKey(Instance, sym, args))
			return nil
		}
	}
	t.Base = base
	if base != nil {
		t.Props = append(t.Props, base.Props...)
		t.StringIndex, t.NumberIndex = base.StringIndex, base.NumberIndex
		// A base still being typed (a cycle through a member's type) has
		// not got all its members yet.
		t.partial = t.partial || base.partial || c.building[base]
	}
	if c.opts.CompatJS() && !generic && decl.Constructor != nil && !hasInstanceFields(decl) {
		for _, p := range c.constructorFields(decl) {
			t.setProp(p)
		}
	}
	for _, f := range decl.Fields {
		if f.Static {
			continue
		}
		var ft *Type
		switch {
		case f.Type != nil && f.Type.TypeNode() != nil:
			ft = c.typeFromNode(f.Type.TypeNode(), sym.Scope)
		case f.Initializer != nil && !generic:
			// (In a generic class an expression's type could depend on the
			// arguments, and expression types are cached per node.)
			ft = c.widenFrom(f.Initializer, c.TypeOf(f.Initializer))
		case !generic:
			// Neither: the type the constructor assigns it (tsc's inference
			// from constructors), else an implicit any.
			ft = c.untypedFieldType(decl, f.Name)
		default:
			ft = c.unanswered
		}
		if f.Optional && !c.Unanswered(ft) {
			ft = c.in.union(ft, c.missingT) // absent, not an explicit undefined
		}
		if prev := t.Prop(f.Name); prev != nil && prev.Owner == sym {
			continue // a duplicate: the first declaration is the member
		}
		t.setProp(&Property{Name: f.Name, Type: ft, Optional: f.Optional, Readonly: f.Readonly, Visibility: f.Visibility, Owner: sym, Field: true})
	}
	// Methods whose result is declared come first: an inferred result
	// (a generator's yields over `this.next()`) may read them, in any
	// declaration order, as TypeScript's lazy resolution does.
	methods := make([]*ast.FunctionDeclaration, 0, len(decl.Methods))
	for _, m := range decl.Methods {
		if m.ReturnType != nil || len(m.Overloads) > 0 {
			methods = append(methods, m)
		}
	}
	for _, m := range decl.Methods {
		if m.ReturnType == nil && len(m.Overloads) == 0 {
			methods = append(methods, m)
		}
	}
	for _, m := range methods {
		if m.IsStatic {
			continue
		}
		if m.AccessorKind != "" {
			// A getter's type is its result; a setter alone gives its
			// parameter's. The getter wins when both exist.
			at := c.unanswered
			if !m.IsAsync && !m.IsGenerator && (!generic || m.ReturnType != nil || m.AccessorKind == "set") {
				if sig := c.signatureType(m, m.Params, m.ReturnType, m.Body, nil, sym.Scope); !c.Unanswered(sig) {
					switch {
					case m.AccessorKind == "get":
						at = sig.Result
					case len(sig.Params) == 1:
						at = sig.Params[0]
					}
				}
			}
			if prev := t.Prop(m.Name); prev != nil && prev.Accessor {
				// The pair: the getter's type is read, the setter's written.
				get, set := prev.Type, at
				if m.AccessorKind == "get" {
					get, set = at, prev.Type
				}
				np := &Property{Name: m.Name, Type: get, Accessor: true, Visibility: prev.Visibility, WriteVisibility: m.Visibility, Owner: sym}
				if m.AccessorKind == "get" {
					np.Visibility, np.WriteVisibility = m.Visibility, prev.Visibility
				}
				if set != get {
					np.Write = set
				}
				t.setProp(np)
				continue
			}
			t.setProp(&Property{Name: m.Name, Type: at, Accessor: true, Visibility: m.Visibility, WriteVisibility: m.Visibility, Owner: sym})
			continue
		}
		mt := c.unanswered
		if len(m.Overloads) > 0 {
			mt = c.overloadsType(strconv.Itoa(sym.ID)+"."+m.Name+"<"+ids(args), sym.Scope, m.Overloads)
		} else if !generic || m.ReturnType != nil {
			mt = c.signatureType(m, m.Params, m.ReturnType, m.Body, nil, sym.Scope)
		}
		if m.IsOptional && !c.Unanswered(mt) {
			mt = c.in.union(mt, c.missingT) // `m?(): T` is absent unless implemented
		}
		t.setProp(&Property{Name: m.Name, Type: mt, Optional: m.IsOptional, Visibility: m.Visibility, Owner: sym, Decl: m})
	}
	return t
}

// setProp adds p, replacing an inherited property of the same name.
func (t *Type) setProp(p *Property) {
	for i, q := range t.Props {
		if q.Name == p.Name {
			props := append([]*Property(nil), t.Props...)
			props[i] = p
			t.Props = props
			return
		}
	}
	t.Props = append(t.Props, p)
}

// interfaceOf is the type interface sym declares with type arguments args
// (nil for a non-generic interface).
func (c *Checker) interfaceOf(sym *binder.Symbol, args []*Type) (res *Type) {
	var decls []*ast.InterfaceDeclaration
	for _, d := range sym.Declarations {
		if id, ok := d.Node.(*ast.InterfaceDeclaration); ok {
			decls = append(decls, id)
		}
	}
	if len(decls) == 0 {
		return c.unanswered
	}
	if n := len(decls[0].TypeParams); len(args) < n && len(decls[0].TypeParameters) == n {
		// Omitted arguments take their parameters' defaults (`EventEmitter`
		// is `EventEmitter<any>`), each typed with the ones before it.
		full := append([]*Type(nil), args...)
		for i := len(args); i < n; i++ {
			d := decls[0].TypeParameters[i].Default
			if d == nil {
				return c.unanswered
			}
			pop := c.declEnv(decls[0].TypeParams[:i], full)
			dt := c.typeFromNode(d, sym.Scope)
			pop()
			if c.Unanswered(dt) {
				return dt
			}
			full = append(full, dt)
		}
		args = full
	}
	for _, id := range decls {
		if len(id.TypeParams) != len(args) {
			return c.unanswered
		}
		for _, m := range id.Members {
			if ix, index := m.(*ast.IndexSignature); index {
				if kind, _ := indexKeyKind(ix); kind == "" {
					return c.unanswered // a symbol or template key: not modelled
				}
			}
		}
		for _, h := range id.Heritage {
			if _, ok := h.(*ast.TypeReference); !ok {
				return c.unanswered
			}
		}
	}
	if c.tooDeep(sym, args) {
		return c.unanswered
	}
	t, fresh := c.in.nominal(Interface, sym, args)
	if !fresh {
		if c.failedNominal[t] {
			return c.unanswered
		}
		return t
	}
	defer func() {
		if c.Unanswered(res) {
			c.failedNominal[t] = true
		}
	}()
	// Members are resolved eagerly, so mutually recursive generic
	// interfaces instantiate each other combinatorially; past a program-wide
	// count the rest stay unanswered (tsc bounds the same growth with its
	// instantiation count, TS2589).
	if len(args) > 0 {
		c.instantiations++
		if c.instantiations > maxInstantiations {
			return c.unanswered
		}
	}
	c.building[t] = true
	defer delete(c.building, t)
	defer c.nest(sym, args)()
	if c.inLibrary(sym.Scope) {
		// A builtin declaration lists what this compiler implements, not
		// the whole of TypeScript's library type.
		t.partial = true
	}
	// The polymorphic `this` in a member's type is instantiated with the
	// receiver at each access (withThis).
	defer c.declEnv(append(append([]string(nil), decls[0].TypeParams...), "this"), append(append([]*Type(nil), args...), c.thisT))()
	// The bases' members first, in `extends` order; the interface's own
	// replace them. Its call and construct signatures come before the
	// bases'.
	var baseCalls, baseConstructs []*Type
	var baseCallOrigins, baseConstructOrigins []sigOrigin
	defer func() {
		t.Calls = append(t.Calls, baseCalls...)
		t.Constructs = append(t.Constructs, baseConstructs...)
		t.callOrigins = append(t.callOrigins, baseCallOrigins...)
		t.constructOrigins = append(t.constructOrigins, baseConstructOrigins...)
	}()
	for _, id := range decls {
		for _, h := range id.Heritage {
			ref := h.(*ast.TypeReference)
			var bsym *binder.Symbol
			if len(ref.Qualifier) > 0 {
				bsym = c.qualifiedTypeName(ref, sym.Scope) // `extends NodeJS.Dict<T>`
			} else {
				bsym = resolveTypeName(ref.Name, sym.Scope)
			}
			if bsym == nil || bsym == sym {
				return c.unanswered
			}
			// A generic base's arguments, typed in this interface's own
			// environment (`interface Stats extends StatsBase<number>`).
			var bargs []*Type
			for _, a := range ref.TypeArgs {
				at := c.typeFromNode(a, sym.Scope)
				if c.Unanswered(at) {
					return c.unanswered
				}
				bargs = append(bargs, at)
			}
			var bt *Type
			switch {
			case bsym.Flags&binder.Interface != 0:
				bt = c.interfaceOf(bsym, bargs)
			case bsym.Flags&binder.Class != 0 && len(bargs) == 0:
				bt = c.instanceType(bsym)
			}
			if bt == nil || c.Unanswered(bt) {
				return c.unanswered
			}
			for _, p := range bt.Props {
				t.setProp(p)
			}
			if bt.StringIndex != nil {
				t.StringIndex = bt.StringIndex
			}
			if bt.NumberIndex != nil {
				t.NumberIndex = bt.NumberIndex
			}
			t.partial = t.partial || bt.partial || c.building[bt]
			baseCalls = append(baseCalls, bt.Calls...)
			baseConstructs = append(baseConstructs, bt.Constructs...)
			baseCallOrigins = append(baseCallOrigins, originsOf(bt.Calls, bt.callOrigins, bsym.ID)...)
			baseConstructOrigins = append(baseConstructOrigins, originsOf(bt.Constructs, bt.constructOrigins, bsym.ID)...)
		}
	}
	// A member this interface declares overrides a base's of the same name;
	// only its own declarations of a name merge as overloads.
	own := map[string]bool{}
	for di, id := range decls {
		for mi, m := range id.Members {
			switch m := m.(type) {
			case *ast.IndexSignature:
				kind, vt := c.indexSignature(m, sym.Scope)
				if c.Unanswered(vt) {
					return vt
				}
				if kind == "string" {
					t.StringIndex = vt
				} else {
					t.NumberIndex = vt
				}
			case *ast.PropertySignature:
				if m.Computed {
					wk, ok := wellKnownMemberKey(m.Name)
					if !ok {
						t.partial = true
						continue
					}
					cp := *m
					cp.Name, cp.Computed = wk, false
					m = &cp
				}
				ft := c.anyT
				if m.Type != nil {
					ft = c.typeFromNode(m.Type, sym.Scope)
				}
				if m.Optional && !c.Unanswered(ft) {
					ft = c.in.union(ft, c.missingT)
				}
				if m.Accessor != "" {
					if prev := t.Prop(m.Name); prev != nil && prev.Accessor {
						// The pair: the getter's type is read, the setter's
						// written.
						get, set := prev.Type, ft
						if m.Accessor == "get" {
							get, set = ft, prev.Type
						}
						np := &Property{Name: m.Name, Type: get, Accessor: true}
						if set != get {
							np.Write = set
						}
						t.setProp(np)
						continue
					}
					t.setProp(&Property{Name: m.Name, Type: ft, Accessor: true})
					continue
				}
				t.setProp(&Property{Name: m.Name, Type: ft, Optional: m.Optional, Readonly: m.Readonly})
			case *ast.CallSignature, *ast.ConstructSignature:
				key := strconv.Itoa(sym.ID) + ":" + strconv.Itoa(di) + ":" + strconv.Itoa(mi)
				var st *Type
				if cs, ok := m.(*ast.CallSignature); ok {
					st = c.signatureOfNodes(key, cs.TypeParameters, cs.Parameters, cs.Type, sym.Scope)
				} else {
					ks := m.(*ast.ConstructSignature)
					st = c.signatureOfNodes(key, ks.TypeParameters, ks.Parameters, ks.Type, sym.Scope)
				}
				if c.Unanswered(st) {
					return c.unanswered
				}
				o := sigOrigin{sym: sym.ID, decl: di, literal: literalParams(m)}
				if _, ok := m.(*ast.CallSignature); ok {
					t.Calls = append(t.Calls, st)
					t.callOrigins = append(t.callOrigins, o)
				} else {
					t.Constructs = append(t.Constructs, st)
					t.constructOrigins = append(t.constructOrigins, o)
				}
			case *ast.MethodSignature:
				if m.Computed {
					wk, ok := wellKnownMemberKey(m.Name)
					if !ok {
						t.partial = true
						continue
					}
					cp := *m
					cp.Name, cp.Computed = wk, false
					m = &cp
				}
				key := strconv.Itoa(sym.ID) + ":" + strconv.Itoa(di) + ":" + strconv.Itoa(mi)
				mt := c.withThisParam(c.signatureOfNodes(key, m.TypeParameters, m.Parameters, m.Type, sym.Scope), key, m.TypeParameters, m.This, sym.Scope)
				if m.Optional && !c.Unanswered(mt) {
					mt = c.in.union(mt, c.missingT)
				}
				decls := []ast.Node{m}
				ownPrev := own[m.Name]
				own[m.Name] = true
				if prev := t.Prop(m.Name); ownPrev && prev != nil && !c.Unanswered(prev.Type) && !c.Unanswered(mt) && prev.Type.Flags&Object != 0 && prev.Type.Kind == Function && mt.Flags&Object != 0 && mt.Kind == Function && !m.Optional {
					// Overloaded method signatures: the list, in order.
					sigs := append([]*Type(nil), prev.Type.Overloads...)
					if len(sigs) == 0 {
						sigs = []*Type{prev.Type}
					}
					mt = c.in.overloaded(append(sigs, mt))
					decls = append(append([]ast.Node(nil), prev.Decls...), m)
				}
				t.setProp(&Property{Name: m.Name, Type: mt, Optional: m.Optional, Decl: m, Decls: decls})
			}
		}
	}
	return t
}

// signatureOfNodes is the function type a signature member declares: its
// own type parameters are named in the environment while it is typed (key
// makes them distinct), an unannotated parameter is any, and an omitted
// return type is any.
func (c *Checker) signatureOfNodes(key string, tps []*ast.TypeParameter, params []*ast.SignatureParameter, ret ast.TypeNode, scope *binder.Scope) *Type {
	var tpTypes []*Type
	var names []string
	for i, tp := range tps {
		name := tp.Name
		tpTypes = append(tpTypes, c.in.intern("v"+key+":"+strconv.Itoa(i)+":"+name, func() *Type {
			return &Type{Flags: TypeParam, Value: name}
		}))
		names = append(names, name)
	}
	defer c.pushEnv(names, tpTypes)()
	// Each type parameter's `extends` constraint, read once the parameters
	// are named (a constraint may name another: `<K, V extends K>`).
	for i, tp := range tps {
		if tp.Constraint != nil && tpTypes[i].Constraint == nil {
			if ct := c.typeFromNode(tp.Constraint, scope); !c.Unanswered(ct) {
				tpTypes[i].Constraint = ct
			}
		}
		if tp.Default != nil && tpTypes[i].Default == nil {
			if dt := c.typeFromNode(tp.Default, scope); !c.Unanswered(dt) {
				tpTypes[i].Default = dt
			}
		}
	}
	var ps []*Type
	var opts []bool
	rest := false
	for _, p := range params {
		pt := c.anyT
		if p.Type != nil {
			pt = c.typeFromNode(p.Type, scope)
		}
		if c.Unanswered(pt) {
			return pt
		}
		if p.Optional {
			pt = c.in.union(pt, c.undefinedT)
		}
		ps = append(ps, pt)
		opts = append(opts, p.Optional)
		rest = rest || p.Rest
	}
	r := c.anyT
	var pred *Predicate
	if ret != nil {
		if tp, ok := ret.(*ast.TypePredicate); ok {
			var pt *Type
			if tp.Type != nil {
				if pt = c.typeFromNode(tp.Type, scope); c.Unanswered(pt) {
					return pt
				}
			}
			index := -1
			if !tp.This {
				index = -2
				for i, p := range params {
					if p.Name == tp.ParameterName {
						index = i
					}
				}
				if index < 0 {
					return c.unanswered
				}
			}
			pred = &Predicate{Index: index, Type: pt, Asserts: tp.Asserts}
			r = c.boolT
			if tp.Asserts {
				r = c.voidT
			}
		} else {
			r = c.typeFromNode(ret, scope)
		}
	}
	if c.Unanswered(r) {
		return r
	}
	pnames := make([]string, len(params))
	for i, p := range params {
		pnames[i] = p.Name
	}
	return c.in.withParamNames(c.in.function(ps, opts, rest, r, pred, tpTypes), pnames)
}

// lookupName resolves name as any symbol, type or value, from scope out.
func lookupName(name string, scope *binder.Scope) *binder.Symbol {
	for s := scope; s != nil; s = s.Parent {
		if sym := s.Symbols.Get(name); sym != nil {
			return sym
		}
	}
	return nil
}

// resolveTypeName finds the type symbol name denotes from scope outwards.
func resolveTypeName(name string, scope *binder.Scope) *binder.Symbol {
	for s := scope; s != nil; s = s.Parent {
		if sym := s.Symbols.Get(name); sym != nil && sym.Flags&binder.Type != 0 {
			return sym
		}
	}
	return nil
}

// nativeWidths are this compiler's JSDoc numeric widths.
var nativeWidths = map[string]struct {
	bits   int
	signed bool
}{
	"int8": {8, true}, "int16": {16, true}, "int32": {32, true}, "int64": {64, true},
	"uint8": {8, false}, "uint16": {16, false}, "uint32": {32, false}, "uint64": {64, false},
}

// TypeFromNode is the type a type annotation denotes, its names resolved
// from scope.
func (c *Checker) TypeFromNode(n ast.TypeNode, scope *binder.Scope) *Type {
	return c.typeFromNode(n, scope)
}

// TypeFromNodeWithParams is TypeFromNode for a node inside a generic
// declaration (a generic alias's body): each of its type parameter names is
// a type parameter, as while the declaration is instantiated.
func (c *Checker) TypeFromNodeWithParams(n ast.TypeNode, scope *binder.Scope, names []string) *Type {
	tps := make([]*Type, len(names))
	for i, name := range names {
		name := name
		tps[i] = c.in.intern("decl:"+name, func() *Type { return &Type{Flags: TypeParam, Value: name} })
	}
	defer c.declEnv(names, tps)()
	return c.typeFromNode(n, scope)
}

func (c *Checker) typeFromNode(n ast.TypeNode, scope *binder.Scope) *Type {
	switch n := n.(type) {
	case *ast.TypeQuery:
		return c.typeQueryType(n, scope)
	case *ast.KeywordType:
		switch n.Keyword {
		case "any":
			return c.anyT
		case "unknown":
			return c.unknownT
		case "string":
			return c.strT
		case "number":
			return c.numT
		case "boolean":
			return c.boolT
		case "bigint":
			return c.bigintT
		case "symbol":
			return c.symT
		case "void":
			return c.voidT
		case "undefined":
			return c.undefinedT
		case "null":
			return c.nullT
		case "never":
			return c.neverT
		case "object":
			return c.objectT
		case "this":
			// The polymorphic `this` of the class or interface being typed.
			if t := c.lookupEnv("this"); t != nil {
				return t
			}
			return c.unanswered
		}
	case *ast.LiteralType:
		switch n.Kind {
		case "string":
			return c.in.literal(StringLiteral, n.Value)
		case "number":
			return c.in.literal(NumberLiteral, canonicalNumber(n.Value))
		case "boolean":
			if n.Value == "true" {
				return c.trueT
			}
			return c.falseT
		}
	case *ast.TemplateLiteralType:
		return c.templateLiteralType(n, scope)
	case *ast.TypeOperator:
		if n.Operator == "keyof" {
			t := c.typeFromNode(n.Type, scope)
			switch {
			case c.Unanswered(t):
				return t
			case c.generic(t):
				return c.deferredType(n, scope)
			}
			return c.keyofType(t)
		}
		if n.Operator == "unique" {
			return c.symT // `unique symbol`: a symbol, its identity not modelled
		}
		if n.Operator == "readonly" {
			// `readonly T[]` is ReadonlyArray<T>; a readonly tuple is the
			// tuple (its elements' writability is not modelled).
			inner := c.typeFromNode(n.Type, scope)
			if c.Unanswered(inner) || inner.Flags&Object == 0 {
				return inner
			}
			if inner.Kind == Array && c.b.Globals != nil {
				if sym := c.b.Globals.Symbols.Get("ReadonlyArray"); sym != nil && sym.Flags&binder.Interface != 0 {
					if ro := c.interfaceOf(sym, []*Type{inner.Elem}); !c.Unanswered(ro) {
						return ro
					}
				}
			}
			return inner
		}
		return c.unanswered
	case *ast.ParenthesizedType:
		return c.typeFromNode(n.Type, scope)
	case *ast.IndexedAccessType:
		o := c.typeFromNode(n.ObjectType, scope)
		k := c.typeFromNode(n.IndexType, scope)
		switch {
		case c.Unanswered(o) || c.Unanswered(k):
			return c.unanswered
		case c.generic(o) || c.generic(k):
			return c.deferredType(n, scope)
		}
		return c.indexedAccess(o, k)
	case *ast.MappedType:
		return c.mappedType(n, scope)
	case *ast.ConditionalType:
		return c.conditionalType(n, scope)
	case *ast.InferType:
		if t := c.lookupEnv(n.Name); t != nil {
			return t
		}
		return c.unanswered
	case *ast.ArrayType:
		e := c.typeFromNode(n.ElementType, scope)
		if c.Unanswered(e) {
			return e
		}
		return c.in.array(e)
	case *ast.UnionType:
		var ms []*Type
		for _, m := range n.Types {
			t := c.typeFromNode(m, scope)
			if c.Unanswered(t) {
				return t
			}
			ms = append(ms, t)
		}
		return c.in.union(ms...)
	case *ast.IntersectionType:
		// An intersection of object types is one object with every member's
		// properties (`StatOptions & { bigint?: false }`). A property two
		// members declare has the narrower type, when one is assignable to the
		// other. Anything else is not modelled yet.
		var props []*Property
		partial := false
		seen := map[string]int{}
		var parts []*Type
		var thisMarker *Type
		emptyObj, object := false, false
		for _, m := range n.Types {
			t := c.typeFromNode(m, scope)
			if c.Unanswered(t) {
				return t
			}
			switch {
			case t.Flags&Object != 0 && t.Kind == Interface && len(t.TypeArgs) == 1 && t.Symbol != nil && t.Symbol.Name == "ThisType" && c.inLibrary(t.Symbol.Scope):
				thisMarker = t.TypeArgs[0] // `ThisType<T>`: a marker, no members
				continue
			case t.Flags&NonPrimitive != 0:
				object = true // `object & {…}`: the object member decides
				continue
			case t.Flags == Object && t.Kind == Anonymous && len(t.Props) == 0 && len(t.Calls) == 0 && len(t.Constructs) == 0 &&
				t.StringIndex == nil && t.NumberIndex == nil && !t.partial:
				emptyObj = true // `T & {}`: T without null and undefined
				continue
			}
			parts = append(parts, t)
		}
		if len(parts) == 1 && (emptyObj || object) {
			t := parts[0]
			if c.generic(t) {
				return c.deferredType(n, scope)
			}
			if emptyObj {
				return c.filter(t, func(m *Type) bool { return m.Flags&Nullish == 0 })
			}
			if t.Flags&Object != 0 {
				return t
			}
			return c.unanswered
		}
		if len(parts) == 0 {
			if object {
				return c.objectT
			}
			return c.in.object(nil)
		}
		if r := c.refinedInstance(parts); r != nil {
			return r
		}
		// A branded primitive (`string & { __brand: "id" }`): one primitive
		// member, the others object types.
		var base *Type
		var objs []*Type
		for _, t := range parts {
			switch {
			case t.Flags&Union == 0 && t.Brand == nil && t.Flags&(String|Number|Boolean|BigInt|StringLiteral|NumberLiteral|BooleanLiteral|BigIntLiteral|Int|Float32|ESSymbol) != 0 && base == nil:
				base = t
			case t.Brand != nil && base == nil:
				base = t.BrandBase // a brand of a brand: the brands merge
				objs = append(objs, t.Brand)
			case t.Flags == Object && (t.Kind == Anonymous || t.Kind == Interface) && len(t.Calls) == 0 && len(t.Constructs) == 0:
				objs = append(objs, t)
			default:
				base, objs = nil, nil
			}
			if base == nil && objs == nil {
				break
			}
		}
		if base != nil && len(objs) > 0 {
			brand := objs[0]
			if len(objs) > 1 {
				var bp []*Property
				named := map[string]bool{}
				for _, o := range objs {
					for _, p := range o.Props {
						if !named[p.Name] {
							named[p.Name] = true
							bp = append(bp, p)
						}
					}
				}
				brand = c.in.object(bp)
			}
			return c.in.branded(base, brand)
		}
		for _, t := range parts {
			if t.Flags != Object || (t.Kind != Anonymous && t.Kind != Interface) || len(t.Calls) > 0 || len(t.Constructs) > 0 || len(t.TypeArgs) > 0 || c.building[t] ||
				t.StringIndex != nil || t.NumberIndex != nil {
				return c.unanswered
			}
			partial = partial || nominalOpen(t) // a library or DOM name's members stay open
			for _, p := range t.Props {
				i, dup := seen[p.Name]
				if !dup {
					seen[p.Name] = len(props)
					props = append(props, p)
					continue
				}
				q := props[i]
				if p.Accessor || q.Accessor {
					return c.unanswered
				}
				np := *q
				switch {
				case c.assignable(p.Type, q.Type):
					np.Type = p.Type
				case !c.assignable(q.Type, p.Type):
					return c.unanswered
				}
				np.Optional = p.Optional && q.Optional
				np.Readonly = p.Readonly || q.Readonly
				props[i] = &np
			}
		}
		if thisMarker != nil {
			return c.in.withLiteralThis(c.in.objectOf(props, partial, true), thisMarker)
		}
		return c.in.objectOf(props, partial, true)
	case *ast.TupleType:
		// A tuple of one rest element over an array (`[...args: any[]]`) is
		// that array, as tsc normalizes it.
		if len(n.Elements) == 1 {
			var inner ast.TypeNode
			switch m := n.Elements[0].(type) {
			case *ast.NamedTupleMember:
				if m.Rest {
					inner = m.Type
				}
			case *ast.RestType:
				inner = m.Type
			}
			if inner != nil {
				if at := c.typeFromNode(inner, scope); c.Unanswered(at) || at.Flags&Object != 0 && at.Kind == Array {
					return at
				}
				return c.unanswered
			}
		}
		var es []*Type
		var labels []string
		opt := 0
		for _, e := range n.Elements {
			optional := false
			switch m := e.(type) {
			case *ast.NamedTupleMember:
				if !m.Rest {
					e, optional = m.Type, m.Optional
					labels = append(labels, m.Name)
				}
			case *ast.OptionalType:
				e, optional = m.Type, true
			}
			t := c.typeFromNode(e, scope)
			if c.Unanswered(t) {
				return t
			}
			if optional {
				t = c.in.union(t, c.undefinedT) // strict: `T?` holds undefined
				opt++
			} else if opt > 0 {
				return c.unanswered // a required element after an optional one
			}
			es = append(es, t)
		}
		return c.in.tupleLabelled(es, opt, labels)
	case *ast.ConstructorType:
		// `new (…) => R`: an object type whose one member is that construct
		// signature, as tsc models it.
		if len(n.TypeParameters) > 0 {
			break
		}
		sig := c.typeFromNode(&ast.FunctionType{Parameters: n.Parameters, Type: n.Type, Range: n.Range}, scope)
		if c.Unanswered(sig) || sig.Kind != Function {
			return c.unanswered
		}
		return c.in.withSignatures(c.in.object(nil), nil, []*Type{sig})
	case *ast.FunctionType:
		if len(n.TypeParameters) > 0 {
			break
		}
		var ps []*Type
		var opts []bool
		rest := false
		for _, p := range n.Parameters {
			t := c.typeFromNode(p.Type, scope)
			if c.Unanswered(t) {
				return t
			}
			if p.Optional {
				t = c.in.union(t, c.undefinedT) // strict: `a?: T` is T | undefined
			}
			ps = append(ps, t)
			opts = append(opts, p.Optional)
			rest = rest || p.Rest
		}
		// A guard's result: `(v: T) => v is S`, `(v) => asserts v is S`.
		if tp, ok := n.Type.(*ast.TypePredicate); ok && !tp.This {
			var pt *Type
			if tp.Type != nil {
				if pt = c.typeFromNode(tp.Type, scope); c.Unanswered(pt) {
					return pt
				}
			}
			for i, p := range n.Parameters {
				if p.Name == tp.ParameterName {
					r := c.boolT
					if tp.Asserts {
						r = c.voidT
					}
					return c.in.function(ps, opts, rest, r, &Predicate{Index: i, Type: pt, Asserts: tp.Asserts}, nil)
				}
			}
			return c.unanswered
		}
		r := c.typeFromNode(n.Type, scope)
		if c.Unanswered(r) {
			return r
		}
		names := make([]string, len(n.Parameters))
		for i, p := range n.Parameters {
			names[i] = p.Name
		}
		fn := c.in.withParamNames(c.in.function(ps, opts, rest, r, nil, nil), names)
		if n.This != nil {
			tt := c.typeFromNode(n.This, scope)
			if c.Unanswered(tt) {
				return tt
			}
			if tt.Flags&Void == 0 {
				fn = c.in.withThis(fn, tt)
			}
		}
		return fn
	case *ast.TypeLiteral:
		var props []*Property
		var strIdx, numIdx *Type
		var calls, constructs []*Type
		partial := false
		for _, m := range n.Members {
			if ix, ok := m.(*ast.IndexSignature); ok {
				kind, vt := c.indexSignature(ix, scope)
				switch {
				case c.Unanswered(vt):
					return vt
				case kind == "string":
					strIdx = vt
				case kind == "number":
					numIdx = vt
				default:
					return c.unanswered
				}
				continue
			}
			// A method, call or construct signature (`declare var URL:
			// { new (url: string): URL; parse(…): URL | null }`), as an
			// interface's members are.
			switch sm := m.(type) {
			case *ast.MethodSignature:
				if sm.Computed {
					wk, ok := wellKnownMemberKey(sm.Name)
					if !ok {
						return c.unanswered
					}
					cp := *sm
					cp.Name, cp.Computed = wk, false
					sm = &cp
				}
				// A member the checker does not model types as unanswered,
				// as an interface's does; the rest of the type stays known.
				mt := c.withThisParam(c.signatureOfNodes(fmt.Sprintf("lit%p", sm), sm.TypeParameters, sm.Parameters, sm.Type, scope), fmt.Sprintf("lit%p", sm), sm.TypeParameters, sm.This, scope)
				if sm.Optional && !c.Unanswered(mt) {
					mt = c.in.union(mt, c.missingT)
				}
				props = append(props, &Property{Name: sm.Name, Type: mt, Optional: sm.Optional, Decl: sm, Decls: []ast.Node{sm}})
				continue
			case *ast.CallSignature:
				st := c.signatureOfNodes(fmt.Sprintf("lit%p", sm), sm.TypeParameters, sm.Parameters, sm.Type, scope)
				if c.Unanswered(st) {
					partial = true // a signature the checker does not model
					continue
				}
				calls = append(calls, st)
				continue
			case *ast.ConstructSignature:
				st := c.signatureOfNodes(fmt.Sprintf("lit%p", sm), sm.TypeParameters, sm.Parameters, sm.Type, scope)
				if c.Unanswered(st) {
					partial = true
					continue
				}
				constructs = append(constructs, st)
				continue
			}
			p, ok := m.(*ast.PropertySignature)
			if !ok || p.Computed {
				return c.unanswered
			}
			t := c.anyT // `name;` is any
			if p.Type != nil {
				t = c.typeFromNode(p.Type, scope)
			}
			if p.Optional && !c.Unanswered(t) {
				t = c.in.union(t, c.missingT)
			}
			if p.Accessor != "" {
				// A get/set pair: the getter's type is read, the setter's
				// written.
				merged := false
				for i, q := range props {
					if q.Name == p.Name && q.Accessor {
						get, set := q.Type, t
						if p.Accessor == "get" {
							get, set = t, q.Type
						}
						np := &Property{Name: p.Name, Type: get, Accessor: true}
						if set != get {
							np.Write = set
						}
						props[i] = np
						merged = true
					}
				}
				if !merged {
					props = append(props, &Property{Name: p.Name, Type: t, Accessor: true})
				}
				continue
			}
			props = append(props, &Property{Name: p.Name, Type: t, Optional: p.Optional, Readonly: p.Readonly})
		}
		return c.in.withSignatures(c.in.indexedOn(c.in.objectOf(props, partial, false), strIdx, numIdx), calls, constructs)
	case *ast.ImportType:
		// `import("tty").WriteStream`: the builtin module's declaration, by
		// the merged name the program gives it (when the program includes
		// the module).
		if ref := c.importTypeRef(n); ref != nil {
			return c.typeFromNode(ref, c.b.Module)
		}
		return c.unanswered
	case *ast.TypeReference:
		if len(n.Qualifier) > 0 {
			// `NS.T` / `NS.T<A>`: T among the namespace's own declarations.
			if sym := c.qualifiedTypeName(n, scope); sym != nil {
				var args []*Type
				for _, a := range n.TypeArgs {
					at := c.typeFromNode(a, scope)
					if c.Unanswered(at) {
						return at
					}
					args = append(args, at)
				}
				switch {
				case sym.Flags&binder.Interface != 0:
					return c.interfaceOf(sym, args)
				case sym.Flags&binder.TypeAlias != 0:
					return c.aliasOf(sym, args)
				case sym.Flags&binder.Class != 0:
					if t := c.instanceOf(sym, args); t != nil {
						return t
					}
				case sym.Flags&binder.Enum != 0 && len(args) == 0:
					return c.enumType(sym)
				}
			}
			break
		}
		if len(n.TypeArgs) == 0 {
			if sym := resolveTypeName(n.Name, scope); sym != nil && sym.Flags&binder.TypeParameter != 0 {
				// A class's parameter, in an instantiation's members: its
				// argument (instanceOf binds them in the environment).
				if _, isClass := sym.Declarations[0].Node.(*ast.ClassDeclaration); isClass {
					if r := c.lookupEnv(n.Name); r != nil {
						return r
					}
				}
				tp := c.typeParamType(sym)
				if r := c.lookupSubst(tp); r != nil {
					return r
				}
				return tp
			}
		}
		if w, ok := nativeWidths[n.Name]; ok && len(n.TypeArgs) == 0 {
			return c.in.intType(w.bits, w.signed)
		}
		switch n.Name {
		case "float64":
			return c.numT
		case "float32":
			return c.in.intrinsic(Float32)
		case "Array":
			if len(n.TypeArgs) == 1 {
				e := c.typeFromNode(n.TypeArgs[0], scope)
				if c.Unanswered(e) {
					return e
				}
				return c.in.array(e)
			}
		}
		sym := resolveTypeName(n.Name, scope)
		if sym == nil {
			if s := c.builtinImport(n.Name); s != nil && s.Flags&binder.Type != 0 {
				sym = s
			}
		}
		if len(n.TypeArgs) == 0 && (sym == nil || sym.Flags&binder.TypeParameter == 0) {
			if t := c.lookupEnv(n.Name); t != nil {
				return t // a generic class's, interface's or alias's parameter
			}
		}
		var args []*Type
		for _, a := range n.TypeArgs {
			at := c.typeFromNode(a, scope)
			if c.Unanswered(at) {
				return at
			}
			args = append(args, at)
		}
		switch {
		case sym == nil:
		case sym.Flags&binder.Class != 0:
			if t := c.instanceOf(sym, args); t != nil {
				return t
			}
		case sym.Flags&binder.Interface != 0:
			return c.interfaceOf(sym, args)
		case sym.Flags&binder.TypeAlias != 0:
			return c.aliasOf(sym, args)
		case sym.Flags&binder.Enum != 0 && len(args) == 0:
			return c.enumType(sym)
		}
	}
	return c.unanswered
}

// aliasOf is the type alias sym names with type arguments args (nil for a
// non-generic alias), memoised per argument list.
func (c *Checker) aliasOf(sym *binder.Symbol, args []*Type) *Type {
	key := strconv.Itoa(sym.ID) + "<" + ids(args)
	if t, ok := c.aliases[key]; ok {
		return t
	}
	if c.aliasing[key] || c.tooDeep(sym, args) {
		return c.unanswered // a recursive alias
	}
	if rt, ok := c.recursiveLiteralAlias(sym, args, key); ok {
		return rt
	}
	c.aliasing[key] = true
	defer c.nest(sym, args)()
	t := c.unanswered
	for _, d := range sym.Declarations {
		if ad, ok := d.Node.(*ast.TypeAliasDeclaration); ok && len(args) < len(ad.TypeParams) && len(ad.TypeParameters) == len(ad.TypeParams) {
			// Omitted arguments take their parameters' defaults (`type
			// ArrayBufferView<T = ArrayBufferLike>`), each typed with the
			// ones before it, as interfaceOf fills them.
			full := append([]*Type(nil), args...)
			for i := len(args); i < len(ad.TypeParams); i++ {
				dn := ad.TypeParameters[i].Default
				if dn == nil {
					break
				}
				pop := c.declEnv(ad.TypeParams[:i], full)
				dt := c.typeFromNode(dn, sym.Scope)
				pop()
				if c.Unanswered(dt) {
					break
				}
				full = append(full, dt)
			}
			if len(full) == len(ad.TypeParams) {
				args = full
			}
		}
		if ad, ok := d.Node.(*ast.TypeAliasDeclaration); ok && len(ad.TypeParams) == len(args) && ad.Type != nil && ad.Type.TypeNode() != nil {
			pop := c.declEnv(ad.TypeParams, args)
			t = c.typeFromNode(ad.Type.TypeNode(), sym.Scope)
			pop()
		}
	}
	delete(c.aliasing, key)
	c.aliases[key] = t
	return t
}

// recursiveLiteralAlias types a non-generic alias of an object literal that
// names itself (`type Cons = { tail: Cons | null; head: number }`): the
// alias's own object type stands for the self-reference, registered before
// the members are typed, as an interface's is (tsc's recursive anonymous
// type, printed by the alias's name).
func (c *Checker) recursiveLiteralAlias(sym *binder.Symbol, args []*Type, key string) (*Type, bool) {
	if len(args) > 0 || len(sym.Declarations) != 1 {
		return nil, false
	}
	ad, ok := sym.Declarations[0].Node.(*ast.TypeAliasDeclaration)
	if !ok || len(ad.TypeParams) > 0 || ad.Type == nil || sym.Scope == nil || sym.Scope.Kind != binder.ModuleScope {
		return nil, false // inside a generic body it may name the body's parameters
	}
	lit, ok := ad.Type.TypeNode().(*ast.TypeLiteral)
	if !ok || !namesType(lit, sym.Name) {
		return nil, false
	}
	t, fresh := c.in.nominal(Anonymous, sym, nil)
	if !fresh {
		return t, true
	}
	c.aliases[key] = t
	built := c.typeFromNode(lit, sym.Scope)
	if c.Unanswered(built) || built.Flags&Object == 0 {
		c.aliases[key] = c.unanswered
		return c.unanswered, true
	}
	t.Props, t.StringIndex, t.NumberIndex = built.Props, built.StringIndex, built.NumberIndex
	t.Calls, t.Constructs, t.partial = built.Calls, built.Constructs, built.partial
	return t, true
}

// patternAnnotation is a destructuring declaration's declared type (`const
// { a }: T = …`): the source its bindings are typed from; nil when absent.
func (c *Checker) patternAnnotation(ta *ast.TypeAnnotation) *Type {
	if ta == nil || ta.TypeNode() == nil {
		return nil
	}
	return c.typeFromNode(ta.TypeNode(), c.b.Module)
}

// AliasType is the type a non-generic type alias names.
func (c *Checker) AliasType(sym *binder.Symbol) *Type { return c.aliasOf(sym, nil) }

// namesType reports whether a type node mentions the unqualified type name.
func namesType(n ast.Node, name string) bool {
	found := false
	ast.Inspect(n, func(m ast.Node) bool {
		if ref, ok := m.(*ast.TypeReference); ok && len(ref.Qualifier) == 0 && ref.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// pushEnv names args by the type parameter names while a generic
// declaration's body is typed; the returned function pops it.
func (c *Checker) pushEnv(names []string, args []*Type) func() {
	if len(names) == 0 {
		return func() {}
	}
	env := map[string]*Type{}
	for i, n := range names {
		if i < len(args) {
			env[n] = args[i]
		}
	}
	c.env = append(c.env, env)
	return func() { c.env = c.env[:len(c.env)-1] }
}

// maxNesting bounds how deeply one generic declaration is instantiated
// inside its own members: `interface I<T> { x: I<T[]> }` names an
// ever-larger instantiation, which members typed eagerly would follow
// without end (TypeScript resolves members lazily and caps instantiation
// depth). The instantiation past the bound is unanswered.
const maxNesting = 5

// tooDeep reports whether instantiating sym with args would pass maxNesting.
// maxInstantiations bounds the generic interface instantiations one
// program makes (interfaceOf).
const maxInstantiations = 100000

func (c *Checker) tooDeep(sym *binder.Symbol, args []*Type) bool {
	return len(args) > 0 && c.nesting[sym] >= maxNesting
}

// nest counts one instantiation of sym in progress; the returned function
// ends it.
func (c *Checker) nest(sym *binder.Symbol, args []*Type) func() {
	if len(args) == 0 {
		return func() {}
	}
	c.nesting[sym]++
	return func() { c.nesting[sym]-- }
}

// declEnv is pushEnv for a class, interface or alias declaration: its
// members see its own type parameters only, never those of the declaration
// that named it (a base class's field `x: T` is the base's T).
func (c *Checker) declEnv(names []string, args []*Type) func() {
	saved := c.env
	c.env = nil
	pop := c.pushEnv(names, args)
	return func() {
		pop()
		c.env = saved
	}
}

func (c *Checker) lookupEnv(name string) *Type {
	for i := len(c.env) - 1; i >= 0; i-- {
		if t, ok := c.env[i][name]; ok {
			return t
		}
	}
	return nil
}

// classTypeParam is a generic class's type parameter as a type, while its
// type arguments are being inferred from a `new`.
func (c *Checker) classTypeParam(sym *binder.Symbol, name string) *Type {
	t, fresh := c.in.intern2("q"+strconv.Itoa(sym.ID)+":"+name, func() *Type { return &Type{Flags: TypeParam, Value: name} })
	if !fresh {
		return t
	}
	// Its `extends` constraint and `= T` default.
	if decl := classDecl(sym); decl != nil {
		for i, n := range decl.DeclaredTypeParams().Names {
			if n != name {
				continue
			}
			read := func(as []*ast.TypeAnnotation) *Type {
				if i >= len(as) || as[i] == nil || as[i].TypeNode() == nil {
					return nil
				}
				if rt := c.typeFromNode(as[i].TypeNode(), sym.Scope); !c.Unanswered(rt) {
					return rt
				}
				return nil
			}
			t.Constraint = read(decl.DeclaredTypeParams().Constraints)
			t.Default = read(decl.DeclaredTypeParams().Defaults)
		}
	}
	return t
}

// constructorType is the signature `new C(…)` calls: the constructor's
// parameters (a class without one inherits its base's, instantiated by the
// `extends` clause's type arguments), returning the instance, generic over
// the class's type parameters. nil when the class is not modelled.
func (c *Checker) constructorType(sym *binder.Symbol) *Type {
	decl := classDecl(sym)
	if decl == nil || c.ctorChain[sym] {
		return nil // not a class, or a circular base
	}
	var tps []*Type
	for _, n := range decl.DeclaredTypeParams().Names {
		tps = append(tps, c.classTypeParam(sym, n))
	}
	var inst *Type
	if len(tps) == 0 {
		if inst = c.instanceOf(sym, nil); inst == nil {
			return nil
		}
	} else {
		inst = c.anyT // replaced by the instantiated instance at the call
	}
	pop := c.declEnv(decl.DeclaredTypeParams().Names, tps)
	defer pop()
	if decl.Constructor == nil {
		if decl.BaseClass == "" {
			return c.in.function(nil, nil, false, inst, nil, tps)
		}
		bsym := resolveTypeName(decl.BaseClass, sym.Scope)
		if bsym == nil || bsym.Flags&binder.Class == 0 {
			return c.inheritedConstructs(decl, sym, inst, tps)
		}
		c.ctorChain[sym] = true
		bctor := c.constructorType(bsym)
		delete(c.ctorChain, sym)
		bargs, ok := c.baseTypeArgs(decl, sym)
		if bctor != nil && ok {
			bargs = c.withDefaultTypeArgs(bargs, bctor.TypeParams)
		}
		if bctor == nil || !ok || len(bargs) != len(bctor.TypeParams) {
			return nil
		}
		m := map[*Type]*Type{}
		for i, tp := range bctor.TypeParams {
			m[tp] = bargs[i]
		}
		inherit := func(b *Type) *Type {
			ps := make([]*Type, len(b.Params))
			for i, p := range b.Params {
				ps[i] = c.instantiate(p, m)
			}
			return c.in.function(ps, b.optionals, b.restParam, inst, nil, tps)
		}
		if len(bctor.Overloads) > 0 {
			// The base's overloads, each (`extends Array<number>`'s).
			sigs := make([]*Type, len(bctor.Overloads))
			for i, o := range bctor.Overloads {
				sigs[i] = inherit(o)
			}
			return c.in.overloaded(sigs)
		}
		return inherit(bctor)
	}
	if ovs := decl.Constructor.Overloads; len(ovs) > 0 {
		// Overloaded: `new` sees the signatures, not the implementation.
		sigs := make([]*Type, 0, len(ovs))
		for _, o := range ovs {
			sig := c.signatureType(o, o.Params, nil, nil, nil, sym.Scope)
			if c.Unanswered(sig) {
				return nil
			}
			sigs = append(sigs, c.in.function(sig.Params, sig.optionals, sig.restParam, inst, nil, tps))
		}
		if len(sigs) == 1 {
			return sigs[0]
		}
		return c.in.overloaded(sigs)
	}
	sig := c.signatureType(decl.Constructor, decl.Constructor.Params, nil, nil, nil, sym.Scope)
	if c.Unanswered(sig) {
		return nil
	}
	return c.in.function(sig.Params, sig.optionals, sig.restParam, inst, nil, tps)
}

// buildingMember types the non-static method name of class instance t
// while t is still being built (a field initializer's `this.#m()` before
// the methods are typed), as TypeScript resolves a member on demand.
func (c *Checker) buildingMember(t *Type, name string) {
	if t.Kind != Instance || t.Symbol == nil || len(t.TypeArgs) > 0 {
		return
	}
	decl := classDecl(t.Symbol)
	if decl == nil {
		return
	}
	for _, m := range decl.Methods {
		if m.IsStatic || m.AccessorKind != "" || m.Name != name || len(m.Overloads) > 0 || m.IsOptional {
			continue
		}
		key := memberKey{t, name}
		if c.memberBusy[key] {
			return
		}
		c.memberBusy[key] = true
		defer delete(c.memberBusy, key)
		mt := c.signatureType(m, m.Params, m.ReturnType, m.Body, nil, t.Symbol.Scope)
		if !c.Unanswered(mt) && t.Prop(name) == nil {
			t.setProp(&Property{Name: m.Name, Type: mt, Visibility: m.Visibility, Owner: t.Symbol, Decl: m})
		}
		return
	}
}

// thisMemberAhead answers `this.m()`'s callee, an annotated method of the
// enclosing non-generic class, before the class's instance type exists:
// TypeScript resolves the one member on demand, so a method body read while
// another member's return type is inferred (a generator's yields reading a
// binding initialized from `this.next()`) is no cycle.
func (c *Checker) thisMemberAhead(th *ast.ThisExpression, e *ast.MemberExpression) (*Type, bool) {
	var cls *ast.ClassDeclaration
	for n := c.parentOf(th); n != nil && cls == nil; n = c.parentOf(n) {
		switch x := n.(type) {
		case *ast.ArrowFunction:
			continue
		case *ast.FunctionDeclaration:
			cd, ok := c.parentOf(x).(*ast.ClassDeclaration)
			if !ok || x.IsStatic {
				return nil, false
			}
			cls = cd
		case *ast.FunctionExpression, *ast.ObjectLiteral, *ast.ClassDeclaration, *ast.ClassExpression:
			return nil, false
		}
	}
	if cls == nil || len(cls.DeclaredTypeParams().Names) > 0 {
		return nil, false
	}
	sym := c.symbolOf(cls)
	if sym == nil || c.in.byKey[nominalKey(Instance, sym, nil)] != nil {
		return nil, false // built (or being built): the ordinary path
	}
	for _, m := range cls.Methods {
		if m.Name != e.Property || m.IsStatic || m.AccessorKind != "" || m.ReturnType == nil || len(m.Overloads) > 0 || m.IsOptional || len(m.TypeParams) > 0 || m.Visibility == "private" {
			continue
		}
		mt := c.signatureType(m, m.Params, m.ReturnType, m.Body, nil, sym.Scope)
		if c.Unanswered(mt) {
			return nil, false
		}
		c.memberDecls[e] = &Property{Name: m.Name, Type: mt, Visibility: m.Visibility, Owner: sym, Decl: m}
		return mt, true
	}
	return nil, false
}

// memberKey names one member of a type, for buildingMember's guard.
type memberKey struct {
	t    *Type
	name string
}

// classObject is a class's value, TypeScript's `typeof C`: its static
// fields, methods and accessors (a base class's too), its `prototype`, and
// its construct signature.
func (c *Checker) classObject(sym *binder.Symbol, decl *ast.ClassDeclaration) *Type {
	var props []*Property
	partial := false
	add := func(p *Property) {
		for _, q := range props {
			if q.Name == p.Name {
				return // a duplicate, or an inherited static the class redeclares
			}
		}
		props = append(props, p)
	}
	// A static initializer or method body may read the class's own
	// statics (`static f = this.g`, `return C.p`): while they are typed,
	// the class's value is its members so far, as a partial object, rather
	// than a cycle. Each member typed joins the snapshot.
	// A base class's statics are the derived class's too (after its own,
	// which redeclare them), a builtin base's its constructor value's
	// (`Counted.resolve` of `extends Promise`). They are typed first, so the derived class's static
	// bodies read them (`return S.base`).
	var inherited []*Property
	if decl.BaseClass != "" {
		bsym := resolveTypeName(decl.BaseClass, sym.Scope)
		if bt := (*Type)(nil); bsym != nil && bsym.Flags&binder.Class != 0 {
			if bt = c.typeOfSymbol(bsym); bt.Flags&Object != 0 && !c.Unanswered(bt) {
				for _, p := range bt.Props {
					if p.Name != "prototype" {
						inherited = append(inherited, p)
					}
				}
				partial = partial || bt.partial
			} else {
				partial = true
			}
		} else if ps := c.builtinBaseStatics(decl, sym); ps != nil {
			inherited = ps
		} else {
			partial = true
		}
	}
	snapshot := func() {}
	if c.resolving[sym] {
		snapshot = func() {
			ps := append([]*Property(nil), props...)
		base:
			for _, p := range inherited {
				for _, q := range props {
					if q.Name == p.Name {
						continue base
					}
				}
				ps = append(ps, p)
			}
			c.symTypes[sym] = c.in.classValue(c.in.objectOf(ps, true, false), nil, sym)
		}
		snapshot()
		delete(c.resolving, sym)
		defer func() { c.resolving[sym] = true }()
	}
	for _, f := range decl.Fields {
		if !f.Static {
			continue
		}
		ft := c.unanswered
		switch {
		case f.Type != nil && f.Type.TypeNode() != nil:
			ft = c.typeFromNode(f.Type.TypeNode(), sym.Scope)
		case f.Initializer != nil:
			ft = c.widenFrom(f.Initializer, c.TypeOf(f.Initializer))
		}
		if f.Optional && !c.Unanswered(ft) {
			ft = c.in.union(ft, c.missingT)
		}
		add(&Property{Name: f.Name, Type: ft, Optional: f.Optional, Readonly: f.Readonly, Visibility: f.Visibility, Owner: sym})
		snapshot()
	}
	for _, m := range decl.Methods {
		if !m.IsStatic {
			continue
		}
		if m.AccessorKind != "" {
			at := c.unanswered
			if !m.IsAsync {
				if sig := c.signatureType(m, m.Params, m.ReturnType, m.Body, nil, sym.Scope); !c.Unanswered(sig) {
					switch {
					case m.AccessorKind == "get":
						at = sig.Result
					case len(sig.Params) == 1:
						at = sig.Params[0]
					}
				}
			}
			for i, q := range props {
				if q.Name == m.Name && q.Accessor {
					if m.AccessorKind == "get" {
						props[i] = &Property{Name: m.Name, Type: at, Accessor: true, Visibility: m.Visibility, Owner: sym}
					}
					at = nil
				}
			}
			if at != nil {
				add(&Property{Name: m.Name, Type: at, Accessor: true, Visibility: m.Visibility, Owner: sym})
			}
			snapshot()
			continue
		}
		mt := c.unanswered
		if len(m.Overloads) > 0 {
			mt = c.overloadsType(strconv.Itoa(sym.ID)+".static."+m.Name, sym.Scope, m.Overloads)
		} else {
			mt = c.signatureType(m, m.Params, m.ReturnType, m.Body, nil, sym.Scope)
		}
		add(&Property{Name: m.Name, Type: mt, Visibility: m.Visibility, Owner: sym, Decl: m})
		snapshot()
	}
	for _, p := range inherited {
		add(p)
	}
	if len(decl.DeclaredTypeParams().Names) == 0 {
		if inst := c.instanceOf(sym, nil); inst != nil {
			add(&Property{Name: "prototype", Type: inst, Readonly: true})
		}
	}
	return c.in.classValue(c.in.objectOf(props, partial, false), c.constructorType(sym), sym)
}

// baseInstance is the instance type of decl's base class, with the
// `extends` clause's type arguments; nil when it is not modelled (a builtin
// base, Error or Map, waits for the declaration-driven library, P3).
func (c *Checker) baseInstance(decl *ast.ClassDeclaration, sym *binder.Symbol) *Type {
	bsym := resolveTypeName(decl.BaseClass, sym.Scope)
	if bsym == nil || bsym.Flags&binder.Class == 0 {
		// A value with construct signatures (`declare var Map:
		// MapConstructor`, a builtin module's `EventEmitter`): the instance
		// type its construct signature makes with the clause's arguments.
		bargs, ok := c.baseTypeArgs(decl, sym)
		if !ok {
			return nil
		}
		return c.constructedBase(decl.BaseClass, sym.Scope, bargs)
	}
	if c.classing[bsym] {
		return nil // a circular `extends`
	}
	bargs, ok := c.baseTypeArgs(decl, sym)
	if !ok {
		return nil
	}
	if !c.classing[sym] {
		c.classing[sym] = true
		defer delete(c.classing, sym)
	}
	return c.instanceOf(bsym, bargs)
}

// inheritedConstructs is the constructor type of a class without its own
// constructor whose base is a value with construct signatures (`class D
// extends Date {}`): each of the base's signatures, the clause's type
// arguments applied, making the class's instance inst.
func (c *Checker) inheritedConstructs(decl *ast.ClassDeclaration, sym *binder.Symbol, inst *Type, tps []*Type) *Type {
	vsym := lookupName(decl.BaseClass, sym.Scope)
	if vsym == nil || vsym.Flags&binder.Variable == 0 {
		vsym = c.builtinImport(decl.BaseClass)
	}
	if vsym == nil || vsym.Flags&binder.Variable == 0 {
		return nil
	}
	ct := c.typeOfSymbol(vsym)
	if c.Unanswered(ct) || ct.Flags&Object == 0 {
		return nil
	}
	bargs, ok := c.baseTypeArgs(decl, sym)
	if !ok {
		return nil
	}
	var sigs []*Type
	for _, sig := range ct.Constructs {
		if len(sig.TypeParams) < len(bargs) {
			continue
		}
		m := map[*Type]*Type{}
		for i, tp := range sig.TypeParams {
			m[tp] = c.anyT
			if i < len(bargs) {
				m[tp] = bargs[i]
			}
		}
		ps := make([]*Type, len(sig.Params))
		for i, p := range sig.Params {
			ps[i] = c.instantiate(p, m)
		}
		sigs = append(sigs, c.in.function(ps, sig.optionals, sig.restParam, inst, nil, tps))
	}
	switch len(sigs) {
	case 0:
		return nil
	case 1:
		return sigs[0]
	}
	return c.in.overloaded(sigs)
}

// constructedBase is the instance type a base value's construct signature
// makes (`class M extends Map<string, number>`): the signature with as many
// type parameters as the clause has arguments, a parameter with no argument
// taking its default, else `any`. Nil when the base is not such a value or
// its result is not an object type.
func (c *Checker) constructedBase(name string, scope *binder.Scope, args []*Type) *Type {
	vsym := lookupName(name, scope)
	if vsym == nil || vsym.Flags&binder.Variable == 0 {
		vsym = c.builtinImport(name)
	}
	if vsym == nil || vsym.Flags&binder.Variable == 0 {
		return nil
	}
	ct := c.typeOfSymbol(vsym)
	if c.Unanswered(ct) || ct.Flags&Object == 0 {
		return nil
	}
	for _, sig := range ct.Constructs {
		if len(sig.TypeParams) < len(args) {
			continue
		}
		m := map[*Type]*Type{}
		for i, tp := range sig.TypeParams {
			if i < len(args) {
				m[tp] = args[i]
			} else {
				m[tp] = c.anyT
			}
		}
		if r := c.instantiate(sig.Result, m); r != nil && !c.Unanswered(r) && r.Flags&Object != 0 {
			if r.Kind == Array {
				// `extends Array<T>`: the instance has Array<T>'s members.
				return c.apparentType(r)
			}
			return r
		}
	}
	return nil
}

// baseTypeArgs types the `extends` clause's type arguments, in the class's
// own type-parameter environment.
// withDefaultTypeArgs completes type arguments args for parameters tps
// from their defaults, each instantiated with the arguments before it
// (`extends EventEmitter` for `EventEmitter<T = DefaultEventMap>`); a
// missing argument without a default leaves args short.
func (c *Checker) withDefaultTypeArgs(args, tps []*Type) []*Type {
	if len(args) >= len(tps) {
		return args
	}
	m := map[*Type]*Type{}
	for i, a := range args {
		m[tps[i]] = a
	}
	out := append([]*Type(nil), args...)
	for _, tp := range tps[len(args):] {
		if tp.Default == nil {
			return args
		}
		d := c.instantiate(tp.Default, m)
		m[tp] = d
		out = append(out, d)
	}
	return out
}

func (c *Checker) baseTypeArgs(decl *ast.ClassDeclaration, sym *binder.Symbol) ([]*Type, bool) {
	var out []*Type
	for _, a := range decl.BaseTypeArgs {
		if a == nil || a.TypeNode() == nil {
			return nil, false
		}
		t := c.typeFromNode(a.TypeNode(), sym.Scope)
		if c.Unanswered(t) {
			return nil, false
		}
		out = append(out, t)
	}
	return out, true
}

// bindingType is the type of the name a destructuring pattern binds from a
// source value of type src: the element or property it takes, with a
// default replacing undefined; widened for let and var.
func (c *Checker) bindingType(name, kind string, elems []ast.ArrayPatternElem, props []ast.DestructProp, src *Type, sp *srcPath) *Type {
	t, found := c.patternType(name, elems, props, src, sp)
	if !found || c.Unanswered(t) {
		return c.unanswered
	}
	if kind != "const" {
		t = widen(c, t)
	}
	return t
}

// srcPath is the reference a destructuring reads from (`const { a } = x.y`
// reads x.y.a): each property it takes is that path narrowed where the
// source is read, as TypeScript narrows a destructured property.
type srcPath struct {
	root ast.Expression
	r    ref
}

func (c *Checker) pathOf(e ast.Expression) *srcPath {
	if r, root, ok := c.refOf(e); ok {
		return &srcPath{root, r}
	}
	return nil
}

// member is the path to property key under sp, or nil.
func (sp *srcPath) member(key string) *srcPath {
	if sp == nil {
		return nil
	}
	path := append(append([]string(nil), sp.r.path...), key)
	return &srcPath{sp.root, ref{sym: sp.r.sym, path: path}}
}

// narrowPath is declared, the type of path sp, narrowed where sp is read.
func (c *Checker) narrowPath(sp *srcPath, declared *Type) *Type {
	if sp == nil || !narrowable(declared) {
		return declared
	}
	f, local := c.flowAtRoot(sp.root)
	if !local {
		return declared
	}
	t, _ := c.flowType(f, sp.r, declared, newFlowWalk())
	return t
}

func (c *Checker) patternType(name string, elems []ast.ArrayPatternElem, props []ast.DestructProp, src *Type, sp *srcPath) (*Type, bool) {
	if c.Unanswered(src) {
		return src, true
	}
	for i, e := range elems {
		if e.Name != name && e.SubArray == nil && e.SubObject == nil {
			continue
		}
		var t *Type
		switch {
		case src.Flags&Any != 0 && e.Rest:
			t = c.in.array(c.anyT) // `[...r]` of any: any[]
		case src.Flags&Any != 0:
			t = c.anyT
		case e.Rest && src.Flags&Object != 0 && src.Kind == Array:
			t = src
		case e.Rest && src.Flags&Object != 0 && src.Kind == Tuple:
			if i > len(src.Elems) {
				return c.unanswered, true
			}
			t = c.in.tupleOpt(src.Elems[i:], src.Opt)
		case e.Rest:
			return c.unanswered, true
		case src.Flags&Object != 0 && src.Kind == Array:
			t = src.Elem
		case src.Flags&Object != 0 && src.Kind == Tuple && i < len(src.Elems):
			t = src.Elems[i]
		default:
			return c.unanswered, true
		}
		if e.SubArray != nil || e.SubObject != nil {
			// A nested pattern's default replaces its undefined first
			// (`[[x] = [1]]` over `[([number] | undefined)?]`).
			if e.Default != nil && t.Flags&Union != 0 {
				t = c.filter(t, func(m *Type) bool { return m.Flags&(Undefined|Void) == 0 })
			}
			if st, ok := c.patternType(name, e.SubArray, e.SubObject, t, nil); ok {
				return st, true
			}
			continue
		}
		return c.withDefault(t, e.Default), true
	}
	for _, p := range props {
		if p.Local != name && p.SubArray == nil && p.SubObject == nil {
			continue
		}
		from := src
		if !p.Rest && !(src.Flags&Object != 0 && src.Kind == Tuple) && (src.Flags&Object == 0 || src.Kind == Array) {
			// A primitive's or an array's members are its apparent type's
			// (`const { length } = xs`); a tuple's length is its own size.
			if at := c.apparentType(src); at != nil {
				from = at
			}
		}
		if p.Rest || from.Flags&Object == 0 || (from.Kind != Anonymous && from.Kind != Instance && from.Kind != Interface) {
			return c.unanswered, true
		}
		prop := from.Prop(p.Key)
		if prop == nil {
			return c.unanswered, true
		}
		msp := sp.member(p.Key)
		pt := c.narrowPath(msp, prop.Type)
		if p.SubArray != nil || p.SubObject != nil {
			if p.Default != nil && pt.Flags&Union != 0 {
				pt = c.filter(pt, func(m *Type) bool { return m.Flags&(Undefined|Void) == 0 })
			}
			if st, ok := c.patternType(name, p.SubArray, p.SubObject, pt, msp); ok {
				return st, true
			}
			continue
		}
		return c.withDefault(pt, p.Default), true
	}
	return nil, false
}

// withDefault is a destructured value's type when a default applies: the
// default replaces undefined and an absent property's missing, never null.
func (c *Checker) withDefault(t *Type, def ast.Expression) *Type {
	if def == nil {
		return t
	}
	d := c.TypeOf(def)
	if c.Unanswered(d) {
		return d
	}
	var keep []*Type
	for _, m := range members(t) {
		if m.Flags&(Undefined|Void) == 0 {
			keep = append(keep, m)
		}
	}
	return c.in.union(append(keep, d)...)
}

// qualifiedTypeName is the type symbol `A.B.T` names: A a namespace found from
// scope outwards, each further name a member of the previous one's scope.
func (c *Checker) qualifiedTypeName(n *ast.TypeReference, scope *binder.Scope) *binder.Symbol {
	var ns *binder.Symbol
	for s := scope; s != nil && ns == nil; s = s.Parent {
		if sym := s.Symbols.Get(n.Qualifier[0]); sym != nil && sym.Flags&(binder.NamespaceModule|binder.TypeModule) != 0 {
			ns = sym
		}
	}
	for _, q := range n.Qualifier[1:] {
		if ns == nil {
			return nil
		}
		inner := c.b.NamespaceScope(ns)
		if inner == nil {
			return nil
		}
		ns = inner.Symbols.Get(q)
	}
	if ns == nil {
		return nil
	}
	inner := c.b.NamespaceScope(ns)
	if inner == nil {
		return nil
	}
	if sym := inner.Symbols.Get(n.Name); sym != nil && sym.Flags&binder.Type != 0 {
		return sym
	}
	// A member the resolver made file-private (`A__kml_mod0`).
	var found *binder.Symbol
	inner.Symbols.Each(func(sym *binder.Symbol) {
		if found == nil && sym.Flags&binder.Type != 0 && ast.TrimModuleSuffix(sym.Name) == n.Name {
			found = sym
		}
	})
	return found
}

// originsOf is the origins of sigs, a base's signatures: its recorded ones,
// or its symbol's for a base that recorded none.
func originsOf(sigs []*Type, origins []sigOrigin, sym int) []sigOrigin {
	if len(origins) == len(sigs) {
		return origins
	}
	out := make([]sigOrigin, len(sigs))
	for i := range out {
		out[i] = sigOrigin{sym: sym}
	}
	return out
}

// literalParams reports a call or construct signature with a parameter
// annotated by a literal type (tsc's SignatureFlags.HasLiteralTypes).
func literalParams(m ast.Node) bool {
	var params []*ast.SignatureParameter
	switch s := m.(type) {
	case *ast.CallSignature:
		params = s.Parameters
	case *ast.ConstructSignature:
		params = s.Parameters
	}
	for _, p := range params {
		if _, ok := p.Type.(*ast.LiteralType); ok {
			return true
		}
	}
	return false
}

// reorderCandidates is tsc's order of a call's candidate signatures: within
// one symbol, a later declaration's signatures before an earlier one's;
// another symbol's (a base's) after; a specialized signature (a literal
// parameter type) before every other.
func reorderCandidates(sigs []*Type, origins []sigOrigin) []*Type {
	if len(origins) != len(sigs) {
		return sigs
	}
	var result []*Type
	lastSym, lastDecl := -1, -1
	cutoff, index, specialized := 0, 0, -1
	for i, sig := range sigs {
		o := origins[i]
		if lastSym < 0 || o.sym == lastSym {
			if lastDecl >= 0 && o.decl == lastDecl {
				index++
			} else {
				lastDecl = o.decl
				index = cutoff
			}
		} else {
			index, cutoff = len(result), len(result)
			lastDecl = o.decl
		}
		lastSym = o.sym
		at := index
		if o.literal {
			specialized++
			at = specialized
			cutoff++
		}
		result = append(result[:at], append([]*Type{sig}, result[at:]...)...)
	}
	return result
}

// indexKeyKind is an index signature's key kind, "string" or "number", or
// "" for one the checker does not model (a symbol or template key).
func indexKeyKind(ix *ast.IndexSignature) (string, bool) {
	if k, ok := ix.KeyType.(*ast.KeywordType); ok && (k.Keyword == "string" || k.Keyword == "number") {
		return k.Keyword, true
	}
	return "", false
}

// indexSignature is an index signature's key kind and value type.
func (c *Checker) indexSignature(ix *ast.IndexSignature, scope *binder.Scope) (string, *Type) {
	kind, ok := indexKeyKind(ix)
	if !ok || ix.Type == nil {
		return "", c.unanswered
	}
	return kind, c.typeFromNode(ix.Type, scope)
}

// templateLiteralType is a template literal type (`${A}-x`): the string
// literals it spells when every hole is a literal type (or a union of them),
// else string. (A pattern type is not modelled: any string satisfies it.)
func (c *Checker) templateLiteralType(n *ast.TemplateLiteralType, scope *binder.Scope) *Type {
	if len(n.Spans) == 0 {
		return c.in.literal(StringLiteral, n.Head)
	}
	texts := []string{n.Head}
	for _, sp := range n.Spans {
		t := c.typeFromNode(sp.Type, scope)
		if c.Unanswered(t) {
			return c.strT
		}
		var parts []string
		for _, m := range members(t) {
			switch {
			case m.Flags&(StringLiteral|NumberLiteral) != 0:
				parts = append(parts, m.Value)
			case m == c.trueT:
				parts = append(parts, "true")
			case m == c.falseT:
				parts = append(parts, "false")
			default:
				return c.strT
			}
		}
		if len(parts) == 0 || len(texts)*len(parts) > 256 {
			return c.strT
		}
		var next []string
		for _, pre := range texts {
			for _, p := range parts {
				next = append(next, pre+p+sp.Literal)
			}
		}
		texts = next
	}
	var lits []*Type
	for _, s := range texts {
		lits = append(lits, c.in.literal(StringLiteral, s))
	}
	return c.in.union(lits...)
}

// typeQueryType is `typeof a.b.c` in a type position: the type of the value
// a names, then of each property along the path.
func (c *Checker) typeQueryType(n *ast.TypeQuery, scope *binder.Scope) *Type {
	if s := c.b.LookupScope(n); s != nil {
		scope = s // the scope the annotation is written in
	}
	var sym *binder.Symbol
	for s := scope; s != nil && sym == nil; s = s.Parent {
		if v := s.Symbols.Get(n.Name); v != nil && v.Flags&binder.Value != 0 {
			sym = v
		}
	}
	var t *Type
	if sym != nil {
		t = c.typeOfSymbol(sym)
	} else if t = c.builtinValueType(n.Name); t == nil {
		c.unresolvedValue(n.Name, n, n.GetPos(), scope) // TS2304
		return c.unanswered
	}
	for _, seg := range n.Path {
		if c.Unanswered(t) || t.Flags&Any != 0 {
			return t
		}
		var next *Type
		// A primitive's properties are its wrapper's (apparentType); an
		// object's are its own.
		at := c.apparentType(t)
		if at == nil {
			at = t
		}
		for _, p := range at.Props {
			if p.Name == seg {
				next = p.Type
				break
			}
		}
		if next == nil {
			return c.unanswered
		}
		t = next
	}
	return t
}

// generatorReturn is the type an unannotated generator function returns, as
// tsc infers it: Generator<Y, R, unknown>, Y the union of what its
// `yield`s produce (a bare `yield` undefined, a `yield*` its iterable's
// element type) and R what its body returns. An async generator's is
// AsyncGenerator<Y, R, unknown>, its yields and returns awaited. Unanswered
// when a yielded value is, or when there is no such declaration.
func (c *Checker) generatorReturn(body *ast.BlockStatement, async bool) *Type {
	if body == nil || c.b.Globals == nil {
		return c.unanswered
	}
	name := "Generator"
	if async {
		name = "AsyncGenerator"
	}
	sym := c.b.Globals.Symbols.Get(name)
	if sym == nil || sym.Flags&binder.Interface == 0 {
		return c.unanswered
	}
	var ys []*Type
	unknown := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
			return false // a nested function's yields are its own
		case *ast.YieldExpression:
			var t *Type
			switch {
			case n.Argument == nil:
				t = c.undefinedT
			case n.Delegate && async:
				t = c.asyncIteratedType(c.TypeOf(n.Argument))
			case n.Delegate:
				t = c.iteratedType(c.TypeOf(n.Argument))
			default:
				t = c.TypeOf(n.Argument)
				if async && !c.Unanswered(t) {
					t = c.awaited(t, 0)
				}
			}
			if c.Unanswered(t) {
				unknown = true
			} else {
				ys = append(ys, t)
			}
		}
		return true
	})
	if unknown {
		return c.unanswered
	}
	y := c.neverT
	if len(ys) > 0 {
		// A lone literal widens, a union of them stays (tsc's
		// getWidenedLiteralLikeTypeForContextualIterationTypeIfNeeded).
		y = c.widenUnit(c.in.union(ys...))
	}
	r := c.returnType(body, nil)
	if c.Unanswered(r) {
		return r
	}
	if async && r.Flags&Void == 0 {
		r = c.awaited(r, 0)
	}
	return c.interfaceOf(sym, []*Type{y, r, c.unknownT})
}

// iteratedType is the element type `yield*` and spread take from t: an
// array's element, a string's characters, a builtin iterator's or
// generator's first type argument; unanswered for anything else.
// IteratedType is what iterating a value of type t yields, or nil.
func (c *Checker) IteratedType(t *Type) *Type { return c.iteratedType(t) }

func (c *Checker) iteratedType(t *Type) *Type {
	switch {
	case c.Unanswered(t):
		return t
	case t.Flags&(Any|Never) != 0:
		return c.anyT // never: tsc's error type after TS2488
	case c.lacksIterator(t, false):
		return c.anyT // tsc's error type after TS2488
	case isStringLike(t):
		return c.strT
	case t.Flags&Object != 0 && t.Kind == Array:
		return t.Elem
	case t.Flags&Object != 0 && t.Kind == Tuple && len(t.Elems) > 0:
		return c.in.union(t.Elems...)
	case t.Flags&Object != 0 && t.Kind == Interface && t.Symbol != nil && c.inLibrary(t.Symbol.Scope):
		switch t.Symbol.Name {
		case "Generator", "IteratorObject", "ArrayIterator", "MapIterator", "SetIterator", "IterableIterator",
			"AsyncGenerator", "AsyncIterable", "AsyncIterableIterator", "AsyncIteratorObject", "Set", "ReadonlySet",
			"Array", "ReadonlyArray":
			if len(t.TypeArgs) > 0 {
				return t.TypeArgs[0]
			}
		case "Map", "ReadonlyMap":
			// An entry: `[K, V]`.
			if len(t.TypeArgs) == 2 {
				return c.in.tuple([]*Type{t.TypeArgs[0], t.TypeArgs[1]})
			}
		case "Int8Array", "Uint8Array", "Uint8ClampedArray", "Int16Array", "Uint16Array", "Int32Array", "Uint32Array",
			"Float16Array", "Float32Array", "Float64Array":
			return c.numT
		case "BigInt64Array", "BigUint64Array":
			return c.bigintT
		}
	}
	// A subclass of a builtin collection (`class Pos extends Array<number>`)
	// iterates as its base does.
	if t.Flags&Object != 0 && t.Kind == Instance && t.Base != nil && c.iterDepth < 4 {
		c.iterDepth++
		bt := c.iteratedType(t.Base)
		c.iterDepth--
		if !c.Unanswered(bt) && bt.Flags&Any == 0 {
			return bt
		}
	}
	// An object with its own `[Symbol.iterator]` method (`{ *[Symbol.
	// iterator]() { yield 1 } }`, a class declaring one): what the iterator
	// it returns yields.
	if t.Flags&Object != 0 && (t.Kind == Anonymous || t.Kind == Instance) && c.iterDepth < 4 {
		if p := t.Prop("@@iterator"); p != nil && p.Type != nil && p.Type.Flags&Object != 0 && p.Type.Kind == Function && p.Type.Result != nil {
			c.iterDepth++
			defer func() { c.iterDepth-- }()
			return c.iteratedType(p.Type.Result)
		}
		// An iterator itself (an object with a `next` method): the
		// `value` of what next returns.
		if v := c.protocolValue(t, false); v != nil {
			return v
		}
	}
	return c.unanswered
}

// protocolValue is what an iterator-protocol object yields: the `value`
// of its `next()` result (awaited when async), the members' values joined
// for a union of results; nil when t has no such next.
func (c *Checker) protocolValue(t *Type, async bool) *Type {
	p := t.Prop("next")
	if p == nil || p.Type == nil || p.Type.Flags&Object == 0 || p.Type.Kind != Function || p.Type.Result == nil {
		return nil
	}
	r := p.Type.Result
	if async {
		r = c.awaited(r, 0)
	}
	if c.Unanswered(r) {
		return nil
	}
	// A `next()` that only throws has no `value`: tsc's error type after
	// TS2490 (TS2547 when async).
	if r.Flags&Never != 0 {
		return c.anyT
	}
	members := []*Type{r}
	if r.Flags&Union != 0 {
		members = r.Types
	}
	var vs []*Type
	for _, m := range members {
		if m.Flags&(TypeParam|Deferred) != 0 {
			return nil
		}
		if m.Flags&Object == 0 {
			// A primitive result has no `value` either (TS2490, TS2547).
			return c.anyT
		}
		if vp := m.Prop("value"); vp != nil && vp.Type != nil {
			vs = append(vs, vp.Type)
		}
	}
	if len(vs) == 0 {
		return c.anyT // a result without a value: nothing is known of it
	}
	return c.in.union(vs...)
}

// asyncIteratedType is what `yield*` in an async generator, or `for
// await`, takes from t: an own `[Symbol.asyncIterator]`'s iterator's
// awaited `next()` value, else t's (sync) iterated element.
func (c *Checker) asyncIteratedType(t *Type) *Type {
	if !c.Unanswered(t) && (t.Flags&Never != 0 || c.lacksIterator(t, true)) {
		return c.anyT // tsc's error type after TS2504
	}
	if !c.Unanswered(t) && t.Flags&Object != 0 && (t.Kind == Anonymous || t.Kind == Instance) && c.iterDepth < 4 {
		if p := t.Prop("@@asyncIterator"); p != nil && p.Type != nil && p.Type.Flags&Object != 0 && p.Type.Kind == Function && p.Type.Result != nil {
			if it := p.Type.Result; it.Flags&Object != 0 {
				if v := c.protocolValue(it, true); v != nil {
					return v
				}
				c.iterDepth++
				defer func() { c.iterDepth-- }()
				return c.iteratedType(it) // a lib AsyncIterator
			}
		}
	}
	return c.iteratedType(t)
}

// isGeneratorFn reports whether fn is a (non-async) generator function,
// declaration, method or expression.
func isGeneratorFn(fn ast.Node) bool {
	switch f := fn.(type) {
	case *ast.FunctionDeclaration:
		return f.IsGenerator && !f.IsAsync
	case *ast.FunctionExpression:
		return f.IsGenerator && !f.IsAsync
	}
	return false
}

func isAsyncFn(fn ast.Node) bool {
	switch f := fn.(type) {
	case *ast.FunctionDeclaration:
		return f.IsAsync && !f.IsGenerator
	case *ast.FunctionExpression:
		return f.IsAsync && !f.IsGenerator
	case *ast.ArrowFunction:
		return f.IsAsync
	}
	return false
}

func isAsyncGeneratorFn(fn ast.Node) bool {
	switch f := fn.(type) {
	case *ast.FunctionDeclaration:
		return f.IsAsync && f.IsGenerator
	case *ast.FunctionExpression:
		return f.IsAsync && f.IsGenerator
	}
	return false
}

// promiseOf is the builtin Promise<t>, nil without the builtin declarations.
func (c *Checker) promiseOf(t *Type) *Type {
	if c.b.Globals == nil {
		return nil
	}
	sym := c.b.Globals.Symbols.Get("Promise")
	if sym == nil || sym.Flags&binder.Interface == 0 {
		return nil
	}
	p := c.interfaceOf(sym, []*Type{t})
	if p == nil || c.Unanswered(p) {
		return nil
	}
	return p
}

// isUnannotatedFunction reports whether n is a function declaration or a
// named function expression without a return annotation.
func isUnannotatedFunction(n ast.Node) bool {
	switch f := n.(type) {
	case *ast.FunctionDeclaration:
		return f.ReturnType == nil
	case *ast.FunctionExpression:
		return f.RetType == nil
	}
	return false
}

// iifeParam is an unannotated parameter's type when fn is called where it
// is written (`function(a){...}(x)`): its argument's widened type, or any
// past the last argument (tsc's getContextuallyTypedParameterType for an
// immediately invoked function).
func (c *Checker) iifeParam(fn ast.Node, i int, rest bool) *Type {
	call, ok := c.parentOf(fn).(*ast.CallExpression)
	if !ok || ast.Node(call.Callee) != fn || rest {
		return nil
	}
	if i < len(call.Args) {
		if _, spread := call.Args[i].(*ast.SpreadElement); spread {
			return nil
		}
		// The argument's own context is this parameter (`(function (f)
		// {…})(x => x)`): typing it from here re-enters, so that one stays
		// unanswered.
		if c.contextualizing[call] {
			return nil
		}
		c.contextualizing[call] = true
		defer delete(c.contextualizing, call)
		return c.widenFrom(call.Args[i], c.TypeOf(call.Args[i]))
	}
	return c.anyT
}

// iifeOmitted reports whether fn is called where it is written with no
// argument at parameter i.
func (c *Checker) iifeOmitted(fn ast.Node, i int, rest bool) bool {
	call, ok := c.parentOf(fn).(*ast.CallExpression)
	return ok && ast.Node(call.Callee) == fn && !rest && i >= len(call.Args)
}

// reportReturnCycle reports TS7023 at each unannotated function whose
// return type is being inferred on the cycle that re-entered sym (tsc
// gives each one an any result).
func (c *Checker) reportReturnCycle(sym *binder.Symbol) {
	if c.opts.CompatJS() || c.ImplicitAnyVariables {
		return
	}
	// tsc infers a return type when the function is called: a reference
	// to it as a value (`return g`) types the function, not its result.
	called := false
	for i := len(c.resolveStack) - 1; i >= 0 && !called; i-- {
		if s := c.resolveStack[i]; len(s.Declarations) == 1 {
			called = c.callsWithin(s.Declarations[0].Node, sym)
		}
		if c.resolveStack[i] == sym {
			break
		}
	}
	if !called {
		return
	}
	for i := len(c.resolveStack) - 1; i >= 0; i-- {
		s := c.resolveStack[i]
		if len(s.Declarations) == 1 && isUnannotatedFunction(s.Declarations[0].Node) {
			if d := s.Declarations[0].Node; !c.implicitAnyReported[d] {
				c.implicitAnyReported[d] = true
				c.report(diag.ReturnImplicitAnyCycle, functionNamePos(d, s.Declarations[0].Pos), sourceName(s.Name))
			}
		}
		if s == sym {
			return
		}
	}
}

// markReturnCycle records each unannotated, non-async, non-generator
// function declaration on the cycle that re-entered sym through a call:
// tsc resolves its result to any once the cycle closes.
func (c *Checker) markReturnCycle(sym *binder.Symbol) {
	called := false
	for i := len(c.resolveStack) - 1; i >= 0 && !called; i-- {
		if s := c.resolveStack[i]; len(s.Declarations) == 1 {
			called = c.callsWithin(s.Declarations[0].Node, sym)
		}
		if c.resolveStack[i] == sym {
			break
		}
	}
	if !called {
		return
	}
	for i := len(c.resolveStack) - 1; i >= 0; i-- {
		s := c.resolveStack[i]
		if len(s.Declarations) == 1 {
			if fd, ok := s.Declarations[0].Node.(*ast.FunctionDeclaration); ok && fd.ReturnType == nil && !fd.IsAsync && !fd.IsGenerator && len(fd.Overloads) == 0 {
				if c.fdCycled == nil {
					c.fdCycled = map[*ast.FunctionDeclaration]bool{}
				}
				c.fdCycled[fd] = true
			}
		}
		if s == sym {
			return
		}
	}
}

// reportReturnCycleAt reports TS7023 for a variable sym bound to function
// expression fe, read within its own body while its result is inferred.
func (c *Checker) reportReturnCycleAt(sym *binder.Symbol, fe *ast.FunctionExpression) {
	if c.opts.CompatJS() || c.ImplicitAnyVariables || c.implicitAnyReported[fe] {
		return
	}
	called := false
	for i := len(c.resolveStack) - 1; i >= 0 && !called; i-- {
		if s := c.resolveStack[i]; len(s.Declarations) == 1 {
			called = c.callsWithin(s.Declarations[0].Node, sym)
		}
		if c.resolveStack[i] == sym {
			break
		}
	}
	if called {
		c.implicitAnyReported[fe] = true
		c.report(diag.ReturnImplicitAnyCycle, sym.Declarations[0].Pos, sourceName(sym.Name))
	}
}

// functionNamePos is a function's name position, else pos.
func functionNamePos(n ast.Node, pos ast.Pos) ast.Pos {
	switch f := n.(type) {
	case *ast.FunctionDeclaration:
		if f.NamePos.Line > 0 {
			return f.NamePos
		}
	case *ast.FunctionExpression:
		if f.NamePos.Line > 0 {
			return f.NamePos
		}
	}
	return pos
}

// callsItself reports whether e is a call of the function fn by its own
// name (`return f(n - 1)` in `function f`).
func (c *Checker) callsItself(fn ast.Node, e ast.Expression) bool {
	call, ok := e.(*ast.CallExpression)
	if !ok {
		return false
	}
	id, ok := call.Callee.(*ast.Identifier)
	if !ok {
		return false
	}
	name := ""
	switch f := fn.(type) {
	case *ast.FunctionDeclaration:
		name = f.Name
	case *ast.FunctionExpression:
		name = f.Name
	}
	if name == "" || id.Name != name {
		return false // (a parameter's declaration is the function too)
	}
	sym, _ := c.b.Resolve(id)
	return sym != nil && len(sym.Declarations) > 0 && sym.Declarations[0].Node == fn
}

// callsWithin reports whether n contains a call of sym by name.
func (c *Checker) callsWithin(n ast.Node, sym *binder.Symbol) bool {
	found := false
	ast.Inspect(n, func(x ast.Node) bool {
		if found {
			return false
		}
		if call, ok := x.(*ast.CallExpression); ok {
			if id, ok := call.Callee.(*ast.Identifier); ok && id.Name == sym.Name {
				if s, _ := c.b.Resolve(id); s == sym {
					found = true
				}
			}
		}
		return true
	})
	return found
}

// iteratorLike reports a generator annotation tsc accepts: any, or a
// builtin iterator, iterable or generator interface.
func (c *Checker) iteratorLike(t *Type) bool {
	if t.Flags&(Any|Unknown) != 0 {
		return true
	}
	if t.Flags&Object == 0 || t.Symbol == nil || !c.inLibrary(t.Symbol.Scope) {
		return false
	}
	switch t.Symbol.Name {
	case "Generator", "AsyncGenerator", "Iterator", "AsyncIterator", "Iterable", "AsyncIterable",
		"IterableIterator", "AsyncIterableIterator", "IteratorObject", "AsyncIteratorObject":
		return true
	}
	return false
}

// generatorOf is the builtin Generator<y, any, unknown> (AsyncGenerator when
// async), nil without the builtin declarations.
func (c *Checker) generatorOf(y *Type, async bool) *Type {
	if c.b.Globals == nil {
		return nil
	}
	name := "Generator"
	if async {
		name = "AsyncGenerator"
	}
	sym := c.b.Globals.Symbols.Get(name)
	if sym == nil || sym.Flags&binder.Interface == 0 {
		return nil
	}
	return c.interfaceOf(sym, []*Type{y, c.anyT, c.unknownT})
}

// withThisParam is signature fn with a method signature's leading `this: T`
// parameter (`read?(this: H, size: number): void`), as TypeScript keeps it.
// The signature's own type parameters (signatureOfNodes' key and tps) are
// in scope for it (`flat<A>(this: A)`).
func (c *Checker) withThisParam(fn *Type, key string, tps []*ast.TypeParameter, this ast.TypeNode, scope *binder.Scope) *Type {
	if this == nil || c.Unanswered(fn) || fn.Flags&Object == 0 || fn.Kind != Function {
		return fn
	}
	var names []string
	var tpTypes []*Type
	for i, tp := range tps {
		name := tp.Name
		tpTypes = append(tpTypes, c.in.intern("v"+key+":"+strconv.Itoa(i)+":"+name, func() *Type {
			return &Type{Flags: TypeParam, Value: name}
		}))
		names = append(names, name)
	}
	defer c.pushEnv(names, tpTypes)()
	tt := c.typeFromNode(this, scope)
	if c.Unanswered(tt) || tt.Flags&Void != 0 {
		return fn
	}
	return c.in.withThis(fn, tt)
}

// wellKnownMemberKey is the property key a type member named by a
// well-known symbol (`[Symbol.iterator]`) has: the `@@` name the parser
// gives a class's or an object literal's member of the symbol.
func wellKnownMemberKey(name string) (string, bool) {
	if rest, ok := strings.CutPrefix(name, "[Symbol."); ok {
		if w, ok := strings.CutSuffix(rest, "]"); ok && w != "" && !strings.ContainsAny(w, ".[]() ") {
			return "@@" + w, true
		}
	}
	return "", false
}

// memberDisplayName is a property key as TypeScript prints it: a
// well-known symbol's member as `[Symbol.iterator]`.
func memberDisplayName(name string) string {
	if rest, ok := strings.CutPrefix(name, "@@"); ok {
		return "[Symbol." + rest + "]"
	}
	return name
}

// PatternDefaultType is a destructured parameter's type from its default,
// as the parameter's signature takes it (without the `| undefined` an
// omitted argument adds).
func (c *Checker) PatternDefaultType(p ast.Param) *Type { return c.patternDefaultType(p) }

// patternDefaultType is a destructured parameter's type from its default:
// `[x, y] = [1, 2]` is [number, number], otherwise the widened default.
// ImpliedPatternType is the type a parameter's binding pattern implies
// (tsc's getTypeFromBindingPattern).
func (c *Checker) ImpliedPatternType(p ast.Param) *Type {
	return c.impliedPatternType(p.ArrayPattern, p.ObjectPattern)
}

func (c *Checker) patternDefaultType(p ast.Param) *Type {
	if lit, ok := p.Default.(*ast.ArrayLiteral); ok && p.ArrayPattern != nil && len(lit.Elements) == 0 {
		// `[a = 1] = []`: the pattern types the empty literal (tsc's
		// contextual typing by a binding pattern), every element optional.
		t := c.impliedPatternType(p.ArrayPattern, nil)
		if t.Flags&Object != 0 && t.Kind == Tuple {
			t = c.in.tupleOpt(t.Elems, len(t.Elems))
		}
		return t
	}
	if lit, ok := p.Default.(*ast.ObjectLiteral); ok && p.ObjectPattern != nil && len(lit.Properties) == 0 {
		return c.impliedPatternType(nil, p.ObjectPattern)
	}
	if lit, ok := p.Default.(*ast.ArrayLiteral); ok && p.ArrayPattern != nil && len(lit.Elements) > 0 {
		elems := make([]*Type, 0, len(lit.Elements))
		for i, x := range lit.Elements {
			if _, spread := x.(*ast.SpreadElement); spread {
				return c.widenFrom(p.Default, c.TypeOf(p.Default))
			}
			// An empty literal under a nested pattern (`[[a]] = [[]]`): the
			// empty tuple where the pattern's context is tuple-like (no
			// default of its own, or an array literal), else never[] (tsc's
			// binding-pattern contextual typing).
			if i < len(p.ArrayPattern) {
				el := p.ArrayPattern[i]
				if al, ok := x.(*ast.ArrayLiteral); ok && len(al.Elements) == 0 && el.SubArray != nil {
					if _, litDefault := el.Default.(*ast.ArrayLiteral); el.Default == nil || litDefault {
						elems = append(elems, c.in.tuple(nil))
					} else {
						elems = append(elems, c.in.array(c.neverT))
					}
					continue
				}
				if ol, ok := x.(*ast.ObjectLiteral); ok && len(ol.Properties) == 0 && el.SubObject != nil && el.Default == nil {
					elems = append(elems, c.impliedPatternType(nil, el.SubObject))
					continue
				}
			}
			et := c.TypeOf(x)
			if c.Unanswered(et) {
				return c.unanswered
			}
			elems = append(elems, c.widenFrom(x, et))
		}
		return c.in.tuple(elems)
	}
	return c.widenFrom(p.Default, c.TypeOf(p.Default))
}

// defaultRefersTo reports a parameter default reading the parameter itself
// (`x = x`, `x = x + 1`) outside a nested function: tsc reports TS2372
// and types the parameter any.
func defaultRefersTo(def ast.Expression, name string) bool {
	if name == "" {
		return false
	}
	found := false
	ast.Inspect(def, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassExpression:
			return false // a closure reads it later, once it is bound
		case *ast.Identifier:
			if x.Name == name {
				found = true
			}
		case *ast.MemberExpression:
			ast.Inspect(x.Object, func(m ast.Node) bool {
				if id, ok := m.(*ast.Identifier); ok && id.Name == name {
					found = true
				}
				return !found
			})
			return false // a property name is no reference
		}
		return !found
	})
	return found
}

// impliedPatternType is the type an unannotated binding pattern implies:
// an object of its keys, a tuple of its positions, each any unless nested
// (a key with a default is optional; a rest element or a computed key
// adds nothing). An element with a default may be undefined, and the
// trailing ones are optional (`[[x] = [1]]` is `[([number] | undefined)?]`).
func (c *Checker) impliedPatternType(elems []ast.ArrayPatternElem, props []ast.DestructProp) *Type {
	if elems != nil {
		var ts []*Type
		opt := 0
		for _, el := range elems {
			if el.Rest {
				break
			}
			t := c.impliedLeafDefault(el.SubArray, el.SubObject, el.Default)
			if el.Default != nil {
				if t.Flags&(Any|Unknown) == 0 {
					t = c.in.union(t, c.undefinedT)
				}
				opt++
			} else {
				opt = 0
			}
			ts = append(ts, t)
		}
		return c.in.tupleOpt(ts, opt)
	}
	var ps []*Property
	for _, p := range props {
		if p.Rest || p.KeyExpr != nil {
			continue
		}
		ps = append(ps, &Property{Name: p.Key, Type: c.impliedLeafDefault(p.SubArray, p.SubObject, p.Default), Optional: p.Default != nil})
	}
	return c.in.object(ps)
}

// impliedLeafDefault is a pattern element's implied type: its default's
// (a tuple for a nested array pattern over an array literal) when it has
// one, else what its own pattern implies, else any.
func (c *Checker) impliedLeafDefault(elems []ast.ArrayPatternElem, props []ast.DestructProp, def ast.Expression) *Type {
	if def != nil {
		if t := c.elementDefaultType(elems, props, def); !c.Unanswered(t) {
			return t
		}
	}
	return c.impliedLeaf(elems, props)
}

// elementDefaultType types a pattern element's default the way
// patternDefaultType types a parameter's.
func (c *Checker) elementDefaultType(elems []ast.ArrayPatternElem, props []ast.DestructProp, def ast.Expression) *Type {
	return c.patternDefaultType(ast.Param{ArrayPattern: elems, ObjectPattern: props, Default: def})
}

func (c *Checker) impliedLeaf(elems []ast.ArrayPatternElem, props []ast.DestructProp) *Type {
	if elems != nil || props != nil {
		return c.impliedPatternType(elems, props)
	}
	return c.anyT
}

// literalAccessor reports whether fn is an object literal's getter or
// setter body.
func (c *Checker) literalAccessor(fn ast.Node) bool {
	lit, ok := c.parentOf(fn).(*ast.ObjectLiteral)
	if !ok {
		return false
	}
	for _, p := range lit.Properties {
		if p.Value == fn {
			return p.AccessorKind != ""
		}
	}
	return false
}

// importTypeRef is the reference `import("mod").A.B<T>` names in the merged
// program: A by the merged name of builtin module mod's declaration, nil
// when the program does not include it.
func (c *Checker) importTypeRef(n *ast.ImportType) *ast.TypeReference {
	prog, ok := c.b.Module.Node.(*ast.Program)
	if !ok || len(n.Qualifier) == 0 {
		return nil
	}
	mod := strings.TrimPrefix(n.Argument, "node:")
	head, ok := prog.LibExports[mod+":"+n.Qualifier[0]]
	if !ok {
		return nil
	}
	path := append([]string{head}, n.Qualifier[1:]...)
	return &ast.TypeReference{Qualifier: path[:len(path)-1], Name: path[len(path)-1], TypeArgs: n.TypeArgs, Range: n.Range}
}

// refinedInstance is an intersection of one class instance with object
// literals (`WriteStream & { fd: 1 }`): the instance, each property a
// literal redeclares narrowed to the narrower type and each it adds
// appended. Nil for any other shape.
func (c *Checker) refinedInstance(parts []*Type) *Type {
	var inst *Type
	var lits []*Type
	for _, t := range parts {
		switch {
		case t.Flags == Object && t.Kind == Instance && inst == nil && len(t.TypeArgs) == 0 && !c.building[t] && !t.partial:
			inst = t
		case t.Flags == Object && t.Kind == Anonymous && len(t.Calls) == 0 && len(t.Constructs) == 0 && t.StringIndex == nil && t.NumberIndex == nil && !t.partial:
			lits = append(lits, t)
		default:
			return nil
		}
	}
	if inst == nil || len(lits) == 0 {
		return nil
	}
	props := append([]*Property(nil), inst.Props...)
	for _, l := range lits {
		for _, p := range l.Props {
			i := -1
			for k, q := range props {
				if q.Name == p.Name {
					i = k
					break
				}
			}
			if i < 0 {
				props = append(props, p)
				continue
			}
			q := props[i]
			if p.Accessor || q.Accessor {
				return nil
			}
			np := *q
			switch {
			case c.assignable(p.Type, q.Type):
				np.Type = p.Type
			case !c.assignable(q.Type, p.Type):
				return nil
			}
			np.Optional = p.Optional && q.Optional
			np.Readonly = p.Readonly || q.Readonly
			props[i] = &np
		}
	}
	r := *inst
	r.Props = props
	return &r
}

// builtinBaseStatics are the static members a class extending a builtin
// constructor value (`declare var Promise: PromiseConstructor`) inherits:
// that value's properties, its prototype aside. Nil when the base is no
// such value.
func (c *Checker) builtinBaseStatics(decl *ast.ClassDeclaration, sym *binder.Symbol) []*Property {
	vsym := lookupName(decl.BaseClass, sym.Scope)
	if vsym == nil || vsym.Flags&binder.Variable == 0 {
		vsym = c.builtinImport(decl.BaseClass)
	}
	if vsym == nil || vsym.Flags&binder.Variable == 0 {
		return nil
	}
	ct := c.typeOfSymbol(vsym)
	if c.Unanswered(ct) || ct.Flags&Object == 0 || len(ct.Constructs) == 0 {
		return nil
	}
	ps := []*Property{}
	for _, p := range members(ct)[0].Props {
		if p.Name != "prototype" {
			// Without the base's declaration: called on the subclass, the
			// static runs with the subclass as its receiver, not as the
			// base's own lowering.
			q := *p
			q.Decl, q.Decls = nil, nil
			ps = append(ps, &q)
		}
	}
	return ps
}

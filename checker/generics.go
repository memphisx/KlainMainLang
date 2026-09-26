package checker

import (
	"strconv"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// typeParamType is the type a type parameter declares: interned per symbol,
// its constraint read from the declaring function's `extends` clause.
func (c *Checker) typeParamType(sym *binder.Symbol) *Type {
	t, fresh := c.in.intern2("p"+strconv.Itoa(sym.ID), func() *Type { return &Type{Flags: TypeParam, Symbol: sym} })
	if !fresh {
		return t
	}
	for _, d := range sym.Declarations {
		fd, ok := d.Node.(*ast.FunctionDeclaration)
		if !ok {
			continue
		}
		for i, name := range fd.TypeParams {
			if name == sym.Name && i < len(fd.TypeParamConstraints) && fd.TypeParamConstraints[i] != nil {
				if tn := fd.TypeParamConstraints[i].TypeNode(); tn != nil {
					if ct := c.typeFromNode(tn, sym.Scope); !c.Unanswered(ct) {
						t.Constraint = ct
					}
				}
			}
		}
	}
	return t
}

// instantiate substitutes m's types for the type parameters in t.
func (c *Checker) instantiate(t *Type, m map[*Type]*Type) *Type {
	if len(m) == 0 || t == nil {
		return t
	}
	switch {
	case t.Flags&TypeParam != 0:
		if r, ok := m[t]; ok {
			return r
		}
		return t
	case t.Flags&Union != 0:
		ms := make([]*Type, len(t.Types))
		for i, x := range t.Types {
			ms[i] = c.instantiate(x, m)
		}
		return c.in.union(ms...)
	case t.Flags&Object == 0:
		return t
	}
	switch t.Kind {
	case Array:
		return c.in.array(c.instantiate(t.Elem, m))
	case Tuple:
		es := make([]*Type, len(t.Elems))
		for i, x := range t.Elems {
			es[i] = c.instantiate(x, m)
		}
		return c.in.tuple(es)
	case Function:
		if len(t.Overloads) > 0 {
			// An overload set: each signature instantiated, still a set.
			sigs := make([]*Type, len(t.Overloads))
			for i, o := range t.Overloads {
				sigs[i] = c.instantiate(o, m)
			}
			return c.in.overloaded(sigs)
		}
		ps := make([]*Type, len(t.Params))
		for i, x := range t.Params {
			ps[i] = c.instantiate(x, m)
		}
		var pred *Predicate
		if t.Predicate != nil {
			p := *t.Predicate
			if p.Type != nil {
				p.Type = c.instantiate(p.Type, m)
			}
			pred = &p
		}
		var tps []*Type
		for _, tp := range t.TypeParams {
			if _, bound := m[tp]; !bound {
				tps = append(tps, tp)
			}
		}
		fn := c.in.function(ps, t.optionals, t.restParam, c.instantiate(t.Result, m), pred, tps)
		if t.ThisType != nil {
			fn = c.in.withThis(fn, c.instantiate(t.ThisType, m))
		}
		return fn
	case Anonymous:
		props := make([]*Property, len(t.Props))
		for i, p := range t.Props {
			props[i] = &Property{Name: p.Name, Type: c.instantiate(p.Type, m), Optional: p.Optional}
		}
		var str, num *Type
		if t.StringIndex != nil {
			str = c.instantiate(t.StringIndex, m)
		}
		if t.NumberIndex != nil {
			num = c.instantiate(t.NumberIndex, m)
		}
		return c.in.indexed(props, str, num)
	}
	return t
}

// paramAt is the parameter type of fn an argument at index i binds to (a
// rest parameter's element type past the fixed ones).
func paramAt(fn *Type, i int) *Type {
	n := len(fn.Params)
	if fn.restParam && i >= n-1 && n > 0 {
		rp := fn.Params[n-1]
		if rp.Flags&Object != 0 && rp.Kind == Array {
			return rp.Elem
		}
		return nil
	}
	if i < n {
		return fn.Params[i]
	}
	return nil
}

// contextSensitive reports whether an argument's type depends on the
// parameter it is passed to: a function expression or arrow with an
// unannotated parameter, or an object or array literal holding one
// (TypeScript's isContextSensitive).
func contextSensitive(e ast.Expression) bool {
	var params []ast.Param
	switch f := e.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	case *ast.ObjectLiteral:
		for _, p := range f.Properties {
			if p.Value != nil && contextSensitive(p.Value) {
				return true
			}
		}
		return false
	case *ast.ArrayLiteral:
		for _, x := range f.Elements {
			if contextSensitive(x) {
				return true
			}
		}
		return false
	default:
		return false
	}
	for _, p := range params {
		if p.Type == nil {
			return true
		}
	}
	return false
}

// inferArgs infers a generic call's type arguments (TypeScript's
// inference): explicit ones as written; otherwise from each argument's
// type against its parameter's, the context-insensitive arguments first,
// then the callbacks, typed from what the first pass inferred. withSensitive
// false stops after the first pass (a callback asking for its own context).
func (c *Checker) inferArgs(callArgs []ast.Expression, typeArgs []*ast.TypeAnnotation, fn *Type, withSensitive bool) map[*Type]*Type {
	m := map[*Type]*Type{}
	if len(typeArgs) > 0 {
		for i, tp := range fn.TypeParams {
			if i < len(typeArgs) && typeArgs[i] != nil && typeArgs[i].TypeNode() != nil {
				if t := c.typeFromNode(typeArgs[i].TypeNode(), c.b.Module); !c.Unanswered(t) {
					m[tp] = t
					continue
				}
			}
			m[tp] = c.unanswered
		}
		return m
	}
	cands := map[*Type][]*Type{}
	// poisoned marks the type parameters an unanswered argument could have
	// inferred: their answer is unanswered too, never a guess.
	poisoned := map[*Type]bool{}
	infer := func(i int, a ast.Expression) {
		p := paramAt(fn, i)
		if p == nil {
			return
		}
		at := c.argTypeFor(a, p)
		// A generic function argument (`map(xs, first)`) infers through
		// its own instantiation (tsc's instantiateSignatureInContextOf),
		// which is not modelled: what it could infer is unanswered too.
		if c.Unanswered(at) || at.Flags&Object != 0 && at.Kind == Function && len(at.TypeParams) > 0 {
			for _, tp := range fn.TypeParams {
				if occurs(tp, p) {
					poisoned[tp] = true
				}
			}
			return
		}
		c.unify(p, at, fn, cands)
	}
	for i, a := range callArgs {
		if !contextSensitive(a) {
			infer(i, a)
		}
	}
	if withSensitive {
		for i, a := range callArgs {
			if contextSensitive(a) {
				infer(i, a)
			}
		}
	}
	for _, tp := range fn.TypeParams {
		if poisoned[tp] {
			m[tp] = c.unanswered
			continue
		}
		cs := cands[tp]
		if len(cs) == 0 {
			if withSensitive {
				m[tp] = c.unknownT // nothing to infer from: TypeScript infers unknown
			}
			continue
		}
		t := c.in.union(cs...)
		if !c.keepsLiterals(tp, fn) {
			t = widen(c, t)
		}
		m[tp] = t
	}
	return m
}

// keepsLiterals reports whether a type parameter's inference keeps literal
// types: when it has a primitive constraint, or occurs at the top level of
// the return type (`id(1)` is 1, `wrap(1)` is number[]).
func (c *Checker) keepsLiterals(tp, fn *Type) bool {
	if c.primitiveConstraint(tp) {
		return true
	}
	for _, m := range members(fn.Result) {
		if m == tp {
			return true
		}
	}
	return false
}

// unify collects inference candidates for fn's type parameters from an
// argument of type a passed where p is expected.
func (c *Checker) unify(p, a *Type, fn *Type, cands map[*Type][]*Type) {
	switch {
	case p.Flags&TypeParam != 0:
		for _, tp := range fn.TypeParams {
			if tp == p {
				cands[p] = append(cands[p], a)
			}
		}
		return
	case p.Flags&Union != 0:
		// Match a's members against p's fixed members; what is left infers
		// the one generic member (`T | undefined` from `number | undefined`).
		var generic []*Type
		fixed := map[*Type]bool{}
		for _, m := range p.Types {
			if hasTypeParam(m) {
				generic = append(generic, m)
			} else {
				fixed[m] = true
			}
		}
		if len(generic) != 1 {
			return
		}
		var rest []*Type
		for _, m := range members(a) {
			if !fixed[m] {
				rest = append(rest, m)
			}
		}
		if len(rest) > 0 {
			c.unify(generic[0], c.in.union(rest...), fn, cands)
		}
		return
	case p.Flags&Object == 0 || a.Flags&Object == 0:
		return
	}
	if p.Kind == Function {
		// A callable object or an overload list infers through its last
		// signature, as TypeScript pairs signatures from the end.
		if len(a.Calls) > 0 {
			a = a.Calls[len(a.Calls)-1]
		} else if a.Kind == Function && len(a.Overloads) > 0 {
			a = a.Overloads[len(a.Overloads)-1]
		}
	}
	switch {
	case (p.Kind == Interface || p.Kind == Instance) && p.Kind == a.Kind && p.Symbol == a.Symbol && len(p.TypeArgs) == len(a.TypeArgs):
		// One generic declaration: its type arguments pairwise.
		for i := range p.TypeArgs {
			c.unify(p.TypeArgs[i], a.TypeArgs[i], fn, cands)
		}
	case p.Kind == Array && a.Kind == Array:
		c.unify(p.Elem, a.Elem, fn, cands)
	case p.Kind == Array && a.Kind == Tuple:
		for _, e := range a.Elems {
			c.unify(p.Elem, e, fn, cands)
		}
	case p.Kind == Tuple && a.Kind == Tuple && len(p.Elems) == len(a.Elems):
		for i := range p.Elems {
			c.unify(p.Elems[i], a.Elems[i], fn, cands)
		}
	case p.Kind == Function && a.Kind == Function:
		for i := range p.Params {
			if i < len(a.Params) {
				c.unify(p.Params[i], a.Params[i], fn, cands)
			}
		}
		c.unify(p.Result, a.Result, fn, cands)
		if p.Predicate != nil && a.Predicate != nil && p.Predicate.Type != nil && a.Predicate.Type != nil {
			c.unify(p.Predicate.Type, a.Predicate.Type, fn, cands) // `p1 is T` from `p1 is C`
		}
	case p.Kind == Anonymous:
		for _, pp := range p.Props {
			if ap := a.Prop(pp.Name); ap != nil {
				c.unify(pp.Type, ap.Type, fn, cands)
			}
		}
	case p.Kind == Interface && c.unifyDepth < 3:
		// Another declaration (`ReadonlyArray<T>` from an `Array<string>`):
		// member by member, through the source's apparent type (tsc's
		// inferFromProperties).
		src := a
		if a.Kind == Array || a.Kind == Tuple {
			if at := c.apparentType(a); at != nil {
				src = at
			}
		}
		c.unifyDepth++
		defer func() { c.unifyDepth-- }()
		for _, pp := range p.Props {
			if ap := src.Prop(pp.Name); ap != nil && hasTypeParam(pp.Type) {
				c.unify(pp.Type, ap.Type, fn, cands)
			}
		}
	}
}

// occurs reports whether the type parameter tp occurs in t.
func occurs(tp, t *Type) bool {
	switch {
	case t == tp:
		return true
	case t.Flags&Union != 0:
		for _, m := range t.Types {
			if occurs(tp, m) {
				return true
			}
		}
	case t.Flags&Object != 0:
		switch t.Kind {
		case Array:
			return occurs(tp, t.Elem)
		case Tuple:
			for _, e := range t.Elems {
				if occurs(tp, e) {
					return true
				}
			}
		case Function:
			for _, p := range t.Params {
				if occurs(tp, p) {
					return true
				}
			}
			return occurs(tp, t.Result)
		case Anonymous:
			for _, p := range t.Props {
				if occurs(tp, p.Type) {
					return true
				}
			}
		case Interface, Instance:
			for _, a := range t.TypeArgs {
				if occurs(tp, a) {
					return true
				}
			}
		}
	}
	return false
}

func hasTypeParam(t *Type) bool {
	switch {
	case t.Flags&TypeParam != 0:
		return true
	case t.Flags&Union != 0:
		for _, m := range t.Types {
			if hasTypeParam(m) {
				return true
			}
		}
	case t.Flags&Object != 0:
		switch t.Kind {
		case Array:
			return hasTypeParam(t.Elem)
		case Tuple:
			for _, e := range t.Elems {
				if hasTypeParam(e) {
					return true
				}
			}
		case Function:
			for _, p := range t.Params {
				if hasTypeParam(p) {
					return true
				}
			}
			return hasTypeParam(t.Result)
		case Anonymous:
			for _, p := range t.Props {
				if hasTypeParam(p.Type) {
					return true
				}
			}
		}
	}
	return false
}

// contextualParam is the type the context gives parameter i of the function
// expression or arrow fn: the matching parameter of the function type it is
// passed or assigned to (a generic callee instantiated from its other
// arguments). nil when there is none.
func (c *Checker) contextualParam(fn ast.Node, i int, rest bool) *Type {
	ct := c.contextualType(fn)
	if ct == nil || ct.Flags&Object == 0 || ct.Kind != Function {
		return nil
	}
	if rest {
		// A rest parameter takes the rest of the contextual signature's
		// parameters (tsc's getRestTypeAtPosition): its rest array, or the
		// tuple of the parameters from i on.
		n := len(ct.Params)
		switch {
		case ct.restParam && i == n-1:
			if t := ct.Params[i]; !hasTypeParam(t) {
				return t
			}
		case !ct.restParam && i <= n:
			for j := i; j < n; j++ {
				if j < len(ct.optionals) && ct.optionals[j] || hasTypeParam(ct.Params[j]) {
					return nil
				}
			}
			return c.in.tuple(append([]*Type(nil), ct.Params[i:]...))
		}
		return nil
	}
	if t := paramAt(ct, i); t != nil && !hasTypeParam(t) {
		return t
	}
	return nil
}

// contextualType is the type expected where the expression e occurs: a
// call's parameter, or an annotated variable's type.
func (c *Checker) contextualType(e ast.Node) *Type {
	if x, ok := e.(ast.Expression); ok {
		if a, ok := c.assertion(x); ok && a.Satisfies != nil && a.Satisfies.TypeNode() != nil {
			if t := c.typeFromNode(a.Satisfies.TypeNode(), c.b.Module); !c.Unanswered(t) {
				return t // `e satisfies T` types e in T's context
			}
		}
	}
	switch p := c.parentOf(e).(type) {
	case *ast.ObjectLiteral:
		// A property's value: the property of the literal's own context.
		for _, prop := range p.Properties {
			if ast.Node(prop.Value) == e && prop.KeyExpr == nil && prop.Key != "" {
				ct := c.contextualType(p)
				if ct == nil || ct.Flags&Object == 0 || (ct.Kind != Anonymous && ct.Kind != Interface && ct.Kind != Instance) {
					return nil
				}
				if tp := ct.Prop(prop.Key); tp != nil {
					return tp.Type
				}
			}
		}
	case *ast.ArrowFunction:
		// An expression body: the contextual signature's result (an
		// annotated result is its own context).
		if ast.Node(p.Body) == e && p.RetType == nil && !p.IsAsync && !c.contextualizing[p] {
			c.contextualizing[p] = true
			ct := c.contextualType(p)
			delete(c.contextualizing, p)
			if ct != nil && ct.Flags&Object != 0 && ct.Kind == Function && ct.Result != nil && !c.Unanswered(ct.Result) {
				return ct.Result
			}
		}
	case *ast.CallExpression:
		if ast.Node(p.Callee) == e {
			// An immediately invoked function: it returns what the call's
			// context expects.
			switch f := e.(type) {
			case *ast.ArrowFunction:
				if f.IsAsync {
					return nil
				}
			case *ast.FunctionExpression:
				if f.IsAsync || f.IsGenerator {
					return nil
				}
			default:
				return nil
			}
			if ct := c.contextualType(p); ct != nil && !c.Unanswered(ct) {
				return c.in.function(nil, nil, false, ct, nil, nil)
			}
			return nil
		}
		for j, a := range p.Args {
			if ast.Node(a) != e {
				continue
			}
			callee := c.TypeOf(p.Callee)
			if callee.Flags&Object == 0 || callee.Kind != Function || p.Optional {
				return nil
			}
			if len(callee.Overloads) > 0 {
				// The overload the call resolves to gives the context.
				if c.contextualizing[p] {
					return nil
				}
				c.contextualizing[p] = true
				sig, m, ok := c.pickOverload(p, callee, false)
				delete(c.contextualizing, p)
				if !ok {
					return nil
				}
				return c.instantiate(paramAt(sig, j), m)
			}
			pt := paramAt(callee, j)
			if pt == nil {
				return nil
			}
			if len(callee.TypeParams) > 0 {
				pt = c.instantiate(pt, c.contextualInference(c.inferArgs(p.Args, p.TypeArgs, callee, false), callee))
			}
			return pt
		}
	case *ast.NewExpression:
		for j, a := range p.Args {
			if ast.Node(a) != e {
				continue
			}
			sym := c.b.NewTarget(p)
			if sym == nil || sym.Flags&binder.Class == 0 {
				return nil
			}
			ctor := c.constructorType(sym)
			if ctor == nil {
				return nil
			}
			pt := paramAt(ctor, j)
			if pt != nil && len(ctor.TypeParams) > 0 {
				pt = c.instantiate(pt, c.contextualInference(c.inferArgs(p.Args, p.TypeArgs, ctor, false), ctor))
			}
			return pt
		}
	case *ast.AwaitExpression:
		// An awaited operand: the await's own context (tsc's
		// getContextualTypeForAwaitOperand, without its PromiseLike arm).
		if ast.Node(p.Argument) == e {
			return c.contextualType(p)
		}
	case *ast.VarDeclaration:
		if ast.Node(p.Init) == e && p.TypeAnnot != nil && p.TypeAnnot.TypeNode() != nil {
			if t := c.typeFromNode(p.TypeAnnot.TypeNode(), c.b.Module); !c.Unanswered(t) {
				return t
			}
		}
	}
	return nil
}

// contextualInference is what a callback's parameters see of the first
// pass's inferences: literals widened (`apply(2, x => …)` gives x number,
// though the call's own T keeps 2), unless the type parameter's constraint
// is primitive.
func (c *Checker) contextualInference(m map[*Type]*Type, fn *Type) map[*Type]*Type {
	out := map[*Type]*Type{}
	for tp, t := range m {
		if !c.primitiveConstraint(tp) {
			t = widen(c, t)
		}
		out[tp] = t
	}
	return out
}

func (c *Checker) primitiveConstraint(tp *Type) bool {
	if ct := tp.Constraint; ct != nil {
		for _, m := range members(ct) {
			if m.Flags&(String|Number|Boolean|BigInt|Literal) != 0 {
				return true
			}
		}
	}
	return false
}

// contextualOr is the type of the parameter sym of a function expression or
// arrow fn: annotated, else contextual, else its default's.
func (c *Checker) contextualOr(fn ast.Node, sym *binder.Symbol, params []ast.Param) *Type {
	for i, p := range params {
		if p.Name != sym.Name || p.ArrayPattern != nil || p.ObjectPattern != nil || p.Type != nil {
			continue
		}
		if ct := c.contextualParam(fn, i, p.Rest); ct != nil {
			return ct
		}
	}
	return c.paramType(fn, sym, params, sym.Scope)
}

// parentOf returns the node whose child n is, from a parent table built on
// first use over the whole program.
func (c *Checker) parentOf(n ast.Node) ast.Node {
	if c.parents == nil {
		c.parents = map[ast.Node]ast.Node{}
		var walk func(p ast.Node)
		walk = func(p ast.Node) {
			ast.ForEachChild(p, func(ch ast.Node) bool {
				c.parents[ch] = p
				walk(ch)
				return true
			})
		}
		if c.b.Module != nil && c.b.Module.Node != nil {
			walk(c.b.Module.Node)
		}
	}
	return c.parents[n]
}

// thisType is the type of `this` at e: in a class's instance method,
// constructor or field initializer (through arrows, which keep the
// enclosing `this`), the class's instance, a generic class's over its own
// type parameters. Elsewhere (a static member, a function expression, an
// object literal's method, module code) it is unanswered.
func (c *Checker) thisType(e ast.Node) *Type {
	child := e
	for n := c.parentOf(e); n != nil; child, n = n, c.parentOf(n) {
		switch x := n.(type) {
		case *ast.ArrowFunction:
			continue
		case *ast.FunctionExpression:
			// An explicit `this: T`, else the contextual signature's `this`
			// (getContextualThisParameterType).
			if t := c.thisParamType(x, c.b.ScopeOf(x)); t != nil {
				return t
			}
			if ct := c.contextualType(x); ct != nil && !c.Unanswered(ct) {
				if sig := c.signaturesOf(ct, false); sig.Flags&Object != 0 && sig.Kind == Function && sig.ThisType != nil {
					return sig.ThisType
				}
			}
			return c.unanswered
		case *ast.ObjectLiteral:
			return c.unanswered
		case *ast.FunctionDeclaration:
			if prog, ok := c.b.Module.Node.(*ast.Program); ok && prog.ThisParams[x] != nil {
				// An explicit `this: T` parameter types `this`.
				if tn := prog.ThisParams[x].TypeNode(); tn != nil {
					t := c.typeFromNode(tn, c.b.Module)
					if narrowable(t) || t.Flags&Object != 0 && t.Kind == Anonymous {
						return c.unanswered // `this` does not narrow yet
					}
					return t
				}
				return c.unanswered
			}
			cls, ok := c.parentOf(x).(*ast.ClassDeclaration)
			if !ok || x.IsStatic {
				return c.unanswered
			}
			return c.classInstance(cls)
		case *ast.ClassDeclaration:
			if c.staticMember(x, child) {
				return c.unanswered // a static block or field initializer
			}
			return c.classInstance(x) // a field initializer
		}
	}
	return c.unanswered
}

// staticMember reports whether child, a node directly under cls, is a
// static block or a static field's initializer.
func (c *Checker) staticMember(cls *ast.ClassDeclaration, child ast.Node) bool {
	for _, b := range cls.StaticBlocks {
		if ast.Node(b) == child {
			return true
		}
	}
	for _, f := range cls.Fields {
		if f.Static && f.Initializer != nil && ast.Node(f.Initializer) == child {
			return true
		}
	}
	return false
}

// classInstance is the instance type of the class cls declares, as seen
// from inside it.
func (c *Checker) classInstance(cls *ast.ClassDeclaration) *Type {
	sym := c.symbolOf(cls)
	if sym == nil {
		return c.unanswered
	}
	var args []*Type
	for _, n := range cls.TypeParams {
		args = append(args, c.classTypeParam(sym, n))
	}
	if t := c.instanceOf(sym, args); t != nil {
		return t
	}
	return c.unanswered
}

// symbolOf returns the symbol a declaration node declares, or nil.
func (c *Checker) symbolOf(n ast.Node) *binder.Symbol {
	if c.declSyms == nil {
		c.declSyms = map[ast.Node]*binder.Symbol{}
		for _, s := range c.b.Symbols() {
			for _, d := range s.Declarations {
				if d.Node != nil {
					if _, taken := c.declSyms[d.Node]; !taken {
						c.declSyms[d.Node] = s
					}
				}
			}
		}
	}
	return c.declSyms[n]
}

// literalSensitive reports arguments whose inferred type depends on whether
// their literal types widen: a literal, an object or array literal, or a
// callback (whose result is one).
func literalSensitive(args []ast.Expression) bool {
	for _, a := range args {
		switch a.(type) {
		case *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.TemplateLiteral,
			*ast.ObjectLiteral, *ast.ArrayLiteral, *ast.ArrowFunction, *ast.FunctionExpression:
			// A callback's result is inferred in the call's context too.
			return true
		}
	}
	return false
}

// mayHaveContext reports whether an expression may have a contextual type:
// anywhere but a statement of its own or an unannotated declaration's
// initializer. (A syntactic test: typing the context could need e's type.)
func (c *Checker) mayHaveContext(e ast.Expression) bool {
	switch p := c.parentOf(e).(type) {
	case *ast.ExpressionStatement:
		return false
	case *ast.VarDeclaration:
		return p.TypeAnnot != nil
	}
	return true
}

// rawContext is the contextual type e sees before any inference (a
// generic call's parameter as declared, over its type parameters): what
// decides whether a literal in e keeps its type.
func (c *Checker) rawContext(e ast.Node) *Type {
	switch p := c.parentOf(e).(type) {
	case *ast.ArrayLiteral:
		ct := c.rawContext(p)
		if ct == nil {
			return nil
		}
		for i, x := range p.Elements {
			if ast.Node(x) != e {
				continue
			}
			var out []*Type
			for _, m := range members(ct) {
				switch {
				case m.Flags&Object != 0 && m.Kind == Array:
					out = append(out, m.Elem)
				case m.Flags&Object != 0 && m.Kind == Tuple && i < len(m.Elems):
					out = append(out, m.Elems[i])
				}
			}
			if len(out) == 0 {
				return nil
			}
			return c.in.union(out...)
		}
	case *ast.ObjectLiteral:
		for _, prop := range p.Properties {
			if ast.Node(prop.Value) != e || prop.KeyExpr != nil || prop.Key == "" {
				continue
			}
			ct := c.rawContext(p)
			if ct == nil {
				return nil
			}
			var out []*Type
			for _, m := range members(ct) {
				if m.Flags&Object != 0 && (m.Kind == Anonymous || m.Kind == Interface || m.Kind == Instance) {
					if tp := m.Prop(prop.Key); tp != nil {
						out = append(out, tp.Type)
					}
				}
			}
			if len(out) == 0 {
				return nil
			}
			return c.in.union(out...)
		}
	case *ast.CallExpression:
		for j, a := range p.Args {
			if ast.Node(a) != e {
				continue
			}
			callee := c.signaturesOf(c.TypeOf(p.Callee), false)
			if c.Unanswered(callee) || callee.Flags&Object == 0 || callee.Kind != Function || len(callee.Overloads) > 0 {
				return nil
			}
			return paramAt(callee, j)
		}
	case *ast.VarDeclaration:
		if ast.Node(p.Init) == e && p.TypeAnnot != nil && p.TypeAnnot.TypeNode() != nil {
			if t := c.typeFromNode(p.TypeAnnot.TypeNode(), c.b.Module); !c.Unanswered(t) {
				return t
			}
		}
	}
	return nil
}

// literalOfContext is tsc's isLiteralOfContextualType: a literal keeps its
// type (instead of widening) where its context is a literal type of its
// kind, or a type parameter constrained to its primitive.
func (c *Checker) literalOfContext(candidate, ctx *Type) bool {
	if ctx == nil || candidate.Flags&Literal == 0 {
		return false
	}
	for _, m := range members(ctx) {
		switch {
		case m.Flags&TypeParam != 0:
			if con := m.Constraint; con != nil {
				for _, k := range members(con) {
					if candidate.Flags&StringLiteral != 0 && k.Flags&StringLike != 0 ||
						candidate.Flags&NumberLiteral != 0 && k.Flags&NumberLike != 0 ||
						candidate.Flags&BigIntLiteral != 0 && k.Flags&BigIntLike != 0 ||
						candidate.Flags&BooleanLiteral != 0 && k.Flags&BooleanLike != 0 {
						return true
					}
				}
			}
		case candidate.Flags&StringLiteral != 0 && m.Flags&StringLiteral != 0,
			candidate.Flags&NumberLiteral != 0 && m.Flags&NumberLiteral != 0,
			candidate.Flags&BigIntLiteral != 0 && m.Flags&BigIntLiteral != 0,
			candidate.Flags&BooleanLiteral != 0 && m.Flags&BooleanLiteral != 0:
			return true
		}
	}
	return false
}

package checker

import (
	"strconv"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// typeParamType is the type a type parameter declares: interned per symbol,
// its constraint and default read from the declaring function's or class's
// `extends` clause and `= T`.
func (c *Checker) typeParamType(sym *binder.Symbol) *Type {
	// A class's type parameter is the one its instance type is built with.
	for _, d := range sym.Declarations {
		if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
			if cs := c.symbolOf(cd); cs != nil && cs.Flags&binder.Class != 0 {
				return c.classTypeParam(cs, sym.Name)
			}
		}
	}
	t, fresh := c.in.intern2("p"+strconv.Itoa(sym.ID), func() *Type { return &Type{Flags: TypeParam, Symbol: sym} })
	if !fresh {
		return t
	}
	for _, d := range sym.Declarations {
		var names []string
		var constraints, defaults []*ast.TypeAnnotation
		switch n := d.Node.(type) {
		case *ast.FunctionDeclaration:
			names, constraints, defaults = n.TypeParams, n.TypeParamConstraints, n.TypeParamDefaults
		case *ast.ArrowFunction:
			names, constraints, defaults = n.TypeParams, n.TypeParamConstraints, n.TypeParamDefaults
		case *ast.FunctionExpression:
			names, constraints, defaults = n.TypeParams, n.TypeParamConstraints, n.TypeParamDefaults
		case *ast.ClassDeclaration:
			names, constraints, defaults = n.TypeParams, n.TypeParamConstraints, n.TypeParamDefaults
		default:
			continue
		}
		read := func(as []*ast.TypeAnnotation, i int) *Type {
			if i >= len(as) || as[i] == nil {
				return nil
			}
			if tn := as[i].TypeNode(); tn != nil {
				if ct := c.typeFromNode(tn, sym.Scope); !c.Unanswered(ct) {
					return ct
				}
			}
			return nil
		}
		for i, name := range names {
			if name != sym.Name {
				continue
			}
			if ct := read(constraints, i); ct != nil {
				t.Constraint = ct
			}
			if dt := read(defaults, i); dt != nil {
				t.Default = dt
			}
		}
	}
	return t
}

// ErasedSignature is a generic function type with each of its own type
// parameters instantiated to any: the function a generic is as a value
// compiled once (an erased generic's signature).
func (c *Checker) ErasedSignature(t *Type) *Type {
	if t == nil || t.Flags&Object == 0 || t.Kind != Function || len(t.TypeParams) == 0 {
		return t
	}
	m := make(map[*Type]*Type, len(t.TypeParams))
	for _, tp := range t.TypeParams {
		m[tp] = c.anyT
	}
	return c.instantiate(t, m)
}

// instantiate substitutes m's types for the type parameters in t.
func (c *Checker) instantiate(t *Type, m map[*Type]*Type) *Type {
	if len(m) == 0 || t == nil {
		return t
	}
	if t.LiteralThis != nil {
		// A `ThisType<T>` marker instantiates with the object it marks.
		r := c.instantiateType(t, m)
		if lt := c.instantiate(t.LiteralThis, m); r.Flags&Object != 0 && r.LiteralThis != lt {
			r = c.in.withLiteralThis(r, lt)
		}
		return r
	}
	return c.instantiateType(t, m)
}

// instantiateType is instantiate, the `ThisType<T>` marker aside.
func (c *Checker) instantiateType(t *Type, m map[*Type]*Type) *Type {
	switch {
	case t.Flags&Deferred != 0:
		return c.instantiateDeferred(t, m)
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
		return c.in.tupleLabelled(es, t.Opt, t.Labels)
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
		fn := c.in.withParamNames(c.in.function(ps, t.optionals, t.restParam, c.instantiate(t.Result, m), pred, tps), t.ParamNames)
		if t.ThisType != nil {
			fn = c.in.withThis(fn, c.instantiate(t.ThisType, m))
		}
		return fn
	case Instance, Interface:
		// A generic class or interface reference (`Promise<T>`): the same
		// declaration over the substituted arguments.
		if len(t.TypeArgs) == 0 || t.Symbol == nil {
			return t
		}
		args := make([]*Type, len(t.TypeArgs))
		changed := false
		for i, a := range t.TypeArgs {
			args[i] = c.instantiate(a, m)
			changed = changed || args[i] != a
		}
		if !changed {
			return t
		}
		var r *Type
		if t.Kind == Instance {
			r = c.instanceOf(t.Symbol, args)
		} else {
			r = c.interfaceOf(t.Symbol, args)
		}
		if r == nil || c.Unanswered(r) {
			return t
		}
		return r
	case Anonymous:
		if t.RecursiveAlias() {
			return t // names no type parameter
		}
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
		r := c.in.indexed(props, str, num)
		if len(t.Calls) > 0 || len(t.Constructs) > 0 {
			calls := make([]*Type, len(t.Calls))
			for i, s := range t.Calls {
				calls[i] = c.instantiate(s, m)
			}
			ctors := make([]*Type, len(t.Constructs))
			for i, s := range t.Constructs {
				ctors[i] = c.instantiate(s, m)
			}
			r = c.in.withSignatures(r, calls, ctors)
		}
		return r
	}
	return t
}

// paramAt is the parameter type of fn an argument at index i binds to (a
// rest parameter's element type past the fixed ones).
// restTypeParam is fn's rest parameter when its type is one of owner's type
// parameters (`...args: A`), else nil.
func restTypeParam(fn, owner *Type) *Type {
	n := len(fn.Params)
	if !fn.restParam || n == 0 {
		return nil
	}
	rp := fn.Params[n-1]
	if rp.Flags&TypeParam == 0 {
		return nil
	}
	for _, tp := range owner.TypeParams {
		if tp == rp {
			return rp
		}
	}
	return nil
}

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
	m, _ := c.inferArgsDetail(callArgs, typeArgs, fn, withSensitive)
	return m
}

// inferArgsRecv is inferArgs for a call through recv (`recv.m(…)`, else
// nil): a generic `this: T` parameter infers from the receiver's type too
// (`flat<A>(this: A)`).
func (c *Checker) inferArgsRecv(recv ast.Expression, callArgs []ast.Expression, typeArgs []*ast.TypeAnnotation, fn *Type, withSensitive bool) map[*Type]*Type {
	m, _ := c.inferArgsDetailRecv(recv, callArgs, typeArgs, fn, withSensitive)
	return m
}

// callRecv is the receiver of n when it is a call through a property
// (`o.m(…)`), else nil.
func callRecv(n ast.Node) ast.Expression {
	call, ok := n.(*ast.CallExpression)
	if !ok {
		return nil
	}
	if m, ok := skipOuter(call.Callee).(*ast.MemberExpression); ok {
		return m.Object
	}
	return nil
}

func (c *Checker) inferArgsDetail(callArgs []ast.Expression, typeArgs []*ast.TypeAnnotation, fn *Type, withSensitive bool) (map[*Type]*Type, map[*Type]bool) {
	return c.inferArgsDetailRecv(nil, callArgs, typeArgs, fn, withSensitive)
}

// inferArgsDetail is inferArgs, also reporting the type parameters that no
// argument gave a candidate (fixed to their default or unknown).
func (c *Checker) inferArgsDetailRecv(recv ast.Expression, callArgs []ast.Expression, typeArgs []*ast.TypeAnnotation, fn *Type, withSensitive bool) (map[*Type]*Type, map[*Type]bool) {
	uninferred := map[*Type]bool{}
	m := map[*Type]*Type{}
	if len(typeArgs) > 0 {
		for i, tp := range fn.TypeParams {
			if i < len(typeArgs) && typeArgs[i] != nil && typeArgs[i].TypeNode() != nil {
				scope := c.b.TypeArgScope(typeArgs[i])
				if scope == nil {
					scope = c.b.Module
				}
				if t := c.typeFromNode(typeArgs[i].TypeNode(), scope); !c.Unanswered(t) {
					m[tp] = t
					continue
				}
			}
			m[tp] = c.unanswered
		}
		return m, uninferred
	}
	cands := map[*Type][]*Type{}
	savedKept := c.literalKept
	c.literalKept = map[*Type]bool{}
	defer func() { c.literalKept = savedKept }()
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
	// The receiver against a `this: T` parameter (tsc's inferTypes over
	// the this-argument).
	if recv != nil && fn.ThisType != nil {
		if rt := c.TypeOf(recv); !c.Unanswered(rt) {
			c.unify(fn.ThisType, rt, fn, cands)
		} else {
			for _, tp := range fn.TypeParams {
				if occurs(tp, fn.ThisType) {
					poisoned[tp] = true
				}
			}
		}
	}
	// A rest parameter typed by a type parameter (`...args: A`, A extends
	// any[]) infers the tuple of the arguments it collects (`[X, Y]`).
	restTP := restTypeParam(fn, fn)
	restAt := len(fn.Params) - 1
	if restTP != nil && len(callArgs) >= restAt {
		var elems []*Type
		ok := true
		for _, a := range callArgs[restAt:] {
			at := c.TypeOf(a)
			if c.Unanswered(at) {
				ok = false
				break
			}
			elems = append(elems, widen(c, at))
		}
		if ok {
			cands[restTP] = append(cands[restTP], c.in.tuple(elems))
		} else {
			poisoned[restTP] = true
		}
	}
	for i, a := range callArgs {
		if restTP != nil && i >= restAt {
			break
		}
		if !contextSensitive(a) {
			infer(i, a)
		}
	}
	if withSensitive {
		for i, a := range callArgs {
			if restTP != nil && i >= restAt {
				break
			}
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
			// Inference is complete when every argument was seen: the
			// second pass, or a call with no context-sensitive argument.
			if withSensitive || !anyContextSensitive(callArgs) {
				// Nothing to infer from: the default, else unknown (tsc's
				// getInferredType).
				uninferred[tp] = true
				m[tp] = c.unknownT
				if tp.Default != nil {
					m[tp] = tp.Default
				}
			}
			continue
		}
		t := c.in.union(cs...)
		keep := c.keepsLiterals(tp, fn) || c.keepLiterals || c.literalKept[tp]
		if !keep {
			t = widen(c, t)
		}
		if first, ok := c.firstPrimitiveCandidate(cs, keep); ok {
			t = first
		}
		m[tp] = t
	}
	return m, uninferred
}

// firstPrimitiveCandidate is tsc's common supertype of candidates of
// unrelated primitive types (`f(1, "x")` for `f<T>(a: T, b: T)`): none is a
// supertype of the others, so the leftmost wins (getCommonSupertype), its
// null and undefined kept, and the arguments of the other types then fail
// their check.
func (c *Checker) firstPrimitiveCandidate(cs []*Type, keepLiterals bool) (*Type, bool) {
	var firsts, bases, nulls []*Type
	for _, cand := range cs {
		// Each candidate is one inference site: a union one (an array's
		// mixed elements) is a single candidate, not several.
		var first, base *Type
		for _, m := range members(cand) {
			w := widen(c, m)
			switch {
			case w.Flags&(Null|Undefined) != 0:
				nulls = append(nulls, w)
			case w.Flags&(String|Number|Boolean|BigInt) != 0 && w.Flags&(Object|Union) == 0:
				if base == nil {
					first, base = m, w
				} else if base != w {
					return nil, false // a mixed site: a union candidate
				}
			default:
				return nil, false
			}
		}
		if base != nil {
			if !keepLiterals {
				first = base
			}
			firsts, bases = append(firsts, first), append(bases, base)
		}
	}
	if len(bases) < 2 {
		return nil, false
	}
	distinct := false
	for _, b := range bases[1:] {
		if b != bases[0] {
			distinct = true
		}
	}
	if !distinct {
		return nil, false // one primitive: its literals unite (`1 | 2`)
	}
	// The leftmost site, with the other sites' literals of its own kind.
	keep := []*Type{}
	for i, b := range bases {
		if b == bases[0] {
			keep = append(keep, firsts[i])
		}
	}
	return c.in.union(append(keep, nulls...)...), true
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
	key := [2]*Type{p, a}
	if c.unifying[key] {
		return
	}
	if c.unifying == nil {
		c.unifying = map[[2]*Type]bool{}
	}
	c.unifying[key] = true
	defer delete(c.unifying, key)
	switch {
	case p.Flags&Deferred != 0 && c.unifyDepth < 4:
		// A conditional type not yet decided: infer to both its branches
		// (tsc's inferToConditionalType).
		if br := c.deferredBranches(p); br != nil {
			c.unifyDepth++
			for _, b := range br {
				c.unify(b, a, fn, cands)
			}
			c.unifyDepth--
		}
		return
	case p.Flags&TypeParam != 0:
		for _, tp := range fn.TypeParams {
			if tp == p {
				cands[p] = append(cands[p], a)
			}
		}
		return
	case a.Flags&Union != 0 && p.Flags&Object != 0 && p.Kind == Tuple:
		// A union of tuples to a tuple: each member is its own inference
		// (tsc's inferFromTypes over a union source), so `[string, number]
		// | [number, string]` gives K the candidates string and number.
		for _, m := range a.Types {
			c.unify(p, m, fn, cands)
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
		var rest []*Type
		for _, m := range members(a) {
			if !fixed[m] {
				rest = append(rest, m)
			}
		}
		if len(generic) == 1 {
			if len(rest) > 0 {
				c.unify(generic[0], c.in.union(rest...), fn, cands)
				// `"a"` to `K | "z"`: a literal member of the parameter's
				// own kind keeps the inferred literal (`K | undefined` does
				// not).
				if g := generic[0]; g.Flags&TypeParam != 0 && c.literalKept != nil && literalOfKind(fixed, rest) {
					c.literalKept[g] = true
				}
			} else if g := generic[0]; g.Flags&TypeParam != 0 && len(cands[g]) == 0 && a.Flags&Never == 0 {
				// Every member matched a fixed one (`"data"` to `K | keyof
				// Ev`): rather than infer nothing, the whole source infers
				// the bare type parameter, at tsc's lower priority — so
				// only while nothing else has, and keeping its literals.
				c.unify(g, a, fn, cands)
				if c.literalKept != nil {
					c.literalKept[g] = true
				}
			}
			return
		}
		// Several generic members (`T | PromiseLike<T>`): each source
		// member infers to a member that is not a bare type parameter when
		// it matches one (Promise<number> to PromiseLike<T> gives number),
		// else to the bare one, as tsc's inferToMultipleTypes.
		var naked *Type
		var structured []*Type
		for _, g := range generic {
			if g.Flags&TypeParam != 0 {
				if naked != nil {
					return // two bare type parameters: no single target
				}
				naked = g
			} else {
				structured = append(structured, g)
			}
		}
		for _, m := range rest {
			before := countCands(cands)
			for _, g := range structured {
				c.unify(g, m, fn, cands)
			}
			if countCands(cands) == before && naked != nil {
				c.unify(naked, m, fn, cands)
			}
		}
		return
	case p.Flags&Object != 0 && p.Kind == Interface && isStringLike(a) && c.unifyDepth < 3:
		// A string to `Iterable<T>` / `ArrayLike<T>`: its characters, as
		// tsc infers through the String interface (its iterator, its
		// `[index: number]: string`).
		if p.Symbol != nil && (p.Symbol.Name == "Iterable" || p.Symbol.Name == "ArrayLike" || p.Symbol.Name == "IterableIterator") && len(p.TypeArgs) >= 1 {
			c.unify(p.TypeArgs[0], c.strT, fn, cands)
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
		if a.Kind == Function && len(a.TypeParams) > 0 && len(p.TypeParams) == 0 {
			// A generic source signature: its own parameters are unknown
			// (TypeScript's getErasedSignature for inference).
			m := map[*Type]*Type{}
			for _, tp := range a.TypeParams {
				m[tp] = c.unknownT
			}
			a = c.instantiate(a, m)
		}
	}
	// `Iterable<T>` (and its async and iterator kin) from any iterable: T is
	// what iterating the source yields, as tsc infers it through the
	// source's `[Symbol.iterator]` (which this checker does not model as a
	// member).
	if p.Kind == Interface && p.Symbol != nil && len(p.TypeArgs) >= 1 && !(a.Kind == p.Kind && a.Symbol == p.Symbol) {
		switch p.Symbol.Name {
		case "Iterable", "IterableIterator", "IteratorObject":
			if it := c.iteratedType(a); it != nil && !c.Unanswered(it) {
				c.unify(p.TypeArgs[0], it, fn, cands)
				return
			}
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
			if tp := restTypeParam(p, fn); tp != nil && i == len(p.Params)-1 && !a.restParam {
				// `(...args: A) => void` from `(q: X, r: Y) => void`: A is
				// the tuple of the remaining parameters, `[X, Y]`.
				var rest []*Type
				if i < len(a.Params) {
					rest = a.Params[i:]
				}
				c.unify(tp, c.in.tuple(rest), fn, cands)
				break
			}
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
		// A constructor type (`new () => T`) infers from the source's
		// construct signature, the last of an overload list.
		if len(p.Constructs) == 1 && len(a.Constructs) > 0 {
			c.unify(p.Constructs[0], a.Constructs[len(a.Constructs)-1], fn, cands)
		}
	case p.Kind == Instance && (a.Kind == Instance || a.Kind == Anonymous) && c.unifyDepth < 3:
		// Another class's instance (`class { prop = "hello" }` for `class {
		// prop: T }`): member by member, as tsc's inferFromProperties.
		c.unifyDepth++
		for _, pp := range p.Props {
			if ap := a.Prop(pp.Name); ap != nil {
				c.unify(pp.Type, ap.Type, fn, cands)
			}
		}
		c.unifyDepth--
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
		// … and index signature to index signature (tsc's
		// inferFromIndexTypes): `ArrayLike<T>` from a Uint8Array's
		// `[index: number]: number`.
		if p.NumberIndex != nil && hasTypeParam(p.NumberIndex) {
			if si := src.NumberIndex; si != nil {
				c.unify(p.NumberIndex, si, fn, cands)
			} else if si := src.StringIndex; si != nil {
				c.unify(p.NumberIndex, si, fn, cands)
			}
		}
		if p.StringIndex != nil && hasTypeParam(p.StringIndex) && src.StringIndex != nil {
			c.unify(p.StringIndex, src.StringIndex, fn, cands)
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
			if t.RecursiveAlias() {
				return false
			}
			for _, p := range t.Props {
				if occurs(tp, p.Type) {
					return true
				}
			}
			for _, s := range append(append([]*Type{}, t.Calls...), t.Constructs...) {
				if occurs(tp, s) {
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

func hasTypeParam(t *Type) bool { return hasTypeParamSeen(t, nil) }

// hasTypeParamSeen is hasTypeParam, seen holding the member-walked instances
// so a self-referential class ends.
func hasTypeParamSeen(t *Type, seen map[*Type]bool) bool { return hasFreeTypeParam(t, seen, nil) }

// hasFreeTypeParam is hasTypeParamSeen, a type parameter in bound (a
// generic signature's own, `on<K>(…)` on a class) not counting.
func hasFreeTypeParam(t *Type, seen, bound map[*Type]bool) bool {
	switch {
	case t.Flags&TypeParam != 0:
		// A member's polymorphic `this` is its instance's own, not a free
		// parameter.
		return !bound[t] && !(seen != nil && t.Value == "this")
	case t.Flags&Deferred != 0:
		// A member's unresolved library type (`ReturnType<typeof
		// setTimeout>`) names no parameter of the instance's context.
		return seen == nil
	case t.Flags&Union != 0:
		for _, m := range t.Types {
			if hasFreeTypeParam(m, seen, bound) {
				return true
			}
		}
	case t.Flags&Object != 0:
		switch t.Kind {
		case Array:
			return hasFreeTypeParam(t.Elem, seen, bound)
		case Tuple:
			for _, e := range t.Elems {
				if hasFreeTypeParam(e, seen, bound) {
					return true
				}
			}
		case Function:
			if len(t.TypeParams) > 0 && seen != nil { // a member's own signature
				inner := map[*Type]bool{}
				for k := range bound {
					inner[k] = true
				}
				for _, tp := range t.TypeParams {
					inner[tp] = true
				}
				bound = inner
			}
			for _, p := range t.Params {
				if hasFreeTypeParam(p, seen, bound) {
					return true
				}
			}
			return hasFreeTypeParam(t.Result, seen, bound)
		case Anonymous:
			if t.RecursiveAlias() {
				return false
			}
			for _, p := range t.Props {
				if hasFreeTypeParam(p.Type, seen, bound) {
					return true
				}
			}
			for _, s := range append(append([]*Type{}, t.Calls...), t.Constructs...) {
				if hasFreeTypeParam(s, seen, bound) {
					return true
				}
			}
		case Interface, Instance:
			// A generic declaration's reference (`Init<T>`): its arguments.
			for _, a := range t.TypeArgs {
				if hasFreeTypeParam(a, seen, bound) {
					return true
				}
			}
			// A class declared inside a generic function (`class { prop: T }`)
			// names the function's type parameters through its members.
			if t.Kind == Instance && len(t.TypeArgs) == 0 {
				return instanceNamesTypeParam(t, seen, bound)
			}
		}
	}
	return false
}

// instanceNamesTypeParam reports an instance member whose type holds a type
// parameter, through nested instances once each.
func instanceNamesTypeParam(t *Type, seen, bound map[*Type]bool) bool {
	if seen[t] {
		return false
	}
	if seen == nil {
		seen = map[*Type]bool{}
	}
	seen[t] = true
	for _, p := range t.Props {
		pt := p.Type
		if hasFreeTypeParam(pt, seen, bound) {
			return true
		}
	}
	return false
}

// contextualParam is the type the context gives parameter i of the function
// expression or arrow fn: the matching parameter of the function type it is
// passed or assigned to (a generic callee instantiated from its other
// arguments). nil when there is none.
func (c *Checker) contextualParam(fn ast.Node, i int, rest bool) *Type {
	// An immediately invoked function's parameters are its arguments'
	// (iifeParam), not its call's context's.
	if call, ok := c.parentOf(fn).(*ast.CallExpression); ok && ast.Node(call.Callee) == fn {
		if t := c.iifeParam(fn, i, rest); t != nil && !c.Unanswered(t) && t != c.anyT {
			return t
		}
		return nil
	}
	if c.iifeArgContextFree(fn) {
		return nil
	}
	// An argument of a call through any: tsc's untyped call resolves to
	// the any signature, whose parameters are any.
	if call, ok := c.parentOf(fn).(*ast.CallExpression); ok && ast.Node(call.Callee) != fn {
		if ct := c.TypeOf(call.Callee); !c.Unanswered(ct) && ct.Flags&Any != 0 {
			if rest {
				return c.in.array(c.anyT)
			}
			return c.anyT
		}
	}
	ct := c.callSignatureContext(c.contextualType(fn))
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
	if orig, ok := c.ctxStandIn[e]; ok {
		return c.contextualType(orig)
	}
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
			if sp, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" && ast.Node(sp.Arg) == e {
				return c.contextualType(p) // an object spread's operand: the literal's own context
			}
			if ast.Node(prop.Value) == e && prop.KeyExpr == nil && prop.Key != "" {
				ct := c.contextualType(p)
				if ct != nil && ct.Flags&Union != 0 {
					ct = c.nonNullableContext(ct) // `T | undefined`: T's property
				}
				if ct == nil || ct.Flags&Object == 0 || (ct.Kind != Anonymous && ct.Kind != Interface && ct.Kind != Instance) {
					return nil
				}
				if tp := ct.Prop(prop.Key); tp != nil {
					return c.nonNullableContext(tp.Type) // an optional member's, without its undefined
				}
			}
		}
	case *ast.SpreadElement:
		// An object spread's operand: the literal's own context (tsc's
		// getContextualTypeForObjectLiteralElement on a spread assignment).
		if ol, ok := c.parentOf(p).(*ast.ObjectLiteral); ok && ast.Node(p.Arg) == e {
			return c.contextualType(ol)
		}
	case *ast.ArrowFunction:
		// An expression body: the contextual signature's result (an
		// annotated result is its own context).
		if ast.Node(p.Body) == e && p.RetType == nil && !p.IsAsync && !c.contextualizing[p] {
			c.contextualizing[p] = true
			ct := c.contextualType(p)
			delete(c.contextualizing, p)
			ct = c.callSignatureContext(ct)
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
					// The pick needs this argument's own type: as tsc
					// checks it against each candidate in turn, the first
					// whose arity and other arguments fit gives it.
					return c.firstFittingOverloadParam(p, callee, j)
				}
				if rt := c.returnTypeContext(p, sig, j, m); rt != nil {
					return rt
				}
				if pt := paramAt(sig, j); pt != nil && len(sig.TypeParams) > 0 {
					m = copyMap(m)
					c.fixParamTypeParams(p, pt, sig, m)
				}
				// An optional parameter's context is its type without the
				// undefined (tsc's contextual signature skips nullish members).
				return c.nonNullableContext(c.instantiate(paramAt(sig, j), m))
			}
			pt := paramAt(callee, j)
			if pt == nil {
				return nil
			}
			if len(callee.TypeParams) > 0 && !c.inferring[p] {
				// While this call's own type arguments are inferred, an
				// argument's context is the uninstantiated parameter: typing
				// the argument must not start the same inference again.
				c.inferring[p] = true
				m := c.contextualInference(c.inferArgsRecv(callRecv(p), p.Args, p.TypeArgs, callee, false), callee)
				delete(c.inferring, p)
				if rt := c.returnTypeContext(p, callee, j, m); rt != nil {
					return rt
				}
				c.fixParamTypeParams(p, pt, callee, m)
				pt = c.instantiate(pt, m)
			}
			return c.nonNullableContext(pt)
		}
	case *ast.ReturnStatement:
		// A returned value: the enclosing function's declared result (an
		// async one's awaited), tsc's getContextualTypeForReturnExpression.
		if ast.Node(p.Value) == e {
			return c.returnContext(p)
		}
	case *ast.NewExpression:
		for j, a := range p.Args {
			if ast.Node(a) != e {
				continue
			}
			sym := c.b.NewTarget(p)
			if sym != nil && sym.Flags&binder.Class == 0 && p.Callee == nil {
				// A builtin constructor (`new Promise<T>(executor)`): its
				// construct signature's parameter.
				return c.constructParamContext(p, j)
			}
			if sym == nil || sym.Flags&binder.Class == 0 {
				return nil
			}
			ctor := c.constructorType(sym)
			if ctor == nil {
				return nil
			}
			pt := paramAt(ctor, j)
			if pt != nil && len(ctor.TypeParams) > 0 && !c.inferring[p] {
				c.inferring[p] = true
				pt = c.instantiate(pt, c.contextualInference(c.inferArgsRecv(callRecv(p), p.Args, p.TypeArgs, ctor, false), ctor))
				delete(c.inferring, p)
			}
			return pt
		}
	case *ast.ArrayLiteral:
		// An element: the element type of the literal's context (an
		// array's, or the tuple's at that index).
		for i, el := range p.Elements {
			if ast.Node(el) != e {
				continue
			}
			ct := c.contextualType(p)
			if ct == nil || c.Unanswered(ct) || ct.Flags&Object == 0 {
				return nil
			}
			switch ct.Kind {
			case Array:
				return ct.Elem
			case Tuple:
				if i < len(ct.Elems) {
					return ct.Elems[i]
				}
			}
			// A readonly array's, an iterable's or an array-like's element
			// (tsc's iterated type of the context).
			if ct.Symbol != nil && len(ct.TypeArgs) == 1 {
				switch ct.Symbol.Name {
				case "ReadonlyArray", "Iterable", "ArrayLike":
					return ct.TypeArgs[0]
				}
			}
			return nil
		}
	case *ast.ConditionalExpression:
		// A branch: the conditional's own context, so `c ? null : [1, "a"]`
		// is a tuple where a tuple is expected.
		if ast.Node(p.Consequent) == e || ast.Node(p.Alternate) == e {
			return c.contextualType(p)
		}
	case *ast.BinaryExpression:
		// `a || e`, `a ?? e`, `a && e`: an operand the result can be takes
		// the result's context (tsc's getContextualTypeForBinaryOperand).
		switch p.Op {
		case "||", "??":
			return c.contextualType(p)
		case "&&":
			if ast.Node(p.Right) == e {
				return c.contextualType(p)
			}
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
	case *ast.ArrayDestructuring:
		if ast.Node(p.Init) == e {
			if t := c.patternAnnotation(p.TypeAnnot); t != nil && !c.Unanswered(t) {
				return t
			}
		}
	case *ast.ObjectDestructuring:
		if ast.Node(p.Init) == e {
			if t := c.patternAnnotation(p.TypeAnnot); t != nil && !c.Unanswered(t) {
				return t
			}
		}
	case *ast.AssignmentExpression:
		// `x = e`: e in the context of x's declared type (tsc's assignment
		// context), so `pt = [a, b]` is a tuple where pt is declared one.
		if ast.Node(p.Right) == e && (p.Op == "=" || p.Op == "??=" || p.Op == "||=" || p.Op == "&&=") {
			if id, ok := p.Left.(*ast.Identifier); ok {
				// An auto-typed binding (`let f;`) gives no context: tsc's
				// declared type for it is the auto type, and its storage
				// type is built from this very value.
				if sym, _ := c.b.Resolve(id); sym != nil && !evolvingDecl(sym) {
					if t := c.typeOfSymbol(sym); t != nil && !c.Unanswered(t) {
						return t
					}
				}
			}
			// `o.p = e`: p's declared type (`ws.onmessage = (ev) => …`). An
			// expando assignment (`f.p = e` on a function) declares p, typed
			// from e: no context.
			if m, ok := p.Left.(*ast.MemberExpression); ok && !m.Optional && !c.expandoMember(m) {
				if t := c.StorageTypeOf(m); t != nil && !c.Unanswered(t) && t.Flags&Any == 0 {
					return c.nonNullableContext(t)
				}
			}
		}
	default:
		// An argument of the front end's own `new` node (`new
		// ReadableStream<T>({ pull(c) {} })`): the global's construct
		// signature's parameter, as for a NewExpression.
		if name, args, targs, ok := builtinNewParts(p); ok {
			for j, a := range args {
				if ast.Node(a) == e {
					if sym := c.builtinCtorSym(name); sym != nil {
						if v := c.typeOfSymbol(sym); v != nil && !c.Unanswered(v) && v.Flags&Object != 0 {
							return c.constructsParamContext(v.Constructs, targs, j)
						}
					}
					return nil
				}
			}
		}
	}
	return nil
}

// contextualInference is what a callback's parameters see of the first
// pass's inferences: literals widened (`apply(2, x => …)` gives x number,
// though the call's own T keeps 2), unless the type parameter's constraint
// is primitive.
// fixParamTypeParams fixes the type parameters a context-sensitive
// argument's contextual signature takes as parameters but nothing inferred
// (`Array.from({ length: n }, (_, i) => …)`'s T): their default, else
// unknown, else the constraint unknown does not satisfy, as tsc's
// getInferredType fixes them before typing the argument. One that occurs
// only in its result is left to be inferred from it.
func (c *Checker) fixParamTypeParams(e *ast.CallExpression, pt, callee *Type, m map[*Type]*Type) {
	for _, sig := range members(pt) {
		if sig.Flags&Object == 0 || sig.Kind != Function {
			continue
		}
		for _, tp := range callee.TypeParams {
			if _, done := m[tp]; done {
				continue
			}
			if !c.wholeArgsFor(e, callee, tp) {
				continue // an argument that may hold it is only partly known
			}
			for _, prm := range sig.Params {
				if occurs(tp, prm) {
					t := c.unknownT
					if tp.Default != nil {
						t = tp.Default
					} else if tp.Constraint != nil && !c.Unanswered(tp.Constraint) && !c.assignable(c.unknownT, tp.Constraint) {
						t = tp.Constraint
					}
					m[tp] = t
					break
				}
			}
		}
	}
}

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
// returnContext is the declared result of the function a return statement
// is in: unwrapped from its Promise for an async function; none for a
// generator or an unannotated function.
func (c *Checker) returnContext(r *ast.ReturnStatement) *Type {
	for n := c.parentOf(r); n != nil; n = c.parentOf(n) {
		var ta *ast.TypeAnnotation
		async := false
		switch f := n.(type) {
		case *ast.FunctionDeclaration:
			if f.IsGenerator {
				return nil
			}
			ta, async = f.ReturnType, f.IsAsync
		case *ast.FunctionExpression:
			if f.IsGenerator {
				return nil
			}
			ta, async = f.RetType, f.IsAsync
		case *ast.ArrowFunction:
			ta, async = f.RetType, f.IsAsync
		default:
			continue
		}
		t := c.declaredReturn(n, ta, false)
		if t == nil {
			return nil
		}
		if async {
			t = c.awaited(t, 0)
		}
		return t
	}
	return nil
}

// constructParamContext is parameter j of the construct signature of a
// builtin constructor value `new X<T>(…)` calls, with its explicit type
// arguments; nil when there is none.
func (c *Checker) constructParamContext(p *ast.NewExpression, j int) *Type {
	ctorVal := c.typeOfSymbol(c.b.NewTarget(p))
	if ctorVal == nil || c.Unanswered(ctorVal) || ctorVal.Flags&Object == 0 {
		return nil
	}
	if len(p.TypeArgs) == 0 {
		// The type parameters no explicit argument names: what the other
		// arguments and the expression's context infer, else unknown
		// (`new Promise((resolve) => …)` is Promise<unknown>). Inferring
		// them types the arguments, the one asking among them: asked again
		// meanwhile, it has no context.
		if c.paramCtx[p] {
			return nil
		}
		if c.paramCtx == nil {
			c.paramCtx = map[*ast.NewExpression]bool{}
		}
		c.paramCtx[p] = true
		defer delete(c.paramCtx, p)
		for _, sig := range ctorVal.Constructs {
			pt := paramAt(sig, j)
			if pt == nil {
				continue
			}
			if len(sig.TypeParams) == 0 {
				return c.nonNullableContext(pt)
			}
			m := c.inferArgsRecv(callRecv(p), p.Args, nil, sig, false)
			c.inferFromReturnContext(p, sig, m)
			for _, tp := range sig.TypeParams {
				if m[tp] == nil {
					m[tp] = c.unknownT
					if tp.Default != nil {
						m[tp] = tp.Default
					}
				}
				if c.Unanswered(m[tp]) {
					return nil
				}
			}
			return c.nonNullableContext(c.instantiate(pt, m))
		}
		return nil
	}
	return c.constructsParamContext(ctorVal.Constructs, p.TypeArgs, j)
}

// inferFromReturnContext fills m's type parameters that the arguments did
// not infer (absent, or unknown for want of a candidate) from the call's
// contextual type, matched against the signature's result (tsc's
// inference from the return type: `const p: Promise<number> = new
// Promise(…)`).
func (c *Checker) inferFromReturnContext(e ast.Expression, sig *Type, m map[*Type]*Type) {
	if sig.Result == nil || c.returnCtx[e] {
		// Re-entered while e's own context is being typed (an argument of an
		// enclosing generic call whose inference types e again): the
		// arguments alone answer this inner query.
		return
	}
	if c.returnCtx == nil {
		c.returnCtx = map[ast.Expression]bool{}
	}
	c.returnCtx[e] = true
	ct := c.contextualType(e)
	delete(c.returnCtx, e)
	if ct == nil || c.Unanswered(ct) {
		return
	}
	cands := map[*Type][]*Type{}
	c.unify(sig.Result, ct, sig, cands)
	if _, awaited := c.parentOf(e).(*ast.AwaitExpression); len(cands) == 0 && (awaited || c.asyncReturnValue(e)) {
		// An async function's return context is `T | PromiseLike<T>` in
		// tsc; a promise the value is matched against it as Promise<T>.
		if pt := c.promiseOf(ct); pt != nil {
			c.unify(sig.Result, pt, sig, cands)
		}
	}
	for _, tp := range sig.TypeParams {
		if cs := cands[tp]; len(cs) > 0 && (m[tp] == nil || m[tp] == c.unknownT || m[tp] == tp.Default && tp.Default != nil) {
			m[tp] = c.in.union(cs...)
		}
	}
}

// constructsParamContext is parameter j of the first of sigs that has one,
// instantiated by the explicit type arguments.
func (c *Checker) constructsParamContext(sigs []*Type, typeArgs []*ast.TypeAnnotation, j int) *Type {
	for _, sig := range sigs {
		pt := paramAt(sig, j)
		if pt == nil {
			continue
		}
		if len(sig.TypeParams) > 0 && len(typeArgs) == len(sig.TypeParams) {
			m := map[*Type]*Type{}
			for i, tp := range sig.TypeParams {
				ta := typeArgs[i]
				if ta == nil || ta.TypeNode() == nil {
					return pt
				}
				m[tp] = c.typeFromNode(ta.TypeNode(), c.b.Module)
			}
			pt = c.instantiate(pt, m)
		} else if len(sig.TypeParams) > 0 && len(typeArgs) == 0 {
			// No type argument and nothing to infer from but the callback
			// itself: each type parameter's default (`R = any`).
			m := map[*Type]*Type{}
			for _, tp := range sig.TypeParams {
				if tp.Default == nil {
					return c.nonNullableContext(pt)
				}
				m[tp] = tp.Default
			}
			pt = c.instantiate(pt, m)
		}
		return c.nonNullableContext(pt)
	}
	return nil
}

// literalInContext reports whether a literal holding a function sits where
// a contextual type reaches it: its own contextual type, unless that is
// any (which gives a member nothing), else an argument of a callee this
// checker could not type (the benefit of the doubt), or an element of a
// literal that is itself in context.
func (c *Checker) literalInContext(lit ast.Expression) bool {
	if ct := c.contextualType(lit); ct != nil {
		return c.Unanswered(ct) || ct.Flags&Any == 0
	}
	switch p := c.parentOf(lit).(type) {
	case *ast.CallExpression, *ast.NewExpression:
		return true
	case *ast.ObjectLiteral:
		return c.literalInContext(p)
	case *ast.ArrayLiteral:
		return c.literalInContext(p)
	case *ast.ConditionalExpression:
		return c.literalInContext(p)
	case *ast.SpreadElement:
		return c.literalInContext(p)
	}
	return false
}

// contextuallyTyped reports whether parameter i of fn has a contextual
// signature to take its type from (even one naming a type parameter), so an
// unannotated parameter there is not an implicit any.
//
// A function in a position that has a context (an argument, a property
// value, an assigned or returned value, an element, a branch) counts as
// typed even where the checker cannot name that context yet (a callee it
// has no type for): reporting there would reject what TypeScript accepts.
func (c *Checker) contextuallyTyped(fn ast.Node, i int) bool {
	if c.iifeArgContextFree(fn) {
		return false
	}
	if ct := c.contextualType(fn); ct != nil {
		// A union's function member gives the signature (`f: F | null`).
		if !c.Unanswered(ct) {
			ct = c.nonNullable(ct)
		}
		if ct.Flags&Object != 0 && (ct.Kind == Function || len(ct.Calls) > 0) {
			return true
		}
		if ct.Flags&Union != 0 {
			for _, m := range ct.Types {
				if m.Flags&Object != 0 && (m.Kind == Function || len(m.Calls) > 0) {
					return true
				}
			}
		}
	}
	switch p := c.parentOf(fn).(type) {
	case *ast.ObjectLiteral:
		// A method or property value: typed when the literal itself sits
		// where a context reaches it (`const o = { m(x) {} }` has none,
		// and tsc reports x).
		return c.literalInContext(p)
	case *ast.ArrayLiteral:
		return c.literalInContext(p) // as an object literal's member
	case *ast.CallExpression, *ast.NewExpression, *ast.ReturnStatement,
		*ast.ConditionalExpression, *ast.AwaitExpression, *ast.SpreadElement:
		return true
	case *ast.AssignmentExpression:
		// Only a function type gives an assigned value's parameters
		// context (above): an `any` or auto-typed (`let f;`) name leaves
		// them implicitly any, as tsc reports. A target this checker
		// cannot type is given the benefit of the doubt.
		if ast.Node(p.Right) != fn {
			return false
		}
		if id, ok := p.Left.(*ast.Identifier); ok {
			if sym, _ := c.b.Resolve(id); sym != nil {
				if evolvingDecl(sym) {
					return false
				}
				if t := c.typeOfSymbol(sym); t != nil && !c.Unanswered(t) {
					return false
				}
			}
		}
		return true
	case *ast.BinaryExpression:
		return p.Op == "||" || p.Op == "??" || p.Op == "&&"
	case *ast.VarDeclaration:
		return p.TypeAnnot != nil
	case *ast.ArrayDestructuring:
		return p.TypeAnnot != nil
	case *ast.ObjectDestructuring:
		return p.TypeAnnot != nil
	}
	return false
}

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
			// A worker module's body is checked too, outside Body.
			if prog, ok := c.b.Module.Node.(*ast.Program); ok {
				for _, wm := range prog.WorkerModules {
					for _, st := range wm.Body {
						walk(st)
					}
				}
			}
		}
	}
	return c.parents[n]
}

// InProgram reports whether n is a node of the program the checker
// checks: false for code built after parsing (a compiler-synthesized
// helper, a static eval's text), which it has no types for.
func (c *Checker) InProgram(n ast.Node) bool {
	return c.parentOf(n) != nil
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
			// An object literal's method: the literal's contextual type, else
			// the literal's own type (getThisTypeOfObjectLiteral).
			if lit, ok := c.parentOf(x).(*ast.ObjectLiteral); ok {
				if t := c.literalThis(lit, e); t != nil {
					return t
				}
			}
			if c.thisImplicitlyAny(e) {
				return c.anyT // no `this` type from anywhere: any (TS2683)
			}
			// `new function () {…}()` in JavaScript: the constructed
			// instance, which tsc's JS constructor-function inference
			// reads as any here.
			if ne, ok := c.parentOf(x).(*ast.NewExpression); ok && ast.Node(ne.Callee) == x && c.opts.CompatJS() {
				return c.anyT
			}
			// `obj.p = function () {…}`: obj (noImplicitThis), else any.
			if as, ok := c.parentOf(x).(*ast.AssignmentExpression); ok && ast.Node(as.Right) == x && as.Op == "=" {
				var obj ast.Expression
				switch l := as.Left.(type) {
				case *ast.MemberExpression:
					obj = l.Object
				case *ast.IndexExpression:
					obj = l.Object
				}
				if obj != nil {
					if c.opts.CompatJS() {
						return c.anyT
					}
					if ot := c.TypeOf(obj); !c.Unanswered(ot) {
						return c.widenFrom(obj, ot)
					}
				}
			}
			return c.unanswered
		case *ast.ObjectLiteral:
			// A property's value (not a method or accessor, whose function
			// stops the walk above) sees the enclosing `this`.
			continue
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
			if !ok {
				return c.anyT // a plain function's `this`: any (TS2683 where it is checked)
			}
			if x.IsStatic {
				return c.classValueOf(cls) // `typeof C`
			}
			return c.classInstance(cls)
		case *ast.ClassDeclaration:
			if c.staticMember(x, child) {
				return c.classValueOf(x) // a static block or field initializer
			}
			return c.classInstance(x) // a field initializer
		}
	}
	return c.unanswered
}

// thisImplicitlyAny reports tsc's TS2683 (noImplicitThis) for a `this`
// whose function has no type for it: a function declaration outside a
// class, or a function expression with no `this:` parameter that is not an
// object literal's member, not the right side of `obj.p = …`, and has no
// contextual signature with a `this` type (checkThisExpression,
// getContextualThisParameterType). A function expression whose context this
// checker cannot answer is not reported.
func (c *Checker) thisImplicitlyAny(e ast.Node) bool {
	for n := c.parentOf(e); n != nil; n = c.parentOf(n) {
		switch x := n.(type) {
		case *ast.ArrowFunction:
			continue
		case *ast.FunctionExpression:
			if c.thisParamType(x, c.b.ScopeOf(x)) != nil {
				return false
			}
			switch p := c.parentOf(x).(type) {
			case *ast.ObjectLiteral:
				return false
			case *ast.AssignmentExpression:
				if _, ok := p.Left.(*ast.MemberExpression); ok {
					return false
				}
				if _, ok := p.Left.(*ast.IndexExpression); ok {
					return false
				}
			case *ast.VarDeclaration:
				if p.TypeAnnot == nil {
					return true
				}
			case *ast.CallExpression:
				if ast.Node(p.Callee) == x {
					return true // invoked in place: nothing gives it a `this`
				}
			}
			ct := c.contextualType(x)
			if ct == nil || c.Unanswered(ct) {
				return false
			}
			sig := c.signaturesOf(ct, false)
			return sig.Flags&Object != 0 && sig.Kind == Function && sig.ThisType == nil
		case *ast.FunctionDeclaration:
			if prog, ok := c.b.Module.Node.(*ast.Program); ok && prog.ThisParams[x] != nil {
				return false
			}
			_, inClass := c.parentOf(x).(*ast.ClassDeclaration)
			return !inClass
		case *ast.ClassDeclaration, *ast.ClassExpression, *ast.ObjectLiteral:
			return false
		}
	}
	return false
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
	for _, n := range cls.DeclaredTypeParams().Names {
		args = append(args, c.classTypeParam(sym, n))
	}
	if t := c.instanceOf(sym, args); t != nil {
		return t
	}
	return c.unanswered
}

// classValueOf is a class's value, `typeof C`: a static member's `this`.
// ClassValueOf is a class's value, `typeof C`.
func (c *Checker) ClassValueOf(cls *ast.ClassDeclaration) *Type { return c.classValueOf(cls) }

func (c *Checker) classValueOf(cls *ast.ClassDeclaration) *Type {
	if sym := c.symbolOf(cls); sym != nil {
		return c.typeOfSymbol(sym)
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

// hasFunctionArg reports whether an argument is, or holds, a function
// expression.
func hasFunctionArg(args []ast.Expression) bool {
	for _, a := range args {
		switch x := a.(type) {
		case *ast.ArrowFunction, *ast.FunctionExpression:
			return true
		case *ast.ArrayLiteral:
			if hasFunctionArg(x.Elements) {
				return true
			}
		case *ast.ObjectLiteral:
			for _, p := range x.Properties {
				if p.Value != nil && hasFunctionArg([]ast.Expression{p.Value}) {
					return true
				}
			}
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
	case *ast.ArrayDestructuring:
		return p.TypeAnnot != nil
	case *ast.ObjectDestructuring:
		return p.TypeAnnot != nil
	case *ast.MemberExpression:
		return ast.Node(p.Object) != e // a receiver has no contextual type
	case *ast.IndexExpression:
		return ast.Node(p.Object) != e
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
	case *ast.SpreadElement:
		if ol, ok := c.parentOf(p).(*ast.ObjectLiteral); ok && ast.Node(p.Arg) == e {
			return c.rawContext(ol) // an object spread's operand: the literal's own context
		}
	case *ast.AssignmentExpression:
		if p.Op == "=" && ast.Node(p.Right) == e {
			return c.contextualType(e) // the target's declared type
		}
	case *ast.ObjectLiteral:
		for _, prop := range p.Properties {
			if sp, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" && ast.Node(sp.Arg) == e {
				return c.rawContext(p) // an object spread's operand: the literal's own context
			}
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
	case *ast.ArrayDestructuring:
		if ast.Node(p.Init) == e {
			if t := c.patternAnnotation(p.TypeAnnot); t != nil && !c.Unanswered(t) {
				return t
			}
		}
	case *ast.ObjectDestructuring:
		if ast.Node(p.Init) == e {
			if t := c.patternAnnotation(p.TypeAnnot); t != nil && !c.Unanswered(t) {
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

// nonNullableContext is a contextual type without its null and undefined
// members, when what remains is a function type: the signature a callback
// passed for an optional parameter (`cb?: (e, out: string) => void`) is
// typed by.
func (c *Checker) nonNullableContext(t *Type) *Type {
	if t == nil || c.Unanswered(t) || t.Flags&Union == 0 {
		return t
	}
	nn := c.nonNullable(t)
	if nn != nil && nn.Flags&Object != 0 {
		return nn // a function, a callable interface, an object type
	}
	return t
}

// returnTypeContext is an argument's context inferred from the call's own
// context (tsc's return type inference): when the argument's parameter is a
// type parameter T that the other arguments leave uninferred and the call
// returns T (`mustCall<T>(fn: T): T` passed where a listener is expected),
// T is the listener type. Nil otherwise.
func (c *Checker) returnTypeContext(call *ast.CallExpression, sig *Type, j int, m map[*Type]*Type) *Type {
	pt := paramAt(sig, j)
	if pt == nil || pt.Flags&TypeParam == 0 || sig.Result != pt {
		return nil
	}
	if got, ok := m[pt]; ok && got != nil && !c.Unanswered(got) && got != c.unknownT {
		return nil
	}
	if c.contextualizing[call] {
		return nil
	}
	c.contextualizing[call] = true
	ct := c.contextualType(call)
	delete(c.contextualizing, call)
	if ct == nil || c.Unanswered(ct) {
		return nil
	}
	return c.nonNullableContext(ct)
}

// ContextualSignature is the function type a function literal is typed in
// (its contextual signature), or nil.
func (c *Checker) ContextualSignature(fn ast.Expression) *Type {
	ct := c.callSignatureContext(c.contextualType(fn))

	if ct == nil || c.Unanswered(ct) || ct.Flags&Object == 0 || ct.Kind != Function {
		return nil
	}
	if len(ct.Overloads) > 0 {
		return nil
	}
	return ct
}

// countCands is how many inference candidates cands holds.
func countCands(cands map[*Type][]*Type) int {
	n := 0
	for _, v := range cands {
		n += len(v)
	}
	return n
}

// asyncReturnValue reports whether e is the value an async function
// returns: a `return` statement's, or an async arrow's expression body.
func (c *Checker) asyncReturnValue(e ast.Expression) bool {
	switch p := c.parentOf(e).(type) {
	case *ast.ReturnStatement:
		for n := c.parentOf(p); n != nil; n = c.parentOf(n) {
			switch f := n.(type) {
			case *ast.FunctionDeclaration:
				return f.IsAsync
			case *ast.FunctionExpression:
				return f.IsAsync
			case *ast.ArrowFunction:
				return f.IsAsync
			}
		}
	case *ast.ArrowFunction:
		return p.IsAsync && ast.Node(p.Body) == ast.Node(e)
	}
	return false
}

// copyMap is a copy of an instantiation map, to extend without changing m.
func copyMap(m map[*Type]*Type) map[*Type]*Type {
	out := make(map[*Type]*Type, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// anyContextSensitive reports a context-sensitive argument among args.
func anyContextSensitive(args []ast.Expression) bool {
	for _, a := range args {
		if contextSensitive(a) {
			return true
		}
	}
	return false
}

// wholeArgsFor reports that every non-callback argument of e whose
// parameter in sig mentions tp has a whole type (an object literal's, a
// primitive), so its giving tp no candidate is decided, not a member this
// checker does not model (a library type's, a computed key's).
func (c *Checker) wholeArgsFor(e *ast.CallExpression, sig, tp *Type) bool {
	for i, a := range e.Args {
		if contextSensitive(a) {
			continue
		}
		if p := paramAt(sig, i); p != nil && occurs(tp, p) {
			if at := c.TypeOf(a); at.partial || at.Flags&Object != 0 && at.Kind != Anonymous {
				return false
			}
		}
	}
	return true
}

// ParentOf is n's parent node in the checked program, or nil.
func (c *Checker) ParentOf(n ast.Node) ast.Node { return c.parentOf(n) }

// callSignatureContext is a contextual type's signature: a function type
// itself, or the single call signature of an object type declaring one (an
// interface such as `TransformerTransformCallback<I, O>`), as tsc's
// getContextualSignature takes it.
func (c *Checker) callSignatureContext(ct *Type) *Type {
	if ct != nil && ct.Flags&Union != 0 {
		return c.unionSignatureContext(ct)
	}
	if ct == nil || ct.Flags&Object == 0 || ct.Kind == Function || len(ct.Calls) != 1 || len(ct.Props) > 0 {
		return ct
	}
	return ct.Calls[0]
}

// CallTypeArgs is the type arguments a call of a generic, non-overloaded
// function takes, by type parameter name: the explicit ones, else the ones
// its arguments infer, else the call's contextual type through the result
// (tsc's inference from the return type). nil when the callee is not such
// a function.
func (c *Checker) CallTypeArgs(e *ast.CallExpression) map[string]*Type {
	callee := c.signaturesOf(c.TypeOf(e.Callee), false)
	if c.Unanswered(callee) || callee.Flags&Object == 0 || callee.Kind != Function || len(callee.Overloads) > 0 || len(callee.TypeParams) == 0 {
		return nil
	}
	m := c.inferArgsRecv(callRecv(e), e.Args, e.TypeArgs, callee, true)
	if len(e.TypeArgs) == 0 {
		c.inferFromReturnContext(e, callee, m)
	}
	out := map[string]*Type{}
	for _, tp := range callee.TypeParams {
		name := tp.Value
		if tp.Symbol != nil {
			name = tp.Symbol.Name
		}
		if t := m[tp]; t != nil && t != c.unknownT && !c.Unanswered(t) {
			out[name] = t
		}
	}
	return out
}

// literalOfKind reports whether a union parameter's fixed members include a
// literal of the same primitive kind as one of the source's literals.
func literalOfKind(fixed map[*Type]bool, rest []*Type) bool {
	for _, r := range rest {
		if r.Flags&Literal == 0 {
			continue
		}
		base := literalBase(r.Flags)
		for f := range fixed {
			if f.Flags&Literal != 0 && literalBase(f.Flags)&base != 0 {
				return true
			}
		}
	}
	return false
}

// literalThis is `this` in a method of object literal lit: its contextual
// object type, else its own type. While the literal is typed (its methods'
// results may read `this`), it is a snapshot of the literal's other
// properties, and the `this` reading it gets the finished type afterwards.
func (c *Checker) literalThis(lit *ast.ObjectLiteral, this ast.Node) *Type {
	if ct := c.contextualType(lit); ct != nil {
		if ct.LiteralThis != nil {
			return c.settledThis(ct.LiteralThis)
		}
		// An enclosing literal's `ThisType<T>`, through property values
		// (tsc's getContextualThisParameterType walk).
		for outer := lit; ; {
			ol, ok := c.parentOf(outer).(*ast.ObjectLiteral)
			if !ok || !literalPropertyValue(ol, outer) {
				break
			}
			oct := c.contextualType(ol)
			if oct == nil {
				break
			}
			if oct.LiteralThis != nil {
				return c.settledThis(oct.LiteralThis)
			}
			outer = ol
		}
		if !c.Unanswered(ct) && ct.Flags&Object != 0 && ct.Flags&Union == 0 && ct.Kind != Function {
			return ct
		}
		return nil
	}
	if snap, busy := c.literalSnaps[lit]; busy {
		// A method returning `this` itself returns the literal's own type,
		// circular to tsc: the polymorphic `this` a read through the
		// literal instantiates with it (withThis).
		if x, ok := this.(ast.Expression); ok && returnedWhole(c.parentOf(x), x) {
			return c.thisT
		}
		if snap == nil {
			return nil
		}
		if x, ok := this.(ast.Expression); ok {
			c.literalThisFix[lit] = append(c.literalThisFix[lit], x)
		}
		return snap
	}
	t := c.TypeOf(lit)
	if c.Unanswered(t) || t.Flags&Object == 0 || t.Kind != Anonymous {
		return nil
	}
	return t
}

// literalSnapshot is object literal lit's properties other than its
// methods and function values, as a partial object; nil when one of them
// is unanswered or the literal has a spread, accessor or computed key.
func (c *Checker) literalSnapshot(lit *ast.ObjectLiteral) *Type {
	var props []*Property
	for _, p := range lit.Properties {
		if p.AccessorKind != "" || p.KeyExpr != nil || p.Key == "" {
			return nil
		}
		switch p.Value.(type) {
		case *ast.SpreadElement:
			return nil
		case *ast.FunctionExpression, *ast.ArrowFunction:
			continue
		}
		t := c.TypeOf(p.Value)
		if c.Unanswered(t) {
			return nil
		}
		if !c.constAsserted(p.Value) && !c.keepsLiteral(p.Value, t) {
			t = c.widenFrom(p.Value, t)
		}
		props = append(props, &Property{Name: p.Key, Type: t})
	}
	snap := c.in.objectOf(props, true, false)
	c.snapshotOf[snap] = lit
	return snap
}

// literalNames reports whether object literal lit names property name.
func literalNames(lit *ast.ObjectLiteral, name string) bool {
	for _, p := range lit.Properties {
		if p.Key == name || p.KeyExpr != nil {
			return true
		}
	}
	return false
}

// returnedWhole reports x as the whole value a function returns: a return
// statement's argument or an arrow's expression body.
func returnedWhole(parent ast.Node, x ast.Expression) bool {
	switch p := parent.(type) {
	case *ast.ReturnStatement:
		return p.Value == x
	case *ast.ArrowFunction:
		return p.Body == x
	}
	return false
}

// iifeArgContextFree reports whether fn is an argument of an immediately
// invoked function whose parameter at fn's position is unannotated: that
// parameter is typed from fn (iifeParam), so fn is typed with no context
// (tsc resolves the circularity that way: fn's own unannotated parameters
// are any, TS7006).
func (c *Checker) iifeArgContextFree(fn ast.Node) bool {
	call, ok := c.parentOf(fn).(*ast.CallExpression)
	if !ok {
		return false
	}
	var params []ast.Param
	switch callee := call.Callee.(type) {
	case *ast.FunctionExpression:
		params = callee.Params
	case *ast.ArrowFunction:
		params = callee.Params
	default:
		return false
	}
	for j, a := range call.Args {
		if ast.Node(a) != fn {
			continue
		}
		if j >= len(params) {
			return false
		}
		p := params[j]
		return p.Type == nil && p.Default == nil && !p.Rest && p.ArrayPattern == nil && p.ObjectPattern == nil
	}
	return false
}

// firstFittingOverloadParam is argument j's context in a call of an
// overloaded callee whose pick could not be made: parameter j of the first
// non-generic overload whose arity fits and whose other arguments, typed
// without context, are assignable. nil when none does.
func (c *Checker) firstFittingOverloadParam(call *ast.CallExpression, callee *Type, j int) *Type {
	for _, sig := range callee.Overloads {
		if len(sig.TypeParams) > 0 || !arityFits(sig, len(call.Args)) {
			continue
		}
		fits := true
		for i, a := range call.Args {
			if i == j {
				continue
			}
			if _, spread := a.(*ast.SpreadElement); spread || contextSensitive(a) {
				fits = false
				break
			}
			pt, at := paramAt(sig, i), c.TypeOf(a)
			if pt == nil || c.Unanswered(at) || !c.assignable(at, pt) {
				fits = false
				break
			}
		}
		if fits {
			if pt := paramAt(sig, j); pt != nil {
				return c.nonNullableContext(pt)
			}
		}
	}
	return nil
}

// unionSignatureContext is tsc's getContextualSignature over a union: the
// members' signatures, when they are identical but for their results, as
// one signature returning the union of the results. A nullish member gives
// none; any other mismatch, the union itself (no signature).
func (c *Checker) unionSignatureContext(ct *Type) *Type {
	var first *Type
	var results []*Type
	for _, m := range ct.Types {
		if m.Flags&Nullish != 0 {
			continue
		}
		s := c.callSignatureContext(m)
		if s == nil || s.Flags&Object == 0 || s.Kind != Function || len(s.TypeParams) > 0 || s.Result == nil {
			return ct
		}
		if first == nil {
			first = s
		} else if !sameParams(first, s) {
			return ct
		}
		results = append(results, s.Result)
	}
	if first == nil || len(results) < 2 {
		if first != nil {
			return first
		}
		return ct
	}
	return c.in.function(first.Params, first.optionals, first.restParam, c.in.union(results...), nil, nil)
}

// sameParams reports two signatures with identical parameter lists.
func sameParams(a, b *Type) bool {
	if len(a.Params) != len(b.Params) || a.restParam != b.restParam || len(a.optionals) != len(b.optionals) || a.ThisType != b.ThisType {
		return false
	}
	for i := range a.Params {
		if a.Params[i] != b.Params[i] {
			return false
		}
	}
	for i := range a.optionals {
		if a.optionals[i] != b.optionals[i] {
			return false
		}
	}
	return true
}

// literalPropertyValue reports that x is the value of a named property of
// literal ol (`{ k: x }`).
func literalPropertyValue(ol *ast.ObjectLiteral, x ast.Expression) bool {
	for _, p := range ol.Properties {
		if ast.Node(p.Value) == ast.Node(x) && p.Key != "" && p.AccessorKind == "" {
			return true
		}
	}
	return false
}

// settledThis is a `ThisType<T>` marker's T for a method's `this`, or nil
// (not modelled) while T still holds a type parameter the call infers.
func (c *Checker) settledThis(t *Type) *Type {
	if c.generic(t) {
		return nil
	}
	return t
}

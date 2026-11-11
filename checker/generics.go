package checker

import (
	"reflect"
	"strconv"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/diag"
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
	if fresh {
		// The type parameter itself has the bounds its declaration's scope
		// gives; one naming another type parameter (`<U extends T>` of a
		// class's method) may read differently under an instantiation's
		// bindings.
		saved := c.env
		c.env = nil
		ct, dt := c.typeParamBounds(sym)
		c.env = saved
		c.boundCopy(t, ct, dt)
		if ct != nil && hasTypeParam(ct) || dt != nil && hasTypeParam(dt) {
			if outer := c.outerTypeParamNames(sym); len(outer) > 0 {
				if c.envBounds == nil {
					c.envBounds = map[*Type]map[string]bool{}
				}
				c.envBounds[t] = outer
			}
		}
	}
	outer := c.envBounds[t]
	if len(c.env) == 0 || outer == nil {
		return t
	}
	// Under an instantiation's bindings of an enclosing declaration's type
	// parameters (`C<number>`'s methods): a copy bounded by them
	// (boundCopy), `U extends number`.
	var env []map[string]*Type
	for _, f := range c.env {
		var g map[string]*Type
		for n, b := range f {
			if outer[n] {
				if g == nil {
					g = map[string]*Type{}
				}
				g[n] = b
			}
		}
		if g != nil {
			env = append(env, g)
		}
	}
	if env == nil {
		return t
	}
	saved := c.env
	c.env = env
	ct, dt := c.typeParamBounds(sym)
	c.env = saved
	return c.boundCopy(t, ct, dt)
}

// outerTypeParamNames is the type parameter names the declarations
// enclosing type parameter sym's declare, less its own declaration's.
func (c *Checker) outerTypeParamNames(sym *binder.Symbol) map[string]bool {
	var out map[string]bool
	for _, d := range sym.Declarations {
		own := map[string]bool{}
		for _, n := range declTypeParams(d.Node) {
			own[n] = true
		}
		for p := c.parentOf(d.Node); p != nil; p = c.parentOf(p) {
			for _, n := range declTypeParams(p) {
				if !own[n] {
					if out == nil {
						out = map[string]bool{}
					}
					out[n] = true
				}
			}
		}
	}
	return out
}

// declTypeParams is the type parameter names declaration n declares.
func declTypeParams(n ast.Node) []string {
	switch n := n.(type) {
	case *ast.FunctionDeclaration:
		return n.TypeParams
	case *ast.ArrowFunction:
		return n.TypeParams
	case *ast.FunctionExpression:
		return n.TypeParams
	case *ast.ClassDeclaration:
		return n.TypeParams
	}
	return nil
}

// typeParamBounds is the constraint and default (nil when absent) the
// declarations of type parameter sym give it, read under the current
// bindings.
func (c *Checker) typeParamBounds(sym *binder.Symbol) (ct, dt *Type) {
	for _, d := range sym.Declarations {
		var names []string
		var constraints, defaults []ast.TypeNode
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
		read := func(as []ast.TypeNode, i int) *Type {
			if i >= len(as) || as[i] == nil {
				return nil
			}
			if tn := as[i]; tn != nil {
				if t := c.typeFromNode(tn, sym.Scope); !c.Unanswered(t) {
					return t
				}
			}
			return nil
		}
		for i, name := range names {
			if name != sym.Name {
				continue
			}
			if t := read(constraints, i); t != nil {
				ct = t
			}
			if t := read(defaults, i); t != nil {
				dt = t
			}
		}
	}
	return ct, dt
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
	if c.inst == nil {
		// One substitution's function types, each instantiated once.
		c.inst = &instState{busy: map[instKey]bool{}, done: map[instKey]*Type{}}
		defer func() { c.inst = nil }()
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
		// A union met inside its own instantiation (a recursive alias's,
		// `T | Box<R<T>>`): a stand-in, filled once the result is built.
		st := c.inst
		key := instKey{t, reflect.ValueOf(m).Pointer()}
		if r := st.done[key]; r != nil {
			return r
		}
		if st.busy[key] {
			if ph := st.pending[key]; ph != nil {
				return ph
			}
			c.in.next++
			ph := &Type{Flags: Union, ID: c.in.next, selfRef: true}
			if st.pending == nil {
				st.pending = map[instKey]*Type{}
			}
			st.pending[key] = ph
			return ph
		}
		st.busy[key] = true
		ms := make([]*Type, len(t.Types))
		for i, x := range t.Types {
			ms[i] = c.instantiate(x, m)
		}
		r := c.in.union(ms...)
		delete(st.busy, key)
		if ph := st.pending[key]; ph != nil {
			delete(st.pending, key)
			id := ph.ID
			*ph = *r
			ph.ID, ph.selfRef = id, true
			r = ph
		}
		st.done[key] = r
		return r
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
		st := c.inst
		key := instKey{t, reflect.ValueOf(m).Pointer()}
		if r := st.done[key]; r != nil {
			return r
		}
		if st.busy[key] {
			if own := withoutOwn(t, m); own != nil {
				// t met inside its own result is a reference to it, not a
				// call: its own type parameters stay its own.
				return c.instantiate(t, own)
			}
			// A function type met again inside its own instantiation (`let
			// x = { foo: () => x }`): its result is the finished
			// instantiation's, read when asked, as tsc defers it.
			shape := c.instantiateSignature(t, m, c.anyT)
			return c.in.deferred(shape, func() (*Type, bool) {
				if r := st.done[key]; r != nil {
					return r.Ret(), true
				}
				return c.anyT, true
			}, func() *Type { return c.anyT })
		}
		st.busy[key] = true
		fn := c.instantiateSignature(t, m, nil)
		delete(st.busy, key)
		st.done[key] = fn
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
			// A recursive alias's instance: the alias over the substituted
			// arguments (a non-generic one names no type parameter).
			if len(t.TypeArgs) == 0 {
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
			if r := c.aliasOf(t.Symbol, args); r != nil && !c.Unanswered(r) {
				return r
			}
			return t
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
		// An instantiated rest type parameter (`...args: [X, Y]`): the
		// tuple's element at that position.
		if rp.Flags&Object != 0 && rp.Kind == Tuple && i-(n-1) < len(rp.Elems) {
			return rp.Elems[i-(n-1)]
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
	// An arrow whose expression body is itself context-sensitive (`() =>
	// ({ b: x => {} })`): tsc's hasContextSensitiveReturnExpression.
	if af, ok := e.(*ast.ArrowFunction); ok && af.Body != nil && af.RetType == nil {
		return contextSensitive(af.Body)
	}
	return false
}

// inferFromInsensitive unifies the parts of a context-sensitive argument a
// that are not themselves context-sensitive against the matching parts of
// parameter type p: an object literal's properties, an arrow's expression
// body against p's result.
func (c *Checker) inferFromInsensitive(p *Type, a ast.Expression, fn *Type, cands, contra map[*Type][]*Type, depth int) {
	if depth > 8 || p == nil || c.Unanswered(p) {
		return
	}
	switch x := a.(type) {
	case *ast.ArrayLiteral:
		// A tuple for a homomorphic mapped parameter: its elements without
		// their context-sensitive functions infer (as an object literal's
		// properties below).
		if p.Flags&Deferred != 0 && c.homomorphicMapped(p) {
			es := make([]*Type, 0, len(x.Elements))
			for _, el := range x.Elements {
				var t *Type
				switch {
				case el == nil:
					return
				case !contextSensitive(el):
					t = c.TypeOf(el)
				default:
					lit, ok := el.(*ast.ObjectLiteral)
					if !ok {
						return
					}
					t = c.insensitiveLiteralType(lit, 0)
				}
				if t == nil || c.Unanswered(t) {
					return
				}
				es = append(es, t)
			}
			c.unify(p, c.in.tuple(es), fn, cands)
			return
		}
		if p.Flags&Object != 0 && p.Flags&Union == 0 && p.Kind == Array && p.Head == nil {
			// An array parameter (`T[]`): each element that is not
			// context-sensitive infers the element type, the rest by
			// their own insensitive parts (tsc's first pass).
			for _, el := range x.Elements {
				switch {
				case el == nil:
				case !contextSensitive(el):
					if _, spread := el.(*ast.SpreadElement); spread {
						continue
					}
					if t := c.TypeOf(el); !c.Unanswered(t) {
						c.unify(p.Elem, t, fn, cands)
					}
				default:
					c.inferFromInsensitive(p.Elem, el, fn, cands, contra, depth+1)
				}
			}
		}
	case *ast.ObjectLiteral:
		if p.Flags&Deferred != 0 {
			// A mapped type over a type parameter's keys: what the literal
			// is without its context-sensitive functions infers (tsc's
			// first pass, which leaves those non-inferrable).
			if t := c.insensitiveLiteralType(x, 0); t != nil {
				c.unify(p, t, fn, cands)
			}
			return
		}
		if p.Flags&Object == 0 || p.Flags&Union != 0 {
			return
		}
		for _, prop := range x.Properties {
			if prop.KeyExpr != nil || prop.Key == "" || prop.AccessorKind != "" || prop.Value == nil {
				continue
			}
			pp := p.Prop(prop.Key)
			if pp == nil || !c.mayInferIn(pp.Type, fn, cands) {
				continue
			}
			if contextSensitive(prop.Value) {
				c.inferFromInsensitive(pp.Type, prop.Value, fn, cands, contra, depth+1)
				continue
			}
			if at := c.TypeOf(prop.Value); !c.Unanswered(at) {
				if pp.Type.Flags&Object != 0 && pp.Type.Kind == Function && at.Flags&Object != 0 && at.Kind == Function {
					// Its parameters infer contravariantly; its result and
					// predicate covariantly.
					c.unify(pp.Type, at, fn, contra)
					if pr, ar := pp.Type.Ret(), at.Ret(); pr != nil && ar != nil {
						c.unify(pr, ar, fn, cands)
					}
					if pp.Type.Predicate != nil && at.Predicate != nil && pp.Type.Predicate.Type != nil && at.Predicate.Type != nil {
						c.unify(pp.Type.Predicate.Type, at.Predicate.Type, fn, cands)
					}
					continue
				}
				c.unify(pp.Type, at, fn, cands)
			}
		}
	case *ast.ArrowFunction:
		if x.Body == nil || len(x.Params) > 0 || x.RetType != nil {
			return
		}
		if p.Flags&Object != 0 && p.Kind == Function && p.Ret() != nil {
			c.inferFromInsensitive(p.Ret(), x.Body, fn, cands, contra, depth+1)
		}
	}
}

// inferArgs infers a generic call's type arguments (TypeScript's
// inference): explicit ones as written; otherwise from each argument's
// type against its parameter's, the context-insensitive arguments first,
// then the callbacks, typed from what the first pass inferred. withSensitive
// false stops after the first pass (a callback asking for its own context).
func (c *Checker) inferArgs(callArgs []ast.Expression, typeArgs []ast.TypeNode, fn *Type, withSensitive bool) map[*Type]*Type {
	m, _ := c.inferArgsDetail(callArgs, typeArgs, fn, withSensitive)
	return m
}

// inferArgsRecv is inferArgs for a call through recv (`recv.m(…)`, else
// nil): a generic `this: T` parameter infers from the receiver's type too
// (`flat<A>(this: A)`).
func (c *Checker) inferArgsRecv(recv ast.Expression, callArgs []ast.Expression, typeArgs []ast.TypeNode, fn *Type, withSensitive bool) map[*Type]*Type {
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

func (c *Checker) inferArgsDetail(callArgs []ast.Expression, typeArgs []ast.TypeNode, fn *Type, withSensitive bool) (map[*Type]*Type, map[*Type]bool) {
	return c.inferArgsDetailRecv(nil, callArgs, typeArgs, fn, withSensitive)
}

// inferArgsDetail is inferArgs, also reporting the type parameters that no
// argument gave a candidate (fixed to their default or unknown).
func (c *Checker) inferArgsDetailRecv(recv ast.Expression, callArgs []ast.Expression, typeArgs []ast.TypeNode, fn *Type, withSensitive bool) (map[*Type]*Type, map[*Type]bool) {
	uninferred := map[*Type]bool{}
	m := map[*Type]*Type{}
	if len(typeArgs) > 0 {
		for i, tp := range fn.TypeParams {
			if i < len(typeArgs) && typeArgs[i] != nil {
				scope := c.b.TypeArgScope(typeArgs[i])
				if scope == nil {
					scope = c.b.Module
				}
				if t := c.typeFromNode(typeArgs[i], scope); !c.Unanswered(t) {
					m[tp] = t
					continue
				}
			} else if i >= len(typeArgs) && tp.Default != nil {
				// An omitted trailing type argument: its default, with the
				// ones before it (`get<number>()` for `<T, R = Res<T>>`).
				m[tp] = c.instantiate(tp.Default, m)
				continue
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
	var call ast.Node
	if len(callArgs) > 0 {
		switch pc := c.parentOf(callArgs[0]).(type) {
		case *ast.CallExpression, *ast.NewExpression:
			call = pc
		}
	}
	if call != nil {
		if c.inferSoFar == nil {
			c.inferSoFar = map[ast.Node]map[*Type]*Type{}
		}
		saved, had := c.inferSoFar[call]
		defer func() {
			if had {
				c.inferSoFar[call] = saved
			} else {
				delete(c.inferSoFar, call)
			}
		}()
	}
	// The literal types the arguments give fresh and regular: inference
	// widens only fresh ones (tsc's getWidenedLiteralType), so a literal
	// from a declared type stays (`f((q: 0 | 1) => q)` infers 0 | 1).
	freshLits, regularLits := map[*Type]bool{}, map[*Type]bool{}
	infer := func(i int, a ast.Expression) {
		p := paramAt(fn, i)
		if p == nil {
			return
		}
		if call != nil {
			so := map[*Type]*Type{}
			for tp, cs := range cands {
				if len(cs) > 0 && !poisoned[tp] {
					so[tp] = widen(c, c.in.union(cs...))
				}
			}
			c.inferSoFar[call] = so
		}
		at := c.argTypeFor(a, p)
		// A generic function argument (`map(xs, first)`) infers through
		// its own instantiation (tsc's instantiateSignatureInContextOf),
		// which is not modelled: what it could infer is unanswered too.
		if c.Unanswered(at) || at.Flags&Object != 0 && at.Kind == Function && len(at.TypeParams) > 0 && c.keepSigParams == 0 {
			for _, tp := range fn.TypeParams {
				if occurs(tp, p) {
					poisoned[tp] = true
				}
			}
			return
		}
		c.literalSources(a, at, freshLits, regularLits)
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
	if rp := variadicRest(fn); rp != nil && len(callArgs) >= restAt {
		// A variadic tuple rest (`...args: [...A, L]`): the arguments it
		// collects, as a tuple, against it.
		var elems []*Type
		ok := true
		for _, a := range callArgs[restAt:] {
			if _, spread := a.(*ast.SpreadElement); spread {
				ok = false
				break
			}
			at := c.TypeOf(a)
			if c.Unanswered(at) {
				ok = false
				break
			}
			elems = append(elems, widen(c, at)) // a rest argument widens (getSpreadArgumentType)
		}
		if ok {
			c.unify(rp, c.in.tuple(elems), fn, cands)
		}
	}
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
	before := -1 // the argument whose context this pass infers for
	for k, a := range callArgs {
		if a == c.sensBefore && c.sensBefore != nil {
			before = k
		}
	}
	for i, a := range callArgs {
		if restTP != nil && i >= restAt {
			break
		}
		if _, typed := c.nodeTypes[a]; !withSensitive && i < before && typed && contextSensitive(a) {
			infer(i, a) // an earlier callback, already typed
		} else if !contextSensitive(a) {
			infer(i, a)
		} else if p := paramAt(fn, i); p != nil {
			// Intra-expression inference (tsc 4.7): a context-sensitive
			// literal's other properties infer first, so a callback beside
			// them is typed from them (`{ a: { n: 1 }, b: x => … }`). A
			// function-valued one infers contravariantly: only what no
			// other property infers (tsc prefers covariant candidates).
			contra := map[*Type][]*Type{}
			c.inferFromInsensitive(p, a, fn, cands, contra, 0)
			for tp, cs := range contra {
				if len(cands[tp]) == 0 {
					cands[tp] = cs
				}
			}
		}
	}
	fixedTP := map[*Type]bool{}
	if withSensitive {
		for i, a := range callArgs {
			if restTP != nil && i >= restAt {
				break
			}
			if !contextSensitive(a) {
				continue
			}
			// A type parameter an unannotated callback parameter takes
			// its type from is fixed before the callback is typed (tsc's
			// getTypeOfParameter → fixing): its result adds no candidate
			// (`reduce((acc, x) => { if (x) return acc + x }, 0)` keeps
			// U = number, so the possibly undefined result misfits).
			kept := map[*Type][]*Type{}
			lit := map[*Type]bool{}
			for _, tp := range c.fixedByCallback(fn, i, a, cands) {
				kept[tp], lit[tp] = cands[tp], c.literalKept[tp]
				fixedTP[tp] = true
			}
			infer(i, a)
			for tp, cs := range kept {
				cands[tp] = cs
				if !lit[tp] {
					delete(c.literalKept, tp)
				}
			}
		}
	}
	// What no argument infers, the call's contextual type may: its return
	// type against it (tsc's inference with ReturnType priority, which
	// also types the context-sensitive arguments).
	if rc := c.returnContextCands(callArgs, fn, cands, poisoned); rc != nil {
		for tp, cs := range rc {
			if len(cands[tp]) == 0 && !poisoned[tp] {
				cands[tp] = cs
			}
		}
	}
	for _, tp := range fn.TypeParams {
		if poisoned[tp] {
			m[tp] = c.unanswered
			continue
		}
		cs := cands[tp]
		if len(cs) > 1 && c.mappedCand != nil {
			// A candidate inferred through a homomorphic mapped type
			// (`Partial<T>`) yields to a direct one (tsc's
			// InferencePriority.HomomorphicMappedType).
			var direct []*Type
			for _, x := range cs {
				if !c.mappedCand[x] {
					direct = append(direct, x)
				}
			}
			if len(direct) > 0 {
				cs = direct
			}
		}
		if len(cs) == 0 {
			// Inference is complete when every argument was seen: the
			// second pass, or a call with no context-sensitive argument.
			if withSensitive || !anyContextSensitive(callArgs) {
				// Nothing to infer from: the default, else unknown (tsc's
				// getInferredType).
				uninferred[tp] = true
				m[tp] = c.unknownT
				if tp.Default != nil {
					// A default may name an earlier type parameter (`R =
					// Res<T>`): with what that one was given.
					m[tp] = c.instantiate(tp.Default, m)
				}
				// One its constraint does not admit is the constraint
				// (tsc's getInferredType: `C extends keyof S` is S's keys).
				if tp.Constraint != nil && !c.Unanswered(tp.Constraint) {
					if ct := c.instantiate(tp.Constraint, m); !c.Unanswered(ct) && !c.generic(ct) && c.assignableTo(m[tp], ct) == no {
						m[tp] = ct
					}
				}
			}
			continue
		}
		t := c.in.union(cs...)
		// A fixed type parameter widens its literals (tsc's
		// getCovariantInference: inference.isFixed).
		keep := !fixedTP[tp] && (c.keepsLiterals(tp, fn) || c.literalKept[tp]) || c.keepLiterals
		if !keep {
			t = widenExcept(c, t, func(m *Type) bool { return regularLits[m] && !freshLits[m] })
		}
		if first, ok := c.firstPrimitiveCandidate(cs, keep); ok {
			t = first
		} else if sup, ok := c.objectSupertypeCandidate(cs); ok {
			t = sup
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

// objectSupertypeCandidate is tsc's common supertype of candidates that are
// each one type, an object among them (getSupertypeOrUnion): left to right, a candidate
// the current one is assignable to replaces it, so the supertype of all
// wins if there is one, else the leftmost (a relation left maybe counts
// as assignable, as the assignment check counts it); the arguments of the other
// types then fail their check (`then(tfD, tf)` for `then<U>(ok: (v: T) =>
// Deferred<U>, err: (e: any) => U)` is not `U = number | Thenable<number>`).
func (c *Checker) objectSupertypeCandidate(cs []*Type) (*Type, bool) {
	if len(cs) < 2 {
		return nil, false
	}
	distinct, object := false, false
	ws := make([]*Type, len(cs))
	for i, cand := range cs {
		if cand.Flags&Union != 0 || cand.Flags&(Any|Unknown|Null|Undefined|Never|Void) != 0 || c.Unanswered(cand) {
			return nil, false
		}
		ws[i] = widen(c, cand)
		object = object || cand.Flags&Object != 0
		if ws[i] != ws[0] {
			distinct = true
		}
	}
	if !distinct || !object {
		return nil, false
	}
	sup := ws[0]
	for _, cand := range ws[1:] {
		if c.assignableTo(sup, cand) != no {
			sup = cand
		}
	}
	return sup, true
}

// keepsLiterals reports whether a type parameter's inference keeps literal
// types: when it has a primitive constraint, or occurs at the top level of
// the return type (`id(1)` is 1, `wrap(1)` is number[]).
func (c *Checker) keepsLiterals(tp, fn *Type) bool {
	if c.primitiveConstraint(tp) {
		return true
	}
	for _, m := range members(fn.Ret()) {
		if m == tp || m.Flags&Deferred != 0 && deferredAtTopLevel(tp, m.deferNode, m.deferEnv) {
			return true
		}
	}
	return false
}

// deferredAtTopLevel reports type parameter tp at the top level of the
// deferred type node n (env naming its type parameters): n itself, a member
// of a union or intersection, or a conditional's branch (tsc's
// isTypeParameterAtTopLevel; `NonNullable<T>`'s `T & {}`).
func deferredAtTopLevel(tp *Type, n ast.TypeNode, env map[string]*Type) bool {
	switch x := n.(type) {
	case *ast.TypeReference:
		return len(x.Qualifier) == 0 && len(x.TypeArgs) == 0 && env[x.Name] == tp
	case *ast.ParenthesizedType:
		return deferredAtTopLevel(tp, x.Type, env)
	case *ast.UnionType:
		for _, m := range x.Types {
			if deferredAtTopLevel(tp, m, env) {
				return true
			}
		}
	case *ast.IntersectionType:
		for _, m := range x.Types {
			if deferredAtTopLevel(tp, m, env) {
				return true
			}
		}
	case *ast.ConditionalType:
		return deferredAtTopLevel(tp, x.TrueType, env) || deferredAtTopLevel(tp, x.FalseType, env)
	}
	return false
}

// unify collects inference candidates for fn's type parameters from an
// argument of type a passed where p is expected.
// mayInferIn reports whether t mentions a type parameter unify infers:
// one of fn's, or one cands already tracks.
func (c *Checker) mayInferIn(t, fn *Type, cands map[*Type][]*Type) bool {
	if t.Flags&(Any|Unknown) != 0 && t.Flags&TypeParam == 0 {
		return false
	}
	if fn != nil {
		for _, tp := range fn.TypeParams {
			if occurs(tp, t) {
				return true
			}
		}
	}
	for tp := range cands {
		if occurs(tp, t) {
			return true
		}
	}
	return fn == nil && len(cands) == 0
}

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
	case p.Flags&Deferred != 0 && c.unifyVariadic(p, a, fn, cands):
	case p.Flags&Deferred != 0 && c.unifyDepth < 4 && c.unifyMapped(p, a, fn, cands):
	case p.Flags&Deferred != 0 && c.unifyDepth < 4 && c.deferredIntersectionParts(p) != nil:
		// An intersection over a type parameter: inferred to each member
		// (tsc's inferToMultipleTypes).
		c.unifyDepth++
		for _, m := range c.deferredIntersectionParts(p) {
			c.unify(m, a, fn, cands)
		}
		c.unifyDepth--
		return
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
					// Two bare type parameters: no single target, but a
					// call's return context (`Promise<string>` for `then`'s
					// `Promise<TResult1 | TResult2>`) is what each gives.
					if c.returnInfer > 0 {
						for _, n := range generic {
							if n.Flags&TypeParam != 0 {
								c.unify(n, a, fn, cands)
							}
						}
					}
					return
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
	case a.Flags&Union != 0 && p.Flags&Object != 0 && p.Flags&Union == 0 && p.Kind == Anonymous && len(p.Calls) == 0 && allMembers(a, Object) && c.unifyDepth < 4:
		// A union of object types to one object type: each member infers
		// (tsc's inferFromTypes over a union source), `{ value: T }` from
		// `{ value: number } | { value: string }` T number and string.
		c.unifyDepth++
		for _, m := range a.Types {
			c.unify(p, m, fn, cands)
		}
		c.unifyDepth--
		return
	case p.Flags&Object == 0 || a.Flags&Object == 0:
		return
	}
	if parts := c.intersectionOf[p]; parts != nil {
		for _, m := range parts {
			c.unify(m, a, fn, cands)
		}
		return
	}
	if p.Kind != Function && len(p.Calls) > 0 && len(p.Props) == 0 && p.StringIndex == nil && p.NumberIndex == nil {
		// A type literal of call signatures (`{ (e: 0): U; (e: 1): U }`):
		// its signatures pair with the source's from the end (tsc's
		// inferFromSignatures).
		as := a.Calls
		if a.Kind == Function {
			as = a.Overloads
			if len(as) == 0 {
				as = []*Type{a}
			}
		}
		n := min(len(p.Calls), len(as))
		for i := 0; i < n; i++ {
			c.unify(p.Calls[len(p.Calls)-n+i], as[len(as)-n+i], fn, cands)
		}
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
		if a.Kind == Function && len(a.TypeParams) > 0 && len(p.TypeParams) == 0 && c.keepSigParams == 0 {
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
	case p.Kind == Array && a.Kind == Interface && a.Symbol != nil && len(a.TypeArgs) == 1 && (a.Symbol.Name == "ReadonlyArray" || a.Symbol.Name == "Array") && c.inLibrary(a.Symbol.Scope):
		// An array against `ReadonlyArray<X>` (a return context): its
		// element is X, as tsc infers through the array's base.
		c.unify(p.Elem, a.TypeArgs[0], fn, cands)
	case p.Kind == Array && a.Kind == Tuple:
		for _, e := range a.Elems {
			c.unify(p.Elem, e, fn, cands)
		}
	case p.Kind == Tuple && a.Kind == Tuple && len(p.Elems) == len(a.Elems):
		for i := range p.Elems {
			c.unify(p.Elems[i], a.Elems[i], fn, cands)
		}
	case p.Kind == Function && a.Kind == Function && (len(p.Overloads) > 0 || len(a.Overloads) > 0):
		// Overloads pair up from the last signature of each, as tsc's
		// inferFromSignatures: the last is the most general.
		ps, as := p.Overloads, a.Overloads
		if len(ps) == 0 {
			ps = []*Type{p}
		}
		if len(as) == 0 {
			as = []*Type{a}
		}
		n := min(len(ps), len(as))
		for i := 0; i < n; i++ {
			c.unify(ps[len(ps)-n+i], as[len(as)-n+i], fn, cands)
		}
	case p.Kind == Function && a.Kind == Function:
		// A generic source infers through its base signature (each own
		// type parameter its constraint, else unknown), a generic target
		// through its erased one (each own type parameter any), as tsc's
		// inferFromSignatures: neither's own parameters leak into the
		// candidates. (Higher-order inference keeps the source's
		// parameters: they become the result's, higherOrderResult.)
		if len(a.TypeParams) > 0 && c.keepSigParams == 0 {
			base := make(map[*Type]*Type, len(a.TypeParams))
			for _, tp := range a.TypeParams {
				if tp.Constraint != nil {
					base[tp] = tp.Constraint
				} else {
					base[tp] = c.unknownT
				}
			}
			a = c.instantiate(a, base)
		}
		if len(p.TypeParams) > 0 && p != fn {
			erased := make(map[*Type]*Type, len(p.TypeParams))
			for _, tp := range p.TypeParams {
				erased[tp] = c.anyT
			}
			p = c.instantiate(p, erased)
		}
		if p.ThisType != nil && a.ThisType != nil {
			// `this: infer U` from `(this: number) => void`.
			c.unify(p.ThisType, a.ThisType, fn, cands)
		}
		if restTypeParam(p, fn) == nil {
			// A source rest of a fixed tuple (`(...args: [any]) => R`)
			// takes one parameter per element, as tsc's inferFromSignatures
			// reads it.
			a = c.expandTupleRest(a)
		}
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
		// The argument's result only when the parameter's could hold an
		// inference (tsc's applyToReturnTypes): `(...args: infer P) =>
		// any` never infers the argument's result.
		if pr := p.Ret(); pr != nil && c.mayInferIn(pr, fn, cands) {
			c.unify(pr, a.Ret(), fn, cands)
		}
		if p.Predicate != nil && a.Predicate != nil && p.Predicate.Type != nil && a.Predicate.Type != nil {
			c.unify(p.Predicate.Type, a.Predicate.Type, fn, cands) // `p1 is T` from `p1 is C`
		}
	case p.Kind == Anonymous:
		for _, pp := range p.Props {
			if ap := a.Prop(pp.Name); ap != nil {
				c.unify(pp.Type, ap.Type, fn, cands)
			}
		}
		c.unifyIndexes(p, a, fn, cands)
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
		c.unifyIndexes(p, src, fn, cands)
	}
}

// unifyIndexes infers p's string index from src's, or from an object
// type's implicit index (tsc's getImplicitIndexTypeOfType): its
// properties' types.
func (c *Checker) unifyIndexes(p, src *Type, fn *Type, cands map[*Type][]*Type) {
	if p.StringIndex == nil || !hasTypeParam(p.StringIndex) {
		return
	}
	if src.StringIndex != nil {
		c.unify(p.StringIndex, src.StringIndex, fn, cands)
		return
	}
	if src.Kind != Anonymous || len(src.Props) == 0 || len(src.Calls) > 0 || len(p.Props) > 0 {
		return
	}
	ts := make([]*Type, 0, len(src.Props))
	for _, ap := range src.Props {
		if !strings.HasPrefix(ap.Name, "@@") && !c.Unanswered(ap.Type) {
			ts = append(ts, ap.Type)
		}
	}
	if len(ts) > 0 {
		c.unify(p.StringIndex, c.in.union(ts...), fn, cands)
	}
}

// occurs reports whether the type parameter tp occurs in t.
func occurs(tp, t *Type) bool { return occursIn(tp, t, map[*Type]bool{}) }

// occursIn is occurs, seen the types searched so far: a type containing
// itself (`let x = { foo: () => x }`) is searched once.
func occursIn(tp, t *Type, seen map[*Type]bool) bool {
	if seen[t] {
		return false
	}
	seen[t] = true
	switch {
	case t == tp:
		return true
	case t.Flags&Deferred != 0 && tp.Value == "this":
		// A conditional over the polymorphic `this` (withThis decides it).
		for _, p := range t.DeferredTypeParams() {
			if p == tp {
				return true
			}
		}
	case t.Flags&Union != 0:
		for _, m := range t.Types {
			if occursIn(tp, m, seen) {
				return true
			}
		}
	case t.Flags&Object != 0:
		switch t.Kind {
		case Array:
			return occursIn(tp, t.Elem, seen)
		case Tuple:
			for _, e := range t.Elems {
				if occursIn(tp, e, seen) {
					return true
				}
			}
		case Function:
			for _, p := range t.Params {
				if occursIn(tp, p, seen) {
					return true
				}
			}
			if t.Predicate != nil && t.Predicate.Type != nil && occursIn(tp, t.Predicate.Type, seen) {
				return true // `n is T`
			}
			return occursIn(tp, t.Ret(), seen)
		case Anonymous:
			if t.RecursiveAlias() {
				for _, a := range t.TypeArgs {
					if occursIn(tp, a, seen) {
						return true
					}
				}
				return false
			}
			for _, p := range t.Props {
				if occursIn(tp, p.Type, seen) {
					return true
				}
			}
			for _, s := range append(append([]*Type{}, t.Calls...), t.Constructs...) {
				if occursIn(tp, s, seen) {
					return true
				}
			}
		case Interface, Instance:
			for _, a := range t.TypeArgs {
				if occursIn(tp, a, seen) {
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
	case t == nil:
		return false // a stand-in's result not yet filled
	case t.Flags&TypeParam != 0:
		// A member's polymorphic `this` is its instance's own, not a free
		// parameter.
		return !bound[t] && !(seen != nil && t.Value == "this")
	case t.Flags&Deferred != 0:
		// A member's unresolved library type (`ReturnType<typeof
		// setTimeout>`) names no parameter of the instance's context; one
		// over a type parameter not bound here (`Pick<S, C>`) does.
		if seen == nil {
			return true
		}
		for _, tp := range t.DeferredTypeParams() {
			if !bound[tp] && tp.Value != "this" {
				return true
			}
		}
		return false
	case t.Flags&Union != 0:
		ok, leave := t.enterSelf()
		defer leave()
		if !ok {
			return false
		}
		for _, m := range t.Types {
			if hasFreeTypeParam(m, seen, bound) {
				return true
			}
		}
	case t.Flags&Object != 0:
		if t.Kind == Array || t.Kind == Tuple {
			// (One holding itself, `T[]` with T itself, is searched once.)
			if t.freeWalk {
				return false
			}
			t.freeWalk = true
			defer func() { t.freeWalk = false }()
		}
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
			if t.lazyResult != nil {
				// A result not yet inferred is not read to decide this, as
				// tsc's couldContainTypeVariables reads no return type.
				return false
			}
			return hasFreeTypeParam(t.Ret(), seen, bound)
		case Anonymous:
			if t.RecursiveAlias() {
				for _, a := range t.TypeArgs {
					if hasFreeTypeParam(a, seen, bound) {
						return true
					}
				}
				return false
			}
			// An object holding itself (`{ m(o: Self): void }` through an
			// instantiation) is searched once.
			if t.freeWalk {
				return false
			}
			t.freeWalk = true
			defer func() { t.freeWalk = false }()
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
			if hasFreeTypeParam(t.StringIndex, seen, bound) || hasFreeTypeParam(t.NumberIndex, seen, bound) {
				return true // `{ [x: string]: T }`
			}
		case Interface, Instance:
			// A generic declaration's reference (`Init<T>`): its arguments
			// (one among them its own, `Node<Node<…>>` built recursively).
			if t.freeWalk {
				return false
			}
			t.freeWalk = true
			defer func() { t.freeWalk = false }()
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
	if t := paramAt(ct, i); t != nil && !hasFreeTypeParam(t, nil, c.enclosingTypeParams(fn, t, withOwnTypeParams(sigTypeParams(ct), t))) {
		// A generic contextual signature's own type parameters (`<T>(x: T)
		// => T`) type the function as that signature, as tsc gives it.
		return t
	}
	return nil
}

// enclosingTypeParams adds to bound the type parameters t reads that a
// function enclosing fn declares (`keys.forEach(key => …)` inside `function
// S<K>(keys: K[])` types key as K): they are in scope there, not free.
func (c *Checker) enclosingTypeParams(fn ast.Node, t *Type, bound map[*Type]bool) map[*Type]bool {
	var tps []*Type
	seen := map[*Type]bool{}
	var walk func(x *Type, depth int)
	walk = func(x *Type, depth int) {
		if x == nil || depth > 6 || seen[x] {
			return
		}
		seen[x] = true
		switch {
		case x.Flags&TypeParam != 0:
			if x.Symbol != nil && !bound[x] {
				tps = append(tps, x)
			}
		case x.Flags&Union != 0:
			for _, m := range x.Types {
				walk(m, depth+1)
			}
		case x.Flags&Object != 0:
			walk(x.Elem, depth+1)
			for _, e := range x.Elems {
				walk(e, depth+1)
			}
			for _, a := range x.TypeArgs {
				walk(a, depth+1)
			}
			if x.Kind == Function {
				for _, p := range x.Params {
					walk(p, depth+1)
				}
				walk(x.Ret(), depth+1)
			}
		}
	}
	walk(t, 0)
	if len(tps) == 0 {
		return bound
	}
	ancestors := map[ast.Node]bool{}
	for n := c.parentOf(fn); n != nil; n = c.parentOf(n) {
		ancestors[n] = true
	}
	out := bound
	for _, tp := range tps {
		for _, d := range tp.Symbol.Declarations {
			if d.Node != nil && ancestors[d.Node] {
				if len(out) == len(bound) {
					out = make(map[*Type]bool, len(bound)+len(tps))
					for k := range bound {
						out[k] = true
					}
				}
				out[tp] = true
				break
			}
		}
	}
	return out
}

// withOwnTypeParams adds to bound the type parameters a generic function
// type t declares itself (`<W>(r: W) => number` as a parameter's type
// names no free W).
func withOwnTypeParams(bound map[*Type]bool, t *Type) map[*Type]bool {
	if t.Flags&Object == 0 || t.Kind != Function || len(t.TypeParams) == 0 {
		return bound
	}
	m := make(map[*Type]bool, len(bound)+len(t.TypeParams))
	for k := range bound {
		m[k] = true
	}
	for _, tp := range t.TypeParams {
		m[tp] = true
	}
	return m
}

// sigTypeParams is the set of type parameters signature sig declares.
func sigTypeParams(sig *Type) map[*Type]bool {
	if len(sig.TypeParams) == 0 {
		return nil
	}
	m := make(map[*Type]bool, len(sig.TypeParams))
	for _, tp := range sig.TypeParams {
		m[tp] = true
	}
	return m
}

// contextualType is the type expected where the expression e occurs: a
// call's parameter, or an annotated variable's type.
// discriminateContext narrows a union context of object types to the
// members an object literal's discriminant properties select (`{ d: "s",
// cb: x => … }` in `S | N`), as tsc's
// discriminateContextualTypeByObjectMembers does; ct unchanged when none
// does or the union is not one of object types.
func (c *Checker) discriminateContext(lit *ast.ObjectLiteral, ct *Type) *Type {
	for _, m := range ct.Types {
		if m.Flags&Object == 0 || m.Flags&Union != 0 {
			return ct
		}
	}
	kept := ct.Types
	for _, prop := range lit.Properties {
		if prop.Key == "" || prop.KeyExpr != nil {
			continue
		}
		v := c.discriminantValue(prop.Value)
		if v == nil {
			continue
		}
		unit := false
		for _, m := range kept {
			if mp := m.Prop(prop.Key); mp != nil && someMember(mp.Type, Literal|Nullish) {
				unit = true
			}
		}
		if !unit {
			continue // not a discriminant of these members
		}
		var next []*Type
		for _, m := range kept {
			mp := m.Prop(prop.Key)
			if mp == nil || c.assignableTo(v, mp.Type) != no {
				next = append(next, m)
			}
		}
		if len(next) == 0 {
			return ct
		}
		kept = next
	}
	// A discriminant the literal omits, optional in some member, reads as
	// undefined: a member requiring it is not the one meant (tsc's
	// discriminateContextualTypeByObjectMembers over absent optionals).
	written := map[string]bool{}
	for _, prop := range lit.Properties {
		if prop.Key == "" && prop.KeyExpr == nil || prop.KeyExpr != nil {
			return c.unionOrSelf(ct, kept) // a spread or computed key may write any
		}
		written[prop.Key] = true
	}
	for _, m := range kept {
		for _, mp := range m.Props {
			if written[mp.Name] || !mp.Optional || !someMember(mp.Type, Literal|Nullish) {
				continue
			}
			written[mp.Name] = true
			var next []*Type
			for _, o := range kept {
				op := o.Prop(mp.Name)
				if op == nil || op.Optional || c.assignableTo(c.undefinedT, op.Type) != no {
					next = append(next, o)
				}
			}
			if len(next) > 0 {
				kept = next
			}
		}
	}
	return c.unionOrSelf(ct, kept)
}

// unifyMapped infers to a mapped type over a type parameter's keys (`{ [P
// in K]: X }`, `Record<K, V>`) from an object type: K is the object's
// keys, and X infers from its property types (tsc's inferToMappedType).
// False when p is no such mapped type or a is no such object.
func (c *Checker) unifyMapped(p, a *Type, fn *Type, cands map[*Type][]*Type) bool {
	n, ok := p.deferNode.(*ast.MappedType)
	if !ok || n.Type == nil || n.Constraint == nil {
		return false
	}
	if op, isOp := n.Constraint.(*ast.TypeOperator); isOp && op.Operator == "keyof" && a.Flags&Union == 0 &&
		a.Flags&(String|Number|Boolean|BigInt|StringLiteral|NumberLiteral|BooleanLiteral|BigIntLiteral) != 0 {
		// A primitive reverse-maps to its apparent members: held as the
		// primitive.
		if at := c.deferredPart(p, op.Type); !c.Unanswered(at) && at.Flags&TypeParam != 0 && typeParamOf(at, fn.TypeParams) {
			cands[at] = append(cands[at], a)
			return true
		}
		return false
	}
	if op, isOp := n.Constraint.(*ast.TypeOperator); isOp && op.Operator == "keyof" && a.Flags&Object != 0 && a.Flags&Union == 0 && (a.Kind == Tuple || a.Kind == Array && a.Head == nil) {
		return c.unifyReverseMappedList(p, n, op, a, fn, cands)
	}
	if a.Flags&Object == 0 || a.Flags&Union != 0 || a.Kind != Anonymous && a.Kind != Interface || a.partial || len(a.Calls) > 0 {
		return false
	}
	if op, isOp := n.Constraint.(*ast.TypeOperator); isOp && op.Operator == "keyof" {
		return c.unifyReverseMapped(p, n, op, a, fn, cands)
	}
	kt := c.deferredPart(p, n.Constraint)
	if c.Unanswered(kt) || kt.Flags&TypeParam == 0 || kt.Flags&Union != 0 || !typeParamOf(kt, fn.TypeParams) {
		return false
	}
	var keys, vals []*Type
	for _, q := range a.Props {
		if strings.HasPrefix(q.Name, "@@") {
			continue
		}
		keys = append(keys, c.in.literal(StringLiteral, q.Name))
		vals = append(vals, q.Type)
	}
	if len(keys) == 0 {
		return false
	}
	ku := c.in.union(keys...)
	cands[kt] = append(cands[kt], ku)
	if c.literalKept != nil {
		c.literalKept[kt] = true // keys infer as the literals they are
	}
	names := make([]string, 0, len(p.deferEnv)+1)
	vs := make([]*Type, 0, len(p.deferEnv)+1)
	for k, v := range p.deferEnv {
		names, vs = append(names, k), append(vs, v)
	}
	names, vs = append(names, n.KeyName), append(vs, ku)
	savedSubsts := c.substs
	c.substs = []map[*Type]*Type{p.deferSubst}
	pop := c.pushEnv(names, vs)
	tt := c.typeFromNode(n.Type, p.deferScope)
	pop()
	c.substs = savedSubsts
	if !c.Unanswered(tt) {
		c.unifyDepth++
		c.unify(tt, c.in.union(vals...), fn, cands)
		c.unifyDepth--
	}
	return true
}

// unifyReverseMapped infers A from an object type to a homomorphic mapped
// type over it (`{ [K in keyof A]: X<A[K]> }`), as tsc's reverse mapped
// type: each property of a infers what A's property is, from the property
// type to the template with that property of A the inference target.
func (c *Checker) unifyReverseMapped(p *Type, n *ast.MappedType, op *ast.TypeOperator, a *Type, fn *Type, cands map[*Type][]*Type) bool {
	at := c.deferredPart(p, op.Type)
	if c.Unanswered(at) || at.Flags&TypeParam == 0 || at.Flags&Union != 0 || !typeParamOf(at, fn.TypeParams) {
		return false
	}
	var props []*Property
	for _, q := range a.Props {
		if strings.HasPrefix(q.Name, "@@") || q.Accessor {
			continue
		}
		pt := c.reverseMappedMember(p, n, at, q.Name, c.in.literal(StringLiteral, q.Name), q.Type, fn)
		// A literal stays one where A's constraint types the property
		// with a primitive (its contextual type `A[K]`, tsc's
		// isLiteralOfContextualType); elsewhere it widens.
		if !c.primitiveConstrainedProp(at, q.Name) {
			pt = widen(c, pt)
		}
		props = append(props, &Property{Name: q.Name, Type: pt, Optional: q.Optional, Readonly: q.Readonly})
	}
	if len(props) == 0 && len(a.Props) > 0 {
		return false
	}
	rm := c.in.object(props)
	if a.StringIndex != nil {
		// A string index (`{ [x: string]: Box<V> }`): A's index, reversed
		// the same way.
		it := c.reverseMappedMember(p, n, at, stringIndexMember, c.in.literal(StringLiteral, stringIndexMember), a.StringIndex, fn)
		if !c.primitiveConstrainedProp(at, "") {
			it = widen(c, it)
		}
		rm = c.in.indexed(props, it, nil)
	}
	if c.mappedCand == nil {
		c.mappedCand = map[*Type]bool{}
	}
	c.mappedCand[rm] = true
	cands[at] = append(cands[at], rm)
	if c.literalKept != nil {
		c.literalKept[at] = true // each property widened as above
	}
	return true
}

// unifyReverseMappedList is unifyReverseMapped from a tuple, element by
// element, or an array, by its element: A is that tuple or array.
func (c *Checker) unifyReverseMappedList(p *Type, n *ast.MappedType, op *ast.TypeOperator, a *Type, fn *Type, cands map[*Type][]*Type) bool {
	at := c.deferredPart(p, op.Type)
	if c.Unanswered(at) || at.Flags&TypeParam == 0 || at.Flags&Union != 0 || !typeParamOf(at, fn.TypeParams) {
		return false
	}
	if a.Kind == Array {
		et := widen(c, c.reverseMappedMember(p, n, at, "", c.numT, a.Elem, fn))
		cands[at] = append(cands[at], c.in.array(et))
		return true
	}
	es := make([]*Type, len(a.Elems))
	for i, e := range a.Elems {
		k := strconv.Itoa(i)
		es[i] = widen(c, c.reverseMappedMember(p, n, at, k, c.in.literal(StringLiteral, k), e, fn))
	}
	cands[at] = append(cands[at], c.in.tupleLabelled(es, a.Opt, a.Labels))
	if c.literalKept != nil {
		c.literalKept[at] = true
	}
	return true
}

// reverseMappedMember is what A's member name (an array's element when
// name is "") is, inferred from src through mapped type p's template at
// key: A bound to a holder of a fresh target for that member.
func (c *Checker) reverseMappedMember(p *Type, n *ast.MappedType, at *Type, name string, key, src *Type, fn *Type) *Type {
	c.in.next++
	u := &Type{Flags: TypeParam, Value: "reverse%" + name, ID: c.in.next}
	holder := c.in.object([]*Property{{Name: name, Type: u}})
	if name == "" {
		holder = c.in.array(u)
	}
	names := make([]string, 0, len(p.deferEnv)+1)
	vs := make([]*Type, 0, len(p.deferEnv)+1)
	for k, v := range p.deferEnv {
		if v == at {
			v = holder
		}
		names, vs = append(names, k), append(vs, v)
	}
	names, vs = append(names, n.KeyName), append(vs, key)
	subst := map[*Type]*Type{at: holder}
	for k, v := range p.deferSubst {
		if k != at {
			subst[k] = v
		}
	}
	savedSubsts := c.substs
	c.substs = []map[*Type]*Type{subst}
	pop := c.pushEnv(names, vs)
	tt := c.typeFromNode(n.Type, p.deferScope)
	pop()
	c.substs = savedSubsts
	if c.Unanswered(tt) {
		return c.unknownT
	}
	local := map[*Type][]*Type{}
	wide := &Type{TypeParams: append(append([]*Type(nil), fn.TypeParams...), u)}
	c.unifyDepth++
	c.unify(tt, src, wide, local)
	c.unifyDepth--
	if cs := local[u]; len(cs) > 0 {
		return c.in.union(cs...)
	}
	return c.unknownT
}

// deferredMappedMember is the member type a deferred mapped type gives
// key: its template with the key bound (tsc's contextual type of a property
// of a generic mapped type), or nil.
func (c *Checker) deferredMappedMember(ct *Type, key string) *Type {
	t := c.deferredMappedTemplate(ct, key)
	if t == nil || c.generic(t) {
		return nil
	}
	return t
}

// deferredMappedTemplate is a deferred mapped type's template at key,
// generic or not (`A["v"]`), or nil.
func (c *Checker) deferredMappedTemplate(ct *Type, key string) *Type {
	if parts := c.deferredIntersectionParts(ct); parts != nil {
		// Of an intersection of mapped types: their templates at key, one
		// object with all their members.
		var ts []*Type
		for _, m := range parts {
			if m.Flags&Deferred == 0 {
				continue
			}
			if t := c.deferredMappedTemplate(m, key); t != nil {
				ts = append(ts, t)
			}
		}
		switch len(ts) {
		case 0:
			return nil
		case 1:
			return ts[0]
		}
		if t := c.mergeObjects(ts); !c.Unanswered(t) {
			return t
		}
		return nil
	}
	n, ok := ct.deferNode.(*ast.MappedType)
	if !ok || n.Type == nil {
		return nil
	}
	names := make([]string, 0, len(ct.deferEnv)+1)
	vals := make([]*Type, 0, len(ct.deferEnv)+1)
	for k, v := range ct.deferEnv {
		names, vals = append(names, k), append(vals, v)
	}
	names, vals = append(names, n.KeyName), append(vals, c.in.literal(StringLiteral, key))
	pop := c.pushEnv(names, vals)
	defer pop()
	t := c.typeFromNode(n.Type, ct.deferScope)
	if c.Unanswered(t) {
		return nil
	}
	return t
}

// unionOrSelf is ct when kept holds all its members, else their union.
func (c *Checker) unionOrSelf(ct *Type, kept []*Type) *Type {
	if len(kept) == len(ct.Types) {
		return ct
	}
	return c.in.union(kept...)
}

// discriminantValue is the unit type a property value spells for
// discrimination: a literal, a constant template (`${"s"}` is "s"), null
// or undefined; nil otherwise.
func (c *Checker) discriminantValue(v ast.Expression) *Type {
	switch x := v.(type) {
	case *ast.StringLiteral, *ast.NumberLiteral, *ast.BooleanLiteral, *ast.NullLiteral:
		if t := c.TypeOf(x); !c.Unanswered(t) && t.Flags&(Literal|Nullish) != 0 && t.Flags&Union == 0 {
			return t
		}
	case *ast.MemberExpression:
		// An enum member (`K.D`): its literal, which no context changes.
		if t := c.TypeOf(x); !c.Unanswered(t) && t.Member != "" && t.Flags&Union == 0 {
			return t
		}
	case *ast.Identifier:
		// A binding of a unit type (`const kind = "a"; { kind, … }`), as
		// tsc's isPossiblyDiscriminantValue counts an entity name.
		if t := c.TypeOf(x); !c.Unanswered(t) && t.Flags&(Literal|Nullish) != 0 && t.Flags&Union == 0 {
			return t
		}
	case *ast.TemplateLiteral:
		var b strings.Builder
		for i, q := range x.Quasis {
			b.WriteString(q)
			if i < len(x.Exprs) {
				sl, ok := x.Exprs[i].(*ast.StringLiteral)
				if !ok {
					// Other parts: a string, which still excludes members
					// whose property takes none (tsc's
					// isPossiblyDiscriminantValue counts any template).
					return c.strT
				}
				b.WriteString(sl.Value)
			}
		}
		return c.in.literal(StringLiteral, b.String())
	}
	return nil
}

// apparentContext is a contextual type as a literal's members read it: a
// type parameter not yet inferred is its constraint (tsc's
// getApparentTypeOfContextualType).
func apparentContext(ct *Type) *Type {
	for i := 0; ct != nil && ct.Flags&TypeParam != 0 && ct.Constraint != nil && i < 8; i++ {
		ct = ct.Constraint
	}
	return ct
}

func (c *Checker) contextualType(e ast.Node) *Type {
	if orig, ok := c.ctxStandIn[e]; ok {
		return c.contextualType(orig)
	}
	if x, ok := e.(ast.Expression); ok {
		if a, ok := c.assertion(x); ok && a.Satisfies != nil {
			if t := c.typeFromNode(a.Satisfies, c.b.Module); !c.Unanswered(t) {
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
			key := prop.Key
			if prop.KeyExpr != nil {
				// A computed key the checker can name (`[Symbol.iterator]`,
				// a const): its member's context.
				key = ""
				if k, ok := c.accessKey(prop.KeyExpr); ok {
					key = k
				}
			}
			if ast.Node(prop.Value) == e && key != "" {
				prop := prop
				prop.Key = key
				ct := apparentContext(c.contextualType(p))
				if ct != nil && ct.Flags&Union != 0 {
					ct = c.nonNullableContext(ct) // `T | undefined`: T's property
				}
				if ct != nil && ct.Flags&Union != 0 {
					ct = c.discriminateContext(p, ct)
				}
				if ct != nil && ct.Flags&Deferred != 0 {
					// A mapped type over keys not yet known (`{ [P in keyof
					// T]: TakeString }`): its template at this key.
					return c.deferredMappedMember(ct, prop.Key)
				}
				if ct == nil || ct.Flags&Object == 0 || (ct.Kind != Anonymous && ct.Kind != Interface && ct.Kind != Instance) {
					return nil
				}
				if parts := c.intersectionOf[ct]; parts != nil && ct.Prop(prop.Key) != nil {
					// An intersection: the members that declare the
					// property, an index signature's set aside (tsc's
					// getTypeOfPropertyOfContextualType, `{ a: () => "foo" }
					// & { [k: string]: () => any }`).
					var own []*Type
					for _, pt := range parts {
						if pt.Flags&Object != 0 {
							if q := pt.Prop(prop.Key); q != nil {
								own = append(own, q.Type)
							}
						}
					}
					if len(own) == 1 {
						return c.nonNullableContext(c.withThis(own[0], ct))
					}
				}
				if tp := ct.Prop(prop.Key); tp != nil {
					// An optional member's, without its undefined; its
					// `this` the context itself (`a: (p: this) => this`).
					return c.nonNullableContext(c.withThis(tp.Type, ct))
				}
				// No such member: the index signature that takes the key
				// (tsc's getContextualTypeForObjectLiteralElement), a
				// numeric-like key's number index first.
				if _, numeric := strconv.ParseFloat(prop.Key, 64); numeric == nil && ct.NumberIndex != nil {
					return ct.NumberIndex
				}
				if ct.StringIndex != nil {
					return ct.StringIndex
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
		if t := c.paramDefaultContext(p, p.Params, e); t != nil {
			return t
		}
		// An expression body: the declared result when annotated (an async
		// one's awaited), as a returned value's (returnContext), else the
		// contextual signature's result.
		if ast.Node(p.Body) == e && p.RetType != nil {
			t := c.declaredReturn(p, p.RetType, false)
			if t != nil && p.IsAsync {
				t = c.awaited(t, 0)
			}
			return t
		}
		if ast.Node(p.Body) == e && p.RetType == nil && !c.contextualizing[p] {
			c.contextualizing[p] = true
			ct := c.contextualType(p)
			delete(c.contextualizing, p)
			ct = c.callSignatureContext(ct)
			if ct != nil && ct.Flags&Object != 0 && ct.Kind == Function && ct.Ret() != nil && !c.Unanswered(ct.Ret()) {
				if p.IsAsync {
					// An async body's context is the result awaited (tsc's
					// getContextualReturnType for an async function).
					if aw := c.awaited(ct.Ret(), 0); aw != nil && !c.Unanswered(aw) {
						return aw
					}
					return nil
				}
				return ct.Ret()
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
				// (A generator's result is the iterator its yields fill.)
				if f.IsAsync {
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
			if iifeUnannotatedParam(p.Callee, j) {
				// Its parameter takes this argument's type (iifeParam): no
				// context, and typing the callee from here would re-enter.
				return nil
			}
			if _, super := p.Callee.(*ast.SuperExpression); super {
				// `super(…)`: the base class's constructor parameter.
				if sig := c.superSignature(p); sig != nil {
					if pt := paramAt(sig, j); pt != nil {
						return c.nonNullableContext(pt)
					}
				}
				return nil
			}
			callee := c.TypeOf(p.Callee)
			if callee.Flags&Union != 0 && !c.Unanswered(callee) && (p.Optional || inOptionalChain(p.Callee)) {
				// `xs?.forEach(cb)`, `f?.(cb)`: the call runs on the callee
				// the chain reaches, without its short-circuit's undefined.
				callee = c.filter(callee, func(m *Type) bool { return m.Flags&(Undefined|Null|Void) == 0 })
			}
			if callee.Flags&Union != 0 && !c.Unanswered(callee) {
				// A union of functions: the signature the call goes through
				// (unionSignature, tsc's getUnionSignatures).
				if u := c.unionSignature(callee); u != nil {
					callee = u
				}
			}
			if callee.Flags&Object == 0 || callee.Kind != Function {
				return nil
			}
			if len(callee.Overloads) > 0 {
				// The overload the call resolves to gives the context.
				if c.contextualizing[p] {
					// Asked again while the pick is computed: no context
					// yet, and the answers that depend on this are
					// provisional (an argument typed inner-first).
					c.reentries++
					if c.assigning[p] {
						c.cuts = append(c.cuts, p)
						c.cutCount++
					}
					return nil
				}
				c.contextualizing[p] = true
				open := !c.assigning[p]
				if open {
					c.assigning[p] = true
				}
				sig, m, ok := c.pickOverload(p, callee, false)
				delete(c.contextualizing, p)
				if open {
					delete(c.assigning, p)
				}
				if !ok {
					// The pick needs this argument's own type: as tsc
					// checks it against each candidate in turn, the first
					// whose arity and other arguments fit gives it.
					return c.firstFittingOverloadParam(p, callee, j)
				}
				if j > 0 && len(sig.TypeParams) > 0 {
					m = copyMap(m)
					c.inferFromEarlierArgs(p, sig, j, m)
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
			if so := c.inferSoFar[p]; len(callee.TypeParams) > 0 && c.inferring[p] && len(so) > 0 {
				pt = c.instantiate(pt, so)
			}
			if len(callee.TypeParams) > 0 && !c.inferring[p] {
				// The context-sensitive arguments before this one are typed
				// first and infer for it, left to right as tsc's
				// (`foo("", { method(p1) { return p1.length } }, { method(p2)
				// {…} })` types p2 from the first method's result).
				for k := 0; k < j && k < len(p.Args); k++ {
					if contextSensitive(p.Args[k]) {
						c.TypeOf(p.Args[k])
					}
				}
				// While this call's own type arguments are inferred, an
				// argument's context is the uninstantiated parameter: typing
				// the argument must not start the same inference again.
				c.inferring[p] = true
				savedBefore := c.sensBefore
				if j < len(p.Args) {
					c.sensBefore = p.Args[j]
				}
				m := c.contextualInference(c.inferArgsRecv(callRecv(p), p.Args, p.TypeArgs, callee, false), callee)
				c.sensBefore = savedBefore
				delete(c.inferring, p)
				if rt := c.returnTypeContext(p, callee, j, m); rt != nil {
					return rt
				}
				m = c.anyReturnContext(p, callee, m)
				c.fixParamTypeParams(p, pt, callee, m)
				pt = c.instantiate(pt, m)
				if pt.Flags&TypeParam != 0 && pt.Flags&Union == 0 && pt.Constraint != nil && typeParamOf(pt, callee.TypeParams) {
					// A type parameter nothing inferred yet: its constraint,
					// with what the other arguments inferred (`ctx: C` in
					// `M extends Record<string, (ctx: C) => unknown>`), as
					// tsc's inference mapper instantiates the signature.
					pt = c.instantiate(apparentContext(pt), m)
				}
			}
			return c.nonNullableContext(pt)
		}
	case *ast.ReturnStatement:
		// A returned value: the enclosing function's declared result (an
		// async one's awaited), tsc's getContextualTypeForReturnExpression.
		if ast.Node(p.Value) == e {
			return c.returnContext(p)
		}
	case *ast.FunctionDeclaration:
		return c.paramDefaultContext(p, p.Params, e)
	case *ast.FunctionExpression:
		return c.paramDefaultContext(p, p.Params, e)
	case *ast.SequenceExpression:
		// The last operand of a comma expression is its value: the
		// expression's own context (`(a, x => …)`).
		if len(p.Exprs) > 0 && ast.Node(p.Exprs[len(p.Exprs)-1]) == e {
			return c.contextualType(p)
		}
	case *ast.YieldExpression:
		// A yielded value: the enclosing generator's yield type (tsc's
		// getContextualTypeForYieldOperand); `yield*`'s operand, an
		// array of it.
		if ast.Node(p.Argument) == e {
			y := c.yieldContext(p)
			if y == nil {
				return nil
			}
			if p.Delegate {
				// `Iterable<Y>` (tsc's createIterableType; an async
				// generator's delegate is read the same way).
				if c.b.Globals != nil {
					if sym := c.b.Globals.Symbols.Get("Iterable"); sym != nil && sym.Flags&binder.Interface != 0 {
						if it := c.interfaceOf(sym, []*Type{y}); !c.Unanswered(it) {
							return it
						}
					}
				}
				return c.in.array(y)
			}
			return y
		}
	case *ast.TaggedTemplateExpression:
		// A substitution is the tag's argument after the strings array:
		// of the first signature its count fits, a generic one's not
		// modelled.
		for j, x := range p.Exprs {
			if ast.Node(x) != e {
				continue
			}
			fn := c.TypeOf(p.Tag)
			if c.Unanswered(fn) || fn.Flags&Object == 0 {
				return nil
			}
			fn = c.signaturesOf(fn, false)
			if c.Unanswered(fn) || fn.Kind != Function {
				return nil
			}
			sigs := fn.Overloads
			if len(sigs) == 0 {
				sigs = []*Type{fn}
			}
			for _, sig := range sigs {
				if !arityFits(sig, len(p.Exprs)+1) {
					continue
				}
				pt := paramAt(sig, j+1)
				if len(sig.TypeParams) > 0 && (pt == nil || c.readsTypeParams(pt, sig.TypeParams)) {
					return nil // (a generic one's, only where its type parameters are not read)
				}
				if pt != nil {
					return c.nonNullableContext(pt)
				}
				return nil
			}
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
			ct := apparentContext(c.contextualType(p))
			if ct == nil || c.Unanswered(ct) || ct.Flags&Object == 0 && ct.Flags&Union == 0 {
				return nil
			}
			if ct.Flags&Union != 0 {
				// A union context (`string | [string, ...H[]]`): each
				// array or tuple member's element at this index.
				var out []*Type
				for _, m := range ct.Types {
					switch {
					case m.Flags&Object == 0 || m.Flags&Union != 0:
					case m.Kind == Array && m.Head != nil:
						out = append(out, m.elemAt(i))
					case m.Kind == Array:
						out = append(out, m.Elem)
					case m.Kind == Tuple && i < len(m.Elems):
						out = append(out, m.Elems[i])
					}
				}
				if len(out) == 0 {
					return nil
				}
				return c.in.union(out...)
			}
			switch ct.Kind {
			case Array:
				if ct.Head != nil {
					return ct.elemAt(i) // a rest tuple's, at the index
				}
				return ct.Elem
			case Tuple:
				if i < len(ct.Elems) {
					return ct.Elems[i]
				}
			}
			// A readonly array's, an iterable's or an array-like's element
			// (tsc's iterated type of the context).
			if ct.Symbol != nil && len(ct.TypeArgs) >= 1 {
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
			ct := c.contextualType(p)
			if ct == nil && ast.Node(p.Right) == e {
				// No context for the whole: the right operand takes the
				// left's type (`f ?? (x => …)`).
				if lt := c.TypeOf(p.Left); !c.Unanswered(lt) {
					return lt
				}
			}
			return ct
		case "&&":
			if ast.Node(p.Right) == e {
				return c.contextualType(p)
			}
		}
	case *ast.ClassDeclaration:
		// An annotated field's initializer: the field's type.
		for _, f := range p.Fields {
			if f.Initializer != nil && ast.Node(f.Initializer) == e && f.Type != nil {
				scope := c.b.ScopeOf(p)
				if scope == nil {
					scope = c.b.Module
				}
				if t := c.typeFromNode(f.Type, scope); !c.Unanswered(t) {
					return t
				}
				return nil
			}
			if f.Initializer != nil && ast.Node(f.Initializer) == e && f.Static && f.Name != "" {
				// A class expression's static field: the property of the
				// class's own context (`let c: I = class { static x = … }`).
				if ce, ok := c.parentOf(p).(*ast.ClassExpression); ok {
					ct := apparentContext(c.contextualType(ce))
					if ct != nil && ct.Flags&Union != 0 {
						ct = c.nonNullableContext(ct) // `[I?]`'s element: I
					}
					if ct != nil && ct.Flags&Object != 0 && ct.Flags&Union == 0 {
						if pr := ct.Prop(f.Name); pr != nil && !c.Unanswered(pr.Type) {
							return pr.Type
						}
					}
				}
				return nil
			}
		}
	case *ast.AwaitExpression:
		// An awaited operand: the await's own context (tsc's
		// getContextualTypeForAwaitOperand, without its PromiseLike arm).
		if ast.Node(p.Argument) == e {
			return c.contextualType(p)
		}
	case *ast.VarDeclaration:
		if ast.Node(p.Init) == e && p.TypeAnnot != nil {
			if t := c.typeFromNode(p.TypeAnnot, c.b.Module); !c.Unanswered(t) {
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
			if p.TypeAnnot == nil {
				return c.defaultsPattern(p.Props)
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
						t = c.instantiate(tp.Default, m)
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
			// `K extends keyof T`, T generic: a key, string, number or
			// symbol, whatever T is.
			if op, ok := m.deferNode.(*ast.TypeOperator); ok && m.Flags&Deferred != 0 && op.Operator == "keyof" {
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
		var ta ast.TypeNode
		async := false
		switch f := n.(type) {
		case *ast.FunctionDeclaration:
			if f.IsGenerator {
				return c.generatorReturnOf(c.declaredReturn(n, f.ReturnType, false))
			}
			ta, async = f.ReturnType, f.IsAsync
		case *ast.FunctionExpression:
			if f.IsGenerator {
				g := c.declaredReturn(n, f.RetType, false)
				if f.RetType == nil {
					g = c.signatureResultContext(f)
				}
				return c.generatorReturnOf(g)
			}
			ta, async = f.RetType, f.IsAsync
		case *ast.ArrowFunction:
			ta, async = f.RetType, f.IsAsync
		default:
			continue
		}
		t := c.declaredReturn(n, ta, false)
		if fe, ok := n.(*ast.FunctionExpression); ok && ta == nil && c.literalAccessor(fe) {
			return nil // an accessor's result takes no context (tsc)
		}
		if fx, ok := n.(ast.Expression); ok && ta == nil {
			// Unannotated: its contextual signature's result (tsc's
			// getContextualReturnType).
			t = c.signatureResultContext(fx)
		}
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
						m[tp] = c.instantiate(tp.Default, m)
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
	if sig.Ret() == nil || c.returnCtx[e] {
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
	c.returnInfer++
	c.unify(sig.Ret(), ct, sig, cands)
	if len(cands) == 0 && ct.Flags&Union != 0 {
		// `WeakMap<object, number> | null`: inferred from the members
		// beside null and undefined, as tsc infers to a union's members.
		if nn := c.nonNullable(ct); nn != ct {
			c.unify(sig.Ret(), nn, sig, cands)
		}
	}
	c.returnInfer--
	if _, awaited := c.parentOf(e).(*ast.AwaitExpression); len(cands) == 0 && (awaited || c.asyncReturnValue(e)) {
		// An async function's return context is `T | PromiseLike<T>` in
		// tsc; a promise the value is matched against it as Promise<T>.
		if pt := c.promiseOf(ct); pt != nil {
			c.unify(sig.Ret(), pt, sig, cands)
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
func (c *Checker) constructsParamContext(sigs []*Type, typeArgs []ast.TypeNode, j int) *Type {
	for _, sig := range sigs {
		pt := paramAt(sig, j)
		if pt == nil {
			continue
		}
		if len(sig.TypeParams) > 0 && len(typeArgs) == len(sig.TypeParams) {
			m := map[*Type]*Type{}
			for i, tp := range sig.TypeParams {
				ta := typeArgs[i]
				if ta == nil {
					return pt
				}
				m[tp] = c.typeFromNode(ta, c.b.Module)
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
				m[tp] = c.instantiate(tp.Default, m)
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
		// `any`, `unknown` or a type parameter without a constraint give
		// no contextual signature (tsc reports the parameters, TS7006).
		if !c.Unanswered(ct) && ct.Flags&Union == 0 && (ct.Flags&(Any|Unknown) != 0 && ct.Flags&TypeParam == 0 || ct.Flags&TypeParam != 0 && ct.Constraint == nil && ct != c.thisT) {
			if ct.Flags&Any != 0 && c.libraryCallArg(fn) {
				// A parameter this compiler's library declares `any` where
				// Node's own declarations give a signature: no report.
				return true
			}
			return false
		}
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
		// and tsc reports x). An unconstrained type parameter (`f<T>(x:
		// T)`) gives its members none either.
		if lt := c.rawContext(p); lt != nil && lt.Flags&TypeParam != 0 && lt.Flags&Union == 0 && lt.Constraint == nil && lt != c.thisT {
			return false
		}
		return c.literalInContext(p)
	case *ast.ArrayLiteral:
		return c.literalInContext(p) // as an object literal's member
	case *ast.ReturnStatement:
		// A returned function is typed by the enclosing function's return
		// annotation or contextual result; with neither its parameters are
		// implicitly any (TS7006).
		return c.returnContext(p) != nil
	case *ast.CallExpression:
		// An argument of a call through any: the untyped call gives it no
		// contextual signature either.
		if ast.Node(p.Callee) != fn {
			if ct := c.TypeOf(p.Callee); !c.Unanswered(ct) && ct.Flags&Any != 0 && ct.Flags&Union == 0 && c.programAny(p.Callee, 0) {
				return false
			}
		}
		return true
	case *ast.NewExpression,
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
		if (p.Op == "||" || p.Op == "??") && ast.Node(p.Right) == fn {
			// The context the right operand takes (the whole's, else the
			// left's type) was read above: typed only when it is a
			// function, unless nothing answered it.
			ct := c.contextualType(fn)
			return ct == nil || c.Unanswered(ct)
		}
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
		case *ast.EnumDeclaration:
			return c.anyT // TS2332: no `this` in an enum member's value
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
			if c.opts.CompatJS() {
				return c.anyT // a JavaScript function's `this`, set by its call
			}
			return c.unanswered
		case *ast.ObjectLiteral:
			// A property's value (not a method or accessor, whose function
			// stops the walk above) sees the enclosing `this`.
			continue
		case *ast.FunctionDeclaration:
			if prog, ok := c.b.Module.Node.(*ast.Program); ok && prog.ThisParams[x] != nil {
				// An explicit `this: T` parameter types `this`.
				if tn := prog.ThisParams[x]; tn != nil {
					t := c.typeFromNode(tn, c.b.Module)
					// A class or interface instance stands as declared: what
					// narrows `this` is a union's test.
					if t.Flags&Object != 0 && (t.Kind == Instance || t.Kind == Interface) && !c.narrowsThis(x) {
						return t
					}
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
		case *ast.Program:
			// A module's own `this`: undefined in an ES module, the global
			// object in a script (tsc's checkThisExpression); none in a
			// namespace's body (TS2331).
			top, ok := child.(ast.Statement)
			if ok && namespaceMember(x, top) {
				return c.anyT
			}
			if ok && x.IsESM(top) {
				return c.undefinedT
			}
			return c.globalThisT
		}
	}
	return c.unanswered
}

// namespaceMember reports a top-level statement a namespace's body
// desugared to.
func namespaceMember(prog *ast.Program, st ast.Statement) bool {
	for _, g := range prog.NamespaceGroups {
		for _, m := range g.Members {
			if m == st {
				return true
			}
		}
	}
	return false
}

// thisInNamespace reports a `this` in a namespace's body, outside any
// function or class (TS2331).
func (c *Checker) thisInNamespace(e *ast.ThisExpression) bool {
	child := ast.Node(e)
	for n := c.parentOf(e); n != nil; child, n = n, c.parentOf(n) {
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.Program:
			top, ok := child.(ast.Statement)
			return ok && namespaceMember(x, top)
		}
	}
	return false
}

// thisInEnum reports a `this` in an enum member's value (TS2332).
func (c *Checker) thisInEnum(e *ast.ThisExpression) bool {
	for n := c.parentOf(e); n != nil; n = c.parentOf(n) {
		switch n.(type) {
		case *ast.EnumDeclaration:
			return true
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression, *ast.Program:
			return false
		}
	}
	return false
}

// capturesGlobalThis reports a `this` an arrow at a script's top level
// captures: the global object (TS7041 under noImplicitThis).
func (c *Checker) capturesGlobalThis(e *ast.ThisExpression) bool {
	arrow := false
	child := ast.Node(e)
	for n := c.parentOf(e); n != nil; child, n = n, c.parentOf(n) {
		switch x := n.(type) {
		case *ast.ArrowFunction:
			arrow = true
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.Program:
			top, ok := child.(ast.Statement)
			return arrow && !(ok && (x.IsESM(top) || namespaceMember(x, top)))
		}
	}
	return false
}

// narrowsThis reports a function whose body tests `this` (`this
// instanceof C`, `typeof this`, `"k" in this`), which can narrow it.
func (c *Checker) narrowsThis(fd *ast.FunctionDeclaration) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ClassDeclaration:
			return n == ast.Node(fd.Body)
		case *ast.BinaryExpression:
			if _, ok := x.Left.(*ast.ThisExpression); ok && (x.Op == "instanceof" || x.Op == "===" || x.Op == "!==" || x.Op == "==" || x.Op == "!=") {
				found = true
			}
			if _, ok := x.Right.(*ast.ThisExpression); ok && x.Op == "in" {
				found = true
			}
		case *ast.UnaryExpression:
			if _, ok := x.Arg.(*ast.ThisExpression); ok && x.Op == "typeof" {
				found = true
			}
		}
		return !found
	})
	return found
}

// thisImplicitlyAny reports tsc's TS2683 (noImplicitThis) for a `this`
// whose function has no type for it: a function declaration outside a
// class, or a function expression with no `this:` parameter that is not an
// object literal's member, not the right side of `obj.p = …`, and has no
// contextual signature with a `this` type (checkThisExpression,
// getContextualThisParameterType). A function expression whose context this
// checker cannot answer is not reported.
func (c *Checker) thisImplicitlyAny(e ast.Node) bool {
	if th, ok := e.(*ast.ThisExpression); ok && c.thisInNamespace(th) {
		return true
	}
	for n := c.parentOf(e); n != nil; n = c.parentOf(n) {
		switch x := n.(type) {
		case *ast.EnumDeclaration:
			return true
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
			case *ast.NewExpression:
				if ast.Node(p.Callee) == x {
					return true // constructed in place: tsc types no `this` either
				}
			}
			ct := c.contextualType(x)
			if ct == nil {
				// A returned function no context types gives it no `this`.
				_, returned := c.parentOf(x).(*ast.ReturnStatement)
				return returned
			}
			if c.Unanswered(ct) {
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
				if m.Flags&TypeParam != 0 && m.Flags&Union == 0 && m.Constraint != nil && !c.Unanswered(m.Constraint) &&
					m.Constraint.Flags&Object != 0 && m.Constraint.Flags&Union == 0 {
					// A type parameter's property: its constraint's (tsc's
					// apparent contextual type), whose literals it keeps.
					if tp := m.Constraint.Prop(prop.Key); tp != nil {
						out = append(out, tp.Type)
					} else if m.Constraint.StringIndex != nil {
						out = append(out, m.Constraint.StringIndex)
					}
					continue
				}
				switch {
				case m.Flags&Object != 0 && (m.Kind == Anonymous || m.Kind == Interface || m.Kind == Instance):
					if tp := m.Prop(prop.Key); tp != nil {
						out = append(out, tp.Type)
					}
				case m.Flags&Deferred != 0:
					// A mapped type over a type parameter's keys: its
					// template at this key (`A["v"]`).
					if mt := c.deferredMappedTemplate(m, prop.Key); mt != nil {
						out = append(out, mt)
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
			if iifeUnannotatedParam(p.Callee, j) {
				return nil // its parameter takes this argument's type (iifeParam)
			}
			callee := c.signaturesOf(c.TypeOf(p.Callee), false)
			if c.Unanswered(callee) || callee.Flags&Object == 0 || callee.Kind != Function || len(callee.Overloads) > 0 {
				return nil
			}
			return paramAt(callee, j)
		}
	case *ast.VarDeclaration:
		if ast.Node(p.Init) == e && p.TypeAnnot != nil {
			if t := c.typeFromNode(p.TypeAnnot, c.b.Module); !c.Unanswered(t) {
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
	case *ast.ClassDeclaration:
		// A class expression's static field: its context's property.
		for _, f := range p.Fields {
			if f.Static && f.Type == nil && f.Initializer != nil && ast.Node(f.Initializer) == e {
				return c.contextualType(e)
			}
		}
	case *ast.ArrowFunction:
		// An expression body: the contextual signature's result, as
		// contextualType reads it (`() => ({ prop: "foo" })` for `() => {
		// prop: "foo" }`).
		if ast.Node(p.Body) == e {
			if x, ok := e.(ast.Expression); ok {
				return c.contextualType(x)
			}
		}
	case *ast.ReturnStatement:
		// A returned object or array literal: the contextual result.
		if x, ok := e.(ast.Expression); ok && ast.Node(p.Value) == e {
			switch x.(type) {
			case *ast.ObjectLiteral, *ast.ArrayLiteral:
				return c.contextualType(x)
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
		case m.Flags&Deferred != 0:
			// A conditional decides by its branches (tsc's base
			// constraint of one).
			if _, cond := m.deferNode.(*ast.ConditionalType); cond && !c.literalCtxBusy[m] {
				if c.literalCtxBusy == nil {
					c.literalCtxBusy = map[*Type]bool{}
				}
				c.literalCtxBusy[m] = true
				kept := false
				for _, b := range c.deferredBranches(m) {
					if kept = c.literalOfContext(candidate, b); kept {
						break
					}
				}
				delete(c.literalCtxBusy, m)
				if kept {
					return true
				}
			}
			// `A["v"]` of a constrained A (a homomorphic mapped type's
			// member): the constraint's property decides.
			if n, ok := m.deferNode.(*ast.IndexedAccessType); ok {
				obj, key := c.deferredPart(m, n.ObjectType), c.deferredPart(m, n.IndexType)
				if !c.Unanswered(obj) && obj.Flags&TypeParam != 0 && obj.Constraint != nil && !c.Unanswered(key) && key.Flags&Literal != 0 {
					if pt := c.indexedAccess(obj.Constraint, key); !c.Unanswered(pt) && c.literalOfContext(candidate, &Type{Flags: TypeParam, Constraint: pt}) {
						return true
					}
				}
			}
		case m.Flags&TypeParam != 0:
			if con := m.Constraint; con != nil {
				for _, k := range members(con) {
					if k.Flags&Deferred != 0 && candidate.Flags&(StringLiteral|NumberLiteral) != 0 {
						if op, ok := k.deferNode.(*ast.TypeOperator); ok && op.Operator == "keyof" {
							return true // `K extends keyof T`: a key's literal (tsc's maybeTypeOfKind Index)
						}
					}
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
	if pt == nil || pt.Flags&TypeParam == 0 || sig.Ret() != pt {
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
	if ct == nil || c.Unanswered(ct) || ct.Flags&Unknown != 0 && ct.Flags&(Union|TypeParam) == 0 {
		return nil // an unknown context infers nothing (tsc ignores it)
	}
	return c.nonNullableContext(ct)
}

// libraryCallArg reports whether fn is an argument of a call or `new`
// whose callee this compiler's builtin library declares.
func (c *Checker) libraryCallArg(fn ast.Node) bool {
	var callee ast.Expression
	switch p := c.parentOf(fn).(type) {
	case *ast.CallExpression:
		callee = p.Callee
	case *ast.NewExpression:
		callee = p.Callee
	}
	if callee == nil || ast.Node(callee) == fn {
		return false
	}
	if m, ok := callee.(*ast.MemberExpression); ok {
		// A library class's method (`socket.send(…, cb)`).
		if ot := c.TypeOf(m.Object); !c.Unanswered(ot) && ot.Symbol != nil && c.librarySymbol(ot.Symbol) {
			return true
		}
	}
	sym := c.rootSymbol(callee)
	return sym != nil && c.librarySymbol(sym)
}

// programAny reports whether e's `any` is the program's own: its root
// binding declared with a type annotation (`declare const g: any`), or
// initialized from such a value. One the library gives (a listener's
// parameter this compiler's declarations leave untyped, where Node's are
// typed) is not, nor one of no known origin.
func (c *Checker) programAny(e ast.Expression, depth int) bool {
	sym := c.rootSymbol(e)
	if sym == nil || c.librarySymbol(sym) || depth > 4 || len(sym.Declarations) == 0 {
		return false
	}
	var params []ast.Param
	switch d := sym.Declarations[0].Node.(type) {
	case *ast.VarDeclaration:
		if d.TypeAnnot != nil {
			return true
		}
		return d.Init != nil && c.programAny(d.Init, depth+1)
	case *ast.FunctionDeclaration:
		params = d.Params
	case *ast.FunctionExpression:
		params = d.Params
	case *ast.ArrowFunction:
		params = d.Params
	default:
		return false
	}
	for _, prm := range params {
		if prm.Name == sym.Name {
			return prm.Type != nil // a parameter: annotated, or of unknown origin
		}
	}
	return true // the function itself, its result the program's
}

// rootSymbol is the binding a member, call or index chain starts from.
func (c *Checker) rootSymbol(e ast.Expression) *binder.Symbol {
	for {
		switch x := e.(type) {
		case *ast.MemberExpression:
			e = x.Object
		case *ast.CallExpression:
			e = x.Callee
		case *ast.IndexExpression:
			e = x.Object
		case *ast.Identifier:
			sym, _ := c.b.Resolve(x)
			return sym
		default:
			return nil
		}
	}
}

// librarySymbol reports a binding the builtin library declares.
func (c *Checker) librarySymbol(sym *binder.Symbol) bool {
	return ast.IsLibraryName(sym.Name) || c.inLibrary(sym.Scope)
}

// returnContextCands is the candidates fn's return type infers against the
// contextual type of the call whose arguments are callArgs, for the type
// parameters it mentions that no argument inferred; nil when there are
// none.
func (c *Checker) returnContextCands(callArgs []ast.Expression, fn *Type, cands map[*Type][]*Type, poisoned map[*Type]bool) map[*Type][]*Type {
	r := fn.Ret()
	if r == nil || len(callArgs) == 0 || len(fn.TypeParams) == 0 {
		return nil
	}
	need := false
	for _, tp := range fn.TypeParams {
		if len(cands[tp]) == 0 && !poisoned[tp] && occurs(tp, r) {
			need = true
		}
	}
	if !need {
		return nil
	}
	call := c.parentOf(callArgs[0])
	switch call.(type) {
	case *ast.CallExpression, *ast.NewExpression:
	default:
		return nil
	}
	// Its own guard: the call's arguments are typed while it is being
	// contextualized, and their inference asks this again.
	if c.returnCtxBusy[call] {
		return nil
	}
	if c.returnCtxBusy == nil {
		c.returnCtxBusy = map[ast.Node]bool{}
	}
	c.returnCtxBusy[call] = true
	ct := c.contextualType(call.(ast.Expression))
	delete(c.returnCtxBusy, call)
	if ct == nil || c.Unanswered(ct) || ct.Flags&(Any|Unknown) != 0 && ct.Flags&Union == 0 {
		return nil
	}
	out := map[*Type][]*Type{}
	c.unify(r, c.nonNullableContext(ct), fn, out)
	for tp, cs := range out {
		// A candidate naming an enclosing call's type variables (`A` of
		// `pipe<A, B>(op: Op<T, A>)` typing `map(…)`) is no inference: the
		// argument's own decides.
		var kept []*Type
		for _, t := range cs {
			if !c.generic(t) && !c.Unanswered(t) {
				kept = append(kept, t)
			}
		}
		if !typeParamOf(tp, fn.TypeParams) || len(kept) == 0 {
			delete(out, tp)
			continue
		}
		out[tp] = kept
	}
	return out
}

// anyReturnContext binds each type parameter the call's result is, that no
// argument inferred, to any when the call sits in an `any` context (tsc's
// return-type inference from it).
func (c *Checker) anyReturnContext(call *ast.CallExpression, sig *Type, m map[*Type]*Type) map[*Type]*Type {
	r := sig.Ret()
	if r == nil || r.Flags&TypeParam == 0 || r.Flags&Union != 0 || !typeParamOf(r, sig.TypeParams) {
		return m
	}
	if got, ok := m[r]; ok && got != nil && !c.Unanswered(got) && got != c.unknownT {
		return m
	}
	if c.contextualizing[call] {
		return m
	}
	c.contextualizing[call] = true
	ct := c.contextualType(call)
	delete(c.contextualizing, call)
	if ct == nil || c.Unanswered(ct) || ct.Flags&Any == 0 || ct.Flags&Union != 0 {
		return m
	}
	m = copyMap(m)
	m[r] = c.anyT
	return m
}

// ContextualSignature is the function type a function literal is typed in
// (its contextual signature), or nil.
// LiteralContextMember is the member of an object literal's union context
// its discriminant properties select (`{ type: K.D, … }` for `D | P`), or
// nil.
func (c *Checker) LiteralContextMember(lit *ast.ObjectLiteral) *Type {
	ct := c.contextualType(lit)
	if ct == nil || ct.Flags&Union == 0 {
		return nil
	}
	if d := c.discriminateContext(lit, ct); d != nil && d.Flags&Union == 0 {
		return d
	}
	return nil
}

// IsGeneric reports t mentions a type parameter.
func (c *Checker) IsGeneric(t *Type) bool { return c.generic(t) }

// ContextualType is the type expected where e occurs, nil when none is.
func (c *Checker) ContextualType(e ast.Node) *Type { return c.contextualType(e) }

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
	if ct != nil && ct.Flags&TypeParam != 0 && ct.Constraint != nil {
		// A type parameter not yet inferred (`fn: T` for
		// `T extends (...args: any[]) => any`): its constraint's signature,
		// as tsc's apparent contextual type reads it.
		ct = ct.Constraint
	}
	if ct != nil && ct.Flags&Union != 0 {
		return c.unionSignatureContext(ct)
	}
	if ct != nil && ct.Flags&Object != 0 && ct.Flags&Union == 0 {
		// Several signatures (an overload list, `{ (e: 0): A; (e: 1): B }`):
		// one with each parameter the union of theirs (tsc's
		// getIntersectedSignatures).
		sigs := ct.Overloads
		if ct.Kind != Function && len(ct.Props) == 0 {
			sigs = ct.Calls
		}
		if len(sigs) > 1 {
			if c.ImplicitAnyVariables {
				return nil // tsc intersects them only under noImplicitAny
			}
			if s := c.intersectedSignature(sigs); s != nil {
				return s
			}
		}
	}
	if ct == nil || ct.Flags&Object == 0 || ct.Kind == Function || len(ct.Calls) != 1 || len(ct.Props) > 0 {
		return ct
	}
	return ct.Calls[0]
}

// intersectedSignature combines non-generic signatures without rest
// parameters into one whose parameters are the unions of theirs, the longer
// list kept; its result, when they differ, is any. Nil for any other list.
func (c *Checker) intersectedSignature(sigs []*Type) *Type {
	var params []*Type
	var opts []bool
	var ret *Type
	var names []string
	for _, s := range sigs {
		if s.Kind != Function || len(s.TypeParams) > 0 || s.restParam || s.ThisType != nil || s.Ret() == nil {
			return nil
		}
		for i, p := range s.Params {
			opt := i < len(s.optionals) && s.optionals[i]
			if i >= len(params) {
				params, opts = append(params, p), append(opts, opt)
				if i < len(s.ParamNames) {
					names = append(names, s.ParamNames[i])
				} else {
					names = append(names, "")
				}
				continue
			}
			params[i] = c.in.union(params[i], p)
			opts[i] = opts[i] && opt
		}
		switch {
		case ret == nil:
			ret = s.Ret()
		case ret != s.Ret():
			ret = c.anyT
		}
	}
	return c.in.withParamNames(c.in.function(params, opts, false, ret, nil, nil), names)
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
		if p.AccessorKind != "" && p.KeyExpr == nil && p.Key != "" {
			continue // an accessor, like a method: the finished type's
		}
		if p.AccessorKind != "" || p.KeyExpr != nil || p.Key == "" {
			return nil
		}
		switch fv := p.Value.(type) {
		case *ast.SpreadElement:
			return nil
		case *ast.FunctionExpression:
			// A method another one reads through `this` (`a() { return
			// this.b(); }`): its signature, the result resolved when read,
			// as tsc resolves a method's return type apart.
			if fv.IsAsync || fv.IsGenerator {
				continue
			}
			saved := c.skipReturn
			c.skipReturn = fv
			shape := c.signatureType(fv, fv.Params, fv.RetType, fv.Body, nil, c.b.Module)
			c.skipReturn = saved
			if c.Unanswered(shape) || shape.Flags&Object == 0 || shape.Kind != Function {
				continue
			}
			if fv.RetType == nil {
				shape = c.in.deferred(shape, func() (*Type, bool) {
					full := c.TypeOf(fv)
					if c.Unanswered(full) || full.Flags&Object == 0 || full.Kind != Function {
						return c.unanswered, false
					}
					return full.Ret(), true
				}, func() *Type {
					// Read through `this` while its own result is inferred
					// (TS7023), its result any.
					if !c.literalCycles[fv] && !c.opts.CompatJS() && !c.ImplicitAnyVariables {
						c.literalCycles[fv] = true
						c.report(diag.ReturnImplicitAnyCycle, fv.GetPos(), p.Key)
					}
					return c.anyT
				})
			}
			props = append(props, &Property{Name: p.Key, Type: shape})
			continue
		case *ast.ArrowFunction:
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
	snap := c.in.objectOf(props, true, false, nil)
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

// iifeUnannotatedParam reports whether callee is a function written in
// place whose parameter j has no annotation.
func iifeUnannotatedParam(callee ast.Expression, j int) bool {
	var params []ast.Param
	switch f := callee.(type) {
	case *ast.FunctionExpression:
		params = f.Params
	case *ast.ArrowFunction:
		params = f.Params
	default:
		return false
	}
	if len(params) > 0 && params[0].Name == "this" {
		j++
	}
	if j >= len(params) {
		return false
	}
	p := params[j]
	return !p.Rest && (p.Type == nil)
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
	// Two arguments each asking the other's type (two object literals with
	// methods) give each other no context.
	if c.fitting[call] {
		return nil
	}
	c.fitting[call] = true
	defer delete(c.fitting, call)
	for _, sig := range callee.Overloads {
		if !arityFits(sig, len(call.Args)) {
			continue
		}
		generic := len(sig.TypeParams) > 0
		fits := true
		for i, a := range call.Args {
			if i == j {
				continue
			}
			if _, spread := a.(*ast.SpreadElement); spread {
				fits = false
				break
			}
			if contextSensitive(a) {
				// A callback fits a generic candidate's function parameter
				// it has no more parameters than (tsc's first pass, which
				// checks no context-sensitive argument's body).
				if !generic || !c.callbackFits(a, paramAt(sig, i)) {
					fits = false
					break
				}
				continue
			}
			pt, at := paramAt(sig, i), c.TypeOf(a)
			if pt == nil || c.Unanswered(at) || !hasTypeParam(pt) && !c.assignable(at, pt) || generic && hasTypeParam(pt) && pt.Flags&TypeParam == 0 {
				fits = false
				break
			}
		}
		if !fits {
			continue
		}
		pt := paramAt(sig, j)
		if pt == nil {
			continue
		}
		if generic && pt.Flags&TypeParam == 0 {
			// A callback's parameters, from what the call's result context
			// and the arguments before it infer (`pipe(x => x + 1, x => x *
			// 2)` for a `(n: number) => number`).
			if pt.Flags&Object == 0 || pt.Kind != Function {
				continue
			}
			m := map[*Type]*Type{}
			c.inferFromReturnContext(call, sig, m)
			c.inferFromEarlierArgs(call, sig, j, m)
			return c.nonNullableContext(c.instantiate(pt, m))
		}
		if generic {
			// A generic candidate (`mustCall<T>(fn: T): T`): its result's
			// context through T (tsc's return type inference), else T,
			// whose constraint is the signature.
			if rt := c.returnTypeContext(call, sig, j, map[*Type]*Type{}); rt != nil {
				return rt
			}
		}
		return c.nonNullableContext(pt)
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
		if s != nil && s.Flags&Object != 0 && s.Flags&Union == 0 && s.Kind != Function && len(s.Calls) == 0 && (!s.partial || uncallableInterface(s)) && !c.Unanswered(s) {
			continue // a member with no call signature gives none (`PromiseLike<F>` beside F)
		}
		if s != nil && s.Flags&Union == 0 && s.Flags&(String|Number|Boolean|BigInt|StringLiteral|NumberLiteral|BooleanLiteral|BigIntLiteral|ESSymbol|Void) != 0 {
			continue // a primitive has no call signature either
		}
		if s == nil || s.Flags&Object == 0 || s.Kind != Function || len(s.TypeParams) > 0 || s.Ret() == nil {
			return ct
		}
		if first == nil {
			first = s
		} else if !sameParams(first, s) {
			return ct
		}
		results = append(results, s.Ret())
	}
	if first == nil || len(results) < 2 {
		if first != nil {
			return first
		}
		return ct
	}
	return c.in.function(first.Params, first.optionals, first.restParam, c.in.union(results...), nil, nil)
}

// uncallableInterface reports an interface whose declarations name no call
// signature and extend nothing (`PromiseLike<T>`), so a member the checker
// does not model cannot make it callable.
func uncallableInterface(t *Type) bool {
	if t.Kind != Interface || t.Symbol == nil || len(t.Symbol.Declarations) == 0 {
		return false
	}
	for _, d := range t.Symbol.Declarations {
		id, ok := d.Node.(*ast.InterfaceDeclaration)
		if !ok || id.CallSig != nil || len(id.Heritage) > 0 {
			return false
		}
	}
	return true
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

// instKey is a function type instantiated under one mapping.
type instKey struct {
	t *Type
	m uintptr
}

// instState is an outermost instantiation's function types: those being
// instantiated, and those done.
type instState struct {
	busy    map[instKey]bool
	done    map[instKey]*Type
	pending map[instKey]*Type // stand-ins for unions met inside themselves
}

// instantiateSignature is instantiateType for a single signature t; ret,
// when not nil, stands for its result.
func (c *Checker) instantiateSignature(t *Type, m map[*Type]*Type, ret *Type) *Type {
	// A type parameter of its own the substitution leaves open whose
	// constraint names a substituted one (`filter<S extends T>` of a T[]):
	// a copy constrained by the instantiation, as tsc's
	// instantiateSignature gives it fresh type parameters.
	var tps []*Type
	var own map[*Type]*Type
	for _, tp := range t.TypeParams {
		if _, bound := m[tp]; bound {
			continue
		}
		if tp.Constraint != nil {
			if nc := c.instantiate(tp.Constraint, m); nc != tp.Constraint {
				ntp := c.constrainedCopy(tp, nc)
				if own == nil {
					own = make(map[*Type]*Type, len(m)+1)
					for k, v := range m {
						own[k] = v
					}
				}
				own[tp] = ntp
				tps = append(tps, ntp)
				continue
			}
		}
		tps = append(tps, tp)
	}
	if own != nil {
		m = own
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
	fn := c.in.withParamNames(c.in.function(ps, t.optionals, t.restParam, c.instantiateRet(t, m, ret), pred, tps), t.ParamNames)
	if t.ThisType != nil {
		fn = c.in.withThis(fn, c.instantiate(t.ThisType, m))
	}
	return fn
}

// instantiateRet is t's result instantiated, or ret when given.
func (c *Checker) instantiateRet(t *Type, m map[*Type]*Type, ret *Type) *Type {
	if ret != nil {
		return ret
	}
	return c.instantiate(t.Ret(), m)
}

// withoutOwn is m less fn's own type parameters, or nil when m binds none.
func withoutOwn(fn *Type, m map[*Type]*Type) map[*Type]*Type {
	var r map[*Type]*Type
	for _, tp := range fn.TypeParams {
		if _, ok := m[tp]; ok {
			if r == nil {
				r = make(map[*Type]*Type, len(m))
				for k, v := range m {
					r[k] = v
				}
			}
			delete(r, tp)
		}
	}
	return r
}

// typeParamOf reports t among a signature's type parameters ts.
func typeParamOf(t *Type, ts []*Type) bool {
	for _, x := range ts {
		if x == t {
			return true
		}
	}
	return false
}

// defaultsPattern is the contextual type an unannotated object pattern
// gives its initializer: its defaulted properties, of their defaults'
// types (tsc's getTypeFromBindingPattern), so `{ f = (x: string) => 0 } =
// id({ f: x => … })` types x; nil when none has a default of a type.
func (c *Checker) defaultsPattern(props []ast.DestructProp) *Type {
	var ps []*Property
	for _, dp := range props {
		if dp.Default == nil || dp.KeyExpr != nil || dp.Key == "" {
			continue
		}
		if _, fn := dp.Default.(*ast.ArrowFunction); !fn {
			if _, fe := dp.Default.(*ast.FunctionExpression); !fe {
				continue
			}
		}
		t := c.TypeOf(dp.Default)
		if c.Unanswered(t) {
			continue
		}
		ps = append(ps, &Property{Name: dp.Key, Type: t, Optional: true})
	}
	if len(ps) == 0 {
		return nil
	}
	return c.in.object(ps)
}

// generatorReturnOf is the TReturn of a generator's declared result g (a
// Generator<T, TReturn, TNext> or other iterator): the value of its
// `next()` result whose done is true (tsc's getIterationTypesOfGenerator-
// FunctionReturnType); nil when g gives none.
func (c *Checker) generatorReturnOf(g *Type) *Type {
	if g != nil && g.Flags&Union != 0 {
		// `number | Generator<…>`: the generator members' (tsc's
		// getIterationTypeOfGeneratorFunctionReturnType maps the union).
		var out []*Type
		for _, m := range g.Types {
			if r := c.generatorReturnOf(m); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return c.in.union(out...)
	}
	if g == nil || c.Unanswered(g) || g.Flags&Object == 0 {
		return nil
	}
	next := g.Prop("next")
	if next == nil {
		return nil
	}
	sig := c.signaturesOf(next.Type, false)
	if sig == nil || c.Unanswered(sig) || sig.Ret() == nil {
		return nil
	}
	var out []*Type
	for _, m := range members(sig.Ret()) {
		d, v := m.Prop("done"), m.Prop("value")
		if d != nil && v != nil && d.Type == c.trueT {
			out = append(out, v.Type)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return c.in.union(out...)
}

// signatureResultContext is the result of the contextual signature of the
// function expression f, or nil; asked while f's own context is being
// typed, none.
func (c *Checker) signatureResultContext(f ast.Expression) *Type {
	if c.contextualizing[f] {
		return nil
	}
	c.contextualizing[f] = true
	ct := c.contextualType(f)
	delete(c.contextualizing, f)
	ct = c.callSignatureContext(ct)
	if ct != nil && ct.Flags&Object != 0 && ct.Kind == Function && ct.Ret() != nil && !c.Unanswered(ct.Ret()) {
		return ct.Ret()
	}
	return nil
}

// superSignature is the base constructor signature a `super(…)` call
// runs, instantiated with the base's type arguments (`extends B<string>`);
// nil when the base has not exactly one.
func (c *Checker) superSignature(call *ast.CallExpression) *Type {
	inst := c.thisType(call)
	if c.Unanswered(inst) || inst.Flags&Object == 0 || inst.Kind != Instance || inst.Symbol == nil || inst.Base == nil {
		return nil
	}
	bv := c.baseClassValue(inst.Symbol)
	if bv == nil || c.Unanswered(bv) || len(bv.Constructs) != 1 {
		return nil
	}
	sig := bv.Constructs[0]
	if tps := sig.TypeParams; len(tps) > 0 {
		args := inst.Base.TypeArgs
		if len(args) != len(tps) {
			return nil
		}
		m := map[*Type]*Type{}
		for i, tp := range tps {
			m[tp] = args[i]
		}
		sig = c.instantiateSignature(sig, m, nil)
	}
	return sig
}

// primitiveConstrainedProp reports whether type parameter tp's constraint
// types its property name with a primitive.
func (c *Checker) primitiveConstrainedProp(tp *Type, name string) bool {
	k := tp.Constraint
	if k == nil || c.Unanswered(k) || k.Flags&Object == 0 {
		return false
	}
	for _, p := range k.Props {
		if p.Name != name {
			continue
		}
		for _, m := range members(p.Type) {
			if m.Flags&(String|Number|Boolean|BigInt|Literal|ESSymbol) != 0 {
				return true
			}
		}
	}
	return false
}

// insensitiveLiteralType is an object literal's type without its
// context-sensitive functions, a context-sensitive object literal property
// itself so reduced; nil when some property cannot be typed so.
func (c *Checker) insensitiveLiteralType(lit *ast.ObjectLiteral, depth int) *Type {
	if depth > 8 {
		return nil
	}
	var props []*Property
	for _, prop := range lit.Properties {
		if prop.KeyExpr != nil || prop.Key == "" || prop.AccessorKind != "" || prop.Value == nil {
			return nil
		}
		var t *Type
		switch {
		case !contextSensitive(prop.Value):
			t = c.TypeOf(prop.Value)
		default:
			inner, ok := prop.Value.(*ast.ObjectLiteral)
			if !ok {
				continue // a function: non-inferrable
			}
			t = c.insensitiveLiteralType(inner, depth+1)
			if t == nil {
				return nil
			}
		}
		if c.Unanswered(t) {
			return nil
		}
		props = append(props, &Property{Name: prop.Key, Type: t})
	}
	return c.in.object(props)
}

// deferredIntersectionParts are a deferred intersection's members, each
// evaluated as written there; nil for any other type.
func (c *Checker) deferredIntersectionParts(t *Type) []*Type {
	n, ok := t.deferNode.(*ast.IntersectionType)
	if !ok {
		return nil
	}
	parts := make([]*Type, 0, len(n.Types))
	for _, m := range n.Types {
		pt := c.deferredPart(t, m)
		if c.Unanswered(pt) {
			return nil
		}
		parts = append(parts, pt)
	}
	return parts
}

// variadicRest is fn's rest parameter when it is a variadic tuple type
// (`...args: [...A, L]`), else nil.
func variadicRest(fn *Type) *Type {
	n := len(fn.Params)
	if !fn.restParam || n == 0 {
		return nil
	}
	if rp := fn.Params[n-1]; rp.Flags&Deferred != 0 {
		if _, ok := rp.deferNode.(*ast.TupleType); ok {
			return rp
		}
	}
	return nil
}

// unifyVariadic infers from tuple a to variadic tuple type p (`[X, ...A,
// Y]`): its fixed elements from a's at the same place from either end, its
// one spread the tuple of a's elements between them (tsc's
// inferFromObjectTypes over variadic tuples).
func (c *Checker) unifyVariadic(p, a *Type, fn *Type, cands map[*Type][]*Type) bool {
	n, ok := p.deferNode.(*ast.TupleType)
	if !ok || a.Flags&Object == 0 || a.Flags&Union != 0 || a.Kind != Tuple || a.Opt != 0 {
		return false
	}
	spreadAt := -1
	elems := make([]ast.TypeNode, len(n.Elements))
	for i, e := range n.Elements {
		var spread ast.TypeNode
		switch m := e.(type) {
		case *ast.RestType:
			spread = m.Type
		case *ast.NamedTupleMember:
			if m.Optional {
				return false
			}
			if m.Rest {
				spread = m.Type
			} else {
				e = m.Type
			}
		case *ast.OptionalType:
			return false
		}
		if spread != nil {
			if spreadAt >= 0 {
				return false // two spreads: which takes what is not fixed
			}
			spreadAt, e = i, spread
		}
		elems[i] = e
	}
	if spreadAt < 0 {
		return false
	}
	before, after := spreadAt, len(elems)-spreadAt-1
	if len(a.Elems) < before+after {
		return false
	}
	for i := 0; i < before; i++ {
		c.unify(c.deferredPart(p, elems[i]), a.Elems[i], fn, cands)
	}
	for i := 0; i < after; i++ {
		c.unify(c.deferredPart(p, elems[spreadAt+1+i]), a.Elems[len(a.Elems)-after+i], fn, cands)
	}
	mid := a.Elems[before : len(a.Elems)-after]
	var labels []string
	if len(a.Labels) == len(a.Elems) {
		labels = a.Labels[before : len(a.Elems)-after]
	}
	c.unify(c.deferredPart(p, elems[spreadAt]), c.in.tupleLabelled(mid, 0, labels), fn, cands)
	return true
}

// readsTypeParams reports t mentioning one of ps (a generic function
// type's own parameters, nested in t, not counting).
func (c *Checker) readsTypeParams(t *Type, ps []*Type) bool {
	m := make(map[*Type]*Type, len(ps))
	for _, p := range ps {
		m[p] = c.unknownT
	}
	return c.instantiate(t, m) != t
}

// stringIndexMember stands for a string index signature's key to
// reverseMappedMember: a property no source names.
const stringIndexMember = "#string-index#"

// callbackFits reports a function literal a taking no more parameters than
// function type p does.
func (c *Checker) callbackFits(a ast.Expression, p *Type) bool {
	if p == nil || p.Flags&Object == 0 || p.Kind != Function {
		return false
	}
	n := -1
	switch f := skipOuter(a).(type) {
	case *ast.ArrowFunction:
		n = len(f.Params)
	case *ast.FunctionExpression:
		n = len(f.Params)
	}
	return n >= 0 && (p.restParam || n <= len(p.Params))
}

// inferFromEarlierArgs fills in m the type parameters of sig the
// arguments before argument j infer and m does not yet bind: typed left to
// right, an earlier callback's result infers for a later one's parameters.
func (c *Checker) inferFromEarlierArgs(call *ast.CallExpression, sig *Type, j int, m map[*Type]*Type) {
	cands := map[*Type][]*Type{}
	for k := 0; k < j && k < len(call.Args); k++ {
		pk := paramAt(sig, k)
		if pk == nil {
			continue
		}
		if at := c.TypeOf(call.Args[k]); !c.Unanswered(at) {
			c.unify(c.instantiate(pk, m), at, sig, cands)
		}
	}
	for _, tp := range sig.TypeParams {
		if cs := cands[tp]; len(cs) > 0 && (m[tp] == nil || m[tp] == tp || m[tp] == c.unknownT) {
			m[tp] = widen(c, c.in.union(cs...))
		}
	}
}

// yieldContext is the yield type of the generator y is in: its annotation's
// (an iterator or iterable type's first type argument, else what its
// `next()` gives while not done), or its contextual signature's result's;
// nil when there is none.
func (c *Checker) yieldContext(y *ast.YieldExpression) *Type {
	for n := c.parentOf(y); n != nil; n = c.parentOf(n) {
		var g *Type
		async := false
		switch f := n.(type) {
		case *ast.FunctionDeclaration:
			if !f.IsGenerator {
				return nil
			}
			g, async = c.declaredReturn(n, f.ReturnType, false), f.IsAsync
		case *ast.FunctionExpression:
			if !f.IsGenerator {
				return nil
			}
			g, async = c.declaredReturn(n, f.RetType, false), f.IsAsync
			if f.RetType == nil {
				g = c.signatureResultContext(f)
			}
		case *ast.ArrowFunction, *ast.ClassDeclaration:
			return nil
		default:
			continue
		}
		return c.generatorYieldOf(g, async)
	}
	return nil
}

// generatorYieldOf is what generator type g yields, nil for none.
func (c *Checker) generatorYieldOf(g *Type, async bool) *Type {
	if g == nil || c.Unanswered(g) {
		return nil
	}
	if g.Flags&Union != 0 {
		var out []*Type
		for _, m := range g.Types {
			if r := c.generatorYieldOf(m, async); r != nil {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return c.in.union(out...)
	}
	if g.Flags&Object == 0 {
		return nil
	}
	if g.Symbol != nil && len(g.TypeArgs) > 0 && c.inLibrary(g.Symbol.Scope) {
		switch g.Symbol.Name {
		case "Iterable", "Iterator", "IterableIterator", "IteratorObject", "Generator":
			if !async || g.Symbol.Name == "Iterable" {
				return g.TypeArgs[0]
			}
		case "AsyncIterable", "AsyncIterator", "AsyncIterableIterator", "AsyncIteratorObject", "AsyncGenerator":
			if async {
				return g.TypeArgs[0]
			}
		}
	}
	return nil
}

// paramDefaultContext is the contextual type of a parameter's default
// value e: the parameter's annotation (tsc's getContextualTypeForInitializer
// Expression), nil when e is no annotated parameter's default.
func (c *Checker) paramDefaultContext(fn ast.Node, params []ast.Param, e ast.Node) *Type {
	for _, prm := range params {
		if prm.Default == nil || ast.Node(prm.Default) != e || prm.Type == nil {
			continue
		}
		scope := c.b.ScopeOf(fn)
		if scope == nil {
			scope = c.b.Module
		}
		if t := c.typeFromNode(prm.Type, scope); !c.Unanswered(t) {
			return t
		}
	}
	return nil
}

// fixedByCallback lists the type parameters of fn, already given candidates,
// that a context-sensitive function literal argument at i reads through an
// unannotated parameter's contextual type: tsc fixes them on that read.
func (c *Checker) fixedByCallback(fn *Type, i int, a ast.Expression, cands map[*Type][]*Type) []*Type {
	var params []ast.Param
	switch f := a.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return nil
	}
	p := paramAt(fn, i)
	if p == nil || p.Flags&Object == 0 || p.Kind != Function {
		return nil
	}
	var out []*Type
	for _, tp := range fn.TypeParams {
		if len(cands[tp]) == 0 {
			continue
		}
		for j, lp := range params {
			if lp.Type != nil || lp.Rest || j >= len(p.Params) {
				continue
			}
			if occurs(tp, p.Params[j]) {
				out = append(out, tp)
				break
			}
		}
	}
	return out
}

// literalSources adds the literal types of at, the type of argument a, to
// fresh or regular by where they come from: a literal expression's are
// fresh (freshLiterals), a declared type's regular. A function argument's
// are those of its returned values.
func (c *Checker) literalSources(a ast.Expression, at *Type, fresh, regular map[*Type]bool) {
	var body ast.Expression
	var block *ast.BlockStatement
	switch f := a.(type) {
	case *ast.ArrowFunction:
		if f.IsAsync {
			return
		}
		if f.RetType != nil {
			markLiterals(at.Ret(), regular)
			return
		}
		body, block = f.Body, f.Block
	case *ast.FunctionExpression:
		if f.IsAsync || f.IsGenerator {
			return
		}
		if f.RetType != nil {
			markLiterals(at.Ret(), regular)
			return
		}
		block = f.Body
	default:
		fl := map[*Type]bool{}
		c.freshLiterals(a, fl, map[*binder.Symbol]bool{})
		for _, m := range members(at) {
			if m.Flags&Literal != 0 {
				if fl[m] {
					fresh[m] = true
				} else {
					regular[m] = true
				}
			}
		}
		return
	}
	if at == nil || at.Flags&Object == 0 || at.Kind != Function {
		return
	}
	if body != nil {
		c.literalSources(body, c.TypeOf(body), fresh, regular)
		return
	}
	if block == nil {
		return
	}
	ast.Inspect(block, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ArrowFunction, *ast.ClassExpression, *ast.ClassDeclaration:
			return false
		case *ast.ReturnStatement:
			if x.Value != nil {
				c.literalSources(x.Value, c.TypeOf(x.Value), fresh, regular)
			}
			return false
		}
		return true
	})
}

// markLiterals adds t's literal members to out.
func markLiterals(t *Type, out map[*Type]bool) {
	if t == nil {
		return
	}
	for _, m := range members(t) {
		if m.Flags&Literal != 0 {
			out[m] = true
		}
	}
}

// widenExcept is widen(t) leaving the literal members keep reports.
func widenExcept(c *Checker, t *Type, keep func(*Type) bool) *Type {
	if t.Flags&Union == 0 {
		if t.Flags&Literal != 0 && t.Member == "" && keep(t) {
			return t
		}
		return widen(c, t)
	}
	ms := make([]*Type, len(t.Types))
	for i, m := range t.Types {
		ms[i] = widenExcept(c, m, keep)
	}
	return c.in.union(ms...)
}

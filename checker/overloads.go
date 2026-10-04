package checker

import (
	"strconv"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/diag"
)

// overloadsType is the type of a function declared with overload
// signatures: the signatures only (the implementation's is not callable
// from outside, as in TypeScript). A generic signature's type parameters
// are its own, named through the environment while it is typed.
func (c *Checker) overloadsType(key string, scope *binder.Scope, sigs []*ast.FunctionDeclaration) *Type {
	var out []*Type
	for i, sd := range sigs {
		var tps []*Type
		for _, name := range sd.TypeParams {
			tps = append(tps, c.in.intern("v"+key+":"+strconv.Itoa(i)+":"+name, func() *Type {
				return &Type{Flags: TypeParam, Value: name}
			}))
		}
		pop := c.pushEnv(sd.TypeParams, tps)
		t := c.signatureType(sd, sd.Params, sd.ReturnType, nil, nil, scope)
		pop()
		if c.Unanswered(t) {
			return t
		}
		if len(tps) > 0 {
			t = c.in.function(t.Params, t.optionals, t.restParam, t.Result, t.Predicate, tps)
		}
		out = append(out, t)
	}
	return c.in.overloaded(out)
}

// resolveOverload picks the first overload a call's arguments fit (arity,
// then type argument inference, then each argument's assignability) and
// returns its result. An argument the checker cannot type leaves the call
// unanswered: the pick could depend on it.
func (c *Checker) resolveOverload(e *ast.CallExpression, fn *Type) *Type {
	sig, m, ok := c.pickOverload(e, fn, true)
	if !ok {
		return c.unanswered
	}
	for _, tp := range sig.TypeParams {
		// A type parameter the arguments did not infer (no candidate, no
		// explicit argument or default) leaves a result that mentions it
		// unanswered, not a guess of unknown.
		if len(e.TypeArgs) == 0 && m[tp] == c.unknownT && tp.Default == nil && occurs(tp, sig.Result) {
			return c.unanswered
		}
	}
	r := c.instantiate(sig.Result, m)
	for _, tp := range sig.TypeParams {
		if occurs(tp, r) {
			return c.unanswered // a type parameter the arguments did not infer
		}
	}
	return r
}

// pickOverload is the first overload of fn a call's arguments fit (arity,
// then type argument inference, then each argument's assignability), with
// its inferred type arguments; ok is false when an argument cannot be typed
// or none fits (a TypeScript error, P2.7). With guards, a callback for a
// type-guard parameter must itself be a guard; the check types the callback,
// so a callback's own contextual type (contextualType) picks without it.
func (c *Checker) pickOverload(e *ast.CallExpression, fn *Type, guards bool) (*Type, map[*Type]*Type, bool) {
	for _, a := range e.Args {
		if !contextSensitive(a) && !isEmptyArrayLiteral(a) && c.Unanswered(c.TypeOf(a)) {
			return nil, nil, false
		}
	}
	for _, sig := range fn.Overloads {
		if !arityFits(sig, len(e.Args)) || len(e.TypeArgs) > len(sig.TypeParams) {
			continue
		}
		var m map[*Type]*Type
		if len(sig.TypeParams) > 0 {
			// Picking a callback's own context infers from the other
			// arguments only: typing the callback would ask for its context.
			m = c.inferArgs(e.Args, e.TypeArgs, sig, guards)
		}
		fits := c.satisfiesConstraints(sig, m)
		for i, a := range e.Args {
			if !fits {
				break
			}
			if contextSensitive(a) {
				// A function literal needs a parameter that can hold a
				// function (`mustCall(fn)`, not `mustCall(exact?: number)`).
				if isFunctionLiteral(a) && !c.mayHoldFunction(paramAt(sig, i)) {
					fits = false
					break
				}
				// A callback for a type-guard parameter must itself be a
				// guard (`filter(x => x !== null)`), or the next overload
				// is tried (`filter(x => x > 1)`).
				if !guards {
					continue
				}
				if p := c.instantiate(paramAt(sig, i), m); p != nil && p.Flags&Object != 0 && p.Kind == Function && p.Predicate != nil && !p.Predicate.Asserts {
					if at := c.TypeOf(a); at.Flags&Object != 0 && at.Kind == Function && at.Predicate == nil {
						fits = false
						break
					}
				}
				continue
			}
			p := paramAt(sig, i)
			if p == nil || !c.argFits(a, c.instantiate(p, m)) {
				fits = false
				break
			}
		}
		if fits {
			return sig, m, true
		}
	}
	return nil, nil, false
}

// satisfiesConstraints reports whether each type argument inferred for a
// generic overload meets its type parameter's `extends` constraint: an
// overload whose inference breaks a constraint does not fit, and the next
// is tried (tsc's chooseOverload).
func (c *Checker) satisfiesConstraints(sig *Type, m map[*Type]*Type) bool {
	for _, tp := range sig.TypeParams {
		arg, ok := m[tp]
		if !ok || arg == nil || c.Unanswered(arg) || tp.Constraint == nil {
			continue
		}
		if !c.assignable(arg, c.instantiate(tp.Constraint, m)) {
			return false
		}
	}
	return true
}

// arityFits reports whether n arguments fit sig's parameters: at least the
// required ones, at most all of them unless the last is a rest.
func arityFits(sig *Type, n int) bool {
	required := 0
	for i := range sig.Params {
		if (i < len(sig.optionals) && sig.optionals[i]) || (sig.restParam && i == len(sig.Params)-1) {
			break
		}
		required++
	}
	return n >= required && (sig.restParam || n <= len(sig.Params))
}

// assignable reports whether a value of type a may be passed where p is
// expected, for overload applicability: a relation the checker does not
// decide counts as a fit, and a missing property as a misfit.
func (c *Checker) assignable(a, p *Type) bool {
	return c.relate(a, p, relation{missingIsNo: true}, 0) != no
}

// argFits reports whether the argument a fits parameter type p, for an
// overload's pick. An object literal's properties keep their literal types
// (`{ encoding: "utf8" }` fits `{ encoding: BufferEncoding }`), as tsc types
// the literal in each signature's context (overloadArgType).
func (c *Checker) argFits(a ast.Expression, p *Type) bool {
	if isEmptyArrayLiteral(a) {
		return c.acceptsArray(p) // `[]` takes its element type from the context
	}
	at := c.overloadArgType(a)
	if _, ok := a.(*ast.ArrayLiteral); ok {
		at = c.argTypeFor(a, p)
	}
	return !c.argMismatch(a, at, p, relation{missingIsNo: true})
}

// argMismatch reports argument a (of type at) definitely not fitting p:
// an object literal's excess property, or no member of p it relates to.
// Relating to a union is relating to one of its members, each with its
// own weak type check: "nope" is none of `{ x?: T } | "u" | null`.
func (c *Checker) argMismatch(a ast.Expression, at, p *Type, rel relation) bool {
	if c.literalExcess(a, p) {
		return true
	}
	if p != nil && p.Flags&Union != 0 && at != nil && at.Flags&Union == 0 && !c.Unanswered(at) {
		for _, m := range p.Types {
			if !c.weakMismatch(at, m) && c.relate(at, m, rel, 0) != no {
				return false
			}
		}
		return true
	}
	return c.weakMismatch(at, p) || c.relate(at, p, rel, 0) == no
}

// literalExcess is tsc's excess property check (TS2353) for an overload's
// pick: an object literal naming a property no object member of p has
// fits no signature taking p.
func (c *Checker) literalExcess(a ast.Expression, p *Type) bool {
	ol, ok := a.(*ast.ObjectLiteral)
	if !ok || p == nil || c.Unanswered(p) {
		return false
	}
	for _, pr := range ol.Properties {
		if pr.KeyExpr != nil || pr.AccessorKind != "" || pr.Key == "" {
			return false
		}
	}
	var objs []*Type
	for _, m := range members(p) {
		if m.Flags&(Null|Undefined) != 0 || m == c.missingT || m.Flags&(StringLike|NumberLike|BooleanLike|BigIntLike|ESSymbol) != 0 {
			continue
		}
		if m.Flags&Object == 0 || (m.Kind != Anonymous && m.Kind != Interface) || nominalOpen(m) || len(m.Props) == 0 || m.StringIndex != nil || m.NumberIndex != nil {
			return false // a member that takes any property
		}
		objs = append(objs, m)
	}
	if len(objs) == 0 {
		return false
	}
	for _, pr := range ol.Properties {
		if objectProto[pr.Key] {
			continue
		}
		known := false
		for _, m := range objs {
			if m.Prop(pr.Key) != nil {
				known = true
				break
			}
		}
		if !known {
			return true
		}
	}
	return false
}

// weakMismatch is tsc's weak type check (TS2559): a type whose properties
// are all optional accepts no value that shares none of them, an array or
// a primitive included (`["ab"]` is no ExecFileSyncOptions).
func (c *Checker) weakMismatch(a, p *Type) bool {
	if a == nil || p == nil || c.Unanswered(a) || c.Unanswered(p) {
		return false
	}
	for _, m := range members(p) {
		if m.Flags&Object == 0 || (m.Kind != Anonymous && m.Kind != Interface) || len(m.Props) == 0 ||
			m.StringIndex != nil || m.NumberIndex != nil || len(m.Calls) > 0 || len(m.Constructs) > 0 {
			if m.Flags&(Undefined|Null) == 0 {
				return false // a member that is not weak
			}
			continue
		}
		for _, pr := range m.Props {
			if !pr.Optional {
				return false
			}
		}
	}
	switch {
	case a.Flags&Object != 0 && (a.Kind == Array || a.Kind == Tuple):
		return true
	case a.Flags&(String|StringLiteral|Number|NumberLiteral|Boolean|BooleanLiteral) != 0 && a.Flags&Union == 0:
		return true
	}
	return false
}

// argTypeFor is argument a's type where p is expected: an array literal
// against a tuple type of its length is a tuple (its contextual type makes
// it one), otherwise a's own type.
func (c *Checker) argTypeFor(a ast.Expression, p *Type) *Type {
	lit, ok := a.(*ast.ArrayLiteral)
	if !ok || p == nil {
		return c.TypeOf(a)
	}
	if p.Flags&TypeParam != 0 && p.Constraint != nil && len(lit.Elements) > 0 {
		// A type parameter constrained by a tuple type (`T extends
		// readonly unknown[] | []`, Promise.all's) infers a tuple.
		for _, m := range members(p.Constraint) {
			if m.Flags&Object != 0 && m.Kind == Tuple {
				elems := make([]*Type, 0, len(lit.Elements))
				for _, x := range lit.Elements {
					t := c.TypeOf(x)
					if _, spread := x.(*ast.SpreadElement); spread || c.Unanswered(t) {
						return c.TypeOf(a)
					}
					elems = append(elems, widen(c, t))
				}
				return c.in.tuple(elems)
			}
		}
	}
	// A context that may be absent (`[K, V][] | null`): its array or tuple.
	if p.Flags&Union != 0 {
		var ctx *Type
		for _, m := range p.Types {
			if m.Flags&Object != 0 && (m.Kind == Tuple || m.Kind == Array || readonlyArrayElem(m) != nil) {
				if ctx != nil {
					return c.TypeOf(a)
				}
				ctx = m
			}
		}
		if ctx == nil {
			return c.TypeOf(a)
		}
		p = ctx
	}
	// An array of tuples (`[['a', 1]]` for `[K, V][]`): each element typed
	// against the element context, so the tuples infer.
	elemCtx := p.Elem
	if ro := readonlyArrayElem(p); ro != nil {
		elemCtx = ro
	}
	if p.Flags&Object != 0 && (p.Kind == Array || readonlyArrayElem(p) != nil) && elemCtx != nil && elemCtx.Flags&Object != 0 && elemCtx.Kind == Tuple && len(lit.Elements) > 0 {
		elems := make([]*Type, 0, len(lit.Elements))
		for _, x := range lit.Elements {
			if _, spread := x.(*ast.SpreadElement); spread {
				return c.TypeOf(a)
			}
			t := c.argTypeFor(x, elemCtx)
			if c.Unanswered(t) {
				return t
			}
			elems = append(elems, t)
		}
		return c.in.array(c.in.union(elems...))
	}
	if p.Flags&Object != 0 && (p.Kind == Array || readonlyArrayElem(p) != nil) && elemCtx != nil && len(lit.Elements) > 0 {
		// An array of literals for a literal element type (`["sign"]` for
		// `KeyUsage[]`): each element keeps its literal type.
		elems := make([]*Type, 0, len(lit.Elements))
		for _, x := range lit.Elements {
			if _, spread := x.(*ast.SpreadElement); spread {
				return c.TypeOf(a)
			}
			t := c.TypeOf(x)
			if c.Unanswered(t) {
				return t
			}
			if !c.literalOfContext(t, elemCtx) && !c.keepsLiteral(x, t) {
				t = c.widenFrom(x, t)
			}
			elems = append(elems, t)
		}
		return c.in.array(c.in.union(elems...))
	}
	if p.Flags&Object == 0 || p.Kind != Tuple || len(p.Elems) != len(lit.Elements) || len(lit.Elements) == 0 {
		return c.TypeOf(a)
	}
	elems := make([]*Type, len(lit.Elements))
	for i, x := range lit.Elements {
		if _, spread := x.(*ast.SpreadElement); spread {
			return c.TypeOf(a)
		}
		t := c.TypeOf(x)
		if c.Unanswered(t) {
			return t
		}
		if !c.keepsLiteral(x, t) {
			t = c.widenFrom(x, t)
		}
		elems[i] = t
	}
	return c.in.tuple(elems)
}

// overloadArgType is an argument's type for relating it to an overload's
// parameter: an object literal's with its literal property types kept.
func (c *Checker) overloadArgType(a ast.Expression) *Type {
	if ol, ok := a.(*ast.ObjectLiteral); ok {
		if t := c.literalObjectType(ol); t != nil {
			return t
		}
	}
	return c.TypeOf(a)
}

// literalObjectType is an object literal's type with its properties'
// literal types unwidened, nested literals included; nil when a property is
// computed, an accessor or untyped.
func (c *Checker) literalObjectType(ol *ast.ObjectLiteral) *Type {
	var props []*Property
	for _, p := range ol.Properties {
		if p.KeyExpr != nil || p.AccessorKind != "" || p.Key == "" {
			return nil
		}
		var t *Type
		if inner, ok := p.Value.(*ast.ObjectLiteral); ok {
			t = c.literalObjectType(inner)
		} else if arr, ok := p.Value.(*ast.ArrayLiteral); ok {
			t = c.literalArrayType(arr)
		} else {
			t = c.TypeOf(p.Value)
		}
		if t == nil || c.Unanswered(t) {
			return nil
		}
		props = append(props, &Property{Name: p.Key, Type: t})
	}
	return c.in.object(props)
}

// ambientOverloads are a function's declarations when every one is a
// signature without a body (an ambient overload set), or nil.
func ambientOverloads(sym *binder.Symbol) []*ast.FunctionDeclaration {
	var sigs []*ast.FunctionDeclaration
	for _, d := range sym.Declarations {
		fd, ok := d.Node.(*ast.FunctionDeclaration)
		if !ok || !fd.Ambient && fd.Body != nil || len(fd.Overloads) > 0 {
			return nil
		}
		sigs = append(sigs, fd)
	}
	return sigs
}

// argMisfit is where a call's arguments definitely do not fit sig: the
// index of the first argument that is not assignable (with the parameter
// type it was checked against), or -1 when every argument fits or may fit.
func (c *Checker) argMisfit(e *ast.CallExpression, sig *Type) (int, *Type) {
	var m map[*Type]*Type
	if len(sig.TypeParams) > 0 {
		m = c.inferArgs(e.Args, e.TypeArgs, sig, true)
		for _, t := range m {
			if c.Unanswered(t) {
				return -1, nil
			}
		}
	}
	for i, a := range e.Args {
		if contextSensitive(a) {
			// A function literal's annotated parameters still meet the
			// callback's: `(e, a: number) => …` does not take a string `a`.
			if p := paramAt(sig, i); p != nil && !hasTypeParam(p) && c.literalParamsMisfit(a, c.instantiate(p, m)) {
				return i, c.instantiate(p, m)
			}
			continue
		}
		p := paramAt(sig, i)
		if p == nil {
			continue
		}
		p = c.instantiate(p, m)
		if hasTypeParam(p) {
			continue
		}
		at := c.overloadArgType(a)
		if _, ok := a.(*ast.ArrayLiteral); ok {
			at = c.argTypeFor(a, p)
		}
		if c.argMismatch(a, at, p, relation{}) {
			return i, p
		}
	}
	return -1, nil
}

// checkOverloadCall reports a call no overload of fn accepts, as tsc's
// resolveCall does: with one overload of the right arity, that overload's
// argument error (TS2345); with two or three, TS2769 at the call; with more,
// TS2769 at the last one's failing argument. A call no overload takes by
// arity is not reported (tsc's TS2575 family).
func (c *Checker) checkOverloadCall(e *ast.CallExpression, fn *Type) {
	type misfit struct {
		arg int
		p   *Type
	}
	var candidates []misfit
	arityMisses := 0
	for _, sig := range fn.Overloads {
		if !arityFits(sig, len(e.Args)) {
			arityMisses++
			continue
		}
		if len(e.TypeArgs) > len(sig.TypeParams) {
			continue
		}
		i, p := c.argMisfit(e, sig)
		if i < 0 {
			return // this overload may apply
		}
		candidates = append(candidates, misfit{i, p})
	}
	switch n := len(candidates); {
	case n == 0:
		if arityMisses == len(fn.Overloads) {
			c.reportOverloadArity(e, fn.Overloads)
		}
	case n == 1:
		a := e.Args[candidates[0].arg]
		if at := c.TypeOf(a); c.weakMismatch(at, candidates[0].p) {
			c.report(diag.NoCommonProperties, a.GetPos(), at, c.nonNullable(candidates[0].p))
			return
		}
		c.report(diag.ArgNotAssignable, a.GetPos(), widen(c, c.TypeOf(a)), candidates[0].p)
	case n <= 3:
		pos := startOf(e.Callee)
		same := true
		for _, m := range candidates {
			same = same && m.arg == candidates[0].arg
		}
		if same {
			pos = e.Args[candidates[0].arg].GetPos()
		}
		c.report(diag.NoOverloadMatches, pos)
	default:
		c.report(diag.NoOverloadMatches, e.Args[candidates[n-1].arg].GetPos())
	}
}

// literalArrayType is an array literal's type with its elements' literal
// types unwidened (`["pipe", "inherit"]` as ("pipe" | "inherit")[], which a
// candidate's contextual type would keep); its own type when an element is
// spread or untyped.
func (c *Checker) literalArrayType(arr *ast.ArrayLiteral) *Type {
	if len(arr.Elements) == 0 {
		return c.TypeOf(arr)
	}
	var elems []*Type
	for _, x := range arr.Elements {
		if _, spread := x.(*ast.SpreadElement); spread {
			return c.TypeOf(arr)
		}
		var t *Type
		if inner, ok := x.(*ast.ObjectLiteral); ok {
			t = c.literalObjectType(inner)
		} else {
			t = c.TypeOf(x)
		}
		if t == nil || c.Unanswered(t) {
			return c.TypeOf(arr)
		}
		elems = append(elems, t)
	}
	return c.in.array(c.in.union(elems...))
}

// isEmptyArrayLiteral reports `[]`.
func isEmptyArrayLiteral(a ast.Expression) bool {
	lit, ok := a.(*ast.ArrayLiteral)
	return ok && len(lit.Elements) == 0
}

// acceptsArray reports whether p takes some array: an array, a tuple, a
// ReadonlyArray, `any`, or a union with one of those.
func (c *Checker) acceptsArray(p *Type) bool {
	if p == nil || c.Unanswered(p) {
		return false
	}
	for _, m := range members(p) {
		switch {
		case m.Flags&(Any|Unknown) != 0:
			return true
		case m.Flags&Object != 0 && (m.Kind == Array || m.Kind == Tuple):
			return true
		case m.Flags&Object != 0 && m.Kind == Interface && m.Symbol != nil && (m.Symbol.Name == "ReadonlyArray" || m.Symbol.Name == "Array"):
			return true
		}
	}
	return false
}

// readonlyArrayElem is T of a `ReadonlyArray<T>` (`readonly T[]`), or nil.
func readonlyArrayElem(t *Type) *Type {
	if t.Flags&Object != 0 && t.Kind == Interface && t.Symbol != nil && t.Symbol.Name == "ReadonlyArray" && len(t.TypeArgs) == 1 {
		return t.TypeArgs[0]
	}
	return nil
}

// isFunctionLiteral reports an arrow function or function expression.
func isFunctionLiteral(a ast.Expression) bool {
	switch a.(type) {
	case *ast.ArrowFunction, *ast.FunctionExpression:
		return true
	}
	return false
}

// mayHoldFunction reports whether a parameter type admits a function: a
// function or object type, a type parameter, any/unknown, or a union with
// one of those.
func (c *Checker) mayHoldFunction(p *Type) bool {
	if p == nil {
		return false
	}
	switch {
	case p.Flags&(Any|Unknown|TypeParam|Object) != 0:
		return true
	case p.Flags&Union != 0:
		for _, m := range p.Types {
			if c.mayHoldFunction(m) {
				return true
			}
		}
	}
	return false
}

// reportOverloadArity is TS2554 for a call no overload takes that many
// arguments of: the range the overloads span, as tsc words it.
func (c *Checker) reportOverloadArity(e *ast.CallExpression, sigs []*Type) {
	lo, hi := -1, 0
	for _, sig := range sigs {
		required := 0
		for i := range sig.Params {
			if (i < len(sig.optionals) && sig.optionals[i]) || (sig.restParam && i == len(sig.Params)-1) {
				break
			}
			required++
		}
		if lo < 0 || required < lo {
			lo = required
		}
		if sig.restParam {
			hi = -1
		} else if hi >= 0 && len(sig.Params) > hi {
			hi = len(sig.Params)
		}
	}
	if len(e.Args) < lo {
		if hi < 0 {
			c.report(diag.ArgCountAtLeast, e.Callee.GetPos(), lo, len(e.Args))
		} else {
			c.report(diag.ArgCount, e.Callee.GetPos(), arity(lo, hi), len(e.Args))
		}
		return
	}
	if hi >= 0 && len(e.Args) > hi {
		c.report(diag.ArgCount, e.Args[hi].GetPos(), arity(lo, hi), len(e.Args))
	}
}

// literalParamsMisfit reports whether a function literal's annotated
// parameter cannot take what a callback of type p passes there (parameters
// relate contravariantly).
func (c *Checker) literalParamsMisfit(a ast.Expression, p *Type) bool {
	var params []ast.Param
	switch f := a.(type) {
	case *ast.ArrowFunction:
		params = f.Params
	case *ast.FunctionExpression:
		params = f.Params
	default:
		return false
	}
	if p == nil || p.Flags&Object == 0 || p.Kind != Function || len(p.Overloads) > 0 {
		return false
	}
	target := p
	scope := c.b.ScopeOf(a)
	if scope == nil {
		scope = c.b.Module
	}
	for i, prm := range params {
		if prm.Rest || prm.Type == nil || prm.Type.TypeNode() == nil || i >= len(target.Params) {
			continue
		}
		if i == len(target.Params)-1 && target.restParam {
			continue
		}
		annotated := c.typeFromNode(prm.Type.TypeNode(), scope)
		if c.Unanswered(annotated) {
			continue
		}
		if c.relate(target.Params[i], annotated, relation{}, 0) == no {
			return true
		}
	}
	return false
}

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
		if !contextSensitive(a) && c.Unanswered(c.TypeOf(a)) {
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
		fits := true
		for i, a := range e.Args {
			if contextSensitive(a) {
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
	if _, ok := a.(*ast.ArrayLiteral); ok {
		return c.assignable(c.argTypeFor(a, p), p)
	}
	return c.assignable(c.overloadArgType(a), p)
}

// argTypeFor is argument a's type where p is expected: an array literal
// against a tuple type of its length is a tuple (its contextual type makes
// it one), otherwise a's own type.
func (c *Checker) argTypeFor(a ast.Expression, p *Type) *Type {
	lit, ok := a.(*ast.ArrayLiteral)
	if !ok || p == nil || p.Flags&Object == 0 || p.Kind != Tuple || len(p.Elems) != len(lit.Elements) || len(lit.Elements) == 0 {
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
		if c.relate(at, p, relation{}, 0) == no {
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
	for _, sig := range fn.Overloads {
		if !arityFits(sig, len(e.Args)) || len(e.TypeArgs) > len(sig.TypeParams) {
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
	case n == 1:
		a := e.Args[candidates[0].arg]
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

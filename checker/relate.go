package checker

import (
	"KlainMainLang/lib"
	"strconv"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
)

// ternary is a relation's answer: a type the checker does not model fully
// (unanswered, a type parameter, a function) makes the answer maybe, and
// only a definite no is reported.
type ternary int

const (
	no ternary = iota
	maybe
	yes
)

func all(rs ...ternary) ternary {
	out := yes
	for _, r := range rs {
		if r < out {
			out = r
		}
	}
	return out
}

// relation selects what a relation reports.
type relation struct {
	// missingIsNo makes a missing required property a definite no, and a
	// primitive a definite no for a weak type it shares no property with.
	// Check leaves both maybe: TypeScript reports them as TS2741 and TS2559,
	// not TS2322.
	missingIsNo bool
	// bivariant relates a signature's parameters both ways: the target is
	// a method (tsc's strictFunctionTypes exempts method and constructor
	// declarations). It holds for that one signature, not its parameters'
	// own types.
	bivariant bool
}

// assignableTo is TypeScript's assignability of s to t, under --strict, for
// the types the checker models: primitives and their literals, the native
// widths, null and undefined, unions, arrays, tuples, functions and object
// shapes. Anything else is maybe.
func (c *Checker) assignableTo(s, t *Type) ternary {
	return c.relate(s, t, relation{}, 0)
}

// relKey identifies one relation question for relate's memo.
type relKey struct {
	s, t *Type
	rel  relation
}

// relate memoizes relateUncached for object pairs, as TypeScript's
// relation cache does: a pair still being related answers maybe (the
// assumption a recursive type's relation makes), and a result that leaned
// on such an assumption is not remembered.
func (c *Checker) relate(s, t *Type, rel relation, depth int) ternary {
	if s == nil || t == nil || s.Flags&Object == 0 || t.Flags&Object == 0 || s == t {
		return c.relateUncached(s, t, rel, depth)
	}
	k := relKey{s, t, rel}
	if r, ok := c.relMemo[k]; ok {
		return r
	}
	if c.relActive[k] {
		c.relAssumed++
		return maybe
	}
	if c.relMemo == nil {
		c.relMemo = map[relKey]ternary{}
		c.relActive = map[relKey]bool{}
	}
	// A generic type reached again inside its own expansion (`Foo<T>`
	// holding `Foo<(arg: T) => void>`): tsc relates such a reference by the
	// variance it is still measuring, which takes it as related.
	id := recursionIdentity(s) + "|" + recursionIdentity(t)
	if id != "|" && c.relIdent[id] > 0 {
		c.relAssumed++
		return maybe
	}
	if c.relIdent == nil {
		c.relIdent = map[string]int{}
	}
	before := c.relAssumed
	c.relActive[k] = true
	if id != "|" {
		c.relIdent[id]++
	}
	r := c.relateUncached(s, t, rel, depth)
	delete(c.relActive, k)
	if id != "|" {
		c.relIdent[id]--
	}
	if c.relAssumed == before && depth <= 20 {
		c.relMemo[k] = r
	}
	return r
}

func (c *Checker) relateUncached(s, t *Type, rel relation, depth int) ternary {
	switch {
	case s != nil && t != nil && s.Flags&Deferred != 0 && t.Flags&Deferred != 0 && depth <= 20:
		return c.relateConditionals(s, t, rel, depth)
	case s != nil && t != nil && t.Flags&Deferred != 0 && (s == t || s.Flags&Never != 0 && s.Flags&Union == 0 && !c.Unanswered(s)):
		return yes // never goes anywhere, a deferred type to itself
	case s != nil && t != nil && s.Flags&Deferred != 0 && t.Flags&Deferred == 0 && !c.Unanswered(t) && !hasTypeParam(t) && depth <= 20:
		return c.relateConditionalSource(s, t, rel, depth)
	case s == nil || t == nil || c.Unanswered(s) || c.Unanswered(t) || depth > 20,
		s.Flags&Deferred != 0 || t.Flags&Deferred != 0:
		return maybe
	case s == t:
		return yes
	case s.Flags&TypeParam != 0 && s.narrowOf != nil && (s.narrowOf == t || t.narrowOf == s.narrowOf && c.relate(s.Constraint, t.Constraint, rel, depth+1) == yes):
		return yes // `T & string` is a T
	case t.Flags&(Any|Unknown) != 0, s.Flags&(Any|Never) != 0:
		return yes
	case s.Flags&TypeParam != 0 && t.Flags&TypeParam == 0 && s != c.thisT && !hasTypeParam(t) && !c.unionHolds(t, s):
		// A parameter goes where its constraint goes (unknown when it has
		// none), and only there when the target is concrete (tsc relates a
		// type parameter by its constraint).
		k := s.Constraint
		if k == nil {
			k = c.unknownT
		}
		return c.relate(k, t, rel, depth+1)
	case s.Flags&TypeParam != 0 && s.Constraint != nil && t.Flags&TypeParam == 0:
		// A constrained parameter goes where its constraint goes.
		if c.relate(s.Constraint, t, rel, depth+1) == yes {
			return yes
		}
		return maybe
	case t.Flags&TypeParam != 0 && t.Constraint == nil && s.Flags&TypeParam == 0 && s.Flags&Union == 0 &&
		s.Flags&(Null|Undefined|String|Number|Boolean|BigInt|StringLiteral|NumberLiteral|BooleanLiteral|BigIntLiteral|Int|Float32|ESSymbol) != 0:
		// A concrete primitive, null or undefined is no unconstrained
		// type parameter: the parameter may be instantiated with any other
		// type (`return null` from `get(): V`).
		return no
	case s.Flags&TypeParam != 0 || t.Flags&TypeParam != 0:
		return maybe
	case t.Brand != nil && s.Flags&Union == 0:
		// A branded primitive takes only a value carrying its brand: the
		// intersection's every member must hold.
		if s.Brand == nil {
			if c.weakType(t.Brand) && s.Flags&(Nullish|Object) == 0 {
				// A brand of only optional members holds of any value of
				// the base (an intersection's member skips the weak type
				// check): `"x"` is a `string & { tag?: T }`.
				return c.relate(s, t.BrandBase, rel, depth+1)
			}
			return no // a plain primitive lacks the brand; an object is no primitive
		}
		return all(c.relate(s.BrandBase, t.BrandBase, rel, depth+1), c.relate(s.Brand, t.Brand, rel, depth+1))
	case s.Brand != nil:
		// A branded primitive goes where either part goes, or to the
		// union member that is its own brand.
		if t.Flags&Union != 0 {
			for _, m := range t.Types {
				if m.Brand != nil && c.relate(s, m, rel, depth+1) == yes {
					return yes
				}
			}
		}
		if r := c.relate(s.BrandBase, t, rel, depth+1); r != no {
			return r
		}
		return c.relate(s.Brand, t, rel, depth+1)
	case s.Flags&Union != 0:
		// Every member must go.
		out := yes
		for _, m := range s.Types {
			r := c.relate(m, t, rel, depth+1)
			if r == no {
				return no
			}
			out = all(out, r)
		}
		return out
	case s.Flags&Unknown != 0 && t.Flags&Union != 0:
		// unknown goes to a union that is itself unknown: {} | null |
		// undefined.
		var f TypeFlags
		for _, m := range t.Types {
			if m.Flags&Object != 0 && m.Kind == Anonymous && len(m.Props) == 0 {
				f |= Object
			}
			f |= m.Flags & (Null | Undefined)
		}
		return boolTern(f == Object|Null|Undefined)
	case t.Flags&Union != 0:
		// Some member must take it.
		// A member a required property is missing from takes no part
		// (tsc relates each member fully: TS2322 for the union).
		mrel := rel
		mrel.missingIsNo = rel.missingIsNo || s.Flags&Object != 0
		out := no
		for _, m := range t.Types {
			switch c.relate(s, m, mrel, depth+1) {
			case yes:
				return yes
			case maybe:
				out = maybe
			}
		}
		if out == no && s.Flags&Object != 0 && !(s.Kind == Array || s.Kind == Tuple) || out == no && (s.Kind == Array || s.Kind == Tuple) && unionHasArray(t) {
			// An object against a union of objects: discriminated-union rules
			// (an array literal against one with an array member: its
			// contextual element types). With one object member (`T | null |
			// undefined`) its no stands; an array is no other member's.
			objects := 0
			for _, m := range t.Types {
				if m.Flags&Object != 0 {
					objects++
				}
			}
			if objects > 1 && (s.Kind == Array || s.Kind == Tuple || c.sharesDiscriminant(s, t) && !c.nonDiscriminantRulesOut(s, t, rel, depth)) {
				return maybe
			}
		}
		return out
	case s.Flags&Unknown != 0:
		return no // unknown goes only to any and unknown
	}
	// Enum literals, as TypeScript's isSimpleTypeRelatedTo relates them:
	// two members are related only when one (s == t is answered above); a
	// member is otherwise its literal value against a non-enum target (a
	// numeric member goes to number, not to string or to another literal);
	// a numeric member takes any number, and a number literal of its value.
	if s.Member != "" && t.Member != "" {
		return no
	}
	if t.Member != "" {
		if t.Flags&NumberLiteral != 0 {
			switch {
			case s.Flags&(Number|Int|Float32) != 0:
				return yes
			case s.Flags&NumberLiteral != 0:
				return boolTern(s.Value == t.Value)
			}
		}
		return no
	}
	switch {
	case t.Flags&NonPrimitive != 0:
		// `object` takes every object type, and no primitive.
		if s.Flags&(Object|NonPrimitive) != 0 {
			return yes
		}
		return no
	case s.Flags&NonPrimitive != 0:
		switch {
		case t.Flags&Object != 0 && t.Kind == Anonymous && len(t.Props) == 0:
			return yes // `{}`
		case t.Flags&Object != 0:
			return maybe // an object may have any members
		}
		return no
	}
	switch {
	case t.Flags&Literal != 0:
		return no // s is not t, and a literal takes only itself
	case t.Flags&String != 0:
		return boolTern(s.Flags&StringLike != 0)
	case t.Flags&(Number|Int|Float32) != 0:
		// The native widths are this compiler's numbers.
		return boolTern(s.Flags&NumberLike != 0)
	case t.Flags&Boolean != 0:
		return boolTern(s.Flags&BooleanLike != 0)
	case t.Flags&BigInt != 0:
		return boolTern(s.Flags&BigIntLike != 0)
	case t.Flags&ESSymbol != 0:
		return boolTern(s.Flags&ESSymbol != 0)
	case t.Flags&Void != 0:
		return boolTern(s.Flags&(Void|Undefined) != 0)
	case t.Flags&Undefined != 0:
		return boolTern(s.Flags&Undefined != 0)
	case t.Flags&Null != 0:
		return boolTern(s.Flags&Null != 0)
	case t.Flags&Never != 0:
		return no
	case t.Flags&Object != 0:
		if s.Flags&(Nullish|StringLike|NumberLike|BooleanLike|BigIntLike|ESSymbol) != 0 {
			if t.Kind == Anonymous && len(t.Props) == 0 {
				// `{}` takes every non-nullish value.
				return boolTern(s.Flags&Nullish == 0)
			}
			if s.Flags&Nullish != 0 || t.Kind == Array || t.Kind == Tuple || t.Kind == Function {
				return no
			}
			// A weak type (every property optional) takes a primitive only
			// when its apparent type shares one of them (TypeScript's weak
			// type detection): "utf8" is not a `{ encoding?: …; flag?: … }`.
			// Only for an overload's pick: tsc reports it as TS2559, not
			// TS2322.
			if rel.missingIsNo && c.weakType(t) {
				if at := c.apparentType(s); at != nil && !sharesProp(at, t) {
					return no
				}
			}
			// A required member the primitive's apparent type (Number,
			// String, Boolean) lacks rules it out: 42 is not a Buffer.
			if c.primitiveLacksLibraryMember(s, t) {
				return no
			}
			return maybe // a primitive's apparent members (String's length…)
		}
		if s.Flags&Object == 0 {
			return maybe
		}
		return c.relateObjects(s, t, rel, depth)
	}
	return maybe
}

func boolTern(b bool) ternary {
	if b {
		return yes
	}
	return no
}

// sharesDiscriminant reports whether s has a property some object member of
// the union t types as a unit (a literal, null or undefined): the
// discriminant tsc's discriminated-union relation decides by, member by
// member, which the per-member relation above does not model.
func (c *Checker) sharesDiscriminant(s, t *Type) bool {
	for _, m := range t.Types {
		if m.Flags&Object == 0 {
			continue
		}
		for _, mp := range m.Props {
			if mp.Type == nil || s.Prop(mp.Name) == nil {
				continue
			}
			if mp.Type.Flags&(Literal|Boolean|Null|Undefined) != 0 || mp.Type.Flags&Union != 0 && everyMember(mp.Type, Literal|Boolean|Null|Undefined) {
				return true
			}
		}
	}
	return false
}

// nonDiscriminantRulesOut reports whether every object member of the union
// t rejects s through a property that is no discriminant (one no member
// types as a unit): s lacks it while the member requires it, or s's type
// for it does not relate. Splitting s by its discriminants cannot change
// such a property, so the discriminated-union relation rejects s too.
func (c *Checker) nonDiscriminantRulesOut(s, t *Type, rel relation, depth int) bool {
	disc := map[string]bool{}
	for _, m := range t.Types {
		if m.Flags&Object == 0 {
			continue
		}
		for _, mp := range m.Props {
			if mp.Type != nil && (mp.Type.Flags&(Literal|Boolean|Null|Undefined) != 0 || mp.Type.Flags&Union != 0 && everyMember(mp.Type, Literal|Boolean|Null|Undefined)) {
				disc[mp.Name] = true
			}
		}
	}
	for _, m := range t.Types {
		if m.Flags&Object == 0 {
			continue
		}
		out := false
		for _, mp := range m.Props {
			if mp.Type == nil {
				continue
			}
			if disc[mp.Name] {
				// A discriminant none of whose source values the member
				// takes: no split of s reaches the member.
				if sp := s.Prop(mp.Name); sp != nil && sp.Type != nil {
					overlap := false
					for _, v := range c.discriminantValues(sp.Type) {
						if c.relate(v, mp.Type, rel, depth+1) != no {
							overlap = true
							break
						}
					}
					if !overlap {
						out = true
						break
					}
				}
				continue
			}
			sp := s.Prop(mp.Name)
			if sp == nil {
				if !mp.Optional && rel.missingIsNo {
					out = true
					break
				}
				continue
			}
			if sp.Type != nil && c.relate(sp.Type, mp.Type, rel, depth+1) == no {
				out = true
				break
			}
		}
		if !out {
			return false
		}
	}
	return true
}

// discriminantValues is t's members with boolean split into true and
// false, the values tsc's discriminated-union relation tries one by one.
func (c *Checker) discriminantValues(t *Type) []*Type {
	var out []*Type
	for _, m := range members(t) {
		if m.Flags&Boolean != 0 && m.Flags&Literal == 0 {
			out = append(out, c.trueT, c.falseT)
			continue
		}
		out = append(out, m)
	}
	return out
}

// covariantLibInterfaces are the builtin generic interfaces whose type
// parameters tsc measures covariant: two instantiations of one relate by
// their type arguments (Promise<number> is no Promise<string>), which the
// structural comparison through their generic methods cannot decide.
var covariantLibInterfaces = map[string]bool{
	"Promise": true, "PromiseLike": true, "ReadonlyArray": true,
	"Set": true, "ReadonlySet": true, "Map": true, "ReadonlyMap": true,
	"WeakSet": true, "WeakMap": true, "Iterable": true, "Iterator": true,
	"IterableIterator": true, "AsyncIterable": true, "AsyncIterator": true,
	"AsyncIterableIterator": true,
}

// relateObjects relates two object types structurally, as TypeScript does.
func (c *Checker) relateObjects(s, t *Type, rel relation, depth int) ternary {
	if s.Kind == Interface && t.Kind == Interface && s.Symbol != nil && s.Symbol == t.Symbol &&
		len(s.TypeArgs) > 0 && len(s.TypeArgs) == len(t.TypeArgs) && covariantLibInterfaces[s.Symbol.Name] && c.inLibrary(s.Symbol.Scope) {
		for i := range s.TypeArgs {
			if c.relate(s.TypeArgs[i], t.TypeArgs[i], rel, depth+1) == no {
				return no
			}
		}
	}
	switch t.Kind {
	case Array:
		if t.Head != nil {
			// A rest tuple: each leading position the source must hold,
			// then its rest, element by element.
			out := yes
			step := func(a, b *Type) bool {
				r := c.relate(a, b, rel, depth+1)
				out = all(out, r)
				return r != no
			}
			switch {
			case s.Kind == Array && s.Head != nil:
				if len(s.Head) < len(t.Head) {
					return no
				}
				for i, h := range s.Head {
					if !step(h, t.elemAt(i)) {
						return no
					}
				}
				if !step(s.RestElem, t.RestElem) {
					return no
				}
				return out
			case s.Kind == Tuple:
				if s.minLen() < len(t.Head) {
					return no
				}
				for i, e := range s.Elems {
					if !step(e, t.elemAt(i)) {
						return no
					}
				}
				return out
			case s.Kind == Array, s.Kind == Function:
				return no // an array may hold fewer elements than the head
			}
			return maybe
		}
		switch s.Kind {
		case Array:
			return c.relate(s.Elem, t.Elem, rel, depth+1)
		case Tuple:
			out := yes
			for _, e := range s.Elems {
				if r := c.relate(e, t.Elem, rel, depth+1); r == no {
					return no
				} else {
					out = all(out, r)
				}
			}
			return out
		case Function:
			return no
		case Interface, Instance:
			// A ReadonlyArray (`readonly T[]`, a TemplateStringsArray) has
			// no `push`: it is not a mutable array (TS4104, TS2740).
			if len(s.Props) > 0 && s.Prop("push") == nil && s.Prop("length") != nil {
				return no
			}
			// A class or interface without `length` (a Map, a Set) is no
			// array either.
			if len(s.Props) > 0 && s.Prop("length") == nil && (!s.partial || c.libLacks(s, "length")) {
				return no
			}
		case Anonymous:
			if s.Prop("length") == nil {
				return no // an object literal type without one is no array
			}
		}
		return maybe // an object type may still carry every array member
	case Tuple:
		switch s.Kind {
		case Tuple:
			if len(s.Elems) > len(t.Elems) || s.minLen() < t.minLen() {
				return no
			}
			out := yes
			for i := range s.Elems {
				r := c.relate(s.Elems[i], t.Elems[i], rel, depth+1)
				if r == no {
					return no
				}
				out = all(out, r)
			}
			return out
		case Array, Function:
			return no
		case Interface, Instance:
			// As for an array target: a class or interface without
			// `length` (a Generator, a Set), or a readonly one without
			// `push`, is no tuple.
			if len(s.Props) > 0 && s.Prop("length") == nil && (!s.partial || c.libLacks(s, "length")) {
				return no
			}
			if len(s.Props) > 0 && s.Prop("push") == nil && s.Prop("length") != nil {
				return no
			}
		case Anonymous:
			if s.Prop("length") == nil || s.Prop("push") == nil {
				return no // an object literal type without them is no tuple
			}
		}
		return maybe
	case Function:
		if s.Kind != Function {
			// A class value against a constructor type (`new (n?: string)
			// => A`, which the checker holds as a function type): its
			// construct signature must fit. A mismatch is no either way,
			// since a class value fits no call signature.
			if (s.Kind == Anonymous || s.Kind == Interface) && len(s.Calls) == 0 && len(s.Constructs) == 1 {
				if c.relate(s.Constructs[0], t, rel, depth+1) == no {
					return no
				}
			}
			return maybe // a callable object type: not modelled yet
		}
		// A type guard is assignable only to a signature it guards alike: a
		// function returning a plain boolean is not `(x) => x is T`.
		if tp := t.Predicate; tp != nil && !tp.Asserts && len(s.Overloads) == 0 && len(t.Overloads) == 0 {
			sp := s.Predicate
			if sp == nil || sp.Asserts || sp.Index != tp.Index {
				return no
			}
			if sp.Type != nil && tp.Type != nil && !c.generic(tp.Type) && c.relate(sp.Type, tp.Type, rel, depth+1) == no {
				return no
			}
		}
		if len(s.Overloads) > 0 && len(t.Overloads) == 0 && len(t.TypeParams) == 0 {
			// An overloaded source fits when one of its signatures does
			// (tsc's signaturesRelatedTo).
			out := no
			for _, sig := range s.Overloads {
				switch c.relate(sig, t, rel, depth+1) {
				case yes:
					return yes
				case maybe:
					out = maybe
				}
			}
			return out
		}
		if len(s.Overloads) == 0 && len(t.Overloads) == 0 && len(s.TypeParams) > 0 && len(s.TypeParams) == len(t.TypeParams) {
			// Two generic signatures: the source's type parameters as the
			// target's (tsc's compareSignaturesRelated over the target's
			// canonical signature), then related as plain ones.
			m := map[*Type]*Type{}
			for i, tp := range s.TypeParams {
				m[tp] = t.TypeParams[i]
			}
			return c.relate(c.plainSignature(s, m), c.plainSignature(t, nil), rel, depth+1)
		}
		if len(s.Overloads) > 0 || len(t.Overloads) > 0 || len(s.TypeParams) > 0 || len(t.TypeParams) > 0 {
			return maybe
		}
		// A rest parameter of a fixed tuple type is that many parameters
		// (`(...args: []) => void` takes none).
		if s.restParam && len(s.TypeParams) == 0 {
			s = c.expandTupleRest(s)
		}
		if t.restParam && len(t.TypeParams) == 0 {
			t = c.expandTupleRest(t)
		}
		if s.restParam || t.restParam {
			return c.relateRestSignatures(s, t, rel, depth)
		}
		for _, p := range s.Params {
			if p.Flags&Void != 0 {
				return maybe // a void parameter may be omitted
			}
		}
		// A source may take fewer parameters, not more required ones.
		if s.MinArgs() > len(t.Params) && !t.restParam {
			return no
		}
		out := yes
		bivariant := rel.bivariant || t.method || c.bivariantArgs > 0
		rel.bivariant = false
		for i := range s.Params {
			if i >= len(t.Params) {
				break
			}
			// A function type's parameters are contravariant
			// (strictFunctionTypes); a method's are compared both ways.
			if r := c.relate(t.Params[i], s.Params[i], rel, depth+1); r != yes {
				back := c.relate(s.Params[i], t.Params[i], rel, depth+1)
				if r == no && (!bivariant || back == no) {
					return no
				}
				out = all(out, maybe)
			}
		}
		if t.Ret().Flags&(Void|Any) != 0 {
			return out // the source's result unread, as tsc's compareSignaturesRelated
		}
		return all(out, c.relate(s.Ret(), t.Ret(), rel, depth+1))
	case Anonymous, Interface, Instance:
		if s.Kind == Instance && t.Kind == Instance && s.Derives(t.Symbol) && !t.refined {
			return yes
		}
		if s.Kind == Function || s.Kind == Array || s.Kind == Tuple {
			if t.StringIndex != nil && t.StringIndex.Flags&Any == 0 {
				// Neither has a string index signature (an array's index is
				// numeric only): TS2322, "Index signature for type 'string'
				// is missing".
				return no
			}
			if ti := t.NumberIndex; ti != nil && ti.Flags&Any == 0 && s.Kind != Function {
				// The elements are what the target's number index reads.
				elems := s.Elems
				if s.Kind == Array {
					elems = []*Type{s.Elem}
				}
				for _, el := range elems {
					if el != nil && !widenedAgainstLiterals(el, ti) && c.relate(el, ti, rel, depth+1) == no {
						return no
					}
				}
			}
			if s.Kind != Function && t.Kind == Interface && t.Symbol != nil && len(t.TypeArgs) >= 1 &&
				(t.Symbol.Name == "Iterable" || t.Symbol.Name == "IterableIterator" || t.Symbol.Name == "ArrayLike") {
				// What iterating (or indexing) the array yields.
				elems := s.Elems
				if s.Kind == Array {
					elems = []*Type{s.Elem}
				}
				for _, el := range elems {
					if el != nil && !widenedAgainstLiterals(el, t.TypeArgs[0]) && c.relate(el, t.TypeArgs[0], rel, depth+1) == no {
						return no
					}
				}
			}
			if len(t.Props) == 0 {
				return yes
			}
			if s.Kind != Function {
				// An array has its apparent type's members (Array<T>'s): a
				// required property it lacks is a mismatch.
				if at := c.apparentType(s); at != nil {
					for _, tp := range t.Props {
						// A tuple's elements are its numeric members ("0", "1").
						if i, err := strconv.Atoi(tp.Name); err == nil && i >= 0 && strconv.Itoa(i) == tp.Name {
							var el *Type
							switch {
							case s.Kind == Tuple && i < len(s.Elems):
								el = s.Elems[i]
							case s.Kind == Array && i < len(s.Head):
								el = s.Head[i]
							}
							if el != nil {
								if c.relate(el, tp.Type, rel, depth+1) == no {
									return no
								}
								continue
							}
						}
						// Object.prototype's members are every array's too.
						if !tp.Optional && at.Prop(tp.Name) == nil && c.objectMember(tp.Name) == nil {
							return no
						}
					}
				}
			}
			return maybe // an array's or a function's apparent members
		}
		// `Iterable<T>`: the source must iterate (a structural iterator
		// counts, as iteratedType reads it).
		if t.Kind == Interface && t.Symbol != nil && len(t.TypeArgs) >= 1 && c.inLibrary(t.Symbol.Scope) &&
			(t.Symbol.Name == "Iterable" || t.Symbol.Name == "IterableIterator") && rel.missingIsNo &&
			(s.Kind == Instance || s.Kind == Anonymous) && s.Symbol != nil && !c.inLibrary(s.Symbol.Scope) {
			if c.Unanswered(c.iteratedType(s)) {
				return no
			}
		}
		if t.Kind == Instance {
			// A class with private or protected members is nominal; the
			// checker does not record visibility yet.
			if rel.missingIsNo {
				return c.propsFit(s, t, rel, depth)
			}
			return maybe
		}
		r := c.propsFit(s, t, rel, depth)
		if r == no {
			return no
		}
		// A target's construct signature (`typeof A`) needs a source
		// construct signature that fits it.
		if len(t.Constructs) == 1 && len(s.Constructs) == 1 && (s.Kind == Anonymous || s.Kind == Interface) {
			// A class constructor's parameters are bivariant, as a
			// method's are (strictFunctionTypes exempts both).
			crel := rel
			crel.bivariant = crel.bivariant || classDeclared(t)
			cr := c.relate(s.Constructs[0], t.Constructs[0], crel, depth+1)
			if cr == no {
				return no
			}
			r = all(r, cr) // a construct signature that only may fit: so may the value
		}
		return all(r, c.indexesFit(s, t, rel, depth))
	}
	return maybe
}

// libInterfaceKnown reports whether t is a builtin interface TypeScript's
// library declares (lib/tsmembers.txt): such an interface, partial as its
// declaration here is, has no implicit index signature either.
func (c *Checker) libInterfaceKnown(t *Type) bool {
	if t.Symbol == nil || t.Kind != Interface || !c.inLibrary(t.Symbol.Scope) {
		return false
	}
	known, _ := lib.TSInterfaceHas(sourceName(t.Symbol.Name), "")
	return known
}

// indexesFit reports whether s satisfies t's index signatures
// (indexSignaturesRelatedTo): through its own of the kind (a string index
// also serves a number one), or, for an object literal or type literal
// only (an inferable index), through every property the signature covers.
// An interface or class instance without one does not fit (TS2322, "Index
// signature for type 'string' is missing").
func (c *Checker) indexesFit(s, t *Type, rel relation, depth int) ternary {
	if t.StringIndex != nil && t.StringIndex.Flags&Any != 0 {
		return yes // `[k: string]: any` takes any object
	}
	out := yes
	for _, kind := range []string{"string", "number"} {
		ti := t.StringIndex
		if kind == "number" {
			ti = t.NumberIndex
		}
		if ti == nil {
			continue
		}
		si := s.StringIndex
		if kind == "number" && s.NumberIndex != nil {
			si = s.NumberIndex
		}
		if si != nil {
			r := c.relate(si, ti, rel, depth+1)
			if r == no {
				return no
			}
			out = all(out, r)
			continue
		}
		if s.partial && !c.libInterfaceKnown(s) || s.Kind != Anonymous && s.Kind != Interface && s.Kind != Instance {
			return maybe
		}
		if s.Kind != Anonymous {
			return no // an interface or class has no implicit index signature
		}
		for _, p := range s.Props {
			if kind == "number" && !numericName(p.Name) {
				continue
			}
			pt := p.Type
			if p.Optional {
				// tsc's getNonMissingTypeOfSymbol: without
				// exactOptionalPropertyTypes the missing type is undefined,
				// so an optional property's undefined is not related.
				pt = c.NonUndefined(c.withoutMissing(pt))
				if pt.Flags&Never != 0 {
					continue
				}
			}
			r := c.relate(pt, ti, rel, depth+1)
			if r == no {
				return no
			}
			out = all(out, r)
		}
	}
	return out
}

// numericName reports whether a property name is a numeric literal's.
func numericName(name string) bool {
	_, err := strconv.ParseFloat(name, 64)
	return err == nil
}

// withoutMissing is t without the missing type an optional property adds.
func (c *Checker) withoutMissing(t *Type) *Type {
	return c.filter(t, func(m *Type) bool { return m != c.missingT })
}

// propsFit reports whether s has every property t requires, each
// assignable.
func (c *Checker) propsFit(s, t *Type, rel relation, depth int) ternary {
	out := yes
	for _, tp := range t.Props {
		sp := s.Prop(tp.Name)
		if sp == nil && (len(s.Calls) > 0 || len(s.Constructs) > 0) {
			if ft := c.functionMember(tp.Name); ft != nil {
				sp = &Property{Name: tp.Name, Type: ft} // a callable object has Function's
			}
		}
		if sp == nil {
			sp = c.objectMember(tp.Name) // every object has Object.prototype's
		}
		if sp == nil {
			if tp.Optional {
				continue
			}
			if s.partial && !c.libLacks(s, tp.Name) {
				// A builtin declaration lists only what this compiler
				// implements: TypeScript's may still have the member.
				out = all(out, maybe)
				continue
			}
			if rel.missingIsNo {
				return no
			}
			return maybe // a missing property: TS2741, not reported yet
		}
		prel := rel
		prel.bivariant = methodDecl(tp)
		r := c.relate(sp.Type, tp.Type, prel, depth+1)
		if r == no {
			return no
		}
		out = all(out, r)
	}
	return out
}

// methodDecl reports a member declared as a method (a method signature, a
// class's method): its parameters are bivariant.
func methodDecl(p *Property) bool {
	switch d := p.Decl.(type) {
	case *ast.MethodSignature:
		return true
	case *ast.FunctionDeclaration:
		return d.AccessorKind == "" && !p.Field
	}
	return false
}

// objectMember is the member of the Object interface named name that every
// object has through Object.prototype (`toString`), or nil.
func (c *Checker) objectMember(name string) *Property {
	if !objectProto[name] || name == "__proto__" || c.b.Globals == nil {
		return nil
	}
	sym := c.b.Globals.Symbols.Get("Object")
	if sym == nil || sym.Flags&binder.Interface == 0 {
		return nil
	}
	ot := c.interfaceOf(sym, nil)
	if c.Unanswered(ot) {
		return nil
	}
	return ot.Prop(name)
}

// weakType reports whether t is an object type whose properties are all
// optional, with at least one and no call or construct signatures.
func (c *Checker) weakType(t *Type) bool {
	if t.Flags&Object == 0 || (t.Kind != Anonymous && t.Kind != Interface) || t.partial || len(t.Props) == 0 || len(t.Calls) > 0 || len(t.Constructs) > 0 {
		return false
	}
	for _, p := range t.Props {
		if !p.Optional {
			return false
		}
	}
	return true
}

// sharesProp reports whether s has a property t declares.
func sharesProp(s, t *Type) bool {
	for _, p := range t.Props {
		if s.Prop(p.Name) != nil {
			return true
		}
	}
	return false
}

// apparentMembers are the member names of TypeScript's own apparent types
// (lib.es5 through esnext) and of Object, which each inherits: the
// library's declarations list only what this compiler implements.
var apparentMembers = map[string]map[string]bool{
	"Number":  setOf("toString", "toFixed", "toExponential", "toPrecision", "valueOf", "toLocaleString"),
	"Boolean": setOf("valueOf"),
	"Symbol":  setOf("toString", "valueOf", "description"),
	"String": setOf("toString", "charAt", "charCodeAt", "concat", "indexOf", "lastIndexOf", "localeCompare",
		"match", "replace", "search", "slice", "split", "substring", "toLowerCase", "toLocaleLowerCase",
		"toUpperCase", "toLocaleUpperCase", "trim", "length", "substr", "valueOf", "codePointAt", "includes",
		"endsWith", "normalize", "repeat", "startsWith", "anchor", "big", "blink", "bold", "fixed", "fontcolor",
		"fontsize", "italics", "link", "small", "strike", "sub", "sup", "padStart", "padEnd", "trimEnd",
		"trimStart", "trimLeft", "trimRight", "matchAll", "replaceAll", "at", "isWellFormed", "toWellFormed"),
	"Object": setOf("constructor", "toString", "toLocaleString", "valueOf", "hasOwnProperty", "isPrototypeOf",
		"propertyIsEnumerable"),
}

func setOf(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// primitiveLacksLibraryMember reports a primitive s going to an object type
// t that requires a member s's apparent type does not have in TypeScript:
// 42 is not a Buffer, "buffer" is not a `{ encoding: string }`. A
// symbol-keyed member is left to the apparent type's declaration.
func (c *Checker) primitiveLacksLibraryMember(s, t *Type) bool {
	at := c.apparentType(s)
	if at == nil || at.Symbol == nil || (t.Symbol != nil && at.Symbol == t.Symbol) {
		return false
	}
	own := apparentMembers[at.Symbol.Name]
	if own == nil {
		return false
	}
	for _, p := range t.Props {
		if p.Optional || own[p.Name] || apparentMembers["Object"][p.Name] || at.Prop(p.Name) != nil || strings.HasPrefix(p.Name, "__@") || strings.HasPrefix(p.Name, "[") {
			continue
		}
		return true
	}
	return false
}

// restElem is the element type a rest parameter of type t binds each
// argument to (`...args: any` binds any).
func (c *Checker) restElem(t *Type) *Type {
	switch {
	case t.Flags&(Any|Unknown) != 0:
		return t
	case t.Flags&Object != 0 && t.Kind == Array:
		return t.Elem
	}
	return nil
}

// relateRestSignatures relates two signatures either of which ends in a
// rest parameter: each position s reads takes t's parameter there (t's
// rest element past its fixed ones), and the results relate.
func (c *Checker) relateRestSignatures(s, t *Type, rel relation, depth int) ternary {
	fixed := func(f *Type) int {
		if f.restParam {
			return len(f.Params) - 1
		}
		return len(f.Params)
	}
	var sRest, tRest *Type
	if s.restParam {
		if sRest = c.restElem(s.Params[len(s.Params)-1]); sRest == nil {
			return maybe // a tuple-typed rest
		}
	}
	if t.restParam {
		if tRest = c.restElem(t.Params[len(t.Params)-1]); tRest == nil {
			return maybe
		}
	}
	at := func(f *Type, rest *Type, i int) *Type {
		if i < fixed(f) {
			return f.Params[i]
		}
		return rest
	}
	n := fixed(s)
	if fixed(t) > n {
		n = fixed(t)
	}
	out := yes
	for i := 0; i < n; i++ {
		sp, tp := at(s, sRest, i), at(t, tRest, i)
		if sp == nil || tp == nil {
			if sp != nil && !(i < len(s.optionals) && s.optionals[i]) && tp == nil {
				return no // s requires a parameter t never passes
			}
			continue
		}
		if r := c.relate(tp, sp, rel, depth+1); r != yes {
			if c.relate(sp, tp, rel, depth+1) == no && r == no {
				return no
			}
			out = all(out, maybe)
		}
	}
	if sRest != nil && tRest != nil {
		if r := c.relate(tRest, sRest, rel, depth+1); r != yes {
			if c.relate(sRest, tRest, rel, depth+1) == no && r == no {
				return no
			}
			out = all(out, maybe)
		}
	}
	if t.Ret().Flags&(Void|Any) != 0 {
		return out // the source's result unread, as tsc's compareSignaturesRelated
	}
	return all(out, c.relate(s.Ret(), t.Ret(), rel, depth+1))
}

// unionHasArray reports whether a union has an array or tuple member.
func unionHasArray(t *Type) bool {
	for _, m := range t.Types {
		if m.Flags&Object != 0 && (m.Kind == Array || m.Kind == Tuple) {
			return true
		}
	}
	return false
}

// widenedAgainstLiterals reports whether el is a widened primitive (an
// array literal's `string` element) and t holds literal types: TypeScript
// types the literal against them contextually, which an array's widened
// type no longer shows.
func widenedAgainstLiterals(el, t *Type) bool {
	if el.Flags&(String|Number|Boolean) == 0 || el.Flags&Literal != 0 {
		return false
	}
	if t.Flags&Literal != 0 {
		return true
	}
	for _, m := range t.Types {
		if m.Flags&Literal != 0 {
			return true
		}
	}
	return false
}

// recursionIdentity is what a generic type's instantiations share: an
// interface's symbol, or an anonymous object type's member names (an
// alias's expansion); empty for any other type.
func recursionIdentity(t *Type) string {
	switch {
	case t.Kind == Interface && t.Symbol != nil && len(t.TypeArgs) > 0:
		return "S" + strconv.Itoa(t.Symbol.ID)
	case t.Kind == Anonymous && len(t.Props) > 0 && t.ClassOf == nil:
		names := make([]string, len(t.Props))
		for i, p := range t.Props {
			names[i] = p.Name
		}
		return "A" + strings.Join(names, ",")
	}
	return ""
}

// classDeclared reports a class's value type (`typeof A`), whose
// construct signature a constructor declaration declares.
func classDeclared(t *Type) bool { return t.ClassOf != nil }

// plainSignature is generic signature sig with m applied and no type
// parameters of its own.
func (c *Checker) plainSignature(sig *Type, m map[*Type]*Type) *Type {
	params := make([]*Type, len(sig.Params))
	for i, p := range sig.Params {
		params[i] = c.instantiate(p, m)
	}
	var pred *Predicate
	if sig.Predicate != nil {
		pc := *sig.Predicate
		if pc.Type != nil {
			pc.Type = c.instantiate(pc.Type, m)
		}
		pred = &pc
	}
	return c.in.function(params, sig.optionals, sig.restParam, c.instantiate(sig.Ret(), m), pred, nil)
}

// relateConditionals relates two deferred conditional types as tsc does:
// `S1 extends U1 ? X1 : Y1` to `S2 extends U2 ? X2 : Y2` when one check type
// relates to the other, U1 and U2 are identical (interned alike), and the
// branches relate; maybe for any other deferred pair.
func (c *Checker) relateConditionals(s, t *Type, rel relation, depth int) ternary {
	sn, ok1 := s.deferNode.(*ast.ConditionalType)
	tn, ok2 := t.deferNode.(*ast.ConditionalType)
	if !ok1 || !ok2 {
		return maybe
	}
	if hasInferNode(sn.ExtendsType) || hasInferNode(tn.ExtendsType) {
		return maybe
	}
	sc, se, sx, sy := c.deferredPart(s, sn.CheckType), c.deferredPart(s, sn.ExtendsType), c.deferredPart(s, sn.TrueType), c.deferredPart(s, sn.FalseType)
	tc, te, tx, ty := c.deferredPart(t, tn.CheckType), c.deferredPart(t, tn.ExtendsType), c.deferredPart(t, tn.TrueType), c.deferredPart(t, tn.FalseType)
	for _, x := range []*Type{sc, se, sx, sy, tc, te, tx, ty} {
		if c.Unanswered(x) {
			return maybe
		}
	}
	// Both branches going to the target take the conditional there (tsc's
	// default constraint of a source conditional).
	if c.relate(sx, t, rel, depth+1) == yes && c.relate(sy, t, rel, depth+1) == yes {
		return yes
	}
	if se != te {
		if c.generic(se) || c.generic(te) {
			return maybe
		}
		return no
	}
	if sc != tc && c.relate(sc, tc, rel, depth+1) != yes && c.relate(tc, sc, rel, depth+1) != yes {
		return maybe
	}
	return all(c.relate(sx, tx, rel, depth+1), c.relate(sy, ty, rel, depth+1))
}

// deferredPart types part, a node inside deferred type t's own, with t's
// bindings.
func (c *Checker) deferredPart(t *Type, part ast.TypeNode) *Type {
	savedEnv, savedSubsts := c.env, c.substs
	c.env, c.substs = []map[string]*Type{t.deferEnv}, []map[*Type]*Type{t.deferSubst}
	r := c.typeFromNode(part, t.deferScope)
	c.env, c.substs = savedEnv, savedSubsts
	return r
}

// hasInferNode reports an `infer` declaration in n.
func hasInferNode(n ast.TypeNode) bool {
	found := false
	collectInfers(n, func(*ast.InferType) { found = true })
	return found
}

// unionHolds reports whether union t has m itself as a member.
func (c *Checker) unionHolds(t, m *Type) bool {
	if t.Flags&Union == 0 {
		return false
	}
	for _, x := range t.Types {
		if x == m {
			return true
		}
	}
	return false
}

// relateConditionalSource relates a deferred conditional source to a
// concrete target through its branches (tsc's default constraint of a
// conditional type, the union of its branches), when neither branch
// mentions a type parameter; otherwise maybe.
func (c *Checker) relateConditionalSource(s, t *Type, rel relation, depth int) ternary {
	sn, ok := s.deferNode.(*ast.ConditionalType)
	if !ok || hasInferNode(sn.ExtendsType) {
		if _, isCond := s.deferNode.(*ast.ConditionalType); !isCond {
			if b := c.baseConstraint(s, 0); b != nil {
				return c.relate(b, t, rel, depth+1)
			}
		}
		return maybe
	}
	if d := c.distributiveConstraint(s, sn); d != nil && c.relate(d, t, rel, depth+1) == yes {
		// (tsc relates it first, then falls back to the branches)
		return yes
	}
	x, y := c.deferredPart(s, sn.TrueType), c.deferredPart(s, sn.FalseType)
	if c.Unanswered(x) || c.Unanswered(y) || hasTypeParam(x) || hasTypeParam(y) || x.Flags&Deferred != 0 || y.Flags&Deferred != 0 {
		// A generic source goes where its constraint goes (tsc's
		// getBaseConstraintOfType).
		if b := c.baseConstraint(s, 0); b != nil {
			return c.relate(b, t, rel, depth+1)
		}
		return maybe
	}
	return c.relate(c.in.union(x, y), t, rel, depth+1)
}

// distributiveConstraint is distributive conditional t of its check
// type's constraint (`IsArray<T>`, `T extends unknown[]`, is at most
// `true`) when that is concrete: tsc's
// getConstraintOfDistributiveConditionalType. Nil otherwise.
func (c *Checker) distributiveConstraint(t *Type, n *ast.ConditionalType) *Type {
	if _, naked := n.CheckType.(*ast.TypeReference); !naked {
		return nil
	}
	ct := c.deferredPart(t, n.CheckType)
	if ct.Flags&TypeParam == 0 || ct.Flags&Union != 0 || ct.Constraint == nil || c.Unanswered(ct.Constraint) || c.generic(ct.Constraint) {
		return nil
	}
	if r := c.instantiate(t, map[*Type]*Type{ct: ct.Constraint}); r != nil && !c.Unanswered(r) && !c.generic(r) {
		return r
	}
	return nil
}

// baseConstraint is the concrete type generic t is at most: a type
// parameter's constraint (unknown without one), `keyof` any type's
// `string | number | symbol`, a conditional's branches' (its check type's in
// the true branch narrowed to the extends type), an unconstrained object's
// indexed access unknown. Nil when not known so.
func (c *Checker) baseConstraint(t *Type, depth int) *Type {
	if depth > 8 || t == nil || c.Unanswered(t) {
		return nil
	}
	if !c.generic(t) {
		return t
	}
	switch {
	case t.Flags&Union != 0:
		var ms []*Type
		for _, m := range t.Types {
			b := c.baseConstraint(m, depth+1)
			if b == nil {
				return nil
			}
			ms = append(ms, b)
		}
		return c.in.union(ms...)
	case t.Flags&TypeParam != 0:
		if t == c.thisT {
			return nil
		}
		if t.Constraint == nil {
			return c.unknownT
		}
		return c.baseConstraint(t.Constraint, depth+1)
	case t.Flags&Deferred == 0:
		return nil
	}
	switch n := t.deferNode.(type) {
	case *ast.TypeOperator:
		if n.Operator == "keyof" {
			return c.in.union(c.strT, c.numT, c.symT)
		}
	case *ast.TupleType:
		// A variadic tuple (`[...A, L]`): an array, whatever is spread in.
		return c.in.array(c.unknownT)
	case *ast.IndexedAccessType:
		if o := c.deferredPart(t, n.ObjectType); o.Flags&TypeParam != 0 && o.Constraint == nil && o != c.thisT {
			return c.unknownT
		}
	case *ast.ConditionalType:
		if r := c.distributiveConstraint(t, n); r != nil {
			return r
		}
		if hasInferNode(n.ExtendsType) {
			return nil
		}
		x := c.baseConstraint(c.deferredPart(t, n.TrueType), depth+1)
		y := c.baseConstraint(c.deferredPart(t, n.FalseType), depth+1)
		if x == nil || y == nil {
			return nil
		}
		if ct, tt := c.deferredPart(t, n.CheckType), c.deferredPart(t, n.TrueType); ct == tt {
			// `T extends U ? T : …`: what of T's constraint is a U.
			u := c.deferredPart(t, n.ExtendsType)
			if c.Unanswered(u) || c.generic(u) {
				return nil
			}
			var kept []*Type
			for _, m := range members(x) {
				if c.assignableTo(m, u) == yes {
					kept = append(kept, m)
				}
			}
			if len(kept) == 0 {
				x = c.neverT
			} else {
				x = c.in.union(kept...)
			}
		}
		return c.in.union(x, y)
	}
	return nil
}

package checker

import (
	"strconv"

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
}

// assignableTo is TypeScript's assignability of s to t, under --strict, for
// the types the checker models: primitives and their literals, the native
// widths, null and undefined, unions, arrays, tuples, functions and object
// shapes. Anything else is maybe.
func (c *Checker) assignableTo(s, t *Type) ternary {
	return c.relate(s, t, relation{}, 0)
}

func (c *Checker) relate(s, t *Type, rel relation, depth int) ternary {
	switch {
	case s == nil || t == nil || c.Unanswered(s) || c.Unanswered(t) || depth > 8:
		return maybe
	case s == t:
		return yes
	case t.Flags&(Any|Unknown) != 0, s.Flags&(Any|Never) != 0:
		return yes
	case s.Flags&TypeParam != 0 && s.Constraint != nil && t.Flags&TypeParam == 0:
		// A constrained parameter goes where its constraint goes.
		if c.relate(s.Constraint, t, rel, depth+1) == yes {
			return yes
		}
		return maybe
	case s.Flags&TypeParam != 0 || t.Flags&TypeParam != 0:
		return maybe
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
		out := no
		for _, m := range t.Types {
			switch c.relate(s, m, rel, depth+1) {
			case yes:
				return yes
			case maybe:
				out = maybe
			}
		}
		if out == no && s.Flags&Object != 0 {
			// An object against a union of objects: discriminated-union rules.
			// With one object member (`T | null | undefined`) its no stands.
			objects := 0
			for _, m := range t.Types {
				if m.Flags&Object != 0 {
					objects++
				}
			}
			if objects > 1 {
				return maybe
			}
		}
		return out
	case s.Flags&Unknown != 0:
		return no // unknown goes only to any and unknown
	}
	if s.Member != "" || t.Member != "" {
		return maybe // enums: not modelled here yet
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

// relateObjects relates two object types structurally, as TypeScript does.
func (c *Checker) relateObjects(s, t *Type, rel relation, depth int) ternary {
	switch t.Kind {
	case Array:
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
		}
		return maybe // an object type may still carry every array member
	case Tuple:
		switch s.Kind {
		case Tuple:
			if len(s.Elems) != len(t.Elems) {
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
		}
		return maybe
	case Function:
		if s.Kind != Function {
			return maybe // a callable object type: not modelled yet
		}
		// A type guard is assignable only to a signature it guards alike: a
		// function returning a plain boolean is not `(x) => x is T`.
		if tp := t.Predicate; tp != nil && !tp.Asserts && len(s.Overloads) == 0 && len(t.Overloads) == 0 {
			sp := s.Predicate
			if sp == nil || sp.Asserts || sp.Index != tp.Index {
				return no
			}
			if sp.Type != nil && tp.Type != nil && c.relate(sp.Type, tp.Type, rel, depth+1) == no {
				return no
			}
		}
		if len(s.Overloads) > 0 || len(t.Overloads) > 0 || len(s.TypeParams) > 0 || len(t.TypeParams) > 0 || s.restParam || t.restParam {
			return maybe // rest parameters match any number of positions
		}
		for _, p := range s.Params {
			if p.Flags&Void != 0 {
				return maybe // a void parameter may be omitted
			}
		}
		// A source may take fewer parameters, not more required ones.
		required := 0
		for i := range s.Params {
			if !(i < len(s.optionals) && s.optionals[i]) && !(s.restParam && i == len(s.Params)-1) {
				required = i + 1
			}
		}
		if required > len(t.Params) && !t.restParam {
			return no
		}
		out := yes
		for i := range s.Params {
			if i >= len(t.Params) {
				break
			}
			// Parameters are compared both ways (strictFunctionTypes holds
			// for function types; method parameters are bivariant).
			if r := c.relate(t.Params[i], s.Params[i], rel, depth+1); r != yes {
				if c.relate(s.Params[i], t.Params[i], rel, depth+1) == no && r == no {
					return no
				}
				out = all(out, maybe)
			}
		}
		if t.Result.Flags&Void != 0 {
			return out
		}
		return all(out, c.relate(s.Result, t.Result, rel, depth+1))
	case Anonymous, Interface, Instance:
		if s.Kind == Instance && t.Kind == Instance && s.Derives(t.Symbol) {
			return yes
		}
		if s.Kind == Function || s.Kind == Array || s.Kind == Tuple {
			if len(t.Props) == 0 {
				return yes
			}
			return maybe // an array's or a function's apparent members
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
		return all(r, c.indexesFit(s, t, rel, depth))
	}
	return maybe
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
		if s.partial || s.Kind != Anonymous && s.Kind != Interface && s.Kind != Instance {
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
				pt = c.withoutMissing(pt)
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
		if sp == nil {
			sp = c.objectMember(tp.Name) // every object has Object.prototype's
		}
		if sp == nil {
			if tp.Optional {
				continue
			}
			if rel.missingIsNo {
				return no
			}
			return maybe // a missing property: TS2741, not reported yet
		}
		r := c.relate(sp.Type, tp.Type, rel, depth+1)
		if r == no {
			return no
		}
		out = all(out, r)
	}
	return out
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

// primitiveLacksLibraryMember reports a primitive s going to a library
// object type t (Buffer, URL, Promise, …) that requires a member s's
// apparent type does not have in TypeScript: 42 is not a Buffer.
func (c *Checker) primitiveLacksLibraryMember(s, t *Type) bool {
	if t.Symbol == nil || !c.inLibrary(t.Symbol.Scope) || t.Kind == Anonymous {
		return false
	}
	at := c.apparentType(s)
	if at == nil || at.Symbol == nil || at.Symbol == t.Symbol {
		return false
	}
	own := apparentMembers[at.Symbol.Name]
	if own == nil {
		return false
	}
	for _, p := range t.Props {
		if p.Optional || own[p.Name] || apparentMembers["Object"][p.Name] || at.Prop(p.Name) != nil {
			continue
		}
		return true
	}
	return false
}

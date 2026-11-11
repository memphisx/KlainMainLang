// Package checker computes the type of every expression (TDD-00230 P2.3,
// P2.4). It reads the binder's symbols and never changes the AST: every
// answer is memoised in a side table, and each expression is checked once.
package checker

import (
	"KlainMainLang/ast"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"KlainMainLang/binder"
)

// TypeFlags is a type's kind: one bit per kind.
type TypeFlags uint32

const (
	Any TypeFlags = 1 << iota
	Unknown
	String
	Number
	Boolean
	BigInt
	Void
	Undefined
	Null
	Never
	StringLiteral
	NumberLiteral
	BooleanLiteral
	BigIntLiteral
	// Int is a native integer of a declared width (`/** @type {int32} */`);
	// Float32 a single-precision float. Neither exists in TypeScript: they
	// are this compiler's JSDoc widths, and first-class kinds here.
	Int
	Float32
	Object
	Union
	// TypeParam is a generic function's type parameter (Symbol), with its
	// Constraint.
	TypeParam
	// NonPrimitive is TypeScript's `object`: any value that is not a
	// primitive, null or undefined.
	NonPrimitive
	// ESSymbol is `symbol`.
	ESSymbol
	// Deferred is a type operator over a type parameter (`keyof T`, a
	// mapped or conditional type), evaluated once instantiated.
	Deferred

	Literal     = StringLiteral | NumberLiteral | BooleanLiteral | BigIntLiteral
	StringLike  = String | StringLiteral
	NumberLike  = Number | NumberLiteral | Int | Float32
	BooleanLike = Boolean | BooleanLiteral
	BigIntLike  = BigInt | BigIntLiteral
	Nullish     = Undefined | Null | Void
)

// ObjectKind distinguishes the object types.
type ObjectKind int

const (
	Anonymous ObjectKind = iota // an object literal or type literal
	Array
	Tuple
	Function
	Instance // an instance of a class
	Interface
)

// Type is one type. Types are interned by the checker: two equal types are
// the same *Type, so equality is pointer equality.
type Type struct {
	Flags TypeFlags
	ID    int
	// Value is a literal type's value ("1.5", "north", "true", "10n").
	Value string
	// Bits and Signed describe an Int.
	Bits   int
	Signed bool
	// AnyArith marks the number an arithmetic operator on two `any`
	// operands yields: TypeScript types it number, but at run time it is a
	// bigint when both operands hold one, so it is held as `any`.
	AnyArith bool
	// Types are a union's members, ordered by ID.
	Types []*Type
	// The object payload.
	Kind  ObjectKind
	Elem  *Type   // Array
	Elems []*Type // Tuple
	// Head and RestElem make an Array a tuple with a trailing rest
	// element (`[string, ...number[]]`): its fixed leading elements, then
	// any number of RestElem. Elem is the union of them all, so a reader
	// that knows only arrays sees an array of that union.
	Head     []*Type
	RestElem *Type
	Props    []*Property // Anonymous, Instance, Interface
	Params   []*Type     // Function
	result   *Type       // Function: read through Ret
	// lazyResult computes a deferred result on its first read (a function
	// expression's return type, which TypeScript resolves when asked).
	lazyResult func() (*Type, bool)
	// cycleResult is a deferred result read while it is being computed
	// (`const g = () => g()`): tsc's any, TS7023 reported.
	cycleResult func() *Type
	computing   bool
	Symbol      *binder.Symbol // Instance, Interface: the declaring symbol
	ClassOf     *binder.Symbol // Anonymous: a class value's class (`typeof C`)
	// ParamNames are a Function's declared parameter names, for display only:
	// relations ignore them.
	ParamNames []string
	Base       *Type      // Instance: the base class's instance type
	TypeArgs   []*Type    // Instance, Interface: a generic declaration's arguments
	Predicate  *Predicate // Function: a `p is T` result
	TypeParams []*Type    // Function: a generic signature's type parameters
	Overloads  []*Type    // Function: an overloaded function's signatures, in order
	ThisType   *Type      // Function: a `this: T` parameter's T, or nil
	// Opt is a tuple's count of trailing optional elements (`[T?]`), each
	// held with undefined (`T | undefined`, as tsc's strict reading).
	Opt int
	// Labels are a labelled tuple's element names (`[x: number]`), or nil.
	Labels []string
	// ModuleNamespace marks a module's namespace object, built by the
	// resolver as a frozen literal of the module's exports.
	ModuleNamespace bool
	// ImportCall is the dynamic `import()` an object of a module's exports
	// is the result of (one compiled separately, whose layout is its own).
	ImportCall *ast.ImportCallExpression
	// Intersects are the object members an intersection's type merges.
	Intersects []*Type
	// Brand and BrandBase describe a branded primitive (`string & {
	// __brand: "id" }`, an intersection of a primitive with object types):
	// the type carries its primitive's flags, BrandBase is that primitive and
	// Brand the object members' merged type.
	Brand, BrandBase *Type
	// LiteralThis is an intersection's `ThisType<T>` marker's T: the
	// `this` of an object literal's methods contextually typed by it.
	LiteralThis *Type
	// StringIndex and NumberIndex are an object type's index signatures'
	// value types (`[k: string]: T`, `[i: number]: T`), or nil.
	StringIndex, NumberIndex *Type
	// An enum member is a number or string literal whose Symbol is its
	// enum, with its Member name and the enum's member count.
	Member     string
	EnumSize   int
	Constraint *Type  // TypeParam: its `extends` bound, or nil
	Default    *Type  // TypeParam: its `= T` default, or nil
	optionals  []bool // Function: optional parameters
	restParam  bool   // Function: the last parameter is a rest
	// method marks a method's signature (a method signature, a class
	// method): its parameters are bivariant (strictFunctionTypes exempts it).
	method bool
	// Calls and Constructs are an interface's call and construct
	// signatures (function types), in declaration order: `Number(x)`,
	// `new Map()`.
	Calls, Constructs []*Type
	// callOrigins and constructOrigins say where each signature was
	// declared, for ordering the candidates of a call (reorderCandidates).
	callOrigins, constructOrigins []sigOrigin
	// partial marks an object type with members the checker does not model
	// (a computed name such as `[Symbol.iterator]`): its member list is not
	// whole, so nothing is reported as missing from it or absent on it.
	partial bool
	// intersection marks an object type built from an intersection
	// (`A & B`): tsc reports a property missing from it as TS2322.
	intersection bool
	// A Deferred type's node, scope and the bindings in scope there: the
	// environment's names and the function type parameters' substitution.
	deferNode  ast.TypeNode
	deferScope *binder.Scope
	deferEnv   map[string]*Type
	deferSubst map[*Type]*Type
	// deferAlias and deferAliasArgs name the alias instance a deferred type
	// is the body of (`Extract<keyof T, string>`), for display.
	deferAlias     string
	deferAliasArgs []*Type
	// deferParams are the type parameters the deferred node names through
	// its scope rather than its env (`{ [K in C]: S[K] }` in a signature).
	deferParams []*Type
	// selfRef marks a stand-in that contains itself (a recursive alias's
	// `T | Box<R<T>>`); walking is set while a walker is inside it, so a
	// walk reaching it again stops.
	selfRef, walking bool
	// freeWalk is set while hasFreeTypeParam is inside the type, so one
	// holding itself is searched once.
	freeWalk bool
	// narrowOf is the type parameter a narrowed one stands for: `T &
	// string` (tsc's intersection a type guard gives a type parameter) is
	// T's copy constrained to string, related to T as itself.
	narrowOf *Type
	// refined marks a class instance an intersection added literal members
	// to (refinedInstance): no longer every instance of its class.
	refined bool
}

// Predicate is a function's type predicate: a true result means its
// parameter Index is a Type. An Asserts predicate holds once the call
// returns; its Type is nil for `asserts p` (p is truthy).
type Predicate struct {
	Index   int
	Type    *Type
	Asserts bool
}

// Property is a member of an object type.
type Property struct {
	Name     string
	Type     *Type
	Optional bool
	Accessor bool  // a class's get/set accessor
	Write    *Type // an accessor's setter type, when it differs from its getter's
	Readonly bool  // `readonly`: a constant reference's path through it is constant
	// Visibility is a class member's "private" or "protected" ("" when
	// public), and Owner the class that declares it.
	// WriteVisibility is an accessor pair's setter's, when it differs.
	Visibility, WriteVisibility string
	Owner                       *binder.Symbol
	Field                       bool // a class's instance field
	// Written marks a type literal's property signature (`{ a: T }`), as
	// against an object literal value's or a normalization's member.
	Written bool
	// Decl is the member's declaration (a method signature, a property
	// signature, a class member), when it has one; Decls are an overloaded
	// method's, one per overload in order.
	Decl  ast.Node
	Decls []ast.Node
}

// DeferredTypeParams are the type parameters a deferred type's evaluation
// waits on: those its type-parameter names are bound to, sorted by ID.
func (t *Type) DeferredTypeParams() []*Type {
	if t == nil || t.Flags&Deferred == 0 {
		return nil
	}
	var out []*Type
	seen := map[*Type]bool{}
	walked := map[*Type]bool{t: true}
	var add func(v *Type)
	var bindings func(d *Type)
	add = func(v *Type) {
		switch {
		case v == nil || walked[v]:
		case v.Flags&TypeParam != 0:
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		case v.Flags&Deferred != 0:
			// A binding to another deferred type (`C1<C2<T>>`'s argument):
			// the parameters that one reads.
			walked[v] = true
			bindings(v)
		case v.Flags&Union != 0:
			walked[v] = true
			for _, m := range v.Types {
				add(m)
			}
		}
	}
	bindings = func(d *Type) {
		for _, v := range d.deferEnv {
			add(v)
		}
		for _, v := range d.deferSubst {
			add(v)
		}
		for _, v := range d.deferParams {
			add(v)
		}
	}
	bindings(t)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// IsMissing reports whether t is the type of an omitted optional property
// (TypeScript's missingType): it reads as undefined and prints as undefined,
// but is not the undefined an annotation or a value names.
func (t *Type) IsMissing() bool { return t.Flags&Undefined != 0 && t.Value == "missing" }

// sourceName is a declaration's name as the source wrote it: the resolver's
// per-file suffix (`P__kml_mod0`) removed, and a namespace member's
// mangling (`ns__kmlns_P`) read back as `ns.P`.
func sourceName(name string) string {
	return strings.ReplaceAll(ast.TrimModuleSuffix(name), "__kmlns_", ".")
}

// symbolName is how a type prints sym: an anonymous class expression by its
// display name.
func symbolName(sym *binder.Symbol) string {
	if sym.Display != "" {
		return sourceName(sym.Display)
	}
	return sourceName(sym.Name)
}

// Prop returns the property named name, or nil.
func (t *Type) Prop(name string) *Property {
	for _, p := range t.Props {
		if p.Name == name {
			return p
		}
	}
	return nil
}

// String renders a type the way TypeScript prints it.
// RecursiveAlias reports whether t is the object type of a module-level,
// non-generic alias of a literal naming itself (recursiveLiteralAlias): a
// cyclic type that names no type parameter.
func (t *Type) RecursiveAlias() bool {
	return t.Flags&Object != 0 && t.Kind == Anonymous && t.Symbol != nil && t.Symbol.Flags&binder.TypeAlias != 0
}

func (t *Type) String() string { return t.str(map[*Type]bool{}) }

// str is String, seen the types being printed around t: a type that
// contains itself (`const f = () => f`, `() => ...`) prints as tsc does.
func (t *Type) str(seen map[*Type]bool) string {
	if seen[t] {
		return "..."
	}
	seen[t] = true
	defer delete(seen, t)
	if t.Brand != nil {
		return t.BrandBase.str(seen) + " & " + t.Brand.str(seen)
	}
	if t.ClassOf != nil {
		return "typeof " + symbolName(t.ClassOf)
	}
	if t.Kind == Anonymous && t.Value == "globalThis" {
		return "typeof globalThis"
	}
	// A constructor type: an object whose one member is a construct
	// signature.
	if t.Kind == Anonymous && len(t.Props) == 0 && len(t.Calls) == 0 && len(t.Constructs) == 1 && t.StringIndex == nil && t.NumberIndex == nil {
		return "new " + t.Constructs[0].str(seen)
	}
	switch {
	case t.Flags&Any != 0:
		return "any"
	case t.Flags&Unknown != 0:
		return "unknown"
	case t.Flags&String != 0:
		return "string"
	case t.Flags&Number != 0:
		return "number"
	case t.Flags&Boolean != 0:
		return "boolean"
	case t.Flags&BigInt != 0:
		return "bigint"
	case t.Flags&ESSymbol != 0:
		return "symbol"
	case t.Flags&Void != 0:
		return "void"
	case t.Flags&Undefined != 0:
		return "undefined"
	case t.Flags&Null != 0:
		return "null"
	case t.Flags&Never != 0:
		return "never"
	case t.Flags&NonPrimitive != 0:
		return "object"
	case t.Flags&Literal != 0 && t.Member != "":
		return symbolName(t.Symbol) + "." + t.Member
	case t.Flags&StringLiteral != 0:
		return strconv.Quote(t.Value)
	case t.Flags&(NumberLiteral|BooleanLiteral) != 0:
		return t.Value
	case t.Flags&BigIntLiteral != 0:
		return t.Value + "n"
	case t.Flags&Int != 0:
		if t.Signed {
			return "int" + strconv.Itoa(t.Bits)
		}
		return "uint" + strconv.Itoa(t.Bits)
	case t.Flags&Float32 != 0:
		return "float32"
	case t.Flags&Deferred != 0:
		return t.deferredStr(seen)
	case t.Flags&TypeParam != 0:
		if t.narrowOf != nil {
			k := t.Constraint.str(seen)
			if t.Constraint.Flags&Union != 0 {
				k = "(" + k + ")"
			}
			return t.narrowOf.str(seen) + " & " + k
		}
		if t.Symbol == nil {
			return t.Value // a class's or interface's type parameter
		}
		return symbolName(t.Symbol)
	case t.Flags&Union != 0:
		// TypeScript prints null and undefined last, and a whole enum by its
		// name.
		var parts, tail []string
		members := map[*binder.Symbol]int{}
		for _, m := range t.Types {
			if m.Member != "" {
				members[m.Symbol]++
			}
		}
		named := map[*binder.Symbol]bool{}
		undef := false
		for _, m := range t.Types {
			if m.Member != "" && members[m.Symbol] == m.EnumSize {
				if !named[m.Symbol] {
					named[m.Symbol] = true
					parts = append(parts, symbolName(m.Symbol))
				}
				continue
			}
			if m.Flags&Undefined != 0 {
				// undefined and missing print alike, once.
				if !undef {
					undef = true
					tail = append(tail, "undefined")
				}
			} else if m.Flags&Null != 0 {
				tail = append(tail, m.str(seen))
			} else if ms := m.str(seen); m.Flags&Object != 0 && m.Kind == Function && strings.Contains(ms, "=> ") && !strings.HasPrefix(ms, "{") {
				parts = append(parts, "("+ms+")") // `(() => number) | null`
			} else {
				parts = append(parts, ms)
			}
		}
		return strings.Join(append(parts, tail...), " | ")
	}
	switch t.Kind {
	case Array:
		if t.Head != nil {
			parts := make([]string, 0, len(t.Head)+1)
			for _, h := range t.Head {
				parts = append(parts, h.str(seen))
			}
			r := t.RestElem.str(seen) + "[]"
			if t.RestElem.Flags&Union != 0 || t.RestElem.Flags&Object != 0 && t.RestElem.Kind == Function {
				r = "(" + t.RestElem.str(seen) + ")[]"
			}
			return "[" + strings.Join(append(parts, "..."+r), ", ") + "]"
		}
		if t.Elem.Flags&Union != 0 || t.Elem.Flags&Object != 0 && t.Elem.Kind == Function {
			return "(" + t.Elem.str(seen) + ")[]" // tsc's parentheses: `(string | symbol)[]`
		}
		return t.Elem.str(seen) + "[]"
	case Tuple:
		parts := make([]string, len(t.Elems))
		for i, e := range t.Elems {
			parts[i] = e.str(seen)
			switch {
			case t.Labels != nil && i >= t.minLen():
				parts[i] = t.Labels[i] + "?: " + parts[i]
			case t.Labels != nil:
				parts[i] = t.Labels[i] + ": " + parts[i]
			case i >= t.minLen():
				if e.Flags&Union != 0 || e.Flags&Object != 0 && e.Kind == Function {
					parts[i] = "(" + parts[i] + ")"
				}
				parts[i] += "?"
			}
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case Function:
		if len(t.Overloads) > 0 {
			// As tsc prints an overloaded function: its call signatures.
			sigs := make([]string, len(t.Overloads))
			for i, s := range t.Overloads {
				str := s.str(seen)
				if j := strings.Index(str, ") => "); j >= 0 {
					str = str[:j+1] + ": " + str[j+5:]
				}
				sigs[i] = str
			}
			return "{ " + strings.Join(sigs, "; ") + " }"
		}
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			name := "p" + strconv.Itoa(i)
			if i < len(t.ParamNames) && t.ParamNames[i] != "" {
				name = t.ParamNames[i]
			}
			switch {
			case t.restParam && i == len(t.Params)-1:
				name = "..." + name
			case i < len(t.optionals) && t.optionals[i]:
				name += "?"
			}
			parts[i] = name + ": " + p.str(seen)
		}
		if t.ThisType != nil {
			parts = append([]string{"this: " + t.ThisType.str(seen)}, parts...)
		}
		generic := ""
		if len(t.TypeParams) > 0 {
			names := make([]string, len(t.TypeParams))
			for i, tp := range t.TypeParams {
				names[i] = tp.str(seen)
			}
			generic = "<" + strings.Join(names, ", ") + ">"
		}
		if p := t.Predicate; p != nil {
			s := "p" + strconv.Itoa(p.Index)
			if p.Index >= 0 && p.Index < len(t.ParamNames) && t.ParamNames[p.Index] != "" {
				s = t.ParamNames[p.Index]
			}
			if p.Index == -1 {
				s = "this"
			}
			if p.Type != nil {
				s += " is " + p.Type.str(seen)
			}
			if p.Asserts {
				s = "asserts " + s
			}
			return generic + "(" + strings.Join(parts, ", ") + ") => " + s
		}
		return generic + "(" + strings.Join(parts, ", ") + ") => " + t.Ret().str(seen)
	case Anonymous:
		if t.RecursiveAlias() {
			// A recursive alias's literal, by its alias.
			if len(t.TypeArgs) == 0 {
				return symbolName(t.Symbol)
			}
			args := make([]string, len(t.TypeArgs))
			for i, a := range t.TypeArgs {
				args[i] = a.str(seen)
			}
			return symbolName(t.Symbol) + "<" + strings.Join(args, ", ") + ">"
		}
	case Instance, Interface:
		if t.Symbol != nil {
			if len(t.TypeArgs) > 0 {
				args := make([]string, len(t.TypeArgs))
				for i, a := range t.TypeArgs {
					args[i] = a.str(seen)
				}
				return symbolName(t.Symbol) + "<" + strings.Join(args, ", ") + ">"
			}
			return symbolName(t.Symbol)
		}
	}
	var idx []string
	if t.StringIndex != nil {
		idx = append(idx, "[x: string]: "+t.StringIndex.str(seen)+";")
	}
	if t.NumberIndex != nil {
		idx = append(idx, "[x: number]: "+t.NumberIndex.str(seen)+";")
	}
	if len(t.Props) == 0 && len(idx) == 0 {
		return "{}"
	}
	parts := make([]string, len(t.Props))
	for i, p := range t.Props {
		opt := ""
		if p.Optional {
			opt = "?"
		}
		ro := ""
		if p.Readonly {
			ro = "readonly "
		}
		parts[i] = ro + memberDisplayName(p.Name) + opt + ": " + p.Type.str(seen) + ";"
	}
	return "{ " + strings.Join(append(idx, parts...), " ") + " }" // tsc: `{ x: number; y: string; }`
}

// interner hands out the one *Type for each distinct type.
type interner struct {
	byKey map[string]*Type
	next  int
}

// sigOrigin is where a signature of an interface was declared: the
// interface's symbol, which of its declarations, and whether a parameter is
// annotated with a literal type (a specialized signature).
type sigOrigin struct {
	sym, decl int
	literal   bool
}

func (in *interner) intern(key string, make func() *Type) *Type {
	if t := in.byKey[key]; t != nil {
		return t
	}
	t := make()
	in.next++
	t.ID = in.next
	in.byKey[key] = t
	return t
}

// intern2 interns like intern and reports whether the type is new.
func (in *interner) intern2(key string, make func() *Type) (*Type, bool) {
	if t := in.byKey[key]; t != nil {
		return t, false
	}
	return in.intern(key, make), true
}

func (in *interner) intrinsic(f TypeFlags) *Type {
	return in.intern("i"+strconv.Itoa(int(f)), func() *Type { return &Type{Flags: f} })
}

// anyNumber is the number of an arithmetic operator on two `any` operands
// (Type.AnyArith).
func (in *interner) anyNumber() *Type {
	return in.intern("anynum", func() *Type { return &Type{Flags: Number, AnyArith: true} })
}

func (in *interner) literal(f TypeFlags, value string) *Type {
	return in.intern("l"+strconv.Itoa(int(f))+":"+value, func() *Type { return &Type{Flags: f, Value: value} })
}

func (in *interner) intType(bits int, signed bool) *Type {
	return in.intern("n"+strconv.Itoa(bits)+strconv.FormatBool(signed), func() *Type {
		return &Type{Flags: Int, Bits: bits, Signed: signed}
	})
}

func (in *interner) array(elem *Type) *Type {
	return in.intern("a"+strconv.Itoa(elem.ID), func() *Type { return &Type{Flags: Object, Kind: Array, Elem: elem} })
}

func (in *interner) tuple(elems []*Type) *Type {
	return in.intern("t"+ids(elems), func() *Type { return &Type{Flags: Object, Kind: Tuple, Elems: elems} })
}

// tupleOpt is a tuple whose last opt elements are optional.
func (in *interner) tupleOpt(elems []*Type, opt int) *Type {
	return in.tupleLabelled(elems, opt, nil)
}

// tupleLabelled is tupleOpt with element labels (nil for none).
func (in *interner) tupleLabelled(elems []*Type, opt int, labels []string) *Type {
	opt = min(opt, len(elems))
	if len(labels) != len(elems) {
		labels = nil
	}
	if opt <= 0 && labels == nil {
		return in.tuple(elems)
	}
	return in.intern("t"+ids(elems)+"?"+strconv.Itoa(opt)+"#"+strings.Join(labels, ","), func() *Type {
		return &Type{Flags: Object, Kind: Tuple, Elems: elems, Opt: max(opt, 0), Labels: labels}
	})
}

// tupleRest is the tuple `[...head, ...rest[]]`.
func (in *interner) tupleRest(head []*Type, rest *Type) *Type {
	if len(head) == 0 {
		return in.array(rest)
	}
	elem := in.union(append(append([]*Type(nil), head...), rest)...)
	return in.intern("r"+ids(head)+"|"+strconv.Itoa(rest.ID), func() *Type {
		return &Type{Flags: Object, Kind: Array, Elem: elem, Head: head, RestElem: rest}
	})
}

// elemAt is the type a rest tuple holds at index i.
func (t *Type) elemAt(i int) *Type {
	if i < len(t.Head) {
		return t.Head[i]
	}
	return t.RestElem
}

// minLen is how many elements a tuple requires.
func (t *Type) minLen() int { return len(t.Elems) - t.Opt }

// branded is the branded primitive base & brand (Type.Brand).
func (in *interner) branded(base, brand *Type) *Type {
	return in.intern("B"+strconv.Itoa(base.ID)+":"+strconv.Itoa(brand.ID), func() *Type {
		b := *base
		b.Brand, b.BrandBase = brand, base
		return &b
	})
}

func (in *interner) function(params []*Type, optionals []bool, rest bool, result *Type, pred *Predicate, typeParams []*Type) *Type {
	key := "f" + ids(params) + ">" + strconv.Itoa(result.ID) + "?" + bools(optionals) + strconv.FormatBool(rest) + "<" + ids(typeParams)
	if pred != nil {
		key += "!" + strconv.Itoa(pred.Index) + ":" + strconv.FormatBool(pred.Asserts)
		if pred.Type != nil {
			key += ":" + strconv.Itoa(pred.Type.ID)
		}
	}
	return in.intern(key, func() *Type {
		return &Type{Flags: Object, Kind: Function, Params: params, result: result, Predicate: pred, TypeParams: typeParams, optionals: optionals, restParam: rest}
	})
}

// narrowedParam is type parameter tp narrowed to k (`T & string`).
func (in *interner) narrowedParam(tp, k *Type) *Type {
	if tp.narrowOf != nil {
		tp = tp.narrowOf
	}
	return in.intern("N"+strconv.Itoa(tp.ID)+":"+strconv.Itoa(k.ID), func() *Type {
		c := *tp
		c.Constraint, c.narrowOf = k, tp
		return &c
	})
}

// asMethod is signature fn declared as a method.
func (in *interner) asMethod(fn *Type) *Type {
	if fn == nil || fn.Flags&Object == 0 || fn.Kind != Function || fn.method {
		return fn
	}
	return in.intern("M"+strconv.Itoa(fn.ID), func() *Type {
		c := *fn
		c.method = true
		return &c
	})
}

// withParamNames is signature fn with its parameters' declared names, for
// display.
func (in *interner) withParamNames(fn *Type, names []string) *Type {
	if fn.Flags&Object == 0 || fn.Kind != Function || len(names) == 0 {
		return fn
	}
	return in.intern("n"+strconv.Itoa(fn.ID)+":"+strings.Join(names, ","), func() *Type {
		c := *fn
		c.ParamNames = names
		return &c
	})
}

// withResult is signature fn returning r.
func (in *interner) withResult(fn, r *Type) *Type {
	return in.intern("r"+strconv.Itoa(fn.ID)+":"+strconv.Itoa(r.ID), func() *Type {
		c := *fn
		c.result, c.lazyResult = r, nil
		return &c
	})
}

// withThis is signature fn with a `this: t` parameter.
func (in *interner) withThis(fn, t *Type) *Type {
	if t == nil || fn.Flags&Object == 0 || fn.Kind != Function {
		return fn
	}
	return in.intern("t"+strconv.Itoa(fn.ID)+":"+strconv.Itoa(t.ID), func() *Type {
		c := *fn
		c.ThisType = t
		return &c
	})
}

// withLiteralThis is object type o carrying a `ThisType<t>` marker.
func (in *interner) withLiteralThis(o, t *Type) *Type {
	return in.intern("T"+strconv.Itoa(o.ID)+":"+strconv.Itoa(t.ID), func() *Type {
		c := *o
		c.LiteralThis = t
		return &c
	})
}

// indexed interns an anonymous object type with properties props and the
// index signatures str and num (either may be nil).
func (in *interner) indexed(props []*Property, str, num *Type) *Type {
	return in.indexedOn(in.object(props), str, num)
}

// indexedOn is the object type o with string and number index signatures.
func (in *interner) indexedOn(o *Type, str, num *Type) *Type {
	if str == nil && num == nil {
		return o
	}
	key := "x" + strconv.Itoa(o.ID)
	if str != nil {
		key += "s" + strconv.Itoa(str.ID)
	}
	if num != nil {
		key += "n" + strconv.Itoa(num.ID)
	}
	return in.intern(key, func() *Type {
		c := *o
		c.StringIndex, c.NumberIndex = str, num
		return &c
	})
}

// withSignatures is o with call and construct signatures (a type literal's
// `(…): R` and `new (…): R` members), interned by them.
func (in *interner) withSignatures(o *Type, calls, constructs []*Type) *Type {
	if len(calls) == 0 && len(constructs) == 0 {
		return o
	}
	key := "g" + strconv.Itoa(o.ID)
	for _, t := range calls {
		key += "c" + strconv.Itoa(t.ID)
	}
	for _, t := range constructs {
		key += "k" + strconv.Itoa(t.ID)
	}
	return in.intern(key, func() *Type {
		c := *o
		c.Calls, c.Constructs = calls, constructs
		return &c
	})
}

// classValue is the type of class sym's value: its statics o with its
// construct signature ctor (nil for none modelled), named `typeof C`.
func (in *interner) classValue(o, ctor *Type, sym *binder.Symbol) *Type {
	key := "v" + strconv.Itoa(o.ID) + "|" + sym.Name + fmt.Sprintf("|%p", sym)
	if ctor != nil {
		key += "k" + strconv.Itoa(ctor.ID)
	}
	return in.intern(key, func() *Type {
		c := *o
		if ctor != nil {
			c.Constructs = []*Type{ctor}
		}
		c.ClassOf = sym
		return &c
	})
}

// object interns an anonymous object type by its properties' names, types
// and optionality, in declaration order.
func (in *interner) object(props []*Property) *Type { return in.objectOf(props, false, false, nil) }

// objectOf is object, with partial a type whose member list is not whole (an
// intersection with a builtin interface), with intersection one an
// intersection of members built.
func (in *interner) objectOf(props []*Property, partial, intersection bool, members []*Type) *Type {
	var b strings.Builder
	b.WriteString("o")
	if partial {
		b.WriteByte(1) // not a name's character
	}
	if intersection {
		b.WriteByte(2)
		for _, m := range members {
			b.WriteString(strconv.Itoa(m.ID))
			b.WriteByte(',')
		}
	}
	for _, p := range props {
		b.WriteString(p.Name)
		if p.Optional {
			b.WriteByte('?')
		}
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(p.Type.ID))
		if p.Accessor {
			b.WriteString("@")
			if p.Write != nil {
				b.WriteString(strconv.Itoa(p.Write.ID))
			}
		}
		if p.Readonly {
			b.WriteString("!")
		}
		b.WriteByte(';')
	}
	return in.intern(b.String(), func() *Type {
		return &Type{Flags: Object, Kind: Anonymous, Props: props, partial: partial, intersection: intersection, Intersects: members}
	})
}

// nominal interns the instance or interface type a symbol declares; its
// properties are filled once, by the caller that creates it.
func (in *interner) nominal(kind ObjectKind, sym *binder.Symbol, args []*Type) (*Type, bool) {
	key := nominalKey(kind, sym, args)
	if t := in.byKey[key]; t != nil {
		return t, false
	}
	return in.intern(key, func() *Type { return &Type{Flags: Object, Kind: kind, Symbol: sym, TypeArgs: args} }), true
}

func nominalKey(kind ObjectKind, sym *binder.Symbol, args []*Type) string {
	return "s" + strconv.Itoa(int(kind)) + ":" + strconv.Itoa(sym.ID) + "<" + ids(args)
}

// union interns the union of ts: nested unions flattened, duplicates
// dropped, members ordered by ID; a single member is itself; never drops out;
// any and unknown absorb everything; a literal whose primitive is a member drops out,
// and true | false is boolean (TypeScript's literal reduction).
func (in *interner) union(ts ...*Type) *Type {
	set := map[*Type]bool{}
	var members []*Type
	var add func(t *Type)
	add = func(t *Type) {
		if t.Flags&Union != 0 {
			for _, m := range t.Types {
				add(m)
			}
			return
		}
		if t.Flags&Never != 0 || set[t] {
			return
		}
		set[t] = true
		members = append(members, t)
	}
	for _, t := range ts {
		if t == nil {
			continue
		}
		if t.Flags&Any != 0 {
			return t
		}
		add(t)
	}
	for _, m := range members {
		if m.Flags&Unknown != 0 {
			return m // unknown absorbs every other member, as any does
		}
	}
	// The number of `any` arithmetic (AnyArith) holds every number, so it
	// absorbs plain numbers and number literals.
	for _, m := range members {
		if m.AnyArith {
			kept := members[:0]
			for _, o := range members {
				if o.AnyArith || o.Flags&(Number|NumberLiteral) == 0 {
					kept = append(kept, o)
				}
			}
			members = kept
			break
		}
	}
	members = in.reduceLiterals(members)
	switch len(members) {
	case 0:
		return in.intrinsic(Never)
	case 1:
		return members[0]
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return in.intern("u"+ids(members), func() *Type { return &Type{Flags: Union, Types: members} })
}

func ids(ts []*Type) string {
	parts := make([]string, len(ts))
	for i, t := range ts {
		parts[i] = strconv.Itoa(t.ID)
	}
	return strings.Join(parts, ",")
}

func bools(bs []bool) string {
	var b strings.Builder
	for _, x := range bs {
		if x {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

// reduceLiterals drops the literals whose primitive is among members and
// folds true | false into boolean.
func (in *interner) reduceLiterals(members []*Type) []*Type {
	var prims TypeFlags
	hasTrue, hasFalse := false, false
	for _, m := range members {
		prims |= m.Flags & (String | Number | Boolean | BigInt)
		if m.Flags&BooleanLiteral != 0 {
			hasTrue = hasTrue || m.Value == "true"
			hasFalse = hasFalse || m.Value == "false"
		}
	}
	if hasTrue && hasFalse && prims&Boolean == 0 {
		members = append(members, in.intrinsic(Boolean))
		prims |= Boolean
	}
	var base = map[TypeFlags]TypeFlags{StringLiteral: String, NumberLiteral: Number, BooleanLiteral: Boolean, BigIntLiteral: BigInt}
	out := members[:0:0]
	for _, m := range members {
		if b := base[m.Flags&Literal]; b != 0 && prims&b != 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}

// Derives reports whether the instance type t is of class sym or a subclass.
func (t *Type) Derives(sym *binder.Symbol) bool {
	for seen := 0; t != nil && seen < 64; seen++ {
		if t.Kind == Instance && t.Symbol == sym {
			return true
		}
		t = t.Base
	}
	return false
}

// overloaded interns the type of a function with overload signatures sigs:
// the first signature's shape, carrying the whole list.
func (in *interner) overloaded(sigs []*Type) *Type {
	return in.intern("o"+ids(sigs), func() *Type {
		f := *sigs[0]
		f.ID = 0
		f.Overloads = sigs
		return &f
	})
}

// HasRestParam reports whether a function type's last parameter is a rest.
func (t *Type) HasRestParam() bool { return t.restParam }

// MinArgs is how many arguments a call of function type t must pass: up to
// its last required parameter, a trailing parameter that accepts void
// counting as optional (tsc's getMinArgumentCount: `resolve()` for a
// `(value: void | T) => void`).
func (t *Type) MinArgs() int {
	min := 0
	for i := range t.Params {
		if (i < len(t.optionals) && t.optionals[i]) || (t.restParam && i == len(t.Params)-1) {
			break
		}
		min = i + 1
	}
	for min > 0 && acceptsVoid(t.Params[min-1]) {
		min--
	}
	return min
}

func acceptsVoid(t *Type) bool {
	if t == nil {
		return false
	}
	if t.Flags&Void != 0 {
		return true
	}
	if t.Flags&Union != 0 {
		for _, m := range t.Types {
			if m.Flags&Void != 0 {
				return true
			}
		}
	}
	return false
}

// OptionalParam reports whether a function type's parameter i is optional.
func (t *Type) OptionalParam(i int) bool { return i < len(t.optionals) && t.optionals[i] }

// Ret is a Function's result type, computed on first read when deferred.
func (t *Type) Ret() *Type {
	if t.computing && t.cycleResult != nil {
		return t.cycleResult()
	}
	if t.lazyResult != nil {
		f := t.lazyResult
		t.lazyResult = nil
		t.computing = true
		r, done := f()
		t.computing = false
		if !done {
			// Asked again inside the cycle that deferred it: try again later.
			t.lazyResult = f
		}
		t.result = r
	}
	return t.result
}

// WithoutResult is a copy of signature t with no result, for a caller that
// fills one in itself.
func (t *Type) WithoutResult() *Type {
	c := *t
	c.result, c.lazyResult = nil, nil
	return &c
}

// deferred is a fresh copy of signature fn whose result f computes on its
// first read that f reports done.
func (in *interner) deferred(fn *Type, f func() (*Type, bool), cycle func() *Type) *Type {
	c := *fn
	c.result, c.lazyResult, c.cycleResult = nil, f, cycle
	in.next++
	c.ID = in.next
	return &c
}

// enterSelf reports whether a walk may enter t: false when t is a
// self-containing stand-in the walk is already inside. leave undoes it.
func (t *Type) enterSelf() (ok bool, leave func()) {
	if !t.selfRef {
		return true, func() {}
	}
	if t.walking {
		return false, func() {}
	}
	t.walking = true
	return true, func() { t.walking = false }
}

// SelfReferential reports a type that contains itself (a recursive
// alias's union, `T | Box<R<T>>`).
func (t *Type) SelfReferential() bool { return t != nil && t.selfRef }

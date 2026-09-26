// Package checker computes the type of every expression (TDD-00230 P2.3,
// P2.4). It reads the binder's symbols and never changes the AST: every
// answer is memoised in a side table, and each expression is checked once.
package checker

import (
	"KlainMainLang/ast"
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
	// Types are a union's members, ordered by ID.
	Types []*Type
	// The object payload.
	Kind       ObjectKind
	Elem       *Type          // Array
	Elems      []*Type        // Tuple
	Props      []*Property    // Anonymous, Instance, Interface
	Params     []*Type        // Function
	Result     *Type          // Function
	Symbol     *binder.Symbol // Instance, Interface: the declaring symbol
	Base       *Type          // Instance: the base class's instance type
	TypeArgs   []*Type        // Instance, Interface: a generic declaration's arguments
	Predicate  *Predicate     // Function: a `p is T` result
	TypeParams []*Type        // Function: a generic signature's type parameters
	Overloads  []*Type        // Function: an overloaded function's signatures, in order
	ThisType   *Type          // Function: a `this: T` parameter's T, or nil
	// StringIndex and NumberIndex are an object type's index signatures'
	// value types (`[k: string]: T`, `[i: number]: T`), or nil.
	StringIndex, NumberIndex *Type
	// An enum member is a number or string literal whose Symbol is its
	// enum, with its Member name and the enum's member count.
	Member     string
	EnumSize   int
	Constraint *Type  // TypeParam: its `extends` bound, or nil
	optionals  []bool // Function: optional parameters
	restParam  bool   // Function: the last parameter is a rest
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
	// Decl is the member's declaration (a method signature, a property
	// signature, a class member), when it has one; Decls are an overloaded
	// method's, one per overload in order.
	Decl  ast.Node
	Decls []ast.Node
}

// IsMissing reports whether t is the type of an omitted optional property
// (TypeScript's missingType): it reads as undefined and prints as undefined,
// but is not the undefined an annotation or a value names.
func (t *Type) IsMissing() bool { return t.Flags&Undefined != 0 && t.Value == "missing" }

// sourceName is a declaration's name as the source wrote it: the resolver's
// per-file suffix (`P__kml_mod0`) removed, and a namespace member's
// mangling (`ns__kmlns_P`) read back as `ns.P`.
func sourceName(name string) string {
	if i := strings.LastIndex(name, "__kml_mod"); i > 0 && allDigits(name[i+len("__kml_mod"):]) {
		name = name[:i]
	}
	return strings.ReplaceAll(name, "__kmlns_", ".")
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
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
func (t *Type) String() string {
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
		return sourceName(t.Symbol.Name) + "." + t.Member
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
	case t.Flags&TypeParam != 0:
		if t.Symbol == nil {
			return t.Value // a class's or interface's type parameter
		}
		return sourceName(t.Symbol.Name)
	case t.Flags&Union != 0:
		// TypeScript prints null and undefined last, and a whole enum by its
		// name.
		var parts, tail []string
		seen := map[*binder.Symbol]int{}
		for _, m := range t.Types {
			if m.Member != "" {
				seen[m.Symbol]++
			}
		}
		named := map[*binder.Symbol]bool{}
		undef := false
		for _, m := range t.Types {
			if m.Member != "" && seen[m.Symbol] == m.EnumSize {
				if !named[m.Symbol] {
					named[m.Symbol] = true
					parts = append(parts, sourceName(m.Symbol.Name))
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
				tail = append(tail, m.String())
			} else {
				parts = append(parts, m.String())
			}
		}
		return strings.Join(append(parts, tail...), " | ")
	}
	switch t.Kind {
	case Array:
		if t.Elem.Flags&Union != 0 || t.Elem.Flags&Object != 0 && t.Elem.Kind == Function {
			return "(" + t.Elem.String() + ")[]" // tsc's parentheses: `(string | symbol)[]`
		}
		return t.Elem.String() + "[]"
	case Tuple:
		parts := make([]string, len(t.Elems))
		for i, e := range t.Elems {
			parts[i] = e.String()
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case Function:
		if len(t.Overloads) > 0 {
			// As tsc prints an overloaded function: its call signatures.
			sigs := make([]string, len(t.Overloads))
			for i, s := range t.Overloads {
				str := s.String()
				if j := strings.Index(str, ") => "); j >= 0 {
					str = str[:j+1] + ": " + str[j+5:]
				}
				sigs[i] = str
			}
			return "{ " + strings.Join(sigs, "; ") + " }"
		}
		parts := make([]string, len(t.Params))
		for i, p := range t.Params {
			parts[i] = "p" + strconv.Itoa(i) + ": " + p.String()
		}
		if t.ThisType != nil {
			parts = append([]string{"this: " + t.ThisType.String()}, parts...)
		}
		generic := ""
		if len(t.TypeParams) > 0 {
			names := make([]string, len(t.TypeParams))
			for i, tp := range t.TypeParams {
				names[i] = tp.String()
			}
			generic = "<" + strings.Join(names, ", ") + ">"
		}
		if p := t.Predicate; p != nil {
			s := "p" + strconv.Itoa(p.Index)
			if p.Index == -1 {
				s = "this"
			}
			if p.Type != nil {
				s += " is " + p.Type.String()
			}
			if p.Asserts {
				s = "asserts " + s
			}
			return generic + "(" + strings.Join(parts, ", ") + ") => " + s
		}
		return generic + "(" + strings.Join(parts, ", ") + ") => " + t.Result.String()
	case Instance, Interface:
		if t.Symbol != nil {
			if len(t.TypeArgs) > 0 {
				args := make([]string, len(t.TypeArgs))
				for i, a := range t.TypeArgs {
					args[i] = a.String()
				}
				return sourceName(t.Symbol.Name) + "<" + strings.Join(args, ", ") + ">"
			}
			return sourceName(t.Symbol.Name)
		}
	}
	if len(t.Props) == 0 {
		return "{}"
	}
	parts := make([]string, len(t.Props))
	for i, p := range t.Props {
		opt := ""
		if p.Optional {
			opt = "?"
		}
		parts[i] = p.Name + opt + ": " + p.Type.String() + ";"
	}
	return "{ " + strings.Join(parts, " ") + " }" // tsc: `{ x: number; y: string; }`
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

func (in *interner) function(params []*Type, optionals []bool, rest bool, result *Type, pred *Predicate, typeParams []*Type) *Type {
	key := "f" + ids(params) + ">" + strconv.Itoa(result.ID) + "?" + bools(optionals) + strconv.FormatBool(rest) + "<" + ids(typeParams)
	if pred != nil {
		key += "!" + strconv.Itoa(pred.Index) + ":" + strconv.FormatBool(pred.Asserts)
		if pred.Type != nil {
			key += ":" + strconv.Itoa(pred.Type.ID)
		}
	}
	return in.intern(key, func() *Type {
		return &Type{Flags: Object, Kind: Function, Params: params, Result: result, Predicate: pred, TypeParams: typeParams, optionals: optionals, restParam: rest}
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

// indexed interns an anonymous object type with properties props and the
// index signatures str and num (either may be nil).
func (in *interner) indexed(props []*Property, str, num *Type) *Type {
	o := in.object(props)
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

// object interns an anonymous object type by its properties' names, types
// and optionality, in declaration order.
func (in *interner) object(props []*Property) *Type { return in.objectOf(props, false, false) }

// objectOf is object, with partial a type whose member list is not whole (an
// intersection with a builtin interface), with intersection one an
// intersection built.
func (in *interner) objectOf(props []*Property, partial, intersection bool) *Type {
	var b strings.Builder
	b.WriteString("o")
	if partial {
		b.WriteByte(1) // not a name's character
	}
	if intersection {
		b.WriteByte(2)
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
		return &Type{Flags: Object, Kind: Anonymous, Props: props, partial: partial, intersection: intersection}
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

package ast

// Type syntax as node kinds. The parser builds these for every type it reads;
// TypeAnnotationOf (type_convert.go) turns a tree into the TypeAnnotation code
// generation still consumes, and the annotation keeps a pointer back to the
// node it came from. The node kinds and field names follow TypeScript's own
// (TypeReference, UnionType, TypePredicate, …) so the checker can read them.
//
// Every node records where it came from in Range: its first token's line and
// column (also GetPos) and the byte range [Start, End) of its source text.

// TypeNode is a node in a type position.
type TypeNode interface {
	Node
	GetLoc() Loc
	typeNodeMarker()
}

// TypeMember is a member of an object type literal or of an interface body.
type TypeMember interface {
	Node
	GetLoc() Loc
	typeMemberMarker()
}

// Loc is a node's source location: its first token's position, and the byte
// offsets [Start, End) of the text it spans.
type Loc struct {
	Pos        Pos
	Start, End int
}

// KeywordType is a keyword type: any, unknown, never, string, number, boolean,
// bigint, symbol, object, void, null or undefined.
type KeywordType struct {
	Keyword string
	Range   Loc
}

// TypeReference is a named type, `T`, `ns.T` or `Box<A, B>`. Qualifier holds
// the leading segments of a qualified name (`a.b.T` → [a b], Name T).
type TypeReference struct {
	Qualifier []string
	Name      string
	TypeArgs  []TypeNode
	Range     Loc
	// ArgSeparators is the position of the comma after each type argument
	// but the last, for arity diagnostics.
	ArgSeparators []Pos
}

// ArrayType is `T[]`.
type ArrayType struct {
	ElementType TypeNode
	Range       Loc
}

// TupleType is `[A, B]`. An element may be a NamedTupleMember, an
// OptionalType or a RestType.
type TupleType struct {
	Elements []TypeNode
	Range    Loc
}

// NamedTupleMember is a labelled tuple element `name: T`, `name?: T` or
// `...name: T`.
type NamedTupleMember struct {
	Name     string
	Optional bool
	Rest     bool
	Type     TypeNode
	Range    Loc
}

// OptionalType is an unlabelled optional tuple element `T?`.
type OptionalType struct {
	Type  TypeNode
	Range Loc
}

// RestType is an unlabelled rest tuple element `...T`.
type RestType struct {
	Type  TypeNode
	Range Loc
}

// UnionType is `A | B | …`, with at least two members.
type UnionType struct {
	Types []TypeNode
	Range Loc
}

// IntersectionType is `A & B & …`, with at least two members.
type IntersectionType struct {
	Types []TypeNode
	Range Loc
}

// ParenthesizedType is `(T)`.
type ParenthesizedType struct {
	Type  TypeNode
	Range Loc
}

// TypeParameter is one entry of a `<T extends C, …>` list.
type TypeParameter struct {
	Name       string
	Constraint TypeNode // nil when unconstrained
	Default    TypeNode // `= T`; nil when there is none
	Range      Loc
}

// SignatureParameter is one parameter of a function type or a signature
// member. Name is empty for an unnamed parameter (`(number) => void`).
type SignatureParameter struct {
	Name     string
	Optional bool
	Rest     bool
	Type     TypeNode
	Range    Loc
}

// FunctionType is `<T>(a: A, b?: B, ...c: C[]) => R`.
type FunctionType struct {
	TypeParameters []*TypeParameter
	Parameters     []*SignatureParameter
	// This is a leading `this: T` parameter's type (TypeScript's
	// thisParameter), nil when there is none; it is not in Parameters.
	This  TypeNode
	Type  TypeNode
	Range Loc
}

// ConstructorType is `new (a: A) => R`.
type ConstructorType struct {
	TypeParameters []*TypeParameter
	Parameters     []*SignatureParameter
	Type           TypeNode
	Range          Loc
}

// TypeLiteral is an object type `{ … }`.
type TypeLiteral struct {
	Members []TypeMember
	Range   Loc
}

// PropertySignature is `name?: T`. Name is the key's literal text (an
// identifier, or a string or numeric literal's value).
type PropertySignature struct {
	Name     string
	Optional bool
	Readonly bool
	// Computed marks a computed name (`[Symbol.iterator]`): Name is its
	// source text in brackets.
	Computed bool
	Type     TypeNode // nil when the member has no annotation (any)
	// JSDocType is a `/** @type {…} */` width that overrides Type for code
	// generation (`int32`, `float64`, …).
	JSDocType string
	// Accessor is "get" or "set" for an accessor signature (`get x(): T`,
	// `set x(v: T)`), whose Type is the getter's result or the setter's
	// parameter type.
	Accessor string
	Range    Loc
}

// MethodSignature is `name?<T>(params): R`. Type is nil when the return type
// is omitted.
type MethodSignature struct {
	Name     string
	Optional bool
	Computed bool // see PropertySignature.Computed
	// Lower is a builtin declaration's `/** @lower symbol */`: the runtime
	// entry point a call compiles to (TDD-00230 P3.2); Link are its
	// `@link lib` libraries.
	Lower          string
	Link           []string
	TypeParameters []*TypeParameter
	Parameters     []*SignatureParameter
	Type           TypeNode
	Range          Loc
}

// CallSignature is `(params): R`. Type is nil when the return type is omitted.
type CallSignature struct {
	TypeParameters []*TypeParameter
	Parameters     []*SignatureParameter
	Type           TypeNode
	Range          Loc
}

// ConstructSignature is `new (params): R`. Type is nil when the return type is
// omitted.
type ConstructSignature struct {
	TypeParameters []*TypeParameter
	Parameters     []*SignatureParameter
	Type           TypeNode
	Range          Loc
}

// IndexSignature is `[key: K]: T`.
type IndexSignature struct {
	Readonly bool
	KeyName  string
	KeyType  TypeNode
	Type     TypeNode
	Range    Loc
}

// MappedType is `{ readonly [K in C]?: T }`.
type MappedType struct {
	Readonly   bool
	KeyName    string
	Constraint TypeNode
	Optional   bool
	Type       TypeNode
	Range      Loc
}

// ConditionalType is `C extends E ? T : F`.
type ConditionalType struct {
	CheckType   TypeNode
	ExtendsType TypeNode
	TrueType    TypeNode
	FalseType   TypeNode
	Range       Loc
}

// InferType is `infer R`, inside a conditional type's extends clause.
type InferType struct {
	Name  string
	Range Loc
}

// TypeOperator is `keyof T` or `readonly T`.
type TypeOperator struct {
	Operator string
	Type     TypeNode
	Range    Loc
}

// IndexedAccessType is `T[K]`.
type IndexedAccessType struct {
	ObjectType TypeNode
	IndexType  TypeNode
	Range      Loc
}

// ImportType is `import("mod").A.B<T>`: a type named through a module
// specifier (Argument "mod", Qualifier [A B]).
type ImportType struct {
	Argument  string
	Qualifier []string
	TypeArgs  []TypeNode
	Range     Loc
}

// TypeQuery is `typeof a.b.c` (Name a, Path [b c]).
type TypeQuery struct {
	Name  string
	Path  []string
	Range Loc
}

// LiteralType is a string, numeric or boolean literal type. Kind is "string",
// "number" or "boolean"; Value is the literal's text ("-1.5", "true") or, for
// a string, its decoded value.
type LiteralType struct {
	Kind  string
	Value string
	Range Loc
}

// TemplateLiteralType is “ `head${A}middle${B}tail` “.
type TemplateLiteralType struct {
	Head  string
	Spans []*TemplateLiteralTypeSpan
	Range Loc
}

// TemplateLiteralTypeSpan is one `${T}text` part of a template literal type.
type TemplateLiteralTypeSpan struct {
	Type    TypeNode
	Literal string
	Range   Loc
}

// TypePredicate is a return type `x is T`, `asserts x is T`, `asserts x` or
// `asserts this`. Type is nil for the `asserts x` form.
type TypePredicate struct {
	Asserts       bool
	ParameterName string // empty when the subject is `this`
	This          bool
	Type          TypeNode
	Range         Loc
}

func (*KeywordType) nodeMarker()             {}
func (*TypeReference) nodeMarker()           {}
func (*ArrayType) nodeMarker()               {}
func (*TupleType) nodeMarker()               {}
func (*NamedTupleMember) nodeMarker()        {}
func (*OptionalType) nodeMarker()            {}
func (*RestType) nodeMarker()                {}
func (*UnionType) nodeMarker()               {}
func (*IntersectionType) nodeMarker()        {}
func (*ParenthesizedType) nodeMarker()       {}
func (*TypeParameter) nodeMarker()           {}
func (*SignatureParameter) nodeMarker()      {}
func (*FunctionType) nodeMarker()            {}
func (*ConstructorType) nodeMarker()         {}
func (*TypeLiteral) nodeMarker()             {}
func (*PropertySignature) nodeMarker()       {}
func (*MethodSignature) nodeMarker()         {}
func (*CallSignature) nodeMarker()           {}
func (*ConstructSignature) nodeMarker()      {}
func (*IndexSignature) nodeMarker()          {}
func (*MappedType) nodeMarker()              {}
func (*ConditionalType) nodeMarker()         {}
func (*InferType) nodeMarker()               {}
func (*TypeOperator) nodeMarker()            {}
func (*IndexedAccessType) nodeMarker()       {}
func (*TypeQuery) nodeMarker()               {}
func (*ImportType) nodeMarker()              {}
func (*LiteralType) nodeMarker()             {}
func (*TemplateLiteralType) nodeMarker()     {}
func (*TemplateLiteralTypeSpan) nodeMarker() {}
func (*TypePredicate) nodeMarker()           {}

func (*KeywordType) typeNodeMarker()         {}
func (*TypeReference) typeNodeMarker()       {}
func (*ArrayType) typeNodeMarker()           {}
func (*TupleType) typeNodeMarker()           {}
func (*NamedTupleMember) typeNodeMarker()    {}
func (*OptionalType) typeNodeMarker()        {}
func (*RestType) typeNodeMarker()            {}
func (*UnionType) typeNodeMarker()           {}
func (*IntersectionType) typeNodeMarker()    {}
func (*ParenthesizedType) typeNodeMarker()   {}
func (*FunctionType) typeNodeMarker()        {}
func (*ConstructorType) typeNodeMarker()     {}
func (*TypeLiteral) typeNodeMarker()         {}
func (*MappedType) typeNodeMarker()          {}
func (*ConditionalType) typeNodeMarker()     {}
func (*InferType) typeNodeMarker()           {}
func (*TypeOperator) typeNodeMarker()        {}
func (*IndexedAccessType) typeNodeMarker()   {}
func (*TypeQuery) typeNodeMarker()           {}
func (*ImportType) typeNodeMarker()          {}
func (*LiteralType) typeNodeMarker()         {}
func (*TemplateLiteralType) typeNodeMarker() {}
func (*TypePredicate) typeNodeMarker()       {}

func (*PropertySignature) typeMemberMarker()  {}
func (*MethodSignature) typeMemberMarker()    {}
func (*CallSignature) typeMemberMarker()      {}
func (*ConstructSignature) typeMemberMarker() {}
func (*IndexSignature) typeMemberMarker()     {}

func (n *KeywordType) GetPos() Pos             { return n.Range.Pos }
func (n *TypeReference) GetPos() Pos           { return n.Range.Pos }
func (n *ArrayType) GetPos() Pos               { return n.Range.Pos }
func (n *TupleType) GetPos() Pos               { return n.Range.Pos }
func (n *NamedTupleMember) GetPos() Pos        { return n.Range.Pos }
func (n *OptionalType) GetPos() Pos            { return n.Range.Pos }
func (n *RestType) GetPos() Pos                { return n.Range.Pos }
func (n *UnionType) GetPos() Pos               { return n.Range.Pos }
func (n *IntersectionType) GetPos() Pos        { return n.Range.Pos }
func (n *ParenthesizedType) GetPos() Pos       { return n.Range.Pos }
func (n *TypeParameter) GetPos() Pos           { return n.Range.Pos }
func (n *SignatureParameter) GetPos() Pos      { return n.Range.Pos }
func (n *FunctionType) GetPos() Pos            { return n.Range.Pos }
func (n *ConstructorType) GetPos() Pos         { return n.Range.Pos }
func (n *TypeLiteral) GetPos() Pos             { return n.Range.Pos }
func (n *PropertySignature) GetPos() Pos       { return n.Range.Pos }
func (n *MethodSignature) GetPos() Pos         { return n.Range.Pos }
func (n *CallSignature) GetPos() Pos           { return n.Range.Pos }
func (n *ConstructSignature) GetPos() Pos      { return n.Range.Pos }
func (n *IndexSignature) GetPos() Pos          { return n.Range.Pos }
func (n *MappedType) GetPos() Pos              { return n.Range.Pos }
func (n *ConditionalType) GetPos() Pos         { return n.Range.Pos }
func (n *InferType) GetPos() Pos               { return n.Range.Pos }
func (n *TypeOperator) GetPos() Pos            { return n.Range.Pos }
func (n *IndexedAccessType) GetPos() Pos       { return n.Range.Pos }
func (n *TypeQuery) GetPos() Pos               { return n.Range.Pos }
func (n *ImportType) GetPos() Pos              { return n.Range.Pos }
func (n *LiteralType) GetPos() Pos             { return n.Range.Pos }
func (n *TemplateLiteralType) GetPos() Pos     { return n.Range.Pos }
func (n *TemplateLiteralTypeSpan) GetPos() Pos { return n.Range.Pos }
func (n *TypePredicate) GetPos() Pos           { return n.Range.Pos }

func (n *KeywordType) GetLoc() Loc             { return n.Range }
func (n *TypeReference) GetLoc() Loc           { return n.Range }
func (n *ArrayType) GetLoc() Loc               { return n.Range }
func (n *TupleType) GetLoc() Loc               { return n.Range }
func (n *NamedTupleMember) GetLoc() Loc        { return n.Range }
func (n *OptionalType) GetLoc() Loc            { return n.Range }
func (n *RestType) GetLoc() Loc                { return n.Range }
func (n *UnionType) GetLoc() Loc               { return n.Range }
func (n *IntersectionType) GetLoc() Loc        { return n.Range }
func (n *ParenthesizedType) GetLoc() Loc       { return n.Range }
func (n *TypeParameter) GetLoc() Loc           { return n.Range }
func (n *SignatureParameter) GetLoc() Loc      { return n.Range }
func (n *FunctionType) GetLoc() Loc            { return n.Range }
func (n *ConstructorType) GetLoc() Loc         { return n.Range }
func (n *TypeLiteral) GetLoc() Loc             { return n.Range }
func (n *PropertySignature) GetLoc() Loc       { return n.Range }
func (n *MethodSignature) GetLoc() Loc         { return n.Range }
func (n *CallSignature) GetLoc() Loc           { return n.Range }
func (n *ConstructSignature) GetLoc() Loc      { return n.Range }
func (n *IndexSignature) GetLoc() Loc          { return n.Range }
func (n *MappedType) GetLoc() Loc              { return n.Range }
func (n *ConditionalType) GetLoc() Loc         { return n.Range }
func (n *InferType) GetLoc() Loc               { return n.Range }
func (n *TypeOperator) GetLoc() Loc            { return n.Range }
func (n *IndexedAccessType) GetLoc() Loc       { return n.Range }
func (n *TypeQuery) GetLoc() Loc               { return n.Range }
func (n *ImportType) GetLoc() Loc              { return n.Range }
func (n *LiteralType) GetLoc() Loc             { return n.Range }
func (n *TemplateLiteralType) GetLoc() Loc     { return n.Range }
func (n *TemplateLiteralTypeSpan) GetLoc() Loc { return n.Range }
func (n *TypePredicate) GetLoc() Loc           { return n.Range }

// Package binder builds the symbols of a program: one Symbol per declared
// name, held in the symbol table of the scope that declares it, and the
// symbol every identifier reference resolves to (TDD-00230 P2.1). The AST is
// not changed: every result lives in side tables keyed by node.
//
// The design follows TypeScript's binder (symbol flags, declarations merged
// per name, per-container tables) adapted to this compiler's AST, where
// binding names are strings on the declaring node. Redeclarations follow the
// early errors of strict code (see DeclKind) rather than TypeScript's
// exclude masks, which also cover type-space clashes this pass does not
// check yet.
package binder

import "KlainMainLang/ast"

// SymbolFlags classify a symbol's declarations. The values mirror
// TypeScript's so masks read the same.
type SymbolFlags uint32

const (
	FunctionScopedVariable SymbolFlags = 1 << iota // var, parameter, catch variable
	BlockScopedVariable                            // let, const
	Property
	EnumMember
	Function
	Class
	Interface
	ConstEnum
	RegularEnum
	TypeParameter
	TypeAlias
	Alias // an import binding
	NamespaceModule
	// TypeModule is a namespace declaring no value (only interfaces, type
	// aliases, non-exported aliases): TypeScript's non-instantiated
	// namespace, a name for qualified type references and not a value.
	TypeModule

	Enum     = RegularEnum | ConstEnum
	Variable = FunctionScopedVariable | BlockScopedVariable
	Value    = Variable | Property | EnumMember | Function | Class | Enum | Alias | NamespaceModule
	Type     = Class | Interface | Enum | EnumMember | TypeParameter | TypeAlias | Alias
)

// Declaration is one declaration of a symbol: the node that declares the
// name (a VarDeclaration, a destructuring statement, the function whose
// parameter it is, the TryStatement whose catch variable it is, …) and the
// position of that node.
type Declaration struct {
	Node  ast.Node
	Pos   ast.Pos
	Flags SymbolFlags
	Kind  DeclKind
	// Plain marks a function declaration that is neither async nor a
	// generator (the kind Annex B lets a sloppy block redeclare).
	Plain bool
	// Body marks a declaration made by a function body's statements, not by
	// its parameters. A parameter's default is evaluated before the body, in
	// an environment without the body's declarations.
	Body bool
}

// DeclKind says how a declaration may share its name with another in the
// same scope (the redeclaration early errors of strict code, plus the
// merges TypeScript allows).
type DeclKind int

const (
	// VarLike: var, a parameter, a catch variable, a function declared
	// directly in a function, namespace or module. They merge with each other.
	VarLike DeclKind = iota
	// Lexical: let, const, class, enum (and its members), an import, a
	// function declared in a block. It clashes with any other value
	// declaration of the name.
	Lexical
	// TypeOnly: an interface or type alias. It has no value binding to clash.
	TypeOnly
	// NamespaceDecl: a namespace, which merges with a function, class, enum
	// or namespace of the same name.
	NamespaceDecl
	// NameOnly: a binding no other declaration can meet (a named function or
	// class expression's own name, `arguments`).
	NameOnly
)

// Clash is how two declarations of one name in one scope conflict.
type Clash int

const (
	NoClash Clash = iota
	// Redeclaration is an early error (or TypeScript's duplicate identifier).
	Redeclaration
	// UnsupportedMerge is a merge TypeScript allows (two enums) that this
	// compiler does not implement.
	UnsupportedMerge
)

// clashes classifies two declarations of one name in one scope. With
// annexB (sloppy scripts, -compat=js), two plain function declarations in a
// block merge (ECMAScript Annex B.3.3.4).
func clashes(a, b Declaration, annexB bool) Clash {
	if annexB && a.Flags == Function && b.Flags == Function && a.Plain && b.Plain {
		return NoClash
	}
	if a.Kind == NameOnly || b.Kind == NameOnly {
		return NoClash
	}
	if a.Kind == TypeOnly || b.Kind == TypeOnly {
		switch {
		case a.Kind != TypeOnly && b.Kind != TypeOnly:
			return NoClash
		case a.Flags&TypeParameter != 0 && b.Flags&TypeParameter != 0:
			return Redeclaration // `<T, T>`
		case a.Flags&TypeAlias != 0 || b.Flags&TypeAlias != 0:
			// A type alias shares its name with no other type.
			other := a
			if a.Flags&TypeAlias != 0 {
				other = b
			}
			if other.Flags&(Interface|TypeAlias|Class|Enum) != 0 {
				return Redeclaration
			}
			return NoClash
		}
		return NoClash // interfaces merge, with each other and with a class
	}
	if a.Kind == NamespaceDecl || b.Kind == NamespaceDecl {
		other := a
		if a.Kind == NamespaceDecl {
			other = b
		}
		if other.Flags&(Function|Class|Enum|NamespaceModule|TypeModule) == 0 {
			return Redeclaration
		}
		return NoClash
	}
	if a.Flags&Enum != 0 && b.Flags&Enum != 0 {
		if a.Flags&Enum == b.Flags&Enum {
			return UnsupportedMerge
		}
		return Redeclaration
	}
	if a.Kind == Lexical || b.Kind == Lexical {
		return Redeclaration
	}
	return NoClash
}

// Symbol is one name declared in one scope, with every declaration of it.
type Symbol struct {
	ID           int
	Name         string
	Flags        SymbolFlags
	Declarations []Declaration
	Scope        *Scope
	// Implicit marks a symbol no declaration introduces (a function's
	// `arguments`).
	Implicit bool
}

// ScopeKind is the kind of container a scope belongs to.
type ScopeKind int

const (
	ModuleScope    ScopeKind = iota
	FunctionScope            // a function's parameters and body
	BlockScope               // a block, a loop head, a switch body
	CatchScope               // a catch clause's variable
	NameScope                // a named function or class expression's own name
	EnumScope                // an enum's members, visible in its initializers
	NamespaceScope           // a namespace's members
)

// Scope is a container of declarations.
type Scope struct {
	Kind    ScopeKind
	Node    ast.Node
	Parent  *Scope
	Symbols Table
	// Inline marks a function scope bound as part of its caller's flow (an
	// immediately invoked function expression).
	Inline bool
}

// Table is a symbol table that keeps insertion order, so every walk over it
// (and every id derived from it) is deterministic.
type Table struct {
	names []string
	m     map[string]*Symbol
}

// Get returns the symbol named name.
func (t *Table) Get(name string) *Symbol { return t.m[name] }

// Len is the number of symbols.
func (t *Table) Len() int { return len(t.names) }

// Each calls f on every symbol in declaration order.
func (t *Table) Each(f func(*Symbol)) {
	for _, n := range t.names {
		f(t.m[n])
	}
}

func (t *Table) put(s *Symbol) {
	if t.m == nil {
		t.m = map[string]*Symbol{}
	}
	if _, ok := t.m[s.Name]; !ok {
		t.names = append(t.names, s.Name)
	}
	t.m[s.Name] = s
}

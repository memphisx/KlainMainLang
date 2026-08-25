package ast

// Pos tracks source location.
type Pos struct{ Line, Col int }

// Node is the common interface for all AST nodes.
type Node interface {
	nodeMarker()
	GetPos() Pos
}

// Statement nodes.
type Statement interface {
	Node
	stmtMarker()
}

// Expression nodes.
type Expression interface {
	Node
	exprMarker()
}

// --- Program ---

type Program struct {
	Body []Statement
	// Namespaces records TS `namespace X { export ... }` declarations
	// (TDD-00095): namespace name → member-name set. The members themselves
	// were desugared by the parser into ordinary top-level declarations named
	// NamespaceMangle(X, member); this table is what lets `X.member` call/
	// value sites resolve through them. Nil when a program declares none.
	Namespaces map[string]map[string]bool
	// WorkerPaths is filled by the parser for a single file's program: the
	// raw string-literal path of every `new Worker('...')` in the file, so
	// the resolver can resolve worker entry files as dependencies without a
	// full-AST walk (TDD-00098). Empty in the merged program.
	WorkerPaths []string
	// WorkerModules is filled by the resolver on the merged program: one
	// entry per distinct worker entry file, holding that file's top-level
	// statements — kept out of Body so codegen emits them into a dedicated
	// per-worker entry function instead of main (TDD-00098). The worker
	// file's function/class/interface/type/enum declarations are hoisted
	// into Body like any dependency's; only its var declarations and
	// executable statements stay here.
	WorkerModules []WorkerModule
}

// WorkerModule is one worker entry file's diverted top-level statement list
// (TDD-00098). Path is the canonical absolute file path — the same key a
// NewWorkerExpression's ResolvedPath carries, which is how codegen matches a
// `new Worker(...)` site to its entry function.
type WorkerModule struct {
	Path string
	Body []Statement
}

// NamespaceMangle is the flat top-level name a namespace member desugars to —
// shared by the parser (declaration side) and codegen (use side). The
// `__kmlns_` infix can't collide with user identifiers in practice, the same
// convention other internal manglings rely on.
func NamespaceMangle(ns, member string) string {
	return ns + "__kmlns_" + member
}

func (*Program) nodeMarker() {}
func (*Program) GetPos() Pos { return Pos{} }

// --- Statements ---

type BlockStatement struct {
	Body []Statement
	pos  Pos
}

func (*BlockStatement) nodeMarker()   {}
func (*BlockStatement) stmtMarker()   {}
func (b *BlockStatement) GetPos() Pos { return b.pos }

func NewBlockStatement(body []Statement, pos Pos) *BlockStatement {
	return &BlockStatement{Body: body, pos: pos}
}

type VarDeclaration struct {
	Kind      string // "let", "const", "var"
	Name      string
	TypeAnnot *TypeAnnotation // nil if absent
	Init      Expression      // nil if absent
	pos       Pos
}

func (*VarDeclaration) nodeMarker()   {}
func (*VarDeclaration) stmtMarker()   {}
func (v *VarDeclaration) GetPos() Pos { return v.pos }

func NewVarDeclaration(kind, name string, ta *TypeAnnotation, init Expression, pos Pos) *VarDeclaration {
	return &VarDeclaration{Kind: kind, Name: name, TypeAnnot: ta, Init: init, pos: pos}
}

// VarDeclarationList — multiple comma-separated declarators sharing one
// let/const/var (`let i = 0, j = 10;`). Deliberately NOT represented as a
// BlockStatement wrapping N VarDeclarations: BlockStatement pushes its own
// scope (see emitStmt), which would immediately hide every declared name
// once the "statement" ends — fatally wrong for a for-loop's init clause
// (`for (let i = 0, j = 10; ...)`), where the loop's own test/update/body
// need every name to stay visible in the *enclosing* scope, exactly like a
// single-declarator `let` already does.
type VarDeclarationList struct {
	Decls []*VarDeclaration
	pos   Pos
}

func (*VarDeclarationList) nodeMarker()   {}
func (*VarDeclarationList) stmtMarker()   {}
func (v *VarDeclarationList) GetPos() Pos { return v.pos }

func NewVarDeclarationList(decls []*VarDeclaration, pos Pos) *VarDeclarationList {
	return &VarDeclarationList{Decls: decls, pos: pos}
}

type FunctionDeclaration struct {
	Name       string
	TypeParams []string // e.g. ["T"] for `function identity<T>(...)` — TDD-00010 V1, single param only
	// TypeParamConstraints[i] is the `extends X` bound on TypeParams[i] (nil if
	// unconstrained), e.g. `function pluck<T extends HasId>(...)` — TDD-00113.
	TypeParamConstraints []*TypeAnnotation
	// Erased is TDD-00010 V2: set when a `/** @erased */` JSDoc annotation
	// precedes the declaration, opting a generic function out of V1's
	// default monomorphization into compiling its body exactly once, with
	// every bare-T parameter/return position treated as TypeAny instead.
	// Meaningless (always false) unless len(TypeParams) > 0.
	Erased     bool
	Params     []Param
	ReturnType *TypeAnnotation
	Body       *BlockStatement // nil for an abstract method (IsAbstract true) — signature only
	IsAsync    bool
	// IsStatic/Visibility/IsAbstract are TDD-00009 Stage 4 class-member
	// modifiers, meaningful only for a class's Constructor/Methods entries
	// — always zero-value for a plain top-level function, same "harmless
	// zero-value elsewhere" shape IsAsync already established.
	IsStatic   bool
	Visibility string // "private" / "protected" / "" (public, default)
	IsAbstract bool
	// AccessorKind is "get" / "set" / "" (a plain method or top-level
	// function, the default) — TDD-00030. A getter/setter is otherwise a
	// perfectly ordinary FunctionDeclaration (a zero-arg method for "get",
	// a one-arg method for "set"); this is the only new AST field the
	// feature needs, everything else routes through the class method
	// machinery unmodified via a name-mangled dispatch key (see
	// codegen/llvm/emit_classes.go's registerClasses).
	AccessorKind string
	// IsGenerator is `function* name() {}` (TDD-00061) — parses into a real
	// AST flag and enables `yield`/`yield*` inside this function's own body,
	// but codegen has no generator suspend/resume machinery yet: a
	// generator-flagged declaration is a clean, explicit compile-time
	// rejection ("generator functions are not yet supported"), not a
	// silent no-op. V1 scope: top-level/nested function declarations only —
	// not class/object methods, not function expressions/arrows.
	IsGenerator bool
	pos         Pos
}

func (*FunctionDeclaration) nodeMarker()   {}
func (*FunctionDeclaration) stmtMarker()   {}
func (f *FunctionDeclaration) GetPos() Pos { return f.pos }

// SetPos back-fills source position on a FunctionDeclaration built via a
// struct literal outside this package (parser_stmts.go's parseFunctionRest
// sets every other field directly but can't reach the unexported pos field)
// — GetPos() returned a permanently zero Pos{} for every top-level
// function/method until a caller did this. Found while wiring TDD-00010
// V1's generic-function error messages, which need a real declaration
// position; fixed as a pre-existing, unrelated bug rather than left in
// place (see the project's own "fix bugs found during development" rule).
func (f *FunctionDeclaration) SetPos(pos Pos) { f.pos = pos }

type Param struct {
	Name     string
	Type     *TypeAnnotation
	Rest     bool       // true when declared with ...
	Default  Expression // non-nil when declared with = expr
	Optional bool       // true when declared with ?
	// ArrayPattern/ObjectPattern: non-nil exactly when this parameter is a
	// destructuring pattern (`function f([a, b]: number[])` /
	// `function f({x, y}: T)`) rather than a plain bindable name — at most
	// one is ever non-nil. Name still holds a synthetic internal name
	// (e.g. "__param0") in this case, used for LLVM parameter naming and
	// error messages; the pattern fields are what codegen actually unpacks
	// into real local bindings. Mirrors ArrayDestructuring.Elems/
	// ObjectDestructuring.Props' own shapes (including each element's own
	// Default, ADR-00158) so the same unpack codegen can be shared between
	// a destructuring statement and a destructured parameter.
	ArrayPattern  []ArrayPatternElem
	ObjectPattern []DestructProp
}

type ReturnStatement struct {
	Value Expression // nil for bare return
	pos   Pos
}

func (*ReturnStatement) nodeMarker()   {}
func (*ReturnStatement) stmtMarker()   {}
func (r *ReturnStatement) GetPos() Pos { return r.pos }

func NewReturnStatement(val Expression, pos Pos) *ReturnStatement {
	return &ReturnStatement{Value: val, pos: pos}
}

type ForStatement struct {
	Init Statement  // VarDeclaration, VarDeclarationList, or ExpressionStatement, nil if absent
	Test Expression // nil if absent
	// Update holds one entry per comma-separated update expression
	// (`i++, j--` — a real, common idiom, not the general comma operator,
	// which stays out of scope everywhere else); nil (empty) if the update
	// clause is absent. Each is evaluated in order, every iteration, purely
	// for side effects — none of their values are ever used.
	Update []Expression
	Body   *BlockStatement
	pos    Pos
}

func (*ForStatement) nodeMarker()   {}
func (*ForStatement) stmtMarker()   {}
func (f *ForStatement) GetPos() Pos { return f.pos }

func NewForStatement(init Statement, test Expression, update []Expression, body *BlockStatement, pos Pos) *ForStatement {
	return &ForStatement{Init: init, Test: test, Update: update, Body: body, pos: pos}
}

type ForOfStatement struct {
	Kind    string // "let", "const", "var"
	VarName string
	// ArrayPattern/ObjectPattern: non-nil exactly when the loop variable is
	// a destructuring pattern (`for (const [a, b] of …)` /
	// `for (const { x, y } of …)`) rather than a bare VarName — mirrors
	// Param's own pattern carriers, and the per-iteration element is
	// unpacked through the same unpack*PatternInto codegen core the
	// declaration and parameter positions already share (TDD-00065 Stage 1).
	ArrayPattern  []ArrayPatternElem
	ObjectPattern []DestructProp
	Iterable      Expression
	Body          *BlockStatement
	// Await marks a `for await (const x of asyncIter)` loop (async iteration,
	// TDD-00085) — the per-iteration `.next()` yields a Promise the loop awaits.
	Await bool
	pos   Pos
}

func (*ForOfStatement) nodeMarker()   {}
func (*ForOfStatement) stmtMarker()   {}
func (f *ForOfStatement) GetPos() Pos { return f.pos }

func NewForOfStatement(kind, varName string, iterable Expression, body *BlockStatement, pos Pos) *ForOfStatement {
	return &ForOfStatement{Kind: kind, VarName: varName, Iterable: iterable, Body: body, pos: pos}
}

type WhileStatement struct {
	Test Expression
	Body *BlockStatement
	pos  Pos
}

func (*WhileStatement) nodeMarker()   {}
func (*WhileStatement) stmtMarker()   {}
func (w *WhileStatement) GetPos() Pos { return w.pos }

func NewWhileStatement(test Expression, body *BlockStatement, pos Pos) *WhileStatement {
	return &WhileStatement{Test: test, Body: body, pos: pos}
}

type DoWhileStatement struct {
	Body *BlockStatement
	Test Expression
	pos  Pos
}

func (*DoWhileStatement) nodeMarker()   {}
func (*DoWhileStatement) stmtMarker()   {}
func (d *DoWhileStatement) GetPos() Pos { return d.pos }

func NewDoWhileStatement(body *BlockStatement, test Expression, pos Pos) *DoWhileStatement {
	return &DoWhileStatement{Body: body, Test: test, pos: pos}
}

type ForInStatement struct {
	Kind    string // "let", "const", "var"
	VarName string
	Object  Expression
	Body    *BlockStatement
	pos     Pos
}

func (*ForInStatement) nodeMarker()   {}
func (*ForInStatement) stmtMarker()   {}
func (f *ForInStatement) GetPos() Pos { return f.pos }

func NewForInStatement(kind, varName string, object Expression, body *BlockStatement, pos Pos) *ForInStatement {
	return &ForInStatement{Kind: kind, VarName: varName, Object: object, Body: body, pos: pos}
}

type IfStatement struct {
	Test       Expression
	Consequent *BlockStatement
	Alternate  Statement // *BlockStatement, *IfStatement, or nil
	pos        Pos
}

func (*IfStatement) nodeMarker()   {}
func (*IfStatement) stmtMarker()   {}
func (i *IfStatement) GetPos() Pos { return i.pos }

func NewIfStatement(test Expression, cons *BlockStatement, alt Statement, pos Pos) *IfStatement {
	return &IfStatement{Test: test, Consequent: cons, Alternate: alt, pos: pos}
}

type SwitchCase struct {
	Test Expression // nil for default
	Body []Statement
}

type SwitchStatement struct {
	Discriminant Expression
	Cases        []SwitchCase
	pos          Pos
}

func (*SwitchStatement) nodeMarker()   {}
func (*SwitchStatement) stmtMarker()   {}
func (s *SwitchStatement) GetPos() Pos { return s.pos }

func NewSwitchStatement(disc Expression, cases []SwitchCase, pos Pos) *SwitchStatement {
	return &SwitchStatement{Discriminant: disc, Cases: cases, pos: pos}
}

type BreakStatement struct {
	Label string // empty for a bare, unlabeled break
	pos   Pos
}

func (*BreakStatement) nodeMarker()   {}
func (*BreakStatement) stmtMarker()   {}
func (b *BreakStatement) GetPos() Pos { return b.pos }

func NewBreakStatement(label string, pos Pos) *BreakStatement {
	return &BreakStatement{Label: label, pos: pos}
}

type ContinueStatement struct {
	Label string // empty for a bare, unlabeled continue
	pos   Pos
}

func (*ContinueStatement) nodeMarker()   {}
func (*ContinueStatement) stmtMarker()   {}
func (c *ContinueStatement) GetPos() Pos { return c.pos }

func NewContinueStatement(label string, pos Pos) *ContinueStatement {
	return &ContinueStatement{Label: label, pos: pos}
}

// LabeledStatement is `label: statement` — currently only meaningful when
// Body is one of the five loop statement forms (for/while/do-while/for-of/
// for-in), which register the label with their break/continue targets. A
// label placed on anything else parses fine (matching real JS grammar) but
// is simply never registered, so break/continue referencing it fails with a
// clean "undefined label" error rather than silently doing nothing useful.
type LabeledStatement struct {
	Label string
	Body  Statement
	pos   Pos
}

func (*LabeledStatement) nodeMarker()   {}
func (*LabeledStatement) stmtMarker()   {}
func (l *LabeledStatement) GetPos() Pos { return l.pos }

func NewLabeledStatement(label string, body Statement, pos Pos) *LabeledStatement {
	return &LabeledStatement{Label: label, Body: body, pos: pos}
}

// DestructProp is one binding in an object destructuring pattern.
type DestructProp struct {
	Key   string // field name in the source object
	Local string // local variable name (= Key when no rename)
	// Default is non-nil for `{ key = expr }` (ADR-00158) — only ever
	// meaningful (checked and evaluated) when Key names a
	// pointer-backed nullable (`T | null`) field, the only field shape
	// this compiler can tell "was this actually provided" apart from a
	// legitimate value for; rejected at compile time otherwise. See
	// unpackObjectPatternInto's own doc comment.
	Default Expression
	// SubArray/SubObject is non-nil for a nested pattern at this key
	// (`{ key: [a, b] }` / `{ key: { a } }`, TDD-00065 Stage 2) — Key's
	// own value is itself destructured with the sub-pattern instead of
	// bound to a leaf Local. Exactly one is ever set, and never alongside
	// a Default. When set, Local is unused.
	SubArray  []ArrayPatternElem
	SubObject []DestructProp
	// Rest marks `{ ...rest }` (TDD-00065 Stage 3b) — Local collects every
	// source field not named by an earlier property into a fresh residual
	// object. Must be the last property; never carries a Key, Default, or
	// sub-pattern. When true, Key is unused.
	Rest bool
}

// ArrayPatternElem is one binding in an array destructuring pattern —
// ArrayDestructuring's and a destructured Param's own array-pattern
// element, mirroring DestructProp's shape for the array-position case.
type ArrayPatternElem struct {
	Name string // "" = hole (skipped index)
	// Default is non-nil for `[a = expr]` — fires exactly when this
	// position is past the source array's actual length (see
	// unpackArrayPatternInto's own doc comment for why that's the one
	// reliable "was this provided" signal this compiler's array
	// destructuring has, unlike object destructuring's field-shape
	// restriction above).
	Default Expression
	// Rest marks `[a, ...rest]` (ADR-00161) — Name collects every
	// remaining source position from here on into a real, new array.
	// Always the parser-enforced last element (real JS: a SyntaxError
	// otherwise); Default is never set alongside it (real JS doesn't
	// allow one either — `[...rest = []]` is invalid).
	Rest bool
	// SubArray/SubObject is non-nil for a nested pattern at this position
	// (`[[a, b], c]` / `[{ x }, { y }]`, TDD-00065 Stage 2) — the element
	// at this index is itself destructured with the sub-pattern instead of
	// bound to a leaf Name. Exactly one is ever set; Name is "" when a
	// sub-pattern is present, so a plain `Name == ""` no longer implies a
	// hole (check the sub-pattern fields too).
	SubArray  []ArrayPatternElem
	SubObject []DestructProp
}

// ArrayDestructuring — const/let [a, b] = expr
type ArrayDestructuring struct {
	Kind  string // "let", "const", "var"
	Elems []ArrayPatternElem
	Init  Expression
	pos   Pos
}

func (*ArrayDestructuring) nodeMarker()   {}
func (*ArrayDestructuring) stmtMarker()   {}
func (a *ArrayDestructuring) GetPos() Pos { return a.pos }

func NewArrayDestructuring(kind string, elems []ArrayPatternElem, init Expression, pos Pos) *ArrayDestructuring {
	return &ArrayDestructuring{Kind: kind, Elems: elems, Init: init, pos: pos}
}

// ObjectDestructuring — const/let { x, y: alias } = expr
type ObjectDestructuring struct {
	Kind  string
	Props []DestructProp
	Init  Expression
	pos   Pos
}

func (*ObjectDestructuring) nodeMarker()   {}
func (*ObjectDestructuring) stmtMarker()   {}
func (o *ObjectDestructuring) GetPos() Pos { return o.pos }

func NewObjectDestructuring(kind string, props []DestructProp, init Expression, pos Pos) *ObjectDestructuring {
	return &ObjectDestructuring{Kind: kind, Props: props, Init: init, pos: pos}
}

type ExpressionStatement struct {
	Expr Expression
	pos  Pos
}

func (*ExpressionStatement) nodeMarker()   {}
func (*ExpressionStatement) stmtMarker()   {}
func (e *ExpressionStatement) GetPos() Pos { return e.pos }

func NewExpressionStatement(expr Expression, pos Pos) *ExpressionStatement {
	return &ExpressionStatement{Expr: expr, pos: pos}
}

// --- Expressions ---

type NumberLiteral struct {
	Value    string // raw literal digits, e.g. "42", "3.14", "0x1f" (a BigInt keeps its prefix but never the trailing `n`)
	IsBigInt bool   // true for a `123n` BigInt literal (TDD-00074)
	pos      Pos
}

func (*NumberLiteral) nodeMarker()   {}
func (*NumberLiteral) exprMarker()   {}
func (n *NumberLiteral) GetPos() Pos { return n.pos }

func NewNumberLiteral(v string, pos Pos) *NumberLiteral { return &NumberLiteral{Value: v, pos: pos} }

// NewBigIntLiteral builds the `123n` form — the same node with IsBigInt set, so
// every existing NumberLiteral code path keeps working and only the handful of
// bigint-aware sites (inferExprType, literal codegen) branch on the flag.
func NewBigIntLiteral(v string, pos Pos) *NumberLiteral {
	return &NumberLiteral{Value: v, IsBigInt: true, pos: pos}
}

type StringLiteral struct {
	Value string
	pos   Pos
}

func (*StringLiteral) nodeMarker()   {}
func (*StringLiteral) exprMarker()   {}
func (s *StringLiteral) GetPos() Pos { return s.pos }

func NewStringLiteral(v string, pos Pos) *StringLiteral { return &StringLiteral{Value: v, pos: pos} }

type BooleanLiteral struct {
	Value bool
	pos   Pos
}

func (*BooleanLiteral) nodeMarker()   {}
func (*BooleanLiteral) exprMarker()   {}
func (b *BooleanLiteral) GetPos() Pos { return b.pos }

func NewBooleanLiteral(v bool, pos Pos) *BooleanLiteral { return &BooleanLiteral{Value: v, pos: pos} }

// NullLiteral represents `null` (IsUndefined=false) or `undefined` (IsUndefined=true).
type NullLiteral struct {
	IsUndefined bool
	pos         Pos
}

func (*NullLiteral) nodeMarker()   {}
func (*NullLiteral) exprMarker()   {}
func (n *NullLiteral) GetPos() Pos { return n.pos }

func NewNullLiteral(isUndefined bool, pos Pos) *NullLiteral {
	return &NullLiteral{IsUndefined: isUndefined, pos: pos}
}

// AwaitExpression represents `await expr`.
type AwaitExpression struct {
	Argument Expression
	pos      Pos
}

func (*AwaitExpression) nodeMarker()   {}
func (*AwaitExpression) exprMarker()   {}
func (a *AwaitExpression) GetPos() Pos { return a.pos }

func NewAwaitExpression(arg Expression, pos Pos) *AwaitExpression {
	return &AwaitExpression{Argument: arg, pos: pos}
}

// YieldExpression is `yield expr` / `yield* expr` / a bare `yield` with no
// operand (TDD-00061) — Argument is nil for the bare form, matching
// ReturnStatement's own "no value" convention. Delegate is true for `yield*`
// (iterate another iterable/generator, re-yielding each of its own values) —
// parses into real AST either way, but codegen has no generator machinery
// yet at all (see FunctionDeclaration.IsGenerator's own doc comment), so
// `yield*`'s actual delegation semantics are unimplemented design space
// regardless of this flag, not a "works but untested" case.
type YieldExpression struct {
	Argument Expression // nil for a bare `yield`
	Delegate bool       // true for `yield*`
	pos      Pos
}

func (*YieldExpression) nodeMarker()   {}
func (*YieldExpression) exprMarker()   {}
func (y *YieldExpression) GetPos() Pos { return y.pos }

func NewYieldExpression(arg Expression, delegate bool, pos Pos) *YieldExpression {
	return &YieldExpression{Argument: arg, Delegate: delegate, pos: pos}
}

type Identifier struct {
	Name string
	pos  Pos
}

func (*Identifier) nodeMarker()   {}
func (*Identifier) exprMarker()   {}
func (i *Identifier) GetPos() Pos { return i.pos }

func NewIdentifier(name string, pos Pos) *Identifier { return &Identifier{Name: name, pos: pos} }

// ThisExpression — `this`, valid inside method/constructor bodies. Parses
// anywhere an expression can (TDD-00009 Stage 1 gives it meaning as the
// implicit receiver); used elsewhere it reaches codegen's generic
// "unknown expression type" fallback, same as ClassDeclaration does today.
type ThisExpression struct {
	pos Pos
}

func (*ThisExpression) nodeMarker()   {}
func (*ThisExpression) exprMarker()   {}
func (t *ThisExpression) GetPos() Pos { return t.pos }

func NewThisExpression(pos Pos) *ThisExpression { return &ThisExpression{pos: pos} }

type BinaryExpression struct {
	Op          string
	Left, Right Expression
	pos         Pos
}

func (*BinaryExpression) nodeMarker()   {}
func (*BinaryExpression) exprMarker()   {}
func (b *BinaryExpression) GetPos() Pos { return b.pos }

func NewBinaryExpression(op string, left, right Expression, pos Pos) *BinaryExpression {
	return &BinaryExpression{Op: op, Left: left, Right: right, pos: pos}
}

type ConditionalExpression struct {
	Test       Expression
	Consequent Expression
	Alternate  Expression
	pos        Pos
}

func (*ConditionalExpression) nodeMarker()   {}
func (*ConditionalExpression) exprMarker()   {}
func (c *ConditionalExpression) GetPos() Pos { return c.pos }

func NewConditionalExpression(test, consequent, alternate Expression, pos Pos) *ConditionalExpression {
	return &ConditionalExpression{Test: test, Consequent: consequent, Alternate: alternate, pos: pos}
}

// SequenceExpression — the comma operator (`a, b, c`): evaluates each operand
// left to right and yields the last one's value. Only produced where a full
// expression is allowed (a parenthesized group, an expression statement, a
// `for` header) — never for the commas that separate call arguments, array
// elements, or parameters, which are parsed one assignment-expression apiece.
type SequenceExpression struct {
	Exprs []Expression
	pos   Pos
}

func (*SequenceExpression) nodeMarker()   {}
func (*SequenceExpression) exprMarker()   {}
func (s *SequenceExpression) GetPos() Pos { return s.pos }

func NewSequenceExpression(exprs []Expression, pos Pos) *SequenceExpression {
	return &SequenceExpression{Exprs: exprs, pos: pos}
}

// SpreadElement — ...expr inside an array literal.
type SpreadElement struct {
	Arg Expression
	pos Pos
}

func (*SpreadElement) nodeMarker()   {}
func (*SpreadElement) exprMarker()   {}
func (s *SpreadElement) GetPos() Pos { return s.pos }

func NewSpreadElement(arg Expression, pos Pos) *SpreadElement {
	return &SpreadElement{Arg: arg, pos: pos}
}

type UnaryExpression struct {
	Op     string
	Prefix bool
	Arg    Expression
	pos    Pos
}

func (*UnaryExpression) nodeMarker()   {}
func (*UnaryExpression) exprMarker()   {}
func (u *UnaryExpression) GetPos() Pos { return u.pos }

func NewUnaryExpression(op string, prefix bool, arg Expression, pos Pos) *UnaryExpression {
	return &UnaryExpression{Op: op, Prefix: prefix, Arg: arg, pos: pos}
}

type UpdateExpression struct {
	Op     string // "++" or "--"
	Prefix bool
	Arg    Expression
	pos    Pos
}

func (*UpdateExpression) nodeMarker()   {}
func (*UpdateExpression) exprMarker()   {}
func (u *UpdateExpression) GetPos() Pos { return u.pos }

func NewUpdateExpression(op string, prefix bool, arg Expression, pos Pos) *UpdateExpression {
	return &UpdateExpression{Op: op, Prefix: prefix, Arg: arg, pos: pos}
}

type AssignmentExpression struct {
	Op          string // "=", "+=", "-=", "*=", "/="
	Left, Right Expression
	pos         Pos
}

func (*AssignmentExpression) nodeMarker()   {}
func (*AssignmentExpression) exprMarker()   {}
func (a *AssignmentExpression) GetPos() Pos { return a.pos }

func NewAssignmentExpression(op string, left, right Expression, pos Pos) *AssignmentExpression {
	return &AssignmentExpression{Op: op, Left: left, Right: right, pos: pos}
}

type CallExpression struct {
	Callee Expression
	Args   []Expression
	pos    Pos
}

func (*CallExpression) nodeMarker()   {}
func (*CallExpression) exprMarker()   {}
func (c *CallExpression) GetPos() Pos { return c.pos }

func NewCallExpression(callee Expression, args []Expression, pos Pos) *CallExpression {
	return &CallExpression{Callee: callee, Args: args, pos: pos}
}

type MemberExpression struct {
	Object   Expression
	Property string
	Optional bool // true for ?.
	pos      Pos
}

func (*MemberExpression) nodeMarker()   {}
func (*MemberExpression) exprMarker()   {}
func (m *MemberExpression) GetPos() Pos { return m.pos }

func NewMemberExpression(obj Expression, prop string, pos Pos) *MemberExpression {
	return &MemberExpression{Object: obj, Property: prop, pos: pos}
}

type ArrayLiteral struct {
	Elements []Expression
	pos      Pos
}

func (*ArrayLiteral) nodeMarker()   {}
func (*ArrayLiteral) exprMarker()   {}
func (a *ArrayLiteral) GetPos() Pos { return a.pos }

func NewArrayLiteral(elems []Expression, pos Pos) *ArrayLiteral {
	return &ArrayLiteral{Elements: elems, pos: pos}
}

type IndexExpression struct {
	Object Expression
	Index  Expression
	pos    Pos
}

func (*IndexExpression) nodeMarker()   {}
func (*IndexExpression) exprMarker()   {}
func (i *IndexExpression) GetPos() Pos { return i.pos }

func NewIndexExpression(obj, index Expression, pos Pos) *IndexExpression {
	return &IndexExpression{Object: obj, Index: index, pos: pos}
}

type NewArrayExpression struct {
	ElemType *TypeAnnotation // from <T>; nil if omitted
	Size     Expression
	pos      Pos
}

func (*NewArrayExpression) nodeMarker()   {}
func (*NewArrayExpression) exprMarker()   {}
func (n *NewArrayExpression) GetPos() Pos { return n.pos }

func NewNewArrayExpression(elemType *TypeAnnotation, size Expression, pos Pos) *NewArrayExpression {
	return &NewArrayExpression{ElemType: elemType, Size: size, pos: pos}
}

// --- Object expressions ---

type ObjectProperty struct {
	Key string
	// KeyExpr is non-nil for a computed property key `{ [expr]: value }`;
	// Key is unused in that case. nil means a static key (Key holds the name)
	// or, when Key == "" and Value is a *SpreadElement, an object spread.
	KeyExpr Expression
	Value   Expression
}

type ObjectLiteral struct {
	Properties []ObjectProperty
	pos        Pos
}

func (*ObjectLiteral) nodeMarker()   {}
func (*ObjectLiteral) exprMarker()   {}
func (o *ObjectLiteral) GetPos() Pos { return o.pos }

func NewObjectLiteral(props []ObjectProperty, pos Pos) *ObjectLiteral {
	return &ObjectLiteral{Properties: props, pos: pos}
}

// HasComputedKey reports whether any property uses `[expr]: value` syntax.
func (o *ObjectLiteral) HasComputedKey() bool {
	for _, p := range o.Properties {
		if p.KeyExpr != nil {
			return true
		}
	}
	return false
}

// --- Arrow functions (closures) ---

// ArrowFunction is an anonymous function expression. It may capture variables
// from its enclosing scope (closure). Body holds an expression body `=> expr`;
// Block holds a block body `=> { stmts }`. Exactly one is non-nil.
type ArrowFunction struct {
	Params  []Param
	RetType *TypeAnnotation // nil = infer
	Body    Expression      // non-nil for `=> expr`
	Block   *BlockStatement // non-nil for `=> { stmts }`
	IsAsync bool
	pos     Pos
}

func (*ArrowFunction) nodeMarker()   {}
func (*ArrowFunction) exprMarker()   {}
func (a *ArrowFunction) GetPos() Pos { return a.pos }

func NewArrowFunction(params []Param, retType *TypeAnnotation, body Expression, block *BlockStatement, pos Pos) *ArrowFunction {
	return &ArrowFunction{Params: params, RetType: retType, Body: body, Block: block, pos: pos}
}

// --- Function expressions ---

// FunctionExpression is an anonymous function expression (`var f = function(x): number { return x; }`).
// It may capture variables from its enclosing scope (closure) — the same runtime
// closure machinery arrow functions already use. Name is "" for anonymous;
// a named function expression is a V1 scope cut (TDD-00060).
type FunctionExpression struct {
	Name    string // "" for anonymous, rejected for named V1
	Params  []Param
	RetType *TypeAnnotation // nil = infer
	Body    *BlockStatement
	IsAsync bool
	// IsGenerator is `function* () {...}` in expression position
	// (TDD-00096). Only the top-level `const G = function* ...` binding is
	// supported — rewritten into a named generator declaration pre-emission;
	// any other use is a clean codegen rejection.
	IsGenerator bool
	pos         Pos
}

func (*FunctionExpression) nodeMarker()   {}
func (*FunctionExpression) exprMarker()   {}
func (f *FunctionExpression) GetPos() Pos { return f.pos }

func NewFunctionExpression(name string, params []Param, retType *TypeAnnotation, body *BlockStatement, isAsync bool, pos Pos) *FunctionExpression {
	return &FunctionExpression{Name: name, Params: params, RetType: retType, Body: body, IsAsync: isAsync, pos: pos}
}

// ClassExpression — a `class [Name] { ... }` in expression position
// (TDD-00063 Stage 4). Classes are compile-time nominal types here, not
// first-class runtime values, so V1 supports a class expression only as the
// initializer of a top-level `const/let/var X = class {...}` binding: a
// codegen pre-pass rewrites it into a nominal ClassDeclaration named after
// the LHS (rewriteTopLevelClassExpressions). Any other position — an
// argument, a return value, a nested (non-top-level) binding — reaches
// emitExpr and is a clean rejection, since no runtime constructor value
// exists to produce. Decl carries the fully-parsed class body (Stages 1–3
// members included); its Name is a placeholder until the rewrite overwrites
// it with the LHS name.
type ClassExpression struct {
	Decl *ClassDeclaration
	pos  Pos
}

func (*ClassExpression) nodeMarker()   {}
func (*ClassExpression) exprMarker()   {}
func (c *ClassExpression) GetPos() Pos { return c.pos }

func NewClassExpression(decl *ClassDeclaration, pos Pos) *ClassExpression {
	return &ClassExpression{Decl: decl, pos: pos}
}

// --- Template literals ---

// TemplateLiteral represents a template literal `text ${expr} text`.
// Quasis has exactly len(Exprs)+1 elements: the string segments around expressions.
type TemplateLiteral struct {
	Quasis []string     // cooked string segments
	Exprs  []Expression // interpolated expressions
	pos    Pos
}

func (*TemplateLiteral) nodeMarker()   {}
func (*TemplateLiteral) exprMarker()   {}
func (t *TemplateLiteral) GetPos() Pos { return t.pos }

func NewTemplateLiteral(quasis []string, exprs []Expression, pos Pos) *TemplateLiteral {
	return &TemplateLiteral{Quasis: quasis, Exprs: exprs, pos: pos}
}

// TaggedTemplateExpression represents “ tag`text ${expr} text` “ — a
// template literal immediately preceded by the callable expression that
// tags it. Quasis/Exprs have the same "len(Quasis) == len(Exprs)+1" shape
// TemplateLiteral's own do; Quasis holds only the cooked form (no `.raw` —
// see TDD-00059's Context for why). Kept as its own node rather than
// desugared straight to a CallExpression at parse time so "this was
// written as a tagged template" stays recoverable (codegen desugars it on
// demand instead — see desugarTaggedTemplate in codegen/llvm).
type TaggedTemplateExpression struct {
	Tag    Expression
	Quasis []string
	Exprs  []Expression
	pos    Pos
}

func (*TaggedTemplateExpression) nodeMarker()   {}
func (*TaggedTemplateExpression) exprMarker()   {}
func (t *TaggedTemplateExpression) GetPos() Pos { return t.pos }

func NewTaggedTemplateExpression(tag Expression, quasis []string, exprs []Expression, pos Pos) *TaggedTemplateExpression {
	return &TaggedTemplateExpression{Tag: tag, Quasis: quasis, Exprs: exprs, pos: pos}
}

// NewMapExpression — new Map<K, V>() or new Map<K, V>(entries) where entries
// is a [K, V][] array (TDD-00066). Init is the optional initial-entries
// argument, nil for the no-argument form.
type NewMapExpression struct {
	KeyType *TypeAnnotation
	ValType *TypeAnnotation
	Init    Expression
	pos     Pos
}

func (*NewMapExpression) nodeMarker()   {}
func (*NewMapExpression) exprMarker()   {}
func (n *NewMapExpression) GetPos() Pos { return n.pos }

func NewNewMapExpression(key, val *TypeAnnotation, init Expression, pos Pos) *NewMapExpression {
	return &NewMapExpression{KeyType: key, ValType: val, Init: init, pos: pos}
}

// NewSetExpression — new Set<T>() or new Set<T>(iterable) (ADR-00159:
// Init is the optional initial-elements argument, nil for the
// no-argument form).
type NewSetExpression struct {
	ElemType *TypeAnnotation
	Init     Expression
	pos      Pos
}

func (*NewSetExpression) nodeMarker()   {}
func (*NewSetExpression) exprMarker()   {}
func (n *NewSetExpression) GetPos() Pos { return n.pos }

func NewNewSetExpression(elem *TypeAnnotation, init Expression, pos Pos) *NewSetExpression {
	return &NewSetExpression{ElemType: elem, Init: init, pos: pos}
}

// NewWeakMapExpression — new WeakMap<K, V>() (TDD-00112). Object-identity-keyed,
// no initial-entries argument (unlike Map).
type NewWeakMapExpression struct {
	KeyType *TypeAnnotation
	ValType *TypeAnnotation
	pos     Pos
}

func (*NewWeakMapExpression) nodeMarker()   {}
func (*NewWeakMapExpression) exprMarker()   {}
func (n *NewWeakMapExpression) GetPos() Pos { return n.pos }

func NewNewWeakMapExpression(key, val *TypeAnnotation, pos Pos) *NewWeakMapExpression {
	return &NewWeakMapExpression{KeyType: key, ValType: val, pos: pos}
}

// NewWeakSetExpression — new WeakSet<T>() (TDD-00112).
type NewWeakSetExpression struct {
	ElemType *TypeAnnotation
	pos      Pos
}

func (*NewWeakSetExpression) nodeMarker()   {}
func (*NewWeakSetExpression) exprMarker()   {}
func (n *NewWeakSetExpression) GetPos() Pos { return n.pos }

func NewNewWeakSetExpression(elem *TypeAnnotation, pos Pos) *NewWeakSetExpression {
	return &NewWeakSetExpression{ElemType: elem, pos: pos}
}

// NewWeakRefExpression — new WeakRef(obj) (TDD-00112). Init is the referent.
type NewWeakRefExpression struct {
	ElemType *TypeAnnotation
	Init     Expression
	pos      Pos
}

func (*NewWeakRefExpression) nodeMarker()   {}
func (*NewWeakRefExpression) exprMarker()   {}
func (n *NewWeakRefExpression) GetPos() Pos { return n.pos }

func NewNewWeakRefExpression(elem *TypeAnnotation, init Expression, pos Pos) *NewWeakRefExpression {
	return &NewWeakRefExpression{ElemType: elem, Init: init, pos: pos}
}

// NewEventEmitterExpression — `new EventEmitter<T>()` (TDD-00023). Like
// NewMapExpression/NewSetExpression, restricted to a variable declaration's
// initializer, not a general expression.
type NewEventEmitterExpression struct {
	PayloadType *TypeAnnotation
	pos         Pos
}

func (*NewEventEmitterExpression) nodeMarker()   {}
func (*NewEventEmitterExpression) exprMarker()   {}
func (n *NewEventEmitterExpression) GetPos() Pos { return n.pos }

func NewNewEventEmitterExpression(payload *TypeAnnotation, pos Pos) *NewEventEmitterExpression {
	return &NewEventEmitterExpression{PayloadType: payload, pos: pos}
}

// NewReadableStreamExpression — `new ReadableStream<T>(underlyingSource?,
// strategy?)` (TDD-00097 Stage 1). The underlying source must be an object
// literal (its start/pull/cancel members are destructured at compile time);
// the strategy argument may be an object literal ({highWaterMark, size}) or a
// `new CountQueuingStrategy(...)`/`new ByteLengthQueuingStrategy(...)`
// expression, both validated in codegen.
type NewReadableStreamExpression struct {
	ChunkType *TypeAnnotation // T in ReadableStream<T>; nil → number
	Source    Expression      // underlying source object literal, or nil
	Strategy  Expression      // queuing strategy, or nil
	pos       Pos
}

func (*NewReadableStreamExpression) nodeMarker()   {}
func (*NewReadableStreamExpression) exprMarker()   {}
func (n *NewReadableStreamExpression) GetPos() Pos { return n.pos }

func NewNewReadableStreamExpression(chunk *TypeAnnotation, source, strategy Expression, pos Pos) *NewReadableStreamExpression {
	return &NewReadableStreamExpression{ChunkType: chunk, Source: source, Strategy: strategy, pos: pos}
}

// NewWritableStreamExpression — `new WritableStream<T>(underlyingSink?,
// strategy?)` (TDD-00097 Stage 2). Mirrors NewReadableStreamExpression.
type NewWritableStreamExpression struct {
	ChunkType *TypeAnnotation // T; nil → number
	Sink      Expression      // underlying sink object literal, or nil
	Strategy  Expression      // queuing strategy, or nil
	pos       Pos
}

func (*NewWritableStreamExpression) nodeMarker()   {}
func (*NewWritableStreamExpression) exprMarker()   {}
func (n *NewWritableStreamExpression) GetPos() Pos { return n.pos }

func NewNewWritableStreamExpression(chunk *TypeAnnotation, sink, strategy Expression, pos Pos) *NewWritableStreamExpression {
	return &NewWritableStreamExpression{ChunkType: chunk, Sink: sink, Strategy: strategy, pos: pos}
}

// NewTransformStreamExpression — `new TransformStream<I, O>(transformer?,
// writableStrategy?, readableStrategy?)` (TDD-00097 Stage 3).
type NewTransformStreamExpression struct {
	InType           *TypeAnnotation // I; nil → number
	OutType          *TypeAnnotation // O; nil → number
	Transformer      Expression      // {transform, flush} object literal, or nil
	WritableStrategy Expression
	ReadableStrategy Expression
	pos              Pos
}

func (*NewTransformStreamExpression) nodeMarker()   {}
func (*NewTransformStreamExpression) exprMarker()   {}
func (n *NewTransformStreamExpression) GetPos() Pos { return n.pos }

func NewNewTransformStreamExpression(in, out *TypeAnnotation, transformer, wstrat, rstrat Expression, pos Pos) *NewTransformStreamExpression {
	return &NewTransformStreamExpression{InType: in, OutType: out, Transformer: transformer, WritableStrategy: wstrat, ReadableStrategy: rstrat, pos: pos}
}

// NewCompressionStreamExpression — `new CompressionStream(format)` /
// `new DecompressionStream(format)` (TDD-00097 Stage 6). Decompress selects
// the inflate direction; Format must be a string literal ("gzip", "deflate",
// "deflate-raw"), validated at codegen.
type NewCompressionStreamExpression struct {
	Decompress bool
	Format     Expression
	pos        Pos
}

func (*NewCompressionStreamExpression) nodeMarker()   {}
func (*NewCompressionStreamExpression) exprMarker()   {}
func (n *NewCompressionStreamExpression) GetPos() Pos { return n.pos }

func NewNewCompressionStreamExpression(decompress bool, format Expression, pos Pos) *NewCompressionStreamExpression {
	return &NewCompressionStreamExpression{Decompress: decompress, Format: format, pos: pos}
}

// NewNodeStreamExpression — `new Readable<T>(opts?)` / `new Writable<T>(opts?)`
// / `new Transform<I, O>(opts?)` (TDD-00097 Stage 8, Node's stream module).
type NewNodeStreamExpression struct {
	Kind    string // "readable" | "writable" | "transform"
	InType  *TypeAnnotation
	OutType *TypeAnnotation
	Options Expression // object literal, or nil
	pos     Pos
}

func (*NewNodeStreamExpression) nodeMarker()   {}
func (*NewNodeStreamExpression) exprMarker()   {}
func (n *NewNodeStreamExpression) GetPos() Pos { return n.pos }

func NewNewNodeStreamExpression(kind string, in, out *TypeAnnotation, options Expression, pos Pos) *NewNodeStreamExpression {
	return &NewNodeStreamExpression{Kind: kind, InType: in, OutType: out, Options: options, pos: pos}
}

// EnumMember is one member of an enum declaration.
type EnumMember struct {
	Name  string
	Value Expression // nil → auto-increment (numeric) or required (string enum)
}

// EnumDeclaration — `[const] enum Name { A [= expr], B, ... }`
type EnumDeclaration struct {
	Name    string
	Const   bool
	Members []EnumMember
	pos     Pos
}

func (*EnumDeclaration) nodeMarker()   {}
func (*EnumDeclaration) stmtMarker()   {}
func (e *EnumDeclaration) GetPos() Pos { return e.pos }

func NewEnumDeclaration(name string, isConst bool, members []EnumMember, pos Pos) *EnumDeclaration {
	return &EnumDeclaration{Name: name, Const: isConst, Members: members, pos: pos}
}

// ThrowStatement — `throw expr`
type ThrowStatement struct {
	Argument Expression
	pos      Pos
}

func (*ThrowStatement) nodeMarker()   {}
func (*ThrowStatement) stmtMarker()   {}
func (t *ThrowStatement) GetPos() Pos { return t.pos }

func NewThrowStatement(arg Expression, pos Pos) *ThrowStatement {
	return &ThrowStatement{Argument: arg, pos: pos}
}

// TryStatement — `try { } catch (e) { } finally { }`
type TryStatement struct {
	Body    *BlockStatement
	Catch   *CatchClause    // nil if absent
	Finally *BlockStatement // nil if absent
	pos     Pos
}

func (*TryStatement) nodeMarker()   {}
func (*TryStatement) stmtMarker()   {}
func (t *TryStatement) GetPos() Pos { return t.pos }

func NewTryStatement(body *BlockStatement, catch *CatchClause, finally *BlockStatement, pos Pos) *TryStatement {
	return &TryStatement{Body: body, Catch: catch, Finally: finally, pos: pos}
}

type CatchClause struct {
	Param string
	// ObjectPattern is non-nil for a destructured catch binding
	// (`catch ({ message, name }) { ... }`) — Param is unused in that case.
	// No array-pattern form: the caught value's static shape (errorObjType)
	// has no numeric-indexed fields to destructure into, so there's nothing
	// a `catch ([a, b])` binding could ever usefully bind.
	ObjectPattern []DestructProp
	Body          *BlockStatement
	// Pos is the catch clause's own position (the `catch` keyword) — used
	// for a destructured binding's own error messages (an unknown field
	// name, etc.); Param's plain-identifier form doesn't need its own
	// separate position since IDENT-not-found errors point at the token.
	Pos Pos
}

// NewErrorExpression — `new Error("message")`, or one of its built-in
// subtypes (`new TypeError("message")`, etc. — TDD-00013 Option A). Kind is
// always one of the registered names in codegen/llvm's errorKinds table.
// Name is set only for `new DOMException(message, name)`, whose runtime name
// is the second constructor argument rather than fixed to Kind (nil → "Error").
type NewErrorExpression struct {
	Kind    string
	Message Expression // nil if no argument
	Name    Expression // DOMException's 2nd arg; nil for the fixed-name kinds
	Errors  Expression // AggregateError's 1st arg (the aggregated errors); nil otherwise
	pos     Pos
}

func (*NewErrorExpression) nodeMarker()   {}
func (*NewErrorExpression) exprMarker()   {}
func (n *NewErrorExpression) GetPos() Pos { return n.pos }

func NewNewErrorExpression(kind string, msg Expression, pos Pos) *NewErrorExpression {
	return &NewErrorExpression{Kind: kind, Message: msg, pos: pos}
}

// NewDateExpression is `new Date()` (current time), `new Date(ms)` (an
// explicit timestamp or an ISO string), or the multi-argument calendar form
// `new Date(year, month, day?, hours?, minutes?, seconds?, ms?)` — month is
// 0-indexed, matching real JS's convention (and getMonth()'s).
type NewDateExpression struct {
	Millis Expression   // nil for the no-arg (current time) form and the multi-arg form
	Args   []Expression // non-nil only for the 2+ argument calendar form
	pos    Pos
}

func (*NewDateExpression) nodeMarker()   {}
func (*NewDateExpression) exprMarker()   {}
func (n *NewDateExpression) GetPos() Pos { return n.pos }

func NewNewDateExpression(millis Expression, pos Pos) *NewDateExpression {
	return &NewDateExpression{Millis: millis, pos: pos}
}

func NewNewDateExpressionMulti(args []Expression, pos Pos) *NewDateExpression {
	return &NewDateExpression{Args: args, pos: pos}
}

// NewURLExpression is `new URL(url)` — a single required string argument.
// No base-URL second argument (V1 scope — out of scope independent of
// Request/Headers, which now exist for real, see NewRequestExpression/
// NewHeadersExpression below, TDD-00040).
type NewURLExpression struct {
	URL Expression
	pos Pos
}

func (*NewURLExpression) nodeMarker()   {}
func (*NewURLExpression) exprMarker()   {}
func (n *NewURLExpression) GetPos() Pos { return n.pos }

func NewNewURLExpression(url Expression, pos Pos) *NewURLExpression {
	return &NewURLExpression{URL: url, pos: pos}
}

// NewURLPatternExpression is `new URLPattern()` / `new URLPattern(init)` —
// init, when present, must be an object literal with any subset of the six
// supported component patterns (protocol/hostname/port/pathname/search/hash);
// codegen enforces that shape (TDD-00100). The constructor-string form and a
// baseURL second argument are out of scope.
type NewURLPatternExpression struct {
	Init Expression
	pos  Pos
}

func (*NewURLPatternExpression) nodeMarker()   {}
func (*NewURLPatternExpression) exprMarker()   {}
func (n *NewURLPatternExpression) GetPos() Pos { return n.pos }

func NewNewURLPatternExpression(init Expression, pos Pos) *NewURLPatternExpression {
	return &NewURLPatternExpression{Init: init, pos: pos}
}

// NewEventSourceExpression is `new EventSource(url)` — a single required
// string argument, matching the real Web platform's own narrow constructor
// (a `{ withCredentials }` second argument is the only other thing real
// EventSource accepts, and has no meaning without cookies/credentialed
// requests in this compiler's fetch model either — see docs/tdd/TDD-00038.md).
type NewEventSourceExpression struct {
	URL Expression
	pos Pos
}

func (*NewEventSourceExpression) nodeMarker()   {}
func (*NewEventSourceExpression) exprMarker()   {}
func (n *NewEventSourceExpression) GetPos() Pos { return n.pos }

func NewNewEventSourceExpression(url Expression, pos Pos) *NewEventSourceExpression {
	return &NewEventSourceExpression{URL: url, pos: pos}
}

// NewWebSocketExpression is `new WebSocket(url)` (TDD-00039 Stage 3) — a
// single required string argument (`ws://host:port/path` only; `wss://` is
// rejected at construction, see emit_websocket_client.go), matching the
// real Web platform's own constructor shape (a `protocols` second argument
// negotiates a subprotocol, which has no equivalent in this compiler's
// model and is simply omitted rather than accepted-and-ignored, the same
// choice NewEventSourceExpression's own doc comment made for EventSource's
// second argument).
type NewWebSocketExpression struct {
	URL Expression
	pos Pos
}

func (*NewWebSocketExpression) nodeMarker()   {}
func (*NewWebSocketExpression) exprMarker()   {}
func (n *NewWebSocketExpression) GetPos() Pos { return n.pos }

// NewWorkerExpression is `new Worker('./file.ts', { workerData: v })`
// (TDD-00098) — the path must be a compile-time string literal (there is no
// interpreter to load code at runtime; the worker file is compiled into the
// same binary as its own entry function), resolved relative to the file
// containing this expression. The optional second argument is an object
// literal whose only recognized property is `workerData`.
type NewWorkerExpression struct {
	Path string // raw literal as written
	// ResolvedPath is the canonical absolute path, set by the resolver's
	// rename walk — the key that matches a WorkerModule.Path.
	ResolvedPath string
	WorkerData   Expression // nil when no { workerData } option is given
	pos          Pos
}

func (*NewWorkerExpression) nodeMarker()   {}
func (*NewWorkerExpression) exprMarker()   {}
func (n *NewWorkerExpression) GetPos() Pos { return n.pos }

func NewNewWorkerExpression(path string, workerData Expression, pos Pos) *NewWorkerExpression {
	return &NewWorkerExpression{Path: path, WorkerData: workerData, pos: pos}
}

func NewNewWebSocketExpression(url Expression, pos Pos) *NewWebSocketExpression {
	return &NewWebSocketExpression{URL: url, pos: pos}
}

// NewURLSearchParamsExpression is `new URLSearchParams()` (empty) or
// `new URLSearchParams(init)` (parses a query string, with or without a
// leading '?').
type NewURLSearchParamsExpression struct {
	Init Expression // nil for the no-argument form
	pos  Pos
}

func (*NewURLSearchParamsExpression) nodeMarker()   {}
func (*NewURLSearchParamsExpression) exprMarker()   {}
func (n *NewURLSearchParamsExpression) GetPos() Pos { return n.pos }

func NewNewURLSearchParamsExpression(init Expression, pos Pos) *NewURLSearchParamsExpression {
	return &NewURLSearchParamsExpression{Init: init, pos: pos}
}

// NewHeadersExpression is `new Headers()` (empty) or `new Headers(init)`
// (init: Map<string,string>, TDD-00040) — see codegen/llvm's IsHeaders doc
// comment for why this is just a flagged Map<string,string> under the hood.
type NewHeadersExpression struct {
	Init Expression // nil for the no-argument form
	pos  Pos
}

func (*NewHeadersExpression) nodeMarker()   {}
func (*NewHeadersExpression) exprMarker()   {}
func (n *NewHeadersExpression) GetPos() Pos { return n.pos }

func NewNewHeadersExpression(init Expression, pos Pos) *NewHeadersExpression {
	return &NewHeadersExpression{Init: init, pos: pos}
}

// NewAbortControllerExpression is `new AbortController()` (TDD-00081 Stage 3).
type NewAbortControllerExpression struct {
	pos Pos
}

func (*NewAbortControllerExpression) nodeMarker()   {}
func (*NewAbortControllerExpression) exprMarker()   {}
func (n *NewAbortControllerExpression) GetPos() Pos { return n.pos }

func NewNewAbortControllerExpression(pos Pos) *NewAbortControllerExpression {
	return &NewAbortControllerExpression{pos: pos}
}

// NewEventTargetExpression is `new EventTarget()` (WHATWG event bus, TDD-00081
// Stage 2).
type NewEventTargetExpression struct {
	pos Pos
}

func (*NewEventTargetExpression) nodeMarker()   {}
func (*NewEventTargetExpression) exprMarker()   {}
func (n *NewEventTargetExpression) GetPos() Pos { return n.pos }

func NewNewEventTargetExpression(pos Pos) *NewEventTargetExpression {
	return &NewEventTargetExpression{pos: pos}
}

// NewEventExpression is `new Event(type)` (WHATWG Event, TDD-00081 Stage 1).
type NewEventExpression struct {
	TypeArg Expression // the event type string
	pos     Pos
}

func (*NewEventExpression) nodeMarker()   {}
func (*NewEventExpression) exprMarker()   {}
func (n *NewEventExpression) GetPos() Pos { return n.pos }

func NewNewEventExpression(typeArg Expression, pos Pos) *NewEventExpression {
	return &NewEventExpression{TypeArg: typeArg, pos: pos}
}

// NewCustomEventExpression is `new CustomEvent(type, { detail })` (TDD-00081
// Stage 1). Detail is the value of the init object's `detail` property (nil if
// absent), extracted at parse time so codegen doesn't re-inspect the literal.
type NewCustomEventExpression struct {
	TypeArg Expression
	Detail  Expression // nil if the init object omitted `detail`
	pos     Pos
}

func (*NewCustomEventExpression) nodeMarker()   {}
func (*NewCustomEventExpression) exprMarker()   {}
func (n *NewCustomEventExpression) GetPos() Pos { return n.pos }

func NewNewCustomEventExpression(typeArg, detail Expression, pos Pos) *NewCustomEventExpression {
	return &NewCustomEventExpression{TypeArg: typeArg, Detail: detail, pos: pos}
}

// NewRequestExpression is `new Request(url)` or `new Request(url, init)`
// (TDD-00040) — init is any value with some subset of method: string /
// headers: Map<string,string> | Headers / body: string fields, the same
// structural-typing shape fetch(url, init)'s own second argument already
// established (ADR-00074/TDD-00017).
type NewRequestExpression struct {
	URL  Expression
	Init Expression // nil for the no-init form
	pos  Pos
}

func (*NewRequestExpression) nodeMarker()   {}
func (*NewRequestExpression) exprMarker()   {}
func (n *NewRequestExpression) GetPos() Pos { return n.pos }

func NewNewRequestExpression(url, init Expression, pos Pos) *NewRequestExpression {
	return &NewRequestExpression{URL: url, Init: init, pos: pos}
}

// NewXMLHttpRequestExpression is `new XMLHttpRequest()` — no arguments,
// matching the real constructor's own empty parameter list. See
// docs/tdd/TDD-00040.md for this implementation's synchronous-style scope.
type NewXMLHttpRequestExpression struct {
	pos Pos
}

func (*NewXMLHttpRequestExpression) nodeMarker()   {}
func (*NewXMLHttpRequestExpression) exprMarker()   {}
func (n *NewXMLHttpRequestExpression) GetPos() Pos { return n.pos }

func NewNewXMLHttpRequestExpression(pos Pos) *NewXMLHttpRequestExpression {
	return &NewXMLHttpRequestExpression{pos: pos}
}

// NewArrayBufferExpression is `new ArrayBuffer(byteLength)` — a fixed-length,
// zero-initialized raw byte buffer. Unlike Array/Map/Set/TypedArray below,
// this is a general expression (not restricted to a variable-declaration
// initializer), matching Date/URL/URLSearchParams.
// NewDataViewExpression is `new DataView(buffer, byteOffset?, byteLength?)`
// — an arbitrary-endian read/write view over an ArrayBuffer sub-range.
type NewDataViewExpression struct {
	Buffer     Expression
	ByteOffset Expression // nil when omitted (0)
	ByteLength Expression // nil when omitted (buffer length - offset)
	pos        Pos
}

func (*NewDataViewExpression) nodeMarker()   {}
func (*NewDataViewExpression) exprMarker()   {}
func (n *NewDataViewExpression) GetPos() Pos { return n.pos }

func NewNewDataViewExpression(buffer, byteOffset, byteLength Expression, pos Pos) *NewDataViewExpression {
	return &NewDataViewExpression{Buffer: buffer, ByteOffset: byteOffset, ByteLength: byteLength, pos: pos}
}

// NewBlobExpression is `new Blob(parts?, options?)` (TDD-00102) — an
// immutable binary value with a MIME type. Parts is usually an inline
// ArrayLiteral of strings/TypedArrays/ArrayBuffers/Blobs; Options an object
// literal carrying { type }. Both nil-able. A general expression.
type NewBlobExpression struct {
	Parts   Expression
	Options Expression
	pos     Pos
}

func (*NewBlobExpression) nodeMarker()   {}
func (*NewBlobExpression) exprMarker()   {}
func (n *NewBlobExpression) GetPos() Pos { return n.pos }

func NewNewBlobExpression(parts, options Expression, pos Pos) *NewBlobExpression {
	return &NewBlobExpression{Parts: parts, Options: options, pos: pos}
}

type NewArrayBufferExpression struct {
	ByteLength Expression
	// Shared marks `new SharedArrayBuffer(byteLength)` — identical layout
	// and construction, but the result crosses a worker boundary by
	// reference instead of being deep-copied (TDD-00099).
	Shared bool
	pos    Pos
}

func (*NewArrayBufferExpression) nodeMarker()   {}
func (*NewArrayBufferExpression) exprMarker()   {}
func (n *NewArrayBufferExpression) GetPos() Pos { return n.pos }

func NewNewArrayBufferExpression(byteLength Expression, pos Pos) *NewArrayBufferExpression {
	return &NewArrayBufferExpression{ByteLength: byteLength, pos: pos}
}

// NewBroadcastChannelExpression is `new BroadcastChannel('name')`
// (TDD-00099) — a process-wide pub/sub endpoint. The channel name must be a
// compile-time string literal: it keys the compile-time per-name message
// type, the same posture as new Worker's path literal.
type NewBroadcastChannelExpression struct {
	Name string
	pos  Pos
}

func (*NewBroadcastChannelExpression) nodeMarker()   {}
func (*NewBroadcastChannelExpression) exprMarker()   {}
func (n *NewBroadcastChannelExpression) GetPos() Pos { return n.pos }

func NewNewBroadcastChannelExpression(name string, pos Pos) *NewBroadcastChannelExpression {
	return &NewBroadcastChannelExpression{Name: name, pos: pos}
}

// NewMessageChannelExpression is `new MessageChannel<T>()` (TDD-00099) — a
// linked pair of MessagePorts. The explicit type argument declares the
// (single, symmetric) message type both ports carry; omitted, it defaults to
// number.
type NewMessageChannelExpression struct {
	TypeArg *TypeAnnotation // nil when omitted
	pos     Pos
}

func (*NewMessageChannelExpression) nodeMarker()   {}
func (*NewMessageChannelExpression) exprMarker()   {}
func (n *NewMessageChannelExpression) GetPos() Pos { return n.pos }

func NewNewMessageChannelExpression(typeArg *TypeAnnotation, pos Pos) *NewMessageChannelExpression {
	return &NewMessageChannelExpression{TypeArg: typeArg, pos: pos}
}

// NewTypedArrayExpression is `new Int8Array(...)`/`new Uint8Array(...)`/.../
// `new Float64Array(...)` — ElemKind identifies which of the 8 supported
// constructor names matched (see docs/tdd/TDD-00018.md), and Arg is the
// single constructor argument, whose *runtime* type (not knowable at parse
// time) decides which of three construction forms applies: a plain size
// (own new buffer), an existing ArrayBuffer (a view sharing its memory), or
// a number[]/another TypedArray (copy-construct). Like NewArrayExpression/
// NewMapExpression/NewSetExpression, this is restricted to a variable
// declaration's initializer — not a general expression.
// ByteOffset/Length carry the optional 2nd/3rd constructor arguments of the
// sub-range view form `new XArray(buffer, byteOffset, length?)` — both nil
// for the single-argument forms.
type NewTypedArrayExpression struct {
	ElemKind   string
	Arg        Expression
	ByteOffset Expression
	Length     Expression
	pos        Pos
}

func (*NewTypedArrayExpression) nodeMarker()   {}
func (*NewTypedArrayExpression) exprMarker()   {}
func (n *NewTypedArrayExpression) GetPos() Pos { return n.pos }

func NewNewTypedArrayExpression(elemKind string, arg Expression, pos Pos) *NewTypedArrayExpression {
	return &NewTypedArrayExpression{ElemKind: elemKind, Arg: arg, pos: pos}
}

// NewTextEncoderExpression is `new TextEncoder()` — no arguments. See
// docs/status/ENCODING-TEXT.md.
type NewTextEncoderExpression struct {
	pos Pos
}

func (*NewTextEncoderExpression) nodeMarker()   {}
func (*NewTextEncoderExpression) exprMarker()   {}
func (n *NewTextEncoderExpression) GetPos() Pos { return n.pos }

func NewNewTextEncoderExpression(pos Pos) *NewTextEncoderExpression {
	return &NewTextEncoderExpression{pos: pos}
}

// NewTextDecoderExpression is `new TextDecoder()` (default, UTF-8) or
// `new TextDecoder(label)` — Label is evaluated for side effects but
// otherwise ignored (V1 scope: UTF-8 only, no encoding validation). See
// docs/status/ENCODING-TEXT.md.
type NewTextDecoderExpression struct {
	Label Expression // nil for the no-argument form
	pos   Pos
}

func (*NewTextDecoderExpression) nodeMarker()   {}
func (*NewTextDecoderExpression) exprMarker()   {}
func (n *NewTextDecoderExpression) GetPos() Pos { return n.pos }

func NewNewTextDecoderExpression(label Expression, pos Pos) *NewTextDecoderExpression {
	return &NewTextDecoderExpression{Label: label, pos: pos}
}

// NewRegExpExpression is `new RegExp(pattern, flags?)` — Flags is nil for
// the 1-arg form. A `/pattern/flags` regex literal desugars to this same
// node at parse time (parsePrimary's `case lexer.REGEX`), with both Pattern
// and Flags always present as string literals there — so codegen only ever
// sees this one shape regardless of surface syntax. See
// docs/tdd/TDD-00035.md.
type NewRegExpExpression struct {
	Pattern Expression
	Flags   Expression // nil for the no-flags constructor form
	pos     Pos
}

func (*NewRegExpExpression) nodeMarker()   {}
func (*NewRegExpExpression) exprMarker()   {}
func (n *NewRegExpExpression) GetPos() Pos { return n.pos }

func NewNewRegExpExpression(pattern, flags Expression, pos Pos) *NewRegExpExpression {
	return &NewRegExpExpression{Pattern: pattern, Flags: flags, pos: pos}
}

// NewExpression — `new ClassName(args)` for a user-defined class. Unlike
// Array/Map/Set/Error/Date/URL/URLSearchParams/ArrayBuffer/TypedArray/
// TextEncoder/TextDecoder/RegExp above (each its own hardcoded node, keyed on
// the literal callee name at parse time), this is the generic fallthrough for
// any `new <Name>` where Name isn't one of those builtins.
type NewExpression struct {
	ClassName string
	// TypeArgs is non-nil only for `new ClassName<T>(args)` against a generic
	// class (TDD-00010 V1) — nil for every non-generic `new`. Unlike a bare
	// generic function call, `new` unambiguously starts a constructor call,
	// so this doesn't hit the `a<b>(c)` grammar ambiguity that keeps explicit
	// call-site type arguments out of V1 for plain function calls.
	TypeArgs []*TypeAnnotation
	Args     []Expression
	pos      Pos
}

func (*NewExpression) nodeMarker()   {}
func (*NewExpression) exprMarker()   {}
func (n *NewExpression) GetPos() Pos { return n.pos }

func NewNewExpression(className string, args []Expression, pos Pos) *NewExpression {
	return &NewExpression{ClassName: className, Args: args, pos: pos}
}

// InterfaceDeclaration — `interface Name { field: type; ... }`
type InterfaceDeclaration struct {
	Name       string
	TypeParams           []string // e.g. ["T"] for `interface Box<T>` — TDD-00010 V1, single param only
	TypeParamConstraints []*TypeAnnotation
	Fields     []AnnotField
	Methods    []InterfaceMethodSig // TDD-00009 Stage 4 — method signatures, for `implements` conformance checking
	pos        Pos
}

func (*InterfaceDeclaration) nodeMarker()   {}
func (*InterfaceDeclaration) stmtMarker()   {}
func (i *InterfaceDeclaration) GetPos() Pos { return i.pos }

func NewInterfaceDeclaration(name string, fields []AnnotField, methods []InterfaceMethodSig, pos Pos) *InterfaceDeclaration {
	return &InterfaceDeclaration{Name: name, Fields: fields, Methods: methods, pos: pos}
}

// InterfaceMethodSig is a method signature declared inside an interface
// body (TDD-00009 Stage 4, for `implements` conformance checking) —
// signature only, never a body, so it's a distinct lightweight node rather
// than reusing FunctionDeclaration (which always carries a Body field that
// would only ever be nil here).
type InterfaceMethodSig struct {
	Name       string
	Params     []Param
	ReturnType *TypeAnnotation
}

// ClassDeclaration — `class Name { field: type; ...; constructor(...) {...} method(...) {...} }`.
// Fields carry no initializers — matching real usage, initial values are
// assigned via `this.field = value` statements in the constructor body.
// Constructor and each entry in Methods are shaped exactly like a
// FunctionDeclaration (params/return type/block body); the implicit `this`
// receiver every one of them has is a codegen-time concern (TDD-00009 Stage
// 1), not part of this node's shape.
type ClassDeclaration struct {
	Name       string
	TypeParams           []string // e.g. ["T"] for `class Box<T>` — TDD-00010 V1, single param only
	TypeParamConstraints []*TypeAnnotation
	BaseClass  string   // "" if no `extends` clause (TDD-00009 Stage 3)
	// BaseTypeArgs is non-nil only for `extends EventEmitter<T>` (TDD-00023)
	// — the sole generic `extends` target this compiler currently supports.
	BaseTypeArgs []*TypeAnnotation
	Fields       []AnnotField
	Constructor  *FunctionDeclaration // nil if omitted
	Methods      []*FunctionDeclaration
	// IsAbstract/Implements/StaticBlocks are TDD-00009 Stage 4.
	// StaticBlocks are concatenated in declaration order into one
	// per-class initializer, run once before any top-level statement.
	IsAbstract   bool
	Implements   []string
	StaticBlocks []*BlockStatement
	pos          Pos
}

func (*ClassDeclaration) nodeMarker()   {}
func (*ClassDeclaration) stmtMarker()   {}
func (c *ClassDeclaration) GetPos() Pos { return c.pos }

func NewClassDeclaration(name, baseClass string, baseTypeArgs []*TypeAnnotation, isAbstract bool, implements []string, fields []AnnotField, ctor *FunctionDeclaration, methods []*FunctionDeclaration, staticBlocks []*BlockStatement, pos Pos) *ClassDeclaration {
	return &ClassDeclaration{Name: name, BaseClass: baseClass, BaseTypeArgs: baseTypeArgs, IsAbstract: isAbstract, Implements: implements, Fields: fields, Constructor: ctor, Methods: methods, StaticBlocks: staticBlocks, pos: pos}
}

// SuperExpression — bare `super`, valid inside a derived class's
// method/constructor body (TDD-00009 Stage 3). Reuses the existing
// CallExpression/MemberExpression machinery exactly like ThisExpression
// does: `super(args)` parses as CallExpression{Callee: *SuperExpression},
// and `super.method(args)` parses as CallExpression{Callee:
// MemberExpression{Object: *SuperExpression}} — codegen special-cases both
// shapes rather than needing dedicated AST nodes.
type SuperExpression struct {
	pos Pos
}

func (*SuperExpression) nodeMarker()   {}
func (*SuperExpression) exprMarker()   {}
func (s *SuperExpression) GetPos() Pos { return s.pos }

func NewSuperExpression(pos Pos) *SuperExpression { return &SuperExpression{pos: pos} }

// TypeAliasDeclaration — `type Name = TypeAnnotation`, optionally generic
// (`type Name<T> = ...`, TDD-00079 Stage 3).
type TypeAliasDeclaration struct {
	Name                 string
	TypeParams           []string
	TypeParamConstraints []*TypeAnnotation
	Type                 *TypeAnnotation
	pos                  Pos
}

func (*TypeAliasDeclaration) nodeMarker()   {}
func (*TypeAliasDeclaration) stmtMarker()   {}
func (t *TypeAliasDeclaration) GetPos() Pos { return t.pos }

func NewTypeAliasDeclaration(name string, ta *TypeAnnotation, pos Pos) *TypeAliasDeclaration {
	return &TypeAliasDeclaration{Name: name, Type: ta, pos: pos}
}

// ExportDeclaration wraps a top-level declaration marked with `export`
// (function/const/let/var/interface/type alias/enum). Purely a
// module-resolution marker, consumed entirely by resolver/resolver.go
// before codegen ever runs — the resolver validates and then unwraps this
// node, merging Decl directly into the combined program. codegen/llvm never
// sees this node. IsDefault marks `export default ...` (TDD-00042):
// Decl's own declared name is preserved for intra-file self-reference (or
// is already the synthetic name "default" for an anonymous
// function/class or a wrapped expression — see resolver.go's
// mangleFileDecls), and the resolver additionally exposes it under the
// export key "default" alongside whatever its own name is.
type ExportDeclaration struct {
	Decl      Statement
	IsDefault bool
	pos       Pos
}

func (*ExportDeclaration) nodeMarker()   {}
func (*ExportDeclaration) stmtMarker()   {}
func (e *ExportDeclaration) GetPos() Pos { return e.pos }

func NewExportDeclaration(decl Statement, isDefault bool, pos Pos) *ExportDeclaration {
	return &ExportDeclaration{Decl: decl, IsDefault: isDefault, pos: pos}
}

// ImportSpecifier is one `name` or `name as alias` entry in an import list.
// A default import (`import Foo from '...'`) is represented as the
// specifier {Imported: "default", Local: "Foo"} — see TDD-00042 — so it
// reuses this same validation/lookup machinery with no resolver changes.
type ImportSpecifier struct {
	Imported string
	Local    string
}

// ImportDeclaration — `import { a, b as c } from './path'`, a default
// import (`import Foo from '...'`, represented as a "default" specifier —
// see ImportSpecifier's own doc comment), or a namespace import
// (`import * as ns from '...'`, Namespace set and Specifiers empty — see
// TDD-00042). Consumed entirely by the module resolver
// (resolver/resolver.go) before codegen ever runs: resolves Source relative
// to the importing file, validates each specifier's Imported name is
// actually declared and exported there, then this node is dropped from the
// merged program. codegen/llvm never sees this node.
type ImportDeclaration struct {
	Specifiers []ImportSpecifier
	Namespace  string // local alias for `import * as ns`; empty unless this is a namespace import
	Source     string
	pos        Pos
}

func (*ImportDeclaration) nodeMarker()   {}
func (*ImportDeclaration) stmtMarker()   {}
func (i *ImportDeclaration) GetPos() Pos { return i.pos }

func NewImportDeclaration(specs []ImportSpecifier, namespace, source string, pos Pos) *ImportDeclaration {
	return &ImportDeclaration{Specifiers: specs, Namespace: namespace, Source: source, pos: pos}
}

// ExportFromDeclaration re-exports members of another module without
// binding any local name in this file (TDD-00051): `export { a, b as c }
// from './path'` (Specifiers set, All false) or `export * from './path'`
// (All true, Specifiers empty — every name the target exports except
// "default", matching real ES module semantics). Reuses ImportSpecifier's
// {Imported, Local} shape — Imported is the name in the source file, Local
// is what this file exposes it as — since it's the same name-forwarding
// problem `import { a as b }` already solved. Consumed entirely by the
// module resolver (resolver/resolver.go) before codegen ever runs; produces
// no runtime statement, same as ImportDeclaration.
type ExportFromDeclaration struct {
	Specifiers []ImportSpecifier
	All        bool
	Source     string
	pos        Pos
}

func (*ExportFromDeclaration) nodeMarker()   {}
func (*ExportFromDeclaration) stmtMarker()   {}
func (e *ExportFromDeclaration) GetPos() Pos { return e.pos }

func NewExportFromDeclaration(specs []ImportSpecifier, all bool, source string, pos Pos) *ExportFromDeclaration {
	return &ExportFromDeclaration{Specifiers: specs, All: all, Source: source, pos: pos}
}

// ImportMetaUrl represents the single expression `import.meta.url`
// (TDD-00055 Stage 1) — the parser only ever produces this node for that
// exact, complete token sequence; anything else after `import.meta` (a
// different member, or `import.meta` used alone) is a parse-time error
// rather than a node a later pass has to reject, since there's no real
// "module metadata object" value to represent otherwise. Consumed entirely
// by the module resolver (resolver/rename.go), which rewrites it in place
// to a plain string literal (that file's own absolute path as a `file://`
// URL) before codegen ever runs — codegen never sees this node.
type ImportMetaUrl struct {
	pos Pos
}

func (*ImportMetaUrl) nodeMarker()   {}
func (*ImportMetaUrl) exprMarker()   {}
func (i *ImportMetaUrl) GetPos() Pos { return i.pos }

func NewImportMetaUrl(pos Pos) *ImportMetaUrl { return &ImportMetaUrl{pos: pos} }

// --- Type annotations ---

// AnnotField is one field in an object type annotation.
type AnnotField struct {
	Name string
	Type *TypeAnnotation
	// Static/Visibility are TDD-00009 Stage 4 class-member modifiers,
	// meaningful only when this AnnotField is one of a ClassDeclaration's
	// Fields — always zero-value ("", false) for every other reuse of this
	// shared type (interface fields, plain object-type annotations), same
	// "harmless zero-value elsewhere" precedent FunctionDeclaration.IsAsync
	// already established.
	Static     bool
	Visibility string // "private" / "protected" / "" (public, default)
	// Initializer is a class field's `= expr` default (TDD-00063 Stage 1) —
	// nil when the field has none (a bare `x: T;`) and always nil for every
	// non-class reuse of this shared type (interface/object-type fields,
	// which have no initializer syntax), same harmless-zero-value precedent
	// Static/Visibility already established. When present, Type may be nil
	// (an unannotated `x = expr`, whose field type is inferred from the
	// initializer at registerClasses time).
	Initializer Expression
}

// TypeAnnotation holds the resolved type name from TS syntax or JSDoc.
// Fields is non-empty for object type annotations like { x: number; y: number }.
// ElemType is non-nil for structural array types like { x: number }[], and
// also for the single type parameter of Promise<T>/Array<T>/Set<T>.
// KeyType is non-nil only for Map<K,V> — its key type; ElemType holds the value type.
// IsFuncType is true for function type annotations like (x: number) => number.
type TypeAnnotation struct {
	Name        string // e.g. "number", "string", "int32", "uint8", "float64"
	Source      string // "ts" or "jsdoc"
	Fields      []AnnotField
	ElemType    *TypeAnnotation   // non-nil for { ... }[], or Promise<T>/Array<T>/Set<T>'s T, or Map<K,V>'s V
	KeyType     *TypeAnnotation   // non-nil only for Map<K,V> — the key type K
	TypeArgs    []*TypeAnnotation // N type arguments for a user-defined generic interface usage, e.g. Box<number, string> (TDD-00037); built-ins keep using ElemType/KeyType above, unrelated to this field
	IsFuncType  bool
	FuncParams  []TypeAnnotation // param types for function type annotations
	FuncRetType *TypeAnnotation  // return type for function type annotations
	Nullable    bool             // true for T | null or T | undefined
	// UnionMembers holds every non-null/undefined member of a T | U | ...
	// union with more than one such member (TDD-00043). nil for the common
	// single-type case (with or without Nullable) — this field only becomes
	// non-nil once there are genuinely 2+ non-null/undefined members to
	// track. When set, this TypeAnnotation's own Name/other fields describe
	// the first member for backward compatibility with code that hasn't been
	// updated to look at UnionMembers.
	UnionMembers []*TypeAnnotation
	// TupleElems holds the element type annotations of a tuple type
	// (`[T0, T1, ...]`), in order — non-nil (and non-empty) exactly for a
	// tuple. See TDD-00066.
	TupleElems []*TypeAnnotation
	// IntersectionMembers holds every member of an A & B & ... intersection
	// with 2+ members (TDD-00078). nil for the common non-intersection case.
	// Directly parallels UnionMembers, and follows the same head-copy
	// convention: when set, this TypeAnnotation's own Name/other fields
	// describe the first member, while every entry in this list — including
	// that first member — keeps its own IntersectionMembers nil. Unlike a
	// union (a runtime-tagged value that is one of its members), an
	// object-type intersection collapses at resolveType into a single merged
	// ObjectType, so this field is metadata for validation, not a runtime
	// shape.
	IntersectionMembers []*TypeAnnotation
	// IsStringLiteral marks a string-literal type (`"north"`), with its value in
	// LiteralValue (TDD-00079). Its primary use is the key argument of
	// Pick/Omit/Record (`Pick<T, "a" | "b">`), collected at the AST level before
	// resolveType; as a standalone value type it resolves to `string` (the
	// literal value is not narrowed/enforced — a disclosed V1 simplification).
	IsStringLiteral bool
	LiteralValue    string
	// keyof T (TDD-00079 Stage 2): the operand's key set. As a standalone value
	// type it resolves to string; its main use is a mapped-type source.
	IsKeyof      bool
	KeyofOperand *TypeAnnotation
	// Indexed access T[K] (TDD-00079 Stage 2). IndexKey is a string-literal type
	// (`T["name"]`) or a bare reference to a mapped key variable (`T[K]`, resolved
	// per-key during mapped-type expansion).
	IsIndexedAccess bool
	IndexObject     *TypeAnnotation
	IndexKey        *TypeAnnotation
	// Mapped type { [K in Source]: Value } (TDD-00079 Stage 2). Source is
	// `keyof T` or a string-literal union; Value may be `T[K]` (homomorphic) or a
	// concrete type. MappedOptional/MappedReadonly record the `?`/`readonly`
	// modifiers (accepted; near-no-ops in the current object model).
	IsMapped       bool
	MappedKeyVar   string
	MappedSource   *TypeAnnotation
	MappedValue    *TypeAnnotation
	MappedOptional bool
	MappedReadonly bool
	// Conditional type `CheckType extends ExtendsType ? TrueType : FalseType`
	// (TDD-00079 Stage 3). ExtendsType may contain `infer` placeholders.
	IsConditional bool
	CheckType     *TypeAnnotation
	ExtendsType   *TypeAnnotation
	TrueType      *TypeAnnotation
	FalseType     *TypeAnnotation
	// `infer R` inside a conditional's ExtendsType — binds InferName to the
	// structurally-matched sub-type.
	IsInfer   bool
	InferName string
}

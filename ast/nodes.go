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

type FunctionDeclaration struct {
	Name       string
	TypeParams []string // e.g. ["T"] for `function identity<T>(...)` — TDD-00010 V1, single param only
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
	pos        Pos
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
	// into real local bindings. Mirrors ArrayDestructuring.Names/
	// ObjectDestructuring.Props' own shapes so the same unpack codegen can
	// be shared between a destructuring statement and a destructured
	// parameter.
	ArrayPattern  []string
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
	Init   Statement  // VarDeclaration or ExpressionStatement, nil if absent
	Test   Expression // nil if absent
	Update Expression // nil if absent
	Body   *BlockStatement
	pos    Pos
}

func (*ForStatement) nodeMarker()   {}
func (*ForStatement) stmtMarker()   {}
func (f *ForStatement) GetPos() Pos { return f.pos }

func NewForStatement(init Statement, test, update Expression, body *BlockStatement, pos Pos) *ForStatement {
	return &ForStatement{Init: init, Test: test, Update: update, Body: body, pos: pos}
}

type ForOfStatement struct {
	Kind     string // "let", "const", "var"
	VarName  string
	Iterable Expression
	Body     *BlockStatement
	pos      Pos
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
}

// ArrayDestructuring — const/let [a, b] = expr
type ArrayDestructuring struct {
	Kind  string   // "let", "const", "var"
	Names []string // empty string = hole (skipped index)
	Init  Expression
	pos   Pos
}

func (*ArrayDestructuring) nodeMarker()   {}
func (*ArrayDestructuring) stmtMarker()   {}
func (a *ArrayDestructuring) GetPos() Pos { return a.pos }

func NewArrayDestructuring(kind string, names []string, init Expression, pos Pos) *ArrayDestructuring {
	return &ArrayDestructuring{Kind: kind, Names: names, Init: init, pos: pos}
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
	Value string // raw literal, e.g. "42" or "3.14"
	pos   Pos
}

func (*NumberLiteral) nodeMarker()   {}
func (*NumberLiteral) exprMarker()   {}
func (n *NumberLiteral) GetPos() Pos { return n.pos }

func NewNumberLiteral(v string, pos Pos) *NumberLiteral { return &NumberLiteral{Value: v, pos: pos} }

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

// NewMapExpression — new Map<K, V>()
type NewMapExpression struct {
	KeyType *TypeAnnotation
	ValType *TypeAnnotation
	pos     Pos
}

func (*NewMapExpression) nodeMarker()   {}
func (*NewMapExpression) exprMarker()   {}
func (n *NewMapExpression) GetPos() Pos { return n.pos }

func NewNewMapExpression(key, val *TypeAnnotation, pos Pos) *NewMapExpression {
	return &NewMapExpression{KeyType: key, ValType: val, pos: pos}
}

// NewSetExpression — new Set<T>()
type NewSetExpression struct {
	ElemType *TypeAnnotation
	pos      Pos
}

func (*NewSetExpression) nodeMarker()   {}
func (*NewSetExpression) exprMarker()   {}
func (n *NewSetExpression) GetPos() Pos { return n.pos }

func NewNewSetExpression(elem *TypeAnnotation, pos Pos) *NewSetExpression {
	return &NewSetExpression{ElemType: elem, pos: pos}
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
	Body  *BlockStatement
}

// NewErrorExpression — `new Error("message")`, or one of its built-in
// subtypes (`new TypeError("message")`, etc. — TDD-00013 Option A). Kind is
// always one of the registered names in codegen/llvm's errorKinds table.
type NewErrorExpression struct {
	Kind    string
	Message Expression // nil if no argument
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
// No base-URL second argument (V1 scope, matching how fetch's own init
// object narrows the real Request/Headers API down to a plain struct).
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

// NewArrayBufferExpression is `new ArrayBuffer(byteLength)` — a fixed-length,
// zero-initialized raw byte buffer. Unlike Array/Map/Set/TypedArray below,
// this is a general expression (not restricted to a variable-declaration
// initializer), matching Date/URL/URLSearchParams.
type NewArrayBufferExpression struct {
	ByteLength Expression
	pos        Pos
}

func (*NewArrayBufferExpression) nodeMarker()   {}
func (*NewArrayBufferExpression) exprMarker()   {}
func (n *NewArrayBufferExpression) GetPos() Pos { return n.pos }

func NewNewArrayBufferExpression(byteLength Expression, pos Pos) *NewArrayBufferExpression {
	return &NewArrayBufferExpression{ByteLength: byteLength, pos: pos}
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
type NewTypedArrayExpression struct {
	ElemKind string
	Arg      Expression
	pos      Pos
}

func (*NewTypedArrayExpression) nodeMarker()   {}
func (*NewTypedArrayExpression) exprMarker()   {}
func (n *NewTypedArrayExpression) GetPos() Pos { return n.pos }

func NewNewTypedArrayExpression(elemKind string, arg Expression, pos Pos) *NewTypedArrayExpression {
	return &NewTypedArrayExpression{ElemKind: elemKind, Arg: arg, pos: pos}
}

// NewExpression — `new ClassName(args)` for a user-defined class. Unlike
// Array/Map/Set/Error/Date/URL/URLSearchParams/ArrayBuffer/TypedArray above
// (each its own hardcoded node, keyed on the literal callee name at parse
// time), this is the generic fallthrough for any `new <Name>` where Name
// isn't one of
// those builtins.
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
	TypeParams []string // e.g. ["T"] for `interface Box<T>` — TDD-00010 V1, single param only
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
	TypeParams []string // e.g. ["T"] for `class Box<T>` — TDD-00010 V1, single param only
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

// TypeAliasDeclaration — `type Name = TypeAnnotation`
type TypeAliasDeclaration struct {
	Name string
	Type *TypeAnnotation
	pos  Pos
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
// sees this node.
type ExportDeclaration struct {
	Decl Statement
	pos  Pos
}

func (*ExportDeclaration) nodeMarker()   {}
func (*ExportDeclaration) stmtMarker()   {}
func (e *ExportDeclaration) GetPos() Pos { return e.pos }

func NewExportDeclaration(decl Statement, pos Pos) *ExportDeclaration {
	return &ExportDeclaration{Decl: decl, pos: pos}
}

// ImportSpecifier is one `name` or `name as alias` entry in an import list.
// Aliasing (Local != Imported) is parsed but not yet supported by the
// resolver (V1 scope) — parsing it anyway means real TS-shaped `as` syntax
// gets a clear "not yet supported" error instead of a raw parse failure.
type ImportSpecifier struct {
	Imported string
	Local    string
}

// ImportDeclaration — `import { a, b as c } from './path'`. Consumed
// entirely by the module resolver (resolver/resolver.go) before codegen
// ever runs: resolves Source relative to the importing file, validates each
// specifier's Imported name is actually declared and exported there, then
// this node is dropped from the merged program. codegen/llvm never sees
// this node.
type ImportDeclaration struct {
	Specifiers []ImportSpecifier
	Source     string
	pos        Pos
}

func (*ImportDeclaration) nodeMarker()   {}
func (*ImportDeclaration) stmtMarker()   {}
func (i *ImportDeclaration) GetPos() Pos { return i.pos }

func NewImportDeclaration(specs []ImportSpecifier, source string, pos Pos) *ImportDeclaration {
	return &ImportDeclaration{Specifiers: specs, Source: source, pos: pos}
}

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
	ElemType    *TypeAnnotation // non-nil for { ... }[], or Promise<T>/Array<T>/Set<T>'s T, or Map<K,V>'s V
	KeyType     *TypeAnnotation // non-nil only for Map<K,V> — the key type K
	IsFuncType  bool
	FuncParams  []TypeAnnotation // param types for function type annotations
	FuncRetType *TypeAnnotation  // return type for function type annotations
	Nullable    bool             // true for T | null or T | undefined
}

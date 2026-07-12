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

func (*BlockStatement) nodeMarker()  {}
func (*BlockStatement) stmtMarker() {}
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

func (*VarDeclaration) nodeMarker()  {}
func (*VarDeclaration) stmtMarker() {}
func (v *VarDeclaration) GetPos() Pos { return v.pos }

func NewVarDeclaration(kind, name string, ta *TypeAnnotation, init Expression, pos Pos) *VarDeclaration {
	return &VarDeclaration{Kind: kind, Name: name, TypeAnnot: ta, Init: init, pos: pos}
}

type FunctionDeclaration struct {
	Name       string
	Params     []Param
	ReturnType *TypeAnnotation
	Body       *BlockStatement
	IsAsync    bool
	pos        Pos
}

func (*FunctionDeclaration) nodeMarker()  {}
func (*FunctionDeclaration) stmtMarker() {}
func (f *FunctionDeclaration) GetPos() Pos { return f.pos }

type Param struct {
	Name     string
	Type     *TypeAnnotation
	Rest     bool       // true when declared with ...
	Default  Expression // non-nil when declared with = expr
	Optional bool       // true when declared with ?
}

type ReturnStatement struct {
	Value Expression // nil for bare return
	pos   Pos
}

func (*ReturnStatement) nodeMarker()  {}
func (*ReturnStatement) stmtMarker() {}
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

func (*ForStatement) nodeMarker()  {}
func (*ForStatement) stmtMarker() {}
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

func (*ForOfStatement) nodeMarker()  {}
func (*ForOfStatement) stmtMarker() {}
func (f *ForOfStatement) GetPos() Pos { return f.pos }

func NewForOfStatement(kind, varName string, iterable Expression, body *BlockStatement, pos Pos) *ForOfStatement {
	return &ForOfStatement{Kind: kind, VarName: varName, Iterable: iterable, Body: body, pos: pos}
}

type WhileStatement struct {
	Test Expression
	Body *BlockStatement
	pos  Pos
}

func (*WhileStatement) nodeMarker()  {}
func (*WhileStatement) stmtMarker() {}
func (w *WhileStatement) GetPos() Pos { return w.pos }

func NewWhileStatement(test Expression, body *BlockStatement, pos Pos) *WhileStatement {
	return &WhileStatement{Test: test, Body: body, pos: pos}
}

type DoWhileStatement struct {
	Body *BlockStatement
	Test Expression
	pos  Pos
}

func (*DoWhileStatement) nodeMarker()  {}
func (*DoWhileStatement) stmtMarker() {}
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

func (*ForInStatement) nodeMarker()  {}
func (*ForInStatement) stmtMarker() {}
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

func (*IfStatement) nodeMarker()  {}
func (*IfStatement) stmtMarker() {}
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

func (*SwitchStatement) nodeMarker()  {}
func (*SwitchStatement) stmtMarker() {}
func (s *SwitchStatement) GetPos() Pos { return s.pos }

func NewSwitchStatement(disc Expression, cases []SwitchCase, pos Pos) *SwitchStatement {
	return &SwitchStatement{Discriminant: disc, Cases: cases, pos: pos}
}

type BreakStatement struct {
	Label string // empty for a bare, unlabeled break
	pos   Pos
}

func (*BreakStatement) nodeMarker()  {}
func (*BreakStatement) stmtMarker() {}
func (b *BreakStatement) GetPos() Pos { return b.pos }

func NewBreakStatement(label string, pos Pos) *BreakStatement {
	return &BreakStatement{Label: label, pos: pos}
}

type ContinueStatement struct {
	Label string // empty for a bare, unlabeled continue
	pos   Pos
}

func (*ContinueStatement) nodeMarker()  {}
func (*ContinueStatement) stmtMarker() {}
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

func (*LabeledStatement) nodeMarker()  {}
func (*LabeledStatement) stmtMarker() {}
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

func (*ArrayDestructuring) nodeMarker()  {}
func (*ArrayDestructuring) stmtMarker() {}
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

func (*ObjectDestructuring) nodeMarker()  {}
func (*ObjectDestructuring) stmtMarker() {}
func (o *ObjectDestructuring) GetPos() Pos { return o.pos }

func NewObjectDestructuring(kind string, props []DestructProp, init Expression, pos Pos) *ObjectDestructuring {
	return &ObjectDestructuring{Kind: kind, Props: props, Init: init, pos: pos}
}

type ExpressionStatement struct {
	Expr Expression
	pos  Pos
}

func (*ExpressionStatement) nodeMarker()  {}
func (*ExpressionStatement) stmtMarker() {}
func (e *ExpressionStatement) GetPos() Pos { return e.pos }

func NewExpressionStatement(expr Expression, pos Pos) *ExpressionStatement {
	return &ExpressionStatement{Expr: expr, pos: pos}
}

// --- Expressions ---

type NumberLiteral struct {
	Value string // raw literal, e.g. "42" or "3.14"
	pos   Pos
}

func (*NumberLiteral) nodeMarker()  {}
func (*NumberLiteral) exprMarker() {}
func (n *NumberLiteral) GetPos() Pos { return n.pos }

func NewNumberLiteral(v string, pos Pos) *NumberLiteral { return &NumberLiteral{Value: v, pos: pos} }

type StringLiteral struct {
	Value string
	pos   Pos
}

func (*StringLiteral) nodeMarker()  {}
func (*StringLiteral) exprMarker() {}
func (s *StringLiteral) GetPos() Pos { return s.pos }

func NewStringLiteral(v string, pos Pos) *StringLiteral { return &StringLiteral{Value: v, pos: pos} }

type BooleanLiteral struct {
	Value bool
	pos   Pos
}

func (*BooleanLiteral) nodeMarker()  {}
func (*BooleanLiteral) exprMarker() {}
func (b *BooleanLiteral) GetPos() Pos { return b.pos }

func NewBooleanLiteral(v bool, pos Pos) *BooleanLiteral { return &BooleanLiteral{Value: v, pos: pos} }

// NullLiteral represents `null` (IsUndefined=false) or `undefined` (IsUndefined=true).
type NullLiteral struct {
	IsUndefined bool
	pos         Pos
}

func (*NullLiteral) nodeMarker()  {}
func (*NullLiteral) exprMarker() {}
func (n *NullLiteral) GetPos() Pos { return n.pos }

func NewNullLiteral(isUndefined bool, pos Pos) *NullLiteral {
	return &NullLiteral{IsUndefined: isUndefined, pos: pos}
}

// AwaitExpression represents `await expr`.
type AwaitExpression struct {
	Argument Expression
	pos      Pos
}

func (*AwaitExpression) nodeMarker()  {}
func (*AwaitExpression) exprMarker() {}
func (a *AwaitExpression) GetPos() Pos { return a.pos }

func NewAwaitExpression(arg Expression, pos Pos) *AwaitExpression {
	return &AwaitExpression{Argument: arg, pos: pos}
}

type Identifier struct {
	Name string
	pos  Pos
}

func (*Identifier) nodeMarker()  {}
func (*Identifier) exprMarker() {}
func (i *Identifier) GetPos() Pos { return i.pos }

func NewIdentifier(name string, pos Pos) *Identifier { return &Identifier{Name: name, pos: pos} }

type BinaryExpression struct {
	Op          string
	Left, Right Expression
	pos         Pos
}

func (*BinaryExpression) nodeMarker()  {}
func (*BinaryExpression) exprMarker() {}
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

func (*ConditionalExpression) nodeMarker()  {}
func (*ConditionalExpression) exprMarker() {}
func (c *ConditionalExpression) GetPos() Pos { return c.pos }

func NewConditionalExpression(test, consequent, alternate Expression, pos Pos) *ConditionalExpression {
	return &ConditionalExpression{Test: test, Consequent: consequent, Alternate: alternate, pos: pos}
}

// SpreadElement — ...expr inside an array literal.
type SpreadElement struct {
	Arg Expression
	pos Pos
}

func (*SpreadElement) nodeMarker()  {}
func (*SpreadElement) exprMarker()  {}
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

func (*UnaryExpression) nodeMarker()  {}
func (*UnaryExpression) exprMarker() {}
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

func (*UpdateExpression) nodeMarker()  {}
func (*UpdateExpression) exprMarker() {}
func (u *UpdateExpression) GetPos() Pos { return u.pos }

func NewUpdateExpression(op string, prefix bool, arg Expression, pos Pos) *UpdateExpression {
	return &UpdateExpression{Op: op, Prefix: prefix, Arg: arg, pos: pos}
}

type AssignmentExpression struct {
	Op          string // "=", "+=", "-=", "*=", "/="
	Left, Right Expression
	pos         Pos
}

func (*AssignmentExpression) nodeMarker()  {}
func (*AssignmentExpression) exprMarker() {}
func (a *AssignmentExpression) GetPos() Pos { return a.pos }

func NewAssignmentExpression(op string, left, right Expression, pos Pos) *AssignmentExpression {
	return &AssignmentExpression{Op: op, Left: left, Right: right, pos: pos}
}

type CallExpression struct {
	Callee Expression
	Args   []Expression
	pos    Pos
}

func (*CallExpression) nodeMarker()  {}
func (*CallExpression) exprMarker() {}
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

func (*MemberExpression) nodeMarker()  {}
func (*MemberExpression) exprMarker() {}
func (m *MemberExpression) GetPos() Pos { return m.pos }

func NewMemberExpression(obj Expression, prop string, pos Pos) *MemberExpression {
	return &MemberExpression{Object: obj, Property: prop, pos: pos}
}

type ArrayLiteral struct {
	Elements []Expression
	pos      Pos
}

func (*ArrayLiteral) nodeMarker()  {}
func (*ArrayLiteral) exprMarker() {}
func (a *ArrayLiteral) GetPos() Pos { return a.pos }

func NewArrayLiteral(elems []Expression, pos Pos) *ArrayLiteral {
	return &ArrayLiteral{Elements: elems, pos: pos}
}

type IndexExpression struct {
	Object Expression
	Index  Expression
	pos    Pos
}

func (*IndexExpression) nodeMarker()  {}
func (*IndexExpression) exprMarker() {}
func (i *IndexExpression) GetPos() Pos { return i.pos }

func NewIndexExpression(obj, index Expression, pos Pos) *IndexExpression {
	return &IndexExpression{Object: obj, Index: index, pos: pos}
}

type NewArrayExpression struct {
	ElemType *TypeAnnotation // from <T>; nil if omitted
	Size     Expression
	pos      Pos
}

func (*NewArrayExpression) nodeMarker()  {}
func (*NewArrayExpression) exprMarker() {}
func (n *NewArrayExpression) GetPos() Pos { return n.pos }

func NewNewArrayExpression(elemType *TypeAnnotation, size Expression, pos Pos) *NewArrayExpression {
	return &NewArrayExpression{ElemType: elemType, Size: size, pos: pos}
}

// --- Object expressions ---

type ObjectProperty struct {
	Key   string
	Value Expression
}

type ObjectLiteral struct {
	Properties []ObjectProperty
	pos        Pos
}

func (*ObjectLiteral) nodeMarker()  {}
func (*ObjectLiteral) exprMarker() {}
func (o *ObjectLiteral) GetPos() Pos { return o.pos }

func NewObjectLiteral(props []ObjectProperty, pos Pos) *ObjectLiteral {
	return &ObjectLiteral{Properties: props, pos: pos}
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

func (*ArrowFunction) nodeMarker()  {}
func (*ArrowFunction) exprMarker() {}
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

func (*TemplateLiteral) nodeMarker()  {}
func (*TemplateLiteral) exprMarker() {}
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

func (*NewMapExpression) nodeMarker()  {}
func (*NewMapExpression) exprMarker() {}
func (n *NewMapExpression) GetPos() Pos { return n.pos }

func NewNewMapExpression(key, val *TypeAnnotation, pos Pos) *NewMapExpression {
	return &NewMapExpression{KeyType: key, ValType: val, pos: pos}
}

// NewSetExpression — new Set<T>()
type NewSetExpression struct {
	ElemType *TypeAnnotation
	pos      Pos
}

func (*NewSetExpression) nodeMarker()  {}
func (*NewSetExpression) exprMarker() {}
func (n *NewSetExpression) GetPos() Pos { return n.pos }

func NewNewSetExpression(elem *TypeAnnotation, pos Pos) *NewSetExpression {
	return &NewSetExpression{ElemType: elem, pos: pos}
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

func (*EnumDeclaration) nodeMarker()  {}
func (*EnumDeclaration) stmtMarker() {}
func (e *EnumDeclaration) GetPos() Pos { return e.pos }

func NewEnumDeclaration(name string, isConst bool, members []EnumMember, pos Pos) *EnumDeclaration {
	return &EnumDeclaration{Name: name, Const: isConst, Members: members, pos: pos}
}

// ThrowStatement — `throw expr`
type ThrowStatement struct {
	Argument Expression
	pos      Pos
}

func (*ThrowStatement) nodeMarker()  {}
func (*ThrowStatement) stmtMarker() {}
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

func (*TryStatement) nodeMarker()  {}
func (*TryStatement) stmtMarker() {}
func (t *TryStatement) GetPos() Pos { return t.pos }

func NewTryStatement(body *BlockStatement, catch *CatchClause, finally *BlockStatement, pos Pos) *TryStatement {
	return &TryStatement{Body: body, Catch: catch, Finally: finally, pos: pos}
}

type CatchClause struct {
	Param string
	Body  *BlockStatement
}

// NewErrorExpression — `new Error("message")`
type NewErrorExpression struct {
	Message Expression // nil if no argument
	pos     Pos
}

func (*NewErrorExpression) nodeMarker()  {}
func (*NewErrorExpression) exprMarker() {}
func (n *NewErrorExpression) GetPos() Pos { return n.pos }

func NewNewErrorExpression(msg Expression, pos Pos) *NewErrorExpression {
	return &NewErrorExpression{Message: msg, pos: pos}
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

func (*NewDateExpression) nodeMarker()  {}
func (*NewDateExpression) exprMarker() {}
func (n *NewDateExpression) GetPos() Pos { return n.pos }

func NewNewDateExpression(millis Expression, pos Pos) *NewDateExpression {
	return &NewDateExpression{Millis: millis, pos: pos}
}

func NewNewDateExpressionMulti(args []Expression, pos Pos) *NewDateExpression {
	return &NewDateExpression{Args: args, pos: pos}
}

// InterfaceDeclaration — `interface Name { field: type; ... }`
type InterfaceDeclaration struct {
	Name   string
	Fields []AnnotField
	pos    Pos
}

func (*InterfaceDeclaration) nodeMarker()  {}
func (*InterfaceDeclaration) stmtMarker() {}
func (i *InterfaceDeclaration) GetPos() Pos { return i.pos }

func NewInterfaceDeclaration(name string, fields []AnnotField, pos Pos) *InterfaceDeclaration {
	return &InterfaceDeclaration{Name: name, Fields: fields, pos: pos}
}

// TypeAliasDeclaration — `type Name = TypeAnnotation`
type TypeAliasDeclaration struct {
	Name string
	Type *TypeAnnotation
	pos  Pos
}

func (*TypeAliasDeclaration) nodeMarker()  {}
func (*TypeAliasDeclaration) stmtMarker() {}
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

func (*ExportDeclaration) nodeMarker()  {}
func (*ExportDeclaration) stmtMarker() {}
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

func (*ImportDeclaration) nodeMarker()  {}
func (*ImportDeclaration) stmtMarker() {}
func (i *ImportDeclaration) GetPos() Pos { return i.pos }

func NewImportDeclaration(specs []ImportSpecifier, source string, pos Pos) *ImportDeclaration {
	return &ImportDeclaration{Specifiers: specs, Source: source, pos: pos}
}

// --- Type annotations ---

// AnnotField is one field in an object type annotation.
type AnnotField struct {
	Name string
	Type *TypeAnnotation
}

// TypeAnnotation holds the resolved type name from TS syntax or JSDoc.
// Fields is non-empty for object type annotations like { x: number; y: number }.
// ElemType is non-nil for structural array types like { x: number }[].
// IsFuncType is true for function type annotations like (x: number) => number.
type TypeAnnotation struct {
	Name        string // e.g. "number", "string", "int32", "uint8", "float64"
	Source      string // "ts" or "jsdoc"
	Fields      []AnnotField
	ElemType    *TypeAnnotation // non-nil for { ... }[] — the element type
	IsFuncType  bool
	FuncParams  []TypeAnnotation // param types for function type annotations
	FuncRetType *TypeAnnotation  // return type for function type annotations
	Nullable    bool             // true for T | null or T | undefined
}

package parser_test

import (
	"reflect"
	"testing"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

func mustParse(t *testing.T, src string) *ast.Program {
	t.Helper()
	prog, err := parser.Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return prog
}

func mustParseExpr(t *testing.T, src string) ast.Expression {
	t.Helper()
	prog := mustParse(t, src)
	if len(prog.Body) == 0 {
		t.Fatal("empty program")
	}
	es, ok := prog.Body[0].(*ast.ExpressionStatement)
	if !ok {
		t.Fatalf("expected ExpressionStatement, got %T", prog.Body[0])
	}
	return es.Expr
}

// --- Variable declarations ---

func TestVarDeclConst(t *testing.T) {
	prog := mustParse(t, "const x = 42;")
	decl := prog.Body[0].(*ast.VarDeclaration)
	if decl.Kind != "const" || decl.Name != "x" {
		t.Errorf("got kind=%q name=%q", decl.Kind, decl.Name)
	}
	lit, ok := decl.Init.(*ast.NumberLiteral)
	if !ok || lit.Value != "42" {
		t.Errorf("init: got %T %v", decl.Init, decl.Init)
	}
}

func TestVarDeclWithType(t *testing.T) {
	prog := mustParse(t, "let s: string = \"hi\";")
	decl := prog.Body[0].(*ast.VarDeclaration)
	if decl.Kind != "let" || decl.Name != "s" {
		t.Errorf("got kind=%q name=%q", decl.Kind, decl.Name)
	}
	if decl.TypeAnnot == nil || decl.TypeAnnot.Name != "string" {
		t.Errorf("type annotation: %v", decl.TypeAnnot)
	}
}

// --- Binary expressions ---

func TestBinaryExprPrecedence(t *testing.T) {
	// 1 + 2 * 3 should parse as 1 + (2 * 3)
	expr := mustParseExpr(t, "1 + 2 * 3;")
	bin, ok := expr.(*ast.BinaryExpression)
	if !ok || bin.Op != "+" {
		t.Fatalf("expected '+' BinaryExpression, got %T op=%q", expr, bin.Op)
	}
	right, ok := bin.Right.(*ast.BinaryExpression)
	if !ok || right.Op != "*" {
		t.Fatalf("right side: expected '*' BinaryExpression, got %T", bin.Right)
	}
}

func TestBitwiseExprPrecedence(t *testing.T) {
	// a | b & c  →  a | (b & c)
	expr := mustParseExpr(t, "a | b & c;")
	bin, ok := expr.(*ast.BinaryExpression)
	if !ok || bin.Op != "|" {
		t.Fatalf("expected '|' at root, got %T op=%q", expr, bin.Op)
	}
	right, ok := bin.Right.(*ast.BinaryExpression)
	if !ok || right.Op != "&" {
		t.Fatalf("right: expected '&', got %T", bin.Right)
	}
}

func TestShiftExpr(t *testing.T) {
	expr := mustParseExpr(t, "x << 2;")
	bin, ok := expr.(*ast.BinaryExpression)
	if !ok || bin.Op != "<<" {
		t.Fatalf("expected '<<', got %T op=%q", expr, bin.Op)
	}
}

func TestStrictEquality(t *testing.T) {
	expr := mustParseExpr(t, "a === b;")
	bin, ok := expr.(*ast.BinaryExpression)
	if !ok || bin.Op != "===" {
		t.Fatalf("expected '===', got %T op=%q", expr, bin.Op)
	}
}

// --- Unary expressions ---

func TestUnaryNot(t *testing.T) {
	expr := mustParseExpr(t, "!flag;")
	un, ok := expr.(*ast.UnaryExpression)
	if !ok || un.Op != "!" {
		t.Fatalf("expected '!' UnaryExpression, got %T", expr)
	}
}

func TestUnaryBitwiseNot(t *testing.T) {
	expr := mustParseExpr(t, "~x;")
	un, ok := expr.(*ast.UnaryExpression)
	if !ok || un.Op != "~" {
		t.Fatalf("expected '~' UnaryExpression, got %T", expr)
	}
}

func TestUnaryNegate(t *testing.T) {
	expr := mustParseExpr(t, "-x;")
	un, ok := expr.(*ast.UnaryExpression)
	if !ok || un.Op != "-" {
		t.Fatalf("expected '-' UnaryExpression, got %T", expr)
	}
}

// --- Ternary ---

func TestTernary(t *testing.T) {
	expr := mustParseExpr(t, "a > 0 ? a : -a;")
	cond, ok := expr.(*ast.ConditionalExpression)
	if !ok {
		t.Fatalf("expected ConditionalExpression, got %T", expr)
	}
	_, condIsOk := cond.Test.(*ast.BinaryExpression)
	if !condIsOk {
		t.Errorf("test should be BinaryExpression, got %T", cond.Test)
	}
}

// --- Arrow functions ---

func TestArrowFunctionExprBody(t *testing.T) {
	expr := mustParseExpr(t, "(x: number) => x * 2;")
	af, ok := expr.(*ast.ArrowFunction)
	if !ok {
		t.Fatalf("expected ArrowFunction, got %T", expr)
	}
	if len(af.Params) != 1 || af.Params[0].Name != "x" {
		t.Errorf("params: %v", af.Params)
	}
	_, ok = af.Body.(*ast.BinaryExpression)
	if !ok {
		t.Errorf("body should be BinaryExpression, got %T", af.Body)
	}
}

func TestArrowFunctionBlockBody(t *testing.T) {
	src := "(x: number): number => { return x + 1; }"
	expr := mustParseExpr(t, src)
	af, ok := expr.(*ast.ArrowFunction)
	if !ok {
		t.Fatalf("expected ArrowFunction, got %T", expr)
	}
	if af.Block == nil {
		t.Errorf("block body: expected non-nil Block")
	}
}

// --- Control flow ---

func TestIfElse(t *testing.T) {
	prog := mustParse(t, "if (x > 0) { } else { }")
	_, ok := prog.Body[0].(*ast.IfStatement)
	if !ok {
		t.Fatalf("expected IfStatement, got %T", prog.Body[0])
	}
}

func TestForLoop(t *testing.T) {
	prog := mustParse(t, "for (let i = 0; i < 10; i++) { }")
	_, ok := prog.Body[0].(*ast.ForStatement)
	if !ok {
		t.Fatalf("expected ForStatement, got %T", prog.Body[0])
	}
}

func TestWhileLoop(t *testing.T) {
	prog := mustParse(t, "while (x > 0) { x--; }")
	_, ok := prog.Body[0].(*ast.WhileStatement)
	if !ok {
		t.Fatalf("expected WhileStatement, got %T", prog.Body[0])
	}
}

func TestForOf(t *testing.T) {
	prog := mustParse(t, "for (const n of nums) { }")
	_, ok := prog.Body[0].(*ast.ForOfStatement)
	if !ok {
		t.Fatalf("expected ForOfStatement, got %T", prog.Body[0])
	}
}

func TestSwitch(t *testing.T) {
	prog := mustParse(t, "switch (x) { case 1: break; default: break; }")
	sw, ok := prog.Body[0].(*ast.SwitchStatement)
	if !ok {
		t.Fatalf("expected SwitchStatement, got %T", prog.Body[0])
	}
	if len(sw.Cases) != 2 {
		t.Errorf("want 2 cases, got %d", len(sw.Cases))
	}
}

// --- Function declarations ---

func TestFunctionDeclaration(t *testing.T) {
	prog := mustParse(t, "function add(a: number, b: number): number { return a + b; }")
	fn, ok := prog.Body[0].(*ast.FunctionDeclaration)
	if !ok {
		t.Fatalf("expected FunctionDeclaration, got %T", prog.Body[0])
	}
	if fn.Name != "add" || len(fn.Params) != 2 {
		t.Errorf("name=%q params=%d", fn.Name, len(fn.Params))
	}
	if fn.IsGenerator {
		t.Error("plain function declaration should not be IsGenerator")
	}
}

// --- Generator functions (TDD-00061) ---

func TestGeneratorFunctionDeclaration(t *testing.T) {
	prog := mustParse(t, "function* gen() { yield 1; }")
	fn, ok := prog.Body[0].(*ast.FunctionDeclaration)
	if !ok {
		t.Fatalf("expected FunctionDeclaration, got %T", prog.Body[0])
	}
	if fn.Name != "gen" || !fn.IsGenerator {
		t.Errorf("name=%q isGenerator=%v", fn.Name, fn.IsGenerator)
	}
}

func TestAsyncGeneratorFunctionDeclaration(t *testing.T) {
	prog := mustParse(t, "async function* gen() { yield 1; }")
	fn, ok := prog.Body[0].(*ast.FunctionDeclaration)
	if !ok {
		t.Fatalf("expected FunctionDeclaration, got %T", prog.Body[0])
	}
	if !fn.IsGenerator || !fn.IsAsync {
		t.Errorf("isGenerator=%v isAsync=%v", fn.IsGenerator, fn.IsAsync)
	}
}

func TestYieldExpressionWithArgument(t *testing.T) {
	prog := mustParse(t, "function* gen() { yield 1 + 2; }")
	fn := prog.Body[0].(*ast.FunctionDeclaration)
	es := fn.Body.Body[0].(*ast.ExpressionStatement)
	y, ok := es.Expr.(*ast.YieldExpression)
	if !ok {
		t.Fatalf("expected YieldExpression, got %T", es.Expr)
	}
	if y.Delegate {
		t.Error("plain yield should not be Delegate")
	}
	if _, ok := y.Argument.(*ast.BinaryExpression); !ok {
		t.Errorf("expected a BinaryExpression argument, got %T", y.Argument)
	}
}

func TestYieldStarExpression(t *testing.T) {
	prog := mustParse(t, "function* gen() { yield* other(); }")
	fn := prog.Body[0].(*ast.FunctionDeclaration)
	es := fn.Body.Body[0].(*ast.ExpressionStatement)
	y, ok := es.Expr.(*ast.YieldExpression)
	if !ok {
		t.Fatalf("expected YieldExpression, got %T", es.Expr)
	}
	if !y.Delegate {
		t.Error("yield* should be Delegate")
	}
}

func TestBareYieldNoOperand(t *testing.T) {
	prog := mustParse(t, "function* gen() { yield; }")
	fn := prog.Body[0].(*ast.FunctionDeclaration)
	es := fn.Body.Body[0].(*ast.ExpressionStatement)
	y, ok := es.Expr.(*ast.YieldExpression)
	if !ok {
		t.Fatalf("expected YieldExpression, got %T", es.Expr)
	}
	if y.Argument != nil {
		t.Errorf("bare yield should have a nil Argument, got %v", y.Argument)
	}
}

func TestYieldNoOperandBeforeLineTerminator(t *testing.T) {
	// Same ASI restriction as a bare return/break/continue: a line
	// terminator right after `yield` means no operand — the next line's
	// leading expression must not be swallowed as yield's own argument.
	prog := mustParse(t, "function* gen() { yield\n1; }")
	fn := prog.Body[0].(*ast.FunctionDeclaration)
	if len(fn.Body.Body) != 2 {
		t.Fatalf("expected 2 statements (yield; then 1;), got %d", len(fn.Body.Body))
	}
	es := fn.Body.Body[0].(*ast.ExpressionStatement)
	y, ok := es.Expr.(*ast.YieldExpression)
	if !ok {
		t.Fatalf("expected YieldExpression, got %T", es.Expr)
	}
	if y.Argument != nil {
		t.Errorf("yield before a line terminator should have a nil Argument, got %v", y.Argument)
	}
}

func TestYieldAsAssignmentRHS(t *testing.T) {
	// `x = yield y` must parse as `x = (yield y)` — yield binds at
	// assignment precedence, lower than the ternary/logical/etc. chain.
	prog := mustParse(t, "function* gen() { x = yield y; }")
	fn := prog.Body[0].(*ast.FunctionDeclaration)
	es := fn.Body.Body[0].(*ast.ExpressionStatement)
	assign, ok := es.Expr.(*ast.AssignmentExpression)
	if !ok {
		t.Fatalf("expected AssignmentExpression, got %T", es.Expr)
	}
	if _, ok := assign.Right.(*ast.YieldExpression); !ok {
		t.Errorf("expected assignment RHS to be a YieldExpression, got %T", assign.Right)
	}
}

func TestYieldReservedAsIdentifier(t *testing.T) {
	// `yield` is a hard keyword in this compiler (matching `await`'s own
	// precedent, not real JS's narrower "reserved only inside a generator
	// body" rule) — see TDD-00061.
	_, err := parser.Parse("let yield = 5;")
	if err == nil {
		t.Fatal("expected a parse error using 'yield' as a plain identifier, got none")
	}
}

// --- Literals ---

func TestArrayLiteral(t *testing.T) {
	expr := mustParseExpr(t, "[1, 2, 3];")
	lit, ok := expr.(*ast.ArrayLiteral)
	if !ok || len(lit.Elements) != 3 {
		t.Fatalf("expected ArrayLiteral with 3 elements, got %T", expr)
	}
}

func TestObjectLiteral(t *testing.T) {
	expr := mustParseExpr(t, "({ x: 1, y: 2 });")
	lit, ok := expr.(*ast.ObjectLiteral)
	if !ok || len(lit.Properties) != 2 {
		t.Fatalf("expected ObjectLiteral with 2 properties, got %T %v", expr, expr)
	}
}

func TestTemplateLiteral(t *testing.T) {
	expr := mustParseExpr(t, "`x = ${x}, y = ${y}`;")
	_, ok := expr.(*ast.TemplateLiteral)
	if !ok {
		t.Fatalf("expected TemplateLiteral, got %T", expr)
	}
}

// --- Compound assignments ---

func TestCompoundAssign(t *testing.T) {
	cases := []string{"+=", "-=", "*=", "/=", "&=", "|=", "^=", "<<=", ">>=", ">>>="}
	for _, op := range cases {
		t.Run(op, func(t *testing.T) {
			expr := mustParseExpr(t, "x "+op+" 1;")
			assign, ok := expr.(*ast.AssignmentExpression)
			if !ok || assign.Op != op {
				t.Errorf("expected AssignmentExpression op=%q, got %T op=%q", op, expr, assign.Op)
			}
		})
	}
}

// --- Classes (TDD-00009 Stage 0) ---

func TestClassDeclaration(t *testing.T) {
	prog := mustParse(t, `
		class Point {
			x: number;
			y: number;
			constructor(x: number, y: number) {
				this.x = x;
				this.y = y;
			}
			length(): number {
				return this.x;
			}
		}
	`)
	cls, ok := prog.Body[0].(*ast.ClassDeclaration)
	if !ok {
		t.Fatalf("expected ClassDeclaration, got %T", prog.Body[0])
	}
	if cls.Name != "Point" {
		t.Errorf("name = %q", cls.Name)
	}
	if len(cls.Fields) != 2 || cls.Fields[0].Name != "x" || cls.Fields[1].Name != "y" {
		t.Errorf("fields = %+v", cls.Fields)
	}
	if cls.Constructor == nil || len(cls.Constructor.Params) != 2 {
		t.Fatalf("constructor = %+v", cls.Constructor)
	}
	if len(cls.Methods) != 1 || cls.Methods[0].Name != "length" {
		t.Errorf("methods = %+v", cls.Methods)
	}
}

func TestClassDeclarationExport(t *testing.T) {
	prog := mustParse(t, "export class Foo { x: number; }")
	exp, ok := prog.Body[0].(*ast.ExportDeclaration)
	if !ok {
		t.Fatalf("expected ExportDeclaration, got %T", prog.Body[0])
	}
	if _, ok := exp.Decl.(*ast.ClassDeclaration); !ok {
		t.Fatalf("expected exported ClassDeclaration, got %T", exp.Decl)
	}
}

func TestNewExpressionGeneric(t *testing.T) {
	expr := mustParseExpr(t, "new Foo(1, 2);")
	n, ok := expr.(*ast.NewExpression)
	if !ok {
		t.Fatalf("expected NewExpression, got %T", expr)
	}
	if n.ClassName != "Foo" || len(n.Args) != 2 {
		t.Errorf("className=%q args=%d", n.ClassName, len(n.Args))
	}
}

// TestNewBuiltinFormsUnaffected guards against the generic `new ClassName(...)`
// fallback (added for TDD-00009 Stage 0) accidentally swallowing any of the
// five pre-existing hardcoded `new` forms — each must still parse to its own
// dedicated node type, not the new generic ast.NewExpression.
func TestNewBuiltinFormsUnaffected(t *testing.T) {
	cases := []struct {
		src  string
		want any
	}{
		{"new Array<number>(3);", &ast.NewArrayExpression{}},
		{"new Map<string, number>();", &ast.NewMapExpression{}},
		{"new Set<number>();", &ast.NewSetExpression{}},
		{`new Error("oops");`, &ast.NewErrorExpression{}},
		{"new Date();", &ast.NewDateExpression{}},
	}
	for _, c := range cases {
		t.Run(c.src, func(t *testing.T) {
			expr := mustParseExpr(t, c.src)
			wantType := reflect.TypeOf(c.want)
			if reflect.TypeOf(expr) != wantType {
				t.Errorf("got %T, want %v", expr, wantType)
			}
		})
	}
}

func TestThisExpression(t *testing.T) {
	expr := mustParseExpr(t, "this;")
	if _, ok := expr.(*ast.ThisExpression); !ok {
		t.Fatalf("expected ThisExpression, got %T", expr)
	}

	expr = mustParseExpr(t, "this.x;")
	member, ok := expr.(*ast.MemberExpression)
	if !ok || member.Property != "x" {
		t.Fatalf("expected MemberExpression on 'x', got %T", expr)
	}
	if _, ok := member.Object.(*ast.ThisExpression); !ok {
		t.Fatalf("expected ThisExpression receiver, got %T", member.Object)
	}
}

// --- Error cases ---

func TestParseError(t *testing.T) {
	cases := []string{
		"let",       // missing name
		"const x =", // missing initialiser
		"if x { }",  // missing parens
		"class Foo { constructor() {} constructor() {} }", // duplicate constructor
	}
	for _, src := range cases {
		t.Run(src, func(t *testing.T) {
			_, err := parser.Parse(src)
			if err == nil {
				t.Errorf("Parse(%q): expected error, got nil", src)
			}
		})
	}
}

// TDD-00085 Stage 1: `for await (const x of it)` parses to a ForOfStatement with
// Await set; the destructuring form carries it too, and a `for await` that isn't
// a for-of is a parse error.
func TestParseForAwaitOf(t *testing.T) {
	prog := mustParse(t, "async function m(): Promise<void> { for await (const x of it) { g(x); } }")
	// Descend into the async function body's first statement.
	fn, ok := prog.Body[0].(*ast.FunctionDeclaration)
	if !ok {
		t.Fatalf("expected FunctionDeclaration, got %T", prog.Body[0])
	}
	fo, ok := fn.Body.Body[0].(*ast.ForOfStatement)
	if !ok {
		t.Fatalf("expected ForOfStatement, got %T", fn.Body.Body[0])
	}
	if !fo.Await {
		t.Fatalf("expected ForOfStatement.Await to be true")
	}
	if fo.VarName != "x" || fo.Kind != "const" {
		t.Fatalf("unexpected loop var: kind=%q name=%q", fo.Kind, fo.VarName)
	}

	// A plain for-of has Await == false.
	prog2 := mustParse(t, "for (const y of ys) { g(y); }")
	fo2 := prog2.Body[0].(*ast.ForOfStatement)
	if fo2.Await {
		t.Fatalf("plain for-of should not have Await set")
	}

	// `for await` on a C-style loop is a parse error.
	if _, err := parser.Parse("for await (let i = 0; i < 3; i = i + 1) {}"); err == nil {
		t.Fatalf("expected a parse error for 'for await' on a C-style loop")
	}
}

// A reserved word is a valid class member name (IdentifierName / JS PropertyName)
// — plain, async, and after `.` — which is what lets a user async iterator declare
// the iterator-protocol `throw`/`return` methods that `yield*` delegates into.
func TestReservedWordClassMemberNames(t *testing.T) {
	prog := mustParse(t, `
class C {
  throw(): number { return 1 }
  return(): number { return 2 }
  async catch(): Promise<number> { return 3 }
}`)
	cd, ok := prog.Body[0].(*ast.ClassDeclaration)
	if !ok {
		t.Fatalf("expected a ClassDeclaration, got %T", prog.Body[0])
	}
	want := map[string]bool{"throw": false, "return": false, "catch": false}
	seen := map[string]bool{}
	for _, m := range cd.Methods {
		if _, ok := want[m.Name]; ok {
			seen[m.Name] = true
		}
	}
	for name := range want {
		if !seen[name] {
			t.Fatalf("method %q not parsed as a class member", name)
		}
	}
}


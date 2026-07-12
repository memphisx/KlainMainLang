package parser_test

import (
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

// --- Error cases ---

func TestParseError(t *testing.T) {
	cases := []string{
		"let",          // missing name
		"const x =",   // missing initialiser
		"if x { }",    // missing parens
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

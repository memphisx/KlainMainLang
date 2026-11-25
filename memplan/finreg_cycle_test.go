package memplan

import (
	"testing"

	"KlainMainLang/ast"
)

// TestCollectFinRegNamesCyclicAST: the walk visits each node once, so an
// AST that reaches a node again (a back-reference closing a loop) finishes
// instead of recursing until the stack overflows, and still finds the
// registry declared inside the loop.
func TestCollectFinRegNamesCyclicAST(t *testing.T) {
	reg := &ast.VarDeclaration{Kind: "const", Name: "reg", Init: &ast.NewExpression{ClassName: "FinalizationRegistry"}}
	body := &ast.BlockStatement{}
	cls := &ast.ClassDeclaration{Name: "C", Constructor: &ast.FunctionDeclaration{Name: "constructor", Body: body}}
	stmt := &ast.ExpressionStatement{Expr: &ast.ClassExpression{Decl: cls}}
	body.Body = []ast.Statement{reg, stmt} // the class's constructor body holds the statement holding the class
	got := collectFinRegNames(&ast.Program{Body: []ast.Statement{stmt}})
	if !got["reg"] {
		t.Fatalf("want reg found as a FinalizationRegistry name, got %v", got)
	}
}

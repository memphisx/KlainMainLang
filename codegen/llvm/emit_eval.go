package llvm

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

// emitStaticEval implements the static-string fast path for `eval(...)`
// (TDD-00046's static subset — no embedded JS engine): when the argument is
// a compile-time-constant string that parses as a single expression, that
// expression is compiled through this compiler's own parser + codegen, in
// place, and its value becomes eval's result. Because the expression is
// emitted at the call site, its free variables resolve against the enclosing
// scope — exactly direct eval's own scoping — and, being an expression, it
// introduces no bindings (so there is no var-hoisting-into-caller-scope or
// completion-value subtlety to get wrong).
//
// Anything outside that subset — a dynamic (non-constant) argument, a string
// that doesn't parse, or one that parses as statements/declarations rather
// than a lone expression — is a clean compile-time error, deliberately NOT a
// runtime throw. A runtime throw would be indistinguishable to the
// conformance harness's `assert.throws(SyntaxError, () => eval("bad"))` from
// the SyntaxError it expects (the shim's assert.throws matches any thrown
// value — this compiler has no first-class error constructors to compare
// against), which would falsely pass negative tests. A compile error keeps
// those honestly failing while positive expression-evals genuinely pass. See
// docs/tdd/TDD-00046.md.
func (e *Emitter) emitStaticEval(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: eval expects exactly 1 argument", pos.Line, pos.Col)
	}
	src, ok := staticStringValue(args[0])
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: eval is only supported with a compile-time-constant string argument; a dynamic eval needs an embedded JS engine", pos.Line, pos.Col)
	}
	prog, err := parser.Parse(src)
	if err != nil {
		return Value{}, fmt.Errorf("%d:%d: eval of a static string only supports a single expression, and this one did not parse as one: %v", pos.Line, pos.Col, err)
	}
	if len(prog.Body) != 1 {
		return Value{}, fmt.Errorf("%d:%d: eval of a static string only supports a single expression (got %d statements)", pos.Line, pos.Col, len(prog.Body))
	}
	exprStmt, ok := prog.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: eval of a static string only supports a single expression (got a %T)", pos.Line, pos.Col, prog.Body[0])
	}
	// Compile the parsed expression in place — its value is eval's result.
	return e.emitExpr(exprStmt.Expr)
}

// staticStringValue returns the compile-time-constant string an expression
// denotes, if any: a plain string literal, or a template literal with no
// interpolations (`\`abc\``). Its Value is already this compiler's decoded
// UTF-8 bytes (escapes resolved at lex time — see ADR-00194), which is
// exactly what a re-parse needs.
func staticStringValue(expr ast.Expression) (string, bool) {
	switch x := expr.(type) {
	case *ast.StringLiteral:
		return x.Value, true
	case *ast.TemplateLiteral:
		if len(x.Exprs) == 0 && len(x.Quasis) == 1 {
			return x.Quasis[0], true
		}
	}
	return "", false
}

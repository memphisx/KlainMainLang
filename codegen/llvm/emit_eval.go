package llvm

import (
	"fmt"
	"strings"

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
// A string that does not parse throws SyntaxError when the eval runs, as
// in JavaScript; statements are lowered by the resolver. A dynamic
// (non-constant) argument is still a compile-time error. See
// docs/tdd/TDD-00046.md.
func (e *Emitter) emitStaticEval(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) == 0 || isUndefinedLiteral(e, stripAsAny(args[0])) && len(args) == 1 {
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil // eval(), eval(undefined): undefined
	}
	// Arguments past the first are evaluated, and ignored.
	for _, a := range args[1:] {
		if _, err := e.emitExpr(a); err != nil {
			return Value{}, err
		}
	}
	src, ok := e.evalSource(args[0])
	if !ok {
		switch t := e.typeOfExpr(args[0]); {
		case evalReturnsArgument(t):
			// A value that is not a string is eval's result as it is.
			v, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			return e.emitBoxValue(v)
		case t.IsDynamic():
			// Its kind is known when it runs: a string would need an
			// embedded JavaScript engine (the one form this compiler
			// cannot run), its EvalError; any other value is the result.
			v, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			boxed, err := e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
			tag, _ := e.emitUnboxTagPayload(boxed)
			strL, okL := e.emitTagCheck(tag, kmlTagString, "eval.str")
			e.emitLabel(strL)
			e.ensureExceptionHelpers()
			errObj := e.buildErrorObj(errorKindIDs["EvalError"], e.internString("eval of a string computed at run time is not supported: it needs an embedded JavaScript engine"), e.internString("EvalError"))
			e.excThrow(errObj)
			e.emitTerminator("unreachable")
			e.emitLabel(okL)
			return boxed, nil
		}
		// A string computed at run time: evaluated, then its EvalError.
		if _, err := e.emitExpr(args[0]); err != nil {
			return Value{}, err
		}
		if !e.blockDone {
			e.ensureExceptionHelpers()
			errObj := e.buildErrorObj(errorKindIDs["EvalError"], e.internString("eval of a string computed at run time is not supported: it needs an embedded JavaScript engine"), e.internString("EvalError"))
			e.excThrow(errObj)
			e.emitTerminator("unreachable")
		}
		e.emitLabel(e.freshLabel("eval.dead"))
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	prog, err := parser.ParseEval(src)
	if err != nil {
		// Source that does not parse throws when the eval runs, as in JS.
		e.emitThrowSyntaxError(evalSyntaxMessage(err))
		e.emitLabel(e.freshLabel("eval.dead"))
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	// The resolver lowers eval code of statements (rewriteStaticEval).
	if len(prog.Body) != 1 {
		return Value{}, fmt.Errorf("internal compiler error: eval code of %d statements reached code generation", len(prog.Body))
	}
	exprStmt, ok := prog.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return Value{}, fmt.Errorf("internal compiler error: eval code of a %T reached code generation", prog.Body[0])
	}
	// Compile the parsed expression in place — its value is eval's result.
	return e.emitExpr(exprStmt.Expr)
}

// evalSyntaxMessage is a parse error's message without its position in
// the evaluated string.
func evalSyntaxMessage(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, ": "); i > 0 && strings.Count(msg[:i], ":") == 1 {
		msg = msg[i+2:]
	}
	return msg
}

// staticStringValue returns the compile-time-constant string an expression
// denotes, if any: a plain string literal, or a template literal with no
// interpolations (`\`abc\“). Its Value is already this compiler's decoded
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

// evalSource is the constant source eval's argument denotes: a literal, or
// a `const` declared once and bound to one (`const s = "1+2"; eval(s)`).
func (e *Emitter) evalSource(expr ast.Expression) (string, bool) {
	if src, ok := staticStringValue(expr); ok {
		return src, true
	}
	id, ok := expr.(*ast.Identifier)
	c := e.front()
	if !ok || c == nil {
		return "", false
	}
	sym, _ := c.Binding().Resolve(id)
	if sym == nil || len(sym.Declarations) != 1 {
		return "", false
	}
	vd, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
	if !ok || vd.Kind != "const" || vd.Init == nil {
		return "", false
	}
	return staticStringValue(vd.Init)
}

// inferStaticEvalType is emitStaticEval's compile-time-type mirror: a static
// `eval("<expr>")` produces exactly the value of the single expression the
// string parses to, so its type is that expression's inferred type. Keeping
// this in lockstep with emitStaticEval stops the enclosing closure's inferred
// return type (default i64) from disagreeing with the ptr/object/regexp the
// body actually returns — `() => eval("/re/")` emitted `ret ptr` inside a
// an i64 function otherwise (invalid IR). Pure: parsing and typeOfExpr do no
// codegen. Returns false when the argument is not a single-expression constant
// string (emitStaticEval will then raise its own clean compile error).
func (e *Emitter) inferStaticEvalType(args []ast.Expression) (Type, bool) {
	if len(args) == 0 {
		return TypeAny, true
	}
	src, ok := e.evalSource(args[0])
	if !ok {
		if t := e.typeOfExpr(args[0]); evalReturnsArgument(t) || t.IsDynamic() {
			return TypeAny, true // emitStaticEval's box of it
		}
		return Type{}, false
	}
	prog, err := parser.ParseEval(src)
	if err != nil || len(prog.Body) != 1 {
		return Type{}, false
	}
	exprStmt, ok := prog.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return Type{}, false
	}
	return e.typeOfExpr(exprStmt.Expr), true
}

// evalReturnsArgument reports a static type no value of which is a string:
// eval returns such an argument unchanged.
func evalReturnsArgument(t Type) bool {
	if t.IsNull() || t.IsUndefined() {
		return true
	}
	if t.IsDynamic() || t.IR == "void" {
		return false
	}
	return !isPlainStringTy(t.withoutNullable()) && !t.IsStrLiteral()
}

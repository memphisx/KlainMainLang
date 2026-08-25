package parser

import (
	"fmt"

	"KlainMainLang/ast"
)

// desugarRequire rewrites a top-level static CommonJS `require('<literal>')`
// into the equivalent ES import declaration, so the whole existing import
// machinery (resolver merge + codegen) handles it with no downstream changes.
// Only the *static* subset is accepted — a string-literal specifier at
// top-level declaration/statement position:
//
//	const x = require('mod')        → import * as x from 'mod'   (namespace: a
//	                                  CJS require binds the whole exports object,
//	                                  which the namespace form mirrors exactly —
//	                                  it also works for a named-only module,
//	                                  where a default import would not)
//	const { a, b: c } = require('mod') → import { a, b as c } from 'mod'
//	require('mod')                  → import 'mod'   (side-effect only)
//
// A dynamic specifier (a non-literal argument) is a clean compile error rather
// than a silent miscompile: runtime/lazy module loading is a separate, deferred
// capability, not something this static rewrite pretends to cover. Called only
// from ParseProgram, so a `require` nested inside a function body is left as an
// ordinary call (the lazy form, out of scope here).
func (p *Parser) desugarRequire(stmt ast.Statement) (ast.Statement, error) {
	switch s := stmt.(type) {
	case *ast.VarDeclaration:
		if s.Init == nil {
			return stmt, nil
		}
		src, matched, dynamic := requireCall(s.Init)
		if !matched {
			return stmt, nil
		}
		if dynamic {
			return nil, requireDynamicErr(s.GetPos())
		}
		return ast.NewImportDeclaration(nil, s.Name, src, s.GetPos()), nil

	case *ast.ObjectDestructuring:
		src, matched, dynamic := requireCall(s.Init)
		if !matched {
			return stmt, nil
		}
		if dynamic {
			return nil, requireDynamicErr(s.GetPos())
		}
		specs := make([]ast.ImportSpecifier, 0, len(s.Props))
		for _, prop := range s.Props {
			if prop.Default != nil || prop.SubArray != nil || prop.SubObject != nil || prop.Rest {
				return nil, fmt.Errorf("%d:%d: only simple `{ a, b: c }` destructuring is supported when binding a require('...') import (no defaults, nested patterns, or rest)", s.GetPos().Line, s.GetPos().Col)
			}
			specs = append(specs, ast.ImportSpecifier{Imported: prop.Key, Local: prop.Local})
		}
		return ast.NewImportDeclaration(specs, "", src, s.GetPos()), nil

	case *ast.ExpressionStatement:
		src, matched, dynamic := requireCall(s.Expr)
		if !matched {
			return stmt, nil
		}
		if dynamic {
			return nil, requireDynamicErr(s.GetPos())
		}
		// Side-effect-only `require('mod')` — an import with no bindings.
		return ast.NewImportDeclaration(nil, "", src, s.GetPos()), nil
	}
	return stmt, nil
}

// requireCall inspects an expression for a `require(...)` call. matched is true
// when the callee is the identifier `require` with exactly one argument;
// dynamic is true when that argument is not a plain string literal (the
// runtime/dynamic form). On a static match, source is the literal module path.
func requireCall(expr ast.Expression) (source string, matched, dynamic bool) {
	call, ok := expr.(*ast.CallExpression)
	if !ok {
		return "", false, false
	}
	id, ok := call.Callee.(*ast.Identifier)
	if !ok || id.Name != "require" {
		return "", false, false
	}
	if len(call.Args) != 1 {
		return "", true, true // require() or require(a, b) — not the static form
	}
	lit, ok := call.Args[0].(*ast.StringLiteral)
	if !ok {
		return "", true, true // require(variable) / require(`${x}`) — dynamic
	}
	return lit.Value, true, false
}

func requireDynamicErr(pos ast.Pos) error {
	return fmt.Errorf("%d:%d: dynamic require(...) with a non-string-literal module path is not supported — use a string-literal path (e.g. require('path')); runtime/lazy module loading is a separate capability", pos.Line, pos.Col)
}

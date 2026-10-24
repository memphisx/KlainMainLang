package checker

import (
	"KlainMainLang/ast"
	"KlainMainLang/diag"
)

// checkStrictCode reports strict code's early errors: TypeScript's
// alwaysStrict makes every file strict code, so `eval` or `arguments` as a
// binding name, a parameter or an assignment target, and `delete` of a
// plain name, are errors (the parser raises them for JavaScript that is
// strict itself: a "use strict" directive, a module).
func (c *Checker) checkStrictCode(n ast.Node) {
	if c.b.Sloppy {
		return // sloppy JavaScript (-compat=js, a script without alwaysStrict)
	}
	bad := func(name string) bool {
		name = ast.Unmangle(name)
		return name == "eval" || name == "arguments"
	}
	// `yield` outside a generator is an identifier only in sloppy code.
	reserved := func(name string, pos ast.Pos) {
		if ast.Unmangle(name) == "yield" {
			c.report(diag.StrictReservedWord, pos, "yield")
		}
	}
	params := func(ps []ast.Param, pos ast.Pos) {
		for _, p := range ps {
			if bad(p.Name) {
				c.report(diag.StrictInvalidUse, namePos(p.Pos, pos), ast.Unmangle(p.Name))
			}
			reserved(p.Name, namePos(p.Pos, pos))
		}
	}
	// A method's name is a property key, not a binding (`eval(js) {…}`).
	methods := map[*ast.FunctionDeclaration]bool{}
	ast.Inspect(n, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ClassDeclaration:
			for _, m := range x.Methods {
				methods[m] = true
			}
		case *ast.Identifier:
			reserved(x.Name, x.GetPos())
		case *ast.FunctionDeclaration:
			if bad(x.Name) && !methods[x] {
				c.report(diag.StrictInvalidUse, namePos(x.NamePos, x.GetPos()), ast.Unmangle(x.Name))
			}
			if !methods[x] {
				reserved(x.Name, namePos(x.NamePos, x.GetPos()))
			}
			params(x.Params, x.GetPos())
		case *ast.FunctionExpression:
			if bad(x.Name) && !x.Method {
				c.report(diag.StrictInvalidUse, namePos(x.NamePos, x.GetPos()), ast.Unmangle(x.Name))
			}
			if !x.Method {
				reserved(x.Name, namePos(x.NamePos, x.GetPos()))
			}
			params(x.Params, x.GetPos())
		case *ast.ArrowFunction:
			params(x.Params, x.GetPos())
		case *ast.VarDeclaration:
			if bad(x.Name) {
				c.report(diag.StrictInvalidUse, namePos(x.NamePos, x.GetPos()), ast.Unmangle(x.Name))
			}
			reserved(x.Name, namePos(x.NamePos, x.GetPos()))
		case *ast.TryStatement:
			if x.Catch != nil && bad(x.Catch.Param) {
				c.report(diag.StrictInvalidUse, x.GetPos(), ast.Unmangle(x.Catch.Param))
			}
		case *ast.AssignmentExpression:
			if id, ok := x.Left.(*ast.Identifier); ok && bad(id.Name) {
				c.report(diag.StrictInvalidUse, id.GetPos(), ast.Unmangle(id.Name))
			}
		case *ast.UpdateExpression:
			if id, ok := x.Arg.(*ast.Identifier); ok && bad(id.Name) {
				c.report(diag.StrictInvalidUse, id.GetPos(), ast.Unmangle(id.Name))
			}
		case *ast.UnaryExpression:
			if _, ok := x.Arg.(*ast.Identifier); ok && x.Op == "delete" {
				c.report(diag.StrictDeleteIdent, x.GetPos())
			}
		}
		return true
	})
}

// namePos is a declared name's position, as tsc reports there, or at when
// none was recorded.
func namePos(name, at ast.Pos) ast.Pos {
	if name.Line > 0 {
		return name
	}
	return at
}

package resolver

import "KlainMainLang/ast"

// annexBFunctions applies Annex B.3.3 to prog under -compat=js: a plain
// function declared in a block also binds a `var` of its name in the
// enclosing function (or the program), undefined until the block runs and
// the function once it has. Each such declaration becomes `name = function
// name(…) {…}` at the start of its block, with `var name = undefined`
// hoisted to the enclosing body. The cases the spec leaves alone keep the
// block-scoped function: an async or generator function, and a name that a
// parameter, a `let`/`const`/class of the enclosing body, or an enclosing
// block's lexical declaration already binds.
func annexBFunctions(prog *ast.Program) {
	prog.Body = annexBScope(prog.Body, nil)
	ast.Inspect(prog, func(n ast.Node) bool {
		switch f := n.(type) {
		case *ast.FunctionDeclaration:
			if f.Body != nil {
				f.Body.Body = annexBScope(f.Body.Body, paramNames(f.Params))
			}
		case *ast.FunctionExpression:
			if f.Body != nil {
				f.Body.Body = annexBScope(f.Body.Body, paramNames(f.Params))
			}
		case *ast.ArrowFunction:
			if f.Block != nil {
				f.Block.Body = annexBScope(f.Block.Body, paramNames(f.Params))
			}
		}
		return true
	})
}

func paramNames(ps []ast.Param) map[string]bool {
	out := map[string]bool{}
	for _, p := range ps {
		out[p.Name] = true
	}
	return out
}

// lexicalNames are the let/const/class names body declares directly.
func lexicalNames(body []ast.Statement) map[string]bool {
	out := map[string]bool{}
	for _, st := range body {
		switch s := st.(type) {
		case *ast.VarDeclaration:
			if s.Kind != "var" {
				out[s.Name] = true
			}
		case *ast.VarDeclarationList:
			for _, d := range s.Decls {
				if d.Kind != "var" {
					out[d.Name] = true
				}
			}
		case *ast.ClassDeclaration:
			out[s.Name] = true
		}
	}
	return out
}

// annexBScope rewrites one function (or program) body: its blocks'
// functions, and the hoisted vars they need.
func annexBScope(body []ast.Statement, params map[string]bool) []ast.Statement {
	top := lexicalNames(body)
	declared := map[string]bool{} // var or function names the body has itself
	for _, st := range body {
		switch s := st.(type) {
		case *ast.VarDeclaration:
			if s.Kind == "var" {
				declared[s.Name] = true
			}
		case *ast.FunctionDeclaration:
			declared[s.Name] = true
		}
	}
	var hoist []string
	hoisted := map[string]bool{}
	blocked := func(name string, outer []map[string]bool) bool {
		if params[name] || top[name] {
			return true
		}
		for _, m := range outer {
			if m[name] {
				return true
			}
		}
		return false
	}
	var block func(b *ast.BlockStatement, outer []map[string]bool)
	var stmt func(st ast.Statement, outer []map[string]bool)
	block = func(b *ast.BlockStatement, outer []map[string]bool) {
		if b == nil {
			return
		}
		b.Body = blockStmts(b.Body, outer, blocked, &hoist, hoisted, declared, stmt)
	}
	stmt = func(st ast.Statement, outer []map[string]bool) {
		switch s := st.(type) {
		case *ast.BlockStatement:
			block(s, outer)
		case *ast.IfStatement:
			block(s.Consequent, outer)
			if s.Alternate != nil {
				stmt(s.Alternate, outer)
			}
		case *ast.ForStatement:
			block(s.Body, outer)
		case *ast.ForOfStatement:
			block(s.Body, outer)
		case *ast.ForInStatement:
			block(s.Body, outer)
		case *ast.WhileStatement:
			block(s.Body, outer)
		case *ast.DoWhileStatement:
			block(s.Body, outer)
		case *ast.LabeledStatement:
			stmt(s.Body, outer)
		case *ast.TryStatement:
			block(s.Body, outer)
			if s.Catch != nil {
				block(s.Catch.Body, outer)
			}
			block(s.Finally, outer)
		case *ast.SwitchStatement:
			for i := range s.Cases {
				c := &s.Cases[i]
				c.Body = blockStmts(c.Body, outer, blocked, &hoist, hoisted, declared, stmt)
			}
		}
	}
	for _, st := range body {
		stmt(st, nil)
	}
	if len(hoist) == 0 {
		return body
	}
	pre := make([]ast.Statement, 0, len(hoist)+len(body))
	for _, name := range hoist {
		pos := body[0].GetPos()
		pre = append(pre, ast.NewVarDeclaration("var", name, nil, ast.NewNullLiteral(true, pos), pos))
	}
	return append(pre, body...)
}

// blockStmts rewrites one block's statements (a block, a switch case): each
// eligible function declaration becomes an assignment of a function
// expression to the hoisted var, at the block's start (where the function
// is initialized), and nested statements are walked with this block's
// lexical names in scope.
func blockStmts(body []ast.Statement, outer []map[string]bool, blocked func(string, []map[string]bool) bool,
	hoist *[]string, hoisted, declared map[string]bool, stmt func(ast.Statement, []map[string]bool)) []ast.Statement {
	here := lexicalNames(body)
	inner := append(append([]map[string]bool{}, outer...), here)
	var assigns, rest []ast.Statement
	for _, st := range body {
		fd, ok := st.(*ast.FunctionDeclaration)
		if !ok || fd.IsAsync || fd.IsGenerator || fd.Body == nil || len(fd.TypeParams) > 0 || blocked(fd.Name, outer) {
			stmt(st, inner)
			rest = append(rest, st)
			continue
		}
		if !declared[fd.Name] && !hoisted[fd.Name] {
			hoisted[fd.Name] = true
			*hoist = append(*hoist, fd.Name)
		}
		pos := fd.GetPos()
		fe := ast.NewFunctionExpression(fd.Name, fd.Params, fd.ReturnType, fd.Body, false, pos)
		assigns = append(assigns, ast.NewExpressionStatement(ast.NewAssignmentExpression("=", ast.NewIdentifier(fd.Name, pos), fe, pos), pos))
	}
	if len(assigns) == 0 {
		return rest
	}
	return append(assigns, rest...)
}

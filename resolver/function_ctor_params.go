package resolver

import (
	"strings"

	"KlainMainLang/ast"
)

// specializeFunctionCtorParams gives a `Function(…)` / `new Function(…)`
// whose source a function builds from its own string parameters
// (`function sub(t) { return new Function("return class My" + t + " extends
// " + t + " {}")(); }`) one constant-source arm per argument tuple a call
// site of that function passes as string literals: `t === "A" ? new
// Function("…A…") : … : new Function(<as written>)`. Each arm then compiles
// as a constant Function source does (rewriteFunctionConstructor); a call
// the arms do not cover keeps the run-time source.
func specializeFunctionCtorParams(prog *ast.Program) {
	decls := map[string][]*ast.FunctionDeclaration{}
	ast.Inspect(prog, func(n ast.Node) bool {
		if fd, ok := n.(*ast.FunctionDeclaration); ok && fd.Name != "" && fd.Body != nil {
			decls[fd.Name] = append(decls[fd.Name], fd)
		}
		return true
	})
	// The string-literal argument tuples of each name's call sites.
	sites := map[string][][]string{}
	ast.Inspect(prog, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpression)
		if !ok {
			return true
		}
		id, ok := call.Callee.(*ast.Identifier)
		if !ok || len(decls[id.Name]) != 1 {
			return true
		}
		var tuple []string
		for _, a := range call.Args {
			s, ok := a.(*ast.StringLiteral)
			if !ok {
				return true
			}
			tuple = append(tuple, s.Value)
		}
		sites[id.Name] = append(sites[id.Name], tuple)
		return true
	})
	for name, ds := range decls {
		if len(ds) != 1 || len(sites[name]) == 0 {
			continue
		}
		fd := ds[0]
		params := map[string]int{}
		for i, p := range fd.Params {
			if p.Name == "" || p.Rest || p.Default != nil {
				params = nil
				break
			}
			params[p.Name] = i
		}
		if params == nil || paramAssigned(fd, params) {
			continue
		}
		var rw func(ast.Node) ast.Node
		rw = func(n ast.Node) ast.Node {
			switch n.(type) {
			case *ast.FunctionDeclaration, *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassDeclaration, *ast.ClassExpression:
				return n // (its own scope: a parameter may be shadowed)
			}
			ast.RewriteChildren(n, rw)
			args, mk := functionCtorArgs(n)
			if mk == nil {
				return n
			}
			used := map[int]bool{}
			for _, a := range args {
				if !foldableSource(a, params, used) {
					return n
				}
			}
			if len(used) == 0 {
				return n // (constant already)
			}
			pos := n.(ast.Expression).GetPos()
			var out ast.Expression = n.(ast.Expression)
			seen := map[string]bool{}
			tuples := sites[name]
			for k := len(tuples) - 1; k >= 0; k-- {
				t := tuples[k]
				key := strings.Join(t, "\x00")
				if seen[key] {
					continue
				}
				seen[key] = true
				var test ast.Expression
				var folded []ast.Expression
				ok := true
				for i := range fd.Params {
					if !used[i] {
						continue
					}
					if i >= len(t) {
						ok = false
						break
					}
					eq := ast.NewBinaryExpression("===", ast.NewIdentifier(fd.Params[i].Name, pos), ast.NewStringLiteral(t[i], pos), pos)
					if test == nil {
						test = eq
					} else {
						test = ast.NewBinaryExpression("&&", test, eq, pos)
					}
				}
				if !ok {
					continue
				}
				for _, a := range args {
					s, _ := foldSource(a, params, t)
					folded = append(folded, ast.NewStringLiteral(s, pos))
				}
				out = ast.NewConditionalExpression(test, mk(folded, pos), out, pos)
			}
			return out
		}
		ast.RewriteChildren(fd.Body, rw)
	}
}

// functionCtorArgs is n's arguments when n is `Function(…)` or `new
// Function(…)`, with a maker of the same call over other arguments.
func functionCtorArgs(n ast.Node) ([]ast.Expression, func([]ast.Expression, ast.Pos) ast.Expression) {
	switch x := n.(type) {
	case *ast.CallExpression:
		if id, ok := x.Callee.(*ast.Identifier); ok && id.Name == "Function" && !x.Optional {
			return x.Args, func(a []ast.Expression, pos ast.Pos) ast.Expression {
				return ast.NewCallExpression(ast.NewIdentifier("Function", pos), a, pos)
			}
		}
	case *ast.NewExpression:
		if x.Callee == nil && !x.Qualified && x.ClassName == "Function" && len(x.TypeArgs) == 0 {
			return x.Args, func(a []ast.Expression, pos ast.Pos) ast.Expression {
				return ast.NewNewExpression("Function", a, pos)
			}
		}
	}
	return nil, nil
}

// foldableSource reports x built only of string literals, parameters
// (recorded in used) and `+` / template concatenation.
func foldableSource(x ast.Expression, params map[string]int, used map[int]bool) bool {
	switch v := x.(type) {
	case *ast.StringLiteral:
		return true
	case *ast.Identifier:
		i, ok := params[v.Name]
		if ok {
			used[i] = true
		}
		return ok
	case *ast.BinaryExpression:
		return v.Op == "+" && foldableSource(v.Left, params, used) && foldableSource(v.Right, params, used)
	case *ast.TemplateLiteral:
		for _, e := range v.Exprs {
			if !foldableSource(e, params, used) {
				return false
			}
		}
		return true
	}
	return false
}

// foldSource is the string x makes with the parameters bound to t.
func foldSource(x ast.Expression, params map[string]int, t []string) (string, bool) {
	switch v := x.(type) {
	case *ast.StringLiteral:
		return v.Value, true
	case *ast.Identifier:
		return t[params[v.Name]], true
	case *ast.BinaryExpression:
		l, _ := foldSource(v.Left, params, t)
		r, _ := foldSource(v.Right, params, t)
		return l + r, true
	case *ast.TemplateLiteral:
		var b strings.Builder
		for i, q := range v.Quasis {
			b.WriteString(q)
			if i < len(v.Exprs) {
				s, _ := foldSource(v.Exprs[i], params, t)
				b.WriteString(s)
			}
		}
		return b.String(), true
	}
	return "", false
}

// paramAssigned reports an assignment to one of params in fd's body.
func paramAssigned(fd *ast.FunctionDeclaration, params map[string]int) bool {
	found := false
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			if id, ok := x.Left.(*ast.Identifier); ok {
				_, p := params[id.Name]
				found = found || p
			}
		case *ast.UpdateExpression:
			if id, ok := x.Arg.(*ast.Identifier); ok {
				_, p := params[id.Name]
				found = found || p
			}
		}
		return !found
	})
	return found
}

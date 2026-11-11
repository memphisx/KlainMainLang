package resolver

import (
	"sort"
	"strconv"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// builtinDynamicSpec reports a specifier naming a builtin module, and its
// canonical form (the `node:` prefix dropped).
func builtinDynamicSpec(s string) (string, bool) {
	bare := strings.TrimPrefix(s, "node:")
	if _, ok := virtualBuiltinMarkers[bare]; ok {
		return bare, true
	}
	if _, ok := virtualBuiltinMarkers[s]; ok {
		return s, true
	}
	if _, ok := lib.ModulePath(s); ok {
		return bare, true
	}
	return "", false
}

// lowerBuiltinDynamicImports makes each `import(spec)` of a builtin module
// its namespace, imported statically (a builtin is part of every program
// that names it): `import("os")` is a promise fulfilled with the module's
// namespace object. A computed specifier first checks the builtins the
// file names in string literals, then falls through to the file's other
// candidates (lowerComputedImports).
func lowerBuiltinDynamicImports(prog *ast.Program) error {
	nsOf := map[string]string{} // canonical spec → namespace binding
	var order []string
	ns := func(canon, written string) string {
		if n, ok := nsOf[canon]; ok {
			return n
		}
		n := "__kml_dynns_" + strconv.Itoa(len(nsOf))
		nsOf[canon] = n
		order = append(order, written)
		return n
	}
	written := map[string]string{} // canonical → the form to import
	computed := false
	ast.Inspect(prog, func(n ast.Node) bool {
		if ic, ok := n.(*ast.ImportCallExpression); ok {
			if _, lit := ic.Specifier.(*ast.StringLiteral); !lit {
				computed = true
			}
		}
		return true
	})
	if computed {
		var specs []string
		ast.Inspect(prog, func(n ast.Node) bool {
			if s, ok := n.(*ast.StringLiteral); ok {
				if canon, ok := builtinDynamicSpec(s.Value); ok {
					if _, seen := written[canon]; !seen {
						written[canon] = s.Value
						specs = append(specs, canon)
					}
				}
			}
			return true
		})
		sort.Strings(specs)
		for _, c := range specs {
			ns(c, written[c])
		}
	}
	parseExpr := func(src string) (ast.Expression, error) {
		p, err := parser.Parse(src)
		if err != nil {
			return nil, err
		}
		return p.Body[0].(*ast.ExpressionStatement).Expr, nil
	}
	var err error
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rw)
		ic, ok := n.(*ast.ImportCallExpression)
		if !ok || err != nil {
			return n
		}
		if lit, ok := ic.Specifier.(*ast.StringLiteral); ok {
			canon, builtin := builtinDynamicSpec(lit.Value)
			if !builtin {
				return n
			}
			if _, seen := written[canon]; !seen {
				written[canon] = lit.Value
			}
			x, perr := parseExpr("Promise.resolve(" + ns(canon, lit.Value) + ")")
			if perr != nil {
				err = perr
				return n
			}
			return x
		}
		if len(nsOf) == 0 {
			return n
		}
		// The specifier is evaluated once, then ToString'd (a throw is the
		// rejection lowerComputedImports gives it).
		var b strings.Builder
		b.WriteString("((__kml_b: any): Promise<any> => { let __kml_t: string; try { __kml_t = String(__kml_b); } catch { return import(__kml_b); } ")
		keys := make([]string, 0, len(nsOf))
		for c := range nsOf {
			keys = append(keys, c)
		}
		sort.Strings(keys)
		for _, c := range keys {
			b.WriteString("if (__kml_t === " + strconv.Quote(c))
			if !strings.HasPrefix(c, "klain:") && !strings.HasPrefix(c, "node:") {
				b.WriteString(" || __kml_t === " + strconv.Quote("node:"+c))
			}
			b.WriteString(") return Promise.resolve(" + nsOf[c] + "); ")
		}
		b.WriteString("return import(__kml_t); })(0)")
		x, perr := parseExpr(b.String())
		if perr != nil {
			err = perr
			return n
		}
		x.(*ast.CallExpression).Args[0] = ic.Specifier
		return x
	}
	for i, st := range prog.Body {
		if out := rw(st); out != nil {
			if s, ok := out.(ast.Statement); ok {
				prog.Body[i] = s
			}
		}
	}
	if err != nil || len(nsOf) == 0 {
		return err
	}
	var decls []ast.Statement
	for _, w := range order {
		canon, _ := builtinDynamicSpec(w)
		decls = append(decls, ast.NewImportDeclaration(nil, nsOf[canon], w, ast.Pos{Line: 1, Col: 1}))
	}
	prog.Body = append(decls, prog.Body...)
	return nil
}

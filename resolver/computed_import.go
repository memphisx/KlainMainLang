package resolver

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

// computedImportAlternatives gives each `import(expr)` of the file whose
// specifier is computed its Alternatives: a literal import() of every
// module the file names in a relative-path string literal, the targets a
// specifier computed at run time can select (a native program loads only
// modules compiled into it). They are the alternatives' nodes, to resolve
// as literal imports are.
func computedImportAlternatives(prog *ast.Program, path, dir, klainModulesDir string) []*ast.ImportCallExpression {
	var computed []*ast.ImportCallExpression
	ast.Inspect(prog, func(n ast.Node) bool {
		if ic, ok := n.(*ast.ImportCallExpression); ok {
			if _, lit := ic.Specifier.(*ast.StringLiteral); !lit {
				computed = append(computed, ic)
			}
		}
		return true
	})
	if len(computed) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var specs []string
	ast.Inspect(prog, func(n ast.Node) bool {
		s, ok := n.(*ast.StringLiteral)
		if !ok || seen[s.Value] || !(strings.HasPrefix(s.Value, "./") || strings.HasPrefix(s.Value, "../")) {
			return true
		}
		seen[s.Value] = true
		switch strings.ToLower(filepath.Ext(s.Value)) {
		case "", ".js", ".mjs", ".cjs", ".ts", ".mts", ".cts":
		default:
			return true
		}
		if resolved, err := resolveImportPath(dir, s.Value, klainModulesDir); err == nil && resolved != path {
			specs = append(specs, s.Value)
		}
		return true
	})
	sort.Strings(specs)
	var out []*ast.ImportCallExpression
	for _, ic := range computed {
		ic.Alternatives = nil
		for _, sp := range specs {
			alt := ast.NewImportCallExpression(ast.NewStringLiteral(sp, ic.GetPos()), ic.GetPos())
			ic.Alternatives = append(ic.Alternatives, alt)
			out = append(out, alt)
		}
	}
	return out
}

// lowerComputedImports replaces each `import(expr)` whose specifier is
// computed with its selection among the Alternatives: the specifier
// evaluated (a throw is the call's), then, in the promise, ToString of it
// (a throw rejects), the literal import() it names, else the rejection
// Node gives a module it cannot find. importer is the file's path.
func lowerComputedImports(prog *ast.Program, importerOf func(ast.Statement) string) error {
	var err error
	var rw func(n ast.Node) ast.Node
	importer := ""
	rw = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rw)
		ic, ok := n.(*ast.ImportCallExpression)
		if !ok || err != nil {
			return n
		}
		if _, lit := ic.Specifier.(*ast.StringLiteral); lit {
			return n
		}
		var b strings.Builder
		b.WriteString("((__kml_spec: any): Promise<any> => { let __kml_s: string; try { __kml_s = String(__kml_spec); } catch (__kml_e) { return Promise.reject(__kml_e); } ")
		for _, alt := range ic.Alternatives {
			q := strconv.Quote(alt.Specifier.(*ast.StringLiteral).Value)
			b.WriteString("if (__kml_s === " + q + ") return import(" + q + "); ")
		}
		dir := filepath.ToSlash(filepath.Dir(importer))
		b.WriteString("const __kml_at = __kml_s.startsWith('./') ? " + strconv.Quote(dir) + " + __kml_s.slice(1) : __kml_s.startsWith('../') ? " + strconv.Quote(dir+"/") + " + __kml_s : __kml_s; ")
		b.WriteString("return Promise.reject(Object.assign(new Error(\"Cannot find module '\" + __kml_at + \"' imported from \" + " + strconv.Quote(importer) + "), { code: 'ERR_MODULE_NOT_FOUND' })); })(0)")
		p, perr := parser.Parse(b.String())
		if perr != nil {
			err = perr
			return n
		}
		es, ok := p.Body[0].(*ast.ExpressionStatement)
		if !ok {
			return n
		}
		call, ok := es.Expr.(*ast.CallExpression)
		if !ok {
			return n
		}
		call.Args[0] = ic.Specifier
		byValue := map[string]*ast.ImportCallExpression{}
		for _, alt := range ic.Alternatives {
			byValue[alt.Specifier.(*ast.StringLiteral).Value] = alt
		}
		var swap func(ast.Node) ast.Node
		swap = func(m ast.Node) ast.Node {
			ast.RewriteChildren(m, swap)
			if inner, ok := m.(*ast.ImportCallExpression); ok {
				if s, ok := inner.Specifier.(*ast.StringLiteral); ok && byValue[s.Value] != nil {
					return byValue[s.Value]
				}
			}
			return m
		}
		ast.RewriteChildren(call.Callee, swap)
		return call
	}
	for i, st := range prog.Body {
		importer = importerOf(st)
		if out := rw(st); out != nil {
			if s, ok := out.(ast.Statement); ok {
				prog.Body[i] = s
			}
		}
	}
	return err
}

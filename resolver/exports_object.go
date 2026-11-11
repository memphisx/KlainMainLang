package resolver

import (
	"fmt"
	"path/filepath"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// mutableExportsObjects binds, in JavaScript, a builtin module's default
// import or require (`import util from 'util'`, `const fs = require('fs')`)
// that the file writes or deletes a member of as the mutable object it is
// in Node (module.exports), not the frozen namespace: a copy of the
// module's exports, so `util.inspect = f` and `delete util.inspect` change
// what this file's later reads see. The library's own calls are unchanged,
// as they are in Node.
func mutableExportsObjects(path string, prog *ast.Program, compatJS bool) error {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".cjs", ".mjs":
	default:
		if !compatJS {
			return nil
		}
	}
	imports := map[string]*ast.ImportDeclaration{}
	for _, st := range prog.Body {
		imp, ok := st.(*ast.ImportDeclaration)
		if !ok || imp.Namespace == "" || imp.TypeOnly || !(imp.FromDefault || imp.FromRequire) {
			continue
		}
		if _, isLib := lib.ModulePath(imp.Source); isLib {
			imports[imp.Namespace] = imp
		}
	}
	if len(imports) == 0 {
		return nil
	}
	written := map[string]bool{}
	member := func(x ast.Expression) {
		var obj ast.Expression
		switch m := x.(type) {
		case *ast.MemberExpression:
			obj = m.Object
		case *ast.IndexExpression:
			obj = m.Object
		}
		if id, ok := obj.(*ast.Identifier); ok && imports[id.Name] != nil {
			written[id.Name] = true
		}
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			member(x.Left)
		case *ast.UnaryExpression:
			if x.Op == "delete" {
				member(x.Arg)
			}
		}
		return true
	})
	if len(written) == 0 {
		return nil
	}
	var body []ast.Statement
	for _, st := range prog.Body {
		body = append(body, st)
		imp, ok := st.(*ast.ImportDeclaration)
		if !ok || !written[imp.Namespace] || imports[imp.Namespace] != imp {
			continue
		}
		local := imp.Namespace
		imp.Namespace = "__kml_exports_" + local
		src := fmt.Sprintf("const %s: any = ((n: any) => { const o: any = {}; for (const k of Object.keys(n)) { if (k !== 'default') o[k] = n[k]; } return o; })(%s);", local, imp.Namespace)
		p, err := parser.Parse(src)
		if err != nil {
			return err
		}
		body = append(body, p.Body...)
	}
	prog.Body = body
	return nil
}

package resolver

import (
	"regexp"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
)

// namespaceClassMarker names the class a default-namespace module's value
// is in Node (`require('stream')` is Stream): `// kml:namespace-class K`.
var namespaceClassMarker = regexp.MustCompile(`(?m)^// kml:namespace-class ([A-Za-z_$][A-Za-z0-9_$]*)$`)

// namespacesAsClasses makes a namespace binding of such a module read as
// its class where a class is what is read: extended (`class M extends
// stream`), constructed (`new stream()`) or tested (`x instanceof
// stream`). Its members (`stream.Readable`) stay the namespace's. A
// function that declares the name keeps its own.
func namespacesAsClasses(prog *ast.Program) {
	alias := map[string]string{} // namespace binding → its class's local
	var imports []ast.Statement
	for _, st := range prog.Body {
		imp, ok := st.(*ast.ImportDeclaration)
		if !ok || imp.Namespace == "" || imp.TypeOnly {
			continue
		}
		p, isLib := lib.ModulePath(imp.Source)
		if !isLib {
			continue
		}
		src, ok := lib.ModuleSource(p)
		if !ok {
			continue
		}
		m := namespaceClassMarker.FindSubmatch(src)
		if m == nil {
			continue
		}
		local := "__kml_nsclass_" + imp.Namespace
		alias[imp.Namespace] = local
		imports = append(imports, ast.NewImportDeclaration([]ast.ImportSpecifier{{Imported: string(m[1]), Local: local}}, "", imp.Source, imp.GetPos()))
	}
	if len(alias) == 0 {
		return
	}
	used := false
	var walk func(n ast.Node, shadowed map[string]bool)
	walk = func(n ast.Node, shadowed map[string]bool) {
		ast.Inspect(n, func(c ast.Node) bool {
			if c != n {
				var params []ast.Param
				var body *ast.BlockStatement
				switch f := c.(type) {
				case *ast.FunctionDeclaration:
					params, body = f.Params, f.Body
				case *ast.FunctionExpression:
					params, body = f.Params, f.Body
				case *ast.ArrowFunction:
					params, body = f.Params, f.Block
				default:
					goto node
				}
				{
					inner := map[string]bool{}
					for k := range shadowed {
						inner[k] = true
					}
					for _, p := range params {
						inner[p.Name] = true
					}
					if body != nil {
						for _, st := range body.Body {
							for _, r := range declRefsOf(st) {
								inner[r.Name] = true
							}
						}
					}
					for _, p := range params {
						if p.Default != nil {
							walk(p.Default, inner)
						}
					}
					if body != nil {
						walk(body, inner)
					} else if af, ok := c.(*ast.ArrowFunction); ok && af.Body != nil {
						walk(af.Body, inner)
					}
					return false
				}
			}
		node:
			switch x := c.(type) {
			case *ast.ClassDeclaration:
				if a, ok := alias[x.BaseClass]; ok && !x.BaseQualified && !shadowed[x.BaseClass] {
					x.BaseClass = a
					used = true
				}
			case *ast.NewExpression:
				if a, ok := alias[x.ClassName]; ok && !x.Qualified && x.Callee == nil && !shadowed[x.ClassName] {
					x.ClassName = a
					used = true
				}
			case *ast.BinaryExpression:
				if id, ok := x.Right.(*ast.Identifier); ok && x.Op == "instanceof" && !shadowed[id.Name] {
					if a, ok := alias[id.Name]; ok {
						x.Right = ast.NewIdentifier(a, id.GetPos())
						used = true
					}
				}
			}
			return true
		})
	}
	for _, st := range prog.Body {
		walk(st, map[string]bool{})
	}
	if used {
		prog.Body = append(imports, prog.Body...)
	}
}

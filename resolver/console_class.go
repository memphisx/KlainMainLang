package resolver

import (
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
)

// consoleClassLocal is the binding the global console's Console reads as.
const consoleClassLocal = "__kml_Console"

// lowerConsoleClass gives the global console its Console property, Node's
// Console class (the console module's export): a read of `console.Console`
// and a class extending it name that class, imported for the file. A file
// that declares its own `console` is left alone.
func lowerConsoleClass(prog *ast.Program, path string, src []byte) {
	if strings.HasPrefix(path, lib.ModuleRoot) || !strings.Contains(string(src), "Console") || declaresTopLevel(prog, "console") {
		return
	}
	used := false
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rw)
		switch x := n.(type) {
		case *ast.MemberExpression:
			if id, ok := x.Object.(*ast.Identifier); ok && id.Name == "console" && x.Property == "Console" && !x.Optional {
				used = true
				return ast.NewIdentifier(consoleClassLocal, x.GetPos())
			}
		case *ast.NewExpression:
			if x.Qualified && x.Qualifier == "console" && len(x.QualifierPath) <= 1 && x.ClassName == "Console" {
				used = true
				x.ClassName, x.Qualified, x.Qualifier, x.QualifierPath = consoleClassLocal, false, "", nil
			}
		case *ast.ClassDeclaration:
			if x.BaseQualified && x.BaseQualifier == "console" && x.BaseClass == "Console" {
				used = true
				x.BaseClass, x.BaseQualified, x.BaseQualifier = consoleClassLocal, false, ""
			}
		}
		return n
	}
	for i, st := range prog.Body {
		if out := rw(st); out != nil {
			if s, ok := out.(ast.Statement); ok {
				prog.Body[i] = s
			}
		}
	}
	// `const { Console } = console`: the pattern's Console binds the class.
	var lists func(list []ast.Statement) []ast.Statement
	lists = func(list []ast.Statement) []ast.Statement {
		var out []ast.Statement
		for _, st := range list {
			out = append(out, st)
			od, ok := st.(*ast.ObjectDestructuring)
			if !ok {
				continue
			}
			id, ok := od.Init.(*ast.Identifier)
			if !ok || id.Name != "console" {
				continue
			}
			for i, p := range od.Props {
				if p.Key == "Console" && p.KeyExpr == nil && p.Default == nil && p.SubObject == nil && p.SubArray == nil && !p.Rest {
					used = true
					od.Props = append(od.Props[:i:i], od.Props[i+1:]...)
					out = append(out, ast.NewVarDeclaration(od.Kind, p.Local, nil, ast.NewIdentifier(consoleClassLocal, od.GetPos()), od.GetPos()))
					break
				}
			}
		}
		return out
	}
	prog.Body = lists(prog.Body)
	ast.Inspect(prog, func(n ast.Node) bool {
		if b, ok := n.(*ast.BlockStatement); ok {
			b.Body = lists(b.Body)
		}
		return true
	})
	if !used {
		return
	}
	imp := ast.NewImportDeclaration([]ast.ImportSpecifier{{Imported: "Console", Local: consoleClassLocal}}, "", "console", ast.Pos{Line: 1, Col: 1})
	prog.Body = append([]ast.Statement{imp}, prog.Body...)
}

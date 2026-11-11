package resolver

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// CommonJS files: a program file that writes `module` or `exports` (and
// declares neither, nor any ES export) is a CommonJS module, as Node loads
// one. Its body runs with a `module` object whose `exports` starts as an
// empty object, `exports` aliasing it; whatever `module.exports` holds once
// the body has run is the module's value, which this file exports as its
// default. A `require` of it binds that value: `const m = require('./m')`
// is m = module.exports, and `const { a } = require('./m')` reads a from it.

// isCommonJS reports whether prog, the file at path, is a CommonJS module.
func isCommonJS(prog *ast.Program, path string, src []byte) bool {
	if strings.HasPrefix(path, lib.ModuleRoot) {
		return false
	}
	for _, st := range prog.Body {
		switch st.(type) {
		case *ast.ExportDeclaration, *ast.ExportFromDeclaration:
			return false
		}
	}
	idents := codeIdents(src)
	uses := idents["module"] && !declaresTopLevel(prog, "module") || idents["exports"] && !declaresTopLevel(prog, "exports")
	return uses && !declaresTopLevel(prog, "module") && !declaresTopLevel(prog, "exports")
}

// wrapCommonJS gives a CommonJS file its module object and, unless it is
// the entry file (whose module.exports nothing reads), exports what
// module.exports holds after its body as the default.
func wrapCommonJS(prog *ast.Program, entry bool) error {
	pre, err := parser.Parse("var module: any = { exports: {} };\nvar exports: any = module.exports;\n")
	if err != nil {
		return err
	}
	body := append(append([]ast.Statement{}, pre.Body...), prog.Body...)
	if !entry {
		post, err := parser.Parse("export default module.exports;\n")
		if err != nil {
			return err
		}
		body = append(body, post.Body...)
	}
	prog.Body = body
	return nil
}

// requiresOfCommonJS rewrites each `require` of a CommonJS file in prog to
// bind module.exports: the namespace form to the default import, the
// destructuring form to the default import destructured. target resolves a
// specifier to its file, reporting whether that file is CommonJS.
func requiresOfCommonJS(prog *ast.Program, target func(source string) bool) {
	var body []ast.Statement
	n := 0
	for _, st := range prog.Body {
		imp, ok := st.(*ast.ImportDeclaration)
		if !ok || !imp.FromRequire || (imp.Namespace == "" && len(imp.Specifiers) == 0) || !target(imp.Source) {
			body = append(body, st)
			continue
		}
		pos := imp.GetPos()
		if imp.Namespace != "" {
			body = append(body, ast.NewImportDeclaration([]ast.ImportSpecifier{{Imported: "default", Local: imp.Namespace}}, "", imp.Source, pos))
			continue
		}
		tmp := fmt.Sprintf("__kml_cjs%d", n)
		n++
		body = append(body, ast.NewImportDeclaration([]ast.ImportSpecifier{{Imported: "default", Local: tmp}}, "", imp.Source, pos))
		props := make([]ast.DestructProp, 0, len(imp.Specifiers))
		for _, sp := range imp.Specifiers {
			props = append(props, ast.DestructProp{Key: sp.Imported, Local: sp.Local})
		}
		body = append(body, ast.NewObjectDestructuring("const", props, ast.NewIdentifier(tmp, pos), pos))
	}
	prog.Body = body
}

// commonJSTarget reports whether source, required from a file in dir,
// resolves to a CommonJS file.
func commonJSTarget(dir, source, klainModulesDir string) bool {
	p, err := resolveImportPath(dir, source, klainModulesDir)
	if err != nil || strings.HasPrefix(p, lib.ModuleRoot) {
		return false
	}
	src, err := readSource(p)
	if err != nil {
		return false
	}
	prog, err := parser.Parse(string(src))
	return err == nil && isCommonJS(prog, p, src)
}

// bindNodeGlobal gives a file that reads Node's `global` (and declares no
// binding of that name) the global object under it: `global` is globalThis
// in every Node module, CommonJS or ES.
func bindNodeGlobal(prog *ast.Program, path string, src []byte) (bool, error) {
	if strings.HasPrefix(path, lib.ModuleRoot) || !codeIdents(src)["global"] {
		return false, nil
	}
	pre, err := parser.Parse("const global: any = globalThis;\n")
	if err != nil {
		return false, err
	}
	// `declare const global: …` says the environment provides it: the
	// declaration becomes that binding.
	for i, st := range prog.Body {
		if v, ok := st.(*ast.VarDeclaration); ok && v.Ambient && v.Name == "global" {
			prog.Body[i] = pre.Body[0]
			return true, nil
		}
	}
	if declaresTopLevel(prog, "global") {
		return false, nil // the file's own binding
	}
	prog.Body = append(append([]ast.Statement{}, pre.Body...), prog.Body...)
	return true, nil
}

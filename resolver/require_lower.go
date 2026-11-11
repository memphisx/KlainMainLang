package resolver

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// lowerRequires gives a CommonJS-style file Node's `require` past the
// top-level forms the parser turns into imports (desugarRequire):
//
//   - a `require('<literal>')` anywhere else (inside a function, in an
//     expression) is that module's binding, imported statically: a compiled
//     program links every module it can load;
//   - `require` read as a value (`require(name)`, `require.resolve`,
//     `require.main`) is the file's require function, answering from the
//     modules this file links (its literal requires, and the builtins its
//     string literals name) and throwing MODULE_NOT_FOUND for any other.
//
// A file that declares its own `require`, an ES module file (.mjs, .mts)
// and a builtin module are left alone. The module body of a linked file
// runs at program start, not at its first require.
func lowerRequires(prog *ast.Program, path string, entry bool, src []byte, klainModulesDir string) error {
	if strings.HasPrefix(path, lib.ModuleRoot) {
		return nil
	}
	idents := codeIdents(src)
	esm := false
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mjs", ".mts":
		esm = true
	}
	// A file that makes a require function (`createRequire`) has its
	// string literals' modules registered for it.
	makesRequire := idents["createRequire"]
	if esm || !idents["require"] || declaresRequire(prog) {
		if !makesRequire {
			return nil
		}
		return registerLiteralModules(prog, path, klainModulesDir)
	}
	dir := filepath.Dir(path)
	hoisted := map[string]string{} // specifier → its binding
	var order []string
	canonOf := map[string]string{} // a builtin's canonical name → its first spelling
	bind := func(spec string) string {
		if canon, builtin := builtinDynamicSpec(spec); builtin {
			if first, ok := canonOf[canon]; ok {
				spec = first
			} else {
				canonOf[canon] = spec
			}
		}
		if n, ok := hoisted[spec]; ok {
			return n
		}
		n := "__kml_req" + strconv.Itoa(len(hoisted))
		hoisted[spec] = n
		order = append(order, spec)
		return n
	}
	valueUse := false
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		if call, ok := n.(*ast.CallExpression); ok {
			if id, ok := call.Callee.(*ast.Identifier); ok && id.Name == "require" && len(call.Args) == 1 {
				if lit, ok := call.Args[0].(*ast.StringLiteral); ok && linkable(dir, lit.Value, klainModulesDir, path) {
					return ast.NewIdentifier(bind(lit.Value), call.GetPos())
				}
			}
		}
		if id, ok := n.(*ast.Identifier); ok && id.Name == "require" {
			valueUse = true
			return n
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	for i, st := range prog.Body {
		if imp, ok := st.(*ast.ImportDeclaration); ok {
			if imp.FromRequire && imp.Namespace != "" {
				if _, seen := hoisted[imp.Source]; !seen {
					hoisted[imp.Source] = imp.Namespace
					order = append(order, imp.Source)
					if canon, builtin := builtinDynamicSpec(imp.Source); builtin {
						if _, ok := canonOf[canon]; !ok {
							canonOf[canon] = imp.Source
						}
					}
				}
			}
			continue
		}
		if out := rw(st); out != nil {
			if s, ok := out.(ast.Statement); ok {
				prog.Body[i] = s
			}
		}
	}
	if valueUse || makesRequire {
		// A computed require may name any builtin the file spells out.
		ast.Inspect(prog, func(n ast.Node) bool {
			if s, ok := n.(*ast.StringLiteral); ok {
				if _, builtin := builtinDynamicSpec(s.Value); builtin && linkable(dir, s.Value, klainModulesDir, path) {
					bind(s.Value)
				}
			}
			return true
		})
	}
	if len(order) == 0 && !valueUse && !makesRequire {
		return nil
	}
	var pre []ast.Statement
	pos := ast.Pos{Line: 1, Col: 1}
	existing := map[string]bool{}
	for _, st := range prog.Body {
		if imp, ok := st.(*ast.ImportDeclaration); ok && imp.FromRequire && imp.Namespace != "" {
			existing[imp.Namespace] = true
		}
	}
	for _, spec := range order {
		if existing[hoisted[spec]] {
			continue
		}
		imp := ast.NewImportDeclaration(nil, hoisted[spec], spec, pos)
		imp.FromRequire = true
		pre = append(pre, imp)
	}
	if valueUse || makesRequire {
		var b strings.Builder
		b.WriteString("import { _kmlRequireFor as __kml_requireFor, _kmlRegister as __kml_requireRegister } from 'module';\n")
		specs := append([]string{}, order...)
		sort.Strings(specs)
		for _, spec := range specs {
			key, isFile := spec, false
			if canon, builtin := builtinDynamicSpec(spec); builtin {
				key = canon
			} else if p, err := resolveImportPath(dir, spec, klainModulesDir); err == nil {
				key, isFile = p, true
			}
			fmt.Fprintf(&b, "__kml_requireRegister(%s, %s, %t);\n", strconv.Quote(key), hoisted[spec], isFile)
		}
		if valueUse {
			fmt.Fprintf(&b, "const require: any = __kml_requireFor(%s);\n", strconv.Quote(path))
		}
		if valueUse && entry && (codeIdents(src)["module"] && !declaresTopLevel(prog, "module")) {
			b.WriteString("require.main = module;\n")
		}
		p, err := parser.Parse(b.String())
		if err != nil {
			return err
		}
		pre = append(pre, p.Body...)
	}
	prog.Body = append(pre, prog.Body...)
	return nil
}

// linkable reports a specifier a require can link: a builtin module, or a
// file the program can import other than the file itself.
func linkable(dir, spec, klainModulesDir, self string) bool {
	if _, builtin := builtinDynamicSpec(spec); builtin {
		return true
	}
	if !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") && !strings.HasPrefix(spec, "/") {
		return false
	}
	p, err := resolveImportPath(dir, spec, klainModulesDir)
	return err == nil && p != self
}

// declaresRequire reports a file that binds `require` itself, anywhere.
func declaresRequire(prog *ast.Program) bool {
	if declaresTopLevel(prog, "require") {
		return true
	}
	found := false
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.VarDeclaration:
			found = found || x.Name == "require"
		case *ast.FunctionDeclaration:
			found = found || x.Name == "require"
			for _, p := range x.Params {
				found = found || p.Name == "require"
			}
		case *ast.FunctionExpression:
			for _, p := range x.Params {
				found = found || p.Name == "require"
			}
		case *ast.ArrowFunction:
			for _, p := range x.Params {
				found = found || p.Name == "require"
			}
		}
		return !found
	})
	return found
}

// registerLiteralModules registers, for a require function the file makes
// (`createRequire(import.meta.url)`), the builtins and files its string
// literals name, each imported for it.
func registerLiteralModules(prog *ast.Program, path, klainModulesDir string) error {
	dir := filepath.Dir(path)
	seen := map[string]bool{}
	var specs []string
	ast.Inspect(prog, func(n ast.Node) bool {
		if s, ok := n.(*ast.StringLiteral); ok && !seen[s.Value] && linkable(dir, s.Value, klainModulesDir, path) {
			key := s.Value
			if canon, builtin := builtinDynamicSpec(s.Value); builtin {
				key = canon
			}
			if !seen[key] {
				seen[key], seen[s.Value] = true, true
				specs = append(specs, s.Value)
			}
		}
		return true
	})
	if len(specs) == 0 {
		return nil
	}
	sort.Strings(specs)
	var b strings.Builder
	b.WriteString("import { _kmlRegister as __kml_requireRegister } from 'module';\n")
	for i, spec := range specs {
		key, isFile := spec, false
		if canon, builtin := builtinDynamicSpec(spec); builtin {
			key = canon
		} else if p, err := resolveImportPath(dir, spec, klainModulesDir); err == nil {
			key, isFile = p, true
		}
		fmt.Fprintf(&b, "import * as __kml_reqlit%d from %s;\n__kml_requireRegister(%s, __kml_reqlit%d, %t);\n", i, strconv.Quote(spec), strconv.Quote(key), i, isFile)
	}
	p, err := parser.Parse(b.String())
	if err != nil {
		return err
	}
	prog.Body = append(p.Body, prog.Body...)
	return nil
}

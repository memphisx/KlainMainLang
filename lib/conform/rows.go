package conform

import (
	"fmt"
	"strings"
	"sync"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// A status row's `decl` names the builtin declarations it covers, so the
// status pages' ✅ rows are joined to declarations (TDD-00230 P3.1):
//
//   - `Iface.member`: a member of a declared interface or one of its bases
//     (`String.slice`, `StringConstructor.raw`);
//   - `Name`: a declared global value, interface or type (`parseInt`);
//   - `module#name`: an export of a builtin module written in TypeScript or
//     declared with `declare module` (`fs#readFileSync`), and `module#` the
//     module itself;
//   - `syntax`: a row that is syntax or an operator, which has no
//     declaration.

var (
	oursOnce  sync.Once
	oursDecl  *decls
	ambient   map[string]map[string]bool
	nsGlobals = map[string]bool{}
	oursErr   error
)

func ours() (*decls, error) {
	oursOnce.Do(func() {
		oursDecl = newDecls()
		ambient = map[string]map[string]bool{}
		progs, err := lib.Programs()
		if err != nil {
			oursErr = err
			return
		}
		for _, p := range progs {
			oursDecl.add(p)
			// A namespace's interfaces, by their dotted names
			// (`NodeJS.Process`).
			for _, g := range p.NamespaceGroups {
				for _, st := range g.Members {
					if n := declName(st); n != "" {
						// A namespace's functions and values
						// (`Reflect.get`), by their dotted names.
						nsGlobals[g.Name+"."+strings.TrimPrefix(n, ast.NamespaceMangle(g.Name, ""))] = true
					}
					if n, ok := st.(*ast.InterfaceDeclaration); ok {
						ms := map[string]*member{}
						for _, m := range n.Members {
							oursDecl.addMember(ms, m)
						}
						oursDecl.ifaces[g.Name+"."+n.Name] = ms
					}
				}
			}
			for _, st := range p.Body {
				if m, ok := st.(*ast.AmbientModuleDeclaration); ok {
					names := map[string]bool{}
					addExports(names, m.Body, "", 0)
					ambient[m.Name] = names
				}
			}
		}
	})
	return oursDecl, oursErr
}

// hasMember reports whether interface iface, or a base of it, declares
// member.
func (d *decls) hasMember(iface, member string, seen map[string]bool) bool {
	if seen[iface] {
		return false
	}
	seen[iface] = true
	if d.ifaces[iface][member] != nil {
		return true
	}
	for b := range d.bases[iface] {
		if d.hasMember(b, member, seen) {
			return true
		}
	}
	return false
}

// moduleExports are the names the builtin module spec exports, following
// `export * from` and `export { … } from` into sibling modules.
func moduleExports(spec string) (map[string]bool, bool) {
	// A module with a `declare module` block (`url`'s code-generated part)
	// may have a file of its own for the rest.
	if names, ok := ambient[spec]; ok {
		if path, ok := lib.ModulePath(spec); ok {
			merged := map[string]bool{}
			for n := range names {
				merged[n] = true
			}
			addModuleFile(merged, path, 0)
			return merged, true
		}
		return names, true
	}
	path, ok := lib.ModulePath(spec)
	if !ok {
		// `http` and `https` have no module file of their own: their
		// declarations are the companion's.
		path, ok = lib.CompanionPath(spec)
	}
	if !ok {
		return nil, false
	}
	names := map[string]bool{}
	if !addModuleFile(names, path, 0) {
		return nil, false
	}
	return names, true
}

func addModuleFile(names map[string]bool, path string, depth int) bool {
	src, ok := lib.ModuleSource(path)
	if !ok || depth > 8 {
		return false
	}
	prog, err := parser.Parse(string(src))
	if err != nil {
		return false
	}
	addExports(names, prog.Body, path, depth)
	return true
}

func addExports(names map[string]bool, body []ast.Statement, path string, depth int) {
	for _, st := range body {
		switch x := st.(type) {
		case *ast.ExportDeclaration:
			if x.IsDefault {
				names["default"] = true
			}
			if n := declName(x.Decl); n != "" {
				names[n] = true
			}
		case *ast.ExportFromDeclaration:
			if x.All && x.Source != "" && path != "" {
				addModuleFile(names, siblingPath(path, x.Source), depth+1)
			}
			for _, s := range x.Specifiers {
				n := s.Local
				if n == "" {
					n = s.Imported
				}
				names[n] = true
			}
		default:
			// A `declare module` body exports every declaration.
			if path == "" {
				if n := declName(st); n != "" {
					names[n] = true
				}
			}
		}
	}
}

func siblingPath(path, source string) string {
	return lib.ModuleRoot + strings.TrimPrefix(source, "./") + ".ts"
}

func declName(st ast.Statement) string {
	switch d := st.(type) {
	case *ast.FunctionDeclaration:
		return d.Name
	case *ast.VarDeclaration:
		return d.Name
	case *ast.ClassDeclaration:
		return d.Name
	case *ast.InterfaceDeclaration:
		return d.Name
	case *ast.TypeAliasDeclaration:
		return d.Name
	case *ast.EnumDeclaration:
		return d.Name
	}
	return ""
}

// ResolveDecl reports why a status row's declaration name does not resolve,
// or nil when it does.
func ResolveDecl(name string) error {
	d, err := ours()
	if err != nil {
		return err
	}
	if name == "syntax" {
		return nil
	}
	if mod, export, ok := strings.Cut(name, "#"); ok {
		names, found := moduleExports(mod)
		switch {
		case !found:
			return fmt.Errorf("%s: no builtin module %q", name, mod)
		case export != "" && !names[export]:
			return fmt.Errorf("%s: module %q does not export %q", name, mod, export)
		}
		return nil
	}
	if nsGlobals[name] {
		return nil
	}
	if i := strings.LastIndex(name, "."); i > 0 {
		iface, member := name[:i], name[i+1:]
		if d.ifaces[iface] == nil {
			return fmt.Errorf("%s: no declared interface %q", name, iface)
		}
		if !d.hasMember(iface, member, map[string]bool{}) {
			return fmt.Errorf("%s: interface %q declares no %q", name, iface, member)
		}
		return nil
	}
	if !d.globals[name] {
		return fmt.Errorf("%s: no declared global", name)
	}
	return nil
}

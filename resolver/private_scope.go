package resolver

import (
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
)

// scopePrivateNames gives a private name two classes declare a spelling of
// its own in each (`#f` → `#f` + ast.PrivateScopeSep + n), and every
// reference the class it lexically resolves to: a private name is the
// innermost enclosing class's that declares it, never a base class's or an
// outer one's, so `#f` in a subclass and in its base are two members.
// A class with decorators or auto-accessors keeps its names.
func scopePrivateNames(prog *ast.Program) {
	declares := func(cd *ast.ClassDeclaration) map[string]bool {
		out := map[string]bool{}
		for _, f := range cd.Fields {
			if strings.HasPrefix(f.Name, "#") {
				out[f.Name] = true
			}
		}
		for _, m := range cd.Methods {
			if strings.HasPrefix(m.Name, "#") {
				out[m.Name] = true
			}
		}
		return out
	}
	kept := func(cd *ast.ClassDeclaration) bool {
		if len(cd.Decorators) > 0 || len(cd.AutoAccessors) > 0 {
			return true
		}
		for _, m := range cd.Methods {
			if len(m.Decorators) > 0 {
				return true
			}
		}
		for _, f := range cd.Fields {
			if len(f.Decorators) > 0 {
				return true
			}
		}
		return false
	}
	// A builtin library class's renaming depends on its own module alone,
	// so the library's compiled unit is the same in every program: its
	// names are counted, and numbered, within the module. A program's own
	// class counts every class's.
	libKey := func(cd *ast.ClassDeclaration) string {
		const sep = "__kml_modL"
		if i := strings.Index(cd.Name, sep); i > 0 {
			return cd.Name[i+len(sep):]
		}
		return ""
	}
	count := map[string]int{}       // across the program
	libCount := map[[2]string]int{} // within a library module
	ast.Inspect(prog, func(n ast.Node) bool {
		if cd, ok := n.(*ast.ClassDeclaration); ok {
			for nm := range declares(cd) {
				count[nm]++
				if k := libKey(cd); k != "" {
					libCount[[2]string{k, nm}]++
				}
			}
		}
		return true
	})
	shared := false
	for _, c := range count {
		shared = shared || c > 1
	}
	if !shared {
		return
	}
	renames := func(cd *ast.ClassDeclaration, nm string) bool {
		if k := libKey(cd); k != "" {
			return libCount[[2]string{k, nm}] > 1
		}
		return count[nm] > 1
	}
	next := map[string]int{} // per library module ("" for the program's own)
	var visit func(root ast.Node, scope map[string]string)
	visit = func(root ast.Node, scope map[string]string) {
		ast.Inspect(root, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ClassDeclaration:
				if n == root {
					return true
				}
				inner := make(map[string]string, len(scope))
				for k, v := range scope {
					inner[k] = v
				}
				own := declares(x)
				keep := kept(x)
				names := make([]string, 0, len(own))
				for nm := range own {
					names = append(names, nm)
				}
				sort.Strings(names)
				for _, nm := range names {
					if renames(x, nm) && !keep {
						k := libKey(x)
						next[k]++
						inner[nm] = fmt.Sprintf("%s%s%s_%d", nm, ast.PrivateScopeSep, k, next[k])
					} else {
						delete(inner, nm) // the class's own, unrenamed
					}
				}
				for i := range x.Fields {
					if r, ok := inner[x.Fields[i].Name]; ok && own[x.Fields[i].Name] {
						x.Fields[i].Name = r
					}
				}
				for _, m := range x.Methods {
					if r, ok := inner[m.Name]; ok && own[m.Name] {
						m.Name = r
					}
				}
				visit(x, inner)
				return false
			case *ast.MemberExpression:
				if r, ok := scope[x.Property]; ok {
					x.Property = r
				}
			case *ast.BinaryExpression:
				if id, ok := x.Left.(*ast.Identifier); ok && x.Op == "in" {
					if r, ok := scope[id.Name]; ok {
						id.Name = r
					}
				}
			}
			return true
		})
	}
	visit(prog, map[string]string{})
}

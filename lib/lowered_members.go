package lib

import (
	"strings"
	"sync"

	"KlainMainLang/ast"
)

var (
	loweredMembersOnce sync.Once
	loweredMembers     map[string]map[string]string
)

// LoweredMembers maps each builtin global value (`process`, `Buffer`) to the
// members its declaration links to a builtin module's export — a member's
// own `@lower module:export`, or its interface's (a member held by the
// object the link returns) — each to that module's file. A program reading
// one needs the module.
func LoweredMembers() map[string]map[string]string {
	loweredMembersOnce.Do(func() {
		loweredMembers = map[string]map[string]string{}
		progs, err := Programs()
		if err != nil {
			return
		}
		ifaces := map[string][]*ast.InterfaceDeclaration{}
		globals := map[string]string{}
		for _, p := range progs {
			ast.Inspect(p, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.InterfaceDeclaration:
					ifaces[x.Name] = append(ifaces[x.Name], x)
				case *ast.VarDeclaration:
					if x.TypeAnnot != nil {
						if ref, ok := x.TypeAnnot.(*ast.TypeReference); ok {
							globals[x.Name] = ref.Name
						}
					}
				}
				return true
			})
		}
		for global, iface := range globals {
			members := map[string]string{}
			seen := map[string]bool{}
			var walk func(name string)
			walk = func(name string) {
				if seen[name] {
					return
				}
				seen[name] = true
				for _, d := range ifaces[name] {
					for _, m := range d.Members {
						var member, link string
						switch m := m.(type) {
						case *ast.PropertySignature:
							member, link = m.Name, m.Lower
							if link == "" {
								link = m.Holder
							}
						case *ast.MethodSignature:
							member, link = m.Name, m.Lower
							if link == "" {
								link = m.Holder
							}
						}
						if mod, _, ok := strings.Cut(link, ":"); ok && member != "" {
							members[member] = ModuleRoot + mod + ".ts"
						}
					}
					for _, h := range d.Heritage {
						if ref, ok := h.(*ast.TypeReference); ok {
							walk(ref.Name)
						}
					}
				}
			}
			walk(iface)
			if len(members) > 0 {
				loweredMembers[global] = members
			}
		}
	})
	return loweredMembers
}

// Package conform diffs the builtin declarations (lib/*.d.ts, TDD-00230
// P3.1) against TypeScript's own library declarations: for every global and
// interface member this compiler declares, a name TypeScript's library does
// not declare, an arity difference, or a readonly difference is a problem;
// a callable member with fewer signatures than TypeScript's is listed for
// review (fewer signatures may accept fewer argument types).
package conform

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"KlainMainLang/ast"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
)

// arity is the argument counts a callable member accepts: at least min, at
// most max (-1: any number).
type arity struct{ min, max int }

func (a arity) String() string {
	if a.max < 0 {
		return fmt.Sprintf("%d+", a.min)
	}
	if a.min == a.max {
		return fmt.Sprint(a.min)
	}
	return fmt.Sprintf("%d-%d", a.min, a.max)
}

func paramsArity(ps []*ast.SignatureParameter) arity {
	a := arity{}
	for _, p := range ps {
		if p.Rest {
			a.max = -1
			return a
		}
		a.max++
		if !p.Optional {
			a.min = a.max
		}
	}
	return a
}

// union widens a to take b's counts too.
func (a arity) union(b arity) arity {
	if b.min < a.min {
		a.min = b.min
	}
	if a.max >= 0 && (b.max < 0 || b.max > a.max) {
		a.max = b.max
	}
	return a
}

type member struct {
	kind     string // "method", "property", "call", "construct"
	arity    arity
	readonly bool
	sigs     int // a callable member's signatures
}

// decls is one library's declarations: each interface's members, and the
// global value and type names.
type decls struct {
	ifaces  map[string]map[string]*member
	globals map[string]bool
}

func newDecls() *decls {
	return &decls{ifaces: map[string]map[string]*member{}, globals: map[string]bool{}}
}

func (d *decls) add(prog *ast.Program) {
	nested := map[ast.Statement]bool{} // a namespace's members, not globals
	for _, g := range prog.NamespaceGroups {
		for _, m := range g.Members {
			nested[m] = true
		}
	}
	for _, st := range prog.Body {
		if nested[st] {
			continue
		}
		switch n := st.(type) {
		case *ast.InterfaceDeclaration:
			d.globals[n.Name] = true
			ms := d.ifaces[n.Name]
			if ms == nil {
				ms = map[string]*member{}
				d.ifaces[n.Name] = ms
			}
			for _, m := range n.Members {
				d.addMember(ms, m)
			}
		case *ast.VarDeclaration:
			d.globals[n.Name] = true
		case *ast.FunctionDeclaration:
			d.globals[n.Name] = true
		case *ast.TypeAliasDeclaration:
			d.globals[n.Name] = true
		case *ast.ClassDeclaration:
			d.globals[n.Name] = true
		}
	}
}

func (d *decls) addMember(ms map[string]*member, m ast.TypeMember) {
	var name, kind string
	var ar arity
	callable := false
	readonly := false
	switch m := m.(type) {
	case *ast.MethodSignature:
		name, kind, ar, callable = m.Name, "method", paramsArity(m.Parameters), true
	case *ast.CallSignature:
		name, kind, ar, callable = "()", "call", paramsArity(m.Parameters), true
	case *ast.ConstructSignature:
		name, kind, ar, callable = "new()", "construct", paramsArity(m.Parameters), true
	case *ast.PropertySignature:
		name, kind, readonly = m.Name, "property", m.Readonly
		if m.Accessor == "get" {
			readonly = true
		}
	default:
		return
	}
	prev := ms[name]
	if prev == nil {
		ms[name] = &member{kind: kind, arity: ar, readonly: readonly, sigs: 1}
		return
	}
	if callable {
		prev.arity = prev.arity.union(ar)
		prev.sigs++
	} else if m, ok := m.(*ast.PropertySignature); ok && m.Accessor == "set" {
		prev.readonly = false
	}
}

// Diff compares the builtin declarations with the TypeScript library in
// tsLib (its src/lib directory). Problems allowed lists are left out.
func Diff(tsLib string, allowed map[string]bool) (problems, review []string, err error) {
	ts := newDecls()
	files, _ := filepath.Glob(filepath.Join(tsLib, "*.d.ts"))
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("no TypeScript library at %s (run tools/conformance/fetch.sh)", tsLib)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, nil, err
		}
		p, err := parser.ParseDeclarations(string(src))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f, err)
		}
		ts.add(p)
	}
	ours := newDecls()
	progs, err := lib.Programs()
	if err != nil {
		return nil, nil, fmt.Errorf("builtin declarations: %w", err)
	}
	for _, p := range progs {
		ours.add(p)
	}
	report := func(key, msg string) {
		if !allowed[key] {
			problems = append(problems, key+": "+msg)
		}
	}
	var names []string
	for n := range ours.globals {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if !ts.globals[n] && !lib.KnownGlobal(n) {
			report(n, "not declared by TypeScript's library or @types/node")
		}
	}
	var ifaces []string
	for n := range ours.ifaces {
		ifaces = append(ifaces, n)
	}
	sort.Strings(ifaces)
	for _, in := range ifaces {
		tm := ts.ifaces[in]
		if tm == nil && lib.KnownGlobal(in) {
			// Only @types/node declares it (Buffer), and its files do not
			// parse whole: its name is checked above, its members are not.
			continue
		}
		var mnames []string
		for m := range ours.ifaces[in] {
			mnames = append(mnames, m)
		}
		sort.Strings(mnames)
		for _, mn := range mnames {
			om := ours.ifaces[in][mn]
			key := in + "." + mn
			t := tm[mn]
			switch {
			case t == nil:
				report(key, "not a member of TypeScript's "+in)
			case om.kind != t.kind && !(om.kind == "method" && t.kind == "property") && !(om.kind == "property" && t.kind == "method"):
				report(key, fmt.Sprintf("a %s here, a %s in TypeScript", om.kind, t.kind))
			case om.kind == "property" && t.kind == "property" && om.readonly != t.readonly:
				report(key, fmt.Sprintf("readonly %v here, %v in TypeScript", om.readonly, t.readonly))
			case om.kind != "property" && t.kind != "property" && om.arity != t.arity:
				report(key, fmt.Sprintf("takes %s arguments here, %s in TypeScript", om.arity, t.arity))
			case om.kind != "property" && om.sigs < t.sigs:
				review = append(review, fmt.Sprintf("%s: %d signatures here, %d in TypeScript", key, om.sigs, t.sigs))
			}
		}
	}
	return problems, review, nil
}

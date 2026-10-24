package ast

import (
	"sort"
	"strings"
)

// A namespace read as a value (`console.log(N)`, `Object.keys(N)`,
// `const o = N.Sub`) is the object TypeScript's emit builds for it: its
// exported members as properties, in the order they are assigned. The
// parser desugars the members to top-level declarations, so the object is
// a hidden module constant built after the namespace's last statement,
// each property read as `N.member` is. Only namespaces the program reads
// whole get one, and it is added before binding, so the checker types it
// as it does the code around it.

// NamespaceObjectName is the hidden constant holding namespace ns's object.
func NamespaceObjectName(ns string) string {
	return "__kml_nsobj_" + strings.ReplaceAll(ns, ".", "__kmlns_")
}

// namespaceChain is the dotted namespace name as the member chain naming it.
func namespaceChain(ns string, pos Pos) Expression {
	segs := strings.Split(ns, ".")
	var x Expression = NewIdentifier(segs[0], pos)
	for _, s := range segs[1:] {
		x = NewMemberExpression(x, s, pos)
	}
	return x
}

// isNamespace reports whether dotted name ns is a namespace of prog: one
// with members, or the outer name of a dotted declaration (`namespace A.B`).
func (prog *Program) isNamespace(ns string) bool {
	if _, ok := prog.Namespaces[ns]; ok {
		return true
	}
	_, ok := prog.NamespaceExports[ns]
	return ok
}

// WholeNamespace is the namespace expr names when it is a namespace
// reference itself (`N`, `N.Sub`, a top-level `import X = N.Sub` alias
// of one), or "".
func (prog *Program) WholeNamespace(expr Expression) string {
	switch x := expr.(type) {
	case *Identifier:
		for _, n := range []string{x.Name, Unmangle(x.Name)} {
			if prog.isNamespace(n) {
				return n
			}
			for _, a := range prog.NSAliases {
				if a.Scope == "" && a.Name == n && prog.isNamespace(a.Target) {
					return a.Target
				}
			}
		}
	case *MemberExpression:
		if root := prog.WholeNamespace(x.Object); root != "" {
			if full := root + "." + x.Property; prog.isNamespace(full) {
				return full
			}
		}
	}
	return ""
}

// AddNamespaceObjects adds the hidden object constant of every namespace
// prog reads whole, and of the nested namespaces those objects hold, and
// records them in prog.NamespaceObjects. A second call does nothing.
func AddNamespaceObjects(prog *Program) {
	if prog.NamespaceObjects != nil || len(prog.NamespaceGroups) == 0 {
		return
	}
	prog.NamespaceObjects = map[string]string{}
	need := map[string]bool{}
	var visit func(n Node, asObject bool)
	visit = func(n Node, asObject bool) {
		switch x := n.(type) {
		case nil:
			return
		case *Identifier:
			if !asObject {
				if ns := prog.WholeNamespace(x); ns != "" {
					need[ns] = true
				}
			}
			return
		case *MemberExpression:
			if !asObject {
				if ns := prog.WholeNamespace(x); ns != "" {
					need[ns] = true
				}
			}
			visit(x.Object, true)
			return
		}
		ForEachChild(n, func(c Node) bool {
			visit(c, false)
			return true
		})
	}
	for _, st := range prog.Body {
		visit(st, false)
	}
	// A namespace merged with a function, class or enum of its name is that
	// value, its members added to it.
	for _, st := range prog.Body {
		switch d := st.(type) {
		case *FunctionDeclaration:
			delete(need, d.Name)
			delete(need, Unmangle(d.Name))
		case *ClassDeclaration:
			delete(need, d.Name)
			delete(need, Unmangle(d.Name))
		case *EnumDeclaration:
			delete(need, d.Name)
			delete(need, Unmangle(d.Name))
		}
	}
	if len(need) == 0 {
		return
	}
	// An object holds its nested namespaces' objects.
	for changed := true; changed; {
		changed = false
		for ns := range need {
			for _, x := range prog.NamespaceExports[ns] {
				if sub := ns + "." + x.Name; !need[sub] && prog.isNamespace(sub) {
					need[sub] = true
					changed = true
				}
			}
		}
	}
	// Each object is built after the last statement of its namespace or of
	// any namespace inside it.
	index := map[Statement]int{}
	for i, st := range prog.Body {
		index[st] = i
	}
	last := map[string]Statement{}
	for _, g := range prog.NamespaceGroups {
		if len(g.Members) == 0 {
			continue
		}
		st := g.Members[len(g.Members)-1]
		for ns := g.Name; ; {
			if cur := last[ns]; cur == nil || index[st] > index[cur] {
				last[ns] = st
			}
			i := strings.LastIndexByte(ns, '.')
			if i < 0 {
				break
			}
			ns = ns[:i]
		}
	}
	names := make([]string, 0, len(need))
	for ns := range need {
		if last[ns] != nil {
			names = append(names, ns)
		}
	}
	// Deepest first, so a nested object is built before the one holding it
	// when both end on the same statement.
	sort.Slice(names, func(i, j int) bool {
		di, dj := strings.Count(names[i], "."), strings.Count(names[j], ".")
		if di != dj {
			return di > dj
		}
		return names[i] < names[j]
	})
	after := map[Statement][]Statement{}
	for _, ns := range names {
		anchor := last[ns]
		pos := anchor.GetPos()
		var props []ObjectProperty
		for _, x := range prog.NamespaceExports[ns] {
			member := func() Expression { return NewMemberExpression(namespaceChain(ns, pos), x.Name, pos) }
			if !x.Mutable {
				props = append(props, ObjectProperty{Key: x.Name, Value: member()})
				continue
			}
			// An exported `let`: the property is the binding.
			get := NewFunctionExpression("", nil, nil, NewBlockStatement([]Statement{NewReturnStatement(member(), pos)}, pos), false, pos)
			set := NewFunctionExpression("", []Param{{Name: "v", Type: AnyAnnotation("")}}, nil, NewBlockStatement([]Statement{
				NewExpressionStatement(NewAssignmentExpression("=", member(), NewIdentifier("v", pos), pos), pos)}, pos), false, pos)
			props = append(props, ObjectProperty{Key: x.Name, Value: get, AccessorKind: "get"}, ObjectProperty{Key: x.Name, Value: set, AccessorKind: "set"})
		}
		init := NewCallExpression(NewMemberExpression(NewIdentifier("Object", pos), "assign", pos), []Expression{NewObjectLiteral(props, pos)}, pos)
		init.Intrinsic = LiveBindingsIntrinsic
		hidden := NamespaceObjectName(ns)
		prog.NamespaceObjects[ns] = hidden
		after[anchor] = append(after[anchor], NewVarDeclaration("const", hidden, AnyAnnotation(""), init, pos))
	}
	var body []Statement
	for _, st := range prog.Body {
		body = append(body, st)
		body = append(body, after[st]...)
	}
	prog.Body = body
}

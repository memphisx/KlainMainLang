package resolver

import (
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
)

// lowerRuntimeClassKeys lowers each class declaration whose members have a
// computed key evaluated at run time (`[a + b]() {}`, `static [k] = 1`;
// ast KeyExpr) to the definitions JavaScript makes of them: each key is
// evaluated, in source order, when the class is, and converted to a
// property key; the member, compiled under its placeholder name, is then
// defined on the prototype (or the class) under that key, and a field is
// defined on each instance (or the class) when its initializer runs. Fields
// are lowered only when fields is set (the js lane).
func lowerRuntimeClassKeys(prog *ast.Program, fields bool) {
	if fields {
		repeatedFieldsAtRunTime(prog)
		deletedStaticsAtRunTime(prog)
	}
	n := 0
	var lowerList func(stmts []ast.Statement) []ast.Statement
	lowerList = func(stmts []ast.Statement) []ast.Statement {
		var out []ast.Statement
		changed := false
		for _, st := range stmts {
			cd, ok := st.(*ast.ClassDeclaration)
			var alias ast.Statement
			if v, isVar := st.(*ast.VarDeclaration); isVar && v.TypeAnnot == nil {
				// `let C = class { … }`: the declaration `class C { … }`,
				// a binding of the same name holding it.
				if ce, isCE := v.Init.(*ast.ClassExpression); isCE && hasRuntimeKey(ce.Decl, fields) {
					cd, ok = ce.Decl, true
					if cd.Name == "$ClassExpr" {
						cd.Name = v.Name
					} else if cd.Name != v.Name {
						alias = ast.NewVarDeclaration(v.Kind, v.Name, nil, ast.NewIdentifier(cd.Name, v.GetPos()), v.GetPos())
					}
				}
			}
			if !ok || !hasRuntimeKey(cd, fields) {
				out = append(out, st)
				continue
			}
			pre, post := lowerClassKeys(cd, &n, fields)
			out = append(append(append(out, pre...), cd), post...)
			if alias != nil {
				out = append(out, alias)
			}
			changed = true
		}
		if !changed {
			return stmts
		}
		return out
	}
	prog.Body = lowerList(prog.Body)
	ast.Inspect(prog, func(x ast.Node) bool {
		switch b := x.(type) {
		case *ast.BlockStatement:
			b.Body = lowerList(b.Body)
		case *ast.SwitchStatement:
			for i := range b.Cases {
				b.Cases[i].Body = lowerList(b.Cases[i].Body)
			}
		}
		return true
	})
	// Any other class expression (`C = class { … }`, `f(class { … })`):
	// `(() => { keys; class N { … } definitions; return N; })()`, where it is
	// evaluated. An anonymous one assigned to a name is named by it.
	var rw func(x ast.Node) ast.Node
	rw = func(x ast.Node) ast.Node {
		if a, ok := x.(*ast.AssignmentExpression); ok && a.Op == "=" {
			if id, isID := a.Left.(*ast.Identifier); isID {
				if ce, isCE := a.Right.(*ast.ClassExpression); isCE && ce.Decl.Name == "$ClassExpr" && hasRuntimeKey(ce.Decl, fields) {
					// (its source name: the binding is the IIFE's own)
					ce.Decl.Name = ast.Unmangle(id.Name)
				}
			}
		}
		ast.RewriteChildren(x, rw)
		ce, ok := x.(*ast.ClassExpression)
		if !ok || !hasRuntimeKey(ce.Decl, fields) {
			return x
		}
		cd := ce.Decl
		pos := ce.GetPos()
		pre, post := lowerClassKeys(cd, &n, fields)
		body := append(append(append([]ast.Statement{}, pre...), cd), post...)
		body = append(body, ast.NewReturnStatement(ast.NewIdentifier(cd.Name, pos), pos))
		block := ast.NewBlockStatement(body, pos)
		switch {
		case keysUse(pre, func(n ast.Node) bool { _, ok := n.(*ast.YieldExpression); return ok }):
			// A key's `yield` is the enclosing generator's:
			// `yield* function* () { … }.call(this)`.
			gen := ast.NewFunctionExpression("", nil, nil, block, false, pos)
			gen.IsGenerator = true
			call := ast.NewCallExpression(ast.NewMemberExpression(gen, "call", pos), []ast.Expression{ast.NewThisExpression(pos)}, pos)
			return ast.NewYieldExpression(call, true, pos)
		case keysUse(pre, func(n ast.Node) bool { _, ok := n.(*ast.AwaitExpression); return ok }):
			// A key's `await` is the enclosing async function's.
			fn := ast.NewArrowFunction(nil, nil, nil, block, pos)
			fn.IsAsync = true
			return ast.NewAwaitExpression(ast.NewCallExpression(fn, nil, pos), pos)
		}
		fn := ast.NewArrowFunction(nil, nil, nil, block, pos)
		return ast.NewCallExpression(fn, nil, pos)
	}
	ast.RewriteChildren(prog, rw)
}

// hasRuntimeKey reports a member of cd keyed at run time (a field only
// when fields is set).
func hasRuntimeKey(cd *ast.ClassDeclaration, fields bool) bool {
	for _, m := range cd.Methods {
		if m.KeyExpr != nil {
			return true
		}
	}
	for _, f := range cd.Fields {
		if f.KeyExpr != nil && fields {
			return true
		}
	}
	return false
}

// runtimeKeyMember is one member keyed at run time: a method (m) or a
// field (f, an index into the class's fields).
type runtimeKeyMember struct {
	key ast.Expression
	m   *ast.FunctionDeclaration
	f   int
}

// lowerClassKeys is cd's statements before (the keys) and after (the
// method definitions) its declaration; cd's fields keyed at run time are
// rewritten in place to define themselves.
func lowerClassKeys(cd *ast.ClassDeclaration, n *int, fields bool) (pre, post []ast.Statement) {
	var members []runtimeKeyMember
	for _, m := range cd.Methods {
		if m.KeyExpr != nil {
			members = append(members, runtimeKeyMember{key: m.KeyExpr, f: -1, m: m})
		}
	}
	for i, f := range cd.Fields {
		if f.KeyExpr != nil && fields {
			members = append(members, runtimeKeyMember{key: f.KeyExpr, f: i})
		}
	}
	sort.SliceStable(members, func(i, j int) bool {
		a, b := members[i].key.GetPos(), members[j].key.GetPos()
		return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
	})
	for _, mem := range members {
		pos := mem.key.GetPos()
		k := fmt.Sprintf("__kml_rk%d", *n)
		*n++
		toKey := "((k: any): any => typeof k === \"symbol\" ? k : String(k))(KEY)"
		if mem.m != nil && mem.m.IsStatic || mem.m == nil && cd.Fields[mem.f].Static {
			// The class's own `prototype` cannot be redefined.
			toKey = "((k: any): any => { var p: any = typeof k === \"symbol\" ? k : String(k); if (p === \"prototype\") throw new TypeError(\"Cannot redefine property: prototype\"); return p; })(KEY)"
		}
		pre = append(pre, keyTemplate("var "+k+" = "+toKey+";", cd.Name, "", mem.key, nil, pos))
		if mem.m != nil {
			m := mem.m
			m.KeyExpr = nil
			on := "CLS.prototype"
			if m.IsStatic {
				on = "CLS"
			}
			var src string
			switch m.AccessorKind {
			case "get", "set":
				acc := "(Object.getOwnPropertyDescriptor(" + on + ", \"HID\") as any)." + m.AccessorKind
				if m.IsStatic {
					// (The class's own accessor, forwarded to.)
					acc = "function (this: any): any { return this.HID; }"
					if m.AccessorKind == "set" {
						acc = "function (this: any, v: any): void { this.HID = v; }"
					}
				}
				src = "Object.defineProperty(" + on + ", " + k + ", { " + m.AccessorKind + ": " + acc + ", enumerable: false, configurable: true });"
			default:
				src = "Object.defineProperty(" + on + ", " + k + ", { value: (" + on + " as any).HID, writable: true, enumerable: false, configurable: true });"
			}
			post = append(post, keyTemplate(src, cd.Name, m.Name, nil, nil, pos))
			continue
		}
		f := &cd.Fields[mem.f]
		f.KeyExpr = nil
		var init ast.Expression = ast.NewNullLiteral(true, pos)
		if f.Initializer != nil {
			init = f.Initializer
		}
		def := keyTemplate("(Object.defineProperty(this, "+k+", { value: INIT, writable: true, enumerable: true, configurable: true }), undefined);", cd.Name, f.Name, nil, init, pos)
		f.Initializer = def.(*ast.ExpressionStatement).Expr
		f.Type = nil
	}
	return pre, append(post, reorderAfterRuntimeKeys(cd)...)
}

// reorderAfterRuntimeKeys keeps the prototype's (and the class's) own
// keys in declaration order: a constant-named method declared after one
// keyed at run time — defined after the class — is moved behind it, by
// redefining it with its own descriptor.
func reorderAfterRuntimeKeys(cd *ast.ClassDeclaration) []ast.Statement {
	var out []ast.Statement
	// (A static's place among the class's own keys is its declaration's.)
	for _, static := range []bool{false} {
		var ms []*ast.FunctionDeclaration
		for _, m := range cd.Methods {
			if m.IsStatic == static && m.Body != nil && m.Name != "constructor" {
				ms = append(ms, m)
			}
		}
		sort.SliceStable(ms, func(i, j int) bool {
			a, b := ms[i].GetPos(), ms[j].GetPos()
			return a.Line < b.Line || a.Line == b.Line && a.Col < b.Col
		})
		seenRuntime, moved := false, map[string]bool{}
		for _, m := range ms {
			if strings.HasPrefix(m.Name, ast.RuntimeKeyPrefix) {
				seenRuntime = true
				continue
			}
			key := m.Name
			if m.AccessorKind != "" {
				key = strings.TrimPrefix(strings.TrimPrefix(key, "__kml_get_"), "__kml_set_")
			}
			if !seenRuntime || moved[key] || key == "" || strings.HasPrefix(key, "#") || strings.HasPrefix(key, "\x01") || isIndexName(key) {
				continue
			}
			moved[key] = true
			src := "((o: any, n: string): void => { var d: any = Object.getOwnPropertyDescriptor(o, n); if (d) { delete o[n]; Object.defineProperty(o, n, d); } })(CLS.prototype, \"HID\");"
			out = append(out, keyTemplate(src, cd.Name, key, nil, nil, m.GetPos()))
		}
	}
	return out
}

// isIndexName reports an array-index key, which orders first anyway.
func isIndexName(k string) bool {
	if k == "" || len(k) > 1 && k[0] == '0' {
		return false
	}
	for _, c := range k {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// keyTemplate is the statement src with CLS the class cls, HID (a member
// name or a string) the member's placeholder name hid, KEY key and INIT
// init.
func keyTemplate(src, cls, hid string, key, init ast.Expression, pos ast.Pos) ast.Statement {
	ev, err := parser.ParseEval(src)
	if err != nil || len(ev.Body) != 1 {
		panic("run-time class key template: " + src)
	}
	var sub func(ast.Node) ast.Node
	sub = func(x ast.Node) ast.Node {
		switch v := x.(type) {
		case *ast.Identifier:
			switch v.Name {
			case "CLS":
				return ast.NewIdentifier(cls, pos)
			case "KEY":
				return key
			case "INIT":
				return init
			}
		case *ast.MemberExpression:
			if v.Property == "HID" {
				v.Property = hid
			}
		case *ast.StringLiteral:
			if v.Value == "HID" {
				v.Value = hid
			}
		}
		ast.RewriteChildren(x, sub)
		return x
	}
	return sub(ev.Body[0]).(ast.Statement)
}

// keysUse reports a node is matches in the key statements, outside nested
// functions.
func keysUse(stmts []ast.Statement, is func(ast.Node) bool) bool {
	found := false
	for _, st := range stmts {
		ast.Inspect(st, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassExpression, *ast.ClassDeclaration:
				return false
			}
			if is(n) {
				found = true
			}
			return !found
		})
	}
	return found
}

// repeatedFieldsAtRunTime makes each repeat of a class field's key (`[y] =
// 1; [y] = 2;`, `a = 1; a = 2;`) write the property the first defined:
// every initializer runs in order, and the property keeps its first place.
func repeatedFieldsAtRunTime(prog *ast.Program) {
	ast.Inspect(prog, func(n ast.Node) bool {
		cd, ok := n.(*ast.ClassDeclaration)
		if !ok {
			return true
		}
		type key struct {
			static bool
			name   string
		}
		seen := map[key]bool{}
		for i := range cd.Fields {
			f := &cd.Fields[i]
			if f.KeyExpr != nil || f.Declare || f.Name == "" || strings.HasPrefix(f.Name, "#") || strings.HasPrefix(f.Name, ast.RuntimeKeyPrefix) {
				continue
			}
			k := key{f.Static, f.Name}
			if !seen[k] {
				seen[k] = true
				continue
			}
			// The first definition made a writable own data property, so
			// the repeat's definition is an assignment to it, in its place:
			// a hidden (private) field whose initializer is `this[k] = init`.
			pos := f.Pos
			var target ast.Expression = ast.NewMemberExpression(ast.NewThisExpression(pos), f.Name, pos)
			if f.Key != nil {
				target = ast.NewIndexExpression(ast.NewThisExpression(pos), f.Key, pos) // (a symbol's binding)
			}
			var init ast.Expression = ast.NewNullLiteral(true, pos)
			if f.Initializer != nil {
				init = f.Initializer
			}
			assign := ast.NewAssignmentExpression("=", target, init, pos)
			f.Initializer = ast.NewSequenceExpression([]ast.Expression{assign, ast.NewNullLiteral(true, pos)}, pos)
			// (A private name: no key lists, JSON or inspection see it.)
			f.Name, f.Key, f.Type = fmt.Sprintf("#__kml_rep%d_%d", pos.Line, pos.Col), nil, nil
		}
		return true
	})
}

// deletedStaticsAtRunTime makes each static field a `delete` of the class
// by name removes (`delete C.f`, `delete C["f"]`) a property its definition
// creates when the class is evaluated, in its place (a hidden private
// static whose initializer assigns it): a static field is a configurable
// own property of the class, which delete removes.
func deletedStaticsAtRunTime(prog *ast.Program) {
	deleted := map[string]map[string]bool{} // class name → keys
	note := func(obj ast.Expression, key string) {
		id, ok := obj.(*ast.Identifier)
		if !ok {
			return
		}
		if deleted[id.Name] == nil {
			deleted[id.Name] = map[string]bool{}
		}
		deleted[id.Name][key] = true
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		u, ok := n.(*ast.UnaryExpression)
		if !ok || u.Op != "delete" {
			return true
		}
		switch a := u.Arg.(type) {
		case *ast.MemberExpression:
			note(a.Object, a.Property)
		case *ast.IndexExpression:
			switch k := a.Index.(type) {
			case *ast.StringLiteral:
				note(a.Object, k.Value)
			case *ast.NumberLiteral:
				note(a.Object, k.Value)
			}
		}
		return true
	})
	if len(deleted) == 0 {
		return
	}
	// A class expression is named by the binding it initializes too.
	held := map[*ast.ClassDeclaration]string{}
	ast.Inspect(prog, func(n ast.Node) bool {
		if v, ok := n.(*ast.VarDeclaration); ok {
			if ce, ok := v.Init.(*ast.ClassExpression); ok && ce.Decl != nil {
				held[ce.Decl] = v.Name
			}
		}
		return true
	})
	ast.Inspect(prog, func(n ast.Node) bool {
		cd, ok := n.(*ast.ClassDeclaration)
		if !ok {
			return true
		}
		keys := map[string]bool{}
		for _, nm := range []string{cd.Name, held[cd]} {
			for k := range deleted[nm] {
				keys[k] = true
			}
		}
		if len(keys) == 0 {
			return true
		}
		for i := range cd.Fields {
			f := &cd.Fields[i]
			if !f.Static || f.KeyExpr != nil || f.Key != nil || f.Declare || !keys[f.Name] || strings.HasPrefix(f.Name, "#") {
				continue
			}
			pos := f.Pos
			var init ast.Expression = ast.NewNullLiteral(true, pos)
			if f.Initializer != nil {
				init = f.Initializer
			}
			assign := ast.NewAssignmentExpression("=", ast.NewIndexExpression(ast.NewThisExpression(pos), ast.NewStringLiteral(f.Name, pos), pos), init, pos)
			f.Initializer = ast.NewSequenceExpression([]ast.Expression{assign, ast.NewNullLiteral(true, pos)}, pos)
			f.Name, f.Type = fmt.Sprintf("#__kml_sdel%d_%d", pos.Line, pos.Col), nil
		}
		return true
	})
}

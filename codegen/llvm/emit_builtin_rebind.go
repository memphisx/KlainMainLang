package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/lib"
)

// jsRebindAssignedBuiltins lowers each builtin global the program's own
// code assigns (`parseInt = 5`, `isNaN++`, a sloppy `eval = 42`) to a
// global variable of the program seeded with the builtin's value, as
// JavaScript's global object holds it: `var __kml_g_N = N;` ahead of the
// program's first statement, every reference to the builtin renamed to it.
// A call of `eval` keeps its name (a direct eval of the builtin).
func jsRebindAssignedBuiltins(prog *ast.Program, libStmts map[ast.Statement]string) {
	assigned := map[string]bool{}
	for _, st := range prog.Body {
		if libStmts[st] != "" {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignmentExpression:
				if id, ok := x.Left.(*ast.Identifier); ok {
					assigned[id.Name] = true
				}
			case *ast.UpdateExpression:
				if id, ok := x.Arg.(*ast.Identifier); ok {
					assigned[id.Name] = true
				}
			}
			return true
		})
	}
	if len(assigned) == 0 {
		return
	}
	libProgs, err := lib.Programs()
	if err != nil {
		return
	}
	own := map[ast.Node]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		own[n] = true
		return true
	})
	b := binder.BindWith(prog, binder.Options{AnnexB: true, Lib: libProgs})
	builtin := func(id *ast.Identifier) bool {
		if !assigned[id.Name] {
			return false
		}
		sym, _ := b.Resolve(id)
		if sym == nil || len(sym.Declarations) == 0 || sym.Flags&binder.Value == 0 {
			return false
		}
		for _, d := range sym.Declarations {
			if own[d.Node] {
				return false
			}
		}
		return true
	}
	// The builtins the program assigns, and every reference to each.
	var refs []*ast.Identifier
	names := map[string]bool{}
	directEval := map[*ast.Identifier]bool{}
	for _, st := range prog.Body {
		if libStmts[st] != "" {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpression); ok {
				if id, ok := c.Callee.(*ast.Identifier); ok && id.Name == "eval" {
					directEval[id] = true
				}
			}
			if id, ok := n.(*ast.Identifier); ok && builtin(id) {
				refs = append(refs, id)
			}
			return true
		})
	}
	for _, id := range refs {
		if !directEval[id] {
			names[id.Name] = true
		}
	}
	if len(names) == 0 {
		return
	}
	var seeds []ast.Statement
	for _, n := range sortedSet(names) {
		seeds = append(seeds, ast.NewVarDeclaration("var", "__kml_g_"+n, ast.AnyAnnotation(""), ast.NewIdentifier(n, ast.Pos{}), ast.Pos{}))
	}
	for _, id := range refs {
		if !directEval[id] {
			id.Name = "__kml_g_" + id.Name
		}
	}
	at := 0
	for at < len(prog.Body) && libStmts[prog.Body[at]] != "" {
		at++
	}
	prog.Body = append(prog.Body[:at:at], append(seeds, prog.Body[at:]...)...)
}

// jsClassNameAssigns lowers an assignment to a class's own name inside its
// body (`class C { m() { C = 42; } }`), where the name is the class's
// immutable inner binding, to its right side's evaluation then V8's
// TypeError, as JavaScript does; the left side becomes a sink the program
// never reads.
func jsClassNameAssigns(prog *ast.Program, libStmts map[ast.Statement]string) {
	var sites []*ast.AssignmentExpression
	var inner func(cd *ast.ClassDeclaration)
	inner = func(cd *ast.ClassDeclaration) {
		if cd.Name == "" || cd.Name == "$ClassExpr" {
			return
		}
		shadow := func(n ast.Node) bool {
			// A nested function or class naming its own binding cd.Name
			// shadows the class's.
			switch x := n.(type) {
			case *ast.FunctionDeclaration:
				for _, p := range x.Params {
					if p.Name == cd.Name {
						return true
					}
				}
				return x.Name == cd.Name
			case *ast.VarDeclaration:
				return x.Name == cd.Name
			}
			return false
		}
		shadowed := false
		ast.Inspect(cd, func(n ast.Node) bool {
			if n != ast.Node(cd) && shadow(n) {
				shadowed = true
			}
			return !shadowed
		})
		if shadowed {
			return
		}
		ast.Inspect(cd, func(n ast.Node) bool {
			if a, ok := n.(*ast.AssignmentExpression); ok && n != ast.Node(cd) {
				if id, ok := a.Left.(*ast.Identifier); ok && id.Name == cd.Name && (a.Op == "=" || len(a.Op) == 2) {
					sites = append(sites, a)
				}
			}
			return true
		})
	}
	for _, st := range prog.Body {
		if libStmts[st] != "" {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ClassDeclaration:
				inner(x)
			case *ast.ClassExpression:
				inner(x.Decl)
			}
			return true
		})
	}
	if len(sites) == 0 {
		return
	}
	for _, a := range sites {
		pos := a.GetPos()
		right := a.Right
		if a.Op != "=" {
			right = ast.NewBinaryExpression(a.Op[:1], ast.NewIdentifier(a.Left.(*ast.Identifier).Name, pos), right, pos)
		}
		throw := ast.NewArrowFunction(nil, nil, nil, ast.NewBlockStatement([]ast.Statement{
			ast.NewThrowStatement(ast.NewNewErrorExpression("TypeError", ast.NewStringLiteral("Assignment to constant variable.", pos), pos), pos),
		}, pos), pos)
		a.Op = "="
		a.Left = ast.NewIdentifier("__kml_g_constsink", pos)
		a.Right = ast.NewSequenceExpression([]ast.Expression{right, ast.NewCallExpression(throw, nil, pos)}, pos)
	}
	at := 0
	for at < len(prog.Body) && libStmts[prog.Body[at]] != "" {
		at++
	}
	sink := ast.NewVarDeclaration("var", "__kml_g_constsink", ast.AnyAnnotation(""), nil, ast.Pos{})
	prog.Body = append(prog.Body[:at:at], append([]ast.Statement{sink}, prog.Body[at:]...)...)
}

// jsClassBindingAssigns gives a class declaration whose binding the code
// outside its body assigns (`class C {} … C = null`) a mutable binding of
// its own, as JavaScript's class declaration has beside the class's inner,
// immutable one: `let __kml_c_C = C;` right after the declaration, each
// outside reference renamed to it. The body keeps naming the class.
func jsClassBindingAssigns(prog *ast.Program, libStmts map[ast.Statement]string) {
	classNames := map[string]bool{}
	assigned := map[string]bool{}
	for _, st := range prog.Body {
		if libStmts[st] != "" {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.ClassDeclaration:
				classNames[x.Name] = true
			case *ast.AssignmentExpression:
				if id, ok := x.Left.(*ast.Identifier); ok {
					assigned[id.Name] = true
				}
			case *ast.UpdateExpression:
				if id, ok := x.Arg.(*ast.Identifier); ok {
					assigned[id.Name] = true
				}
			}
			return true
		})
	}
	any := false
	for n := range assigned {
		if classNames[n] {
			any = true
		}
	}
	if !any {
		return
	}
	libProgs, err := lib.Programs()
	if err != nil {
		return
	}
	b := binder.BindWith(prog, binder.Options{AnnexB: true, Lib: libProgs})
	// Each class declaration, by its symbol, and the nodes inside it.
	declOf := map[*binder.Symbol]*ast.ClassDeclaration{}
	insideOf := map[*ast.ClassDeclaration]map[ast.Node]bool{}
	symOf := func(id *ast.Identifier) (*binder.Symbol, *ast.ClassDeclaration) {
		sym, _ := b.Resolve(id)
		if sym == nil {
			return nil, nil
		}
		for _, d := range sym.Declarations {
			if cd, ok := d.Node.(*ast.ClassDeclaration); ok {
				return sym, cd
			}
		}
		return nil, nil
	}
	inside := func(cd *ast.ClassDeclaration, n ast.Node) bool {
		m := insideOf[cd]
		if m == nil {
			m = map[ast.Node]bool{}
			ast.Inspect(cd, func(x ast.Node) bool {
				m[x] = true
				return true
			})
			insideOf[cd] = m
		}
		return m[n]
	}
	// The classes assigned from outside their bodies.
	rebound := map[*ast.ClassDeclaration]bool{}
	for _, st := range prog.Body {
		if libStmts[st] != "" {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			var id *ast.Identifier
			switch x := n.(type) {
			case *ast.AssignmentExpression:
				id, _ = x.Left.(*ast.Identifier)
			case *ast.UpdateExpression:
				id, _ = x.Arg.(*ast.Identifier)
			}
			if id != nil && classNames[id.Name] {
				if sym, cd := symOf(id); cd != nil && !inside(cd, id) {
					declOf[sym] = cd
					rebound[cd] = true
				}
			}
			return true
		})
	}
	if len(rebound) == 0 {
		return
	}
	// Rename every outside reference, then declare the binding after each.
	for _, st := range prog.Body {
		if libStmts[st] != "" {
			continue
		}
		ast.Inspect(st, func(n ast.Node) bool {
			if id, ok := n.(*ast.Identifier); ok && classNames[id.Name] {
				if sym, cd := symOf(id); cd != nil && declOf[sym] == cd && !inside(cd, id) {
					id.Name = "__kml_c_" + cd.Name
				}
			}
			return true
		})
	}
	insert := func(list []ast.Statement) []ast.Statement {
		var out []ast.Statement
		for _, st := range list {
			out = append(out, st)
			if cd, ok := st.(*ast.ClassDeclaration); ok && rebound[cd] {
				out = append(out, ast.NewVarDeclaration("let", "__kml_c_"+cd.Name, ast.AnyAnnotation(""), ast.NewIdentifier(cd.Name, cd.GetPos()), cd.GetPos()))
			}
		}
		return out
	}
	prog.Body = insert(prog.Body)
	ast.Inspect(prog, func(n ast.Node) bool {
		if blk, ok := n.(*ast.BlockStatement); ok {
			blk.Body = insert(blk.Body)
		}
		return true
	})
}

// jsDropReplacedAccessors removes each class accessor that a later one of
// the same kind, placement and name replaces (`get b() {…} get ['b']()
// {…}`): JavaScript defines both in order, the last one winning.
func jsDropReplacedAccessors(prog *ast.Program) {
	ast.Inspect(prog, func(n ast.Node) bool {
		var cd *ast.ClassDeclaration
		switch x := n.(type) {
		case *ast.ClassDeclaration:
			cd = x
		case *ast.ClassExpression:
			cd = x.Decl
		}
		if cd == nil {
			return true
		}
		seen := map[string]bool{}
		keep := make([]*ast.FunctionDeclaration, len(cd.Methods))
		k := len(keep)
		for i := len(cd.Methods) - 1; i >= 0; i-- {
			m := cd.Methods[i]
			if m.AccessorKind != "" && m.Body != nil {
				key := m.AccessorKind + "\x00" + m.Name
				if m.IsStatic {
					key = "static\x00" + key
				}
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			k--
			keep[k] = m
		}
		cd.Methods = keep[k:]
		return true
	})
}

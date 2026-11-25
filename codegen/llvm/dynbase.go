package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
	"KlainMainLang/lib"
	"KlainMainLang/parser"
	"KlainMainLang/resolver"
	"fmt"
	"strings"
)

// dynbase.go — a class whose base is a value known only at run time
// (`const Base = c ? A : B; class C extends Base {}`, a mixin's `Base:
// Ctor` parameter).
//
// A class's layout is fixed at compile time, so its base must be a class
// the compiler knows. The program is the whole world: the classes the
// value can hold are the classes it names, or, for a constructor type
// (`new (...a: any[]) => T`), every class of the program assignable to it
// (checker.DynamicBase). The class is specialized: one copy per candidate,
// each extending that class, and the declaration evaluates to the copy
// whose base is the value it holds then:
//
//	class C extends Base { … }
//	// becomes
//	const C = Base === A ? class C extends A { … } : Base === B ? class C extends B { … } : <TypeError>;
//
// Each copy is a real subclass of its base: instanceof, the prototype
// chain, super calls and the base's fields are those of that class. The
// rewrite runs before the checker, so the copies are typed as written.

// specializeDynamicBases rewrites every class declaration and class
// expression of prog whose base is a run-time value into a selection of
// its specializations.
func (e *Emitter) specializeDynamicBases(prog *ast.Program) {
	bindQualifiedBases(prog)
	// JavaScript makes the class from the value at run time
	// (checker.DynamicBase).
	// The library's modules compile in the strict lane, typed as written:
	// in a JavaScript program only its own classes may need the pass.
	own := prog
	if e.compatJS() {
		own = e.userStatements(prog)
	}
	unbound := unboundBaseNames(own)
	if len(unbound) > 0 {
		// An error class's base (`extends Error`) is no unbound name and
		// needs no rewrite: only another base costs the checker pass below
		// (a host class's, `extends Map`, is specialized by it).
		ast.Inspect(own, func(n ast.Node) bool {
			if cd, ok := n.(*ast.ClassDeclaration); ok && unbound[cd.BaseClass] && e.isErrorRootKind(cd.BaseClass) {
				delete(unbound, cd.BaseClass)
			}
			return true
		})
	}
	if !mayHaveDynamicBase(own) && len(unbound) == 0 {
		return
	}
	libProgs, err := lib.Programs()
	if err != nil {
		return
	}
	b := binder.BindWith(prog, binder.Options{AnnexB: e.compatJS(), Lib: libProgs})
	c := checker.NewWith(b, e.opts)
	c.Check()
	cands := map[*ast.ClassDeclaration][]*ast.ClassDeclaration{}
	rewritten := map[*ast.ClassDeclaration]bool{}
	throwing := map[*ast.ClassDeclaration]bool{}
	// Every class first: lifting a candidate rewrites the program.
	var all []*ast.ClassDeclaration
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ClassDeclaration:
			all = append(all, x)
		case *ast.ClassExpression:
			all = append(all, x.Decl)
		}
		return true
	})
	for _, cd := range all {
		// A base no binding names at all, nor any global this runtime or
		// the program provides: reading it throws ReferenceError, so the
		// definition does (no candidate).
		if unbound[cd.BaseClass] && c.UnboundBase(cd) && !e.knownGlobalBase(cd) {
			throwing[cd] = true
			continue
		}
		ks, hidden, hosts, dyn := c.DynamicBase(cd)
		if !dyn {
			continue
		}
		for _, h := range hosts {
			ks = append(ks, &ast.ClassDeclaration{Name: h}) // a builtin: only its name is read
		}
		lifted := true
		for _, h := range hidden {
			if name, ok := e.liftDynBaseCandidate(prog, b, h); ok {
				h.Name = name
				ks = append(ks, h)
			} else {
				lifted = false
			}
		}
		// No candidate at all: only a value that is no constructor can
		// reach the base, and the definition throws. A candidate that
		// cannot be named here keeps the class as written (codegen's
		// rejection).
		if lifted {
			cands[cd] = ks
			rewritten[cd] = true
		}
	}
	if len(throwing) > 0 {
		// The declaration statement reads its base first and throws; the
		// class itself, kept without the base, is what the code after it
		// (never reached) is typed against.
		var unb func(list []ast.Statement) []ast.Statement
		unb = func(list []ast.Statement) []ast.Statement {
			var out []ast.Statement
			for _, st := range list {
				if cd, ok := st.(*ast.ClassDeclaration); ok && throwing[cd] {
					out = append(out, ast.NewExpressionStatement(dynBaseThrow(cd.BaseClass, cd.GetPos()), cd.GetPos()))
					cd.BaseClass, cd.BaseTypeArgs = "", nil
					dropSuperBodies(cd)
					delete(throwing, cd)
				}
				out = append(out, st)
			}
			return out
		}
		prog.Body = unb(prog.Body)
		ast.Inspect(prog, func(n ast.Node) bool {
			if blk, ok := n.(*ast.BlockStatement); ok {
				blk.Body = unb(blk.Body)
			}
			return true
		})
	}
	if len(rewritten) == 0 {
		return
	}
	// Statements first (a declaration becomes a constant), then the
	// class expressions left.
	var stmts func(list []ast.Statement) []ast.Statement
	stmts = func(list []ast.Statement) []ast.Statement {
		for i, st := range list {
			cd, ok := st.(*ast.ClassDeclaration)
			if !ok || !rewritten[cd] {
				continue
			}
			sel := e.dynBaseSelect(cd, cands[cd])
			v := ast.NewVarDeclaration("const", cd.Name, nil, sel, cd.GetPos())
			list[i] = v
			// `x: C` still names the instance type.
			if len(cd.TypeParams) == 0 {
				it := &ast.TypeReference{Name: "InstanceType", TypeArgs: []ast.TypeNode{&ast.TypeQuery{Name: cd.Name}}}
				list = append(list[:i+1], append([]ast.Statement{ast.NewTypeAliasDeclaration(cd.Name, it, cd.GetPos())}, list[i+1:]...)...)
			}
			delete(rewritten, cd)
		}
		return list
	}
	prog.Body = stmts(prog.Body)
	ast.Inspect(prog, func(n ast.Node) bool {
		if blk, ok := n.(*ast.BlockStatement); ok {
			blk.Body = stmts(blk.Body)
		}
		return true
	})
	// `const E = class extends K {}`: each copy takes the binding's name,
	// as the class expression written there would (NamedEvaluation).
	ast.Inspect(prog, func(n ast.Node) bool {
		if v, ok := n.(*ast.VarDeclaration); ok {
			if ce, ok := v.Init.(*ast.ClassExpression); ok && rewritten[ce.Decl] && anonymousClass(ce) {
				if e.dynBaseNames == nil {
					e.dynBaseNames = map[*ast.ClassDeclaration]string{}
				}
				e.dynBaseNames[ce.Decl] = v.Name
			}
		}
		return true
	})
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rw)
		if ce, ok := n.(*ast.ClassExpression); ok && rewritten[ce.Decl] {
			return e.dynBaseSelect(ce.Decl, cands[ce.Decl])
		}
		return n
	}
	rw(prog)
}

// dropSuperBodies empties each member body of cd naming `super`, a class
// kept without its base after its definition throws: never run, and with
// no base to call.
func dropSuperBodies(cd *ast.ClassDeclaration) {
	usesSuper := func(n ast.Node) bool {
		found := false
		ast.Inspect(n, func(m ast.Node) bool {
			if _, ok := m.(*ast.SuperExpression); ok {
				found = true
			}
			return !found
		})
		return found
	}
	fns := append([]*ast.FunctionDeclaration{cd.Constructor}, cd.Methods...)
	for _, fd := range fns {
		if fd != nil && fd.Body != nil && usesSuper(fd.Body) {
			fd.Body = ast.NewBlockStatement(nil, fd.Body.GetPos())
		}
	}
	for i := range cd.Fields {
		if cd.Fields[i].Initializer != nil && usesSuper(cd.Fields[i].Initializer) {
			cd.Fields[i].Initializer = nil
		}
	}
}

// dynBaseSelect is `base === K1 ? class extends K1 {…} : … : <TypeError>`
// over cd's candidate bases.
func (e *Emitter) dynBaseSelect(cd *ast.ClassDeclaration, ks []*ast.ClassDeclaration) ast.Expression {
	pos := cd.GetPos()
	sel := dynBaseThrow(cd.BaseClass, pos)
	for i := len(ks) - 1; i >= 0; i-- {
		cp := ast.Clone(cd)
		cp.BaseClass = ks[i].Name
		if name, ok := e.dynBaseNames[cd]; ok {
			cp.Name = name
			if e.anonClasses == nil {
				e.anonClasses = map[*ast.ClassDeclaration]bool{}
			}
			e.anonClasses[cp] = true
		}
		test := ast.NewBinaryExpression("===", ast.NewIdentifier(cd.BaseClass, pos), ast.NewIdentifier(ks[i].Name, pos), pos)
		sel = ast.NewConditionalExpression(test, ast.NewClassExpression(cp, pos), sel, pos)
	}
	return sel
}

// dynBaseThrow is the TypeError a class definition throws for a base that
// is no constructor (one no candidate is), as an expression.
func dynBaseThrow(base string, pos ast.Pos) ast.Expression {
	src := `(() => { throw new TypeError("Class extends value " + String(` + base + `) + " is not a constructor or null"); })()`
	prog, err := parser.Parse(src)
	if err == nil && len(prog.Body) == 1 {
		if es, ok := prog.Body[0].(*ast.ExpressionStatement); ok {
			return es.Expr
		}
	}
	return ast.NewIdentifier("undefined", pos)
}

// mayHaveDynamicBase is a cheap syntactic test: some class extends a name
// no class of the program declares but a variable or parameter does.
func mayHaveDynamicBase(prog *ast.Program) bool {
	classes, values := map[string]bool{}, map[string]bool{}
	var bases []string
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ClassDeclaration:
			classes[x.Name] = true
			if x.BaseClass != "" && !x.BaseQualified && x.BaseCall == nil {
				bases = append(bases, x.BaseClass)
			}
		case *ast.VarDeclaration:
			values[x.Name] = true
		case *ast.ForOfStatement:
			values[x.VarName] = true
		case *ast.FunctionDeclaration:
			for _, p := range x.Params {
				values[p.Name] = true
			}
		case *ast.FunctionExpression:
			for _, p := range x.Params {
				values[p.Name] = true
			}
		case *ast.ArrowFunction:
			for _, p := range x.Params {
				values[p.Name] = true
			}
		}
		return true
	})
	for _, b := range bases {
		if !classes[b] && values[b] {
			return true
		}
	}
	return false
}

// liftDynBaseCandidate makes the class expression h, a candidate base not
// visible by name where the class extending it is, a top-level class
// declaration: placed before the top-level statement holding it, the
// expression reading its name. A class naming locals of its function stays
// where it is (false). The lifted class is one class however often its
// expression is evaluated, as codegen hoists such a class expression
// anyway.
func (e *Emitter) liftDynBaseCandidate(prog *ast.Program, b *binder.Binding, h *ast.ClassDeclaration) (string, bool) {
	if name, ok := e.dynBaseLifted[h]; ok {
		return name, true
	}
	if len(classLocalRefs(b, h, nil)) > 0 || len(h.TypeParams) > 0 {
		return "", false
	}
	at := -1
	var expr *ast.ClassExpression
	for i, st := range prog.Body {
		ast.Inspect(st, func(n ast.Node) bool {
			if ce, ok := n.(*ast.ClassExpression); ok && ce.Decl == h {
				at, expr = i, ce
			}
			return expr == nil
		})
		if expr != nil {
			break
		}
	}
	var blk *ast.BlockStatement
	if expr == nil {
		// A declaration in a function's block, naming none of its locals
		// (a call-site copy of a function's own class).
		for i, st := range prog.Body {
			ast.Inspect(st, func(n ast.Node) bool {
				if bs, ok := n.(*ast.BlockStatement); ok && blk == nil {
					for _, x := range bs.Body {
						if x == ast.Statement(h) {
							at, blk = i, bs
						}
					}
				}
				return blk == nil
			})
			if blk != nil {
				break
			}
		}
		if blk == nil || h.Name == "$ClassExpr" {
			return "", false
		}
	}
	e.classExprSeq++
	shown := h.Name
	if shown == "$ClassExpr" {
		shown = ""
	}
	name := fmt.Sprintf("dynbase__kml_cx%d", e.classExprSeq)
	if shown != "" {
		name = fmt.Sprintf("%s__kml_cx%d", shown, e.classExprSeq)
		resolver.RenameInClass(h, h.Name, name)
	}
	if e.classShownNames == nil {
		e.classShownNames = map[string]string{}
	}
	e.classShownNames[name] = shown
	if e.dynBaseLifted == nil {
		e.dynBaseLifted = map[*ast.ClassDeclaration]string{}
	}
	e.dynBaseLifted[h] = name
	if blk != nil {
		// The declaration's binding now reads the lifted class.
		for i, x := range blk.Body {
			if x == ast.Statement(h) {
				blk.Body[i] = ast.NewVarDeclaration("const", h.Name, nil, ast.NewIdentifier(name, h.GetPos()), h.GetPos())
			}
		}
		h.Name = name
		prog.Body = append(prog.Body[:at:at], append([]ast.Statement{h}, prog.Body[at:]...)...)
		return name, true
	}
	h.Name = name
	ref := ast.NewIdentifier(name, expr.GetPos())
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		if n == ast.Node(expr) {
			return ref
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	prog.Body[at] = rw(prog.Body[at]).(ast.Statement)
	prog.Body = append(prog.Body[:at:at], append([]ast.Statement{h}, prog.Body[at:]...)...)
	return name, true
}

// bindQualifiedBases rewrites a class declaration extending a member of a
// variable (`class T extends events.EventEmitter`, events a require()'s
// value) to extend a constant holding that member, declared before it:
// a base value like any other. A namespace's member is already a class
// the resolver named.
func bindQualifiedBases(prog *ast.Program) {
	values, classes := map[string]bool{}, map[string]*ast.ClassDeclaration{}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.VarDeclaration:
			values[x.Name] = true
		case *ast.ClassDeclaration:
			classes[x.Name] = x
		}
		return true
	})
	// staticClass is the class a class's static getter always answers
	// (`static get EventEmitter() { return EventEmitter; }`), or "".
	staticClass := func(owner *ast.ClassDeclaration, name string) string {
		for _, m := range owner.Methods {
			if !m.IsStatic || m.AccessorKind != "get" || m.Name != name || m.Body == nil || len(m.Body.Body) != 1 {
				continue
			}
			if r, ok := m.Body.Body[0].(*ast.ReturnStatement); ok {
				if id, ok := r.Value.(*ast.Identifier); ok && classes[id.Name] != nil {
					return id.Name
				}
			}
		}
		return ""
	}
	// Its reads elsewhere (`new events.EventEmitter()`) are that class too.
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rw)
		if ne, ok := n.(*ast.NewExpression); ok && ne.Qualified && len(ne.QualifierPath) == 1 && classes[ne.Qualifier] != nil {
			if k := staticClass(classes[ne.Qualifier], ne.ClassName); k != "" {
				ne.ClassName, ne.Qualified, ne.Qualifier, ne.QualifierPath = k, false, "", nil
			}
		}
		if m, ok := n.(*ast.MemberExpression); ok && !m.Optional {
			if id, ok := m.Object.(*ast.Identifier); ok && classes[id.Name] != nil {
				if k := staticClass(classes[id.Name], m.Property); k != "" {
					return ast.NewIdentifier(k, m.GetPos())
				}
			}
		}
		return n
	}
	rw(prog)
	seq := 0
	fix := func(list []ast.Statement) []ast.Statement {
		for i := 0; i < len(list); i++ {
			cd, ok := list[i].(*ast.ClassDeclaration)
			if !ok || !cd.BaseQualified || cd.BaseQualifier == "" {
				continue
			}
			parts := strings.Split(cd.BaseQualifier, ".")
			// A class's static (`require('events').EventEmitter`, the
			// module being the class) is a value as well.
			if owner := classes[cd.BaseQualifier]; owner != nil {
				if k := staticClass(owner, cd.BaseClass); k != "" {
					cd.BaseClass, cd.BaseQualified, cd.BaseQualifier = k, false, ""
					continue
				}
			}
			// A global object a global module provides (`WebAssembly`) is a
			// value too, whatever classes its module declares internally.
			globalValue := prog.GlobalLinks[parts[0]] != "" && classes[parts[0]] == nil && !values[parts[0]]
			if !globalValue && (!(values[parts[0]] || classes[parts[0]] != nil) || classes[cd.BaseClass] != nil) {
				continue // a namespace's class, named by the resolver
			}
			pos := cd.GetPos()
			root := parts[0]
			if globalValue {
				root = prog.GlobalLinks[root]
			}
			var ref ast.Expression = ast.NewIdentifier(root, pos)
			for _, p := range parts[1:] {
				ref = ast.NewMemberExpression(ref, p, pos)
			}
			ref = ast.NewMemberExpression(ref, cd.BaseClass, pos)
			seq++
			name := fmt.Sprintf("%s__kml_base%d", cd.Name, seq)
			cd.BaseClass, cd.BaseQualified, cd.BaseQualifier = name, false, ""
			list = append(list[:i], append([]ast.Statement{ast.NewVarDeclaration("const", name, nil, ref, pos)}, list[i:]...)...)
			i++
		}
		return list
	}
	prog.Body = fix(prog.Body)
	ast.Inspect(prog, func(n ast.Node) bool {
		if blk, ok := n.(*ast.BlockStatement); ok {
			blk.Body = fix(blk.Body)
		}
		return true
	})
}

// unboundBaseNames are the bare names classes of prog extend that nothing
// in prog declares or assigns (as a binding, or as a property of the
// global object): candidates for a base no binding names.
func unboundBaseNames(prog *ast.Program) map[string]bool {
	declared := map[string]bool{}
	var bases []string
	ast.Inspect(prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ClassDeclaration:
			declared[x.Name] = true
			if x.BaseClass != "" && !x.BaseQualified && x.BaseCall == nil {
				bases = append(bases, x.BaseClass)
			}
		case *ast.VarDeclaration:
			declared[x.Name] = true
		case *ast.FunctionDeclaration:
			declared[x.Name] = true
		case *ast.AssignmentExpression:
			switch t := x.Left.(type) {
			case *ast.Identifier:
				declared[t.Name] = true
			case *ast.MemberExpression:
				declared[t.Property] = true
			}
		}
		return true
	})
	out := map[string]bool{}
	for _, b := range bases {
		if !declared[b] && (prog.GlobalLinks == nil || prog.GlobalLinks[b] == "") && !prog.GlobalObjectNames[b] {
			out[b] = true
		}
	}
	return out
}

// knownGlobalBase reports cd's base a builtin this compiler makes a class
// from (an Error kind, a host type).
func (e *Emitter) knownGlobalBase(cd *ast.ClassDeclaration) bool {
	return e.isErrorRootKind(cd.BaseClass) || e.hostBaseOf(cd) != nil || e.isHostLayoutBase(cd.BaseClass)
}

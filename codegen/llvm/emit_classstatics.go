package llvm

import (
	"fmt"
	"sort"
	"strings"

	"KlainMainLang/ast"
)

// emit_classstatics.go — the statics of a class that is a class per
// evaluation (TDD-00242). Each evaluation's record has a static area of its
// own (unitregsrc `__kml_classrec_statics`), laid out as every static field
// the class has, then an own flag per inherited one. Code reaches the area
// of the record it runs against:
//
//   - in the function declaring the class, the local holding this
//     evaluation's record;
//   - in a static member, the receiver its call passed (a direct call
//     passes the record it names; a dispatch through the class as a value
//     notes the record it looked up);
//   - in an instance member, the record its instance was built from.
//
// A class with no record keeps its statics in globals.

// staticArea is a record class's static layout.
type staticArea struct {
	ir   string         // the area's struct type
	slot map[string]int // a static field's index
	own  map[string]int // an inherited static field's own flag's index
}

// recordStaticClass reports whether class c keeps its statics per
// evaluation.
func (e *Emitter) recordStaticClass(c string) bool {
	return e.classRecords[c] && len(e.classes[c].StaticFieldTypes) > 0
}

// staticAreaOf is record class c's static layout.
func (e *Emitter) staticAreaOf(c string) *staticArea {
	if a, ok := e.staticAreas[c]; ok {
		return a
	}
	info := e.classes[c]
	names := make([]string, 0, len(info.StaticFieldTypes))
	for n := range info.StaticFieldTypes {
		names = append(names, n)
	}
	sort.Strings(names)
	a := &staticArea{slot: map[string]int{}, own: map[string]int{}}
	var parts []string
	for _, n := range names {
		a.slot[n] = len(parts)
		parts = append(parts, storageIR(info.StaticFieldTypes[n]))
	}
	for _, n := range names {
		if _, own := info.OwnStaticFieldTypes[n]; !own {
			a.own[n] = len(parts)
			parts = append(parts, "i1")
		}
	}
	a.ir = "{ " + strings.Join(parts, ", ") + " }"
	if e.staticAreas == nil {
		e.staticAreas = map[string]*staticArea{}
	}
	e.staticAreas[c] = a
	return a
}

// recordFor is the record of class c the code being emitted runs against,
// or a null pointer.
func (e *Emitter) recordFor(c string) string {
	if sym, ok := e.lookup(c + classRecSuffix); ok {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", r, sym.Ptr))
		return r
	}
	if sym, ok := e.lookup("this"); ok && sym.Ty.IsClass() && !sym.Ty.IsDynamic() && (sym.Ty.ClassName == c || e.classDerives(sym.Ty.ClassName, c)) {
		this, of, up := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", this, sym.Ptr))
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classrec_of("+irPtr+" %s)", of, this))
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classrec_up("+irPtr+" %s, "+irPtr+" %s)", up, of, e.classRefPtr(c)))
		return up
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classrec_static_cur("+irPtr+" %s)", r, e.classRefPtr(c)))
	return r
}

// staticAreaPtr is the static area of the record of class c the code runs
// against (the class's own when there is none).
func (e *Emitter) staticAreaPtr(c string) string {
	a := e.staticAreaOf(c)
	rec := e.recordFor(c)
	area := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classrec_statics("+irPtr+" %s, "+irPtr+" %s, i64 ptrtoint ("+irPtr+" getelementptr (%s, "+irPtr+" null, i32 1) to i64))", area, rec, e.classRefPtr(c), a.ir))
	return area
}

// staticSlotPtr is the storage of class c's static field f: its global, or
// its slot in the record's area.
func (e *Emitter) staticSlotPtr(c, f string) string {
	if !e.recordStaticClass(c) {
		return "@" + llvmSafeSymbol(c+"_static_"+f)
	}
	a := e.staticAreaOf(c)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, "+irPtr+" %s, i32 0, i32 %d", p, a.ir, e.staticAreaPtr(c), a.slot[f]))
	return p
}

// staticOwnPtr is the own flag of class c's inherited static field f.
func (e *Emitter) staticOwnPtr(c, f string) string {
	if !e.recordStaticClass(c) {
		return "@" + llvmSafeSymbol(c+"_static_"+f+"__own")
	}
	a := e.staticAreaOf(c)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds %s, "+irPtr+" %s, i32 0, i32 %d", p, a.ir, e.staticAreaPtr(c), a.own[f]))
	return p
}

// bindStaticReceiver opens a static member of record class c (or its
// static initialization): the receiver its call passed is the record its
// statics are read from, and each local class it extends reads its own up
// the chain.
func (e *Emitter) bindStaticReceiver(c string) {
	if !e.classRecords[c] {
		return
	}
	for b, i := c, 0; b != "" && i < 64 && e.classRecords[b]; i++ {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classrec_static_cur("+irPtr+" %s)", r, e.classRefPtr(b)))
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", r, slot))
		e.define(b+classRecSuffix, Symbol{Ptr: slot, Ty: TypePtr})
		b = e.classes[b].BaseClass
	}
}

// passStaticReceiver passes a direct call of a static member of record
// class c the record it names; the returned function restores the
// previous receiver after the call.
func (e *Emitter) passStaticReceiver(c string) func() {
	if !e.classRecords[c] {
		return func() {}
	}
	rec := e.recordFor(c)
	old := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classrec_recv("+irPtr+" %s)", old, rec))
	return func() {
		e.emitInstr(fmt.Sprintf("call "+irPtr+" @__kml_classrec_recv("+irPtr+" %s)", old))
	}
}

// warmStatics has the checker type each class's value and its static
// members' bodies before the rewrites after binding (staticThisToClass,
// staticPrivateBrand, liftStaticGenerators) change them: the checker reads
// the code as written, and keeps those answers (warmFront).
func (e *Emitter) warmStatics(prog *ast.Program) {
	c := e.front()
	if c == nil {
		return
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		var cd *ast.ClassDeclaration
		switch n := n.(type) {
		case *ast.ClassDeclaration:
			cd = n
		case *ast.ClassExpression:
			cd = n.Decl
		}
		if cd == nil || cd.Name == "" {
			return true
		}
		statics := false
		for _, m := range cd.Methods {
			if m.IsStatic && m.Body != nil {
				statics = true
			}
		}
		if !statics {
			return true
		}
		c.ClassValueOf(cd)
		for _, m := range cd.Methods {
			if m.IsStatic && m.Body != nil {
				e.warmFront(m.Body)
			}
		}
		return true
	})
}

// staticThisToClass rewrites `this` in a class's static members (static
// methods and accessors, static field initializers, static blocks) to the
// class's name: there `this` is the class. A nested non-arrow function has
// its own `this` and is left alone.
//
// A static method whose receiver may be other than its class takes it as
// a leading `this` parameter instead (staticReceivers): a subclass calling
// it (`D.m()`), a call through its value (`C.m.call(o)`), or `super.m()`.
func (e *Emitter) staticThisToClass(prog *ast.Program) {
	e.warmStatics(prog)
	real := e.staticReceivers(prog)
	var cd *ast.ClassDeclaration
	var rw func(cls string, n ast.Node) ast.Node
	rw = func(cls string, n ast.Node) ast.Node {
		switch n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return n // its own `this`
		case *ast.NewTargetExpression:
			return n // undefined in a static member; its receiver stays
		case *ast.ThisExpression:
			id := ast.NewIdentifier(cls, n.GetPos())
			if prog.StaticThis == nil {
				prog.StaticThis = map[*ast.Identifier]*ast.ClassDeclaration{}
			}
			prog.StaticThis[id] = cd
			return id
		case *ast.AssignmentExpression:
			// `super.x = v` stores on the receiver, the class.
			if a := n.(*ast.AssignmentExpression); a != nil {
				if name, ok := superPropertyName(a.Left); ok {
					a.Left = ast.NewMemberExpression(rw(cls, ast.NewThisExpression(a.Left.GetPos())).(ast.Expression), name, a.Left.GetPos())
				}
			}
		}
		ast.RewriteChildren(n, func(m ast.Node) ast.Node { return rw(cls, m) })
		return n
	}
	ast.Inspect(prog, func(n ast.Node) bool {
		cd = nil
		switch n := n.(type) {
		case *ast.ClassDeclaration:
			cd = n
		case *ast.ClassExpression:
			cd = n.Decl
		}
		if cd == nil || cd.Name == "" {
			return true
		}
		staticSuperInits(cd, e.originate)
		for i := range cd.Fields {
			if cd.Fields[i].Static && cd.Fields[i].Initializer != nil {
				cd.Fields[i].Initializer = rw(cd.Name, cd.Fields[i].Initializer).(ast.Expression)
			}
		}
		for _, m := range cd.Methods {
			if m.IsStatic && m.Body != nil && real[m] {
				m.Params = append([]ast.Param{{Name: "this", Type: ast.AnyAnnotation("ts")}}, m.Params...)
				staticSuperToCall(cd, m, e.originate)
				staticPrivateBrand(cd, m, e.originate, e.anonClasses[cd])
				continue
			}
			if m.IsStatic && m.Body != nil {
				staticSuperToCall(cd, m, e.originate) // its `this`, rewritten next, is the class
				ast.RewriteChildren(m.Body, func(x ast.Node) ast.Node { return rw(cd.Name, x) })
			}
		}
		for _, sb := range cd.StaticBlocks {
			ast.RewriteChildren(sb, func(x ast.Node) ast.Node { return rw(cd.Name, x) })
		}
		return true
	})
}

// staticPrivatesNamed applies staticPrivateNamed to every named class,
// before hoisting: a nested class still sits in its outer one.
func (e *Emitter) staticPrivatesNamed(prog *ast.Program) {
	ast.Inspect(prog, func(n ast.Node) bool {
		var cd *ast.ClassDeclaration
		switch x := n.(type) {
		case *ast.ClassDeclaration:
			cd = x
		case *ast.ClassExpression:
			cd = x.Decl
		}
		if cd != nil && cd.Name != "" {
			staticPrivateNamed(cd, e.originate)
		}
		return true
	})
}

// staticPrivateNamed rewrites each `o.#p` anywhere in class cd's body
// (nested functions and classes too), where #p is one of cd's own static
// private members and o a name, to `(brand check, C.#p)`: only the class
// itself carries a static private member, any other receiver throws.
func staticPrivateNamed(cd *ast.ClassDeclaration, origin func(syn, orig ast.Expression)) {
	own := map[string]bool{}
	for _, f := range cd.Fields {
		if f.Static && strings.HasPrefix(f.Name, "#") {
			own[f.Name] = true
		}
	}
	for _, sm := range cd.Methods {
		if sm.IsStatic && strings.HasPrefix(sm.Name, "#") {
			own[sm.Name] = true
		}
	}
	if len(own) == 0 {
		return
	}
	shadows := func(inner *ast.ClassDeclaration, name string) bool {
		for _, f := range inner.Fields {
			if f.Name == name {
				return true
			}
		}
		for _, m := range inner.Methods {
			if m.Name == name {
				return true
			}
		}
		return false
	}
	check := func(recv ast.Expression, pos ast.Pos, msg string) ast.Expression {
		c := ast.NewCallExpression(ast.NewIdentifier("__kml_static_brand", pos), []ast.Expression{
			ast.NewIdentifier(recv.(*ast.Identifier).Name, pos), ast.NewIdentifier(cd.Name, pos), ast.NewStringLiteral(msg, pos)}, pos)
		c.Intrinsic = "kml.staticBrand"
		return c
	}
	onClass := func(me *ast.MemberExpression) *ast.MemberExpression {
		id := ast.NewIdentifier(cd.Name, me.GetPos())
		origin(id, me.Object)
		on := ast.NewMemberExpression(id, me.Property, me.GetPos())
		origin(on, me)
		return on
	}
	seq := func(x ast.Expression, exprs ...ast.Expression) ast.Expression {
		s := ast.NewSequenceExpression(exprs, x.GetPos())
		origin(s, x)
		return s
	}
	readMsg := func(p string) string {
		return "Cannot read private member " + ast.PrivateDisplay(p) + " from an object whose class did not declare it"
	}
	var rw func(hidden map[string]bool) func(n ast.Node) ast.Node
	rw = func(hidden map[string]bool) func(n ast.Node) ast.Node {
		var f func(n ast.Node) ast.Node
		priv := func(x ast.Expression) (*ast.MemberExpression, bool) {
			me, ok := x.(*ast.MemberExpression)
			if !ok || !own[me.Property] || hidden[me.Property] || me.Optional {
				return nil, false
			}
			_, isID := me.Object.(*ast.Identifier)
			return me, isID
		}
		f = func(n ast.Node) ast.Node {
			switch x := n.(type) {
			case *ast.ClassDeclaration:
				// A nested class declaring the name hides it.
				h := hidden
				for name := range own {
					if shadows(x, name) {
						h = map[string]bool{}
						for k := range hidden {
							h[k] = true
						}
						h[name] = true
					}
				}
				ast.RewriteChildren(n, rw(h))
				return n
			case *ast.AssignmentExpression:
				if me, ok := priv(x.Left); ok {
					x.Right = f(x.Right).(ast.Expression)
					msg := "Cannot write private member " + ast.PrivateDisplay(me.Property) + " to an object whose class did not declare it"
					if x.Op != "=" {
						msg = readMsg(me.Property)
					}
					recv := me.Object
					x.Left = onClass(me)
					return seq(x, check(recv, x.GetPos(), msg), x)
				}
			case *ast.UpdateExpression:
				if me, ok := priv(x.Arg); ok {
					recv := me.Object
					x.Arg = onClass(me)
					return seq(x, check(recv, x.GetPos(), readMsg(me.Property)), x)
				}
			case *ast.CallExpression:
				if me, ok := priv(x.Callee); ok && !x.Optional {
					for i, a := range x.Args {
						x.Args[i] = f(a).(ast.Expression)
					}
					recv := me.Object
					x.Callee = onClass(me)
					return seq(x, check(recv, x.GetPos(), "Receiver must be class "+ast.Unmangle(cd.Name)), x)
				}
			case *ast.MemberExpression:
				if me, ok := priv(x); ok {
					return seq(x, check(me.Object, x.GetPos(), readMsg(me.Property)), onClass(me))
				}
			}
			ast.RewriteChildren(n, f)
			return n
		}
		return f
	}
	f := rw(map[string]bool{})
	for _, m := range cd.Methods {
		if m.Body != nil {
			ast.RewriteChildren(m.Body, f)
		}
	}
	if cd.Constructor != nil && cd.Constructor.Body != nil {
		ast.RewriteChildren(cd.Constructor.Body, f)
	}
	for i := range cd.Fields {
		if cd.Fields[i].Initializer != nil {
			cd.Fields[i].Initializer = f(cd.Fields[i].Initializer).(ast.Expression)
		}
	}
	for _, sb := range cd.StaticBlocks {
		ast.RewriteChildren(sb, f)
	}
}

// liftStaticGenerators turns each static generator method into a generator
// function declared just before its class, the method a plain one that
// calls it with its parameters' values (its defaults evaluated there). A
// static member's `this` is already the class's name (staticThisToClass),
// so the generator needs no receiver. Only a module-level class's: the
// binder ran before this pass, so a class declared in a function could not
// capture the generator as a local. A body reading `arguments` is passed
// the method's whole `arguments`.
func liftStaticGenerators(prog *ast.Program) map[*ast.FunctionDeclaration]*ast.FunctionDeclaration {
	lifted := map[*ast.FunctionDeclaration]*ast.FunctionDeclaration{}
	lift := func(list []ast.Statement) []ast.Statement {
		var out []ast.Statement
		for _, st := range list {
			cd, ok := st.(*ast.ClassDeclaration)
			if !ok || cd.Name == "" {
				out = append(out, st)
				continue
			}
			for _, m := range cd.Methods {
				if !m.IsStatic || !m.IsGenerator || m.Body == nil || m.AccessorKind != "" {
					continue
				}
				pos := m.GetPos()
				if readsArguments(m.Body) {
					// The method forwards its whole `arguments`, which the
					// generator keeps as its own; the method's parameters
					// stay only for its length, a default an undefined.
					gen := *m
					gen.Name = cd.Name + "__kml_sgen_" + m.Name
					gen.IsStatic = false
					gen.Params = append([]ast.Param(nil), m.Params...)
					out = append(out, &gen)
					lifted[m] = &gen
					ps := make([]ast.Param, len(m.Params))
					for i, p := range m.Params {
						ps[i] = ast.Param{Name: p.Name, Rest: p.Rest, Type: ast.AnyAnnotation("ts")}
						if p.ArrayPattern != nil || p.ObjectPattern != nil {
							ps[i].Name = fmt.Sprintf("__kml_sgp%d", i)
						}
						if p.Rest {
							ps[i].Type = nil
						}
						if p.Default != nil {
							ps[i].Default = ast.NewNullLiteral(true, pos)
						}
					}
					m.Params = ps
					m.IsGenerator = false
					m.IsAsync = false
					m.ReturnType = nil
					fwd := ast.NewSpreadElement(ast.NewIdentifier("arguments", pos), pos)
					m.Body = ast.NewBlockStatement([]ast.Statement{
						ast.NewReturnStatement(ast.NewCallExpression(ast.NewIdentifier(gen.Name, pos), []ast.Expression{fwd}, pos), pos),
					}, pos)
					continue
				}
				gen := *m
				gen.Name = cd.Name + "__kml_sgen_" + m.Name
				gen.IsStatic = false
				gen.Params = make([]ast.Param, len(m.Params))
				args := make([]ast.Expression, len(m.Params))
				for i, p := range m.Params {
					gp := p
					gp.Default, gp.Optional = nil, false
					if p.ArrayPattern != nil || p.ObjectPattern != nil {
						gp.Type = nil // the method unpacks nothing; the generator does
					}
					gen.Params[i] = gp
					var a ast.Expression = ast.NewIdentifier(p.Name, pos)
					if p.Rest {
						a = ast.NewSpreadElement(a, pos)
					}
					args[i] = a
				}
				out = append(out, &gen)
				lifted[m] = &gen
				for i := range m.Params {
					if m.Params[i].ArrayPattern != nil || m.Params[i].ObjectPattern != nil {
						// The method forwards the whole value, whatever it
						// is; the generator destructures it.
						m.Params[i].ArrayPattern, m.Params[i].ObjectPattern = nil, nil
						if m.Params[i].Type == nil {
							m.Params[i].Type = ast.AnyAnnotation("ts")
						}
					}
				}
				m.IsGenerator = false
				m.IsAsync = false
				m.ReturnType = nil
				m.Body = ast.NewBlockStatement([]ast.Statement{
					ast.NewReturnStatement(ast.NewCallExpression(ast.NewIdentifier(gen.Name, pos), args, pos), pos),
				}, pos)
			}
			out = append(out, st)
		}
		return out
	}
	prog.Body = lift(prog.Body)
	return lifted
}

// readsArguments reports a read of `arguments` in body, outside the nested
// non-arrow functions that have their own.
func readsArguments(body *ast.BlockStatement) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return false
		case *ast.Identifier:
			if x.Name == "arguments" {
				found = true
			}
		}
		return !found
	})
	return found
}

// takesStaticThis reports a static method given a receiver parameter
// (staticThisToClass).
func takesStaticThis(m *ast.FunctionDeclaration) bool {
	return m.IsStatic && len(m.Params) > 0 && m.Params[0].Name == "this"
}

// staticReceivers is the set of static methods reading `this` (or `super`)
// whose receiver may be other than their own class. A builtin module's
// class's always may: its code is the same whatever the program does. A
// program class's may when some class extends it, or when it is used other
// than through `C.f`/`C.m(…)` (the class or one of its static methods
// taken as a value).
func (e *Emitter) staticReceivers(prog *ast.Program) map[*ast.FunctionDeclaration]bool {
	type info struct {
		cd  *ast.ClassDeclaration
		lib bool
	}
	classes := map[string]*info{}
	var order []*info
	extended := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		var cd *ast.ClassDeclaration
		switch n := n.(type) {
		case *ast.ClassDeclaration:
			cd = n
		case *ast.ClassExpression:
			cd = n.Decl
		}
		if cd != nil {
			if cd.BaseClass != "" {
				extended[cd.BaseClass] = true
			}
			if cd.Name != "" {
				in := &info{cd: cd, lib: e.libStmts[cd] != ""}
				classes[cd.Name] = in
				order = append(order, in)
			}
		}
		return true
	})
	readsThis := func(m *ast.FunctionDeclaration) bool {
		found := false
		ast.Inspect(m.Body, func(n ast.Node) bool {
			switch n.(type) {
			case *ast.ThisExpression, *ast.SuperExpression:
				found = true
			case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
				return false
			}
			return !found
		})
		return found
	}
	// A class name used other than as `C.f` / `C.m(…)` escapes as a value.
	statics := func(c string) map[string]bool {
		out := map[string]bool{}
		if in := classes[c]; in != nil {
			for _, m := range in.cd.Methods {
				if m.IsStatic {
					out[m.Name] = true
				}
			}
		}
		return out
	}
	allowed := map[*ast.Identifier]bool{}
	escapes := map[string]bool{}
	ast.Inspect(prog, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpression:
			if me, ok := n.Callee.(*ast.MemberExpression); ok {
				if id, ok := me.Object.(*ast.Identifier); ok && classes[id.Name] != nil {
					allowed[id] = true
				}
			}
		case *ast.MemberExpression:
			if id, ok := n.Object.(*ast.Identifier); ok && classes[id.Name] != nil && !allowed[id] {
				if statics(id.Name)[n.Property] {
					escapes[id.Name] = true // `C.m` as a value
				} else {
					allowed[id] = true
				}
			}
		case *ast.BinaryExpression:
			if id, ok := n.Right.(*ast.Identifier); ok && n.Op == "instanceof" {
				allowed[id] = true
			}
		case *ast.Identifier:
			if classes[n.Name] != nil && !allowed[n] {
				escapes[n.Name] = true
			}
		}
		return true
	})
	real := map[*ast.FunctionDeclaration]bool{}
	for _, in := range order {
		may := in.lib || extended[in.cd.Name] || escapes[in.cd.Name]
		if !may {
			continue
		}
		for _, m := range in.cd.Methods {
			if m.IsStatic && m.Body != nil && !takesStaticThis(m) && readsThis(m) {
				real[m] = true
			}
		}
	}
	return real
}

// staticSuperToCall rewrites `super.m(…)` in static method m of class cd to
// `Base.m.call(this, …)` and `super.f` to `Base.f`: its receiver is m's own.
// Each built node is linked to the expression it stands for (origin).
func staticSuperToCall(cd *ast.ClassDeclaration, m *ast.FunctionDeclaration, origin func(syn, orig ast.Expression)) {
	if rw := staticSuperRewriter(cd, origin); rw != nil {
		ast.RewriteChildren(m.Body, rw)
	}
}

// staticSuperInits is staticSuperToCall over class cd's static blocks and
// static field initializers, whose home object is the class too.
func staticSuperInits(cd *ast.ClassDeclaration, origin func(syn, orig ast.Expression)) {
	rw := staticSuperRewriter(cd, origin)
	if rw == nil {
		return
	}
	for _, sb := range cd.StaticBlocks {
		ast.RewriteChildren(sb, rw)
	}
	for i := range cd.Fields {
		if cd.Fields[i].Static && cd.Fields[i].Initializer != nil {
			cd.Fields[i].Initializer = rw(cd.Fields[i].Initializer).(ast.Expression)
		}
	}
}

// staticSuperRewriter is the rewrite staticSuperToCall applies, nil when
// cd has no base it names.
func staticSuperRewriter(cd *ast.ClassDeclaration, origin func(syn, orig ast.Expression)) func(ast.Node) ast.Node {
	if cd.BaseClass == "" || cd.BaseQualified {
		return nil
	}
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return n
		case *ast.CallExpression:
			if me, ok := x.Callee.(*ast.MemberExpression); ok {
				if _, isSuper := me.Object.(*ast.SuperExpression); isSuper {
					for i, a := range x.Args {
						x.Args[i] = rw(a).(ast.Expression)
					}
					pos := x.GetPos()
					base := ast.NewMemberExpression(ast.NewIdentifier(cd.BaseClass, pos), me.Property, pos)
					args := append([]ast.Expression{ast.NewThisExpression(pos)}, x.Args...)
					call := ast.NewCallExpression(ast.NewMemberExpression(base, "call", pos), args, pos)
					origin(base, me)
					origin(call, x)
					return call
				}
			}
			if ix, ok := x.Callee.(*ast.IndexExpression); ok {
				if _, isSuper := ix.Object.(*ast.SuperExpression); isSuper {
					// `super[k](…)`: `Base[k].call(this, …)`.
					ix.Index = rw(ix.Index).(ast.Expression)
					for i, a := range x.Args {
						x.Args[i] = rw(a).(ast.Expression)
					}
					pos := x.GetPos()
					base := ast.NewIndexExpression(ast.NewAsExpression(ast.NewIdentifier(cd.BaseClass, pos), ast.AnyAnnotation("ts"), pos), ix.Index, pos)
					args := append([]ast.Expression{ast.NewThisExpression(pos)}, x.Args...)
					call := ast.NewCallExpression(ast.NewMemberExpression(base, "call", pos), args, pos)
					origin(base, ix)
					origin(call, x)
					return call
				}
			}
		case *ast.MemberExpression:
			if _, isSuper := x.Object.(*ast.SuperExpression); isSuper {
				base := ast.NewMemberExpression(ast.NewIdentifier(cd.BaseClass, x.GetPos()), x.Property, x.GetPos())
				origin(base, x)
				return base
			}
		case *ast.IndexExpression:
			if _, isSuper := x.Object.(*ast.SuperExpression); isSuper {
				x.Index = rw(x.Index).(ast.Expression)
				pos := x.GetPos()
				base := ast.NewIndexExpression(ast.NewAsExpression(ast.NewIdentifier(cd.BaseClass, pos), ast.AnyAnnotation("ts"), pos), x.Index, pos)
				origin(base, x)
				return base
			}
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	return rw
}

// staticPrivateBrand rewrites each `this.#p` in static method m of class
// cd, whose receiver is a parameter, to `(brand check, C.#p)`: a static
// private member is the class's own, and any other receiver throws
// (`C.m.call({})`, or `D.m()` for a subclass D).
//
// Each node it builds is linked to the expression it stands for (origin):
// the checker bound the program before this rewrite.
func staticPrivateBrand(cd *ast.ClassDeclaration, m *ast.FunctionDeclaration, origin func(syn, orig ast.Expression), anonymous bool) {
	own := map[string]bool{}
	for _, f := range cd.Fields {
		if f.Static && strings.HasPrefix(f.Name, "#") {
			own[f.Name] = true
		}
	}
	for _, sm := range cd.Methods {
		if sm.IsStatic && strings.HasPrefix(sm.Name, "#") {
			own[sm.Name] = true
		}
	}
	// An instance's private names: `this` must be an instance of the class.
	inst := map[string]bool{}
	for _, f := range cd.Fields {
		if !f.Static && strings.HasPrefix(f.Name, "#") {
			inst[f.Name] = true
		}
	}
	for _, im := range cd.Methods {
		if !im.IsStatic {
			if found, static := privateInDeclarer(cd, privateNameOf(im.Name)); found && !static && strings.HasPrefix(privateNameOf(im.Name), "#") {
				inst[privateNameOf(im.Name)] = true
			}
		}
	}
	if len(own) == 0 && len(inst) == 0 {
		return
	}
	instPriv := func(x ast.Expression) (*ast.MemberExpression, bool) {
		me, ok := x.(*ast.MemberExpression)
		if !ok || !inst[me.Property] || own[me.Property] {
			return nil, false
		}
		_, isThis := me.Object.(*ast.ThisExpression)
		return me, isThis
	}
	// branded makes me's receiver the checked instance.
	branded := func(me *ast.MemberExpression, msg string) {
		pos := me.GetPos()
		c := ast.NewCallExpression(ast.NewIdentifier("__kml_instance_brand", pos), []ast.Expression{
			me.Object, ast.NewIdentifier(cd.Name, pos), ast.NewStringLiteral(msg, pos)}, pos)
		// Not linked to the `this` it wraps: that is the class value to the
		// checker, and this is an instance (the intrinsic's own type).
		c.Intrinsic = "kml.instanceBrand"
		me.Object = c
	}
	priv := func(x ast.Expression) (*ast.MemberExpression, bool) {
		me, ok := x.(*ast.MemberExpression)
		if !ok || !own[me.Property] {
			return nil, false
		}
		_, isThis := me.Object.(*ast.ThisExpression)
		return me, isThis
	}
	check := func(pos ast.Pos, msg string) ast.Expression {
		c := ast.NewCallExpression(ast.NewIdentifier("__kml_static_brand", pos), []ast.Expression{
			ast.NewThisExpression(pos), ast.NewIdentifier(cd.Name, pos), ast.NewStringLiteral(msg, pos)}, pos)
		c.Intrinsic = "kml.staticBrand"
		return c
	}
	onClass := func(me *ast.MemberExpression) *ast.MemberExpression {
		id := ast.NewIdentifier(cd.Name, me.GetPos())
		origin(id, me.Object) // the static `this`: the class
		on := ast.NewMemberExpression(id, me.Property, me.GetPos())
		origin(on, me)
		return on
	}
	seq := func(x ast.Expression, exprs ...ast.Expression) ast.Expression {
		s := ast.NewSequenceExpression(exprs, x.GetPos())
		origin(s, x)
		return s
	}
	readMsg := func(p string) string {
		return "Cannot read private member " + ast.PrivateDisplay(p) + " from an object whose class did not declare it"
	}
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			if me, ok := instPriv(x.Left); ok {
				x.Right = rw(x.Right).(ast.Expression)
				msg := "Cannot write private member " + ast.PrivateDisplay(me.Property) + " to an object whose class did not declare it"
				if x.Op != "=" {
					msg = readMsg(me.Property)
				}
				branded(me, msg)
				return x
			}
		case *ast.UpdateExpression:
			if me, ok := instPriv(x.Arg); ok {
				branded(me, readMsg(me.Property))
				return x
			}
		case *ast.CallExpression:
			if me, ok := instPriv(x.Callee); ok {
				for i, a := range x.Args {
					x.Args[i] = rw(a).(ast.Expression)
				}
				shown := ast.Unmangle(cd.Name)
				if anonymous {
					shown = "anonymous" // V8's name for a class without its own
				}
				branded(me, "Receiver must be an instance of class "+shown)
				return x
			}
		case *ast.MemberExpression:
			if me, ok := instPriv(x); ok {
				branded(me, readMsg(me.Property))
				return x
			}
		}
		switch x := n.(type) {
		case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
			return n
		case *ast.AssignmentExpression:
			if me, ok := priv(x.Left); ok {
				x.Right = rw(x.Right).(ast.Expression)
				msg := "Cannot write private member " + ast.PrivateDisplay(me.Property) + " to an object whose class did not declare it"
				if x.Op != "=" {
					msg = readMsg(me.Property)
				}
				x.Left = onClass(me)
				return seq(x, check(x.GetPos(), msg), x)
			}
		case *ast.UpdateExpression:
			if me, ok := priv(x.Arg); ok {
				x.Arg = onClass(me)
				return seq(x, check(x.GetPos(), readMsg(me.Property)), x)
			}
		case *ast.CallExpression:
			if me, ok := priv(x.Callee); ok {
				for i, a := range x.Args {
					x.Args[i] = rw(a).(ast.Expression)
				}
				x.Callee = onClass(me)
				return seq(x, check(x.GetPos(), "Receiver must be class "+ast.Unmangle(cd.Name)), x)
			}
		case *ast.MemberExpression:
			if me, ok := priv(x); ok {
				return seq(x, check(x.GetPos(), readMsg(me.Property)), onClass(me))
			}
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	ast.RewriteChildren(m.Body, rw)
}

// kml.staticBrand(recv, C, msg): throws a TypeError with msg unless recv
// is class C itself (staticPrivateBrand).
func init() {
	intrinsics["kml.staticBrand"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			pos := ex.GetPos()
			any := func(x ast.Expression) ast.Expression { return ast.NewAsExpression(x, ast.AnyAnnotation(""), pos) }
			same, err := e.emitExpr(ast.NewBinaryExpression("===", any(ex.Args[0]), any(ex.Args[1]), pos))
			if err != nil {
				return Value{}, err
			}
			ok, bad := e.freshLabel("brand.ok"), e.freshLabel("brand.bad")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", same.Ref, ok, bad))
			e.emitLabel(bad)
			e.emitThrowTypeError(ex.Args[2].(*ast.StringLiteral).Value)
			e.emitLabel(ok)
			return Value{Ref: "true", Ty: TypeBool}, nil
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypeBool },
	}
}

// privateNameOf is a class method's member name: an accessor's property
// (accessorMethodName), else the method's own.
func privateNameOf(method string) string {
	for _, k := range []string{"get", "set"} {
		if p := strings.TrimPrefix(method, accessorMethodName(k, "")); p != method {
			return p
		}
	}
	return method
}

// kml.instanceBrand(recv, C, msg): recv as an instance of class C, or a
// TypeError with msg when it is not one (staticPrivateBrand: an instance's
// private name read off a static method's receiver).
func init() {
	intrinsics["kml.instanceBrand"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			pos := ex.GetPos()
			cls := ex.Args[1].(*ast.Identifier)
			tmp := fmt.Sprintf("__kml_ibrand_%d", e.weakSeedCtr)
			e.weakSeedCtr++
			if err := e.emitStmt(ast.NewVarDeclaration("const", tmp, ast.AnyAnnotation(""), ex.Args[0], pos)); err != nil {
				return Value{}, err
			}
			o := func() ast.Expression { return ast.NewIdentifier(tmp, pos) }
			saved := e.brandOnly
			e.brandOnly = true
			isC := ast.NewBinaryExpression("instanceof", o(), ast.NewIdentifier(cls.Name, pos), pos)
			err := e.emitStmt(ast.NewIfStatement(ast.NewUnaryExpression("!", true, isC, pos), ast.NewBlockStatement([]ast.Statement{
				ast.NewThrowStatement(ast.NewNewErrorExpression("TypeError", ast.NewStringLiteral(ex.Args[2].(*ast.StringLiteral).Value, pos), pos), pos)}, pos), nil, pos))
			e.brandOnly = saved
			if err != nil {
				return Value{}, err
			}
			return e.emitExpr(ast.NewAsExpression(o(), e.classAnnotation(cls.Name), pos))
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type {
			if info, ok := e.classes[ex.Args[1].(*ast.Identifier).Name]; ok {
				return info.Ty
			}
			return TypeAny
		},
	}
}

// privateInDeclarer reports whether cd declares the private name, and
// whether as a static member.
func privateInDeclarer(cd *ast.ClassDeclaration, name string) (found, static bool) {
	for _, f := range cd.Fields {
		if f.Name == name {
			return true, f.Static
		}
	}
	for _, m := range cd.Methods {
		if m.Name == name || m.Name == accessorMethodName("get", name) || m.Name == accessorMethodName("set", name) {
			return true, m.IsStatic
		}
	}
	return false, false
}

// privateInOwners records, before class expressions hoist out of their
// enclosing classes, the innermost enclosing class declaring each `#x in o`
// private name (private names resolve lexically).
func (e *Emitter) privateInOwners(prog *ast.Program) {
	e.privateInOwner = map[privateInKey]*ast.ClassDeclaration{}
	var stack []*ast.ClassDeclaration
	var walk func(n ast.Node) bool
	walk = func(n ast.Node) bool {
		var cd *ast.ClassDeclaration
		switch x := n.(type) {
		case *ast.ClassDeclaration:
			cd = x
		case *ast.ClassExpression:
			cd = x.Decl
		case *ast.BinaryExpression:
			if id, ok := x.Left.(*ast.Identifier); ok && x.Op == "in" && strings.HasPrefix(id.Name, "#") {
				for i := len(stack) - 1; i >= 0; i-- {
					if found, _ := privateInDeclarer(stack[i], id.Name); found {
						e.privateInOwner[privateInKey{id.GetPos(), id.Name}] = stack[i]
						break
					}
				}
			}
		}
		if cd != nil {
			stack = append(stack, cd)
			ast.ForEachChild(n, walk)
			stack = stack[:len(stack)-1]
			return true
		}
		ast.ForEachChild(n, walk)
		return true
	}
	ast.ForEachChild(prog, walk)
}

// privateInKey names a `#x in o`'s private name by where it is written,
// which survives the passes that copy the nodes.
type privateInKey struct {
	pos  ast.Pos
	name string
}

func posOrZero(id *ast.Identifier) ast.Pos {
	if id == nil {
		return ast.Pos{}
	}
	return id.GetPos()
}

func nameOrEmpty(id *ast.Identifier) string {
	if id == nil {
		return ""
	}
	return id.Name
}

// privateInChecks rewrites each `#x in o` to kml.privateIn(o, C, static):
// C is the class privateInOwners found declaring #x, by its final name.
func (e *Emitter) privateInChecks(prog *ast.Program) {
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		if x, ok := n.(*ast.BinaryExpression); ok && x.Op == "in" {
			id, _ := x.Left.(*ast.Identifier)
			if cd := e.privateInOwner[privateInKey{posOrZero(id), nameOrEmpty(id)}]; id != nil && cd != nil && cd.Name != "" {
				_, static := privateInDeclarer(cd, id.Name)
				pos := x.GetPos()
				right := rw(x.Right).(ast.Expression)
				c := ast.NewCallExpression(ast.NewIdentifier("__kml_private_in", pos), []ast.Expression{
					right, ast.NewIdentifier(cd.Name, pos), ast.NewStringLiteral(id.Name, pos), ast.NewBooleanLiteral(static, pos)}, pos)
				c.Intrinsic = "kml.privateIn"
				e.originate(c, x) // typed as the `in` it stands for
				return c
			}
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	ast.RewriteChildren(prog, rw)
}

// outerPrivateReads rewrites a read `o.#p` in a class that does not
// declare #p, where an enclosing named class D declares it as an instance
// field, to kml.privateGet(o, D, "#p"): the name is D's (private names
// resolve lexically), and o must carry D's brand. Hoisting D later renames
// the reference with the rest of D's body. The checker binds
// the program first and keeps its answer for each read (warmFront), which
// the call it becomes stands for (originate).
func (e *Emitter) outerPrivateReads(prog *ast.Program) {
	e.front()
	declares := func(cd *ast.ClassDeclaration, name string) (field, any bool) {
		for _, f := range cd.Fields {
			if f.Name == name {
				return !f.Static, true
			}
		}
		for _, m := range cd.Methods {
			if m.Name == name || m.Name == accessorMethodName("get", name) || m.Name == accessorMethodName("set", name) {
				return false, true
			}
		}
		return false, false
	}
	var stack []*ast.ClassDeclaration
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		var cd *ast.ClassDeclaration
		switch x := n.(type) {
		case *ast.ClassDeclaration:
			cd = x
		case *ast.ClassExpression:
			cd = x.Decl
		case *ast.AssignmentExpression:
			// A write keeps its member; its right side is rewritten.
			if me, ok := x.Left.(*ast.MemberExpression); ok && strings.HasPrefix(me.Property, "#") {
				me.Object = rw(me.Object).(ast.Expression)
				x.Right = rw(x.Right).(ast.Expression)
				return n
			}
		case *ast.UpdateExpression:
			if me, ok := x.Arg.(*ast.MemberExpression); ok && strings.HasPrefix(me.Property, "#") {
				return n
			}
		case *ast.CallExpression:
			if me, ok := x.Callee.(*ast.MemberExpression); ok && strings.HasPrefix(me.Property, "#") {
				me.Object = rw(me.Object).(ast.Expression)
				for i, a := range x.Args {
					x.Args[i] = rw(a).(ast.Expression)
				}
				return n
			}
		case *ast.MemberExpression:
			if _, self := x.Object.(*ast.ThisExpression); !self && !x.Optional && strings.HasPrefix(x.Property, "#") && len(stack) > 0 && stack[len(stack)-1].Name != "" {
				// `o.#p` of the class's own field on another receiver: o
				// must be an instance (its brand), and is read as one.
				if field, own := declares(stack[len(stack)-1], x.Property); own && field {
					pos := x.GetPos()
					e.warmFront(x)
					c := ast.NewCallExpression(ast.NewIdentifier("__kml_private_get", pos), []ast.Expression{
						rw(x.Object).(ast.Expression), ast.NewIdentifier(stack[len(stack)-1].Name, pos), ast.NewStringLiteral(x.Property, pos)}, pos)
					c.Intrinsic = "kml.privateGet"
					e.originate(c, x)
					return c
				}
			}
			if strings.HasPrefix(x.Property, "#") && len(stack) > 1 {
				if _, own := declares(stack[len(stack)-1], x.Property); !own {
					for i := len(stack) - 2; i >= 0; i-- {
						field, found := declares(stack[i], x.Property)
						if !found {
							continue
						}
						if field && stack[i].Name != "" {
							pos := x.GetPos()
							e.warmFront(x)
							c := ast.NewCallExpression(ast.NewIdentifier("__kml_private_get", pos), []ast.Expression{
								rw(x.Object).(ast.Expression), ast.NewIdentifier(stack[i].Name, pos), ast.NewStringLiteral(x.Property, pos)}, pos)
							c.Intrinsic = "kml.privateGet"
							e.originate(c, x)
							return c
						}
						break
					}
				}
			}
		}
		if cd != nil {
			stack = append(stack, cd)
			defer func() { stack = stack[:len(stack)-1] }()
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	ast.RewriteChildren(prog, rw)
}

// kml.privateGet(o, D, "#p"): D's private field #p of o, which must be an
// instance of D (V8's TypeError otherwise) — outerPrivateReads.
func init() {
	intrinsics["kml.privateGet"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			pos := ex.GetPos()
			name := ex.Args[2].(*ast.StringLiteral).Value
			cls := ex.Args[1].(*ast.Identifier)
			tmp := fmt.Sprintf("__kml_pget_%d", e.weakSeedCtr)
			e.weakSeedCtr++
			if err := e.emitStmt(ast.NewVarDeclaration("const", tmp, ast.AnyAnnotation(""), ex.Args[0], pos)); err != nil {
				return Value{}, err
			}
			o := func() ast.Expression { return ast.NewIdentifier(tmp, pos) }
			readMsg := "Cannot read private member " + ast.PrivateDisplay(name) + " from an object whose class did not declare it"
			if e.overrideInstances(cls.Name) {
				// Another object a base constructor returned keeps it in a
				// side table.
				ov, err := e.emitExpr(o())
				if err != nil {
					return Value{}, err
				}
				return e.overrideBranch(ov, cls.Name, func() (Value, error) {
					return e.emitExpr(ast.NewMemberExpression(e.asClass(ov, cls.Name, pos), name, pos))
				}, func() (Value, error) {
					return e.emitPtabGet(ov, cls.Name+name, readMsg), nil
				})
			}
			saved := e.brandOnly
			e.brandOnly = true
			isD := ast.NewBinaryExpression("instanceof", o(), ast.NewIdentifier(cls.Name, pos), pos)
			msg := ast.NewStringLiteral(readMsg, pos)
			err := e.emitStmt(ast.NewIfStatement(ast.NewUnaryExpression("!", true, isD, pos), ast.NewBlockStatement([]ast.Statement{
				ast.NewThrowStatement(ast.NewNewErrorExpression("TypeError", msg, pos), pos)}, pos), nil, pos))
			e.brandOnly = saved
			if err != nil {
				return Value{}, err
			}
			as := ast.NewAsExpression(o(), e.classAnnotation(cls.Name), pos)
			return e.emitExpr(ast.NewMemberExpression(as, name, pos))
		},
		ty: func(e *Emitter, ex *ast.CallExpression) Type {
			cls := ex.Args[1].(*ast.Identifier)
			if info, ok := e.classes[cls.Name]; ok {
				if _, ft, ok := info.Ty.FieldIndex(ex.Args[2].(*ast.StringLiteral).Value); ok {
					return ft
				}
			}
			return TypeAny
		},
	}
}

// kml.privateIn(o, C, "#x", static): whether object o has C's private
// #x; a non-object o throws, as `in` does.
func init() {
	intrinsics["kml.privateIn"] = intrinsic{
		emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
			pos := ex.GetPos()
			name := ex.Args[2].(*ast.StringLiteral).Value
			tmp := fmt.Sprintf("__kml_pin_%d", e.weakSeedCtr)
			e.weakSeedCtr++
			if err := e.emitStmt(ast.NewVarDeclaration("const", tmp, ast.AnyAnnotation(""), ex.Args[0], pos)); err != nil {
				return Value{}, err
			}
			o := func() ast.Expression { return ast.NewIdentifier(tmp, pos) }
			typeOf := func() ast.Expression { return ast.NewUnaryExpression("typeof", true, o(), pos) }
			notObj := ast.NewBinaryExpression("||",
				ast.NewBinaryExpression("===", o(), ast.NewNullLiteral(false, pos), pos),
				ast.NewBinaryExpression("&&",
					ast.NewBinaryExpression("!==", typeOf(), ast.NewStringLiteral("object", pos), pos),
					ast.NewBinaryExpression("!==", typeOf(), ast.NewStringLiteral("function", pos), pos), pos), pos)
			msg := ast.NewBinaryExpression("+", ast.NewStringLiteral("Cannot use 'in' operator to search for '"+name+"' in ", pos), o(), pos)
			if err := e.emitStmt(ast.NewIfStatement(notObj, ast.NewBlockStatement([]ast.Statement{
				ast.NewThrowStatement(ast.NewNewErrorExpression("TypeError", msg, pos), pos)}, pos), nil, pos)); err != nil {
				return Value{}, err
			}
			cls := ast.NewAsExpression(ex.Args[1], ast.AnyAnnotation(""), pos)
			test := ast.Expression(ast.NewBinaryExpression("instanceof", o(), ex.Args[1], pos))
			if ex.Args[3].(*ast.BooleanLiteral).Value {
				test = ast.NewBinaryExpression("===", o(), cls, pos) // a static private: the class's own
			}
			saved := e.brandOnly
			e.brandOnly = true
			defer func() { e.brandOnly = saved }()
			cn := ex.Args[1].(*ast.Identifier).Name
			if ex.Args[3].(*ast.BooleanLiteral).Value || !e.overrideInstances(cn) {
				return e.emitExpr(test)
			}
			// An object a base constructor returned keeps it in a side table.
			ov, err := e.emitExpr(o())
			if err != nil {
				return Value{}, err
			}
			key := cn + name
			if field, _ := e.privateKind(cn, name); !field {
				key = cn + "#brand"
			}
			return e.overrideBranch(ov, cn, func() (Value, error) {
				return Value{Ref: "true", Ty: TypeBool}, nil
			}, func() (Value, error) {
				return Value{Ref: e.ptabHas(ov, key), Ty: TypeBool}, nil
			})
		},
		ty: func(*Emitter, *ast.CallExpression) Type { return TypeBool },
	}
}

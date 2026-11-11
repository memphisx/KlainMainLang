package llvm

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/binder"
	"KlainMainLang/checker"
)

// emit_classclosure.go — a class declared in a function that names the
// function's locals (`function h(b) { class D { f() { return b } } … }`).
//
// Each evaluation of such a declaration is a class of its own, closing over
// that evaluation's bindings. The class is closure-converted: hoisted like a
// class that captures nothing, with each local it names held by every
// instance in a hidden private field (`#__kml_cap_b`, invisible to inspect,
// JSON and Object.keys as any private field is). A `new` evaluated where the
// locals are in scope stores them into the instance before its constructor
// runs, so field initializers and the constructor already see them; a `new`
// inside the class's own methods copies them from `this`. Two classes made
// by two calls of h thus never share a binding.
//
// A local the class names must not be written after its declaration (a
// copy would miss the write), and the class itself must not be used as a
// value or have static members naming a local: those need a class object
// carrying the evaluation's environment.

// classCapture is one local a closure-converted class names.
type classCapture struct {
	local string // the local's name where the class is declared
	field string // the hidden instance field holding it
}

// captureField is the hidden field that holds local name.
func captureField(name string) string { return "#__kml_cap_" + name }

// staticCaptureField is the hidden static field that holds local name for
// a class's static members.
func staticCaptureField(name string) string { return "#__kml_scap_" + name }

// closureConvertClass rewrites cd, declared in blk, to hold the locals it
// names in hidden fields, and records the `new` sites that fill them.
// False (cd untouched) when cd is outside what this conversion expresses.
func (e *Emitter) closureConvertClass(c frontView, b *binder.Binding, blk *ast.BlockStatement, cd *ast.ClassDeclaration, hoisted map[ast.Node]bool) bool {
	caps := classLocalRefs(b, cd, hoisted)
	if len(caps) == 0 {
		return false
	}
	reject := func(why string) bool {
		if e.classCaptureRejects == nil {
			e.classCaptureRejects = map[*ast.ClassDeclaration][2]string{}
		}
		e.classCaptureRejects[cd] = [2]string{caps[0].sym.Name, why}
		return false
	}
	switch {
	case len(cd.AutoAccessors) > 0:
		return reject("has decorated accessors")
	}
	// Every reference must sit outside any nested non-arrow function (whose
	// `this` is not the instance or the class), and the local must keep its
	// initial value. A static member's references read the evaluation's
	// record (TDD-00242).
	nestedIDs := map[*ast.Identifier]bool{}
	order := []*binder.Symbol{}
	seen := map[*binder.Symbol]bool{}
	staticRef := map[*binder.Symbol]bool{}
	for _, r := range caps {
		if r.static {
			staticRef[r.sym] = true
		}
		if r.nestedFn && !r.static {
			nestedIDs[r.id] = true
		}
		if !seen[r.sym] {
			seen[r.sym] = true
			order = append(order, r.sym)
		}
	}
	// A nested function reads an instance member's copy through a local
	// the member declares first (its `this` is not the instance): a field
	// initializer, or a derived constructor before super(), has no place
	// for one.
	names := func(n ast.Node) bool {
		found := false
		if n != nil {
			ast.Inspect(n, func(m ast.Node) bool {
				if id, ok := m.(*ast.Identifier); ok && nestedIDs[id] {
					found = true
				}
				return !found
			})
		}
		return found
	}
	for _, f := range cd.Fields {
		if f.Initializer != nil && names(f.Initializer) {
			return reject("reads it in a function nested in a field initializer")
		}
	}
	// A derived constructor's copies are declared after its top-level
	// super() call (`this` exists from there); a read before it has none.
	superAt := -1
	if cd.Constructor != nil && cd.BaseClass != "" && cd.Constructor.Body != nil {
		for i, st := range cd.Constructor.Body.Body {
			if superCallStmt(st) {
				superAt = i
				break
			}
			if names(st) {
				break
			}
		}
		if names(cd.Constructor) && superAt < 0 {
			return reject("reads it in a function nested in a derived constructor before super()")
		}
	}
	written := map[*binder.Symbol]bool{}
	for _, sym := range order {
		if e.writtenAfterCapture(b, sym) {
			if cellSite(sym) == nil && forCellLoop(sym) == nil {
				return reject("names '" + sym.Name + "', a var binding written after the class")
			}
			written[sym] = true
		}
	}
	// Each evaluation is a record (TDD-00242): every instance can reach its
	// class as a value through `constructor`, whether or not the program
	// names it as one, and a `new` through that value needs these bindings.
	if e.classRecordsPending == nil {
		e.classRecordsPending = map[*ast.ClassDeclaration]bool{}
	}
	e.classRecordsPending[cd] = true

	byName := map[*binder.Symbol]string{}
	cells := map[*binder.Symbol]ast.TypeNode{}
	var list, statics []classCapture
	for _, sym := range order {
		local := sym.Name
		ty := e.captureAnnotation(c, sym, caps)
		if written[sym] {
			// A local written after its declaration is shared through a
			// cell, `{ v: T }`, that the function and every instance hold.
			cells[sym] = ty
			ty = cellAnnotation(ty)
			local = sym.Name + "__kml_cell"
		}
		byName[sym] = captureField(sym.Name)
		list = append(list, classCapture{local: local, field: captureField(sym.Name)})
		cd.Fields = append(cd.Fields, ast.AnnotField{Name: captureField(sym.Name), Type: ty})
		if staticRef[sym] {
			// Read by a static member: a hidden static field of the record.
			statics = append(statics, classCapture{local: local, field: staticCaptureField(sym.Name)})
			cd.Fields = append(cd.Fields, ast.AnnotField{Name: staticCaptureField(sym.Name), Type: ty, Static: true})
		}
	}
	// References in the class body read the instance's copy, and in a
	// static member the record's.
	static := false
	// need is the member being rewritten's locals for its nested
	// functions' reads: each local's hidden field.
	var need map[string]string
	var needOrder []string
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		if ne, ok := n.(*ast.NewExpression); ok {
			if e.classCapFromThis == nil {
				e.classCapFromThis = map[*ast.NewExpression]bool{}
			}
			e.classCapFromThis[ne] = true
		}
		ast.RewriteChildren(n, rw)
		if id, ok := n.(*ast.Identifier); ok {
			if sym, _ := b.Resolve(id); sym != nil {
				if f, ok := byName[sym]; ok {
					recv := ast.NewThisExpression(id.GetPos())
					recv.Capture = true
					var ref ast.Expression = ast.NewMemberExpression(recv, f, id.GetPos())
					if nestedIDs[id] && !static {
						local := "__kml_capv_" + sym.Name
						if _, ok := need[local]; !ok {
							need[local] = f
							needOrder = append(needOrder, local)
						}
						ref = ast.NewIdentifier(local, id.GetPos())
					}
					if static {
						ref = ast.NewMemberExpression(ast.NewIdentifier(cd.Name, id.GetPos()), staticCaptureField(sym.Name), id.GetPos())
					}
					if _, cell := cells[sym]; cell {
						e.lowered(ref) // the cell itself: no expression of the code as written
						ref = ast.NewMemberExpression(ref, "v", id.GetPos())
					}
					e.originate(ref, id)
					if ty := fieldAnnot(cd, f); annotIs(ty, "any") {
						// The field holds a box whatever the local's type.
						if e.boxedCapReads == nil {
							e.boxedCapReads = map[ast.Expression]bool{}
						}
						e.boxedCapReads[ref] = true
					}
					return ref
				}
			}
		}
		return n
	}
	for i := range cd.Fields {
		if cd.Fields[i].Initializer != nil {
			static = cd.Fields[i].Static
			cd.Fields[i].Initializer = rw(cd.Fields[i].Initializer).(ast.Expression)
		}
	}
	static = false
	member := func(m *ast.FunctionDeclaration, at int) {
		need, needOrder = map[string]string{}, nil
		rw(m)
		if len(needOrder) == 0 || m.Body == nil {
			return
		}
		var decls []ast.Statement
		for _, local := range needOrder {
			f := need[local]
			pos := m.Body.GetPos()
			init := ast.NewMemberExpression(ast.NewThisExpression(pos), f, pos)
			e.lowered(init)
			ty := fieldAnnot(cd, f)
			for _, cf := range cd.Fields {
				if cf.Name == f {
					ty = cf.Type // a cell field: the cell itself
				}
			}
			decls = append(decls, ast.NewVarDeclaration("const", local, ty, init, pos))
		}
		rest := append(decls, m.Body.Body[at:]...)
		m.Body.Body = append(append([]ast.Statement{}, m.Body.Body[:at]...), rest...)
	}
	if cd.Constructor != nil {
		member(cd.Constructor, superAt+1)
	}
	for _, m := range cd.Methods {
		static = m.IsStatic
		member(m, 0)
	}
	static = true
	for _, sb := range cd.StaticBlocks {
		rw(sb)
	}
	for sym, ty := range cells {
		e.toCell(b, sym, ty)
	}
	if e.classCapturesPending == nil {
		e.classCapturesPending = map[*ast.ClassDeclaration][]classCapture{}
	}
	e.classCapturesPending[cd] = list
	if len(statics) > 0 {
		if e.classStaticCapsPending == nil {
			e.classStaticCapsPending = map[*ast.ClassDeclaration][]classCapture{}
		}
		e.classStaticCapsPending[cd] = statics
	}
	return true
}

// classLocalRef is one reference in a class body to a local outside it.
type classLocalRef struct {
	id       *ast.Identifier
	sym      *binder.Symbol
	static   bool // in a static member
	nestedFn bool // inside a non-arrow function nested in a member
}

// classLocalRefs lists the references cd's body makes to locals of the
// enclosing functions and blocks (classDeclCaptures, every one).
func classLocalRefs(b *binder.Binding, cd *ast.ClassDeclaration, hoisted map[ast.Node]bool) []classLocalRef {
	var out []classLocalRef
	inside := map[*binder.Scope]bool{}
	var walk func(n ast.Node, static, nested bool)
	walk = func(n ast.Node, static, nested bool) {
		ast.Inspect(n, func(m ast.Node) bool {
			if s := b.ScopeOf(m); s != nil {
				inside[s] = true
			}
			switch m := m.(type) {
			case *ast.FunctionExpression:
				if m != n {
					walk(m.Body, static, true)
					return false
				}
			case *ast.FunctionDeclaration:
				if m != n {
					walk(m, static, true)
					return false
				}
			case *ast.Identifier:
				sym, _ := b.Resolve(m)
				if sym == nil || sym.Scope == nil || sym.Scope.Kind == binder.ModuleScope {
					return true
				}
				for _, d := range sym.Declarations {
					if d.Node == ast.Node(cd) || hoisted[d.Node] {
						return true
					}
				}
				for s := sym.Scope; s != nil; s = s.Parent {
					if inside[s] {
						return true
					}
				}
				out = append(out, classLocalRef{id: m, sym: sym, static: static, nestedFn: nested})
			}
			return true
		})
	}
	ast.Inspect(cd, func(n ast.Node) bool {
		if s := b.ScopeOf(n); s != nil {
			inside[s] = true
		}
		return true
	})
	for _, f := range cd.Fields {
		if f.Initializer != nil {
			walk(f.Initializer, f.Static, false)
		}
	}
	if cd.Constructor != nil {
		walk(cd.Constructor, false, false)
	}
	for _, m := range cd.Methods {
		walk(m, m.IsStatic, false)
	}
	for _, sb := range cd.StaticBlocks {
		walk(sb, true, false)
	}
	return out
}

// symbolWritten reports a store to sym other than its declaration's.
func symbolWritten(b *binder.Binding, sym *binder.Symbol) bool {
	for _, st := range b.StoresTo(sym) {
		if st == nil {
			return true
		}
		if !declaredBy(sym, st) {
			return true
		}
	}
	return false
}

// writtenAfterCapture is symbolWritten, except for a write in the update
// clause of the `for (let …)` that declares sym (it writes the next
// iteration's copy of the binding, never the one an iteration's class
// captured) and a `for (const x of …)` / `for (let k in …)` binding each
// element (each iteration's own binding).
func (e *Emitter) writtenAfterCapture(b *binder.Binding, sym *binder.Symbol) bool {
	updates := map[ast.Node]bool{}
	if e.prog != nil {
		ast.Inspect(e.prog, func(n ast.Node) bool {
			fs, ok := n.(*ast.ForStatement)
			if !ok {
				return true
			}
			var decls []*ast.VarDeclaration
			switch d := fs.Init.(type) {
			case *ast.VarDeclaration:
				decls = []*ast.VarDeclaration{d}
			case *ast.VarDeclarationList:
				decls = d.Decls
			}
			for _, d := range decls {
				if d.Kind != "var" && declaredBy(sym, d) {
					for _, u := range fs.Update {
						ast.Inspect(u, func(m ast.Node) bool {
							updates[m] = true
							return true
						})
					}
				}
			}
			return true
		})
	}
	// A `for (const x of …)` / `for (let k in …)` binding: the loop's own
	// store of each element (recorded without a node) is the binding of
	// each iteration, not a write after the class.
	perIteration := false
	for _, d := range sym.Declarations {
		switch l := d.Node.(type) {
		case *ast.ForOfStatement:
			perIteration = l.Kind != "var"
		case *ast.ForInStatement:
			perIteration = l.Kind != "var"
		}
	}
	for _, st := range b.StoresTo(sym) {
		if st == nil {
			if perIteration {
				continue
			}
			return true
		}
		if !declaredBy(sym, st) && !updates[st] {
			return true
		}
	}
	return false
}

func declaredBy(sym *binder.Symbol, n ast.Node) bool {
	for _, d := range sym.Declarations {
		if d.Node == n {
			return true
		}
	}
	return false
}

// captureAnnotation is the hidden field's type: the local's own annotation,
// else the primitive the checker gives it, else any.
func (e *Emitter) captureAnnotation(c frontView, sym *binder.Symbol, refs []classLocalRef) ast.TypeNode {
	for _, d := range sym.Declarations {
		switch n := d.Node.(type) {
		case *ast.VarDeclaration:
			if n.TypeAnnot != nil {
				return n.TypeAnnot
			}
		case *ast.FunctionDeclaration:
			if t := paramAnnot(n.Params, sym.Name); t != nil {
				return t
			}
		case *ast.FunctionExpression:
			if t := paramAnnot(n.Params, sym.Name); t != nil {
				return t
			}
		case *ast.ArrowFunction:
			if t := paramAnnot(n.Params, sym.Name); t != nil {
				return t
			}
		}
	}
	name := "any"
	for _, r := range refs {
		if r.sym != sym {
			continue
		}
		t := c.TypeOf(r.id)
		switch {
		case c.Unanswered(t) || t.Flags&checker.Union != 0:
		case t.Flags&(checker.String|checker.StringLiteral) != 0:
			name = "string"
		case t.Flags&(checker.Number|checker.NumberLiteral) != 0 && !t.AnyArith:
			name = "number"
		case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
			name = "boolean"
		}
		break
	}
	return nameAnnotation(name)
}

func paramAnnot(params []ast.Param, name string) ast.TypeNode {
	for _, p := range params {
		if p.Name == name && !p.Rest && p.ArrayPattern == nil && p.ObjectPattern == nil {
			return p.Type
		}
	}
	return nil
}

// storeClassCaptures fills the hidden fields of a new instance of className
// (and of the closure-converted classes it extends) from the locals in
// scope, or from `this` for a `new` inside such a class.
func (e *Emitter) storeClassCaptures(ex *ast.NewExpression, className, dataReg string, ty Type) error {
	var caps []classCapture
	for cls := className; cls != ""; {
		caps = append(caps, e.capturesOf(cls)...)
		info, ok := e.classes[cls]
		if !ok {
			break
		}
		cls = info.BaseClass
	}
	if len(caps) == 0 {
		return nil
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", dataReg, slot))
	const tmp = "__kml_new_instance"
	e.pushScope()
	defer e.popScope()
	e.define(tmp, Symbol{Ptr: slot, Ty: ty})
	pos := ex.GetPos()
	stored := map[string]bool{}
	// In a construction arm (no local in scope), the bindings come from the
	// record the `new` goes through.
	recBindings := map[string]string{}
	rec := ""
	if e.inCtorArm && !e.classCapFromThis[ex] {
		r, err := e.bindRecordCaptures(className, recBindings)
		if err != nil {
			return err
		}
		rec = r
	}
	if e.classRecords[className] {
		// The instance remembers its evaluation, for instanceof.
		switch {
		case rec != "":
		case e.classCapFromThis[ex]:
			self, err := e.emitExpr(ast.NewThisExpression(pos))
			if err != nil {
				return err
			}
			rec = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classrec_of("+irPtr+" %s)", rec, self.Ref))
		default:
			if sym, ok := e.lookup(className + classRecSuffix); ok {
				rec = e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", rec, sym.Ptr))
			}
		}
		if rec != "" {
			e.emitInstr(fmt.Sprintf("call void @__kml_classrec_bind("+irPtr+" %s, "+irPtr+" %s)", dataReg, rec))
		}
	}
	for _, cp := range caps {
		if stored[cp.field] {
			continue
		}
		stored[cp.field] = true
		var val ast.Expression = ast.NewIdentifier(cp.local, pos)
		if e.classCapFromThis[ex] {
			val = ast.NewMemberExpression(ast.NewThisExpression(pos), cp.field, pos)
		} else if recName, ok := recBindings[cp.field]; ok {
			val = ast.NewIdentifier(recName, pos)
		}
		set := ast.NewAssignmentExpression("=", ast.NewMemberExpression(ast.NewIdentifier(tmp, pos), cp.field, pos), val, pos)
		if _, err := e.emitExpr(set); err != nil {
			return err
		}
	}
	return nil
}

// cellSite is the block a written local's cell is declared in: the block
// holding its `let`/`var` declaration as a statement, or a function's body
// for a parameter. Nil for any other declaration (a loop or catch variable,
// a destructured name), which keeps its own per-iteration semantics.
func cellSite(sym *binder.Symbol) *ast.BlockStatement {
	if len(sym.Declarations) != 1 || sym.Scope == nil {
		return nil
	}
	switch n := sym.Declarations[0].Node.(type) {
	case *ast.VarDeclaration:
		if n.Kind == "var" {
			return nil
		}
		// A statement of a block, alone or in a declaration list (`let a,
		// b;`), with or without an initializer.
		var site *ast.BlockStatement
		ast.Inspect(sym.Scope.Node, func(m ast.Node) bool {
			if blk, ok := m.(*ast.BlockStatement); ok {
				for _, st := range blk.Body {
					if st == ast.Statement(n) {
						site = blk
					}
					if vl, ok := st.(*ast.VarDeclarationList); ok {
						for _, d := range vl.Decls {
							if d == n {
								site = blk
							}
						}
					}
				}
			}
			return site == nil
		})
		return site
	case *ast.FunctionDeclaration:
		if paramAnnot(n.Params, sym.Name) != nil || hasParam(n.Params, sym.Name) {
			return n.Body
		}
	case *ast.FunctionExpression:
		if hasParam(n.Params, sym.Name) {
			return n.Body
		}
	case *ast.ArrowFunction:
		if hasParam(n.Params, sym.Name) {
			return n.Block
		}
	case *ast.ObjectDestructuring, *ast.ArrayDestructuring:
		// `let { a } = o`: a declaration after the destructuring, in the
		// block holding it.
		if k := destructKind(n); k == "var" {
			return nil
		}
		var site *ast.BlockStatement
		ast.Inspect(sym.Scope.Node, func(m ast.Node) bool {
			if blk, ok := m.(*ast.BlockStatement); ok {
				for _, st := range blk.Body {
					if ast.Node(st) == n {
						site = blk
					}
				}
			}
			return site == nil
		})
		return site
	case *ast.ForOfStatement:
		// `for (let x of …)`: each iteration binds x anew, so its cell
		// opens the body, with nothing to write back.
		if n.Kind != "var" && n.Body != nil {
			return n.Body
		}
	case *ast.ForInStatement:
		if n.Kind != "var" && n.Body != nil {
			return n.Body
		}
	case *ast.TryStatement:
		// `catch (err)`: a declaration opening the clause's body, as a
		// parameter's opens its function's.
		if c := n.Catch; c != nil && c.Param == sym.Name && c.ObjectPattern == nil {
			return c.Body
		}
	}
	return nil
}

func hasParam(params []ast.Param, name string) bool {
	for _, p := range params {
		if p.Name == name && !p.Rest && p.ArrayPattern == nil && p.ObjectPattern == nil {
			return true
		}
	}
	return false
}

// toCell moves written local sym into a cell `name__kml_cell: { v: T }`:
// its declaration (or, for a parameter, a declaration opening the body)
// makes the cell, and every reference reads and writes `cell.v`.
func (e *Emitter) toCell(b *binder.Binding, sym *binder.Symbol, ty ast.TypeNode) {
	site := cellSite(sym)
	cellName := sym.Name + "__kml_cell"
	if fs := forCellLoop(sym); fs != nil && site == nil {
		e.loopCell(b, sym, fs, cellName, cellAnnotation(ty))
		return
	}
	var rw func(n ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		ast.RewriteChildren(n, rw)
		if id, ok := n.(*ast.Identifier); ok {
			if s, _ := b.Resolve(id); s == sym {
				return ast.NewMemberExpression(ast.NewIdentifier(cellName, id.GetPos()), "v", id.GetPos())
			}
		}
		return n
	}
	rw(sym.Scope.Node)
	cellTy := cellAnnotation(ty)
	if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok {
		v.Kind, v.Name = "const", cellName
		init := v.Init
		if init == nil {
			init = ast.NewNullLiteral(true, v.GetPos())
		}
		v.Init = ast.NewObjectLiteral([]ast.ObjectProperty{{Key: "v", Value: init}}, v.GetPos())
		v.TypeAnnot = cellTy
		return
	}
	pos := site.GetPos()
	decl := ast.NewVarDeclaration("const", cellName, cellTy, ast.NewObjectLiteral([]ast.ObjectProperty{{Key: "v", Value: ast.NewIdentifier(sym.Name, pos)}}, pos), pos)
	for _, st := range site.Body {
		if ast.Node(st) == sym.Declarations[0].Node {
			// After the destructuring that binds it: spliced in as the
			// block being converted is rebuilt (cellsAfter).
			if e.cellsAfter == nil {
				e.cellsAfter = map[ast.Statement][]ast.Statement{}
			}
			e.cellsAfter[st] = append(e.cellsAfter[st], decl)
			return
		}
	}
	site.Body = append([]ast.Statement{decl}, site.Body...)
}

// spliceCells puts each pending cell statement in blk's rebuilt body:
// e.cellsBefore and e.cellsAfter around their statements, e.cellsTail at
// the end.
func (e *Emitter) spliceCells(blk *ast.BlockStatement, body []ast.Statement) []ast.Statement {
	if len(e.cellsAfter) == 0 && len(e.cellsBefore) == 0 && len(e.cellsTail) == 0 {
		return body
	}
	out := make([]ast.Statement, 0, len(body))
	for _, st := range body {
		if ds, ok := e.cellsBefore[st]; ok {
			out = append(out, ds...)
			delete(e.cellsBefore, st)
		}
		out = append(out, st)
		if ds, ok := e.cellsAfter[st]; ok {
			out = append(out, ds...)
			delete(e.cellsAfter, st)
		}
	}
	if ds, ok := e.cellsTail[blk]; ok {
		out = append(out, ds...)
		delete(e.cellsTail, blk)
	}
	return out
}

// forCellLoop is the `for (let i …)` loop whose initializer declares sym,
// its body a block; nil for any other binding.
func forCellLoop(sym *binder.Symbol) *ast.ForStatement {
	if len(sym.Declarations) != 1 || sym.Scope == nil || sym.Scope.Node == nil {
		return nil
	}
	v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration)
	if !ok || v.Kind != "let" {
		return nil
	}
	var loop *ast.ForStatement
	ast.Inspect(sym.Scope.Node, func(n ast.Node) bool {
		if fs, ok := n.(*ast.ForStatement); ok && fs.Body != nil {
			switch d := fs.Init.(type) {
			case *ast.VarDeclaration:
				if d == v {
					loop = fs
				}
			case *ast.VarDeclarationList:
				for _, x := range d.Decls {
					if x == v {
						loop = fs
					}
				}
			}
		}
		return loop == nil
	})
	return loop
}

// loopCell shares loop variable sym, which a local class in the loop body
// reads and the body writes, through a cell each iteration opens: the
// body reads and writes the cell, and its value goes back to the loop's
// own binding at the body's end and before each continue of the loop, so
// the test and update clauses see it.
func (e *Emitter) loopCell(b *binder.Binding, sym *binder.Symbol, fs *ast.ForStatement, cellName string, cellTy ast.TypeNode) {
	label := ""
	ast.Inspect(e.prog, func(n ast.Node) bool { // the label is outside the loop's scope
		if ls, ok := n.(*ast.LabeledStatement); ok && ls.Body == ast.Statement(fs) {
			label = ls.Label
		}
		return label == ""
	})
	pos := fs.Body.GetPos()
	writeBack := func() ast.Statement {
		cellRead := ast.NewMemberExpression(ast.NewIdentifier(cellName, pos), "v", pos)
		return ast.NewExpressionStatement(ast.NewAssignmentExpression("=", ast.NewIdentifier(sym.Name, pos), cellRead, pos), pos)
	}
	cellRef := func(at ast.Pos) ast.Expression {
		return ast.NewMemberExpression(ast.NewIdentifier(cellName, at), "v", at)
	}
	// depth counts the loops a bare continue would target instead.
	var rw func(n ast.Node, depth int) ast.Node
	rw = func(n ast.Node, depth int) ast.Node {
		inner := depth
		switch n.(type) {
		case *ast.ForStatement, *ast.ForOfStatement, *ast.ForInStatement, *ast.WhileStatement, *ast.DoWhileStatement:
			inner++
		}
		ast.RewriteChildren(n, func(m ast.Node) ast.Node { return rw(m, inner) })
		switch x := n.(type) {
		case *ast.Identifier:
			if s, _ := b.Resolve(x); s == sym {
				return cellRef(x.GetPos())
			}
		case *ast.ContinueStatement:
			if x.Label == "" && depth == 0 || x.Label != "" && x.Label == label {
				return ast.NewBlockStatement([]ast.Statement{writeBack(), x}, x.GetPos())
			}
		}
		return n
	}
	for i, st := range fs.Body.Body {
		if c, ok := st.(*ast.ContinueStatement); ok && (c.Label == "" || c.Label == label) {
			// A continue of the body itself: the block rebuild keeps the
			// statement, so its write-back goes in before it.
			if e.cellsBefore == nil {
				e.cellsBefore = map[ast.Statement][]ast.Statement{}
			}
			e.cellsBefore[st] = append(e.cellsBefore[st], writeBack())
			continue
		}
		fs.Body.Body[i] = rw(st, 0).(ast.Statement)
	}
	decl := ast.NewVarDeclaration("const", cellName, cellTy, ast.NewObjectLiteral([]ast.ObjectProperty{{Key: "v", Value: ast.NewIdentifier(sym.Name, pos)}}, pos), pos)
	fs.Body.Body = append([]ast.Statement{decl}, fs.Body.Body...)
	if e.cellsTail == nil {
		e.cellsTail = map[*ast.BlockStatement][]ast.Statement{}
	}
	e.cellsTail[fs.Body] = append(e.cellsTail[fs.Body], writeBack())
}

// destructKind is a destructuring declaration's keyword.
func destructKind(n ast.Node) string {
	switch d := n.(type) {
	case *ast.ObjectDestructuring:
		return d.Kind
	case *ast.ArrayDestructuring:
		return d.Kind
	}
	return ""
}

// classRecSuffix names the local holding a class's record in the function
// that declares it.
const classRecSuffix = "__kml_rec"

// emitClassRecord makes this evaluation's record of class name: its static
// reference and its captured bindings, boxed (TDD-00242), held by a local
// the class's name reads in value position.
func (e *Emitter) emitClassRecord(name string) error {
	e.ensureNanBox()
	caps := e.capturesOf(name)
	base := "null"
	if info, ok := e.classes[name]; ok && info.BaseClass != "" {
		if b, ok := e.lookup(info.BaseClass + classRecSuffix); ok {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", r, b.Ptr))
			base = r
		}
	}
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classrec_new("+irPtr+" %s, "+irPtr+" %s, i64 %d, i64 %d)", rec, e.classRefPtr(name), base, len(caps), ctorRowBytes))
	for i, cp := range caps {
		v, err := e.emitExpr(ast.NewIdentifier(cp.local, ast.Pos{}))
		if err != nil {
			return err
		}
		b, err := e.emitBoxValue(v)
		if err != nil {
			return err
		}
		at := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds i64, "+irPtr+" %s, i64 %d", at, rec, 2+i))
		e.heapStore("i64", b.Ref, at, 8)
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", rec, slot))
	e.define(name+classRecSuffix, Symbol{Ptr: slot, Ty: TypePtr})
	// The locals its static members read, into the record's statics.
	for _, cp := range e.classStaticCaps[name] {
		v, err := e.emitExpr(ast.NewIdentifier(cp.local, ast.Pos{}))
		if err != nil {
			return err
		}
		ty := e.classes[name].StaticFieldTypes[cp.field]
		v = e.convert(v, ty)
		e.heapStore(storageIR(ty), v.Ref, e.staticFieldWritePtr(name, cp.field), ty.Align())
	}
	return nil
}

// bindRecordCaptures binds, in a construction arm of className, each hidden
// field's value from the pending record: out maps the field to a local of
// the field's type. A construction of the class itself (no record) leaves
// the fields as allocated.
func (e *Emitter) bindRecordCaptures(className string, out map[string]string) (string, error) {
	caps := e.capturesOf(className)
	if len(caps) == 0 || !e.classRecords[className] {
		return "", nil
	}
	info := e.classes[className]
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classrec_current("+irPtr+" %s)", rec, e.classRefPtr(className)))
	for i, cp := range caps {
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_classrec_cap("+irPtr+" %s, i64 %d)", w, rec, i))
		fieldTy := TypeAny
		if _, fty, ok := info.Ty.FieldIndex(cp.field); ok {
			fieldTy = fty
		}
		v := e.convert(Value{Ref: w, Ty: TypeAny}, fieldTy)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, StructFieldIR(fieldTy), fieldTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", StructFieldIR(fieldTy), v.Ref, slot, fieldTy.Align()))
		name := fmt.Sprintf("__kml_rcap%d", i)
		e.define(name, Symbol{Ptr: slot, Ty: fieldTy})
		out[cp.field] = name
	}
	return rec, nil
}

// fieldAnnot is the annotation of cd's field name, cells unwrapped to their
// value's.
func fieldAnnot(cd *ast.ClassDeclaration, name string) ast.TypeNode {
	for _, f := range cd.Fields {
		if f.Name == name && f.Type != nil {
			if tl, ok := ast.Unparen(f.Type).(*ast.TypeLiteral); ok && len(tl.Members) == 1 {
				if ps, ok := tl.Members[0].(*ast.PropertySignature); ok && ps.Name == "v" && ps.Type != nil {
					return ps.Type
				}
			}
			return f.Type
		}
	}
	return nil
}

// cellAnnotation is `{ v: T }`, the cell sharing a captured local of type
// ty: a type node over ty's own, which the checker reads.
func cellAnnotation(ty ast.TypeNode) ast.TypeNode {
	var v ast.TypeNode = &ast.KeywordType{Keyword: "any"}
	if ty != nil {
		v = ty
	}
	lit := &ast.TypeLiteral{Members: []ast.TypeMember{&ast.PropertySignature{Name: "v", Type: v}}}
	if ta, err := ast.TypeAnnotationOf(lit, "ts"); err == nil {
		return ta
	}
	return lit
}

// addClassCaptureReads adds to refs, a closure's free names, the locals a
// closure-converted class it constructs reads (`() => new D()`, D naming
// b): its `new` fills the instance's hidden fields from them.
func (e *Emitter) addClassCaptureReads(refs map[string]bool) {
	for name := range refs {
		for cls, seen := name, 0; cls != "" && seen < 16; seen++ {
			for _, cp := range e.capturesOf(cls) {
				refs[cp.local] = true
			}
			info, ok := e.classes[cls]
			if !ok {
				break
			}
			cls = info.BaseClass
		}
	}
}

// capturesOf is the captures of closure-converted class cls, an
// instantiation of a generic class taking its generic's.
func (e *Emitter) capturesOf(cls string) []classCapture {
	if caps, ok := e.classCaptures[cls]; ok {
		return caps
	}
	if info, ok := e.classes[cls]; ok && info.GenericOf != "" {
		return e.classCaptures[info.GenericOf]
	}
	return nil
}

// superCallStmt is a statement that is a bare `super(…)` call.
func superCallStmt(st ast.Statement) bool {
	es, ok := st.(*ast.ExpressionStatement)
	if !ok {
		return false
	}
	call, ok := es.Expr.(*ast.CallExpression)
	if !ok {
		return false
	}
	_, ok = call.Callee.(*ast.SuperExpression)
	return ok
}

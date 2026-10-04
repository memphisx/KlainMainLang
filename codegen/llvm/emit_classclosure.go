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
func (e *Emitter) closureConvertClass(c *checker.Checker, b *binder.Binding, blk *ast.BlockStatement, cd *ast.ClassDeclaration, hoisted map[ast.Node]bool) bool {
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
	case len(cd.TypeParams) > 0:
		return reject("is generic")
	case len(cd.AutoAccessors) > 0:
		return reject("has decorated accessors")
	}
	// Every reference must sit outside any nested non-arrow function (whose
	// `this` is not the instance or the class), and the local must keep its
	// initial value. A static member's references read the evaluation's
	// record (TDD-00242).
	order := []*binder.Symbol{}
	seen := map[*binder.Symbol]bool{}
	staticRef := map[*binder.Symbol]bool{}
	for _, r := range caps {
		if r.static {
			staticRef[r.sym] = true
		}
		if r.nestedFn {
			return reject("reads it in a nested function")
		}
		if !seen[r.sym] {
			seen[r.sym] = true
			order = append(order, r.sym)
		}
	}
	written := map[*binder.Symbol]bool{}
	for _, sym := range order {
		if symbolWritten(b, sym) {
			if cellSite(sym) == nil {
				return reject("'" + sym.Name + "' is a loop, catch or destructured binding written after the class")
			}
			written[sym] = true
		}
	}
	// The class as a value (`return D`, `D.x`): a class expression always is.
	self := blk == nil
	ast.Inspect(blk, func(n ast.Node) bool {
		if self {
			return false
		}
		if be, ok := n.(*ast.BinaryExpression); ok && be.Op == "instanceof" {
			if id, ok := be.Right.(*ast.Identifier); ok && id.Name == cd.Name {
				ast.Inspect(be.Left, func(m ast.Node) bool {
					if id, ok := m.(*ast.Identifier); ok && id.Name == cd.Name {
						self = true
					}
					return !self
				})
				return false
			}
		}
		if id, ok := n.(*ast.Identifier); ok && id.Name == cd.Name {
			if sym, _ := b.Resolve(id); sym != nil && declaredBy(sym, cd) {
				self = true
			}
		}
		return true
	})
	if self || len(staticRef) > 0 {
		// Used as a value, or with statics reading a local: each
		// evaluation is a record (TDD-00242).
		if e.classRecordsPending == nil {
			e.classRecordsPending = map[*ast.ClassDeclaration]bool{}
		}
		e.classRecordsPending[cd] = true
	}

	byName := map[*binder.Symbol]string{}
	cells := map[*binder.Symbol]*ast.TypeAnnotation{}
	var list, statics []classCapture
	for _, sym := range order {
		local := sym.Name
		ty := e.captureAnnotation(c, sym, caps)
		if written[sym] {
			// A local written after its declaration is shared through a
			// cell, `{ v: T }`, that the function and every instance hold.
			cells[sym] = ty
			ty = &ast.TypeAnnotation{Source: "ts", Fields: []ast.AnnotField{{Name: "v", Type: ty}}}
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
					var ref ast.Expression = ast.NewMemberExpression(ast.NewThisExpression(id.GetPos()), f, id.GetPos())
					if static {
						ref = ast.NewMemberExpression(ast.NewIdentifier(cd.Name, id.GetPos()), staticCaptureField(sym.Name), id.GetPos())
					}
					if _, cell := cells[sym]; cell {
						ref = ast.NewMemberExpression(ref, "v", id.GetPos())
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
	if cd.Constructor != nil {
		rw(cd.Constructor)
	}
	for _, m := range cd.Methods {
		static = m.IsStatic
		rw(m)
	}
	static = true
	for _, sb := range cd.StaticBlocks {
		rw(sb)
	}
	for sym, ty := range cells {
		toCell(b, sym, ty)
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
func (e *Emitter) captureAnnotation(c *checker.Checker, sym *binder.Symbol, refs []classLocalRef) *ast.TypeAnnotation {
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
		case t.Flags&(checker.Number|checker.NumberLiteral) != 0:
			name = "number"
		case t.Flags&(checker.Boolean|checker.BooleanLiteral) != 0:
			name = "boolean"
		}
		break
	}
	return &ast.TypeAnnotation{Name: name, Source: "ts"}
}

func paramAnnot(params []ast.Param, name string) *ast.TypeAnnotation {
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
		caps = append(caps, e.classCaptures[cls]...)
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
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, slot))
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
		e.ensureClassRec()
		switch {
		case rec != "":
		case e.classCapFromThis[ex]:
			self, err := e.emitExpr(ast.NewThisExpression(pos))
			if err != nil {
				return err
			}
			rec = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classrec_of(ptr %s)", rec, self.Ref))
		default:
			if sym, ok := e.lookup(className + classRecSuffix); ok {
				rec = e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rec, sym.Ptr))
			}
		}
		if rec != "" {
			e.emitInstr(fmt.Sprintf("call void @__kml_classrec_bind(ptr %s, ptr %s)", dataReg, rec))
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
		if n.Init == nil {
			return nil
		}
		var site *ast.BlockStatement
		ast.Inspect(sym.Scope.Node, func(m ast.Node) bool {
			if blk, ok := m.(*ast.BlockStatement); ok {
				for _, st := range blk.Body {
					if st == ast.Statement(n) {
						site = blk
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
func toCell(b *binder.Binding, sym *binder.Symbol, ty *ast.TypeAnnotation) {
	site := cellSite(sym)
	cellName := sym.Name + "__kml_cell"
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
	cellTy := &ast.TypeAnnotation{Source: "ts", Fields: []ast.AnnotField{{Name: "v", Type: ty}}}
	if v, ok := sym.Declarations[0].Node.(*ast.VarDeclaration); ok {
		v.Kind, v.Name = "const", cellName
		v.Init = ast.NewObjectLiteral([]ast.ObjectProperty{{Key: "v", Value: v.Init}}, v.GetPos())
		v.TypeAnnot = cellTy
		return
	}
	pos := site.GetPos()
	decl := ast.NewVarDeclaration("const", cellName, cellTy, ast.NewObjectLiteral([]ast.ObjectProperty{{Key: "v", Value: ast.NewIdentifier(sym.Name, pos)}}, pos), pos)
	site.Body = append([]ast.Statement{decl}, site.Body...)
}

// classRecSuffix names the local holding a class's record in the function
// that declares it.
const classRecSuffix = "__kml_rec"

// ensureClassRec declares the evaluation-record runtime (unitregsrc).
func (e *Emitter) ensureClassRec() {
	e.ensureUnitReg()
	if e.usedClassRec {
		return
	}
	e.usedClassRec = true
	e.emitGlobal("declare ptr @__kml_classrec_new(ptr, ptr, i64, i64)")
	e.emitGlobal("declare void @__kml_classrec_bind(ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_classrec_of(ptr)")
	e.emitGlobal("declare i32 @__kml_classrec_is(ptr, ptr)")
	e.emitGlobal("declare void @__kml_classrec_enter(ptr)")
	e.emitGlobal("declare ptr @__kml_classrec_current(ptr)")
	e.emitGlobal("declare i64 @__kml_classrec_cap(ptr, i64)")
	e.emitGlobal("declare ptr @__kml_classref_name(ptr)")
	e.emitGlobal("declare ptr @__kml_classrec_statics(ptr, ptr, i64)")
	e.emitGlobal("declare ptr @__kml_classrec_up(ptr, ptr)")
	e.emitGlobal("declare ptr @__kml_classrec_recv(ptr)")
	e.emitGlobal("declare ptr @__kml_classrec_static_cur(ptr)")
}

// emitClassRecord makes this evaluation's record of class name: its static
// reference and its captured bindings, boxed (TDD-00242), held by a local
// the class's name reads in value position.
func (e *Emitter) emitClassRecord(name string) error {
	e.ensureClassRec()
	e.ensureNanBox()
	caps := e.classCaptures[name]
	base := "null"
	if info, ok := e.classes[name]; ok && info.BaseClass != "" {
		if b, ok := e.lookup(info.BaseClass + classRecSuffix); ok {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, b.Ptr))
			base = r
		}
	}
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classrec_new(ptr %s, ptr %s, i64 %d, i64 %d)", rec, e.classRefPtr(name), base, len(caps), ctorRowBytes))
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
		e.emitInstr(fmt.Sprintf("%s = getelementptr inbounds i64, ptr %s, i64 %d", at, rec, 2+i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, at))
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", rec, slot))
	e.define(name+classRecSuffix, Symbol{Ptr: slot, Ty: TypePtr})
	// The locals its static members read, into the record's statics.
	for _, cp := range e.classStaticCaps[name] {
		v, err := e.emitExpr(ast.NewIdentifier(cp.local, ast.Pos{}))
		if err != nil {
			return err
		}
		ty := e.classes[name].StaticFieldTypes[cp.field]
		v = e.coerce(v, ty)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, v.Ref, e.staticFieldWritePtr(name, cp.field), ty.Align()))
	}
	return nil
}

// bindRecordCaptures binds, in a construction arm of className, each hidden
// field's value from the pending record: out maps the field to a local of
// the field's type. A construction of the class itself (no record) leaves
// the fields as allocated.
func (e *Emitter) bindRecordCaptures(className string, out map[string]string) (string, error) {
	caps := e.classCaptures[className]
	if len(caps) == 0 || !e.classRecords[className] {
		return "", nil
	}
	e.ensureClassRec()
	info := e.classes[className]
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classrec_current(ptr %s)", rec, e.classRefPtr(className)))
	for i, cp := range caps {
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_classrec_cap(ptr %s, i64 %d)", w, rec, i))
		fieldTy := TypeAny
		if _, fty, ok := info.Ty.FieldIndex(cp.field); ok {
			fieldTy = fty
		}
		v := e.coerce(Value{Ref: w, Ty: TypeAny}, fieldTy)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, StructFieldIR(fieldTy), fieldTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), v.Ref, slot, fieldTy.Align()))
		name := fmt.Sprintf("__kml_rcap%d", i)
		e.define(name, Symbol{Ptr: slot, Ty: fieldTy})
		out[cp.field] = name
	}
	return rec, nil
}

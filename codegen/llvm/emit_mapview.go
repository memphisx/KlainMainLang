// emit_mapview.go — a Map or Set read through a view type whose key or value
// involves `any` (TDD-00258).
//
// A Map or Set stores its entries in its own instantiation's representation
// (raw strings and doubles for a Map<string, number>, boxes for a Map<any,
// any>). A view of another instantiation — a typed map passed to a
// `Map<any, any>` parameter, `box as Map<any, any>` — must not run its own
// flavor over those words. Its operations test the object's header word: its
// own layout runs as before; any other dispatches to a routine generated per
// layout at module end, which runs that layout's own typed operation on the
// view's `any` operands and boxes the result. Reads that walk every entry
// (iteration, keys/values/entries, forEach, inspect) read a snapshot copied
// into the view's layout; the object itself stays shared, so writes through
// the view reach the original.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"sort"
	"strings"
)

// mapViewOps are the operations dispatched by layout, with their arity.
var mapViewOps = map[string]int{"get": 1, "has": 1, "delete": 1, "set": 2, "add": 1}

// isMapView reports a Map or Set type whose object may be another
// instantiation: any of them, since `any` and an assertion reach a typed
// variable without converting (`box as Map<string, number>`).
func isMapView(t Type) bool {
	return (t.IsMap() || t.IsSet()) && !t.IsClass() && !t.Weak && t.MapKey != nil
}

// noteMapLayout registers a Map or Set instantiation the program creates,
// so the view dispatchers cover it.
func (e *Emitter) noteMapLayout(t Type) {
	if t.IsClass() || t.MapKey == nil {
		return
	}
	base := t
	base.setNullable(false)
	base.setUndefined(false)
	id := e.hostHeaderWord(base)
	for _, h := range e.mapLayouts {
		if h.id == id {
			return
		}
	}
	e.mapLayouts = append(e.mapLayouts, hostLayout{id: id, class: hostClassName(base), ty: base})
}

// mapViewIsOwn tests whether ptr reads as view's own layout: its header
// word is view's, or names none of the program's other Map or Set layouts
// (a class extending Map carries its class's word, over view's layout).
// Only a header other than view's pays the call.
func (e *Emitter) mapViewIsOwn(view Type, ptr string) string {
	hp, hdr, eq := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 -8", hp, ptr))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, hp))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", eq, e.headerIdentity(hdr), e.hostHeaderWord(e.mapViewBase(view))))
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", slot))
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", slot))
	checkL, joinL := e.freshLabel("mview.check"), e.freshLabel("mview.owned")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eq, joinL, checkL))
	e.emitLabel(checkL)
	f, notF := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s, i64 0, i64 0)", f, e.mapViewFn("foreign", view), ptr))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", notF, f))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", notF, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	own := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", own, slot))
	return own
}

func (e *Emitter) mapViewBase(t Type) Type {
	b := t
	b.setNullable(false)
	b.setUndefined(false)
	return b
}

// mapViewPtr is ptr as view reads every entry: the object itself when it
// is view's own layout, else a snapshot copied into view's layout.
func (e *Emitter) mapViewPtr(view Type, ptr string) string {
	if !isMapView(view) || e.programIndependent() {
		return ptr
	}
	e.noteMapLayout(view)
	e.usedMapView = true
	own := e.mapViewIsOwn(view, ptr)
	ownL, otherL, joinL := e.freshLabel("mview.own"), e.freshLabel("mview.copy"), e.freshLabel("mview.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", own, ownL, otherL))
	e.emitLabel(otherL)
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr %s(ptr %s)", cp, e.mapViewFn("copy", view), ptr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(ownL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", r, cp, otherL, ptr, ownL))
	return r
}

// mapViewFn names the dispatcher for op through view, registering it.
func (e *Emitter) mapViewFn(op string, view Type) string {
	e.usedMapView = true // every request reaches emitMapViewFinalize
	key := op + "|" + hostKey(e.mapViewBase(view))
	if e.mapViewFns == nil {
		e.mapViewFns = map[string]mapViewReq{}
	}
	if r, ok := e.mapViewFns[key]; ok {
		return r.name
	}
	name := fmt.Sprintf("@__kml_mview_%s_%d", op, e.hostHeaderWord(e.mapViewBase(view))&kmlHdrIDMask)
	e.mapViewFns[key] = mapViewReq{name: name, op: op, view: e.mapViewBase(view)}
	return name
}

type mapViewReq struct {
	name string
	op   string
	view Type
}

// emitMapViewCall emits a dispatched operation (mapViewOps) through view:
// args are evaluated once, into `any` temporaries both paths read; own
// emits the operation for view's own layout over those temporaries. The
// other path boxes each, calls the dispatcher and converts its boxed result
// to own's result type (set returns the receiver).
func (e *Emitter) emitMapViewCall(view Type, ptr, op string, args []ast.Expression, own func(args []ast.Expression) (Value, error)) (Value, error) {
	e.noteMapLayout(view)
	e.usedMapView = true
	// Each argument is evaluated once into a temporary of its own type, so
	// the own-layout path runs unboxed; the other path boxes it.
	temps := make([]ast.Expression, len(args))
	vals := make([]Value, len(args))
	for i, a := range args {
		var v Value
		var err error
		if vt := view.MapVal; op == "set" && i == 1 && vt != nil && mapValHinted(*vt) {
			v, err = e.emitExprWithObjectHint(a, *vt) // built as the map holds it, as emitMapMethodSet
		} else {
			v, err = e.emitExpr(a)
		}
		if err != nil {
			return Value{}, err
		}
		if v.Ty.IR == "" || v.Ty.IR == "void" {
			v = Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
		}
		slot, ir := e.freshReg(), mapViewIR(v.Ty)
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, ir))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, v.Ref, slot))
		name := fmt.Sprintf("__kml_mview_arg%d_%d", e.regCtr, i)
		e.define(name, Symbol{Ptr: slot, Ty: v.Ty})
		temps[i] = ast.NewIdentifier(name, ast.Pos{})
		vals[i] = v
	}
	isOwn := e.mapViewIsOwn(view, ptr)
	ownL, otherL, joinL := e.freshLabel("mview.own"), e.freshLabel("mview.other"), e.freshLabel("mview.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOwn, ownL, otherL))
	e.emitLabel(otherL)
	boxed := make([]string, len(vals))
	for i, v := range vals {
		b, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		boxed[i] = b.Ref
	}
	otherBody := e.freshLabel("mview.call")
	e.emitTerminator(fmt.Sprintf("br label %%%s", otherBody))
	e.emitLabel(ownL)
	ov, err := own(temps)
	if err != nil {
		return Value{}, err
	}
	ownEnd := e.freshLabel("mview.ownend")
	e.emitTerminator(fmt.Sprintf("br label %%%s", ownEnd))
	e.emitLabel(ownEnd)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(otherBody)
	argList := []string{"ptr " + ptr}
	for i := 0; i < 2; i++ {
		a := fmt.Sprint(nbUndefined)
		if i < len(boxed) {
			a = boxed[i]
		}
		argList = append(argList, "i64 "+a)
	}
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(%s)", raw, e.mapViewFn(op, view), strings.Join(argList, ", ")))
	var other Value
	switch {
	case op == "set" || op == "add":
		other = Value{Ref: ptr, Ty: ov.Ty}
	case ov.Ty.IR == "" || ov.Ty.IR == "void":
		other = ov
	default:
		other = e.convert(Value{Ref: raw, Ty: TypeAny}, ov.Ty)
	}
	otherEnd := e.freshLabel("mview.otherend")
	e.emitTerminator(fmt.Sprintf("br label %%%s", otherEnd))
	e.emitLabel(otherEnd)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	if ov.Ty.IR == "" || ov.Ty.IR == "void" {
		return ov, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi %s [ %s, %%%s ], [ %s, %%%s ]", r, mapViewIR(ov.Ty), ov.Ref, ownEnd, other.Ref, otherEnd))
	return Value{Ref: r, Ty: ov.Ty}, nil
}

// emitMapViewFinalize generates the dispatchers registered by view sites:
// each switches on the receiver's header word over every Map or Set layout
// the program creates (of the view's kind), running that layout's own
// operation. Generating one can create another layout or dispatcher, so it
// runs to a fixed point; a later finalize step can request another, so it
// runs again after those, generating only what is new.
func (e *Emitter) emitMapViewFinalize() {
	if !e.usedMapView {
		return
	}
	if e.mapViewDone == nil {
		e.mapViewDone = map[string]bool{}
	}
	for {
		var pending []mapViewReq
		for k, r := range e.mapViewFns {
			if !e.mapViewDone[k] {
				e.mapViewDone[k] = true
				pending = append(pending, r)
			}
		}
		if len(pending) == 0 {
			return
		}
		sort.Slice(pending, func(i, j int) bool { return pending[i].name < pending[j].name })
		for _, r := range pending {
			e.emitMapViewDispatcher(r)
		}
	}
}

// emitMapViewDispatcher generates r's dispatcher: `i64 op(ptr m, i64 a0, i64
// a1)`, or for "copy" `ptr copy(ptr m)`.
func (e *Emitter) emitMapViewDispatcher(r mapViewReq) {
	var cases []hostLayout
	viewID := e.hostHeaderWord(r.view)
	for _, h := range e.mapLayouts {
		if h.id != viewID && h.ty.IsMap() == r.view.IsMap() && h.ty.IsSet() == r.view.IsSet() {
			cases = append(cases, h)
		}
	}
	restore := e.beginDetachedFunc()
	// A library type's dispatcher sits in its shared unit: built the same
	// whatever classes the program declares.
	endStable := e.stableHelperBody(viewID)
	defer endStable()
	retIR := "i64"
	if r.op == "copy" {
		retIR = "ptr"
	}
	hp, hdr := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %%m, i64 -8", hp))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, hp))
	hdr = e.headerIdentity(hdr)
	for _, h := range cases {
		hit, next := e.freshLabel("mview.case"), e.freshLabel("mview.next")
		c := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", c, hdr, h.id))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", c, hit, next))
		e.emitLabel(hit)
		e.pushScope()
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %%m, ptr %s, align 8", slot))
		e.define("__kml_mview_self", Symbol{Ptr: slot, Ty: h.ty})
		self := ast.NewIdentifier("__kml_mview_self", ast.Pos{})
		ret := e.mapViewCase(r, h, self)
		e.popScope()
		if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret %s %s", retIR, ret))
		}
		e.emitLabel(next)
	}
	// An unknown layout: a copy reads as itself, a foreign test says no,
	// an operation reads undefined.
	fallback := map[string]string{"copy": "ret ptr %m", "foreign": "ret i64 0"}[r.op]
	if fallback == "" {
		fallback = fmt.Sprintf("ret i64 %d", nbUndefined)
	}
	e.emitTerminator(fallback)
	body := e.allocas.String() + e.body.String()
	restore()
	header := fmt.Sprintf("internal i64 %s(ptr %%m, i64 %%a0, i64 %%a1)", r.name)
	if r.op == "copy" {
		header = fmt.Sprintf("internal ptr %s(ptr %%m)", r.name)
	}
	e.writeFunc(header, body)
}

// mapViewCase emits r's operation on self, of layout h, and returns the
// value the dispatcher returns.
func (e *Emitter) mapViewCase(r mapViewReq, h hostLayout, self *ast.Identifier) string {
	undef := fmt.Sprint(nbUndefined)
	if r.op == "foreign" {
		return "1"
	}
	if r.op == "copy" {
		return e.mapViewCopy(r.view, h, self)
	}
	var args []ast.Expression
	for i := 0; i < mapViewOps[r.op]; i++ {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %%a%d, ptr %s, align 8", i, slot))
		name := fmt.Sprintf("__kml_mview_a%d", i)
		e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
		args = append(args, ast.NewIdentifier(name, ast.Pos{}))
	}
	v, err := e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(self, r.op, ast.Pos{}), args, ast.Pos{}))
	if err != nil || e.blockDone || v.Ty.IR == "" || v.Ty.IR == "void" || r.op == "set" || r.op == "add" {
		return undef
	}
	b, err := e.emitBoxValue(v)
	if err != nil {
		return undef
	}
	return b.Ref
}

// mapViewCopy copies self (layout h) into a fresh map of view's layout:
// `for (const [k, v] of self) dst.set(k, v)` (a Set: `dst.add(x)`).
func (e *Emitter) mapViewCopy(view Type, h hostLayout, self *ast.Identifier) string {
	dst := e.emitMapOrSetCreate(view)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dst, slot))
	e.define("__kml_mview_dst", Symbol{Ptr: slot, Ty: view})
	d := ast.NewIdentifier("__kml_mview_dst", ast.Pos{})
	var stmt ast.Statement
	if view.IsSet() {
		bx := ast.NewVarDeclaration("const", "__kml_mview_bx", anyAnnot(), ast.NewIdentifier("__kml_mview_x", ast.Pos{}), ast.Pos{})
		body := ast.NewExpressionStatement(ast.NewCallExpression(ast.NewMemberExpression(d, "add", ast.Pos{}), []ast.Expression{ast.NewIdentifier("__kml_mview_bx", ast.Pos{})}, ast.Pos{}), ast.Pos{})
		stmt = ast.NewForOfStatement("const", "__kml_mview_x", self, ast.NewBlockStatement([]ast.Statement{bx, body}, ast.Pos{}), ast.Pos{})
	} else {
		ent := ast.NewIdentifier("__kml_mview_e", ast.Pos{})
		k := ast.NewIndexExpression(ent, ast.NewNumberLiteral("0", ast.Pos{}), ast.Pos{})
		v := ast.NewIndexExpression(ent, ast.NewNumberLiteral("1", ast.Pos{}), ast.Pos{})
		bk := ast.NewVarDeclaration("const", "__kml_mview_bk", anyAnnot(), k, ast.Pos{})
		bv := ast.NewVarDeclaration("const", "__kml_mview_bv", anyAnnot(), v, ast.Pos{})
		body := ast.NewExpressionStatement(ast.NewCallExpression(ast.NewMemberExpression(d, "set", ast.Pos{}), []ast.Expression{ast.NewIdentifier("__kml_mview_bk", ast.Pos{}), ast.NewIdentifier("__kml_mview_bv", ast.Pos{})}, ast.Pos{}), ast.Pos{})
		stmt = ast.NewForOfStatement("const", "__kml_mview_e", self, ast.NewBlockStatement([]ast.Statement{bk, bv, body}, ast.Pos{}), ast.Pos{})
	}
	if err := e.emitStmt(stmt); err != nil {
		return dst
	}
	return dst
}

// anyAnnot is the `any` annotation a generated declaration boxes through.
func anyAnnot() *ast.TypeAnnotation { return &ast.TypeAnnotation{Name: "any", Source: "ts"} }

// mapViewIR is the register type of a value of type t.
func mapViewIR(t Type) string {
	if isNullableScalar(t) {
		return nullableScalarStorageIR(t)
	}
	return t.IR
}

package llvm

// emit_host_shape.go — reading, writing and calling a host value's members
// through `any` (TDD-00230 P3.3). A host box (emit_hostbox.go) carries a
// handle of a builtin class — a Map, a Set, a RegExp, a Blob, … — whose
// members the builtin declarations list (lib/es.d.ts, lib/node.d.ts). Its
// shape row is generated from that declaration: each property read is the
// typed read on the handle, boxed; each method is a dynamic-function record
// whose adapter unboxes the receiver and the arguments and makes the typed
// call. Code generation's own emitters do the work, so a member behaves
// through `any` exactly as it does on a typed value.

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/checker"
)

// hostMember is one member of a host class's declaration.
type hostMember struct {
	name     string
	method   bool
	readonly bool
	sig      *checker.Type // a method's signature (the longest overload)
	// paramTypeNames are the declared parameters' type names (`K` of
	// `get(key: K)`), for the type parameters the concrete host type binds.
	paramTypeNames []string
}

// hostMembers lists the members the declaration of class declares.
func (e *Emitter) hostMembers(class string) []hostMember {
	c := e.front()
	if c == nil {
		return nil
	}
	t := c.GlobalInterface(class)
	if t == nil {
		return nil
	}
	var out []hostMember
	for _, p := range t.Props {
		if p.Name == "" || strings.HasPrefix(p.Name, "__") || strings.ContainsAny(p.Name, "[]@ ") {
			continue
		}
		m := hostMember{name: p.Name, readonly: p.Readonly}
		ft := p.Type
		if ft != nil && ft.Flags&checker.Object != 0 && ft.Kind == checker.Function && !p.Accessor {
			m.method = true
			m.sig = ft
			for _, o := range ft.Overloads {
				if len(o.Params) > len(m.sig.Params) || len(m.sig.Overloads) > 0 {
					m.sig = o
				}
			}
		}
		if m.method {
			m.paramTypeNames = hostParamTypeNames(c, class, p.Name, len(m.sig.Params))
		}
		out = append(out, m)
	}
	return out
}

// hostParamTypeNames reads the type names of a method's parameters off the
// declaration of class (the signature with n parameters).
func hostParamTypeNames(c *checker.Checker, class, method string, n int) []string {
	g := c.Binding().Globals
	if g == nil {
		return nil
	}
	sym := g.Symbols.Get(class)
	if sym == nil {
		return nil
	}
	for _, d := range sym.Declarations {
		id, ok := d.Node.(*ast.InterfaceDeclaration)
		if !ok {
			continue
		}
		for _, mem := range id.Members {
			ms, ok := mem.(*ast.MethodSignature)
			if !ok || ms.Name != method || len(ms.Parameters) != n {
				continue
			}
			names := make([]string, n)
			for i, p := range ms.Parameters {
				if tr, ok := p.Type.(*ast.TypeReference); ok && len(tr.TypeArgs) == 0 {
					names[i] = tr.Name
				}
			}
			return names
		}
	}
	return nil
}

// hostParamType is the representation an adapter unboxes a declared
// parameter to: its scalar or string, else `any` (the typed call converts).
func hostParamType(pt *checker.Type) Type {
	if r, ok := lowerRepr(pt); ok && !r.IsArray && !r.IsObject && r.IR != "void" {
		return r
	}
	return TypeAny
}

// emitHostShapeRow generates host layout h's get and set routines from its
// class's declaration and returns its table row prefixed with "id|".
func (e *Emitter) emitHostShapeRow(h hostLayout) string {
	id := h.id & kmlHdrIDMask
	members := e.hostMembers(h.class)
	getName := fmt.Sprintf("@__kml_shape_get_host_%d", id)
	setName := fmt.Sprintf("@__kml_shape_set_host_%d", id)
	e.emitHostShapeGetter(getName, h, members)
	e.emitHostShapeSetter(setName, h, members)
	return fmt.Sprintf("%d|{ i64, ptr, ptr, ptr, i64, ptr } { i64 %d, ptr %s, ptr %s, ptr %s, i64 0, ptr null }", id, id, e.internString(h.class), getName, setName)
}

// bindHostSelf loads the handle out of the host cell at cellReg and binds it
// as `__kml_host_self`, of the host type.
func (e *Emitter) bindHostSelf(cellReg string, h hostLayout) *ast.Identifier {
	handle := e.emitHostCellLoad(cellReg, h.ty)
	hir := hostHandleIR(h.ty)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, hir))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", hir, handle, slot))
	e.define("__kml_host_self", Symbol{Ptr: slot, Ty: h.ty})
	return ast.NewIdentifier("__kml_host_self", ast.Pos{})
}

// emitHostShapeGetter generates `i64 get(ptr cell, ptr key, ptr found)`.
func (e *Emitter) emitHostShapeGetter(name string, h hostLayout, members []hostMember) {
	restore := e.beginDetachedFunc()
	self := e.bindHostSelf("%obj", h)
	for _, m := range members {
		miss := e.emitKeyIs("%key", m.name)
		if m.method {
			rec := e.hostMethodRecord(h, m)
			e.emitInstr(fmt.Sprintf("store i32 %d, ptr %%found, align 4", shapeMember))
			e.emitTerminator(fmt.Sprintf("ret i64 %s", e.emitNbTagPtr(rec, kmlTagDynFunc)))
		} else {
			v, err := e.emitExpr(ast.NewMemberExpression(self, m.name, ast.Pos{}))
			var bv Value
			if err == nil && v.Ty.IR != "" && v.Ty.IR != "void" {
				bv, err = e.emitBoxValue(v)
			}
			if err != nil || bv.Ref == "" {
				if !e.blockDone {
					e.emitThrowTypeError("the property '" + m.name + "' can't be read through a dynamic value")
				}
			} else {
				e.emitInstr(fmt.Sprintf("store i32 %d, ptr %%found, align 4", shapeMember))
				e.emitTerminator(fmt.Sprintf("ret i64 %s", bv.Ref))
			}
		}
		e.emitLabel(miss)
	}
	e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal i64 %s(ptr %%obj, ptr %%key, ptr %%found) {\nentry:\n%s}\n", name, body))
}

// emitHostShapeSetter generates `i32 set(ptr cell, ptr key, i64 value)`: a
// writable property is assigned through the typed path; anything else is
// left to the caller (an own property of the box).
func (e *Emitter) emitHostShapeSetter(name string, h hostLayout, members []hostMember) {
	restore := e.beginDetachedFunc()
	self := e.bindHostSelf("%obj", h)
	vslot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", vslot))
	e.emitInstr(fmt.Sprintf("store i64 %%value, ptr %s, align 8", vslot))
	e.define("__kml_host_value", Symbol{Ptr: vslot, Ty: TypeAny})
	for _, m := range members {
		if m.method || m.readonly {
			continue
		}
		miss := e.emitKeyIs("%key", m.name)
		assign := ast.NewAssignmentExpression("=", ast.NewMemberExpression(self, m.name, ast.Pos{}), ast.NewIdentifier("__kml_host_value", ast.Pos{}), ast.Pos{})
		if _, err := e.emitExpr(assign); err != nil {
			if !e.blockDone {
				e.emitTerminator("ret i32 -2")
			}
		} else {
			e.emitTerminator("ret i32 1")
		}
		e.emitLabel(miss)
	}
	e.emitTerminator("ret i32 0")
	body := e.allocas.String() + e.body.String()
	restore()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal i32 %s(ptr %%obj, ptr %%key, i64 %%value) {\nentry:\n%s}\n", name, body))
}

// hostMethodRecord returns the dynamic-function record for method m of host
// layout h — one per method, so `a.m === b.m` holds.
func (e *Emitter) hostMethodRecord(h hostLayout, m hostMember) string {
	id := h.id & kmlHdrIDMask
	key := fmt.Sprintf("host%d.%s", id, m.name)
	if e.shapeMethodRecs == nil {
		e.shapeMethodRecs = map[string]string{}
	}
	if rec, ok := e.shapeMethodRecs[key]; ok {
		return rec
	}
	base := fmt.Sprintf("__kml_host_m_%d_%s", id, llvmSafeSymbol(m.name))
	fn := "@" + base
	rec := "@" + base + "_rec"
	e.shapeMethodRecs[key] = rec
	arity := len(m.sig.Params)
	if m.sig.HasRestParam() {
		arity--
	}
	e.registerFnMeta(fn, m.name, arity, fnKindPlain)
	e.emitGlobal(fmt.Sprintf("%s = internal constant { ptr, ptr, i64 } { ptr %s, ptr null, i64 %d }", rec, fn, arity))
	e.emitHostMethodAdapter(fn, h, m)
	return rec
}

// emitHostMethodAdapter generates `i64 fn(ptr env, i64 this, i64 argc, ptr
// argv)`: the receiver must be a host box of this layout (else a
// TypeError, as calling a method on an incompatible receiver is); the
// arguments are unboxed to their declared scalars (or kept boxed) and the
// typed method is called with as many as were passed.
func (e *Emitter) emitHostMethodAdapter(fn string, h hostLayout, m hostMember) {
	restore := e.beginDetachedFunc()
	e.ensureNanBox()
	tag, pay := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %%this)", tag))
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %%this)", pay))
	isObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
	checkL, badL, okL := e.freshLabel("hostm.check"), e.freshLabel("hostm.bad"), e.freshLabel("hostm.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, checkL, badL))
	e.emitLabel(checkL)
	cell := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", cell, pay))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, cell))
	hdrL := e.freshLabel("hostm.hdr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, badL, hdrL))
	e.emitLabel(hdrL)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, cell))
	same := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", same, hdr, h.id))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", same, okL, badL))
	e.emitLabel(badL)
	e.emitThrowTypeError("Method " + h.class + ".prototype." + m.name + " called on incompatible receiver")
	e.emitLabel(okL)
	self := e.bindHostSelf(cell, h)

	n := len(m.sig.Params)
	if m.sig.HasRestParam() {
		n--
	}
	var args []ast.Expression
	for i := 0; i < n; i++ {
		pty := hostParamType(m.sig.Params[i])
		if i < len(m.paramTypeNames) && m.paramTypeNames[i] != "" {
			if ta := hostTypeArg(h.ty, m.paramTypeNames[i]); !ta.IsDynamic {
				pty = ta
			}
		}
		if h.ty.Weak && i == 0 {
			// A weak collection's key stays boxed: the typed call tests it
			// is an object at run time.
			pty = TypeAny
		}
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%argc, %d", have, i))
		slotp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%argv, i64 %d", slotp, i))
		loaded := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loaded, slotp))
		word := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", word, have, loaded, nbUndefined))
		av := Value{Ref: word, Ty: TypeAny}
		if !pty.IsDynamic {
			av = e.unboxArgToParam(word, pty)
		}
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, storageIR(pty)))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", storageIR(pty), av.Ref, slot))
		name := fmt.Sprintf("__kml_host_a%d", i)
		e.define(name, Symbol{Ptr: slot, Ty: pty})
		args = append(args, ast.NewIdentifier(name, ast.Pos{}))
	}
	minArgs := n
	for minArgs > 0 && m.sig.OptionalParam(minArgs-1) {
		minArgs--
	}
	call := func(k int) {
		ce := ast.NewCallExpression(ast.NewMemberExpression(self, m.name, ast.Pos{}), append([]ast.Expression(nil), args[:k]...), ast.Pos{})
		// The synthesized call has no declaration for the checker to
		// resolve: an intrinsic method goes straight to its emitter.
		var v Value
		var err error
		if in, ok := intrinsics[h.class+".prototype."+m.name]; ok && e.intrinsicApplies(h.class+".prototype."+m.name, ce.Callee.(*ast.MemberExpression)) {
			v, err = in.emit(e, ce)
		} else {
			v, err = e.emitExpr(ce)
		}
		if err != nil {
			if !e.blockDone {
				e.emitThrowTypeError("the method '" + m.name + "' can't be called through a dynamic value")
			}
			return
		}
		if e.blockDone {
			return
		}
		if v.Ty.IR == "" || v.Ty.IR == "void" {
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
			return
		}
		bv, err := e.emitBoxValue(v)
		if err != nil {
			e.emitThrowTypeError("the result of '" + m.name + "' can't be held in a dynamic value")
			return
		}
		e.emitTerminator(fmt.Sprintf("ret i64 %s", bv.Ref))
	}
	for k := minArgs; k < n; k++ {
		lt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sle i64 %%argc, %d", lt, k))
		here, next := e.freshLabel("hostm.argc"), e.freshLabel("hostm.argc.more")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", lt, here, next))
		e.emitLabel(here)
		call(k)
		e.emitLabel(next)
	}
	call(n)
	e.shapeFinishAdapter(fn, restore)
}

// hostTypeArg is the representation of a collection's type parameter in the
// concrete host type: a Map's K and V, a Set's T; `any` otherwise.
func hostTypeArg(t Type, name string) Type {
	switch {
	case (t.IsMap || t.IsSet) && (name == "K" || name == "T") && t.MapKey != nil:
		return *t.MapKey
	case t.IsMap && name == "V" && t.MapVal != nil:
		return *t.MapVal
	}
	return TypeAny
}

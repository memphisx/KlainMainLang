package llvm

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
	"KlainMainLang/checker"
)

// A value of another kind written through `any` into an object's field
// shadows the field (ADR-01215). A static read of a field whose name some
// write through `any` names reads that shadow when the object has one, as a
// box: the value the field holds in JavaScript.

// shadowWrittenKeys is the property names a write may store a value of
// another kind under: through `any` (`a.x = v`, `a["x"] = v`, `a.x += v`,
// `a.x++`), or through a structural type whose member is a union or `any`
// (`v.x = "s"` with `v: { x: number | string }`).
func (e *Emitter) shadowWrittenKeys() map[string]bool {
	if e.shadowKeys != nil {
		return e.shadowKeys
	}
	e.shadowKeys = map[string]bool{}
	o := e.reprOracle()
	if o == nil || e.prog == nil {
		return e.shadowKeys
	}
	note := func(target ast.Expression) {
		var obj ast.Expression
		var key string
		switch m := target.(type) {
		case *ast.MemberExpression:
			obj, key = m.Object, m.Property
		case *ast.IndexExpression:
			lit, ok := m.Index.(*ast.StringLiteral)
			if !ok {
				return
			}
			obj, key = m.Object, lit.Value
		default:
			return
		}
		if strings.HasPrefix(key, "#") {
			return // a private name has no dynamic access
		}
		if t := o.c.TypeOf(obj); !o.c.Unanswered(t) && t.Flags&checker.Any != 0 {
			e.shadowKeys[key] = true
			return
		}
		// A structural view (an interface or type literal, never a class's
		// own instance type, whose layout holds what it declares) whose
		// member is a union or any.
		rt := o.c.TypeOf(obj)
		if o.c.Unanswered(rt) || rt.Flags&checker.Object == 0 || (rt.Kind != checker.Interface && rt.Kind != checker.Anonymous) {
			return
		}
		if t := o.c.TypeOf(target); !o.c.Unanswered(t) && t.Flags&(checker.Any|checker.Union) != 0 {
			e.shadowKeys[key] = true
		}
	}
	ast.Inspect(e.prog, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignmentExpression:
			note(x.Left)
		case *ast.UpdateExpression:
			note(x.Arg)
		}
		return true
	})
	return e.shadowKeys
}

// shadowableRead reports a static read the shadow check covers: a field of
// a primitive kind, named by a write through `any`, off a reference whose
// second evaluation reads the same object and has no effect.
// Library code checks every such field: any program may write a library
// object's field through `any`, and the library compiles the same for all.
func (e *Emitter) shadowableRead(m *ast.MemberExpression) bool {
	if m.Optional || strings.HasPrefix(m.Property, "#") || (e.inLib == "" && !e.shadowWrittenKeys()[m.Property]) {
		return false
	}
	t := e.inferExprType(m.Object)
	if !t.IsObject() || t.IsDynamic() || t.IsArray() || t.IR != "ptr" {
		return false
	}
	_, ft, ok := t.FieldIndex(m.Property)
	// A host handle (a Promise, a stream, …) is a pointer but no primitive.
	return ok && anyArgScalarFits(ft) && (!isStringTy(ft) || isPlainStringTy(ft))
}

// pureRef reports an expression whose evaluation has no effect, read again
// as it is: a binding, `this`, or a property chain of one.
func pureRef(x ast.Expression) bool {
	switch v := x.(type) {
	case *ast.Identifier, *ast.ThisExpression:
		return true
	case *ast.MemberExpression:
		return !v.Optional && pureRef(v.Object)
	}
	return false
}

// emitShadowableRead is the read shadowableRead covers: the field, boxed,
// or the object's shadow of it once a shadow exists.
func (e *Emitter) emitShadowableRead(m *ast.MemberExpression) (Value, error) {
	if !pureRef(m.Object) {
		// Any other receiver is evaluated once, into a binding read twice.
		ov, err := e.emitExpr(m.Object)
		if err != nil {
			return Value{}, err
		}
		name := fmt.Sprintf("__kml_shadow_recv_%s", e.freshReg()[1:])
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, ov.Ty.IR))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ov.Ty.IR, ov.Ref, slot))
		e.define(name, Symbol{Ptr: slot, Ty: ov.Ty})
		m = ast.NewMemberExpression(ast.NewIdentifier(name, m.GetPos()), m.Property, m.GetPos())
	}
	e.noShadowRead = true
	v, err := e.emitMember(m)
	e.noShadowRead = false
	if err != nil {
		return Value{}, err
	}
	box, err := e.emitBoxValue(v)
	if err != nil {
		return Value{}, err
	}
	obj, err := e.emitExpr(m.Object)
	if err != nil {
		return Value{}, err
	}
	e.declareFn("__kml_shadow_get_hook", "@__kml_shadow_get_hook = weak global ptr null, align 8")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", box.Ref, slot))
	fp, has := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr @__kml_shadow_get_hook, align 8", fp))
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, fp))
	callL, joinL := e.freshLabel("shadowread"), e.freshLabel("shadowread.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, callL, joinL))
	e.emitLabel(callL)
	e.emitInstr(fmt.Sprintf("call i32 %s(ptr %s, ptr %s, ptr %s)", fp, obj.Ref, e.internString(m.Property), slot))
	e.emitTerminator("br label %" + joinL)
	e.emitLabel(joinL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// boxOverride is the type codegen holds an expression in where the checker
// types it otherwise: `any` for one exprHoldsBox reports, and `any[]` for
// an array literal holding such an element (each element kept as it is).
func (e *Emitter) boxOverride(x ast.Expression) (Type, bool) {
	if e.boxedCapReads[x] || e.exprHoldsBox(x) {
		return TypeAny, true
	}
	// A function-seeded var (seedFuncGlobals), and a call of it: the box.
	if c, ok := x.(*ast.CallExpression); ok {
		x = c.Callee
		if id, isID := x.(*ast.Identifier); isID && e.funcSeededRead(id) {
			return TypeAny, true
		}
	} else if id, isID := x.(*ast.Identifier); isID && e.funcSeededRead(id) {
		return TypeAny, true
	}
	if lit, ok := x.(*ast.ArrayLiteral); ok {
		for _, el := range lit.Elements {
			if sp, isSpread := el.(*ast.SpreadElement); isSpread {
				el = sp.Arg
			}
			if el != nil && e.exprHoldsBox(el) {
				return ArrayOf(TypeAny), true
			}
		}
	}
	return Type{}, false
}

// exprHoldsBox reports an expression codegen holds as a box where the
// checker types it otherwise: a call the any-argument check boxes, or a
// shadowable field read.
func (e *Emitter) exprHoldsBox(x ast.Expression) bool {
	switch v := x.(type) {
	case *ast.CallExpression:
		return e.anyArgReturnsBox(v)
	case *ast.MemberExpression:
		return e.shadowableRead(v)
	}
	return false
}

// funcSeededRead reports a read of a module var that shares a function
// declaration's name (seedFuncGlobals), not shadowed by a local.
func (e *Emitter) funcSeededRead(id *ast.Identifier) bool {
	if !e.funcSeeded[id.Name] {
		return false
	}
	g := e.moduleGlobals[id.Name]
	sym, ok := e.lookup(id.Name)
	return ok && sym.Ptr == g.Ptr
}

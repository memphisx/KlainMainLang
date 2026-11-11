package llvm

// emit_privtab.go — private names on objects that are not instances of the
// declaring class's layout. A derived class whose base constructor may
// return another object (`constructor(o) { return o; }`) has that object
// for `this` (emitOverrideThis): its private elements are added to it, and
// its methods may run on it (emitForeignReceiverCall). An instance of the
// class's layout keeps them in its own fields; any other object keeps them
// in a side table per private name, keyed weakly by the object.

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// overridePrivClass is the class whose private names the code being
// emitted may reach on another object: the constructor's after its
// super() bound `this` to what the base returned, or a method's compiled
// for a receiver of another layout. "" elsewhere.
func (e *Emitter) overridePrivClass() string {
	if e.foreignThisBody && e.foreignClass != nil && e.overrideInstances(e.foreignClass.Ty.ClassName) {
		return e.foreignClass.Ty.ClassName
	}
	if e.overrideThis != "" && e.currentCtorClass != "" {
		return e.currentCtorClass
	}
	if e.memberClass != "" && e.overrideInstances(e.memberClass) {
		return e.memberClass // a receiver held in an any (`static val(o)`)
	}
	return ""
}

// overrideInstances reports a class whose `this` may be another object
// than an instance of its layout: its base's constructor may return one.
func (e *Emitter) overrideInstances(cls string) bool {
	info, ok := e.classes[cls]
	return ok && e.compatJS() && info.BaseClass != "" && e.ctorMayOverride(info.BaseClass)
}

// privateKind is what cls declares private name p as: a field, a method or
// accessor (its brand), or nothing.
func (e *Emitter) privateKind(cls, p string) (field, member bool) {
	info := e.classes[cls]
	if _, _, ok := info.Ty.FieldIndex(p); ok {
		return true, false
	}
	_, m := info.MethodSigs[p]
	_, g := info.MethodSigs[accessorMethodName("get", p)]
	_, s := info.MethodSigs[accessorMethodName("set", p)]
	return false, m || g || s
}

// ptabHandle is the side table of private key (class and name).
func (e *Emitter) ptabHandle(key string) string {
	g := "__kml_ptab." + llvmSafeSymbol(key)
	e.declareFn(g, fmt.Sprintf("@%s = internal"+irGlobalAS+" global "+irPtr+" null, align 8", g))
	h := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_ptab("+irPtr+" @%s)", h, g))
	return h
}

// ptabObj is the object an any holds, as a side table's key.
func (e *Emitter) ptabObj(o Value) string {
	e.ensureNanBox()
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, o.Ref))
	return e.emitIntToPtr(pay)
}

// ptabHas is whether object o (an any) has private key.
func (e *Emitter) ptabHas(o Value, key string) string {
	h := e.ptabHandle(key)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_weak_has("+irPtr+" %s, "+irPtr+" %s)", r, h, e.ptabObj(o)))
	return r
}

// emitPtabGet reads private key of o, throwing msg when o has none.
func (e *Emitter) emitPtabGet(o Value, key, msg string) Value {
	has := e.ptabHas(o, key)
	okL, badL := e.freshLabel("ptab.has"), e.freshLabel("ptab.none")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, okL, badL))
	e.emitLabel(badL)
	e.emitThrowTypeError(msg)
	e.emitLabel(okL)
	h := e.ptabHandle(key)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_weak_get("+irPtr+" %s, "+irPtr+" %s)", r, h, e.ptabObj(o)))
	return Value{Ref: r, Ty: TypeAny}
}

// emitPtabPut stores v (an any) as private key of o: a definition throws
// msg when o has the name already, a write when it has not.
func (e *Emitter) emitPtabPut(o Value, key string, v Value, msg string, define bool) {
	has := e.ptabHas(o, key)
	okL, badL := e.freshLabel("ptab.put"), e.freshLabel("ptab.bad")
	if define {
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, badL, okL))
	} else {
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, okL, badL))
	}
	e.emitLabel(badL)
	e.emitThrowTypeError(msg)
	e.emitLabel(okL)
	h := e.ptabHandle(key)
	e.emitInstr(fmt.Sprintf("call void @__kml_weak_set("+irPtr+" %s, "+irPtr+" %s, i64 %s)", h, e.ptabObj(o), v.Ref))
}

// overrideBranch emits the two arms of a private operation on o (an any):
// own runs on o as an instance of cls's layout, other on any other object;
// each yields an any (or nothing). The result is the arm's value.
func (e *Emitter) overrideBranch(o Value, cls string, own, other func() (Value, error)) (Value, error) {
	isK, err := e.emitAnyInstanceOfClass(o, cls)
	if err != nil {
		return Value{}, err
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", nbUndefined, slot))
	ownL, otherL, endL := e.freshLabel("ovpriv.own"), e.freshLabel("ovpriv.other"), e.freshLabel("ovpriv.end")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isK.Ref, ownL, otherL))
	for _, arm := range []struct {
		l string
		f func() (Value, error)
	}{{ownL, own}, {otherL, other}} {
		e.emitLabel(arm.l)
		v, err := arm.f()
		if err != nil {
			return Value{}, err
		}
		if !e.blockDone {
			if v.Ty.IR != "" && v.Ty.IR != "void" {
				b, err := e.emitBoxValue(v)
				if err != nil {
					return Value{}, err
				}
				e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", b.Ref, slot))
			}
			e.emitTerminator("br label %" + endL)
		}
	}
	e.emitLabel(endL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// asClass is bound any o read as an instance of cls.
func (e *Emitter) asClass(o Value, cls string, pos ast.Pos) ast.Expression {
	return ast.NewAsExpression(e.bindValue(o, pos), e.classAnnotation(cls), pos)
}

// emitPrivateDefine defines private p of cls on o (an any) with value v (an
// any, or nil for the brand of its methods): the instance being built keeps
// it in its field; another instance of the layout, or another object that
// has it, throws.
func (e *Emitter) emitPrivateDefine(o Value, cls, p string, v *Value, pos ast.Pos) error {
	msg := "Cannot initialize " + ast.PrivateDisplay(p) + " twice on the same object"
	key := cls + p
	if v == nil {
		msg = "Cannot initialize private methods of class " + e.classDisplayName(cls) + " twice on the same object"
		key = cls + "#brand"
	}
	alloc := e.overrideThisStatic
	_, err := e.overrideBranch(o, cls, func() (Value, error) {
		// The instance allocated for this construction, else another one.
		same := "false"
		if alloc != nil {
			tr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", tr, alloc.Ptr))
			op := e.ptabObj(o)
			same = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, %s", same, op, tr))
		}
		okL, badL := e.freshLabel("ovdef.self"), e.freshLabel("ovdef.twice")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", same, okL, badL))
		e.emitLabel(badL)
		e.emitThrowTypeError(msg)
		e.emitLabel(okL)
		if v == nil {
			return Value{}, nil
		}
		return e.emitExpr(ast.NewAssignmentExpression("=", ast.NewMemberExpression(e.asClass(o, cls, pos), p, pos), e.bindValue(*v, pos), pos))
	}, func() (Value, error) {
		val := Value{Ref: "1", Ty: TypeAny}
		if v != nil {
			val = *v
		} else {
			t, err := e.emitBoxValue(Value{Ref: "true", Ty: TypeBool})
			if err != nil {
				return Value{}, err
			}
			val = t
		}
		e.emitPtabPut(o, key, val, msg, true)
		return Value{}, nil
	})
	return err
}

// emitOverrideDefinitions, right after super() bound `this` (o) for cls,
// adds the brand of its private methods and its uninitialized private
// fields (undefined); the initialized ones are their initializer's
// assignments, which follow (overrideFieldDef).
func (e *Emitter) emitOverrideDefinitions(o Value, cls string, pos ast.Pos) error {
	decl := e.programClassDecl(cls)
	if !e.overrideInstances(cls) || decl == nil {
		return nil
	}
	brand := false
	for _, m := range decl.Methods {
		if !m.IsStatic && strings.HasPrefix(privateNameOf(m.Name), "#") {
			brand = true
		}
	}
	if brand {
		if err := e.emitPrivateDefine(o, cls, "", nil, pos); err != nil {
			return err
		}
	}
	for _, f := range decl.Fields {
		if !f.Static && f.Initializer == nil && strings.HasPrefix(f.Name, "#") {
			u := Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}
			if err := e.emitPrivateDefine(o, cls, f.Name, &u, pos); err != nil {
				return err
			}
		}
	}
	return nil
}

// overrideFieldDef reports an assignment that is a field initializer of
// cls (`this.#x = init`, spliced after super()): a definition.
func (e *Emitter) overrideFieldDef(cls string, x *ast.AssignmentExpression) bool {
	decl := e.programClassDecl(cls)
	if decl == nil || x.Op != "=" {
		return false
	}
	me, ok := x.Left.(*ast.MemberExpression)
	if !ok {
		return false
	}
	if _, onThis := me.Object.(*ast.ThisExpression); !onThis {
		return false
	}
	for _, f := range decl.Fields {
		if !f.Static && f.Name == me.Property && f.Initializer == x.Right {
			return true
		}
	}
	return false
}

// emitOverridePrivate emits a private access whose receiver is held in an
// any, in a class whose `this` may be another object (overridePrivClass).
func (e *Emitter) emitOverridePrivate(ex ast.Expression) (Value, bool, error) {
	cls := e.overridePrivClass()
	if cls == "" {
		return Value{}, false, nil
	}
	dynRecv := func(x ast.Expression) bool {
		if _, ok := x.(*ast.ThisExpression); ok {
			sym, _ := e.lookup("this")
			return sym.Ty.IsDynamic() // `this` rebound to an any
		}
		return e.typeOfExpr(x).IsDynamic()
	}
	priv := func(x ast.Expression) (*ast.MemberExpression, bool, bool) {
		me, ok := x.(*ast.MemberExpression)
		if !ok || !strings.HasPrefix(me.Property, "#") {
			return nil, false, false
		}
		// A static method's brand check of its receiver (staticPrivateBrand):
		// the side table's lookup is the check here.
		obj := me.Object
		for as, ok := obj.(*ast.AsExpression); ok; as, ok = obj.(*ast.AsExpression) {
			obj = as.Expr
		}
		if c, ok := obj.(*ast.CallExpression); ok && c.Intrinsic == "kml.instanceBrand" && len(c.Args) > 0 {
			if !dynRecv(c.Args[0]) {
				return nil, false, false
			}
			me = ast.NewMemberExpression(c.Args[0], me.Property, me.GetPos())
		} else if !dynRecv(me.Object) {
			return nil, false, false
		}
		field, member := e.privateKind(cls, me.Property)
		if !field && !member {
			return nil, false, false
		}
		return me, field, true
	}
	recv := func(me *ast.MemberExpression) (Value, error) {
		o, err := e.emitExpr(me.Object)
		if err != nil {
			return Value{}, err
		}
		if !o.Ty.IsDynamic() || o.Ty.IR != "i64" {
			return e.emitBoxValue(o)
		}
		return o, nil
	}
	pos := ex.GetPos()
	readMsg := func(p string) string {
		return "Cannot read private member " + ast.PrivateDisplay(p) + " from an object whose class did not declare it"
	}
	switch x := ex.(type) {
	case *ast.MemberExpression:
		me, field, ok := priv(x)
		if !ok {
			return Value{}, false, nil
		}
		getter := accessorMethodName("get", me.Property)
		if !field && e.classes[cls].Methods[getter] == nil {
			return Value{}, false, nil
		}
		o, err := recv(me)
		if err != nil {
			return Value{}, true, err
		}
		v, err := e.overrideBranch(o, cls, func() (Value, error) {
			return e.emitExpr(ast.NewMemberExpression(e.asClass(o, cls, pos), me.Property, pos))
		}, func() (Value, error) {
			if !field {
				// Its private getter, run on another object with the brand.
				e.emitPtabGet(o, cls+"#brand", readMsg(me.Property))
				return e.emitForeignPrivateCall(o, cls, getter, nil, pos)
			}
			return e.emitPtabGet(o, cls+me.Property, readMsg(me.Property)), nil
		})
		return v, true, err
	case *ast.UpdateExpression:
		me, field, ok := priv(x.Arg)
		if !ok || !field {
			return Value{}, false, nil
		}
		// `o.#p++`: the old value as a number, then the store.
		o, err := recv(me)
		if err != nil {
			return Value{}, true, err
		}
		target := func() ast.Expression { return ast.NewMemberExpression(e.bindValue(o, pos), me.Property, pos) }
		old, _, err := e.emitOverridePrivate(target())
		if err != nil {
			return Value{}, true, err
		}
		num, err := e.emitExpr(ast.NewUnaryExpression("+", true, e.bindValue(old, pos), pos))
		if err != nil {
			return Value{}, true, err
		}
		op := map[string]string{"++": "+", "--": "-"}[x.Op]
		next := ast.NewBinaryExpression(op, e.bindValue(num, pos), ast.NewNumberLiteral("1", pos), pos)
		nv, _, err := e.emitOverridePrivate(ast.NewAssignmentExpression("=", target(), next, pos))
		if err != nil || x.Prefix {
			return nv, true, err
		}
		return num, true, nil
	case *ast.AssignmentExpression:
		me, field, ok := priv(x.Left)
		if !ok || !field {
			return Value{}, false, nil
		}
		if op := strings.TrimSuffix(x.Op, "="); x.Op != "=" && op != "&&" && op != "||" && op != "??" {
			// `o.#p op= v`: read, combine, store, the receiver once.
			o, err := recv(me)
			if err != nil {
				return Value{}, true, err
			}
			target := func() ast.Expression { return ast.NewMemberExpression(e.bindValue(o, pos), me.Property, pos) }
			old, _, err := e.emitOverridePrivate(target())
			if err != nil {
				return Value{}, true, err
			}
			next := ast.NewBinaryExpression(op, e.bindValue(old, pos), x.Right, pos)
			v, _, err := e.emitOverridePrivate(ast.NewAssignmentExpression("=", target(), next, pos))
			return v, true, err
		}
		if x.Op != "=" {
			return Value{}, false, nil
		}
		o, err := recv(me)
		if err != nil {
			return Value{}, true, err
		}
		rv, err := e.emitExpr(x.Right)
		if err != nil {
			return Value{}, true, err
		}
		bv, err := e.emitBoxValue(rv)
		if err != nil {
			return Value{}, true, err
		}
		if e.currentCtorClass == cls && e.overrideFieldDef(cls, x) {
			return bv, true, e.emitPrivateDefine(o, cls, me.Property, &bv, pos)
		}
		_, err = e.overrideBranch(o, cls, func() (Value, error) {
			return e.emitExpr(ast.NewAssignmentExpression("=", ast.NewMemberExpression(e.asClass(o, cls, pos), me.Property, pos), e.bindValue(bv, pos), pos))
		}, func() (Value, error) {
			e.emitPtabPut(o, cls+me.Property, bv, "Cannot write private member "+ast.PrivateDisplay(me.Property)+" to an object whose class did not declare it", false)
			return Value{}, nil
		})
		return bv, true, err
	case *ast.CallExpression:
		me, field, ok := priv(x.Callee)
		if !ok || field {
			return Value{}, false, nil
		}
		o, err := recv(me)
		if err != nil {
			return Value{}, true, err
		}
		v, err := e.overrideBranch(o, cls, func() (Value, error) {
			return e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(e.asClass(o, cls, pos), me.Property, pos), x.Args, pos))
		}, func() (Value, error) {
			// Another object with the brand runs the method on it.
			e.emitPtabGet(o, cls+"#brand", "Receiver must be an instance of class "+e.classDisplayName(cls))
			return e.emitForeignPrivateCall(o, cls, me.Property, x.Args, pos)
		})
		return v, true, err
	}
	return Value{}, false, nil
}

// emitForeignPrivateCall runs cls's private method p on o (an any that is
// not an instance of its layout): its body compiled for a receiver held in
// an any, as emitForeignReceiverCall compiles a method's.
func (e *Emitter) emitForeignPrivateCall(o Value, cls, p string, args []ast.Expression, pos ast.Pos) (Value, error) {
	info := e.classes[cls]
	decl := info.Methods[p]
	if decl == nil || decl.Body == nil {
		e.emitThrowTypeError(ast.PrivateDisplay(p) + " is not a function")
		return Value{}, nil
	}
	params := append([]ast.Param{{Name: "this", Type: ast.AnyAnnotation("ts")}}, decl.Params...)
	fe := &ast.FunctionExpression{Params: params, Body: decl.Body, IsAsync: decl.IsAsync}
	fe.Src = decl.Src
	savedT, savedC, savedB := e.foreignThisBody, e.foreignClass, e.foreignBody
	e.foreignThisBody, e.foreignClass, e.foreignBody = true, &info, fe
	fn, err := e.emitFunctionExpression(fe, nil)
	e.foreignThisBody, e.foreignClass, e.foreignBody = savedT, savedC, savedB
	if err != nil {
		return Value{}, err
	}
	callArgs := append([]ast.Expression{e.bindValue(o, pos)}, args...)
	return e.emitClosureCallByPtr(fn.Ref, fn.Ty, callArgs, pos)
}

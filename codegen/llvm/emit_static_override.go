package llvm

// emit_static_override.go — a static method the program replaces at run
// time (`C.m = f`, or a standard decorator returning a new method): its
// reads and calls consult the class object's own properties first, as a
// JavaScript method lookup does. Other static methods stay direct calls.

import (
	"fmt"

	"KlainMainLang/ast"
)

// staticMethodOverridable reports whether the program may replace static
// method m of class className.
func (e *Emitter) staticMethodOverridable(className, m string) bool {
	if e.staticWrites == nil {
		e.staticWrites = map[string]bool{}
		ast.Inspect(e.prog, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignmentExpression:
				if me, ok := x.Left.(*ast.MemberExpression); ok {
					if id, ok := me.Object.(*ast.Identifier); ok {
						e.staticWrites[id.Name+"\x00"+me.Property] = true
					}
				}
			case *ast.UnaryExpression:
				// `delete C.m` removes it, as a write replaces it.
				if me, ok := x.Arg.(*ast.MemberExpression); ok && x.Op == "delete" {
					if id, ok := me.Object.(*ast.Identifier); ok {
						e.staticWrites[id.Name+"\x00"+me.Property] = true
					}
				}
			case *ast.ClassDeclaration:
				for _, fm := range x.Methods {
					if fm.IsStatic && len(fm.Decorators) > 0 && fm.AccessorKind == "" {
						e.staticWrites[x.Name+"\x00"+fm.Name] = true
					}
				}
			}
			return true
		})
	}
	info := e.classes[className]
	return e.staticWrites[className+"\x00"+m] || e.staticWrites[info.StaticMethodImplementor[m]+"\x00"+m]
}

// emitStaticMethodOverrideRead is `C.m` for an overridable static method:
// the class object's own replacement when it has one, else the method.
func (e *Emitter) emitStaticMethodOverrideRead(info ClassInfo, className, m string, pos ast.Pos) (Value, error) {
	sig := info.StaticMethodSigs[m]
	orig, err := e.emitBoxValue(e.emitNamedFuncValue(staticMethodName(info.StaticMethodImplementor[m], m), sig, m))
	if err != nil {
		return Value{}, err
	}
	key := e.internString(m)
	e.declareFn("__kml_classref_extra_has", "declare i1 @__kml_classref_extra_has("+irPtr+", "+irPtr+")")
	e.declareFn("__kml_classref_get", "declare i64 @__kml_classref_get("+irPtr+", "+irPtr+")")
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", orig.Ref, res))
	refs := []string{e.classRefPtr(className)}
	if impl := info.StaticMethodImplementor[m]; impl != "" && impl != className {
		refs = append(refs, e.classRefPtr(impl))
	}
	doneL := e.freshLabel("statov.done")
	for _, ref := range refs {
		has := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_classref_extra_has("+irPtr+" %s, "+irPtr+" %s)", has, ref, key))
		hitL, nextL := e.freshLabel("statov.own"), e.freshLabel("statov.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, hitL, nextL))
		e.emitLabel(hitL)
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_classref_get("+irPtr+" %s, "+irPtr+" %s)", v, ref, key))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", v, res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(nextL)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, res))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// emitStaticMethodOverrideCall is `C.m(args)` for an overridable static
// method: the current method called with C as its receiver, its result
// as the method's declared type.
func (e *Emitter) emitStaticMethodOverrideCall(info ClassInfo, className, m string, args []ast.Expression, pos ast.Pos) (Value, error) {
	fv, err := e.emitStaticMethodOverrideRead(info, className, m, pos)
	if err != nil {
		return Value{}, err
	}
	cv, err := e.emitExpr(ast.NewIdentifier(className, pos))
	if err != nil {
		return Value{}, err
	}
	recv, err := e.emitBoxValue(cv)
	if err != nil {
		return Value{}, err
	}
	argv, n, err := e.emitDynArgv(args, pos)
	if err != nil {
		return Value{}, err
	}
	v, err := e.emitDynFnBoxCallN(fv, recv, argv, n, className+"."+m+" is not a function", pos)
	if err != nil {
		return Value{}, err
	}
	rt := info.StaticMethodSigs[m].RetType
	if rt.IR == "void" || rt.IR == "" {
		return Value{Ty: TypeVoid}, nil
	}
	if rt.IsDynamic() {
		return v, nil
	}
	return e.emitUnboxBoxToType(v.Ref, rt), nil
}

// staticMethodDecoratorSupported reports a standard-dialect decorated
// static method: a plain (non-generator, non-accessor) one of a
// non-generic class, replaced through the class object's own property.
func staticMethodDecoratorSupported(cd *ast.ClassDeclaration, m *ast.FunctionDeclaration) bool {
	return len(m.Decorators) > 0 && m.IsStatic && m.AccessorKind == "" && !m.IsGenerator && len(cd.TypeParams) == 0
}

type decoratorCall func(dec ast.Expression, args []ast.Expression) (Value, error)
type decoratorContext func(kind, name string, isStatic bool) (ast.Expression, error)

// emitStaticMethodDecorators threads static method m through its
// decorators (bottom-up); a callable one returns replaces the method.
func (e *Emitter) emitStaticMethodDecorators(cd *ast.ClassDeclaration, m *ast.FunctionDeclaration, call decoratorCall, context decoratorContext) error {
	pos := m.GetPos()
	info := e.classes[cd.Name]
	orig, err := e.emitStaticMethodOverrideRead(info, cd.Name, m.Name, pos)
	if err != nil {
		return err
	}
	val := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", val))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", orig.Ref, val))
	valName := "__kml_stddec_sval$" + cd.Name + "$" + m.Name
	e.define(valName, Symbol{Ptr: val, Ty: TypeAny})
	ctx, err := context("method", m.Name, true)
	if err != nil {
		return err
	}
	for i := len(m.Decorators) - 1; i >= 0; i-- {
		ret, err := call(m.Decorators[i], []ast.Expression{ast.NewIdentifier(valName, pos), ctx})
		if err != nil {
			return err
		}
		if ret.Ty.IR == "void" || ret.Ty.IR == "" {
			continue
		}
		boxed, err := e.emitBoxValue(ret)
		if err != nil {
			return err
		}
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", cur, val))
		keep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", keep, boxed.Ref, nbUndefined))
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, keep, cur, boxed.Ref))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", sel, val))
	}
	final := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", final, val))
	changed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %s", changed, final, orig.Ref))
	setL, doneL := e.freshLabel("staticdec.set"), e.freshLabel("staticdec.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", changed, setL, doneL))
	e.emitLabel(setL)
	e.declareFn("__kml_classref_set", "declare void @__kml_classref_set("+irPtr+", "+irPtr+", i64)")
	e.emitInstr(fmt.Sprintf("call void @__kml_classref_set("+irPtr+" %s, "+irPtr+" %s, i64 %s)", e.classRefPtr(cd.Name), e.internString(m.Name), final))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return nil
}

// emitStaticFieldDecorators calls static field f's decorators (value
// undefined); each initializer one returns transforms the field's initial
// value once the statics initialize (emitStaticFieldInitializers).
func (e *Emitter) emitStaticFieldDecorators(cd *ast.ClassDeclaration, f ast.AnnotField, call decoratorCall, context decoratorContext) error {
	pos := cd.GetPos()
	ctx, err := context("field", f.Name, true)
	if err != nil {
		return err
	}
	if e.staticFieldInits == nil {
		e.staticFieldInits = map[*ast.ClassDeclaration][]staticFieldInit{}
	}
	for i := len(f.Decorators) - 1; i >= 0; i-- {
		ret, err := call(f.Decorators[i], []ast.Expression{ast.NewNullLiteral(true, pos), ctx})
		if err != nil {
			return err
		}
		if ret.Ty.IR == "void" || ret.Ty.IR == "" {
			continue
		}
		boxed, err := e.emitBoxValue(ret)
		if err != nil {
			return err
		}
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", boxed.Ref, slot))
		e.staticFieldInits[cd] = append(e.staticFieldInits[cd], staticFieldInit{field: f.Name, slot: slot})
	}
	return nil
}

// staticFieldInit is one initializer a static field decorator returned,
// held in slot.
type staticFieldInit struct {
	field, slot string
}

// emitStaticFieldInitializers runs, in order, the initializers class cd's
// static field decorators returned: `C.f = init.call(C, C.f)`.
func (e *Emitter) emitStaticFieldInitializers(cd *ast.ClassDeclaration) error {
	pos := cd.GetPos()
	for i, in := range e.staticFieldInits[cd] {
		name := fmt.Sprintf("__kml_stddec_sinit$%s$%d", cd.Name, i)
		e.define(name, Symbol{Ptr: in.slot, Ty: TypeAny})
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", cur, in.slot))
		has := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", has, cur, nbUndefined))
		runL, doneL := e.freshLabel("staticinit.run"), e.freshLabel("staticinit.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, runL, doneL))
		e.emitLabel(runL)
		cls := ast.NewIdentifier(cd.Name, pos)
		field := ast.NewMemberExpression(ast.NewIdentifier(cd.Name, pos), in.field, pos)
		callee := ast.NewMemberExpression(ast.NewIdentifier(name, pos), "call", pos)
		assign := ast.NewAssignmentExpression("=", ast.NewMemberExpression(ast.NewIdentifier(cd.Name, pos), in.field, pos),
			ast.NewCallExpression(callee, []ast.Expression{cls, field}, pos), pos)
		if _, err := e.emitSynthExpr(assign); err != nil {
			return err
		}
		if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}
		e.emitLabel(doneL)
	}
	delete(e.staticFieldInits, cd)
	return nil
}

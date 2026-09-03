package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emit_decorators_standard.go — the TC39 standard decorator dialect (TDD-00161
// Stage 5), selected by `-decorators=standard`. Each decorator is called
// `(value, context)`; the return value replaces `value`. This differs from the
// experimental dialect's `(target, key, descriptor)` shape, so the two are
// selected per compilation, never mixed.
//
// This slice covers the two most-used placements — class decorators (observe;
// a returned replacement constructor is the same documented divergence as the
// experimental dialect's Stage 4b) and method decorators (value = the method as
// a callable; a returned callable re-routes the method's calls through its
// per-method slot). Field, getter/setter, and `accessor` auto-accessor
// decorators, and `context.addInitializer`, need construction-time injection
// and are a clean rejection here — the standard dialect's remaining sub-stage.
// Parameter decorators do not exist in TC39 decorators and are rejected.
func (e *Emitter) emitStandardDecoratorApplications(cd *ast.ClassDeclaration) error {
	pos := cd.GetPos()

	// Reject the placements this slice does not implement, with clear messages,
	// rather than silently ignoring their decorators. Instance field decorators
	// are supported (their initializer runs in the constructor tail); a *static*
	// field decorator would need class-time application and is rejected.
	for _, f := range cd.Fields {
		if len(f.Decorators) > 0 && f.Static {
			return fmt.Errorf("%d:%d: a standard (TC39) static field decorator on '%s' is not supported yet", pos.Line, pos.Col, f.Name)
		}
	}
	rejectParams := func(params []ast.Param) error {
		for _, p := range params {
			if len(p.Decorators) > 0 {
				return fmt.Errorf("%d:%d: parameter decorators do not exist in the standard (TC39) decorator dialect — use -decorators=experimental for parameter decorators", pos.Line, pos.Col)
			}
		}
		return nil
	}
	if cd.Constructor != nil {
		if err := rejectParams(cd.Constructor.Params); err != nil {
			return err
		}
	}
	for _, m := range cd.Methods {
		if err := rejectParams(m.Params); err != nil {
			return err
		}
		if len(m.Decorators) > 0 {
			if m.AccessorKind != "" {
				if m.IsStatic || len(cd.TypeParams) > 0 {
					return fmt.Errorf("%d:%d: a standard (TC39) decorator on a static/generic-class accessor '%s' is not supported yet", pos.Line, pos.Col, m.Name)
				}
			} else if !methodDecoratorSupported(cd, m) {
				return fmt.Errorf("%d:%d: a standard (TC39) decorator on '%s' is not supported (static/generator methods and generic-class methods are the unsupported cases)", pos.Line, pos.Col, m.Name)
			}
		}
	}

	// One shared per-class value/target object, bound to a synthetic local the
	// synthesized calls reference (the class value for class decorators, and the
	// object metadata lives on).
	e.ensureDynObj()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	classBox := e.emitNbTagPtr(bag, kmlTagDynObject)
	classSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", classSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", classBox, classSlot))
	classValName := "__kml_stddec_cls$" + cd.Name
	e.define(classValName, Symbol{Ptr: classSlot, Ty: TypeAny})

	applyGet := func(dec ast.Expression, args []ast.Expression) (Value, error) {
		return e.emitExpr(ast.NewCallExpression(dec, args, dec.GetPos()))
	}

	// One per-class addInitializer callable, shared by every member's context —
	// each `context.addInitializer(fn)` appends to the class's init list, run
	// per-instance in the constructor tail.
	addInitBox, err := e.emitAddInitializerFn(cd.Name)
	if err != nil {
		return err
	}

	// context object builder for a member (or the class).
	makeContext := func(kind, name string, isStatic bool) (ast.Expression, error) {
		ctx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", ctx))
		set := func(key, boxRef string) {
			st := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", st, ctx, e.internString(key), boxRef))
		}
		set("kind", e.emitNbTagPtr(e.internString(kind), kmlTagString))
		set("name", e.emitNbTagPtr(e.internString(name), kmlTagString))
		set("static", boolBox(isStatic))
		set("private", boolBox(false))
		set("addInitializer", addInitBox.Ref)
		set("metadata", fmt.Sprintf("%d", nbUndefined))
		ctxBox := e.emitNbTagPtr(ctx, kmlTagDynObject)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ctxBox, slot))
		nm := "__kml_stddec_ctx$" + cd.Name + "$" + kind + "$" + name
		e.define(nm, Symbol{Ptr: slot, Ty: TypeAny})
		return ast.NewIdentifier(nm, pos), nil
	}

	// A per-(class,memberKey) decorator applier shared by methods and
	// getters/setters: box the member as a callable, thread it through the
	// decorators (each replacing it when it returns a callable), store the
	// result in the member's routing slot.
	applyMemberDecorators := func(memberKey, valName, kind, ctxName string, decorators []ast.Expression, isStatic bool) error {
		slot := e.decoratedMethodSlots[cd.Name][memberKey]
		boxed, err := e.emitMethodBoxAdapter(cd.Name, memberKey, pos)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, slot))
		valAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", valAlloca))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, valAlloca))
		e.define(valName, Symbol{Ptr: valAlloca, Ty: TypeAny})
		ctxExpr, err := makeContext(kind, ctxName, isStatic)
		if err != nil {
			return err
		}
		for i := len(decorators) - 1; i >= 0; i-- {
			ret, err := applyGet(decorators[i], []ast.Expression{ast.NewIdentifier(valName, pos), ctxExpr})
			if err != nil {
				return err
			}
			if ret.Ty.IsDynamic {
				boxedRet, err := e.emitBoxValue(ret)
				if err != nil {
					return err
				}
				cur := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, valAlloca))
				keep := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", keep, boxedRet.Ref, nbUndefined))
				sel := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, keep, cur, boxedRet.Ref))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sel, valAlloca))
			}
		}
		final := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", final, valAlloca))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", final, slot))
		return nil
	}

	// Getter/setter decorators: routed through the accessor's slot (kind
	// "getter"/"setter"); applied before the class decorator.
	for _, m := range cd.Methods {
		if len(m.Decorators) == 0 || m.AccessorKind == "" {
			continue
		}
		kind := "getter"
		if m.AccessorKind == "set" {
			kind = "setter"
		}
		key := accessorMethodName(m.AccessorKind, m.Name)
		if err := applyMemberDecorators(key, "__kml_stddec_val$"+cd.Name+"$"+key, kind, m.Name, m.Decorators, m.IsStatic); err != nil {
			return err
		}
	}

	// Auto-accessor (`accessor x`) decorators — the TC39 `{get,set,init}`
	// protocol: the decorator receives `{get, set}` and may return `{get, set,
	// init}`. A returned get/set replaces the accessor's slots; a returned init
	// transforms the backing field's initial value (via its field-init slot).
	for _, aa := range cd.AutoAccessors {
		getKey := accessorMethodName("get", aa.Name)
		setKey := accessorMethodName("set", aa.Name)
		getSlot := e.decoratedMethodSlots[cd.Name][getKey]
		setSlot := e.decoratedMethodSlots[cd.Name][setKey]
		initSlot := e.standardFieldInitSlots[cd.Name][aa.Backing]
		boxedGet, err := e.emitMethodBoxAdapter(cd.Name, getKey, pos)
		if err != nil {
			return err
		}
		boxedSet, err := e.emitMethodBoxAdapter(cd.Name, setKey, pos)
		if err != nil {
			return err
		}
		// Running trio of boxes: get / set / init.
		gA := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", gA))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxedGet.Ref, gA))
		sA := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", sA))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxedSet.Ref, sA))
		iA := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iA))
		e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, iA))
		// A synthetic local holding the `{get, set}` accessor object, rebuilt
		// before each decorator so a later decorator sees the prior's result.
		accAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", accAlloca))
		accName := "__kml_stddec_acc$" + cd.Name + "$" + aa.Name
		e.define(accName, Symbol{Ptr: accAlloca, Ty: TypeAny})
		ctxExpr, err := makeContext("accessor", aa.Name, false)
		if err != nil {
			return err
		}
		adopt := func(retBox, cur string) string { // cur unless retBox present (non-undefined)
			isU := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, retBox, nbUndefined))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, isU, cur, retBox))
			return sel
		}
		for i := len(aa.Decorators) - 1; i >= 0; i-- {
			// Build the current { get, set } object.
			bag := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
			curG := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curG, gA))
			curS := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curS, sA))
			st := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", st, bag, e.internString("get"), curG))
			st2 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", st2, bag, e.internString("set"), curS))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbTagPtr(bag, kmlTagDynObject), accAlloca))
			ret, err := applyGet(aa.Decorators[i], []ast.Expression{ast.NewIdentifier(accName, pos), ctxExpr})
			if err != nil {
				return err
			}
			if !ret.Ty.IsDynamic {
				continue
			}
			retBox, err := e.emitBoxValue(ret)
			if err != nil {
				return err
			}
			// Only read {get,set,init} when the return is an object.
			tag, _ := e.emitUnboxTagPayload(retBox)
			isObj := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagDynObject))
			objL := e.freshLabel("aadec.obj")
			contL := e.freshLabel("aadec.cont")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, contL))
			e.emitLabel(objL)
			ng, err := e.emitDynAnyMemberGet(retBox, e.internString("get"), pos)
			if err != nil {
				return err
			}
			curGv := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curGv, gA))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", adopt(ng.Ref, curGv), gA))
			ns, err := e.emitDynAnyMemberGet(retBox, e.internString("set"), pos)
			if err != nil {
				return err
			}
			curSv := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curSv, sA))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", adopt(ns.Ref, curSv), sA))
			ni, err := e.emitDynAnyMemberGet(retBox, e.internString("init"), pos)
			if err != nil {
				return err
			}
			curIv := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curIv, iA))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", adopt(ni.Ref, curIv), iA))
			e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
			e.emitLabel(contL)
		}
		fg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fg, gA))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fg, getSlot))
		fs := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fs, sA))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fs, setSlot))
		fi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fi, iA))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fi, initSlot))
	}

	// Method decorators: value = the method (a callable); the return replaces it.
	for _, m := range cd.Methods {
		if len(m.Decorators) == 0 || m.AccessorKind != "" {
			continue
		}
		slot := e.decoratedMethodSlots[cd.Name][m.Name]
		boxedMethod, err := e.emitMethodBoxAdapter(cd.Name, m.Name, pos)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxedMethod.Ref, slot))
		// Thread the value through the decorators (bottom-up), each replacing it
		// when it returns a callable.
		valAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", valAlloca))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxedMethod.Ref, valAlloca))
		valName := "__kml_stddec_val$" + cd.Name + "$" + m.Name
		e.define(valName, Symbol{Ptr: valAlloca, Ty: TypeAny})
		ctxExpr, err := makeContext("method", m.Name, m.IsStatic)
		if err != nil {
			return err
		}
		for i := len(m.Decorators) - 1; i >= 0; i-- {
			ret, err := applyGet(m.Decorators[i], []ast.Expression{
				ast.NewIdentifier(valName, pos),
				ctxExpr,
			})
			if err != nil {
				return err
			}
			// A standard decorator returns the replacement (or undefined to
			// keep the current value). Adopt a dynamic function/object return.
			if ret.Ty.IsDynamic {
				boxedRet, err := e.emitBoxValue(ret)
				if err != nil {
					return err
				}
				cur := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, valAlloca))
				keep := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", keep, boxedRet.Ref, nbUndefined))
				sel := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, keep, cur, boxedRet.Ref))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sel, valAlloca))
			}
		}
		final := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", final, valAlloca))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", final, slot))
	}

	// Field decorators: value = undefined; the return is an initializer stored
	// in the field's init slot, applied per-instance in the constructor tail.
	// (V1: for multiple decorators on one field the last-returned initializer
	// wins; the common single-decorator case is exact.)
	for _, f := range cd.Fields {
		if len(f.Decorators) == 0 || f.Static {
			continue
		}
		slot := e.standardFieldInitSlots[cd.Name][f.Name]
		ctxExpr, err := makeContext("field", f.Name, false)
		if err != nil {
			return err
		}
		for i := len(f.Decorators) - 1; i >= 0; i-- {
			ret, err := applyGet(f.Decorators[i], []ast.Expression{
				ast.NewNullLiteral(true, pos),
				ctxExpr,
			})
			if err != nil {
				return err
			}
			// Store a returned initializer (a callable); a void/undefined return
			// leaves the slot as undefined (the constructor tail then keeps the
			// field's original value).
			if ret.Ty.IsDynamic {
				boxedRet, err := e.emitBoxValue(ret)
				if err != nil {
					return err
				}
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxedRet.Ref, slot))
			}
		}
	}

	// Class decorators: value = the class; applied last, bottom-up. A returned
	// replacement is refused at runtime (the documented static-model divergence).
	if len(cd.TypeParams) == 0 {
		ctxExpr, err := makeContext("class", demangleModuleName(cd.Name), false)
		if err != nil {
			return err
		}
		for i := len(cd.Decorators) - 1; i >= 0; i-- {
			ret, err := applyGet(cd.Decorators[i], []ast.Expression{
				ast.NewIdentifier(classValName, pos),
				ctxExpr,
			})
			if err != nil {
				return err
			}
			if ret.Ty.IsDynamic {
				boxedRet, err := e.emitBoxValue(ret)
				if err != nil {
					return err
				}
				// Returning undefined (keep) or the same class value (identity)
				// is fine; only a *different* non-undefined return is a genuine
				// replacement, which the static-class model can't route.
				cur := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, classSlot))
				isUndef := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, boxedRet.Ref, nbUndefined))
				isSame := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", isSame, boxedRet.Ref, cur))
				ok := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, isUndef, isSame))
				contL := e.freshLabel("stdcls.cont")
				replL := e.freshLabel("stdcls.repl")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, contL, replL))
				e.emitLabel(replL)
				e.emitThrowTypeError("a class decorator that returns a replacement class is not supported yet")
				e.emitLabel(contL)
			}
		}
	}
	return nil
}

// emitAddInitializerFn generates (once per class) a tag-12 callable for
// `context.addInitializer`: it pushes its argument onto the class's init-list
// global (creating the dynamic array on first use). Returns the boxed callable.
func (e *Emitter) emitAddInitializerFn(className string) (Value, error) {
	listG, ok := e.standardInitListGlobals[className]
	if !ok {
		return Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}, nil
	}
	fnName := fmt.Sprintf("@__kml_addinit_%s", llvmSafeSymbol(className))
	e.ensureDynArr()

	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedLabelCtr := e.labelCtr
	savedScopes := e.scopes
	savedRetType := e.currentRetType
	savedBlockDone := e.blockDone
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.labelCtr = 0
	e.scopes = nil
	e.blockDone = false
	e.currentRetType = TypeAny
	e.pushScope()
	e.ensureNanBox()

	// fn = argv[0] (the callback)
	fn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %%p_argv, align 8", fn))
	listBox := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", listBox, listG))
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", tag, listBox))
	isArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagDynArray))
	arrPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", arrPtr))
	haveL := e.freshLabel("addinit.have")
	makeL := e.freshLabel("addinit.make")
	pushL := e.freshLabel("addinit.push")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, haveL, makeL))
	e.emitLabel(haveL)
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, listBox))
	existing := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", existing, pay))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", existing, arrPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", pushL))
	e.emitLabel(makeL)
	fresh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 0)", fresh))
	freshBox := e.emitNbTagPtr(fresh, kmlTagDynArray)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", freshBox, listG))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fresh, arrPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", pushL))
	e.emitLabel(pushL)
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", arr, arrPtr))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", arr, fn))
	e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))

	e.functions.WriteString(fmt.Sprintf("\ndefine i64 %s(ptr %%env, i64 %%p_this, i64 %%p_argc, ptr %%p_argv) {\nentry:\n", fnName))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")
	e.restoreDynFnState(savedAllocas, savedBody, savedRegCtr, savedLabelCtr, savedScopes, savedRetType, savedBlockDone)

	e.ensureMalloc()
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", rec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fnName, rec))
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", envSlot))
	aritySlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", aritySlot, rec))
	e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", aritySlot))
	return Value{Ref: e.emitNbTagPtr(rec, kmlTagDynFunc), Ty: TypeAny}, nil
}

// emitStandardConstructorTail runs, at the end of a class's constructor, the
// per-instance standard-decorator effects (TDD-00161 Stage 5): each decorated
// field's initializer transforms the field's initial value, and every
// `context.addInitializer` callback runs with `this` as receiver. No-op unless
// the standard dialect is selected and the class carries such decorators.
func (e *Emitter) emitStandardConstructorTail(className string) error {
	if !e.standardDecorators() {
		return nil
	}
	fieldSlots := e.standardFieldInitSlots[className]
	listG, hasList := e.standardInitListGlobals[className]
	if len(fieldSlots) == 0 && !hasList {
		return nil
	}
	if hasList {
		e.ensureDynArr()
	}
	classTy := e.classes[className].Ty
	thisSym, ok := e.lookup("this")
	if !ok {
		return nil
	}
	thisPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", thisPtr, thisSym.Ptr))
	structIR := classTy.StructIR()
	undef := fmt.Sprintf("%d", nbUndefined)

	// Field initializer transforms: field = initializer(field) when the field's
	// standard decorator returned an initializer callable.
	for field, slot := range fieldSlots {
		idx, fieldTy, ok := classTy.FieldIndex(field)
		if !ok {
			continue
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, thisPtr, idx))
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", cur, StructFieldIR(fieldTy), gep, fieldTy.Align()))
		curBox, err := e.emitBoxValue(Value{Ref: cur, Ty: fieldTy})
		if err != nil {
			return err
		}
		initBox := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", initBox, slot))
		tag := e.freshReg()
		e.ensureNanBox()
		e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", tag, initBox))
		isFn := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isFn, tag, kmlTagDynFunc))
		res := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", curBox.Ref, res))
		callL := e.freshLabel("fielddec.call")
		doneL := e.freshLabel("fielddec.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFn, callL, doneL))
		e.emitLabel(callL)
		argv := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca [1 x i64], align 8", argv))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", curBox.Ref, argv))
		r := e.emitDynFnBoxCallUnchecked(initBox, undef, argv, 1)
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, res))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		newBox := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", newBox, res))
		newVal := e.coerce(Value{Ref: newBox, Ty: TypeAny}, fieldTy)
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), newVal.Ref, gep, fieldTy.Align()))
	}

	// addInitializer callbacks: run each with `this` as receiver.
	if hasList {
		listBox := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", listBox, listG))
		ltag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", ltag, listBox))
		isArr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, ltag, kmlTagDynArray))
		runL := e.freshLabel("initlist.run")
		endL := e.freshLabel("initlist.end")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, runL, endL))
		e.emitLabel(runL)
		lpay := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", lpay, listBox))
		arr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", arr, lpay))
		alen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynarr_len(ptr %s)", alen, arr))
		iPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iPtr))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", iPtr))
		thisBox, err := e.emitBoxValue(Value{Ref: thisPtr, Ty: classTy})
		if err != nil {
			return err
		}
		noArgv := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", noArgv))
		condL := e.freshLabel("initlist.cond")
		bodyL := e.freshLabel("initlist.body")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		i := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, iPtr))
		more := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, alen))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
		e.emitLabel(bodyL)
		fnBox := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynarr_at(ptr %s, i64 %s)", fnBox, arr, i))
		e.emitDynFnBoxCallUnchecked(fnBox, thisBox.Ref, noArgv, 0)
		iN := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", iN, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", iN, iPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(endL)
	}
	return nil
}

// boolBox returns the NaN-box constant for a compile-time boolean.
func boolBox(b bool) string {
	if b {
		return fmt.Sprintf("%d", nbTrue)
	}
	return fmt.Sprintf("%d", nbFalse)
}

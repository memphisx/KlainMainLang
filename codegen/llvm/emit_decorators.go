package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emitClassDecoratorApplications runs a class's observe-only decorators
// (TDD-00161 Stage 1) — property decorators and parameter decorators — at
// class-definition time, from inside the class's `_staticinit` function.
//
// Faithfulness model: each decorator is invoked exactly as the experimental
// decorators spec prescribes, reusing the ordinary call machinery by
// synthesizing an `ast.CallExpression` whose callee is the decorator
// expression and whose arguments are the spec's `(target, key[, index])`.
// A single per-class `target` object (a D1 dynamic object) stands in for the
// prototype, created once and shared across the class's decorators so a
// decorator that stashes data on `target` and another that reads it back see
// the same object identity. Return values are discarded — which is spec-exact
// for property and parameter decorators (their return is always ignored).
//
// Decorators on one element apply bottom-up (the decorator nearest the
// declaration first), so each element's Decorators slice — stored top-first —
// is iterated in reverse.
//
// Class and method/accessor decorators are rejected earlier (emitClassDecl);
// they never reach here.
func (e *Emitter) emitClassDecoratorApplications(cd *ast.ClassDeclaration) error {
	if !classHasObserveDecorators(cd) {
		return nil
	}
	if e.standardDecorators() {
		return e.emitStandardDecoratorApplications(cd)
	}
	pos := cd.GetPos()

	// One shared per-class target object, bound to a synthetic local the
	// synthesized decorator calls reference as their first argument.
	e.ensureDynObj()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	boxed := e.emitNbTagPtr(bag, kmlTagDynObject)
	targetSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", targetSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed, targetSlot))
	// A per-class synthetic binding name — decorator applications for several
	// classes emit into the same top-level scope, so the target of one must not
	// shadow another's.
	targetName := "__kml_dec_target$" + cd.Name
	e.define(targetName, Symbol{Ptr: targetSlot, Ty: TypeAny})
	targetExpr := func() ast.Expression { return ast.NewIdentifier(targetName, pos) }

	// emitDecoratorMetadata: define design:type/paramtypes/returntype on the
	// target before any user decorator runs, so a user decorator can read it
	// (TDD-00161 Stage 3). No-op unless the flag is set.
	e.emitClassMemberDesignMetadata(bag, cd, pos)

	apply := func(dec ast.Expression, args []ast.Expression) error {
		call := ast.NewCallExpression(dec, args, dec.GetPos())
		_, err := e.emitExpr(call)
		return err
	}

	// Property decorators: dec(target, "propertyName").
	for _, f := range cd.Fields {
		for i := len(f.Decorators) - 1; i >= 0; i-- {
			if err := apply(f.Decorators[i], []ast.Expression{
				targetExpr(),
				ast.NewStringLiteral(f.Name, pos),
			}); err != nil {
				return err
			}
		}
	}

	// Parameter decorators: dec(target, key, parameterIndex). key is the method
	// name, or `undefined` for a constructor parameter (matching TS).
	applyParamDecorators := func(params []ast.Param, key ast.Expression) error {
		for idx, prm := range params {
			for i := len(prm.Decorators) - 1; i >= 0; i-- {
				if err := apply(prm.Decorators[i], []ast.Expression{
					targetExpr(),
					key,
					ast.NewNumberLiteral(fmt.Sprintf("%d", idx), pos),
				}); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if cd.Constructor != nil {
		if err := applyParamDecorators(cd.Constructor.Params, ast.NewNullLiteral(true, pos)); err != nil {
			return err
		}
	}
	for _, m := range cd.Methods {
		if err := applyParamDecorators(m.Params, ast.NewStringLiteral(m.Name, pos)); err != nil {
			return err
		}
	}

	// Method decorators (Stage 2): dec(target, key, descriptor). The descriptor
	// is a live dynamic object whose `value` is the method (boxed as a callable);
	// a decorator may mutate it or return a replacement. The method's routing
	// slot is set to the effective descriptor's `value` afterwards, so calls go
	// to the (possibly replaced) implementation.
	applyGet := func(dec ast.Expression, args []ast.Expression) (Value, error) {
		return e.emitExpr(ast.NewCallExpression(dec, args, dec.GetPos()))
	}
	for _, m := range cd.Methods {
		if !methodDecoratorSupported(cd, m) {
			continue
		}
		slot := e.decoratedMethodSlots[cd.Name][m.Name]
		boxedMethod, err := e.emitMethodBoxAdapter(cd.Name, m.Name, pos)
		if err != nil {
			return err
		}
		// Default the slot to the original method, so an observe-only decorator
		// (no replacement) leaves dispatch on the real implementation.
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxedMethod.Ref, slot))

		// Build the property descriptor { value, writable, enumerable,
		// configurable } as a dynamic object, bound to a synthetic local.
		descBag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", descBag))
		descField := func(name string, boxRef string) {
			st := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", st, descBag, e.internString(name), boxRef))
		}
		descField("value", boxedMethod.Ref)
		descField("writable", fmt.Sprintf("%d", nbTrue))
		descField("enumerable", fmt.Sprintf("%d", nbFalse))
		descField("configurable", fmt.Sprintf("%d", nbTrue))
		descBox := e.emitNbTagPtr(descBag, kmlTagDynObject)
		descAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", descAlloca))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", descBox, descAlloca))
		descName := "__kml_dec_desc$" + cd.Name + "$" + m.Name
		e.define(descName, Symbol{Ptr: descAlloca, Ty: TypeAny})

		for i := len(m.Decorators) - 1; i >= 0; i-- {
			ret, err := applyGet(m.Decorators[i], []ast.Expression{
				targetExpr(),
				ast.NewStringLiteral(m.Name, pos),
				ast.NewIdentifier(descName, pos),
			})
			if err != nil {
				return err
			}
			// TS __decorate: if the decorator returns a value, it replaces the
			// descriptor. Adopt a dynamic (or object) return at runtime; a
			// statically void/scalar return can't be a descriptor, so ignore it.
			if ret.Ty.IsDynamic {
				boxedRet, err := e.emitBoxValue(ret)
				if err != nil {
					return err
				}
				tag, _ := e.emitUnboxTagPayload(boxedRet)
				isObj := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagDynObject))
				cur := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, descAlloca))
				sel := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, isObj, boxedRet.Ref, cur))
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sel, descAlloca))
			}
		}

		// slot = effective descriptor's `value`.
		finalDesc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalDesc, descAlloca))
		valBox, err := e.emitDynAnyMemberGet(Value{Ref: finalDesc, Ty: TypeAny}, e.internString("value"), pos)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", valBox.Ref, slot))
	}

	// Class decorators (Stage 4): dec(constructor). Applied last (after member
	// decorators, matching TS's __decorate ordering) and bottom-up. The
	// per-class target stands in for the constructor. Observe-only decorators
	// (the registration pattern) run faithfully; a decorator that *returns* a
	// replacement constructor is refused at runtime — the static-class model has
	// no constructor-replacement routing yet (Stage 4b) — rather than silently
	// dropping the replacement.
	if len(cd.TypeParams) == 0 {
		for i := len(cd.Decorators) - 1; i >= 0; i-- {
			ret, err := applyGet(cd.Decorators[i], []ast.Expression{targetExpr()})
			if err != nil {
				return err
			}
			if ret.Ty.IsDynamic {
				boxedRet, err := e.emitBoxValue(ret)
				if err != nil {
					return err
				}
				isRepl := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", isRepl, boxedRet.Ref, nbUndefined))
				replL := e.freshLabel("clsdec.repl")
				contL := e.freshLabel("clsdec.cont")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRepl, replL, contL))
				e.emitLabel(replL)
				e.emitThrowTypeError("a class decorator that returns a replacement constructor is not supported yet")
				e.emitLabel(contL)
			}
		}
	}
	return nil
}

// methodDecoratorSupported reports whether a decorated method is one Stage 2
// can route: a plain instance method on a non-generic class. Static methods,
// accessors (get/set), generator methods, and generic classes are rejected
// (emitClassDecl) rather than silently mis-decorated.
func methodDecoratorSupported(cd *ast.ClassDeclaration, m *ast.FunctionDeclaration) bool {
	return len(m.Decorators) > 0 && !m.IsStatic && m.AccessorKind == "" &&
		!m.IsGenerator && len(cd.TypeParams) == 0
}

// registerDecoratedMethodSlots declares, for every supported decorated method,
// a module-level global holding that method's current callable (a boxed tag-12
// value) and records it in e.decoratedMethodSlots. Runs before any body/call
// emission so call sites can route through the slot (TDD-00161 Stage 2).
func (e *Emitter) registerDecoratedMethodSlots(prog *ast.Program) error {
	for _, stmt := range prog.Body {
		cd, ok := stmt.(*ast.ClassDeclaration)
		if !ok {
			continue
		}
		for _, m := range cd.Methods {
			if !methodDecoratorSupported(cd, m) {
				continue
			}
			slot := "@" + llvmSafeSymbol(cd.Name+"_"+m.Name+"__decslot")
			e.emitGlobal(fmt.Sprintf("%s = global i64 0", slot))
			if e.decoratedMethodSlots[cd.Name] == nil {
				e.decoratedMethodSlots[cd.Name] = map[string]string{}
			}
			e.decoratedMethodSlots[cd.Name][m.Name] = slot
		}
		// Standard-dialect construction-time slots (TDD-00161 Stage 5): a
		// per-field initializer slot for each decorated field, and a per-class
		// addInitializer callback list. Only under the standard dialect.
		if !e.standardDecorators() || len(cd.TypeParams) > 0 {
			continue
		}
		for _, f := range cd.Fields {
			if len(f.Decorators) == 0 || f.Static {
				continue
			}
			slot := "@" + llvmSafeSymbol(cd.Name+"_"+f.Name+"__fieldinit")
			e.emitGlobal(fmt.Sprintf("%s = global i64 %d", slot, nbUndefined))
			if e.standardFieldInitSlots[cd.Name] == nil {
				e.standardFieldInitSlots[cd.Name] = map[string]string{}
			}
			e.standardFieldInitSlots[cd.Name][f.Name] = slot
		}
		// A decorated getter/setter routes through the same per-method slot the
		// method decorators use, keyed by its accessor dispatch name (so the
		// getter's emitClassCall / the setter's emitClassSetterCall pick it up).
		for _, m := range cd.Methods {
			if len(m.Decorators) == 0 || m.AccessorKind == "" || m.IsStatic {
				continue
			}
			key := accessorMethodName(m.AccessorKind, m.Name)
			slot := "@" + llvmSafeSymbol(cd.Name+"_"+key+"__decslot")
			e.emitGlobal(fmt.Sprintf("%s = global i64 0", slot))
			if e.decoratedMethodSlots[cd.Name] == nil {
				e.decoratedMethodSlots[cd.Name] = map[string]string{}
			}
			e.decoratedMethodSlots[cd.Name][key] = slot
		}
		// A decorated `accessor x` auto-field: slots for its generated get/set
		// (routing accessor access through the {get,set} the decorator returns)
		// and a field-init slot on its backing field (for the returned init).
		for _, aa := range cd.AutoAccessors {
			for _, kind := range []string{"get", "set"} {
				key := accessorMethodName(kind, aa.Name)
				slot := "@" + llvmSafeSymbol(cd.Name+"_"+key+"__decslot")
				e.emitGlobal(fmt.Sprintf("%s = global i64 0", slot))
				if e.decoratedMethodSlots[cd.Name] == nil {
					e.decoratedMethodSlots[cd.Name] = map[string]string{}
				}
				e.decoratedMethodSlots[cd.Name][key] = slot
			}
			bslot := "@" + llvmSafeSymbol(cd.Name+"_"+aa.Backing+"__fieldinit")
			e.emitGlobal(fmt.Sprintf("%s = global i64 %d", bslot, nbUndefined))
			if e.standardFieldInitSlots[cd.Name] == nil {
				e.standardFieldInitSlots[cd.Name] = map[string]string{}
			}
			e.standardFieldInitSlots[cd.Name][aa.Backing] = bslot
		}
		if classHasStandardDecorators(cd) || len(cd.AutoAccessors) > 0 {
			g := "@" + llvmSafeSymbol(cd.Name+"__initlist")
			e.emitGlobal(fmt.Sprintf("%s = global i64 %d", g, nbUndefined))
			e.standardInitListGlobals[cd.Name] = g
		}
	}
	return nil
}

// classHasStandardDecorators reports whether cd carries any decorator that runs
// under the standard dialect (class, method, or field) — the gate for the
// per-class addInitializer list.
func classHasStandardDecorators(cd *ast.ClassDeclaration) bool {
	if len(cd.Decorators) > 0 {
		return true
	}
	for _, m := range cd.Methods {
		if len(m.Decorators) > 0 {
			return true
		}
	}
	for _, f := range cd.Fields {
		if len(f.Decorators) > 0 {
			return true
		}
	}
	return false
}

// emitMethodBoxAdapter generates a tag-12 dynamic-function record wrapping a
// static instance method so it can live in a property descriptor's `value` and
// be invoked through the dynamic-call ABI (TDD-00161 Stage 2). The adapter
// unboxes the call-time receiver and each boxed argument to the method's
// concrete parameter types, calls the static `@<Implementor>_<method>`, and
// boxes the result. Mirrors emitDynClosureAdapter's shape; the difference is
// the receiver comes from the tag-12 call's `%p_this` word rather than a bound
// closure env. Rejects a method whose signature the adapter can't marshal
// (array/nullable/rest parameters) — a clean compile-time error.
func (e *Emitter) emitMethodBoxAdapter(className, methodName string, pos ast.Pos) (Value, error) {
	info := e.classes[className]
	sig := info.MethodSigs[methodName]
	for _, pty := range sig.ParamTypes {
		if pty.IsArray || isNullableScalar(pty) {
			return Value{}, fmt.Errorf("%d:%d: a method decorator on '%s.%s' is not supported yet — its parameter types can't be marshalled through the decorator ABI", pos.Line, pos.Col, className, methodName)
		}
	}
	if sig.HasRest {
		return Value{}, fmt.Errorf("%d:%d: a method decorator on '%s.%s' (rest parameter) is not supported yet", pos.Line, pos.Col, className, methodName)
	}
	llvmName := llvmSafeSymbol(info.MethodImplementor[methodName] + "_" + methodName)
	fnName := fmt.Sprintf("@__kml_methadapt_%s", llvmSafeSymbol(className+"_"+methodName))

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

	// Receiver: unbox %p_this (a boxed class instance) to the raw instance ptr.
	_, thisPay := e.emitUnboxTagPayload(Value{Ref: "%p_this", Ty: TypeAny})
	thisPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", thisPtr, thisPay))

	argParts := []string{"ptr " + thisPtr}
	for i, pty := range sig.ParamTypes {
		word := e.freshReg()
		have := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %%p_argc, %d", have, i))
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %%p_argv, i64 %d", slot, i))
		loaded := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", loaded, slot))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %d", word, have, loaded, nbUndefined))
		if pty.IsDynamic {
			argParts = append(argParts, "i64 "+word)
			continue
		}
		uv := e.emitUnboxBoxToType(word, pty)
		argParts = append(argParts, fmt.Sprintf("%s %s", pty.IR, uv.Ref))
	}

	retTy := sig.RetType
	boxFailed := false
	if retTy.IR == "void" || retTy.IR == "" {
		e.emitInstr(fmt.Sprintf("call void @%s(%s)", llvmName, joinArgs(argParts)))
		e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
	} else {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call %s @%s(%s)", r, retTy.LLVMRetType(), llvmName, joinArgs(argParts)))
		b, err := e.emitBoxValue(Value{Ref: r, Ty: retTy})
		if err != nil {
			boxFailed = true
		} else {
			e.emitTerminator(fmt.Sprintf("ret i64 %s", b.Ref))
		}
	}

	e.functions.WriteString(fmt.Sprintf("\ndefine i64 %s(ptr %%env, i64 %%p_this, i64 %%p_argc, ptr %%p_argv) {\nentry:\n", fnName))
	e.functions.WriteString(e.allocas.String())
	e.functions.WriteString(e.body.String())
	e.functions.WriteString("}\n")

	e.restoreDynFnState(savedAllocas, savedBody, savedRegCtr, savedLabelCtr, savedScopes, savedRetType, savedBlockDone)
	if boxFailed {
		return Value{}, fmt.Errorf("%d:%d: a method decorator on '%s.%s' is not supported yet — its return type can't be boxed", pos.Line, pos.Col, className, methodName)
	}

	e.ensureMalloc()
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 24)", rec))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fnName, rec))
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", envSlot))
	aritySlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", aritySlot, rec))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", len(sig.ParamTypes), aritySlot))
	return Value{Ref: e.emitNbTagPtr(rec, kmlTagDynFunc), Ty: TypeAny}, nil
}

// emitDecoratedMethodCall routes a call to a decorated method through its
// runtime slot (TDD-00161 Stage 2): load the current callable (a boxed tag-12
// value the decorators may have replaced), and invoke it via the dynamic-call
// ABI with the receiver and boxed arguments, coercing the boxed result back to
// the method's declared return type.
func (e *Emitter) emitDecoratedMethodCall(objTy Type, thisVal Value, methodName string, args []ast.Expression, slot string, pos ast.Pos) (Value, error) {
	sig := e.classes[objTy.ClassName].MethodSigs[methodName]
	n := len(args)
	argv := e.freshReg()
	if n > 0 {
		e.emitAlloca(fmt.Sprintf("%s = alloca [%d x i64], align 8", argv, n))
	} else {
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", argv))
	}
	for i, a := range args {
		av, err := e.emitExprWithObjectHint(a, TypeAny)
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitBoxValue(av)
		if err != nil {
			return Value{}, err
		}
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", gep, argv, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, gep))
	}
	fnBox := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", fnBox, slot))
	_, payload := e.emitUnboxTagPayload(Value{Ref: fnBox, Ty: TypeAny})
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", rec, payload))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, rec))
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", env, envSlot))
	recvBox, err := e.emitBoxValue(thisVal)
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s, i64 %s, i64 %d, ptr %s)", r, fp, env, recvBox.Ref, n, argv))
	if sig.RetType.IR == "void" || sig.RetType.IR == "" {
		return Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeVoid}, nil
	}
	return e.coerce(Value{Ref: r, Ty: TypeAny}, sig.RetType), nil
}

// classHasObserveDecorators reports whether cd carries any Stage-1 (property or
// parameter) decorator — the cheap gate that keeps an undecorated class's
// static initializer byte-for-byte unchanged.
func classHasObserveDecorators(cd *ast.ClassDeclaration) bool {
	if len(cd.Decorators) > 0 && len(cd.TypeParams) == 0 {
		return true
	}
	if len(cd.AutoAccessors) > 0 {
		return true
	}
	for _, f := range cd.Fields {
		if len(f.Decorators) > 0 {
			return true
		}
	}
	if cd.Constructor != nil {
		for _, p := range cd.Constructor.Params {
			if len(p.Decorators) > 0 {
				return true
			}
		}
	}
	for _, m := range cd.Methods {
		// Any decorated method or accessor (the specific supported/rejected
		// shapes are decided in emitClassDecl / the dialect emitters).
		if len(m.Decorators) > 0 {
			return true
		}
		for _, p := range m.Params {
			if len(p.Decorators) > 0 {
				return true
			}
		}
	}
	return false
}

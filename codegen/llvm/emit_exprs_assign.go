package llvm

import (
	"KlainMainLang/ast"
	"KlainMainLang/parser"
	"fmt"
	"sort"
	"strings"
)

// isLogicalAssignOp reports whether op is one of the three logical
// assignment operators (&&=, ||=, ??=) — genuinely short-circuiting, unlike
// every other compound-assignment operator, so they can't reuse emitArith's
// eager-evaluate-both-sides shape at all (see emitLogicalCompoundAssign).
func isLogicalAssignOp(op string) bool {
	return op == "&&=" || op == "||=" || op == "??="
}

// tryEmitAccessorAssign attempts `obj.prop = rhs` / `obj.prop OP= rhs`
// against a registered class accessor (TDD-00030). handled is false when
// objVal's class has no accessor at all for this property name — the
// caller should fall through to its own existing FieldIndex-based
// plain-field path unchanged, exactly as if this function had never been
// called.
func (e *Emitter) tryEmitAccessorAssign(objVal Value, prop, op string, rhsExpr ast.Expression, pos ast.Pos) (handled bool, result Value, err error) {
	getter, setter, ok := e.classAccessorSigs(objVal.Ty.ClassName, prop)
	if !ok {
		if info, isCls := e.classes[objVal.Ty.ClassName]; isCls && strings.HasPrefix(prop, "#") {
			if _, isMethod := info.MethodSigs[prop]; isMethod {
				if _, _, isField := info.Ty.FieldIndex(prop); !isField {
					// A private method is not writable: the value is
					// computed, then storing it throws.
					v, err := e.emitExpr(rhsExpr)
					if err != nil {
						return true, Value{}, err
					}
					e.emitThrowTypeError("Private method is not writable")
					return true, v, nil
				}
			}
		}
		return false, Value{}, nil
	}
	if isLogicalAssignOp(op) {
		// `o.p &&= v` is `o.p && (o.p = v)` over the once-evaluated o: the
		// getter runs once, the setter only when the value does not
		// short-circuit.
		obj := e.bindValue(objVal, pos)
		write := ast.NewAssignmentExpression("=", ast.NewMemberExpression(obj, prop, pos), rhsExpr, pos)
		v, err := e.emitExpr(ast.NewBinaryExpression(strings.TrimSuffix(op, "="), ast.NewMemberExpression(obj, prop, pos), write, pos))
		return true, v, err
	}
	if setter == nil {
		// The value is computed (the getter read, the right side run), then
		// storing it throws, as JavaScript's PutValue does.
		var v Value
		if op == "=" {
			v, err = e.emitExpr(rhsExpr)
		} else {
			obj := e.bindValue(objVal, pos)
			v, err = e.emitExpr(ast.NewBinaryExpression(strings.TrimSuffix(op, "="), ast.NewMemberExpression(obj, prop, pos), rhsExpr, pos))
		}
		if err != nil {
			return true, Value{}, err
		}
		if strings.HasPrefix(prop, "#") {
			e.emitThrowTypeError("'" + ast.PrivateDisplay(prop) + "' was defined without a setter")
		} else {
			e.emitThrowTypeError("Cannot set property " + prop + " of #<" + pureDisplayName(objVal.Ty.ClassName) + "> which has only a getter")
		}
		return true, v, nil
	}

	paramTy := setter.ParamTypes[0]
	var rhs Value
	if op == "=" {
		// Hint-aware (TDD-00028/TDD-00007): `obj.prop = [1,2,3]`/`obj.prop
		// = {...}` coerces against the setter's own declared parameter type.
		rhs, err = e.emitExprWithObjectHint(rhsExpr, paramTy)
		if err != nil {
			return true, Value{}, err
		}
	} else if getter == nil {
		// A setter-only accessor reads undefined: `undefined OP rhs`.
		if rhs, err = e.emitExpr(ast.NewBinaryExpression(strings.TrimSuffix(op, "="), ast.NewNullLiteral(true, pos), rhsExpr, pos)); err != nil {
			return true, Value{}, err
		}
	} else if getter.RetType.IR != paramTy.IR {
		// The getter's and the setter's types differ: `get OP rhs` is an
		// ordinary binary expression, its value then the setter's.
		obj := e.bindValue(objVal, pos)
		if rhs, err = e.emitExpr(ast.NewBinaryExpression(strings.TrimSuffix(op, "="), ast.NewMemberExpression(obj, prop, pos), rhsExpr, pos)); err != nil {
			return true, Value{}, err
		}
	} else {
		cur, err := e.emitClassCall(objVal.Ty, objVal, accessorMethodName("get", prop), nil, pos, false)
		if err != nil {
			return true, Value{}, err
		}
		rhsVal, err := e.emitExpr(rhsExpr)
		if err != nil {
			return true, Value{}, err
		}
		if err := dateCompoundAssignGuard(op, paramTy.IsDate(), rhsVal.Ty.IsDate()); err != nil {
			return true, Value{}, internalAt(pos, "%s", err)
		}
		rhsVal = e.convert(rhsVal, paramTy)
		rhs, err = e.emitArith(strings.TrimSuffix(op, "="), cur, rhsVal, paramTy, pos)
		if err != nil {
			return true, Value{}, err
		}
	}
	rhs = e.convert(rhs, paramTy)
	if _, err := e.emitClassSetterCall(objVal.Ty, objVal, accessorMethodName("set", prop), rhs, pos); err != nil {
		return true, Value{}, err
	}
	return true, rhs, nil
}

// emitLogicalCompoundAssign implements &&=/||=/??= against an lvalue whose
// storage is already resolved to a single ptr+Type pair — the shape shared
// by every assignable form this compiler has (scalar variable, array
// element, object field, static field). The right side is only evaluated
// (and only then stored) down the branch where the operator's own
// short-circuit rule requires it; the other branch leaves the existing
// value at ptr untouched. Reloading from ptr at the merge point yields the
// correct final value either way, so no separate result temporary is
// needed — unlike emitNullCoalesce/emitConditional, which need one since
// their two branches produce a value that was never already sitting in a
// shared memory location. `??=` against a non-ptr-typed location can never
// trigger (mirrors emitNullCoalesce's own "left can never be null" fast
// path for non-ptr types) — the right side is never evaluated, exactly like
// bare `x ?? y`.
func (e *Emitter) emitLogicalCompoundAssign(op, ptr string, ty Type, rhsExpr ast.Expression) (Value, error) {
	curReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", curReg, storageIR(ty), ptr, ty.Align()))
	cur := Value{Ref: curReg, Ty: ty}

	// `??=` can only ever fire against a location whose value may be nullish: a
	// bare `ptr` (which may be null) or a dynamic any-box (whose tag may be
	// null/undefined). Any other scalar type is never nullish, so the right side
	// is never evaluated — exactly like bare `x ?? y`.
	if op == "??=" && ty.IR != irPtr && !ty.IsDynamic() && !isF64Slot(ty) && !isNullableScalar(ty) {
		return cur, nil
	}

	var cond Value
	switch op {
	case "&&=":
		cond = e.toBool(cur)
	case "||=":
		notReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notReg, e.toBool(cur).Ref))
		cond = Value{Ref: notReg, Ty: TypeBool}
	case "??=":
		if ty.IsDynamic() {
			// Nullish when the box tag is null or undefined.
			tag, _ := e.emitUnboxTagPayload(cur)
			isNull := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
			isUndef := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
			nullishReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullishReg, isNull, isUndef))
			cond = Value{Ref: nullishReg, Ty: TypeBool}
			break
		}
		if isF64Slot(ty) {
			cond = Value{Ref: e.emitF64IsUndefined(cur.Ref), Ty: TypeBool}
			break
		}
		if isNullableScalar(ty) {
			// Nullish when the presence bit is clear.
			present, _ := e.nullableScalarAggParts(cur)
			absent := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", absent, present))
			cond = Value{Ref: absent, Ty: TypeBool}
			break
		}
		cond = Value{Ref: e.isAbsentPtr(cur.Ref, cur.Ty), Ty: TypeBool} // a string's nullRef too
	default:
		return Value{}, fmt.Errorf("unknown logical assignment operator '%s'", op)
	}

	storeL := e.freshLabel("logassign.store")
	mergeL := e.freshLabel("logassign.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, storeL, mergeL))

	e.emitLabel(storeL)
	rhs, err := e.emitExpr(rhsExpr)
	if err != nil {
		return Value{}, err
	}
	// convertChecked, not coerce: a right-hand side whose type is incompatible
	// with the location (e.g. `x ??= 1` where x is a null-typed `ptr` and the
	// evolving-any widening is off under --no-any) is a clean compile error, not
	// a `store ptr <double>` (invalid IR).
	rhs, err = e.convertChecked(rhs, ty, rhsExpr.GetPos(), "compound assignment")
	if err != nil {
		return Value{}, err
	}
	e.heapStore(storageIR(ty), rhs.Ref, ptr, ty.Align())
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", result, storageIR(ty), ptr, ty.Align()))
	return Value{Ref: result, Ty: ty}, nil
}

// superPropertyName is the name `super.name` or `super["name"]` reads.
func superPropertyName(x ast.Expression) (string, bool) {
	switch m := x.(type) {
	case *ast.MemberExpression:
		if _, ok := m.Object.(*ast.SuperExpression); ok {
			return m.Property, true
		}
	case *ast.IndexExpression:
		if _, ok := m.Object.(*ast.SuperExpression); ok {
			if k, lit := m.Index.(*ast.StringLiteral); lit {
				return k.Value, true
			}
		}
	}
	return "", false
}

// hostIndexBase reports a host object whose bracket keys are its own
// properties (a Map's, a Date's): neither an array, a string, a primitive
// nor a static object layout.
func hostIndexBase(t Type) bool {
	return hasHandleFlag(t) && !t.IsDynamic() && !t.IsArray() && !t.IsFlatArray() && !t.IsObject() && !t.IsTuple() && !t.Nullable() && !ownlessPrimitive(t) && !t.IsTypedArray() && !t.IsBuffer()
}

// assignTarget is a target under its type assertions (`(x as any) = 1`,
// `x! = 1`, `(o.p satisfies T) = v`): what TypeScript assigns to.
func assignTarget(x ast.Expression) ast.Expression {
	for {
		switch t := x.(type) {
		case *ast.AsExpression:
			x = t.Expr
		case *ast.NonNullExpression:
			x = t.Arg
		default:
			return x
		}
	}
}

func (e *Emitter) emitAssign(ex *ast.AssignmentExpression) (Value, error) {
	if t := assignTarget(ex.Left); t != ex.Left {
		c := *ex
		c.Left = t
		ex = &c
	}
	if me, ok := ex.Left.(*ast.MemberExpression); ok && e.restrictedClassProp(me.Object, me.Property) {
		// The value is computed, then the accessor's setter throws.
		v, err := e.emitExpr(ex.Right)
		if err != nil {
			return Value{}, err
		}
		e.emitThrowTypeError(restrictedPropMessage)
		e.emitLabel(e.freshLabel("restricted.dead"))
		return v, nil
	}
	if v, ok, err := e.emitNullishBaseAssign(ex); ok {
		return v, err
	}
	if len(e.moduleTDZ) > 0 && isNestedAssignPattern(ex.Left) {
		// A pattern's targets in a top-level binding's dead zone.
		ast.Inspect(ex.Left, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.Identifier:
				e.emitModuleTDZCheck(x.Name)
			case *ast.MemberExpression, *ast.IndexExpression, *ast.FunctionExpression, *ast.ArrowFunction:
				return false
			}
			return true
		})
	}
	// `o["#m"] = v` on a class instance stores the property keyed "#m",
	// never the class's private #m.
	if ix, ok := ex.Left.(*ast.IndexExpression); ok {
		if lit, ok := ix.Index.(*ast.StringLiteral); ok && strings.HasPrefix(lit.Value, "#") {
			if t := e.typeOfExpr(ix.Object); t.IsClass() && !t.IsDynamic() {
				pos := ix.GetPos()
				left := ast.NewIndexExpression(ast.NewAsExpression(ix.Object, ast.AnyAnnotation("ts"), pos), ix.Index, pos)
				syn := ast.NewAssignmentExpression(ex.Op, left, ex.Right, ex.GetPos())
				e.originate(syn, ex)
				return e.emitAssign(syn)
			}
		}
	}
	// `super.x = v` stores on the receiver, as [[Set]] through the base's
	// prototype with `this` as the receiver does.
	if name, ok := superPropertyName(ex.Left); ok && len(ex.Op) > 1 && !logicalAssignOp(ex.Op) {
		// `super.x += v`: read through super, store on `this`.
		ex = ast.NewAssignmentExpression(ex.Op, ast.NewIndexExpression(ast.NewSuperExpression(ex.Left.GetPos()), ast.NewStringLiteral(name, ex.Left.GetPos()), ex.Left.GetPos()), ex.Right, ex.GetPos())
	} else if ok {
		syn := ast.NewAssignmentExpression(ex.Op, ast.NewMemberExpression(ast.NewThisExpression(ex.Left.GetPos()), name, ex.Left.GetPos()), ex.Right, ex.GetPos())
		e.originate(syn, ex)
		return e.emitAssign(syn)
	}
	// `super[k] = v`: `this` is resolved before k runs (a derived
	// constructor's ReferenceError before super()), then the store as
	// `super.x = v`'s.
	if ix, ok := ex.Left.(*ast.IndexExpression); ok {
		if _, sup := ix.Object.(*ast.SuperExpression); sup {
			e.emitThisBoundCheck()
			pos := ix.GetPos()
			recv := ast.NewAsExpression(ast.NewThisExpression(pos), ast.AnyAnnotation("ts"), pos)
			if ex.Op == "=" || logicalAssignOp(ex.Op) {
				syn := ast.NewAssignmentExpression(ex.Op, ast.NewIndexExpression(recv, ix.Index, pos), ex.Right, ex.GetPos())
				e.originate(syn, ex)
				return e.emitAssign(syn)
			}
			// A compound one reads through super, once-evaluated key.
			kv, err := e.emitExpr(ix.Index)
			if err != nil {
				return Value{}, err
			}
			key := e.bindValue(kv, pos)
			read := ast.NewIndexExpression(ast.NewSuperExpression(pos), key, pos)
			rhs := ast.NewBinaryExpression(strings.TrimSuffix(ex.Op, "="), read, ex.Right, ex.GetPos())
			syn := ast.NewAssignmentExpression("=", ast.NewIndexExpression(recv, key, pos), rhs, ex.GetPos())
			e.originate(syn, ex)
			return e.emitAssign(syn)
		}
	}
	if id, ok := ex.Left.(*ast.Identifier); ok {
		if m := e.nsSiblingTarget(id); m != nil {
			// A store to a sibling namespace member from a member
			// function's body: its mangled binding.
			syn := ast.NewAssignmentExpression(ex.Op, m, ex.Right, ex.GetPos())
			e.originate(syn, ex)
			return e.emitAssign(syn)
		}
		// A bare `new Map()` / `new Set()` stored to a typed binding builds
		// the binding's own K/V family, as its declaration's does.
		if sym, found := e.lookup(id.Name); found && ex.Op == "=" && e.newCollectionHint == nil {
			defer e.setCollectionHint(sym.Ty, ex.Right)()
		}
	}
	if m, ok := ex.Left.(*ast.MemberExpression); ok && len(ex.Op) > 1 && !logicalAssignOp(ex.Op) && e.shadowableRead(m) {
		// A compound write to a field a write through any may shadow:
		// `(o as any).k = o.k op r`, the read seeing the shadow and the
		// write storing the field or the shadow as the result's kind is.
		anyObj := ast.NewAsExpression(m.Object, ast.AnyAnnotation("ts"), m.GetPos())
		rhs := ast.NewBinaryExpression(strings.TrimSuffix(ex.Op, "="), m, ex.Right, ex.GetPos())
		return e.emitAssign(ast.NewAssignmentExpression("=", ast.NewMemberExpression(anyObj, m.Property, m.GetPos()), rhs, ex.GetPos()))
	}
	// A namespace-member assignment (`X.member = v` / `A.B.member = v`,
	// TDD-00148/ADR-00474) rewrites to an assignment to the desugared flat
	// binding, mirroring the call/value-read dispatch in emitCall/emitMember.
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok && e.isStackTraceLimit(memEx) {
		right := ex.Right
		if ex.Op != "=" {
			right = ast.NewBinaryExpression(strings.TrimSuffix(ex.Op, "="), memEx, ex.Right, ex.GetPos())
		}
		v, err := e.emitExpr(right)
		if err != nil {
			return Value{}, err
		}
		n, err := e.convertChecked(v, TypeF64, ex.GetPos(), "Error.stackTraceLimit")
		if err != nil {
			return Value{}, err
		}
		e.declareFn("__kml_stack_limit_set", "declare void @__kml_stack_limit_set(double)")
		e.emitInstr(fmt.Sprintf("call void @__kml_stack_limit_set(double %s)", n.Ref))
		return n, nil
	}
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		if id, isID := memEx.Object.(*ast.Identifier); isID {
			if members, nsName := e.namespaceMembers(id.Name); members != nil && !e.isShadowedByLocal(id.Name) {
				if exported, present := members[memEx.Property]; present {
					if !exported && e.curNamespace != nsName {
						return Value{}, internalAt(ex.GetPos(), "'%s.%s' is not exported from namespace '%s'", id.Name, memEx.Property, nsName)
					}
					rewritten := ast.NewAssignmentExpression(ex.Op, ast.NewIdentifier(ast.NamespaceMangle(nsName, memEx.Property), ex.GetPos()), ex.Right, ex.GetPos())
					return e.emitAssign(rewritten)
				}
			}
		} else if members, nsName := e.namespaceByChain(memEx.Object); members != nil {
			if exported, present := members[memEx.Property]; present {
				if !exported && e.curNamespace != nsName {
					return Value{}, internalAt(ex.GetPos(), "'%s.%s' is not exported from namespace '%s'", nsName, memEx.Property, nsName)
				}
				rewritten := ast.NewAssignmentExpression(ex.Op, ast.NewIdentifier(ast.NamespaceMangle(nsName, memEx.Property), ex.GetPos()), ex.Right, ex.GetPos())
				return e.emitAssign(rewritten)
			}
		}
	}

	// An element or Array property (`length`) of a class extending Array:
	// written on the instance's array body.
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok && e.isHostArrayMember(memEx) {
		body, _, err := e.hostArrayReceiver(memEx.Object)
		if err != nil {
			return Value{}, err
		}
		var target ast.Expression = ast.NewMemberExpression(body, memEx.Property, memEx.GetPos())
		if _, ok := isArrayIndexName(memEx.Property); ok {
			target = ast.NewIndexExpression(body, ast.NewNumberLiteral(memEx.Property, memEx.GetPos()), memEx.GetPos())
		}
		return e.emitAssign(ast.NewAssignmentExpression(ex.Op, target, ex.Right, ex.GetPos()))
	}

	if ix, ok := ex.Left.(*ast.IndexExpression); ok {
		if vk := e.voidKeyIndex(ix); vk != ix {
			return e.emitAssign(ast.NewAssignmentExpression(ex.Op, vk, ex.Right, ex.GetPos()))
		}
	}
	// A write through a run-time undefined/null base throws; a compound
	// assignment reads first, so it reports the read (emit_nullderef.go).
	switch left := ex.Left.(type) {
	case *ast.MemberExpression:
		undo, err := e.guardBase(left.Object, left.Property, ex.Op == "=")
		if err != nil {
			return Value{}, err
		}
		defer undo()
	case *ast.IndexExpression:
		undo, err := e.guardIndexBase(left.Object, left.Index, ex.Op == "=")
		if err != nil {
			return Value{}, err
		}
		defer undo()
	}

	// Buffer.poolSize = v (or compound): a write to the module variable
	// its declaration lowers to.
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		if r, ok := e.loweredPropertyRead(memEx); ok {
			if id, ok := r.(*ast.Identifier); ok {
				re := *ex
				re.Left = id
				return e.emitAssign(&re)
			}
		}
	}
	// process.title = v (or another member the emitter object holds): a
	// write to that object.
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		if m, ok := e.heldMember(memEx); ok {
			re := *ex
			re.Left = m
			return e.emitAssign(&re)
		}
	}
	// process.env.KEY = v / process.env[key] = v: envSet(key, v); a compound
	// assignment reads the variable first (lib/node/internal_process_methods.ts).
	if call, ok := e.processEnvAssign(ex); ok {
		return e.emitExpr(call)
	}
	// `F.prototype = value` on a recognized vanilla-JS constructor function
	// (TDD-00155 Stage 4, `-compat=js`) re-points the prototype bag.
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		if id, ok := memEx.Object.(*ast.Identifier); ok && e.jsProtoCtor[id.Name] && memEx.Property == "prototype" {
			if ex.Op != "=" {
				// `F.prototype op= v`: F is a plain name, so the read and the
				// store name the same prototype; a logical one stores only
				// when it short-circuits to the right side.
				pos := ex.GetPos()
				op := strings.TrimSuffix(ex.Op, "=")
				var syn ast.Expression
				if logicalAssignOp(ex.Op) {
					syn = ast.NewBinaryExpression(op, memEx, ast.NewAssignmentExpression("=", memEx, ex.Right, pos), pos)
				} else {
					syn = ast.NewAssignmentExpression("=", memEx, ast.NewBinaryExpression(op, memEx, ex.Right, pos), pos)
				}
				return e.emitExpr(syn)
			}
			rhs, err := e.emitExprWithObjectHint(ex.Right, TypeAny)
			if err != nil {
				return Value{}, err
			}
			return e.emitProtoBagWrite(id.Name, rhs, ex.GetPos())
		}
	}
	// Static field assignment: ClassName.staticField = val (or compound
	// ops) — TDD-00009 Stage 4. A bare class-name identifier is a
	// compile-time namespace, never a real runtime value, so this must be
	// checked before memEx.Object is evaluated generically below (same
	// reasoning the read-side check in emitMember follows).
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		if id, ok := memEx.Object.(*ast.Identifier); ok {
			if info, found := e.classes[id.Name]; found {
				return e.emitStaticFieldAssign(info, id.Name, memEx.Property, ex.Op, ex.Right, ex.GetPos())
			}
			if info, name, found := e.genericStaticMember(id, memEx.Property); found {
				return e.emitStaticFieldAssign(info, name, memEx.Property, ex.Op, ex.Right, ex.GetPos())
			}
		}
	}
	// Dynamic object bracket assignment: obj[expr] = val (or compound ops) —
	// a computed-key object literal is a real Map<string,V>, so this must be
	// checked before emitIndexPtr, which only understands array storage.
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		if id, ok := idxEx.Object.(*ast.Identifier); ok {
			if sym, found := e.lookup(id.Name); found && sym.Ty.IsDynamicObject() {
				mapPtr := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", mapPtr, sym.Ptr))
				return e.emitDynamicObjectAssign(sym.Ty, mapPtr, idxEx.Index, ex.Op, ex.Right, ex.GetPos())
			}
		} else if objTy := e.typeOfExpr(idxEx.Object); objTy.IsDynamicObject() {
			objVal, err := e.emitExpr(idxEx.Object)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynamicObjectAssign(objVal.Ty, objVal.Ref, idxEx.Index, ex.Op, ex.Right, ex.GetPos())
		}
	}
	// Bracket assignment on a bare any/unknown base: runtime-keyed write into
	// the D1 dynamic object model (TDD-00155 Stage 1).
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		baseTy := e.typeOfExpr(idxEx.Object)
		// A function's own property, or a computed key into a class
		// instance (classDynIndex): through the boxed value.
		fnObj := baseTy.IsFunc() && !baseTy.IsDynamic() || e.classDynIndex(idxEx) || hostIndexBase(baseTy) || e.errorDynIndex(baseTy, idxEx)
		if isUnconstrainedDynamic(baseTy) || fnObj {
			objVal, err := e.emitExpr(idxEx.Object)
			if err != nil {
				return Value{}, err
			}
			if fnObj {
				// Its boxed function object's bag (TDD-00229), or the boxed
				// instance.
				if objVal, err = e.emitBoxValue(objVal); err != nil {
					return Value{}, err
				}
			}
			keyRef, err := e.dynAnyKeyRef(idxEx.Index, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			if ex.Op != "=" {
				return e.emitDynAnyCompoundAssign(objVal, keyRef, "", ex.Op, ex.Right, ex.GetPos())
			}
			rhs, err := e.emitExprWithObjectHint(ex.Right, e.dynStoreHint(baseTy, ex.Right))
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMemberSet(objVal, keyRef, rhs, ex.GetPos())
		}
	}
	// Tuple element assignment: t[0] = val (TDD-00066), of the element's own
	// type; any other index writes through the array.
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		if tupleTy, ok := e.tupleElemTarget(idxEx); ok {
			return e.emitTupleElemAssign(idxEx, tupleTy, ex.Op, ex.Right)
		}
	}
	// Fixed-object constant-key assignment: `o["a"] = v` / `o[0] = v` on a
	// static-shape object (or class instance) is the same as `o.a = v`
	// (ADR-00608) — rewrite to a member-target assignment so it reuses the
	// accessor / readonly / nullable / frozen machinery unchanged.
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		if objTy := e.typeOfExpr(idxEx.Object); objTy.IsObject() && !objTy.IsArray() && !objTy.IsDynamicObject() {
			if key, ok := constObjectKey(idxEx.Index); ok {
				member := ast.NewMemberExpression(idxEx.Object, key, idxEx.GetPos())
				rewritten := ast.NewAssignmentExpression(ex.Op, member, ex.Right, ex.GetPos())
				return e.emitAssign(rewritten)
			}
		}
	}
	// Array element assignment: arr[i] = val  or  arr[i] += val
	if idxEx, ok := ex.Left.(*ast.IndexExpression); ok {
		if body, ok, err := e.hostArrayReceiver(idxEx.Object); err != nil || ok {
			if err != nil {
				return Value{}, err
			}
			plain := *idxEx
			plain.Object = body
			idxEx = &plain
		}
		// Plain `=` on a growable array supports JS's append-by-index idiom
		// (`arr[arr.length] = v` extends by one) — see
		// emitArrayIndexAssignGrow. Flat @value arrays and TypedArrays keep
		// fixed-bounds semantics; compound ops read the slot first, where
		// out-of-range is genuinely an error.
		if ex.Op == "=" {
			if objTy := e.typeOfExpr(idxEx.Object); objTy.IsArray() && !objTy.IsFlatArray() && !objTy.IsTypedArray() {
				if v, handled, err := e.emitArrayIndexAssignGrow(idxEx, ex.Right); handled {
					return v, err
				}
			}
		}
		if ex.Op != "=" && e.sloppyWrite && !isLogicalAssignOp(ex.Op) && pureArrayRef(idxEx.Object) {
			switch idxEx.Index.(type) {
			case *ast.Identifier, *ast.NumberLiteral:
				// Sloppy code: `a[i] op= v` is `a[i] = a[i] op v` (its parts
				// have no side effects), whose refused write does nothing.
				if t := e.typeOfExpr(idxEx.Object); t.IsArray() && !t.IsTypedArray() && !t.IsFlatArray() {
					op := strings.TrimSuffix(ex.Op, "=")
					return e.emitAssign(ast.NewAssignmentExpression("=", idxEx, ast.NewBinaryExpression(op, idxEx, ex.Right, ex.GetPos()), ex.GetPos()))
				}
			}
		}
		if ex.Op != "=" {
			e.emitArrayGuardFor(idxEx.Object, arrOpSet, idxEx.Index)
		}
		gepReg, elemTy, err := e.emitIndexPtr(idxEx)
		if err != nil {
			return Value{}, err
		}
		// TDD-00101: BigInt64Array/BigUint64Array/Uint8ClampedArray element
		// stores go through the conversion layer (bigint unwrap / ToUint8Clamp)
		// instead of the plain coerce below.
		if taTy := e.typeOfExpr(idxEx.Object); taTy.IsTypedArray() && (taTy.BigIntElem || taTy.Clamped) {
			if ex.Op != "=" {
				if taTy.BigIntElem {
					// Load the raw i64 element, wrap it into a bigint (signed for
					// BigInt64Array, unsigned for BigUint64Array), apply the op via
					// the bigint runtime, then store the unwrapped result.
					e.ensureBigInt()
					curReg := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align %d", curReg, gepReg, elemTy.Align()))
					fromFn := "__kml_bigint_from_i64"
					if !elemTy.Signed {
						fromFn = "__kml_bigint_from_u64" // BigUint64Array
					}
					curBig := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @%s(i64 %s)", curBig, fromFn, curReg))
					rhsVal, err := e.emitExpr(ex.Right)
					if err != nil {
						return Value{}, err
					}
					if !rhsVal.Ty.IsBigInt() && !rhsVal.Ty.IsDynamic() {
						// A bigint element and a number: JavaScript's TypeError.
						e.emitThrowTypeError("Cannot mix BigInt and other types, use explicit conversions")
						e.emitLabel(e.freshLabel("bigelem.mix.dead"))
						return Value{Ref: curBig, Ty: BigIntType()}, nil
					}
					var res Value
					if rhsVal.Ty.IsDynamic() {
						// An any operand: the operator's own run-time rules
						// (a number there is the mixing TypeError).
						cur := e.emitBoxBigInt(Value{Ref: curBig, Ty: BigIntType()})
						res, err = e.emitExpr(ast.NewBinaryExpression(strings.TrimSuffix(ex.Op, "="), e.bindValue(cur, ex.GetPos()), e.bindValue(rhsVal, ex.GetPos()), ex.GetPos()))
					} else {
						res, err = e.emitBigIntBinary(strings.TrimSuffix(ex.Op, "="), Value{Ref: curBig, Ty: BigIntType()}, rhsVal, ex.GetPos())
					}
					if err != nil {
						return Value{}, err
					}
					stored, err := e.toTypedArrayElem(res, taTy, ex.GetPos())
					if err != nil {
						return Value{}, err
					}
					e.storeArrayElem(gepReg, elemTy, stored)
					return res, nil
				}
				// Clamped compound: load the u8, apply the op as a plain
				// number, then clamp-store the result.
				curReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", curReg, elemTy.IR, gepReg, elemTy.Align()))
				cur := Value{Ref: curReg, Ty: elemTy}
				rhsVal, err := e.emitExpr(ex.Right)
				if err != nil {
					return Value{}, err
				}
				res, err := e.emitArith(strings.TrimSuffix(ex.Op, "="), e.convert(cur, TypeI64), e.convert(rhsVal, TypeI64), TypeI64, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				stored, err := e.toTypedArrayElem(res, taTy, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				e.storeArrayElem(gepReg, elemTy, stored)
				return stored, nil
			}
			rhsVal, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			stored, err := e.toTypedArrayElem(rhsVal, taTy, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			e.storeArrayElem(gepReg, elemTy, stored)
			return rhsVal, nil
		}
		if isLogicalAssignOp(ex.Op) {
			return e.emitLogicalCompoundAssign(ex.Op, gepReg, elemTy, ex.Right)
		}
		var rhs Value
		if ex.Op == "=" {
			// Hint-aware (TDD-00028): `arr[i] = [1,2,3]` when elemTy is
			// itself array-typed builds/coerces against elemTy instead of
			// erroring — the same reasoning emitExprWithObjectHint already
			// established for object literals (TDD-00007).
			rhs, err = e.emitExprWithObjectHint(ex.Right, elemTy)
			if err != nil {
				return Value{}, err
			}
		} else {
			// Compound: load current element, apply op, store
			curReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", curReg, elemTy.IR, gepReg, elemTy.Align()))
			cur := Value{Ref: curReg, Ty: elemTy}
			rhsVal, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			if err := dateCompoundAssignGuard(ex.Op, elemTy.IsDate(), rhsVal.Ty.IsDate()); err != nil {
				return Value{}, internalAt(ex.GetPos(), "%s", err)
			}
			rhsVal = e.convert(rhsVal, elemTy)
			rhs, err = e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, elemTy, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
		}
		// convertChecked (not plain coerce): a value that can't convert to the
		// element type — e.g. `const a: number[] = []; a[i] = 'str'` — is a clean
		// compile error, not an aggregate/pointer stored into a scalar slot
		// (invalid IR: "constant expression type mismatch"). ADR-00688. In
		// `-compat=js` the element type is `any`, which every value coerces into
		// (boxes), so this only rejects a genuine strict-mode type mismatch.
		if e.bigIntIntoNumberElem(idxEx.Object, rhs) {
			return rhs, nil
		}
		rhs, err = e.convertChecked(rhs, elemTy, ex.GetPos(), "array-element assignment")
		if err != nil {
			return Value{}, err
		}
		e.storeArrayElem(gepReg, elemTy, rhs)
		return rhs, nil
	}

	// Array variable reassignment: arr = val (the whole array, not
	// arr[i] = val). A separate branch from the generic scalar-variable
	// case below, because an array binding's slot holds a shared header
	// pointer that the whole-array store must rebind (not copy through), unlike
	// the generic scalar path's single "store %s %s, ptr sym.Ptr". Found
	// while wiring TDD-00028's
	// array-literal-as-general-expression fix — a real, pre-existing,
	// unrelated bug (arr = otherArrayVar already failed identically before
	// any array-literal changes, confirmed directly).
	if ident, ok := ex.Left.(*ast.Identifier); ok {
		if sym, found := e.lookup(ident.Name); found && sym.Ty.IsArray() {
			if isLogicalAssignOp(ex.Op) && !sym.IsConst {
				// `xs ??= ys` is `xs ?? (xs = ys)` (and `||=`, `&&=` alike): the
				// name reads twice without side effects.
				op := strings.TrimSuffix(ex.Op, "=")
				return e.emitExpr(ast.NewBinaryExpression(op, ident,
					ast.NewAssignmentExpression("=", ident, ex.Right, ex.GetPos()), ex.GetPos()))
			}
			if ex.Op != "=" {
				return Value{}, internalAt(ex.GetPos(), "compound %s on array variable", ex.Op)
			}
			if sym.IsConst {
				if e.compatJS() {
					return e.emitConstAssignThrow(ex.Right)
				}
				return Value{}, internalAt(ex.GetPos(), "assignment to const %s", ident.Name)
			}
			// `xs = JSON.parse(s)` / `xs = await res.json()` into an array-typed
			// binding projects against the binding's declared element type — the
			// same type-directed deserialization a typed var-decl gets, extended
			// to a bare reassignment (ADR-00571).
			if val, ok, err := e.emitDeclJSONProjection(ex.Right, sym.Ty); ok {
				if err != nil {
					return Value{}, err
				}
				newHeader := e.boxArrayValue(val)
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", newHeader, sym.Ptr))
				return val, nil
			}
			val, err := e.emitExprWithObjectHint(ex.Right, sym.Ty)
			if err != nil {
				return Value{}, err
			}
			// `xs = null` / `xs = undefined` on a `T[] | null` binding: the slot
			// holds no array (the null header). Only this binding changes — an
			// alias keeps the array.
			// A JavaScript binding laid out as an array (a parameter its
			// calls pass arrays) takes null as well.
			if val.Ty.IsNull() && sym.Ty.Nullable() {
				val = e.emitAbsentArrayValue(val.Ty, sym.Ty)
			} else if val.Ty.IsNull() && e.compatJS() {
				// null (nullRef) kept apart from undefined (the null header).
				ref := "null"
				if !val.Ty.IsUndefined() {
					ref = e.nullRef()
				}
				val = Value{Ref: ref, Ty: sym.Ty}
			}
			if !val.Ty.IsArray() {
				return Value{}, internalAt(ex.GetPos(), "cannot assign a non-array value to array variable '%s'", ident.Name)
			}
			// A concrete array into an `any[]` binding (or the reverse): its
			// elements converted, in a copy.
			if conv, ok := e.convertArrayElems(val, sym.Ty); ok {
				val = conv
			}
			// Reference semantics (TDD-00213 Stage 1): if the RHS is an existing
			// header-backed array (`a = b`), rebind this slot to the SAME header
			// cell so `a` and `b` alias — a later `push` through either is visible
			// through the other, and they are `===`. A RHS that produces a NEW array
			// (`a = a.filter(...)`, a literal) rebinds to a fresh
			// header, so this frame gets its own array without clobbering a caller's
			// (an in-place `a.push(...)` still writes through to the caller).
			e.heapStore(irPtr, val.Ref, sym.Ptr, 8)
			return val, nil
		}
	}

	// Object field assignment: obj.field = val  or  pts[i].field = val  (or compound ops)
	if memEx, ok := ex.Left.(*ast.MemberExpression); ok {
		objVal, err := e.emitExpr(memEx.Object)
		if err != nil {
			return Value{}, err
		}
		if objVal.Ty.IsDynamicObject() {
			keyExpr := ast.NewStringLiteral(memEx.Property, memEx.GetPos())
			return e.emitDynamicObjectAssign(objVal.Ty, objVal.Ref, keyExpr, ex.Op, ex.Right, ex.GetPos())
		}
		// Property write on a bare any/unknown base: a runtime tag dispatch
		// into the D1 dynamic object model (TDD-00155 Stage 1).
		// A function's own property (a TypeScript expando on a function
		// declaration): written to its boxed function object's bag
		// (TDD-00229).
		// A write through `(x as any).p`: x is written as an `any` holding it
		// is, a property it lacks added at run time.
		if objVal.Ty.IsFunc() && !objVal.Ty.IsDynamic() || castToAny(memEx.Object) && !objVal.Ty.IsDynamic() {
			boxed, err := e.emitBoxValue(objVal)
			if err != nil {
				return Value{}, err
			}
			objVal = boxed
		}
		if isUnconstrainedDynamic(objVal.Ty) {
			if ex.Op != "=" {
				// Read-modify-write through the runtime dispatch (`this.x *= k`,
				// `o.n ??= 0`).
				return e.emitDynAnyCompoundAssign(objVal, e.internString(memEx.Property), memEx.Property, ex.Op, ex.Right, ex.GetPos())
			}
			rhs, err := e.emitExprWithObjectHint(ex.Right, e.dynStoreHint(objVal.Ty, ex.Right))
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMemberSetNamed(objVal, e.internString(memEx.Property), memEx.Property, rhs, ex.GetPos())
		}
		if objVal.Ty.IsArray() && !objVal.Ty.IsTypedArray() && memEx.Property == "length" && ex.Op == "=" {
			return e.emitArrayLengthAssign(memEx, ex.Right)
		}
		if _, member := e.nativeIfaceMember(objVal.Ty, memEx.Property); member && !objVal.Ty.IsObject() {
			// An interface's own member over a native layout (`Date`).
			box, err := e.emitBoxValue(objVal)
			if err != nil {
				return Value{}, err
			}
			if ex.Op != "=" {
				return e.emitDynAnyCompoundAssign(box, e.internString(memEx.Property), memEx.Property, ex.Op, ex.Right, ex.GetPos())
			}
			rhs, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMemberSetNamed(box, e.internString(memEx.Property), memEx.Property, rhs, ex.GetPos())
		}
		if e.compatJS() && (isStringTy(objVal.Ty) || objVal.Ty.IR == "i1" || objVal.Ty.Float || objVal.Ty.IsInteger() || objVal.Ty.IsSymbol() || objVal.Ty.IsBigInt()) && !objVal.Ty.IsArray() {
			// A property write on a primitive: through the box (refused —
			// a TypeError in strict code, nothing in sloppy code).
			box, err := e.emitBoxValue(objVal)
			if err != nil {
				return Value{}, err
			}
			if ex.Op != "=" {
				return e.emitDynAnyCompoundAssign(box, e.internString(memEx.Property), memEx.Property, ex.Op, ex.Right, ex.GetPos())
			}
			rhs, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMemberSetNamed(box, e.internString(memEx.Property), memEx.Property, rhs, ex.GetPos())
		}
		if objVal.Ty.IsArray() && !objVal.Ty.IsObject() {
			// An array's own named property (`a.foo = 1`): its box keeps it,
			// keyed by the array's header, as a write through `any` does.
			box, err := e.emitBoxValue(objVal)
			if err != nil {
				return Value{}, err
			}
			if ex.Op != "=" {
				return e.emitDynAnyCompoundAssign(box, e.internString(memEx.Property), memEx.Property, ex.Op, ex.Right, ex.GetPos())
			}
			rhs, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			return e.emitDynAnyMemberSetNamed(box, e.internString(memEx.Property), memEx.Property, rhs, ex.GetPos())
		}
		if !objVal.Ty.IsObject() {
			return Value{}, internalAt(ex.GetPos(), "field assignment on non-object")
		}
		// TDD-00030: a class accessor (getter/setter) is checked before the
		// plain-field FieldIndex path below — an accessor-only property
		// name is never a real Field, so FieldIndex would otherwise report
		// "no field" for it. Every non-accessor class, and every non-class
		// object, falls through unchanged.
		if objVal.Ty.IsClass() {
			if handled, result, err := e.tryEmitAccessorAssign(objVal, memEx.Property, ex.Op, ex.Right, ex.GetPos()); err != nil {
				return Value{}, err
			} else if handled {
				return result, nil
			}
		}
		idx, fieldTy, ok := objVal.Ty.FieldIndex(memEx.Property)
		if !ok {
			if info, isHost := e.classes[objVal.Ty.ClassName]; objVal.Ty.IsClass() && isHost && info.HostBase != nil && !isIndexKeyName(memEx.Property) {
				// The host base's property (`lastIndex`), on the instance's
				// body.
				body := e.bindHostBody(objVal, *info.HostBase, memEx.GetPos())
				hostEx := *ex
				hostEx.Left = ast.NewMemberExpression(body, memEx.Property, memEx.GetPos())
				return e.emitAssign(&hostEx)
			}
			// (JavaScript adds a property to any object a fixed layout
			// holds, a library callback's options bag too.)
			if _, member := e.nativeIfaceMember(objVal.Ty, memEx.Property); objVal.Ty.IsClass() || member || objVal.Ty.IsError() || e.compatJS() && objVal.Ty.IsObject() && !objVal.Ty.IsArray() {
				// A property (or method) the instance takes as its own (an
				// Error's, into its bag of further own properties).
				box, err := e.emitBoxValue(objVal)
				if err != nil {
					return Value{}, err
				}
				if ex.Op != "=" {
					return e.emitDynAnyCompoundAssign(box, e.internString(memEx.Property), memEx.Property, ex.Op, ex.Right, ex.GetPos())
				}
				rhs, err := e.emitExpr(ex.Right)
				if err != nil {
					return Value{}, err
				}
				return e.emitDynAnyMemberSetNamed(box, e.internString(memEx.Property), memEx.Property, rhs, ex.GetPos())
			}
			return Value{}, internalAt(ex.GetPos(), "no field '%s'", memEx.Property)
		}
		ownPath := func() (Value, error) {
			if e.sloppyWrite && ex.Op != "=" && !isLogicalAssignOp(ex.Op) && !isNullableScalar(fieldTy) && !isRecordView(objVal.Ty) && pureArrayRef(memEx.Object) {
				// Sloppy code: `o.f op= v` is `o.f = o.f op v` (o has no side
				// effects), whose refused write does nothing.
				op := strings.TrimSuffix(ex.Op, "=")
				return e.emitAssign(ast.NewAssignmentExpression("=", memEx, ast.NewBinaryExpression(op, memEx, ex.Right, ex.GetPos()), ex.GetPos()))
			}
			if e.sloppyWrite && ex.Op == "=" && !isNullableScalar(fieldTy) && !isRecordView(objVal.Ty) {
				// Sloppy code: the value is computed, and a frozen object or a
				// read-only property keeps its old one.
				rhs, err := e.emitExprWithObjectHint(ex.Right, fieldTy)
				if err != nil {
					return Value{}, err
				}
				if !fieldTy.IsArray() && !fieldTy.IsDynamic() {
					rhs = e.convert(rhs, fieldTy)
					if rhs.Ty.IR != fieldTy.IR || rhs.Ty.IsArray() != fieldTy.IsArray() {
						return Value{}, internalAt(ex.GetPos(), "type '%s' is not assignable to type '%s' (field '%s')",
							tsTypeName(rhs.Ty), tsTypeName(fieldTy), memEx.Property)
					}
				}
				refused := e.emitStaticWriteRefused(objVal, memEx.Property)
				if ran, err := e.emitAccessorShadowWrite(objVal.Ref, memEx.Property, rhs); err != nil {
					return Value{}, err
				} else if ran != "false" {
					either := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, refused, ran))
					refused = either
				}
				storeL, skipL := e.freshLabel("sloppyset.store"), e.freshLabel("sloppyset.done")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", refused, skipL, storeL))
				e.emitLabel(storeL)
				e.emitUnshadow(objVal.Ref, memEx.Property)
				gepReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
				e.storeScalarOrNullableField(gepReg, fieldTy, rhs)
				if memEx.Property == "name" && e.isErrorValue(objVal.Ty) {
					e.markErrorNameOwn(objVal)
				}
				if memEx.Property == "cause" && e.isErrorValue(objVal.Ty) {
					e.markErrorCauseOwn(objVal)
				}
				e.emitSetPresence(objVal.Ref, objVal.Ty, memEx.Property, true)
				e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
				e.emitLabel(skipL)
				return rhs, nil
			}
			if ex.Op == "=" && e.accessorKeys()[memEx.Property] {
				// A field an accessor may shadow: the write goes by name,
				// running the setter when one is defined (shape.c).
				rhs, err := e.emitExprWithObjectHint(ex.Right, fieldTy)
				if err != nil {
					return Value{}, err
				}
				if !fieldTy.IsArray() && !fieldTy.IsDynamic() {
					rhs = e.convert(rhs, fieldTy)
				}
				e.emitFrozenCheck(objVal, memEx.Property)
				e.emitReadOnlyCheck(objVal.Ref, objVal.Ty, memEx.Property)
				ob, err := e.emitBoxValue(objVal)
				if err != nil {
					return Value{}, err
				}
				rb, err := e.emitBoxValue(rhs)
				if err != nil {
					return Value{}, err
				}
				if _, err := e.emitDynAnyMemberSetNamed(ob, e.internString(memEx.Property), memEx.Property, rb, ex.GetPos()); err != nil {
					return Value{}, err
				}
				e.emitSetPresence(objVal.Ref, objVal.Ty, memEx.Property, true)
				return rhs, nil
			}
			e.emitFrozenCheck(objVal, memEx.Property)
			e.emitReadOnlyCheck(objVal.Ref, objVal.Ty, memEx.Property)
			e.emitUnshadow(objVal.Ref, memEx.Property)
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, idx))
			// A structural type's value may have another layout (TDD-00233): the
			// member is written into a temporary, then through the layout table.
			view := isRecordView(objVal.Ty)
			var viewForeign string
			if view {
				gepReg, viewForeign = e.emitRecordFieldSlot(objVal, gepReg, fieldTy, memEx.Property, ex.Op != "=")
			}
			finish := func(v Value, err error) (Value, error) {
				if err == nil && view {
					e.emitRecordWriteBack(objVal, gepReg, viewForeign, fieldTy, memEx.Property)
				}
				if err == nil {
					e.emitSetPresence(objVal.Ref, objVal.Ty, memEx.Property, true) // the key now exists
				}
				return v, err
			}
			if isLogicalAssignOp(ex.Op) {
				// A nullable-scalar field's { i1, T } presence-flagged slot needs the
				// dedicated present-bit path (TDD-00064 Stage 3); the generic
				// emitLogicalCompoundAssign reads the whole aggregate as a non-`ptr`
				// value and would no-op `??=` (it can never equal null). Every other
				// field type uses the generic path unchanged.
				if isNullableScalar(fieldTy) {
					return finish(e.emitNullableScalarNullishOrLogicalAssignAt(gepReg, fieldTy, ex.Op, ex.Right))
				}
				return finish(e.emitLogicalCompoundAssign(ex.Op, gepReg, fieldTy, ex.Right))
			}
			// A plain `=` into a nullable-scalar field boxes straight from the RHS
			// expression, preserving a null-valued source lvalue's null-ness
			// (TDD-00064 Stage 3) — reading it back into a Value first would
			// auto-unwrap to a present payload.
			if ex.Op == "=" && isNullableScalar(fieldTy) {
				if err := e.storeScalarOrNullableFieldExpr(gepReg, fieldTy, ex.Right); err != nil {
					return Value{}, err
				}
				return finish(e.loadScalarOrNullableField(gepReg, fieldTy), nil)
			}
			var rhs Value
			if ex.Op == "=" {
				// Hint-aware (TDD-00028/TDD-00007): `obj.field = [1,2,3]`/`obj.field
				// = {...}` coerces against the field's own declared type.
				rhs, err = e.emitExprWithObjectHint(ex.Right, fieldTy)
				if err != nil {
					return Value{}, err
				}
			} else {
				// A nullable-scalar field's current value loads as its { i1, T }
				// aggregate; compound arithmetic runs on the demoted payload (a
				// null reads as its zero, lenient) and re-boxes on store.
				cur := e.loadScalarOrNullableField(gepReg, fieldTy)
				arithTy := fieldTy
				if isNullableScalar(fieldTy) {
					cur = e.nullableScalarPayloadOf(cur)
					arithTy = fieldTy.withoutNullable()
				}
				rhsVal, err := e.emitExpr(ex.Right)
				if err != nil {
					return Value{}, err
				}
				if err := dateCompoundAssignGuard(ex.Op, fieldTy.IsDate(), rhsVal.Ty.IsDate()); err != nil {
					return Value{}, internalAt(ex.GetPos(), "%s", err)
				}
				rhsVal = e.convert(rhsVal, arithTy)
				rhs, err = e.emitArith(strings.TrimSuffix(ex.Op, "="), cur, rhsVal, arithTy, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
			}
			// The field counterpart of the variable-reassignment check below: a value
			// with no conversion to the field's declared type (`p.n = "foo"` into a
			// `number` field, `re.lastIndex = {}`) is tsc's "Type 'string' is not
			// assignable to type 'number'". coerce leaves such a value as it is, and
			// storing it was invalid IR (the variable path rejected it, the field
			// path did not). Aggregate-shaped fields box or re-header whatever they
			// are given in storeScalarOrNullableField, so only a plain slot can
			// mismatch.
			if !fieldTy.IsArray() && !fieldTy.IsDynamic() && !isNullableScalar(fieldTy) {
				rhs = e.convert(rhs, fieldTy)
				if rhs.Ty.IR != fieldTy.IR || rhs.Ty.IsArray() != fieldTy.IsArray() {
					return Value{}, internalAt(ex.GetPos(), "type '%s' is not assignable to type '%s' (field '%s')",
						tsTypeName(rhs.Ty), tsTypeName(fieldTy), memEx.Property)
				}
			}
			e.storeScalarOrNullableField(gepReg, fieldTy, rhs)
			if memEx.Property == "name" && e.isErrorValue(objVal.Ty) {
				e.markErrorNameOwn(objVal)
			}
			if memEx.Property == "cause" && e.isErrorValue(objVal.Ty) {
				e.markErrorCauseOwn(objVal)
			}
			return finish(rhs, nil)
		}
		if tags := e.foreignFieldTags(memEx.Object, objVal.Ty, memEx.Property); tags != nil && !argsHoldFunctionLiteral([]ast.Expression{ex.Right}) {
			return e.emitClassFieldWriteOrForeign(objVal, memEx.Property, ex, tags, ownPath)
		}
		return ownPath()
	}

	// Array destructuring assignment: [a, b] = expr (ADR-00160), with a
	// rest target `[a, ...rest] = expr` (ADR-00161, same independent-copy
	// semantics as the declaration form's own rest — unpackArrayPatternInto,
	// emit_arrays_core.go). A target is a variable, a property, or a nested
	// pattern, with an optional default; a hole skips its position. A
	// pattern position past the source array's length reads as undefined.
	if ex.Op == "=" {
		if _, arr := ex.Left.(*ast.ArrayLiteral); arr || isObjectLiteral(ex.Left) {
			if names := e.undeclaredPatternTargets(ex.Left); len(names) > 0 {
				return e.emitUndeclaredPatternAssign(ex, names)
			}
		}
	}
	if arrLit, ok := ex.Left.(*ast.ArrayLiteral); ok {
		if ex.Op != "=" {
			return Value{}, internalAt(ex.GetPos(), "compound destructuring assignment (the parser rejects it)")
		}
		if st := e.typeOfExpr(ex.Right); (st.IsDynamic() || st.IsGenerator() || st.IsClass() || st.IsObject()) && !st.IsArray() && !st.IsTuple() && !st.IsSymbol() && !patternSuspends(arrLit) {
			if lowered, err := iteratorPatternAssign(arrLit, ex.Right, ex.GetPos()); err == nil {
				return e.emitExpr(lowered)
			}
		}
		// The assignment's value is its right-hand side (`r = [a] = v` is
		// v): read where used, the source is evaluated once into a binding
		// both the destructuring and the result read.
		src := ex.Right
		var result Value
		used := e.assignValueUsed(ex)
		if used {
			v, err := e.emitExpr(ex.Right)
			if err != nil {
				return Value{}, err
			}
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, v.Ty.IR))
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", v.Ty.IR, v.Ref, slot))
			name := "__kml_destr_src" + slot[1:]
			e.define(name, Symbol{Ptr: slot, Ty: v.Ty})
			src = ast.NewIdentifier(name, ex.Right.GetPos())
			result = v
		}
		// A tuple source assigns each position from the tuple's own field
		// (its elements may differ in type), as the declaration form binds.
		if srcTy := e.typeOfExpr(ex.Right); srcTy.IsTuple() && !srcTy.IsDynamic() && !e.arrayIterPatchable() && tupleAssignPattern(arrLit.Elements) {
			if !used {
				v, err := e.emitExpr(ex.Right)
				if err != nil {
					return Value{}, err
				}
				src = e.bindValue(v, ex.Right.GetPos())
				result = v
			}
			for i, el := range arrLit.Elements {
				if h, ok := el.(*ast.NullLiteral); ok && h.Elision {
					continue // a hole skips its position
				}
				if sp, ok := el.(*ast.SpreadElement); ok {
					// The rest: the remaining positions, as an array.
					var rest []ast.Expression
					for j := i; j < len(srcTy.Fields); j++ {
						rest = append(rest, ast.NewIndexExpression(src, ast.NewNumberLiteral(fmt.Sprint(j), el.GetPos()), el.GetPos()))
					}
					if _, err := e.emitExpr(ast.NewAssignmentExpression("=", sp.Arg, ast.NewArrayLiteral(rest, el.GetPos()), el.GetPos())); err != nil {
						return Value{}, err
					}
					break
				}
				target, def := unwrapDestructDefault(el)
				var val ast.Expression = ast.NewIndexExpression(src, ast.NewNumberLiteral(fmt.Sprint(i), el.GetPos()), el.GetPos())
				if def != nil {
					isU := ast.NewBinaryExpression("===", val, ast.NewNullLiteral(true, el.GetPos()), el.GetPos())
					val = ast.NewConditionalExpression(isU, def, ast.NewIndexExpression(src, ast.NewNumberLiteral(fmt.Sprint(i), el.GetPos()), el.GetPos()), el.GetPos())
				}
				if _, err := e.emitExpr(ast.NewAssignmentExpression("=", target, val, el.GetPos())); err != nil {
					return Value{}, err
				}
			}
			if used {
				return result, nil
			}
			return Value{Ty: TypeVoid}, nil
		}
		if srcTy := e.typeOfExpr(src); isNeverIterable(srcTy) {
			// A primitive other than a string (`[,] = sym`): GetIterator
			// throws V8's TypeError.
			v := result
			if !used {
				var err error
				if v, err = e.emitExpr(src); err != nil {
					return Value{}, err
				}
			}
			b, err := e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
			if err := e.emitThrowNotIterableValue(b); err != nil {
				return Value{}, err
			}
			return v, nil
		}
		e.iterTake = assignPatternTake(arrLit.Elements) + 1
		e.iterUse = true
		dataPtr, lenVal, elemTy, err := e.resolveArrayDataPtr(src, ex.GetPos())
		e.iterTake = 0
		if err != nil {
			return Value{}, err
		}
		if err := e.emitDestructAssignArrayElems(dataPtr, lenVal, elemTy, arrLit.Elements, ex.GetPos()); err != nil {
			return Value{}, err
		}
		if used {
			return result, nil
		}
		return Value{Ty: TypeVoid}, nil
	}

	// Object destructuring assignment: ({ x, y } = expr) etc. — see
	// emitDestructAssignObjectProps.
	if objLit, ok := ex.Left.(*ast.ObjectLiteral); ok {
		if ex.Op != "=" {
			return Value{}, internalAt(ex.GetPos(), "compound destructuring assignment (the parser rejects it)")
		}
		if rt := e.typeOfExpr(ex.Right); isUnconstrainedDynamic(rt) || objLit.HasComputedKey() || objPatternHasRest(objLit) ||
			e.compatJS() && (!rt.IsObject() || rt.IsClass() || !objPatternFieldsIn(objLit, rt)) {
			// From a dynamic value, or by a computed key: each target is
			// assigned its property, read off the (boxed) value once.
			return e.emitDynDestructAssign(objLit, ex.Right, ex.GetPos())
		}
		objPtr, objTy, err := e.resolveObjectPtr(ex.Right, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		if err := e.emitDestructAssignObjectProps(objPtr, objTy, objLit.Properties, ex.GetPos()); err != nil {
			return Value{}, err
		}
		// The assignment's value is its right-hand side.
		return Value{Ref: objPtr, Ty: objTy}, nil
	}

	// Scalar variable assignment
	ident, ok := ex.Left.(*ast.Identifier)
	if !ok {
		return Value{}, internalAt(ex.Left.GetPos(), "an assignment target of kind %T", ex.Left)
	}
	if e.selfNameAssigns[ex] {
		return e.emitSelfNameAssign(ex, ident)
	}
	sym, ok := e.lookup(ident.Name)
	if !ok && e.unresolvedReference(ident) || ok && sym.NotDefined {
		// Undeclared (in a parameter default, a name only the body declares).
		return e.emitGlobalPropertyAssign(ex, ident)
	}
	if !ok {
		return Value{}, internalAt(ident.GetPos(), "undefined variable '%s'", ident.Name)
	}
	e.emitModuleTDZCheck(ident.Name)
	if sym.ForwardFlag > 0 {
		e.emitForwardCheck(sym, ident.Name) // a let assigned before its declaration ran
	}

	if sym.SelfName {
		return e.emitSelfNameAssign(ex, ident)
	}
	if sym.IsConst {
		if e.compatJS() && !sym.SelfName {
			return e.emitConstAssignThrow(ex.Right)
		}
		return Value{}, internalAt(ex.GetPos(), "assignment to const %s", ident.Name)
	}

	// A nullable-scalar local stores into its { i1, T } slot (TDD-00064) —
	// intercepted before the bare-scalar load/store path below, which would
	// mis-shape the aggregate. A bare-storage nullable scalar (param/field,
	// Stage 3) is deliberately excluded and keeps the generic path.
	if sym.isNullableScalarLocal() {
		return e.emitNullableScalarAssign(sym, ex)
	}

	if sym.Ty.IsDynamic() && ex.Op != "=" && !isLogicalAssignOp(ex.Op) {
		// Compound assignment dispatches the operator at runtime (TDD-00076
		// A2), as TypeScript allows on `any`.
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", cur, sym.Ptr))
		rhsVal, err := e.emitExprWithObjectHint(ex.Right, TypeAny)
		if err != nil {
			return Value{}, err
		}
		res, err := e.emitAnyBinary(strings.TrimSuffix(ex.Op, "="), Value{Ref: cur, Ty: TypeAny}, rhsVal, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		boxed, err := e.emitBoxValue(res)
		if err != nil {
			return Value{}, err
		}
		if fresh, ok := e.lookup(ident.Name); ok {
			sym = fresh
		}
		e.heapStore("i64", boxed.Ref, sym.Ptr, 8)
		return boxed, nil
	}

	if isLogicalAssignOp(ex.Op) {
		return e.emitLogicalCompoundAssign(ex.Op, sym.Ptr, sym.Ty, ex.Right)
	}

	var rhs Value
	if ex.Op == "=" {
		var err error
		// `obj = JSON.parse(s)` / `obj = await res.json()` into an object-typed
		// binding projects against the binding's declared shape — the bare-
		// reassignment counterpart of the typed var-decl path (ADR-00571).
		if sym.Ty.IsObject() {
			if val, ok, perr := e.emitDeclJSONProjection(ex.Right, sym.Ty); ok {
				if perr != nil {
					return Value{}, perr
				}
				e.heapStore(sym.Ty.IR, val.Ref, sym.Ptr, sym.Ty.Align())
				return val, nil
			}
		}
		// Hint-aware (TDD-00028/TDD-00007): sym.Ty is never array-typed
		// here (that case is handled by its own branch above), so this
		// only matters for an object-literal-typed scalar variable, but
		// costs nothing to route through uniformly.
		rhs, err = e.emitExprWithObjectHint(ex.Right, sym.Ty)
		if err != nil {
			return Value{}, err
		}
	} else {
		loadReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", loadReg, sym.Ty.IR, sym.Ptr, sym.Ty.Align()))
		cur := Value{Ref: loadReg, Ty: sym.Ty}

		rhsVal, err := e.emitExpr(ex.Right)
		if err != nil {
			return Value{}, err
		}
		if err := dateCompoundAssignGuard(ex.Op, sym.Ty.IsDate(), rhsVal.Ty.IsDate()); err != nil {
			return Value{}, internalAt(ex.GetPos(), "%s", err)
		}
		op := strings.TrimSuffix(ex.Op, "=")
		if (op != "+" || !isStringTy(sym.Ty) || hasHandleFlag(sym.Ty)) && !(sym.Ty.IsBigInt() && !rhsVal.Ty.IsBigInt()) {
			// (`s += n` concatenates n's string form, in emitArith; a
			// bigint target beside another type throws there.)
			rhsVal = e.convert(rhsVal, sym.Ty)
		}
		rhs, err = e.emitArith(op, cur, rhsVal, sym.Ty, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
	}

	if sym.Ty.IsDynamic() {
		var err error
		rhs, err = e.emitBoxValueWidened(rhs, ex.Right)
		if err != nil {
			return Value{}, err
		}
	} else {
		rhs = e.convert(rhs, sym.Ty)
		// A binding's type is fixed at its declaration (annotated, or inferred
		// from its initializer / first assignment). Assigning an incompatible
		// type (`let x = 5; x = 'hi'`, or an untyped `let x; x = 1; x = 's'`)
		// leaves rhs with a storage IR that differs from the slot's — a bare
		// `store <sym.IR> <rhs>` that clang rejects as invalid. Reject cleanly
		// instead, matching TypeScript (which infers the binding's type and
		// errors on the incompatible assignment) and this compiler's two-tier
		// rule: a variable that genuinely holds different types must be
		// annotated `: any` (works today) or compiled with `-compat=js`. The
		// composite/aggregate targets (array/object/dynamic/nullable) all
		// return from their own branches above, so this scalar-store site is
		// the only place an IR mismatch can reach the emitter.
		//
		// An array Value reports Type.IR irPtr but is really a header pointer
		// (an array-typed sym takes its own store branch far
		// above; this else branch only runs for a non-array slot). A bare `.IR`
		// comparison therefore accepts an array RHS into a `ptr`-shaped object /
		// string / handle slot, storing a header where an object or string is
		// expected. Reject that shape divergence
		// too (e.g. `var x = {}; x = []` in strict, ADR-00933).
		// A binding that is `undefined` because its initializer produced no value
		// (`let c = log()`, ADR-00479) takes another no-value result the same way:
		// the call has run for its effects, and the binding still reads `undefined`.
		if rhs.Ty.IR == "void" && sym.Ty.IsUndefined() {
			return Value{Ref: "null", Ty: TypeUndefined}, nil
		}
		if rhs.Ty.IR != sym.Ty.IR || rhs.Ty.IsArray() != sym.Ty.IsArray() {
			describe := func(t Type) string {
				switch {
				case t.IsArray():
					return "array"
				case t.IsSymbol():
					return "symbol"
				case t.IsObject():
					return "object"
				}
				if k := scalarTypeKind(t); k != "" {
					return k
				}
				return t.IR
			}
			return Value{}, internalAt(ex.GetPos(), "cannot assign a %s value to '%s' (a %s) — a variable's type is fixed at its declaration; annotate it `: any` for a value that holds different types, or compile with -compat=js", describe(rhs.Ty), pureDisplayName(ident.Name), describe(sym.Ty))
		}
	}
	// Evaluating the RHS may have promoted `ident` to a heap cell for closure
	// capture (updateSymbolInPlace changes its storage pointer) — re-resolve it so
	// the store targets the cell the closure now reads. Fixes the self-referential
	// `id = setInterval(() => { ... clearInterval(id) ... }, ...)`, where the
	// closure captured id during RHS evaluation and the assignment otherwise wrote
	// to the stale pre-promotion slot, leaving the closure's id at its old value.
	if fresh, ok := e.lookup(ident.Name); ok {
		sym = fresh
	}
	// Rebind-free (-mm=auto): a plan-approved churned binding frees the value
	// being overwritten — RHS is already evaluated, so this is the last
	// point the old allocation is reachable.
	if e.isAutoMode() {
		e.maybeFreeOnRebind(ident.Name, sym, ex.GetPos())
	}
	e.heapStore(sym.Ty.IR, rhs.Ref, sym.Ptr, sym.Ty.Align())
	return rhs, nil
}

// --- Destructuring-assignment recursion (ADR-00160 successor) ---
//
// The assignment form `[a, [b]] = e` / `({ x, y: { z } } = e)` reaches parity
// with the declaration form's nesting (unpack*PatternInto): every target is an
// already-declared variable, and a target position may itself be a nested array
// or object pattern. The source-element load and out-of-bounds handling mirror
// unpackNestedArrayElem exactly (OOB reads a deterministic zero / empty).

// emitDestructAssignArrayElems assigns each position of `elems` (a
// destructuring-assignment array pattern) from the source array at
// (dataPtr, lenVal, elemTy). A trailing `...rest` collects the remainder.
func (e *Emitter) emitDestructAssignArrayElems(dataPtr, lenVal string, elemTy Type, elems []ast.Expression, pos ast.Pos) error {
	for i, elemExpr := range elems {
		if spread, isSpread := elemExpr.(*ast.SpreadElement); isSpread {
			if i != len(elems)-1 {
				return internalAt(spread.GetPos(), "rest target not last")
			}
			id, plain := spread.Arg.(*ast.Identifier)
			var sym Symbol
			if plain {
				var found bool
				if sym, found = e.lookup(id.Name); !found {
					return internalAt(spread.Arg.GetPos(), "undefined variable '%s'", id.Name)
				}
				if sym.IsConst {
					if e.compatJS() {
						e.emitConstTargetThrow()
						return nil
					}
					return internalAt(spread.Arg.GetPos(), "assignment to const %s", id.Name)
				}
				plain = sym.Ty.IsArray()
			}
			rawLen, isNegLen, restLen := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %d", rawLen, lenVal, i))
			e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNegLen, rawLen))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", restLen, isNegLen, rawLen))
			byteCount, newPtr := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteCount, restLen, elemTy.Align()))
			e.heapMallocInto(newPtr, elemAllocKind(elemTy), byteCount)
			srcGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", srcGep, elemTy.IR, dataPtr, i))
			e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", newPtr, srcGep, byteCount))
			restHeader := e.newArrayHeader(newPtr, restLen)
			if plain {
				e.heapStore(irPtr, restHeader, sym.Ptr, 8)
				break
			}
			// Any other target (a property, a nested pattern, a variable
			// of another type): the rest array, assigned to it through a
			// hidden any.
			boxed, err := e.emitBoxValue(Value{Ref: restHeader, Ty: ArrayOf(elemTy)})
			if err != nil {
				return err
			}
			hidden, err := e.declareHiddenAny(pos)
			if err != nil {
				return err
			}
			hs, _ := e.lookup(hidden)
			e.heapStore(hs.Ty.IR, boxed.Ref, hs.Ptr, hs.Ty.Align())
			if _, err := e.emitExpr(ast.NewAssignmentExpression("=", spread.Arg, ast.NewIdentifier(hidden, pos), pos)); err != nil {
				return err
			}
			break
		}

		if h, ok := elemExpr.(*ast.NullLiteral); ok && h.Elision {
			continue // a hole skips its element
		}
		target, def := unwrapDestructDefault(elemExpr)
		_, arrPat := target.(*ast.ArrayLiteral)
		_, objPat := target.(*ast.ObjectLiteral)
		mismatch := arrPat && (!elemTy.IsArray() || elemTy.ElemType == nil) || objPat && !elemTy.IsObject()
		if (def != nil || mismatch) && isNestedAssignPattern(target) {
			// `[[a] = d] = src`: the element (or d) under a hidden any,
			// then assigned into the nested pattern.
			hidden, err := e.declareHiddenAny(pos)
			if err != nil {
				return err
			}
			if err := e.destructAssignArrayLeaf(dataPtr, lenVal, elemTy, i, ast.NewIdentifier(hidden, pos), def); err != nil {
				return err
			}
			if _, err := e.emitExpr(ast.NewAssignmentExpression("=", target, ast.NewIdentifier(hidden, pos), pos)); err != nil {
				return err
			}
			continue
		}
		switch t := target.(type) {
		case *ast.ArrayLiteral:
			if !elemTy.IsArray() || elemTy.ElemType == nil {
				return internalAt(t.GetPos(), "array pattern over a %s element", elemTy.IR)
			}
			subPtr, subLen := e.destructLoadNestedArray(dataPtr, lenVal, elemTy, i)
			if err := e.emitDestructAssignArrayElems(subPtr, subLen, *elemTy.ElemType, t.Elements, pos); err != nil {
				return err
			}
		case *ast.ObjectLiteral:
			if !elemTy.IsObject() {
				return internalAt(t.GetPos(), "object pattern over a %s element", elemTy.IR)
			}
			subObj := e.destructLoadNestedObject(dataPtr, lenVal, elemTy, i)
			if err := e.emitDestructAssignObjectProps(subObj, elemTy, t.Properties, pos); err != nil {
				return err
			}
		case *ast.Identifier:
			if err := e.destructAssignArrayLeaf(dataPtr, lenVal, elemTy, i, t, def); err != nil {
				return err
			}
		case *ast.MemberExpression, *ast.IndexExpression:
			// `[o.a, arr[0]] = src`: the element through a hidden local of its
			// type, then stored to the target as any assignment is.
			hid := e.hiddenLocal(elemTy, target.GetPos())
			if err := e.destructAssignArrayLeaf(dataPtr, lenVal, elemTy, i, hid, def); err != nil {
				return err
			}
			if _, err := e.emitExpr(ast.NewAssignmentExpression("=", target, hid, target.GetPos())); err != nil {
				return err
			}
		default:
			return internalAt(elemExpr.GetPos(), "pattern target %T", elemExpr)
		}
	}
	return nil
}

// emitNullishBaseAssign is an assignment through a base that is always
// null or undefined (`var base = null; base[k] = v`): the base and the key
// evaluated, then, for `=`, the right-hand side, and the TypeError V8
// throws (a compound assignment reads first, so it throws before the
// right-hand side). The key is not converted to a property key.
func (e *Emitter) emitNullishBaseAssign(ex *ast.AssignmentExpression) (Value, bool, error) {
	var base, key ast.Expression
	name := ""
	switch l := ex.Left.(type) {
	case *ast.IndexExpression:
		if l.Optional {
			return Value{}, false, nil
		}
		base, key = l.Object, l.Index
		if k, ok := indexPropName(l.Index); ok {
			name = k
		}
	case *ast.MemberExpression:
		if l.Optional {
			return Value{}, false, nil
		}
		base, name = l.Object, l.Property
	default:
		return Value{}, false, nil
	}
	if !e.compatJS() {
		return Value{}, false, nil // (TypeScript rejects it: TS18050)
	}
	switch base.(type) {
	case *ast.Identifier, *ast.NullLiteral:
	default:
		return Value{}, false, nil // (an element or member: its type is no proof)
	}
	bt := e.typeOfExpr(base)
	if !bt.IsNull() && !bt.IsUndefined() {
		return Value{}, false, nil
	}
	what := "null"
	if bt.IsUndefined() {
		what = "undefined"
	}
	if _, err := e.emitExpr(base); err != nil {
		return Value{}, true, err
	}
	if key != nil {
		if _, err := e.emitExpr(key); err != nil {
			return Value{}, true, err
		}
	}
	msg := "Cannot read properties of " + what
	if name != "" {
		msg += " (reading '" + name + "')"
	}
	if ex.Op == "=" {
		if _, err := e.emitExpr(ex.Right); err != nil {
			return Value{}, true, err
		}
		msg = "Cannot set properties of " + what
		if name != "" {
			msg += " (setting '" + name + "')"
		}
	}
	if !e.blockDone {
		e.emitThrowTypeError(msg)
	}
	e.emitLabel(e.freshLabel("nullbase.dead"))
	return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, true, nil
}

// emitConstAssignThrow evaluates an assignment's right-hand side, then
// throws the TypeError assigning a const binding throws.
func (e *Emitter) emitConstAssignThrow(rhs ast.Expression) (Value, error) {
	if _, err := e.emitExpr(rhs); err != nil {
		return Value{}, err
	}
	if !e.blockDone {
		e.emitThrowTypeError("Assignment to constant variable.")
	}
	e.emitLabel(e.freshLabel("const.dead"))
	return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
}

// patternTargetsConst reports a const binding among an assignment
// pattern's identifier targets.
func (e *Emitter) patternTargetsConst(pat ast.Expression) bool {
	found := false
	ast.Inspect(pat, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Identifier:
			if sym, ok := e.lookup(x.Name); ok && sym.IsConst && !sym.SelfName {
				found = true
			}
		case *ast.MemberExpression, *ast.IndexExpression, *ast.FunctionExpression, *ast.ArrowFunction:
			return false
		case *ast.AssignmentExpression:
			ast.Inspect(x.Left, func(m ast.Node) bool {
				if id, ok := m.(*ast.Identifier); ok {
					if sym, ok := e.lookup(id.Name); ok && sym.IsConst && !sym.SelfName {
						found = true
					}
				}
				switch m.(type) {
				case *ast.MemberExpression, *ast.IndexExpression:
					return false
				}
				return true
			})
			return false // (a default's own expression is no target)
		}
		return !found
	})
	return found
}

// objPatternHasRest reports an object assignment pattern with a rest
// property (`{ a, ...rest } = o`).
func objPatternHasRest(lit *ast.ObjectLiteral) bool {
	for _, p := range lit.Properties {
		if _, ok := p.Value.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}

// objPatternFieldsIn reports whether every key an object assignment
// pattern names is a field of the object type t.
func objPatternFieldsIn(lit *ast.ObjectLiteral, t Type) bool {
	for _, p := range lit.Properties {
		if _, _, ok := t.FieldIndex(p.Key); !ok {
			return false
		}
	}
	return true
}

// emitDestructAssignObjectProps assigns each property of `props` (an
// object-destructuring-assignment pattern) from the source object at objPtr.
func (e *Emitter) emitDestructAssignObjectProps(objPtr string, objTy Type, props []ast.ObjectProperty, pos ast.Pos) error {
	structIR := objTy.StructIR()
	for _, prop := range props {
		if _, isSpread := prop.Value.(*ast.SpreadElement); isSpread {
			return internalAt(pos, "a rest target ('...') is not supported in a destructuring assignment")
		}
		idx, fieldTy, ok := objTy.FieldIndex(prop.Key)
		if !ok {
			return internalAt(pos, "object has no field '%s'", prop.Key)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gepReg, structIR, objPtr, idx))

		target, def := unwrapDestructDefault(prop.Value)
		_, idTarget := target.(*ast.Identifier)
		boxedDefault := idTarget && def != nil && fieldTy.IsUndefined() && !(fieldTy.Nullable() && fieldTy.IR == irPtr) && !fieldTy.IsArray()
		_, arrPat := target.(*ast.ArrayLiteral)
		_, objPat := target.(*ast.ObjectLiteral)
		mismatch := arrPat && (!fieldTy.IsArray() || fieldTy.ElemType == nil) || objPat && !fieldTy.IsObject()
		if def != nil && (isNestedAssignPattern(target) || boxedDefault) || mismatch {
			// `{ k: [a] = d } = src`: field k (or d, when undefined) under a
			// hidden any, then assigned into the nested pattern; so is a
			// binding target of a field held as no nullable reference
			// (`{ x = 1 } = { x: undefined }`).
			hidden, err := e.declareHiddenAny(pos)
			if err != nil {
				return err
			}
			var fv Value
			if fieldTy.IsArray() {
				fv = e.loadArrayFieldValue(gepReg, fieldTy)
			} else {
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", r, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
				fv = Value{Ref: r, Ty: fieldTy}
			}
			bv, err := e.emitBoxValue(fv)
			if err != nil {
				return err
			}
			hid := ast.NewIdentifier(hidden, pos)
			isU := ast.NewBinaryExpression("===", e.bindValue(bv, pos), ast.NewNullLiteral(true, pos), pos)
			var pick ast.Expression = e.bindValue(bv, pos)
			if def != nil {
				pick = ast.NewConditionalExpression(isU, def, pick, pos)
			}
			if _, err := e.emitExpr(ast.NewAssignmentExpression("=", hid, pick, pos)); err != nil {
				return err
			}
			if _, err := e.emitExpr(ast.NewAssignmentExpression("=", target, ast.NewIdentifier(hidden, pos), pos)); err != nil {
				return err
			}
			continue
		}
		switch t := target.(type) {
		case *ast.ArrayLiteral:
			if !fieldTy.IsArray() || fieldTy.ElemType == nil {
				return internalAt(pos, "array pattern over field %s", prop.Key)
			}
			subPtr, subLen := e.arrayParts(e.loadArrayFieldValue(gepReg, fieldTy))
			if err := e.emitDestructAssignArrayElems(subPtr, subLen, *fieldTy.ElemType, t.Elements, pos); err != nil {
				return err
			}
		case *ast.ObjectLiteral:
			if !fieldTy.IsObject() {
				return internalAt(pos, "object pattern over field %s", prop.Key)
			}
			subObj := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", subObj, gepReg))
			if err := e.emitDestructAssignObjectProps(subObj, fieldTy, t.Properties, pos); err != nil {
				return err
			}
		case *ast.Identifier, *ast.MemberExpression, *ast.IndexExpression:
			if id, ok := t.(*ast.Identifier); ok {
				sym, found := e.lookup(id.Name)
				if !found {
					return internalAt(id.GetPos(), "undeclared pattern target %s", id.Name)
				}
				if sym.IsConst {
					if e.compatJS() {
						e.emitConstTargetThrow()
						continue
					}
					return internalAt(id.GetPos(), "assignment to const %s", id.Name)
				}
			}
			// `({ k: o.a } = src)`: field k stored to the target as any
			// assignment is (a default, when undefined, in its place).
			var fv Value
			if fieldTy.IsArray() {
				fv = e.loadArrayFieldValue(gepReg, fieldTy)
			} else {
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", r, StructFieldIR(fieldTy), gepReg, fieldTy.Align()))
				fv = Value{Ref: r, Ty: fieldTy}
			}
			var src ast.Expression = e.bindValue(fv, t.GetPos())
			if def != nil && !fieldTy.IsUndefined() && fieldTy.Nullable() && fieldTy.IR == irPtr {
				// An optional reference field's absent value is its null slot.
				// (null itself is a value: a distinct reference.)
				isN := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isN, fv.Ref))
				src = ast.NewConditionalExpression(e.bindValue(Value{Ref: isN, Ty: TypeBool}, t.GetPos()), def, src, t.GetPos())
			} else if def != nil && fieldTy.IsUndefined() {
				isU := ast.NewBinaryExpression("===", src, ast.NewNullLiteral(true, t.GetPos()), t.GetPos())
				src = ast.NewConditionalExpression(isU, def, src, t.GetPos())
			}
			if _, err := e.emitExpr(ast.NewAssignmentExpression("=", target, src, t.GetPos())); err != nil {
				return err
			}
		default:
			return internalAt(pos, "pattern target %T", prop.Value)
		}
	}
	return nil
}

// destructAssignArrayLeaf assigns the source array's element i into the
// already-declared scalar/pointer variable `id` (out-of-bounds reads a
// deterministic zero, the same convention the declaration form uses).
func (e *Emitter) destructAssignArrayLeaf(dataPtr, lenVal string, elemTy Type, i int, id *ast.Identifier, def ast.Expression) error {
	sym, found := e.lookup(id.Name)
	if !found {
		return internalAt(id.GetPos(), "undeclared pattern target %s", id.Name)
	}
	if sym.IsConst {
		if e.compatJS() {
			e.emitConstTargetThrow()
			return nil
		}
		return internalAt(id.GetPos(), "assignment to const %s", id.Name)
	}
	if sym.Ty.IsArray() {
		// An array target: the element (or the default, or undefined past
		// the end) assigned as any assignment to it is.
		inBounds := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBounds, i, lenVal))
		okL, oobL, afterL := e.freshLabel("destrassign.ok"), e.freshLabel("destrassign.oob"), e.freshLabel("destrassign.after")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))
		e.emitLabel(okL)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", gepReg, elemTy.IR, dataPtr, i))
		var src ast.Expression = e.bindValue(e.loadArrayElem(gepReg, elemTy), id.GetPos())
		if def != nil && elemTy.IsDynamic() {
			isU := ast.NewBinaryExpression("===", src, ast.NewNullLiteral(true, id.GetPos()), id.GetPos())
			src = ast.NewConditionalExpression(isU, def, src, id.GetPos())
		}
		if _, err := e.emitExpr(ast.NewAssignmentExpression("=", id, src, id.GetPos())); err != nil {
			return err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
		e.emitLabel(oobL)
		if def != nil {
			if _, err := e.emitExpr(ast.NewAssignmentExpression("=", id, def, id.GetPos())); err != nil {
				return err
			}
		} else {
			// Past the end: undefined, the slot's absent (null) header.
			e.heapStore(irPtr, "null", sym.Ptr, 8)
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
		e.emitLabel(afterL)
		return nil
	}
	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBounds, i, lenVal))
	okL, oobL, afterL := e.freshLabel("destrassign.ok"), e.freshLabel("destrassign.oob"), e.freshLabel("destrassign.after")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))
	e.emitLabel(okL)
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", gepReg, elemTy.IR, dataPtr, i))
	loaded := e.loadArrayElem(gepReg, elemTy)
	if def != nil && elemTy.IsDynamic() {
		// An undefined element takes the default as a missing one does.
		isU, haveL := e.freshReg(), e.freshLabel("destrassign.have")
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, loaded.Ref, nbUndefined))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isU, oobL, haveL))
		e.emitLabel(haveL)
	}
	okVal := e.convert(loaded, sym.Ty)
	// A nullable scalar's slot is its { i1, T } storage.
	stIR, stAlign := storageIR(sym.Ty), storageAlign(sym.Ty)
	e.heapStore(stIR, okVal.Ref, sym.Ptr, stAlign)
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	// Out of bounds: the array-pattern default fires here (the length is the
	// presence signal, so no nullable-reference requirement — unlike an object
	// field), else a deterministic zero.
	e.emitLabel(oobL)
	if def != nil {
		defVal, err := e.emitExpr(def)
		if err != nil {
			return err
		}
		defVal = e.convert(defVal, sym.Ty)
		e.heapStore(stIR, defVal.Ref, sym.Ptr, stAlign)
	} else if isNullableScalar(sym.Ty) {
		// Past the end is undefined: the slot's absent state.
		e.heapStore(stIR, "zeroinitializer", sym.Ptr, stAlign)
	} else if isF64Slot(sym.Ty) {
		e.heapStore("i64", fmt.Sprint(undefF64), sym.Ptr, 8) // the double slot's undefined (TDD-00241)
	} else if sym.Ty.IsDynamic() && stIR == "i64" {
		e.heapStore("i64", fmt.Sprint(nbUndefined), sym.Ptr, 8)
	} else {
		e.heapStore(stIR, sym.Ty.zeroLiteral(), sym.Ptr, stAlign)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(afterL)
	return nil
}

// destructLoadNestedArray loads source element i as a nested array's (ptr, len),
// OOB → (null, 0). Mirrors unpackNestedArrayElem's SubArray branch.
func (e *Emitter) destructLoadNestedArray(dataPtr, lenVal string, elemTy Type, i int) (string, string) {
	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBounds, i, lenVal))
	okL, oobL, afterL := e.freshLabel("destrn.ok"), e.freshLabel("destrn.oob"), e.freshLabel("destrn.after")
	subPtrA, subLenA := e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", subPtrA))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", subLenA))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))
	e.emitLabel(okL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", gep, elemTy.IR, dataPtr, i))
	val := e.loadArrayElem(gep, elemTy)
	p, l := e.freshReg(), e.freshReg()
	p = e.arrayData(val)
	l = e.arrayLen(val)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", p, subPtrA))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", l, subLenA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(oobL)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", subPtrA))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", subLenA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(afterL)
	subPtr, subLen := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", subPtr, subPtrA))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", subLen, subLenA))
	return subPtr, subLen
}

// destructLoadNestedObject loads source element i as a nested object pointer,
// OOB → a zeroed stand-in object (so the recursive unpack never derefs null).
func (e *Emitter) destructLoadNestedObject(dataPtr, lenVal string, elemTy Type, i int) string {
	inBounds := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ult i64 %d, %s", inBounds, i, lenVal))
	okL, oobL, afterL := e.freshLabel("destrno.ok"), e.freshLabel("destrno.oob"), e.freshLabel("destrno.after")
	subObjA := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", subObjA))
	zeroObj := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", zeroObj, elemTy.StructIR()))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inBounds, okL, oobL))
	e.emitLabel(okL)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i64 %d", gep, elemTy.IR, dataPtr, i))
	objp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", objp, gep))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", objp, subObjA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(oobL)
	e.emitInstr(fmt.Sprintf("store %s zeroinitializer, "+irPtr+" %s, align 8", elemTy.StructIR(), zeroObj))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", zeroObj, subObjA))
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
	e.emitLabel(afterL)
	subObj := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", subObj, subObjA))
	return subObj
}

// unwrapDestructDefault splits a `target = default` assignment-pattern element
// into (target, default); a plain target returns (target, nil).
func unwrapDestructDefault(elem ast.Expression) (ast.Expression, ast.Expression) {
	if ae, ok := elem.(*ast.AssignmentExpression); ok && ae.Op == "=" {
		return ae.Left, ae.Right
	}
	return elem, nil
}

// emitDynAnyCompoundAssign is `o[k] op= rhs` on an any base: an arithmetic
// op reads, combines and writes; a logical one (`&&=`, `||=`, `??=`) writes
// only when the current value says so, and is the current value otherwise.
func (e *Emitter) emitDynAnyCompoundAssign(objVal Value, keyRef, propName, op string, rhsExpr ast.Expression, pos ast.Pos) (Value, error) {
	cur, err := e.emitDynAnyMemberGetNamed(objVal, keyRef, propName, pos)
	if err != nil {
		return Value{}, err
	}
	if !isLogicalAssignOp(op) {
		rhsVal, err := e.emitExprWithObjectHint(rhsExpr, TypeAny)
		if err != nil {
			return Value{}, err
		}
		res, err := e.emitAnyBinary(strings.TrimSuffix(op, "="), cur, rhsVal, pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitDynAnyMemberSetNamed(objVal, keyRef, propName, res, pos)
	}
	var assign string
	switch op {
	case "&&=":
		assign = e.emitAnyTruthy(cur).Ref
	case "||=":
		t := e.emitAnyTruthy(cur).Ref
		assign = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", assign, t))
	default: // ??=
		tag, _ := e.emitUnboxTagPayload(cur)
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
		isUndef := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
		assign = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", assign, isNull, isUndef))
	}
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", cur.Ref, slot))
	setL := e.freshLabel("dynlassign.set")
	doneL := e.freshLabel("dynlassign.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", assign, setL, doneL))
	e.emitLabel(setL)
	rhsVal, err := e.emitExprWithObjectHint(rhsExpr, TypeAny)
	if err != nil {
		return Value{}, err
	}
	res, err := e.emitDynAnyMemberSetNamed(objVal, keyRef, propName, rhsVal, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", res.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// processEnvAssign is an assignment to `process.env.KEY` / `process.env[key]`
// as envSet(key, value), `op=` reading envGet(key) first.
func (e *Emitter) processEnvAssign(ex *ast.AssignmentExpression) (ast.Expression, bool) {
	var key ast.Expression
	switch l := ex.Left.(type) {
	case *ast.MemberExpression:
		if !e.isProcessEnvExpr(l.Object) {
			return nil, false
		}
		key = ast.NewStringLiteral(l.Property, l.GetPos())
	case *ast.IndexExpression:
		if !e.isProcessEnvExpr(l.Object) {
			return nil, false
		}
		key = l.Index
	default:
		return nil, false
	}
	value := ex.Right
	if ex.Op != "=" {
		op := strings.TrimSuffix(ex.Op, "=")
		if op == "&&" || op == "||" || op == "??" {
			return nil, false
		}
		cur, ok := e.processEnvCall("envGet", ex.GetPos(), key)
		if !ok {
			return nil, false
		}
		value = ast.NewBinaryExpression(op, cur, ex.Right, ex.GetPos())
	}
	return e.processEnvCall("envSet", ex.GetPos(), key, value)
}

// castToAny reports `x as any` / `x as unknown` (parenthesized or not).
func castToAny(x ast.Expression) bool {
	as, ok := x.(*ast.AsExpression)
	return ok && (annotIs(as.TypeAnnot, "any") || annotIs(as.TypeAnnot, "unknown"))
}

// emitDynDestructAssign is `({ a, b: [c], d = 1, [k]: e } = v)` for a v held
// in `any`: v is evaluated once into a temporary, and each target is assigned
// that property of it, its default when the property reads undefined; a
// nested pattern target is itself a destructuring assignment. The value is v.
func (e *Emitter) emitDynDestructAssign(pat *ast.ObjectLiteral, rhs ast.Expression, pos ast.Pos) (Value, error) {
	v, err := e.emitExprWithObjectHint(rhs, TypeAny)
	if err != nil {
		return Value{}, err
	}
	src, err := e.emitBoxValue(v)
	if err != nil {
		return Value{}, err
	}
	temp := func(word string) *ast.Identifier { return e.anyTemp(word, pos) }
	srcID := temp(src.Ref)
	var named []ast.Expression // the keys read so far, which a rest leaves out
	for _, p := range pat.Properties {
		if sp, rest := p.Value.(*ast.SpreadElement); rest {
			rv, err := e.emitAnyObjRestExcluding(src, named, pos)
			if err != nil {
				return Value{}, err
			}
			if _, err := e.emitExpr(ast.NewAssignmentExpression("=", sp.Arg, temp(rv.Ref), pos)); err != nil {
				return Value{}, err
			}
			continue
		}
		var read ast.Expression = ast.NewMemberExpression(srcID, p.Key, pos)
		named = append(named, ast.NewStringLiteral(p.Key, pos))
		if p.KeyExpr != nil {
			kid, err := e.emitPropertyKeyTemp(p.KeyExpr, pos)
			if err != nil {
				return Value{}, err
			}
			read = ast.NewIndexExpression(srcID, kid, pos)
			named[len(named)-1] = kid
		}
		target, dflt := p.Value, ast.Expression(nil)
		if a, ok := p.Value.(*ast.AssignmentExpression); ok && a.Op == "=" {
			target, dflt = a.Left, a.Right
		}
		var value ast.Expression = read
		if dflt != nil {
			got, err := e.emitExpr(read)
			if err != nil {
				return Value{}, err
			}
			gb, err := e.emitBoxValue(got)
			if err != nil {
				return Value{}, err
			}
			gotID := temp(gb.Ref)
			value = ast.NewConditionalExpression(
				ast.NewBinaryExpression("===", gotID, ast.NewNullLiteral(true, pos), pos), dflt, gotID, pos)
		}
		if _, err := e.emitExpr(ast.NewAssignmentExpression("=", target, value, pos)); err != nil {
			return Value{}, err
		}
	}
	return src, nil
}

// dynStoreHint is the contextual type of a value stored into a dynamic
// object's property: an index-signature view's declared property type, else
// `any` (a plain any member's array literal stays an array of any, which a
// later push of another kind needs).
func (e *Emitter) dynStoreHint(baseTy Type, rhs ast.Expression) Type {
	if baseTy.DynPropTy != nil {
		return *baseTy.DynPropTy
	}
	return TypeAny
}

// logicalAssignOp reports `&&=`, `||=` and `??=`.
func logicalAssignOp(op string) bool { return op == "&&=" || op == "||=" || op == "??=" }

// emitGlobalPropertyAssign assigns an undeclared name as JavaScript does:
// in sloppy code, the global object's property (created by `=`; a compound
// assignment reads it first, so a missing one is a ReferenceError); in
// strict code, a ReferenceError once the right-hand side is evaluated.
func (e *Emitter) emitGlobalPropertyAssign(ex *ast.AssignmentExpression, id *ast.Identifier) (Value, error) {
	key, linked := namespaceValueExport("globalThis")
	linked = linked && e.libExports[key] != ""
	if !linked {
		v, err := e.emitExpr(ex.Right)
		if err != nil {
			return Value{}, err
		}
		e.emitNotDefined(id)
		return v, nil
	}
	if ex.Op == "=" && !e.sloppyAt(id) {
		// Strict code: the right-hand side, then the write, which throws
		// when the global object lacks the name.
		v, err := e.emitExpr(ex.Right)
		if err != nil {
			return Value{}, err
		}
		pos := id.GetPos()
		g := ast.NewIdentifier("globalThis", pos)
		has, err := e.emitExpr(ast.NewBinaryExpression("in", ast.NewStringLiteral(id.Name, pos), g, pos))
		if err != nil {
			return Value{}, err
		}
		has = e.toBool(has)
		hitL, missL := e.freshLabel("global.has"), e.freshLabel("global.miss")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has.Ref, hitL, missL))
		e.emitLabel(missL)
		e.emitNotDefined(id)
		e.emitTerminator(fmt.Sprintf("br label %%%s", hitL))
		e.emitLabel(hitL)
		bv, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		name := fmt.Sprintf("__kml_gput_%s", e.freshReg()[1:])
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", bv.Ref, slot))
		e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
		return e.emitExpr(ast.NewAssignmentExpression("=", ast.NewMemberExpression(g, id.Name, pos), ast.NewIdentifier(name, pos), ex.GetPos()))
	}
	if ex.Op != "=" {
		if _, err := e.emitGlobalPropertyRead(id); err != nil {
			return Value{}, err
		}
	}
	target := ast.NewMemberExpression(ast.NewIdentifier("globalThis", id.GetPos()), id.Name, id.GetPos())
	return e.emitExpr(ast.NewAssignmentExpression(ex.Op, target, ex.Right, ex.GetPos()))
}

// sloppyAt reports whether id is in a sloppy-mode file (sloppyFiles).
func (e *Emitter) sloppyAt(id *ast.Identifier) bool {
	if len(e.sloppyFileSet) == 0 {
		return false
	}
	if e.identFiles == nil {
		e.identFiles = map[*ast.Identifier]string{}
		if e.prog != nil {
			for st, path := range e.prog.StmtFiles {
				ast.Inspect(st, func(n ast.Node) bool {
					if i, ok := n.(*ast.Identifier); ok {
						e.identFiles[i] = path
					}
					return true
				})
			}
		}
	}
	return e.sloppyFileSet[e.identFiles[id]]
}

// isNestedAssignPattern reports a destructuring-assignment target that is
// itself a pattern (`[a]`, `{ a }`).
func isNestedAssignPattern(t ast.Expression) bool {
	switch t.(type) {
	case *ast.ArrayLiteral, *ast.ObjectLiteral:
		return true
	}
	return false
}

// declareHiddenAny declares a fresh hidden `let` of type any and returns
// its name.
// hiddenLocal is a fresh local of type ty, named by an identifier codegen
// built (typed as ty).
func (e *Emitter) hiddenLocal(ty Type, pos ast.Pos) *ast.Identifier {
	name := e.nestedDefaultName()
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", slot, StructFieldIR(ty), ty.Align()))
	e.define(name, Symbol{Ptr: slot, Ty: ty})
	return typed(e, ast.NewIdentifier(name, pos), ty)
}

func (e *Emitter) declareHiddenAny(pos ast.Pos) (string, error) {
	name := e.nestedDefaultName()
	err := e.emitStmt(ast.NewVarDeclaration("let", name, ast.AnyAnnotation("ts"), nil, pos))
	return name, err
}

// assignPatternTake is patternTake for a destructuring-assignment pattern.
// tupleAssignPattern reports a pattern a tuple source assigns position by
// position: any rest is its last element.
func tupleAssignPattern(elems []ast.Expression) bool {
	for i, el := range elems {
		if _, ok := el.(*ast.SpreadElement); ok && i != len(elems)-1 {
			return false
		}
	}
	return true
}

func assignPatternTake(elems []ast.Expression) int {
	for _, el := range elems {
		if _, ok := el.(*ast.SpreadElement); ok {
			return -1
		}
	}
	return len(elems)
}

// emitSelfNameAssign is an assignment to a named function expression's own
// name inside it: the right side is evaluated, then the binding is left
// unchanged, silently in sloppy code and with a TypeError in strict code.
func (e *Emitter) emitSelfNameAssign(ex *ast.AssignmentExpression, ident *ast.Identifier) (Value, error) {
	rhs := ex.Right
	if op := strings.TrimSuffix(ex.Op, "="); op != "" && !isLogicalAssignOp(ex.Op) {
		rhs = ast.NewBinaryExpression(op, ident, ex.Right, ex.GetPos())
	}
	v, err := e.emitExpr(rhs)
	if err != nil {
		return Value{}, err
	}
	// Ignored only in sloppy code: a sloppy file's write outside a class
	// and a "use strict" function (sloppyWrite).
	if !e.sloppyAt(ident) || !e.sloppyWrite {
		e.emitThrowTypeError("Assignment to constant variable.")
		e.emitLabel(e.freshLabel("selfassign.after"))
	}
	return v, nil
}

// anyTemp holds boxed word in a new named temporary, for expressions built
// over an already-evaluated value.
func (e *Emitter) anyTemp(word string, pos ast.Pos) *ast.Identifier {
	name := fmt.Sprintf("__kml_dtmp_%d", e.optRecvCtr)
	e.optRecvCtr++
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", word, slot))
	e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
	return ast.NewIdentifier(name, pos)
}

// emitPropertyKeyTemp evaluates a computed key once, as a property key (a
// symbol stays one, anything else becomes its string), into a temporary.
func (e *Emitter) emitPropertyKeyTemp(keyExpr ast.Expression, pos ast.Pos) (*ast.Identifier, error) {
	kv, err := e.emitExprWithObjectHint(keyExpr, TypeAny)
	if err != nil {
		return nil, err
	}
	kb, err := e.emitBoxValue(kv)
	if err != nil {
		return nil, err
	}
	k := e.anyTemp(kb.Ref, pos)
	isSym := ast.NewBinaryExpression("===", ast.NewUnaryExpression("typeof", true, k, pos), ast.NewStringLiteral("symbol", pos), pos)
	key, err := e.emitExpr(ast.NewConditionalExpression(isSym, k, stringCall(k, pos), pos))
	if err != nil {
		return nil, err
	}
	if key, err = e.emitBoxValue(key); err != nil {
		return nil, err
	}
	return e.anyTemp(key.Ref, pos), nil
}

// assignValueUsed reports an assignment whose value something reads: not
// an expression statement's, a for loop's update or a sequence's non-last
// element.
func (e *Emitter) assignValueUsed(ex *ast.AssignmentExpression) bool {
	c := e.front()
	if c == nil {
		return true
	}
	switch p := c.ParentOf(ex).(type) {
	case *ast.ExpressionStatement:
		return false
	case *ast.ForStatement:
		for _, u := range p.Update {
			if u == ast.Expression(ex) {
				return false
			}
		}
		return true
	case *ast.SequenceExpression:
		return len(p.Exprs) > 0 && p.Exprs[len(p.Exprs)-1] == ast.Expression(ex)
	}
	return true
}

func isObjectLiteral(x ast.Expression) bool {
	_, ok := x.(*ast.ObjectLiteral)
	return ok
}

// undeclaredPatternTargets is the names a destructuring assignment's
// pattern assigns that no scope declares (JavaScript: a global property
// write, or strict mode's ReferenceError).
// Each is its first target node.
func (e *Emitter) undeclaredPatternTargets(pat ast.Expression) map[string]*ast.Identifier {
	out := map[string]*ast.Identifier{}
	mapPatternTargets(pat, func(id *ast.Identifier) ast.Expression {
		if _, ok := e.lookup(id.Name); !ok && out[id.Name] == nil && e.unresolvedReference(id) {
			out[id.Name] = id
		}
		return nil
	})
	return out
}

// mapPatternTargets copies an assignment pattern with each identifier
// target rep returns an expression for replaced by it (the pattern
// itself when none is).
func mapPatternTargets(x ast.Expression, rep func(*ast.Identifier) ast.Expression) ast.Expression {
	switch t := x.(type) {
	case *ast.Identifier:
		if r := rep(t); r != nil {
			return r
		}
	case *ast.AssignmentExpression:
		if t.Op == "=" {
			if l := mapPatternTargets(t.Left, rep); l != t.Left {
				return ast.NewAssignmentExpression("=", l, t.Right, t.GetPos())
			}
		}
	case *ast.SpreadElement:
		if a := mapPatternTargets(t.Arg, rep); a != t.Arg {
			return ast.NewSpreadElement(a, t.GetPos())
		}
	case *ast.ArrayLiteral:
		elems := make([]ast.Expression, len(t.Elements))
		changed := false
		for i, el := range t.Elements {
			if el != nil {
				elems[i] = mapPatternTargets(el, rep)
			}
			changed = changed || elems[i] != el
		}
		if changed {
			c := *t
			c.Elements = elems
			return &c
		}
	case *ast.ObjectLiteral:
		props := make([]ast.ObjectProperty, len(t.Properties))
		changed := false
		for i, p := range t.Properties {
			props[i] = p
			if v := mapPatternTargets(p.Value, rep); v != p.Value {
				props[i].Value = v
				// `{ u }` / `{ u = 1 }` keep key u, now with a member target.
				props[i].Shorthand, props[i].CoverInit = false, false
				changed = true
			}
		}
		if changed {
			c := *t
			c.Properties = props
			return &c
		}
	}
	return x
}

// emitUndeclaredPatternAssign assigns a pattern some of whose targets no
// scope declares: each such target is a setter's (`{ set u(v) { u = v } }`)
// property, so it is written, as a plain assignment `u = v` is, when the
// pattern reaches it.
func (e *Emitter) emitUndeclaredPatternAssign(ex *ast.AssignmentExpression, names map[string]*ast.Identifier) (Value, error) {
	pos := ex.GetPos()
	var props []ast.ObjectProperty
	keys := make([]string, 0, len(names))
	for n := range names {
		keys = append(keys, n)
	}
	sort.Strings(keys)
	for _, n := range keys {
		// The target's own node: the binder resolves it (to nothing).
		body := ast.NewBlockStatement([]ast.Statement{ast.NewExpressionStatement(
			ast.NewAssignmentExpression("=", names[n], ast.NewIdentifier("__kml_pv", pos), pos), pos)}, pos)
		set := ast.NewFunctionExpression("", []ast.Param{{Name: "__kml_pv"}}, nil, body, false, pos)
		set.Method = true
		props = append(props, ast.ObjectProperty{Key: n, Value: set, AccessorKind: "set"})
	}
	obj, err := e.emitExpr(ast.NewObjectLiteral(props, pos))
	if err != nil {
		return Value{}, err
	}
	if obj, err = e.emitBoxValue(obj); err != nil {
		return Value{}, err
	}
	name := fmt.Sprintf("__kml_pset_%s", e.freshReg()[1:])
	slot := "%" + name
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", obj.Ref, slot))
	e.define(name, Symbol{Ptr: slot, Ty: TypeAny})
	left := mapPatternTargets(ex.Left, func(id *ast.Identifier) ast.Expression {
		if names[id.Name] != nil {
			return ast.NewMemberExpression(ast.NewIdentifier(name, id.GetPos()), id.Name, id.GetPos())
		}
		return nil
	})
	return e.emitExpr(ast.NewAssignmentExpression("=", left, ex.Right, pos))
}

// patternSuspends reports a `yield` or `await` in an assignment pattern
// (its targets' and defaults' own code, not a nested function's).
func patternSuspends(pat ast.Expression) bool {
	found := false
	ast.Inspect(pat, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.FunctionExpression, *ast.ArrowFunction, *ast.ClassExpression:
			return false
		case *ast.YieldExpression, *ast.AwaitExpression:
			found = true
		}
		return !found
	})
	return found
}

// iteratorPatternAssign lowers `[t0, t1 = d, , ...r] = src` over an
// iterable whose iteration the program can observe (an object, a
// generator, an any) to the iterator protocol, step by step as
// JavaScript runs it: each target's reference (a member's object and key)
// is evaluated, then the iterator stepped, then the default applied and
// the value assigned; a rest collects what remains. The iterator is closed
// when the pattern finishes before it does, or throws (its return()'s own
// error then lost). Its value is src.
func iteratorPatternAssign(pat *ast.ArrayLiteral, src ast.Expression, pos ast.Pos) (ast.Expression, error) {
	sub := map[string]ast.Expression{"__KML_SRC": src}
	var b strings.Builder
	b.WriteString(`((__kml_s: any): any => {
const __kml_m: any = __kml_s === null || __kml_s === undefined ? undefined : __kml_s[Symbol.iterator];
if (typeof __kml_m !== "function") {
	const __kml_t: string = typeof __kml_s;
	throw new TypeError((__kml_s === undefined ? "undefined" : __kml_s === null ? "object null" : __kml_t === "symbol" || __kml_t === "bigint" || __kml_t === "object" || __kml_t === "function" ? __kml_t : __kml_t + " " + __kml_s) + " is not iterable (cannot read property Symbol(Symbol.iterator))");
}
const __kml_i: any = __kml_m.call(__kml_s);
const __kml_n: any = __kml_i.next;
let __kml_d: boolean = false;
let __kml_r: any;
const __kml_step = (): any => {
	if (__kml_d) { return undefined; }
	try { __kml_r = __kml_n.call(__kml_i); } catch (__kml_e) { __kml_d = true; throw __kml_e; }
	if (__kml_r === null || typeof __kml_r !== "object" && typeof __kml_r !== "function") { __kml_d = true; throw new TypeError("Iterator result " + (typeof __kml_r === "symbol" ? __kml_r.toString() : "" + __kml_r) + " is not an object"); }
	let __kml_done: any;
	try { __kml_done = __kml_r.done; } catch (__kml_e) { __kml_d = true; throw __kml_e; }
	if (__kml_done) { __kml_d = true; return undefined; }
	try { return __kml_r.value; } catch (__kml_e) { __kml_d = true; throw __kml_e; }
};
try {
`)
	for k, el := range pat.Elements {
		if h, ok := el.(*ast.NullLiteral); ok && h.Elision || el == nil {
			b.WriteString("__kml_step();\n")
			continue
		}
		if sp, ok := el.(*ast.SpreadElement); ok {
			ref, err := patternRef(sp.Arg, k, sub, &b)
			if err != nil {
				return nil, err
			}
			fmt.Fprintf(&b, "{ const __kml_a: any[] = []; while (!__kml_d) { const __kml_x: any = __kml_step(); if (!__kml_d) { __kml_a.push(__kml_x); } } %s = __kml_a; }\n", ref)
			continue
		}
		target, def := unwrapDestructDefault(el)
		ref, err := patternRef(target, k, sub, &b)
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&b, "{ let __kml_v: any = __kml_step();\n")
		if def != nil {
			dn := fmt.Sprintf("__KML_D%d", k)
			sub[dn] = def
			fmt.Fprintf(&b, "if (__kml_v === undefined) { __kml_v = %s; }\n", dn)
		}
		fmt.Fprintf(&b, "%s = __kml_v; }\n", ref)
	}
	b.WriteString(`} catch (__kml_e) {
	if (!__kml_d) { try { const __kml_f: any = __kml_i.return; if (__kml_f !== undefined && __kml_f !== null) { __kml_f.call(__kml_i); } } catch (__kml_e2) {} }
	throw __kml_e;
}
if (!__kml_d) {
	const __kml_f: any = __kml_i.return;
	if (__kml_f !== undefined && __kml_f !== null) {
		const __kml_c: any = __kml_f.call(__kml_i);
		if (__kml_c === null || typeof __kml_c !== "object" && typeof __kml_c !== "function") { throw new TypeError("Iterator result " + (typeof __kml_c === "symbol" ? __kml_c.toString() : "" + __kml_c) + " is not an object"); }
	}
}
return __kml_s;
})(__KML_SRC)`)
	prog, err := parser.Parse(b.String())
	if err != nil || len(prog.Body) != 1 {
		return nil, fmt.Errorf("iterator pattern lowering: %v", err)
	}
	es, ok := prog.Body[0].(*ast.ExpressionStatement)
	if !ok {
		return nil, fmt.Errorf("iterator pattern lowering: not an expression")
	}
	var rw func(ast.Node) ast.Node
	rw = func(n ast.Node) ast.Node {
		if id, ok := n.(*ast.Identifier); ok {
			if r, ok := sub[id.Name]; ok {
				return r
			}
			return n
		}
		ast.RewriteChildren(n, rw)
		return n
	}
	return rw(es.Expr).(ast.Expression), nil
}

// patternRef writes the evaluation of element k's target reference (a
// member's object and key, into temporaries) and returns the target to
// assign: the identifier or nested pattern itself (by placeholder), or the
// member over its temporaries.
func patternRef(target ast.Expression, k int, sub map[string]ast.Expression, b *strings.Builder) (string, error) {
	switch t := target.(type) {
	case *ast.MemberExpression:
		if t.Optional || strings.HasPrefix(t.Property, "#") {
			// (an optional chain is no target; a private name is read
			// only inside its class)
			return "", fmt.Errorf("not lowered")
		}
		on := fmt.Sprintf("__KML_O%d", k)
		sub[on] = t.Object
		fmt.Fprintf(b, "const __kml_o%d: any = %s;\n", k, on)
		return fmt.Sprintf("__kml_o%d.%s", k, t.Property), nil
	case *ast.IndexExpression:
		on, kn := fmt.Sprintf("__KML_O%d", k), fmt.Sprintf("__KML_K%d", k)
		sub[on], sub[kn] = t.Object, t.Index
		fmt.Fprintf(b, "const __kml_o%d: any = %s;\nconst __kml_k%d: any = %s;\n", k, on, k, kn)
		return fmt.Sprintf("__kml_o%d[__kml_k%d]", k, k), nil
	}
	tn := fmt.Sprintf("__KML_T%d", k)
	sub[tn] = target
	return tn, nil
}

// emitConstTargetThrow throws JavaScript's TypeError for a destructuring
// target that is a const, at that target: the ones before it are assigned.
func (e *Emitter) emitConstTargetThrow() {
	// The code after stays on the same (never taken) path, so the values
	// the pattern computed still dominate their uses.
	throwL, contL := e.freshLabel("const.target"), e.freshLabel("const.target.after")
	e.emitTerminator(fmt.Sprintf("br i1 true, label %%%s, label %%%s", throwL, contL))
	e.emitLabel(throwL)
	e.emitThrowTypeError("Assignment to constant variable.")
	e.emitLabel(contL)
}

// isIndexKeyName reports a property name that is an array index (`0`),
// never a builtin's named property: an instance's own extra property.
func isIndexKeyName(name string) bool {
	if name == "" || len(name) > 1 && name[0] == '0' {
		return false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// errorDynIndex reports a key on an Error that is not a constant name
// (`err[Symbol.toStringTag]`, `err[k]`): the boxed error's own property.
func (e *Emitter) errorDynIndex(t Type, ix *ast.IndexExpression) bool {
	if !t.IsError() || t.IsDynamic() || t.IsArray() {
		return false
	}
	_, constKey := constObjectKey(ix.Index)
	return !constKey
}

package llvm

// emit_reflect.go — Reflect.* and Proxy glue (TDD-00155 Stage 7). Reflect
// maps 1:1 onto the __kml_dynobj_* runtime, with the spec's boolean-result
// (non-throwing) forms for set/deleteProperty/setPrototypeOf/
// preventExtensions. Proxy construction builds a special bag header (the
// PROXY flag, target at the proto slot, handler at the props slot) that the
// runtime get/set/has/delete entry points trap on — see runtime_dynobj.go.

import (
	"fmt"

	"KlainMainLang/ast"
)

// The Reflect functions: one emitter each, registered as the Reflect.*
// intrinsics with their result types (reflectIntrinsics).
func init() {
	for name, emit := range map[string]func(e *Emitter, method string, args []ast.Expression, pos ast.Pos) (Value, error){
		"apply":                    (*Emitter).emitReflectApply,
		"get":                      (*Emitter).emitReflectGet,
		"set":                      (*Emitter).emitReflectSet,
		"has":                      (*Emitter).emitReflectHas,
		"deleteProperty":           (*Emitter).emitReflectDeleteProperty,
		"construct":                (*Emitter).emitReflectConstruct,
		"getOwnPropertyDescriptor": (*Emitter).emitReflectGetOwnPropertyDescriptor,
		"ownKeys":                  (*Emitter).emitReflectOwnKeysCall,
		"getPrototypeOf":           (*Emitter).emitReflectGetPrototypeOf,
		"setPrototypeOf":           (*Emitter).emitReflectSetPrototypeOf,
		"isExtensible":             (*Emitter).emitReflectIsExtensible,
		"preventExtensions":        (*Emitter).emitReflectPreventExtensions,
		"defineProperty":           (*Emitter).emitReflectDefineProperty,
		"defineMetadata":           (*Emitter).emitReflectDefineMetadata,
		"getMetadata":              (*Emitter).emitReflectGetMetadata,
		"getOwnMetadata":           (*Emitter).emitReflectGetMetadata,
		"hasMetadata":              (*Emitter).emitReflectHasMetadata,
		"hasOwnMetadata":           (*Emitter).emitReflectHasMetadata,
		"metadata":                 (*Emitter).emitReflectMetadata,
	} {
		method, emit, ty := name, emit, reflectIntrinsics[name]
		intrinsics["Reflect."+method] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				e.ensureAnyToPrimitive()
				e.ensureNanBox()
				e.ensureErrorNames()
				e.ensureStrHeaderRuntime()
				return emit(e, method, ex.Args, ex.GetPos())
			},
			ty: func(*Emitter, *ast.CallExpression) Type { return ty },
		}
	}
}

// reflectNeed checks a Reflect function's argument count.
func reflectNeed(method string, args []ast.Expression, pos ast.Pos, n int) error {
	if len(args) != n {
		return internalAt(pos, "Reflect.%s given %d arguments past the lowered call's fit (%d)", method, len(args), n)
	}
	return nil
}

// reflectArgBag is argument i's dynamic bag, or a TypeError.
func (e *Emitter) reflectArgBag(args []ast.Expression, pos ast.Pos, i int, what string) (string, error) {
	return e.emitDynBagOrThrow(args[i], what, pos)
}

func (e *Emitter) emitReflectApply(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Reflect.apply(f, thisArg, args): f called with thisArg and the
	// elements of args.
	if err := reflectNeed(method, args, pos, 3); err != nil {
		return Value{}, err
	}
	fv, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	fb, err := e.emitBoxValue(fv)
	if err != nil {
		return Value{}, err
	}
	tv, err := e.emitExprWithObjectHint(args[1], TypeAny)
	if err != nil {
		return Value{}, err
	}
	argv, n, err := e.emitDynArgv([]ast.Expression{ast.NewSpreadElement(args[2], args[2].GetPos())}, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynFnBoxCallN(fb, tv, argv, n, "Function.prototype.apply was called on a value that is not a function", pos)
}

func (e *Emitter) emitReflectGet(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return Value{}, internalAt(pos, "Reflect.get arity")
	}
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !v.Ty.IsDynamic() {
		if scalarTypeKind(v.Ty) != "" || isStringTy(v.Ty) || v.Ty.IsUndefined() || v.Ty.IsNull() || v.Ty.Nullable() {
			// A primitive (or possibly one): checked at run time.
			if v, err = e.emitBoxValue(v); err != nil {
				return Value{}, err
			}
		} else if len(args) == 2 {
			// An object of a static type: its property, read as `o[k]`
			// reads it, boxed as Reflect.get's result.
			r, err := e.emitExpr(ast.NewIndexExpression(e.bindValue(v, args[0].GetPos()), args[1], pos))
			if err != nil {
				return Value{}, err
			}
			return e.emitBoxValue(r)
		} else if v, err = e.emitBoxValue(v); err != nil {
			return Value{}, err
		}
	}
	if err := e.emitRequireObject(v, "Reflect.get called on non-object"); err != nil {
		return Value{}, err
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	if len(args) == 2 {
		return e.emitDynAnyMemberGet(v, keyRef, pos)
	}
	// A receiver: a dynamic object's getter runs with it as this.
	rv, err := e.emitExprWithObjectHint(args[2], TypeAny)
	if err != nil {
		return Value{}, err
	}
	recv, err := e.emitBoxValue(rv)
	if err != nil {
		return Value{}, err
	}
	e.ensureNanBox()
	tag, payload := e.emitUnboxTagPayload(v)
	isBag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", res))
	bagL, otherL, doneL := e.freshLabel("rget.bag"), e.freshLabel("rget.other"), e.freshLabel("rget.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBag, bagL, otherL))
	e.emitLabel(bagL)
	e.declareFn("__kml_dynobj_get_recv", "declare i64 @__kml_dynobj_get_recv("+irPtr+", "+irPtr+", i64)")
	g := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get_recv("+irPtr+" %s, "+irPtr+" %s, i64 %s)", g, e.emitIntToPtr(payload), keyRef, recv.Ref))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", g, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(otherL)
	o, err := e.emitDynAnyMemberGet(v, keyRef, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", o.Ref, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", out, res))
	return Value{Ref: out, Ty: TypeAny}, nil
}

func (e *Emitter) emitReflectSet(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 && len(args) != 4 {
		return Value{}, internalAt(pos, "Reflect.set given %d arguments past the lowered call's fit", len(args))
	}
	bag, err := e.reflectArgBag(args, pos, 0, "Reflect.set")
	if err != nil {
		return Value{}, err
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	val, err := e.emitExprWithObjectHint(args[2], TypeAny)
	if err != nil {
		return Value{}, err
	}
	boxed, err := e.emitBoxValue(val)
	if err != nil {
		return Value{}, err
	}
	if len(args) == 3 {
		status := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv("+irPtr+" %s, "+irPtr+" %s, i64 %s)", status, bag, keyRef, boxed.Ref))
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", ok, status))
		return Value{Ref: ok, Ty: TypeBool}, nil
	}
	// A receiver: a setter on the target's chain runs with it as this,
	// else the value lands on it.
	rv, err := e.emitExprWithObjectHint(args[3], TypeAny)
	if err != nil {
		return Value{}, err
	}
	recv, err := e.emitBoxValue(rv)
	if err != nil {
		return Value{}, err
	}
	e.declareFn("__kml_dynobj_setv_recv", "declare i64 @__kml_dynobj_setv_recv("+irPtr+", "+irPtr+", i64, i64)")
	status := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv_recv("+irPtr+" %s, "+irPtr+" %s, i64 %s, i64 %s)", status, bag, keyRef, boxed.Ref, recv.Ref))
	plain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 4", plain, status))
	plainL, doneL := e.freshLabel("rset.plain"), e.freshLabel("rset.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", plain, plainL, doneL))
	e.emitLabel(plainL)
	// Another kind of receiver (a class instance): an ordinary store to it.
	if _, err := e.emitDynAnyMemberSet(recv, keyRef, boxed, pos); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i64 %s, 0", ok, status))
	ok4 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok4, ok, plain))
	return Value{Ref: ok4, Ty: TypeBool}, nil
}

func (e *Emitter) emitReflectHas(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 2); err != nil {
		return Value{}, err
	}
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !v.Ty.IsDynamic() && !(scalarTypeKind(v.Ty) != "" || isStringTy(v.Ty) || v.Ty.IsUndefined() || v.Ty.IsNull() || v.Ty.Nullable()) {
		// An object of a static type: `k in o`, its prototype's keys
		// included.
		return e.emitExpr(ast.NewBinaryExpression("in", args[1], e.bindValue(v, args[0].GetPos()), pos))
	}
	if !v.Ty.IsDynamic() {
		if v, err = e.emitBoxValue(v); err != nil {
			return Value{}, err
		}
	}
	if err := e.emitRequireObject(v, "Reflect.has called on non-object"); err != nil {
		return Value{}, err
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynAnyHas(v, keyRef, false, pos)
}

func (e *Emitter) emitReflectDeleteProperty(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 2); err != nil {
		return Value{}, err
	}
	bag, err := e.reflectArgBag(args, pos, 0, "Reflect.deleteProperty")
	if err != nil {
		return Value{}, err
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_delete("+irPtr+" %s, "+irPtr+" %s)", r, bag, keyRef))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitReflectConstruct(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Reflect.construct(C, args): `new C(...args)` through the run-time
	// constructor dispatch.
	if len(args) != 2 && len(args) != 3 {
		return Value{}, internalAt(pos, "Reflect.construct given %d arguments past the lowered call's fit", len(args))
	}
	cv, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.emitBoxValue(cv)
	if err != nil {
		return Value{}, err
	}
	{
		// A primitive target is no constructor: its TypeError comes before
		// the arguments list is read.
		tag, _ := e.emitUnboxTagPayload(cb)
		prim, primL, objL := e.freshReg(), e.freshLabel("refl.ctor.prim"), e.freshLabel("refl.ctor.obj")
		e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", prim, tag, kmlTagUndefined))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", prim, primL, objL))
		e.emitLabel(primL)
		shown, err := e.emitDynamicInspect(cb)
		if err != nil {
			return Value{}, err
		}
		msg, err := e.emitStringConcat(shown, Value{Ref: e.internString(" is not a constructor"), Ty: TypePtr})
		if err != nil {
			return Value{}, err
		}
		e.ensureExceptionHelpers()
		e.emitInternalThrowKind("TypeError", msg.Ref)
		e.emitLabel(objL)
	}
	pending := false
	if len(args) == 3 {
		// A newTarget other than the target itself: the instance takes
		// newTarget.prototype (when that is an object) before the
		// constructor runs (shapesrc/shape.c's pending prototype).
		nv, err := e.emitExprWithObjectHint(args[2], TypeAny)
		if err != nil {
			return Value{}, err
		}
		nb, err := e.emitBoxValue(nv)
		if err != nil {
			return Value{}, err
		}
		e.ensureBoxedBigIntHooks()
		e.ensureFnMeta()
		undef, same, ok := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", undef, nb.Ref, nbUndefined))
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_eq(i64 %s, i64 %s)", same, nb.Ref, cb.Ref))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, undef, same))
		okL, otherL := e.freshLabel("refl.nt.same"), e.freshLabel("refl.nt.other")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, otherL))
		e.emitLabel(otherL)
		ctorOK := e.freshReg()
		e.declareFn("__kml_is_constructor", "declare i64 @__kml_is_constructor(i64)")
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_is_constructor(i64 %s)", ctorOK, nb.Ref))
		isCtor := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isCtor, ctorOK))
		ntL, ntBadL := e.freshLabel("refl.nt.ctor"), e.freshLabel("refl.nt.notctor")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isCtor, ntL, ntBadL))
		e.emitLabel(ntBadL)
		shownNT, err := e.emitDynamicToString(nb)
		if err != nil {
			return Value{}, err
		}
		ntMsg, err := e.emitStringConcat(shownNT, Value{Ref: e.internString(" is not a constructor"), Ty: TypePtr})
		if err != nil {
			return Value{}, err
		}
		e.ensureExceptionHelpers()
		e.emitInternalThrowKind("TypeError", ntMsg.Ref)
		e.emitLabel(ntL)
		proto, err := e.emitDynAnyMemberGet(nb, e.internString("prototype"), pos)
		if err != nil {
			return Value{}, err
		}
		pp, isObj, err := e.emitDynProtoOperand(proto, false)
		if err != nil {
			return Value{}, err
		}
		nonNull, take := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", nonNull, pp))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", take, isObj, nonNull))
		setL := e.freshLabel("refl.nt.set")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", take, setL, okL))
		e.emitLabel(setL)
		e.ensureShapeRuntime()
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" @__kml_pending_proto, align 8", pp))
		e.emitInstr("store i32 1, " + irPtr + " @__kml_pending_proto_set, align 4")
		e.emitTerminator(fmt.Sprintf("br label %%%s", okL))
		e.emitLabel(okL)
		pending = true
	}
	argv, n, err := e.emitDynArgv([]ast.Expression{ast.NewSpreadElement(args[1], args[1].GetPos())}, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureNanBox()
	e.usedDynConstruct = true
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dyn_construct(i64 %s, "+irPtr+" %s, i64 %s, "+irPtr+" %s)", r, cb.Ref, argv, n, e.internString("Reflect.construct target is not a constructor")))
	if pending {
		// A target that made no instance of a class (a builtin's) leaves
		// nothing pending for the next one.
		e.emitInstr("store i32 0, " + irPtr + " @__kml_pending_proto_set, align 4")
	}
	return Value{Ref: r, Ty: TypeAny}, nil
}

func (e *Emitter) emitReflectGetOwnPropertyDescriptor(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 2); err != nil {
		return Value{}, err
	}
	return e.emitObjectGetOwnPropertyDescriptor(args, pos)
}

func (e *Emitter) emitReflectOwnKeysCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 1); err != nil {
		return Value{}, err
	}
	return e.emitReflectOwnKeys(args[0], pos)
}

func (e *Emitter) emitReflectGetPrototypeOf(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 1); err != nil {
		return Value{}, err
	}
	return e.emitObjectGetPrototypeOf(args, pos)
}

func (e *Emitter) emitReflectSetPrototypeOf(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 2); err != nil {
		return Value{}, err
	}
	bag, err := e.reflectArgBag(args, pos, 0, "Reflect.setPrototypeOf")
	if err != nil {
		return Value{}, err
	}
	protoVal, err := e.emitExprWithObjectHint(args[1], TypeAny)
	if err != nil {
		return Value{}, err
	}
	protoReg, okReg, err := e.emitDynProtoOperand(protoVal, true)
	if err != nil {
		return Value{}, err
	}
	_ = okReg
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_set_proto("+irPtr+" %s, "+irPtr+" %s)", r, bag, protoReg))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitReflectIsExtensible(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 1); err != nil {
		return Value{}, err
	}
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	return e.emitDynFlagsTest(v, 0)
}

func (e *Emitter) emitReflectPreventExtensions(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if err := reflectNeed(method, args, pos, 1); err != nil {
		return Value{}, err
	}
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if _, err := e.emitDynPrevent(v, 0); err != nil {
		return Value{}, err
	}
	return Value{Ref: "true", Ty: TypeBool}, nil
}

func (e *Emitter) emitReflectDefineProperty(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// The boolean-result form; a spec violation still throws in V1
	// (disclosed) rather than returning false.
	if _, err := e.emitObjectDefineProperty(args, pos); err != nil {
		return Value{}, err
	}
	return Value{Ref: "true", Ty: TypeBool}, nil
}

func (e *Emitter) emitReflectDefineMetadata(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Reflect.defineMetadata(metadataKey, metadataValue, target[, propertyKey])
	if len(args) != 3 && len(args) != 4 {
		return Value{}, internalAt(pos, "Reflect.defineMetadata given %d arguments past the lowered call's fit", len(args))
	}
	metaKeyRef, err := e.dynAnyKeyRef(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	val, err := e.emitExprWithObjectHint(args[1], TypeAny)
	if err != nil {
		return Value{}, err
	}
	valBox, err := e.emitBoxValue(val)
	if err != nil {
		return Value{}, err
	}
	targetBag, err := e.reflectMetaTargetBag(args[2], pos)
	if err != nil {
		return Value{}, err
	}
	propKeyRef, err := e.reflectMetaPropKey(args, 3, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitDefineMetadata(targetBag, propKeyRef, metaKeyRef, valBox.Ref)
	return Value{Ref: fmt.Sprintf("%d", nbUndefined), Ty: TypeAny}, nil
}

func (e *Emitter) emitReflectGetMetadata(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// V1 does not walk the prototype chain, so getMetadata == getOwnMetadata.
	if len(args) != 2 && len(args) != 3 {
		return Value{}, internalAt(pos, "Reflect.%s given %d arguments past the lowered call's fit", method, len(args))
	}
	metaKeyRef, err := e.dynAnyKeyRef(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	targetBag, err := e.reflectMetaTargetBag(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	propKeyRef, err := e.reflectMetaPropKey(args, 2, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitGetMetadata(targetBag, propKeyRef, metaKeyRef), nil
}

func (e *Emitter) emitReflectHasMetadata(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 && len(args) != 3 {
		return Value{}, internalAt(pos, "Reflect.%s given %d arguments past the lowered call's fit", method, len(args))
	}
	metaKeyRef, err := e.dynAnyKeyRef(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	targetBag, err := e.reflectMetaTargetBag(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	propKeyRef, err := e.reflectMetaPropKey(args, 2, pos)
	if err != nil {
		return Value{}, err
	}
	got := e.emitGetMetadata(targetBag, propKeyRef, metaKeyRef)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", r, got.Ref, nbUndefined))
	return Value{Ref: r, Ty: TypeBool}, nil
}

func (e *Emitter) emitReflectMetadata(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	// Reflect.metadata(key, value) returns a decorator; used manually it
	// hits the same returned-value-call gap factory decorators do. The
	// compiler-emitted design:* metadata does not go through it.
	m, linked := e.lowerLink("__kml_Reflect_metadata")
	if !linked {
		return Value{}, internalAt(pos, "Reflect.metadata's library function is not linked")
	}
	return e.emitExpr(ast.NewCallExpression(&ast.Identifier{Name: m}, args, pos))
}

// reflectMetaTargetBag resolves a Reflect metadata `target` argument to the raw
// ptr of its dynamic-object bag, rejecting a non-dynamic target (metadata is
// stored on the dynamic decorator target, TDD-00161 Stage 3).
func (e *Emitter) reflectMetaTargetBag(arg ast.Expression, pos ast.Pos) (string, error) {
	v, err := e.emitExprWithObjectHint(arg, TypeAny)
	if err != nil {
		return "", err
	}
	if v, err = e.emitBoxValue(v); err != nil {
		return "", err
	}
	// A dynamic object keeps its metadata in its own bag; any other object
	// (a class, a static object, a function) in one its identity keys. A
	// primitive target is reflect-metadata's TypeError.
	tag, pay := e.emitUnboxTagPayload(v)
	res := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", res))
	dynL, notDynL := e.emitTagCheck(tag, kmlTagDynObject, "meta.tgt")
	contL := e.freshLabel("meta.tgt.cont")
	e.emitLabel(dynL)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", e.emitIntToPtr(pay), res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
	e.emitLabel(notDynL)
	prim, primL, objL := e.freshReg(), e.freshLabel("meta.tgt.prim"), e.freshLabel("meta.tgt.obj")
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", prim, tag, kmlTagUndefined))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", prim, primL, objL))
	e.emitLabel(primL)
	e.emitThrowTypeError("")
	e.emitLabel(objL)
	// A function's identity is its property bag (each boxing of it is a new
	// record).
	key := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", key))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", pay, key))
	fnL, keyL := e.emitTagCheck(tag, kmlTagDynFunc, "meta.tgt.fn")
	e.emitLabel(fnL)
	fbag := e.emitFnBagOf(pay)
	fk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", fk, fbag))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", fk, key))
	e.emitTerminator(fmt.Sprintf("br label %%%s", keyL))
	e.emitLabel(keyL)
	pay = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", pay, key))
	e.declareFn("__kml_meta_bag_of", "declare "+irPtr+" @__kml_meta_bag_of(i64, i64, i64)")
	tag64, side := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", tag64, tag))
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_meta_bag_of(i64 %s, i64 %s, i64 1)", side, tag64, pay))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", side, res))
	e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
	e.emitLabel(contL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", bag, res))
	return bag, nil
}

// reflectMetaPropKey returns the interned key ref for a metadata property key
// argument at index i, or the constructor sentinel when the argument is absent.
func (e *Emitter) reflectMetaPropKey(args []ast.Expression, i int, pos ast.Pos) (string, error) {
	if i >= len(args) || isUndefinedLiteral(e, stripAsAny(args[i])) {
		return e.internString(metaCtorKey), nil
	}
	// An undefined key (an optional parameter passed on) is the target's
	// own metadata, as an absent one is.
	v, err := e.emitExprWithObjectHint(args[i], TypeAny)
	if err != nil {
		return "", err
	}
	box, err := e.emitBoxValue(v)
	if err != nil {
		return "", err
	}
	key, err := e.dynAnyKeyRef(e.bindValue(box, pos), pos)
	if err != nil {
		return "", err
	}
	undef, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", undef, box.Ref, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", r, undef, e.internString(metaCtorKey), key))
	return r, nil
}

// emitNewProxy implements `new Proxy(target, handler)` (TDD-00155 Stage 7):
// a special bag header — PROXY flag, the target's bag (null for a target
// that is no dynamic object) where a bag keeps its proto, handler where it
// keeps its props, zero count, the target boxed in the cap word — that the
// runtime property entry points trap on. Any object is a target; a
// primitive is Node's TypeError. `typeof` answers "object" (it is a tag-10
// value).
func (e *Emitter) emitNewProxy(ex *ast.NewExpression) (Value, error) {
	if len(ex.Args) != 2 {
		// A missing argument is undefined (the TypeError below), one past
		// the handler evaluated and ignored.
		fitted, err := e.fitArgs(ex.Args, 2, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		c := *ex
		c.Args = fitted
		ex = &c
	}
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	// A Proxy's [[OwnPropertyKeys]]/[[GetOwnProperty]] live in shape.c,
	// which reads an ownKeys result through the array runtimes.
	e.ensureShapeRuntime()
	e.ensureDynJSONC()
	tv, err := e.emitExprWithObjectHint(ex.Args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !tv.Ty.IsDynamic() {
		if tv, err = e.emitBoxValue(tv); err != nil {
			return Value{}, err
		}
	}
	tag, payload := e.emitUnboxTagPayload(tv)
	prim, objL := e.freshReg(), e.freshLabel("proxy.obj")
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", prim, tag, kmlTagUndefined))
	primL := e.freshLabel("proxy.prim")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", prim, primL, objL))
	e.emitLabel(primL)
	e.emitThrowTypeError("Cannot create proxy with a non-object as target or handler")
	e.emitLabel(objL)
	isBag, bagP, target := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isBag, tag, kmlTagDynObject))
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, bagP, payload))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" null", target, isBag, bagP))
	e.ensureProxyTargetHooks()
	hv, err := e.emitExprWithObjectHint(ex.Args[1], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !hv.Ty.IsDynamic() && hv.Ty.IsObject() && !hostCellObject(hv.Ty) && !hv.Ty.IsDynamicObject() {
		// A typed handler: its traps are read by key, from its bag copy.
		if hv, err = e.emitStaticObjToBag(hv); err != nil {
			return Value{}, err
		}
	}
	if hv, err = e.emitBoxValue(hv); err != nil {
		return Value{}, err
	}
	htag, _ := e.emitUnboxTagPayload(hv)
	hprim, hobjL, hprimL := e.freshReg(), e.freshLabel("proxy.hobj"), e.freshLabel("proxy.hprim")
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", hprim, htag, kmlTagUndefined))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hprim, hprimL, hobjL))
	e.emitLabel(hprimL)
	e.emitThrowTypeError("Cannot create proxy with a non-object as target or handler")
	e.emitLabel(hobjL)
	handler, _, err := e.emitDynOwnBagOf(hv, "Proxy handler", false, ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	hdr := e.freshReg()
	e.heapCallocInto(hdr, allocHost, "1", "56") // a whole Bag
	e.heapStore("i64", fmt.Sprint(kmlBagHdr|kmlBagFlagProxy), hdr, 8)
	tSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 8", tSlot, hdr))
	e.heapStore(irPtr, target, tSlot, 8)
	hSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 16", hSlot, hdr))
	e.heapStore(irPtr, handler, hSlot, 8)
	bSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 32", bSlot, hdr))
	e.heapStore("i64", tv.Ref, bSlot, 8)
	return e.emitDynObjBox(hdr), nil
}

// ensureProxyTargetHooks defines the hooks dynobj.c reaches a Proxy's
// non-bag target through: __kml_proxy_tget(target, key) and
// __kml_proxy_tset(target, key, v), the program's own any get and set.
func (e *Emitter) ensureProxyTargetHooks() {
	if e.fnDecls["__kml_proxy_tget"] {
		return
	}
	e.fnDecls["__kml_proxy_tget"] = true
	get, set := e.dynGetHelper("", ast.Pos{}), e.dynSetHelper("", ast.Pos{})
	restore := e.beginDetachedFunc()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(i64 %%v, "+irPtr+" %%key)", r, get))
	e.emitTerminator("ret i64 " + r)
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("i64 @__kml_proxy_tget(i64 %v, "+irPtr+" %key)", body)
	restore = e.beginDetachedFunc()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(i64 %%v, "+irPtr+" %%key, i64 %%x)", e.freshReg(), set))
	e.emitTerminator("ret i64 0")
	body = e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("i64 @__kml_proxy_tset(i64 %v, "+irPtr+" %key, i64 %x)", body)
}

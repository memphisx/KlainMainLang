package llvm

// emit_dyndesc.go — property descriptors on dynamic objects (TDD-00155
// Stage 5): Object.defineProperty / getOwnPropertyDescriptor /
// getOwnPropertyNames, accessor properties (object-literal get/set and
// descriptor-installed), and real freeze/seal/preventExtensions with their
// is* probes — all over the attrs word the bag has carried since Stage 1.

import (
	"fmt"
	"strings"

	"KlainMainLang/ast"
)

// dynAttr bits (mirrored in runtime_dynobj.go and dynjsonsrc/dynjson.c).
const (
	dynAttrWritable     = 1
	dynAttrEnumerable   = 2
	dynAttrConfigurable = 4
	dynAttrAccessor     = 8
)

// emitDynBagOrThrow unboxes a dynamic-object argument for a descriptor
// static, throwing the JS TypeError for anything else.
// descriptorWhat ends the what of a property descriptor's emitDynOwnBagOf.
const descriptorWhat = " (descriptor)"

func (e *Emitter) emitDynBagOrThrow(arg ast.Expression, what string, pos ast.Pos) (string, error) {
	bag, _, err := e.emitDynOwnBag(arg, what, false, pos)
	return bag, err
}

// emitDynOwnBag is emitDynBagOrThrow for the Object statics that act on a
// value's own properties: a function's are its bag (TDD-00229) when fn is
// set. It also returns the argument boxed.
func (e *Emitter) emitDynOwnBag(arg ast.Expression, what string, fn bool, pos ast.Pos) (string, Value, error) {
	v, err := e.emitExprWithObjectHint(arg, TypeAny)
	if err != nil {
		return "", Value{}, err
	}
	return e.emitDynOwnBagOf(v, what, fn, pos)
}

// emitDynOwnBagOf is emitDynOwnBag of an already-emitted value.
func (e *Emitter) emitDynOwnBagOf(v Value, what string, fn bool, pos ast.Pos) (string, Value, error) {
	var err error
	if fn && !v.Ty.IsDynamic() && v.Ty.IsFunc() {
		if v, err = e.emitBoxValue(v); err != nil {
			return "", Value{}, err
		}
	}
	if !v.Ty.IsDynamic() && (scalarTypeKind(v.Ty) != "" || isStringTy(v.Ty) || v.Ty.IsUndefined() || v.Ty.IsNull()) {
		// A primitive: the run-time check below throws its TypeError.
		if v, err = e.emitBoxValue(v); err != nil {
			return "", Value{}, err
		}
	}
	if !v.Ty.IsDynamic() && v.Ty.IsObject() && !hostCellObject(v.Ty) && !v.Ty.IsDynamicObject() {
		// A typed object or class instance read for its properties (a
		// descriptor, a handler): its copy as a bag answers the same
		// own keys.
		if v, err = e.emitStaticObjToBag(v); err != nil {
			return "", Value{}, err
		}
	}
	if !v.Ty.IsDynamic() {
		if v, err = e.emitBoxValue(v); err != nil {
			return "", Value{}, err
		}
	}
	tag, payload := e.emitUnboxTagPayload(v)
	contL := e.freshLabel("dyndesc.cont")
	var incoming []string
	if fn {
		fnL, notFnL := e.emitTagCheck(tag, kmlTagDynFunc, "dyndesc.fn")
		e.emitLabel(fnL)
		fnBag := e.emitFnPropsBag(e.emitIntToPtr(payload))
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		incoming = append(incoming, fmt.Sprintf("[ %s, %%%s ]", fnBag, fnL))
		e.emitLabel(notFnL)
	}
	if fn {
		// A class reference: a snapshot of its own properties.
		crL, notCrL := e.emitTagCheck(tag, kmlTagFuncRef, "dyndesc.classref")
		e.emitLabel(crL)
		e.usedClassRefBag = true
		e.declareFn("__kml_classref_bag", "declare "+irPtr+" @__kml_classref_bag("+irPtr+")")
		crBag := e.freshReg()
		if e.defineOnClassOwn {
			// A definition: the reference's own bag, which its reads find.
			e.declareFn("__kml_classref_extra", "declare "+irPtr+" @__kml_classref_extra("+irPtr+", i32)")
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classref_extra("+irPtr+" %s, i32 1)", crBag, e.emitIntToPtr(payload)))
		} else {
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classref_bag("+irPtr+" %s)", crBag, e.emitIntToPtr(payload)))
		}
		some := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", some, crBag))
		haveL := e.freshLabel("dyndesc.crbag")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", some, haveL, notCrL))
		e.emitLabel(haveL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		incoming = append(incoming, fmt.Sprintf("[ %s, %%%s ]", crBag, haveL))
		e.emitLabel(notCrL)
	}
	okL, badL := e.emitTagCheck(tag, kmlTagDynObject, "dyndesc.obj")
	e.emitLabel(okL)
	objBag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, objBag, payload))
	e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
	incoming = append(incoming, fmt.Sprintf("[ %s, %%%s ]", objBag, okL))
	e.emitLabel(badL)
	if strings.HasSuffix(what, descriptorWhat) {
		// V8 names the value: `Property description must be an object: 5`.
		desc, err := e.emitValueToString(v)
		if err != nil {
			return "", Value{}, err
		}
		msg, err := e.emitStringConcat(Value{Ref: e.internString("Property description must be an object: "), Ty: TypePtr}, desc)
		if err != nil {
			return "", Value{}, err
		}
		e.emitThrowTypeErrorValue(msg.Ref)
	} else if toObjectStatics[what] {
		// ToObject: only null and undefined are refused; any other
		// primitive's wrapper has no own properties here (a string's are
		// listed before this).
		nullish := e.emitIsNullish(v)
		throwL, primL := e.freshLabel("dyndesc.nullish"), e.freshLabel("dyndesc.prim")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, throwL, primL))
		e.emitLabel(primL)
		e.declareFn("__kml_dynobj_new", "declare "+irPtr+" @__kml_dynobj_new()")
		empty := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_dynobj_new()", empty))
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		incoming = append(incoming, fmt.Sprintf("[ %s, %%%s ]", empty, primL))
		e.emitLabel(throwL)
		e.emitThrowTypeError("Cannot convert undefined or null to object")
	} else {
		e.emitThrowTypeError(what + " called on non-object")
	}
	e.emitLabel(contL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi "+irPtr+" %s", bag, strings.Join(incoming, ", ")))
	return bag, v, nil
}

// descFieldBool reads a boolean-ish descriptor field: returns (present i1,
// value i1) registers. An undefined field is absent.
func (e *Emitter) descFieldBool(descBag, name string) (present, val string) {
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get("+irPtr+" %s, "+irPtr+" %s)", r, descBag, e.internString(name)))
	present = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", present, r, nbUndefined))
	val = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_any_tobool(i64 %s)", val, r))
	return present, val
}

// descFieldRec reads a get/set descriptor field: (present i1, record ptr —
// null when absent or not a dynamic function).
func (e *Emitter) descFieldRec(descBag, name string) (present, rec string) {
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get("+irPtr+" %s, "+irPtr+" %s)", r, descBag, e.internString(name)))
	present = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", present, r, nbUndefined))
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", tag, r))
	isfn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isfn, tag, kmlTagDynFunc))
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, r))
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, p, pay))
	rec = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" null", rec, isfn, p))
	return present, rec
}

// emitObjectDefineProperty implements Object.defineProperty(obj, key, desc)
// on dynamic objects. V1 semantics: create defaults every absent attribute
// to false (per spec); redefinition merges present fields; a
// non-configurable property rejects any change except a value write while
// still writable; a new property on a non-extensible object rejects.
func (e *Emitter) emitObjectDefineProperty(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, internalAt(pos, "Object.defineProperty arity")
	}
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	target, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	// A declared field of a static object keeps its layout: its attributes
	// go into the object's record (propattr.c).
	// An accessor on one is an added property shadowing it (shape.c),
	// which the field's reads and writes find (accessorKeys).
	if lit, ok := args[1].(*ast.StringLiteral); ok && !target.Ty.IsDynamic() && target.Ty.IsObject() &&
		!target.Ty.IsError() && !target.Ty.IsFunc() && !target.Ty.Nullable() && !accessorDescriptor(args[2]) {
		for _, f := range target.Ty.VisibleFields() {
			if f.Name == lit.Value {
				return e.emitStaticDefineProperty(target, f, args[2], pos)
			}
		}
	}
	// A static object's or class instance's other properties live beside
	// its layout, as an array's do.
	if !target.Ty.IsDynamic() && (target.Ty.IsError() || target.Ty.IsFunc() || target.Ty.IsArray() ||
		(target.Ty.IsObject() || target.Ty.IsClass()) && !target.Ty.IsDynamicObject()) {
		if target, err = e.emitBoxValue(target); err != nil {
			return Value{}, err
		}
	}
	if target.Ty.IsDynamic() || target.Ty.IsUndefined() || target.Ty.IsNull() || scalarTypeKind(target.Ty) != "" || isStringTy(target.Ty) {
		// The target is checked first: `Object.defineProperty called on
		// non-object`.
		if target, err = e.emitBoxValue(target); err != nil {
			return Value{}, err
		}
		if err := e.emitRequireObject(target, "Object.defineProperty called on non-object"); err != nil {
			return Value{}, err
		}
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	descBag, err := e.emitDynBagOrThrow(args[2], "Object.defineProperty"+descriptorWhat, pos)
	if err != nil {
		return Value{}, err
	}
	// An Error's properties are its fields and its extra bag: the
	// descriptor's value is stored as an assignment stores it.
	errL, bagL, doneL := e.freshLabel("defprop.err"), e.freshLabel("defprop.bag"), e.freshLabel("defprop.done")
	if target.Ty.IsDynamic() {
		tag, payload := e.emitUnboxTagPayload(target)
		objL := e.freshLabel("defprop.obj")
		isObj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isObj, tag, kmlTagObject))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, bagL))
		e.emitLabel(objL)
		errPtr, isErr := e.emitBoxedErrorProbe(payload) // a boxed Error (field-0 flag)
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, errL, bagL))
		e.emitLabel(errL)
		// Whether the key was its own enumerable property before the store:
		// the descriptor's omitted `enumerable` keeps that.
		e.emitErrorOwnKeysHooks()
		e.declareFn("__kml_error_key_enumerable", "declare i64 @__kml_error_key_enumerable("+irPtr+", "+irPtr+")")
		e.declareFn("__kml_error_define_enum", "declare void @__kml_error_define_enum("+irPtr+", "+irPtr+", i64, "+irPtr+")")
		was := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_error_key_enumerable("+irPtr+" %s, "+irPtr+" %s)", was, errPtr, keyRef))
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get("+irPtr+" %s, "+irPtr+" %s)", v, descBag, e.internString("value")))
		e.emitErrorDefineAttrs(errPtr, keyRef, descBag, v)
		set := func() (Value, error) {
			return e.emitDynAnyMemberSet(target, keyRef, Value{Ref: v, Ty: TypeAny}, pos)
		}
		if lit, ok := args[1].(*ast.StringLiteral); ok {
			// A named key reaches the error's own field (`cause`, `code`).
			set = func() (Value, error) {
				return e.emitDynAnyMemberSetNamed(target, keyRef, lit.Value, Value{Ref: v, Ty: TypeAny}, pos)
			}
		}
		// A descriptor without a value leaves the property's value as it is.
		hasV := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", hasV, v, nbUndefined))
		setL, setDoneL := e.freshLabel("defprop.errset"), e.freshLabel("defprop.errsetdone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasV, setL, setDoneL))
		e.emitLabel(setL)
		if _, err := set(); err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", setDoneL))
		e.emitLabel(setDoneL)
		e.emitInstr(fmt.Sprintf("call void @__kml_error_define_enum("+irPtr+" %s, "+irPtr+" %s, i64 %s, "+irPtr+" %s)", errPtr, keyRef, was, descBag))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(bagL)
	}
	if target.Ty.IsPromise() && !target.Ty.IsDynamic() {
		// A promise's own properties live on its box, a static object.
		var err error
		if target, err = e.emitBoxValue(target); err != nil {
			return Value{}, err
		}
	}
	if target.Ty.IsDynamic() {
		// An array or static object keeps its other properties, their
		// attributes and accessors, in the added-property table.
		e.emitArrayDefineProperty(target, keyRef, descBag, e.freshLabel("defprop.rest"), doneL)
	}
	e.defineOnClassOwn = true
	bag, obj, err := e.emitDynOwnBagOf(target, "Object.defineProperty", true, pos)
	e.defineOnClassOwn = false
	if err != nil {
		return Value{}, err
	}
	e.emitDefinePropertyOn(bag, keyRef, descBag)
	if target.Ty.IsDynamic() {
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
	}
	return obj, nil
}

// emitObjectDefineProperties is Object.defineProperties(o, props): each of
// props' own enumerable keys defined on o by its descriptor, as
// Object.defineProperty does; the result is o.
func (e *Emitter) emitObjectDefineProperties(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 {
		// No properties object: ToObject(undefined) throws, after the
		// target's own check.
		args = append(append([]ast.Expression{}, args...), ast.NewNullLiteral(true, pos))
		if len(args) < 2 {
			args = append([]ast.Expression{ast.NewNullLiteral(true, pos)}, args...)
		}
	}
	if len(args) > 2 {
		kept, err := e.dropArgsPast(args, 2, pos)
		if err != nil {
			return Value{}, err
		}
		args = kept
	}
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	obj, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !obj.Ty.IsDynamic() && (obj.Ty.IsFunc() || obj.Ty.IsArray() || (obj.Ty.IsObject() || obj.Ty.IsClass()) && !obj.Ty.IsDynamicObject()) {
		if obj, err = e.emitBoxValue(obj); err != nil {
			return Value{}, err
		}
	}
	if !obj.Ty.IsDynamic() && (scalarTypeKind(obj.Ty) != "" || isStringTy(obj.Ty) || obj.Ty.IsUndefined() || obj.Ty.IsNull()) {
		if obj, err = e.emitBoxValue(obj); err != nil {
			return Value{}, err
		}
	}
	if !obj.Ty.IsDynamic() {
		// Any other value (a Map, a Date, an Error, a tuple): its box.
		if obj, err = e.emitBoxValue(obj); err != nil {
			return Value{}, err
		}
	}
	if err := e.emitRequireObject(obj, "Object.defineProperties called on non-object"); err != nil {
		return Value{}, err
	}
	pv, err := e.emitExprWithObjectHint(args[1], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !pv.Ty.IsDynamic() && pv.Ty.IsObject() && !pv.Ty.IsClass() {
		// A typed descriptor map is only read: its copy as a bag (its
		// nested descriptors too) answers the same keys and values.
		if pv, err = e.emitStaticObjToBag(pv); err != nil {
			return Value{}, err
		}
	}
	props, _, err := e.emitDynOwnBagOf(pv, "Object.defineProperties (properties)", false, pos)
	if err != nil {
		return Value{}, err
	}
	// Each key of the descriptor map in order (its string keys, then its
	// symbols), key(data, i) naming the i-th.
	var defErr error
	each := func(data, n string, key func(data, i string) string) {
		iSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iSlot))
		e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", iSlot))
		condL, bodyL, endL := e.freshLabel("defprops.cond"), e.freshLabel("defprops.body"), e.freshLabel("defprops.end")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		i := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", i, iSlot))
		more := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, n))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, endL))
		e.emitLabel(bodyL)
		k := key(data, i)
		desc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get("+irPtr+" %s, "+irPtr+" %s)", desc, props, k))
		dtag, dpay := e.emitUnboxTagPayload(Value{Ref: desc, Ty: TypeAny})
		okL, badL := e.emitTagCheck(dtag, kmlTagDynObject, "defprops.desc")
		e.emitLabel(badL)
		e.emitThrowTypeError("Property description must be an object")
		e.emitLabel(okL)
		if defErr == nil {
			defErr = e.emitDefinePropertyAny(obj, k, e.emitIntToPtr(dpay), "Object.defineProperties", pos)
		}
		i2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i2, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", i2, iSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(endL)
	}
	keys := e.emitDynObjKeyList(props, true)
	data, n := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { "+irPtr+", i64 } %s, 0", data, keys))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { "+irPtr+", i64 } %s, 1", n, keys))
	each(data, n, func(data, i string) string {
		kp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %s", kp, data, i))
		key := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", key, kp))
		return key
	})
	syms, err := e.emitOwnEnumSymbolsOf(Value{Ref: e.emitNbTagPtr(props, kmlTagDynObject), Ty: TypeAny})
	if err != nil {
		return Value{}, err
	}
	each(e.arrayData(syms), e.arrayLen(syms), func(data, i string) string {
		sp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr "+irPtr+", "+irPtr+" %s, i64 %s", sp, data, i))
		sym := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", sym, sp))
		return e.emitSymbolPropertyKey(Value{Ref: sym, Ty: SymbolType()})
	})
	return obj, defErr
}

// emitDefinePropertyAny applies descriptor descBag to property keyRef of
// the boxed obj: an array's or static object's property beside its
// elements or layout, else a dynamic object's (or function's) own.
func (e *Emitter) emitDefinePropertyAny(obj Value, keyRef, descBag, what string, pos ast.Pos) error {
	doneL, restL := e.freshLabel("defany.done"), e.freshLabel("defany.rest")
	e.emitArrayDefineProperty(obj, keyRef, descBag, restL, doneL)
	bag, _, err := e.emitDynOwnBagOf(obj, what, true, pos)
	if err != nil {
		return err
	}
	e.emitDefinePropertyOn(bag, keyRef, descBag)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return nil
}

// emitArrayDefineProperty applies descriptor descBag to property keyRef of
// the boxed target when it is an array or a static object: on to doneL,
// else (another value, or a static object's field) on at restL, which it
// opens.
func (e *Emitter) emitArrayDefineProperty(target Value, keyRef, descBag, restL, doneL string) {
	// An accessor element is marked in the array's holes, and every read
	// of an element asks holes.c for its getter's value.
	e.declareFn("__kml_anyarr_defprop", "declare i32 @__kml_anyarr_defprop(i64, "+irPtr+", "+irPtr+")")
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_anyarr_defprop(i64 %s, "+irPtr+" %s, "+irPtr+" %s)", r, target.Ref, keyRef, descBag))
	notArr, isArr := e.freshReg(), e.freshLabel("defprop.arr")
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", notArr, r))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", notArr, restL, isArr))
	e.emitLabel(isArr)
	ok := e.freshReg()
	okL, badL := e.freshLabel("defprop.arrok"), e.freshLabel("defprop.arrbad")
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", ok, r))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, badL))
	e.emitLabel(badL)
	redef, redefL, accL := e.freshReg(), e.freshLabel("defprop.redef"), e.freshLabel("defprop.acc")
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 2", redef, r))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", redef, redefL, accL))
	e.emitLabel(redefL)
	e.emitThrowRedefine(keyRef)
	e.emitLabel(accL)
	badLen, lenL, accOnlyL := e.freshReg(), e.freshLabel("defprop.badlen"), e.freshLabel("defprop.accel")
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 4", badLen, r))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badLen, lenL, accOnlyL))
	e.emitLabel(lenL)
	e.emitThrowRangeError("Invalid array length")
	e.emitLabel(accOnlyL)
	badIdx, idxL, accElL := e.freshReg(), e.freshLabel("defprop.badidx"), e.freshLabel("defprop.accelem")
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 5", badIdx, r))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badIdx, idxL, accElL))
	e.emitLabel(idxL)
	e.emitThrowTypeError("Invalid typed array index")
	e.emitLabel(accElL)
	e.emitThrowRedefine(keyRef)
	e.emitLabel(okL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(restL)
}

// emitThrowRedefine throws V8's TypeError for a refused redefinition of
// the run-time property key keyRef.
func (e *Emitter) emitThrowRedefine(keyRef string) {
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	e.declareFn("__kml_redefine_msg", "declare "+irPtr+" @__kml_redefine_msg("+irPtr+")")
	msg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_redefine_msg("+irPtr+" %s)", msg, keyRef))
	e.emitThrowTypeErrorValue(msg)
}

// emitDefinePropertyOn applies descriptor descBag to property keyRef of the
// dynamic-object bag.
func (e *Emitter) emitDefinePropertyOn(bag, keyRef, descBag string) {
	// A Proxy defines through its defineProperty trap, else its target.
	e.ensureShapeRuntime()
	e.declareFn("__kml_proxy_define", "declare "+irPtr+" @__kml_proxy_define("+irPtr+", "+irPtr+", "+irPtr+")")
	tgt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_proxy_define("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s)", tgt, bag, keyRef, descBag))
	trapped, ownL, trapL := e.freshReg(), e.freshLabel("defprop.own"), e.freshLabel("defprop.trapped")
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", trapped, tgt))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", trapped, trapL, ownL))
	e.emitLabel(ownL)
	bag = tgt
	defer func() {
		after := e.freshLabel("defprop.after")
		e.emitTerminator(fmt.Sprintf("br label %%%s", after))
		e.emitLabel(trapL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", after))
		e.emitLabel(after)
	}()
	// Read the descriptor once.
	gPresent, gRec := e.descFieldRec(descBag, "get")
	sPresent, sRec := e.descFieldRec(descBag, "set")
	isAcc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isAcc, gPresent, sPresent))
	wPresent, wVal := e.descFieldBool(descBag, "writable")
	ePresent, eVal := e.descFieldBool(descBag, "enumerable")
	cPresent, cVal := e.descFieldBool(descBag, "configurable")
	valBox := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get("+irPtr+" %s, "+irPtr+" %s)", valBox, descBag, e.internString("value")))

	idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_find("+irPtr+" %s, "+irPtr+" %s)", idx, bag, keyRef))
	exists := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", exists, idx))

	existL := e.freshLabel("defprop.exist")
	freshL := e.freshLabel("defprop.fresh")
	writeL := e.freshLabel("defprop.write")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", exists, existL, freshL))

	// Existing property: a non-configurable one rejects any redefinition
	// except a value write while still writable (the one spec allowance the
	// V1 keeps).
	e.emitLabel(existL)
	oldAttrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_attrs_at("+irPtr+" %s, i64 %s)", oldAttrs, bag, idx))
	oc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", oc, oldAttrs, dynAttrConfigurable))
	confOK := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", confOK, oc))
	ncL := e.freshLabel("defprop.nc")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", confOK, writeL, ncL))
	e.emitLabel(ncL)
	ow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", ow, oldAttrs, dynAttrWritable))
	stillW := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", stillW, ow))
	// permitted iff (ValidateAndApplyPropertyDescriptor): a data
	// descriptor that leaves configurable false and enumerable as it is;
	// over a read-only property, one that neither makes it writable nor
	// changes its value.
	notAcc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notAcc, isAcc))
	oe := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", oe, oldAttrs, dynAttrEnumerable))
	oldE, eSame, eOK := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", oldE, oe))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i1 %s, %s", eSame, oldE, eVal))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i1 %s, i1 true", eOK, ePresent, eSame))
	cOn, cOK2 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", cOn, cPresent, cVal))
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", cOK2, cOn))
	wOn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", wOn, wPresent, wVal))
	ncV, ncVPresent, ncSame := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get_at("+irPtr+" %s, i64 %s)", ncV, bag, idx))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", ncVPresent, valBox, nbUndefined))
	e.ensureBoxedBigIntHooks()
	e.ensureFnMeta()
	e.declareFn("__kml_Object_is", "declare zeroext i1 @__kml_Object_is(i64, i64)")
	e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_Object_is(i64 %s, i64 %s)", ncSame, ncV, valBox))
	vOK, roOK, dataOK := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i1 %s, i1 true", vOK, ncVPresent, ncSame))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i1 false, i1 %s", roOK, wOn, vOK))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", dataOK, stillW, roOK))
	p1, p2 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", p1, notAcc, dataOK))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", p2, p1, eOK))
	permitted := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", permitted, p2, cOK2))
	ncOKL := e.freshLabel("defprop.ncok")
	ncErrL := e.freshLabel("defprop.ncerr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", permitted, ncOKL, ncErrL))
	e.emitLabel(ncErrL)
	e.emitThrowRedefine(keyRef)
	e.emitLabel(ncOKL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", writeL))

	// New property: extensibility check, then a raw append (patched below).
	e.emitLabel(freshL)
	ext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_flags_test("+irPtr+" %s, i64 0)", ext, bag))
	extOKL := e.freshLabel("defprop.extok")
	extErrL := e.freshLabel("defprop.exterr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ext, extOKL, extErrL))
	e.emitLabel(extErrL)
	e.emitThrowTypeError("Cannot define property, object is not extensible")
	e.emitLabel(extOKL)
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set("+irPtr+" %s, "+irPtr+" %s, i64 %d)", bag, keyRef, nbUndefined))
	e.emitTerminator(fmt.Sprintf("br label %%%s", writeL))

	// Write the merged entry. Attribute source: descriptor field when
	// present; else the old attribute for an existing property, false for a
	// fresh one. (existsFinal distinguishes the two via the pre-branch reg.)
	e.emitLabel(writeL)
	fidx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_find("+irPtr+" %s, "+irPtr+" %s)", fidx, bag, keyRef))
	curAttrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_attrs_at("+irPtr+" %s, i64 %s)", curAttrs, bag, fidx))
	// For a fresh property the raw append set attrs=7; mask them to 0 so
	// absent fields default false. An existing property keeps its old bits.
	baseAttrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", baseAttrs, exists, curAttrs))
	mergeBit := func(present, val string, bit int) string {
		oldBit := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", oldBit, baseAttrs, bit))
		newBit := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 0", newBit, val, bit))
		merged := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", merged, present, newBit, oldBit))
		return merged
	}
	wBit := mergeBit(wPresent, wVal, dynAttrWritable)
	eBit := mergeBit(ePresent, eVal, dynAttrEnumerable)
	cBit := mergeBit(cPresent, cVal, dynAttrConfigurable)
	a1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", a1, wBit, eBit))
	dataAttrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", dataAttrs, a1, cBit))

	accL := e.freshLabel("defprop.acc")
	dataL := e.freshLabel("defprop.data")
	doneL := e.freshLabel("defprop.done")
	keepL := e.freshLabel("defprop.keepacc")
	notAccL := e.freshLabel("defprop.notacc")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAcc, accL, notAccL))

	// A generic descriptor (no value, writable, get or set) over an
	// accessor keeps the accessor, changing only the attributes it names.
	e.emitLabel(notAccL)
	wasAccK := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", wasAccK, curAttrs, dynAttrAccessor))
	isAccK, hasVal, dataField, generic, keep := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isAccK, wasAccK))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", hasVal, valBox, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", dataField, hasVal, wPresent))
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", generic, dataField))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", keep, generic, isAccK))
	keep2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", keep2, keep, exists))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", keep2, keepL, dataL))
	e.emitLabel(keepL)
	kpay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_rawpay_at("+irPtr+" %s, i64 %s)", kpay, bag, fidx))
	kAttrs0, kAttrs := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", kAttrs0, eBit, cBit))
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", kAttrs, kAttrs0, dynAttrAccessor))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_patch("+irPtr+" %s, i64 %s, i64 0, i64 %s, i64 %s)", bag, fidx, kpay, kAttrs))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(accL)
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_defacc("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s)", bag, keyRef, gRec, sRec))
	aidx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_find("+irPtr+" %s, "+irPtr+" %s)", aidx, bag, keyRef))
	apay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_rawpay_at("+irPtr+" %s, i64 %s)", apay, bag, aidx))
	accAttrs0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", accAttrs0, eBit, cBit))
	accAttrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", accAttrs, accAttrs0, dynAttrAccessor))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_patch("+irPtr+" %s, i64 %s, i64 0, i64 %s, i64 %s)", bag, aidx, apay, accAttrs))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(dataL)
	// value: present descriptor field, else the existing value (fresh append
	// already holds undefined).
	vPresent := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", vPresent, valBox, nbUndefined))
	curVal0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get_at("+irPtr+" %s, i64 %s)", curVal0, bag, fidx))
	// An accessor entry converting to data without an explicit value starts
	// at undefined — its raw payload is the pair pointer, not a value.
	wasAccB := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", wasAccB, curAttrs, dynAttrAccessor))
	wasAcc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", wasAcc, wasAccB))
	curVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", curVal, wasAcc, nbUndefined, curVal0))
	finalVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", finalVal, vPresent, valBox, curVal))
	ftag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", ftag, finalVal))
	ftag64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", ftag64, ftag))
	fpay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", fpay, finalVal))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_patch("+irPtr+" %s, i64 %s, i64 %s, i64 %s, i64 %s)", bag, fidx, ftag64, fpay, dataAttrs))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
}

// emitObjectGetOwnPropertyDescriptor builds the descriptor object for an own
// property (or undefined).
func (e *Emitter) emitObjectGetOwnPropertyDescriptor(args []ast.Expression, pos ast.Pos) (Value, error) {
	args, err := e.fitArgs(args, 2, pos) // (a missing argument is undefined)
	if err != nil {
		return Value{}, err
	}
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !v.Ty.IsDynamic() && (v.Ty.IsObject() || v.Ty.IsClass() || v.Ty.IsError() || v.Ty.IsArray() || isStringTy(v.Ty) || scalarTypeKind(v.Ty) != "" || v.Ty.IsUndefined() || v.Ty.IsNull()) && !v.Ty.IsDynamicObject() {
		// A static object, class instance, Error or array: its layout
		// answers; a primitive: its wrapper's (ToObject).
		if v, err = e.emitBoxValue(v); err != nil {
			return Value{}, err
		}
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", nbUndefined, resPtr))
	doneL := e.freshLabel("gopd.done")
	if v.Ty.IsDynamic() {
		// An array: its elements, length and added properties.
		e.declareFn("__kml_anyarr_gopd", "declare i32 @__kml_anyarr_gopd(i64, "+irPtr+", "+irPtr+")")
		ar := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_anyarr_gopd(i64 %s, "+irPtr+" %s, "+irPtr+" %s)", ar, v.Ref, keyRef, resPtr))
		isArr, arrL, restL := e.freshReg(), e.freshLabel("gopd.arr"), e.freshLabel("gopd.rest")
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", isArr, ar))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, arrL, restL))
		e.emitLabel(arrL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(restL)
		// A primitive string: its String object's index or length.
		e.ensureShapeRuntime()
		e.declareFn("__kml_str_own_desc", "declare i64 @__kml_str_own_desc("+irPtr+", "+irPtr+")")
		stag, spay := e.emitUnboxTagPayload(v)
		strL, notStrL := e.emitTagCheck(stag, kmlTagString, "gopd.str")
		e.emitLabel(strL)
		strd := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_own_desc("+irPtr+" %s, "+irPtr+" %s)", strd, e.emitIntToPtr(spay), keyRef))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", strd, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(notStrL)
		// A boxed static object reads its own field through its layout.
		e.ensureAnyToPrimitive()
		e.ensureNanBox()
		e.ensureErrorNames()
		e.ensureStrHeaderRuntime()
		e.ensureShapeRuntime()
		tag, payload := e.emitUnboxTagPayload(v)
		staticL, dynL := e.emitTagCheck(tag, kmlTagObject, "gopd.static")
		e.emitLabel(staticL)
		// An Error owns its message, cause, stack and system fields.
		errPtr, isErr := e.emitBoxedErrorProbe(payload)
		errL, shapeL := e.freshLabel("gopd.err"), e.freshLabel("gopd.shape")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, errL, shapeL))
		e.emitLabel(errL)
		e.emitErrorOwnKeysHooks()
		e.declareFn("__kml_error_own_desc", "declare i64 @__kml_error_own_desc("+irPtr+", "+irPtr+")")
		ed := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_error_own_desc("+irPtr+" %s, "+irPtr+" %s)", ed, errPtr, keyRef))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", ed, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(shapeL)
		sd := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_own_desc("+irPtr+" %s, "+irPtr+" %s)", sd, e.emitIntToPtr(payload), keyRef))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", sd, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(dynL)
	}
	bag, _, err := e.emitDynOwnBagOf(v, "Object.getOwnPropertyDescriptor", true, pos)
	if err != nil {
		return Value{}, err
	}
	// [[GetOwnProperty]]: a bag's own property, or a Proxy's trap.
	e.declareFn("__kml_dynobj_own_desc", "declare i64 @__kml_dynobj_own_desc("+irPtr+", "+irPtr+")")
	dd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_own_desc("+irPtr+" %s, "+irPtr+" %s)", dd, bag, keyRef))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", dd, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", out, resPtr))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// emitObjectGetOwnPropertyNames returns every own key (non-enumerable
// included), insertion order.
func (e *Emitter) emitObjectGetOwnPropertyNames(args []ast.Expression, pos ast.Pos) (Value, error) {
	args, err := e.fitArgs(args, 1, pos) // (a missing argument is undefined)
	if err != nil {
		return Value{}, err
	}
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	return e.emitOwnPropertyNamesOf(v, "Object.getOwnPropertyNames", pos)
}

// toObjectStatics are the Object statics that coerce their argument with
// ToObject rather than requiring an object.
var toObjectStatics = map[string]bool{"Object.getOwnPropertyNames": true, "Object.getOwnPropertyDescriptor": true, "Object.defineProperties (properties)": true}

// emitOwnPropertyNamesOf is getOwnPropertyNames of the evaluated v, for
// the caller what names (Object.getOwnPropertyNames, Reflect.ownKeys).
func (e *Emitter) emitOwnPropertyNamesOf(v Value, what string, pos ast.Pos) (Value, error) {
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	var err error
	if !v.Ty.IsDynamic() && (v.Ty.IsObject() || v.Ty.IsClass() || v.Ty.IsArray() || isStringTy(v.Ty) || scalarTypeKind(v.Ty) != "") && !v.Ty.IsDynamicObject() {
		if v, err = e.emitBoxValue(v); err != nil {
			return Value{}, err
		}
	}
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca { "+irPtr+", i64 }, align 8", resPtr))
	doneL := e.freshLabel("gopn.done")
	if v.Ty.IsDynamic() {
		e.emitWrapperKeys(v, true, resPtr, doneL)
		// An array: its index keys, length and added properties.
		e.declareFn("__kml_anyarr_names_c", "declare "+irPtr+" @__kml_anyarr_names_c(i64, "+irPtr+")")
		np := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", np))
		arr, isArr := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_anyarr_names_c(i64 %s, "+irPtr+" %s)", arr, v.Ref, np))
		e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", isArr, arr))
		arrL, restL := e.freshLabel("gopn.arr"), e.freshLabel("gopn.rest")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, arrL, restL))
		e.emitLabel(arrL)
		n, a0, a1 := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", n, np))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { "+irPtr+", i64 } undef, "+irPtr+" %s, 0", a0, arr))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { "+irPtr+", i64 } %s, i64 %s, 1", a1, a0, n))
		e.emitInstr(fmt.Sprintf("store { "+irPtr+", i64 } %s, "+irPtr+" %s, align 8", a1, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(restL)
		// A primitive string: its String object's indices and length.
		e.declareFn("__kml_str_own_names_c", "declare "+irPtr+" @__kml_str_own_names_c("+irPtr+", "+irPtr+")")
		stag, spay := e.emitUnboxTagPayload(v)
		strL, notStrL := e.emitTagCheck(stag, kmlTagString, "gopn.str")
		e.emitLabel(strL)
		sn := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", sn))
		sarr, slen, s0, s1 := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_own_names_c("+irPtr+" %s, "+irPtr+" %s)", sarr, e.emitIntToPtr(spay), sn))
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", slen, sn))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { "+irPtr+", i64 } undef, "+irPtr+" %s, 0", s0, sarr))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { "+irPtr+", i64 } %s, i64 %s, 1", s1, s0, slen))
		e.emitInstr(fmt.Sprintf("store { "+irPtr+", i64 } %s, "+irPtr+" %s, align 8", s1, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(notStrL)
		// A boxed static object: its layout's keys, all enumerable.
		e.ensureShapeKeysArray()
		tag, payload := e.emitUnboxTagPayload(v)
		staticL, dynL := e.emitTagCheck(tag, kmlTagObject, "gopn.static")
		e.emitLabel(staticL)
		// its non-enumerable added properties too
		e.declareFn("__kml_shape_names_c", "declare void @__kml_shape_names_c("+irPtr+", "+irPtr+")")
		e.emitInstr(fmt.Sprintf("call void @__kml_shape_names_c("+irPtr+" %s, "+irPtr+" %s)", e.emitIntToPtr(payload), resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(dynL)
	}
	bag, _, err := e.emitDynOwnBagOf(v, what, true, pos)
	if err != nil {
		return Value{}, err
	}
	r := e.emitDynObjKeyList(bag, false)
	e.emitInstr(fmt.Sprintf("store { "+irPtr+", i64 } %s, "+irPtr+" %s, align 8", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load { "+irPtr+", i64 }, "+irPtr+" %s, align 8", out, resPtr))
	return e.arrayFromAgg(out, ArrayOf(TypePtr)), nil
}

// emitObjectGetOwnPropertySymbols is Object.getOwnPropertySymbols(o): the
// symbols of o's own symbol-keyed properties (shape.c's
// __kml_own_symbols_c), a static object's symbol-keyed fields among them.
func (e *Emitter) emitObjectGetOwnPropertySymbols(args []ast.Expression, pos ast.Pos) (Value, error) {
	args, err := e.fitArgs(args, 1, pos) // (a missing argument is undefined)
	if err != nil {
		return Value{}, err
	}
	v, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	return e.emitOwnSymbolsOf(v)
}

// emitOwnEnumSymbolsOf is the evaluated v's own enumerable symbol keys, the
// ones Object.assign copies and Object.defineProperties reads (v is neither
// null nor undefined).
func (e *Emitter) emitOwnEnumSymbolsOf(v Value) (Value, error) {
	boxed, err := e.emitBoxValue(v)
	if err != nil {
		return Value{}, err
	}
	e.ensureShapeRuntime()
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	e.declareFn("__kml_own_enum_symbols_c", "declare void @__kml_own_enum_symbols_c(i64, "+irPtr+")")
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca { "+irPtr+", i64 }, align 8", out))
	e.emitInstr(fmt.Sprintf("call void @__kml_own_enum_symbols_c(i64 %s, "+irPtr+" %s)", boxed.Ref, out))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load { "+irPtr+", i64 }, "+irPtr+" %s, align 8", r, out))
	return e.arrayFromAgg(r, ArrayOf(SymbolType())), nil
}

// emitOwnSymbolsOf is getOwnPropertySymbols of the evaluated v.
func (e *Emitter) emitOwnSymbolsOf(v Value) (Value, error) {
	boxed, err := e.emitBoxValue(v)
	if err != nil {
		return Value{}, err
	}
	e.ensureShapeRuntime()
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	// A Proxy's ownKeys result is read through __kml_anyarr_get_by_key or
	// the dynamic-array accessors.
	e.ensureDynJSONC()
	e.declareFn("__kml_own_symbols_checked", "declare "+irPtr+" @__kml_own_symbols_checked(i64, "+irPtr+")")
	boxed = e.classRefAsBag(boxed)
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca { "+irPtr+", i64 }, align 8", out))
	// A null or undefined argument, or a Proxy whose ownKeys trap breaks
	// the spec's invariants, is a TypeError.
	msg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_own_symbols_checked(i64 %s, "+irPtr+" %s)", msg, boxed.Ref, out))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", bad, msg))
	throwL, okL := e.freshLabel("ownsyms.throw"), e.freshLabel("ownsyms.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, throwL, okL))
	e.emitLabel(throwL)
	e.emitThrowTypeErrorValue(msg)
	e.emitLabel(okL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load { "+irPtr+", i64 }, "+irPtr+" %s, align 8", r, out))
	return e.arrayFromAgg(r, ArrayOf(SymbolType())), nil
}

// emitReflectOwnKeys is Reflect.ownKeys(o): o's own string keys, then its
// own symbols, as one (string | symbol)[] (shape.c's __kml_own_keys_c).
func (e *Emitter) emitReflectOwnKeys(arg ast.Expression, pos ast.Pos) (Value, error) {
	v, err := e.emitExprWithObjectHint(arg, TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !v.Ty.IsDynamic() && (v.Ty.IsObject() || v.Ty.IsClass()) && !v.Ty.IsDynamicObject() {
		if v, err = e.emitBoxValue(v); err != nil {
			return Value{}, err
		}
	}
	out := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca { "+irPtr+", i64 }, align 8", out))
	doneL := ""
	if v.Ty.IsDynamic() {
		// A Proxy: its [[OwnPropertyKeys]] once, in the trap's order.
		boxed, err := e.emitBoxValue(v)
		if err != nil {
			return Value{}, err
		}
		e.ensureShapeRuntime()
		e.declareFn("__kml_proxy_reflect_own_keys", "declare i32 @__kml_proxy_reflect_own_keys(i64, "+irPtr+", "+irPtr+")")
		errSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", errSlot))
		isPx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_proxy_reflect_own_keys(i64 %s, "+irPtr+" %s, "+irPtr+" %s)", isPx, boxed.Ref, out, errSlot))
		pxB := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", pxB, isPx))
		pxL, ordL := e.freshLabel("ownkeys.proxy"), e.freshLabel("ownkeys.ord")
		doneL = e.freshLabel("ownkeys.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", pxB, pxL, ordL))
		e.emitLabel(pxL)
		msg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", msg, errSlot))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", bad, msg))
		throwL := e.freshLabel("ownkeys.throw")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, throwL, doneL))
		e.emitLabel(throwL)
		e.emitThrowTypeErrorValue(msg)
		e.emitLabel(ordL)
	}
	names, err := e.emitOwnPropertyNamesOf(v, "Reflect.ownKeys", pos)
	if err != nil {
		return Value{}, err
	}
	syms, err := e.emitOwnSymbolsOf(v)
	if err != nil {
		return Value{}, err
	}
	e.declareFn("__kml_own_keys_c", "declare void @__kml_own_keys_c({ "+irPtr+", i64 }, { "+irPtr+", i64 }, "+irPtr+")")
	e.emitInstr(fmt.Sprintf("call void @__kml_own_keys_c({ "+irPtr+", i64 } %s, { "+irPtr+", i64 } %s, "+irPtr+" %s)", e.arrayAggOf(names), e.arrayAggOf(syms), out))
	if doneL != "" {
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load { "+irPtr+", i64 }, "+irPtr+" %s, align 8", r, out))
	return e.arrayFromAgg(r, ArrayOf(TypeAny)), nil
}

// emitDynPrevent backs freeze/seal/preventExtensions on a dynamic object;
// returns the (boxed) object, like JS.
func (e *Emitter) emitDynPrevent(objVal Value, mode int) (Value, error) {
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	boxed, err := e.emitBoxValue(objVal)
	if err != nil {
		return Value{}, err
	}
	tag, payload := e.emitUnboxTagPayload(boxed)
	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynprev.obj")
	doneL := e.freshLabel("dynprev.done")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, bag, payload))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_prevent("+irPtr+" %s, i64 %d)", bag, mode))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)
	// A function's own properties are its bag's.
	fnL, otherL := e.emitTagCheck(tag, kmlTagDynFunc, "dynprev.fn")
	e.emitLabel(fnL)
	fbag := e.emitFnBagOf(payload)
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_prevent("+irPtr+" %s, i64 %d)", fbag, mode))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(otherL)
	// A host value (a Map, a Date) records its level in its header; any
	// other non-object argument is returned unchanged.
	hostL, primL := e.emitTagCheck(tag, kmlTagObject, "dynprev.host")
	e.emitLabel(hostL)
	e.ensureShapeRuntime()
	e.declareFn("__kml_host_prevent", "declare i1 @__kml_host_prevent("+irPtr+", i64)")
	e.emitInstr(fmt.Sprintf("call i1 @__kml_host_prevent("+irPtr+" %s, i64 %d)", e.emitIntToPtr(payload), mode))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(primL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return boxed, nil
}

// emitDynFlagsTest backs isExtensible/isSealed/isFrozen on a dynamic value.
// A non-object answers per JS: isExtensible false, isSealed/isFrozen true.
func (e *Emitter) emitDynFlagsTest(objVal Value, mode int) (Value, error) {
	e.ensureAnyToPrimitive()
	e.ensureNanBox()
	e.ensureErrorNames()
	e.ensureStrHeaderRuntime()
	boxed, err := e.emitBoxValue(objVal)
	if err != nil {
		return Value{}, err
	}
	tag, payload := e.emitUnboxTagPayload(boxed)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
	nonObjDefault := "true"
	if mode == 0 {
		nonObjDefault = "false"
	}
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", nonObjDefault, resPtr))
	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynflag.obj")
	doneL := e.freshLabel("dynflag.done")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, bag, payload))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_flags_test("+irPtr+" %s, i64 %d)", r, bag, mode))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)
	fnL, otherL := e.emitTagCheck(tag, kmlTagDynFunc, "dynflag.fn")
	e.emitLabel(fnL)
	fbag := e.emitFnBagOf(payload)
	fr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_flags_test("+irPtr+" %s, i64 %d)", fr, fbag, mode))
	e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", fr, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(otherL)
	// A host value (a Map, a Date): from its header's level.
	hostL, primL := e.emitTagCheck(tag, kmlTagObject, "dynflag.host")
	e.emitLabel(hostL)
	e.ensureShapeRuntime()
	e.declareFn("__kml_host_level_test", "declare i1 @__kml_host_level_test("+irPtr+", i64, "+irPtr+")")
	e.emitInstr(fmt.Sprintf("call i1 @__kml_host_level_test("+irPtr+" %s, i64 %d, "+irPtr+" %s)", e.emitIntToPtr(payload), mode, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(primL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", out, resPtr))
	return Value{Ref: out, Ty: TypeBool}, nil
}

// emitFnBagOf is the property bag of the boxed function whose payload is
// payload (fnmeta.c), made on first use.
func (e *Emitter) emitFnBagOf(payload string) string {
	e.ensureFnMeta()
	e.declareFn("__kml_fn_bag_of", "declare "+irPtr+" @__kml_fn_bag_of("+irPtr+")")
	rec, bag := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, rec, payload))
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fn_bag_of("+irPtr+" %s)", bag, rec))
	return bag
}

// classRefAsBag is boxed, or for a reference to a class of the program a
// boxed snapshot of that class's own properties.
func (e *Emitter) classRefAsBag(boxed Value) Value {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", boxed.Ref, slot))
	tag, payload := e.emitUnboxTagPayload(boxed)
	crL, doneL := e.emitTagCheck(tag, kmlTagFuncRef, "crbag.ref")
	e.emitLabel(crL)
	e.usedClassRefBag = true
	e.declareFn("__kml_classref_bag", "declare "+irPtr+" @__kml_classref_bag("+irPtr+")")
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_classref_bag("+irPtr+" %s)", bag, e.emitIntToPtr(payload)))
	some := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", some, bag))
	haveL := e.freshLabel("crbag.have")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", some, haveL, doneL))
	e.emitLabel(haveL)
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", e.emitNbTagPtr(bag, kmlTagDynObject), slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, slot))
	return Value{Ref: r, Ty: boxed.Ty}
}

// accessorDescriptor reports a descriptor literal naming get or set.
func accessorDescriptor(d ast.Expression) bool {
	lit, ok := d.(*ast.ObjectLiteral)
	if !ok {
		return false
	}
	for _, p := range lit.Properties {
		if p.Key == "get" || p.Key == "set" {
			return true
		}
	}
	return false
}

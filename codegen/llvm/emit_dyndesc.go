package llvm

// emit_dyndesc.go — property descriptors on dynamic objects (TDD-00155
// Stage 5): Object.defineProperty / getOwnPropertyDescriptor /
// getOwnPropertyNames, accessor properties (object-literal get/set and
// descriptor-installed), and real freeze/seal/preventExtensions with their
// is* probes — all over the attrs word the bag has carried since Stage 1.

import (
	"fmt"

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
func (e *Emitter) emitDynBagOrThrow(arg ast.Expression, what string, pos ast.Pos) (string, error) {
	v, err := e.emitExprWithObjectHint(arg, TypeAny)
	if err != nil {
		return "", err
	}
	if !v.Ty.IsDynamic {
		return "", fmt.Errorf("%d:%d: %s requires a dynamic (any-typed) object", pos.Line, pos.Col, what)
	}
	tag, payload := e.emitUnboxTagPayload(v)
	okL, badL := e.emitTagCheck(tag, kmlTagDynObject, "dyndesc.obj")
	contL := e.freshLabel("dyndesc.cont")
	e.emitLabel(okL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
	e.emitLabel(badL)
	e.emitThrowTypeError(what + " called on non-object")
	e.emitLabel(contL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	return bag, nil
}

// descFieldBool reads a boolean-ish descriptor field: returns (present i1,
// value i1) registers. An undefined field is absent.
func (e *Emitter) descFieldBool(descBag, name string) (present, val string) {
	e.ensureDynObj()
	e.ensureAnyOps()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", r, descBag, e.internString(name)))
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
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", r, descBag, e.internString(name)))
	present = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", present, r, nbUndefined))
	tag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_nb_tag(i64 %s)", tag, r))
	isfn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isfn, tag, kmlTagDynFunc))
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, r))
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, pay))
	rec = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr null", rec, isfn, p))
	return present, rec
}

// emitObjectDefineProperty implements Object.defineProperty(obj, key, desc)
// on dynamic objects. V1 semantics: create defaults every absent attribute
// to false (per spec); redefinition merges present fields; a
// non-configurable property rejects any change except a value write while
// still writable; a new property on a non-extensible object rejects.
func (e *Emitter) emitObjectDefineProperty(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: Object.defineProperty takes 3 arguments", pos.Line, pos.Col)
	}
	e.ensureDynObj()
	e.ensureAnyOps()
	bag, err := e.emitDynBagOrThrow(args[0], "Object.defineProperty", pos)
	if err != nil {
		return Value{}, err
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	descBag, err := e.emitDynBagOrThrow(args[2], "Object.defineProperty (descriptor)", pos)
	if err != nil {
		return Value{}, err
	}

	// Read the descriptor once.
	gPresent, gRec := e.descFieldRec(descBag, "get")
	sPresent, sRec := e.descFieldRec(descBag, "set")
	isAcc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isAcc, gPresent, sPresent))
	wPresent, wVal := e.descFieldBool(descBag, "writable")
	ePresent, eVal := e.descFieldBool(descBag, "enumerable")
	cPresent, cVal := e.descFieldBool(descBag, "configurable")
	valBox := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", valBox, descBag, e.internString("value")))

	idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_find(ptr %s, ptr %s)", idx, bag, keyRef))
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
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_attrs_at(ptr %s, i64 %s)", oldAttrs, bag, idx))
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
	// permitted iff: still-writable data prop, and the descriptor is a plain
	// data descriptor that doesn't try to flip enumerable/configurable on.
	notAcc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notAcc, isAcc))
	noE := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", noE, ePresent))
	noC := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", noC, cPresent))
	p1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", p1, stillW, notAcc))
	p2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", p2, p1, noE))
	permitted := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", permitted, p2, noC))
	ncOKL := e.freshLabel("defprop.ncok")
	ncErrL := e.freshLabel("defprop.ncerr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", permitted, ncOKL, ncErrL))
	e.emitLabel(ncErrL)
	e.emitThrowTypeError("Cannot redefine property")
	e.emitLabel(ncOKL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", writeL))

	// New property: extensibility check, then a raw append (patched below).
	e.emitLabel(freshL)
	ext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_flags_test(ptr %s, i64 0)", ext, bag))
	extOKL := e.freshLabel("defprop.extok")
	extErrL := e.freshLabel("defprop.exterr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ext, extOKL, extErrL))
	e.emitLabel(extErrL)
	e.emitThrowTypeError("Cannot define property, object is not extensible")
	e.emitLabel(extOKL)
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %d)", bag, keyRef, nbUndefined))
	e.emitTerminator(fmt.Sprintf("br label %%%s", writeL))

	// Write the merged entry. Attribute source: descriptor field when
	// present; else the old attribute for an existing property, false for a
	// fresh one. (existsFinal distinguishes the two via the pre-branch reg.)
	e.emitLabel(writeL)
	fidx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_find(ptr %s, ptr %s)", fidx, bag, keyRef))
	curAttrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_attrs_at(ptr %s, i64 %s)", curAttrs, bag, fidx))
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
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAcc, accL, dataL))

	e.emitLabel(accL)
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_defacc(ptr %s, ptr %s, ptr %s, ptr %s)", bag, keyRef, gRec, sRec))
	aidx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_find(ptr %s, ptr %s)", aidx, bag, keyRef))
	apay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_rawpay_at(ptr %s, i64 %s)", apay, bag, aidx))
	accAttrs0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", accAttrs0, eBit, cBit))
	accAttrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", accAttrs, accAttrs0, dynAttrAccessor))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_patch(ptr %s, i64 %s, i64 0, i64 %s, i64 %s)", bag, aidx, apay, accAttrs))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(dataL)
	// value: present descriptor field, else the existing value (fresh append
	// already holds undefined).
	vPresent := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", vPresent, valBox, nbUndefined))
	curVal0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get_at(ptr %s, i64 %s)", curVal0, bag, fidx))
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
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_patch(ptr %s, i64 %s, i64 %s, i64 %s, i64 %s)", bag, fidx, ftag64, fpay, dataAttrs))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	bagBox := e.emitDynObjBox(bag)
	return bagBox, nil
}

// emitObjectGetOwnPropertyDescriptor builds the descriptor object for an own
// property (or undefined).
func (e *Emitter) emitObjectGetOwnPropertyDescriptor(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Object.getOwnPropertyDescriptor takes 2 arguments", pos.Line, pos.Col)
	}
	e.ensureDynObj()
	bag, err := e.emitDynBagOrThrow(args[0], "Object.getOwnPropertyDescriptor", pos)
	if err != nil {
		return Value{}, err
	}
	keyRef, err := e.dynAnyKeyRef(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, resPtr))
	idx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_find(ptr %s, ptr %s)", idx, bag, keyRef))
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", found, idx))
	buildL := e.freshLabel("gopd.build")
	doneL := e.freshLabel("gopd.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", found, buildL, doneL))

	e.emitLabel(buildL)
	attrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_attrs_at(ptr %s, i64 %s)", attrs, bag, idx))
	desc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", desc))
	boolBox := func(bit int) string {
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", b, attrs, bit))
		nz := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", nz, b))
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %d", v, nz, nbTrue, nbFalse))
		return v
	}
	accB := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", accB, attrs, dynAttrAccessor))
	isAcc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isAcc, accB))
	accL := e.freshLabel("gopd.acc")
	dataL := e.freshLabel("gopd.data")
	commonL := e.freshLabel("gopd.common")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAcc, accL, dataL))

	e.emitLabel(accL)
	pairI := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_rawpay_at(ptr %s, i64 %s)", pairI, bag, idx))
	pair := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", pair, pairI))
	recBox := func(off int) string {
		slot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", slot, pair, off))
		rec := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rec, slot))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, rec))
		ri := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", ri, rec))
		tagged := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, 7", tagged, ri))
		out := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", out, isNull, nbUndefined, tagged))
		return out
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", desc, e.internString("get"), recBox(0)))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", desc, e.internString("set"), recBox(8)))
	e.emitTerminator(fmt.Sprintf("br label %%%s", commonL))

	e.emitLabel(dataL)
	val := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get_at(ptr %s, i64 %s)", val, bag, idx))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", desc, e.internString("value"), val))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", desc, e.internString("writable"), boolBox(dynAttrWritable)))
	e.emitTerminator(fmt.Sprintf("br label %%%s", commonL))

	e.emitLabel(commonL)
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", desc, e.internString("enumerable"), boolBox(dynAttrEnumerable)))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", desc, e.internString("configurable"), boolBox(dynAttrConfigurable)))
	descBox := e.emitDynObjBox(desc)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", descBox.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, resPtr))
	return Value{Ref: out, Ty: TypeAny}, nil
}

// emitObjectGetOwnPropertyNames returns every own key (non-enumerable
// included), insertion order.
func (e *Emitter) emitObjectGetOwnPropertyNames(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.getOwnPropertyNames takes 1 argument", pos.Line, pos.Col)
	}
	e.ensureDynObj()
	bag, err := e.emitDynBagOrThrow(args[0], "Object.getOwnPropertyNames", pos)
	if err != nil {
		return Value{}, err
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_dynobj_keys(ptr %s)", r, bag))
	return Value{Ref: r, Ty: ArrayOf(TypePtr)}, nil
}

// emitDynPrevent backs freeze/seal/preventExtensions on a dynamic object;
// returns the (boxed) object, like JS.
func (e *Emitter) emitDynPrevent(objVal Value, mode int) (Value, error) {
	e.ensureDynObj()
	boxed, err := e.emitBoxValue(objVal)
	if err != nil {
		return Value{}, err
	}
	tag, payload := e.emitUnboxTagPayload(boxed)
	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynprev.obj")
	doneL := e.freshLabel("dynprev.done")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_prevent(ptr %s, i64 %d)", bag, mode))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)
	// JS: a non-object argument is returned unchanged.
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return boxed, nil
}

// emitDynFlagsTest backs isExtensible/isSealed/isFrozen on a dynamic value.
// A non-object answers per JS: isExtensible false, isSealed/isFrozen true.
func (e *Emitter) emitDynFlagsTest(objVal Value, mode int) (Value, error) {
	e.ensureDynObj()
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
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", nonObjDefault, resPtr))
	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynflag.obj")
	doneL := e.freshLabel("dynflag.done")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_flags_test(ptr %s, i64 %d)", r, bag, mode))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", out, resPtr))
	return Value{Ref: out, Ty: TypeBool}, nil
}

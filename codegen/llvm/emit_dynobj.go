package llvm

// emit_dynobj.go — codegen glue for the D1 dynamic object model (TDD-00155
// Stage 1). A dynamic object is an any-boxed (tag kmlTagDynObject) pointer to
// a __kml_dynobj property bag (runtime_dynobj.go). Compile-time type-wise it
// is only ever seen through any/unknown (TypeAny) — there is no new Type kind:
// the box tag is the runtime discriminator, checked at every member site.
//
// Stage-1 semantics on an any-typed base value, per runtime tag:
//   - dynamic object (10): real bag get/set/delete/has/keys
//   - null/undefined (4/5): TypeError, matching JS
//   - primitives (0-3): member reads yield undefined (JS auto-boxing without
//     the primitive prototypes — .length etc. stay a disclosed gap); writes
//     throw TypeError (strict mode)
//   - statically-shaped refs (6-9): TypeError — a static struct carries no
//     runtime shape, and lifting it into a dynamic view is Stage 6
//     (allocation-site widening), never a runtime migration

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitDynObjBox wraps a raw __kml_dynobj bag pointer register into an any box.
func (e *Emitter) emitDynObjBox(ptrReg string) Value {
	return Value{Ref: e.emitNbTagPtr(ptrReg, kmlTagDynObject), Ty: TypeAny}
}

// emitThrowTypeError emits an unconditional runtime TypeError throw. It ends
// the current block (unreachable terminator) — the caller resumes with
// emitLabel.
func (e *Emitter) emitThrowTypeError(msg string) {
	e.ensureExceptionHelpers()
	errObj := e.buildErrorObj(errorKindIDs["TypeError"], e.internString(msg), e.internString("TypeError"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errObj))
	e.emitTerminator("unreachable")
}

// dynAnyKeyRef evaluates a bracket key expression for a dynamic-object access
// and returns a string-pointer register: strings pass through, an any key is
// stringified with the runtime tag dispatch, an integer is formatted "%lld"
// (JS obj[1] ≡ obj["1"]), a float via the JS-faithful dtoa.
func (e *Emitter) dynAnyKeyRef(keyExpr ast.Expression, pos ast.Pos) (string, error) {
	kv, err := e.emitExpr(keyExpr)
	if err != nil {
		return "", err
	}
	switch {
	case kv.Ty.IsDynamic:
		s, err := e.emitDynamicToString(kv)
		if err != nil {
			return "", err
		}
		return s.Ref, nil
	case isStringTy(kv.Ty):
		return kv.Ref, nil
	case kv.Ty.Float:
		scratch := e.emitStringScratch(32)
		e.ensureDtoa()
		d := e.coerce(kv, TypeF64)
		e.emitInstr(fmt.Sprintf("call void @__kml_dtoa(ptr %s, double %s)", scratch, d.Ref))
		e.emitStringFinalizeLen(scratch)
		return scratch, nil
	case kv.Ty.IR == "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, kv.Ref, e.internString("true"), e.internString("false")))
		return r, nil
	default:
		e.ensureSprintf()
		scratch := e.emitStringScratch(32)
		i := e.coerce(kv, TypeI64)
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", scratch, e.internString("%lld"), i.Ref))
		e.emitStringFinalizeLen(scratch)
		return scratch, nil
	}
}

// emitDynAnyMemberGet reads property keyRef off an any-typed base value with
// the Stage-1 per-tag semantics described in the file comment. Returns an
// any-boxed value.
func (e *Emitter) emitDynAnyMemberGet(objVal Value, keyRef string, pos ast.Pos) (Value, error) {
	return e.emitDynAnyMemberGetNamed(objVal, keyRef, "", pos)
}

// emitDynAnyMemberGetNamed is emitDynAnyMemberGet with a compile-time
// property name (when the access is `x.prop` rather than a runtime-keyed
// bracket) so the null/undefined TypeError matches Node's
// "(reading 'prop')" suffix.
func (e *Emitter) emitDynAnyMemberGetNamed(objVal Value, keyRef, propName string, pos ast.Pos) (Value, error) {
	// `x.__proto__` reads the prototype link (TDD-00155 Stage 3), like the
	// Object.prototype accessor it stands in for.
	if propName == "__proto__" {
		return e.emitDynProtoRead(objVal, pos)
	}
	reading := ""
	if propName != "" {
		reading = fmt.Sprintf(" (reading '%s')", propName)
	}
	e.ensureDynObj()
	tag, payload := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	mergeL := e.freshLabel("dynget.merge")

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynget.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", r, bag, keyRef))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// Dynamic array (TDD-00155 Stage 2): numeric-string index or `length`.
	e.ensureDynArr()
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynArray, "dynget.arr")
	e.emitLabel(matchL)
	arrHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", arrHdr, payload))
	ar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynarr_get_by_key(ptr %s, ptr %s)", ar, arrHdr, keyRef))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ar, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagNull, "dynget.null")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Cannot read properties of null" + reading)
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagUndefined, "dynget.undef")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Cannot read properties of undefined" + reading)
	e.emitLabel(nextL)

	// Primitives (int/float/string/boolean) read as undefined; a
	// statically-shaped reference (object/array/funcRef/stream) has no runtime
	// shape to read from — a clean TypeError until Stage 6 widening.
	isPrim := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", isPrim, tag, kmlTagBoolean))
	primL := e.freshLabel("dynget.prim")
	errL := e.freshLabel("dynget.err")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isPrim, primL, errL))
	e.emitLabel(primL)
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbUndefined, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(errL)
	e.emitThrowTypeError("dynamic property access on a statically-typed value is not supported")

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: TypeAny}, nil
}

// emitDynAnyMemberSet writes property keyRef on an any-typed base value. Only
// a dynamic object accepts the write; everything else throws TypeError (JS
// strict-mode behavior for primitives; the honest rejection for
// statically-shaped refs). Returns the (boxed) assigned value.
func (e *Emitter) emitDynAnyMemberSet(objVal Value, keyRef string, rhs Value, pos ast.Pos) (Value, error) {
	return e.emitDynAnyMemberSetNamed(objVal, keyRef, "", rhs, pos)
}

// emitDynAnyMemberSetNamed is emitDynAnyMemberSet with a compile-time
// property name for Node-matching "(setting 'prop')" TypeError text.
func (e *Emitter) emitDynAnyMemberSetNamed(objVal Value, keyRef, propName string, rhs Value, pos ast.Pos) (Value, error) {
	// `x.__proto__ = p` re-links the prototype (TDD-00155 Stage 3): JS setter
	// semantics — non-object/non-null silently ignored, a cycle throws.
	if propName == "__proto__" {
		return e.emitDynProtoWrite(objVal, rhs, pos)
	}
	setting := ""
	if propName != "" {
		setting = fmt.Sprintf(" (setting '%s')", propName)
	}
	e.ensureDynObj()
	boxed, err := e.emitBoxValue(rhs)
	if err != nil {
		return Value{}, err
	}
	tag, payload := e.emitUnboxTagPayload(objVal)
	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynset.obj")
	doneL := e.freshLabel("dynset.done")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	// Stage 5: the checked assignment — accessors run, WRITABLE and the
	// extensibility bit are honored; a rejection is the JS strict TypeError.
	status := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", status, bag, keyRef, boxed.Ref))
	nameSfx := ""
	if propName != "" {
		nameSfx = " '" + propName + "'"
	}
	throwStatus := func(code int, msg string) {
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, status, code))
		badL := e.freshLabel("dynset.stbad")
		okL := e.freshLabel("dynset.stok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, badL, okL))
		e.emitLabel(badL)
		e.emitThrowTypeError(msg)
		e.emitLabel(okL)
	}
	throwStatus(1, "Cannot assign to read only property"+nameSfx+" of object")
	throwStatus(2, "Cannot add property"+nameSfx+", object is not extensible")
	throwStatus(3, "Cannot set property"+nameSfx+" of #<Object> which has only a getter")
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)

	// Dynamic array (TDD-00155 Stage 2): index writes only — an expando /
	// `length` write is a clean TypeError until arrays grow real properties.
	e.ensureDynArr()
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynArray, "dynset.arr")
	e.emitLabel(matchL)
	arrHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", arrHdr, payload))
	okReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynarr_set_by_key(ptr %s, ptr %s, i64 %s)", okReg, arrHdr, keyRef, boxed.Ref))
	arrOkL := e.freshLabel("dynset.arrok")
	arrErrL := e.freshLabel("dynset.arrerr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", okReg, arrOkL, arrErrL))
	e.emitLabel(arrErrL)
	e.emitThrowTypeError("only index assignments are supported on a dynamic array")
	e.emitLabel(arrOkL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagNull, "dynset.null")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Cannot set properties of null" + setting)
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagUndefined, "dynset.undef")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Cannot set properties of undefined" + setting)
	e.emitLabel(nextL)

	e.emitThrowTypeError("Cannot set properties of a non-object value")
	e.emitLabel(doneL)
	return boxed, nil
}

// emitDynAnyDelete implements `delete anyVal.key` / `delete anyVal[key]`:
// bag delete on a dynamic object, TypeError on null/undefined (JS), true on
// everything else (JS's delete-on-primitive result).
func (e *Emitter) emitDynAnyDelete(objVal Value, keyRef string, pos ast.Pos) (Value, error) {
	e.ensureDynObj()
	tag, payload := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
	mergeL := e.freshLabel("dyndel.merge")

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dyndel.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_delete(ptr %s, ptr %s)", r, bag, keyRef))
	// Stage 5: false now means a non-configurable property refused deletion
	// — strict JS throws (a miss returns true, so this is unambiguous).
	delOKL := e.freshLabel("dyndel.ok")
	delErrL := e.freshLabel("dyndel.nc")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", r, delOKL, delErrL))
	e.emitLabel(delErrL)
	e.emitThrowTypeError("Cannot delete property of #<Object>")
	e.emitLabel(delOKL)
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagNull, "dyndel.null")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Cannot convert null to object")
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagUndefined, "dyndel.undef")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Cannot convert undefined to object")
	e.emitLabel(nextL)

	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resPtr))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitDynAnyHas implements membership on a dynamic object: the `in`
// operator (ownOnly=false — walks the prototype chain, Stage 3) and
// Object.hasOwn/hasOwnProperty (ownOnly=true). Anything but a dynamic
// object throws TypeError, matching JS's `'x' in 5`.
func (e *Emitter) emitDynAnyHas(objVal Value, keyRef string, ownOnly bool, pos ast.Pos) (Value, error) {
	e.ensureDynObj()
	hasFn := "__kml_dynobj_has_chain"
	if ownOnly {
		hasFn = "__kml_dynobj_has"
	}
	tag, payload := e.emitUnboxTagPayload(objVal)
	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynin.obj")
	doneL := e.freshLabel("dynin.done")
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @%s(ptr %s, ptr %s)", r, hasFn, bag, keyRef))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)
	e.emitThrowTypeError("Cannot use 'in' operator on a non-object value")
	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resPtr))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitDynAnyKeys implements Object.keys / for...in key collection on an
// any-typed value: the bag's insertion-ordered key list for a dynamic object,
// TypeError on null/undefined, and an empty string[] for every other tag
// (ES2015 Object.keys(primitive) → []).
func (e *Emitter) emitDynAnyKeys(objVal Value, pos ast.Pos) (Value, error) {
	e.ensureDynObj()
	tag, payload := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca { ptr, i64 }, align 8", resPtr))
	mergeL := e.freshLabel("dynkeys.merge")

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynkeys.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_dynobj_keys_enum(ptr %s)", r, bag))
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// Dynamic array: index strings "0".."len-1" (TDD-00155 Stage 2).
	e.ensureDynArr()
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynArray, "dynkeys.arr")
	e.emitLabel(matchL)
	arrHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", arrHdr, payload))
	ak := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_dynarr_keys(ptr %s)", ak, arrHdr))
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", ak, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	isNullish := e.freshReg()
	nullCmp, undefCmp := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", nullCmp, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", undefCmp, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isNullish, nullCmp, undefCmp))
	throwL := e.freshLabel("dynkeys.throw")
	emptyL := e.freshLabel("dynkeys.empty")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish, throwL, emptyL))
	e.emitLabel(throwL)
	e.emitThrowTypeError("Cannot convert null or undefined to object")
	e.emitLabel(emptyL)
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } { ptr null, i64 0 }, ptr %s, align 8", resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load { ptr, i64 }, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: ArrayOf(TypePtr)}, nil
}

// emitDynProtoOperand resolves a would-be prototype value (the second
// argument of Object.create/setPrototypeOf, or an assigned `__proto__`) to a
// bag-or-null ptr register. A dynamic object yields its bag pointer, null
// yields null; anything else either throws the JS TypeError (strict=true,
// the statics) or falls back to null-and-skip via the returned validity flag
// (strict=false, the `__proto__` setter's silent ignore).
func (e *Emitter) emitDynProtoOperand(protoVal Value, strict bool) (ptrReg, okReg string, err error) {
	boxed, berr := e.emitBoxValue(protoVal)
	if berr != nil {
		return "", "", berr
	}
	tag, payload := e.emitUnboxTagPayload(boxed)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	okSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", okSlot))
	mergeL := e.freshLabel("protoop.merge")

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "protoop.obj")
	e.emitLabel(matchL)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, payload))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, slot))
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", okSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagNull, "protoop.null")
	e.emitLabel(matchL)
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", okSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	if strict {
		e.emitThrowTypeError("Object prototype may only be an Object or null")
	} else {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
		e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", okSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}

	e.emitLabel(mergeL)
	ptrReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, slot))
	okReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", okReg, okSlot))
	return ptrReg, okReg, nil
}

// emitDynSetProtoChecked calls set_proto and turns a refused cycle into the
// JS TypeError.
func (e *Emitter) emitDynSetProtoChecked(bagReg, protoReg string) {
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_set_proto(ptr %s, ptr %s)", ok, bagReg, protoReg))
	okL := e.freshLabel("setproto.ok")
	cycL := e.freshLabel("setproto.cycle")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, cycL))
	e.emitLabel(cycL)
	e.emitThrowTypeError("Cyclic __proto__ value")
	e.emitLabel(okL)
}

// emitBoxBagOrNull boxes a bag-or-null ptr register: tag 10 for a bag, the
// null box otherwise.
func (e *Emitter) emitBoxBagOrNull(ptrReg string) Value {
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, ptrReg))
	tagged := e.emitNbTagPtr(ptrReg, kmlTagDynObject)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", r, isNull, nbNull, tagged))
	return Value{Ref: r, Ty: TypeAny}
}

// emitObjectCreate implements Object.create(proto) for the dynamic model
// (TDD-00155 Stage 3): a fresh empty bag whose prototype is the given
// dynamic object or null. The property-descriptors second argument waits for
// Stage 5.
func (e *Emitter) emitObjectCreate(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.create takes 1 argument (the property-descriptors argument is not supported yet)", pos.Line, pos.Col)
	}
	protoVal, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !protoVal.Ty.IsDynamic && !protoVal.Ty.IsNull {
		return Value{}, fmt.Errorf("%d:%d: Object.create requires a dynamic (any-typed) object or null prototype", pos.Line, pos.Col)
	}
	e.ensureDynObj()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	if protoVal.Ty.IsNull {
		return e.emitDynObjBox(bag), nil
	}
	protoReg, _, err := e.emitDynProtoOperand(protoVal, true)
	if err != nil {
		return Value{}, err
	}
	e.emitDynSetProtoChecked(bag, protoReg)
	return e.emitDynObjBox(bag), nil
}

// emitObjectGetPrototypeOf implements Object.getPrototypeOf(x) for dynamic
// values: a dynamic object's proto link (or null), TypeError on
// null/undefined. A boxed primitive answers null — this compiler has no
// primitive prototype objects (disclosed).
func (e *Emitter) emitObjectGetPrototypeOf(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.getPrototypeOf takes 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !val.Ty.IsDynamic {
		return Value{}, fmt.Errorf("%d:%d: Object.getPrototypeOf requires a dynamic (any-typed) value", pos.Line, pos.Col)
	}
	e.ensureDynObj()
	return e.emitDynProtoRead(val, pos)
}

// emitDynProtoRead reads a boxed value's prototype (backs getPrototypeOf and
// `__proto__` reads).
func (e *Emitter) emitDynProtoRead(objVal Value, pos ast.Pos) (Value, error) {
	e.ensureDynObj()
	tag, payload := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	mergeL := e.freshLabel("protoget.merge")

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "protoget.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	proto := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_get_proto(ptr %s)", proto, bag))
	boxed := e.emitBoxBagOrNull(proto)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	isNullish := e.freshReg()
	nullCmp, undefCmp := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", nullCmp, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", undefCmp, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isNullish, nullCmp, undefCmp))
	throwL := e.freshLabel("protoget.throw")
	otherL := e.freshLabel("protoget.other")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish, throwL, otherL))
	e.emitLabel(throwL)
	e.emitThrowTypeError("Cannot convert undefined or null to object")
	e.emitLabel(otherL)
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", nbNull, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: TypeAny}, nil
}

// emitObjectSetPrototypeOf implements Object.setPrototypeOf(x, proto) for
// dynamic values: sets a dynamic object's proto (cycle → TypeError), throws
// on null/undefined x, and returns any other value unchanged (JS).
func (e *Emitter) emitObjectSetPrototypeOf(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Object.setPrototypeOf takes 2 arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !objVal.Ty.IsDynamic {
		return Value{}, fmt.Errorf("%d:%d: Object.setPrototypeOf requires a dynamic (any-typed) target", pos.Line, pos.Col)
	}
	protoVal, err := e.emitExprWithObjectHint(args[1], TypeAny)
	if err != nil {
		return Value{}, err
	}
	e.ensureDynObj()
	boxed, err := e.emitBoxValue(objVal)
	if err != nil {
		return Value{}, err
	}
	tag, payload := e.emitUnboxTagPayload(boxed)
	doneL := e.freshLabel("setprotoof.done")

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "setprotoof.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	protoReg, _, err := e.emitDynProtoOperand(protoVal, true)
	if err != nil {
		return Value{}, err
	}
	e.emitDynSetProtoChecked(bag, protoReg)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagNull, "setprotoof.null")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Object.setPrototypeOf called on null or undefined")
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagUndefined, "setprotoof.undef")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Object.setPrototypeOf called on null or undefined")
	e.emitLabel(nextL)

	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return boxed, nil
}

// emitDynProtoWrite backs a `__proto__` assignment on a dynamic object: the
// JS setter's semantics — a non-object, non-null value is silently ignored;
// a cycle throws.
func (e *Emitter) emitDynProtoWrite(objVal Value, rhs Value, pos ast.Pos) (Value, error) {
	e.ensureDynObj()
	boxedRhs, err := e.emitBoxValue(rhs)
	if err != nil {
		return Value{}, err
	}
	tag, payload := e.emitUnboxTagPayload(objVal)
	doneL := e.freshLabel("protoset.done")

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "protoset.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	protoReg, okReg, err := e.emitDynProtoOperand(boxedRhs, false)
	if err != nil {
		return Value{}, err
	}
	setL := e.freshLabel("protoset.set")
	skipL := e.freshLabel("protoset.skip")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", okReg, setL, skipL))
	e.emitLabel(setL)
	e.emitDynSetProtoChecked(bag, protoReg)
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
	e.emitLabel(skipL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagNull, "protoset.null")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Cannot set properties of null (setting '__proto__')")
	e.emitLabel(nextL)

	matchL, nextL = e.emitTagCheck(tag, kmlTagUndefined, "protoset.undef")
	e.emitLabel(matchL)
	e.emitThrowTypeError("Cannot set properties of undefined (setting '__proto__')")
	e.emitLabel(nextL)

	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return boxedRhs, nil
}

// emitJSONStringifyDynamic serializes a boxed dynamic value via the embedded
// C walker (TDD-00155 Stage 2). Result is `any`: a string box, or undefined
// when the top-level value is undefined/function (JS returns undefined).
// A circular structure or a statically-typed value inside the tree throws.
func (e *Emitter) emitJSONStringifyDynamic(val Value, pos ast.Pos) (Value, error) {
	boxed, err := e.emitBoxValue(val)
	if err != nil {
		return Value{}, err
	}
	e.ensureDynJSONC()
	tag, payload := e.emitUnboxTagPayload(boxed)
	// Widened to i64 for the C boundary: the arm64 ABI wants the caller to
	// extend sub-32-bit arguments, which a bare i8 call arg doesn't guarantee.
	tagW := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", tagW, tag))
	errSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", errSlot))
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynjson_stringify(i64 %s, i64 %s, ptr %s)", s, tagW, payload, errSlot))
	errv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", errv, errSlot))

	circL := e.freshLabel("dynjson.circ")
	next1 := e.freshLabel("dynjson.next1")
	isCirc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", isCirc, errv))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isCirc, circL, next1))
	e.emitLabel(circL)
	e.emitThrowTypeError("Converting circular structure to JSON")
	e.emitLabel(next1)

	staticL := e.freshLabel("dynjson.static")
	next2 := e.freshLabel("dynjson.next2")
	isStatic := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 2", isStatic, errv))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStatic, staticL, next2))
	e.emitLabel(staticL)
	e.emitThrowTypeError("JSON.stringify of a statically-typed value in a dynamic position is not supported")
	e.emitLabel(next2)

	// NULL with err==0: the JS result is undefined; otherwise a string box.
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, s))
	sInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", sInt, s))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", r, isNull, nbUndefined, sInt))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// emitDynArrLiteral builds a D1 dynamic array (tag 11) from an array literal
// in an any-typed context (TDD-00155 Stage 2): every element is boxed, and
// nested object/array literals recurse into dynamic values — which is what
// makes a heterogeneous literal (`[1, "two", null]`) representable at all.
func (e *Emitter) emitDynArrLiteral(lit *ast.ArrayLiteral) (Value, error) {
	e.ensureDynArr()
	arr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 %d)", arr, len(lit.Elements)))
	for _, el := range lit.Elements {
		if _, ok := el.(*ast.SpreadElement); ok {
			return Value{}, fmt.Errorf("%d:%d: spread in a dynamic (any-typed) array literal is not supported yet", lit.GetPos().Line, lit.GetPos().Col)
		}
		var boxed Value
		switch n := el.(type) {
		case *ast.ObjectLiteral:
			v, err := e.emitDynObjLiteral(n)
			if err != nil {
				return Value{}, err
			}
			boxed = v
		case *ast.ArrayLiteral:
			v, err := e.emitDynArrLiteral(n)
			if err != nil {
				return Value{}, err
			}
			boxed = v
		default:
			v, err := e.emitExpr(el)
			if err != nil {
				return Value{}, err
			}
			boxed, err = e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", arr, boxed.Ref))
	}
	return Value{Ref: e.emitNbTagPtr(arr, kmlTagDynArray), Ty: TypeAny}, nil
}

// emitDynObjLiteral builds a D1 dynamic object from an object literal in an
// any-typed context (TDD-00155 boundary rule 3): every value is boxed, nested
// object literals recurse into nested dynamic objects, and a spread of
// another any value merges its bag (a null/undefined/primitive spread is a
// JS no-op; a statically-shaped object spread copies its visible fields at
// compile time).
func (e *Emitter) emitDynObjLiteral(lit *ast.ObjectLiteral) (Value, error) {
	e.ensureDynObj()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))

	for _, prop := range lit.Properties {
		if spread, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" && prop.KeyExpr == nil {
			sv, err := e.emitExpr(spread.Arg)
			if err != nil {
				return Value{}, err
			}
			if sv.Ty.IsDynamic {
				tag, payload := e.emitUnboxTagPayload(sv)
				matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynlit.spread")
				e.emitLabel(matchL)
				src := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", src, payload))
				e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_merge(ptr %s, ptr %s)", bag, src))
				e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
				e.emitLabel(nextL)
				continue
			}
			if !sv.Ty.IsObject {
				return Value{}, fmt.Errorf("%d:%d: spread in an object literal requires an object value", spread.GetPos().Line, spread.GetPos().Col)
			}
			srcStructIR := sv.Ty.StructIR()
			for _, f := range sv.Ty.VisibleFields() {
				srcIdx, _, _ := sv.Ty.FieldIndex(f.Name)
				srcGep, loadReg := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", srcGep, srcStructIR, sv.Ref, srcIdx))
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(f.Ty), srcGep, f.Ty.Align()))
				boxed, err := e.emitBoxValue(Value{Ref: loadReg, Ty: f.Ty})
				if err != nil {
					return Value{}, err
				}
				e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", bag, e.internString(f.Name), boxed.Ref))
			}
			continue
		}

		// `{ __proto__: p }` in a literal sets the prototype (JS special
		// form; a computed `["__proto__"]` key stays a plain property, as in
		// JS). Non-object/non-null values are silently ignored, per spec.
		if prop.KeyExpr == nil && prop.Key == "__proto__" {
			pv, err := e.emitExprWithObjectHint(prop.Value, TypeAny)
			if err != nil {
				return Value{}, err
			}
			protoReg, okReg, err := e.emitDynProtoOperand(pv, false)
			if err != nil {
				return Value{}, err
			}
			setL := e.freshLabel("dynlit.protoset")
			skipL := e.freshLabel("dynlit.protoskip")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", okReg, setL, skipL))
			e.emitLabel(setL)
			e.emitDynSetProtoChecked(bag, protoReg)
			e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
			e.emitLabel(skipL)
			continue
		}

		var keyRef string
		if prop.KeyExpr != nil {
			kr, err := e.dynAnyKeyRef(prop.KeyExpr, lit.GetPos())
			if err != nil {
				return Value{}, err
			}
			keyRef = kr
		} else {
			keyRef = e.internString(prop.Key)
		}

		// `get x() {...}` / `set x(v) {...}` (Stage 5): the accessor compiles
		// under the dynamic ABI and installs as an accessor entry
		// (enumerable+configurable, per literal semantics). A get+set pair on
		// one key merges via defacc.
		if prop.AccessorKind != "" {
			fe, ok := prop.Value.(*ast.FunctionExpression)
			if !ok {
				return Value{}, fmt.Errorf("%d:%d: a getter/setter must be a function", lit.GetPos().Line, lit.GetPos().Col)
			}
			recBox, err := e.emitDynFunctionExpression(fe, lit.GetPos())
			if err != nil {
				return Value{}, err
			}
			recPay := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i64 %s, -8", recPay, recBox.Ref))
			rec := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", rec, recPay))
			getRec, setRec := "null", "null"
			if prop.AccessorKind == "get" {
				getRec = rec
			} else {
				setRec = rec
			}
			e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_defacc(ptr %s, ptr %s, ptr %s, ptr %s)", bag, keyRef, getRec, setRec))
			continue
		}

		var boxed Value
		if nested, ok := prop.Value.(*ast.ObjectLiteral); ok {
			nv, err := e.emitDynObjLiteral(nested)
			if err != nil {
				return Value{}, err
			}
			boxed = nv
		} else if fe, ok := prop.Value.(*ast.FunctionExpression); ok {
			// A function-valued property compiles under the dynamic ABI —
			// this is also what a descriptor literal's `get:`/`set:` fields
			// carry into Object.defineProperty (Stage 5).
			nv, err := e.emitDynFunctionExpression(fe, lit.GetPos())
			if err != nil {
				return Value{}, err
			}
			boxed = nv
		} else if af, ok := prop.Value.(*ast.ArrowFunction); ok {
			nv, err := e.emitDynArrowFunction(af, lit.GetPos())
			if err != nil {
				return Value{}, err
			}
			boxed = nv
		} else {
			v, err := e.emitExpr(prop.Value)
			if err != nil {
				return Value{}, err
			}
			boxed, err = e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", bag, keyRef, boxed.Ref))
	}
	return e.emitDynObjBox(bag), nil
}

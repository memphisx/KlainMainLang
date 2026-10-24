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
	"sort"
	"strings"

	"KlainMainLang/ast"
)

// symbolKeySentinel prefixes the synthetic string key a Symbol property maps to
// in the string-keyed dynobj bag. The leading SOH byte (0x01) never appears in a
// real JS property key written in source, so symbol keys never collide with
// string keys and the string enumeration (Object.keys / for-in /
// getOwnPropertyNames) can filter them out by this prefix.
const symbolKeySentinel = "\x01@@sym:"

// emitSymbolPropertyKey builds the synthetic bag key for a Symbol value from its
// pointer identity (see symbolKeySentinel). Two references to the same Symbol
// share the pointer, so `obj[sym]` set/get/has/delete round-trip consistently.
func (e *Emitter) emitSymbolPropertyKey(sym Value) string {
	e.ensureSprintf()
	scratch := e.emitStringScratch(40)
	fmtStr := e.internString(symbolKeySentinel + "%p")
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, ptr %s)", scratch, fmtStr, sym.Ref))
	e.emitStringFinalizeLen(scratch)
	// A well-known symbol keys the member a `[Symbol.iterator]()` (or
	// asyncIterator, toPrimitive, dispose, asyncDispose) compiles to.
	key := scratch
	for _, wk := range wellKnownMemberKeys {
		is := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, %s", is, sym.Ref, e.wellKnownSymbol(wk)))
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, is, e.internString("@@"+wk), key))
		key = sel
	}
	return key
}

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
	e.excThrow(errObj)
	e.emitTerminator("unreachable")
}

// emitThrowRangeError emits an unconditional runtime RangeError throw with
// a constant message; it ends the current block.
func (e *Emitter) emitThrowRangeError(msg string) {
	e.ensureExceptionHelpers()
	errObj := e.buildErrorObj(errorKindIDs["RangeError"], e.internString(msg), e.internString("RangeError"))
	e.excThrow(errObj)
	e.emitTerminator("unreachable")
}

// emitThrowTypeErrorValue emits an unconditional runtime TypeError throw whose
// message is a runtime string-pointer register (not a compile-time constant) —
// used when the message embeds a runtime value (e.g. the offending property
// name). Like emitThrowTypeError it ends the current block.
func (e *Emitter) emitThrowTypeErrorValue(msgReg string) {
	e.ensureExceptionHelpers()
	errObj := e.buildErrorObj(errorKindIDs["TypeError"], msgReg, e.internString("TypeError"))
	e.excThrow(errObj)
	e.emitTerminator("unreachable")
}

// dynAnyKeyRef evaluates a bracket key expression for a dynamic-object access
// and returns a string-pointer register: strings pass through, an any key is
// stringified with the runtime tag dispatch, an integer is formatted "%lld"
// (JS obj[1] ≡ obj["1"]), a float via the JS-faithful dtoa.
func (e *Emitter) dynAnyKeyRef(keyExpr ast.Expression, pos ast.Pos) (string, error) {
	if nl, ok := keyExpr.(*ast.NullLiteral); ok && !nl.Void {
		// `obj[null]` is `obj["null"]`, `obj[undefined]` `obj["undefined"]`.
		if nl.IsUndefined {
			return e.internString("undefined"), nil
		}
		return e.internString("null"), nil
	}
	kv, err := e.emitExpr(keyExpr)
	if err != nil {
		return "", err
	}
	switch {
	case kv.Ty.IsSymbol():
		// A Symbol is a valid, unique property key — ToPropertyKey keeps it a
		// symbol, it does NOT ToString (which throws). The dynobj bag is
		// string-keyed, so a symbol maps to a stable synthetic key derived from
		// its pointer identity (two references to the same Symbol share the
		// pointer, so `obj[sym]` round-trips). The sentinel prefix keeps symbol
		// keys out of the string-keyed enumeration (Object.keys/for-in).
		return e.emitSymbolPropertyKey(kv), nil
	case kv.Ty.IsArray() || kv.Ty.IsObject():
		// A non-symbol array/object key ToStrings (ToPropertyKey → ToString):
		// `obj[[1,2]]` is `obj["1,2"]`, `obj[{}]` is `obj["[object Object]"]`.
		// The default `sprintf("%lld")` branch below would feed a header
		// pointer to a `i64` format arg (invalid IR).
		s, serr := e.emitValueToString(kv)
		if serr != nil {
			return "", serr
		}
		return s.Ref, nil
	case kv.Ty.IsDynamic():
		// A Symbol held in an `any` keys by its identity, as a Symbol-typed
		// key does; anything else by its ToString.
		tag, pay := e.emitUnboxTagPayload(kv)
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		objL, strL := e.emitTagCheck(tag, kmlTagObject, "dynkey.obj")
		symL, doneL := e.freshLabel("dynkey.sym"), e.freshLabel("dynkey.done")
		e.emitLabel(objL)
		objPtr, isSym := e.emitBoxedSymbolProbe(pay)
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSym, symL, strL))
		e.emitLabel(symL)
		sk := e.emitSymbolPropertyKey(Value{Ref: objPtr, Ty: SymbolType()})
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sk, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(strL)
		s, err := e.emitDynamicToString(kv)
		if err != nil {
			return "", err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", s.Ref, slot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		key := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", key, slot))
		return key, nil
	case isStringTy(kv.Ty) && (kv.Ty.Nullable() || kv.Ty.IsNull() || kv.Ty.IsUndefined()):
		// A string that may be absent keys as "null" or "undefined" when it
		// is.
		word := "null"
		if kv.Ty.IsUndefined() {
			word = "undefined"
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, e.ptrIsNull(kv.Ref), e.internString(word), kv.Ref))
		return r, nil
	case isStringTy(kv.Ty):
		return kv.Ref, nil
	case kv.Ty.Float:
		scratch := e.emitStringScratch(32)
		e.ensureDtoa()
		d := e.convert(kv, TypeF64)
		e.emitInstr(fmt.Sprintf("call void @__kml_dtoa(ptr %s, double %s)", scratch, d.Ref))
		e.emitStringFinalizeLen(scratch)
		return scratch, nil
	case kv.Ty.IR == "i1":
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", r, kv.Ref, e.internString("true"), e.internString("false")))
		return r, nil
	case kv.Ty.IsInteger() && !kv.Ty.IsDynamic() && !kv.Ty.IsBigInt() && !isNullableScalar(kv.Ty):
		e.ensureSprintf()
		scratch := e.emitStringScratch(32)
		i := e.convert(kv, TypeI64)
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", scratch, e.internString("%lld"), i.Ref))
		e.emitStringFinalizeLen(scratch)
		return scratch, nil
	default:
		// Anything else (a function, a class, a bigint, null or undefined)
		// keys by its ToString.
		s, serr := e.emitValueToString(kv)
		if serr != nil {
			return "", serr
		}
		return s.Ref, nil
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
	if objProtoMember[propName] {
		e.ensureObjProtoFill() // the read may end in Object.prototype's bag
	}
	if !objVal.Ty.IsDynamic() {
		// A receiver inferred dynamic but emitted static (a well-known
		// Symbol): every path below reads a box.
		b, err := e.emitBoxValue(objVal)
		if err != nil {
			return Value{}, err
		}
		objVal = b
	}
	// `x.__proto__` reads the prototype link (TDD-00155 Stage 3), like the
	// Object.prototype accessor it stands in for.
	if propName == "__proto__" {
		e.dunderRead = true
		defer func() { e.dunderRead = false }()
		return e.emitDynProtoRead(objVal, pos)
	}
	objVal = e.emitWrapperRecv(objVal, keyRef)
	if e.dynGetInline {
		return e.emitDynAnyMemberGetInline(objVal, keyRef, propName, pos)
	}
	// One routine per property name (its per-kind dispatch is long), called
	// with the value and the key.
	fn := e.dynGetHelper(propName, pos)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(i64 %s, ptr %s)", r, fn, objVal.Ref, keyRef))
	return Value{Ref: r, Ty: TypeAny}, nil
}

// dynGetHelper is `i64 get(i64 value, ptr key)` for property propName ("" for
// a run-time key), generated once.
func (e *Emitter) dynGetHelper(propName string, pos ast.Pos) string {
	if fn, ok := e.dynGetHelpers[propName]; ok {
		return fn
	}
	if e.dynGetHelpers == nil {
		e.dynGetHelpers = map[string]string{}
	}
	// Named by the property, not the body: the dispatch covers the
	// program's own classes, and library code calls it, so a content name
	// would make the library's IR differ per program (TDD-00238 Stage 4).
	// The program's unit owns it.
	fn := contentSymbol("@__kml_dynget.", propName)
	e.dynGetHelpers[propName] = fn
	restore := e.beginDetachedFunc()
	saved := e.dynGetInline
	e.dynGetInline = true
	v, err := e.emitDynAnyMemberGetInline(Value{Ref: "%v", Ty: TypeAny}, "%key", propName, pos)
	e.dynGetInline = saved
	if err != nil {
		if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		}
	} else if !e.blockDone {
		e.emitTerminator(fmt.Sprintf("ret i64 %s", v.Ref))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeHelper("hidden", "i64", fn, "i64 %v, ptr %key", body)
	return fn
}

// emitDynAnyMemberGetInline is emitDynAnyMemberGetNamed's dispatch, emitted
// in place.
func (e *Emitter) emitDynAnyMemberGetInline(objVal Value, keyRef, propName string, pos ast.Pos) (Value, error) {
	reading := ""
	if propName != "" {
		reading = fmt.Sprintf(" (reading '%s')", propName)
	}
	e.ensureDynObj()
	tag, payload := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resPtr))
	mergeL := e.freshLabel("dynget.merge")
	if propName == "constructor" {
		// A host box (a Map, a Date): the host class it was made by.
		e.ensureHostBoxHooks()
		cn, isHost := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_host_class_name(i64 %s)", cn, objVal.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isHost, cn))
		hostL, contL := e.freshLabel("dynget.hostctor"), e.freshLabel("dynget.nothost")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isHost, hostL, contL))
		e.emitLabel(hostL)
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbTagPtr(cn, kmlTagFuncRef), resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(contL)
		// A primitive: its wrapper's constructor (String, Number, Boolean),
		// the same value the constructor's name references.
		for _, k := range []struct {
			tags []int
			ctor string
		}{{[]int{kmlTagString}, "String"}, {[]int{kmlTagInt, kmlTagFloat}, "Number"}, {[]int{kmlTagBoolean}, "Boolean"}} {
			hit := ""
			for _, t := range k.tags {
				is := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", is, tag, t))
				if hit == "" {
					hit = is
				} else {
					or := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", or, hit, is))
					hit = or
				}
			}
			primL, nextPrimL := e.freshLabel("dynget.primctor"), e.freshLabel("dynget.notprim")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, primL, nextPrimL))
			e.emitLabel(primL)
			if cv, ok, err := e.builtinCtorValue(k.ctor); err != nil {
				return Value{}, err
			} else if ok {
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", cv.Ref, resPtr))
			} else {
				e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbTagPtr(e.internString(k.ctor), kmlTagFuncRef), resPtr))
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
			e.emitLabel(nextPrimL)
		}
	}

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynget.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", r, bag, keyRef))
	if propName == "constructor" {
		// Not an own or inherited property: Object.prototype's, Object —
		// unless the chain ends in an explicit null prototype.
		ord := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_ordinary(ptr %s)", ord, bag))
		r = e.emitCtorFallback(r, ord, e.internString("Object"))
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// A string's `[Symbol.iterator]`: an iterator over its code points.
	if propName == "" || propName == "@@iterator" {
		isS := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isS, tag, kmlTagString))
		keyL, contL := e.freshLabel("dynget.strkey"), e.freshLabel("dynget.strcont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isS, keyL, contL))
		e.emitLabel(keyL)
		e.ensureStrcmp()
		c := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", c, keyRef, e.internString("@@iterator")))
		m := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", m, c))
		hitL := e.freshLabel("dynget.striter")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", m, hitL, contL))
		e.emitLabel(hitL)
		e.noteBoxedLayout(arrayIterType())
		e.noteBoxedLayout(iterResultType())
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbTagPtr(e.arrayIterMethodRecord("stringValues"), kmlTagDynFunc), resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(contL)
	}
	// An array's iteration protocol: `arr[Symbol.iterator]` (and values()).
	{
		isDA := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDA, tag, kmlTagDynArray))
		isSA := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isSA, tag, kmlTagArray))
		isA := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isA, isDA, isSA))
		keyL, contL := e.freshLabel("dynget.arrkey"), e.freshLabel("dynget.arrcont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isA, keyL, contL))
		e.emitLabel(keyL)
		e.ensureStrcmp()
		c1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", c1, keyRef, e.internString("@@iterator")))
		c2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", c2, keyRef, e.internString("values")))
		m1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", m1, c1))
		m2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", m2, c2))
		m := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", m, m1, m2))
		hitL := e.freshLabel("dynget.arriter")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", m, hitL, contL))
		e.emitLabel(hitL)
		e.noteBoxedLayout(arrayIterType())
		e.noteBoxedLayout(iterResultType())
		// Array.prototype.values, the one function (its [Symbol.iterator]
		// as made, or as the program replaced it).
		// (Shared code, this helper reads it at run time.)
		rec := e.emitNbTagPtr(e.arrayValuesRecord(), kmlTagDynFunc)
		fixed := e.emitIterMethodOf(objVal, Value{Ref: rec, Ty: TypeAny})
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, m1, fixed.Ref, rec))
		rec = sel
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", rec, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(contL)
	}

	// An iterable host box's (a Map's, a Set's) `[Symbol.iterator]`: the
	// host spreads into an array when it is one. Only a computed key or the
	// symbol itself can name it.
	if propName == "" || propName == "@@iterator" {
		e.ensureStrcmp()
		c := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", c, keyRef, e.internString("@@iterator")))
		isIter := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isIter, c))
		probeL, contL := e.freshLabel("dynget.hostiter"), e.freshLabel("dynget.hostcont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isIter, probeL, contL))
		e.emitLabel(probeL)
		spread := e.emitHostIterValue(objVal.Ref)
		isHost := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %s", isHost, spread, objVal.Ref))
		hitL := e.freshLabel("dynget.hostiter.hit")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isHost, hitL, contL))
		e.emitLabel(hitL)
		e.noteBoxedLayout(arrayIterType())
		e.noteBoxedLayout(iterResultType())
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbTagPtr(e.arrayIterMethodRecord("hostValues"), kmlTagDynFunc), resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(contL)
	}

	// Dynamic array (TDD-00155 Stage 2): numeric-string index or `length`.
	e.ensureDynArr()
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynArray, "dynget.arr")
	e.emitLabel(matchL)
	arrHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", arrHdr, payload))
	ar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynarr_get_by_key(ptr %s, ptr %s)", ar, arrHdr, keyRef))
	if propName == "constructor" {
		ar = e.emitCtorFallback(ar, "true", e.internString("Array"))
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ar, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// A boxed STATIC array (ADR-01059): `length` and a canonical index read
	// through the box's element kind (a number widens to a double, a string is
	// its pointer, an `any[]` element is the word itself). An element kind the
	// box could not describe (nested array / object / Map / BigInt64Array) has
	// no readable shape — a clean TypeError rather than a made-up `undefined`.
	e.ensureDynJSONC()
	matchL, nextL = e.emitTagCheck(tag, kmlTagArray, "dynget.sarr")
	e.emitLabel(matchL)
	sBox := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", sBox, payload))
	sr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_anyarr_get_by_key(ptr %s, ptr %s)", sr, sBox, keyRef))
	// The helper answers the unused immediate 1 for an in-range index into an
	// undescribed element kind — the refusal, distinct from `undefined` (10).
	sBad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", sBad, sr))
	sOkL := e.freshLabel("dynget.sarr.ok")
	sRefuseL := e.freshLabel("dynget.sarr.refuse")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sBad, sRefuseL, sOkL))
	e.emitLabel(sRefuseL)
	e.emitThrowTypeError("element access through `any` on an array whose element type is not representable (nested array / object / Map / BigInt64Array) is not supported")
	e.emitLabel(sOkL)
	if propName == "constructor" {
		// Array, the TypedArray kind the box holds, or Object for an
		// Arguments object (__kml_anyarr_ctor).
		k := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_anyarr_ctor(ptr %s)", k, sBox))
		name := e.internString("Array")
		for i, n := range []string{"Float64Array", "Float32Array", "BigInt64Array", "BigUint64Array", "Int32Array", "Uint32Array", "Int16Array", "Uint16Array", "Int8Array", "Uint8Array", "Uint8ClampedArray"} {
			is, sel := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, k, i+1))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, is, e.internString(n), name))
			name = sel
		}
		// An Arguments object's is Object.
		isArgs, sel := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, -1", isArgs, k))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, isArgs, e.internString("Object"), name))
		name = sel
		sr = e.emitCtorFallback(sr, "true", name)
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sr, resPtr))
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

	// Primitive-member dispatch through `any` (V1: `.length`): a boxed string
	// answers its length instead of the silent `undefined` its tag otherwise
	// falls into below (a boxed static array's length is the arm above).
	if propName == "length" {
		e.ensureStrHeaderRuntime()
		matchL, nextL = e.emitTagCheck(tag, kmlTagString, "dynget.strlen")
		e.emitLabel(matchL)
		sp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", sp, payload))
		sn := e.emitStrU16Len(sp)
		sd := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", sd, sn))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e.emitNbEncodeDouble(sd), resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(nextL)
	}
	// An index into a boxed string (`s[i]` with `s: any`): its character;
	// a method name, String.prototype's (__kml_string_proto_get).
	if _, method := e.classMethod("String", propName); propName == "" || isCanonicalIndex(propName) || method {
		matchL, nextL = e.emitTagCheck(tag, kmlTagString, "dynget.strindex")
		e.emitLabel(matchL)
		sp, sc := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", sp, payload))
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_get_by_key(ptr %s, ptr %s)", sc, sp, keyRef))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sc, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(nextL)
	}

	// A boxed built-in Error (field-0 type-id flag, low bits a builtin kind):
	// recover the requested property from the real errorObjType fields —
	// `reason.message`/`.name` on a rejected/caught Error boxed into `any`
	// (TDD-00222/TDD-00169). A boxed non-error object keeps the existing
	// "no dynamic shape" TypeError.
	matchL, nextL = e.emitTagCheck(tag, kmlTagObject, "dynget.errobj")
	e.emitLabel(matchL)
	errPtr, errIsErr := e.emitBoxedErrorProbe(payload)
	errReadL := e.freshLabel("dynget.errread")
	notErrL := e.freshLabel("dynget.noterr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", errIsErr, errReadL, notErrL))
	e.emitLabel(errReadL)
	// An Error subclass's own field: its layout's getter.
	{
		e.ensureShapeRuntime()
		found := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", found))
		e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", found))
		v, fv, hit := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_get(ptr %s, ptr %s, ptr %s)", v, errPtr, keyRef, found))
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", fv, found))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i32 %s, 0", hit, fv))
		ownL, fixL := e.freshLabel("dynget.errown"), e.freshLabel("dynget.errfixed")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, ownL, fixL))
		e.emitLabel(ownL)
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(fixL)
	}
	// propName is a compile-time constant, so exactly one arm is emitted.
	readField := func(idx int) string {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, errorObjType.StructIR(), errPtr, idx))
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", p, gep))
		return e.emitNbTagPtr(p, kmlTagString)
	}
	// The `extra` bag (field 13, ADR-01080): an own property beyond the
	// fixed fields — execSync's `status`/`stdout`/… — or undefined.
	bagEnd := ""
	bagGet := func() string {
		xIdx, _, _ := errorObjType.FieldIndex(errorExtraField)
		xGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", xGep, errorObjType.StructIR(), errPtr, xIdx))
		xBag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", xBag, xGep))
		xNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", xNull, xBag))
		xGetL := e.freshLabel("dynget.errextra")
		xNoneL := e.freshLabel("dynget.errnoextra")
		xJoinL := e.freshLabel("dynget.errextrajoin")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", xNull, xNoneL, xGetL))
		e.emitLabel(xGetL)
		xGet := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", xGet, xBag, keyRef))
		e.emitTerminator(fmt.Sprintf("br label %%%s", xJoinL))
		e.emitLabel(xNoneL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", xJoinL))
		e.emitLabel(xJoinL)
		sel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ %d, %%%s ]", sel, xGet, xGetL, nbUndefined, xNoneL))
		bagEnd = xJoinL
		return sel
	}
	// orBag reads the bag where an optional fixed field is absent (a value
	// of another kind set through `any` lives there).
	orBag := func(present, boxedField string) string {
		hereL := e.freshLabel("dynget.errfix")
		bagL := e.freshLabel("dynget.errfixbag")
		joinL := e.freshLabel("dynget.errfixjoin")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, hereL, bagL))
		e.emitLabel(hereL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
		e.emitLabel(bagL)
		fromBag := bagGet()
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
		e.emitLabel(joinL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ %s, %%%s ]", r, boxedField, hereL, fromBag, bagEnd))
		return r
	}
	var errBoxed string
	if propName == "" {
		// A runtime key (`err[k]`): the named reads, dispatched on it.
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_error_get_key(i64 %s, ptr %s)", r, objVal.Ref, keyRef))
		e.ensureErrorGetKey(pos)
		errBoxed = r
	} else {
		// Any other fixed errorObjType field (code/errno/syscall/path/dest/cause/
		// address/port, ADR-01080; node:sqlite's errcode/errstr) reads and boxes
		// per its type — a null string field is `undefined`, as the property is
		// absent on Node's plain errors.
		fixedIdx, fixedTy, isFixed := errorObjType.FieldIndex(propName)
		switch {
		case propName == "errors":
			// An AggregateError's errors array, boxed; any other error's
			// own `errors` property, if it has one (the extra bag).
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
			kp, k, isAgg := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", kp, errorObjType.StructIR(), errPtr))
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", k, kp))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isAgg, e.headerIdentity(k), errorTypeIDStored(errorKindIDs["AggregateError"])))
			aggL, ownL, joinL := e.freshLabel("dynget.aggerrs"), e.freshLabel("dynget.errsown"), e.freshLabel("dynget.errsjoin")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAgg, aggL, ownL))
			e.emitLabel(aggL)
			boxedArr, err := e.emitBoxValue(e.emitErrorErrorsAccess(errPtr))
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxedArr.Ref, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(ownL)
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bagGet(), slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			errBoxed = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", errBoxed, slot))
		case propName == "message":
			errBoxed = readField(1)
		case propName == "name":
			errBoxed = readField(2)
		case propName == "constructor":
			// The error's constructor as a built-in constructor reference (tag 8,
			// payload = its interned name): `e.constructor.name`, and
			// `e.constructor === TypeError` compares the same interned name
			// (TDD-00229).
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", gep, errorObjType.StructIR(), errPtr))
			np := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", np, gep))
			// A builtin kind's constructor is the kind (a DOMException named
			// "DataError", an Error renamed), whatever its name says.
			kind := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", kind, errPtr))
			kind = e.headerIdentity(kind)
			// One check per kind id: two modules' `class X extends Error`
			// share a name but not an id. Sorted, so the IR is the same on
			// every compile.
			type ctor struct {
				name string
				id   int64
			}
			var ctors []ctor
			for _, k := range errorKinds {
				ctors = append(ctors, ctor{k, errorTypeIDStored(errorKindIDs[k])})
			}
			for name, info := range e.classes {
				if info.IsErrorSubclass && info.TagID != 0 {
					ctors = append(ctors, ctor{demangleModuleName(name), errorTypeIDStored(info.TagID)}) // `class X extends Error`
				}
			}
			sort.Slice(ctors, func(i, j int) bool {
				if ctors[i].name != ctors[j].name {
					return ctors[i].name < ctors[j].name
				}
				return ctors[i].id < ctors[j].id
			})
			for _, c := range ctors {
				is, sel := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", is, kind, c.id))
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, is, e.internString(c.name), np))
				np = sel
			}
			errBoxed = e.emitNbTagPtr(np, kmlTagFuncRef)
		case isFixed && propName != ClassTagField && propName != errorExtraField:
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, errorObjType.StructIR(), errPtr, fixedIdx))
			raw := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", raw, StructFieldIR(fixedTy), gep))
			if fixedTy.IsDynamic() {
				errBoxed = raw
			} else if fixedTy.IR == "ptr" {
				isNull := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, raw))
				tagged := e.emitNbTagPtr(raw, kmlTagString)
				present := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", present, isNull))
				errBoxed = orBag(present, tagged)
				if propName == "code" {
					// A DOMException's code is the number its name has.
					e.ensureDOMExceptionCode()
					k0, isDom := e.freshReg(), e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", k0, errPtr))
					e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isDom, e.headerIdentity(k0), errorTypeIDStored(errorKindIDs["DOMException"])))
					ngep, nm, num := e.freshReg(), e.freshReg(), e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", ngep, errorObjType.StructIR(), errPtr))
					e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", nm, ngep))
					e.emitInstr(fmt.Sprintf("%s = call double @__kml_domexc_code(ptr %s)", num, nm))
					sel := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, isDom, e.emitNbEncodeDouble(num), errBoxed))
					errBoxed = sel
				}
			} else {
				v := Value{Ref: raw, Ty: fixedTy}
				optional := errorOptionalNumber(errorObjType, propName)
				bv, berr := e.emitBoxValue(v)
				if berr != nil {
					return Value{}, berr
				}
				errBoxed = bv.Ref
				if optional {
					// Unset (0): absent, as on Node's plain errors.
					present := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = fcmp une double %s, 0.0", present, raw))
					errBoxed = orBag(present, bv.Ref)
				}
			}
		case propName == "stack":
			// No real stack is retained; `err.stack` is approximated by the
			// `name: message` toString form, a faithful subset of Node's string.
			s, serr := e.emitErrorToString(Value{Ref: errPtr, Ty: TypePtr})
			if serr != nil {
				return Value{}, serr
			}
			errBoxed = e.emitNbTagPtr(s.Ref, kmlTagString)
		default:
			errBoxed = bagGet()
			// An Error subclass's own field (`e.operator`): its layout's.
			// Read whether or not this program declares one, so the code
			// is the same in every program (TDD-00238).
			e.noteErrorSubclassLayouts()
			{
				e.ensureShapeRuntime()
				found := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", found))
				own := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_get(ptr %s, ptr %s, ptr %s)", own, errPtr, keyRef, found))
				fromBag := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", fromBag, errBoxed, nbUndefined))
				sel := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, fromBag, errBoxed, own))
				errBoxed = sel
			}
		}
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", errBoxed, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(notErrL)
	// A boxed Symbol (ADR-01059): `description` is its string; any other
	// property is undefined (a Symbol has no own properties).
	symPtr, isSym := e.emitBoxedSymbolProbe(payload)
	symReadL := e.freshLabel("dynget.symread")
	notSymL := e.freshLabel("dynget.notsym")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isSym, symReadL, notSymL))
	e.emitLabel(symReadL)
	var symBoxed string
	if propName == "description" {
		symTy := SymbolType()
		dIdx, _, _ := symTy.FieldIndex("description")
		dGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dGep, symTy.StructIR(), symPtr, dIdx))
		dp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dp, dGep))
		none := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", none, dp))
		symBoxed = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", symBoxed, none, nbUndefined, e.emitNbTagPtr(dp, kmlTagString)))
	} else {
		symBoxed = fmt.Sprintf("%d", nbUndefined)
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", symBoxed, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(notSymL)
	// A static object: its layout's getter (TDD-00230 phase 5).
	{
		objp := e.emitIntToPtr(payload)
		sv, found := e.emitShapeGet(objp, keyRef)
		noShape := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", noShape, found, shapeNoShape))
		okL := e.freshLabel("dynget.shape")
		badL := e.freshLabel("dynget.noshape")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", noShape, badL, okL))
		e.emitLabel(okL)
		if propName == "constructor" {
			// Not an own field: the class the instance was constructed as.
			absent := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", absent, found, shapeAbsent))
			ctor := e.emitConstructorOf(Value{Ref: objp, Ty: TypePtr})
			// No class made it (an object literal): Object.
			noClass, orObj := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", noClass, ctor.Ref, nbUndefined))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", orObj, noClass, e.emitNbTagPtr(e.internString("Object"), kmlTagFuncRef), ctor.Ref))
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, absent, orObj, sv))
			sv = sel
		}
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sv, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(badL)
		e.emitThrowTypeError("dynamic property access ('" + propName + "') on a statically-typed value is not supported")
	}
	e.emitLabel(nextL)

	// A function value's `name` / `length` (TDD-00229): a dynamic record reads
	// the code-pointer metadata; a built-in Error constructor reference carries
	// its name as the payload (every one has length 1). Other properties of a
	// function read undefined.
	{
		fnVal := func(dyn bool) string {
			switch propName {
			case "name":
				if !dyn {
					// An own static `name` member first, else the class's name.
					own := e.emitClassRefGet(payload, keyRef)
					e.ensureClassRec()
					r := e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_classref_name(ptr %s)", r, e.emitIntToPtr(payload)))
					absent, sel := e.freshReg(), e.freshReg()
					e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", absent, own, nbUndefined))
					e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, absent, e.emitNbTagPtr(r, kmlTagString), own))
					return sel
				}
				e.ensureFnMeta()
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_name_dyn(ptr %s)", r, e.emitIntToPtr(payload)))
				return e.emitNbTagPtr(r, kmlTagString)
			case "length":
				if !dyn {
					return e.emitClassRefGet(payload, keyRef)
				}
				e.ensureFnMeta()
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_fn_length_dyn(ptr %s)", r, e.emitIntToPtr(payload)))
				d := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", d, r))
				return e.emitNbEncodeDouble(d)
			}
			if !dyn {
				return e.emitClassRefGet(payload, keyRef) // a class's static member
			}
			// The function's own-property bag (TDD-00229): `fn.x` through
			// `any`; a function without one reads undefined.
			rec := e.emitIntToPtr(payload)
			e.ensureFnMeta()
			props := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_props_dyn(ptr %s)", props, rec))
			isExt := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isExt, props))
			extL := e.freshLabel("dynget.fnprops")
			noL := e.freshLabel("dynget.fnnoprops")
			joinL := e.freshLabel("dynget.fnjoin")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isExt, extL, noL))
			e.emitLabel(extL)
			got := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", got, props, keyRef))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(noL)
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ %d, %%%s ]", sel, got, extL, nbUndefined, noL))
			// No own property: a builtin constructor's static (`N.isInteger`).
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sel, slot))
			miss := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", miss, sel, nbUndefined))
			stL, doneL := e.freshLabel("dynget.fnstatic"), e.freshLabel("dynget.fndone")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", miss, stL, doneL))
			e.emitLabel(stL)
			st := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_fn_static_get(ptr %s, ptr %s)", st, rec, keyRef))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", st, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(doneL)
			out := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, slot))
			return out
		}
		// Not its own: Function.prototype's method of that name.
		inherited := func(dyn bool) string {
			v := fnVal(dyn)
			if isFnProtoMethod(propName) {
				miss, sel := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", miss, v, nbUndefined))
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", sel, miss, e.emitFnProtoBox(propName).Ref, v))
				return sel
			}
			if !dyn {
				return v
			}
			// Then Object.prototype's (Function.prototype inherits it).
			slot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", v, slot))
			miss := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", miss, v, nbUndefined))
			opL, joinL := e.freshLabel("dynget.fnobjproto"), e.freshLabel("dynget.fnobjjoin")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", miss, opL, joinL))
			e.emitLabel(opL)
			op := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_object_prototype()", op))
			ov := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_get(ptr %s, ptr %s)", ov, op, keyRef))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", ov, slot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			sel := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sel, slot))
			return sel
		}
		for _, t := range []struct {
			tag int
			dyn bool
		}{{kmlTagDynFunc, true}, {kmlTagFuncRef, false}} {
			matchL, nextL = e.emitTagCheck(tag, t.tag, "dynget.fn")
			e.emitLabel(matchL)
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", inherited(t.dyn), resPtr))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
			e.emitLabel(nextL)
		}
	}

	// Primitives (int/float/string/boolean) read Object.prototype's
	// property (undefined when it has none); a statically-shaped reference
	// (object/array/funcRef/stream) has no runtime shape to read from — a
	// clean TypeError until Stage 6 widening.
	isPrim := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", isPrim, tag, kmlTagBoolean))
	primL := e.freshLabel("dynget.prim")
	errL := e.freshLabel("dynget.err")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isPrim, primL, errL))
	e.emitLabel(primL)
	if e.primReadKeys == nil {
		e.primReadKeys = map[string]bool{}
	}
	e.primReadKeys[propName] = true
	pv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_prim_proto_get(i64 %s, ptr %s)", pv, objVal.Ref, keyRef))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pv, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(errL)
	e.emitThrowTypeError("dynamic property access ('" + propName + "') on a statically-typed value is not supported")

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
	if !objVal.Ty.IsDynamic() {
		// As for a read: the paths below take a box.
		b, err := e.emitBoxValue(objVal)
		if err != nil {
			return Value{}, err
		}
		objVal = b
	}
	// `x.__proto__ = p` re-links the prototype (TDD-00155 Stage 3): JS setter
	// semantics — non-object/non-null silently ignored, a cycle throws.
	if propName == "__proto__" {
		return e.emitDynProtoWrite(objVal, rhs, pos)
	}
	if e.dynSetInline {
		return e.emitDynAnyMemberSetInline(objVal, keyRef, propName, rhs, pos)
	}
	boxed, err := e.emitBoxValue(rhs)
	if err != nil {
		return Value{}, err
	}
	// A wrapper object keeps the property in its own bag.
	wrapDoneL, resSlot := "", ""
	if e.wrappersUsed() || e.programIndependent() {
		resSlot = e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resSlot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, resSlot))
		hit := e.freshReg()
		sloppy := 0
		if e.sloppyWrite {
			sloppy = 1
		}
		h := e.emitWrapperSet(objVal.Ref, keyRef, boxed.Ref, sloppy)
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", hit, h))
		wrapDoneL = e.freshLabel("dynset.wrapped")
		setL := e.freshLabel("dynset.plain")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, wrapDoneL, setL))
		e.emitLabel(setL)
	}
	// One routine per property name, as for a read (dynGetHelper).
	fn := e.dynSetHelper(propName, pos)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(i64 %s, ptr %s, i64 %s)", r, fn, objVal.Ref, keyRef, boxed.Ref))
	if wrapDoneL != "" {
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", r, resSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", wrapDoneL))
		e.emitLabel(wrapDoneL)
		r = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", r, resSlot))
	}
	return Value{Ref: r, Ty: TypeAny}, nil
}

// dynSetHelper is `i64 set(i64 value, ptr key, i64 rhs)` for property
// propName ("" for a run-time key), generated once.
func (e *Emitter) dynSetHelper(propName string, pos ast.Pos) string {
	if e.dynSetHelpers == nil {
		e.dynSetHelpers = map[string]string{}
	}
	key := propName
	if e.sloppyWrite {
		key = "\x00sloppy\x00" + propName // a failed write does nothing
	}
	return e.contentNamedHelper(e.dynSetHelpers, key, "@__kml_dynset.", "i64", "i64 %v, ptr %key, i64 %rhs", func() {
		saved := e.dynSetInline
		e.dynSetInline = true
		v, err := e.emitDynAnyMemberSetInline(Value{Ref: "%v", Ty: TypeAny}, "%key", propName, Value{Ref: "%rhs", Ty: TypeAny}, pos)
		e.dynSetInline = saved
		if err != nil {
			if !e.blockDone {
				e.emitTerminator("ret i64 %rhs")
			}
		} else if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret i64 %s", v.Ref))
		}
	})
}

// emitDynAnyMemberSetInline is emitDynAnyMemberSetNamed's dispatch, emitted
// in place.
func (e *Emitter) emitDynAnyMemberSetInline(objVal Value, keyRef, propName string, rhs Value, pos ast.Pos) (Value, error) {
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
	doneL := e.freshLabel("dynset.done")
	// A function's own properties live in its bag (TDD-00229), written as a
	// dynamic object's are.
	fnL, notFnL := e.emitTagCheck(tag, kmlTagDynFunc, "dynset.fn")
	setL := e.freshLabel("dynset.bag")
	e.emitLabel(fnL)
	fnBag := e.emitFnPropsBag(e.emitIntToPtr(payload))
	// `caller`/`arguments`: Function.prototype's throwing accessor, or a
	// sloppy function's read-only own property.
	e.declareFn("__kml_fn_restricted_set", "declare i32 @__kml_fn_restricted_set(ptr, ptr)")
	restr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_fn_restricted_set(ptr %s, ptr %s)", restr, e.emitIntToPtr(payload), keyRef))
	isRestr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", isRestr, restr))
	roL, fnSetL := e.freshLabel("dynset.fnro"), e.freshLabel("dynset.fnset")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRestr, roL, fnSetL))
	e.emitLabel(roL)
	if e.sloppyWrite {
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	} else {
		e.emitDynSetThrow(fnBag, keyRef, "1")
	}
	e.emitLabel(fnSetL)
	fnL = fnSetL
	e.emitTerminator(fmt.Sprintf("br label %%%s", setL))
	e.emitLabel(notFnL)
	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynset.obj")
	e.emitLabel(matchL)
	objBag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", objBag, payload))
	e.emitTerminator(fmt.Sprintf("br label %%%s", setL))
	e.emitLabel(setL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", bag, fnBag, fnL, objBag, matchL))
	// Stage 5: the checked assignment — accessors run, WRITABLE and the
	// extensibility bit are honored; a rejection is the JS strict TypeError.
	status := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", status, bag, keyRef, boxed.Ref))
	nameSfx := ""
	if propName != "" {
		nameSfx = " '" + propName + "'"
	}
	_ = nameSfx
	// A rejection (status 1 to 3) is V8's TypeError for the object.
	rejected := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", rejected, status))
	badL, okL := e.freshLabel("dynset.stbad"), e.freshLabel("dynset.stok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", rejected, badL, okL))
	e.emitLabel(badL)
	if e.sloppyWrite {
		e.emitTerminator(fmt.Sprintf("br label %%%s", okL)) // sloppy: no effect
	} else {
		e.emitDynSetThrow(bag, keyRef, status)
	}
	e.emitLabel(okL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)

	// An array's write result: 2 is a length of no valid array length, 3 a
	// TypedArray's getter-only length, 4 a property a frozen, sealed or
	// non-extensible array refuses (no effect in sloppy code), 0 an element
	// kind its box cannot describe.
	arrStatus := func(code string) {
		{
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 4", is, code))
			badL, okL := e.freshLabel("dynset.arrfrozen"), e.freshLabel("dynset.arrok")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, badL, okL))
			e.emitLabel(badL)
			if e.sloppyWrite {
				e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			} else {
				e.emitThrowTypeError("Cannot add property " + propName + ", object is not extensible")
			}
			e.emitLabel(okL)
		}
		for _, c := range []struct {
			n   int
			msg string
		}{{2, ""}, {3, "Cannot set property length of [object Object] which has only a getter"}, {0, "Cannot assign to an element of this array through `any`"}} {
			is := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, %d", is, code, c.n))
			badL, okL := e.freshLabel("dynset.arrbad"), e.freshLabel("dynset.arrok")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is, badL, okL))
			e.emitLabel(badL)
			if c.n == 2 {
				e.emitThrowRangeError("Invalid array length")
			} else {
				e.emitThrowTypeError(c.msg)
			}
			e.emitLabel(okL)
		}
	}
	// Dynamic array (TDD-00155 Stage 2).
	e.ensureDynArr()
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynArray, "dynset.arr")
	e.emitLabel(matchL)
	arrHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", arrHdr, payload))
	code := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_dynarr_set_by_key(ptr %s, ptr %s, i64 %s)", code, arrHdr, keyRef, boxed.Ref))
	arrStatus(code)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)

	// A boxed static array: an index write stores into its storage.
	e.ensureDynJSONC()
	matchL, nextL = e.emitTagCheck(tag, kmlTagArray, "dynset.sarr")
	e.emitLabel(matchL)
	sbox := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", sbox, payload))
	sok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_anyarr_set_by_key(ptr %s, ptr %s, i64 %s)", sok, sbox, keyRef, boxed.Ref))
	arrStatus(sok)
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

	// A boxed Error: a fixed field (`code`, `syscall`, `errno`, …) of its own
	// type is stored in place; any other own property goes to its `extra`
	// bag, which a read consults (emitDynAnyMemberGetNamed's error arm).
	matchL, nextL = e.emitTagCheck(tag, kmlTagObject, "dynset.errobj")
	e.emitLabel(matchL)
	errPtr, errIsErr := e.emitBoxedErrorProbe(payload)
	errSetL := e.freshLabel("dynset.err")
	notErrL := e.freshLabel("dynset.noterr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", errIsErr, errSetL, notErrL))
	e.emitLabel(errSetL)
	if propName == "name" {
		e.markErrorNameOwn(Value{Ref: errPtr, Ty: errorObjType})
	}
	// An Error subclass's own field: its layout's setter.
	{
		e.ensureShapeRuntime()
		r, set := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_shape_set(ptr %s, ptr %s, i64 %s)", r, errPtr, keyRef, boxed.Ref))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", set, r))
		fixL := e.freshLabel("dynset.errfixed")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", set, doneL, fixL))
		e.emitLabel(fixL)
	}
	fixedIdx, fixedTy, isFixed := errorObjType.FieldIndex(propName)
	storedFixed := false
	if isFixed && propName != ClassTagField && propName != errorExtraField {
		var v Value
		switch {
		case fixedTy.IsDynamic():
			v, storedFixed = boxed, true
		case fixedTy.IR == "ptr" && isStringTy(rhs.Ty) && !rhs.Ty.IsDynamic():
			v, storedFixed = rhs, true
		case fixedTy.Float && isNumberTy(rhs.Ty) && !rhs.Ty.IsDynamic() && !isNullableScalar(rhs.Ty):
			v, storedFixed = e.convert(rhs, TypeF64), true
		}
		if storedFixed {
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, errorObjType.StructIR(), errPtr, fixedIdx))
			e.heapStore(StructFieldIR(fixedTy), v.Ref, gep, 8)
			if propName == "cause" {
				e.markErrorCauseOwn(Value{Ref: errPtr, Ty: errorObjType})
			}
		} else if fixedTy.Float || (fixedTy.IR == "ptr" && isStringTy(fixedTy)) {
			// A value known only at run time (`T | undefined`, any): one of
			// the field's kind is stored there; any other value, undefined
			// included (an own property still, as in Node), clears the field
			// and goes to the bag, which a read of an absent field consults.
			tag, pay := e.emitUnboxTagPayload(boxed)
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, errorObjType.StructIR(), errPtr, fixedIdx))
			var fits, val, zero string
			if fixedTy.Float {
				isF := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isF, tag, kmlTagFloat))
				isI := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isI, tag, kmlTagInt))
				fd := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", fd, pay))
				id := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = sitofp i64 %s to double", id, pay))
				val = e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, double %s, double %s", val, isF, fd, id))
				fits = e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", fits, isF, isI))
				zero = "0.0"
			} else {
				fits = e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", fits, tag, kmlTagString))
				val = e.emitIntToPtr(pay)
				zero = "null"
			}
			stored := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, %s %s, %s %s", stored, fits, StructFieldIR(fixedTy), val, StructFieldIR(fixedTy), zero))
			e.heapStore(StructFieldIR(fixedTy), stored, gep, 8)
			inField := fits
			bagL := e.freshLabel("dynset.errfixbag")
			clearL := e.freshLabel("dynset.errfixclear")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", inField, clearL, bagL))
			// The field holds it now: an earlier value in the bag goes.
			e.emitLabel(clearL)
			cxIdx, _, _ := errorObjType.FieldIndex(errorExtraField)
			cxGep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", cxGep, errorObjType.StructIR(), errPtr, cxIdx))
			cxBag := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", cxBag, cxGep))
			cxNull := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", cxNull, cxBag))
			delL := e.freshLabel("dynset.errfixdel")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cxNull, doneL, delL))
			e.emitLabel(delL)
			e.emitInstr(fmt.Sprintf("call i1 @__kml_dynobj_delete(ptr %s, ptr %s)", cxBag, keyRef))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
			e.emitLabel(bagL)
		}
	}
	if !storedFixed {
		xIdx, _, _ := errorObjType.FieldIndex(errorExtraField)
		xGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", xGep, errorObjType.StructIR(), errPtr, xIdx))
		xBag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", xBag, xGep))
		xNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", xNull, xBag))
		mkL := e.freshLabel("dynset.errbag")
		haveL := e.freshLabel("dynset.errhave")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", xNull, mkL, haveL))
		e.emitLabel(mkL)
		fresh := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", fresh))
		e.heapStore("ptr", fresh, xGep, 8)
		e.emitTerminator(fmt.Sprintf("br label %%%s", haveL))
		e.emitLabel(haveL)
		bag2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bag2, xGep))
		st := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", st, bag2, keyRef, boxed.Ref))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(notErrL)
	// A static object: its layout's setter (TDD-00230 phase 5), unless the
	// object is frozen.
	{
		e.ensureShapeRuntime()
		objp := e.emitIntToPtr(payload)
		e.ensureIntegrityLevels()
		lvl, fz := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_obj_level(ptr %s)", lvl, objp))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", fz, lvl, staticIntegrityFrozen))
		fzL, liveL := e.freshLabel("dynset.frozen"), e.freshLabel("dynset.live")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fz, fzL, liveL))
		e.emitLabel(fzL)
		// A key the object lacks is an addition the freeze refuses.
		fslot, fgot, fhas := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", fslot))
		e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", fslot))
		e.emitInstr(fmt.Sprintf("call i64 @__kml_shape_get(ptr %s, ptr %s, ptr %s)", objp, keyRef, fslot))
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", fgot, fslot))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", fhas, fgot))
		roL, addFzL := e.freshLabel("dynset.frozenro"), e.freshLabel("dynset.frozenadd")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fhas, roL, addFzL))
		e.emitLabel(addFzL)
		if e.sloppyWrite {
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL)) // sloppy: no effect
		} else {
			e.emitStaticSetThrow(objp, keyRef, 2)
		}
		e.emitLabel(roL)
		if e.sloppyWrite {
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		} else {
			e.emitStaticSetThrow(objp, keyRef, 1)
		}
		e.emitLabel(liveL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_shape_set(ptr %s, ptr %s, i64 %s)", r, objp, keyRef, boxed.Ref))
		stored := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", stored, r))
		failL := e.freshLabel("dynset.shapefail")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", stored, doneL, failL))
		e.emitLabel(failL)
		noShape := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, -1", noShape, r))
		absentL := e.freshLabel("dynset.shapeabsent")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", noShape, nextL, absentL))
		e.emitLabel(absentL)
		// A new own property: added beside the layout, unless the object was
		// made non-extensible (Node's message).
		absent2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", absent2, r))
		ext := e.freshReg()
		e.usedObjExtensible = true
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_obj_extensible(ptr %s)", ext, objp))
		add := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", add, absent2, ext))
		addL, refuseL := e.freshLabel("dynset.expando"), e.freshLabel("dynset.refuse")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", add, addL, refuseL))
		e.emitLabel(addL)
		e.emitInstr(fmt.Sprintf("call void @__kml_shape_expando_set(ptr %s, ptr %s, i64 %s)", objp, keyRef, boxed.Ref))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(refuseL)
		if e.sloppyWrite {
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL)) // sloppy: no effect
		} else {
			e.emitThrowTypeError(strings.TrimSpace("Cannot add property "+propName) + ", object is not extensible")
		}
	}
	e.emitLabel(nextL)

	// A class's static member through a constructor reference.
	refL, notRefL := e.emitTagCheck(tag, kmlTagFuncRef, "dynset.classref")
	e.emitLabel(refL)
	e.usedClassRefGet = true
	e.emitInstr(fmt.Sprintf("call void @__kml_classref_set(ptr %s, ptr %s, i64 %s)", e.emitIntToPtr(payload), keyRef, boxed.Ref))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(notRefL)
	// A primitive: sloppy code's write does nothing; strict code's throws.
	if e.sloppyWrite {
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	} else {
		ty, err := e.emitDynamicTypeof(objVal)
		if err != nil {
			return Value{}, err
		}
		str, err := e.emitDynamicToString(objVal)
		if err != nil {
			return Value{}, err
		}
		e.ensureDynJSONC()
		e.declareFn("__kml_prim_set_throw", "declare void @__kml_prim_set_throw(ptr, ptr, ptr)")
		e.emitInstr(fmt.Sprintf("call void @__kml_prim_set_throw(ptr %s, ptr %s, ptr %s)", ty.Ref, str.Ref, keyRef))
		e.emitTerminator("unreachable")
	}
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
	if e.propAttrsUsed() {
		e.emitDynDeleteConfigurableCheck(tag, payload, keyRef)
	}
	if e.wrappersUsed() || e.programIndependent() {
		// A wrapper's own property leaves its bag; a String's index and
		// length are not configurable.
		d := e.emitWrapperDelete(objVal, keyRef)
		gone, kept := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", gone, d))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", kept, d))
		goneL, chkL, keptL, contL := e.freshLabel("dyndel.wgone"), e.freshLabel("dyndel.wchk"), e.freshLabel("dyndel.wkept"), e.freshLabel("dyndel.notwrapped")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", gone, goneL, chkL))
		e.emitLabel(goneL)
		e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(chkL)
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", kept, keptL, contL))
		e.emitLabel(keptL)
		if e.sloppyWrite {
			e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", resPtr))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		} else {
			msg := Value{Ref: e.internString("Cannot delete property '"), Ty: TypePtr}
			for _, part := range []string{keyRef, e.internString("' of [object String]")} {
				if m2, err := e.emitStringConcat(msg, Value{Ref: part, Ty: TypePtr}); err == nil {
					msg = m2
				}
			}
			e.emitThrowTypeErrorValue(msg.Ref)
		}
		e.emitLabel(contL)
	}

	// A function's own property leaves its bag (TDD-00229).
	fnL, notFnL := e.emitTagCheck(tag, kmlTagDynFunc, "dyndel.fn")
	e.emitLabel(fnL)
	{
		e.ensureFnMeta()
		props := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_props_dyn(ptr %s)", props, e.emitIntToPtr(payload)))
		some := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", some, props))
		bagL, noneL := e.freshLabel("dyndel.fnbag"), e.freshLabel("dyndel.fnnone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", some, bagL, noneL))
		e.emitLabel(bagL)
		d := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_delete(ptr %s, ptr %s)", d, props, keyRef))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", d, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(noneL)
		e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}
	e.emitLabel(notFnL)

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
	if e.sloppyWrite {
		// Sloppy code: the delete answers false.
		e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	} else {
		// V8's wording: `Cannot delete property 'x' of #<Object>`.
		e.emitDynSetThrow(bag, keyRef, "4")
	}
	e.emitLabel(delOKL)
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// A static object: a property added beside its layout is removed; a
	// sealed or frozen one's own property is not configurable.
	matchL, nextL = e.emitTagCheck(tag, kmlTagObject, "dyndel.static")
	e.emitLabel(matchL)
	e.ensureShapeRuntime()
	{
		objp := e.emitIntToPtr(payload)
		e.ensureIntegrityLevels()
		lvl, isF, isS, locked := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_obj_level(ptr %s)", lvl, objp))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isF, lvl, staticIntegrityFrozen))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isS, lvl, staticIntegritySealed))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", locked, isF, isS))
		lockL, freeL := e.freshLabel("dyndel.locked"), e.freshLabel("dyndel.free")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", locked, lockL, freeL))
		e.emitLabel(lockL)
		slot, got, has := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", slot))
		e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", slot))
		e.emitInstr(fmt.Sprintf("call i64 @__kml_shape_get(ptr %s, ptr %s, ptr %s)", objp, keyRef, slot))
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", got, slot))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", has, got))
		ncL := e.freshLabel("dyndel.staticnc")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, ncL, freeL))
		e.emitLabel(ncL)
		if e.sloppyWrite {
			e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", resPtr))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		} else {
			e.emitStaticSetThrow(objp, keyRef, 4)
		}
		e.emitLabel(freeL)
	}
	e.emitInstr(fmt.Sprintf("call i32 @__kml_shape_expando_delete(ptr %s, ptr %s)", e.emitIntToPtr(payload), keyRef))
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// An array: a property added to it is removed (a dynamic array keys its
	// added properties by itself, a boxed static array by its header).
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynArray, "dyndel.arr")
	e.emitLabel(matchL)
	e.declareFn("__kml_dynarr_delete_key", "declare void @__kml_dynarr_delete_key(ptr, ptr)")
	e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_delete_key(ptr %s, ptr %s)", e.emitIntToPtr(payload), keyRef))
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)
	// A boxed static array: an index becomes a hole; a TypedArray's element
	// cannot be deleted.
	matchL, nextL = e.emitTagCheck(tag, kmlTagArray, "dyndel.sarr")
	e.emitLabel(matchL)
	e.ensureDynJSONC()
	e.ensureHoles()
	e.declareFn("__kml_anyarr_delete_key", "declare ptr @__kml_anyarr_delete_key(ptr, ptr)")
	dr, drOk := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_delete_key(ptr %s, ptr %s)", dr, e.emitIntToPtr(payload), keyRef))
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", drOk, dr))
	tdelL, tdelErrL := e.freshLabel("dyndel.sarrok"), e.freshLabel("dyndel.typed")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", drOk, tdelL, tdelErrL))
	e.emitLabel(tdelErrL)
	tmsg1, derr := e.emitStringConcat(Value{Ref: e.internString("Cannot delete property '"), Ty: TypePtr}, Value{Ref: keyRef, Ty: TypePtr})
	if derr != nil {
		return Value{}, derr
	}
	tmsg2, derr := e.emitStringConcat(tmsg1, Value{Ref: e.internString("' of [object "), Ty: TypePtr})
	if derr != nil {
		return Value{}, derr
	}
	e.ensureStrHeaderRuntime()
	tname := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", tname, dr))
	if tmsg2, derr = e.emitStringConcat(tmsg2, Value{Ref: tname, Ty: TypePtr}); derr != nil {
		return Value{}, derr
	}
	if tmsg2, derr = e.emitStringConcat(tmsg2, Value{Ref: e.internString("]"), Ty: TypePtr}); derr != nil {
		return Value{}, derr
	}
	e.emitThrowTypeErrorValue(tmsg2.Ref)
	e.emitLabel(tdelL)
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", resPtr))
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
	doneL := e.freshLabel("dynin.done")
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", resPtr))
	if e.wrappersUsed() || e.programIndependent() {
		// A wrapper's own property: a String's index or length, or one
		// assigned to it.
		h, hit := e.emitWrapperHas(objVal, keyRef), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", hit, h))
		hitL, contL := e.freshLabel("dynin.wrapped"), e.freshLabel("dynin.notwrapped")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, hitL, contL))
		e.emitLabel(hitL)
		e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(contL)
	}
	tag, payload := e.emitUnboxTagPayload(objVal)
	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynin.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @%s(ptr %s, ptr %s)", r, hasFn, bag, keyRef))
	e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nextL)
	// An array: its indices below the length and `length`, then
	// Array.prototype's members.
	{
		isDynArr, isStatArr := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDynArr, tag, kmlTagDynArray))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStatArr, tag, kmlTagArray))
		isArr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isArr, isDynArr, isStatArr))
		arrL, notArrL := e.freshLabel("dynin.arr"), e.freshLabel("dynin.notarr")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, arrL, notArrL))
		e.emitLabel(arrL)
		lenVal, err := e.emitDynAnyMemberGetNamed(objVal, e.internString("length"), "length", pos)
		if err != nil {
			return Value{}, err
		}
		n := e.convert(lenVal, TypeI64)
		e.ensureFnMeta()
		own := "0"
		if ownOnly {
			own = "1"
		}
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_array_has_key(i64 %s, ptr %s, i1 %s, i64 %s)", b, n.Ref, keyRef, own, objVal.Ref))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", b, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(notArrL)
	}
	// A function: its own-property bag (TDD-00229), then `name`/`length`
	// and, through the prototype, Function.prototype's members.
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynFunc, "dynin.fn")
	e.emitLabel(matchL)
	{
		e.ensureFnMeta()
		props := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_props_dyn(ptr %s)", props, e.emitIntToPtr(payload)))
		some := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", some, props))
		bagL := e.freshLabel("dynin.fnbag")
		restL := e.freshLabel("dynin.fnrest")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", some, bagL, restL))
		e.emitLabel(bagL)
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @%s(ptr %s, ptr %s)", r, hasFn, props, keyRef))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", r, resPtr))
		// The bag holds `name` and `length` from its creation, so a miss
		// there (deleted) leaves only Function.prototype's members.
		protoL := e.freshLabel("dynin.fnproto")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", r, doneL, protoL))
		e.emitLabel(protoL)
		// A sloppy function owns `caller` and `arguments`.
		e.declareFn("__kml_fn_owns_restricted", "declare i1 @__kml_fn_owns_restricted(ptr, ptr)")
		ownsR := func() string {
			r := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_fn_owns_restricted(ptr %s, ptr %s)", r, e.emitIntToPtr(payload), keyRef))
			return r
		}
		if ownOnly {
			e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", ownsR(), resPtr))
		} else {
			pm := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_fn_has_proto_member(ptr %s)", pm, keyRef))
			e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", pm, resPtr))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(restL)
		own := "0"
		if ownOnly {
			own = "1"
		}
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_fn_has_builtin(ptr %s, i1 %s)", b, keyRef, own))
		if ownOnly {
			either := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", either, b, ownsR()))
			b = either
		}
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", b, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(nextL)
	// A static object: its layout row answers — an own field or added
	// property (found 1), or a class member on its prototype (found 2).
	matchL, nextL = e.emitTagCheck(tag, kmlTagObject, "dynin.static")
	e.emitLabel(matchL)
	{
		e.noteErrorSubclassLayouts()
		obj := e.emitIntToPtr(payload)
		_, found := e.emitShapeGet(obj, keyRef)
		hit := e.freshReg()
		if ownOnly {
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", hit, found))
		} else {
			e.emitInstr(fmt.Sprintf("%s = icmp sgt i32 %s, 0", hit, found))
		}
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", hit, resPtr))
		// An Error's own `message` and `stack`, and its prototype's `name`,
		// `toString` and `constructor`, which no layout row lists.
		errL := e.freshLabel("dynin.err")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hit, doneL, errL))
		e.emitLabel(errL)
		e.ensureStrcmp()
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", hdr, obj))
		flag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", flag, hdr, int64(1)<<48))
		isErr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isErr, flag))
		acc := "false"
		keys := []string{"message", "stack"}
		if !ownOnly {
			keys = append(keys, "name", "toString", "constructor")
		}
		for _, k := range keys {
			c, eq, or := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", c, keyRef, e.internString(k)))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eq, c))
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", or, acc, eq))
			acc = or
		}
		// Its system fields set on it (`errno`, `code`, …) and its bag of
		// further own properties.
		e.declareFn("__kml_error_extra_get", "declare i64 @__kml_error_extra_get(ptr, ptr, ptr)")
		xfound := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", xfound))
		e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", xfound))
		xL, xDoneL := e.freshLabel("dynin.errx"), e.freshLabel("dynin.errxdone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, xL, xDoneL))
		e.emitLabel(xL)
		e.emitInstr(fmt.Sprintf("call i64 @__kml_error_extra_get(ptr %s, ptr %s, ptr %s)", obj, keyRef, xfound))
		e.emitTerminator(fmt.Sprintf("br label %%%s", xDoneL))
		e.emitLabel(xDoneL)
		xf, xHit := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", xf, xfound))
		e.emitInstr(fmt.Sprintf("%s = icmp sgt i32 %s, 0", xHit, xf))
		named := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", named, isErr, acc))
		errHit := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", errHit, named, xHit))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", errHit, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(nextL)
	if ownOnly {
		// hasOwnProperty converts a primitive to its object: a string has
		// its indices and length, the others nothing; null and undefined
		// throw.
		strL, notStrL := e.emitTagCheck(tag, kmlTagString, "dynin.str")
		e.emitLabel(strL)
		e.ensureWrapperC()
		h, hit := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_str_has_own(ptr %s, ptr %s)", h, e.emitIntToPtr(payload), keyRef))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", hit, h))
		e.emitInstr(fmt.Sprintf("store i1 %s, ptr %s, align 1", hit, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(notStrL)
		isNull, isUndef, nullish := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isNull, tag, kmlTagNull))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isUndef, tag, kmlTagUndefined))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", nullish, isNull, isUndef))
		throwL, primL := e.freshLabel("dynin.nullish"), e.freshLabel("dynin.prim")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, throwL, primL))
		e.emitLabel(throwL)
		e.emitThrowTypeError("Cannot convert undefined or null to object")
		e.emitLabel(primL)
		e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	} else {
		e.emitThrowTypeError("Cannot use 'in' operator on a non-object value")
	}
	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", result, resPtr))
	return Value{Ref: result, Ty: TypeBool}, nil
}

// emitDynAnyKeys implements Object.keys / for...in key collection on an
// any-typed value: the bag's insertion-ordered key list for a dynamic object,
// TypeError on null/undefined, a string's index keys, and an empty string[]
// for every other primitive (ES2015 Object.keys(primitive) → []).
func (e *Emitter) emitDynAnyKeys(objVal Value, pos ast.Pos) (Value, error) {
	e.ensureDynObj()
	tag, payload := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca { ptr, i64 }, align 8", resPtr))
	mergeL := e.freshLabel("dynkeys.merge")
	e.emitWrapperKeys(objVal, false, resPtr, mergeL)

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynkeys.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	r := e.freshReg()
	if e.dynKeysForIn {
		// for...in also walks the prototype chain.
		e.declareFn("__kml_dynobj_forin_keys_c", "declare ptr @__kml_dynobj_forin_keys_c(ptr, ptr)")
		np, arr, n, r0 := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", np))
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_forin_keys_c(ptr %s, ptr %s)", arr, bag, np))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, np))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", r0, arr))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", r, r0, n))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_dynobj_keys_enum(ptr %s)", r, bag))
	}
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// A string: its index keys, one per UTF-16 unit.
	e.ensureStrHeaderRuntime()
	matchL, nextL = e.emitTagCheck(tag, kmlTagString, "dynkeys.str")
	e.emitLabel(matchL)
	{
		sp, nSlot, keysC, nk, k0, k := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", sp, payload))
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", nSlot))
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_index_keys(ptr %s, ptr %s)", keysC, sp, nSlot))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", nk, nSlot))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", k0, keysC))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", k, k0, nk))
		e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", k, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}
	e.emitLabel(nextL)

	// Dynamic array: index strings "0".."len-1" (TDD-00155 Stage 2).
	e.ensureDynArr()
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynArray, "dynkeys.arr")
	e.emitLabel(matchL)
	arrHdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", arrHdr, payload))
	// Its index keys, then the properties added to it.
	anSlot, akC, akN, ak0, ak := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", anSlot))
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_keys_c(ptr %s, ptr %s)", akC, arrHdr, anSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", akN, anSlot))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", ak0, akC))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", ak, ak0, akN))
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", ak, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// A static array or TypedArray: its index keys.
	e.ensureDynJSONC()
	matchL, nextL = e.emitTagCheck(tag, kmlTagArray, "dynkeys.sarr")
	e.emitLabel(matchL)
	sbox := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", sbox, payload))
	slen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_anyarr_len(ptr %s)", slen, sbox))
	_ = slen
	// A hole is no key.
	nSlot, keysC, nk, ik0, ik := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", nSlot))
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_anyarr_keys_c(ptr %s, ptr %s)", keysC, sbox, nSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", nk, nSlot))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", ik0, keysC))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", ik, ik0, nk))
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", ik, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// A function's own enumerable keys: its own-property bag (TDD-00229),
	// when it has one.
	matchL, nextL = e.emitTagCheck(tag, kmlTagDynFunc, "dynkeys.fn")
	e.emitLabel(matchL)
	e.ensureFnMeta()
	props := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_props_dyn(ptr %s)", props, e.emitIntToPtr(payload)))
	isExt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isExt, props))
	extL := e.freshLabel("dynkeys.fnprops")
	noPropsL := e.freshLabel("dynkeys.fnnone")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isExt, extL, noPropsL))
	e.emitLabel(extL)
	pk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_dynobj_keys_enum(ptr %s)", pk, props))
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", pk, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(noPropsL)
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } { ptr null, i64 0 }, ptr %s, align 8", resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// A class: its own enumerable static fields.
	matchL, nextL = e.emitTagCheck(tag, kmlTagFuncRef, "dynkeys.class")
	e.emitLabel(matchL)
	e.ensureMalloc()
	ck := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_classref_keys(ptr %s)", ck, e.emitIntToPtr(payload)))
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", ck, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// A static object: its layout row's keys (TDD-00233).
	matchL, nextL = e.emitTagCheck(tag, kmlTagObject, "dynkeys.static")
	e.emitLabel(matchL)
	e.ensureShapeKeysArray()
	so := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", so, payload))
	sk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { ptr, i64 } @__kml_shape_keys_array(ptr %s)", sk, so))
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", sk, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// A string: its index keys (Object.assign / spread copy its characters).
	matchL, nextL = e.emitTagCheck(tag, kmlTagString, "dynkeys.str")
	e.emitLabel(matchL)
	strLen, err := e.emitDynAnyMemberGetNamed(objVal, e.internString("length"), "length", pos)
	if err != nil {
		return Value{}, err
	}
	sn := e.convert(strLen, TypeI64)
	sKeys, sk0, sk1 := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_index_keys_c(i64 %s)", sKeys, sn.Ref))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", sk0, sKeys))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", sk1, sk0, sn.Ref))
	e.emitInstr(fmt.Sprintf("store { ptr, i64 } %s, ptr %s, align 8", sk1, resPtr))
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
	return e.arrayFromAgg(result, ArrayOf(TypePtr)), nil
}

// ensureDynObjEntries emits @__kml_dynobj_entries(bag, withKeys): a dynarr of
// the bag's enumerable own values (withKeys=0, Object.values) or of [key,
// value] two-element dynarrs (withKeys=1, Object.entries), in keys_enum order.
func (e *Emitter) ensureDynObjEntries() {
	if e.usedDynObjEntries {
		return
	}
	e.usedDynObjEntries = true
	e.ensureDynObj()
	e.ensureDynArr()
	e.emitGlobal(`
define ptr @__kml_dynobj_entries(ptr %o, i1 %withKeys) {
entry:
  %keys = call { ptr, i64 } @__kml_dynobj_keys_enum(ptr %o)
  %kp = extractvalue { ptr, i64 } %keys, 0
  %n = extractvalue { ptr, i64 } %keys, 1
  %out = call ptr @__kml_dynarr_new(i64 %n)
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %next ]
  %done = icmp sge i64 %i, %n
  br i1 %done, label %ret, label %body
body:
  %slot = getelementptr ptr, ptr %kp, i64 %i
  %key = load ptr, ptr %slot, align 8
  %v = call i64 @__kml_dynobj_get(ptr %o, ptr %key)
  br i1 %withKeys, label %pair, label %plain
pair:
  %p = call ptr @__kml_dynarr_new(i64 2)
  %ks = call ptr @__kml_str_from_cstr(ptr %key)
  %kb = ptrtoint ptr %ks to i64
  call void @__kml_dynarr_push(ptr %p, i64 %kb)
  call void @__kml_dynarr_push(ptr %p, i64 %v)
  %pb0 = ptrtoint ptr %p to i64
  %pb = or i64 %pb0, 6
  call void @__kml_dynarr_push(ptr %out, i64 %pb)
  br label %next
plain:
  call void @__kml_dynarr_push(ptr %out, i64 %v)
  br label %next
next:
  %inext = add i64 %i, 1
  br label %loop
ret:
  ret ptr %out
}`)
}

// emitDynAnyEntries implements Object.entries / Object.values on an any-typed
// value (TDD-00155 Stage 6 residue): a dynamic array of [key, value] pairs or
// of values for a dynamic object; a dynamic array answers its index/element
// pairs; null/undefined throw; any other tag yields []. The result is `any`
// (its element shapes exist only at run time).
func (e *Emitter) emitDynAnyEntries(objVal Value, withKeys bool, pos ast.Pos) (Value, error) {
	e.ensureDynObjEntries()
	e.ensureStrHeaderRuntime()
	tag, payload := e.emitUnboxTagPayload(objVal)
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	mergeL := e.freshLabel("dynentries.merge")
	wk := "0"
	if withKeys {
		wk = "1"
	}

	// A function's own properties (TDD-00229), when it has a bag.
	fnL, notFnL := e.emitTagCheck(tag, kmlTagDynFunc, "dynentries.fn")
	e.emitLabel(fnL)
	{
		e.ensureFnMeta()
		props := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fn_props_dyn(ptr %s)", props, e.emitIntToPtr(payload)))
		some := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", some, props))
		bagL, noneL := e.freshLabel("dynentries.fnbag"), e.freshLabel("dynentries.fnnone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", some, bagL, noneL))
		e.emitLabel(bagL)
		fr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_entries(ptr %s, i1 %s)", fr, props, wk))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fr, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(noneL)
		er := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 0)", er))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", er, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}
	e.emitLabel(notFnL)

	matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynentries.obj")
	e.emitLabel(matchL)
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", bag, payload))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_entries(ptr %s, i1 %s)", r, bag, wk))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", r, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)

	// An array (a dynarr, or a static array boxed into any) or a string:
	// walk it by its runtime length through the dynamic index read, so each
	// element comes out by its own kind rules — ["0", v0], ["1", v1], … or
	// the values (a string's UTF-16 units).
	isDynArr, isStatArr, isStr := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDynArr, tag, kmlTagDynArray))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStatArr, tag, kmlTagArray))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isStr, tag, kmlTagString))
	isArr0, isArr := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isArr0, isDynArr, isStatArr))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isArr, isArr0, isStr))
	arrL := e.freshLabel("dynentries.arr")
	notArrL := e.freshLabel("dynentries.notarr")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, arrL, notArrL))
	e.emitLabel(arrL)
	e.ensureSprintf()
	lenVal, err := e.emitDynAnyMemberGetNamed(objVal, e.internString("length"), "length", pos)
	if err != nil {
		return Value{}, err
	}
	_, lenPay := e.emitUnboxTagPayload(lenVal)
	lenD := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = bitcast i64 %s to double", lenD, lenPay))
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", n, lenD))
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 %s)", out, n))
	idxSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxSlot))
	condL := e.freshLabel("dynentries.arr.cond")
	bodyL := e.freshLabel("dynentries.arr.body")
	endL := e.freshLabel("dynentries.arr.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idxSlot))
	c := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", c, i, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", c, bodyL, endL))
	e.emitLabel(bodyL)
	key := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 24)", key))
	e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", key, e.internString("%lld"), i))
	e.emitInstr(fmt.Sprintf("call void @__kml_str_finalize(ptr %s)", key))
	elem, err := e.emitDynAnyMemberGet(objVal, key, pos)
	if err != nil {
		return Value{}, err
	}
	if withKeys {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 2)", p))
		kb := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", kb, key))
		e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", p, kb))
		e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", p, elem.Ref))
		pb := e.emitNbTagPtr(p, kmlTagDynArray)
		e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", out, pb))
	} else {
		e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", out, elem.Ref))
	}
	i2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i2, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i2, idxSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", out, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(notArrL)

	// A static object: its row's present keys, in order.
	matchL, nextL = e.emitTagCheck(tag, kmlTagObject, "dynentries.static")
	e.emitLabel(matchL)
	{
		e.ensureShapeRuntime()
		obj := e.emitIntToPtr(payload)
		n0, n := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_nkeys(ptr %s)", n0, obj))
		neg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", neg, n0))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n, neg, n0))
		sout := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 %s)", sout, n))
		found := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", found))
		sidx := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", sidx))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", sidx))
		scond, sbody, skeep, snext, send := e.freshLabel("dynentries.st.cond"), e.freshLabel("dynentries.st.body"), e.freshLabel("dynentries.st.keep"), e.freshLabel("dynentries.st.next"), e.freshLabel("dynentries.st.end")
		e.emitTerminator(fmt.Sprintf("br label %%%s", scond))
		e.emitLabel(scond)
		si, sc := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", si, sidx))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", sc, si, n))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sc, sbody, send))
		e.emitLabel(sbody)
		k, v, fv, has := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_shape_key(ptr %s, i64 %s)", k, obj, si))
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_shape_get(ptr %s, ptr %s, ptr %s)", v, obj, k, found))
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", fv, found))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", has, fv))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, skeep, snext))
		e.emitLabel(skeep)
		if withKeys {
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 2)", p))
			kb := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", kb, k))
			e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", p, kb))
			e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", p, v))
			e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", sout, e.emitNbTagPtr(p, kmlTagDynArray)))
		} else {
			e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", sout, v))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", snext))
		e.emitLabel(snext)
		sn := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", sn, si))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sn, sidx))
		e.emitTerminator(fmt.Sprintf("br label %%%s", scond))
		e.emitLabel(send)
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sout, resPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}
	e.emitLabel(nextL)

	isNullish := e.freshReg()
	nullCmp, undefCmp := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", nullCmp, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", undefCmp, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isNullish, nullCmp, undefCmp))
	throwL := e.freshLabel("dynentries.throw")
	emptyL := e.freshLabel("dynentries.empty")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullish, throwL, emptyL))
	e.emitLabel(throwL)
	e.emitThrowTypeError("Cannot convert undefined or null to object")
	e.emitLabel(emptyL)
	empty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 0)", empty))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", empty, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return Value{Ref: e.emitNbTagPtr(result, kmlTagDynArray), Ty: TypeAny}, nil
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

// emitDynSetProtoChecked calls set_proto and turns a refusal into the JS
// TypeError: a non-extensible object's, else a cycle's.
func (e *Emitter) emitDynSetProtoChecked(bagReg, protoReg string) {
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_set_proto(ptr %s, ptr %s)", ok, bagReg, protoReg))
	okL := e.freshLabel("setproto.ok")
	failL := e.freshLabel("setproto.fail")
	nonExtL := e.freshLabel("setproto.nonext")
	cycL := e.freshLabel("setproto.cycle")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, failL))
	e.emitLabel(failL)
	ext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_flags_test(ptr %s, i64 0)", ext, bagReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ext, cycL, nonExtL))
	e.emitLabel(nonExtL)
	e.emitThrowTypeError("#<Object> is not extensible")
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
	if len(args) == 2 {
		// Object.create(proto, props): the object, then
		// Object.defineProperties(o, props) unless props is undefined.
		o, err := e.emitObjectCreate(args[:1], pos)
		if err != nil {
			return Value{}, err
		}
		if isUndefinedLiteral(e, args[1]) {
			return o, nil
		}
		return e.emitObjectDefineProperties([]ast.Expression{e.bindValue(o, pos), args[1]}, pos)
	}
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.create takes 1 or 2 arguments", pos.Line, pos.Col)
	}
	protoVal, err := e.emitExprWithObjectHint(args[0], TypeAny)
	if err != nil {
		return Value{}, err
	}
	if !protoVal.Ty.IsDynamic() && !protoVal.Ty.IsNull() {
		return Value{}, fmt.Errorf("%d:%d: Object.create requires a dynamic (any-typed) object or null prototype", pos.Line, pos.Col)
	}
	e.ensureDynObj()
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	if protoVal.Ty.IsNull() {
		// set_proto(null) marks the bag NULLPROTO (`[Object: null prototype]`).
		e.emitDynSetProtoChecked(bag, "null")
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
	if !val.Ty.IsDynamic() && (isHostHandle(val.Ty) || headeredHost(val.Ty) || val.Ty.IsPromise() || val.Ty.IsArray() || isForOfStringTy(val.Ty) || val.Ty.IsClass() || val.Ty.IsFunc() || val.Ty.IsGenerator()) {
		// A builtin class's instance boxes; its prototype is its class's.
		if val, err = e.emitBoxValue(val); err != nil {
			return Value{}, err
		}
	}
	if !val.Ty.IsDynamic() {
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
	if e.dunderRead {
		// `x.__proto__` on a chain not reaching Object.prototype's accessor
		// reads a plain property (`Object.create(null).__proto__` is
		// undefined).
		e.dunderRead = false
		e.declareFn("__kml_dynobj_proto_prop", "declare i1 @__kml_dynobj_proto_prop(ptr, ptr)")
		plain := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_proto_prop(ptr %s, ptr %s)", plain, bag, resPtr))
		plainL, linkL := e.freshLabel("protoget.plain"), e.freshLabel("protoget.link")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", plain, plainL, linkL))
		e.emitLabel(plainL)
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(linkL)
	}
	proto := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_get_proto(ptr %s)", proto, bag))
	boxed := e.emitBoxBagOrNull(proto)
	if e.genProtos {
		// %GeneratorFunction.prototype% inherits Function.prototype, a
		// function record no bag links to.
		e.ensureGenProtoRuntime()
		ov := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_genfn_proto_parent(ptr %s, i64 %s, i64 %s)", ov, bag, e.emitFnProtoBox("").Ref, boxed.Ref))
		boxed = Value{Ref: ov, Ty: TypeAny}
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", boxed.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(nextL)
	// An array: Array.prototype; a TypedArray its kind's.
	arrProto := e.emitBuiltinProto("Array")
	dynArrL, staticL := e.emitTagCheck(tag, kmlTagDynArray, "protoget.dynarr")
	e.emitLabel(dynArrL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", arrProto.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(staticL)
	statArrL, notArrL := e.emitTagCheck(tag, kmlTagArray, "protoget.arr")
	e.emitLabel(statArrL)
	e.ensureDynJSONC()
	e.declareFn("__kml_anyarr_is_typed", "declare i32 @__kml_anyarr_is_typed(ptr)")
	abox, typed, isTyped := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", abox, payload))
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_anyarr_is_typed(ptr %s)", typed, abox))
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", isTyped, typed))
	sel := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 %s", sel, isTyped, nbNull, arrProto.Ref))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sel, resPtr))
	if !e.programIndependent() && e.usesTypedArrays() {
		// A TypedArray: its kind's prototype.
		e.declareFn("__kml_any_typed_index", "declare i64 @__kml_any_typed_index(i64)")
		k := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_any_typed_index(i64 %s)", k, objVal.Ref))
		var cases strings.Builder
		labels := make([]string, len(typedKinds))
		for i := range typedKinds {
			labels[i] = e.freshLabel("protoget.typed")
			fmt.Fprintf(&cases, " i64 %d, label %%%s", i, labels[i])
		}
		e.emitTerminator(fmt.Sprintf("switch i64 %s, label %%%s [%s ]", k, mergeL, cases.String()))
		for i, class := range typedKinds {
			e.emitLabel(labels[i])
			pv := e.emitTypedKindProto(class)
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", pv.Ref, resPtr))
			e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		}
	} else {
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	}
	e.emitLabel(notArrL)
	// A string: String.prototype.
	strL, notStrL := e.emitTagCheck(tag, kmlTagString, "protoget.str")
	e.emitLabel(strL)
	strProto := e.emitBuiltinProto("String")
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", strProto.Ref, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(notStrL)
	// A function: Function.prototype.
	fnL, notFnL := e.emitTagCheck(tag, kmlTagDynFunc, "protoget.fn")
	e.emitLabel(fnL)
	fnProto := e.emitFnProtoBox("").Ref
	if e.genProtos {
		// A generator function's is %GeneratorFunction.prototype%.
		e.ensureGenProtoRuntime()
		fp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_fn_proto_of(ptr %s, i64 %s)", fp, e.emitIntToPtr(payload), fnProto))
		fnProto = fp
	}
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", fnProto, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(notFnL)
	// A class: the class it extends, else Function.prototype.
	refL, notRefL := e.emitTagCheck(tag, kmlTagFuncRef, "protoget.ref")
	e.emitLabel(refL)
	e.usedClassRefProto = true
	e.declareFn("__kml_classref_proto", "declare i64 @__kml_classref_proto(ptr)")
	cp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_classref_proto(ptr %s)", cp, e.emitIntToPtr(payload)))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", cp, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(notRefL)
	// A builtin class's instance (a Map, a Promise, …): its class's
	// prototype object.
	hostL, restL := e.emitTagCheck(tag, kmlTagObject, "protoget.host")
	e.emitLabel(hostL)
	cell := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", cell, payload))
	hp := e.emitBuiltinProtoOf(cell)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", hp, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(restL)

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
	if !objVal.Ty.IsDynamic() {
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
	// A chain not reaching Object.prototype's accessor sets a plain
	// property (`Object.create(null).__proto__ = 5`).
	e.declareFn("__kml_dynobj_proto_setplain", "declare i1 @__kml_dynobj_proto_setplain(ptr, i64)")
	plain := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dynobj_proto_setplain(ptr %s, i64 %s)", plain, bag, boxedRhs.Ref))
	linkL := e.freshLabel("protoset.link")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", plain, doneL, linkL))
	e.emitLabel(linkL)
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
func (e *Emitter) emitJSONStringifyDynamic(val Value, ind jsonIndent, pos ast.Pos) (Value, error) {
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
	// The pretty-print unit (JSON.stringify's `space`) is a compile-time
	// constant string; an empty unit passes null → compact output, byte-
	// identical to the pre-pretty path.
	indentArg := "null"
	if ind.unit != "" {
		indentArg = e.internString(ind.unit)
	}
	errSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", errSlot))
	s := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynjson_stringify_at(i64 %s, i64 %s, ptr %s, i64 %d, ptr %s)", s, tagW, payload, indentArg, ind.depth, errSlot))
	errv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", errv, errSlot))

	circL := e.freshLabel("dynjson.circ")
	next1 := e.freshLabel("dynjson.next1")
	isCirc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", isCirc, errv))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isCirc, circL, next1))
	e.emitLabel(circL)
	// V8's message, naming the path around the cycle (the walk's own).
	cm, hasMsg, msg := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynjson_circ_msg()", cm))
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasMsg, cm))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", msg, hasMsg, cm, e.internString("Converting circular structure to JSON")))
	e.emitThrowTypeErrorValue(msg)
	e.emitLabel(next1)

	staticL := e.freshLabel("dynjson.static")
	next2 := e.freshLabel("dynjson.next2")
	isStatic := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 2", isStatic, errv))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStatic, staticL, next2))
	e.emitLabel(staticL)
	e.emitThrowTypeError("JSON.stringify of a statically-typed value in a dynamic position is not supported")
	e.emitLabel(next2)

	bigL := e.freshLabel("dynjson.bigint")
	next3 := e.freshLabel("dynjson.next3")
	isBig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 3", isBig, errv))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBig, bigL, next3))
	e.emitLabel(bigL)
	e.emitThrowTypeError("Do not know how to serialize a BigInt")
	e.emitLabel(next3)

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
		if sp, ok := el.(*ast.SpreadElement); ok {
			if err := e.pushSpreadIntoDynArr(arr, sp, lit.GetPos()); err != nil {
				return Value{}, err
			}
			continue
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

// isFreshObjectAlloc reports whether srcExpr allocates a brand-new object with
// no prior binding — a `new C()`. Only a fresh allocation is eligible for
// allocation-site widening: with no alias, realizing it as a D1 bag keeps the
// single-representation invariant that makes `===`/mutation faithful. (Object
// *literals* in an any context already widen upstream via emitDynObjLiteral.)
func (e *Emitter) isFreshObjectAlloc(srcExpr ast.Expression) bool {
	if srcExpr == nil {
		return false
	}
	_, ok := srcExpr.(*ast.NewExpression)
	return ok
}

// emitBoxValueWidened boxes v into `any`, applying allocation-site widening
// (TDD-00155 Stage 6) when v is a fresh, widenable static-object allocation —
// realizing it as a D1 bag so `a.x`/`Object.keys(a)`/index access work — and
// otherwise boxing normally. srcExpr is the AST that produced v (nil disables
// widening). Used at every box-to-`any` boundary (var-decl, return, argument,
// assignment) so the behavior is uniform across them.
func (e *Emitter) emitBoxValueWidened(v Value, srcExpr ast.Expression) (Value, error) {
	if v.Ty.IsObject() && !v.Ty.IsDynamic() && e.isFreshObjectAlloc(srcExpr) &&
		e.dynWidenable(v.Ty, map[string]bool{}) {
		return e.emitStaticObjToBag(v)
	}
	return e.emitBoxValue(v)
}

// dynWidenable reports whether a static object type can be faithfully realized
// as a D1 bag by emitStaticObjToBag (TDD-00155 Stage 6). It rejects:
//   - a class carrying methods — a data-only bag would drop `a.method()`, a
//     silent divergence (that case stays a clean rejection until dynamic
//     method installation is built);
//   - a cyclic object graph (a self- or mutually-referential object field) —
//     the helper unrolls the shape at compile time, so a cycle would unroll
//     forever; `seen` breaks the recursion by declaring a cycle non-widenable.
//
// A plain data shape (interface/struct types, data-only classes) with
// scalar/array/nested-data-object fields is widenable. `seen` is keyed by the
// type's registry name; pass a fresh map at the top call.
func (e *Emitter) dynWidenable(t Type, seen map[string]bool) bool {
	if !t.IsObject() || t.IsDynamic() {
		return false
	}
	key := t.ClassName
	if key == "" {
		key = t.RefName
	}
	if key != "" && seen[key] {
		return false // cycle → not widenable (would unroll forever)
	}
	if t.IsClass() {
		// A class instance stays itself (its identity, class name and
		// methods); properties added through `any` live beside its layout
		// (shape.c).
		return false
	}
	if key != "" {
		seen[key] = true
		defer delete(seen, key)
	}
	for _, f := range t.VisibleFields() {
		if f.Ty.IsObject() && !e.dynWidenable(f.Ty, seen) {
			return false
		}
		// An array field converts to a D1 dynamic array; that is faithful for
		// scalar/nested-array elements and for object elements that are
		// themselves widenable, but an array of method-carrying objects would
		// silently drop their methods — reject so the whole binding stays a
		// clean rejection rather than half-working.
		if f.Ty.IsArray() {
			el := f.Ty.ElemType
			for el != nil && el.IsArray() {
				el = el.ElemType
			}
			if el != nil && el.IsObject() && !e.dynWidenable(*el, seen) {
				return false
			}
		}
	}
	return true
}

// emitStaticArrayToDynarr realizes a statically-typed array value as a fresh D1
// dynamic array (tag 11), boxing each element, so dynamic index/length/push
// (`a.tags[0]`, `a.tags.length`) work through the D1 path once the array lives
// inside a widened bag. Used only for fresh-allocation widening, where the
// source array has no external alias, so the element-by-element copy preserves
// observable semantics. An object element that is itself widenable recurses to a
// nested bag so `a.items[0].x` reaches; other elements box normally. `arr` is
// an array Value (header pointer).
func (e *Emitter) emitStaticArrayToDynarr(arr Value) (Value, error) {
	e.ensureDynArr()
	if arr.Ty.ElemType == nil {
		return Value{}, fmt.Errorf("internal: emitStaticArrayToDynarr on non-array type %s", arr.Ty.IR)
	}
	elemTy := *arr.Ty.ElemType
	data := e.freshReg()
	length := e.freshReg()
	data = e.arrayData(arr)
	length = e.arrayLen(arr)
	darr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynarr_new(i64 %s)", darr, length))

	iptr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", iptr))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", iptr))
	condL := e.freshLabel("dynarr.cvt.cond")
	bodyL := e.freshLabel("dynarr.cvt.body")
	endL := e.freshLabel("dynarr.cvt.end")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, iptr))
	cmp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", cmp, i, length))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cmp, bodyL, endL))
	e.emitLabel(bodyL)
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", ep, elemTy.IR, data, i))
	var boxed Value
	var err error
	switch {
	case elemTy.IsArray():
		boxed, err = e.emitStaticArrayToDynarr(e.loadArraySlotAggregate(ep, elemTy))
	case elemTy.IsObject() && e.dynWidenable(elemTy, map[string]bool{}):
		load := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", load, ep))
		boxed, err = e.emitStaticObjToBag(Value{Ref: load, Ty: elemTy})
	default:
		load := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", load, elemTy.IR, ep, elemTy.Align()))
		boxed, err = e.emitBoxValue(Value{Ref: load, Ty: elemTy})
	}
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", darr, boxed.Ref))
	inext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", inext, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", inext, iptr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(endL)
	return Value{Ref: e.emitNbTagPtr(darr, kmlTagDynArray), Ty: TypeAny}, nil
}

// emitStaticObjToBag realizes a statically-typed object value `v` as a fresh D1
// dynamic-object bag (tag 10), field-by-field over its compile-time visible
// shape (TDD-00155 Stage 6, allocation-site widening). Each field is boxed into
// the bag: scalars/functions through emitBoxValue, arrays through the live
// header-pointer slot, and a nested static-object field recurses into its own
// nested bag so deep dynamic access (`a.inner.x`) works. The result is a
// tag-10 `any` Value. A nullable object whose pointer is null at runtime widens
// to the `null` sentinel rather than an empty bag, matching `x === null`.
//
// This is used only where widening is faithful — a fresh allocation flowing
// straight into a dynamic position, or a binding proved to have a single
// dynamic-compatible representation — never as a box-time copy of a value that
// still lives on as a static struct (that would break reference identity).
func (e *Emitter) emitStaticObjToBag(v Value) (Value, error) {
	e.ensureDynObj()
	if !v.Ty.IsObject() {
		return Value{}, fmt.Errorf("internal: emitStaticObjToBag on non-object type %s", v.Ty.IR)
	}
	if hostCellObject(v.Ty) {
		// A host object (a Response, a RegExp) has no user fields to copy:
		// it stays itself, boxed by its header.
		return e.emitBoxHost(v), nil
	}

	build := func() (string, error) {
		bag := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
		// A class instance keeps its instanceof identity across the widening: record
		// the class's TagID in the bag so `x instanceof C` still answers correctly
		// once x has been widened into `any` (ADR-00997). A plain object literal /
		// interface shape has no class tag — the bag's default 0 means "no class".
		if v.Ty.IsClass() {
			if info, ok := e.classes[v.Ty.ClassName]; ok {
				e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set_classtag(ptr %s, i64 %d)", bag, info.TagID))
			}
		}
		structIR := v.Ty.StructIR()
		for _, f := range v.Ty.SpreadFields() {
			idx, _, _ := v.Ty.FieldIndex(f.Name)
			gep := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, v.Ref, idx))
			var boxed Value
			var err error
			switch {
			case f.Ty.IsArray():
				// Convert to a D1 dynamic array so dynamic index/length/push work
				// on the field through the bag (a fresh allocation has no alias).
				boxed, err = e.emitStaticArrayToDynarr(e.loadArrayFieldValue(gep, f.Ty))
			case f.Ty.IsObject():
				// Recurse so nested static objects become nested bags; a null
				// nested pointer widens to the null sentinel inside the recursion.
				loadReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", loadReg, gep))
				boxed, err = e.emitStaticObjToBag(Value{Ref: loadReg, Ty: f.Ty})
			default:
				loadReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, StructFieldIR(f.Ty), gep, f.Ty.Align()))
				boxed, err = e.emitBoxValue(Value{Ref: loadReg, Ty: f.Ty})
			}
			if err != nil {
				return "", err
			}
			e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", bag, e.fieldPropertyKey(f.Name), boxed.Ref))
		}
		return e.emitNbTagPtr(bag, kmlTagDynObject), nil
	}

	if !v.Ty.Nullable() {
		tagged, err := build()
		if err != nil {
			return Value{}, err
		}
		return Value{Ref: tagged, Ty: TypeAny}, nil
	}

	// Nullable object: guard the null pointer to the null sentinel. build() may
	// span several blocks (nested nullable fields recurse), so a fixed trailing
	// label pins the phi predecessor rather than guessing the current block.
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, v.Ref))
	nullL := e.freshLabel("widen.null")
	bagL := e.freshLabel("widen.bag")
	bagEndL := e.freshLabel("widen.bagend")
	joinL := e.freshLabel("widen.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, bagL))
	e.emitLabel(bagL)
	tagged, err := build()
	if err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", bagEndL))
	e.emitLabel(bagEndL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(nullL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi i64 [ %s, %%%s ], [ %d, %%%s ]", r, tagged, bagEndL, nbNull, nullL))
	return Value{Ref: r, Ty: TypeAny}, nil
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
			if sv.Ty.IsArray() || sv.Ty.IsFunc() || (isSyntheticObjLitClass(sv.Ty) && !sv.Ty.Nullable()) || !sv.Ty.IsObject() && !sv.Ty.IsDynamic() {
				// An array's indices and added properties, a function's
				// own properties, an object literal's own accessors' values,
				// a primitive's (a string's indices; nothing for the rest):
				// read through its box.
				if sv, err = e.emitBoxValue(sv); err != nil {
					return Value{}, err
				}
			}
			if sv.Ty.IsDynamic() {
				tag, payload := e.emitUnboxTagPayload(sv)
				matchL, nextL := e.emitTagCheck(tag, kmlTagDynObject, "dynlit.spread")
				e.emitLabel(matchL)
				src := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", src, payload))
				e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_merge(ptr %s, ptr %s)", bag, src))
				e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
				e.emitLabel(nextL)
				// A static object held in the value: its own enumerable
				// fields, through its layout (TDD-00230 phase 5).
				objL, restL := e.emitTagCheck(tag, kmlTagObject, "dynlit.spreadobj")
				e.emitLabel(objL)
				e.ensureShapeSpread()
				e.emitInstr(fmt.Sprintf("call void @__kml_shape_spread(ptr %s, ptr %s)", bag, e.emitIntToPtr(payload)))
				doneL := e.freshLabel("dynlit.spreaddone")
				e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
				// Anything else (an array, a string, a function) spreads
				// its own enumerable properties, read at run time.
				e.emitLabel(restL)
				isDyn := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isDyn, tag, kmlTagDynObject))
				otherL := e.freshLabel("dynlit.spreadother")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDyn, doneL, otherL))
				e.emitLabel(otherL)
				if err := e.emitDynCopyOwnProps(Value{Ref: e.emitNbTagPtr(bag, kmlTagDynObject), Ty: TypeAny}, sv, spread.GetPos()); err != nil {
					return Value{}, err
				}
				e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
				e.emitLabel(doneL)
				continue
			}
			if !sv.Ty.IsObject() {
				return Value{}, fmt.Errorf("%d:%d: spread in an object literal requires an object value", spread.GetPos().Line, spread.GetPos().Col)
			}
			// `{ ...undefined }` copies nothing.
			skipL := ""
			if sv.Ty.Nullable() || sv.Ty.IsNull() {
				isNull := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, sv.Ref))
				copyL := e.freshLabel("dynlit.spread.copy")
				skipL = e.freshLabel("dynlit.spread.skip")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, skipL, copyL))
				e.emitLabel(copyL)
			}
			// An object of another layout behind a structural type spreads
			// its own fields, through its layout (emit_record_view.go).
			ownL := ""
			if isRecordView(sv.Ty.withoutNullable()) {
				foreign := e.emitRecordForeign(sv.Ref, sv.Ty.withoutNullable())
				forL := e.freshLabel("dynlit.spread.foreign")
				ownL = e.freshLabel("dynlit.spread.own")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", foreign, forL, ownL))
				e.emitLabel(forL)
				e.ensureShapeSpread()
				e.emitInstr(fmt.Sprintf("call void @__kml_shape_spread(ptr %s, ptr %s)", bag, sv.Ref))
				if skipL == "" {
					skipL = e.freshLabel("dynlit.spread.skip")
				}
				e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
				e.emitLabel(ownL)
			}
			for _, f := range sv.Ty.SpreadFields() {
				// An absent optional field (`a?: T`) is no key of the copy.
				present, fieldForBox := e.emitFieldPresent(sv.Ref, sv.Ty.withoutNullable(), f)
				contL := ""
				if present != "true" {
					setL := e.freshLabel("dynlit.spread.set")
					contL = e.freshLabel("dynlit.spread.next")
					e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", present, setL, contL))
					e.emitLabel(setL)
				}
				boxed, err := e.emitBoxValue(fieldForBox)
				if err != nil {
					return Value{}, err
				}
				e.emitInstr(fmt.Sprintf("call void @__kml_dynobj_set(ptr %s, ptr %s, i64 %s)", bag, e.fieldPropertyKey(f.Name), boxed.Ref))
				if contL != "" {
					e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
					e.emitLabel(contL)
				}
			}
			if skipL != "" {
				e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
				e.emitLabel(skipL)
			}
			continue
		}

		// `{ __proto__: p }` in a literal sets the prototype (JS special
		// form; a computed `["__proto__"]` key stays a plain property, as in
		// JS; so does the shorthand `{ __proto__ }`). Non-object/non-null
		// values are silently ignored, per spec.
		if prop.KeyExpr == nil && prop.Key == "__proto__" && !prop.Shorthand {
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
		} else if nested, ok := prop.Value.(*ast.ArrayLiteral); ok {
			// A nested array literal must recurse into a dynamic array (tag 11),
			// symmetric to emitDynArrLiteral's handling of nested object/array
			// literals — otherwise it is built as a static array boxed as any,
			// which neither dynamic member access nor JSON.stringify can walk.
			nv, err := e.emitDynArrLiteral(nested)
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

// emitDictToBag snapshots a string-keyed dictionary (an index-signature
// object) into a dynamic bag, keys in insertion order and each value boxed:
// what inspect and JSON.stringify render, with an object's key formatting.
func (e *Emitter) emitDictToBag(v Value) (Value, error) {
	e.ensureDynObj()
	e.ensureMapStrHelpers()
	valTy := TypePtr
	if v.Ty.MapVal != nil {
		valTy = *v.Ty.MapVal
	}
	bag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dynobj_new()", bag))
	// A null-prototype dictionary (Object.create(null)) renders as one.
	flagsP, flags, nullProto := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 56", flagsP, v.Ref))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", flags, flagsP))
	e.emitInstr(fmt.Sprintf("%s = trunc i64 %s to i1", nullProto, flags))
	npL, npDone := e.freshLabel("dict2bag.nullproto"), e.freshLabel("dict2bag.proto")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullProto, npL, npDone))
	e.emitLabel(npL)
	e.emitInstr(fmt.Sprintf("call i1 @__kml_dynobj_set_proto(ptr %s, ptr null)", bag))
	e.emitTerminator(fmt.Sprintf("br label %%%s", npDone))
	e.emitLabel(npDone)
	keys := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", keys, v.Ref))
	kPtr, kLen := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", kPtr, keys))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", kLen, keys))
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	condL, bodyL, doneL := e.freshLabel("dict2bag.cond"), e.freshLabel("dict2bag.body"), e.freshLabel("dict2bag.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, kLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	kg, key := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", kg, kPtr, i))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", key, kg))
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, v.Ref, key))
	var boxed Value
	if valTy.IsDynamic() {
		boxed = Value{Ref: raw, Ty: TypeAny}
	} else {
		var err error
		boxed, err = e.emitBoxValue(e.mapValFromI64(raw, valTy))
		if err != nil {
			return Value{}, err
		}
	}
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_dynobj_setv(ptr %s, ptr %s, i64 %s)", st, bag, key, boxed.Ref))
	nx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return Value{Ref: e.emitNbTagPtr(bag, kmlTagDynObject), Ty: TypeAny}, nil
}

// errorGetKeySentinel is the property name the runtime-key helper passes for
// a key that is none of an Error's named fields: it matches no named arm.
const errorGetKeySentinel = "\x01"

// ensureErrorGetKey defines `i64 __kml_error_get_key(i64 box, ptr key)`:
// a boxed Error's property by run-time key, through the same reads a named
// access (`err.message`) emits.
func (e *Emitter) ensureErrorGetKey(pos ast.Pos) {
	if e.errorGetKeyDone {
		return
	}
	e.errorGetKeyDone = true
	e.ensureStrcmp()
	names := []string{"message", "name", "stack", "constructor"}
	for _, f := range errorObjType.Fields {
		if f.Name != ClassTagField && f.Name != errorExtraField && f.Name != "message" && f.Name != "name" && !strings.HasPrefix(f.Name, "__kml_") {
			names = append(names, f.Name)
		}
	}
	restore := e.beginDetachedFunc()
	box := Value{Ref: "%box", Ty: TypeAny}
	for _, n := range names {
		c, eq := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %%key, ptr %s)", c, e.internString(n)))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eq, c))
		hit, next := e.freshLabel("errkey.hit"), e.freshLabel("errkey.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", eq, hit, next))
		e.emitLabel(hit)
		v, err := e.emitDynAnyMemberGetNamed(box, e.internString(n), n, pos)
		if err != nil || e.blockDone {
			if !e.blockDone {
				e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
			}
		} else {
			e.emitTerminator(fmt.Sprintf("ret i64 %s", v.Ref))
		}
		e.emitLabel(next)
	}
	v, err := e.emitDynAnyMemberGetNamed(box, "%key", errorGetKeySentinel, pos)
	if err != nil || e.blockDone {
		if !e.blockDone {
			e.emitTerminator(fmt.Sprintf("ret i64 %d", nbUndefined))
		}
	} else {
		e.emitTerminator(fmt.Sprintf("ret i64 %s", v.Ref))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("internal i64 @__kml_error_get_key(i64 %box, ptr %key)", body)
}

// emitCtorFallback is v, or — where v is undefined and when holds — the
// built-in constructor reference named name: an own-less `constructor` read.
func (e *Emitter) emitCtorFallback(v, when, name string) string {
	undef, use, r := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", undef, v, nbUndefined))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", use, undef, when))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", r, use, e.emitNbTagPtr(name, kmlTagFuncRef), v))
	return r
}

// isCanonicalIndex reports whether a property name is an array index's
// canonical form ("0", "17"; not "01" or "+1").
func isCanonicalIndex(s string) bool {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// emitDynSetThrow throws the TypeError of a write or delete the dynamic
// object bag rejected with code (__kml_dyn_set_throw), ending the block.
func (e *Emitter) emitDynSetThrow(bag, keyRef, code string) {
	e.ensureDynJSONC()
	e.declareFn("__kml_dyn_set_throw", "declare void @__kml_dyn_set_throw(ptr, ptr, i64)")
	e.emitInstr(fmt.Sprintf("call void @__kml_dyn_set_throw(ptr %s, ptr %s, i64 %s)", bag, keyRef, code))
	e.emitTerminator("unreachable")
}

// emitStaticSetThrow is emitDynSetThrow for a static object obj.
func (e *Emitter) emitStaticSetThrow(obj, keyRef string, code int) {
	e.ensureDynJSONC()
	e.declareFn("__kml_static_set_throw", "declare void @__kml_static_set_throw(ptr, ptr, i64)")
	e.emitInstr(fmt.Sprintf("call void @__kml_static_set_throw(ptr %s, ptr %s, i64 %d)", obj, keyRef, code))
	e.emitTerminator("unreachable")
}

// pushSpreadIntoDynArr appends `...src`'s elements to the dynamic array arr,
// each boxed: src is collected as `[...src]` (any iterable), then walked.
func (e *Emitter) pushSpreadIntoDynArr(arr string, sp *ast.SpreadElement, pos ast.Pos) error {
	col, err := e.emitExpr(ast.NewArrayLiteral([]ast.Expression{sp}, pos))
	if err != nil {
		return err
	}
	if !col.Ty.IsArray() || col.Ty.ElemType == nil {
		return fmt.Errorf("%d:%d: spread operand is not iterable", pos.Line, pos.Col)
	}
	ptr, length, elemTy, err := e.resolveArrayForHOF(e.bindValue(col, pos), pos)
	if err != nil {
		return err
	}
	idx := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
	condL, bodyL, doneL := e.freshLabel("dynarr.spread.cond"), e.freshLabel("dynarr.spread.body"), e.freshLabel("dynarr.spread.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	i := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
	more := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", more, i, length))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", more, bodyL, doneL))
	e.emitLabel(bodyL)
	ep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", ep, elemTy.IR, ptr, i))
	ev := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", ev, elemTy.IR, ep, elemTy.Align()))
	boxed, err := e.emitBoxValue(Value{Ref: ev, Ty: elemTy})
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_dynarr_push(ptr %s, i64 %s)", arr, boxed.Ref))
	i2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i2, i))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", i2, idx))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(doneL)
	return nil
}

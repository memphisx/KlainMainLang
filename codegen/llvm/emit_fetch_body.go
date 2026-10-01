package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

var ast0 = ast.Pos{}

// emitFetchBodyFromBox is a fetch body's bytes, byte length and implied
// Content-Type (a null ptr for none) from its boxed value, as undici's
// extractBody has them: a string (text/plain;charset=UTF-8), a
// URLSearchParams (its serialization, form-urlencoded), a Blob (its bytes,
// its own type), an ArrayBuffer or a TypedArray/Buffer/DataView (its raw
// bytes); null/undefined is no body (a null data pointer, length -1); any
// other value is its String() (text/plain).
func (e *Emitter) emitFetchBodyFromBox(box Value) (data, length, ct string) {
	e.ensureStrHeaderRuntime()
	textCT := e.internString("text/plain;charset=UTF-8")
	dSlot, lSlot, cSlot := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", dSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", cSlot))
	doneL := e.freshLabel("fbody.done")
	set := func(d, l, c string) {
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", d, dSlot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l, lSlot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", c, cSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	tag, pay := e.emitUnboxTagPayload(box)

	// null / undefined: no body.
	isN, isU, none := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isN, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isU, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", none, isN, isU))
	noneL, next1 := e.freshLabel("fbody.none"), e.freshLabel("fbody.n1")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", none, noneL, next1))
	e.emitLabel(noneL)
	set("null", "-1", "null")
	e.emitLabel(next1)

	// A string.
	strL, next2 := e.emitTagCheck(tag, kmlTagString, "fbody.str")
	e.emitLabel(strL)
	sp := e.emitIntToPtr(pay)
	sl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sl, sp))
	set(sp, sl, textCT)
	e.emitLabel(next2)

	// A typed array / Buffer: its elements' bytes. A plain array is not a
	// body; it falls through to String().
	arrL, next3 := e.freshLabel("fbody.arr"), e.freshLabel("fbody.n3")
	notPlain := e.freshReg()
	isPlainOrNot := e.emitBoxIsTypedArray(box, anyArrayPlain)
	isArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isArr, tag, kmlTagArray))
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notPlain, isPlainOrNot))
	typedArr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", typedArr, isArr, notPlain))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", typedArr, arrL, next3))
	e.emitLabel(arrL)
	{
		bx := e.emitIntToPtr(pay)
		hg, kg := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", hg, anyArrayBoxTy, bx))
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", kg, anyArrayBoxTy, bx))
		hdr, kind := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, hg))
		e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", kind, kg))
		agg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load {ptr, i64}, ptr %s, align 8", agg, hdr))
		d, n := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", d, agg))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", n, agg))
		// Element width from the kind byte (arrayElemKind).
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_elem_kind_width(i8 %s)", w, kind))
		e.ensureElemKindWidth()
		bytes := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %s", bytes, n, w))
		set(d, bytes, "null")
	}
	e.emitLabel(next3)

	// A host object: Blob, ArrayBuffer, URLSearchParams.
	objL, strDefL := e.emitTagCheck(tag, kmlTagObject, "fbody.obj")
	e.emitLabel(objL)
	isBlob := e.emitDynHostInstanceOf(box, "Blob")
	blobL, nb := e.freshLabel("fbody.blob"), e.freshLabel("fbody.nb")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBlob.Ref, blobL, nb))
	e.emitLabel(blobL)
	{
		b := e.emitUnboxHost(box, BlobType())
		size, bd := e.emitBlobSizeData(b.Ref)
		tslot, tv := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", tslot, blobStructIR, b.Ref))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", tv, tslot))
		// An empty type is no Content-Type.
		tl, empty, c := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", tl, tv))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", empty, tl))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", c, empty, tv))
		set(bd, size, c)
	}
	e.emitLabel(nb)
	isAB := e.emitDynHostInstanceOf(box, "ArrayBuffer")
	abL, nab := e.freshLabel("fbody.ab"), e.freshLabel("fbody.nab")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAB.Ref, abL, nab))
	e.emitLabel(abL)
	{
		ab := e.emitUnboxHost(box, ArrayBufferType())
		lg, dg, l, d := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 0", lg, ab.Ref))
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dg, ab.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", l, lg))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", d, dg))
		set(d, l, "null")
	}
	e.emitLabel(nab)
	isUSP := e.emitDynHostInstanceOf(box, "URLSearchParams")
	uspL := e.freshLabel("fbody.usp")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isUSP.Ref, uspL, strDefL))
	e.emitLabel(uspL)
	{
		e.ensureURLSearchParams()
		u := e.emitUnboxHost(box, URLSearchParamsType())
		s := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_to_string(ptr %s)", s, u.Ref))
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", l, s))
		set(s, l, e.internString("application/x-www-form-urlencoded;charset=UTF-8"))
	}

	// Anything else: its String().
	e.emitLabel(strDefL)
	sv, ok := e.emitAnyIntoString(box, TypePtr)
	if !ok {
		sv = e.coerce(box, TypePtr)
	}
	svl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", svl, sv.Ref))
	set(sv.Ref, svl, textCT)

	e.emitLabel(doneL)
	data, length, ct = e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", data, dSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", length, lSlot))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ct, cSlot))
	return data, length, ct
}

// ensureElemKindWidth emits @__kml_elem_kind_width(kind): the byte width of
// an array box's element kind (arrayElemKind's codes).
func (e *Emitter) ensureElemKindWidth() {
	if e.usedElemKindWidth {
		return
	}
	e.usedElemKindWidth = true
	e.emitGlobal(`define internal i64 @__kml_elem_kind_width(i8 %k) {
entry:
  switch i8 %k, label %w8 [
    i8 1, label %w4
    i8 4, label %w4
    i8 5, label %w4
    i8 6, label %w2
    i8 7, label %w2
    i8 8, label %w1
    i8 9, label %w1
  ]
w1:
  ret i64 1
w2:
  ret i64 2
w4:
  ret i64 4
w8:
  ret i64 8
}`)
}

// emitHeadersFromBox is a header map (lowercased names) from a boxed
// HeadersInit, as `new Headers(init)` reads it: null/undefined is empty; a
// Headers is copied; an array is [name, value] pairs, appended; any other
// object is a record of its own string-keyed entries. A primitive is
// Node's TypeError.
func (e *Emitter) emitHeadersFromBox(box Value) Value {
	e.ensureMapStrHelpers()
	e.ensureStringToLower()
	strMap := MapType(TypePtr, TypePtr)
	m := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", m))
	outSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", outSlot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", m, outSlot))
	doneL := e.freshLabel("hinit.done")
	tag, _ := e.emitUnboxTagPayload(box)

	// append name/value into m: joined with ", " when the name is set.
	appendHdr := func(name, value string) {
		low := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tolower(ptr %s)", low, name))
		has := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", has, m, low))
		joinL, setL, nextL := e.freshLabel("hinit.join"), e.freshLabel("hinit.set"), e.freshLabel("hinit.next")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, joinL, setL))
		e.emitLabel(joinL)
		old := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", old, m, low))
		op := e.emitIntToPtr(old)
		j1, _ := e.emitStringConcat(Value{Ref: op, Ty: TypePtr}, Value{Ref: e.internString(", "), Ty: TypePtr})
		j2, _ := e.emitStringConcat(j1, Value{Ref: value, Ty: TypePtr})
		ji := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", ji, j2.Ref))
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", m, low, ji))
		e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
		e.emitLabel(setL)
		vi := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", vi, value))
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", m, low, vi))
		e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
		e.emitLabel(nextL)
	}
	// loop i over [0, n): body(i).
	loop := func(n string, body func(i string)) {
		idx := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idx))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idx))
		condL, bodyL, endL := e.freshLabel("hinit.cond"), e.freshLabel("hinit.body"), e.freshLabel("hinit.end")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		i, c := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", i, idx))
		e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", c, i, n))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", c, bodyL, endL))
		e.emitLabel(bodyL)
		body(i)
		nx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nx, i))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nx, idx))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(endL)
	}
	str := func(v Value) string {
		s, ok := e.emitAnyIntoString(v, TypePtr)
		if !ok {
			s = e.coerce(v, TypePtr)
		}
		return s.Ref
	}

	// null / undefined: no headers.
	isN, isU, none := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isN, tag, kmlTagNull))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isU, tag, kmlTagUndefined))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", none, isN, isU))
	next1 := e.freshLabel("hinit.n1")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", none, doneL, next1))
	e.emitLabel(next1)

	// A Headers: a copy.
	isH := e.emitDynHostInstanceOf(box, "Headers")
	hL, next2 := e.freshLabel("hinit.headers"), e.freshLabel("hinit.n2")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isH.Ref, hL, next2))
	e.emitLabel(hL)
	{
		h := e.emitUnboxHost(box, HeadersType())
		c, err := e.emitHeadersFromMapValue(Value{Ref: h.Ref, Ty: strMap})
		if err == nil {
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", c.Ref, outSlot))
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(next2)

	// An array: [name, value] pairs.
	isA1, isA2, isArr := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isA1, tag, kmlTagArray))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isA2, tag, kmlTagDynArray))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isArr, isA1, isA2))
	arrL, next3 := e.freshLabel("hinit.arr"), e.freshLabel("hinit.n3")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isArr, arrL, next3))
	e.emitLabel(arrL)
	{
		lv, _ := e.emitDynAnyMemberGetNamed(box, e.internString("length"), "length", ast0)
		n := e.coerce(lv, TypeI64)
		e.ensureSprintf()
		loop(n.Ref, func(i string) {
			key := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 24)", key))
			e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", key, e.internString("%lld"), i))
			e.emitInstr(fmt.Sprintf("call void @__kml_str_finalize(ptr %s)", key))
			pair, _ := e.emitDynAnyMemberGet(box, key, ast0)
			k, _ := e.emitDynAnyMemberGet(pair, e.internString("0"), ast0)
			v, _ := e.emitDynAnyMemberGet(pair, e.internString("1"), ast0)
			appendHdr(str(k), str(v))
		})
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(next3)

	// Any other object: a record of its own entries.
	isO1, isO2, isObj := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isO1, tag, kmlTagObject))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isO2, tag, kmlTagDynObject))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isObj, isO1, isO2))
	objL, badL := e.freshLabel("hinit.obj"), e.freshLabel("hinit.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isObj, objL, badL))
	e.emitLabel(objL)
	{
		keys, _ := e.emitDynAnyKeys(box, ast0)
		kd, kn := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 0", kd, keys.Ref))
		e.emitInstr(fmt.Sprintf("%s = extractvalue { ptr, i64 } %s, 1", kn, keys.Ref))
		loop(kn, func(i string) {
			kg, k := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", kg, kd, i))
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", k, kg))
			v, _ := e.emitDynAnyMemberGet(box, k, ast0)
			appendHdr(k, str(v))
		})
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	}
	e.emitLabel(badL)
	e.emitThrowTypeError("Failed to construct 'Headers': The provided value is not of type '(record<ByteString, ByteString> or sequence<sequence<ByteString>>)'.")

	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", out, outSlot))
	return Value{Ref: out, Ty: HeadersType()}
}

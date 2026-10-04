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
	// A Blob (the global module's class, linked into any program naming
	// fetch): its bytes, its type the Content-Type.
	if blobCls, ok := e.globalClass("Blob"); ok {
		isBlob, err := e.emitAnyInstanceOfClass(box, blobCls.ClassName)
		if err == nil {
			blobL, nb := e.freshLabel("fbody.blob"), e.freshLabel("fbody.nb")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBlob.Ref, blobL, nb))
			e.emitLabel(blobL)
			bd, size, tv, err := e.emitBlobBytes(e.coerce(box, blobCls), ast.Pos{})
			if err != nil {
				bd, size, tv = "null", "0", "null"
			}
			// An empty type is no Content-Type.
			tl, empty, c := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", tl, tv))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", empty, tl))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", c, empty, tv))
			set(bd, size, c)
			e.emitLabel(nb)
		}
	}
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
	// A URLSearchParams (the global module's class): its serialization,
	// form-urlencoded.
	if usp, ok := e.globalClass("URLSearchParams"); ok {
		isUSP, err := e.emitAnyInstanceOfClass(box, usp.ClassName)
		if err != nil {
			panic(err)
		}
		uspL, nusp := e.freshLabel("fbody.usp"), e.freshLabel("fbody.nusp")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isUSP.Ref, uspL, nusp))
		e.emitLabel(uspL)
		obj := e.coerce(box, usp)
		sv, err := e.emitClassCall(usp, obj, "toString", nil, ast.Pos{}, false)
		if err != nil {
			panic(err)
		}
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", l, sv.Ref))
		set(sv.Ref, l, e.internString("application/x-www-form-urlencoded;charset=UTF-8"))
		e.emitLabel(nusp)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", strDefL))

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

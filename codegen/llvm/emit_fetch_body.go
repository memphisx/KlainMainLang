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
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", dSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", cSlot))
	doneL := e.freshLabel("fbody.done")
	set := func(d, l, c string) {
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", d, dSlot))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", l, lSlot))
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", c, cSlot))
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
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len("+irPtr+" %s)", sl, sp))
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
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", hg, anyArrayBoxTy, bx))
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 1", kg, anyArrayBoxTy, bx))
		hdr, kind := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", hdr, hg))
		e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" %s, align 1", kind, kg))
		agg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load {"+irPtr+", i64}, "+irPtr+" %s, align 8", agg, hdr))
		d, n := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {"+irPtr+", i64} %s, 0", d, agg))
		e.emitInstr(fmt.Sprintf("%s = extractvalue {"+irPtr+", i64} %s, 1", n, agg))
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
	// A FormData (linked when the program names it): its
	// multipart/form-data encoding, a Blob whose type carries the boundary.
	if fdCls, ok := e.globalClass("FormData"); ok {
		if blobCls, ok := e.globalClass("Blob"); ok {
			isFD, err := e.emitAnyInstanceOfClass(box, fdCls.ClassName)
			if err == nil {
				fdL, nfd := e.freshLabel("fbody.fd"), e.freshLabel("fbody.nfd")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFD.Ref, fdL, nfd))
				e.emitLabel(fdL)
				bd, size, tv := "null", "0", "null"
				const tmp = "__kml_body_formdata"
				e.pushScope()
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
				fv := e.convert(box, fdCls)
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", fv.Ref, slot))
				e.define(tmp, Symbol{Ptr: slot, Ty: fdCls})
				bv, err := e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(tmp, ast.Pos{}), "kmlBlob", ast.Pos{}), nil, ast.Pos{}))
				e.popScope()
				if err == nil {
					if d, n, t, berr := e.emitBlobBytes(e.convert(bv, blobCls), ast.Pos{}); berr == nil {
						bd, size, tv = d, n, t
					}
				}
				set(bd, size, tv)
				e.emitLabel(nfd)
			}
		}
	}
	// A Blob (the global module's class, linked into any program naming
	// fetch): its bytes, its type the Content-Type.
	if blobCls, ok := e.globalClass("Blob"); ok {
		isBlob, err := e.emitAnyInstanceOfClass(box, blobCls.ClassName)
		if err == nil {
			blobL, nb := e.freshLabel("fbody.blob"), e.freshLabel("fbody.nb")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isBlob.Ref, blobL, nb))
			e.emitLabel(blobL)
			bd, size, tv, err := e.emitBlobBytes(e.convert(box, blobCls), ast.Pos{})
			if err != nil {
				bd, size, tv = "null", "0", "null"
			}
			// An empty type is no Content-Type.
			tl, empty, c := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len("+irPtr+" %s)", tl, tv))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", empty, tl))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" null, "+irPtr+" %s", c, empty, tv))
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
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", lg, ab.Ref))
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", dg, ab.Ref))
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", l, lg))
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", d, dg))
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
		obj := e.convert(box, usp)
		sv, err := e.emitClassCall(usp, obj, "toString", nil, ast.Pos{}, false)
		if err != nil {
			panic(err)
		}
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len("+irPtr+" %s)", l, sv.Ref))
		set(sv.Ref, l, e.internString("application/x-www-form-urlencoded;charset=UTF-8"))
		e.emitLabel(nusp)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", strDefL))

	// Anything else: its String().
	e.emitLabel(strDefL)
	sv, ok := e.emitAnyIntoString(box, TypePtr)
	if !ok {
		sv = e.convert(box, TypePtr)
	}
	svl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len("+irPtr+" %s)", svl, sv.Ref))
	set(sv.Ref, svl, textCT)

	e.emitLabel(doneL)
	data, length, ct = e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", data, dSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", length, lSlot))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", ct, cSlot))
	return data, length, ct
}

// ensureElemKindWidth emits @__kml_elem_kind_width(kind): the byte width of
// an array box's element kind (arrayElemKind's codes).
func (e *Emitter) ensureElemKindWidth() {
	if e.usedElemKindWidth {
		return
	}
	e.usedElemKindWidth = true
	restore := e.beginDetachedFunc()
	w1, w2, w4, w8 := e.freshLabel("w1"), e.freshLabel("w2"), e.freshLabel("w4"), e.freshLabel("w8")
	e.emitTerminator(fmt.Sprintf("switch i8 %%k, label %%%s [ i8 1, label %%%s i8 4, label %%%s i8 5, label %%%s i8 6, label %%%s i8 7, label %%%s i8 8, label %%%s i8 9, label %%%s ]",
		w8, w4, w4, w4, w2, w2, w1, w1))
	for _, w := range []struct {
		l string
		n int
	}{{w1, 1}, {w2, 2}, {w4, 4}, {w8, 8}} {
		e.emitLabel(w.l)
		e.emitTerminator(fmt.Sprintf("ret i64 %d", w.n))
	}
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("internal i64 @__kml_elem_kind_width(i8 %k)", body)
}

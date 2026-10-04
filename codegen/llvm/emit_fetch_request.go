// emit_fetch_request.go — `new Request(url)`/`new Request(url, init)`
// (TDD-00040): a heap object (url/method/headers readable as fields) with
// Fetch's Body members — `.body`, `bodyUsed`, text()/json()/arrayBuffer() —
// served by Response's own (emitResponseCall, emitResponseBodyStream),
// reusing fetch(url, init)'s own existing init-field-extraction pattern
// (loadFieldValue, emit_fetch.go) almost verbatim.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitNewRequestExpression builds a FetchRequestType() object: method
// defaults to "GET", headers to an empty Headers, body to null — the same
// defaults real fetch(url) has today when no init is given.
func (e *Emitter) emitNewRequestExpression(ex *ast.NewRequestExpression) (Value, error) {
	urlVal, err := e.emitExpr(stringifiedInput(ex.URL))
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime()
	// The init's parts, in slots: an optional init may be absent at run time.
	mSlot, hSlot, bSlot, lSlot, cSlot := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	for _, sl := range []string{mSlot, hSlot, bSlot, cSlot} {
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", sl))
	}
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lSlot))
	emptyHeaders, err := e.emitNewHeaders(nil, ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	headersTy := emptyHeaders.Ty
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString("GET"), mSlot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", emptyHeaders.Ref, hSlot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", bSlot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", cSlot))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", lSlot))

	if ex.Init != nil {
		initVal, err := e.emitExpr(ex.Init)
		if err != nil {
			return Value{}, err
		}
		dyn := isUnconstrainedDynamic(initVal.Ty)
		if !initVal.Ty.IsObject && !dyn {
			return Value{}, fmt.Errorf("%d:%d: Request's second argument must be an object with an optional method/headers/body field", ex.GetPos().Line, ex.GetPos().Col)
		}
		doneInitL := e.freshLabel("req.initdone")
		if dyn {
			// An init held in `any`: undefined or null is none; otherwise
			// each member is read at run time.
			pos := ex.GetPos()
			dynGet := func(name string) Value {
				v, _ := e.emitDynAnyMemberGetNamed(initVal, e.internString(name), name, pos)
				return v
			}
			undef, null, absent := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", undef, initVal.Ref, nbUndefined))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", null, initVal.Ref, nbNull))
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absent, undef, null))
			haveL := e.freshLabel("req.dyninit")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", absent, doneInitL, haveL))
			e.emitLabel(haveL)
			mv := dynGet("method")
			ms, _ := e.emitAnyIntoString(mv, TypePtr)
			isUndef, sel := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, mv.Ref, nbUndefined))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, isUndef, e.internString("GET"), ms.Ref))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sel, mSlot))
			hv := dynGet("headers")
			h, err := e.emitNewHeaders(&hv, pos)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", h.Ref, hSlot))
			d, l, c := e.emitFetchBodyFromBox(dynGet("body"))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", d, bSlot))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l, lSlot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", c, cSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneInitL))
			e.emitLabel(doneInitL)
		} else if initVal.Ty.Nullable {
			haveL := e.freshLabel("req.init")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(initVal.Ref), doneInitL, haveL))
			e.emitLabel(haveL)
			nn := initVal.Ty
			nn.Nullable, nn.IsUndefined = false, false
			initVal = Value{Ref: initVal.Ref, Ty: nn}
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("method"); ok && !dyn {
			mv := e.loadFieldValue(initVal, idx, fieldTy)
			switch {
			case isStringTy(mv.Ty) && !mv.Ty.IsObject && !mv.Ty.IsArray:
			case mv.Ty.IsDynamic:
				mv, _ = e.emitAnyIntoString(mv, TypePtr)
			default:
				return Value{}, fmt.Errorf("%d:%d: Request's init.method must be a string", ex.GetPos().Line, ex.GetPos().Col)
			}
			// An absent member keeps GET.
			isNull, sel := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, mv.Ref))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, isNull, e.internString("GET"), mv.Ref))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sel, mSlot))
		}
		if _, _, ok := initVal.Ty.FieldIndex("headers"); ok && !dyn {
			hv, err := e.emitHeadersFromInit(initVal, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", hv.Ref, hSlot))
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("body"); ok && !dyn {
			bv := e.loadFieldValue(initVal, idx, fieldTy)
			box, berr := e.emitBoxValue(bv)
			if berr != nil {
				return Value{}, fmt.Errorf("%d:%d: Request's init.body: %v", ex.GetPos().Line, ex.GetPos().Col, berr)
			}
			d, l, c := e.emitFetchBodyFromBox(box)
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", d, bSlot))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", l, lSlot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", c, cSlot))
		}
		if !dyn {
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneInitL))
			e.emitLabel(doneInitL)
		}
	}
	methodRaw, headersRef, bodyData, bodyLen, ctRef := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", methodRaw, mSlot))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", headersRef, hSlot))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bodyData, bSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", bodyLen, lSlot))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ctRef, cSlot))
	headersVal := Value{Ref: headersRef, Ty: headersTy}

	// Fetch normalizes the standard methods' case ("post" is "POST").
	e.ensureStrcasecmp()
	m := methodRaw
	for _, std := range []string{"DELETE", "GET", "HEAD", "OPTIONS", "POST", "PUT"} {
		c, eq, sel := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp(ptr %s, ptr %s)", c, methodRaw, e.internString(std)))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eq, c))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, eq, e.internString(std), m))
		m = sel
	}
	methodVal := Value{Ref: m, Ty: TypePtr}

	// A body: a GET or HEAD request has none (Fetch's Request constructor
	// throws); its bytes are copied; its implied Content-Type is set unless
	// the headers have one.
	hasBody := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasBody, bodyData))
	bodySlot, lenSlot, flagSlot := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", bodySlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", flagSlot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", bodySlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lenSlot))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", responseNullBody, flagSlot))
	bodyL, noBodyL := e.freshLabel("req.hasbody"), e.freshLabel("req.nobody")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasBody, bodyL, noBodyL))
	e.emitLabel(bodyL)
	{
		cg, ch := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp(ptr %s, ptr %s)", cg, methodVal.Ref, e.internString("GET")))
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp(ptr %s, ptr %s)", ch, methodVal.Ref, e.internString("HEAD")))
		isGet, isHead, bad := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isGet, cg))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isHead, ch))
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, isGet, isHead))
		badL, okL := e.freshLabel("req.getbody"), e.freshLabel("req.body")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
		e.emitLabel(badL)
		e.emitThrowTypeErrorValue(e.internString("Request with GET/HEAD method cannot have body."))
		e.emitLabel(okL)
		cp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 %s)", cp, bodyLen))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", cp, bodyData, bodyLen))
		end := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", end, cp, bodyLen))
		e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", end))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cp, bodySlot))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bodyLen, lenSlot))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", flagSlot))
		if err := e.emitSetHeaderIfAbsent(headersVal, "content-type", ctRef, ex.GetPos()); err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", noBodyL))
	}
	e.emitLabel(noBodyL)
	bodyRef, lenRef, flags := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", bodyRef, bodySlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenRef, lenSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", flags, flagSlot))
	bodyVal := Value{Ref: bodyRef, Ty: TypePtr}

	ty := FetchRequestType()
	objReg := e.freshReg()
	e.emitObjAllocInto(objReg, ty)
	structIR := ty.StructIR()

	storeField := func(name string, val Value) {
		idx, fieldTy, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, val.Ref, gep, fieldTy.Align()))
	}
	storeField("url", urlVal)
	storeField("method", methodVal)
	storeField("__kml_headers", headersVal)
	storeField("body", bodyVal)
	storeField("bodyLength", Value{Ref: lenRef, Ty: TypeI64})
	storeField("__kml_bodyflags", Value{Ref: flags, Ty: TypeI64})

	return Value{Ref: objReg, Ty: ty}, nil
}

// stringifiedInput is a fetch input as the Fetch standard reads one that is
// not a Request: `String(input)` (a URL by its href).
func stringifiedInput(x ast.Expression) ast.Expression {
	if _, isStr := x.(*ast.StringLiteral); isStr {
		return x
	}
	return stringCall(x, x.GetPos())
}

// emitRequestHeaders is a Request's Headers.
func (e *Emitter) emitRequestHeaders(req Value, pos ast.Pos) (Value, error) {
	cls, err := e.headersClass(pos)
	if err != nil {
		return Value{}, err
	}
	idx, fieldTy, _ := req.Ty.FieldIndex("__kml_headers")
	h := e.loadFieldValue(req, idx, fieldTy)
	return Value{Ref: h.Ref, Ty: cls}, nil
}

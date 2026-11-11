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
	var src *Value // a Request as the input: its parts are the defaults
	var urlVal Value
	var err error
	if t := e.typeOfExpr(ex.URL); t.IsFetchRequest() {
		rv, err := e.emitExpr(ex.URL)
		if err != nil {
			return Value{}, err
		}
		src = &rv
		idx, fieldTy, _ := rv.Ty.FieldIndex("url")
		urlVal = e.loadFieldValue(rv, idx, fieldTy)
	} else {
		urlVal, err = e.emitExpr(stringifiedInput(ex.URL))
		if err != nil {
			return Value{}, err
		}
	}
	urlVal = e.convert(urlVal, TypePtr)

	e.ensureStrHeaderRuntime()
	// The init's parts, in slots: an optional init may be absent at run time.
	mSlot, hSlot, bSlot, lSlot, cSlot := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	for _, sl := range []string{mSlot, hSlot, bSlot, cSlot} {
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", sl))
	}
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lSlot))
	emptyHeaders, err := e.emitNewHeaders(nil, ex.GetPos())
	if err != nil {
		return Value{}, err
	}
	headersTy := emptyHeaders.Ty
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", e.internString("GET"), mSlot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", emptyHeaders.Ref, hSlot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", bSlot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", cSlot))
	e.emitInstr(fmt.Sprintf("store i64 -1, "+irPtr+" %s, align 8", lSlot))

	// The init's other string members and keepalive, at their defaults.
	extraSlots := map[string]string{}
	for _, m := range requestInitStrings {
		sl := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", sl))
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", e.internString(m.dflt), sl))
		extraSlots[m.name] = sl
	}
	sigSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", sigSlot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", sigSlot))
	sigCls, hasSignals := e.abortSignalClass()
	kaSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", kaSlot))
	e.emitInstr(fmt.Sprintf("store i1 false, "+irPtr+" %s, align 1", kaSlot))

	if src != nil {
		// Fetch's Request(input: Request): its method, a copy of its
		// headers, its body and its other members, which the init then
		// overrides.
		field := func(name string) Value {
			idx, fieldTy, _ := src.Ty.FieldIndex(name)
			return e.loadFieldValue(*src, idx, fieldTy)
		}
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", field("method").Ref, mSlot))
		sh, err := e.emitRequestHeaders(*src, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		h, err := e.emitNewHeaders(&sh, ex.GetPos())
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", h.Ref, hSlot))
		body, blen, flags := field("body"), field("bodyLength"), field("__kml_bodyflags")
		if ex.Init == nil {
			// The input's body moves to the new request: one already read
			// cannot.
			used, hasBody, bad := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", used, flags.Ref, responseBodyUsed))
			e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", hasBody, body.Ref))
			usedB := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", usedB, used))
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", bad, usedB, hasBody))
			badL, okL := e.freshLabel("req.srcused"), e.freshLabel("req.srcok")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
			e.emitLabel(badL)
			e.emitThrowTypeErrorValue(e.internString("Cannot construct a Request with a Request object that has already been used."))
			e.emitLabel(okL)
			// The input is now used (when it had a body).
			fIdx, _, _ := src.Ty.FieldIndex("__kml_bodyflags")
			fGep, bit, f2 := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", fGep, src.Ty.StructIR(), src.Ref, fIdx))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %d, i64 0", bit, hasBody, responseBodyUsed))
			e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", f2, flags.Ref, bit))
			e.heapStore("i64", f2, fGep, 8)
		}
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", body.Ref, bSlot))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", blen.Ref, lSlot))
		for _, m := range requestInitStrings {
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", field(m.name).Ref, extraSlots[m.name]))
		}
		e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", field("keepalive").Ref, kaSlot))
	}

	if ex.Init != nil {
		initVal, err := e.emitExpr(ex.Init)
		if err != nil {
			return Value{}, err
		}
		dyn := isUnconstrainedDynamic(initVal.Ty)
		if !initVal.Ty.IsObject() && !dyn {
			// A primitive init (the JS lane): the dictionary conversion's
			// TypeError, at run time.
			b, err := e.emitBoxValue(initVal)
			if err != nil {
				return Value{}, err
			}
			initVal, dyn = b, true
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
			if err := e.emitThrowIfPrimitiveInit(initVal); err != nil {
				return Value{}, err
			}
			// An absent member keeps what the slot holds: GET, no headers
			// or body, or the input Request's.
			mv := dynGet("method")
			ms, _ := e.emitAnyIntoString(mv, TypePtr)
			isUndef, sel, curM := e.freshReg(), e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", curM, mSlot))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, mv.Ref, nbUndefined))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", sel, isUndef, curM, ms.Ref))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", sel, mSlot))
			present := func(v Value, then func() error) error {
				has := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, %d", has, v.Ref, nbUndefined))
				setL, skipL := e.freshLabel("req.dynset"), e.freshLabel("req.dynkeep")
				e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, setL, skipL))
				e.emitLabel(setL)
				if err := then(); err != nil {
					return err
				}
				e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
				e.emitLabel(skipL)
				return nil
			}
			hv := dynGet("headers")
			if err := present(hv, func() error {
				h, err := e.emitNewHeaders(&hv, pos)
				if err != nil {
					return err
				}
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", h.Ref, hSlot))
				return nil
			}); err != nil {
				return Value{}, err
			}
			bv := dynGet("body")
			if err := present(bv, func() error {
				d, l, c := e.emitFetchBodyFromBox(bv)
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", d, bSlot))
				e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", l, lSlot))
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", c, cSlot))
				return nil
			}); err != nil {
				return Value{}, err
			}
			for _, m := range requestInitStrings {
				v := dynGet(m.name)
				vs, _ := e.emitAnyIntoString(v, TypePtr)
				isUndef, sel := e.freshReg(), e.freshReg()
				cur := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", cur, extraSlots[m.name]))
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, v.Ref, nbUndefined))
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", sel, isUndef, cur, vs.Ref))
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", sel, extraSlots[m.name]))
			}
			if hasSignals {
				if err := e.storeRequestSignal(dynGet("signal"), sigCls, sigSlot); err != nil {
					return Value{}, err
				}
			}
			kv := dynGet("keepalive")
			kb := e.toBool(kv).Ref
			e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", kb, kaSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneInitL))
			e.emitLabel(doneInitL)
		} else if initVal.Ty.Nullable() {
			haveL := e.freshLabel("req.init")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.ptrIsNull(initVal.Ref), doneInitL, haveL))
			e.emitLabel(haveL)
			nn := initVal.Ty
			nn.setNullable(false)
			nn.setUndefined(false)
			initVal = Value{Ref: initVal.Ref, Ty: nn}
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("method"); ok && !dyn {
			mv := e.loadFieldValue(initVal, idx, fieldTy)
			switch {
			case isStringTy(mv.Ty) && !mv.Ty.IsObject() && !mv.Ty.IsArray():
			case mv.Ty.IsDynamic():
				mv, _ = e.emitAnyIntoString(mv, TypePtr)
			default:
				// Any other value: its ToString, as the WebIDL ByteString
				// conversion takes it.
				b, err := e.emitBoxValue(mv)
				if err != nil {
					return Value{}, err
				}
				mv, _ = e.emitAnyIntoString(b, TypePtr)
			}
			// An absent member keeps GET.
			isNull, sel := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, mv.Ref))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", sel, isNull, e.internString("GET"), mv.Ref))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", sel, mSlot))
		}
		if _, _, ok := initVal.Ty.FieldIndex("headers"); ok && !dyn {
			hv, err := e.emitHeadersFromInit(initVal, ex.GetPos())
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", hv.Ref, hSlot))
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("body"); ok && !dyn {
			bv := e.loadFieldValue(initVal, idx, fieldTy)
			box, berr := e.emitBoxValue(bv)
			if berr != nil {
				return Value{}, internalAt(ex.GetPos(), "Request's init.body: %v", berr)
			}
			d, l, c := e.emitFetchBodyFromBox(box)
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", d, bSlot))
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", l, lSlot))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", c, cSlot))
		}
		if !dyn {
			for _, m := range requestInitStrings {
				idx, fieldTy, ok := initVal.Ty.FieldIndex(m.name)
				if !ok {
					continue
				}
				mv := e.loadFieldValue(initVal, idx, fieldTy)
				switch {
				case isStringTy(mv.Ty) && !mv.Ty.IsObject() && !mv.Ty.IsArray():
				case mv.Ty.IsDynamic():
					mv, _ = e.emitAnyIntoString(mv, TypePtr)
				default:
					b, err := e.emitBoxValue(mv)
					if err != nil {
						return Value{}, err
					}
					mv, _ = e.emitAnyIntoString(b, TypePtr)
				}
				isNull, cur, sel := e.freshReg(), e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", cur, extraSlots[m.name]))
				e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, mv.Ref))
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", sel, isNull, cur, mv.Ref))
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", sel, extraSlots[m.name]))
			}
			if idx, fieldTy, ok := initVal.Ty.FieldIndex("signal"); ok && hasSignals {
				sv := e.loadFieldValue(initVal, idx, fieldTy)
				if e.isAbortSignalType(fieldTy) {
					e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", sv.Ref, sigSlot))
				} else {
					// Another type (the JS lane, or an any-typed field):
					// checked at run time.
					b, err := e.emitBoxValue(sv)
					if err != nil {
						return Value{}, err
					}
					if err := e.storeRequestSignal(b, sigCls, sigSlot); err != nil {
						return Value{}, err
					}
				}
			}
			if idx, fieldTy, ok := initVal.Ty.FieldIndex("keepalive"); ok {
				kv := e.loadFieldValue(initVal, idx, fieldTy)
				e.emitInstr(fmt.Sprintf("store i1 %s, "+irPtr+" %s, align 1", e.toBool(kv).Ref, kaSlot))
			}
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneInitL))
			e.emitLabel(doneInitL)
		}
	}
	methodRaw, headersRef, bodyData, bodyLen, ctRef := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", methodRaw, mSlot))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", headersRef, hSlot))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", bodyData, bSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", bodyLen, lSlot))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", ctRef, cSlot))
	headersVal := Value{Ref: headersRef, Ty: headersTy}

	// Fetch normalizes the standard methods' case ("post" is "POST").
	m := methodRaw
	for _, std := range []string{"DELETE", "GET", "HEAD", "OPTIONS", "POST", "PUT"} {
		c, eq, sel := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp("+irPtr+" %s, "+irPtr+" %s)", c, methodRaw, e.internString(std)))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", eq, c))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", sel, eq, e.internString(std), m))
		m = sel
	}
	methodVal := Value{Ref: m, Ty: TypePtr}

	// A body: a GET or HEAD request has none (Fetch's Request constructor
	// throws); its bytes are copied; its implied Content-Type is set unless
	// the headers have one.
	hasBody := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", hasBody, bodyData))
	bodySlot, lenSlot, flagSlot := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", bodySlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lenSlot))
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", flagSlot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", bodySlot))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", lenSlot))
	e.emitInstr(fmt.Sprintf("store i64 %d, "+irPtr+" %s, align 8", responseNullBody, flagSlot))
	bodyL, noBodyL := e.freshLabel("req.hasbody"), e.freshLabel("req.nobody")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasBody, bodyL, noBodyL))
	e.emitLabel(bodyL)
	{
		cg, ch := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp("+irPtr+" %s, "+irPtr+" %s)", cg, methodVal.Ref, e.internString("GET")))
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp("+irPtr+" %s, "+irPtr+" %s)", ch, methodVal.Ref, e.internString("HEAD")))
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
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_alloc(i64 %s)", cp, bodyLen))
		e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", cp, bodyData, bodyLen))
		end := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", end, cp, bodyLen))
		e.heapStore("i8", "0", end, 1)
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", cp, bodySlot))
		e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", bodyLen, lenSlot))
		e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", flagSlot))
		if err := e.emitSetHeaderIfAbsent(headersVal, "content-type", ctRef, ex.GetPos()); err != nil {
			return Value{}, err
		}
		e.emitTerminator(fmt.Sprintf("br label %%%s", noBodyL))
	}
	e.emitLabel(noBodyL)
	bodyRef, lenRef, flags := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", bodyRef, bodySlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", lenRef, lenSlot))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", flags, flagSlot))
	bodyVal := Value{Ref: bodyRef, Ty: TypePtr}

	ty := FetchRequestType()
	objReg := e.freshReg()
	e.emitObjAllocInto(objReg, ty)
	structIR := ty.StructIR()

	storeField := func(name string, val Value) {
		idx, fieldTy, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.heapStore(fieldTy.IR, val.Ref, gep, fieldTy.Align())
	}
	storeField("url", urlVal)
	storeField("method", methodVal)
	storeField("__kml_headers", headersVal)
	storeField("body", bodyVal)
	storeField("bodyLength", Value{Ref: lenRef, Ty: TypeI64})
	storeField("__kml_bodyflags", Value{Ref: flags, Ty: TypeI64})
	extra := map[string]string{}
	for _, m := range requestInitStrings {
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", v, extraSlots[m.name]))
		extra[m.name] = v
		if m.kind >= 0 {
			msg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_request_enum_error(i64 %d, "+irPtr+" %s)", msg, m.kind, v))
			e.emitThrowTypeErrorIfNonNull(msg)
		}
	}
	// 'only-if-cached' needs same-origin mode.
	{
		c1, c2, b1, b2, bad := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp("+irPtr+" %s, "+irPtr+" %s)", c1, extra["cache"], e.internString("only-if-cached")))
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp("+irPtr+" %s, "+irPtr+" %s)", c2, extra["mode"], e.internString("same-origin")))
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", b1, c1))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", b2, c2))
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", bad, b1, b2))
		badL, okL := e.freshLabel("req.oic"), e.freshLabel("req.oicok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
		e.emitLabel(badL)
		e.emitThrowTypeErrorValue(e.internString("'only-if-cached' can be set only with 'same-origin' mode"))
		e.emitLabel(okL)
	}
	errSlot, ref := e.freshReg(), e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", errSlot))
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_request_referrer("+irPtr+" %s, "+irPtr+" %s)", ref, extra["referrer"], errSlot))
	refErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", refErr, errSlot))
	e.emitThrowTypeErrorIfNonNull(refErr)
	extra["referrer"] = ref
	for _, m := range requestInitStrings {
		storeField(m.name, Value{Ref: extra[m.name], Ty: TypePtr})
	}
	if hasSignals {
		// Its own signal: one following the init's (AbortSignal.any), else a
		// fresh one that never aborts.
		const tmp = "__kml_req_init_signal"
		e.pushScope()
		nt := sigCls
		nt.setNullable(true)
		e.define(tmp, Symbol{Ptr: sigSlot, Ty: nt})
		p := ex.GetPos()
		id := ast.NewIdentifier(tmp, p)
		follow := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(e.globalLinks["AbortSignal"], p), "any", p),
			[]ast.Expression{ast.NewArrayLiteral([]ast.Expression{id}, p)}, p)
		fresh := ast.NewMemberExpression(ast.NewNewExpression(e.globalLinks["AbortController"], nil, p), "signal", p)
		sig, err := e.emitExpr(ast.NewConditionalExpression(id, follow, fresh, p))
		e.popScope()
		if err != nil {
			return Value{}, err
		}
		storeField("__kml_signal", Value{Ref: e.convert(sig, sigCls).Ref, Ty: TypePtr})
	}
	ka := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, "+irPtr+" %s, align 1", ka, kaSlot))
	storeField("keepalive", Value{Ref: ka, Ty: TypeBool})

	return Value{Ref: objReg, Ty: ty}, nil
}

// requestInitStrings are a Request init's string members besides method:
// their defaults, and the enum each is checked against
// (__kml_request_enum_error's kind; -1 for none).
var requestInitStrings = []struct {
	name, dflt string
	kind       int
}{
	{"cache", "default", 0},
	{"credentials", "same-origin", 1},
	{"destination", "", -1},
	{"integrity", "", -1},
	{"mode", "cors", 2},
	{"redirect", "follow", 3},
	{"referrer", "about:client", -1},
	{"referrerPolicy", "", 4},
}

// emitThrowTypeErrorIfNonNull throws a TypeError with message msg when msg
// is non-null.
func (e *Emitter) emitThrowTypeErrorIfNonNull(msg string) {
	isErr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", isErr, msg))
	badL, okL := e.freshLabel("req.bad"), e.freshLabel("req.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isErr, badL, okL))
	e.emitLabel(badL)
	e.emitThrowTypeErrorValue(msg)
	e.emitLabel(okL)
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

// storeRequestSignal stores init.signal (boxed sv) into slot: null when it
// is null or undefined, the signal when it is an AbortSignal, else the
// TypeError Node's RequestInit conversion throws.
func (e *Emitter) storeRequestSignal(sv Value, sigCls Type, slot string) error {
	nullish := e.emitIsNullish(sv)
	noneL, someL, badL, okL, doneL := e.freshLabel("req.sig.none"), e.freshLabel("req.sig.some"), e.freshLabel("req.sig.bad"), e.freshLabel("req.sig.ok"), e.freshLabel("req.sig.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", nullish, noneL, someL))
	e.emitLabel(noneL)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(someL)
	is, err := e.emitAnyInstanceOfClass(sv, sigCls.ClassName)
	if err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", is.Ref, okL, badL))
	e.emitLabel(badL)
	shown, err := e.emitInspectQuoted(sv, "\"")
	if err != nil {
		return err
	}
	msg, err := e.emitStringConcat(Value{Ref: e.internString("RequestInit: Expected signal (\""), Ty: TypePtr}, shown)
	if err != nil {
		return err
	}
	if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString("\") to be an instance of AbortSignal."), Ty: TypePtr}); err != nil {
		return err
	}
	e.emitThrowTypeErrorValue(msg.Ref)
	e.emitLabel(okL)
	sig := e.convert(sv, sigCls)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", sig.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	return nil
}

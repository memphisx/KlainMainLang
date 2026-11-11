// emit_fetch.go — fetch(url)/fetch(url, init), Response (status/ok/body
// fields, text()/json() methods). init (ADR-00074, TDD-00017) is any value
// with some subset of method: string / headers: Map<string,string> /
// body: string fields.
//
// fetch() itself issues a real, non-blocking libcurl multi-interface
// transfer (see runtime.go's ensureFetchAsync, ADR-00050) and returns
// immediately with a pending Promise<Response> — the actual wait (yielding
// if running inside an http.listen connection fiber, so a slow upstream
// call doesn't block any other connection; busy-spinning via curl_multi
// otherwise, since there's nothing else to overlap with at the top level)
// and the Response object's own construction both happen at await time
// (emit_async.go's emitAwait), not here.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// hasBodyMixin reports a type with Fetch's Body members (body, bodyUsed,
// text(), json(), arrayBuffer()): a Response or a Request.
func hasBodyMixin(t Type) bool {
	return t.IsResponse() || t.IsFetchRequest()
}

// emitFetch implements fetch(url), fetch(url, init), and fetch(request)
// (ADR-00074/TDD-00017, TDD-00040): kicks off a non-blocking libcurl
// multi-interface transfer via __kml_fetch_async and wraps the returned
// pending-fetch handle in a Promise<Response> slot — the same slot shape
// emitAsyncEpilogue/emitAwait (emit_async.go) already expect, just holding
// a not-yet-resolved pending handle instead of an already-built Response,
// since building the Response needs the transfer to have actually finished
// (see emitAwait's IsResponse-specific branch).
//
// init, when present, is any value whose inferred type has some subset of
// method: string / headers: Map<string,string> | Headers / body: string
// fields — no shared RequestInit interface has to exist, matching the same
// FieldIndex-on-the-inferred-type pattern http.listen's own optional
// headers field already uses (emit_http.go's isPlainStringType, ADR-00072).
// Each field present resolves to a ptr value passed to __kml_fetch_async;
// each field absent passes a literal "null", which the shared runtime
// function treats as "use curl's default" (see ensureFetchAsync's doc
// comment in runtime.go).
//
// A single argument whose inferred type is IsFetchRequest (TDD-00040) is
// destructured the same way — a real Request object always has every field
// present (method defaults to "GET", headers to an empty Headers, body to
// null at construction, see emit_fetch_request.go), so this is really just
// the init-object path with the fields already resolved and validated.
func (e *Emitter) emitFetch(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 2 {
		kept, err := e.dropArgsPast(args, 2, pos)
		if err != nil {
			return Value{}, err
		}
		args = kept
	}
	if len(args) == 0 {
		args = []ast.Expression{ast.NewNullLiteral(true, pos)} // (its URL "undefined" then fails to parse)
	}

	if len(args) == 1 {
		if reqTy := e.typeOfExpr(args[0]); reqTy.IsFetchRequest() {
			reqVal, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			urlIdx, urlFieldTy, _ := reqTy.FieldIndex("url")
			methodIdx, methodFieldTy, _ := reqTy.FieldIndex("method")
			bodyIdx, bodyFieldTy, _ := reqTy.FieldIndex("body")

			urlVal := e.loadFieldValue(reqVal, urlIdx, urlFieldTy)
			methodVal := e.loadFieldValue(reqVal, methodIdx, methodFieldTy)
			bodyVal := e.loadFieldValue(reqVal, bodyIdx, bodyFieldTy)
			headersVal, err := e.emitRequestHeaders(reqVal, pos)
			if err != nil {
				return Value{}, err
			}
			headersRef, err := e.emitHeadersSlist(headersVal, pos)
			if err != nil {
				return Value{}, err
			}
			sigIdx, sigFieldTy, _ := reqTy.FieldIndex("__kml_signal")
			sig := e.loadFieldValue(reqVal, sigIdx, sigFieldTy)
			lenIdx, lenFieldTy, _ := reqTy.FieldIndex("bodyLength")
			bodyLen := e.loadFieldValue(reqVal, lenIdx, lenFieldTy)
			return e.emitFetchAsyncCallN(urlVal.Ref, methodVal.Ref, headersRef, bodyVal.Ref, bodyLen.Ref, sig.Ref)
		}
	}

	urlVal, err := e.emitExpr(stringifiedInput(args[0]))
	if err != nil {
		return Value{}, err
	}
	urlVal = e.convert(urlVal, TypePtr)

	methodRef, headersRef, bodyRef, signalRef := "null", "null", "null", "null"
	bodyLen := "-1"
	if len(args) == 2 {
		initVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		// An init held in `any` is read member by member at run time;
		// undefined or null is no init (WebIDL's empty dictionary).
		dyn := isUnconstrainedDynamic(initVal.Ty)
		if nullishLiteralType(initVal.Ty) {
			// undefined or null: no init.
			return e.emitFetchAsyncCallN(urlVal.Ref, "null", "null", "null", "-1", "null")
		}
		if initVal.Ty.IsSymbol() {
			// Not a dictionary (WebIDL's RequestInit conversion), and one
			// its message cannot show: the promise rejects, nothing sent.
			return e.emitFetchRejected(e.internString("Cannot convert a Symbol value to a string"), pos)
		}
		if !initVal.Ty.IsObject() && !dyn {
			// Not a dictionary: the promise rejects, nothing sent.
			shown, err := e.emitValueToString(initVal)
			if err != nil {
				return Value{}, err
			}
			msg, err := e.emitStringConcat(Value{Ref: e.internString("undefined: Expected "), Ty: TypePtr}, shown)
			if err != nil {
				return Value{}, err
			}
			if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(" to be one of: Null, Undefined, Object."), Ty: TypePtr}); err != nil {
				return Value{}, err
			}
			return e.emitFetchRejected(msg.Ref, pos)
		}
		dynGet := func(name string) Value {
			v, _ := e.emitDynAnyMemberGetNamed(initVal, e.internString(name), name, pos)
			return v
		}
		// An optional init (`init?: RequestInit`) may be absent: its parts
		// are read only when it is there, and default to none.
		var slots [5]string
		var skipL, doneInitL string
		// The error a refused init member rejects with (null: none), set
		// before the init may be skipped.
		rejectSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", rejectSlot))
		e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", rejectSlot))
		rejectUsed := false
		if initVal.Ty.Nullable() || dyn {
			for i := range slots {
				slots[i] = e.freshReg()
				ir := irPtr
				if i == 4 {
					ir = "i64"
				}
				e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slots[i], ir))
			}
			for i := 0; i < 4; i++ {
				e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", slots[i]))
			}
			e.emitInstr(fmt.Sprintf("store i64 -1, "+irPtr+" %s, align 8", slots[4]))
			haveL := e.freshLabel("fetch.init")
			skipL, doneInitL = e.freshLabel("fetch.noinit"), e.freshLabel("fetch.initdone")
			var absent string
			if !dyn {
				absent = e.ptrIsNull(initVal.Ref)
			} else {
				undef, null := e.freshReg(), e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", undef, initVal.Ref, nbUndefined))
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", null, initVal.Ref, nbNull))
				absent = e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", absent, undef, null))
			}
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", absent, skipL, haveL))
			e.emitLabel(haveL)
			if !dyn {
				nn := initVal.Ty
				nn.setNullable(false)
				nn.setUndefined(false)
				initVal = Value{Ref: initVal.Ref, Ty: nn}
			}
		}
		if dyn {
			// signal: an AbortSignal, or none when undefined/null; any
			// other value rejects.
			rejectUsed = true
			if err := e.emitFetchSignalCheck(dynGet("signal"), rejectSlot, pos); err != nil {
				return Value{}, err
			}
			if cls, hasSignals := e.abortSignalClass(); hasSignals {
				sv := dynGet("signal")
				sig := e.convert(sv, cls)
				nullish := e.emitIsNullish(sv)
				sel := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" null, "+irPtr+" %s", sel, nullish, sig.Ref))
				signalRef = sel
			}
			mv := dynGet("method")
			ms, _ := e.emitAnyIntoString(mv, TypePtr)
			isUndef, sel := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, mv.Ref, nbUndefined))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" null, "+irPtr+" %s", sel, isUndef, ms.Ref))
			methodRef = sel
		} else if idx, fieldTy, ok := initVal.Ty.FieldIndex("signal"); ok {
			_, hasSignals := e.abortSignalClass()
			switch {
			case e.isAbortSignalType(fieldTy):
				signalRef = e.loadFieldValue(initVal, idx, fieldTy).Ref
			case !hasSignals:
				// A program with no AbortSignal has none to pass: the member
				// (RequestInit's `signal?: AbortSignal | null`) is absent.
			default:
				// Another value: the promise rejects (an undefined or null
				// one is no signal).
				box, err := e.emitBoxValue(e.loadFieldValue(initVal, idx, fieldTy))
				if err != nil {
					return Value{}, err
				}
				rejectUsed = true
				if err := e.emitFetchSignalCheck(box, rejectSlot, pos); err != nil {
					return Value{}, err
				}
			}
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("method"); ok && !dyn {
			mv := e.loadFieldValue(initVal, idx, fieldTy)
			switch {
			case isStringTy(mv.Ty) && !mv.Ty.IsObject() && !mv.Ty.IsArray():
				methodRef = mv.Ref
			case mv.Ty.IsDynamic():
				s, _ := e.emitAnyIntoString(mv, TypePtr)
				methodRef = s.Ref
			default:
				// Another value: its ToString (WebIDL's ByteString).
				sv, err := e.emitValueToString(mv)
				if err != nil {
					return Value{}, err
				}
				methodRef = sv.Ref
			}
		}
		// The body, as undici's extractBody reads it, and the Content-Type
		// it implies.
		ctRef := "null"
		if dyn {
			bodyRef, bodyLen, ctRef = e.emitFetchBodyFromBox(dynGet("body"))
		} else if idx, fieldTy, ok := initVal.Ty.FieldIndex("body"); ok {
			bv := e.loadFieldValue(initVal, idx, fieldTy)
			box, err := e.emitBoxValue(bv)
			if err != nil {
				return Value{}, internalAt(pos, "fetch's init.body: %v", err)
			}
			bodyRef, bodyLen, ctRef = e.emitFetchBodyFromBox(box)
		}
		// The headers: init's, plus the implied Content-Type unless they
		// set one.
		var headers Value
		if dyn {
			hv := dynGet("headers")
			headers, err = e.emitNewHeaders(&hv, pos)
		} else {
			headers, err = e.emitHeadersFromInit(initVal, pos)
		}
		if err != nil {
			return Value{}, err
		}
		if err := e.emitSetHeaderIfAbsent(headers, "content-type", ctRef, pos); err != nil {
			return Value{}, err
		}
		if headersRef, err = e.emitHeadersSlist(headers, pos); err != nil {
			return Value{}, err
		}
		if bodyRef != "null" {
			// A body with no Content-Type is sent without one, as Node sends
			// it: `Content-Type:` removes curl's form-urlencoded default.
			hasV, err := e.emitHeadersMethod(headers, "has", pos, ast.NewStringLiteral("content-type", pos))
			if err != nil {
				return Value{}, err
			}
			has := e.convert(hasV, TypeBool).Ref
			hasBody, need := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", hasBody, bodyRef))
			noCT := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", noCT, has))
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", need, noCT, hasBody))
			addL, joinL := e.freshLabel("fetch.noct"), e.freshLabel("fetch.ctdone")
			lslot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", lslot))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", headersRef, lslot))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", need, addL, joinL))
			e.emitLabel(addL)
			l2 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @curl_slist_append("+irPtr+" %s, "+irPtr+" %s)", l2, headersRef, e.internString("Content-Type:")))
			e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", l2, lslot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			l3 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", l3, lslot))
			headersRef = l3
		}
		if skipL != "" {
			for i, r := range []string{methodRef, headersRef, bodyRef, signalRef} {
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", r, slots[i]))
			}
			e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", bodyLen, slots[4]))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneInitL))
			e.emitLabel(skipL)
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneInitL))
			e.emitLabel(doneInitL)
			refs := make([]string, 5)
			for i := range refs {
				refs[i] = e.freshReg()
				ir := irPtr
				if i == 4 {
					ir = "i64"
				}
				e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", refs[i], ir, slots[i]))
			}
			methodRef, headersRef, bodyRef, signalRef, bodyLen = refs[0], refs[1], refs[2], refs[3], refs[4]
		}
		if rejectUsed {
			// A refused init member: the promise rejects with its error.
			errRef, rejected := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", errRef, rejectSlot))
			e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", rejected, errRef))
			rejL, sendL := e.freshLabel("fetch.rejected"), e.freshLabel("fetch.send")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", rejected, rejL, sendL))
			e.emitLabel(rejL)
			rv, err := e.emitFetchRejectedErr(errRef)
			if err != nil {
				return Value{}, err
			}
			res := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", res, rv.Ty.IR))
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", rv.Ty.IR, rv.Ref, res))
			joinL := e.freshLabel("fetch.settled")
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(sendL)
			sv, err := e.emitFetchAsyncCallN(urlVal.Ref, methodRef, headersRef, bodyRef, bodyLen, signalRef)
			if err != nil {
				return Value{}, err
			}
			e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", sv.Ty.IR, sv.Ref, res))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			out := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", out, sv.Ty.IR, res))
			return Value{Ref: out, Ty: sv.Ty}, nil
		}
	}

	return e.emitFetchAsyncCallN(urlVal.Ref, methodRef, headersRef, bodyRef, bodyLen, signalRef)
}

// emitFetchRejected is a fetch's result when its arguments are refused
// before any request: a promise rejected with a TypeError of message msg
// (a string register).
func (e *Emitter) emitFetchRejected(msg string, pos ast.Pos) (Value, error) {
	ev, err := e.emitExpr(ast.NewNewErrorExpression("TypeError", e.bindValue(Value{Ref: msg, Ty: TypePtr}, pos), pos))
	if err != nil {
		return Value{}, err
	}
	return e.emitFetchRejectedErr(ev.Ref)
}

// emitFetchRejectedErr is a fetch rejected with the error object errRef.
func (e *Emitter) emitFetchRejectedErr(errRef string) (Value, error) {
	e.ensureFetchAsync()
	e.declareFn("__kml_fetch_rejected_pending", "declare "+irPtr+" @__kml_fetch_rejected_pending("+irPtr+")")
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_rejected_pending("+irPtr+" %s)", p, errRef))
	return e.finishFetchPending(p)
}

// emitFetchSignalCheck refuses an init's signal (the box sv) that is not
// an AbortSignal, undefined or null: WebIDL's TypeError, `RequestInit:
// Expected signal ("5") to be an instance of AbortSignal.`, goes in the
// reject slot the fetch then rejects with.
func (e *Emitter) emitFetchSignalCheck(sv Value, slot string, pos ast.Pos) error {
	nullish := e.emitIsNullish(sv)
	isSig := "false"
	if _, has := e.abortSignalClass(); has {
		r := e.emitDynHostInstanceOf(sv, "AbortSignal")
		isSig = r.Ref
	}
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", ok, nullish, isSig))
	okL, badL := e.freshLabel("fetch.sig.ok"), e.freshLabel("fetch.sig.bad")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, okL, badL))
	e.emitLabel(badL)
	shown, err := e.emitFetchSignalShown(sv, pos)
	if err != nil {
		return err
	}
	msg, err := e.emitStringConcat(Value{Ref: e.internString(`RequestInit: Expected signal ("`), Ty: TypePtr}, shown)
	if err != nil {
		return err
	}
	if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(`") to be an instance of AbortSignal.`), Ty: TypePtr}); err != nil {
		return err
	}
	ev, err := e.emitExpr(ast.NewNewErrorExpression("TypeError", e.bindValue(msg, pos), pos))
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", ev.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", okL))
	e.emitLabel(okL)
	return nil
}

// emitFetchSignalShown is the value sv as undici's webidl names it in a
// message: a string in double quotes, a symbol as its description, anything
// else as util.inspect shows it.
func (e *Emitter) emitFetchSignalShown(sv Value, pos ast.Pos) (Value, error) {
	id := e.bindValue(sv, pos)
	isStr := ast.NewBinaryExpression("===", ast.NewUnaryExpression("typeof", true, id, pos), ast.NewStringLiteral("string", pos), pos)
	isSym := ast.NewBinaryExpression("===", ast.NewUnaryExpression("typeof", true, id, pos), ast.NewStringLiteral("symbol", pos), pos)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	strV, err := e.emitExpr(isStr)
	if err != nil {
		return Value{}, err
	}
	sL, nsL, doneL := e.freshLabel("sig.str"), e.freshLabel("sig.notstr"), e.freshLabel("sig.shown")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.toBool(strV).Ref, sL, nsL))
	e.emitLabel(sL)
	q, err := e.emitExpr(ast.NewBinaryExpression("+", ast.NewBinaryExpression("+", ast.NewStringLiteral(`"`, pos), ast.NewAsExpression(id, nameAnnotation("string"), pos), pos), ast.NewStringLiteral(`"`, pos), pos))
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", e.convert(q, TypePtr).Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(nsL)
	symV, err := e.emitExpr(isSym)
	if err != nil {
		return Value{}, err
	}
	yL, oL := e.freshLabel("sig.sym"), e.freshLabel("sig.other")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", e.toBool(symV).Ref, yL, oL))
	e.emitLabel(yL)
	ds, err := e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(id, "toString", pos), nil, pos))
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", e.convert(ds, TypePtr).Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(oL)
	ins, err := e.emitDynamicInspect(sv)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", e.convert(ins, TypePtr).Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", out, slot))
	return Value{Ref: out, Ty: TypePtr}, nil
}

// emitFetchAsyncCallN is emitFetchAsyncCall with the body's byte length (-1:
// a NUL-terminated string).
func (e *Emitter) emitFetchAsyncCallN(urlRef, methodRef, headersRef, bodyRef, bodyLen, signalRef string) (Value, error) {
	e.ensureFetchAsync()
	pendingReg := e.freshReg()
	if bodyRef == "null" {
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_async_n("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, i64 %s, "+irPtr+" %s)",
			pendingReg, urlRef, methodRef, headersRef, bodyRef, bodyLen, signalRef))
		return e.finishFetchPending(pendingReg)
	}
	// A body on a GET or HEAD request (the default method is GET) rejects
	// with Node's TypeError, and nothing is sent.
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	hasBody, noMethod := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", hasBody, bodyRef))
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", noMethod, methodRef))
	m := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", m, noMethod, e.internString("GET"), methodRef))
	g, h, gz, hz, gh, bad := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp("+irPtr+" %s, "+irPtr+" %s)", g, m, e.internString("GET")))
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp("+irPtr+" %s, "+irPtr+" %s)", h, m, e.internString("HEAD")))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", gz, g))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", hz, h))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", gh, gz, hz))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", bad, gh, hasBody))
	badL, goL, doneL := e.freshLabel("fetch.getbody"), e.freshLabel("fetch.go"), e.freshLabel("fetch.started")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, goL))
	e.emitLabel(badL)
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_failed_pending(i64 -2)", fp))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", fp, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(goL)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_async_n("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, i64 %s, "+irPtr+" %s)",
		p, urlRef, methodRef, headersRef, bodyRef, bodyLen, signalRef))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", p, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", pendingReg, slot))
	return e.finishFetchPending(pendingReg)
}

// emitFetchAsyncCall is the shared tail every fetch() call form (bare url,
// url+init, or a Request object) reduces to once url/method/headers/body
// refs are resolved: call __kml_fetch_async, box the returned pending
// handle into a fresh Promise<Response> slot.
func (e *Emitter) emitFetchAsyncCall(urlRef, methodRef, headersRef, bodyRef, signalRef string) (Value, error) {
	e.ensureFetchAsync()
	pendingReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_async("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s)",
		pendingReg, urlRef, methodRef, headersRef, bodyRef, signalRef))
	return e.finishFetchPending(pendingReg)
}

// finishFetchPending boxes a pending fetch handle into a Promise<Response>
// slot.
func (e *Emitter) finishFetchPending(pendingReg string) (Value, error) {
	slotReg := e.freshReg()
	e.heapMallocInto(slotReg, allocHost, "8")
	e.heapStore(irPtr, pendingReg, slotReg, 8)
	return Value{Ref: slotReg, Ty: PromiseOf(ResponseType())}, nil
}

// loadFieldValue GEPs and loads objVal's field at idx/fieldTy — the same
// GEP+load shape emitResponseBody below already uses for Response's own
// "body" field, generalized here since emitFetch needs it for three
// different optional init fields (method/headers/body) rather than one
// fixed one.
//
// A structural type's field is read through the checked view (TDD-00233):
// an object of another layout behind it reads by name.
func (e *Emitter) loadFieldValue(objVal Value, idx int, fieldTy Type) Value {
	var gep string
	if isRecordView(objVal.Ty) && !objVal.Ty.Nullable() && idx < len(objVal.Ty.Fields) {
		gep = e.emitRecordGep(objVal.Ref, objVal.Ty, idx, fieldTy, objVal.Ty.Fields[idx].Name)
	} else {
		gep = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	}
	if isNullableScalar(fieldTy) {
		// Its { i1, T } storage.
		return e.loadScalarOrNullableField(gep, fieldTy)
	}
	if fieldTy.IsArray() {
		// Its header-pointer slot (TDD-00213 Stage 2).
		return e.loadArrayFieldValue(gep, fieldTy)
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", r, fieldTy.IR, gep, fieldTy.Align()))
	return Value{Ref: r, Ty: fieldTy}
}

// buildFetchHeaderList is the curl_slist (ADR-00074/TDD-00017) of a
// Map<string,string>'s entries (XMLHttpRequest's and http's header maps).
func (e *Emitter) buildFetchHeaderList(mapPtr string) (string, error) {
	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(mapPtr, "str", Type{IR: irPtr})
	return e.buildCurlSlist(keysLen, func(i string) (string, string) {
		return e.loadPtrAt(keysPtr, i), e.loadPtrAt(valsPtr, i)
	})
}

// buildCurlSlist is a curl_slist of n "name: value" lines, entry(i) giving
// line i's name and value. No lines is a null list.
func (e *Emitter) buildCurlSlist(n string, entry func(i string) (name, value string)) (string, error) {
	listAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", listAlloca))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" null, "+irPtr+" %s, align 8", listAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, "+irPtr+" %s, align 8", idxAlloca))

	condL := e.freshLabel("fetchheaders.cond")
	bodyL := e.freshLabel("fetchheaders.body")
	doneL := e.freshLabel("fetchheaders.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal, isDone := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", isDone, idxVal, n))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDone, doneL, bodyL))

	e.emitLabel(bodyL)
	name, value := entry(idxVal)
	line1, err := e.emitStringConcat(Value{Ref: name, Ty: TypePtr}, Value{Ref: e.internString(": "), Ty: TypePtr})
	if err != nil {
		return "", err
	}
	line2, err := e.emitStringConcat(line1, Value{Ref: value, Ty: TypePtr})
	if err != nil {
		return "", err
	}
	curList, newList := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", curList, listAlloca))
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @curl_slist_append("+irPtr+" %s, "+irPtr+" %s)", newList, curList, line2.Ref))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", newList, listAlloca))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, "+irPtr+" %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	finalList := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", finalList, listAlloca))
	return finalList, nil
}

// emitResponseBody extracts a Response value's buffered body string (a
// plain GEP+load of its "body" field — factored out since both text() and
// json() need the same raw string before doing anything method-specific).
func (e *Emitter) emitResponseBody(objVal Value, pos ast.Pos) (Value, error) {
	e.emitResponseDriveToDone(objVal)
	idx, fieldTy, ok := objVal.Ty.FieldIndex("body")
	if !ok {
		return Value{}, internalAt(pos, "not a Response")
	}
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", r, fieldTy.IR, gep, fieldTy.Align()))
	// TDD-00120: the raw body field is the dual-use curl buffer (also .body /
	// .arrayBuffer()), with no length header. text()/json() decode all
	// bodyLength bytes as TextDecoder does: UTF-8, an invalid sequence as
	// U+FFFD, a leading BOM dropped, an embedded NUL byte kept. A Request
	// without a body has a null one: "".
	e.ensureStrHeaderRuntime()
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, r))
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", src, isNull, e.internString(""), r))
	lIdx, lTy, hasLen := objVal.Ty.FieldIndex("bodyLength")
	if !hasLen || lTy.IR != "i64" {
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_from_cstr("+irPtr+" %s)", rc, src))
		return Value{Ref: rc, Ty: fieldTy}, nil
	}
	lGep, raw, n := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", lGep, objVal.Ty.StructIR(), objVal.Ref, lIdx))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", raw, lGep))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", n, isNull, raw))
	// A UTF-8 BOM (EF BB BF) opening the body is not part of the text.
	long3, b0, b1, b2 := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp uge i64 %s, 3", long3, n))
	p1, p2 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 1", p1, src))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 2", p2, src))
	safe0, safe1, safe2 := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", safe0, long3, src, e.internString("___")))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", safe1, long3, p1, e.internString("___")))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", safe2, long3, p2, e.internString("___")))
	e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" %s, align 1", b0, safe0))
	e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" %s, align 1", b1, safe1))
	e.emitInstr(fmt.Sprintf("%s = load i8, "+irPtr+" %s, align 1", b2, safe2))
	c0, c1, c2, c01, bom := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, -17", c0, b0))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, -69", c1, b1))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, -65", c2, b2))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", c01, c0, c1))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", bom, c01, c2))
	skip, from, cnt := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 3, i64 0", skip, bom))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", from, src, skip))
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", cnt, n, skip))
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_buf_utf8_str("+irPtr+" %s, i64 %s)", rc, from, cnt))
	return Value{Ref: rc, Ty: fieldTy}, nil
}

// emitResponseDriveToDone (TDD-00097 Stage 4): a headers-resolved Response
// carries its pending handle; before any buffered body read, drive the
// transfer to completion and cache body/bodyLength into the object. The
// handle stays: its headers, status line, URL and redirect count are read
// from it after the body too (a finished transfer's drive returns at once).
func (e *Emitter) emitResponseDriveToDone(objVal Value) {
	pIdx, _, ok := objVal.Ty.FieldIndex("__kml_pending")
	if !ok {
		return
	}
	e.ensureFetchAsync()
	structIR := objVal.Ty.StructIR()
	pGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", pGep, structIR, objVal.Ref, pIdx))
	pending := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", pending, pGep))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, pending))
	driveL := e.freshLabel("resp.drive")
	doneL := e.freshLabel("resp.drive.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, driveL))
	e.emitLabel(driveL)
	raw := e.emitOutAggCall("__kml_await_fetch_c", "{ i64, "+irPtr+", i64 }", irPtr+" "+pending)
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, "+irPtr+", i64 } %s, 1", body, raw))
	bodyLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, "+irPtr+", i64 } %s, 2", bodyLen, raw))
	bIdx, _, _ := objVal.Ty.FieldIndex("body")
	bGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", bGep, structIR, objVal.Ref, bIdx))
	e.heapStore(irPtr, body, bGep, 8)
	lIdx, _, _ := objVal.Ty.FieldIndex("bodyLength")
	lGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", lGep, structIR, objVal.Ref, lIdx))
	e.heapStore("i64", bodyLen, lGep, 8)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
}

// emitResponseArrayBuffer implements response.arrayBuffer() (ADR-00094):
// unlike .text()/.json(), which read body as a plain (strlen-bounded)
// string, this reads the real byte count from the bodyLength field
// __kml_await_fetch/__kml_pending_finish now thread through — so a binary
// body with an embedded null byte comes back whole. Hand-builds an
// ArrayBuffer header exactly like emitNewArrayBufferExpression
// (emit_arraybuffer.go) does, except it wraps the response's own already-
// buffered body pointer directly instead of calloc'ing a fresh one — no
// copy needed, the bytes are already there.
func (e *Emitter) emitResponseArrayBuffer(objVal Value, pos ast.Pos) (Value, error) {
	e.emitResponseDriveToDone(objVal)
	bodyIdx, bodyFieldTy, ok := objVal.Ty.FieldIndex("body")
	if !ok {
		return Value{}, internalAt(pos, "not a Response")
	}
	bodyVal := e.loadFieldValue(objVal, bodyIdx, bodyFieldTy)

	lenIdx, lenFieldTy, ok := objVal.Ty.FieldIndex("bodyLength")
	if !ok {
		return Value{}, internalAt(pos, "not a Response")
	}
	lenVal := e.loadFieldValue(objVal, lenIdx, lenFieldTy)

	hdrReg := e.freshReg()
	e.emitArrayBufferHeaderAlloc(hdrReg, 16, false, "malloc")
	e.heapStore("i64", lenVal.Ref, hdrReg, 8)
	dataSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", dataSlot, hdrReg))
	e.heapStore(irPtr, bodyVal.Ref, dataSlot, 8)

	return Value{Ref: hdrReg, Ty: ArrayBufferType()}, nil
}

// emitResponseCall dispatches a Response method call reached through the
// generic (non-declaration-context) path — text() always, json() when
// there's no surrounding typed declaration to parse into (falls back to
// TypePtr, matching bare JSON.parse's own default-context behavior), and
// arrayBuffer() (ADR-00094).
//
// Each returns a real Promise<T> (TDD-00186 Part B, ADR-00794): the body is
// buffered synchronously — emitResponseBody/emitResponseArrayBuffer drive the
// parked fetch to done — then wrapped in an already-settled task promise, so
// the observable return type matches WHATWG (`await r.json()`,
// `r.json().then(...)`, `Promise.all([r.json(), ...])`). A declaration's
// `await r.json()` (emitDeclJSONProjection) parses the body into the
// declared type through jsonTarget.
func (e *Emitter) emitResponseCall(objVal Value, method string, pos ast.Pos, jsonTarget ...Type) (Value, error) {
	var target *Type
	if len(jsonTarget) == 1 && method == "json" {
		target = &jsonTarget[0]
	}
	runner, innerTy, err := e.emitFetchBodyPromRunner(objVal.Ty, method, pos, target)
	if err != nil {
		return Value{}, err
	}
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
	e.ensureNanBox()

	q := e.emitAllocSettledPromise() // pending (state 0); the runner settles it

	// env = { ptr resp, ptr promise }
	env := e.freshReg()
	e.heapMallocInto(env, allocClosure, "16")
	e0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", e0, env))
	e.heapStore(irPtr, objVal.Ref, e0, 8)
	e1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", e1, env))
	e.heapStore(irPtr, q, e1, 8)

	// closure = { runner, env }
	clo := e.freshReg()
	c0 := e.freshReg()
	c1 := e.freshReg()
	e.emitClosureAlloc(clo, "16", runner)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 0", c0, clo))
	e.heapStore(irPtr, runner, c0, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 1", c1, clo))
	e.heapStore(irPtr, env, c1, 8)

	// Register keyed on the Response's in-flight fetch handle. If it's already
	// done (or null — body previously driven), register settles synchronously.
	pIdx, _, ok := objVal.Ty.FieldIndex("__kml_pending")
	if !ok {
		return Value{}, internalAt(pos, "not a Response")
	}
	pGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", pGep, objVal.Ty.StructIR(), objVal.Ref, pIdx))
	pending := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", pending, pGep))
	// A used or locked body rejects (Fetch's "unusable" body); else the body
	// is marked used.
	used := e.emitResponseBodyUsed(objVal)
	unusable := e.orStreamFlag(objVal, used, 32) // locked
	badL, goodL := e.freshLabel("resp.unusable"), e.freshLabel("resp.usable")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", unusable, badL, goodL))
	e.emitLabel(badL)
	te := e.buildErrorObj(errorKindIDs["TypeError"], e.internString("Body is unusable: Body has already been read"), e.internString("TypeError"))
	teBits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", teBits, te))
	e.emitRejectCaught(q, fmt.Sprint(kmlTagError), teBits)
	doneL := e.freshLabel("resp.bodyprom.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(goodL)
	{
		fIdx, _, _ := objVal.Ty.FieldIndex("__kml_bodyflags")
		fGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", fGep, objVal.Ty.StructIR(), objVal.Ref, fIdx))
		f := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", f, fGep))
		// A null body is never used.
		nb := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", nb, f, responseNullBody))
		isNB := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isNB, nb))
		bit := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %d", bit, isNB, responseBodyUsed))
		f2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", f2, f, bit))
		e.heapStore("i64", f2, fGep, 8)
	}
	// A `.body` stream already handed out is now the consumed one (a stream
	// body's is locked by the read itself).
	{
		sIdx, _, _ := objVal.Ty.FieldIndex("__kml_stream")
		sv := e.loadFieldValue(objVal, sIdx, TypePtr)
		fIdx, _, _ := objVal.Ty.FieldIndex("__kml_bodyflags")
		fl := e.loadFieldValue(objVal, fIdx, TypeI64)
		sb := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", sb, fl.Ref, responseStreamBody))
		notStream := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", notStream, sb))
		e.emitLockStreamIf(notStream, sv.Ref)
	}
	// A stream body (a constructed Response's) is read to its end first.
	e.respCollectCalled = e.respCollectCalled || objVal.Ty.IsResponse()
	fIdx, _, _ := objVal.Ty.FieldIndex("__kml_bodyflags")
	flags := e.loadFieldValue(objVal, fIdx, TypeI64)
	sb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", sb, flags.Ref, responseStreamBody))
	isStream := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isStream, sb))
	collectL, regL, joinL := e.freshLabel("resp.collect"), e.freshLabel("resp.register"), e.freshLabel("resp.bodyprom")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStream, collectL, regL))
	e.emitLabel(collectL)
	if objVal.Ty.IsResponse() { // a Request's body is never a stream
		e.emitInstr(fmt.Sprintf("call void @__kml_resp_collect("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s)", objVal.Ref, clo, q))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(regL)
	e.emitInstr(fmt.Sprintf("call void @__kml_fetch_bodyprom_register("+irPtr+" %s, "+irPtr+" %s)", pending, clo))
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)

	qt := PromiseOf(innerTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

// emitRejectWithThrown rejects task promise q with the value just caught.
func (e *Emitter) emitRejectWithThrown(q string) {
	pay, tag := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_get_thrown_pay()", pay))
	e.emitInstr(fmt.Sprintf("%s = call i8 @__kml_get_thrown_tag()", tag))
	e.emitRejectCaught(q, tag, pay)
}

// emitRejectCaught rejects task promise q with the caught value (tag, pay).
func (e *Emitter) emitRejectCaught(q, tag, pay string) {
	e.emitRejectPromise(q, tag, true, pay)
}

// emitResponseBodyUsed is response.bodyUsed: a body method has run, or the
// body stream has been read.
func (e *Emitter) emitResponseBodyUsed(objVal Value) string {
	fIdx, _, _ := objVal.Ty.FieldIndex("__kml_bodyflags")
	flags := e.loadFieldValue(objVal, fIdx, TypeI64)
	u := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", u, flags.Ref, responseBodyUsed))
	used := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", used, u))
	return e.orStreamFlag(objVal, used, 16) // disturbed
}

// orStreamFlag is `acc || (the Response's body stream has flag bit)`.
func (e *Emitter) orStreamFlag(objVal Value, acc string, bit int) string {
	sIdx, _, _ := objVal.Ty.FieldIndex("__kml_stream")
	s := e.loadFieldValue(objVal, sIdx, TypePtr)
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", has, s.Ref))
	// A null stream reads a zero flags word from a private zero cell.
	e.ensureZeroCell()
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" @__kml_zero_cell", sp, has, s.Ref))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 11", fp, rstreamStructIR, sp))
	f := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", f, fp))
	b := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", b, f, bit))
	set := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", set, b))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", r, acc, set))
	return r
}

// ensureZeroCell defines @__kml_zero_cell, a zeroed ReadableStream-sized
// block a null stream's field reads resolve to.
func (e *Emitter) ensureZeroCell() {
	if e.usedZeroCell {
		return
	}
	e.usedZeroCell = true
	e.emitGlobal(fmt.Sprintf("@__kml_zero_cell = internal"+irGlobalAS+" constant [%d x i8] zeroinitializer, align 8", rstreamStructSize))
}

// emitFetchBodyPromRunner synthesizes (once per method) the settle runner
// `void @__kml_fetch_bodyprom_run_<method>(ptr %env)` invoked when the body is
// complete: it builds the value (reusing the eager text/json/arrayBuffer
// emitters — the drive-to-done is trivial since the fetch is already done) under
// a setjmp guard and settles the promise fulfilled, or — for a JSON parse error
// — rejected. Returns the runner name and the promise's inner value type.
func (e *Emitter) emitFetchBodyPromRunner(objTy Type, method string, pos ast.Pos, jsonTarget *Type) (string, Type, error) {
	respTy := objTy // a Response or a Request
	reader, ok := bodyReaders[method]
	if !ok {
		return "", Type{}, internalAt(pos, "unknown Response method '%s'", method)
	}
	innerTy := reader.inner(respTy, jsonTarget)
	if method == "blob" {
		cls, ok := e.globalClass("Blob")
		if !ok {
			return "", Type{}, internalAt(pos, "blob() needs Blob")
		}
		innerTy = cls
	}
	if method == "formData" {
		cls, ok := e.globalClass("FormData")
		if !ok {
			return "", Type{}, internalAt(pos, "formData() needs FormData")
		}
		innerTy = cls
	}

	if e.fetchBodyPromRunner == nil {
		e.fetchBodyPromRunner = map[string]string{}
	}
	key := method
	if objTy.IsFetchRequest() {
		key += "_req"
	}
	name := "@__kml_fetch_bodyprom_run_" + key
	if jsonTarget != nil {
		// one runner per typed call site: its parse is specialised to T
		e.fetchBodyPromTypedCtr++
		name = fmt.Sprintf("@__kml_fetch_bodyprom_run_json_t%d", e.fetchBodyPromTypedCtr)
	} else {
		if cached, ok := e.fetchBodyPromRunner[key]; ok {
			return cached, innerTy, nil
		}
		e.fetchBodyPromRunner[key] = name
	}
	e.ensureExceptionHelpers()

	restoreBuilder := e.beginThunkEmit()

	build := func() error {
		respP := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %%env, i32 0, i32 0", respP))
		resp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", resp, respP))
		qP := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+" }, "+irPtr+" %%env, i32 0, i32 1", qP))
		q := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", q, qP))
		objVal := Value{Ref: resp, Ty: respTy}

		tryL := e.freshLabel("fbp.try")
		catchL := e.freshLabel("fbp.catch")
		e.excEnter(catchL, tryL)

		e.emitLabel(tryL)
		val, err := reader.read(e, objVal, innerTy, pos)
		if err != nil {
			return err
		}
		e.excLeave()
		if jsonTarget != nil {
			e.storePromiseValue(q, val) // any T, arrays included (two value words)
		} else {
			bits := e.promiseBitsOf(val)
			vSlot := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 2", vSlot, promiseStructIR, q))
			e.heapStore("i64", bits, vSlot, 8)
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle("+irPtr+" %s, i64 1)", q))
		e.emitTerminator("ret void")

		e.emitLabel(catchL)
		// The reason as thrown: payload in v0, tag in v1.
		e.emitRejectWithThrown(q)
		e.emitTerminator("ret void")
		return nil
	}
	buildErr := build()
	if buildErr == nil {
		e.writeFunc(fmt.Sprintf("void %s("+irPtr+" %%env)", name), e.allocas.String()+e.body.String())
	}
	restoreBuilder()
	return name, innerTy, buildErr
}

// The Body mixin's readers on a Response or a fetch Request, reached
// through their `@intrinsic` declarations.
func init() {
	for _, name := range []string{"text", "json", "arrayBuffer", "bytes", "blob", "formData"} {
		name := name
		intrinsics["Body.prototype."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				mem := ex.Callee.(*ast.MemberExpression)
				objVal, err := e.emitExpr(mem.Object)
				if err != nil {
					return Value{}, err
				}
				if ty, ok := e.callAssertedTargetTy(ex); ok && name == "json" {
					// `res.json() as T` supplies the parse target, as
					// `JSON.parse(s) as T` does; the result is still a
					// Promise<T> (TDD-00186 Part B).
					return e.emitResponseCall(objVal, name, ex.GetPos(), ty)
				}
				return e.emitResponseCall(objVal, name, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				if !hasBodyMixin(e.typeOfExpr(ex.Callee.(*ast.MemberExpression).Object)) {
					return Type{}
				}
				t, _ := e.intrinsicMethodType(bodyMethodTypes, ex)
				return t
			},
		}
	}
}

// bodyReader is a Body mixin reader (text, json, arrayBuffer): the value its
// promise settles to, and how that value is read from a completed body.
type bodyReader struct {
	inner func(respTy Type, jsonTarget *Type) Type
	read  func(e *Emitter, objVal Value, innerTy Type, pos ast.Pos) (Value, error)
}

var bodyReaders map[string]bodyReader

func init() {
	bodyReaders = map[string]bodyReader{
		"text": {
			inner: func(respTy Type, _ *Type) Type {
				_, bodyTy, _ := respTy.FieldIndex("body")
				return bodyTy
			},
			read: func(e *Emitter, objVal Value, _ Type, pos ast.Pos) (Value, error) {
				return e.emitResponseBody(objVal, pos)
			},
		},
		"json": {
			// `res.json() as T`: the same lazy promise, parsed into T when the
			// body completes — never a synchronous drive of the transfer
			// (TDD-00223).
			inner: func(_ Type, jsonTarget *Type) Type {
				if jsonTarget != nil {
					return *jsonTarget
				}
				return TypeAny
			},
			read: func(e *Emitter, objVal Value, innerTy Type, pos ast.Pos) (Value, error) {
				body, err := e.emitResponseBody(objVal, pos)
				if err != nil {
					return Value{}, err
				}
				return e.emitJSONParseValue(body, innerTy, pos)
			},
		},
		"arrayBuffer": {
			inner: func(Type, *Type) Type { return ArrayBufferType() },
			read: func(e *Emitter, objVal Value, _ Type, pos ast.Pos) (Value, error) {
				return e.emitResponseArrayBuffer(objVal, pos)
			},
		},
		// blob(): a Blob of arrayBuffer()'s bytes, typed by the
		// Content-Type header ("" without one).
		"blob": {
			inner: func(Type, *Type) Type { return Type{} }, // the Blob class (blobInner)
			read: func(e *Emitter, objVal Value, _ Type, pos ast.Pos) (Value, error) {
				ab, err := e.emitResponseArrayBuffer(objVal, pos)
				if err != nil {
					return Value{}, err
				}
				const tmpAB, tmpObj = "__kml_body_blob_ab", "__kml_body_blob_obj"
				e.pushScope()
				defer e.popScope()
				for _, b := range []struct {
					name string
					v    Value
				}{{tmpAB, ab}, {tmpObj, objVal}} {
					slot := e.freshReg()
					e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
					e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", b.v.Ref, slot))
					e.define(b.name, Symbol{Ptr: slot, Ty: b.v.Ty})
				}
				ct := ast.NewBinaryExpression("??", ast.NewCallExpression(ast.NewMemberExpression(
					ast.NewMemberExpression(ast.NewIdentifier(tmpObj, pos), "headers", pos), "get", pos),
					[]ast.Expression{ast.NewStringLiteral("content-type", pos)}, pos), ast.NewStringLiteral("", pos), pos)
				opts := ast.NewObjectLiteral([]ast.ObjectProperty{{Key: "type", Value: ct}}, pos)
				return e.emitExpr(ast.NewNewExpression(e.globalLinks["Blob"], []ast.Expression{
					ast.NewArrayLiteral([]ast.Expression{ast.NewIdentifier(tmpAB, pos)}, pos), opts}, pos))
			},
		},
		// formData(): FormData's parse of arrayBuffer()'s bytes, by the
		// Content-Type header (multipart/form-data or urlencoded).
		"formData": {
			inner: func(Type, *Type) Type { return Type{} }, // the FormData class
			read: func(e *Emitter, objVal Value, _ Type, pos ast.Pos) (Value, error) {
				ab, err := e.emitResponseArrayBuffer(objVal, pos)
				if err != nil {
					return Value{}, err
				}
				const tmpAB, tmpObj = "__kml_body_fd_ab", "__kml_body_fd_obj"
				e.pushScope()
				defer e.popScope()
				for _, b := range []struct {
					name string
					v    Value
				}{{tmpAB, ab}, {tmpObj, objVal}} {
					slot := e.freshReg()
					e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
					e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", b.v.Ref, slot))
					e.define(b.name, Symbol{Ptr: slot, Ty: b.v.Ty})
				}
				ct := ast.NewBinaryExpression("??", ast.NewCallExpression(ast.NewMemberExpression(
					ast.NewMemberExpression(ast.NewIdentifier(tmpObj, pos), "headers", pos), "get", pos),
					[]ast.Expression{ast.NewStringLiteral("content-type", pos)}, pos), ast.NewStringLiteral("", pos), pos)
				return e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier(e.globalLinks["FormData"], pos), "kmlParse", pos),
					[]ast.Expression{ast.NewIdentifier(tmpAB, pos), ct}, pos))
			},
		},
		// bytes(): a Uint8Array over arrayBuffer()'s bytes.
		"bytes": {
			inner: func(Type, *Type) Type { return TypedArrayType("uint8") },
			read: func(e *Emitter, objVal Value, _ Type, pos ast.Pos) (Value, error) {
				ab, err := e.emitResponseArrayBuffer(objVal, pos)
				if err != nil {
					return Value{}, err
				}
				const tmp = "__kml_body_bytes"
				e.pushScope()
				defer e.popScope()
				slot := e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
				e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", ab.Ref, slot))
				e.define(tmp, Symbol{Ptr: slot, Ty: ab.Ty})
				return e.emitExpr(ast.NewNewTypedArrayExpression("uint8", ast.NewIdentifier(tmp, pos), pos))
			},
		},
	}
}

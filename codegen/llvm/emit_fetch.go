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
	"strings"

	"KlainMainLang/ast"
)

// hasBodyMixin reports a type with Fetch's Body members (body, bodyUsed,
// text(), json(), arrayBuffer()): a Response or a Request.
func hasBodyMixin(t Type) bool {
	return t.IsResponse || t.IsFetchRequest
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
	if len(args) != 1 && len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: fetch takes 1 argument (url or Request) or 2 (url, init)", pos.Line, pos.Col)
	}

	if len(args) == 1 {
		if reqTy := e.inferExprType(args[0]); reqTy.IsFetchRequest {
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
			return e.emitFetchAsyncCall(urlVal.Ref, methodVal.Ref, headersRef, bodyVal.Ref, "null")
		}
	}

	urlVal, err := e.emitExpr(stringifiedInput(args[0]))
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

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
		if !initVal.Ty.IsObject && !dyn {
			return Value{}, fmt.Errorf("%d:%d: fetch's second argument must be an object with an optional method/headers/body field", pos.Line, pos.Col)
		}
		dynGet := func(name string) Value {
			v, _ := e.emitDynAnyMemberGetNamed(initVal, e.internString(name), name, pos)
			return v
		}
		// An optional init (`init?: RequestInit`) may be absent: its parts
		// are read only when it is there, and default to none.
		var slots [5]string
		var skipL, doneInitL string
		if initVal.Ty.Nullable || dyn {
			for i := range slots {
				slots[i] = e.freshReg()
				ir := "ptr"
				if i == 4 {
					ir = "i64"
				}
				e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slots[i], ir))
			}
			for i := 0; i < 4; i++ {
				e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slots[i]))
			}
			e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", slots[4]))
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
				nn.Nullable, nn.IsUndefined = false, false
				initVal = Value{Ref: initVal.Ref, Ty: nn}
			}
		}
		if dyn {
			// signal: an AbortSignal, or none when undefined/null.
			if cls, hasSignals := e.abortSignalClass(); hasSignals {
				sv := dynGet("signal")
				sig := e.coerce(sv, cls)
				nullish := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp ule i64 %s, %d", nullish, sv.Ref, nbUndefined))
				sel := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", sel, nullish, sig.Ref))
				signalRef = sel
			}
			mv := dynGet("method")
			ms, _ := e.emitAnyIntoString(mv, TypePtr)
			isUndef, sel := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUndef, mv.Ref, nbUndefined))
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", sel, isUndef, ms.Ref))
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
				return Value{}, fmt.Errorf("%d:%d: fetch's init.signal must be an AbortSignal", pos.Line, pos.Col)
			}
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("method"); ok && !dyn {
			mv := e.loadFieldValue(initVal, idx, fieldTy)
			switch {
			case isStringTy(mv.Ty) && !mv.Ty.IsObject && !mv.Ty.IsArray:
				methodRef = mv.Ref
			case mv.Ty.IsDynamic:
				s, _ := e.emitAnyIntoString(mv, TypePtr)
				methodRef = s.Ref
			default:
				return Value{}, fmt.Errorf("%d:%d: fetch's init.method must be a string", pos.Line, pos.Col)
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
				return Value{}, fmt.Errorf("%d:%d: fetch's init.body: %v", pos.Line, pos.Col, err)
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
			has := e.coerce(hasV, TypeBool).Ref
			hasBody, need := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasBody, bodyRef))
			noCT := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", noCT, has))
			e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", need, noCT, hasBody))
			addL, joinL := e.freshLabel("fetch.noct"), e.freshLabel("fetch.ctdone")
			lslot := e.freshReg()
			e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", lslot))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", headersRef, lslot))
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", need, addL, joinL))
			e.emitLabel(addL)
			e.ensureCurlSlist()
			l2 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @curl_slist_append(ptr %s, ptr %s)", l2, headersRef, e.internString("Content-Type:")))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", l2, lslot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
			e.emitLabel(joinL)
			l3 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", l3, lslot))
			headersRef = l3
		}
		if skipL != "" {
			for i, r := range []string{methodRef, headersRef, bodyRef, signalRef} {
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", r, slots[i]))
			}
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bodyLen, slots[4]))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneInitL))
			e.emitLabel(skipL)
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneInitL))
			e.emitLabel(doneInitL)
			refs := make([]string, 5)
			for i := range refs {
				refs[i] = e.freshReg()
				ir := "ptr"
				if i == 4 {
					ir = "i64"
				}
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", refs[i], ir, slots[i]))
			}
			methodRef, headersRef, bodyRef, signalRef, bodyLen = refs[0], refs[1], refs[2], refs[3], refs[4]
		}
	}

	return e.emitFetchAsyncCallN(urlVal.Ref, methodRef, headersRef, bodyRef, bodyLen, signalRef)
}

// emitFetchAsyncCallN is emitFetchAsyncCall with the body's byte length (-1:
// a NUL-terminated string).
func (e *Emitter) emitFetchAsyncCallN(urlRef, methodRef, headersRef, bodyRef, bodyLen, signalRef string) (Value, error) {
	e.ensureFetchAsync()
	pendingReg := e.freshReg()
	if bodyRef == "null" {
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_async_n(ptr %s, ptr %s, ptr %s, ptr %s, i64 %s, ptr %s)",
			pendingReg, urlRef, methodRef, headersRef, bodyRef, bodyLen, signalRef))
		return e.finishFetchPending(pendingReg)
	}
	// A body on a GET or HEAD request (the default method is GET) rejects
	// with Node's TypeError, and nothing is sent.
	e.ensureStrcasecmp()
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	hasBody, noMethod := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasBody, bodyRef))
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", noMethod, methodRef))
	m := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", m, noMethod, e.internString("GET"), methodRef))
	g, h, gz, hz, gh, bad := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp(ptr %s, ptr %s)", g, m, e.internString("GET")))
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcasecmp(ptr %s, ptr %s)", h, m, e.internString("HEAD")))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", gz, g))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", hz, h))
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", gh, gz, hz))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", bad, gh, hasBody))
	badL, goL, doneL := e.freshLabel("fetch.getbody"), e.freshLabel("fetch.go"), e.freshLabel("fetch.started")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, goL))
	e.emitLabel(badL)
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_failed_pending(i64 -2)", fp))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", fp, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(goL)
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_async_n(ptr %s, ptr %s, ptr %s, ptr %s, i64 %s, ptr %s)",
		p, urlRef, methodRef, headersRef, bodyRef, bodyLen, signalRef))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pendingReg, slot))
	return e.finishFetchPending(pendingReg)
}

// emitFetchAsyncCall is the shared tail every fetch() call form (bare url,
// url+init, or a Request object) reduces to once url/method/headers/body
// refs are resolved: call __kml_fetch_async, box the returned pending
// handle into a fresh Promise<Response> slot.
func (e *Emitter) emitFetchAsyncCall(urlRef, methodRef, headersRef, bodyRef, signalRef string) (Value, error) {
	e.ensureFetchAsync()
	pendingReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_async(ptr %s, ptr %s, ptr %s, ptr %s, ptr %s)",
		pendingReg, urlRef, methodRef, headersRef, bodyRef, signalRef))
	return e.finishFetchPending(pendingReg)
}

// finishFetchPending boxes a pending fetch handle into a Promise<Response>
// slot.
func (e *Emitter) finishFetchPending(pendingReg string) (Value, error) {
	e.ensureMalloc()
	slotReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", slotReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", pendingReg, slotReg))
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
	if isRecordView(objVal.Ty) && !objVal.Ty.Nullable && idx < len(objVal.Ty.Fields) {
		gep = e.emitRecordGep(objVal.Ref, objVal.Ty, idx, fieldTy, objVal.Ty.Fields[idx].Name)
	} else {
		gep = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	}
	if isNullableScalar(fieldTy) {
		// Its { i1, T } storage.
		return e.loadScalarOrNullableField(gep, fieldTy)
	}
	if fieldTy.IsArray {
		// Its header-pointer slot (TDD-00213 Stage 2).
		return e.loadArrayFieldValue(gep, fieldTy)
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, fieldTy.IR, gep, fieldTy.Align()))
	return Value{Ref: r, Ty: fieldTy}
}

// buildFetchHeaderList is the curl_slist (ADR-00074/TDD-00017) of a
// Map<string,string>'s entries (XMLHttpRequest's and http's header maps).
func (e *Emitter) buildFetchHeaderList(mapPtr string) (string, error) {
	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(mapPtr, "str", Type{IR: "ptr"})
	return e.buildCurlSlist(keysLen, func(i string) (string, string) {
		return e.loadPtrAt(keysPtr, i), e.loadPtrAt(valsPtr, i)
	})
}

// buildCurlSlist is a curl_slist of n "name: value" lines, entry(i) giving
// line i's name and value. No lines is a null list.
func (e *Emitter) buildCurlSlist(n string, entry func(i string) (name, value string)) (string, error) {
	e.ensureCurlSlist()
	listAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", listAlloca))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", listAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("fetchheaders.cond")
	bodyL := e.freshLabel("fetchheaders.body")
	doneL := e.freshLabel("fetchheaders.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal, isDone := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
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
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curList, listAlloca))
	e.emitInstr(fmt.Sprintf("%s = call ptr @curl_slist_append(ptr %s, ptr %s)", newList, curList, line2.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newList, listAlloca))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	finalList := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", finalList, listAlloca))
	return finalList, nil
}

// emitResponseBody extracts a Response value's buffered body string (a
// plain GEP+load of its "body" field — factored out since both text() and
// json() need the same raw string before doing anything method-specific).
func (e *Emitter) emitResponseBody(objVal Value, pos ast.Pos) (Value, error) {
	e.emitResponseDriveToDone(objVal)
	idx, fieldTy, ok := objVal.Ty.FieldIndex("body")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: not a Response", pos.Line, pos.Col)
	}
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, fieldTy.IR, gep, fieldTy.Align()))
	// TDD-00120: the raw body field is the dual-use curl buffer (also .body /
	// .arrayBuffer()), with no length header. Hand text()/json() a length-prefixed
	// copy (strlen-bounded, matching text()'s existing NUL semantics). A
	// Request without a body has a null one: "".
	e.ensureStrHeaderRuntime()
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, r))
	src := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", src, isNull, e.internString(""), r))
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", rc, src))
	return Value{Ref: rc, Ty: fieldTy}, nil
}

// emitResponseDriveToDone (TDD-00097 Stage 4): a headers-resolved Response
// carries its pending handle; before any buffered body read, drive the
// transfer to completion and cache body/bodyLength into the object (clearing
// the handle so repeat reads are plain field loads).
func (e *Emitter) emitResponseDriveToDone(objVal Value) {
	pIdx, _, ok := objVal.Ty.FieldIndex("__kml_pending")
	if !ok {
		return
	}
	e.ensureFetchAsync()
	structIR := objVal.Ty.StructIR()
	pGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", pGep, structIR, objVal.Ref, pIdx))
	pending := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pending, pGep))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, pending))
	driveL := e.freshLabel("resp.drive")
	doneL := e.freshLabel("resp.drive.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, driveL))
	e.emitLabel(driveL)
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { i64, ptr, i64 } @__kml_await_fetch(ptr %s)", raw, pending))
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr, i64 } %s, 1", body, raw))
	bodyLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr, i64 } %s, 2", bodyLen, raw))
	bIdx, _, _ := objVal.Ty.FieldIndex("body")
	bGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", bGep, structIR, objVal.Ref, bIdx))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", body, bGep))
	lIdx, _, _ := objVal.Ty.FieldIndex("bodyLength")
	lGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", lGep, structIR, objVal.Ref, lIdx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bodyLen, lGep))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", pGep))
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
		return Value{}, fmt.Errorf("%d:%d: not a Response", pos.Line, pos.Col)
	}
	bodyVal := e.loadFieldValue(objVal, bodyIdx, bodyFieldTy)

	lenIdx, lenFieldTy, ok := objVal.Ty.FieldIndex("bodyLength")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: not a Response", pos.Line, pos.Col)
	}
	lenVal := e.loadFieldValue(objVal, lenIdx, lenFieldTy)

	e.ensureMalloc()
	hdrReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdrReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenVal.Ref, hdrReg))
	dataSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, hdrReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", bodyVal.Ref, dataSlot))

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
// `r.json().then(...)`, `Promise.all([r.json(), ...])`). The declaration-context
// json() fast path (emitResponseJSON via emitDeclJSONProjection) still parses
// straight into the declared type without the promise box — `await` there is
// stripped before dispatch, so `const p: T = await r.json()` is unaffected.
func (e *Emitter) emitResponseCall(objVal Value, method string, pos ast.Pos, jsonTarget ...Type) (Value, error) {
	var target *Type
	if len(jsonTarget) == 1 && method == "json" {
		target = &jsonTarget[0]
	}
	runner, innerTy, err := e.emitFetchBodyPromRunner(objVal.Ty, method, pos, target)
	if err != nil {
		return Value{}, err
	}
	e.ensureFetchBodyProm()
	e.ensureMalloc()

	q := e.emitAllocSettledPromise() // pending (state 0); the runner settles it

	// env = { ptr resp, ptr promise }
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", env))
	e0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", e0, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objVal.Ref, e0))
	e1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", e1, env))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", q, e1))

	// closure = { runner, env }
	clo := e.freshReg()
	c0 := e.freshReg()
	c1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", clo))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 0", c0, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", runner, c0))
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", c1, clo))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", env, c1))

	// Register keyed on the Response's in-flight fetch handle. If it's already
	// done (or null — body previously driven), register settles synchronously.
	pIdx, _, ok := objVal.Ty.FieldIndex("__kml_pending")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: not a Response", pos.Line, pos.Col)
	}
	pGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", pGep, objVal.Ty.StructIR(), objVal.Ref, pIdx))
	pending := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pending, pGep))
	// A used or locked body rejects (Fetch's "unusable" body); else the body
	// is marked used.
	used := e.emitResponseBodyUsed(objVal)
	unusable := e.orStreamFlag(objVal, used, 32) // locked
	badL, goodL := e.freshLabel("resp.unusable"), e.freshLabel("resp.usable")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", unusable, badL, goodL))
	e.emitLabel(badL)
	e.ensureExceptionHelpers()
	te := e.buildErrorObj(errorKindIDs["TypeError"], e.internString("Body is unusable: Body has already been read"), e.internString("TypeError"))
	teBits := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", teBits, te))
	e.emitRejectCaught(q, fmt.Sprint(kmlTagError), teBits)
	doneL := e.freshLabel("resp.bodyprom.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(goodL)
	{
		fIdx, _, _ := objVal.Ty.FieldIndex("__kml_bodyflags")
		fGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", fGep, objVal.Ty.StructIR(), objVal.Ref, fIdx))
		f := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", f, fGep))
		// A null body is never used.
		nb := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", nb, f, responseNullBody))
		isNB := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isNB, nb))
		bit := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %d", bit, isNB, responseBodyUsed))
		f2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %s", f2, f, bit))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", f2, fGep))
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
	e.respCollectCalled = e.respCollectCalled || objVal.Ty.IsResponse
	fIdx, _, _ := objVal.Ty.FieldIndex("__kml_bodyflags")
	flags := e.loadFieldValue(objVal, fIdx, TypeI64)
	sb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", sb, flags.Ref, responseStreamBody))
	isStream := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isStream, sb))
	collectL, regL, joinL := e.freshLabel("resp.collect"), e.freshLabel("resp.register"), e.freshLabel("resp.bodyprom")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isStream, collectL, regL))
	e.emitLabel(collectL)
	if objVal.Ty.IsResponse { // a Request's body is never a stream
		e.emitInstr(fmt.Sprintf("call void @__kml_resp_collect(ptr %s, ptr %s, ptr %s)", objVal.Ref, clo, q))
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(regL)
	e.emitInstr(fmt.Sprintf("call void @__kml_fetch_bodyprom_register(ptr %s, ptr %s)", pending, clo))
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
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", has, s.Ref))
	// A null stream reads a zero flags word from a private zero cell.
	e.ensureZeroCell()
	sp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr @__kml_zero_cell", sp, has, s.Ref))
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 11", fp, rstreamStructIR, sp))
	f := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", f, fp))
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
	e.emitGlobal(fmt.Sprintf("@__kml_zero_cell = internal constant [%d x i8] zeroinitializer, align 8", rstreamStructSize))
}

// emitFetchBodyPromRunner synthesizes (once per method) the settle runner
// `void @__kml_fetch_bodyprom_run_<method>(ptr %env)` invoked when the body is
// complete: it builds the value (reusing the eager text/json/arrayBuffer
// emitters — the drive-to-done is trivial since the fetch is already done) under
// a setjmp guard and settles the promise fulfilled, or — for a JSON parse error
// — rejected. Returns the runner name and the promise's inner value type.
func (e *Emitter) emitFetchBodyPromRunner(objTy Type, method string, pos ast.Pos, jsonTarget *Type) (string, Type, error) {
	respTy := objTy // a Response or a Request
	var innerTy Type
	switch method {
	case "text":
		_, bodyTy, _ := respTy.FieldIndex("body")
		innerTy = bodyTy
	case "json":
		innerTy = TypeAny
		if jsonTarget != nil {
			// `res.json() as T`: the same lazy promise, parsed into T when the body
			// completes — never a synchronous drive of the transfer (TDD-00223).
			innerTy = *jsonTarget
		}
	case "arrayBuffer":
		innerTy = ArrayBufferType()
	default:
		return "", Type{}, fmt.Errorf("%d:%d: unknown Response method '%s'", pos.Line, pos.Col, method)
	}

	if e.fetchBodyPromRunner == nil {
		e.fetchBodyPromRunner = map[string]string{}
	}
	key := method
	if objTy.IsFetchRequest {
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

	savedAllocas := e.allocas
	savedBody := e.body
	savedRegCtr := e.regCtr
	savedBlockDone := e.blockDone
	e.allocas = strings.Builder{}
	e.body = strings.Builder{}
	e.regCtr = 0
	e.blockDone = false

	build := func() error {
		respP := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 0", respP))
		resp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", resp, respP))
		qP := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %%env, i32 0, i32 1", qP))
		q := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", q, qP))
		objVal := Value{Ref: resp, Ty: respTy}

		jb := e.freshReg()
		sj := e.freshReg()
		thr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_push_jmpbuf()", jb))
		e.emitInstr(fmt.Sprintf("%s = %s", sj, e.setjmpCall(jb)))
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", thr, sj))
		tryL := e.freshLabel("fbp.try")
		catchL := e.freshLabel("fbp.catch")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", thr, catchL, tryL))

		e.emitLabel(tryL)
		var val Value
		var err error
		switch method {
		case "text":
			val, err = e.emitResponseBody(objVal, pos)
		case "json":
			body, berr := e.emitResponseBody(objVal, pos)
			if berr != nil {
				return berr
			}
			val, err = e.emitJSONParseValue(body, innerTy, pos)
		case "arrayBuffer":
			val, err = e.emitResponseArrayBuffer(objVal, pos)
		}
		if err != nil {
			return err
		}
		e.emitInstr("call void @__kml_pop_jmpbuf()")
		if jsonTarget != nil {
			e.storePromiseValue(q, val) // any T, arrays included (two value words)
		} else {
			bits := e.promiseBitsOf(val)
			vSlot := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", vSlot, promiseStructIR, q))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bits, vSlot))
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 1)", q))
		e.emitTerminator("ret void")

		e.emitLabel(catchL)
		// The reason as thrown: payload in v0, tag in v1.
		e.emitRejectWithThrown(q)
		e.emitTerminator("ret void")
		return nil
	}
	buildErr := build()
	if buildErr == nil {
		e.functions.WriteString(fmt.Sprintf("\ndefine void %s(ptr %%env) {\nentry:\n", name))
		e.functions.WriteString(e.allocas.String())
		e.functions.WriteString(e.body.String())
		e.functions.WriteString("}\n")
	}
	e.allocas = savedAllocas
	e.body = savedBody
	e.regCtr = savedRegCtr
	e.blockDone = savedBlockDone
	return name, innerTy, buildErr
}

// emitResponseJSON is response.json()'s declaration-context analogue of
// JSON.parse's own special-casing (emit_call.go/emit_objects.go): evaluates
// objExpr (the Response receiver, any expression — a variable, a chained
// await, etc.), extracts its body, and parses it into targetTy so
// `const p: Point = response.json()` deserializes into the declared type
// instead of defaulting to a plain string.
func (e *Emitter) emitResponseJSON(objExpr ast.Expression, targetTy Type, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	bodyVal, err := e.emitResponseBody(objVal, pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitJSONParseValue(bodyVal, targetTy, pos)
}

// The Body mixin's readers on a Response or a fetch Request, reached
// through their `@intrinsic` declarations.
func init() {
	for _, name := range []string{"text", "json", "arrayBuffer"} {
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
				t, _ := e.inferMethodNameType(ex, ex.Callee.(*ast.MemberExpression))
				return t
			},
		}
	}
}

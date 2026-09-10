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

// isResponseMethodName reports whether name is one of Response's dispatched
// methods. status/ok/body are plain object fields (already handled by the
// generic object field-read path) and need no entry here.
func isResponseMethodName(name string) bool {
	switch name {
	case "text", "json", "arrayBuffer":
		return true
	}
	return false
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
			headersIdx, headersFieldTy, _ := reqTy.FieldIndex("headers")
			bodyIdx, bodyFieldTy, _ := reqTy.FieldIndex("body")

			urlVal := e.loadFieldValue(reqVal, urlIdx, urlFieldTy)
			methodVal := e.loadFieldValue(reqVal, methodIdx, methodFieldTy)
			headersVal := e.loadFieldValue(reqVal, headersIdx, headersFieldTy)
			bodyVal := e.loadFieldValue(reqVal, bodyIdx, bodyFieldTy)

			headersRef, err := e.buildFetchHeaderList(headersVal.Ref)
			if err != nil {
				return Value{}, err
			}
			return e.emitFetchAsyncCall(urlVal.Ref, methodVal.Ref, headersRef, bodyVal.Ref, "null")
		}
	}

	urlVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

	methodRef, headersRef, bodyRef, signalRef := "null", "null", "null", "null"
	if len(args) == 2 {
		initVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		if !initVal.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: fetch's second argument must be an object with an optional method/headers/body field", pos.Line, pos.Col)
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("signal"); ok {
			if !fieldTy.IsAbortSignal {
				return Value{}, fmt.Errorf("%d:%d: fetch's init.signal must be an AbortSignal", pos.Line, pos.Col)
			}
			signalRef = e.loadFieldValue(initVal, idx, fieldTy).Ref
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("method"); ok {
			if !isPlainStringType(fieldTy) {
				return Value{}, fmt.Errorf("%d:%d: fetch's init.method must be a string", pos.Line, pos.Col)
			}
			methodRef = e.loadFieldValue(initVal, idx, fieldTy).Ref
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("headers"); ok {
			if !fieldTy.IsMap || fieldTy.MapKey == nil || fieldTy.MapVal == nil ||
				!isPlainStringType(*fieldTy.MapKey) || !isPlainStringType(*fieldTy.MapVal) {
				return Value{}, fmt.Errorf("%d:%d: fetch's init.headers must be Map<string, string> or Headers", pos.Line, pos.Col)
			}
			mapVal := e.loadFieldValue(initVal, idx, fieldTy)
			headersRef, err = e.buildFetchHeaderList(mapVal.Ref)
			if err != nil {
				return Value{}, err
			}
		}
		if idx, fieldTy, ok := initVal.Ty.FieldIndex("body"); ok {
			if !isPlainStringType(fieldTy) {
				return Value{}, fmt.Errorf("%d:%d: fetch's init.body must be a string", pos.Line, pos.Col)
			}
			bodyRef = e.loadFieldValue(initVal, idx, fieldTy).Ref
		}
	}

	return e.emitFetchAsyncCall(urlVal.Ref, methodRef, headersRef, bodyRef, signalRef)
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
func (e *Emitter) loadFieldValue(objVal Value, idx int, fieldTy Type) Value {
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, fieldTy.IR, gep, fieldTy.Align()))
	return Value{Ref: r, Ty: fieldTy}
}

// buildFetchHeaderList builds a struct curl_slist* (ADR-00074/TDD-00017)
// from a Map<string,string> value, one curl_slist_append call per entry —
// walks the same {ptr, i64} key/value arrays map.entries()/map.forEach()
// already use (emit_collections.go's mapKeysAndVals), building each
// "key: value" line with the existing emitStringConcat helper
// (emit_strings.go). A runtime-empty map naturally produces a null list
// (the loop just runs zero iterations) — no separate empty-map case needed.
func (e *Emitter) buildFetchHeaderList(mapPtr string) (string, error) {
	e.ensureCurlSlist()
	keysPtr, keysLen, valsPtr := e.mapKeysAndVals(mapPtr, true)

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
	idxVal := e.freshReg()
	isDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", isDone, idxVal, keysLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDone, doneL, bodyL))

	e.emitLabel(bodyL)
	keyGep, keyVal := e.freshReg(), e.freshReg()
	valGep, valVal := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", keyGep, keysPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", keyVal, keyGep))
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", valGep, valsPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", valVal, valGep))

	sep := e.internString(": ")
	line1, err := e.emitStringConcat(Value{Ref: keyVal, Ty: TypePtr}, Value{Ref: sep, Ty: TypePtr})
	if err != nil {
		return "", err
	}
	line2, err := e.emitStringConcat(line1, Value{Ref: valVal, Ty: TypePtr})
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
	// copy (strlen-bounded, matching text()'s existing NUL semantics).
	e.ensureStrHeaderRuntime()
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", rc, r))
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
func (e *Emitter) emitResponseCall(objVal Value, method string, pos ast.Pos) (Value, error) {
	runner, innerTy, err := e.emitFetchBodyPromRunner(method, pos)
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
	e.emitInstr(fmt.Sprintf("call void @__kml_fetch_bodyprom_register(ptr %s, ptr %s)", pending, clo))

	qt := PromiseOf(innerTy)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}, nil
}

// emitFetchBodyPromRunner synthesizes (once per method) the settle runner
// `void @__kml_fetch_bodyprom_run_<method>(ptr %env)` invoked when the body is
// complete: it builds the value (reusing the eager text/json/arrayBuffer
// emitters — the drive-to-done is trivial since the fetch is already done) under
// a setjmp guard and settles the promise fulfilled, or — for a JSON parse error
// — rejected. Returns the runner name and the promise's inner value type.
func (e *Emitter) emitFetchBodyPromRunner(method string, pos ast.Pos) (string, Type, error) {
	respTy := ResponseType()
	var innerTy Type
	switch method {
	case "text":
		_, bodyTy, _ := respTy.FieldIndex("body")
		innerTy = bodyTy
	case "json":
		innerTy = TypePtr
	case "arrayBuffer":
		innerTy = ArrayBufferType()
	default:
		return "", Type{}, fmt.Errorf("%d:%d: unknown Response method '%s'", pos.Line, pos.Col, method)
	}

	if e.fetchBodyPromRunner == nil {
		e.fetchBodyPromRunner = map[string]string{}
	}
	if name, ok := e.fetchBodyPromRunner[method]; ok {
		return name, innerTy, nil
	}
	name := "@__kml_fetch_bodyprom_run_" + method
	e.fetchBodyPromRunner[method] = name
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
		e.emitInstr(fmt.Sprintf("%s = %s", sj, setjmpCall(jb)))
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
			val, err = e.emitJSONParseValue(body, TypePtr, pos)
		case "arrayBuffer":
			val, err = e.emitResponseArrayBuffer(objVal, pos)
		}
		if err != nil {
			return err
		}
		e.emitInstr("call void @__kml_pop_jmpbuf()")
		bits := e.promiseBitsOf(val)
		vSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", vSlot, promiseStructIR, q))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", bits, vSlot))
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 1)", q))
		e.emitTerminator("ret void")

		e.emitLabel(catchL)
		errPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_get_thrown()", errPtr))
		errBits := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", errBits, errPtr))
		evSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", evSlot, promiseStructIR, q))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", errBits, evSlot))
		e.emitInstr(fmt.Sprintf("call void @__kml_promise_settle(ptr %s, i64 2)", q))
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

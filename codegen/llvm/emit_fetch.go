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

// isResponseMethodName reports whether name is one of Response's dispatched
// methods. status/ok/body are plain object fields (already handled by the
// generic object field-read path) and need no entry here.
func isResponseMethodName(name string) bool {
	switch name {
	case "text", "json":
		return true
	}
	return false
}

// emitFetch implements fetch(url) and fetch(url, init) (ADR-00074,
// TDD-00017): kicks off a non-blocking libcurl multi-interface transfer via
// __kml_fetch_async and wraps the returned pending-fetch handle in a
// Promise<Response> slot — the same slot shape emitAsyncEpilogue/emitAwait
// (emit_async.go) already expect, just holding a not-yet-resolved pending
// handle instead of an already-built Response, since building the Response
// needs the transfer to have actually finished (see emitAwait's
// IsResponse-specific branch).
//
// init, when present, is any value whose inferred type has some subset of
// method: string / headers: Map<string,string> / body: string fields — no
// shared RequestInit interface has to exist, matching the same
// FieldIndex-on-the-inferred-type pattern http.listen's own optional
// headers field already uses (emit_http.go's isPlainStringType, ADR-00072).
// Each field present resolves to a ptr value passed to __kml_fetch_async;
// each field absent passes a literal "null", which the shared runtime
// function treats as "use curl's default" (see ensureFetchAsync's doc
// comment in runtime.go).
func (e *Emitter) emitFetch(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 && len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: fetch takes 1 argument (url) or 2 (url, init)", pos.Line, pos.Col)
	}
	urlVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

	methodRef, headersRef, bodyRef := "null", "null", "null"
	if len(args) == 2 {
		initVal, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		if !initVal.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: fetch's second argument must be an object with an optional method/headers/body field", pos.Line, pos.Col)
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
				return Value{}, fmt.Errorf("%d:%d: fetch's init.headers must be Map<string, string>", pos.Line, pos.Col)
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

	e.ensureFetchAsync()
	pendingReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_async(ptr %s, ptr %s, ptr %s, ptr %s)",
		pendingReg, urlVal.Ref, methodRef, headersRef, bodyRef))

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
	idx, fieldTy, ok := objVal.Ty.FieldIndex("body")
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: not a Response", pos.Line, pos.Col)
	}
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, fieldTy.IR, gep, fieldTy.Align()))
	return Value{Ref: r, Ty: fieldTy}, nil
}

// emitResponseCall dispatches a Response method call reached through the
// generic (non-declaration-context) path — text() always, and json() when
// there's no surrounding typed declaration to parse into (falls back to
// TypePtr, matching bare JSON.parse's own default-context behavior).
func (e *Emitter) emitResponseCall(objVal Value, method string, pos ast.Pos) (Value, error) {
	switch method {
	case "text":
		return e.emitResponseBody(objVal, pos)
	case "json":
		bodyVal, err := e.emitResponseBody(objVal, pos)
		if err != nil {
			return Value{}, err
		}
		return e.emitJSONParseValue(bodyVal, TypePtr, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown Response method '%s'", pos.Line, pos.Col, method)
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

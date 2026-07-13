// emit_fetch.go — fetch(url), Response (status/ok/body fields, text()/json()
// methods). GET only for V1: no custom method/headers/request body yet.
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

// emitFetch implements the fetch(url) builtin: kicks off a non-blocking
// libcurl multi-interface transfer via __kml_fetch_async and wraps the
// returned pending-fetch handle in a Promise<Response> slot — the same
// slot shape emitAsyncEpilogue/emitAwait (emit_async.go) already expect,
// just holding a not-yet-resolved pending handle instead of an already-built
// Response, since building the Response needs the transfer to have
// actually finished (see emitAwait's IsResponse-specific branch).
func (e *Emitter) emitFetch(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fetch takes exactly 1 argument (url); custom method/headers/body are not yet supported", pos.Line, pos.Col)
	}
	urlVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

	e.ensureFetchAsync()
	pendingReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_async(ptr %s)", pendingReg, urlVal.Ref))

	e.ensureMalloc()
	slotReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", slotReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", pendingReg, slotReg))

	return Value{Ref: slotReg, Ty: PromiseOf(ResponseType())}, nil
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

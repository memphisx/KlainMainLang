// emit_fetch.go — fetch(url), Response (status/ok/body fields, text()/json()
// methods). GET only for V1: no custom method/headers/request body yet.
//
// fetch() itself is a blocking libcurl call (see runtime.go's ensureFetch) —
// there's no event loop in this compiler yet, so "async" here means the same
// thing async/await already means elsewhere (emit_async.go): a malloc'd
// Promise slot holding an already-resolved value. Two fetches issued before
// either is awaited will therefore run one after another, not concurrently —
// a deliberate, documented deviation from real fetch, not an oversight.
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

// emitFetch implements the fetch(url) builtin: calls the blocking
// __kml_fetch runtime helper, builds a Response struct from its result, and
// wraps it as an already-resolved Promise<Response> — mirroring exactly what
// an async function's own prologue/epilogue (emit_async.go) would produce,
// just constructed by hand since fetch isn't user-authored async code.
func (e *Emitter) emitFetch(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: fetch takes exactly 1 argument (url); custom method/headers/body are not yet supported", pos.Line, pos.Col)
	}
	urlVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

	e.ensureFetch()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { i64, ptr } @__kml_fetch(ptr %s)", raw, urlVal.Ref))
	status := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr } %s, 0", status, raw))
	body := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i64, ptr } %s, 1", body, raw))

	ok := e.freshReg()
	okHigh := e.freshReg()
	okLow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 200", okLow, status))
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 300", okHigh, status))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", ok, okLow, okHigh))

	respTy := ResponseType()
	e.ensureMalloc()
	respReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", respReg, respTy.StructSize()))
	structIR := respTy.StructIR()
	storeField := func(name, ir, ref string, align int) {
		idx, _, _ := respTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, respReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, ref, gep, align))
	}
	storeField("status", "i64", status, 8)
	storeField("ok", "i1", ok, 1)
	storeField("body", "ptr", body, 8)

	// Wrap as an already-resolved Promise<Response>: malloc a slot, store the
	// Response pointer into it — the same shape emitAsyncEpilogue/emitAwait
	// (emit_async.go) already expect, so `await fetch(...)` needs no changes
	// there at all.
	slotReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 8)", slotReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", respReg, slotReg))

	return Value{Ref: slotReg, Ty: PromiseOf(respTy)}, nil
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

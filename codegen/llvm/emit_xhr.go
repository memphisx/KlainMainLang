// emit_xhr.go — `new XMLHttpRequest()`, `.open()`/`.setRequestHeader()`/
// `.send()`/`.abort()` (TDD-00040): a legacy synchronous-style client.
// `.send()` looks synchronous from TS code but reuses fetch()'s own
// non-blocking `__kml_fetch_async` primitive underneath rather than a
// separate blocking transfer path — see runtime_fetch.go's
// ensureFetchAwaitSettled for the full design (also documented in the
// TDD). `.onreadystatechange`/`.onload`/`.onerror` are plain zero-argument
// FuncType fields — assigning to them needs no dedicated codegen (the same
// generic object-field-assignment path every other callback field already
// uses); firing one is a null-check-then-call, done inline in send()/open()
// below via emitClosureCallByPtr with a nil (empty) argument list.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitNewXMLHttpRequestExpression implements `new XMLHttpRequest()`: no
// arguments, matching the real constructor. Every field starts at its
// spec-defined initial value (readyState=0 UNSENT, status=0, empty
// responseText/response, no callbacks registered, a fresh empty headers
// map for setRequestHeader() to accumulate into before send()).
func (e *Emitter) emitNewXMLHttpRequestExpression(ex *ast.NewXMLHttpRequestExpression) (Value, error) {
	e.ensureMalloc()
	e.ensureMapStrHelpers()

	ty := XMLHttpRequestType()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, ty.StructSize()))
	structIR := ty.StructIR()

	storeField := func(name, ir, val string, align int) {
		idx, _, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, val, gep, align))
	}

	emptyStr := e.internString("")
	headersPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", headersPtr))

	storeField(XHRMethodField, "ptr", emptyStr, 8)
	storeField(XHRURLField, "ptr", emptyStr, 8)
	storeField(XHRHeadersField, "ptr", headersPtr, 8)
	storeField("readyState", "i64", "0", 8)
	storeField("status", "i64", "0", 8)
	storeField("responseText", "ptr", emptyStr, 8)
	storeField("response", "ptr", emptyStr, 8)
	storeField("onreadystatechange", "ptr", "null", 8)
	storeField("onload", "ptr", "null", 8)
	storeField("onerror", "ptr", "null", 8)

	return Value{Ref: objReg, Ty: ty}, nil
}

// emitXHRFireCallback loads objVal's fieldName (a zero-argument FuncType
// field) and, if non-null, calls it — the shared null-check-then-call shape
// every callback firing site below needs. emitClosureCallByPtr's own args
// loop is a no-op for a nil slice, so passing nil here is exactly "call
// with zero arguments", no separate zero-arg call path needed.
func (e *Emitter) emitXHRFireCallback(objVal Value, fieldName string, pos ast.Pos) error {
	idx, fieldTy, ok := objVal.Ty.FieldIndex(fieldName)
	if !ok {
		return fmt.Errorf("%d:%d: not an XMLHttpRequest", pos.Line, pos.Col)
	}
	cbVal := e.loadFieldValue(objVal, idx, fieldTy)

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", isNull, cbVal.Ref))
	callL := e.freshLabel("xhr.cb.call")
	skipL := e.freshLabel("xhr.cb.skip")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, callL, skipL))

	e.emitLabel(callL)
	if _, err := e.emitClosureCallByPtr(cbVal.Ref, cbVal.Ty, nil, pos); err != nil {
		return err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))

	e.emitLabel(skipL)
	return nil
}

// xhrStoreField GEPs and stores into one of objVal's fields — the mutating
// counterpart to loadFieldValue (emit_fetch.go), needed throughout
// open()/send()/abort() below since, unlike every other builtin object type
// this compiler has, XMLHttpRequest's own fields are written after
// construction, not just read.
func (e *Emitter) xhrStoreField(objVal Value, fieldName string, val Value) {
	idx, fieldTy, _ := objVal.Ty.FieldIndex(fieldName)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, val.Ref, gep, fieldTy.Align()))
}

// emitXHROpen implements xhr.open(method, url): records method/url into the
// hidden fields send() reads later, resets the accumulated headers map (a
// fresh open() call starts clean, matching the real spec), and transitions
// readyState 0 (UNSENT) -> 1 (OPENED), firing onreadystatechange.
func (e *Emitter) emitXHROpen(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: xhr.open() requires 2 arguments (method, url)", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	methodVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	methodVal = e.coerce(methodVal, TypePtr)
	urlVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

	e.xhrStoreField(objVal, XHRMethodField, methodVal)
	e.xhrStoreField(objVal, XHRURLField, urlVal)

	e.ensureMapStrHelpers()
	freshHeaders := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", freshHeaders))
	e.xhrStoreField(objVal, XHRHeadersField, Value{Ref: freshHeaders, Ty: TypePtr})

	e.xhrStoreField(objVal, "readyState", Value{Ref: "1", Ty: TypeI64})

	if err := e.emitXHRFireCallback(objVal, "onreadystatechange", pos); err != nil {
		return Value{}, err
	}
	return Value{Ty: TypeVoid}, nil
}

// emitXHRSetRequestHeader implements xhr.setRequestHeader(name, value):
// accumulates into the hidden headers map send() later converts into a
// curl_slist via the exact same buildFetchHeaderList (emit_fetch.go)
// fetch(url, init) already uses. Unlike Headers' own get/set/has/delete
// (emit_headers.go), this does NOT lowercase name — a real, documented V1
// narrowing (see docs/status/NETWORKING.md's Known Limitations).
func (e *Emitter) emitXHRSetRequestHeader(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: xhr.setRequestHeader() requires 2 arguments (name, value)", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	nameVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	nameVal = e.coerce(nameVal, TypePtr)
	valVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	valVal = e.coerce(valVal, TypePtr)

	idx, fieldTy, _ := objVal.Ty.FieldIndex(XHRHeadersField)
	headersVal := e.loadFieldValue(objVal, idx, fieldTy)

	e.ensureMapStrHelpers()
	valAsI64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", valAsI64, valVal.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", headersVal.Ref, nameVal.Ref, valAsI64))

	return Value{Ty: TypeVoid}, nil
}

// emitXHRSend implements xhr.send()/xhr.send(body): the entire transfer,
// via __kml_fetch_async + __kml_await_fetch_settled (see this file's own
// header comment and runtime_fetch.go's ensureFetchAwaitSettled for why —
// TDD-00040). On success: status/responseText/response are set,
// readyState becomes 4 (DONE), onreadystatechange then onload fire. On a
// network-level failure: status stays 0, responseText/response stay empty,
// readyState still becomes 4, onreadystatechange then onerror fire —
// real XMLHttpRequest never throws from send() on a network error either.
func (e *Emitter) emitXHRSend(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: xhr.send() takes at most 1 argument (body)", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}

	methodIdx, methodFieldTy, _ := objVal.Ty.FieldIndex(XHRMethodField)
	methodVal := e.loadFieldValue(objVal, methodIdx, methodFieldTy)
	urlIdx, urlFieldTy, _ := objVal.Ty.FieldIndex(XHRURLField)
	urlVal := e.loadFieldValue(objVal, urlIdx, urlFieldTy)
	headersIdx, headersFieldTy, _ := objVal.Ty.FieldIndex(XHRHeadersField)
	headersVal := e.loadFieldValue(objVal, headersIdx, headersFieldTy)

	bodyRef := "null"
	if len(args) == 1 {
		bodyVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		bodyVal = e.coerce(bodyVal, TypePtr)
		bodyRef = bodyVal.Ref
	}

	headersRef, err := e.buildFetchHeaderList(headersVal.Ref)
	if err != nil {
		return Value{}, err
	}

	e.ensureFetchAwaitSettled()
	pendingReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_fetch_async(ptr %s, ptr %s, ptr %s, ptr %s, ptr null)",
		pendingReg, urlVal.Ref, methodVal.Ref, headersRef, bodyRef))

	resultReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call { i1, i64, ptr, ptr, i64 } @__kml_await_fetch_settled(ptr %s)", resultReg, pendingReg))

	failedReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, ptr, ptr, i64 } %s, 0", failedReg, resultReg))

	okL := e.freshLabel("xhr.send.ok")
	failL := e.freshLabel("xhr.send.fail")
	doneL := e.freshLabel("xhr.send.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", failedReg, failL, okL))

	e.emitLabel(okL)
	statusReg := e.freshReg()
	bodyPtrReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, ptr, ptr, i64 } %s, 1", statusReg, resultReg))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, ptr, ptr, i64 } %s, 2", bodyPtrReg, resultReg))
	e.xhrStoreField(objVal, "status", Value{Ref: statusReg, Ty: TypeI64})
	e.xhrStoreField(objVal, "responseText", Value{Ref: bodyPtrReg, Ty: TypePtr})
	e.xhrStoreField(objVal, "response", Value{Ref: bodyPtrReg, Ty: TypePtr})
	e.xhrStoreField(objVal, "readyState", Value{Ref: "4", Ty: TypeI64})
	if err := e.emitXHRFireCallback(objVal, "onreadystatechange", pos); err != nil {
		return Value{}, err
	}
	if err := e.emitXHRFireCallback(objVal, "onload", pos); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(failL)
	e.xhrStoreField(objVal, "readyState", Value{Ref: "4", Ty: TypeI64})
	if err := e.emitXHRFireCallback(objVal, "onreadystatechange", pos); err != nil {
		return Value{}, err
	}
	if err := e.emitXHRFireCallback(objVal, "onerror", pos); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	return Value{Ty: TypeVoid}, nil
}

// emitXHRAbort implements xhr.abort(): a best-effort readyState reset —
// real abort-mid-flight has no meaning here, since send() (synchronous by
// construction, see this file's header comment) has always already
// completed by the time abort() could possibly be called.
func (e *Emitter) emitXHRAbort(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("%d:%d: xhr.abort() takes no arguments", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	e.xhrStoreField(objVal, "readyState", Value{Ref: "0", Ty: TypeI64})
	return Value{Ty: TypeVoid}, nil
}

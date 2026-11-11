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
	e.ensureMapStrHelpers()

	ty := XMLHttpRequestType()
	objReg := e.freshReg()
	e.emitObjMallocInto(objReg, ty)
	structIR := ty.StructIR()

	storeField := func(name, ir, val string, align int) {
		idx, _, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.heapStore(ir, val, gep, align)
	}

	emptyStr := e.internString("")
	headersPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_map_str_create()", headersPtr))

	storeField(XHRMethodField, irPtr, emptyStr, 8)
	storeField(XHRURLField, irPtr, emptyStr, 8)
	storeField(XHRHeadersField, irPtr, headersPtr, 8)
	storeField("readyState", "i64", "0", 8)
	storeField("status", "i64", "0", 8)
	storeField("responseText", irPtr, emptyStr, 8)
	storeField("response", irPtr, emptyStr, 8)
	storeField("onreadystatechange", irPtr, "null", 8)
	storeField("onload", irPtr, "null", 8)
	storeField("onerror", irPtr, "null", 8)
	storeField(XHRRespHeadersField, irPtr, "null", 8)

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
		return internalAt(pos, "an XMLHttpRequest callback on a %s", objVal.Ty.IR)
	}
	cbVal := e.loadFieldValue(objVal, idx, fieldTy)

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", isNull, cbVal.Ref))
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	e.heapStore(fieldTy.IR, val.Ref, gep, fieldTy.Align())
}

// emitXHROpen implements xhr.open(method, url, async?, user?, password?):
// records method/url into the hidden fields send() reads later, resets the
// accumulated headers map (a fresh open() call starts clean, matching the
// real spec), and transitions readyState 0 (UNSENT) -> 1 (OPENED), firing
// onreadystatechange.
//
// The optional 3rd argument is `async`. send() is already synchronous
// underneath (fetch_async + await_fetch_settled — see this file's header),
// so `async === false` maps exactly onto that behavior: a genuine blocking
// request, the primitive a closed-loop load generator needs. `async === true`
// currently also blocks in send() — there is no main-thread event loop to
// dispatch a deferred onload off — so it is a documented narrowing rather
// than a fake async. The optional 4th/5th `user`/`password` args are
// evaluated (for their side effects) but not yet wired to curl auth, also a
// documented narrowing (see docs/status/NETWORKING.md and ADR-00615).
func (e *Emitter) emitXHROpen(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	args, short, err := e.xhrArity(args, 2, 5, "open", pos)
	if err != nil || short {
		return Value{Ty: TypeVoid}, err
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	methodVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	methodVal = e.convert(methodVal, TypePtr)
	urlVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	urlVal = e.convert(urlVal, TypePtr)

	// Evaluate any async/user/password arguments so their side effects occur,
	// even though behavior is synchronous regardless (see the doc comment).
	for i := 2; i < len(args); i++ {
		if _, err := e.emitExpr(args[i]); err != nil {
			return Value{}, err
		}
	}

	e.xhrStoreField(objVal, XHRMethodField, methodVal)
	e.xhrStoreField(objVal, XHRURLField, urlVal)

	e.ensureMapStrHelpers()
	freshHeaders := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_map_str_create()", freshHeaders))
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
	args, short, err := e.xhrArity(args, 2, 2, "setRequestHeader", pos)
	if err != nil || short {
		return Value{Ty: TypeVoid}, err
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	nameVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	nameVal = e.convert(nameVal, TypePtr)
	valVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	valVal = e.convert(valVal, TypePtr)

	idx, fieldTy, _ := objVal.Ty.FieldIndex(XHRHeadersField)
	headersVal := e.loadFieldValue(objVal, idx, fieldTy)

	e.ensureMapStrHelpers()
	valAsI64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint "+irPtr+" %s to i64", valAsI64, valVal.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set("+irPtr+" %s, "+irPtr+" %s, i64 %s)", headersVal.Ref, nameVal.Ref, valAsI64))

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
	args, short, err := e.xhrArity(args, 0, 1, "send", pos)
	if err != nil || short {
		return Value{Ty: TypeVoid}, err
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
		bodyVal = e.convert(bodyVal, TypePtr)
		bodyRef = bodyVal.Ref
	}

	headersRef, err := e.buildFetchHeaderList(headersVal.Ref)
	if err != nil {
		return Value{}, err
	}

	e.ensureFetchAsync()
	e.ensureFetch()
	pendingReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_async("+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" %s, "+irPtr+" null)",
		pendingReg, urlVal.Ref, methodVal.Ref, headersRef, bodyRef))

	resultReg := e.emitOutAggCall("__kml_await_fetch_settled_c", "{ i1, i64, "+irPtr+", "+irPtr+", i64 }", irPtr+" "+pendingReg)

	failedReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, "+irPtr+", "+irPtr+", i64 } %s, 0", failedReg, resultReg))

	okL := e.freshLabel("xhr.send.ok")
	failL := e.freshLabel("xhr.send.fail")
	doneL := e.freshLabel("xhr.send.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", failedReg, failL, okL))

	e.emitLabel(okL)
	statusReg := e.freshReg()
	bodyPtrReg := e.freshReg()
	bodyLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, "+irPtr+", "+irPtr+", i64 } %s, 1", statusReg, resultReg))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, "+irPtr+", "+irPtr+", i64 } %s, 2", bodyPtrReg, resultReg))
	e.emitInstr(fmt.Sprintf("%s = extractvalue { i1, i64, "+irPtr+", "+irPtr+", i64 } %s, 4", bodyLenReg, resultReg))
	// TDD-00120: the curl body is a raw buffer — copy it into a length-prefixed
	// header string so responseText's binary-safe consumers (=== / .split read
	// ptr-8) work. bodyLen (tuple index 4) is the exact byte count.
	e.ensureStrHeaderRuntime()
	respTextReg := e.freshReg()
	respNulReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_alloc(i64 %s)", respTextReg, bodyLenReg))
	e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", respTextReg, bodyPtrReg, bodyLenReg))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", respNulReg, respTextReg, bodyLenReg))
	e.heapStore("i8", "0", respNulReg, 1)
	e.ensureMapStrHelpers()
	respHdrsReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_headers_map("+irPtr+" %s)", respHdrsReg, pendingReg))
	e.xhrStoreField(objVal, XHRRespHeadersField, Value{Ref: respHdrsReg, Ty: TypePtr})
	e.xhrStoreField(objVal, "status", Value{Ref: statusReg, Ty: TypeI64})
	e.xhrStoreField(objVal, "responseText", Value{Ref: respTextReg, Ty: TypePtr})
	e.xhrStoreField(objVal, "response", Value{Ref: respTextReg, Ty: TypePtr})
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
	args, short, err := e.xhrArity(args, 0, 0, "abort", pos)
	if err != nil || short {
		return Value{Ty: TypeVoid}, err
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	e.xhrStoreField(objVal, "readyState", Value{Ref: "0", Ty: TypeI64})
	return Value{Ty: TypeVoid}, nil
}

// emitXHRGetResponseHeader implements xhr.getResponseHeader(name): a
// case-insensitive lookup (the parsed map's keys are lowercased, so the
// query is lowercased too, same as Headers.get) against the response-header
// map send() stored. Before send() completes — or after a network failure —
// the map is null and the result is a null string, matching the spec's null
// return for the not-yet/absent cases.
func (e *Emitter) emitXHRGetResponseHeader(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	args, short, err := e.xhrArity(args, 1, 1, "getResponseHeader", pos)
	if err != nil || short {
		return Value{Ref: "null", Ty: TypePtr}, err
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	lowered, err := e.emitLoweredHeaderName(args[0])
	if err != nil {
		return Value{}, err
	}
	idx, fieldTy, _ := objVal.Ty.FieldIndex(XHRRespHeadersField)
	mapVal := e.loadFieldValue(objVal, idx, fieldTy)

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", isNull, mapVal.Ref))
	nullL := e.freshLabel("xhr.grh.null")
	getL := e.freshLabel("xhr.grh.get")
	doneL := e.freshLabel("xhr.grh.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, nullL, getL))

	e.emitLabel(getL)
	e.ensureMapStrHelpers()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get("+irPtr+" %s, "+irPtr+" %s)", raw, mapVal.Ref, lowered))
	asPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, asPtr, raw))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(nullL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(doneL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi "+irPtr+" [ %s, %%%s ], [ null, %%%s ]", res, asPtr, getL, nullL))
	return Value{Ref: res, Ty: TypePtr}, nil
}

// emitXHRGetAllResponseHeaders implements xhr.getAllResponseHeaders():
// the "name: value\r\n" concatenation, serialized by
// __kml_xhr_headers_all from the same parsed map getResponseHeader() reads.
func (e *Emitter) emitXHRGetAllResponseHeaders(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	args, short, err := e.xhrArity(args, 0, 0, "getAllResponseHeaders", pos)
	if err != nil || short {
		return Value{Ref: "null", Ty: TypePtr}, err
	}
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	idx, fieldTy, _ := objVal.Ty.FieldIndex(XHRRespHeadersField)
	mapVal := e.loadFieldValue(objVal, idx, fieldTy)
	e.ensureMapStrHelpers()
	e.ensureStrHeaderRuntime()
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_xhr_headers_all("+irPtr+" %s)", res, mapVal.Ref))
	return Value{Ref: res, Ty: TypePtr}, nil
}

// emitLoweredHeaderName evaluates nameExpr, lowercased.
func (e *Emitter) emitLoweredHeaderName(nameExpr ast.Expression) (string, error) {
	nameVal, err := e.emitExpr(nameExpr)
	if err != nil {
		return "", err
	}
	nameVal = e.convert(nameVal, TypePtr)
	e.ensureStrHeaderRuntime()
	lowered := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_tolower("+irPtr+" %s)", lowered, nameVal.Ref))
	return lowered, nil
}

// The XMLHttpRequest methods, reached through their `@intrinsic`
// declarations.
func init() {
	for name, emit := range map[string]func(e *Emitter, obj ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error){
		"open":                  (*Emitter).emitXHROpen,
		"setRequestHeader":      (*Emitter).emitXHRSetRequestHeader,
		"send":                  (*Emitter).emitXHRSend,
		"abort":                 (*Emitter).emitXHRAbort,
		"getResponseHeader":     (*Emitter).emitXHRGetResponseHeader,
		"getAllResponseHeaders": (*Emitter).emitXHRGetAllResponseHeaders,
	} {
		emit := emit
		intrinsics["XMLHttpRequest.prototype."+name] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				return emit(e, ex.Callee.(*ast.MemberExpression).Object, ex.Args, ex.GetPos())
			},
			ty: func(*Emitter, *ast.CallExpression) Type { return Type{} },
		}
	}
}

// xhrArity fits a method call's arguments to [min, max]: past max they are
// evaluated and dropped; short of min the call throws the platform's
// TypeError (short is then true, and the caller emits nothing more).
func (e *Emitter) xhrArity(args []ast.Expression, min, max int, method string, pos ast.Pos) ([]ast.Expression, bool, error) {
	if len(e.passedArgs(args)) >= min {
		out, err := e.dropArgsPast(args, max, pos)
		return out, false, err
	}
	args = e.passedArgs(args)
	for _, a := range args {
		if _, err := e.emitExpr(a); err != nil {
			return nil, false, err
		}
	}
	plural := "s"
	if min == 1 {
		plural = ""
	}
	e.emitThrowTypeError(fmt.Sprintf("Failed to execute '%s' on 'XMLHttpRequest': %d argument%s required, but only %d present.", method, min, plural, len(args)))
	e.emitLabel(e.freshLabel("xhr.arity.dead"))
	return nil, true, nil
}

// passedArgs is args without the trailing undefineds -compat=js padded in
// for arguments the call omitted.
func (e *Emitter) passedArgs(args []ast.Expression) []ast.Expression {
	for len(args) > 0 && e.omittedArg(args[len(args)-1]) {
		args = args[:len(args)-1]
	}
	return args
}

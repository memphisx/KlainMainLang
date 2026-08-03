// emit_eventemitter.go — EventEmitter<T> (TDD-00023): standalone
// `new EventEmitter<T>()` variable declarations, `class X extends
// EventEmitter<T>` instance dispatch, and the on/once/emit/off/
// removeListener/removeAllListeners/listenerCount/eventNames method
// surface. Mirrors emit_collections.go's split (Go codegen here,
// hand-written C-in-IR-text helpers in runtime_eventemitter.go) — the
// underlying event-name -> listener-list map reuses Map<string,ptr>'s own
// runtime helpers directly (see IsEventEmitter's doc comment in types.go
// for why that's safe without setting IsMap).
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// eventEmitterMethodNames is the fixed, reserved set of EventEmitter's own
// method names — never real AST-driven class methods (see registerClasses'
// reserved-name collision check), always hand-written codegen dispatched by
// name from emit_call.go.
var eventEmitterMethodNames = map[string]bool{
	"on": true, "once": true, "emit": true, "off": true,
	"removeListener": true, "removeAllListeners": true,
	"listenerCount": true, "eventNames": true,
}

func isEventEmitterMethodName(name string) bool { return eventEmitterMethodNames[name] }

// resolveEventEmitterPayloadType resolves the T in EventEmitter<T>/
// `new EventEmitter<T>()`/`extends EventEmitter<T>` — the one shared place
// all three call sites (emitEventEmitterVarDecl, emitter.go's resolveType
// EventEmitter<T> annotation case, and registerClasses' extends handling)
// go through, so "Error" only needs special-casing once. "Error" is
// deliberately not a resolvable type-annotation name in general (see
// emit_classes.go's emitErrorInstanceOf doc comment — that invariant is
// specifically about a Nullable Error-typed value, which this narrow
// special-case doesn't produce), but EventEmitter<Error> is an explicitly
// designed-for payload shape (TDD-00023) — rethrowing the exact Error
// instance on an unlistened 'error' event needs payloadTy.IsError to
// actually be true, so this one generic-argument position resolves it
// directly rather than falling through to resolveType's generic default
// (TypeI64).
func (e *Emitter) resolveEventEmitterPayloadType(ta *ast.TypeAnnotation) Type {
	if ta.Name == "Error" {
		return errorObjType
	}
	return e.resolveType(ta)
}

// classEventEmitterFieldIndex returns the hidden listener-map field's
// struct index for a HasEventEmitter class — position 1 or 2 depending on
// whether the class is also HasVTable (see ClassType's field-order doc
// comment in types.go).
func classEventEmitterFieldIndex(classTy Type) int {
	if classTy.HasVTable {
		return 2
	}
	return 1
}

// emitEventEmitterVarDecl handles `const e = new EventEmitter<T>()`.
func (e *Emitter) emitEventEmitterVarDecl(v *ast.VarDeclaration, init *ast.NewEventEmitterExpression) error {
	payload := TypePtr
	if init.PayloadType != nil {
		payload = e.resolveEventEmitterPayloadType(init.PayloadType)
	}
	ty := EventEmitterType(payload)

	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})

	e.ensureMapStrHelpers()
	handlePtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", handlePtr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", handlePtr, ptrName))
	return nil
}

// resolveEventEmitterForCall resolves a standalone EventEmitter method
// call's receiver expression to its already-loaded heap pointer — the same
// named-variable-vs-arbitrary-expression split resolveMapOrSetForCall
// (emit_collections.go) uses.
func (e *Emitter) resolveEventEmitterForCall(objExpr ast.Expression, pos ast.Pos) (Type, string, error) {
	if id, ok := objExpr.(*ast.Identifier); ok {
		sym, found := e.lookup(id.Name)
		if !found || !sym.Ty.IsEventEmitter {
			return Type{}, "", fmt.Errorf("%d:%d: '%s' is not an EventEmitter", pos.Line, pos.Col, id.Name)
		}
		ptr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptr, sym.Ptr))
		return sym.Ty, ptr, nil
	}
	val, err := e.emitExpr(objExpr)
	if err != nil {
		return Type{}, "", err
	}
	if !val.Ty.IsEventEmitter {
		return Type{}, "", fmt.Errorf("%d:%d: value is not an EventEmitter", pos.Line, pos.Col)
	}
	return val.Ty, val.Ref, nil
}

// resolveEventEmitterListenerArg evaluates and validates arg as a
// single-argument, void-returning closure whose parameter matches
// payloadTy — the only listener shape .on()/.once() accept, generalizing
// timerCallbackPtr's arity-0 validation (emit_timers.go:19-35) to arity 1 +
// a payload-type check. An untyped arrow-function parameter has payloadTy
// propagated in as a hint (emitArrowFunctionWithHints), the same mechanism
// Map/Set's own forEach callbacks already use — so `emitter.on('x', data =>
// ...)` needs no explicit parameter annotation. A bare reference to a
// top-level named function is rejected (same restriction timerCallbackPtr
// already has): .emit() must be able to invoke the listener later, from a
// different call site entirely, which requires a real runtime closure
// pointer to store — a named function has no such pointer representation.
func (e *Emitter) resolveEventEmitterListenerArg(arg ast.Expression, payloadTy Type, fnName string, pos ast.Pos) (string, error) {
	var val Value
	var err error
	if af, ok := arg.(*ast.ArrowFunction); ok {
		val, err = e.emitArrowFunctionWithHints(af, []Type{payloadTy})
	} else {
		val, err = e.emitExpr(arg)
	}
	if err != nil {
		return "", err
	}
	if !val.Ty.IsFunc {
		return "", fmt.Errorf("%d:%d: %s's listener must be a function", pos.Line, pos.Col, fnName)
	}
	if len(val.Ty.FuncParams) != 1 || val.Ty.FuncParams[0].IR != payloadTy.IR {
		return "", fmt.Errorf("%d:%d: %s's listener must take exactly 1 argument matching this EventEmitter's payload type", pos.Line, pos.Col, fnName)
	}
	if val.Ty.FuncRetType != nil && val.Ty.FuncRetType.IR != "void" {
		return "", fmt.Errorf("%d:%d: %s's listener must return nothing (void)", pos.Line, pos.Col, fnName)
	}
	return val.Ref, nil
}

// emitEventEmitterGetOrCreateList returns the listener-list heap pointer
// for eventRef (a ptr to an already-evaluated event-name string) inside
// listenersMapPtr's Map<string,ptr>, creating and registering a fresh empty
// list the first time a listener is registered for that event name.
func (e *Emitter) emitEventEmitterGetOrCreateList(listenersMapPtr, eventRef string) string {
	e.ensureMapStrHelpers()
	e.ensureEventEmitterRuntime()

	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, listenersMapPtr, eventRef))
	existing := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", existing, raw))

	resultAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", existing, resultAlloca))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, existing))
	createL := e.freshLabel("ee.getlist.create")
	mergeL := e.freshLabel("ee.getlist.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, createL, mergeL))

	e.emitLabel(createL)
	newList := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_ee_list_create()", newList))
	newListI64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", newListI64, newList))
	e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", listenersMapPtr, eventRef, newListI64))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newList, resultAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resultAlloca))
	return result
}

// eventEmitterListPtr loads (without creating) the listener-list pointer
// for an already-evaluated event-name ptr — null if no listener was ever
// registered for that event name. Shared by off/removeListener,
// removeAllListeners(event), and listenerCount.
func (e *Emitter) eventEmitterListPtr(listenersMapPtr, eventRef string) string {
	e.ensureMapStrHelpers()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, listenersMapPtr, eventRef))
	listPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", listPtr, raw))
	return listPtr
}

// emitEventEmitterCall is the shared TDD-00023 method-call-dispatch core —
// used by both a standalone EventEmitter<T> value and a `class X extends
// EventEmitter<T>` instance (emit_call.go resolves either receiver shape
// down to a payload type + the loaded listener-map pointer before calling
// this). chainVal is what every chainable method (on/once/off/
// removeListener/removeAllListeners) returns — the emitter's own value for
// a standalone receiver, the class instance pointer for the embedded case —
// so both callers get correct chaining semantics from one implementation.
func (e *Emitter) emitEventEmitterCall(payloadTy Type, listenersMapPtr string, method string, args []ast.Expression, pos ast.Pos, chainVal Value) (Value, error) {
	switch method {
	case "on", "once":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: %s() requires 2 arguments (event, listener)", pos.Line, pos.Col, method)
		}
		eventVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		eventVal = e.coerce(eventVal, TypePtr)
		listenerPtr, err := e.resolveEventEmitterListenerArg(args[1], payloadTy, method, pos)
		if err != nil {
			return Value{}, err
		}
		listPtr := e.emitEventEmitterGetOrCreateList(listenersMapPtr, eventVal.Ref)
		once := "0"
		if method == "once" {
			once = "1"
		}
		e.ensureEventEmitterRuntime()
		e.emitInstr(fmt.Sprintf("call void @__kml_ee_list_push(ptr %s, ptr %s, i64 %s)", listPtr, listenerPtr, once))
		return chainVal, nil

	case "off", "removeListener":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: %s() requires 2 arguments (event, listener)", pos.Line, pos.Col, method)
		}
		eventVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		eventVal = e.coerce(eventVal, TypePtr)
		listenerPtr, err := e.resolveEventEmitterListenerArg(args[1], payloadTy, method, pos)
		if err != nil {
			return Value{}, err
		}
		listPtr := e.eventEmitterListPtr(listenersMapPtr, eventVal.Ref)
		e.ensureEventEmitterRuntime()
		e.emitInstr(fmt.Sprintf("call void @__kml_ee_list_remove(ptr %s, ptr %s)", listPtr, listenerPtr))
		return chainVal, nil

	case "removeAllListeners":
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: removeAllListeners() takes at most 1 argument (event?)", pos.Line, pos.Col)
		}
		if len(args) == 0 {
			// No-arg form: literally Map.clear()'s own IR — just reset the
			// map's size, don't free (same "leak in manual mode" convention
			// clear() already uses).
			e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", listenersMapPtr))
			return chainVal, nil
		}
		eventVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		eventVal = e.coerce(eventVal, TypePtr)
		listPtr := e.eventEmitterListPtr(listenersMapPtr, eventVal.Ref)
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, listPtr))
		zeroL := e.freshLabel("ee.removeall.zero")
		doneL := e.freshLabel("ee.removeall.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, zeroL))
		e.emitLabel(zeroL)
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", listPtr))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		return chainVal, nil

	case "listenerCount":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: listenerCount() requires 1 argument (event)", pos.Line, pos.Col)
		}
		eventVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		eventVal = e.coerce(eventVal, TypePtr)
		listPtr := e.eventEmitterListPtr(listenersMapPtr, eventVal.Ref)

		resultAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resultAlloca))
		e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", resultAlloca))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, listPtr))
		haveL := e.freshLabel("ee.count.have")
		mergeL := e.freshLabel("ee.count.merge")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, mergeL, haveL))
		e.emitLabel(haveL)
		lenVal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenVal, listPtr))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenVal, resultAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
		e.emitLabel(mergeL)
		result := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", result, resultAlloca))
		return Value{Ref: result, Ty: TypeI64}, nil

	case "eventNames":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: eventNames() takes no arguments", pos.Line, pos.Col)
		}
		e.ensureMapStrHelpers()
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", res, listenersMapPtr))
		return Value{Ref: res, Ty: ArrayOf(TypePtr)}, nil

	case "emit":
		return e.emitEventEmitterEmit(payloadTy, listenersMapPtr, args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown EventEmitter method '%s'", pos.Line, pos.Col, method)
}

// emitEventEmitterEmit implements `.emit(event, data)`: snapshot-copies the
// event's listener list (so a `.once()` entry removing itself mid-loop
// can't perturb indices not yet visited), invokes every entry via the
// standard closure-call trampoline (emitCBCall), removes once-flagged
// entries from the *real* list after invoking them, and — if zero listeners
// were invoked and event is exactly "error" — throws instead of returning,
// matching real Node's one specially-treated event name. Returns whether
// any listener was invoked.
func (e *Emitter) emitEventEmitterEmit(payloadTy Type, listenersMapPtr string, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: emit() requires 2 arguments (event, data)", pos.Line, pos.Col)
	}
	eventVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	eventVal = e.coerce(eventVal, TypePtr)
	dataVal, err := e.emitExprWithObjectHint(args[1], payloadTy)
	if err != nil {
		return Value{}, err
	}
	dataVal = e.coerce(dataVal, payloadTy)

	e.ensureMapStrHelpers()
	e.ensureEventEmitterRuntime()
	e.ensureMemcpy()
	e.ensureMalloc()

	listPtr := e.eventEmitterListPtr(listenersMapPtr, eventVal.Ref)

	countAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countAlloca))

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, listPtr))
	hasListL := e.freshLabel("ee.emit.haslist")
	afterListL := e.freshLabel("ee.emit.afterlist")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, afterListL, hasListL))

	e.emitLabel(hasListL)
	len0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", len0, listPtr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", len0, countAlloca))
	dataFieldGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", dataFieldGep, listPtr))
	origData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", origData, dataFieldGep))
	snapBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 16", snapBytes, len0))
	snapData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", snapData, snapBytes))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", snapData, origData, snapBytes))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("ee.emit.cond")
	bodyL := e.freshLabel("ee.emit.body")
	doneLoopL := e.freshLabel("ee.emit.doneloop")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	atEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", atEnd, idxVal, len0))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", atEnd, doneLoopL, bodyL))

	e.emitLabel(bodyL)
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i64 %s, i32 0", lp, snapData, idxVal))
	listenerPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", listenerPtr, lp))
	op := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i64 %s, i32 1", op, snapData, idxVal))
	onceFlag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", onceFlag, op))

	cb := Callback{kind: cbClosure, hdrPtr: listenerPtr, ty: FuncType([]Type{payloadTy}, TypeVoid)}
	if _, err := e.emitCBCall(cb, []Value{dataVal}); err != nil {
		return Value{}, err
	}

	isOnce := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isOnce, onceFlag))
	isOnceL := e.freshLabel("ee.emit.isonce")
	notOnceL := e.freshLabel("ee.emit.notonce")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOnce, isOnceL, notOnceL))
	e.emitLabel(isOnceL)
	e.emitInstr(fmt.Sprintf("call void @__kml_ee_list_remove(ptr %s, ptr %s)", listPtr, listenerPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", notOnceL))
	e.emitLabel(notOnceL)

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneLoopL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", afterListL))

	e.emitLabel(afterListL)
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", count, countAlloca))
	noneCalled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", noneCalled, count))

	// 'error'-unlistened-throws special case: resolved entirely here, no
	// runtime round-trip needed — a strcmp against the literal "error",
	// gated on "zero listeners were invoked".
	e.ensureStrcmp()
	cmpResult := e.freshReg()
	errLit := e.internString("error")
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", cmpResult, eventVal.Ref, errLit))
	isErrorEvent := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isErrorEvent, cmpResult))
	shouldThrow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", shouldThrow, noneCalled, isErrorEvent))

	throwL := e.freshLabel("ee.emit.throw")
	retL := e.freshLabel("ee.emit.ret")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", shouldThrow, throwL, retL))

	e.emitLabel(throwL)
	e.ensureExceptionHelpers()
	if payloadTy.IsError {
		// The payload is already an errorObjType-shaped pointer — rethrow
		// it directly rather than re-wrapping/stringifying it.
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", dataVal.Ref))
	} else {
		strVal, err := e.emitValueToString(dataVal)
		if err != nil {
			return Value{}, err
		}
		errPtr := e.buildErrorObj(0, strVal.Ref, e.internString("Error"))
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errPtr))
	}
	e.emitTerminator("unreachable")

	e.emitLabel(retL)
	anyCalled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", anyCalled, noneCalled))
	return Value{Ref: anyCalled, Ty: TypeBool}, nil
}

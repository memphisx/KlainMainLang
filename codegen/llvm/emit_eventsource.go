package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_eventsource.go — `new EventSource(url)` construction, `.close()`,
// and `.addEventListener`/`.removeEventListener` (TDD-00038 Stages 0-2).
// See runtime_eventsource.go for the C-runtime side (connection setup, the
// event-loop scan, SSE record parsing/dispatch) this file's IR calls into.
// `.onmessage = (ev) => ...`/`.onopen = ...`/`.onerror = ...` need no
// dedicated codegen here at all — each is a plain FuncType-typed field, so
// assignment already goes through the ordinary generic object-field-
// assignment path (emit_exprs_assign.go), the same one every other object
// field uses.

// emitNewEventSourceExpression implements `new EventSource(url)`: allocates
// the KML-level object first (url/readyState=CONNECTING/lastEventId=""/
// onmessage=null), so its own pointer can be passed to
// __kml_eventsource_open as `instance` — the two-way link the event-loop
// scan later needs to write a readyState transition (and, Stage 1, dispatch
// a parsed SSE record) back into this exact object — then stores the
// returned entry pointer into the object's own hidden
// EventSourceHandleField.
func (e *Emitter) emitNewEventSourceExpression(ex *ast.NewEventSourceExpression) (Value, error) {
	e.ensureEventSourceRuntime()

	urlVal, err := e.emitExpr(ex.URL)
	if err != nil {
		return Value{}, err
	}
	urlVal = e.coerce(urlVal, TypePtr)

	ty := EventSourceType()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, ty.StructSize()))
	structIR := ty.StructIR()

	storeField := func(name, ir, val string, align int) {
		idx, _, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, val, gep, align))
	}
	storeField("url", "ptr", urlVal.Ref, 8)
	storeField("readyState", "i64", "0", 8)
	storeField(EventSourceLastEventIdField, "ptr", e.internString(""), 8)
	storeField("onmessage", "ptr", "null", 8)
	storeField("onopen", "ptr", "null", 8)
	storeField("onerror", "ptr", "null", 8)

	entryReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_eventsource_open(ptr %s, ptr %s)", entryReg, urlVal.Ref, objReg))
	storeField(EventSourceHandleField, "ptr", entryReg, 8)

	e.usedEventSource = true
	return Value{Ref: objReg, Ty: ty}, nil
}

// emitEventSourceClose implements `es.close()`: loads the hidden entry
// pointer and calls __kml_eventsource_close (idempotent, curl-level
// teardown + marks the runtime entry CLOSED), then additionally stores
// readyState=2 directly into the object — synchronously, matching real
// EventSource's own close() taking effect immediately rather than waiting
// for the next event-loop scan to notice.
func (e *Emitter) emitEventSourceClose(objExpr ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	ty := EventSourceType()
	structIR := ty.StructIR()

	handleIdx, _, _ := ty.FieldIndex(EventSourceHandleField)
	handleGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", handleGep, structIR, objVal.Ref, handleIdx))
	entryReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", entryReg, handleGep))
	e.emitInstr(fmt.Sprintf("call void @__kml_eventsource_close(ptr %s)", entryReg))

	rsIdx, _, _ := ty.FieldIndex("readyState")
	rsGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", rsGep, structIR, objVal.Ref, rsIdx))
	e.emitInstr(fmt.Sprintf("store i64 2, ptr %s, align 8", rsGep))

	return Value{Ty: TypeVoid}, nil
}

// resolveEventSourceListenerArg evaluates a listener argument (an arrow
// function, hinted against MessageEventType() so an unannotated parameter
// resolves correctly, or any other closure-typed expression) and validates
// its shape — mirrors resolveEventEmitterListenerArg (emit_eventemitter.go)
// exactly, MessageEventType() standing in for EventEmitter<T>'s own
// per-instance payload type.
func (e *Emitter) resolveEventSourceListenerArg(arg ast.Expression, fnName string, pos ast.Pos) (string, error) {
	payloadTy := MessageEventType()
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
		return "", fmt.Errorf("%d:%d: %s's listener must take exactly 1 argument (a MessageEvent)", pos.Line, pos.Col, fnName)
	}
	if val.Ty.FuncRetType != nil && val.Ty.FuncRetType.IR != "void" {
		return "", fmt.Errorf("%d:%d: %s's listener must return nothing (void)", pos.Line, pos.Col, fnName)
	}
	return val.Ref, nil
}

// eventSourceEntryPtr loads objExpr's hidden runtime-entry pointer — the
// shared first step both emitEventSourceAddListener and
// emitEventSourceRemoveListener need before calling into
// runtime_eventsource.go's map-backed listener storage.
func (e *Emitter) eventSourceEntryPtr(objExpr ast.Expression) (string, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return "", err
	}
	ty := EventSourceType()
	structIR := ty.StructIR()
	handleIdx, _, _ := ty.FieldIndex(EventSourceHandleField)
	handleGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", handleGep, structIR, objVal.Ref, handleIdx))
	entryReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", entryReg, handleGep))
	return entryReg, nil
}

// emitEventSourceAddListener implements `es.addEventListener(type, cb)`
// (TDD-00038 Stage 2) — cb is called for every subsequent record whose
// resolved type matches type exactly (including "message"/"open"/"error",
// which also still reach onmessage/onopen/onerror independently — both
// registration surfaces share the same underlying listeners map, see
// runtime_eventsource.go's __kml_eventsource_dispatch_event).
func (e *Emitter) emitEventSourceAddListener(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: addEventListener() requires 2 arguments (type, listener)", pos.Line, pos.Col)
	}
	entryReg, err := e.eventSourceEntryPtr(objExpr)
	if err != nil {
		return Value{}, err
	}
	typeVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	typeVal = e.coerce(typeVal, TypePtr)
	listenerRef, err := e.resolveEventSourceListenerArg(args[1], "addEventListener", pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_eventsource_add_listener(ptr %s, ptr %s, ptr %s)", entryReg, typeVal.Ref, listenerRef))
	return Value{Ty: TypeVoid}, nil
}

// emitEventSourceRemoveListener implements
// `es.removeEventListener(type, cb)`.
func (e *Emitter) emitEventSourceRemoveListener(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: removeEventListener() requires 2 arguments (type, listener)", pos.Line, pos.Col)
	}
	entryReg, err := e.eventSourceEntryPtr(objExpr)
	if err != nil {
		return Value{}, err
	}
	typeVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	typeVal = e.coerce(typeVal, TypePtr)
	listenerRef, err := e.resolveEventSourceListenerArg(args[1], "removeEventListener", pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_eventsource_remove_listener(ptr %s, ptr %s, ptr %s)", entryReg, typeVal.Ref, listenerRef))
	return Value{Ty: TypeVoid}, nil
}

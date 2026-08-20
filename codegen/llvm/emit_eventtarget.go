// emit_eventtarget.go — the WHATWG EventTarget bus (TDD-00081 Stage 2). An
// EventTarget is a bare `Map<string, listener-list>` pointer — the same registry
// EventEmitter uses — so add/remove reuse the `__kml_ee_list_*` helpers and
// dispatchEvent reuses EventEmitter's snapshot-and-invoke loop, differing only in
// that it reads the type off the Event object and invokes each listener with that
// object. Single-target dispatch (no capture/bubble tree).
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// resolveEventTargetListenerArg evaluates an addEventListener/removeEventListener
// listener to a closure header pointer, hinting the Event type onto an untyped
// param. Unlike EventEmitter's resolver it does NOT require a void return — a
// WHATWG listener's return value is simply ignored — but it does require the
// single event parameter (a 0-arg listener is a V1 limitation).
func (e *Emitter) resolveEventTargetListenerArg(arg ast.Expression, pos ast.Pos) (string, error) {
	var val Value
	var err error
	switch a := arg.(type) {
	case *ast.ArrowFunction:
		val, err = e.emitArrowFunctionWithHints(a, []Type{EventType()})
	case *ast.FunctionExpression:
		val, err = e.emitFunctionExpression(a, []Type{EventType()})
	default:
		val, err = e.emitExpr(arg)
	}
	if err != nil {
		return "", err
	}
	if !val.Ty.IsFunc {
		return "", fmt.Errorf("%d:%d: an event listener must be a function", pos.Line, pos.Col)
	}
	if len(val.Ty.FuncParams) != 1 || val.Ty.FuncParams[0].IR != "ptr" {
		return "", fmt.Errorf("%d:%d: an event listener must take exactly 1 argument (the event)", pos.Line, pos.Col)
	}
	return val.Ref, nil
}

func (e *Emitter) emitNewEventTargetExpression() (Value, error) {
	e.ensureMapStrHelpers()
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))
	return Value{Ref: mapPtr, Ty: EventTargetType()}, nil
}

// resolveEventTargetMap yields the listener-registry map pointer for an object
// that behaves as an EventTarget: a plain EventTarget *is* the map, while an
// AbortSignal keeps it in its hidden `listeners` field (TDD-00081 Stage 3).
func (e *Emitter) resolveEventTargetMap(objExpr ast.Expression) (string, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return "", err
	}
	if objVal.Ty.IsAbortSignal {
		idx, _, _ := objVal.Ty.FieldIndex("listeners")
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
		mapPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, gep))
		return mapPtr, nil
	}
	return objVal.Ref, nil
}

// eventTargetOnceFlag inspects an addEventListener options argument for
// `{ once: true }`, returning "1" if present, else "0". Other options
// (capture/passive/signal) are accepted but ignored in V1.
func (e *Emitter) eventTargetOnceFlag(optsExpr ast.Expression) string {
	if obj, ok := optsExpr.(*ast.ObjectLiteral); ok {
		for _, prop := range obj.Properties {
			if prop.Key == "once" {
				if b, ok := prop.Value.(*ast.BooleanLiteral); ok && b.Value {
					return "1"
				}
			}
		}
	}
	return "0"
}

func (e *Emitter) emitEventTargetAddListener(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 {
		return Value{}, fmt.Errorf("%d:%d: addEventListener() requires 2 arguments (type, listener)", pos.Line, pos.Col)
	}
	mapPtr, err := e.resolveEventTargetMap(objExpr)
	if err != nil {
		return Value{}, err
	}
	typeVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	typeVal = e.coerce(typeVal, TypePtr)
	listenerPtr, err := e.resolveEventTargetListenerArg(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	once := "0"
	if len(args) >= 3 {
		once = e.eventTargetOnceFlag(args[2])
	}
	list := e.emitEventEmitterGetOrCreateList(mapPtr, typeVal.Ref)
	e.ensureEventEmitterRuntime()
	e.emitInstr(fmt.Sprintf("call void @__kml_ee_list_push(ptr %s, ptr %s, i64 %s)", list, listenerPtr, once))
	return Value{Ty: TypeVoid}, nil
}

func (e *Emitter) emitEventTargetRemoveListener(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 2 {
		return Value{}, fmt.Errorf("%d:%d: removeEventListener() requires 2 arguments (type, listener)", pos.Line, pos.Col)
	}
	mapPtr, err := e.resolveEventTargetMap(objExpr)
	if err != nil {
		return Value{}, err
	}
	typeVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	typeVal = e.coerce(typeVal, TypePtr)
	listenerPtr, err := e.resolveEventTargetListenerArg(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureEventEmitterRuntime()
	list := e.eventEmitterListPtr(mapPtr, typeVal.Ref)
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, list))
	skipL := e.freshLabel("et.rm.skip")
	doL := e.freshLabel("et.rm.do")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, skipL, doL))
	e.emitLabel(doL)
	e.emitInstr(fmt.Sprintf("call void @__kml_ee_list_remove(ptr %s, ptr %s)", list, listenerPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))
	e.emitLabel(skipL)
	return Value{Ty: TypeVoid}, nil
}

// emitEventTargetDispatch implements `dispatchEvent(event)`: read the type off
// the Event object, snapshot the listener list for that type, and invoke each
// listener with the event object — honoring `once` (removed after) and
// `stopImmediatePropagation` (halts the remaining listeners). Returns
// `!event.defaultPrevented`.
func (e *Emitter) emitEventTargetDispatch(objExpr ast.Expression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: dispatchEvent() requires 1 argument (the event)", pos.Line, pos.Col)
	}
	mapPtr, err := e.resolveEventTargetMap(objExpr)
	if err != nil {
		return Value{}, err
	}
	eventVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !eventVal.Ty.IsEvent {
		return Value{}, fmt.Errorf("%d:%d: dispatchEvent() requires an Event object", pos.Line, pos.Col)
	}
	return e.emitDispatchToMap(mapPtr, eventVal)
}

// emitDispatchToMap runs the WHATWG dispatch loop over a listener-registry map
// for a given event object (shared by EventTarget.dispatchEvent and
// AbortController.abort). Returns !event.defaultPrevented.
func (e *Emitter) emitDispatchToMap(mapPtr string, eventVal Value) (Value, error) {
	eventTy := eventVal.Ty

	e.ensureEventEmitterRuntime()
	e.ensureMemcpy()
	e.ensureMalloc()

	// event.type string, for the list lookup.
	typeIdx, _, _ := eventTy.FieldIndex("type")
	typeGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", typeGep, eventTy.StructIR(), eventVal.Ref, typeIdx))
	typeRef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", typeRef, typeGep))

	stopIdx, _, _ := eventTy.FieldIndex("stopImmediate")
	stopGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", stopGep, eventTy.StructIR(), eventVal.Ref, stopIdx))

	list := e.eventEmitterListPtr(mapPtr, typeRef)

	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, list))
	hasL := e.freshLabel("et.disp.has")
	afterL := e.freshLabel("et.disp.after")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, afterL, hasL))

	e.emitLabel(hasL)
	length := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", length, list))
	dataGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 16", dataGep, list))
	origData := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", origData, dataGep))
	snapBytes := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 16", snapBytes, length))
	snap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", snap, snapBytes))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", snap, origData, snapBytes))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("et.disp.cond")
	bodyL := e.freshLabel("et.disp.body")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	atEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", atEnd, idxVal, length))
	// stopImmediatePropagation: halt if the event's stop flag is set.
	stopped := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", stopped, stopGep))
	endOrStop := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", endOrStop, atEnd, stopped))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", endOrStop, afterL, bodyL))

	e.emitLabel(bodyL)
	lp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i64 %s, i32 0", lp, snap, idxVal))
	listenerPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", listenerPtr, lp))
	op := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i64 %s, i32 1", op, snap, idxVal))
	onceFlag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", onceFlag, op))

	cb := Callback{kind: cbClosure, hdrPtr: listenerPtr, ty: FuncType([]Type{eventTy}, TypeVoid)}
	if _, err := e.emitCBCall(cb, []Value{eventVal}); err != nil {
		return Value{}, err
	}

	isOnce := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isOnce, onceFlag))
	onceL := e.freshLabel("et.disp.once")
	nextL := e.freshLabel("et.disp.next")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOnce, onceL, nextL))
	e.emitLabel(onceL)
	e.emitInstr(fmt.Sprintf("call void @__kml_ee_list_remove(ptr %s, ptr %s)", list, listenerPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
	e.emitLabel(nextL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(afterL)
	// Return !event.defaultPrevented.
	dpIdx, _, _ := eventTy.FieldIndex("defaultPrevented")
	dpGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", dpGep, eventTy.StructIR(), eventVal.Ref, dpIdx))
	dp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", dp, dpGep))
	notDp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", notDp, dp))
	return Value{Ref: notDp, Ty: TypeBool}, nil
}

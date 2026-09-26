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
	"on": true, "once": true, "emit": true, "off": true, "addListener": true,
	"prependListener": true, "prependOnceListener": true,
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
	val, err := e.emitNewEventEmitterValue(init)
	if err != nil {
		return err
	}
	e.storePtrHandleVarDecl(v, val)
	return nil
}

// emitNewEventEmitterValue is emitMap/SetVarDecl's EventEmitter<T> sibling
// (TDD-00028) — builds `new EventEmitter<T>()` as a plain ptr Value, usable
// as a general expression, not just a var-decl initializer.
func (e *Emitter) emitNewEventEmitterValue(init *ast.NewEventEmitterExpression) (Value, error) {
	payload := TypeAny // @types/node's DefaultEventMap
	if init.PayloadType != nil {
		payload = e.resolveEventEmitterPayloadType(init.PayloadType)
	}
	e.ensureMapStrHelpers()
	handlePtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", handlePtr))
	return Value{Ref: handlePtr, Ty: EventEmitterType(payload)}, nil
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

// resolveEventPayload resolves one on/once/emit/off call's event payload
// against the emitter's event map, @types/node's `EventEmitter<T extends
// EventMap<T>>`: each property of the map is an event whose value is its
// argument tuple (`{ data: [chunk: string]; end: [] }`), and the event-name
// argument must be a string literal naming one. The result is the tuple, or
// isVoid for an event that takes no arguments. A dynamic emitter (no type
// argument) is handled by the callers before this.
func (e *Emitter) resolveEventPayload(payloadTy Type, eventArg ast.Expression, pos ast.Pos) (Type, bool, error) {
	if eeDynamic(payloadTy) {
		return TypeAny, false, nil
	}
	if !payloadTy.IsObject || payloadTy.IsError || payloadTy.IsClass || payloadTy.IsTuple {
		return Type{}, false, fmt.Errorf("%d:%d: EventEmitter's type argument must be an event map of argument tuples (`{ data: [chunk: string] }`)", pos.Line, pos.Col)
	}
	lit, ok := eventArg.(*ast.StringLiteral)
	if !ok {
		return Type{}, false, fmt.Errorf("%d:%d: an event-map EventEmitter requires a string-literal event name", pos.Line, pos.Col)
	}
	_, fieldTy, found := payloadTy.FieldIndex(lit.Value)
	if !found {
		return Type{}, false, fmt.Errorf("%d:%d: event '%s' is not declared in this EventEmitter's event map", pos.Line, pos.Col, lit.Value)
	}
	if !fieldTy.IsTuple {
		return Type{}, false, fmt.Errorf("%d:%d: event '%s' in this EventEmitter's event map must be an argument tuple (`%s: [value: T]`)", pos.Line, pos.Col, lit.Value, lit.Value)
	}
	if len(fieldTy.Fields) == 0 {
		return fieldTy, true, nil
	}
	return fieldTy, false, nil
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
// tupleElemTypes returns a tuple type's element types in order.
func tupleElemTypes(t Type) []Type {
	elems := make([]Type, len(t.Fields))
	for i, f := range t.Fields {
		elems[i] = f.Ty
	}
	return elems
}

// listenerTypeName maps an event payload Type to a source-level type name a
// contextually-typed parameter annotation can carry, or "" when the payload
// has no plain name (an object/tuple type). Used to see through `test`
// counting wrappers: `p.on('data', mustCall((d) => …))` types the inner
// arrow's `d` the way real Node infers it — the same ADR-00412 treatment
// createServer handlers get.
func listenerTypeName(ty Type) string {
	switch {
	case isStringTy(ty):
		return "string"
	case ty.IsError:
		return "Error"
	case ty.IR == "i64" || ty.IR == "double":
		return "number"
	case ty.IR == "i1":
		return "boolean"
	}
	return ""
}

func (e *Emitter) resolveEventEmitterListenerArg(arg ast.Expression, payloadTy Type, isVoid bool, fnName string, pos ast.Pos) (string, error) {
	// A tuple payload `[A, B, …]` (TDD-00131) is Node's multi-argument event:
	// the listener takes one parameter per tuple element, hinted per-position.
	hints := []Type{payloadTy}
	if isVoid {
		hints = nil
	} else if payloadTy.IsTuple {
		hints = tupleElemTypes(payloadTy)
	}
	// A listener inside a `test` counting wrapper can't receive the hints
	// below (the wrapper call is emitted as an opaque expression), so
	// contextually annotate the inner arrow's bare parameters first.
	if unwrapTestWrapper(arg) != arg && !isVoid {
		if payloadTy.IsTuple {
			names := make([]string, 0, len(hints))
			ok := true
			for _, h := range hints {
				n := listenerTypeName(h)
				if n == "" {
					ok = false
					break
				}
				names = append(names, n)
			}
			if ok {
				contextTypeArrowParams(arg, names...)
			}
		} else if n := listenerTypeName(payloadTy); n != "" {
			contextTypeArrowParams(arg, n)
		}
	}
	var val Value
	var err error
	if af, ok := arg.(*ast.ArrowFunction); ok {
		val, err = e.emitArrowFunctionWithHints(af, hints)
	} else if fe, ok := arg.(*ast.FunctionExpression); ok {
		val, err = e.emitFunctionExpression(fe, hints)
	} else {
		val, err = e.emitExpr(arg)
	}
	if err != nil {
		return "", err
	}
	if !val.Ty.IsFunc {
		return "", fmt.Errorf("%d:%d: %s's listener must be a function", pos.Line, pos.Col, fnName)
	}
	if isVoid {
		if len(val.Ty.FuncParams) != 0 {
			return "", fmt.Errorf("%d:%d: %s's listener for a payload-less event must take no arguments", pos.Line, pos.Col, fnName)
		}
	} else if payloadTy.IsTuple {
		elems := tupleElemTypes(payloadTy)
		if len(val.Ty.FuncParams) > len(elems) {
			return "", fmt.Errorf("%d:%d: %s's listener takes more than the event's %d arguments", pos.Line, pos.Col, fnName, len(elems))
		}
		for i, p := range val.Ty.FuncParams {
			if p.IR != elems[i].IR {
				return "", fmt.Errorf("%d:%d: %s's listener argument %d does not match the event's tuple-payload element type", pos.Line, pos.Col, fnName, i+1)
			}
		}
	} else if len(val.Ty.FuncParams) != 1 || val.Ty.FuncParams[0].IR != payloadTy.IR {
		return "", fmt.Errorf("%d:%d: %s's listener must take exactly 1 argument matching this event's payload type", pos.Line, pos.Col, fnName)
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
	dynamic := eeDynamic(payloadTy)
	// eventArgs resolves a mapped event's argument types (nil when it takes
	// none); a dynamic emitter's events take any arguments.
	eventArgs := func(ev ast.Expression) ([]Type, error) {
		if dynamic {
			return nil, nil
		}
		evTy, evVoid, err := e.resolveEventPayload(payloadTy, ev, pos)
		if err != nil || evVoid {
			return nil, err
		}
		return tupleElemTypes(evTy), nil
	}
	switch method {
	case "on", "once", "off", "removeListener", "addListener", "prependListener", "prependOnceListener":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: %s() requires 2 arguments (event, listener)", pos.Line, pos.Col, method)
		}
		argTys, err := eventArgs(args[0])
		if err != nil {
			return Value{}, err
		}
		eventVal, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		eventVal = e.coerce(eventVal, TypePtr)
		rec, err := e.resolveDynListener(args[1], argTys, dynamic, method, pos)
		if err != nil {
			return Value{}, err
		}
		e.ensureEventEmitterRuntime()
		if method != "off" && method != "removeListener" {
			listPtr := e.emitEventEmitterGetOrCreateList(listenersMapPtr, eventVal.Ref)
			once := "0"
			if method == "once" || method == "prependOnceListener" {
				once = "1"
			}
			fn := "__kml_ee_list_push"
			if method == "prependListener" || method == "prependOnceListener" {
				fn = "__kml_ee_list_prepend"
			}
			e.emitInstr(fmt.Sprintf("call void @%s(ptr %s, ptr %s, i64 %s)", fn, listPtr, rec, once))
		} else {
			listPtr := e.eventEmitterListPtr(listenersMapPtr, eventVal.Ref)
			e.emitInstr(fmt.Sprintf("call void @__kml_ee_list_remove_dyn(ptr %s, ptr %s)", listPtr, rec))
			e.eePrune(listenersMapPtr, eventVal.Ref, listPtr)
		}
		return chainVal, nil
	case "emit":
		if len(args) < 1 {
			return Value{}, fmt.Errorf("%d:%d: emit() takes (event, ...args)", pos.Line, pos.Col)
		}
		argTys, err := eventArgs(args[0])
		if err != nil {
			return Value{}, err
		}
		if !dynamic && len(args)-1 != len(argTys) {
			return Value{}, fmt.Errorf("%d:%d: emit() for this event takes %d data arguments", pos.Line, pos.Col, len(argTys))
		}
		return e.emitEventEmitterEmit(listenersMapPtr, args, argTys, pos, chainVal)
	}
	switch method {
	case "removeAllListeners":
		if len(args) > 1 {
			return Value{}, fmt.Errorf("%d:%d: removeAllListeners() takes at most 1 argument (event?)", pos.Line, pos.Col)
		}
		if len(args) == 0 {
			// No-arg form: literally Map.clear() — reset size AND the hash
			// index, don't free (same "leak in manual mode" convention
			// clear() already uses).
			e.ensureMapClear()
			e.emitInstr(fmt.Sprintf("call void @__kml_map_clear(ptr %s)", listenersMapPtr))
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
		e.eePrune(listenersMapPtr, eventVal.Ref, listPtr)
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

	}
	return Value{}, fmt.Errorf("%d:%d: unknown EventEmitter method '%s'", pos.Line, pos.Col, method)
}

// eeDynamic reports whether an emitter uses @types/node's default event map
// (`EventEmitter` with no type argument, or `EventEmitter<any>`): every
// event takes any arguments, and each listener is held as a dynamic
// function record called through the dynamic ABI.
func eeDynamic(payloadTy Type) bool { return payloadTy.IsDynamic }

// resolveDynListener evaluates a listener for a dynamic-mode emitter and
// returns its function record (untagged). An untyped parameter is `any`, as
// tsc types it against `(...args: any[]) => void`.
func (e *Emitter) resolveDynListener(arg ast.Expression, argTys []Type, dynamic bool, fnName string, pos ast.Pos) (string, error) {
	// A dynamic emitter's listener parameters are `any`, as tsc types them
	// against `(...args: any[]) => void`; a mapped event's are its tuple's.
	anyHints := func(params []ast.Param) []Type {
		if !dynamic {
			return argTys
		}
		hs := make([]Type, len(params))
		for i, p := range params {
			hs[i] = TypeAny
			if p.Rest {
				hs[i] = ArrayOf(TypeAny)
			}
		}
		return hs
	}
	var val Value
	var err error
	switch fn := arg.(type) {
	case *ast.ArrowFunction:
		val, err = e.emitArrowFunctionWithHints(fn, anyHints(fn.Params))
	case *ast.FunctionExpression:
		val, err = e.emitFunctionExpression(fn, anyHints(fn.Params))
	default:
		val, err = e.emitExpr(arg)
	}
	if err != nil {
		return "", err
	}
	if val.Ty.IsFunc && !dynamic {
		if len(val.Ty.FuncParams) > len(argTys) && !val.Ty.FuncHasRest {
			return "", fmt.Errorf("%d:%d: %s's listener takes more than the event's %d arguments", pos.Line, pos.Col, fnName, len(argTys))
		}
	}
	box := val
	if val.Ty.IsFunc {
		if box, err = e.emitDynClosureAdapter(val); err != nil {
			return "", fmt.Errorf("%d:%d: %s's listener: %v", pos.Line, pos.Col, fnName, err)
		}
	} else if !val.Ty.IsDynamic {
		return "", fmt.Errorf("%d:%d: %s's listener must be a function", pos.Line, pos.Col, fnName)
	}
	e.ensureNanBox()
	pay := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_nb_pay(i64 %s)", pay, box.Ref))
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", rec, pay))
	return rec, nil
}

// emitEventEmitterEmit is emit() for a dynamic-mode emitter: the data
// arguments are boxed once into an argv, and each listener is called
// through the dynamic ABI with the emitter as `this`. An unlistened
// 'error' event throws its argument when that is an Error, and otherwise
// an Error reading "Unhandled error. (<value>)", as Node does.
func (e *Emitter) emitEventEmitterEmit(listenersMapPtr string, args []ast.Expression, argTys []Type, pos ast.Pos, self Value) (Value, error) {
	eventVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	eventVal = e.coerce(eventVal, TypePtr)
	data := args[1:]
	vals := make([]Value, len(data))
	for i, a := range data {
		if i < len(argTys) {
			if vals[i], err = e.emitExprWithObjectHint(a, argTys[i]); err != nil {
				return Value{}, err
			}
			vals[i] = e.coerce(vals[i], argTys[i])
		} else if vals[i], err = e.emitExprWithObjectHint(a, TypeAny); err != nil {
			// A listener of a dynamic emitter takes `any` arguments: an
			// object literal is built as the dynamic object it reads as.
			return Value{}, err
		}
	}
	e.ensureMalloc()
	e.ensureNanBox()
	argv := "null"
	if len(vals) > 0 {
		argv = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", argv, 8*len(vals)))
		for i, v := range vals {
			b, err := e.emitBoxValue(v)
			if err != nil {
				return Value{}, err
			}
			slot := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %d", slot, argv, i))
			e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", b.Ref, slot))
		}
	}
	recv := fmt.Sprintf("%d", nbUndefined)
	if self.Ref != "" {
		if b, err := e.emitBoxValue(self); err == nil {
			recv = b.Ref
		}
	}

	e.ensureMapStrHelpers()
	e.ensureEventEmitterRuntime()
	e.ensureMemcpy()
	listPtr := e.eventEmitterListPtr(listenersMapPtr, eventVal.Ref)
	countAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countAlloca))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, listPtr))
	hasListL := e.freshLabel("ee.demit.haslist")
	afterListL := e.freshLabel("ee.demit.afterlist")
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
	condL := e.freshLabel("ee.demit.cond")
	bodyL := e.freshLabel("ee.demit.body")
	doneLoopL := e.freshLabel("ee.demit.doneloop")
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
	rec := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", rec, lp))
	op := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr {ptr, i64}, ptr %s, i64 %s, i32 1", op, snapData, idxVal))
	onceFlag := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", onceFlag, op))
	// A once listener is removed before it runs, as Node's once wrapper does.
	isOnce := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isOnce, onceFlag))
	isOnceL := e.freshLabel("ee.demit.isonce")
	callL := e.freshLabel("ee.demit.call")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOnce, isOnceL, callL))
	e.emitLabel(isOnceL)
	e.emitInstr(fmt.Sprintf("call void @__kml_ee_list_remove(ptr %s, ptr %s)", listPtr, rec))
	e.eePrune(listenersMapPtr, eventVal.Ref, listPtr)
	e.emitTerminator(fmt.Sprintf("br label %%%s", callL))
	e.emitLabel(callL)
	fp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fp, rec))
	envSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", envSlot, rec))
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", env, envSlot))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 %s(ptr %s, i64 %s, i64 %d, ptr %s)", r, fp, env, recv, len(vals), argv))
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
	e.ensureStrcmp()
	cmpResult := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", cmpResult, eventVal.Ref, e.internString("error")))
	isErrorEvent := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", isErrorEvent, cmpResult))
	shouldThrow := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", shouldThrow, noneCalled, isErrorEvent))
	throwL := e.freshLabel("ee.demit.throw")
	retL := e.freshLabel("ee.demit.ret")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", shouldThrow, throwL, retL))

	e.emitLabel(throwL)
	e.ensureExceptionHelpers()
	switch {
	case len(vals) > 0 && vals[0].Ty.IsError:
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", e.coerce(vals[0], TypePtr).Ref))
	case len(vals) == 0:
		errPtr := e.buildErrorObj(0, e.internString("Unhandled error. (undefined)"), e.internString("Error"))
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errPtr))
	default:
		shown := vals[0]
		var inner Value
		if isStringTy(shown.Ty) {
			// Node renders the value with util.inspect: a string is quoted.
			inner, _ = e.emitStringConcat(Value{Ref: e.internString("'"), Ty: TypePtr}, e.coerce(shown, TypePtr))
			inner, _ = e.emitStringConcat(inner, Value{Ref: e.internString("'"), Ty: TypePtr})
		} else {
			str, err := e.emitValueToString(shown)
			if err != nil {
				return Value{}, err
			}
			inner = str
		}
		s, _ := e.emitStringConcat(Value{Ref: e.internString("Unhandled error. ("), Ty: TypePtr}, inner)
		s, _ = e.emitStringConcat(s, Value{Ref: e.internString(")"), Ty: TypePtr})
		errPtr := e.buildErrorObj(0, s.Ref, e.internString("Error"))
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errPtr))
	}
	e.emitTerminator("unreachable")

	e.emitLabel(retL)
	anyCalled := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = xor i1 %s, true", anyCalled, noneCalled))
	return Value{Ref: anyCalled, Ty: TypeBool}, nil
}

// eePrune drops an event's entry once its listener list is empty, so
// eventNames() no longer reports it and a later listener re-adds the name at
// the end, as Node deletes the key with its last listener.
func (e *Emitter) eePrune(listenersMapPtr, eventRef, listPtr string) {
	e.ensureMapStrHelpers()
	e.ensureEventEmitterRuntime()
	e.emitInstr(fmt.Sprintf("call void @__kml_ee_prune(ptr %s, ptr %s, ptr %s)", listenersMapPtr, eventRef, listPtr))
}

// emit_diagch.go — the diagnostics_channel module surface (V1): dc.channel /
// dc.subscribe / dc.unsubscribe / dc.hasSubscribers, and the Channel handle's
// publish/subscribe/unsubscribe methods + hasSubscribers/name reads. Messages
// are strings in V1 (a typed subset can't carry an arbitrary object across
// the publish→subscribe boundary statically); tracingChannel and the store
// bindings are clean rejections. Backed by runtime_diagch.go.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emitDiagChModuleCall dispatches diagnostics_channel.<member>(…).
func (e *Emitter) emitDiagChModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureDiagChRuntime()
	switch method {
	case "channel":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: diagnostics_channel.channel takes one name", pos.Line, pos.Col)
		}
		nv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		ch := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dc_channel(ptr %s)", ch, e.coerce(nv, TypePtr).Ref))
		return Value{Ref: ch, Ty: DiagChannelType()}, nil
	case "subscribe", "unsubscribe":
		if len(args) != 2 {
			return Value{}, fmt.Errorf("%d:%d: diagnostics_channel.%s takes (name, subscriber)", pos.Line, pos.Col, method)
		}
		nv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		ch := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dc_channel(ptr %s)", ch, e.coerce(nv, TypePtr).Ref))
		return e.emitDiagChSubUnsub(Value{Ref: ch, Ty: DiagChannelType()}, method, args[1], pos)
	case "hasSubscribers":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: diagnostics_channel.hasSubscribers takes one name", pos.Line, pos.Col)
		}
		nv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		ch := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_dc_channel(ptr %s)", ch, e.coerce(nv, TypePtr).Ref))
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dc_has_subscribers(ptr %s)", b, ch))
		return Value{Ref: b, Ty: TypeBool}, nil
	case "tracingChannel":
		return Value{}, fmt.Errorf("%d:%d: diagnostics_channel.tracingChannel is not implemented — the plain channel/subscribe/publish surface is", pos.Line, pos.Col)
	}
	return Value{}, fmt.Errorf("%d:%d: diagnostics_channel has no method '%s' (supported: channel, subscribe, unsubscribe, hasSubscribers)", pos.Line, pos.Col, method)
}

// emitDiagChSubUnsub resolves a subscriber callback (0–2 params: message,
// name — both strings, contextually typed) and registers/removes it.
func (e *Emitter) emitDiagChSubUnsub(chVal Value, method string, cbExpr ast.Expression, pos ast.Pos) (Value, error) {
	contextTypeArrowParams(cbExpr, "string", "string")
	cb, err := e.resolveCallbackWithHints(cbExpr, []Type{TypePtr, TypePtr})
	if err != nil {
		return Value{}, err
	}
	if cb.kind != cbClosure {
		return Value{}, fmt.Errorf("%d:%d: a diagnostics_channel subscriber must be an arrow/function-expression literal (or a mustCall-wrapped one)", pos.Line, pos.Col)
	}
	if method == "unsubscribe" {
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dc_unsubscribe(ptr %s, ptr %s)", b, chVal.Ref, cb.hdrPtr))
		return Value{Ref: b, Ty: TypeBool}, nil
	}
	arity := len(cb.paramTypes())
	if arity > 2 {
		return Value{}, fmt.Errorf("%d:%d: a subscriber takes at most (message, name)", pos.Line, pos.Col)
	}
	// Publish passes string pointers; a subscriber whose params defaulted to
	// number (e.g. an untyped arrow wrapped in mustCall and bound to a const
	// BEFORE subscribing — the wrapper's signature is fixed at wrap time)
	// would silently reinterpret them. Reject, naming the fix.
	for _, pt := range cb.paramTypes() {
		if pt.IR != "ptr" {
			return Value{}, fmt.Errorf("%d:%d: a subscriber's parameters must be strings — annotate them ((message: string, name: string) => …), especially when wrapping in mustCall before subscribing", pos.Line, pos.Col)
		}
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_dc_subscribe(ptr %s, ptr %s, i64 %d)", chVal.Ref, cb.hdrPtr, arity))
	return Value{Ty: TypeVoid}, nil
}

// emitDiagChannelMethod dispatches publish/subscribe/unsubscribe on a
// Channel handle.
func (e *Emitter) emitDiagChannelMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "publish":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: channel.publish takes one message", pos.Line, pos.Col)
		}
		mv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if !isPlainStringType(mv.Ty) {
			return Value{}, fmt.Errorf("%d:%d: channel.publish carries string messages in V1 (a typed subset can't hand an arbitrary object shape to unknown subscribers) — serialize with JSON.stringify", pos.Line, pos.Col)
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_dc_publish(ptr %s, ptr %s)", objVal.Ref, mv.Ref))
		return Value{Ty: TypeVoid}, nil
	case "subscribe", "unsubscribe":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: channel.%s takes one subscriber", pos.Line, pos.Col, method)
		}
		return e.emitDiagChSubUnsub(objVal, method, args[0], pos)
	case "bindStore", "unbindStore", "runStores":
		return Value{}, fmt.Errorf("%d:%d: channel.%s is not implemented (the store bindings ride AsyncLocalStorage, which does not exist)", pos.Line, pos.Col, method)
	}
	return Value{}, fmt.Errorf("%d:%d: a diagnostics Channel supports .publish(message), .subscribe(fn), .unsubscribe(fn) (got '%s')", pos.Line, pos.Col, method)
}

// emitDiagChannelMember reads hasSubscribers / name off a Channel handle.
func (e *Emitter) emitDiagChannelMember(objVal Value, prop string, pos ast.Pos) (Value, error) {
	switch prop {
	case "hasSubscribers":
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_dc_has_subscribers(ptr %s)", b, objVal.Ref))
		return Value{Ref: b, Ty: TypeBool}, nil
	case "name":
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 32", g, objVal.Ref))
		n := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", n, g))
		return Value{Ref: n, Ty: TypePtr}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: a diagnostics Channel has no property '%s' (hasSubscribers, name)", pos.Line, pos.Col, prop)
}
